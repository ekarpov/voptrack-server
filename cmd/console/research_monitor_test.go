package main

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func monitorTestApp(t *testing.T) (*app, *session, *httptest.Server) {
	a, s, old := savedResearchApp(t)
	old.Close()
	a.cfg.Analysis = "analysis"
	a.analysisCache = newServiceTokens("analysis", "")
	a.analysisCache.secret = "synthetic"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth/account":
			writeJSON(w, 200, map[string]any{"account": "researcher", "server_administrator": true})
		case r.URL.Path == "/oauth/token":
			writeJSON(w, 200, map[string]any{"access_token": "synthetic-reader", "expires_in": 300})
		case r.URL.Path == "/admin/api/v1/overview":
			writeJSON(w, 200, map[string]any{"accounts": []map[string]any{{"account": "analysis", "datasets": map[string]any{"one": nil, "two": nil}}, {"account": "p-one", "datasets": map[string]any{"one": nil}}, {"account": "p-two", "datasets": map[string]any{"two": nil}}}})
		case strings.HasSuffix(r.URL.Path, ":aggregate"):
			var q map[string]any
			must(t, json.NewDecoder(r.Body).Decode(&q))
			if _, ok := q["field"]; ok {
				t.Error("monitor requested an answer field")
			}
			n := "2"
			if strings.Contains(r.URL.Path, "/two/") {
				n = "0"
			}
			writeJSON(w, 200, map[string]any{"aggregates": []map[string]any{{"row_count": n}}})
		case strings.HasSuffix(r.URL.Path, ":query"):
			var body struct {
				Query struct {
					Fields []string `json:"fields"`
				} `json:"query"`
			}
			must(t, json.NewDecoder(r.Body).Decode(&body))
			if len(body.Query.Fields) != 1 || body.Query.Fields[0] != "timestamp" {
				t.Error("monitor fetched recorded values")
			}
			writeJSON(w, 200, map[string]any{"records": []any{map[string]any{"values": []any{map[string]any{"type": "date_time", "value": "2026-11-07T12:00:00Z"}}}}})
		case strings.HasSuffix(r.URL.Path, ":page"):
			var body struct {
				Query struct {
					Fields []string `json:"fields"`
				} `json:"query"`
			}
			must(t, json.NewDecoder(r.Body).Decode(&body))
			if len(body.Query.Fields) != 1 || body.Query.Fields[0] != "timestamp" {
				t.Error("trend fetched recorded values")
			}
			writeJSON(w, 200, map[string]any{"cursor": "dates"})
		case strings.HasSuffix(r.URL.Path, ":next"):
			writeJSON(w, 200, map[string]any{"meta": map[string]any{"fields": []map[string]string{{"id": "timestamp"}}}, "records": []any{map[string]any{"values": []any{map[string]any{"type": "date_time", "value": "2026-11-07T12:00:00Z"}}}, map[string]any{"values": []any{map[string]any{"type": "date_time", "value": "2026-11-07T13:00:00Z"}}}}})
		default:
			w.WriteHeader(503)
		}
	}))
	a.tessera = &Tessera{internal: server.URL, http: server.Client()}
	for _, page := range []string{"research_monitor", "research_exports", "studies"} {
		tpl, err := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix, "chartLabelVisible": chartLabelVisible, "chartLabelText": chartLabelText, "formErrorKey": formErrorKey}).ParseFS(assets, "templates/layout.html", "templates/chart.html", "templates/"+page+".html")
		must(t, err)
		a.pages[page] = tpl
	}
	first := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for _, p := range []string{"p-one", "p-two"} {
		must(t, a.studies.recordConsent("synthetic", ConsentRecord{Account: p, Version: 1, At: first}))
	}
	must(t, a.studies.recordConsent("synthetic", ConsentRecord{Account: "p-one", Version: 2, At: first.Add(time.Hour)}))
	return a, s, server
}
func TestResearchMonitorCountsOnlyDatesAndDeduplicatesPeople(t *testing.T) {
	a, s, server := monitorTestApp(t)
	defer server.Close()
	w := savedRequest(a, s, "GET", "/studies/synthetic/dashboard?period=all", nil)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `data-enrolled>2<`) || !strings.Contains(body, "2</strong><span>visible records") {
		t.Fatal("monitor counts or roster incorrect")
	}
	for _, raw := range []string{`data-value>`, `/panel?`, `/dashboards/`, `hours_slept`} {
		if strings.Contains(body, raw) {
			t.Fatal("monitor exposed answer UI", raw)
		}
	}
	for _, path := range []string{"/panel?scope=study:synthetic", "/dashboards/study:synthetic/old"} {
		if savedRequest(a, s, "GET", path, nil).Code != 404 {
			t.Fatal("old value page remains")
		}
	}
	if savedRequest(a, s, "POST", "/dashboards/add", url.Values{"csrf": {s.CSRF}}).Code != 404 {
		t.Fatal("saved analytical UI remains")
	}
	for _, path := range []string{"/studies/synthetic/dashboard", "/studies/synthetic/participants", "/studies/synthetic/exports"} {
		if code := savedRequest(a, nil, "GET", path, nil).Code; code != 303 {
			t.Fatal("anonymous monitoring access")
		}
	}
}
func TestResearchMonitorReadFailureIsNotZeroOrQuiet(t *testing.T) {
	a, s, server := monitorTestApp(t)
	defer server.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/account" {
			writeJSON(w, 200, map[string]any{"account": "researcher", "server_administrator": true})
			return
		}
		w.WriteHeader(503)
	}))
	defer backend.Close()
	a.tessera = &Tessera{internal: backend.URL, http: backend.Client()}
	w := savedRequest(a, s, "GET", "/studies/synthetic/dashboard", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Collection information unavailable") || strings.Contains(w.Body.String(), `data-enrolled>`) {
		t.Fatal("discovery failure became zero enrolment")
	}
	st, _ := a.studies.get("synthetic")
	if _, err := a.planExport(context.Background(), s, st); err == nil {
		t.Fatal("export silently omitted unavailable sources")
	}
}
func TestResearchMonitorIndependentFiltersAndActivity(t *testing.T) {
	a, s, server := monitorTestApp(t)
	defer server.Close()
	w := savedRequest(a, s, "GET", "/studies/synthetic/dashboard?period=all&activity=quiet", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "0</strong><span>visible records") || !strings.Contains(w.Body.String(), "No recorded activity to show") {
		t.Fatal("activity filter does not match trend/totals")
	}
	r := httptest.NewRequest("GET", "/studies/synthetic/participants?period=custom&from=2026-11-01&to=2026-11-07&status=state_active&collection=study.synthetic.sleep&q=p-one&offset=20", nil)
	win, _ := requestedWindow(r.URL.Query(), time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC))
	types, _ := studyTracks(mustStudy(t, a, "synthetic").Tracks)
	c := monitorSelection(r, win, types)
	if c.SearchHidden.Get("from") != "2026-11-01" || c.SearchHidden.Get("status") != "state_active" || c.FilterHidden.Get("q") != "p-one" || c.SearchHidden.Has("offset") {
		t.Fatal("independent search lost context")
	}
	if strings.Contains(c.ResetURL, "status=") || !strings.Contains(c.ResetURL, "q=p-one") || strings.Contains(c.SearchClear, "q=") {
		t.Fatal("reset/clear changed the wrong selection")
	}
}
func mustStudy(t *testing.T, a *app, slug string) Study {
	t.Helper()
	s, ok := a.studies.get(slug)
	if !ok {
		t.Fatal("study missing")
	}
	return s
}
func TestResearchMonitorReviewExport(t *testing.T) {
	dir := os.Getenv("VOPTRACK_RESEARCH_REVIEW_DIR")
	if dir == "" {
		t.Skip("optional synthetic export")
	}
	must(t, os.MkdirAll(filepath.Join(dir, "static"), 0700))
	a, s, server := monitorTestApp(t)
	defer server.Close()
	for _, page := range []struct{ name, path string }{{"overview", "/studies/synthetic/dashboard?period=all"}, {"participants", "/studies/synthetic/participants?period=all"}, {"exports", "/studies/synthetic/exports"}, {"studies", "/studies"}} {
		w := savedRequest(a, s, "GET", page.path, nil)
		if w.Code != 200 {
			t.Fatal("render failed")
		}
		must(t, os.WriteFile(filepath.Join(dir, page.name+".html"), []byte(strings.ReplaceAll(w.Body.String(), `="/static/`, `="static/`)), 0600))
	}
	for _, name := range []string{"console.css", "brand-mark-light.svg", "brand-mark-dark.svg", "favicon.svg"} {
		raw, err := assets.ReadFile("static/" + name)
		must(t, err)
		must(t, os.WriteFile(filepath.Join(dir, "static", name), raw, 0600))
	}
}

func TestResearchMonitorWithdrawalSurvivesErasedStore(t *testing.T) {
	a, s, server := monitorTestApp(t)
	defer server.Close()
	must(t, a.studies.recordWithdrawal("synthetic", Withdrawal{Account: "p-one", At: time.Now().Add(-time.Minute)}))
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/api/v1/overview" {
			writeJSON(w, 200, map[string]any{"accounts": []map[string]any{{"account": "analysis", "datasets": map[string]any{}}, {"account": "p-one", "datasets": map[string]any{}}}})
			return
		}
		w.WriteHeader(503)
	}))
	defer backend.Close()
	a.tessera = &Tessera{internal: backend.URL, http: backend.Client()}
	people, _, err := a.monitorRoster(context.Background(), s, mustStudy(t, a, "synthetic"))
	must(t, err)
	for _, p := range people {
		if p.Account == "p-one" {
			if p.State != "state_withdrawn" || p.StateDetail == "" || p.CanSuspend || p.CanReinstate {
				t.Fatal("known withdrawal was replaced by missing-store status")
			}
			return
		}
	}
	t.Fatal("withdrawn participant missing")
}

func TestResearchMonitorAppErasureWithoutStore(t *testing.T) {
	a, sess, server := monitorTestApp(t)
	defer server.Close()
	erased := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/api/v1/overview" {
			writeJSON(w, 200, map[string]any{"accounts": []map[string]any{{"account": "analysis", "datasets": map[string]any{}}, {"account": "p-one", "datasets": map[string]any{}}, {"account": "p-two", "datasets": map[string]any{}}}})
			return
		}
		w.WriteHeader(503)
	}))
	defer erased.Close()
	a.tessera = &Tessera{internal: erased.URL, http: erased.Client()}
	people, sources, err := a.monitorRoster(context.Background(), sess, mustStudy(t, a, "synthetic"))
	must(t, err)
	if len(people) != 2 || len(sources) != 0 {
		t.Fatal("erased roster incorrect")
	}
	for _, p := range people {
		if p.State != "state_withdrawn" || p.StateDetail != "" || p.CanSuspend || p.CanReinstate {
			t.Fatal("app erasure must be withdrawn without an invented date")
		}
	}
}

func TestResearchStudyNavigationKeepsSelection(t *testing.T) {
	a, _, server := monitorTestApp(t)
	defer server.Close()
	r := httptest.NewRequest("GET", "/studies/synthetic/dashboard?period=all&status=state_active&q=review&offset=20&token=discard", nil)
	c := a.researchStudyContext(r)
	if c == nil || c.Query != "?period=all&q=review&status=state_active" {
		t.Fatal("study navigation lost selection or carried unrelated input")
	}
}
func TestResearchWebsiteOwnerLandsOnStudies(t *testing.T) {
	sessions := newSessions()
	sess := sessions.get(httptest.NewRecorder(), httptest.NewRequest("GET", "https://research.invalid/login", nil), true)
	who := Account{Account: "synthetic", ServerAdministrator: true}
	who.Installation.Administer = true
	a := &app{cfg: config{Variant: "research"}, sessions: sessions, tessera: &Tessera{}, docs: &stateDocs{}}
	w := httptest.NewRecorder()
	a.websiteApproved(w, httptest.NewRequest("GET", "https://research.invalid/auth/callback", nil), sess, pending{}, tokens{ConsoleSession: &consoleSession{}}, Listing{}, who)
	if w.Code != 303 || w.Header().Get("Location") != "/studies" {
		t.Fatal("research owner did not land on Studies")
	}
}
