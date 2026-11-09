package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// A portable signed 32-bit integer is the policy's storage/input limit.
// It is a technical bound, not a privacy rule or an enrolment limit.
const maxSuppressionK = 2147483647

type suppressionPolicy struct {
	Version  int       `json:"version"`
	K        int       `json:"k"`
	Revision uint64    `json:"revision"`
	Changed  time.Time `json:"changed"`
}
type policyStore struct {
	mu   sync.Mutex
	docs docStore
}

func parseThreshold(text string) (int, error) {
	if text == "" || len(text) > 10 {
		return 0, errors.New("policy_invalid")
	}
	for _, c := range text {
		if c < '0' || c > '9' {
			return 0, errors.New("policy_invalid")
		}
	}
	n, err := strconv.ParseUint(text, 10, 31)
	if err != nil || n < 2 || n > maxSuppressionK {
		return 0, errors.New("policy_invalid")
	}
	return int(n), nil
}
func policyPath(slug string) (string, error) {
	if !validSlug.MatchString(slug) {
		return "", errors.New("policy_unavailable")
	}
	return "research-policies/" + slug + ".json", nil
}
func (s *policyStore) read(slug string) (suppressionPolicy, bool, error) {
	name, err := policyPath(slug)
	if err != nil {
		return suppressionPolicy{}, false, err
	}
	raw, found, err := s.docs.get(name)
	if err != nil || !found {
		return suppressionPolicy{}, found, err
	}
	var p suppressionPolicy
	if json.Unmarshal(raw, &p) != nil || p.Version != 1 || p.K < 2 || p.K > maxSuppressionK || p.Revision == 0 || p.Changed.IsZero() {
		return suppressionPolicy{}, true, errors.New("policy_unavailable")
	}
	return p, true, nil
}
func (s *policyStore) load(slug string) (suppressionPolicy, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(slug)
}
func (s *policyStore) save(slug, text, revision string, lower bool) (suppressionPolicy, error) {
	k, err := parseThreshold(text)
	if err != nil {
		return suppressionPolicy{}, err
	}
	rev, err := strconv.ParseUint(revision, 10, 64)
	if err != nil {
		return suppressionPolicy{}, errors.New("policy_stale")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, _, err := s.read(slug)
	if err != nil {
		return old, errors.New("policy_unavailable")
	}
	if old.Revision != rev || rev == ^uint64(0) {
		return old, errors.New("policy_stale")
	}
	if old.Revision > 0 && k < old.K && !lower {
		return old, errors.New("policy_lower_required")
	}
	p := suppressionPolicy{Version: 1, K: k, Revision: rev + 1, Changed: time.Now().UTC()}
	raw, _ := json.Marshal(p)
	name, _ := policyPath(slug)
	if err := s.docs.put(name, raw); err != nil {
		return old, errors.New("policy_save_failed")
	}
	return p, nil
}

type policyPageData struct {
	Study     Study
	Threshold string
	Revision  uint64
	Current   int
	Error     string
}

func (a *app) studyPolicyPage(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.researcher(w, r)
	if !ok {
		return
	}
	st, ok := a.studies.get(r.PathValue("slug"))
	if !ok {
		a.fail(w, r, sess, 404, "no_study", nil)
		return
	}
	p, found, err := a.policies.load(st.Slug)
	d := policyPageData{Study: st, Revision: p.Revision, Current: p.K}
	if found && err == nil {
		d.Threshold = strconv.Itoa(p.K)
	}
	if err != nil {
		d.Error = "policy_unavailable"
	}
	a.render(w, r, sess, "study_policy", 200, d)
}
func (a *app) saveStudyPolicy(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.researcher(w, r)
	if !ok {
		return
	}
	if !sess.checkCSRF(r) {
		a.fail(w, r, sess, 403, "expired", nil)
		return
	}
	st, ok := a.studies.get(r.PathValue("slug"))
	if !ok {
		a.fail(w, r, sess, 404, "no_study", nil)
		return
	}
	text := r.PostFormValue("threshold")
	_, err := a.policies.save(st.Slug, text, r.PostFormValue("revision"), r.PostFormValue("confirm_lower") == "yes")
	if err == nil {
		http.Redirect(w, r, "/studies/"+st.Slug+"/policy", http.StatusSeeOther)
		return
	}
	p, _, _ := a.policies.load(st.Slug)
	// A stale submission is never automatically rebased. Display the current
	// revision for a new explicit submission, retaining the entered threshold.
	d := policyPageData{Study: st, Threshold: text, Revision: p.Revision, Current: p.K, Error: err.Error()}
	a.render(w, r, sess, "study_policy", http.StatusUnprocessableEntity, d)
}

// pointValue is the only value accessor used by charts and formatted rows.
// Research Points contain no raw Tally or contributor identities after release.
func pointValue(res Result, p Point) *big.Rat {
	if res.Research {
		return p.ReleasedValue
	}
	if res.Spec.Measure == MeasureLatest {
		return p.LatestValue
	}
	return p.Tally.Value(res.Spec.Measure, res.Spec.Field)
}
func withheld(spec Spec, p suppressionPolicy, status string) Result {
	spec.Research = false
	return Result{Spec: spec, Research: true, K: p.K, Revision: p.Revision, Status: status,
		Points: []Point{{Status: status}}}
}

// releaseResearch never returns hidden tally values, labels or contributor sets.
// Empty and all-null calculations are distinguished internally, but receive the
// same public suppression status: an "exactly zero" notice would bypass K.
func releaseResearch(raw Result, p suppressionPolicy, err error) Result {
	if err != nil || raw.Truncated || len(raw.Skipped) > 0 {
		return withheld(raw.Spec, p, "unavailable")
	}
	if raw.Unsupported {
		return withheld(raw.Spec, p, "unsupported")
	}
	out := Result{Spec: raw.Spec, Research: true, K: p.K, Revision: p.Revision, Status: "released"}
	out.Spec.Research = false
	if len(raw.Points) == 0 {
		return withheld(raw.Spec, p, "suppressed")
	}
	hidden := false
	for _, pt := range raw.Points {
		value := pt.Tally.Value(raw.Spec.Measure, raw.Spec.Field)
		if len(pt.contributors) < p.K || value == nil {
			if raw.Spec.Group == "spread" {
				return withheld(raw.Spec, p, "suppressed")
			}
			out.Status = "suppressed"
			if raw.Spec.Group == "period" {
				out.Points = append(out.Points, Point{Label: pt.Label, Status: "suppressed"})
			} else {
				hidden = true
			}
			continue
		}
		released := Point{Label: pt.Label, Status: "released", ReleasedValue: new(big.Rat).Set(value)}
		if raw.Spec.Field == "" {
			released.Records = strconv.FormatInt(pt.Tally.Rows, 10)
			released.Values = strconv.FormatInt(pt.Tally.Values, 10)
		}
		out.Points = append(out.Points, released)
	}
	if hidden {
		out.Points = append(out.Points, Point{Status: "suppressed"})
	}
	return out
}

type researchInput struct {
	cat     Catalog
	sources []Source
	policy  suppressionPolicy
	status  string
}

// Fixed study metadata supplies the form even when there are no readable stores.
// It does not turn failed server metadata reads into a successful empty result.
func studyCatalog(st Study) (Catalog, error) {
	defs, err := studyTracks(st.Tracks)
	if err != nil {
		return Catalog{}, err
	}
	defs = append([]TypeDef{subjectType(), sourceType()}, defs...)
	var cat Catalog
	for _, d := range defs {
		info := TypeInfo{ID: d.ID, Name: d.Name}
		fields := d.Fields
		if d.ParentID != nil {
			fields = append(append([]TypeField{}, trackRoot().Fields...), fields...)
		}
		for _, f := range fields {
			ff := Field{ID: f.ID, Name: f.Name, Type: f.Type}
			switch f.Type {
			case "integer", "float", "fixed":
				info.Numeric = append(info.Numeric, ff)
			case "date_time":
				info.Dates = append(info.Dates, ff)
			case "text", "reference":
				info.Groups = append(info.Groups, ff)
			}
		}
		cat.Types = append(cat.Types, info)
	}
	return cat, nil
}
func (a *app) researchInput(ctx context.Context, sess *session, st Study) researchInput {
	cat, err := studyCatalog(st)
	in := researchInput{cat: cat}
	if err != nil {
		in.status = "unavailable"
		return in
	}
	p, found, err := a.policies.load(st.Slug)
	in.policy = p
	if err != nil {
		in.status = "unavailable"
		return in
	}
	if !found {
		in.status = "unconfigured"
		return in
	}
	in.sources, err = a.discoverStudySources(ctx, sess, st.Slug)
	if err != nil {
		in.status = "unavailable"
		return in
	}
	if len(in.sources) > 0 {
		actual, skipped := catalog(ctx, a.tessera, in.sources)
		if len(skipped) > 0 {
			in.status = "unavailable"
			return in
		}
		// The actual catalog includes effective fields and any support types.
		for i := range actual.Types {
			actual.Types[i].Stores = nil
		}
		in.cat = actual
	}
	return in
}
func (a *app) researchAnswer(ctx context.Context, in researchInput, spec Spec) Result {
	if in.status != "" {
		return withheld(spec, in.policy, in.status)
	}
	spec.Research = true
	raw, err := compute(ctx, a.tessera, in.sources, spec)
	return releaseResearch(raw, in.policy, err)
}
func (a *app) researchPanelResult(w http.ResponseWriter, r *http.Request, sess *session, slug string) (*session, string, Spec, TypeInfo, Result, bool) {
	st, found := a.studies.get(slug)
	if !found {
		a.fail(w, r, sess, 404, "no_study", nil)
		return nil, "", Spec{}, TypeInfo{}, Result{}, false
	}
	in := a.researchInput(r.Context(), sess, st)
	spec, info, err := a.parseSpec(r, in.cat)
	if err != nil {
		a.fail(w, r, sess, 404, "no_tracks", nil)
		return nil, "", spec, info, Result{}, false
	}
	res := a.researchAnswer(r.Context(), in, spec)
	return sess, studyScopePrefix + slug, spec, info, res, true
}
func (a *app) researchOverview(w http.ResponseWriter, r *http.Request, sess *session, slug string) {
	st, found := a.studies.get(slug)
	if !found {
		a.fail(w, r, sess, 404, "no_study", nil)
		return
	}
	in := a.researchInput(r.Context(), sess, st)
	data := overviewData{Scope: studyScopePrefix + slug, Status: in.status, K: in.policy.K, StudyTitle: st.Title}
	if a.saved != nil {
		var err error
		data.Saved, err = a.saved.load("study-" + slug)
		data.SavedError = err != nil
	}
	now := time.Now().In(a.tz)
	yearAgo := time.Date(now.Year()-1, now.Month()+1, 1, 0, 0, 0, 0, a.tz)
	if in.status == "" {
		for _, info := range in.cat.Types {
			spec := Spec{Type: info.ID, Measure: MeasureCount, Group: "store", TZ: a.tz}
			res := a.researchAnswer(r.Context(), in, spec)
			c := card{Type: info, PerStore: res, PerChart: buildChart(res, false), PerLink: panelLink(data.Scope, spec)}
			if len(info.Numeric) > 0 && len(info.Dates) > 0 {
				f := info.Numeric[0]
				trend := Spec{Type: info.ID, Field: f.ID, Measure: defaultMeasure(f), Group: "period", Period: "month", TZ: a.tz, From: yearAgo, Date: info.Dates[0].ID}
				tr := a.researchAnswer(r.Context(), in, trend)
				c.Trend = &tr
				c.Chart = buildChart(tr, true)
				c.Link = panelLink(data.Scope, trend)
				c.Measure = "m_" + string(trend.Measure)
				c.Field = fieldName(f)
			}
			data.Cards = append(data.Cards, c)
		}
	}
	a.render(w, r, sess, "overview", 200, data)
}

// Research panel CSV v1 preserves the four existing columns in their order.
// The additive fifth column is a stable English release status. All auxiliary
// numeric-field counts are blank; the selected count measure remains in "value".
func writeResearchCSV(out *csv.Writer, res Result) {
	s := res.Spec
	out.Write([]string{"# track", s.Type})
	out.Write([]string{"# measure", string(s.Measure), s.Field})
	out.Write([]string{"# grouped by", s.Group, s.Period, s.TZ.String()})
	k, revision := "", ""
	if res.K >= 2 {
		k = strconv.Itoa(res.K)
	}
	if res.Revision > 0 {
		revision = strconv.FormatUint(res.Revision, 10)
	}
	out.Write([]string{"# research suppression", "1", k, revision, res.Status})
	from, to := "", ""
	if !s.From.IsZero() {
		from = s.From.UTC().Format(time.RFC3339Nano)
	}
	if !s.To.IsZero() {
		to = s.To.UTC().Format(time.RFC3339Nano)
	}
	out.Write([]string{"# window", s.Date, from, to, "inclusive/exclusive"})
	out.Write([]string{"group", "value", "records", "values", "status"})
	for _, p := range res.Points {
		out.Write([]string{cell(p.Label), exact(pointValue(res, p)), p.Records, p.Values, p.Status})
	}
}
