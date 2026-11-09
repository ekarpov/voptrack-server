package main

import (
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func renderUIForTest(t *testing.T, r *http.Request, page string, data any) string {
	t.Helper()
	tpl, err := template.New("layout.html").Funcs(template.FuncMap{
		"hasPrefix": strings.HasPrefix, "formErrorKey": formErrorKey, "catalogueLabel": catalogueLabel, "resultRows": resultRows, "designerProblem": designerProblem, "chartLabelVisible": chartLabelVisible, "chartLabelText": chartLabelText, "localizeName": localizeName, "localizePanelTitle": localizePanelTitle,
	}).ParseFS(assets, "templates/layout.html", "templates/chart.html", "templates/design_fields.html", "templates/"+page+".html")
	if err != nil {
		t.Fatal(err)
	}
	a := &app{pages: map[string]*template.Template{page: tpl}}
	w := httptest.NewRecorder()
	a.render(w, r, nil, page, http.StatusOK, data)
	if w.Code != http.StatusOK {
		t.Fatalf("render returned %d", w.Code)
	}
	return w.Body.String()
}

// Deferred locale requests must neither expose a switcher nor change the
// selected panel or invitation. Retained catalogs are checked independently.
func TestEnglishReleaseIgnoresLocalePreferences(t *testing.T) {
	for _, uri := range []string{"/panel?scope=mine&type=synthetic.spending&lang=de", "/study/synthetic/join?code=synthetic%2Bcode&lang=ru"} {
		r := httptest.NewRequest(http.MethodGet, uri, nil)
		r.Header.Set("Accept-Language", "fi,ru;q=0.9")
		original := r.URL.RawQuery
		body := renderUIForTest(t, r, "landing", nil)
		if !strings.Contains(body, `<html lang="en">`) || strings.Contains(body, `class="language-menu"`) || strings.Contains(body, `?lang=`) {
			t.Fatal("English release exposed alternate locale navigation")
		}
		if r.URL.RawQuery != original {
			t.Fatal("render changed the request query")
		}
	}
}
func TestPostMessageHasNoDeferredLanguageLinks(t *testing.T) {
	body := renderUIForTest(t, httptest.NewRequest(http.MethodPost, "/logout?lang=de", nil), "message", map[string]any{"Key": "signed_out"})
	if strings.Contains(body, `?lang=`) || !strings.Contains(body, `lang="en"`) {
		t.Fatal("message exposed a deferred locale")
	}
}

func TestStudyFieldErrorIsLocalizedAndAssociatedWithControl(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/studies?error=a+study+needs+a+title", nil)
	r.Header.Set("Accept-Language", "de")
	body := renderUIForTest(t, r, "studies", map[string]any{"Error": "a study needs a title"})
	if !strings.Contains(body, messages["en"]["form_title_required"]) || strings.Contains(body, "a study needs a title") {
		t.Fatal("field error is not translated")
	}
	if !strings.Contains(body, `aria-invalid="true" aria-describedby="error-title"`) ||
		!strings.Contains(body, `id="error-title"`) {
		t.Fatal("title error is not associated with its input")
	}
}

// A notice is only shown after its archive name has been validated. Language
// links must not echo an arbitrary name supplied in the address either.
func TestLanguageLinksDoNotRepeatUnvalidatedBackupNotice(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/backups?taken=made-up.tar", nil)
	body := renderUIForTest(t, r, "backups", map[string]any{"Enabled": false})
	if strings.Contains(body, "made-up.tar") {
		t.Fatal("language menu repeated an unvalidated backup name")
	}
}

func TestDenseOverviewKeepsEveryResultEvenWhenChartCannotDraw(t *testing.T) {
	for _, count := range []int{65, maxChartPoints + 1} {
		res := Result{Spec: Spec{Measure: MeasureCount}}
		for i := 0; i < count; i++ {
			res.Points = append(res.Points, Point{Label: fmt.Sprintf("synthetic-store-%03d", i), Tally: Tally{Rows: int64(i + 1)}})
		}
		chart := buildChart(res, false)
		c := card{Type: TypeInfo{Name: "Synthetic track"}, PerStore: res, PerChart: chart}
		prepareHomeCard(&c)
		body := renderUIForTest(t, httptest.NewRequest(http.MethodGet, "/home", nil), "overview",
			overviewData{Scope: "home", HomeReady: true, Cards: []card{c}})
		for _, point := range res.Points {
			if !strings.Contains(body, "<th scope=\"row\">"+point.Label+"</th>") {
				t.Fatalf("overview omitted %q from its exact-value table", point.Label)
			}
		}
		if count <= maxChartPoints {
			if strings.Count(body, "<rect class=\"bar\"") != 2*count {
				t.Fatal("dense chart dropped data points")
			}
			if strings.Count(body, "<text class=\"axis\"") > 12 {
				t.Fatal("dense chart labels still overlap")
			}
		}
	}
}

// A saved panel's generated caption must use the language selected now,
// even when its caption was assembled before the render updated that choice.
func TestSavedPanelCaptionUsesCurrentLanguageAndCatalogueLabels(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/dashboards/mine/synthetic?lang=de", nil)
	body := renderUIForTest(t, r, "dashboard", dashboardData{
		Board: Dashboard{Name: "Synthetic board"}, Scope: "mine",
		Panels: []dashboardPanel{{Title: "strings.track.weight · strings.track.weight.field.weight · Average",
			Result: Result{Spec: Spec{Measure: MeasureAverage}}, Chart: Chart{Empty: true},
			Rows: []panelRow{{Label: "Synthetic", Value: "7.42"}}}},
	})
	if strings.Contains(body, "strings.track.weight") || strings.Contains(body, "Gewicht") {
		t.Fatal("saved panel exposed an app string key or stale language")
	}
	if !strings.Contains(body, "Weight · Weight · Average") {
		t.Fatal("caption is not localized")
	}
}
