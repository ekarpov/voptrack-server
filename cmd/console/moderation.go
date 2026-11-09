package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"time"
)

// These are wire response models, not a second moderation history. Authority,
// shares and stopped notices remain entirely in Tessera.
type shareImpact struct {
	Dataset string   `json:"dataset"`
	Groups  []string `json:"groups"`
	Readers []string `json:"readers"`
	Whole   []string `json:"whole_dataset_readers"`
}
type moderationTarget struct {
	Dataset, Type, Name string
	LastHomeTrack       bool // successful Home metadata contains only this concrete type
}
type moderationIntent struct {
	Target         moderationTarget
	Impact         shareImpact
	Nonce, Account string
	Expires        time.Time
}
type moderationData struct {
	Current        *shareImpact
	Candidates     []moderationTarget
	Target         moderationTarget
	Impact         *shareImpact
	Nonce, Message string
	Confirm        bool
}

func (t *Tessera) unshare(ctx context.Context, token, dataset, track string, dry bool) (shareImpact, error) {
	// Never let a caller accidentally select all stores or all types.
	if dataset == "" || track == "" {
		return shareImpact{}, errors.New("missing moderation target")
	}
	var out struct {
		Datasets []shareImpact `json:"datasets"`
		DryRun   bool          `json:"dry_run"`
	}
	err := t.post(ctx, "/admin/api/v1/unshare", token, map[string]any{"datasets": []string{dataset}, "types": []string{track}, "dry_run": dry}, &out)
	if err != nil {
		return shareImpact{}, err
	}
	if out.DryRun != dry || len(out.Datasets) > 1 {
		return shareImpact{}, errors.New("unexpected moderation result")
	}
	impact := shareImpact{Dataset: dataset}
	if len(out.Datasets) == 1 {
		impact = out.Datasets[0]
		if impact.Dataset != dataset {
			return shareImpact{}, errors.New("unexpected moderation store")
		}
	}
	// Order is immaterial; preserve duplicates because they can indicate a changed impact.
	sort.Strings(impact.Groups)
	sort.Strings(impact.Readers)
	sort.Strings(impact.Whole)
	if len(impact.Groups) == 0 {
		impact.Groups = nil
	}
	if len(impact.Readers) == 0 {
		impact.Readers = nil
	}
	if len(impact.Whole) == 0 {
		impact.Whole = nil
	}
	return impact, nil
}
func (i shareImpact) changes() bool { return len(i.Groups) > 0 || len(i.Readers) > 0 }

// Home's registry is the existing discovery boundary. The owner's overview
// deliberately hides grants on stores they do not administer (alpha.33), so an
// empty account.Datasets map cannot establish that Home has no shared stores.
// Do not infer ownership or discover stores via unrelated member accounts.
func (a *app) moderationStores() ([]string, error) {
	a.homeReg.mu.Lock()
	defer a.homeReg.mu.Unlock()
	return a.homeReg.readChecked()
}

func (a *app) moderationOwner(w http.ResponseWriter, r *http.Request) (*session, string) {
	sess := a.sessions.get(w, r, false)
	if a.cfg.Variant != "household" || a.cfg.HomeService == "" {
		a.fail(w, r, sess, 404, "no_home", nil)
		return nil, ""
	}
	if sess == nil || sess.Owner == nil || (r.Method == "POST" && !sess.checkCSRF(r)) {
		a.fail(w, r, sess, 403, "expired", nil)
		return nil, ""
	}
	token, err := sess.Owner.token(r.Context(), a.tessera)
	if err == nil {
		who, e := a.tessera.account(r.Context(), token)
		if e == nil && who.ServerAdministrator && who.Installation.Administer {
			return sess, token
		}
	}
	a.fail(w, r, sess, 403, "moderation_authority", nil)
	return nil, ""
}

// Every discovery error fails the entire listing. No private records, ownership
// guesses, free-form selectors or failed reads become moderation authority.
func (a *app) moderationCandidates(ctx context.Context, token string) ([]moderationTarget, error) {
	ids, err := a.moderationStores()
	if err != nil {
		return nil, err
	}
	var out []moderationTarget
	for _, id := range ids {
		home, err := a.homeToken(ctx, id)
		if err != nil {
			return nil, err
		}
		metas, err := a.tessera.types(ctx, home, id)
		if err != nil {
			return nil, err
		}
		for _, m := range metas {
			if m.ID == "" {
				continue
			}
			impact, err := a.tessera.unshare(ctx, token, id, m.ID, true)
			if err != nil {
				return nil, err
			}
			// Parent and child types can be shared independently. Use the exact
			// dry-run impact, not inheritance, to establish eligibility; Home
			// may also read a type through an unaffected whole-store grant.
			if !containsString(impact.Readers, a.cfg.HomeService) {
				continue
			}
			out = append(out, moderationTarget{Dataset: id, Type: m.ID, Name: m.Name, LastHomeTrack: len(metas) == 1})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dataset != out[j].Dataset {
			return out[i].Dataset < out[j].Dataset
		}
		return out[i].Type < out[j].Type
	})
	return out, nil
}
func containsString(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}
func (a *app) moderationPage(w http.ResponseWriter, r *http.Request) {
	sess, token := a.moderationOwner(w, r)
	if sess == nil {
		return
	}
	candidates, err := a.moderationCandidates(r.Context(), token)
	if err != nil {
		a.render(w, r, sess, "moderation", 503, moderationData{Message: "moderation_discovery"})
		return
	}
	a.render(w, r, sess, "moderation", 200, moderationData{Candidates: candidates})
}
func singlePost(r *http.Request, key string) (string, bool) {
	v := r.PostForm[key]
	if len(v) != 1 || v[0] == "" {
		return "", false
	}
	return v[0], true
}
func moderationForm(r *http.Request, keys ...string) bool {
	if r.ParseForm() != nil || len(r.URL.Query()) != 0 {
		return false
	}
	allowed := map[string]bool{"csrf": true}
	for _, k := range keys {
		allowed[k] = true
	}
	for k, v := range r.PostForm {
		if !allowed[k] || len(v) != 1 {
			return false
		}
	}
	return true
}
func (a *app) moderationPreview(w http.ResponseWriter, r *http.Request) {
	sess, token := a.moderationOwner(w, r)
	if sess == nil {
		return
	}
	a.moderationMu.Lock()
	defer a.moderationMu.Unlock()
	if !moderationForm(r, "dataset", "track") {
		a.fail(w, r, sess, 400, "moderation_invalid", nil)
		return
	}
	dataset, ok := singlePost(r, "dataset")
	track, ok2 := singlePost(r, "track")
	if !ok || !ok2 {
		a.fail(w, r, sess, 400, "moderation_invalid", nil)
		return
	}
	candidates, err := a.moderationCandidates(r.Context(), token)
	if err != nil {
		a.render(w, r, sess, "moderation", 503, moderationData{Message: "moderation_discovery"})
		return
	}
	var target moderationTarget
	for _, c := range candidates {
		if c.Dataset == dataset && c.Type == track {
			target = c
		}
	}
	if target.Dataset == "" {
		a.fail(w, r, sess, 409, "moderation_invalid", nil)
		return
	}
	impact, err := a.tessera.unshare(r.Context(), token, dataset, track, true)
	if err != nil || !impact.changes() || !containsString(impact.Readers, a.cfg.HomeService) {
		a.render(w, r, sess, "moderation", 409, moderationData{Target: target, Message: "moderation_stale"})
		return
	}
	intent := &moderationIntent{Target: target, Impact: impact, Nonce: random(32), Account: sess.Account, Expires: time.Now().Add(5 * time.Minute)}
	a.sessions.mu.Lock()
	sess.Moderation = intent
	a.sessions.mu.Unlock()
	a.render(w, r, sess, "moderation", 200, moderationData{Target: target, Impact: &impact, Nonce: intent.Nonce, Confirm: true})
}
func (a *app) moderationConfirm(w http.ResponseWriter, r *http.Request) {
	sess, token := a.moderationOwner(w, r)
	if sess == nil {
		return
	}
	a.moderationMu.Lock()
	defer a.moderationMu.Unlock()
	if !moderationForm(r, "confirmation") {
		a.fail(w, r, sess, 400, "moderation_invalid", nil)
		return
	}
	nonce, ok := singlePost(r, "confirmation")
	a.sessions.mu.Lock()
	intent := sess.Moderation
	if ok && intent != nil && nonce == intent.Nonce {
		sess.Moderation = nil
	} else {
		intent = nil
	}
	a.sessions.mu.Unlock()
	if intent == nil || time.Now().After(intent.Expires) || intent.Account != sess.Account {
		a.fail(w, r, sess, 409, "moderation_expired", nil)
		return
	}
	d := moderationData{Target: intent.Target}
	// The nonce is already spent. Any failure requires an entirely new review.
	candidates, err := a.moderationCandidates(r.Context(), token)
	if err != nil {
		d.Message = "moderation_discovery"
		a.render(w, r, sess, "moderation", 503, d)
		return
	}
	eligible := false
	lastHomeTrack := false
	for _, c := range candidates {
		if c.Dataset == intent.Target.Dataset && c.Type == intent.Target.Type {
			eligible = true
			lastHomeTrack = c.LastHomeTrack
		}
	}
	impact, err := a.tessera.unshare(r.Context(), token, intent.Target.Dataset, intent.Target.Type, true)
	if err != nil {
		d.Message = "moderation_discovery"
		a.render(w, r, sess, "moderation", 503, d)
		return
	}
	if !eligible || !reflect.DeepEqual(impact, intent.Impact) {
		d.Message = "moderation_stale"
		a.render(w, r, sess, "moderation", 409, d)
		return
	}
	// Recheck the approved owner credential after discovery, immediately before
	// execution; a long metadata scan must not substitute for current authority.
	who, authErr := a.tessera.account(r.Context(), token)
	if authErr != nil || !who.ServerAdministrator || !who.Installation.Administer {
		a.fail(w, r, sess, 403, "moderation_authority", nil)
		return
	}
	a.sessions.mu.Lock()
	current := a.sessions.byID[sess.ID] == sess && sess.Account == intent.Account && sess.Owner != nil
	a.sessions.mu.Unlock()
	if !current || time.Now().After(intent.Expires) {
		a.fail(w, r, sess, 409, "moderation_expired", nil)
		return
	}
	// No atomic upstream compare-and-execute exists. Out-of-band changes can
	// still race this call. Never retry it, even when its response is lost.
	result, mutationErr := a.tessera.unshare(r.Context(), token, intent.Target.Dataset, intent.Target.Type, false)
	a.forgetHomeToken(intent.Target.Dataset)
	// A fresh token is fetched even after an uncertain response. In alpha.33
	// client_credentials invalid_grant is the explicit per-store authorization
	// refusal, distinct from a failed metadata/record read or a service failure.
	// Only that refusal, a known final Home type from fresh metadata, and a
	// successful dry-run no longer reaching Home can
	// retire this known source. All other failures preserve it and report unknown.
	remaining, readErr := a.tessera.unshare(r.Context(), token, intent.Target.Dataset, intent.Target.Type, true)
	if readErr == nil {
		d.Current = &remaining
	}
	_, stateErr := a.homeToken(r.Context(), intent.Target.Dataset)
	var api *apiError
	cacheErr := error(nil)
	if lastHomeTrack && errors.As(stateErr, &api) && api.Status == 400 && api.Code == "invalid_grant" && readErr == nil && !containsString(remaining.Readers, a.cfg.HomeService) {
		cacheErr = a.homeReg.set(intent.Target.Dataset, false)
		stateErr = nil
	}
	switch {
	case mutationErr != nil:
		d.Message = "moderation_unknown"
	case !result.changes():
		d.Message = "moderation_noop"
		d.Impact = &result
	default:
		d.Message = "moderation_done"
		d.Impact = &result
	}
	if stateErr != nil || readErr != nil || cacheErr != nil || remaining.changes() {
		d.Message = "moderation_unknown"
	}
	a.render(w, r, sess, "moderation", 200, d)
}

type stoppedShare struct {
	Type       string `json:"type"`
	By         string `json:"stopped_by"`
	At         string `json:"stopped_at"`
	Name, Time string
}

func (t *Tessera) stoppedShares(ctx context.Context, token, dataset string) ([]stoppedShare, error) {
	var out struct {
		Stopped []stoppedShare `json:"stopped"`
	}
	err := t.call(ctx, "GET", "/admin/api/v1/datasets/"+url.PathEscape(dataset)+"/readers", token, "", nil, 200, &out)
	return out.Stopped, err
}
