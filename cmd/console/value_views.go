package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type valueField struct{ Field, UnitField, Unit, Label string }

type valueMetric struct {
	Options                                       []valueField
	Catalogue, Unsupported, Level                 bool
	Unit                                          string
	Field, UnitField, KindField, KindLabel, Label string
	Money, Latest                                 bool
	Measure                                       Measure
}

func moneyType(id string) bool {
	switch id {
	case "com.voptrack.track.expense", "com.voptrack.track.income", "com.voptrack.track.balance", "com.voptrack.track.bill", "com.voptrack.track.reimbursement_claim", "com.voptrack.track.asset_value":
		return true
	}
	return false
}
func moneyMajor(v *big.Rat) *big.Rat { return new(big.Rat).Quo(v, big.NewRat(100, 1)) }
func metricFor(t TypeInfo) valueMetric {
	if t.CompiledMetric != nil {
		return *t.CompiledMetric
	}
	if m, known := catalogueMetric(t); known {
		return m
	}
	return legacyMetricFor(t)
}
func recordMetricFor(t TypeInfo, rows ...readRecord) valueMetric {
	m := metricFor(t)
	if m.Unsupported && !catalogueReadCompatible(t) {
		return m
	}
	if m.Measure == MeasureCount || m.Unsupported {
		m = legacyMetricFor(t)
	}
	if len(rows) > 0 {
		if selected, ok := recordValueMetric(m, rows[0]); ok {
			m = selected
		}
	}
	return m
}
func legacyMetricFor(t TypeInfo) valueMetric {
	m := valueMetric{Measure: MeasureAverage}
	if len(t.Numeric) == 0 {
		return m
	}
	f := t.Numeric[0]
	if f.Type == "fixed" {
		m.Measure = MeasureSum
	}
	m.Field = f.ID
	m.Label = localizeName(fieldName(f), translator("en"))
	choose := func(id string) {
		for _, n := range t.Numeric {
			if n.ID == id {
				f = n
				m.Field = id
				m.Label = localizeName(fieldName(n), translator("en"))
			}
		}
	}
	hasCurrency := false
	for _, g := range t.Groups {
		if g.ID == "currency" && g.Type == "text" {
			hasCurrency = true
		}
	}
	hasIntegerAmount := false
	for _, n := range t.Numeric {
		if n.ID == "amount" && n.Type == "integer" {
			hasIntegerAmount = true
		}
	}
	if moneyType(t.ID) && hasCurrency && hasIntegerAmount {
		choose("amount")
		m.Money = true
		m.UnitField = "currency"
		m.Measure = MeasureSum
		if t.ID == "com.voptrack.track.balance" || t.ID == "com.voptrack.track.asset_value" {
			m.Latest = true
			m.Measure = MeasureLatest
			m.KindField = "subject"
			m.KindLabel = "$member"
		}
	}
	switch t.ID {
	case "com.voptrack.track.pet_feeding":
		choose("mass")
		m.UnitField = "unit"
		m.KindField = "foodID"
		m.KindLabel = "food"
		m.Measure = MeasureSum
	case "com.voptrack.track.chore_completion":
		choose("duration")
		m.UnitField = "durationUnit"
		m.Measure = MeasureSum
	case "com.voptrack.track.inventory_level", "com.voptrack.track.meter_reading", "com.voptrack.track.electricity_meter_reading", "com.voptrack.track.water_meter_reading", "com.voptrack.track.gas_meter_reading", "com.voptrack.track.odometer_reading":
		m.KindField = "meterID"
		m.KindLabel = "meterID"
		if t.ID == "com.voptrack.track.meter_reading" || t.ID == "com.voptrack.track.inventory_level" {
			m.KindField = "subject"
			m.KindLabel = "$member"
		}
		m.Latest = true
		m.Measure = MeasureLatest
	}
	if !m.Money {
		if f.Annotations["meaning"] == "additive" {
			m.Measure = MeasureSum
		}
		if f.Annotations["meaning"] == "sample" {
			m.Measure = MeasureAverage
		}
		if m.UnitField == "" {
			m.UnitField = f.Annotations["unit_field"]
			for _, g := range t.Groups {
				if m.UnitField == "" && (g.ID == f.ID+"_unit" || g.ID == f.ID+"Unit" || g.ID == "unit") {
					m.UnitField = g.ID
				}
			}
		}
		if m.Latest {
			m.Measure = MeasureLatest
		}
	}
	return m
}

type valueSeries struct {
	Key, Label, Unit string
	Metric           valueMetric
	Result           Result
	Records          []readRecord
}

func seriesFor(rows []readRecord, t TypeInfo, tz *time.Location, from time.Time, incomplete bool) []valueSeries {
	return seriesForBucket(rows, t, tz, from, incomplete, "month")
}
func seriesForBucket(rows []readRecord, t TypeInfo, tz *time.Location, from time.Time, incomplete bool, bucket string) []valueSeries {
	baseMetric := metricFor(t)
	if baseMetric.Unsupported || baseMetric.Field == "" {
		return nil
	}
	byKey := map[string]*valueSeries{}
	buckets := map[string]map[string]*Point{}
	latest := map[string]map[string]time.Time{}
	for _, rec := range rows {
		m, valid := recordValueMetric(baseMetric, rec)
		if !valid {
			incomplete = true
			continue
		}
		value, e := rat(rec.Values[m.Field])
		if e != nil || value == nil {
			if e != nil {
				incomplete = true
			}
			continue
		}
		unit := m.Unit
		if m.UnitField != "" {
			unit, _ = rec.Values[m.UnitField].(string)
			unit = strings.TrimSpace(unit)
			if unit == "" {
				incomplete = true
				continue
			}
		}
		if m.Money {
			unit = strings.ToUpper(unit)
			if len(unit) != 3 {
				incomplete = true
				continue
			}
			value = moneyMajor(value)
		}
		kind := ""
		if m.KindField != "" {
			kind, _ = rec.Values[m.KindField].(string)
			if kind == "" && rec.Values[m.KindField] != nil {
				raw, _ := json.Marshal(rec.Values[m.KindField])
				kind = string(raw)
			}
			if kind == "" && m.KindField == "subject" {
				kind = rec.Dataset
			}
			if kind == "" {
				incomplete = true
				continue
			}
		}
		if m.Latest {
			namespace := rec.Dataset
			if ref, ok := valueReference(rec.Values["subject"]); ok {
				namespace = refKey(ref)
			}
			kind = namespace + "\x00" + kind
			for _, extra := range []string{"registerID", "epochID", "registerEpochID", "meterEpochID", "assetID"} {
				if value, ok := rec.Values[extra].(string); ok && value != "" {
					kind += "\x00" + extra + "=" + value
				}
			}
		}
		qualifier := ""
		if len(baseMetric.Options) > 1 {
			qualifier = m.Field + "\x00"
		}
		hash := sha256.Sum256([]byte(qualifier + unit + "\x00" + kind))
		key := hex.EncodeToString(hash[:12])
		s := byKey[key]
		if s == nil {
			label := m.Label
			if unit != "" {
				label += " · " + unit
			}
			if kind != "" {
				displayKind, _ := rec.Values[m.KindLabel].(string)
				if m.KindLabel == "$member" {
					displayKind = rec.Member
				}
				if displayKind != "" {
					label += " · " + displayKind
				}
			}
			s = &valueSeries{Key: key, Label: label, Unit: unit, Metric: m, Result: Result{Spec: Spec{Type: t.ID, Field: m.Field, Measure: m.Measure, Group: "period", Period: bucket, TZ: tz, Date: dateField(t), From: from}}}
			byKey[key] = s
			buckets[key] = map[string]*Point{}
			latest[key] = map[string]time.Time{}
		}
		s.Records = append(s.Records, rec)
		if rec.When.IsZero() {
			incomplete = true
			continue
		}
		period := bucketLabel(rec.When.In(tz), bucket)
		p := buckets[key][period]
		if p == nil {
			p = &Point{Label: period}
			buckets[key][period] = p
		}
		p.Tally.addValue(value)
		if at, ok := latest[key][period]; !ok || rec.When.After(at) {
			p.LatestValue = new(big.Rat).Set(value)
			latest[key][period] = rec.When
		}
	}
	var out []valueSeries
	for key, s := range byKey {
		var periods []string
		for period := range buckets[key] {
			periods = append(periods, period)
		}
		sort.Slice(periods, func(i, j int) bool {
			a, _ := bucketStart(periods[i], bucket, tz)
			b, _ := bucketStart(periods[j], bucket, tz)
			return a.Before(b)
		})
		if len(periods) > 0 {
			start, _ := bucketStart(periods[0], bucket, tz)
			last, _ := bucketStart(periods[len(periods)-1], bucket, tz)
			if (last.Year()-start.Year())*12+int(last.Month()-start.Month()) > 240 {
				incomplete = true
			} else {
				for at, n := start, 0; !at.After(last) && n < 1500; at, n = nextBucket(at, bucket), n+1 {
					period := bucketLabel(at, bucket)
					if point := buckets[key][period]; point != nil {
						s.Result.Points = append(s.Result.Points, *point)
					} else {
						s.Result.Points = append(s.Result.Points, Point{Label: period})
					}
				}
			}
		}
		s.Result.Truncated = incomplete
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}
func metricMeasure(m valueMetric) string {
	if m.Measure == MeasureCount {
		return "Count"
	}
	if m.Unsupported {
		return "Summary unavailable"
	}
	if m.Latest {
		return "Latest reading"
	}
	if m.Measure == MeasureSum {
		return "Total"
	}
	return "Average"
}

type entryView struct{ When, Member, Summary, Value, Detail, URL, Badge string }
type seriesOption struct {
	Name, URL string
	Selected  bool
}
type trackView struct {
	ContextName, ContextMember                                                                                       string
	ContextUnavailable                                                                                               bool
	SummaryUnavailable                                                                                               bool
	Title, Back, ChartLabel, From, To, Next, Previous, Advanced, Count, Selection, ClearBucket, Headline, Comparison string
	Controls                                                                                                         exploreControls
	Chart, CompactChart                                                                                              Chart
	Incomplete                                                                                                       bool
	Series                                                                                                           []seriesOption
	Entries                                                                                                          []entryView
	Rows                                                                                                             []panelRow
	Scope, Type                                                                                                      string
}

func (a *app) trackPage(w http.ResponseWriter, r *http.Request) {
	sess, scope, sources, ok := a.recordSources(w, r)
	if !ok {
		return
	}
	cat, skipped := catalog(r.Context(), a.tessera, sources)
	typ, found := cat.find(r.URL.Query().Get("type"))
	if !found {
		a.fail(w, r, sess, 404, "no_tracks", nil)
		return
	}
	q := r.URL.Query()
	windo, valid := requestedWindow(q, time.Now().In(a.tz))
	if !valid {
		a.fail(w, r, sess, 422, "error", nil)
		return
	}
	ctrl := controls(q, sources, windo, "/tracks")
	var shared *homeSharedContext
	if scope == "home" && q.Get("subject") != "" {
		shared = a.readHomeShared(r.Context(), sources, cat, windo.PreviousFrom, windo.To)
	}
	rows, cut, _ := readTrackRecords(r.Context(), a.tessera, sources, typ, windo.From, windo.To)
	rows = filterRecords(rows, ctrl.Member, ctrl.Query)
	if shared != nil {
		shared.annotate(rows, typ)
		cut = cut || shared.Incomplete
	}
	rows = filterSubject(rows, q.Get("subject"))
	var previous []readRecord
	prevCut := false
	if windo.Comparable {
		previous, prevCut, _ = readTrackRecords(r.Context(), a.tessera, sources, typ, windo.PreviousFrom, windo.PreviousTo)
	}
	previous = filterRecords(previous, ctrl.Member, ctrl.Query)
	if shared != nil {
		shared.annotate(previous, typ)
		prevCut = prevCut || shared.Incomplete
	}
	previous = filterSubject(previous, q.Get("subject"))
	series := viewSeries(rows, typ, a.tz, windo.From, cut || len(skipped) > 0, windo.Bucket)
	before := viewSeries(previous, typ, a.tz, windo.PreviousFrom, prevCut || len(skipped) > 0, windo.Bucket)
	d := trackView{Title: localizeName(typ.Name, translator("en")), Back: "/home?" + filterQuery(ctrl).Encode(), Scope: scope, Type: typ.ID, Controls: ctrl, Incomplete: cut || len(skipped) > 0, Chart: Chart{Empty: true}, CompactChart: Chart{Empty: true}, Advanced: panelLink(scope, Spec{Type: typ.ID, Measure: MeasureCount, Group: "store", TZ: a.tz})}
	backq := filterQuery(ctrl)
	backq.Del("bucket")
	d.Back = homeNavigation(backq)["directory"]
	if scope == "mine" {
		d.Back = a.personalHome()
	}
	if scope == "home" && q.Get("subject") != "" && shared != nil {
		d.ContextUnavailable = true
		for _, sub := range shared.Subjects {
			if sub.Key == q.Get("subject") {
				d.ContextName, d.ContextMember, d.ContextUnavailable = sub.Name, sub.Member, false
				back := filterQuery(ctrl)
				back.Del("scope")
				back.Del("type")
				back.Del("series")
				back.Del("bucket")
				back.Del("offset")
				back.Set("view", sub.Family)
				back.Set("subject", sub.Key)
				d.Back = "/home?" + back.Encode()
				break
			}
		}
	}

	if known, ok := homeTrackIdentities[typ.ID]; ok && typ.Name == known.Key {
		d.Title = known.Name
	}
	if a.cfg.Variant != "research" && !qualifiedHomeMetric(typ) {
		d.SummaryUnavailable = true
		series = nil
		before = nil
	}
	selected := q.Get("series")
	if d.SummaryUnavailable {
		selected = "all"
	}
	if selected == "" && len(series) > 0 {
		selected = series[0].Key
	}
	if len(series) > 0 {
		v := qCopy(q)
		v.Set("series", "all")
		v.Del("offset")
		d.Series = append(d.Series, seriesOption{"All records", "/tracks?" + v.Encode(), selected == "all"})
	}
	var picked *valueSeries
	for i := range series {
		s := &series[i]
		v := qCopy(q)
		v.Set("series", s.Key)
		v.Del("offset")
		d.Series = append(d.Series, seriesOption{s.Label, "/tracks?" + v.Encode(), s.Key == selected})
		if s.Key == selected {
			picked = s
		}
	}
	if picked != nil {
		d.Chart = buildChartSized(picked.Result, metricLine(picked.Metric), 720, 180)
		d.CompactChart = buildChartSized(picked.Result, metricLine(picked.Metric), 320, 180)
		d.CompactChart.Compact = true
		d.ChartLabel = picked.Label + " · " + metricMeasure(picked.Metric)
		d.Rows = resultRows(picked.Result)
		d.Incomplete = d.Incomplete || picked.Result.Truncated
		linkq := filterQuery(ctrl)
		linkq.Set("scope", scope)
		linkq.Set("type", typ.ID)
		linkq.Set("series", picked.Key)
		linkChart(&d.Chart, linkq, windo.Bucket)
		linkChart(&d.CompactChart, linkq, windo.Bucket)
		for i := range d.Rows {
			v := qCopy(linkq)
			v.Set("bucket", d.Rows[i].Label)
			d.Rows[i].URL = "/tracks?" + v.Encode() + "#records"
		}
		d.Headline = formattedValue(seriesValue(*picked), picked.Metric, picked.Unit)
		var old *big.Rat
		for _, s := range before {
			if s.Key == picked.Key {
				old = seriesValue(s)
			}
		}
		d.Comparison = comparison(seriesValue(*picked), old, picked.Metric, picked.Unit)
		if !windo.Comparable {
			d.Comparison = windo.ComparisonLabel
		}
		rows = picked.Records
	} else if selected != "" && selected != "all" {
		rows = nil
		d.Comparison = "No values for this series in the selected period"
	}
	if ctrl.Bucket != "" {
		at, err := bucketStart(ctrl.Bucket, windo.Bucket, a.tz)
		if err != nil || !nextBucket(at, windo.Bucket).After(windo.From) || !at.Before(windo.To) {
			rows = nil
			d.Selection = "This chart period is outside the selected range"
		} else {
			rows = inWindow(rows, at, nextBucket(at, windo.Bucket))
			d.Selection = "Records for " + ctrl.Bucket
		}
		v := qCopy(q)
		v.Del("bucket")
		v.Del("offset")
		d.ClearBucket = "/tracks?" + v.Encode() + "#records"
	}
	d.Count = recordsLabel(len(rows), d.Incomplete)
	if scope == "home" {
		d.Count = strings.ReplaceAll(d.Count, "records", "entries")
	}
	offset := pageOffset(r, len(rows))
	end := offset + 30
	if end > len(rows) {
		end = len(rows)
	}
	badges := a.attachmentBadges(r.Context(), sources, rows[offset:end])
	for _, row := range rows[offset:end] {
		when := "Time not recorded"
		if !row.When.IsZero() {
			when = row.When.In(a.tz).Format("2 Jan 2006, 15:04")
		}
		v := filterQuery(ctrl)
		v.Set("scope", scope)
		v.Set("type", typ.ID)
		v.Set("series", selected)
		if q.Get("offset") != "" {
			v.Set("offset", q.Get("offset"))
		}
		u, _ := url.Parse(recordURL(scope, row))
		rv := u.Query()
		rv.Set("return", v.Encode())
		u.RawQuery = rv.Encode()
		value, detail := trackRecordColumns(row, typ)
		d.Entries = append(d.Entries, entryView{When: when, Member: row.Member, Summary: recordSummary(row, typ), Value: value, Detail: detail, URL: u.String(), Badge: badges[recordMapKey(row)]})
	}
	if end < len(rows) {
		v := qCopy(q)
		v.Set("offset", fmt.Sprint(end))
		d.Next = "/tracks?" + v.Encode() + "#records"
	}
	if offset > 0 {
		v := qCopy(q)
		v.Set("offset", fmt.Sprint(max(0, offset-30)))
		d.Previous = "/tracks?" + v.Encode() + "#records"
	}
	a.render(w, r, sess, "track", 200, d)
}

// Keep the exact existing summary for record context, with separate display
// columns for its primary value and text. This changes no measurement semantics.
func trackRecordColumns(row readRecord, typ TypeInfo) (string, string) {
	value := "—"
	m := recordMetricFor(typ, row)
	if m.Field != "" {
		if n, err := rat(row.Values[m.Field]); err == nil && n != nil {
			if m.Money {
				n = moneyMajor(n)
			}
			unit, _ := row.Values[m.UnitField].(string)
			value = formattedValue(n, m, unit)
		}
	}
	detail := recordSummary(row, typ)
	if value != "—" && strings.HasPrefix(detail, value) {
		detail = strings.TrimLeft(strings.TrimPrefix(detail, value), " ·")
	}
	if detail == "" {
		detail = "—"
	}
	return value, detail
}

// An arbitrary first numeric field does not declare a period rule. Unsupported
// Home types keep their raw history without guessing a sum or average.
func qualifiedHomeMetric(t TypeInfo) bool {
	if t.SchemaConflict {
		return false
	}
	if m, known := catalogueMetric(t); known {
		return !m.Unsupported
	}
	if len(t.Numeric) == 0 || homePlainNumber(t) {
		return true
	}
	m := metricFor(t)
	if m.Money || m.Latest {
		return true
	}
	if t.ID == "com.voptrack.track.pet_feeding" && m.Field == "mass" {
		return true
	}
	if t.ID == "com.voptrack.track.chore_completion" && m.Field == "duration" {
		return true
	}
	for _, f := range t.Numeric {
		if f.ID == m.Field && (f.Annotations["meaning"] == "additive" || f.Annotations["meaning"] == "sample") {
			return true
		}
	}
	return false
}
