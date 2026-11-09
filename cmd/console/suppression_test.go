package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testPolicy(k int) suppressionPolicy {
	return suppressionPolicy{Version: 1, K: k, Revision: 1, Changed: time.Now().UTC()}
}
func rawPoint(label string, values []int64, n int) Point {
	p := Point{Label: label, contributors: map[string]bool{}}
	for i := 0; i < n; i++ {
		p.contributors[fmt.Sprint(i)] = true
	}
	for _, v := range values {
		p.Tally.addValue(big.NewRat(v, 1))
	}
	return p
}
func TestResearchReleaseBoundaryAndMeasures(t *testing.T) {
	for _, m := range []Measure{MeasureCount, MeasureSum, MeasureAverage, MeasureMinimum, MeasureMaximum} {
		for _, group := range []string{"none", "store", "cohort", "period", "field:category", "spread"} {
			for _, n := range []int{1, 2, 3} {
				t.Run(fmt.Sprintf("%s/%s/%d", m, group, n), func(t *testing.T) {
					spec := Spec{Field: "v", Measure: m, Group: group, TZ: time.UTC}
					pt := rawPoint("private-category-8123", []int64{-4, 0, 4}, n)
					res := releaseResearch(Result{Spec: spec, Points: []Point{pt}}, testPolicy(2), nil)
					if n < 2 {
						if res.Status != "suppressed" || pointValue(res, res.Points[0]) != nil {
							t.Fatal("below boundary released")
						}
						if group != "period" && res.Points[0].Label != "" {
							t.Fatal("private label released")
						}
					} else if res.Status != "released" || pointValue(res, res.Points[0]).Cmp(pt.Tally.Value(m, "v")) != 0 {
						t.Fatal("boundary or arithmetic changed")
					}
					for _, p := range res.Points {
						if p.Tally.Sum != nil || p.Tally.Rows != 0 || p.contributors != nil {
							t.Fatal("raw data reaches template")
						}
					}
				})
			}
		}
	}
	for _, r := range []Result{
		{Spec: Spec{Measure: MeasureCount, Group: "none", TZ: time.UTC}},
		{Spec: Spec{Measure: MeasureCount, Group: "none", TZ: time.UTC}, Points: []Point{{}}},
		{Spec: Spec{Measure: MeasureSum, Field: "v", Group: "none", TZ: time.UTC}, Points: []Point{{Tally: Tally{Rows: 100}}}},
	} {
		res := releaseResearch(r, testPolicy(2), nil)
		if res.Status != "suppressed" || pointValue(res, res.Points[0]) != nil {
			t.Fatal("empty/null result exposes exact zero")
		}
	}
}
func computeFixture(t *testing.T, sources []Source, spec Spec, answer http.HandlerFunc) (Result, error) {
	t.Helper()
	srv := httptest.NewServer(answer)
	defer srv.Close()
	return compute(context.Background(), &Tessera{internal: srv.URL, http: srv.Client()}, sources, spec)
}
func syntheticSource(id, participant string) Source {
	return Source{Dataset: id, Name: "mutable-" + id, Participant: participant, Cohort: "v2", Token: func(context.Context) (string, error) { return "synthetic", nil }}
}
func TestResearchComputeDistinctParticipants(t *testing.T) {
	for _, field := range []string{"", "v"} {
		for _, group := range []string{"none", "store", "cohort", "field:label"} {
			spec := Spec{Research: true, Type: "track", Field: field, Measure: MeasureCount, Group: group, TZ: time.UTC}
			// Two stores claiming the same stable participant still count only once.
			sources := []Source{syntheticSource("one", "same"), syntheticSource("two", "same")}
			res, err := computeFixture(t, sources, spec, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, map[string]any{"aggregates": []Aggregate{{Group: json.RawMessage(`"private-label"`), RowCount: "10000", ValueCount: "9999", Sum: json.Number("0"), Minimum: json.Number("-1"), Maximum: json.Number("1")}}})
			})
			must(t, err)
			for _, p := range res.Points {
				if len(p.contributors) != 1 {
					t.Fatal("records/stores counted as people")
				}
			}
			safe := releaseResearch(res, testPolicy(2), nil)
			if safe.Status != "suppressed" {
				t.Fatal("one prolific person passed")
			}
		}
	}
	for _, group := range []string{"none", "cohort", "field:label"} {
		spec := Spec{Research: true, Type: "track", Field: "v", Measure: MeasureAverage, Group: group, TZ: time.UTC}
		res, err := computeFixture(t, []Source{syntheticSource("one", "a"), syntheticSource("two", "b")}, spec, func(w http.ResponseWriter, r *http.Request) {
			rows, sum := "1", "10"
			if strings.Contains(r.URL.Path, "two") {
				rows, sum = "3", "60"
			}
			writeJSON(w, 200, map[string]any{"aggregates": []Aggregate{{Group: json.RawMessage(`"same"`), RowCount: json.Number(rows), ValueCount: json.Number(rows), Sum: json.Number(sum), Minimum: json.Number("10"), Maximum: json.Number("20")}}})
		})
		must(t, err)
		safe := releaseResearch(res, testPolicy(2), nil)
		if safe.Status != "released" || pointValue(safe, safe.Points[0]).Cmp(big.NewRat(70, 4)) != 0 {
			t.Fatal("weighted average changed")
		}
	}
}
func TestResearchRowsDatesAndDistributions(t *testing.T) {
	for _, group := range []string{"period", "spread"} {
		spec := Spec{Research: true, Type: "track", Field: "v", Date: "timestamp", Measure: MeasureCount, Group: group, Period: "day", TZ: time.FixedZone("east", 2*3600)}
		sources := []Source{syntheticSource("one", "a"), syntheticSource("two", "b")}
		res, err := computeFixture(t, sources, spec, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, ":page") {
				writeJSON(w, 200, map[string]string{"cursor": "synthetic"})
				return
			}
			fields := []map[string]string{{"id": "v"}}
			row := []map[string]any{{"type": "float", "value": 7}}
			if group == "period" {
				fields = append(fields, map[string]string{"id": "timestamp"})
				row = append(row, map[string]any{"type": "date_time", "value": "2026-09-20T23:00:00.000Z"})
			}
			// Server field order is reversed; repeated rows cannot inflate n.
			writeJSON(w, 200, map[string]any{"meta": map[string]any{"fields": fields}, "records": []map[string]any{{"values": row}, {"values": row}}, "next_cursor": nil})
		})
		must(t, err)
		if len(res.Points) != 1 || len(res.Points[0].contributors) != 2 {
			t.Fatal("row contributor dedup failed")
		}
		if group == "period" && res.Points[0].Label != "2026-09-21" {
			t.Fatal("timezone grouping changed")
		}
		safe := releaseResearch(res, testPolicy(2), nil)
		if safe.Status != "released" || exact(pointValue(safe, safe.Points[0])) != "4" {
			t.Fatal("row count changed")
		}
	}
	// Numeric missing values contribute only to record count.
	for _, field := range []string{"", "v"} {
		spec := Spec{Research: true, Type: "track", Field: field, Date: "timestamp", Measure: MeasureCount, Group: "period", Period: "day", TZ: time.UTC}
		res, err := computeFixture(t, []Source{syntheticSource("one", "a")}, spec, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, ":page") {
				writeJSON(w, 200, map[string]string{"cursor": "x"})
				return
			}
			fields := []map[string]string{{"id": "timestamp"}}
			row := []map[string]any{{"value": "2026-09-21T00:00:00Z"}}
			if field != "" {
				fields = append(fields, map[string]string{"id": "v"})
				row = append(row, map[string]any{"value": nil})
			}
			writeJSON(w, 200, map[string]any{"meta": map[string]any{"fields": fields}, "records": []map[string]any{{"values": row}}})
		})
		must(t, err)
		want := 0
		if field == "" {
			want = 1
		}
		if len(res.Points[0].contributors) != want {
			t.Fatal("null contributes to numeric measure")
		}
	}
	// 13 exact values trigger the unsupported adaptive format, even with K people.
	spec := Spec{Research: true, Type: "track", Field: "v", Group: "spread", Measure: MeasureCount, TZ: time.UTC}
	res, err := computeFixture(t, []Source{syntheticSource("one", "a"), syntheticSource("two", "b")}, spec, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":page") {
			writeJSON(w, 200, map[string]string{"cursor": "x"})
			return
		}
		var rows []map[string]any
		for i := 0; i < 13; i++ {
			rows = append(rows, map[string]any{"values": []map[string]any{{"value": i}}})
		}
		writeJSON(w, 200, map[string]any{"meta": map[string]any{"fields": []map[string]string{{"id": "v"}}}, "records": rows})
	})
	must(t, err)
	if releaseResearch(res, testPolicy(2), nil).Status != "unsupported" {
		t.Fatal("adaptive distribution mislabeled")
	}
	small := Result{Spec: spec, Points: []Point{rawPoint("private-123", []int64{1}, 1), rawPoint("public", []int64{2, 2}, 2)}}
	safe := releaseResearch(small, testPolicy(2), nil)
	if len(safe.Points) != 1 || safe.Points[0].Label != "" || safe.Status != "suppressed" {
		t.Fatal("partial exact distribution released")
	}
}
func TestResearchDateFilterBoundaries(t *testing.T) {
	a := &app{tz: time.UTC}
	cat := Catalog{Types: []TypeInfo{{ID: "track", Numeric: []Field{{ID: "v"}}, Dates: []Field{{ID: "timestamp"}}}}}
	q := url.Values{"type": {"track"}, "field": {"v"}, "group": {"period"}, "from": {"2026-03-29"}, "to": {"2026-03-29"}, "tz": {"Europe/Helsinki"}}
	spec, _, err := a.parseSpecValues(q, cat)
	must(t, err)
	if spec.To.Sub(spec.From) != 23*time.Hour {
		t.Fatal("DST day changed")
	}
	res, err := computeFixture(t, []Source{syntheticSource("one", "a"), syntheticSource("two", "b")}, withResearch(spec), func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":page") {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			raw, _ := json.Marshal(body)
			if !strings.Contains(string(raw), "2026-03-28T22:00:00.000Z") || !strings.Contains(string(raw), "2026-03-29T20:59:59.999Z") {
				t.Error("half-open date filter changed")
			}
			writeJSON(w, 200, map[string]string{"cursor": "x"})
			return
		}
		var rows []map[string]any
		if strings.Contains(r.URL.Path, "one") {
			rows = []map[string]any{{"values": []map[string]any{{"value": "2026-03-29T10:00:00Z"}, {"value": 0}}}}
		}
		writeJSON(w, 200, map[string]any{"meta": map[string]any{"fields": []map[string]string{{"id": "timestamp"}, {"id": "v"}}}, "records": rows})
	})
	must(t, err)
	if releaseResearch(res, testPolicy(2), nil).Status != "suppressed" {
		t.Fatal("unfiltered participants counted")
	}
}
func withResearch(s Spec) Spec { s.Research = true; return s }
func TestResearchFailuresNeverBecomeValues(t *testing.T) {
	spec := Spec{Research: true, Type: "track", Field: "v", Group: "period", Measure: MeasureSum, Date: "timestamp", Period: "day", TZ: time.UTC}
	for _, failure := range []string{"skipped", "later-page", "unknown", "token"} {
		t.Run(failure, func(t *testing.T) {
			src := syntheticSource("one", "a")
			if failure == "unknown" {
				src.Participant = ""
			}
			if failure == "token" {
				src.Token = func(context.Context) (string, error) { return "", errors.New("synthetic failure") }
			}
			res, err := computeFixture(t, []Source{src}, spec, func(w http.ResponseWriter, r *http.Request) {
				if failure == "skipped" {
					w.WriteHeader(403)
					return
				}
				if strings.HasSuffix(r.URL.Path, ":page") {
					writeJSON(w, 200, map[string]string{"cursor": "first"})
					return
				}
				if strings.Contains(r.URL.Path, "first") {
					writeJSON(w, 200, map[string]any{"meta": map[string]any{"fields": []map[string]string{{"id": "timestamp"}, {"id": "v"}}}, "records": []map[string]any{{"values": []map[string]any{{"value": "2026-09-21T00:00:00Z"}, {"value": 12345}}}}, "next_cursor": "second"})
					return
				}
				w.WriteHeader(503)
			})
			safe := releaseResearch(res, testPolicy(2), err)
			if safe.Status != "unavailable" || safe.Points[0].ReleasedValue != nil || safe.Points[0].Label != "" {
				t.Fatal("failure leaked partial data")
			}
		})
	}
	res := Result{Spec: spec, Points: []Point{rawPoint("private", []int64{12345}, 2)}, Truncated: true}
	if releaseResearch(res, testPolicy(2), nil).Status != "unavailable" {
		t.Fatal("truncated result released")
	}
	// Verify the real row limit path, not just the release flag.
	res, err := computeFixture(t, []Source{syntheticSource("one", "a")}, spec, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":page") {
			writeJSON(w, 200, map[string]string{"cursor": "x"})
			return
		}
		row := map[string]any{"values": []map[string]any{{"value": "2026-09-21T00:00:00Z"}, {"value": 1}}}
		rows := make([]map[string]any, rowLimit)
		for i := range rows {
			rows[i] = row
		}
		writeJSON(w, 200, map[string]any{"meta": map[string]any{"fields": []map[string]string{{"id": "timestamp"}, {"id": "v"}}}, "records": rows})
	})
	must(t, err)
	if !res.Truncated || releaseResearch(res, testPolicy(2), nil).Status != "unavailable" {
		t.Fatal("actual row limit released")
	}
}

type failedPolicyDocs struct{ docStore }

func (f failedPolicyDocs) put(string, []byte) error { return errors.New("synthetic write failure") }
func TestPolicyValidationPersistenceAndStale(t *testing.T) {
	dir := t.TempDir()
	docs := fileDocs{dir: dir}
	store := &policyStore{docs: docs}
	for _, v := range []string{"", "0", "1", "-2", "+2", "2.0", "2.5", " 5", "5 ", "2147483648", "99999999999999999999999", "٢"} {
		if _, err := parseThreshold(v); err == nil {
			t.Errorf("accepted %q", v)
		}
	}
	if k, err := parseThreshold("2147483647"); err != nil || k != maxSuppressionK {
		t.Fatal("upper technical boundary rejected")
	}
	p, found, err := store.load("synthetic")
	must(t, err)
	if found || p.K != 0 {
		t.Fatal("implicit threshold")
	}
	p, err = store.save("synthetic", "5", "0", false)
	must(t, err)
	if p.Revision != 1 {
		t.Fatal("revision")
	}
	if _, err := store.save("synthetic", "4", "0", true); err == nil || err.Error() != "policy_stale" {
		t.Fatal("stale accepted")
	}
	if _, err := store.save("synthetic", "4", "1", false); err == nil || err.Error() != "policy_lower_required" {
		t.Fatal("unconfirmed reduction")
	}
	p, err = store.save("synthetic", "4", "1", true)
	must(t, err)
	restored := &policyStore{docs: fileDocs{dir: dir}}
	got, found, err := restored.load("synthetic")
	must(t, err)
	if !found || got.K != 4 || got.Revision != 2 || !got.Changed.Equal(p.Changed) {
		t.Fatal("policy did not survive reconstruction")
	}
	failing := &policyStore{docs: failedPolicyDocs{docs}}
	if _, err := failing.save("synthetic", "9", "2", false); err == nil {
		t.Fatal("failed write accepted")
	}
	got, _, _ = restored.load("synthetic")
	if got.K != 4 || got.Revision != 2 {
		t.Fatal("failed save changed policy")
	}
	must(t, docs.put("research-policies/broken.json", []byte(`{"version":1,"k":1,"revision":1}`)))
	if _, _, err := restored.load("broken"); err == nil {
		t.Fatal("invalid persisted policy")
	}
	// Existing file migration namespace must include policy docs.
	foundPrefix := false
	for _, prefix := range docPrefixes {
		if prefix == "research-policies/" {
			foundPrefix = true
		}
	}
	if !foundPrefix {
		t.Fatal("policy omitted from migration")
	}
	if list, _ := docs.list("studies/"); len(list) != 0 {
		t.Fatal("policies pollute study list")
	}
}
func TestResearchReleaseNoLeaksAndCSVCompatibility(t *testing.T) {
	spec := Spec{Type: "track", Field: "v", Measure: MeasureSum, Group: "period", Period: "day", TZ: time.UTC}
	raw := Result{Spec: spec, Points: []Point{rawPoint("2026-01-01", []int64{2, 3}, 2), rawPoint("2026-01-02", []int64{987654321}, 1), rawPoint("2026-01-03", []int64{-4, 0}, 2)}}
	safe := releaseResearch(raw, testPolicy(2), nil)
	chart := buildChart(safe, true)
	if strings.Contains(chart.Path, "L") {
		t.Fatal("line crosses suppressed point")
	}
	for _, lang := range languages {
		html := renderUIForTest(t, httptest.NewRequest("GET", "/panel?lang="+lang, nil), "panel", panelData{Scope: "study:synthetic", Spec: spec, Type: TypeInfo{Name: "Synthetic"}, Result: safe, Chart: chart, Rows: resultRows(safe), Back: "/", TZ: "UTC"})
		if strings.Contains(html, "987654321") || strings.Contains(html, "<script") {
			t.Fatal("HTML/SVG/accessibility leaks")
		}
		if !strings.Contains(html, translator("en")("policy_result_suppressed")) {
			t.Fatal("status untranslated")
		}
	}
	var b strings.Builder
	cw := csv.NewWriter(&b)
	writeResearchCSV(cw, safe)
	cw.Flush()
	reader := csv.NewReader(strings.NewReader(b.String()))
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	must(t, err)
	if strings.Join(rows[5], ",") != "group,value,records,values,status" {
		t.Fatal("CSV columns changed")
	}
	if strings.Join(rows[7], ",") != "2026-01-02,,,,suppressed" || rows[6][1] != "5" || rows[8][1] != "-4" {
		t.Fatal("CSV status/value mismatch")
	}
	if strings.Contains(b.String(), "987654321") {
		t.Fatal("CSV leaks")
	}
	raw.Spec.Group = "field:secret"
	raw.Points[1].Label = "secret-label-unique"
	safe = releaseResearch(raw, testPolicy(2), nil)
	if len(safe.Points) != 3 || safe.Points[2].Label != "" {
		t.Fatal("hidden categories not collapsed")
	}
	raw.Points = append(raw.Points, rawPoint("another-secret", []int64{999}, 1))
	safe = releaseResearch(raw, testPolicy(2), nil)
	if len(safe.Points) != 3 {
		t.Fatal("exact number of hidden categories exposed")
	}
	for _, status := range []string{"unconfigured", "unavailable", "unsupported"} {
		safe = withheld(spec, testPolicy(2), status)
		b.Reset()
		cw = csv.NewWriter(&b)
		writeResearchCSV(cw, safe)
		cw.Flush()
		if !strings.Contains(b.String(), ",,,,"+status) || strings.Contains(b.String(), "987654321") {
			t.Fatal("CSV withheld state")
		}
	}
}
func policyHTTPApp(t *testing.T) (*app, *session, *httptest.Server) {
	a := participantTestApp(t)
	a.policies = &policyStore{docs: a.studies.docs}
	a.tz = time.UTC
	for _, page := range []string{"study_policy", "overview", "panel"} {
		tpl, err := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix, "resultRows": resultRows, "localizeName": localizeName, "chartLabelVisible": chartLabelVisible, "chartLabelText": chartLabelText}).ParseFS(assets, "templates/layout.html", "templates/chart.html", "templates/"+page+".html")
		must(t, err)
		a.pages[page] = tpl
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/account" {
			writeJSON(w, 200, map[string]any{"account": "owner", "server_administrator": true})
			return
		}
		w.WriteHeader(503)
	}))
	a.tessera = &Tessera{internal: srv.URL, http: srv.Client()}
	sess := &session{ID: "synthetic-cookie", CSRF: "synthetic-csrf", LastSeen: time.Now(), Owner: &connection{tokens: tokens{Access: "synthetic", Expires: time.Now().Add(time.Hour)}}}
	a.sessions.byID[sess.ID] = sess
	return a, sess, srv
}
func TestPolicyHTMLPermissionsErrorsAndIsolation(t *testing.T) {
	a, sess, srv := policyHTTPApp(t)
	defer srv.Close()
	// Rewrite a usable track so direct URLs remain available with no stores.
	st, _ := a.studies.get("synthetic")
	st.Tracks = []TrackDesign{{TypeID: "study.synthetic.sleep", Name: "Sleep", Fields: []TrackField{{ID: "hours", Name: "Hours", Kind: KindNumber, Meaning: "sample"}}}}
	raw, _ := json.Marshal(st)
	must(t, a.studies.docs.put("studies/synthetic.json", raw))
	request := func(method, path string, form url.Values, cookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie {
			r.AddCookie(&http.Cookie{Name: a.sessions.cookieName(), Value: sess.ID})
		}
		w := httptest.NewRecorder()
		// The former value UI is retired; retain suppression renderer engine
		// coverage separately from the actual-router retirement tests.
		if r.URL.Path == "/panel" {
			a.analyticalPanel(w, r)
		} else if strings.HasSuffix(r.URL.Path, "/dashboard") {
			if owner, ok := a.researcher(w, r); ok {
				a.researchOverview(w, r, owner, "synthetic")
			}
		} else {
			a.routes().ServeHTTP(w, r)
		}
		return w
	}
	path := "/studies/synthetic/policy"
	for _, cookie := range []bool{false, true} {
		w := request("POST", path, url.Values{"csrf": {"forged"}, "threshold": {"5"}, "revision": {"0"}}, cookie)
		if w.Code != 303 && w.Code != 403 {
			t.Fatal("unauthorized policy write")
		}
	}
	if _, found, _ := a.policies.load("synthetic"); found {
		t.Fatal("unauthorized persisted")
	}
	q := "/panel?scope=study:synthetic&type=study.synthetic.sleep&field=hours&group=none"
	if w := request("GET", q, nil, true); w.Code != 200 || !strings.Contains(w.Body.String(), `data-release="unconfigured"`) {
		t.Fatal("unconfigured bypass")
	}
	bad := url.Values{"csrf": {sess.CSRF}, "threshold": {"2.5"}, "revision": {"0"}}
	w := request("POST", path, bad, true)
	if w.Code != 422 || !strings.Contains(w.Body.String(), `value="2.5"`) {
		t.Fatal("validation lost input")
	}
	good := url.Values{"csrf": {sess.CSRF}, "threshold": {"5"}, "revision": {"0"}}
	if w := request("POST", path, good, true); w.Code != 303 {
		t.Fatalf("save %d %s", w.Code, w.Body.String())
	}
	if w := request("POST", path, good, true); w.Code != 422 || !strings.Contains(w.Body.String(), "policy_stale") {
		t.Fatal("stale update")
	}
	a.cfg.Analysis = "analysis"
	for _, path := range []string{q, strings.Replace(q, "/panel?", "/panel.csv?", 1), "/studies/synthetic/dashboard"} {
		w := request("GET", path, nil, true)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "unavailable") {
			t.Fatal("discovery failure became empty")
		}
	}
	sess.Owner = nil
	sess.Connections = []*connection{{}}
	if w := request("POST", path, good, true); w.Code != 403 {
		t.Fatal("participant changed policy")
	}
	sess.Owner = &connection{tokens: tokens{Access: "synthetic", Expires: time.Now().Add(time.Hour)}}
	a.cfg.Variant = "household"
	if w := request("GET", path, nil, true); w.Code != 404 {
		t.Fatal("Home exposes study policy")
	}
}
func TestResearchCatalogFailureAndLegacyDiscovery(t *testing.T) {
	a, sess, srv := policyHTTPApp(t)
	srv.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/api/v1/overview" {
			writeJSON(w, 200, map[string]any{"accounts": []map[string]any{{"account": "p", "datasets": map[string]any{"one": nil}}}})
			return
		}
		w.WriteHeader(503)
	}))
	defer server.Close()
	a.tessera = &Tessera{internal: server.URL, http: server.Client()}
	a.cfg.Analysis = "analysis"
	a.analysisCache = newServiceTokens("analysis", filepath.Join(t.TempDir(), "missing-secret"))
	a.analysisCache.byID["one"] = &tokenSlot{tok: tokensForTest()}
	must(t, a.studies.recordConsent("synthetic", ConsentRecord{Account: "p", Version: 1, At: time.Now()}))
	_, err := a.policies.save("synthetic", "2", "0", false)
	must(t, err)
	st, _ := a.studies.get("synthetic")
	st.Tracks = []TrackDesign{{TypeID: "study.synthetic.sleep", Name: "Sleep", Fields: []TrackField{{ID: "hours", Name: "Hours", Kind: KindNumber, Meaning: "sample"}}}}
	in := a.researchInput(context.Background(), sess, st)
	if in.status != "unavailable" {
		t.Fatal("catalog failure silently skipped")
	}
}
func TestPolicyDocumentsRemoteRestoration(t *testing.T) {
	// Exercise actual tesseraDocs serialization and index reconstruction.
	var stored map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, ":page") {
			writeJSON(w, 200, map[string]string{"cursor": "x"})
			return
		}
		if strings.Contains(r.URL.Path, "/pages/") {
			body, _ := json.Marshal(stored["body"])
			writeJSON(w, 200, map[string]any{"meta": map[string]any{"fields": []map[string]string{{"id": "name"}, {"id": "body"}}}, "records": []map[string]any{{"id": map[string]string{"id": "doc"}, "values": []map[string]any{{"value": stored["name"]}, {"value": json.RawMessage(body)}}}}})
			return
		}
		var req struct {
			Fields map[string]struct {
				Value any `json:"value"`
			} `json:"fields"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		stored = map[string]any{"name": req.Fields["name"].Value, "body": req.Fields["body"].Value}
		writeJSON(w, 201, map[string]any{"objects": []map[string]string{{"id": "doc"}}})
	}))
	defer srv.Close()
	tess := &Tessera{internal: srv.URL, http: srv.Client()}
	tokens := newServiceTokens("state", "")
	tokens.byID["dataset"] = &tokenSlot{tok: tokensForTest()}
	docs := &tesseraDocs{t: tess, tokens: tokens, dataset: "dataset", index: map[string]docEntry{}}
	store := &policyStore{docs: docs}
	p, err := store.save("synthetic", "7", "0", false)
	must(t, err)
	restored := &tesseraDocs{t: tess, tokens: tokens, dataset: "dataset", index: map[string]docEntry{}}
	must(t, restored.each(context.Background(), "synthetic", docType, []string{"name", "body"}, func(id string, v map[string]json.RawMessage) {
		var name, body string
		json.Unmarshal(v["name"], &name)
		json.Unmarshal(v["body"], &body)
		restored.index[name] = docEntry{id: id, raw: []byte(body)}
	}))
	got, found, err := (&policyStore{docs: restored}).load("synthetic")
	must(t, err)
	if !found || got.K != p.K || got.Revision != p.Revision {
		t.Fatal("remote policy reconstruction failed")
	}
}
func tokensForTest() tokens { return tokens{Access: "synthetic", Expires: time.Now().Add(time.Hour)} }

func TestSuppressionPresentationStates(t *testing.T) {
	spec := Spec{Type: "synthetic.sleep", Field: "hours", Measure: MeasureSum, Group: "period", Period: "day", TZ: time.UTC}
	mixed := releaseResearch(Result{Spec: spec, Points: []Point{rawPoint("2026-09-20", []int64{3, 4}, 2), rawPoint("2026-09-21", []int64{987654321}, 1), rawPoint("2026-09-22", []int64{2, 4}, 2)}}, testPolicy(2), nil)
	states := map[string]Result{"mixed": mixed, "suppressed": withheld(spec, testPolicy(5), "suppressed"), "unconfigured": withheld(spec, suppressionPolicy{}, "unconfigured"), "unavailable": withheld(spec, testPolicy(5), "unavailable"), "unsupported": withheld(Spec{Type: spec.Type, Field: spec.Field, Measure: MeasureCount, Group: "spread", TZ: time.UTC}, testPolicy(5), "unsupported")}
	dir := os.Getenv("VOPTRACK_SUPPRESSION_PREVIEW")
	for _, lang := range languages {
		for name, res := range states {
			data := panelData{Scope: "study:synthetic", Spec: res.Spec, Result: res, Type: TypeInfo{ID: spec.Type, Name: "Synthetic sleep", Numeric: []Field{{ID: "hours", Name: "Hours"}}, Dates: []Field{{ID: "timestamp"}}}, Chart: buildChart(res, res.Spec.Group == "period"), Rows: resultRows(res), Back: "/studies/synthetic/dashboard", CSV: "/panel.csv", TZ: "UTC", Measures: []string{"count", "sum", "average", "minimum", "maximum"}}
			body := renderUIForTest(t, httptest.NewRequest("GET", "/panel?lang="+lang, nil), "panel", data)
			if strings.Contains(body, "987654321") {
				t.Fatal("preview exposes hidden value")
			}
			if !strings.Contains(body, translator("en")("policy_limits")) {
				t.Fatal("privacy limits missing")
			}
			if dir != "" {
				writeSuppressionPreview(t, dir, lang, name, body)
			}
		}
		for _, name := range []string{"policy", "validation"} {
			data := policyPageData{Study: Study{Slug: "synthetic", Title: "Synthetic sleep"}}
			if name == "validation" {
				data.Threshold = "2.5"
				data.Error = "policy_invalid"
				data.Current = 5
				data.Revision = 3
			}
			body := renderUIForTest(t, httptest.NewRequest("GET", "/studies/synthetic/policy?lang="+lang, nil), "study_policy", data)
			if !strings.Contains(body, translator("en")("policy_range")) {
				t.Fatal("policy range missing")
			}
			if name == "validation" && !strings.Contains(body, `value="2.5"`) {
				t.Fatal("entered value lost")
			}
			if dir != "" {
				writeSuppressionPreview(t, dir, lang, name, body)
			}
		}
	}
}
func writeSuppressionPreview(t *testing.T, dir, lang, name, body string) {
	t.Helper()
	css, err := assets.ReadFile("static/console.css")
	must(t, err)
	for _, theme := range []string{"light", "dark"} {
		path := filepath.Join(dir, theme, lang)
		must(t, os.MkdirAll(path, 0700))
		preview := strings.ReplaceAll(body, "/static/console.css", "/"+theme+"/console.css")
		must(t, os.WriteFile(filepath.Join(path, name+".html"), []byte(preview), 0600))
		media := "@media not all"
		if theme == "dark" {
			media = "@media all"
		}
		palette := strings.ReplaceAll(string(css), "@media (prefers-color-scheme: dark)", media)
		must(t, os.WriteFile(filepath.Join(dir, theme, "console.css"), []byte(palette), 0600))
	}
}

func TestResearchEmptyAndNoNumericValuesAreDistinctInternally(t *testing.T) {
	for _, rows := range []string{"0", "5"} {
		spec := Spec{Research: true, Type: "track", Field: "v", Measure: MeasureCount, Group: "none", TZ: time.UTC}
		raw, err := computeFixture(t, []Source{syntheticSource("one", "a")}, spec, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{"aggregates": []Aggregate{{RowCount: json.Number(rows), ValueCount: "0"}}})
		})
		must(t, err)
		want := "empty"
		if rows == "5" {
			want = "no_numeric_values"
		}
		if raw.calculation != want {
			t.Fatalf("internal outcome %s, want %s", raw.calculation, want)
		}
		safe := releaseResearch(raw, testPolicy(2), nil)
		if safe.Status != "suppressed" || safe.calculation != "" {
			t.Fatal("public empty/null distinction bypassed zero-contributor suppression")
		}
	}
	spec := Spec{Research: true, Type: "track", Field: "v", Measure: MeasureSum, Group: "none", TZ: time.UTC}
	raw, err := computeFixture(t, []Source{syntheticSource("one", "a")}, spec, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"aggregates": []Aggregate{{RowCount: "3", ValueCount: "3"}}})
	})
	if releaseResearch(raw, testPolicy(2), err).Status != "unavailable" {
		t.Fatal("incomplete numeric aggregate silently accepted")
	}
}
func TestResearchRecordCountCSVRetainsColumnMeanings(t *testing.T) {
	spec := Spec{Type: "track", Measure: MeasureCount, Group: "none", TZ: time.UTC}
	pt := rawPoint("", []int64{1, 1, 1}, 2)
	// Fieldless server aggregate counts rows, not numeric values.
	pt.Tally.Values = 0
	safe := releaseResearch(Result{Spec: spec, Points: []Point{pt}}, testPolicy(2), nil)
	var b strings.Builder
	cw := csv.NewWriter(&b)
	writeResearchCSV(cw, safe)
	cw.Flush()
	if !strings.Contains(b.String(), ",3,3,0,released") {
		t.Fatal("released record-count CSV column meanings changed")
	}
	b.Reset()
	cw = csv.NewWriter(&b)
	writeResearchCSV(cw, withheld(spec, suppressionPolicy{}, "unconfigured"))
	cw.Flush()
	if !strings.Contains(b.String(), "# research suppression,1,,,unconfigured") {
		t.Fatal("unconfigured policy looks like threshold zero")
	}
}
