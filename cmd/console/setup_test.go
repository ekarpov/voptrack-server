package main

import (
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func setupHTTPApp(t *testing.T) (*app, *session, *httptest.Server) {
	a, s, srv := policyHTTPApp(t)
	a.cfg.StateDir = t.TempDir()
	a.cfg.Analysis = "analysis"
	a.cfg.PublicURL = "https://synthetic.example"
	a.studies.dir = t.TempDir()
	for _, page := range []string{"study_setup", "study_tracks", "studies", "study_edit"} {
		tpl, err := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix, "formErrorKey": formErrorKey, "catalogueLabel": catalogueLabel, "designerProblem": designerProblem}).ParseFS(assets, "templates/layout.html", "templates/design_fields.html", "templates/"+page+".html")
		must(t, err)
		a.pages[page] = tpl
	}
	st := Study{Slug: "synthetic", Title: "Synthetic", Status: "draft", JoinMode: "invite", Summary: "Purpose", Collected: "Data", WhoSees: "Team", HowToLeave: "Erase", Tracks: []TrackDesign{{Catalogue: "com.voptrack.track.weight"}}}
	must(t, a.studies.putSetup(st))
	return a, s, srv
}
func setupPost(a *app, s *session, stage, action string, fields url.Values) *httptest.ResponseRecorder {
	if fields == nil {
		fields = url.Values{}
	}
	if !fields.Has("revision") {
		st, _ := a.studies.loadSetup("synthetic")
		fields.Set("revision", setupRevision(st))
	}
	fields.Set("csrf", s.CSRF)
	fields.Set("action", action)
	return savedRequest(a, s, "POST", "/studies/synthetic/setup/"+stage, fields)
}
func setupState(t *testing.T, a *app) Study {
	t.Helper()
	st, err := a.studies.loadSetup("synthetic")
	must(t, err)
	return st
}
func infoFields(text string) url.Values {
	return url.Values{"summary": {"Purpose"}, "collected": {"Data"}, "who_sees": {"Team"}, "how_to_leave": {"Erase"}, "consent": {text}, "material": {"yes"}}
}
func assertSetupStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d want %d: %s", w.Code, status, w.Body.String())
	}
}
func TestSetupStagesHistoryPreservationAndRestart(t *testing.T) {
	a, s, srv := setupHTTPApp(t)
	defer srv.Close()
	for _, action := range []string{"next", "back", "save"} {
		w := setupPost(a, s, "information", action, infoFields("Draft private sentinel"))
		assertSetupStatus(t, w, 303)
		st := setupState(t, a)
		if len(st.Consent) != 0 || st.Status != "draft" || st.Setup.ConsentText != "Draft private sentinel" {
			t.Fatal("navigation published consent or opened")
		}
	}
	before := setupState(t, a)
	w := setupPost(a, s, "basics", "next", url.Values{"title": {"New title"}, "run_by": {""}})
	assertSetupStatus(t, w, 303)
	st := setupState(t, a)
	if st.Summary != before.Summary || st.Collected != before.Collected || st.WhoSees != before.WhoSees || st.HowToLeave != before.HowToLeave || st.Setup.ConsentText != before.Setup.ConsentText || len(st.Tracks) != 1 || st.JoinMode != "invite" {
		t.Fatal("basics erased another stage")
	}
	w = setupPost(a, s, "recruitment", "back", url.Values{"join_mode": {"open"}})
	assertSetupStatus(t, w, 303)
	st = setupState(t, a)
	if st.Summary != "Purpose" || st.Setup.ConsentText != "Draft private sentinel" || st.Setup.Stage != "information" {
		t.Fatal("recruitment erased progress")
	}
	for _, stage := range setupStages {
		w = savedRequest(a, s, "GET", "/studies/synthetic/setup/"+stage, nil)
		assertSetupStatus(t, w, 200)
	}
	if len(setupState(t, a).Consent) != 0 {
		t.Fatal("GET published")
	}
	// Published consent exists in old drafts too. No migration or GET rewrites it.
	w = setupPost(a, s, "review", "publish", nil)
	assertSetupStatus(t, w, 303)
	st = setupState(t, a)
	first := st.Consent[0]
	if first.Editorial || first.Version != 1 {
		t.Fatal("first must be material")
	}
	w = setupPost(a, s, "review", "publish", nil)
	assertSetupStatus(t, w, 303)
	if len(setupState(t, a).Consent) != 1 {
		t.Fatal("publication retry duplicated")
	}
	w = setupPost(a, s, "information", "save", infoFields("Revised private sentinel"))
	assertSetupStatus(t, w, 303)
	w = setupPost(a, s, "review", "open", nil)
	assertSetupStatus(t, w, 422)
	st = setupState(t, a)
	if st.Consent[0] != first || st.IssuerID != "" {
		t.Fatal("history changed or opened older text")
	}
	// A new store instance sees progress and draft in the existing namespace.
	restart := &studyStore{docs: a.studies.docs}
	restored, err := restart.loadSetup("synthetic")
	must(t, err)
	if restored.Setup.ConsentText != "Revised private sentinel" || restored.Consent[0] != first {
		t.Fatal("restart lost draft/history")
	}
	w = savedRequest(a, nil, "GET", "/api/v1/studies/synthetic", nil)
	assertSetupStatus(t, w, 404)
	if strings.Contains(w.Body.String(), "sentinel") {
		t.Fatal("private draft leaked")
	}
	// A legacy draft with published history initializes from that text in memory only.
	st.Setup = nil
	must(t, a.studies.putSetup(st))
	old := setupRevision(setupState(t, a))
	w = savedRequest(a, s, "GET", "/studies/synthetic/setup/information", nil)
	assertSetupStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), first.Text) || setupRevision(setupState(t, a)) != old {
		t.Fatal("legacy history rewritten on resume")
	}
}
func TestSetupStaleLegacyDesignerAndRetainedInputs(t *testing.T) {
	a, s, srv := setupHTTPApp(t)
	defer srv.Close()
	old := setupRevision(setupState(t, a))
	_, err := a.studies.saveTracks("synthetic", []TrackDesign{{Name: "Incomplete", TypeID: "study.synthetic.incomplete"}})
	must(t, err)
	w := setupPost(a, s, "basics", "save", url.Values{"revision": {old}, "title": {"Retained title"}, "run_by": {"Retained organiser"}})
	assertSetupStatus(t, w, 409)
	if !strings.Contains(w.Body.String(), "Retained title") || !strings.Contains(w.Body.String(), `value="`+old+`"`) || !strings.Contains(w.Body.String(), "setup_stale") {
		t.Fatal("stale input or revision lost")
	}
	if setupState(t, a).Title == "Retained title" {
		t.Fatal("stale overwrite")
	}
	old = setupRevision(setupState(t, a))
	_, err = a.studies.save("synthetic", StudyEdit{Title: "Legacy", Summary: "Legacy summary", ConsentText: "Legacy published"}, false)
	must(t, err)
	w = setupPost(a, s, "information", "save", url.Values{"revision": {old}, "consent": {"Retained private text"}, "summary": {"Retained disclosure"}})
	assertSetupStatus(t, w, 409)
	if !strings.Contains(w.Body.String(), "Retained private text") || !strings.Contains(w.Body.String(), "Retained disclosure") {
		t.Fatal("lost conflict text")
	}
	w = setupPost(a, s, "basics", "save", url.Values{"title": {""}, "run_by": {"Team retained"}})
	assertSetupStatus(t, w, 422)
	if !strings.Contains(w.Body.String(), "Team retained") {
		t.Fatal("invalid input lost")
	}
	w = setupPost(a, s, "recruitment", "save", url.Values{"join_mode": {"forged"}})
	assertSetupStatus(t, w, 422)
	// Incomplete designs persist and readiness links back to Collection.
	w = setupPost(a, s, "collection", "next", url.Values{"t0.present": {"1"}, "t0.name": {"Incomplete"}})
	assertSetupStatus(t, w, 303)
	if len(setupState(t, a).Tracks) != 1 {
		t.Fatal("incomplete design lost")
	}
	w = savedRequest(a, s, "GET", "/studies/synthetic/setup/review", nil)
	if !strings.Contains(w.Body.String(), "/setup/collection") || !strings.Contains(w.Body.String(), "form_track_values") && !strings.Contains(w.Body.String(), messages["en"]["form_track_values"]) {
		t.Fatal("missing actionable track validation")
	}
}

type setupFaultDocs struct {
	docStore
	fail   atomic.Bool
	reads  atomic.Bool
	writes atomic.Int32
	failAt int32
}

func (d *setupFaultDocs) put(name string, raw []byte) error {
	n := d.writes.Add(1)
	if d.fail.Load() || n == d.failAt {
		return errors.New("private-storage-detail")
	}
	return d.docStore.put(name, raw)
}
func (d *setupFaultDocs) get(name string) ([]byte, bool, error) {
	if d.reads.Load() {
		return nil, false, errors.New("private-read-detail")
	}
	return d.docStore.get(name)
}
func TestSetupFailedWritesUnreadableMissingAndCreate(t *testing.T) {
	a, s, srv := setupHTTPApp(t)
	defer srv.Close()
	before := setupRevision(setupState(t, a))
	docs := &setupFaultDocs{docStore: a.studies.docs}
	a.studies.docs = docs
	docs.fail.Store(true)
	for _, stage := range []string{"basics", "information", "recruitment", "collection"} {
		fields := infoFields("Unpublished retained")
		fields.Set("title", "Retained save")
		fields.Set("join_mode", "open")
		fields.Set("t0.present", "1")
		fields.Set("t0.name", "Retained track")
		w := setupPost(a, s, stage, "save", fields)
		assertSetupStatus(t, w, 503)
		if strings.Contains(w.Body.String(), "private-storage-detail") || setupRevision(setupState(t, a)) != before {
			t.Fatal("failed write changed progress or leaked error")
		}
		token := "Retained save"
		if stage == "information" {
			token = "Unpublished retained"
		}
		if stage == "collection" {
			token = "Retained track"
		}
		if stage != "recruitment" && !strings.Contains(w.Body.String(), token) {
			t.Fatal("failed save lost input")
		}
	}
	docs.fail.Store(false)
	w := setupPost(a, s, "information", "save", infoFields("Publish me"))
	assertSetupStatus(t, w, 303)
	docs.fail.Store(true)
	w = setupPost(a, s, "review", "publish", nil)
	assertSetupStatus(t, w, 503)
	if len(setupState(t, a).Consent) != 0 {
		t.Fatal("failed publication appeared successful")
	}
	docs.reads.Store(true)
	w = savedRequest(a, s, "GET", "/studies/synthetic/setup/review", nil)
	assertSetupStatus(t, w, 503)
	w = savedRequest(a, s, "POST", "/studies", url.Values{"csrf": {s.CSRF}, "title": {"Retained creation"}, "slug": {"fresh-address"}})
	assertSetupStatus(t, w, 422)
	if !strings.Contains(w.Body.String(), "Retained creation") || !strings.Contains(w.Body.String(), "fresh-address") {
		t.Fatal("creation lost input")
	}
	docs.reads.Store(false)
	docs.fail.Store(false)
	w = savedRequest(a, s, "GET", "/studies/missing/setup/review", nil)
	assertSetupStatus(t, w, 404)
	for _, slug := range []string{"UPPERCASE", "bad/slug", "synthetic"} {
		w = savedRequest(a, s, "POST", "/studies", url.Values{"csrf": {s.CSRF}, "title": {"Duplicate retained"}, "slug": {slug}})
		assertSetupStatus(t, w, 422)
	}
}
func TestSetupAuthorizationAndMalformedRoutes(t *testing.T) {
	a, s, srv := setupHTTPApp(t)
	defer srv.Close()
	for _, stage := range setupStages {
		for _, method := range []string{"GET", "POST"} {
			w := savedRequest(a, nil, method, "/studies/synthetic/setup/"+stage, nil)
			assertSetupStatus(t, w, 303)
			participant := &session{ID: "participant", CSRF: "csrf", LastSeen: s.LastSeen, Connections: []*connection{{}}}
			a.sessions.byID[participant.ID] = participant
			w = savedRequest(a, participant, method, "/studies/synthetic/setup/"+stage, url.Values{"csrf": {"csrf"}})
			assertSetupStatus(t, w, 403)
		}
	}
	for _, stage := range setupStages {
		w := savedRequest(a, s, "POST", "/studies/synthetic/setup/"+stage, url.Values{"csrf": {"forged"}})
		assertSetupStatus(t, w, 403)
	}
	w := setupPost(a, s, "unknown", "save", nil)
	assertSetupStatus(t, w, 404)
	w = setupPost(a, s, "basics", "publish", nil)
	assertSetupStatus(t, w, 400)
	w = setupPost(a, s, "review", "forged", nil)
	assertSetupStatus(t, w, 400)
	a.cfg.Variant = "home"
	w = savedRequest(a, s, "GET", "/studies/synthetic/setup/review", nil)
	assertSetupStatus(t, w, 404)
	a.cfg.Variant = "research"
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"account": "owner", "server_administrator": false})
	})
	w = savedRequest(a, s, "GET", "/studies/synthetic/setup/review", nil)
	assertSetupStatus(t, w, 403)
}

// A deterministic fake implements only APIs in pinned Tessera. Fault switches
// model lost responses and local writes, not invented provisioning capabilities.
func TestSetupOpeningRecoveryAndConcurrentDefinitionEdits(t *testing.T) {
	for _, fault := range []string{"none", "lost-response", "secret-write", "issuer-write", "final-write", "collision"} {
		t.Run(fault, func(t *testing.T) {
			a, s, old := setupHTTPApp(t)
			defer old.Close()
			w := setupPost(a, s, "information", "save", infoFields("Explicit consent"))
			assertSetupStatus(t, w, 303)
			w = setupPost(a, s, "review", "publish", nil)
			assertSetupStatus(t, w, 303)
			var creates, rotates atomic.Int32
			var exists atomic.Bool
			if fault == "collision" {
				exists.Store(true)
			}
			entered := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/oauth/account":
					writeJSON(w, 200, map[string]any{"account": "owner", "server_administrator": true, "installation": map[string]any{"administer": true}})
				case "/admin/api/v1/issuers":
					if r.Method == "GET" {
						ids := []map[string]string{}
						if exists.Load() {
							ids = append(ids, map[string]string{"id": "study-synthetic"})
						}
						writeJSON(w, 200, map[string]any{"issuers": ids})
						return
					}
					creates.Add(1)
					once.Do(func() { close(entered) })
					<-release
					exists.Store(true)
					if fault == "lost-response" {
						w.WriteHeader(503)
						return
					}
					writeJSON(w, 201, map[string]string{"secret": "synthetic-secret"})
				case "/admin/api/v1/issuers/study-synthetic/secret":
					rotates.Add(1)
					writeJSON(w, 200, map[string]string{"secret": "rotated-synthetic"})
				default:
					w.WriteHeader(503)
				}
			}))
			defer srv.Close()
			a.tessera = &Tessera{internal: srv.URL, http: srv.Client()}
			if fault == "secret-write" {
				a.studies.dir = filepath.Join(t.TempDir(), "not-a-directory")
				must(t, os.WriteFile(a.studies.dir, []byte("x"), 0600))
			}
			docs := &setupFaultDocs{docStore: a.studies.docs}
			a.studies.docs = docs
			if fault == "issuer-write" {
				docs.failAt = 2
			}
			if fault == "final-write" {
				docs.failAt = 3
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- setupPost(a, s, "review", "open", nil) }()
			if fault == "collision" {
				w = <-done
				assertSetupStatus(t, w, 503)
				if creates.Load() != 0 || rotates.Load() != 0 {
					t.Fatal("adopted unrelated issuer")
				}
				return
			}
			<-entered
			edited := make(chan error, 1)
			go func() {
				_, err := a.studies.saveTracks("synthetic", []TrackDesign{{Name: "Must not win"}})
				edited <- err
			}()
			close(release)
			w = <-done
			if <-edited == nil {
				t.Fatal("track edit raced past first opening")
			}
			if fault == "none" {
				assertSetupStatus(t, w, 303)
			} else {
				assertSetupStatus(t, w, 503)
			}
			st := setupState(t, a)
			if fault == "final-write" && !strings.Contains(w.Body.String(), "data-setup-tracks-locked") {
				t.Fatal("failed final opening hid the issuer track lock")
			}
			if fault != "none" && (st.Status != "draft" || !st.Setup.Opening) {
				t.Fatal("failure falsely opened or lost intent")
			}
			if fault == "secret-write" {
				a.studies.dir = t.TempDir()
			}
			docs.failAt = 0
			if fault != "none" {
				w = setupPost(a, s, "review", "open", nil)
				assertSetupStatus(t, w, 303)
			}
			st = setupState(t, a)
			if st.Status != "open" || st.IssuerID != "study-synthetic" || len(st.Consent) != 1 || st.Setup.Opening || creates.Load() != 1 {
				t.Fatal("unsafe retry", st.Status, creates.Load())
			}
			w = savedRequest(a, s, "GET", "/studies/synthetic/setup/share", nil)
			assertSetupStatus(t, w, 200)
			if !strings.Contains(w.Body.String(), "data-setup-complete") || strings.Contains(w.Body.String(), "data-app-link") {
				t.Fatal("invite-only share exposes unrestricted joining")
			}
			w = setupPost(a, s, "review", "open", nil)
			assertSetupStatus(t, w, 303)
			if creates.Load() != 1 || len(setupState(t, a).Consent) != 1 {
				t.Fatal("repeat opening duplicated issuer/consent")
			}
		})
	}
}

func TestSetupSyntheticViews(t *testing.T) {
	a, s, srv := setupHTTPApp(t)
	defer srv.Close()
	dir := os.Getenv("VOPTRACK_SETUP_PREVIEW")
	for _, lang := range languages {
		for _, state := range []string{"basics", "collection", "information", "recruitment", "review", "share", "incomplete", "stale", "validation", "storage", "recovery"} {
			st := setupState(t, a)
			st.Title = "Synthetic study / Tutkimus / Исследование — " + strings.Repeat("Long title ", 8)
			st.Summary = strings.Repeat("Synthetic participant information. ", 20)
			draft := studySetup{Stage: "review", ConsentText: strings.Repeat("Synthetic consent text. ", 30)}
			st.Setup = &draft
			stage, problem := state, ""
			switch state {
			case "share":
				st.Status = "open"
				st.JoinMode = "open"
				st.IssuerID = "study-synthetic"
				st.Consent = []ConsentVersion{{Version: 1, Text: draft.ConsentText}}
			case "incomplete":
				stage = "review"
				st.Summary = ""
				st.Tracks = nil
			case "stale":
				stage = "information"
				problem = "setup_stale"
			case "validation":
				stage = "basics"
				problem = "form_run_by_long"
				st.RunBy = strings.Repeat("Team ", 30)
			case "storage":
				stage = "information"
				problem = "setup_unavailable"
			case "recovery":
				stage = "review"
				problem = "setup_recovery"
				draft.Opening = true
			case "collection":
				st.Tracks = []TrackDesign{{TypeID: "study.synthetic.sleep", Name: "Synthetic sleep", Fields: []TrackField{{ID: "hours", Name: "Hours of sleep with a long synthetic value name", Kind: KindNumber, Meaning: "sample", Unit: "h"}}}}
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/studies/synthetic/setup/"+stage+"?lang="+lang, nil)
			a.renderSetup(w, r, s, st, stage, 200, problem, "")
			body := w.Body.String()
			if strings.Contains(body, "<script") || !strings.Contains(body, translator("en")("setup_title")) {
				t.Fatal("unsafe or untranslated setup")
			}
			for _, key := range []string{"setup_title", "setup_navigation", "setup_reload", "setup_save", "setup_publish_note"} {
				if strings.Contains(body, ">"+key+"<") {
					t.Fatal("untranslated key", lang, key)
				}
			}
			if dir != "" {
				writeSuppressionPreview(t, dir, lang, state, body)
			}
		}
	}
}

func TestSetupStalePublicationOpeningAndPublicContract(t *testing.T) {
	a, s, srv := setupHTTPApp(t)
	defer srv.Close()
	w := setupPost(a, s, "information", "save", infoFields("Published text"))
	assertSetupStatus(t, w, 303)
	old := setupRevision(setupState(t, a))
	w = setupPost(a, s, "review", "publish", nil)
	assertSetupStatus(t, w, 303)
	for _, action := range []string{"publish", "open"} {
		w = setupPost(a, s, "review", action, url.Values{"revision": {old}})
		assertSetupStatus(t, w, 409)
	}
	// No revision must not become a fresh overwrite token on the error page.
	w = setupPost(a, s, "basics", "save", url.Values{"revision": {""}, "title": {"Retained"}})
	assertSetupStatus(t, w, 409)
	if !strings.Contains(w.Body.String(), `name="revision" value=""`) {
		t.Fatal("missing revision silently refreshed")
	}
	st := setupState(t, a)
	first := st.Consent[0]
	fields := infoFields("Editorial wording")
	fields.Del("material")
	w = setupPost(a, s, "information", "save", fields)
	assertSetupStatus(t, w, 303)
	w = setupPost(a, s, "review", "publish", nil)
	assertSetupStatus(t, w, 303)
	st = setupState(t, a)
	if len(st.Consent) != 2 || st.Consent[0] != first || !st.Consent[1].Editorial || st.LatestMaterial() != 1 {
		t.Fatal("immutable/editorial history changed")
	}
	st.Status = "open"
	st.IssuerID = "study-synthetic"
	st.Setup.ConsentText = "UNPUBLISHED PRIVATE API SENTINEL"
	must(t, a.studies.putSetup(st))
	w = savedRequest(a, nil, "GET", "/api/v1/studies/synthetic", nil)
	assertSetupStatus(t, w, 200)
	if strings.Contains(w.Body.String(), "SENTINEL") || strings.Contains(w.Body.String(), "setup") || !strings.Contains(w.Body.String(), "Editorial wording") {
		t.Fatal("public study API exposed draft or changed published text")
	}
}

func TestSetupDetectsLegacyHTTPRoutes(t *testing.T) {
	a, s, srv := setupHTTPApp(t)
	defer srv.Close()
	old := setupRevision(setupState(t, a))
	form := url.Values{"csrf": {s.CSRF}, "revision": {old}, "catalogue": {"com.voptrack.track.weight"}}
	w := savedRequest(a, s, "POST", "/studies/synthetic/tracks", form)
	assertSetupStatus(t, w, 303)
	w = setupPost(a, s, "review", "publish", url.Values{"revision": {old}})
	assertSetupStatus(t, w, 409)
	old = setupRevision(setupState(t, a))
	form = infoFields("Legacy publication")
	form.Set("csrf", s.CSRF)
	form.Set("revision", old)
	form.Set("title", "Legacy HTTP")
	form.Set("status", "open")
	form.Set("join_mode", "open")
	w = savedRequest(a, s, "POST", "/studies/synthetic/edit", form)
	assertSetupStatus(t, w, 303)
	w = setupPost(a, s, "basics", "save", url.Values{"revision": {old}, "title": {"Must not replace legacy"}})
	assertSetupStatus(t, w, 409)
	st := setupState(t, a)
	if st.Status != "draft" || st.IssuerID != "" || len(st.Consent) != 0 || st.Setup.ConsentText != "Legacy publication" {
		t.Fatal("legacy draft form bypassed explicit publication/opening")
	}
	if st.Title != "Legacy HTTP" {
		t.Fatal("legacy edit overwritten")
	}
}

func TestLegacyDraftFailureIsAtomicAndRetainsInput(t *testing.T) {
	a, s, srv := setupHTTPApp(t)
	defer srv.Close()
	st := setupState(t, a)
	st.Consent = []ConsentVersion{{Version: 1, Text: "Existing published consent"}}
	must(t, a.studies.putSetup(st))
	before := setupRevision(setupState(t, a))
	docs := &setupFaultDocs{docStore: a.studies.docs}
	a.studies.docs = docs
	form := infoFields("Retained private consent")
	form.Set("title", "Retained legacy title")
	form.Set("revision", before)
	form.Set("csrf", s.CSRF)
	form.Set("status", "open")
	docs.fail.Store(true)
	w := savedRequest(a, s, "POST", "/studies/synthetic/edit", form)
	assertSetupStatus(t, w, 503)
	if !strings.Contains(w.Body.String(), "Retained private consent") || !strings.Contains(w.Body.String(), "Retained legacy title") || setupRevision(setupState(t, a)) != before {
		t.Fatal("legacy failed save lost input or altered history")
	}
	docs.fail.Store(false)
	w = savedRequest(a, s, "POST", "/studies/synthetic/edit", form)
	assertSetupStatus(t, w, 303)
	st = setupState(t, a)
	if len(st.Consent) != 1 || st.Consent[0].Text != "Existing published consent" || st.Setup.ConsentText != "Retained private consent" || st.Status != "draft" || st.IssuerID != "" || !strings.HasSuffix(w.Header().Get("Location"), "/setup/review") {
		t.Fatal("legacy draft save published, opened or lost history")
	}
	w = savedRequest(a, s, "POST", "/studies/synthetic/edit", form)
	assertSetupStatus(t, w, 409)
	if !strings.Contains(w.Body.String(), "Retained private consent") {
		t.Fatal("legacy stale input lost")
	}
}

func TestSetupFailedOpeningIntentDoesNotCreateIssuer(t *testing.T) {
	a, s, old := setupHTTPApp(t)
	defer old.Close()
	assertSetupStatus(t, setupPost(a, s, "information", "save", infoFields("Publish first")), 303)
	assertSetupStatus(t, setupPost(a, s, "review", "publish", nil), 303)
	var creates atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/account" {
			writeJSON(w, 200, map[string]any{"account": "owner", "server_administrator": true, "installation": map[string]bool{"administer": true}})
			return
		}
		if r.URL.Path == "/admin/api/v1/issuers" && r.Method == "GET" {
			writeJSON(w, 200, map[string]any{"issuers": []any{}})
			return
		}
		creates.Add(1)
		writeJSON(w, 201, map[string]string{"secret": "synthetic"})
	}))
	defer srv.Close()
	a.tessera = &Tessera{internal: srv.URL, http: srv.Client()}
	before := setupRevision(setupState(t, a))
	docs := &setupFaultDocs{docStore: a.studies.docs}
	a.studies.docs = docs
	docs.fail.Store(true)
	w := setupPost(a, s, "review", "open", nil)
	assertSetupStatus(t, w, 503)
	if creates.Load() != 0 || setupRevision(setupState(t, a)) != before {
		t.Fatal("failed durable intent still created an issuer or changed progress")
	}
}

func TestLegacyDraftPendingOpeningRetainsInput(t *testing.T) {
	a, s, srv := setupHTTPApp(t)
	defer srv.Close()
	st := setupState(t, a)
	draft := setupDraft(st)
	draft.Opening = true
	draft.Stage = "review"
	st.Setup = &draft
	must(t, a.studies.putSetup(st))
	form := infoFields("Retained interrupted consent")
	form.Set("csrf", s.CSRF)
	form.Set("revision", setupRevision(setupState(t, a)))
	form.Set("title", "Retained interrupted title")
	w := savedRequest(a, s, "POST", "/studies/synthetic/edit", form)
	assertSetupStatus(t, w, 409)
	if !strings.Contains(w.Body.String(), "Retained interrupted consent") || !strings.Contains(w.Body.String(), "Retained interrupted title") {
		t.Fatal("opening conflict lost legacy submitted input")
	}
}

func TestStaleDraftEditorAfterOpening(t *testing.T) {
	a, s, srv := setupHTTPApp(t)
	defer srv.Close()
	assertSetupStatus(t, setupPost(a, s, "information", "save", infoFields("Reviewed consent")), 303)
	assertSetupStatus(t, setupPost(a, s, "review", "publish", nil), 303)
	old := setupRevision(setupState(t, a))
	// The other tab has completed opening since this draft editor was displayed.
	st := setupState(t, a)
	st.Status = "open"
	st.IssuerID = "study-synthetic"
	must(t, a.studies.putSetup(st))
	before := setupRevision(setupState(t, a))
	f := infoFields("Unpublished text from stale draft tab")
	f.Set("csrf", s.CSRF)
	f.Set("revision", old)
	f.Set("title", "Old draft title")
	for _, rev := range []string{old, ""} {
		f.Set("revision", rev)
		w := savedRequest(a, s, "POST", "/studies/synthetic/edit", f)
		assertSetupStatus(t, w, 409)
		if setupRevision(setupState(t, a)) != before {
			t.Fatal("stale draft Save published consent or overwrote current study")
		}
		if !strings.Contains(w.Body.String(), "Unpublished text from stale draft tab") || !strings.Contains(w.Body.String(), `name="revision" value="`+rev+`"`) || !strings.Contains(w.Body.String(), translator("en")("setup_reload")) {
			t.Fatal("conflict lost submitted draft, original revision or reload link")
		}
	}
	// Fresh published-study edits still publish through the established editor.
	f.Set("revision", before)
	f.Set("status", "open")
	w := savedRequest(a, s, "POST", "/studies/synthetic/edit", f)
	assertSetupStatus(t, w, 303)
	if len(setupState(t, a).Consent) != 2 {
		t.Fatal("fresh published edit stopped working")
	}
}

func TestStaleDesignerCannotOverwriteWizard(t *testing.T) {
	a, s, srv := setupHTTPApp(t)
	defer srv.Close()
	old := setupRevision(setupState(t, a))
	page := savedRequest(a, s, "GET", "/studies/synthetic/tracks", nil)
	if !strings.Contains(page.Body.String(), `name="revision" value="`+old+`"`) {
		t.Fatal("designer omitted revision")
	}
	assertSetupStatus(t, setupPost(a, s, "collection", "save", url.Values{"t0.present": {"1"}, "t0.name": {"New wizard design"}}), 303)
	before := setupRevision(setupState(t, a))
	f := url.Values{"csrf": {s.CSRF}, "catalogue": {"com.voptrack.track.weight"}, "t0.present": {"1"}, "t0.name": {"Retained old design"}}
	for _, rev := range []string{old, ""} {
		f.Set("revision", rev)
		w := savedRequest(a, s, "POST", "/studies/synthetic/tracks", f)
		assertSetupStatus(t, w, 409)
		if setupRevision(setupState(t, a)) != before {
			t.Fatal("stale designer overwrote wizard changes")
		}
		if !strings.Contains(w.Body.String(), "Retained old design") || !strings.Contains(w.Body.String(), `name="revision" value="`+rev+`"`) || !strings.Contains(w.Body.String(), translator("en")("setup_reload")) {
			t.Fatal("designer conflict lost input, original revision or reload link")
		}
	}
	f.Set("revision", before)
	f.Del("t0.present")
	f.Del("t0.name")
	assertSetupStatus(t, savedRequest(a, s, "POST", "/studies/synthetic/tracks", f), 303)
	if setupRevision(setupState(t, a)) == before {
		t.Fatal("fresh designer save did not persist")
	}
}

type failFirstStudyRead struct {
	docStore
	failed bool
}

func (d *failFirstStudyRead) get(name string) ([]byte, bool, error) {
	if !d.failed && name == "studies/synthetic.json" {
		d.failed = true
		return nil, false, errors.New("temporary read failure")
	}
	return d.docStore.get(name)
}
func TestDraftEditorReadFailureDoesNotFallThrough(t *testing.T) {
	a, s, srv := setupHTTPApp(t)
	defer srv.Close()
	before := setupRevision(setupState(t, a))
	a.studies.docs = &failFirstStudyRead{docStore: a.studies.docs}
	f := infoFields("Must remain unpublished")
	f.Set("csrf", s.CSRF)
	f.Set("revision", before)
	f.Set("title", "Do not save")
	w := savedRequest(a, s, "POST", "/studies/synthetic/edit", f)
	assertSetupStatus(t, w, 503)
	if setupRevision(setupState(t, a)) != before {
		t.Fatal("read failure fell through to consent publication")
	}
}
