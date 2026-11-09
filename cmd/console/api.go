package main

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The study API: what the Voptrack app needs to show a study, take a person's
// consent in the app, and join them -- the same study page, the same rules and
// the same consent record as the web join, in JSON. Agreed with the app team
// on 2026-09-27 (voptrack-ios docs/research/home-and-study-app-flows.md).
//
//	GET  /api/v1/studies/{slug}          what the study page shows; 404 unless open
//	POST /api/v1/studies/{slug}/join     {"version": N, "code": "…"} -> {"redeem_url", "account"}
//	GET  /api/v1/studies/{slug}/consent  own token: status and agreement needed
//	POST /api/v1/studies/{slug}/consent  own token, {"version": N}: agree again
//
// The first two are public, like the page, and use no cookies: nothing ambient
// authorises them, so there is nothing for another site to forge. Consent routes
// take the participant's Tessera token and answer only about its account.

type apiTrackValue struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Unit    string `json:"unit,omitempty"`
	Meaning string `json:"meaning,omitempty"`
}

type apiTrack struct {
	Name      string          `json:"name"`
	Catalogue string          `json:"catalogue,omitempty"`
	Values    []apiTrackValue `json:"values,omitempty"`
}

type apiStudy struct {
	RunBy      string     `json:"run_by"`
	Slug       string     `json:"slug"`
	Title      string     `json:"title"`
	Summary    string     `json:"summary"`
	Collected  string     `json:"collected"`
	WhoSees    string     `json:"who_sees"`
	HowToLeave string     `json:"how_to_leave"`
	JoinMode   string     `json:"join_mode"` // "open" or "invite"
	Tracks     []apiTrack `json:"tracks"`
	Consent    struct {
		Material      bool      `json:"material"`
		AgreementFrom int       `json:"agreement_from"`
		Version       int       `json:"version"`
		Text          string    `json:"text"`
		Published     time.Time `json:"published"`
	} `json:"consent"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code}})
}

// openStudyAPI answers the study when it is open for joining, as the page does,
// and says so when it has closed.
func (a *app) openStudyAPI(w http.ResponseWriter, r *http.Request) (Study, bool) {
	st, found := a.studies.get(r.PathValue("slug"))
	if a.cfg.Variant == "research" && found && st.Status == "closed" {
		// It was public while open, so saying it ended reveals nothing; the
		// date is absent for studies closed before it was recorded.
		problem := map[string]any{"code": "study_closed"}
		if st.ClosedAt != nil {
			problem["closed_at"] = st.ClosedAt
		}
		writeJSON(w, http.StatusGone, map[string]any{"error": problem})
		return Study{}, false
	}
	if a.cfg.Variant != "research" || !found || st.Status != "open" {
		writeAPIError(w, http.StatusNotFound, "not_found")
		return Study{}, false
	}
	return st, true
}

func (a *app) apiStudy(w http.ResponseWriter, r *http.Request) {
	st, ok := a.openStudyAPI(w, r)
	if !ok {
		return
	}
	out := apiStudy{RunBy: st.RunBy, Slug: st.Slug, Title: st.Title, Summary: st.Summary, Collected: st.Collected, WhoSees: st.WhoSees,
		HowToLeave: st.HowToLeave, JoinMode: st.JoinMode, Tracks: []apiTrack{}}
	cur, _ := st.Current()
	out.Consent.Version, out.Consent.Text, out.Consent.Published = cur.Version, cur.Text, cur.Published
	out.Consent.Material, out.Consent.AgreementFrom = !cur.Editorial, st.LatestMaterial()
	for _, t := range st.Tracks {
		track := apiTrack{Name: t.Name, Catalogue: t.Catalogue}
		if t.Catalogue != "" {
			track.Name = catalogueName(t.Catalogue)
		}
		for _, f := range t.Fields {
			track.Values = append(track.Values, apiTrackValue{Name: f.Name, Kind: string(f.Kind), Unit: f.Unit, Meaning: f.Meaning})
		}
		out.Tracks = append(out.Tracks, track)
	}
	writeJSON(w, http.StatusOK, out)
}

func catalogueName(id string) string {
	for _, c := range CatalogueTracks {
		if c.ID == id {
			return c.Name
		}
	}
	return id
}

func (a *app) apiJoin(w http.ResponseWriter, r *http.Request) {
	st, ok := a.openStudyAPI(w, r)
	if !ok {
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeAPIError(w, http.StatusUnsupportedMediaType, "json_required")
		return
	}
	var body struct {
		Version int    `json:"version"`
		Code    string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	inv, refusal, err := a.joinStudy(r.Context(), st, body.Version, body.Code)
	switch {
	case refusal == "stale_version":
		// Consent is to the version the person was shown; the app shows the
		// current one and asks again.
		cur, _ := st.Current()
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "stale_version", "current_version": cur.Version}})
	case refusal == "code_used":
		writeAPIError(w, http.StatusForbidden, "code_used")
	case refusal == "invite_only":
		writeAPIError(w, http.StatusForbidden, "invite_only")
	case refusal == "study_full":
		writeAPIError(w, http.StatusConflict, "study_full")
	case err != nil:
		writeAPIError(w, http.StatusBadGateway, "unavailable")
	default:
		writeJSON(w, http.StatusOK, map[string]string{"redeem_url": withPin(inv.RedeemURL, a.tlsPin()), "account": inv.Account})
	}
}

// participant authenticates a consent-route request with the participant's own
// Tessera token, and answers about that account only. A draft study never has
// participants; a closed one still answers, so the app can say it ended.
func (a *app) participant(w http.ResponseWriter, r *http.Request) (Study, string, string, bool) {
	st, found := a.studies.get(r.PathValue("slug"))
	token, isBearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !isBearer || token == "" {
		writeAPIError(w, http.StatusUnauthorized, "authentication_required")
		return Study{}, "", "", false
	}
	who, err := a.tessera.account(r.Context(), token)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "authentication_failed")
		return Study{}, "", "", false
	}
	if a.cfg.Variant != "research" || !found || st.IssuerID == "" || st.Status == "draft" {
		writeAPIError(w, http.StatusNotFound, "not_found")
		return Study{}, "", "", false
	}
	if len(a.studies.histories(st.Slug)[who.Account].consents) == 0 {
		writeAPIError(w, http.StatusNotFound, "not_a_participant")
		return Study{}, "", "", false
	}
	return st, who.Account, token, true
}

// consentStatus is the body of every consent route: what the participant
// agreed to, whether the app may send, and whether they have withdrawn.
func (a *app) consentStatus(w http.ResponseWriter, st Study, account string) {
	type agreed struct {
		Version int       `json:"version"`
		At      time.Time `json:"at"`
	}
	h := a.studies.histories(st.Slug)[account]
	latest, withdrawal := h.latest(time.Now().UTC())
	cur, _ := st.Current()
	out := struct {
		Study           string     `json:"study"`
		Status          string     `json:"status"`
		ClosedAt        *time.Time `json:"closed_at,omitempty"`
		RunBy           string     `json:"run_by"`
		CurrentVersion  int        `json:"current_version"`
		AgreementNeeded bool       `json:"agreement_needed"`
		Consents        []agreed   `json:"consents"`
		Withdrawn       *time.Time `json:"withdrawn,omitempty"`
	}{Study: st.Slug, Status: st.Status, RunBy: st.RunBy, CurrentVersion: cur.Version, Consents: []agreed{}}
	if st.Status == "closed" {
		out.ClosedAt = st.ClosedAt
	}
	for _, c := range h.consents {
		out.Consents = append(out.Consents, agreed{c.Version, c.At})
	}
	out.AgreementNeeded = latest.Version < st.LatestMaterial() || withdrawal.After(latest.At)
	if withdrawal.After(latest.At) {
		out.Withdrawn = &withdrawal
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *app) apiConsent(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		a.apiAgree(w, r)
		return
	}
	st, account, _, ok := a.participant(w, r)
	if ok {
		a.consentStatus(w, st, account)
	}
}

// apiAgree records agreement to the current consent version. After a
// withdrawal it is rejoining: the participant's own token unfreezes the
// study's reader on their store (Tessera alpha.28), so the researcher reads
// again -- including what was recorded while withdrawn -- and only then is
// the agreement recorded. A covered version writes nothing, so a retry after a
// lost response is harmless and nobody can fill the Console's store by
// repeating it.
func (a *app) apiAgree(w http.ResponseWriter, r *http.Request) {
	st, account, token, ok := a.participant(w, r)
	if !ok {
		return
	}
	if st.Status == "closed" {
		writeAPIError(w, http.StatusConflict, "study_closed")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeAPIError(w, http.StatusUnsupportedMediaType, "json_required")
		return
	}
	var body struct {
		Version int `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	cur, _ := st.Current()
	if body.Version != cur.Version {
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "stale_version", "current_version": cur.Version}})
		return
	}
	latest, withdrawal := a.studies.histories(st.Slug)[account].latest(time.Now().UTC())
	withdrawn := withdrawal.After(latest.At)
	if withdrawn {
		if code := a.studyReader(r, token, "unfreeze"); code != "" {
			writeAPIError(w, statusOf(code), code)
			return
		}
	}
	if withdrawn || latest.Version < st.LatestMaterial() {
		if err := a.studies.recordConsent(st.Slug, ConsentRecord{Account: account, Version: body.Version, At: time.Now().UTC()}); err != nil {
			writeAPIError(w, http.StatusBadGateway, "unavailable")
			return
		}
	}
	a.consentStatus(w, st, account)
}

// apiWithdraw stops the study reading anything new from the participant's
// store and keeps what it already had, the owner's decision of 2026-10-07: the
// participant's own token freezes the study's reader (Tessera alpha.28), then
// the withdrawal is recorded. Nothing is deleted; erasing stays a separate,
// explicit request. Withdrawing again changes nothing: a repeated freeze keeps
// its first cut, and a current withdrawal is not recorded twice.
func (a *app) apiWithdraw(w http.ResponseWriter, r *http.Request) {
	st, account, token, ok := a.participant(w, r)
	if !ok {
		return
	}
	if code := a.studyReader(r, token, "freeze"); code != "" {
		writeAPIError(w, statusOf(code), code)
		return
	}
	latest, withdrawal := a.studies.histories(st.Slug)[account].latest(time.Now().UTC())
	if !withdrawal.After(latest.At) {
		if err := a.studies.recordWithdrawal(st.Slug, Withdrawal{Account: account, At: time.Now().UTC()}); err != nil {
			writeAPIError(w, http.StatusBadGateway, "unavailable")
			return
		}
	}
	a.consentStatus(w, st, account)
}

// studyReader freezes or unfreezes the study's analysis reader on the
// participant's own store, with the participant's own token: Tessera lets only
// the store's recorded owner unfreeze, so the researcher can never undo a
// withdrawal. It answers an API error code, or "" when done. Unfreezing a
// share that is not frozen is done already (a retry after a lost response), and
// so is freezing where the study holds no share.
func (a *app) studyReader(r *http.Request, token, action string) string {
	l, err := a.tessera.listing(r.Context(), token)
	if err != nil {
		return "unavailable"
	}
	var stores []string
	for _, d := range l.Datasets {
		if d.Access.Write {
			stores = append(stores, d.ID)
		}
	}
	switch {
	case len(stores) == 0 && len(l.Datasets) > 0:
		return "write_approval_required" // signed in to read only
	case len(stores) != 1:
		return "store_unavailable" // erased by Remove my data, or never theirs
	}
	path := "/v1/datasets/" + url.PathEscape(stores[0]) + "/readers/" + url.PathEscape(a.cfg.Analysis) + ":" + action
	err = a.tessera.call(r.Context(), "POST", path, token, "", nil, 204, nil)
	var api *apiError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &api) && api.Status == 409 && action == "unfreeze":
		return ""
	case errors.As(err, &api) && api.Status == 403:
		return "write_approval_required"
	case errors.As(err, &api) && api.Status == 404 && action == "freeze":
		// No reader share here (Tessera: 404 not_found): the replacement
		// made when they erased with Remove my data. The study reads nothing
		// of theirs, which is what withdrawing asks for -- done, not a failure.
		return ""
	case errors.As(err, &api) && api.Status == 404:
		// Nothing to unfreeze, so nothing to rejoin: they erased.
		return "store_unavailable"
	default:
		return "unavailable"
	}
}

func statusOf(code string) int {
	switch code {
	case "store_unavailable":
		return http.StatusConflict
	case "write_approval_required":
		return http.StatusForbidden
	default:
		return http.StatusBadGateway
	}
}

// studyAppLink opens the study in the app: its server, which study, and the
// single-use code when the study is invite-only. The app shows the study from
// the API above and asks for consent itself.
func studyAppLink(public, title, slug, code, pin string) string {
	link, err := connectLink(public, title, "study", pin)
	if err != nil {
		return ""
	}
	link += "&study=" + url.QueryEscape(slug)
	if code != "" {
		link += "&code=" + url.QueryEscape(code)
	}
	return link
}
