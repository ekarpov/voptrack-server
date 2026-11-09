package main

import (
	"fmt"
	"html/template"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHomeCategoryMappingAndFiltering(t *testing.T) {
	if len(homeTrackIdentities) != 72 {
		t.Fatal("app inventory correspondence changed")
	}
	cards := []card{{Type: TypeInfo{ID: "com.voptrack.track.expense", Name: "model.expense"}}, {Type: TypeInfo{ID: "com.voptrack.track.electricity_meter_reading", Name: "Electricity"}}, {Type: TypeInfo{ID: "org.example.custom", Name: "Our custom track"}}}
	for i := range cards {
		prepareHomeCard(&cards[i])
	}
	if cards[0].Type.Name != "Expense" || cards[0].Category != "money" || cards[1].Category != "house" || cards[2].Category != "general" {
		t.Fatal("identity mapping lost known or custom tracks")
	}
	d := overviewData{Cards: cards}
	prepareHomeOverview(&d, "house", time.Now())
	if len(d.Cards) != 1 || d.TotalTracks != 3 || len(d.Categories) != 3 || d.Cards[0].Type.Name != "Electricity" {
		t.Fatal("category filter dropped the catalog context")
	}
	d = overviewData{Cards: cards}
	prepareHomeOverview(&d, "not-a-category", time.Now())
	if len(d.Cards) != 3 || d.SelectedCategory != "" {
		t.Fatal("unknown query hid shared tracks")
	}
}
func TestHomeHighlightIsLatestAvailableAndWithholdsIncompleteReads(t *testing.T) {
	r := Result{Spec: Spec{Measure: MeasureSum, Field: "amount"}, Points: []Point{{Label: "2026-10", Tally: Tally{Rows: 1, Values: 1, Sum: big.NewRat(1, 10)}}, {Label: "2026-11", Tally: Tally{Rows: 1, Values: 1, Sum: big.NewRat(3, 10)}}}}
	c := card{Trend: &r}
	prepareHomeCard(&c)
	if c.Headline != "0.3" || !strings.Contains(c.HeadlineLabel, "November 2026") {
		t.Fatal("headline is not the exact latest observed period")
	}
	c.PerStore.Skipped = []string{"synthetic"}
	prepareHomeCard(&c)
	if c.Headline != "—" {
		t.Fatal("failed membership read advertised a complete highlight")
	}
	c.PerStore.Skipped = nil
	r.Skipped = []string{"synthetic"}
	prepareHomeCard(&c)
	if c.Headline != "—" {
		t.Fatal("incomplete value was advertised as a highlight")
	}
	r.Skipped = nil
	r.Truncated = true
	prepareHomeCard(&c)
	if c.Headline != "—" {
		t.Fatal("truncated value was advertised as a highlight")
	}
}
func TestHomeDoesNotTreatMoneyMinorUnitsAndCountersAsBareNumbers(t *testing.T) {
	for _, typ := range []TypeInfo{{Numeric: []Field{{ID: "amount", Type: "integer"}}, Groups: []Field{{ID: "currency", Type: "text"}}}, {Numeric: []Field{{ID: "reading", Type: "float"}}}, {Numeric: []Field{{ID: "amount", Type: "fixed"}}, Groups: []Field{{ID: "amount_unit", Type: "text"}}}} {
		if homePlainNumber(typ) {
			t.Fatal("uninterpreted units or counters accepted as a value trend")
		}
	}
	if !homePlainNumber(TypeInfo{Numeric: []Field{{ID: "amount", Type: "fixed"}}}) {
		t.Fatal("existing exact bare-decimal convention lost")
	}
}
func TestHomeRenderEscapesCustomNamesAndShowsRecoveryStates(t *testing.T) {
	d := overviewData{Scope: "home", HomeReady: true, Skipped: []string{"synthetic"}, Cards: []card{{Type: TypeInfo{Name: "<script>synthetic</script>"}, Headline: "—", PerChart: Chart{Empty: true}, Chart: Chart{Empty: true}, CompactChart: Chart{Empty: true}}}}
	body := renderUIForTest(t, httptest.NewRequest("GET", "/home?category=anything", nil), "overview", d)
	if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, "could not be read") || !strings.Contains(body, "View chart data") {
		t.Fatal("unsafe or incomplete Home render")
	}
}
func TestManageHomeRejectsAnonymous(t *testing.T) {
	a, _, srv := savedResearchApp(t)
	defer srv.Close()
	w := savedRequest(a, nil, http.MethodGet, "/manage", nil)
	if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "Manage members →") {
		t.Fatal("management page allowed anonymous access")
	}
}

// Exercise the real handler: a presentation-helper test alone cannot catch a
// missing preparation call between fetching shared data and rendering it.
func TestHomeRoutePreparesSharedCardsAndCategories(t *testing.T) {
	a, s, old := policyHTTPApp(t)
	old.Close()
	a.cfg.Variant = "household"
	a.cfg.HomeService = "home"
	a.saved = &savedStore{docs: a.studies.docs}
	a.homeReg = &homeRegistry{docs: a.studies.docs}
	must(t, a.homeReg.set("one", true))
	must(t, a.homeReg.set("two", true))
	a.homeCache = newServiceTokens("home", "")
	a.homeCache.secret = "synthetic"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth/account":
			writeJSON(w, 200, map[string]any{"account": "synthetic", "server_administrator": true})
		case r.URL.Path == "/oauth/token":
			writeJSON(w, 200, map[string]any{"access_token": "shared-reader", "expires_in": 300})
		case r.URL.Path == "/v1/datasets":
			writeJSON(w, 200, map[string]any{"datasets": []any{}})
		case strings.HasSuffix(r.URL.Path, "/meta"):
			if r.Header.Get("Authorization") != "Bearer shared-reader" {
				t.Error("Home used a private credential")
			}
			writeJSON(w, 200, map[string]any{"types": []Meta{{ID: "com.voptrack.track.chore_completion", Name: "model.household.choreCompletion", Fields: []Field{{ID: "timestamp", Type: "date_time"}}}}})
		case strings.HasSuffix(r.URL.Path, ":page"):
			dataset := strings.Split(r.URL.Path, "/")[3]
			writeJSON(w, 200, map[string]any{"cursor": dataset})
		case strings.HasSuffix(r.URL.Path, ":next"):
			dataset := strings.Split(r.URL.Path, "/")[3]
			var rows []wireRecord
			for i := 0; i < 3; i++ {
				rows = append(rows, wireRecord{ID: recordIdentity{fmt.Sprint(i), dataset}, Type: "com.voptrack.track.chore_completion", Values: Row{{Type: "date_time", Value: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)}}})
			}
			writeJSON(w, 200, recordSet{Meta: Meta{Fields: []Field{{ID: "timestamp", Type: "date_time"}}}, Records: rows})
		case r.Method == "DELETE":
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected Home read: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	a.tessera = &Tessera{internal: srv.URL, http: srv.Client()}
	tpl, err := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix, "localizeName": localizeName, "resultRows": resultRows, "chartLabelVisible": chartLabelVisible, "chartLabelText": chartLabelText}).ParseFS(assets, "templates/layout.html", "templates/chart.html", "templates/overview.html")
	must(t, err)
	a.pages["overview"] = tpl
	for _, path := range []string{"/", "/home?category=house"} {
		w := savedRequest(a, s, "GET", path, nil)
		body := w.Body.String()
		wants := []string{"<h1>Home</h1>", "Browse records"}
		expectedRows := 0
		if path != "/" {
			wants = []string{"Chore completion", "6 entries"}
			expectedRows = 6
		}
		for _, want := range wants {
			if w.Code != 200 || !strings.Contains(body, want) {
				t.Fatalf("Home route omitted prepared state %q", want)
			}
		}
		if strings.Count(body, `class="transaction-row tone-`) != expectedRows {
			t.Fatal("Home must summarize; detailed category browsing must retain all canonical entries")
		}
		if strings.Contains(body, `class="household-context"`) {
			t.Fatal("technical counters returned to the everyday heading")
		}
	}

	for path, want := range map[string]string{"/home?view=directory": "<h1>Browse activities</h1>", "/home?view=subjects": "<h1>Your household</h1>", "/home?view=timeline": "<h1>Activity</h1>"} {
		got := savedRequest(a, s, "GET", path, nil)
		if got.Code != 200 || !strings.Contains(got.Body.String(), want) {
			t.Fatalf("Home view %s missing %q", path, want)
		}
	}
	w := savedRequest(a, s, "GET", "/home?view=timeline&q=does-not-match", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "No matching entries") || strings.Count(w.Body.String(), `class="transaction-row tone-`) != 0 || strings.Contains(w.Body.String(), "6 entries") {
		t.Fatal("empty search fell back to unfiltered values")
	}
}

func TestHomeWorkspaceShowsConnectedDataWithoutSavedViews(t *testing.T) {
	d := workspaceData{Title: "My data", Scope: "mine", Tracks: []connectedTrack{{Name: "Expense", Family: "expenses", Stores: 2, URL: "/tracks?scope=mine&type=expense"}}}
	body := renderUIForTest(t, httptest.NewRequest("GET", "/mine", nil), "workspace", d)
	for _, want := range []string{"My data", "Connected data", "Expense", "2 connected datasets", "scope=mine"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing connected-data context %q", want)
		}
	}
	if strings.Contains(body, "Last 12") || strings.Contains(body, "class=\"chart\"") {
		t.Fatal("automatic raw-value charts remain in My data")
	}
	for _, retired := range []string{"/views", "/dashboards/", "saved views"} {
		if strings.Contains(body, retired) {
			t.Fatal("retired saved-view entry remains", retired)
		}
	}
}

func TestHomeFamilyAndActivityKeepTheirPrimaryNavigation(t *testing.T) {
	for section, want := range map[string]string{"overview": "overview", "expenses": "expenses", "subjects": "subjects", "pets": "subjects", "house": "subjects", "car": "subjects", "other": "subjects", "directory": "directory", "timeline": "directory"} {
		if got := homePrimarySection(section); got != want {
			t.Errorf("%s selected %s, want %s", section, got, want)
		}
	}
}

func TestExpenseRenderExplainsUncategorizedTotalWithoutPercentageBar(t *testing.T) {
	h := &householdView{Section: "expenses", Title: "Expenses", ShowSpending: true, Current: "EUR 124.15", ExpenseCount: 9, UncategorizedCount: 9, AllUncategorized: true, TrendTitle: "Daily spending", ExpenseComparison: "−EUR 340.82 vs 1–5 Oct", Chart: Chart{Empty: true}, CompactChart: Chart{Empty: true}}
	body := renderUIForTest(t, httptest.NewRequest("GET", "/home?view=expenses", nil), "overview", overviewData{Scope: "home", Household: h})
	for _, want := range []string{"Where EUR 124.15 went", "9 expenses have no category", "Daily spending", "Recorded by", "vs 1–5 Oct"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expense screen omitted %q", want)
		}
	}
	if strings.Contains(body, "<meter") {
		t.Fatal("uncategorized-only expenses were drawn as a complete category breakdown")
	}
}
