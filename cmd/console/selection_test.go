package main

import (
	"html"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestSelectionCanonicalDatesAndContext(t *testing.T) {
	for _, tc := range []struct{ query, period, from, month, view string }{
		{"selection=week&from=2026-01-01&to=2026-01-02&month=2026-02", "week", "", "", ""},
		{"selection=calendar&month=2026-02&from=2026-01-01", "month", "", "2026-02", ""},
		{"selection=custom&from=2026-01-01&to=2026-01-02&month=2026-02", "custom", "2026-01-01", "", ""},
		{"selection=month&view=pets&subject=opaque", "month", "", "", "subjects"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			called := false
			h := selectionPage(func(http.ResponseWriter, *http.Request) { called = true })
			r := httptest.NewRequest("GET", "/home?"+tc.query+"&member=member-key&q=food&currency=EUR&spend=pet&bucket=day&offset=20", nil)
			w := httptest.NewRecorder()
			h(w, r)
			if w.Code != 303 || called {
				t.Fatal("selection was not canonicalized")
			}
			u, err := url.Parse(w.Header().Get("Location"))
			must(t, err)
			q := u.Query()
			if u.Path != "/home" || q.Get("period") != tc.period || q.Get("from") != tc.from || q.Get("month") != tc.month || q.Get("view") != tc.view {
				t.Fatal("wrong active dates or destination", u)
			}
			for k, v := range map[string]string{"member": "member-key", "q": "food", "currency": "EUR", "spend": "pet"} {
				if q.Get(k) != v {
					t.Fatal("lost filter", k)
				}
			}
			for _, k := range []string{"selection", "bucket", "offset"} {
				if q.Has(k) {
					t.Fatal("stale navigation state", k)
				}
			}
		})
	}
	for _, query := range []string{"selection=custom", "selection=calendar", "selection=wrong", "selection=week&selection=all"} {
		w := httptest.NewRecorder()
		selectionPage(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid selection reached reader") })(w, httptest.NewRequest("GET", "/home?"+query, nil))
		if w.Code != 400 {
			t.Fatal("invalid selection accepted")
		}
	}
	called := false
	selectionPage(func(http.ResponseWriter, *http.Request) { called = true })(httptest.NewRecorder(), httptest.NewRequest("GET", "/home?from=2026-01-01", nil))
	if !called {
		t.Fatal("legacy dates no longer reach reader")
	}
}

func TestRetiredModerationRoutesCannotMutate(t *testing.T) {
	a, s, f := moderationHTTPApp(t)
	for _, sess := range []*session{nil, s} {
		for _, target := range [][2]string{{"GET", "/home/moderation"}, {"POST", "/home/moderation/preview"}, {"POST", "/home/moderation/confirm"}} {
			w := savedRequest(a, sess, target[0], target[1], url.Values{"csrf": {s.CSRF}, "dataset": {"one"}, "track": {"track"}})
			if w.Code != 404 {
				t.Fatalf("retired %s %s returned %d", target[0], target[1], w.Code)
			}
		}
	}
	if f.mutations != 0 || f.previews != 0 || f.privateReads != 0 {
		t.Fatal("retired routes reached sharing API")
	}
}

func TestHomeUsesOneSelectionForm(t *testing.T) {
	for _, section := range []string{"overview", "expenses", "pets", "timeline"} {
		h := &householdView{Title: "Home", Section: section, Controls: exploreControls{
			View: section, Range: "Month so far", Period: "month",
			Subjects: []exploreOption{{Name: "Everyone and everything"}, {ID: "opaque", Name: "Rex"}},
		}}
		body := renderUIForTest(t, httptest.NewRequest("GET", "/home?view="+section, nil), "overview", overviewData{Scope: "home", Household: h})
		if strings.Count(body, "class=\"selection-panel\"") != 1 || strings.Count(body, "name=\"selection\"") != 1 || strings.Count(body, "name=\"subject\"") != 1 {
			t.Fatal("duplicated or missing selection control", section)
		}
		for _, old := range []string{"Search and filter", "class=\"dashboard-date\"", "class=\"home-add-link\"", "Saved views", "href=\"/views", "Build a saved view"} {
			if strings.Contains(body, old) {
				t.Fatal("legacy page controls remain", section, old)
			}
		}
	}
}

func TestHomeSettingsReviewExport(t *testing.T) {
	dir := os.Getenv("VOPTRACK_UI_REVIEW_DIR")
	if dir == "" {
		t.Skip("optional synthetic settings export")
	}
	a, s, _, _, _, _ := backupTestApp(t)
	a.cfg.Variant = "household"
	for _, page := range []string{"backups", "manage"} {
		var body []byte
		if page == "backups" {
			w := backupRequest(a, s, "GET", "/backups", nil)
			if w.Code != 200 {
				t.Fatal("backup render failed", w.Code)
			}
			body = w.Body.Bytes()
		} else {
			tpl, err := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix}).ParseFS(assets, "templates/layout.html", "templates/manage.html")
			must(t, err)
			a.pages["manage"] = tpl
			w := httptest.NewRecorder()
			a.render(w, httptest.NewRequest("GET", "/manage", nil), s, "manage", 200, nil)
			body = w.Body.Bytes()
		}
		must(t, os.WriteFile(filepath.Join(dir, page+".html"), body, 0600))
	}
}

func TestFilteredHomeHasOneCurrentNavigationDestination(t *testing.T) {
	a, s, old := policyHTTPApp(t)
	old.Close()
	a.cfg.Variant = "household"
	tpl, err := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix, "localizeName": localizeName, "resultRows": resultRows, "chartLabelVisible": chartLabelVisible, "chartLabelText": chartLabelText}).ParseFS(assets, "templates/layout.html", "templates/chart.html", "templates/overview.html")
	must(t, err)
	a.pages["overview"] = tpl
	sub := householdSubject{Key: "opaque", Name: "Rex", Monogram: "R", URL: "/home?subject=opaque"}
	for _, tc := range []struct{ section, current string }{{"overview", "Rex"}, {"expenses", "Spending"}} {
		h := &householdView{Section: tc.section, Title: tc.current, HasExpenses: true, SelectedSubject: &sub, FilterSubject: sub.Key, Subjects: []householdSubject{sub}}
		w := httptest.NewRecorder()
		a.render(w, httptest.NewRequest("GET", "/home?view="+tc.section+"&subject=opaque", nil), s, "overview", 200, overviewData{Scope: "home", Household: h})
		body := w.Body.String()
		start := strings.Index(body, "<nav class=\"desktop-nav\"")
		if start < 0 {
			t.Fatal("signed-in navigation missing")
		}
		nav := strings.SplitN(body[start:], "</nav>", 2)[0]
		index := strings.Index(nav, "aria-current=")
		if index < 0 {
			t.Fatal("no current destination")
		}
		active := strings.SplitN(nav[index:], "</a>", 2)[0]
		if strings.Count(nav, "aria-current=\"page\"") != 1 || !strings.Contains(active, tc.current) {
			t.Fatal("filtered view selected multiple or wrong sidebar destinations", tc.section)
		}
	}
}

// Check the actual GET forms: search edits must not require applying the filters,
// and filter submissions must retain the previously applied query.
func TestSearchAndSelectionSubmitIndependently(t *testing.T) {
	for _, path := range []string{"/home", "/tracks"} {
		for _, dates := range []string{"period=week", "period=month&month=2026-10", "period=custom&from=2026-10-01&to=2026-10-12"} {
			t.Run(path+dates, func(t *testing.T) {
				q, err := url.ParseQuery(dates + "&view=expenses&scope=home&type=expense&series=money&category=finance&subject=opaque&member=synthetic&spend=pet&currency=EUR&q=vet&bucket=2026-10-02&offset=20")
				must(t, err)
				now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
				w, ok := requestedWindow(q, now)
				if !ok {
					t.Fatal("invalid test period")
				}
				c := controls(q, nil, w, path)
				c.Subjects = []exploreOption{{ID: "opaque", Name: "Rex", Selected: true}}
				c.Categories = []exploreOption{{ID: "pet", Name: "Pet care", Selected: true}}
				tpl, err := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix}).ParseFS(assets, "templates/layout.html")
				must(t, err)
				var out strings.Builder
				must(t, tpl.ExecuteTemplate(&out, "selection_controls", c))
				body := out.String()
				form := func(class string) string {
					start := strings.Index(body, `class="`+class+`"`)
					if start < 0 {
						t.Fatal("form missing", class)
					}
					return strings.SplitN(body[start:], "</form>", 2)[0]
				}
				search, selection := form("entry-search-form"), form("selection-form")
				inputs := func(form string) url.Values {
					v := url.Values{}
					for _, input := range regexp.MustCompile(`<input[^>]*>`).FindAllString(form, -1) {
						attrs := map[string]string{}
						for _, pair := range regexp.MustCompile(`([a-z-]+)="([^"]*)"`).FindAllStringSubmatch(input, -1) {
							attrs[pair[1]] = html.UnescapeString(pair[2])
						}
						if attrs["name"] != "" {
							v.Add(attrs["name"], attrs["value"])
						}
					}
					return v
				}
				sv, fv := inputs(search), inputs(selection)
				for _, key := range []string{"period", "month", "from", "to", "view", "scope", "type", "series", "category", "subject", "member", "spend", "currency", "q"} {
					if sv.Get(key) != q.Get(key) || len(sv[key]) > 1 {
						t.Fatal("search lost or duplicated context", key)
					}
				}
				for _, key := range []string{"selection", "offset", "bucket"} {
					if sv.Has(key) {
						t.Fatal("search retained stale selection or pagination", key)
					}
				}
				if strings.Contains(selection, `type="search"`) || fv.Get("q") != "vet" {
					t.Fatal("search still belongs to filters or applied query is lost")
				}
				if strings.Count(body, `role="search"`) != 1 || !strings.Contains(body, "Searching for “<strong>vet</strong>”") {
					t.Fatal("search status or landmark missing")
				}
				// Submit a changed search without touching the applied filter values.
				sv.Set("q", "food")
				if sv.Get("member") != "synthetic" || sv.Get("subject") != "opaque" || sv.Get("spend") != "pet" {
					t.Fatal("search changed selection")
				}
				// Submit a changed filter through the canonical date handler.
				fv.Set("selection", "year")
				fv.Set("member", "other")
				resp := httptest.NewRecorder()
				selectionPage(func(http.ResponseWriter, *http.Request) { t.Fatal("selection did not redirect") })(resp, httptest.NewRequest("GET", path+"?"+fv.Encode(), nil))
				target, err := url.Parse(resp.Header().Get("Location"))
				must(t, err)
				if resp.Code != 303 || target.Query().Get("q") != "vet" || target.Query().Get("member") != "other" || target.Query().Get("period") != "year" {
					t.Fatal("filter submission changed search")
				}
				clearSearch, err := url.Parse(c.SearchClearURL)
				must(t, err)
				clearFilters, err := url.Parse(c.ClearURL)
				must(t, err)
				for _, key := range []string{"q", "bucket", "offset"} {
					if clearSearch.Query().Has(key) {
						t.Fatal("clear search retained stale state", key)
					}
				}
				for _, key := range []string{"period", "month", "from", "to", "subject", "member", "spend", "currency", "scope", "type", "series", "category", "view"} {
					if clearSearch.Query().Get(key) != q.Get(key) {
						t.Fatal("clear search changed a filter", key)
					}
				}
				if clearFilters.Query().Get("q") != "vet" || clearFilters.Query().Has("member") {
					t.Fatal("clear filters changed search or retained member")
				}
			})
		}
	}
}

func TestSearchOnlyIsNotMarkedFilteredAndEscapesQuery(t *testing.T) {
	c := controls(url.Values{"q": {`<img src=x onerror=alert(1)>`}}, nil, periodWindow("month", time.Now()), "/home")
	tpl, err := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix}).ParseFS(assets, "templates/layout.html")
	must(t, err)
	var out strings.Builder
	must(t, tpl.ExecuteTemplate(&out, "selection_controls", c))
	body := out.String()
	if strings.Contains(body, " · Filtered") || strings.Contains(body, "<img src=x") || c.ClearURL != "" || c.SearchClearURL == "" {
		t.Fatal("query was not escaped or search is still treated as a filter")
	}
}

func TestHomeSavedViewRoutesAreRetiredWithoutChangingDefinitions(t *testing.T) {
	a, owner, server := savedResearchApp(t)
	defer server.Close()
	id, err := a.saved.addPanel("home", "Synthetic retained view", "scope=home&type=synthetic&measure=count&group=store")
	must(t, err)
	before, found, err := a.saved.docs.get("dashboards/home.json")
	must(t, err)
	if !found {
		t.Fatal("stored definition fixture missing")
	}
	for _, variant := range []string{"household", "standard"} {
		a.cfg.Variant = variant
		for _, sess := range []*session{nil, owner} {
			for _, target := range [][2]string{{"GET", "/views"}, {"GET", "/views?scope=mine"}, {"POST", "/dashboards/add"}, {"GET", "/dashboards/home/" + id}, {"GET", "/dashboards/mine/" + id}, {"POST", "/dashboards/home/" + id + "/remove"}} {
				w := savedRequest(a, sess, target[0], target[1], url.Values{"csrf": {owner.CSRF}, "panel": {"scope=home&type=synthetic&measure=count&group=store"}, "name": {"Other"}, "index": {"0"}})
				if w.Code != 404 {
					t.Fatalf("retired route returned %d", w.Code)
				}
			}
		}
	}
	after, _, err := a.saved.docs.get("dashboards/home.json")
	must(t, err)
	if string(before) != string(after) {
		t.Fatal("retired routes mutated stored definitions")
	}
}
