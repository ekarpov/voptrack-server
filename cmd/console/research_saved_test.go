package main

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func savedResearchApp(t *testing.T) (*app, *session, *httptest.Server) {
	a, sess, srv := policyHTTPApp(t)
	a.saved = &savedStore{docs: a.studies.docs}
	for _, page := range []string{"dashboard", "save_study_panel"} {
		tpl, err := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix, "localizePanelTitle": localizePanelTitle, "localizeName": localizeName, "chartLabelVisible": chartLabelVisible, "chartLabelText": chartLabelText}).ParseFS(assets, "templates/layout.html", "templates/chart.html", "templates/"+page+".html")
		must(t, err)
		a.pages[page] = tpl
	}
	st, _ := a.studies.get("synthetic")
	st.Tracks = []TrackDesign{{TypeID: "study.synthetic.sleep", Name: "Sleep", Fields: []TrackField{{ID: "hours", Name: "Hours", Kind: KindNumber, Meaning: "sample"}}}}
	raw, _ := json.Marshal(st)
	must(t, a.studies.docs.put("studies/synthetic.json", raw))
	return a, sess, srv
}
func savedRequest(a *app, sess *session, method, path string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if sess != nil {
		r.AddCookie(&http.Cookie{Name: a.sessions.cookieName(), Value: sess.ID})
	}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	return w
}

// Historical analytical definitions and suppression retain engine-level tests.
// These handlers are deliberately not exposed by the collection-first router.
func savedEngineRequest(a *app, sess *session, method, path string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if sess != nil {
		r.AddCookie(&http.Cookie{Name: a.sessions.cookieName(), Value: sess.ID})
	}
	w := httptest.NewRecorder()
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.URL.Path == "/panel" {
		a.analyticalPanel(w, r)
		return w
	}
	if len(parts) >= 3 && parts[0] == "studies" && parts[2] == "dashboard" {
		if s, ok := a.researcher(w, r); ok {
			a.researchOverview(w, r, s, parts[1])
		}
		return w
	}
	if r.URL.Path == "/dashboards/add" {
		a.addToDashboard(w, r)
		return w
	}
	if len(parts) >= 3 && parts[0] == "dashboards" {
		r.SetPathValue("scope", parts[1])
		r.SetPathValue("id", parts[2])
		if len(parts) == 4 && parts[3] == "remove" {
			a.removeFromDashboard(w, r)
		} else {
			a.dashboard(w, r)
		}
		return w
	}
	return savedRequest(a, sess, method, path, form)
}

const savedQuestion = "scope=study:synthetic&type=study.synthetic.sleep&field=hours&measure=sum&group=period&period=day&date=timestamp&from=2026-03-28&to=2026-03-29&tz=Europe%2FHelsinki"

func TestSavedResearchLifecycleAndWindow(t *testing.T) {
	a, s, srv := savedResearchApp(t)
	defer srv.Close()
	form := url.Values{"csrf": {s.CSRF}, "panel": {savedQuestion}, "name": {"  My study  "}}
	w := savedEngineRequest(a, s, "POST", "/dashboards/add", form)
	if w.Code != 303 {
		t.Fatalf("save %d %s", w.Code, w.Body.String())
	}
	path := w.Header().Get("Location")
	if !strings.HasPrefix(path, "/dashboards/study:synthetic/") {
		t.Fatal(path)
	}
	if w = savedEngineRequest(a, s, "POST", "/dashboards/add", form); w.Code != 303 || w.Header().Get("Location") != path {
		t.Fatal("duplicate save")
	}
	list, err := a.saved.load("study-synthetic")
	must(t, err)
	if len(list) != 1 || len(list[0].Panels) != 1 || list[0].Name != "My study" {
		t.Fatal(list)
	}
	q, _ := url.ParseQuery(list[0].Panels[0])
	if q.Get("to") != "2026-03-29" || q.Get("tz") != "Europe/Helsinki" {
		t.Fatal(q)
	}
	st, _ := a.studies.get("synthetic")
	cat, err := studyCatalog(st)
	must(t, err)
	spec, _, err := a.studyPanel(list[0].Panels[0], "study:synthetic", cat)
	must(t, err)
	if spec.To.Sub(spec.From) != 47*time.Hour {
		t.Fatal("DST window changed", spec)
	}
	for i := 0; i < 4; i++ {
		u, _ := url.Parse(panelLink("study:synthetic", spec))
		spec, _, err = a.parseSpecValues(u.Query(), cat)
		must(t, err)
	}
	if spec.To.Sub(spec.From) != 47*time.Hour {
		t.Fatal("To gained days")
	}
	w = savedEngineRequest(a, s, "GET", path, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `data-release="unconfigured"`) || !strings.Contains(w.Body.String(), translator("en")("dashboard_remove_final")) {
		t.Fatal("missing policy hides definitions or final removal warning", w.Code, w.Body.String())
	}
	if w = savedEngineRequest(a, s, "GET", "/studies/synthetic/dashboard", nil); !strings.Contains(w.Body.String(), "My study") {
		t.Fatal("not discoverable")
	}
	form.Set("panel", strings.Replace(savedQuestion, "group=period", "group=none", 1))
	if w = savedEngineRequest(a, s, "POST", "/dashboards/add", form); w.Code != 303 {
		t.Fatal("append", w.Code)
	}
	list, err = a.saved.load("study-synthetic")
	must(t, err)
	token := dashboardToken(list[0])
	remove := url.Values{"csrf": {s.CSRF}, "version": {token}, "index": {"0"}}
	if w = savedEngineRequest(a, s, "POST", path+"/remove", remove); w.Code != 303 {
		t.Fatal("remove", w.Code)
	}
	if w = savedEngineRequest(a, s, "POST", path+"/remove", remove); w.Code != 409 {
		t.Fatal("stale/repeated removed shifted panel", w.Code)
	}
	list, err = a.saved.load("study-synthetic")
	must(t, err)
	if len(list[0].Panels) != 1 || !strings.Contains(list[0].Panels[0], "group=none") {
		t.Fatal("wrong panel removed")
	}
	remove.Set("version", dashboardToken(list[0]))
	if w = savedEngineRequest(a, s, "POST", path+"/remove", remove); w.Code != 303 || w.Header().Get("Location") != "/studies/synthetic/dashboard" {
		t.Fatal("final removal")
	}
	if w = savedEngineRequest(a, s, "GET", path, nil); w.Code != 404 {
		t.Fatal("removed dashboard remains")
	}
}

func TestSavedResearchScopeAndPermissions(t *testing.T) {
	a, s, srv := savedResearchApp(t)
	defer srv.Close()
	form := url.Values{"csrf": {s.CSRF}, "panel": {savedQuestion}, "name": {"Study"}}
	w := savedEngineRequest(a, s, "POST", "/dashboards/add", form)
	path := w.Header().Get("Location")
	for _, scope := range []string{"study:", "study:../synthetic", "study:Synthetic", "study:unknown", "elsewhere"} {
		form.Set("panel", strings.Replace(savedQuestion, "study:synthetic", scope, 1))
		if w := savedEngineRequest(a, s, "POST", "/dashboards/add", form); w.Code == 303 {
			t.Fatal("forged scope saved", scope)
		}
	}
	form.Set("panel", savedQuestion)
	for _, raw := range []string{savedQuestion + "&scope=mine", strings.Replace(savedQuestion, "study.synthetic.sleep", "study.other.sleep", 1), strings.Replace(savedQuestion, "hours", "removed", 1), strings.Replace(savedQuestion, "group=period", "group=field:secret", 1), strings.Replace(savedQuestion, "date=timestamp", "date=removed", 1)} {
		form.Set("panel", raw)
		if w := savedEngineRequest(a, s, "POST", "/dashboards/add", form); w.Code == 303 {
			t.Fatal("forged panel accepted", raw)
		}
	}
	form.Set("panel", savedQuestion)
	for _, mode := range []string{"signed-out", "participant", "revoked", "home"} {
		oldOwner, oldVariant := s.Owner, a.cfg.Variant
		viewer := s
		if mode == "signed-out" {
			viewer = nil
		}
		if mode == "participant" || mode == "revoked" {
			s.Owner = nil
			s.Connections = []*connection{{}}
		}
		if mode == "home" {
			a.cfg.Variant = "home"
		}
		for _, action := range []struct{ method, path string }{{"GET", path}, {"GET", "/studies/synthetic/dashboard"}, {"POST", "/dashboards/add"}, {"POST", path + "/remove"}} {
			w := savedEngineRequest(a, viewer, action.method, action.path, form)
			if w.Code == 200 || (action.method == "POST" && w.Code == 303 && strings.HasPrefix(w.Header().Get("Location"), "/dashboards/")) {
				t.Fatal("authorization bypass", mode, action, w.Code)
			}
		}
		s.Owner = oldOwner
		a.cfg.Variant = oldVariant
	}
	for _, path := range []string{"/dashboards/add", path + "/remove"} {
		form.Set("csrf", "forged")
		if w := savedEngineRequest(a, s, "POST", path, form); w.Code != 403 {
			t.Fatal("CSRF", path, w.Code)
		}
	}
	list, err := a.saved.load("study-synthetic")
	must(t, err)
	if len(list) != 1 || len(list[0].Panels) != 1 {
		t.Fatal("unauthorized mutation")
	}
	if list, _ := a.saved.load("home"); len(list) > 0 {
		t.Fatal("study in Home")
	}
	// A corrupt stored scope cannot redirect computation or generated links.
	raw, _ := json.Marshal([]Dashboard{{ID: "forged", Name: "Forged", Panels: []string{strings.Replace(savedQuestion, "study:synthetic", "study:other", 1)}}})
	must(t, a.saved.docs.put("dashboards/study-synthetic.json", raw))
	if w := savedEngineRequest(a, s, "GET", "/dashboards/study:synthetic/forged", nil); strings.Contains(w.Body.String(), "scope=study%3Aother") || strings.Contains(w.Body.String(), "data-value>") {
		t.Fatal("stored query escaped study")
	}
}

type dashboardFaultDocs struct {
	docStore
	readFail, writeFail bool
}

func (d dashboardFaultDocs) get(name string) ([]byte, bool, error) {
	if d.readFail {
		return nil, false, errors.New("synthetic read failure")
	}
	return d.docStore.get(name)
}
func (d dashboardFaultDocs) put(name string, raw []byte) error {
	if d.writeFail {
		return errors.New("synthetic write failure")
	}
	return d.docStore.put(name, raw)
}
func TestSavedResearchFailuresAndRestoration(t *testing.T) {
	a, s, srv := savedResearchApp(t)
	defer srv.Close()
	form := url.Values{"csrf": {s.CSRF}, "panel": {savedQuestion}, "name": {"Research <retained>"}}
	a.saved.docs = dashboardFaultDocs{docStore: a.studies.docs, writeFail: true}
	w := savedEngineRequest(a, s, "POST", "/dashboards/add", form)
	if w.Code != 502 || !strings.Contains(w.Body.String(), "Research &lt;retained&gt;") || !strings.Contains(w.Body.String(), "to%3D2026") && !strings.Contains(w.Body.String(), "to=2026-03-29") {
		t.Fatal("failed write lost input", w.Body.String())
	}
	a.saved.docs = a.studies.docs
	form.Set("panel", strings.Replace(savedQuestion, "field=hours", "field=missing", 1))
	w = savedEngineRequest(a, s, "POST", "/dashboards/add", form)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "Research &lt;retained&gt;") || !strings.Contains(w.Body.String(), "field=missing") {
		t.Fatal("invalid panel selection lost input")
	}
	form.Set("panel", savedQuestion)
	form.Set("name", strings.Repeat("x", 81))
	w = savedEngineRequest(a, s, "POST", "/dashboards/add", form)
	if w.Code != 422 || !strings.Contains(w.Body.String(), form.Get("name")) || !strings.Contains(w.Body.String(), "hours") {
		t.Fatal("invalid name lost selections")
	}
	form.Set("name", "Restored")
	w = savedEngineRequest(a, s, "POST", "/dashboards/add", form)
	path := w.Header().Get("Location")
	list, err := a.saved.load("study-synthetic")
	must(t, err)
	a.saved.docs = dashboardFaultDocs{docStore: a.studies.docs, writeFail: true}
	w = savedEngineRequest(a, s, "POST", path+"/remove", url.Values{"csrf": {s.CSRF}, "version": {dashboardToken(list[0])}, "index": {"0"}})
	if w.Code != 502 {
		t.Fatal("failed removal write looks successful")
	}
	a.saved.docs = a.studies.docs
	after, err := a.saved.load("study-synthetic")
	must(t, err)
	if len(after) != 1 || len(after[0].Panels) != 1 {
		t.Fatal("failed removal changed definitions")
	}
	// Existing document namespace survives a fresh file-backed store (restart).
	a.saved = &savedStore{docs: a.studies.docs}
	if w = savedEngineRequest(a, s, "GET", path, nil); w.Code != 200 {
		t.Fatal("restart")
	}
	raw, ok, err := a.saved.docs.get("dashboards/study-synthetic.json")
	must(t, err)
	if !ok {
		t.Fatal("no definitions")
	}
	if strings.Contains(string(raw), `"k"`) || strings.Contains(string(raw), "synthetic-csrf") || strings.Contains(string(raw), "Result") {
		t.Fatal("not definitions only")
	}
	// Restore the backed-up document through existing persistence, without a
	// special study-dashboard storage mechanism.
	restored := fileDocs{dir: t.TempDir()}
	must(t, restored.put("dashboards/study-synthetic.json", raw))
	a.saved = &savedStore{docs: restored}
	if w = savedEngineRequest(a, s, "GET", path, nil); w.Code != 200 {
		t.Fatal("restored definitions")
	}
	a.saved.docs = dashboardFaultDocs{docStore: restored, readFail: true}
	if w = savedEngineRequest(a, s, "GET", path, nil); w.Code != 502 || !strings.Contains(w.Body.String(), translator("en")("dashboard_load_failed")) {
		t.Fatal("failed load looks missing")
	}
	a.saved.docs = restored
	must(t, restored.put("dashboards/study-synthetic.json", []byte(`[{"id":"duplicate","name":"A"},{"id":"duplicate","name":"B"}]`)))
	if w = savedEngineRequest(a, s, "GET", path, nil); w.Code != 502 {
		t.Fatal("corrupt document accepted")
	}
	must(t, restored.put("dashboards/study-synthetic.json", []byte(`{`)))
	if w = savedEngineRequest(a, s, "GET", path, nil); w.Code != 502 {
		t.Fatal("invalid JSON looks absent")
	}
}

func TestSavedResearchPolicyAndDiscoveryChanges(t *testing.T) {
	a, s, srv := savedResearchApp(t)
	defer srv.Close()
	form := url.Values{"csrf": {s.CSRF}, "panel": {savedQuestion}, "name": {"Policy changes"}}
	w := savedEngineRequest(a, s, "POST", "/dashboards/add", form)
	path := w.Header().Get("Location")
	for _, k := range []int{5, 2, 8} {
		// No analysis configuration: data discovery fails. It must remain
		// unavailable, never become a removed track or expose a numeric zero.
		raw, _ := json.Marshal(testPolicy(k))
		must(t, a.policies.docs.put("research-policies/synthetic.json", raw))
		w = savedEngineRequest(a, s, "GET", path, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `data-release="unavailable"`) || strings.Contains(w.Body.String(), translator("en")("panel_gone")) || strings.Contains(w.Body.String(), "data-value>") {
			t.Fatal("discovery failure reinterpreted", w.Body.String())
		}
	}
	list, err := a.saved.load("study-synthetic")
	must(t, err)
	// A field truly absent from a successfully read catalog has a distinct state.
	in := researchInput{cat: Catalog{Types: []TypeInfo{{ID: "study.synthetic.sleep"}}}}
	if _, _, err = a.studyPanel(list[0].Panels[0], "study:synthetic", in.cat); err == nil {
		t.Fatal("removed field silently reinterpreted")
	}
	if len(list[0].Panels) != 1 {
		t.Fatal("definitions deleted")
	}
}

func TestSavedResearchSyntheticViews(t *testing.T) {
	spec := Spec{Type: "study.synthetic.sleep", Field: "hours", Measure: MeasureSum, Group: "period", Period: "day", TZ: time.UTC}
	raw := Result{Spec: spec, Points: []Point{rawPoint("2026-01-01", []int64{2, 3}, 2), rawPoint("2026-01-02", []int64{987654321}, 1), rawPoint("2026-01-03", []int64{-4, 0}, 2)}}
	mixed := releaseResearch(raw, testPolicy(2), nil)
	dir := os.Getenv("VOPTRACK_SAVED_PREVIEW_DIR")
	for _, lang := range languages {
		for _, state := range []string{"mixed", "unconfigured", "unavailable", "unsupported", "empty", "validation", "storage"} {
			var body string
			if state == "validation" || state == "storage" {
				key := "dashboard_name_err"
				if state == "storage" {
					key = "dashboard_save_failed"
				}
				body = renderUIForTest(t, httptest.NewRequest("GET", "/?lang="+lang, nil), "save_study_panel", saveStudyData{Scope: "study:synthetic", StudyTitle: "Synthetic study", Name: "A long dashboard name / Eine lange Übersicht / Pitkä koontinäyttö", Query: savedQuestion, Error: key, Back: "/panel?" + savedQuestion})
			} else {
				data := dashboardData{Scope: "study:synthetic", StudyTitle: "Synthetic study", Board: Dashboard{ID: "synthetic", Name: "Research / Tutkimus / Исследование"}, CanEdit: true, Back: "/studies/synthetic/dashboard", Token: "synthetic-version"}
				if state != "empty" {
					res := mixed
					if state != "mixed" {
						policy := testPolicy(2)
						if state == "unconfigured" {
							policy = suppressionPolicy{}
						}
						res = withheld(spec, policy, state)
					}
					data.Panels = []dashboardPanel{{Title: "Sleep · Hours · sum", Link: panelLink(data.Scope, spec), Result: res, Rows: resultRows(res), Chart: buildChart(res, true)}}
					if state == "mixed" {
						for _, status := range []string{"unconfigured", "unavailable", "unsupported"} {
							policy := testPolicy(2)
							if status == "unconfigured" {
								policy = suppressionPolicy{}
							}
							r := withheld(spec, policy, status)
							data.Panels = append(data.Panels, dashboardPanel{Title: "Sleep · Hours · sum", Link: panelLink(data.Scope, spec), Result: r, Rows: resultRows(r), Chart: buildChart(r, true)})
						}
					}
				}
				body = renderUIForTest(t, httptest.NewRequest("GET", "/?lang="+lang, nil), "dashboard", data)
			}
			if strings.Contains(body, "987654321") || strings.Contains(body, "<script") {
				t.Fatal("saved HTML/SVG/accessibility leak")
			}
			if dir != "" {
				must(t, os.MkdirAll(filepath.Join(dir, lang), 0700))
				writeSuppressionPreview(t, dir, lang, state, body)
			}
		}
	}
}

func TestSavedResearchRecomputesCurrentReleaseAndRevokedAuthority(t *testing.T) {
	a, s, old := savedResearchApp(t)
	old.Close()
	var metaMode atomic.Int32
	var revoked atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth/account":
			if revoked.Load() {
				w.WriteHeader(401)
				return
			}
			writeJSON(w, 200, map[string]any{"account": "owner", "server_administrator": true})
		case r.URL.Path == "/admin/api/v1/overview":
			writeJSON(w, 200, map[string]any{"accounts": []map[string]any{{"account": "p1", "datasets": map[string]any{"one": nil}}, {"account": "p2", "datasets": map[string]any{"two": nil}}}})
		case strings.HasSuffix(r.URL.Path, "/meta"):
			if metaMode.Load() == 1 {
				w.WriteHeader(503)
				return
			}
			fields := []Field{{ID: "timestamp", Type: "date_time"}}
			if metaMode.Load() != 2 {
				fields = append(fields, Field{ID: "hours", Name: "Hours", Type: "float"})
			}
			writeJSON(w, 200, map[string]any{"types": []Meta{{ID: "study.synthetic.sleep", Name: "Sleep", Fields: fields}}})
		default:
			writeJSON(w, 200, map[string]any{"aggregates": []Aggregate{{RowCount: "1", ValueCount: "1", Sum: "987654321", Minimum: "987654321", Maximum: "987654321"}}})
		}
	}))
	defer server.Close()
	a.tessera = &Tessera{internal: server.URL, http: server.Client()}
	a.cfg.Analysis = "analysis"
	a.analysisCache = newServiceTokens("analysis", "")
	for _, id := range []string{"one", "two"} {
		a.analysisCache.byID[id] = &tokenSlot{tok: tokensForTest()}
	}
	for _, id := range []string{"p1", "p2"} {
		must(t, a.studies.recordConsent("synthetic", ConsentRecord{Account: id, Version: 1, At: time.Now()}))
	}
	question := strings.Replace(savedQuestion, "group=period", "group=none", 1)
	w := savedEngineRequest(a, s, "POST", "/dashboards/add", url.Values{"csrf": {s.CSRF}, "panel": {question}, "name": {"Current answers"}})
	path := w.Header().Get("Location")
	for _, k := range []int{3, 2, 4} {
		policy, _ := json.Marshal(testPolicy(k))
		must(t, a.policies.docs.put("research-policies/synthetic.json", policy))
		w = savedEngineRequest(a, s, "GET", path, nil)
		csv := savedEngineRequest(a, s, "GET", "/panel.csv?"+question, nil)
		if w.Code != 200 || csv.Code != 200 {
			t.Fatal(w.Code, csv.Code)
		}
		if k > 2 {
			for _, body := range []string{w.Body.String(), csv.Body.String()} {
				if strings.Contains(body, "987654321") || strings.Contains(body, "1975308642") || !strings.Contains(body, "suppressed") {
					t.Fatal("saved release bypass", body)
				}
			}
		} else if !strings.Contains(w.Body.String(), "1975308642") || !strings.Contains(csv.Body.String(), "1975308642") {
			t.Fatal("current lowered policy not applied")
		}
	}
	metaMode.Store(1)
	w = savedEngineRequest(a, s, "GET", path, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `data-release="unavailable"`) || strings.Contains(w.Body.String(), translator("en")("panel_gone")) {
		t.Fatal("failed discovery looks removed")
	}
	metaMode.Store(2)
	w = savedEngineRequest(a, s, "GET", path, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), translator("en")("panel_gone")) {
		t.Fatal("removed field reinterpreted", w.Body.String())
	}
	list, err := a.saved.load("study-synthetic")
	must(t, err)
	if len(list[0].Panels) != 1 {
		t.Fatal("removed field deletes definition")
	}
	revoked.Store(true)
	for _, action := range []struct{ method, path string }{{"GET", path}, {"GET", "/studies/synthetic/dashboard"}, {"GET", "/panel.csv?" + question}, {"POST", "/dashboards/add"}, {"POST", path + "/remove"}} {
		w := savedEngineRequest(a, s, action.method, action.path, url.Values{"csrf": {s.CSRF}, "panel": {question}, "name": {"Revoked"}, "version": {dashboardToken(list[0])}, "index": {"0"}})
		if w.Code != 403 {
			t.Fatal("revoked researcher retained authority", action, w.Code)
		}
	}
}

func TestSavedResearchStudyIsolation(t *testing.T) {
	a, s, srv := savedResearchApp(t)
	defer srv.Close()
	st, _ := a.studies.get("synthetic")
	st.Slug = "separate"
	st.Title = "Separate study"
	st.Tracks[0].TypeID = "study.separate.sleep"
	raw, _ := json.Marshal(st)
	must(t, a.studies.docs.put("studies/separate.json", raw))
	form := url.Values{"csrf": {s.CSRF}, "name": {"Same name"}, "panel": {savedQuestion}}
	w := savedEngineRequest(a, s, "POST", "/dashboards/add", form)
	path := w.Header().Get("Location")
	form.Set("panel", strings.ReplaceAll(savedQuestion, "synthetic", "separate"))
	w = savedEngineRequest(a, s, "POST", "/dashboards/add", form)
	separatePath := w.Header().Get("Location")
	if path == separatePath || !strings.HasPrefix(separatePath, "/dashboards/study:separate/") {
		t.Fatal("study keys shared", path, separatePath)
	}
	if w = savedEngineRequest(a, s, "GET", strings.Replace(path, "study:synthetic", "study:separate", 1), nil); w.Code != 404 {
		t.Fatal("board visible through another study")
	}
	first, _ := a.saved.load("study-synthetic")
	second, _ := a.saved.load("study-separate")
	if len(first) != 1 || len(second) != 1 || strings.Contains(second[0].Panels[0], "synthetic") {
		t.Fatal("study definitions mixed")
	}
	form.Set("version", dashboardToken(first[0]))
	form.Set("index", "0")
	if w = savedEngineRequest(a, s, "POST", strings.Replace(path, "study:synthetic", "study:separate", 1)+"/remove", form); w.Code != 409 {
		t.Fatal("cross-study removal accepted")
	}
	second, _ = a.saved.load("study-separate")
	if len(second[0].Panels) != 1 {
		t.Fatal("other study changed")
	}
}
