package main

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Study authority is checked at every route, before reading definitions.
func (a *app) savedStudy(w http.ResponseWriter, r *http.Request, scope string) (*session, Study, bool) {
	sess, ok := a.researcher(w, r)
	if !ok {
		return sess, Study{}, false
	}
	slug, valid := studySlug(scope)
	if !valid {
		a.fail(w, r, sess, 400, "no_study", nil)
		return sess, Study{}, false
	}
	st, found := a.studies.get(slug)
	if !found || st.Slug != slug {
		a.fail(w, r, sess, 404, "no_study", nil)
		return sess, Study{}, false
	}
	return sess, st, true
}

// Validate against fixed study definitions without discovering participant data.
// parseSpec's forgiving interactive defaults must not reinterpret a saved question.
func (a *app) studyPanel(raw, scope string, cat Catalog) (Spec, TypeInfo, error) {
	panel, actual, err := cleanPanel(raw)
	if err != nil || actual != scope {
		return Spec{}, TypeInfo{}, errors.New("invalid study panel")
	}
	q, _ := url.ParseQuery(panel)
	spec, info, err := a.parseSpecValues(q, cat)
	if err != nil {
		return spec, info, err
	}
	if v := q.Get("measure"); v != "" && (!validMeasure(v) || Measure(v) != spec.Measure) {
		return spec, info, errors.New("invalid measure")
	}
	for key, value := range map[string]string{"field": spec.Field, "group": spec.Group, "period": spec.Period, "date": spec.Date, "tz": spec.TZ.String()} {
		if v := q.Get(key); v != "" && v != value {
			return spec, info, errors.New("invalid panel selection")
		}
	}
	for _, key := range []string{"from", "to"} {
		if v := q.Get(key); v != "" {
			if _, err := time.ParseInLocation("2006-01-02", v, spec.TZ); err != nil {
				return spec, info, errors.New("invalid date")
			}
		}
	}
	if !spec.From.IsZero() && !spec.To.IsZero() && !spec.From.Before(spec.To) {
		return spec, info, errors.New("invalid window")
	}
	return spec, info, nil
}

type saveStudyData struct {
	Scope, StudyTitle, Query, Name, Error, Back string
	Saved                                       []Dashboard
}

func (a *app) addToStudyDashboard(w http.ResponseWriter, r *http.Request, _ *session, panel, scope string) {
	sess, st, ok := a.savedStudy(w, r, scope)
	if !ok {
		return
	}
	cat, err := studyCatalog(st)
	if err != nil {
		a.savedStudyError(w, r, sess, scope, 502, "dashboard_load_failed")
		return
	}
	spec, _, err := a.studyPanel(panel, scope, cat)
	if err != nil {
		a.render(w, r, sess, "save_study_panel", 422, saveStudyData{Scope: scope, StudyTitle: st.Title, Query: panel, Name: r.PostFormValue("name"), Error: "dashboard_panel_invalid", Back: "/studies/" + st.Slug + "/dashboard"})
		return
	}
	// Canonical links use the inclusive displayed date, not the internal bound.
	panel = strings.TrimPrefix(panelLink(scope, spec), "/panel?")
	d := saveStudyData{Scope: scope, StudyTitle: st.Title, Query: panel, Name: r.PostFormValue("name"), Back: panelLink(scope, spec)}
	key := "study-" + st.Slug
	d.Saved, err = a.saved.load(key)
	status := 422
	if err != nil {
		d.Error = "dashboard_load_failed"
		status = 502
	} else if strings.TrimSpace(d.Name) == "" || len([]rune(strings.TrimSpace(d.Name))) > maxName {
		d.Error = "dashboard_name_err"
	} else {
		id, err := a.saved.addPanel(key, d.Name, panel)
		if err == nil {
			http.Redirect(w, r, "/dashboards/"+scope+"/"+id, 303)
			return
		}
		if errors.Is(err, errFull) {
			d.Error = "dashboards_full"
			status = 409
		} else {
			d.Error = "dashboard_save_failed"
			status = 502
		}
	}
	a.render(w, r, sess, "save_study_panel", status, d)
}

func (a *app) studySavedDashboard(w http.ResponseWriter, r *http.Request) {
	scope := r.PathValue("scope")
	sess, st, ok := a.savedStudy(w, r, scope)
	if !ok {
		return
	}
	list, err := a.saved.load("study-" + st.Slug)
	if err != nil {
		a.savedStudyError(w, r, sess, scope, 502, "dashboard_load_failed")
		return
	}
	var board Dashboard
	found := false
	for _, b := range list {
		if b.ID == r.PathValue("id") {
			board = b
			found = true
			break
		}
	}
	if !found {
		a.fail(w, r, sess, 404, "no_dashboard", nil)
		return
	}
	in := a.researchInput(r.Context(), sess, st)
	fixed, fixedErr := studyCatalog(st)
	d := dashboardData{Scope: scope, Board: board, Token: dashboardToken(board), CanEdit: true, Back: "/studies/" + st.Slug + "/dashboard", StudyTitle: st.Title}
	for _, raw := range board.Panels {
		spec, info, err := a.studyPanel(raw, scope, in.cat)
		if err != nil {
			// A failed discovery must not imply a removed track or field. Fixed
			// definitions also let management work while policy/data are unavailable.
			if in.status != "" && fixedErr == nil {
				spec, info, err = a.studyPanel(raw, scope, fixed)
			}
			if err != nil {
				if in.status != "" || fixedErr != nil {
					d.Panels = append(d.Panels, dashboardPanel{Result: withheld(Spec{}, in.policy, "unavailable")})
					continue
				}
				d.Panels = append(d.Panels, dashboardPanel{Gone: true})
				continue
			}
		}
		res := a.researchAnswer(r.Context(), in, spec)
		title := info.Name + " · " + string(spec.Measure)
		for _, f := range info.Numeric {
			if f.ID == spec.Field {
				title = info.Name + " · " + fieldName(f) + " · " + string(spec.Measure)
			}
		}
		p := dashboardPanel{Title: title, Link: panelLink(scope, spec), Result: res, Chart: buildChart(res, spec.Group == "period"), Rows: resultRows(res)}
		d.Panels = append(d.Panels, p)
	}
	a.render(w, r, sess, "dashboard", 200, d)
}

func (a *app) removeFromStudyDashboard(w http.ResponseWriter, r *http.Request) {
	scope := r.PathValue("scope")
	sess, st, ok := a.savedStudy(w, r, scope)
	if !ok {
		return
	}
	if !sess.checkCSRF(r) {
		a.fail(w, r, sess, 403, "expired", nil)
		return
	}
	index, err := strconv.Atoi(r.PostFormValue("index"))
	if err != nil {
		a.fail(w, r, sess, 400, "dashboard_stale", nil)
		return
	}
	err = a.saved.removeStudyPanel("study-"+st.Slug, r.PathValue("id"), r.PostFormValue("version"), index)
	if err != nil {
		if errors.Is(err, errDashboardStale) {
			a.savedStudyError(w, r, sess, scope, 409, "dashboard_stale")
		} else {
			a.savedStudyError(w, r, sess, scope, 502, "dashboard_save_failed")
		}
		return
	}
	list, err := a.saved.load("study-" + st.Slug)
	if err != nil {
		a.savedStudyError(w, r, sess, scope, 502, "dashboard_load_failed")
		return
	}
	for _, b := range list {
		if b.ID == r.PathValue("id") {
			http.Redirect(w, r, "/dashboards/"+scope+"/"+b.ID, 303)
			return
		}
	}
	http.Redirect(w, r, "/studies/"+st.Slug+"/dashboard", 303)
}

func (a *app) savedStudyError(w http.ResponseWriter, r *http.Request, sess *session, scope string, status int, key string) {
	slug, _ := studySlug(scope)
	retry := "/studies/" + slug + "/dashboard"
	if id := r.PathValue("id"); id != "" {
		retry = "/dashboards/" + scope + "/" + url.PathEscape(id)
	}
	a.render(w, r, sess, "message", status, map[string]string{"Key": key, "Retry": retry, "RetryLabel": "policy_retry"})
}
