package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// Draft consent and navigation share the study document: one atomic document
// replacement, no sidecar ordering or new migration/discovery namespace.
type studySetup struct {
	Stage       string `json:"stage"`
	ConsentText string `json:"consent_text"`
	Editorial   bool   `json:"editorial,omitempty"`
	Opening     bool   `json:"opening,omitempty"` // durable intent before any external issuer creation
}

var setupStages = []string{"basics", "collection", "information", "recruitment", "review", "share"}

func stageIndex(stage string) int {
	for i, s := range setupStages {
		if s == stage {
			return i
		}
	}
	return -1
}
func setupRevision(st Study) string {
	raw, _ := json.Marshal(st)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func setupDraft(st Study) studySetup {
	if st.Setup != nil {
		return *st.Setup
	}
	cur, _ := st.Current()
	return studySetup{Stage: "basics", ConsentText: cur.Text, Editorial: cur.Editorial}
}
func (s *studyStore) loadSetup(slug string) (Study, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(slug)
	if err != nil {
		return Study{}, os.ErrNotExist
	}
	raw, err := s.read(path)
	if err != nil {
		return Study{}, err
	}
	var st Study
	if json.Unmarshal(raw, &st) != nil || st.Slug != slug {
		return Study{}, errors.New("setup_unavailable")
	}
	return st, nil
}

// Caller holds operation across read/check/write and, when opening, the external call.
func (s *studyStore) putSetup(st Study) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(st.Slug)
	if err != nil {
		return err
	}
	st.Updated = time.Now().UTC()
	return s.writeList(path, st)
}
func (a *app) setupPage(w http.ResponseWriter, r *http.Request)   { a.setup(w, r, false) }
func (a *app) setupAction(w http.ResponseWriter, r *http.Request) { a.setup(w, r, true) }
func (a *app) setup(w http.ResponseWriter, r *http.Request, mutate bool) {
	sess, ok := a.researcher(w, r)
	if !ok {
		return
	}
	stage := r.PathValue("stage")
	if stageIndex(stage) < 0 || !validSlug.MatchString(r.PathValue("slug")) {
		a.fail(w, r, sess, 404, "no_study", nil)
		return
	}
	if mutate && !sess.checkCSRF(r) {
		a.fail(w, r, sess, 403, "expired", nil)
		return
	}
	a.studies.operation.Lock()
	defer a.studies.operation.Unlock()
	st, err := a.studies.loadSetup(r.PathValue("slug"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			a.fail(w, r, sess, 404, "no_study", nil)
		} else {
			a.fail(w, r, sess, 503, "busy", nil)
		}
		return
	}
	if st.Status != "draft" {
		if !mutate && stage == "share" && st.Status == "open" {
			a.renderSetup(w, r, sess, st, stage, 200, "", "")
			return
		}
		http.Redirect(w, r, "/studies/"+st.Slug+"/edit", 303)
		return
	}
	if !mutate {
		a.renderSetup(w, r, sess, st, stage, 200, "", "")
		return
	}
	revision := r.PostFormValue("revision")
	action := r.PostFormValue("action")
	if action != "save" && action != "next" && action != "back" && action != "publish" && action != "open" {
		a.renderSetup(w, r, sess, st, stage, 400, "setup_action", revision)
		return
	}
	// Keep the submitted token on errors. Only an explicit reload obtains a new
	// token; resubmission must never turn a conflict into an overwrite.
	submitted := st
	draft := setupDraft(st)
	submitted.Setup = &draft
	if stage == "basics" {
		submitted.Title = r.PostFormValue("title")
		submitted.RunBy = r.PostFormValue("run_by")
	}
	if stage == "information" {
		submitted.Summary = r.PostFormValue("summary")
		submitted.Collected = r.PostFormValue("collected")
		submitted.WhoSees = r.PostFormValue("who_sees")
		submitted.HowToLeave = r.PostFormValue("how_to_leave")
		draft.ConsentText = r.PostFormValue("consent")
		draft.Editorial = r.PostFormValue("material") != "yes"
	}
	if stage == "recruitment" {
		submitted.JoinMode = r.PostFormValue("join_mode")
	}
	if stage == "collection" {
		submitted.Tracks = parseDesign(r.PostForm, st.ID())
	}
	if revision == "" || revision != setupRevision(st) {
		a.renderSetup(w, r, sess, submitted, stage, 409, "setup_stale", revision)
		return
	}
	if (action == "publish" || action == "open") && stage != "review" {
		a.renderSetup(w, r, sess, submitted, stage, 400, "setup_action", revision)
		return
	}
	if draft.Opening && action != "open" {
		a.renderSetup(w, r, sess, submitted, stage, 409, "setup_recovery", revision)
		return
	}
	if st.IssuerID != "" && stage == "collection" {
		a.renderSetup(w, r, sess, submitted, stage, 409, "study_tracks_locked", revision)
		return
	}
	if action == "publish" {
		if clean(draft.ConsentText) == "" {
			a.renderSetup(w, r, sess, st, stage, 422, "form_content_required", revision)
			return
		}
		cur, found := st.Current()
		if !found || cur.Text != draft.ConsentText {
			st.Consent = append(st.Consent, ConsentVersion{Version: len(st.Consent) + 1, Text: draft.ConsentText, Editorial: found && draft.Editorial, Published: time.Now().UTC()})
		} else {
			http.Redirect(w, r, r.URL.Path, 303)
			return
		}
		// History and the unpublished draft are saved atomically.
		st.Setup = &draft
		draft.Stage = "review"
	} else if action == "open" {
		cur, found := st.Current()
		if !found || cur.Text != draft.ConsentText {
			a.renderSetup(w, r, sess, st, stage, 422, "setup_publish_needed", revision)
			return
		}
		if err = st.readyToOpen(); err != nil {
			a.renderSetup(w, r, sess, st, stage, 422, formErrorKey(err.Error()), revision)
			return
		}
		if st.IssuerID == "" {
			err = a.openStudy(r, sess, st)
		} else {
			// A prior attempt may have saved the issuer but failed final opening.
			token, e := sess.Owner.token(r.Context(), a.tessera)
			err = e
			if err == nil {
				who, e := a.tessera.account(r.Context(), token)
				err = e
				if err == nil && (!who.ServerAdministrator || !who.Installation.Administer) {
					err = errors.New("setup_recovery")
				}
			}
			if err == nil {
				_, err = a.studies.issuerSecret(st.Slug)
			}
		}
		if err != nil {
			current, e := a.studies.loadSetup(st.Slug)
			if e == nil {
				st = current
			}
			a.renderSetup(w, r, sess, st, stage, 503, "setup_recovery", setupRevision(st))
			return
		}
		st, err = a.studies.loadSetup(st.Slug)
		if err != nil {
			a.fail(w, r, sess, 503, "busy", nil)
			return
		}
		st.Status = "open"
		st.ClosedAt = nil
		draft = setupDraft(st)
		draft.Stage = "share"
		draft.Opening = false
		st.Setup = &draft
	} else {
		st = submitted
		// Validate only owned fields. Incomplete tracks deliberately remain savable.
		var owned []*string
		switch stage {
		case "basics":
			owned = []*string{&st.Title, &st.RunBy}
		case "information":
			owned = []*string{&st.Summary, &st.Collected, &st.WhoSees, &st.HowToLeave, &draft.ConsentText}
		}
		for _, f := range owned {
			*f = clean(*f)
		}
		problem := ""
		if st.Title == "" {
			problem = "form_title_required"
		}
		if utf8.RuneCountInString(st.RunBy) > 120 {
			problem = "form_run_by_long"
		}
		for _, f := range []string{st.Title, st.Summary, st.Collected, st.WhoSees, st.HowToLeave, draft.ConsentText} {
			if len(f) > maxStudyText {
				problem = "form_text_long"
			}
		}
		if stage == "recruitment" && st.JoinMode != "open" && st.JoinMode != "invite" {
			problem = "setup_action"
		}
		if problem != "" {
			a.renderSetup(w, r, sess, submitted, stage, 422, problem, revision)
			return
		}
		i := stageIndex(stage)
		if action == "next" && i < 4 {
			i++
		}
		if action == "back" && i > 0 {
			i--
		}
		draft.Stage = setupStages[i]
		st.Setup = &draft
	}
	if err = a.studies.putSetup(st); err != nil {
		if action == "open" {
			current, readErr := a.studies.loadSetup(st.Slug)
			if readErr != nil {
				a.fail(w, r, sess, 503, "busy", nil)
				return
			}
			a.renderSetup(w, r, sess, current, stage, 503, "setup_recovery", setupRevision(current))
			return
		}
		a.renderSetup(w, r, sess, submitted, stage, 503, "setup_unavailable", revision)
		return
	}
	target := stage
	if action == "open" {
		target = "share"
	} else if action != "publish" {
		target = draft.Stage
	}
	http.Redirect(w, r, "/studies/"+st.Slug+"/setup/"+target, 303)
}

type setupRequirement struct{ Stage, Key string }

func setupRequirements(st Study) []setupRequirement {
	var out []setupRequirement
	if _, err := studyTypes(st.Tracks); err != nil {
		out = append(out, setupRequirement{"collection", formErrorKey(err.Error())})
	}
	if st.Summary == "" || st.Collected == "" || st.WhoSees == "" || st.HowToLeave == "" {
		out = append(out, setupRequirement{"information", "form_content_required"})
	}
	d := setupDraft(st)
	cur, ok := st.Current()
	if !ok || cur.Text != d.ConsentText || cur.Text == "" {
		stage := "review"
		if d.ConsentText == "" {
			stage = "information"
		}
		out = append(out, setupRequirement{stage, "setup_publish_needed"})
	}
	return out
}
func (a *app) renderSetup(w http.ResponseWriter, r *http.Request, sess *session, st Study, stage string, status int, problem, revision string) {
	if revision == "" && problem == "" {
		revision = setupRevision(st)
	}
	draft := setupDraft(st)
	cur, _ := st.Current()
	picked := map[string]bool{}
	for _, t := range st.Tracks {
		picked[t.Catalogue] = true
	}
	var link template.URL
	if st.Status == "open" && st.JoinMode == "open" {
		link = template.URL(studyAppLink(a.cfg.PublicURL, st.Title, st.Slug, "", a.tlsPin()))
	}
	a.render(w, r, sess, "study_setup", status, map[string]any{"Study": st, "Draft": draft, "Current": cur, "Stage": stage, "Stages": setupStages, "Step": stageIndex(stage) + 1, "Revision": revision, "Problem": problem, "Error": problem, "Missing": setupRequirements(st), "AppLink": link, "Rows": designRows(st.Tracks), "Kinds": Kinds, "Catalogue": CatalogueTracks, "Picked": picked, "Locked": st.IssuerID != "" || draft.Opening, "Saved": r.URL.Query().Get("saved"), "PublicURL": strings.TrimRight(a.cfg.PublicURL, "/")})
}

// The full legacy form remains usable for old drafts, but cannot publish or
// first-open one. Save its full draft payload atomically and send an opening
// request to the explicit, revision-bound Review actions.
// Caller holds the same operation lock as wizard and designer mutations.
func (a *app) editDraftStudy(w http.ResponseWriter, r *http.Request, sess *session, st Study, edit StudyEdit) {
	revision := r.PostFormValue("revision")
	renderError := func(status int, problem string) {
		submitted := st
		submitted.Title, submitted.RunBy, submitted.Summary, submitted.Collected, submitted.WhoSees, submitted.HowToLeave, submitted.JoinMode = edit.Title, edit.RunBy, edit.Summary, edit.Collected, edit.WhoSees, edit.HowToLeave, edit.JoinMode
		cur, _ := st.Current()
		cur.Text = edit.ConsentText
		a.render(w, r, sess, "study_edit", status, map[string]any{"Study": submitted, "Current": cur, "Revision": revision, "Editorial": edit.Editorial, "Error": problem})
	}
	if revision == "" || revision != setupRevision(st) {
		renderError(409, "setup_stale")
		return
	}
	if st.Setup != nil && st.Setup.Opening {
		renderError(409, "setup_recovery")
		return
	}
	draft := setupDraft(st)
	draft.ConsentText = edit.ConsentText
	draft.Editorial = edit.Editorial
	target := "/studies/" + st.Slug + "/edit"
	if r.PostFormValue("status") == "open" {
		draft.Stage = "review"
		target = "/studies/" + st.Slug + "/setup/review"
	}
	edit.Draft = &draft
	edit.ConsentText = ""
	if _, err := a.studies.saveInOperation(st.Slug, edit, false); err != nil {
		edit.ConsentText = draft.ConsentText
		status, problem := 422, err.Error()
		if formErrorKey(problem) == "form_save_error" {
			status, problem = 503, "setup_unavailable"
		}
		renderError(status, problem)
		return
	}
	http.Redirect(w, r, target, 303)
}
