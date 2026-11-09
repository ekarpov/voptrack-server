package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"
)

// A household presents currently shared records across members. All lookups use
// an already authorized Home reader for the referenced dataset; no owner token is used.
type householdResolver struct {
	ctx     context.Context
	t       *Tessera
	sources map[string]Source
	cache   map[string]*readRecord
	shared  *homeSharedContext
}

func (r *householdResolver) object(origin string, ref objectReference) *readRecord {
	key := origin + "\x00" + ref.Dataset + "\x00" + ref.Type + "\x00" + ref.ID
	if got, ok := r.cache[key]; ok {
		return got
	}
	r.cache[key] = nil
	src, ok := r.sources[ref.Dataset]
	if !ok {
		return nil
	}
	tok, e := src.Token(r.ctx)
	if e != nil {
		return nil
	}
	var rec readRecord
	e = retryRefused(r.ctx, src, &tok, func(token string) error {
		var err error
		rec, err = r.t.recordRead(r.ctx, token, ref.Dataset, ref.Type, ref.ID)
		return err
	})
	if e != nil {
		return nil
	}
	r.cache[key] = &rec
	return &rec
}
func valueReference(v any) (objectReference, bool) {
	raw, e := json.Marshal(v)
	var ref objectReference
	if e != nil || json.Unmarshal(raw, &ref) != nil {
		return ref, false
	}
	return ref, ref.ID != "" && ref.Dataset != "" && ref.Type != ""
}
func refKey(ref objectReference) string {
	return memberKey(ref.Dataset + "\x00" + ref.Type + "\x00" + ref.ID)
}
func blobMap(v any) map[string]any {
	str, ok := v.(string)
	if !ok {
		return nil
	}
	raw, e := base64.StdEncoding.DecodeString(str)
	if e != nil {
		return nil
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}
func textField(rec *readRecord, key string) string {
	if rec == nil {
		return ""
	}
	if s := displayBlob(rec.Values[key]); s != "" {
		return s
	}
	for _, field := range rec.Fields {
		if field.ID == key && field.Type == "text" {
			s, _ := rec.Values[key].(string)
			return s
		}
	}
	return ""
}
func categoryTone(key string) string {
	for _, pair := range [][2]string{{"food", "food"}, {"transport", "transport"}, {"home", "house"}, {"health", "health"}, {"leisure", "leisure"}, {"shopping", "shopping"}, {"utilities", "house"}, {"pet", "pets"}} {
		if strings.Contains(key, pair[0]) {
			return pair[1]
		}
	}
	return "general"
}
func categoryLabel(key string) string {
	switch key {
	case "finance.food":
		return "Food & drinks"
	case "finance.transport":
		return "Transport"
	case "finance.home":
		return "Home"
	case "finance.health":
		return "Health"
	case "finance.leisure":
		return "Leisure"
	case "finance.shopping":
		return "Shopping"
	case "finance.utilities":
		return "Utilities"
	case "finance.pets":
		return "Pets"
	case "finance.other":
		return "Other"
	}
	return ""
}

type householdRow struct {
	Record                                                                                                                     readRecord
	Type                                                                                                                       TypeInfo
	TrackName, Title, Value, Date, Member, Category, CategoryKey, Tone, Subject, SubjectKey, SubjectFamily, Family, URL, Badge string
}

func (r *householdResolver) row(rec readRecord, typ TypeInfo) householdRow {
	out := householdRow{Record: rec, Type: typ, Title: recordSummary(rec, typ), Member: rec.Member, Category: "Uncategorized", CategoryKey: "uncategorized", Tone: "general", Family: trackFamily(typ.ID), URL: recordURL("home", rec)}
	out.TrackName = trackTitle(typ)
	out.Date = rec.When.Format("2 Jan, 15:04")
	out.Value = recordSummary(rec, typ)
	m := recordMetricFor(typ, rec)
	if n, e := rat(rec.Values[m.Field]); e == nil && n != nil {
		if m.Money {
			n = moneyMajor(n)
		}
		unit, _ := rec.Values[m.UnitField].(string)
		out.Value = formattedValue(n, m, unit)
	} else {
		out.Value = trackTitle(typ)
	}
	for _, key := range []string{"purpose", "description", "food", "task", "name", "context", "text", "reason"} {
		if text, ok := rec.Values[key].(string); ok && text != "" {
			out.Title = text
			break
		}
	}
	if out.Title == "" {
		out.Title = trackTitle(typ)
	}
	if ref, ok := valueReference(rec.Values["category"]); ok {
		out.Category = "Category unavailable"
		out.CategoryKey = "reference:" + refKey(ref)
		if category := r.object(rec.Dataset, ref); category != nil {
			suggestion, _ := category.Values["suggestionID"].(string)
			out.Category = textField(category, "name")
			if canonical := categoryLabel(suggestion); canonical != "" {
				out.CategoryKey = "library:" + suggestion
				if out.Category == "" || strings.HasPrefix(out.Category, "model.") {
					out.Category = canonical
				}
				out.Tone = categoryTone(suggestion)
			}
			if out.Category == "" {
				out.Category = "Uncategorized"
			}
		}
	} else if text, ok := rec.Values["category"].(string); ok && text != "" {
		out.Category = text
		out.CategoryKey = "literal:" + memberKey(rec.Dataset+"\x00"+text)
	}
	if ref, ok := valueReference(rec.Values["subject"]); ok {
		out.Subject = "Details unavailable"
		if subject := r.legacySubject(rec.Dataset, ref); subject != nil {
			out.SubjectKey = refKey(ref)
			out.Subject = "Name unavailable"
			if name := textField(subject, "name"); name != "" {
				out.Subject = name
			}
			details := blobMap(subject.Values["details"])
			category, _ := details["category"].(string)
			kind, _ := details["kindText"].(string)
			old, _ := details["kind"].(string)
			switch {
			case category == "pet" || old == "pet" || old == "dog" || old == "cat":
				out.SubjectFamily = "pets"
			case strings.EqualFold(kind, "car") || strings.EqualFold(kind, "vehicle") || old == "vehicle":
				out.SubjectFamily = "car"
			case strings.EqualFold(kind, "home") || strings.EqualFold(kind, "house"):
				out.SubjectFamily = "house"
			case category == "thing":
				if out.Family != "house" && out.Family != "car" {
					out.SubjectFamily = "other"
				}
			}
		}
	}
	if out.SubjectFamily != "" && out.Family != "expenses" {
		out.Family = out.SubjectFamily
	}
	if r.shared != nil {
		r.shared.decorate(&out)
	}
	return out
}
func trackTitle(t TypeInfo) string {
	if known, ok := homeTrackIdentities[t.ID]; ok && (t.Name == "" || t.Name == known.Key) {
		return known.Name
	}
	name := localizeName(t.Name, translator("en"))
	if name == "" {
		return "Shared track"
	}
	return name
}
func trackFamily(id string) string {
	switch id {
	case "com.voptrack.track.refuelling_session", "com.voptrack.track.odometer_reading":
		return "car"
	}
	if known, ok := homeTrackIdentities[id]; ok {
		if known.Category == "money" {
			return "expenses"
		}
		if known.Category == "general" {
			return "other"
		}
		return known.Category
	}
	return "other"
}

type householdPart struct {
	Name, Value, State, URL string
	Width                   int
}
type householdMetric struct {
	TypeID                                       string
	Name, Value, Detail, URL, Family, Comparison string
	Parts                                        []householdPart
}

type householdSubject struct {
	Name, Key, Family, URL, Latest, Member, Kind, Monogram, Summary string
	Count                                                           int
}
type spendingGroup struct {
	Name, Key, Tone, Value, URL string
	Width                       int
	Amount                      *big.Rat
}
type householdTrack struct {
	Name, Family, URL string
	Records, People   int
	Incomplete        bool
}
type householdView struct {
	ExpenseRange, ExpenseScope, ExpenseComparison, TrendTitle, MonthValue, MonthLabel, MonthMax, PreviousMonth, NextMonth                                                                              string
	ExpenseCount, UncategorizedCount                                                                                                                                                                   int
	AllUncategorized                                                                                                                                                                                   bool
	Tracks                                                                                                                                                                                             []householdTrack
	ActivityURL                                                                                                                                                                                        string
	ShowSpending, HasExpenses, ContextIncomplete, SubjectUnavailable                                                                                                                                   bool
	ComputedAt, Title, Section, Subtitle, Current, Change, HeroTitle, HeroCaption, HeroURL, Next, Previous, RecordsLabel, ClearURL, BucketClearURL, Selection, FilterCategory, FilterSubject, Currency string
	Controls                                                                                                                                                                                           exploreControls
	Navigation, SubjectOptions, CategoryOptions, Currencies                                                                                                                                            []exploreOption
	Metrics                                                                                                                                                                                            []householdMetric
	LatestFacts                                                                                                                                                                                        []householdMetric
	LatestUnavailable                                                                                                                                                                                  bool
	ExpenseParts                                                                                                                                                                                       []householdPart
	Subjects                                                                                                                                                                                           []householdSubject
	SelectedSubject                                                                                                                                                                                    *householdSubject
	Spending                                                                                                                                                                                           []spendingGroup
	Rows                                                                                                                                                                                               []householdRow
	Chart, CompactChart                                                                                                                                                                                Chart
	ChartRows                                                                                                                                                                                          []panelRow
	TotalTracks, People, TotalRecords                                                                                                                                                                  int
	Incomplete, Filtered                                                                                                                                                                               bool
}

func householdSection(q url.Values) string {
	view := q.Get("view")
	if view == "" {
		switch q.Get("category") {
		case "money":
			view = "expenses"
		case "pets", "house":
			view = q.Get("category")
		}
	}
	switch view {
	case "expenses", "subjects", "directory", "timeline", "pets", "house", "car", "other", "activity", "health", "learning", "add":
		return view
	}
	return "overview"
}

// Keep the primary navigation selected while browsing a family's details.
func homePrimarySection(section string) string {
	switch section {
	case "overview", "expenses", "directory", "add":
		return section
	case "timeline":
		return "directory"
	default:
		return "subjects"
	}
}
func houseQuery(q url.Values) url.Values { v := qCopy(q); v.Del("offset"); v.Del("category"); return v }
func sortHouseRows(rows []householdRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].Record, rows[j].Record
		if a.When.Equal(b.When) {
			return recordMapKey(a) < recordMapKey(b)
		}
		return a.When.After(b.When)
	})
}
func matchHouseRow(row householdRow, q url.Values, section string) bool {
	if section != "overview" && section != "subjects" && section != "directory" && section != "timeline" && row.Family != section && row.SubjectFamily != section {
		if section != "other" || row.Family == "expenses" || row.Family == "pets" || row.Family == "house" || row.Family == "car" {
			return false
		}
	}
	if key := q.Get("subject"); key != "" && row.SubjectKey != key {
		return false
	}
	if key := q.Get("spend"); key != "" && row.CategoryKey != key {
		return false
	}
	if section == "expenses" && q.Get("currency") != "" {
		if unit, _ := row.Record.Values["currency"].(string); unit != q.Get("currency") {
			return false
		}
	}
	return true
}
func (a *app) buildHousehold(r *http.Request, sources []Source, cat Catalog, w exploreWindow, skipped bool) (*householdView, error) {
	q := r.URL.Query()
	section := householdSection(q)
	names := map[string]string{"overview": "Home", "subjects": "Your household", "directory": "Browse activities", "timeline": "Activity", "expenses": "Spending", "pets": "Pets", "house": "House", "car": "Car", "other": "Other tracks", "activity": "Activity", "health": "Health", "learning": "Learning", "add": "Add to your household"}
	d := &householdView{Title: names[section], Section: section, Controls: controls(q, sources, w, "/home"), Current: "—", HeroTitle: "Spending", Chart: Chart{Empty: true}, CompactChart: Chart{Empty: true}, Incomplete: skipped, ComputedAt: time.Now().In(a.tz).Format("2 Jan 2006, 15:04:05 MST"), Currency: q.Get("currency"), FilterCategory: q.Get("spend"), FilterSubject: q.Get("subject")}
	d.Subtitle = w.Label + " · " + w.From.Format("2 Jan") + " to " + w.To.Format("2 Jan 2006")
	if w.Key == "all" {
		d.Subtitle = d.Controls.Range
	}
	if w.Key == "month" && !w.Current {
		d.Subtitle = w.Label + " · " + shortDateRange(w.From, w.To)
	}
	shared := a.readHomeShared(r.Context(), sources, cat, w.PreviousFrom, w.To)
	resolver := householdResolver{shared: shared, ctx: r.Context(), t: a.tessera, sources: map[string]Source{}, cache: map[string]*readRecord{}}
	for _, src := range sources {
		resolver.sources[src.Dataset] = src
	}
	var current, previous []householdRow
	readStates := map[string]map[string]string{}
	var types []TypeInfo
	for _, typ := range cat.Types {
		if homeInfrastructure(typ.ID) {
			continue
		}
		types = append(types, typ)
		if typ.ID == "com.voptrack.track.expense" {
			d.HasExpenses = true
		}
		all, states := a.readHouseholdTrack(r.Context(), sources, cat, typ, w.PreviousFrom, w.To)
		readStates[typ.ID] = states
		cut := false
		for _, state := range states {
			if state == "Unavailable" || state == "Reading limit reached" || state == "Different schema" {
				cut = true
			}
		}
		d.Incomplete = d.Incomplete || cut
		var rows, before []readRecord
		for _, rec := range all {
			if !rec.When.Before(w.From) && rec.When.Before(w.To) {
				rows = append(rows, rec)
			}
			if !rec.When.Before(w.PreviousFrom) && rec.When.Before(w.PreviousTo) {
				before = append(before, rec)
			}
		}
		oldCut := cut
		rows = filterRecords(rows, d.Controls.Member, d.Controls.Query)
		before = filterRecords(before, d.Controls.Member, d.Controls.Query)
		for _, rec := range rows {
			row := resolver.row(rec, typ)
			row.Record.When = rec.When.In(a.tz)
			row.Date = row.Record.When.Format("2 Jan 2006, 15:04")
			current = append(current, row)
		}
		for _, rec := range before {
			row := resolver.row(rec, typ)
			if oldCut {
				row.Record.When = rec.When
			}
			previous = append(previous, row)
		}
		// Previous read failures withhold comparisons, rather than implying a decline.
		if oldCut {
			d.Change = "Comparison unavailable"
		}
	}
	sortHouseRows(current)
	sortHouseRows(previous)
	d.TotalTracks = len(types)
	if section != "overview" {
		seen := map[string]bool{}
		for _, typ := range types {
			if matchHouseRow(householdRow{Family: trackFamily(typ.ID)}, url.Values{}, section) {
				seen[typ.ID] = true
			}
		}
		for _, row := range current {
			if matchHouseRow(row, url.Values{}, section) {
				seen[row.Type.ID] = true
			}
		}
		d.TotalTracks = len(seen)
	}
	if d.Controls.Query != "" || d.Controls.Member != "" {
		seen := map[string]bool{}
		for _, row := range current {
			if matchHouseRow(row, url.Values{}, section) {
				seen[row.Type.ID] = true
			}
		}
		d.TotalTracks = len(seen)
	}
	d.Filtered = d.Controls.Query != "" || d.Controls.Member != "" || d.FilterSubject != "" || d.FilterCategory != "" || q.Get("bucket") != ""
	base := houseQuery(q)
	for _, key := range []string{"overview", "expenses", "subjects", "directory"} {
		v := qCopy(base)
		v.Set("view", key)
		v.Del("subject")
		v.Del("spend")
		v.Del("currency")
		v.Del("bucket")
		d.Navigation = append(d.Navigation, exploreOption{ID: key, Name: names[key], URL: "/home?" + v.Encode(), Selected: homePrimarySection(section) == key})
	}
	subjects := map[string]*householdSubject{}
	for _, sub := range shared.Subjects {
		v := qCopy(base)
		v.Set("view", sub.Family)
		v.Set("subject", sub.Key)
		v.Del("spend")
		v.Del("bucket")
		v.Del("currency")
		subjects[sub.Key] = &householdSubject{Name: sub.Name, Key: sub.Key, Family: sub.Family, Kind: sub.Kind, Member: sub.Member, URL: "/home?" + v.Encode()}
	}
	d.ContextIncomplete = shared.Incomplete
	if d.ContextIncomplete && (d.FilterSubject != "" || d.FilterCategory != "") {
		d.Incomplete = true
	}
	categories := map[string]householdRow{}
	currency := map[string]bool{}
	for _, row := range current {
		if matchHouseRow(row, url.Values{}, section) {
			if row.SubjectKey != "" {
				if subjects[row.SubjectKey] == nil {
					v := qCopy(base)
					family := row.SubjectFamily
					if family == "" {
						family = row.Family
					}
					v.Set("view", family)
					v.Set("subject", row.SubjectKey)
					v.Del("bucket")
					subjects[row.SubjectKey] = &householdSubject{Name: row.Subject, Key: row.SubjectKey, Family: family, URL: "/home?" + v.Encode(), Latest: row.Date, Member: row.Member}
				}
				subjects[row.SubjectKey].Count++
				if subjects[row.SubjectKey].Summary == "" && metricFor(row.Type).Latest && qualifiedHomeMetric(row.Type) {
					subjects[row.SubjectKey].Summary = trackTitle(row.Type) + ": " + row.Value
				}
				if subjects[row.SubjectKey].Latest == "" {
					subjects[row.SubjectKey].Latest = row.Date
				}
			}
			if row.Family == "expenses" {
				categories[row.CategoryKey] = row
				if unit, ok := row.Record.Values["currency"].(string); ok && unit != "" {
					currency[unit] = true
				}
			}
		}
	}
	var subjectKeys []string
	for key := range subjects {
		subjectKeys = append(subjectKeys, key)
	}
	sort.Slice(subjectKeys, func(i, j int) bool {
		if subjects[subjectKeys[i]].Name == subjects[subjectKeys[j]].Name {
			return subjectKeys[i] < subjectKeys[j]
		}
		return subjects[subjectKeys[i]].Name < subjects[subjectKeys[j]].Name
	})
	d.SubjectOptions = append(d.SubjectOptions, exploreOption{Name: "Everyone and everything", Selected: d.FilterSubject == ""})
	for _, key := range subjectKeys {
		sub := subjects[key]
		if runes := []rune(strings.TrimSpace(sub.Name)); len(runes) > 0 {
			sub.Monogram = strings.ToUpper(string(runes[0]))
		} else {
			sub.Monogram = "·"
		}
		d.Subjects = append(d.Subjects, *sub)
		label := sub.Name
		for otherKey, other := range subjects {
			if otherKey != key && other.Name == sub.Name {
				label += " · " + sub.Member
				break
			}
		}
		d.SubjectOptions = append(d.SubjectOptions, exploreOption{ID: key, Name: label, Selected: key == d.FilterSubject})
	}
	if d.FilterSubject != "" && subjects[d.FilterSubject] == nil {
		d.SubjectUnavailable = true
		d.Title = "Details unavailable"
		d.Incomplete = true
		d.SubjectOptions = append(d.SubjectOptions, exploreOption{ID: d.FilterSubject, Name: "Selected details unavailable", Selected: true})
	}
	var categoryKeys []string
	for key := range categories {
		categoryKeys = append(categoryKeys, key)
	}
	sort.Slice(categoryKeys, func(i, j int) bool {
		return categories[categoryKeys[i]].Category < categories[categoryKeys[j]].Category
	})
	d.CategoryOptions = append(d.CategoryOptions, exploreOption{Name: "All categories", Selected: d.FilterCategory == ""})
	for _, key := range categoryKeys {
		d.CategoryOptions = append(d.CategoryOptions, exploreOption{ID: key, Name: categories[key].Category, Selected: key == d.FilterCategory})
	}
	if d.FilterCategory != "" {
		if _, ok := categories[d.FilterCategory]; !ok {
			d.CategoryOptions = append(d.CategoryOptions, exploreOption{ID: d.FilterCategory, Name: "Selected category (no activity)", Selected: true})
		}
	}
	var units []string
	for unit := range currency {
		units = append(units, unit)
	}
	sort.Strings(units)
	if d.Currency == "" && len(units) > 0 {
		d.Currency = units[0]
	}
	for _, unit := range units {
		v := qCopy(base)
		v.Set("currency", unit)
		d.Currencies = append(d.Currencies, exploreOption{ID: unit, Name: unit, URL: "/home?" + v.Encode(), Selected: unit == d.Currency})
	}
	if section == "expenses" && d.Currency != "" {
		q.Set("currency", d.Currency)
	}
	var selected, old []householdRow
	members := map[string]bool{}
	for _, row := range current {
		if matchHouseRow(row, q, section) {
			selected = append(selected, row)
			members[row.Record.Dataset] = true
		}
	}
	for _, row := range previous {
		if matchHouseRow(row, q, section) {
			old = append(old, row)
		}
	}
	d.People = len(members)
	d.TotalRecords = len(selected)
	for _, typ := range types {
		if !matchHouseRow(householdRow{Family: trackFamily(typ.ID)}, url.Values{}, section) && d.FilterSubject == "" {
			continue
		}
		item := householdTrack{Name: trackTitle(typ), Family: trackFamily(typ.ID)}
		people := map[string]bool{}
		for _, row := range selected {
			if row.Type.ID == typ.ID {
				item.Records++
				people[row.Record.Dataset] = true
			}
		}
		if d.FilterSubject != "" && item.Records == 0 {
			continue
		}
		item.People = len(people)
		for _, state := range readStates[typ.ID] {
			if state == "Unavailable" || state == "Different schema" || state == "Reading limit reached" {
				item.Incomplete = true
			}
		}
		v := filterQuery(d.Controls)
		v.Set("scope", "home")
		v.Set("type", typ.ID)
		v.Del("view")
		item.URL = "/tracks?" + v.Encode()
		d.Tracks = append(d.Tracks, item)
	}
	sort.Slice(d.Tracks, func(i, j int) bool { return d.Tracks[i].Name < d.Tracks[j].Name })
	for _, sub := range d.Subjects {
		if sub.Key == d.FilterSubject {
			d.SelectedSubject = subjects[sub.Key]
			d.Title = sub.Name
		}
	}

	if d.SelectedSubject != nil {
		d.LatestFacts, d.LatestUnavailable = a.latestHomeFacts(r.Context(), sources, cat, types, shared, d.SelectedSubject.Key, d.Controls)
	}
	// Metrics are grouped by track semantics and unit, never by contributing member.
	for _, typ := range types {
		if section == "overview" && metricFor(typ).Latest {
			continue
		}
		if d.SelectedSubject != nil && metricFor(typ).Latest {
			continue
		}
		if !qualifiedHomeMetric(typ) {
			continue
		}
		var rows, before []readRecord
		for _, row := range selected {
			if row.Type.ID == typ.ID {
				rows = append(rows, row.Record)
			}
		}
		for _, row := range old {
			if row.Type.ID == typ.ID {
				before = append(before, row.Record)
			}
		}
		if len(rows) == 0 {
			continue
		}
		series := viewSeries(rows, typ, a.tz, w.From, false, w.Bucket)
		prior := viewSeries(before, typ, a.tz, w.PreviousFrom, d.Incomplete, w.Bucket)
		for _, s := range series {
			v := filterQuery(d.Controls)
			v.Set("scope", "home")
			v.Set("type", typ.ID)
			v.Set("series", s.Key)
			if d.FilterSubject != "" {
				v.Set("subject", d.FilterSubject)
			}
			metric := householdMetric{TypeID: typ.ID, Name: trackTitle(typ), Value: formattedValue(seriesValue(s), s.Metric, s.Unit), Detail: metricMeasure(s.Metric) + " · " + s.Label, URL: "/tracks?" + v.Encode(), Family: trackFamily(typ.ID)}
			if s.Metric.Measure == MeasureCount {
				metric.Detail = "Recorded entries in this period"
			}
			if d.SelectedSubject != nil {
				metric.Name = strings.TrimPrefix(metric.Name, "Pet ")
			}
			if d.SelectedSubject != nil && typ.ID == "com.voptrack.track.expense" {
				metric.Name = "Related spending"
			}
			metric.Parts = householdParts(sources, rows, typ, s, readStates[typ.ID], d.Controls, a.tz, w)
			var was *big.Rat
			for _, p := range prior {
				if p.Key == s.Key {
					was = seriesValue(p)
				}
			}
			metric.Comparison = comparison(seriesValue(s), was, s.Metric, s.Unit)
			if d.Incomplete {
				metric.Comparison = "Comparison unavailable"
			}
			if !w.Comparable {
				metric.Comparison = w.ComparisonLabel
			}
			if typ.ID == "com.voptrack.track.expense" && s.Unit == d.Currency {
				d.ExpenseParts = append([]householdPart(nil), metric.Parts...)
				for i := range d.ExpenseParts {
					v := houseQuery(q)
					v.Set("view", "expenses")
					v.Set("currency", s.Unit)
					existing, _ := url.Parse(metric.Parts[i].URL)
					v.Set("member", existing.Query().Get("member"))
					d.ExpenseParts[i].URL = "/home?" + v.Encode()
				}
				d.Current = metric.Value
				d.HeroCaption = w.Label
				d.HeroURL = metric.URL
				if d.Change == "" {
					d.Change = metric.Comparison
				}
				if !w.Comparable {
					d.Change = w.ComparisonLabel
				}
				d.Chart = buildChartSized(s.Result, false, 720, 180)
				d.CompactChart = buildChartSized(s.Result, false, 320, 180)
				d.CompactChart.Compact = true
				d.ChartRows = resultRows(s.Result)
				linkq := houseQuery(q)
				linkq.Set("view", "expenses")
				linkq.Set("currency", s.Unit)
				linkHouseChart(&d.Chart, linkq, w.Bucket)
				linkHouseChart(&d.CompactChart, linkq, w.Bucket)
			}
			if !(section == "overview" && typ.ID == "com.voptrack.track.expense" && s.Unit == d.Currency) {
				d.Metrics = append(d.Metrics, metric)
			}
		}
	}
	if len(d.ExpenseParts) == 0 && (section == "overview" || section == "expenses") {
		for _, typ := range types {
			if typ.ID == "com.voptrack.track.expense" {
				d.ExpenseParts = householdParts(sources, nil, typ, valueSeries{}, readStates[typ.ID], d.Controls, a.tz, w)
				break
			}
		}
	}
	// Bank-style breakdowns contain real categories only; inaccessible references
	// remain visible as unavailable instead of being guessed from a description.
	groups := map[string]*spendingGroup{}
	total := new(big.Rat)
	for _, row := range selected {
		if row.Type.ID != "com.voptrack.track.expense" || !metricFor(row.Type).Money || metricFor(row.Type).Unsupported {
			continue
		}
		unit, _ := row.Record.Values["currency"].(string)
		if unit == "" || unit != d.Currency {
			continue
		}
		n, e := rat(row.Record.Values["amount"])
		if e != nil || n == nil {
			continue
		}
		n = moneyMajor(n)
		d.ExpenseCount++
		if row.CategoryKey == "uncategorized" {
			d.UncategorizedCount++
		}
		if groups[row.CategoryKey] == nil {
			v := houseQuery(q)
			v.Set("view", "expenses")
			v.Set("spend", row.CategoryKey)
			v.Set("currency", d.Currency)
			groups[row.CategoryKey] = &spendingGroup{Name: row.Category, Key: row.CategoryKey, Tone: row.Tone, URL: "/home?" + v.Encode(), Amount: new(big.Rat)}
		}
		groups[row.CategoryKey].Amount.Add(groups[row.CategoryKey].Amount, n)
		total.Add(total, n)
	}
	for _, g := range groups {
		g.Value = formattedValue(g.Amount, valueMetric{Money: true}, d.Currency)
		if total.Sign() > 0 && g.Amount.Sign() > 0 {
			percent := new(big.Rat).Mul(new(big.Rat).Quo(g.Amount, total), big.NewRat(100, 1))
			n, _ := percent.Float64()
			g.Width = min(100, max(1, int(n)))
		}
		d.Spending = append(d.Spending, *g)
	}
	d.AllUncategorized = d.ExpenseCount > 0 && d.UncategorizedCount == d.ExpenseCount
	sort.Slice(d.Spending, func(i, j int) bool { return d.Spending[i].Amount.Cmp(d.Spending[j].Amount) > 0 })
	if section != "overview" && section != "expenses" {
		d.HeroTitle = names[section] + " activity"
		d.Current = ""
		if len(d.Metrics) > 0 {
			d.Current = d.Metrics[0].Value
			d.HeroCaption = d.Metrics[0].Name
		}
		d.Chart.Empty = true
		d.CompactChart.Empty = true
	}
	if bucket := q.Get("bucket"); bucket != "" {
		at, e := bucketStart(bucket, w.Bucket, a.tz)
		var narrowed []householdRow
		if e == nil {
			end := nextBucket(at, w.Bucket)
			for _, row := range selected {
				if !row.Record.When.Before(at) && row.Record.When.Before(end) {
					narrowed = append(narrowed, row)
				}
			}
		}
		selected = narrowed
		d.Selection = "Selected chart period is unavailable"
		if e == nil {
			d.Selection = "Records for " + shortDateRange(at, nextBucket(at, w.Bucket))
			if w.Bucket == "hour" {
				d.Selection = "Records for " + at.Format("2 Jan, 15:04 MST")
			}
		}
	}
	d.RecordsLabel = strings.ReplaceAll(recordsLabel(len(selected), d.Incomplete), "records", "entries")
	if !d.Filtered && !d.Incomplete {
		d.RecordsLabel = fmt.Sprintf("%d entries in this period", len(selected))
	}
	v := houseQuery(q)
	v.Set("view", "timeline")
	v.Del("subject")
	v.Del("bucket")
	d.ActivityURL = "/home?" + v.Encode()
	d.ShowSpending = section == "expenses" || (section == "overview" && d.Current != "—")
	if section == "overview" {
		// The landing page highlights supported app Tracks. Technical demo/media
		// types remain in the directory and activity rather than becoming KPIs.
		var featured []householdMetric
		for _, metric := range d.Metrics {
			if _, known := homeTrackIdentities[metric.TypeID]; known {
				featured = append(featured, metric)
			}
		}
		if len(featured) > 0 {
			d.Metrics = featured
		}
	}
	if section == "overview" && len(d.Metrics) > 4 {
		d.Metrics = d.Metrics[:4]
	}
	offset := pageOffset(r, len(selected))
	pageSize := 20
	if section == "overview" {
		offset = 0
		pageSize = 8
	}
	if section == "directory" {
		offset = 0
		pageSize = 0
	}
	end := min(offset+pageSize, len(selected))
	var pageRecords []readRecord
	for _, row := range selected[offset:end] {
		pageRecords = append(pageRecords, row.Record)
	}
	badges := a.attachmentBadges(r.Context(), sources, pageRecords)
	for _, row := range selected[offset:end] {
		row.Badge = badges[recordMapKey(row.Record)]
		u, _ := url.Parse(row.URL)
		v := u.Query()
		homeReturn := qCopy(q)
		homeReturn.Set("view", section)
		v.Set("home_return", homeReturn.Encode())
		u.RawQuery = v.Encode()
		row.URL = u.String()
		d.Rows = append(d.Rows, row)
	}
	if end < len(selected) && section != "overview" {
		v := qCopy(q)
		v.Set("offset", fmt.Sprint(end))
		d.Next = "/home?" + v.Encode() + "#activity"
	}
	if offset > 0 {
		v := qCopy(q)
		v.Set("offset", fmt.Sprint(max(0, offset-20)))
		d.Previous = "/home?" + v.Encode() + "#activity"
	}
	clear := houseQuery(q)
	for _, key := range []string{"subject", "spend", "bucket", "member"} {
		clear.Del(key)
	}
	d.ClearURL = "/home?" + clear.Encode()
	bucketClear := qCopy(q)
	bucketClear.Del("bucket")
	bucketClear.Del("offset")
	d.BucketClearURL = "/home?" + bucketClear.Encode()
	prepareExpensePresentation(d, q, w, time.Now().In(a.tz))
	d.Controls.View = section
	d.Controls.Range = d.Subtitle
	d.Controls.Subjects = d.SubjectOptions
	d.Controls.ClearURL = d.ClearURL
	d.Controls.Currency = d.Currency
	d.Controls.Spending = d.FilterCategory
	if section == "expenses" {
		d.Controls.Categories = d.CategoryOptions
	}
	d.ContextIncomplete = d.ContextIncomplete || shared.Incomplete
	d.ComputedAt = time.Now().In(a.tz).Format("2 Jan 2006, 15:04:05 MST")
	return d, nil
}
func linkHouseChart(c *Chart, q url.Values, bucket string) {
	linkChart(c, q, bucket)
	for i := range c.Bars {
		c.Bars[i].URL = strings.Replace(c.Bars[i].URL, "/tracks?", "/home?", 1)
		c.Bars[i].URL = strings.TrimSuffix(c.Bars[i].URL, "#records") + "#activity"
	}
	for i := range c.Dots {
		c.Dots[i].URL = strings.Replace(c.Dots[i].URL, "/tracks?", "/home?", 1)
		c.Dots[i].URL = strings.TrimSuffix(c.Dots[i].URL, "#records") + "#activity"
	}
}

func filterSubject(rows []readRecord, key string) []readRecord {
	if key == "" {
		return rows
	}
	var out []readRecord
	for _, row := range rows {
		if row.HomeSubjectKey == key {
			out = append(out, row)
			continue
		}
		if row.HomeSubjectKey != "" {
			continue
		}
		if ref, ok := valueReference(row.Values["subject"]); ok && ref.Type != sharedSubjectType && refKey(ref) == key {
			out = append(out, row)
		}
	}
	return out
}

// One bounded read per dataset/type spans both comparison windows. The totals,
// charts and member parts all use that same response. The dashboard never reads
// an iPhone source as well as its Home copy, and never mutates a copied type.
func (a *app) readHouseholdTrack(ctx context.Context, sources []Source, cat Catalog, typ TypeInfo, from, to time.Time) ([]readRecord, map[string]string) {
	states := map[string]string{}
	var all []readRecord
	for _, src := range sources {
		if _, seen := states[src.Dataset]; seen {
			continue
		}
		states[src.Dataset] = "Unavailable"
		metas, ok := cat.Metadata[src.Dataset]
		if !ok {
			continue
		}
		states[src.Dataset] = "Not shared"
		var found *Meta
		for i := range metas {
			if metas[i].ID == typ.ID {
				found = &metas[i]
				break
			}
		}
		if found == nil {
			continue
		}
		// A shared ID is insufficient evidence that two installations agree.
		if !reflect.DeepEqual(found.Fields, typ.Fields) || !reflect.DeepEqual(found.OpaqueAnnotations, typ.Schema.OpaqueAnnotations) || !reflect.DeepEqual(found.Annotations, typ.Schema.Annotations) || !reflect.DeepEqual(found.Roles, typ.Schema.Roles) || !reflect.DeepEqual(found.ParentID, typ.Schema.ParentID) {
			states[src.Dataset] = "Different schema"
			continue
		}
		token, e := src.Token(ctx)
		if e != nil {
			states[src.Dataset] = "Unavailable"
			continue
		}
		query := map[string]any{"type_scope": map[string]any{"kind": "exact"}}
		if date := dateField(typ); date != "" {
			query["result_sort"] = []any{map[string]any{"field_id": date, "ascending": false}}
			query["filter"] = Spec{Date: date, From: from, To: to}.window()
		}
		var rows []readRecord
		var cut bool
		e = retryRefused(ctx, src, &token, func(tok string) error {
			var err error
			rows, cut, err = a.tessera.recordRows(ctx, tok, src.Dataset, typ.ID, query, recordReadLimit)
			return err
		})
		if e != nil {
			states[src.Dataset] = "Unavailable"
			continue
		}
		states[src.Dataset] = "Read"
		if cut {
			states[src.Dataset] = "Reading limit reached"
			continue
		}
		for _, rec := range rows {
			rec.Member = src.Name
			all = append(all, rec)
		}
	}
	return all, states
}

func householdParts(sources []Source, rows []readRecord, typ TypeInfo, series valueSeries, states map[string]string, controls exploreControls, tz *time.Location, window exploreWindow) []householdPart {
	var parts []householdPart
	seen := map[string]bool{}
	for _, src := range sources {
		if seen[src.Dataset] || (controls.Member != "" && controls.Member != memberKey(src.Dataset)) {
			continue
		}
		seen[src.Dataset] = true
		part := householdPart{Name: src.Name, Value: "—", State: states[src.Dataset]}
		if part.State == "Read" {
			part.State = "No values in this period"
			var own []readRecord
			for _, row := range rows {
				if row.Dataset == src.Dataset {
					own = append(own, row)
				}
			}
			for _, s := range viewSeries(own, typ, tz, window.From, false, window.Bucket) {
				if s.Key == series.Key && len(s.Records) > 0 {
					if value := seriesValue(s); value != nil {
						part.Value = formattedValue(value, s.Metric, s.Unit)
						part.State = ""
					}
				}
			}
		}
		v := filterQuery(controls)
		v.Set("scope", "home")
		v.Set("type", typ.ID)
		v.Set("member", memberKey(src.Dataset))
		if series.Key != "" {
			v.Set("series", series.Key)
		}
		part.URL = "/tracks?" + v.Encode()
		parts = append(parts, part)
	}
	return parts
}

// Dashboard navigation retains the period and household-wide filters. A subject,
// spending category or selected chart point belongs to its previous view.
func homeNavigation(q url.Values) map[string]string {
	links := map[string]string{}
	for _, section := range []string{"overview", "expenses", "subjects", "directory", "timeline", "pets", "house", "car", "other", "add"} {
		v := url.Values{}
		for _, key := range []string{"period", "month", "from", "to", "member", "q"} {
			if value := q.Get(key); value != "" {
				v.Set(key, value)
			}
		}
		if section != "overview" {
			v.Set("view", section)
		}
		links[section] = "/home"
		if len(v) > 0 {
			links[section] += "?" + v.Encode()
		}
	}
	return links
}
