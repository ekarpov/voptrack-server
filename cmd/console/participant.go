package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// These HTML adapters call the existing API handlers in process, with only
// the participant's credential. API responses and audit writes remain there.
type participantResponse struct {
	header http.Header
	status int
	bytes.Buffer
}

func (w *participantResponse) Header() http.Header    { return w.header }
func (w *participantResponse) WriteHeader(status int) { w.status = status }

type participantAgreement struct {
	Version int
	At      time.Time
}
type participantStatus struct {
	Status          string
	AgreementNeeded bool `json:"agreement_needed"`
	Consents        []participantAgreement
	Withdrawn       *time.Time
	Error           struct{ Code string }
}
type participantData struct {
	Study         Study
	Current       ConsentVersion
	Status        participantStatus
	Latest        participantAgreement
	Action, Error string
	Write         bool
}

func participantReturn(p pending) string {
	if p.ReturnStudy != "" {
		return "/study/" + p.ReturnStudy + "/consent"
	}
	return "/"
}
func (a *app) startParticipantSignIn(w http.ResponseWriter, r *http.Request, sess *session, slug, account string) {
	if a.tessera.websiteID != "" {
		a.startWebsiteSignIn(w, r, sess, pending{ReturnStudy: slug, ParticipantAccount: account})
		return
	}
	state, verifier := random(24), random(48)
	a.sessions.addPending(sess, state, pending{Verifier: verifier, Expires: time.Now().Add(10 * time.Minute), ReturnStudy: slug, ParticipantAccount: account, Remember: rememberChoice(r, sess)})
	http.Redirect(w, r, a.tessera.authorizeURL(state, verifier), http.StatusSeeOther)
}

// Fresh identity and listing checks also reject revoked or expired credentials.
func (a *app) participantToken(r *http.Request, sess *session) (string, bool) {
	a.sessions.mu.Lock()
	conns := append([]*connection(nil), sess.Connections...)
	if sess.Owner != nil {
		conns = append(conns, sess.Owner)
	}
	account := sess.Account
	a.sessions.mu.Unlock()
	var readonly string
	for _, c := range conns {
		token, err := c.token(r.Context(), a.tessera)
		if err != nil {
			continue
		}
		who, err := a.tessera.account(r.Context(), token)
		if err != nil || who.Account != account {
			continue
		}
		listing, err := a.tessera.listing(r.Context(), token)
		if err != nil {
			continue
		}
		if len(listing.Datasets) == 1 && listing.Datasets[0].Access.Write {
			return token, true
		}
		readonly = token
	}
	return readonly, false
}
func (a *app) participantAPI(r *http.Request, token, action string, version int) (participantStatus, int) {
	req := r.Clone(r.Context())
	req.Header = make(http.Header)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Method = http.MethodGet
	req.Body = nil
	handler := a.apiConsent
	if action != "" {
		req.Method = http.MethodPost
		if action == "withdraw" {
			handler = a.apiWithdraw
		} else {
			handler = a.apiAgree
			req.Header.Set("Content-Type", "application/json")
			body, _ := json.Marshal(map[string]int{"version": version})
			req.Body = io.NopCloser(bytes.NewReader(body))
		}
	}
	out := &participantResponse{header: make(http.Header)}
	handler(out, req)
	var status participantStatus
	if json.Unmarshal(out.Bytes(), &status) != nil {
		status.Error.Code = "unavailable"
		return status, 502
	}
	return status, out.status
}

func (a *app) participantStudies(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if a.cfg.Variant != "research" {
		a.fail(w, r, sess, 404, "no_study", nil)
		return
	}
	if sess == nil || !sess.signedIn() {
		http.Redirect(w, r, "/login", 303)
		return
	}
	token, _ := a.participantToken(r, sess)
	if token == "" {
		a.participantListingError(w, r, sess, http.StatusUnauthorized)
		return
	}
	var studies []Study
	for _, st := range a.studies.list() {
		// Drafts have no participants; do not mistake their API not_found
		// response for a failed membership lookup.
		if st.Status == "draft" || st.IssuerID == "" {
			continue
		}
		req := r.Clone(r.Context())
		req.SetPathValue("slug", st.Slug)
		status, code := a.participantAPI(req, token, "", 0)
		switch {
		case code == http.StatusOK:
			studies = append(studies, st)
		case code == http.StatusNotFound && status.Error.Code == "not_a_participant":
			continue
		default:
			// Never display an empty or partial list as complete after a failure.
			a.participantListingError(w, r, sess, code)
			return
		}
	}
	a.render(w, r, sess, "participant_studies", 200, studies)
}
func (a *app) participantListingError(w http.ResponseWriter, r *http.Request, sess *session, code int) {
	status, key, retry, label := http.StatusBadGateway, "participant_retry", "/my-studies", "participant_studies"
	if code == http.StatusUnauthorized {
		status, key, retry, label = http.StatusUnauthorized, "expired", "/login", "sign_in"
	}
	a.render(w, r, sess, "message", status, map[string]string{"Key": key, "Retry": retry, "RetryLabel": label})
}

func (a *app) participantPageData(w http.ResponseWriter, r *http.Request) (*session, participantData, string, bool) {
	sess := a.sessions.get(w, r, false)
	st, found := a.studies.get(r.PathValue("slug"))
	if a.cfg.Variant != "research" || !found || st.Status == "draft" {
		a.fail(w, r, sess, 404, "no_study", nil)
		return sess, participantData{}, "", false
	}
	if sess == nil || !sess.signedIn() {
		if r.Method != http.MethodGet {
			a.fail(w, r, sess, 403, "expired", nil)
		} else {
			http.Redirect(w, r, "/login?study="+url.QueryEscape(st.Slug), 303)
		}
		return sess, participantData{}, "", false
	}
	token, write := a.participantToken(r, sess)
	status, code := a.participantAPI(r, token, "", 0)
	if code != 200 {
		key := "participant_unavailable"
		if code == 401 {
			key = "expired"
		}
		a.fail(w, r, sess, code, key, nil)
		return sess, participantData{}, "", false
	}
	cur, _ := st.Current()
	d := participantData{Study: st, Current: cur, Status: status, Write: write}
	// The existing owner-readers route requires Write approval. A missing
	// share on the replacement store can be explained without trying to unfreeze.
	if write && status.Withdrawn != nil {
		if listing, err := a.tessera.listing(r.Context(), token); err == nil && len(listing.Datasets) == 1 {
			path := "/v1/datasets/" + url.PathEscape(listing.Datasets[0].ID) + "/readers/" + url.PathEscape(a.cfg.Analysis)
			err = a.tessera.call(r.Context(), "GET", path, token, "", nil, http.StatusOK, nil)
			var problem *apiError
			if errors.As(err, &problem) && problem.Status == http.StatusNotFound {
				d.Error = "participant_erased"
			}
		}
	}
	for _, c := range status.Consents {
		if !c.At.Before(d.Latest.At) {
			d.Latest = c
		}
	}
	return sess, d, token, true
}
func participantActionValid(action string) bool {
	return action == "renew" || action == "rejoin" || action == "withdraw"
}
func (a *app) participantConsentPage(w http.ResponseWriter, r *http.Request) {
	sess, d, _, ok := a.participantPageData(w, r)
	if !ok {
		return
	}
	action := r.PathValue("action")
	if action != "" && !participantActionValid(action) {
		a.fail(w, r, sess, 404, "no_study", nil)
		return
	}
	if action != "withdraw" && d.Study.Status == "closed" {
		action = ""
	}
	if (action == "renew" && d.Status.Withdrawn != nil) || (action == "rejoin" && d.Status.Withdrawn == nil) {
		action = ""
	}
	d.Action = action
	switch r.URL.Query().Get("error") {
	case "account", "confirm", "changed", "stale", "erased", "retry", "write":
		d.Error = "participant_" + r.URL.Query().Get("error")
	case "closed":
		d.Error = "study_is_closed"
	}
	if d.Error == "participant_erased" {
		d.Action = ""
	}
	a.render(w, r, sess, "participant_consent", 200, d)
}
func (a *app) participantConsentAction(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess == nil || !sess.checkCSRF(r) {
		a.fail(w, r, sess, 403, "expired", nil)
		return
	}
	sess, d, token, ok := a.participantPageData(w, r)
	if !ok {
		return
	}
	action := r.PathValue("action")
	if action == "approve" {
		a.startParticipantSignIn(w, r, sess, d.Study.Slug, sess.Account)
		return
	}
	if !participantActionValid(action) {
		a.fail(w, r, sess, 404, "no_study", nil)
		return
	}
	d.Action = action
	if r.PostFormValue("confirm") != "yes" {
		d.Error = "participant_confirm"
	} else if !d.Write {
		d.Error = "participant_write"
	} else if action != "withdraw" && d.Study.Status == "closed" {
		d.Error = "study_is_closed"
		d.Action = ""
	} else if action == "renew" && d.Status.Withdrawn != nil {
		// An old renewal form must never silently become a rejoining agreement.
		d.Error = "participant_changed"
		d.Action = ""
	} else {
		version, _ := strconv.Atoi(r.PostFormValue("version"))
		status, code := a.participantAPI(r, token, action, version)
		if code == 200 {
			http.Redirect(w, r, "/study/"+d.Study.Slug+"/consent", 303)
			return
		}
		switch status.Error.Code {
		case "stale_version":
			d.Error = "participant_stale"
		case "study_closed":
			d.Error = "study_is_closed"
			d.Action = ""
		case "store_unavailable":
			d.Error = "participant_erased"
			d.Action = ""
		case "write_approval_required":
			d.Error = "participant_write"
			d.Write = false
		default:
			d.Error = "participant_retry"
		}

	}
	// Return to GET even on failure: refresh and language changes never replay a POST.
	errorCode := "retry"
	if d.Error == "study_is_closed" {
		errorCode = "closed"
	} else {
		errorCode = d.Error[len("participant_"):]
	}
	target := "/study/" + d.Study.Slug + "/consent"
	if d.Action != "" {
		target += "/" + d.Action
	}
	http.Redirect(w, r, target+"?error="+url.QueryEscape(errorCode), http.StatusSeeOther)
}
