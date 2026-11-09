package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type researchStudyContext struct{ Slug, Title, Tab, Query string }

func (a *app) researchStudyContext(r *http.Request) *researchStudyContext {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "studies" {
		return nil
	}
	st, ok := a.studies.get(parts[1])
	if !ok {
		return nil
	}
	tab := "settings"
	switch parts[2] {
	case "dashboard":
		tab = "overview"
	case "participants":
		tab = "participants"
	case "exports":
		tab = "exports"
	}
	q := url.Values{}
	for _, key := range []string{"period", "month", "from", "to", "status", "activity", "collection", "q"} {
		if v := r.URL.Query().Get(key); v != "" {
			q.Set(key, v)
		}
	}
	suffix := ""
	if len(q) > 0 {
		suffix = "?" + q.Encode()
	}
	return &researchStudyContext{st.Slug, st.Title, tab, suffix}
}
func (a *app) monitorStudies() ([]Study, bool) {
	files, err := a.studies.docs.list("studies/")
	if err != nil {
		return nil, true
	}
	var out []Study
	partial := false
	if a.docs != nil && a.docs.dataset() != "" && a.docs.consentLog() == nil {
		partial = true
	}
	for _, f := range files {
		if strings.Count(strings.TrimPrefix(f, "studies/"), ".") != 1 {
			continue
		}
		raw, found, e := a.studies.docs.get(f)
		var st Study
		if e != nil || !found || json.Unmarshal(raw, &st) != nil {
			partial = true
			continue
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out, partial
}

// Fail closed when the configured remote history is unavailable, or legacy
// sidecars cannot be decoded. In-memory remote logs are loaded atomically by use.
func (a *app) monitorHistoryAvailable(slug string) error {
	if a.docs != nil && a.docs.dataset() != "" && a.docs.consentLog() == nil {
		return fmt.Errorf("study history unavailable")
	}
	for _, kind := range []string{"consents", "withdrawals"} {
		path, err := a.studies.sidecar(slug, kind)
		if err != nil {
			return err
		}
		raw, found, err := a.studies.docs.get(path)
		if err != nil {
			return fmt.Errorf("study history unavailable")
		}
		if !found {
			continue
		}
		if kind == "consents" {
			var v []ConsentRecord
			if json.Unmarshal(raw, &v) != nil {
				return fmt.Errorf("study history unavailable")
			}
		} else {
			var v []Withdrawal
			if json.Unmarshal(raw, &v) != nil {
				return fmt.Errorf("study history unavailable")
			}
		}
	}
	return nil
}

type monitorCell struct {
	ID, Name           string
	Records            int64
	Available, Partial bool
}
type monitorPerson struct {
	Account, State, StateLabel, StateDetail, Joined, Latest, LatestNote, Consent string
	Version                                                                      int
	Renewal, Available, Partial, HasRecords, CanSuspend, CanReinstate            bool
	Cells                                                                        []monitorCell
	Records                                                                      int64
}
type monitorCollection struct {
	ID, Name            string
	Records             int64
	People, Unavailable int
}
type monitorControls struct {
	exploreControls
	Status, Activity, Collection      string
	Statuses, Activities, Collections []exploreOption
	SearchHidden, FilterHidden        url.Values
	ResetURL, SearchClear             string
}
type monitorData struct {
	Study                                                                                      Study
	Participants                                                                               bool
	People                                                                                     []monitorPerson
	Collections                                                                                []monitorCollection
	Controls                                                                                   monitorControls
	Enrolled, TakingPart, Suspended, Withdrawn, Gone, Unknown, Renewal, Recording, Unavailable int
	Records                                                                                    int64
	Partial, HistoryUnavailable                                                                bool
	Chart, CompactChart                                                                        Chart
	TrendRows                                                                                  []panelRow
	Observed, Next, Previous, Link, AppLink                                                    string
}

func monitorState(key string) string {
	switch key {
	case "state_active":
		return "Taking part"
	case "state_suspended":
		return "Suspended"
	case "state_withdrawn":
		return "Withdrawn"
	case "state_gone":
		return "Account unavailable"
	default:
		return "Status unavailable"
	}
}
func monitorLink(path string, q url.Values) string { return path + "?" + q.Encode() }
func monitorSelection(r *http.Request, w exploreWindow, types []TypeDef) monitorControls {
	q := r.URL.Query()
	c := monitorControls{exploreControls: controls(q, nil, w, r.URL.Path), Status: q.Get("status"), Activity: q.Get("activity"), Collection: q.Get("collection")}
	c.Query = string([]rune(strings.TrimSpace(q.Get("q")))[:min(200, len([]rune(strings.TrimSpace(q.Get("q")))))])
	c.Statuses = []exploreOption{{ID: "", Name: "All statuses"}, {ID: "state_active", Name: "Taking part"}, {ID: "state_suspended", Name: "Suspended"}, {ID: "state_withdrawn", Name: "Withdrawn"}, {ID: "state_gone", Name: "Account unavailable"}, {ID: "state_unknown", Name: "Status unavailable"}}
	c.Activities = []exploreOption{{ID: "", Name: "Any activity"}, {ID: "recording", Name: "With records"}, {ID: "quiet", Name: "No records"}, {ID: "unavailable", Name: "Reading unavailable"}}
	c.Collections = []exploreOption{{ID: "", Name: "All collections"}}
	for _, t := range types {
		c.Collections = append(c.Collections, exploreOption{ID: t.ID, Name: t.Name})
	}
	for i := range c.Statuses {
		c.Statuses[i].Selected = c.Statuses[i].ID == c.Status
	}
	for i := range c.Activities {
		c.Activities[i].Selected = c.Activities[i].ID == c.Activity
	}
	for i := range c.Collections {
		c.Collections[i].Selected = c.Collections[i].ID == c.Collection
	}
	// Whitelist navigation state. Never carry arbitrary fields, identifiers or queries.
	hidden := url.Values{"period": {w.Key}}
	if w.Key == "custom" {
		hidden.Set("from", q.Get("from"))
		hidden.Set("to", q.Get("to"))
	} else if c.Month != "" {
		hidden.Set("month", c.Month)
	}
	for k, v := range map[string]string{"status": c.Status, "activity": c.Activity, "collection": c.Collection} {
		if v != "" {
			hidden.Set(k, v)
		}
	}
	c.SearchHidden = hidden
	c.FilterHidden = url.Values{}
	if c.Query != "" {
		c.FilterHidden.Set("q", c.Query)
	}
	c.SearchClear = monitorLink(r.URL.Path, hidden)
	reset := qCopy(hidden)
	reset.Del("status")
	reset.Del("activity")
	reset.Del("collection")
	if c.Query != "" {
		reset.Set("q", c.Query)
	}
	c.ResetURL = monitorLink(r.URL.Path, reset)
	return c
}
func (a *app) monitorRoster(ctx context.Context, sess *session, st Study) ([]monitorPerson, map[string]Source, error) {
	if err := a.monitorHistoryAvailable(st.Slug); err != nil {
		return nil, nil, err
	}
	histories := a.studies.histories(st.Slug)
	token, err := sess.Owner.token(ctx, a.tessera)
	if err != nil {
		return nil, nil, fmt.Errorf("participant status unavailable")
	}
	accounts, err := a.tessera.accounts(ctx, token)
	if err != nil {
		return nil, nil, fmt.Errorf("participant status unavailable")
	}
	held := map[string]AccountInfo{}
	for _, acc := range accounts {
		held[acc.Account] = acc
	}
	analysis := held[a.cfg.Analysis].Datasets
	var people []monitorPerson
	sources := map[string]Source{}
	for account, h := range histories {
		if len(h.consents) == 0 {
			continue
		}
		latest, withdrawal := h.latest(time.Now().UTC())
		if latest.At.IsZero() {
			continue
		}
		first := latest.At
		for _, c := range h.consents {
			if c.At.Before(first) {
				first = c.At
			}
		}
		p := monitorPerson{Account: account, Version: latest.Version, Joined: first.In(a.tz).Format("2 Jan 2006"), State: "state_active", Renewal: latest.Version < st.LatestMaterial()}
		acc, exists := held[account]
		if !exists {
			p.State = "state_gone"
		} else if acc.Suspended {
			p.State = "state_suspended"
		}
		if withdrawal.After(latest.At) {
			p.State = "state_withdrawn"
			p.StateDetail = withdrawal.In(a.tz).Format("2006-01-02")
		}
		// Preserve the existing app-erasure contract: loss of the analysis
		// hold, or an empty replacement store, means withdrawn without a date.
		if exists && p.State != "state_withdrawn" && a.cfg.Analysis != "" {
			var reads, stores []string
			for ds := range analysis {
				reads = append(reads, ds)
			}
			for ds := range acc.Datasets {
				stores = append(stores, ds)
			}
			if erasedFromApp(reads, stores, func(ds string) ([]Meta, error) {
				tok, e := a.analysisCache.token(ctx, a.tessera, ds)
				if e != nil {
					return nil, e
				}
				return a.tessera.types(ctx, tok, ds)
			}) {
				p.State = "state_withdrawn"
			}
		}
		p.StateLabel = monitorState(p.State)
		p.CanSuspend = p.State == "state_active"
		p.CanReinstate = p.State == "state_suspended"
		p.Consent = fmt.Sprintf("Version %d", latest.Version)
		if p.Renewal {
			p.Consent += " · Renewal needed"
		}
		if exists && len(acc.Datasets) == 1 && a.cfg.Analysis != "" {
			for dataset := range acc.Datasets {
				if _, canRead := analysis[dataset]; canRead {
					ds := dataset
					sources[account] = Source{Dataset: ds, Name: account, Participant: account, Token: func(c context.Context) (string, error) { return a.analysisCache.token(c, a.tessera, ds) }, Forget: func() { a.analysisCache.forget(ds) }}
				} else {
					p.LatestNote = "No study-readable data"
					if p.State != "state_withdrawn" {
						p.State = "state_withdrawn"
						p.StateLabel = "Not shared"
					}
					p.CanSuspend = false
					p.CanReinstate = false
				}
			}
		} else if exists && p.State != "state_withdrawn" {
			p.State = "state_unknown"
			p.StateLabel = "Store unavailable"
			p.CanSuspend = false
			p.CanReinstate = false
		}
		people = append(people, p)
	}
	sort.Slice(people, func(i, j int) bool { return people[i].Account < people[j].Account })
	return people, sources, nil
}

const monitorRows = 10000

// Only fixed study types and a single timestamp field can reach this reader.
// Count aggregation never requests a measurement; trend queries project dates.
func (a *app) readMonitor(ctx context.Context, p monitorPerson, src Source, types []TypeDef, w exploreWindow, trend bool) (monitorPerson, map[string]int64) {
	buckets := map[string]int64{}
	token, err := src.Token(ctx)
	if err != nil {
		return p, buckets
	}
	p.Available = true
	for _, typ := range types {
		cell := monitorCell{ID: typ.ID, Name: typ.Name}
		var groups []Aggregate
		filter := (Spec{Date: "timestamp", From: w.From, To: w.To}).window()
		err := retryRefused(ctx, src, &token, func(tok string) error {
			var e error
			groups, e = a.tessera.aggregate(ctx, tok, src.Dataset, typ.ID, "", "", filter)
			return e
		})
		if err != nil || len(groups) > 1 {
			p.Available = false
			p.Partial = true
			p.Cells = append(p.Cells, cell)
			continue
		}
		var count int64
		var e error
		if len(groups) == 1 {
			count, e = strconv.ParseInt(groups[0].RowCount.String(), 10, 64)
		}
		if e != nil || count < 0 {
			p.Available = false
			p.Partial = true
			p.Cells = append(p.Cells, cell)
			continue
		}
		cell.Records = count
		cell.Available = true
		p.Records += count
		p.HasRecords = p.HasRecords || count > 0
		last, e := a.tessera.latest(ctx, token, src.Dataset, typ.ID, "timestamp")
		if e != nil {
			p.LatestNote = "Latest date unavailable"
		} else if last != "" {
			at, e := time.Parse(time.RFC3339Nano, last)
			if e != nil || at.After(time.Now().UTC()) {
				p.LatestNote = "Future or invalid event date"
			} else {
				previous, _ := time.Parse(time.RFC3339Nano, p.Latest)
				if p.Latest == "" || at.After(previous) {
					p.Latest = at.UTC().Format(time.RFC3339Nano)
				}
			}
		}
		if trend && count > 0 {
			rows, cut, e := a.tessera.rows(ctx, token, src.Dataset, typ.ID, []string{"timestamp"}, filter, monitorRows)
			if e != nil || cut || int64(len(rows)) != count {
				cell.Partial = true
				p.Partial = true
			} else {
				for _, row := range rows {
					if len(row) != 1 {
						cell.Partial = true
						p.Partial = true
						continue
					}
					text, _ := row[0].Value.(string)
					at, e := time.Parse(time.RFC3339Nano, text)
					if e != nil || at.After(time.Now().UTC()) {
						cell.Partial = true
						p.Partial = true
						continue
					}
					if (!w.From.IsZero() && at.Before(w.From)) || !at.Before(w.To) {
						continue
					}
					bucket := at.In(a.tz).Format("2006-01-02")
					if w.Bucket == "month" {
						bucket = at.In(a.tz).Format("2006-01")
					}
					buckets[bucket]++
				}
			}
		}
		p.Cells = append(p.Cells, cell)
	}
	if p.Latest != "" {
		at, _ := time.Parse(time.RFC3339Nano, p.Latest)
		p.Latest = at.In(a.tz).Format("2 Jan 2006, 15:04")
	}
	return p, buckets
}
func (a *app) monitorPage(w http.ResponseWriter, r *http.Request, sess *session, st Study, participants bool) {
	now := time.Now().In(a.tz)
	window, ok := requestedWindow(r.URL.Query(), now)
	if !ok {
		a.fail(w, r, sess, 422, "error", nil)
		return
	}
	types, err := studyTracks(st.Tracks)
	if err != nil {
		a.fail(w, r, sess, 502, "error", nil)
		return
	}
	ctrl := monitorSelection(r, window, types)
	if !monitorChoice(ctrl.Statuses, ctrl.Status) || !monitorChoice(ctrl.Activities, ctrl.Activity) || !monitorChoice(ctrl.Collections, ctrl.Collection) {
		a.fail(w, r, sess, 422, "error", nil)
		return
	}
	d := monitorData{Study: st, Participants: participants, Controls: ctrl, Chart: Chart{Empty: true}, CompactChart: Chart{Empty: true}, Observed: now.Format("2 Jan 2006, 15:04 MST"), Link: r.URL.Query().Get("link"), AppLink: r.URL.Query().Get("app")}
	people, sources, err := a.monitorRoster(r.Context(), sess, st)
	if err != nil {
		d.HistoryUnavailable = true
		d.Partial = true
		a.render(w, r, sess, "research_monitor", 200, d)
		return
	}
	selectedTypes := types
	if ctrl.Collection != "" {
		selectedTypes = nil
		for _, typ := range types {
			if typ.ID == ctrl.Collection {
				selectedTypes = append(selectedTypes, typ)
			}
		}
	}
	for _, p := range people {
		d.Enrolled++
		switch p.State {
		case "state_active":
			d.TakingPart++
		case "state_suspended":
			d.Suspended++
		case "state_withdrawn":
			d.Withdrawn++
		case "state_gone":
			d.Gone++
		default:
			d.Unknown++
		}
		if p.Renewal {
			d.Renewal++
		}
	}
	var selected []monitorPerson
	for _, p := range people {
		if ctrl.Status != "" && p.State != ctrl.Status {
			continue
		}
		if ctrl.Query != "" && !strings.Contains(strings.ToLower(p.Account), strings.ToLower(ctrl.Query)) {
			continue
		}
		selected = append(selected, p)
	}
	// Participant pages query only their page. Activity filtering requires the
	// bounded scan to classify rows first; the UI labels this scope explicitly.
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	if participants && ctrl.Activity == "" {
		offset = min(offset, len(selected))
		end := min(offset+20, len(selected))
		if end < len(selected) {
			q := r.URL.Query()
			q.Set("offset", strconv.Itoa(end))
			d.Next = monitorLink(r.URL.Path, q)
		}
		if offset > 0 {
			q := r.URL.Query()
			q.Set("offset", strconv.Itoa(max(0, offset-20)))
			d.Previous = monitorLink(r.URL.Path, q)
		}
		selected = selected[offset:end]
	}
	ctx, cancel := context.WithTimeout(r.Context(), completenessDeadline)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, completenessWorkers)
	personBuckets := map[string]map[string]int64{}
	buckets := map[string]int64{}
	for i := range selected {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}
			src, ok := sources[selected[i].Account]
			if !ok {
				return
			}
			p, b := a.readMonitor(ctx, selected[i], src, selectedTypes, window, !participants)
			mu.Lock()
			selected[i] = p
			personBuckets[p.Account] = b
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	var visible []monitorPerson
	for _, p := range selected {
		if ctrl.Activity == "recording" && !p.HasRecords {
			continue
		}
		if ctrl.Activity == "quiet" && (!p.Available || p.HasRecords) {
			continue
		}
		if ctrl.Activity == "unavailable" && p.Available {
			continue
		}
		visible = append(visible, p)
		for k, v := range personBuckets[p.Account] {
			buckets[k] += v
		}
	}
	selected = visible
	for _, typ := range selectedTypes {
		c := monitorCollection{ID: typ.ID, Name: typ.Name}
		for _, p := range selected {
			found := false
			for _, cell := range p.Cells {
				if cell.ID == typ.ID {
					found = cell.Available
					if cell.Available {
						c.Records += cell.Records
						if cell.Records > 0 {
							c.People++
						}
					}
				}
			}
			if !found {
				c.Unavailable++
			}
		}
		d.Collections = append(d.Collections, c)
	}
	for _, p := range selected {
		if !p.Available {
			d.Unavailable++
			d.Partial = true
		}
		if p.Partial {
			d.Partial = true
		}
		d.Records += p.Records
		if p.HasRecords {
			d.Recording++
		}
		if ctrl.Activity == "recording" && !p.HasRecords {
			continue
		}
		if ctrl.Activity == "quiet" && (!p.Available || p.HasRecords) {
			continue
		}
		if ctrl.Activity == "unavailable" && p.Available {
			continue
		}
		d.People = append(d.People, p)
	}
	if participants && ctrl.Activity != "" {
		offset = min(offset, len(d.People))
		end := min(offset+20, len(d.People))
		if end < len(d.People) {
			q := r.URL.Query()
			q.Set("offset", strconv.Itoa(end))
			d.Next = monitorLink(r.URL.Path, q)
		}
		if offset > 0 {
			q := r.URL.Query()
			q.Set("offset", strconv.Itoa(max(0, offset-20)))
			d.Previous = monitorLink(r.URL.Path, q)
		}
		d.People = d.People[offset:end]
	}
	if !participants {
		result := Result{Spec: Spec{Measure: MeasureCount}}
		var keys []string
		for k := range buckets {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			result.Points = append(result.Points, Point{Label: k, Tally: Tally{Rows: buckets[k]}})
		}
		d.Chart = buildChartSized(result, false, 720, 180)
		d.CompactChart = buildChartSized(result, false, 320, 180)
		d.CompactChart.Compact = true
		d.TrendRows = resultRows(result)
	}
	a.render(w, r, sess, "research_monitor", 200, d)
}
func monitorChoice(options []exploreOption, id string) bool {
	for _, o := range options {
		if o.ID == id {
			return true
		}
	}
	return false
}
func (a *app) researchExports(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.researcher(w, r)
	if !ok {
		return
	}
	st, ok := a.studies.get(r.PathValue("slug"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	a.render(w, r, sess, "research_exports", 200, researchExportData{Study: st})
}

type researchExportData struct {
	Study
	ExportError string
}
