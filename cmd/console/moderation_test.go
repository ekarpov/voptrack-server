package main

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type moderationFixture struct {
	relatedTypes                                             bool
	mu                                                       sync.Mutex
	owner, approved, shared                                  bool
	failDiscovery, changed, lost, empty, failMutation, whole bool
	tokenFailure, postReadFailure                            bool
	mutations, previews, privateReads                        int
}

func moderationHTTPApp(t *testing.T) (*app, *session, *moderationFixture) {
	a, s, old := policyHTTPApp(t)
	old.Close()
	a.cfg.Variant = "household"
	a.cfg.HomeService = "home"
	a.cfg.IssuerID = "members"
	s.Account = "owner"
	a.homeReg = &homeRegistry{docs: a.studies.docs}
	must(t, a.homeReg.set("one", true))
	must(t, a.homeReg.set("two", true))
	a.homeCache = newServiceTokens("home", "")
	a.homeCache.secret = "synthetic"
	f := &moderationFixture{owner: true, approved: true, shared: true}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == "/oauth/account":
			writeJSON(w, 200, map[string]any{"account": "owner", "server_administrator": f.owner, "installation": map[string]bool{"administer": f.approved}})
		case r.URL.Path == "/admin/api/v1/overview":
			if f.failDiscovery {
				w.WriteHeader(503)
				return
			}
			grants := map[string]any{"two": map[string]bool{"read": true}}
			if f.shared {
				grants["one"] = map[string]bool{"read": true}
			}
			writeJSON(w, 200, map[string]any{"accounts": []any{map[string]any{"account": "home", "kind": "service", "datasets": grants}}})
		case r.URL.Path == "/oauth/token":
			if f.mutations > 0 && f.tokenFailure {
				w.WriteHeader(503)
				return
			}
			if !f.shared {
				writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
				return
			}
			writeJSON(w, 200, map[string]any{"access_token": "home-only", "expires_in": 300})
		case strings.HasSuffix(r.URL.Path, "/meta"):
			if f.failDiscovery {
				w.WriteHeader(503)
				return
			}
			if r.Header.Get("Authorization") != "Bearer home-only" {
				f.privateReads++
				w.WriteHeader(403)
				return
			}
			metas := []Meta{{ID: "track", Name: "<Synthetic track>"}}
			if f.relatedTypes {
				parent := "track"
				metas = append(metas, Meta{ID: "child", Name: "Child track", ParentID: &parent})
			}
			writeJSON(w, 200, map[string]any{"types": metas})
		case r.URL.Path == "/admin/api/v1/unshare":
			if r.Header.Get("Authorization") != "Bearer synthetic" {
				t.Error("moderation used service authority")
			}
			var q struct {
				Datasets, Types []string
				Dry             bool `json:"dry_run"`
			}
			json.NewDecoder(r.Body).Decode(&q)
			if len(q.Datasets) != 1 || q.Datasets[0] == "" || len(q.Types) != 1 || (q.Types[0] != "track" && !(f.relatedTypes && q.Types[0] == "child")) {
				t.Error("broadened selector")
				w.WriteHeader(400)
				return
			}
			if q.Dry {
				if f.mutations > 0 && f.postReadFailure {
					w.WriteHeader(503)
					return
				}
				f.previews++
			} else {
				f.mutations++
				if f.failMutation {
					w.WriteHeader(503)
					return
				}
			}
			impact := shareImpact{Dataset: q.Datasets[0], Groups: []string{"members"}, Readers: []string{"home", "named-outside"}}
			if f.changed {
				impact.Readers = append(impact.Readers, "new-reader")
			}
			if f.whole {
				impact.Whole = []string{"whole-outside"}
			}
			if !f.shared || (!q.Dry && f.empty) {
				impact.Groups = nil
				impact.Readers = nil
			}
			if !q.Dry {
				f.shared = false
			}
			if !q.Dry && f.lost {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
				} else {
					conn.Close()
				}
				return
			}
			var list []shareImpact
			if impact.changes() || len(impact.Whole) > 0 {
				list = append(list, impact)
			}
			writeJSON(w, 200, map[string]any{"datasets": list, "dry_run": q.Dry})
		default:
			f.privateReads++
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)
	a.tessera = &Tessera{internal: srv.URL, http: srv.Client()}
	for _, page := range []string{"moderation", "sharing"} {
		tpl, err := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix, "localizeName": localizeName}).ParseFS(assets, "templates/layout.html", "templates/"+page+".html")
		must(t, err)
		a.pages[page] = tpl
	}
	return a, s, f
}
func moderationReview(t *testing.T, a *app, s *session) url.Values {
	t.Helper()
	w := legacyModerationRequest(a, s, "POST", "/home/moderation/preview", url.Values{"csrf": {s.CSRF}, "dataset": {"one"}, "track": {"track"}})
	assertSetupStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "named-outside") || !strings.Contains(w.Body.String(), "&lt;Synthetic track&gt;") {
		t.Fatal("missing/unsafe preview")
	}
	return url.Values{"csrf": {s.CSRF}, "confirmation": {s.Moderation.Nonce}}
}
func TestModerationSingleUseConcurrentAndReshare(t *testing.T) {
	a, s, f := moderationHTTPApp(t)
	w := legacyModerationRequest(a, s, "GET", "/home/moderation", nil)
	assertSetupStatus(t, w, 200)
	if s.Moderation != nil || f.mutations != 0 {
		t.Fatal("GET made intent or changed shares")
	}
	form := moderationReview(t, a, s)
	if f.mutations != 0 {
		t.Fatal("dry-run mutated")
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- legacyModerationRequest(a, s, "POST", "/home/moderation/confirm", form).Code
		}()
	}
	wg.Wait()
	close(codes)
	got := map[int]int{}
	for c := range codes {
		got[c]++
	}
	if got[200] != 1 || got[409] != 1 || f.mutations != 1 || f.privateReads != 0 {
		t.Fatalf("duplicate execution: %v, %d", got, f.mutations)
	}
	if ids := a.homeReg.load(); len(ids) != 1 || ids[0] != "two" {
		t.Fatal("final share source not reconciled", ids)
	}
	f.shared = true
	must(t, a.homeReg.set("one", true))
	assertSetupStatus(t, legacyModerationRequest(a, s, "POST", "/home/moderation/confirm", form), 409)
	if f.mutations != 1 {
		t.Fatal("replay stopped later re-share")
	}
}
func TestModerationAuthorityAndSelectors(t *testing.T) {
	for _, mode := range []string{"signed-out", "member", "revoked", "unapproved", "research", "standard", "csrf", "empty", "duplicate-store", "duplicate-track", "forged", "query", "extra"} {
		t.Run(mode, func(t *testing.T) {
			a, s, f := moderationHTTPApp(t)
			form := url.Values{"csrf": {s.CSRF}, "dataset": {"one"}, "track": {"track"}}
			path := "/home/moderation/preview"
			switch mode {
			case "signed-out":
				s = nil
			case "member":
				s.Owner = nil
			case "revoked":
				f.owner = false
			case "unapproved":
				f.approved = false
			case "research", "standard":
				a.cfg.Variant = mode
			case "csrf":
				form.Set("csrf", "forged")
			case "empty":
				form.Set("dataset", "")
			case "duplicate-store":
				form.Add("dataset", "two")
			case "duplicate-track":
				form.Add("track", "other")
			case "forged":
				form.Set("dataset", "unshared")
			case "query":
				path += "?dataset=two"
			case "extra":
				form.Add("readers", "forged")
			}
			w := legacyModerationRequest(a, s, "POST", path, form)
			if w.Code < 400 || f.mutations != 0 {
				t.Fatalf("accepted %s: %d", mode, w.Code)
			}
		})
	}
}
func TestModerationFreshnessFailuresAndResult(t *testing.T) {
	for _, mode := range []string{"expired", "other-session", "changed", "gone", "discovery", "revoked", "unapproved", "empty", "error", "lost", "whole"} {
		t.Run(mode, func(t *testing.T) {
			a, s, f := moderationHTTPApp(t)
			if mode == "whole" {
				f.whole = true
			}
			form := moderationReview(t, a, s)
			switch mode {
			case "expired":
				s.Moderation.Expires = time.Now().Add(-time.Minute)
			case "other-session":
				copy := *s
				copy.ID = "other"
				copy.Moderation = nil
				a.sessions.byID[copy.ID] = &copy
				s = &copy
			case "changed":
				f.changed = true
			case "gone":
				f.shared = false
			case "discovery":
				f.failDiscovery = true
			case "revoked":
				f.owner = false
			case "unapproved":
				f.approved = false
			case "empty":
				f.empty = true
			case "error":
				f.failMutation = true
			case "lost":
				f.lost = true
			}
			w := legacyModerationRequest(a, s, "POST", "/home/moderation/confirm", form)
			mutation := mode == "empty" || mode == "error" || mode == "lost" || mode == "whole"
			if !mutation {
				if w.Code < 400 || f.mutations != 0 {
					t.Fatal("stale/failed preview executed", w.Code)
				}
				return
			}
			assertSetupStatus(t, w, 200)
			want := "moderation_done"
			if mode == "empty" {
				want = "moderation_noop"
			} else if mode != "whole" {
				want = "moderation_unknown"
			}
			if !strings.Contains(w.Body.String(), `data-moderation-state="`+want+`"`) || f.mutations != 1 {
				t.Fatalf("incorrect result %s", w.Body.String())
			}
			legacyModerationRequest(a, s, "POST", "/home/moderation/confirm", form)
			if f.mutations != 1 {
				t.Fatal("failure retried mutation")
			}
			if mode == "whole" && !strings.Contains(w.Body.String(), "whole-outside") {
				t.Fatal("whole-store exception missing")
			}
		})
	}
}

func TestHomeAggregationUsesSuccessfulSharedMetadata(t *testing.T) {
	for _, mode := range []string{"absent", "failure"} {
		t.Run(mode, func(t *testing.T) {
			src := syntheticSource("one", "synthetic")
			src.Home = true
			queries := 0
			spec := Spec{Type: "track", Field: "value", Measure: MeasureSum, Group: "none", TZ: time.UTC}
			res, err := computeFixture(t, []Source{src}, spec, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/meta") {
					if mode == "failure" {
						w.WriteHeader(503)
					} else {
						writeJSON(w, 200, map[string]any{"types": []Meta{{ID: "other-track"}}})
					}
					return
				}
				queries++
				w.WriteHeader(403)
			})
			must(t, err)
			if queries != 0 {
				t.Fatal("queried a type without current Home metadata")
			}
			if (len(res.Skipped) > 0) != (mode == "failure") {
				t.Fatal("confused missing share and failed discovery")
			}
		})
	}
}

func TestModerationConfirmationForgeryAndMetadataFailures(t *testing.T) {
	for _, mode := range []string{"missing", "duplicate", "target", "query", "nonce", "token-failure", "post-read-failure", "registry-write"} {
		t.Run(mode, func(t *testing.T) {
			a, s, f := moderationHTTPApp(t)
			form := moderationReview(t, a, s)
			path := "/home/moderation/confirm"
			switch mode {
			case "missing":
				form.Del("confirmation")
			case "duplicate":
				form.Add("confirmation", form.Get("confirmation"))
			case "target":
				form.Set("dataset", "two")
			case "query":
				path += "?track=other"
			case "nonce":
				form.Set("confirmation", "forged")
			case "token-failure":
				f.tokenFailure = true
			case "post-read-failure":
				f.postReadFailure = true
			case "registry-write":
				a.homeReg.docs = failedPolicyDocs{a.homeReg.docs}
			}
			w := legacyModerationRequest(a, s, "POST", path, form)
			if mode == "token-failure" || mode == "post-read-failure" || mode == "registry-write" {
				assertSetupStatus(t, w, 200)
				if !strings.Contains(w.Body.String(), `data-moderation-state="moderation_unknown"`) || len(a.homeReg.load()) != 2 {
					t.Fatal("failure removed source or claimed success", w.Body.String())
				}
			} else if w.Code < 400 || f.mutations != 0 {
				t.Fatal("forged confirmation executed")
			}
		})
	}
	a, s, f := moderationHTTPApp(t)
	must(t, a.homeReg.docs.put(homeSources, []byte(`not-json`)))
	w := legacyModerationRequest(a, s, "GET", "/home/moderation", nil)
	assertSetupStatus(t, w, 503)
	if strings.Contains(w.Body.String(), "No eligible") || f.mutations != 0 {
		t.Fatal("failed discovery presented as empty")
	}
}

func TestMemberNoticeAndSharingFailureUsesOwnCredential(t *testing.T) {
	a, s, old := policyHTTPApp(t)
	old.Close()
	a.cfg.Variant = "household"
	a.cfg.HomeService = "home"
	a.cfg.IssuerID = "members"
	s.Owner = nil
	s.Account = "member"
	s.Connections = []*connection{{Dataset: Dataset{ID: "one"}, Administer: true, tokens: tokens{Access: "member-only", Expires: time.Now().Add(time.Hour)}}}
	mutations := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer member-only" {
			t.Error("notice used another credential")
		}
		switch {
		case r.Method == "PUT":
			mutations++
			w.WriteHeader(503)
		case r.URL.Path == "/admin/api/v1/overview":
			writeJSON(w, 200, map[string]any{"datasets": []Dataset{{ID: "one"}}})
		case strings.HasSuffix(r.URL.Path, "/meta"):
			writeJSON(w, 200, map[string]any{"types": []Meta{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}})
		case strings.HasSuffix(r.URL.Path, "/readers/issuer:members"):
			writeJSON(w, 200, map[string]any{"types": []string{"a"}})
		case strings.HasSuffix(r.URL.Path, "/readers"):
			writeJSON(w, 200, map[string]any{"stopped": []stoppedShare{{Type: "b", By: "<owner>", At: "2026-10-15T10:30:00Z"}}})
		default:
			w.WriteHeader(503)
		}
	}))
	defer srv.Close()
	a.tessera = &Tessera{internal: srv.URL, http: srv.Client()}
	tpl, err := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix, "localizeName": localizeName}).ParseFS(assets, "templates/layout.html", "templates/sharing.html")
	must(t, err)
	a.pages["sharing"] = tpl
	w := legacyModerationRequest(a, s, "GET", "/sharing", nil)
	assertSetupStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "&lt;owner&gt;") || !strings.Contains(w.Body.String(), "2026-10-15 10:30 UTC") || mutations != 0 {
		t.Fatal("notice unsafe, not current, or view wrote")
	}
	w = legacyModerationRequest(a, s, "POST", "/sharing", url.Values{"csrf": {s.CSRF}, "dataset": {"one"}, "track": {"b"}})
	assertSetupStatus(t, w, 502)
	if !strings.Contains(w.Body.String(), `value="b" checked`) || strings.Contains(w.Body.String(), `value="a" checked`) || mutations != 1 || !strings.Contains(w.Body.String(), "selection is retained") {
		t.Fatal("failed sharing lost choice or retried")
	}
}

func TestHomeRegistryFailedReadPreservesOtherStores(t *testing.T) {
	a, _, _ := moderationHTTPApp(t)
	docs := a.homeReg.docs
	fault := &setupFaultDocs{docStore: docs}
	fault.reads.Store(true)
	a.homeReg.docs = fault
	if err := a.homeReg.set("one", false); err == nil {
		t.Fatal("failed read replaced registry")
	}
	fault.reads.Store(false)
	if ids := a.homeReg.load(); len(ids) != 2 {
		t.Fatal("failed read lost other store", ids)
	}
}

func TestModerationUnregisteredAppShareIsNotAnAuthorizedSelector(t *testing.T) {
	a, s, f := moderationHTTPApp(t)
	// The server still shares "one". Removing only its local registration models
	// a share created solely by an app, outside the supported Console save flow.
	must(t, a.homeReg.set("one", false))
	w := legacyModerationRequest(a, s, "GET", "/home/moderation", nil)
	assertSetupStatus(t, w, 200)
	if strings.Contains(w.Body.String(), `name="dataset" value="one"`) || !strings.Contains(w.Body.String(), `name="dataset" value="two"`) {
		t.Fatal("invented registration or lost known source")
	}
	w = legacyModerationRequest(a, s, "POST", "/home/moderation/preview", url.Values{"csrf": {s.CSRF}, "dataset": {"one"}, "track": {"track"}})
	assertSetupStatus(t, w, 409)
	if f.mutations != 0 {
		t.Fatal("unregistered app share bypassed selection boundary")
	}
}

// Inheritance does not make a type abstract: both types can be independently
// shared. Discovery and preview must retain each exact moderation target.
func TestModerationSharedParentAndChildSelectable(t *testing.T) {
	a, s, f := moderationHTTPApp(t)
	f.relatedTypes = true
	w := legacyModerationRequest(a, s, "GET", "/home/moderation", nil)
	assertSetupStatus(t, w, 200)
	for _, track := range []string{"track", "child"} {
		if !strings.Contains(w.Body.String(), `name="track" value="`+track+`"`) {
			t.Fatalf("shared type %q missing from moderation listing", track)
		}
		preview := legacyModerationRequest(a, s, "POST", "/home/moderation/preview", url.Values{"csrf": {s.CSRF}, "dataset": {"one"}, "track": {track}})
		assertSetupStatus(t, preview, 200)
		if s.Moderation == nil || s.Moderation.Target.Dataset != "one" || s.Moderation.Target.Type != track || s.Moderation.Target.LastHomeTrack {
			t.Fatalf("preview did not retain exact shared type %q", track)
		}
	}
	if f.mutations != 0 || f.privateReads != 0 {
		t.Fatal("selection or preview mutated shares or read private data")
	}
}

// Historical handler security checks remain useful while persisted notices and
// member sharing use the same helpers. These endpoints have no production route.
func legacyModerationRequest(a *app, sess *session, method, path string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if sess != nil {
		r.AddCookie(&http.Cookie{Name: a.sessions.cookieName(), Value: sess.ID})
	}
	w := httptest.NewRecorder()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /home/moderation", a.moderationPage)
	mux.HandleFunc("POST /home/moderation/preview", a.moderationPreview)
	mux.HandleFunc("POST /home/moderation/confirm", a.moderationConfirm)
	mux.Handle("/", a.routes())
	mux.ServeHTTP(w, r)
	return w
}
