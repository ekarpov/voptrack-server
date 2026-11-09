package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/url"
	"sort"
	"strings"
	"time"
)

type exploreWindow struct {
	Current                             bool
	Key, Label, Bucket, ComparisonLabel string
	From, To, PreviousFrom, PreviousTo  time.Time
	Comparable                          bool
}
type exploreOption struct {
	ID, Name, URL string
	Selected      bool
}
type exploreControls struct {
	Month                                                 string
	View, Subject                                         string
	ClearURL, SearchClearURL                              string
	Period, Query, Member, Bucket, Range, ComparisonLabel string
	From, To                                              string
	Scope, Type, Series, Category                         string
	Periods, Members                                      []exploreOption
	Subjects, Categories                                  []exploreOption
	Spending, Currency                                    string
}

// Calendar arithmetic preserves local wall time across DST. Month/year shifts
// clamp leap days and month ends rather than letting time.Date spill forward.
func calendarShift(t time.Time, months int) time.Time {
	first := time.Date(t.Year(), t.Month()+time.Month(months), 1, 0, 0, 0, 0, t.Location())
	last := first.AddDate(0, 1, -1).Day()
	day := t.Day()
	if day > last {
		day = last
	}
	return time.Date(first.Year(), first.Month(), day, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}
func periodWindow(key string, now time.Time) exploreWindow {
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	w := exploreWindow{Key: key, To: now, Bucket: "day", Comparable: true, Current: true}
	switch key {
	case "all":
		w.Label = "All history"
		w.From = time.Time{}
		w.Bucket = "month"
		w.Comparable = false
		w.Current = false
		w.ComparisonLabel = "All history has no previous period"
		return w
	case "today":
		w.Label = "Today so far"
		w.From = midnight
		w.PreviousFrom = midnight.AddDate(0, 0, -1)
		w.PreviousTo = now.AddDate(0, 0, -1)
		w.Bucket = "hour"
	case "week":
		w.Label = "Week so far"
		w.From = midnight.AddDate(0, 0, -(int(now.Weekday())+6)%7)
		w.PreviousFrom = w.From.AddDate(0, 0, -7)
		w.PreviousTo = now.AddDate(0, 0, -7)
	case "year":
		w.Label = "Year so far"
		w.From = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
		w.PreviousFrom = w.From.AddDate(-1, 0, 0)
		w.PreviousTo = calendarShift(now, -12)
		w.Bucket = "month"
	default:
		w.Key = "month"
		w.Label = "Month so far"
		w.From = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		w.PreviousFrom = w.From.AddDate(0, -1, 0)
		w.PreviousTo = calendarShift(now, -1)
	}
	if (w.Key == "month" || w.Key == "year") && w.PreviousTo.Day() != now.Day() {
		w.Comparable = false
	}
	w.ComparisonLabel = "Compared with " + w.PreviousFrom.Format("2 Jan 2006") + " – " + w.PreviousTo.Format("2 Jan 2006, 15:04") + " (same elapsed period)"
	if !w.Comparable {
		w.ComparisonLabel = "Previous calendar period is shorter at this cutoff; comparison unavailable"
	}
	if w.PreviousTo.Hour() != now.Hour() || w.PreviousTo.Minute() != now.Minute() {
		w.Comparable = false
		w.ComparisonLabel = "Previous period has no matching local-time cutoff; comparison unavailable"
	}
	return w
}
func bucketLabel(t time.Time, bucket string) string {
	switch bucket {
	case "hour":
		return t.Format("2006-01-02 15:00 -07:00")
	case "day":
		return t.Format("2006-01-02")
	default:
		return t.Format("2006-01")
	}
}
func bucketStart(label, bucket string, tz *time.Location) (time.Time, error) {
	layout := "2006-01"
	if bucket == "day" {
		layout = "2006-01-02"
	}
	if bucket == "hour" {
		layout = "2006-01-02 15:00 -07:00"
	}
	return time.ParseInLocation(layout, label, tz)
}
func nextBucket(t time.Time, bucket string) time.Time {
	if bucket == "hour" {
		return t.Add(time.Hour)
	}
	if bucket == "day" {
		return t.AddDate(0, 0, 1)
	}
	return t.AddDate(0, 1, 0)
}
func memberKey(dataset string) string {
	sum := sha256.Sum256([]byte(dataset))
	return hex.EncodeToString(sum[:12])
}
func controls(q url.Values, sources []Source, w exploreWindow, path string) exploreControls {
	d := exploreControls{Month: q.Get("month"), View: q.Get("view"), Subject: q.Get("subject"), From: q.Get("from"), To: q.Get("to"), Scope: q.Get("scope"), Type: q.Get("type"), Series: q.Get("series"), Category: q.Get("category"), Period: w.Key, Query: strings.TrimSpace(q.Get("q")), Member: q.Get("member"), Bucket: q.Get("bucket"), Currency: q.Get("currency"), Spending: q.Get("spend"), Range: w.Label + " · " + w.From.Format("2 Jan") + " – " + w.To.Format("2 Jan 2006, 15:04 MST"), ComparisonLabel: w.ComparisonLabel}
	if len([]rune(d.Query)) > 200 {
		d.Query = string([]rune(d.Query)[:200])
	}
	if w.To.Hour() == 0 && w.To.Minute() == 0 && w.To.Second() == 0 && w.To.Nanosecond() == 0 {
		d.Range = w.Label + " · " + shortDateRange(w.From, w.To) + " " + w.From.Format("2006 MST")
	}
	if w.Key == "all" {
		d.Range = "All history · through " + w.To.Format("2 Jan 2006, 15:04 MST")
	}
	for _, p := range []struct{ key, name string }{{"today", "Today"}, {"week", "Week"}, {"month", "Month"}, {"year", "Year"}, {"all", "All"}} {
		v := qCopy(q)
		v.Set("period", p.key)
		for _, key := range []string{"bucket", "offset", "from", "to", "month"} {
			v.Del(key)
		}
		d.Periods = append(d.Periods, exploreOption{ID: p.key, Name: p.name, URL: path + "?" + v.Encode(), Selected: p.key == w.Key})
	}
	d.Members = append(d.Members, exploreOption{ID: "", Name: "All members", Selected: d.Member == ""})
	for _, src := range sources {
		key := memberKey(src.Dataset)
		d.Members = append(d.Members, exploreOption{ID: key, Name: src.Name, Selected: key == d.Member})
	}
	if d.Query != "" {
		v := qCopy(q)
		for _, key := range []string{"q", "offset", "bucket"} {
			v.Del(key)
		}
		d.SearchClearURL = path + "?" + v.Encode()
	}
	if d.Member != "" {
		v := qCopy(q)
		for _, key := range []string{"member", "offset", "bucket"} {
			v.Del(key)
		}
		d.ClearURL = path + "?" + v.Encode()
	}
	validMember := d.Member == ""
	for _, m := range d.Members {
		validMember = validMember || m.Selected
	}
	if !validMember {
		d.Members = append(d.Members, exploreOption{ID: d.Member, Name: "Member no longer available", Selected: true})
	}
	return d
}
func qCopy(q url.Values) url.Values {
	v := url.Values{}
	for k, vs := range q {
		v[k] = append([]string(nil), vs...)
	}
	return v
}
func filterRecords(rows []readRecord, member, query string) []readRecord {
	var out []readRecord
	query = strings.ToLower(strings.TrimSpace(query))
	for _, row := range rows {
		if member != "" && memberKey(row.Dataset) != member {
			continue
		}
		match := query == ""
		if !match {
			for _, f := range row.Fields {
				if catalogueInternalField(row.Type, f.ID) {
					continue
				}
				if f.Type != "text" && f.Type != "blob" {
					continue
				}
				if row.Values[f.ID] == nil {
					continue
				}
				text := readableValue(f, row.Values[f.ID])
				if f.Type == "blob" {
					text = displayBlob(row.Values[f.ID])
				}
				if strings.Contains(strings.ToLower(text), query) {
					match = true
					break
				}
			}
		}
		if match {
			out = append(out, row)
		}
	}
	return out
}
func inWindow(rows []readRecord, from, to time.Time) []readRecord {
	var out []readRecord
	for _, r := range rows {
		if !r.When.Before(from) && r.When.Before(to) {
			out = append(out, r)
		}
	}
	return out
}
func seriesValue(s valueSeries) *big.Rat {
	if s.Result.Truncated {
		return nil
	}
	var tally Tally
	var latest *big.Rat
	for _, p := range s.Result.Points {
		if s.Metric.Latest {
			if p.LatestValue != nil {
				latest = p.LatestValue
			}
			continue
		}
		tally.Rows += p.Tally.Rows
		tally.Values += p.Tally.Values
		tally.merge(p.Tally.Sum, p.Tally.Min, p.Tally.Max)
	}
	if s.Metric.Latest {
		return latest
	}
	return pointValue(Result{Spec: Spec{Measure: s.Metric.Measure, Field: s.Metric.Field}}, Point{Tally: tally})
}
func formattedValue(v *big.Rat, m valueMetric, unit string) string {
	if v == nil {
		return "—"
	}
	if m.Money {
		return unit + " " + v.FloatString(2)
	}
	s := display(v, 2)
	if unit != "" {
		s += " " + unit
	}
	return s
}
func comparison(current, previous *big.Rat, m valueMetric, unit string) string {
	if current == nil || previous == nil {
		return "Comparison unavailable · both periods need readable values"
	}
	delta := new(big.Rat).Sub(current, previous)
	if delta.Sign() == 0 {
		return "Unchanged from the previous period"
	}
	sign := "+"
	if delta.Sign() < 0 {
		sign = "−"
	}
	abs := new(big.Rat).Abs(delta)
	text := sign + formattedValue(abs, m, unit)
	if previous.Sign() != 0 {
		percent := new(big.Rat).Mul(new(big.Rat).Quo(abs, new(big.Rat).Abs(previous)), big.NewRat(100, 1))
		text += " (" + sign + display(percent, 1) + "%)"
	}
	return text + " vs previous period"
}
func linkChart(c *Chart, q url.Values, bucket string) {
	c.Interactive = true
	for i := range c.Dots {
		v := qCopy(q)
		v.Set("bucket", c.Dots[i].Label)
		v.Del("offset")
		c.Dots[i].URL = "/tracks?" + v.Encode() + "#records"
		c.Dots[i].Selected = q.Get("bucket") == c.Dots[i].Label
	}
	for i := range c.Bars {
		v := qCopy(q)
		v.Set("bucket", c.Bars[i].Label)
		v.Del("offset")
		c.Bars[i].URL = "/tracks?" + v.Encode() + "#records"
		if at, e := bucketStart(c.Bars[i].Label, bucket, time.UTC); e == nil {
			layout := "Jan 2006"
			if bucket == "hour" {
				layout = "15:04"
			}
			if bucket == "day" {
				layout = "2 Jan"
			}
			c.Bars[i].AxisLabel = at.Format(layout)
		}
	}
}
func recordSummary(row readRecord, typ TypeInfo) string {
	m := recordMetricFor(typ, row)
	summary := ""
	if m.Field != "" {
		v, e := rat(row.Values[m.Field])
		if e == nil {
			if m.Money && v != nil {
				v = moneyMajor(v)
			}
			unit, _ := row.Values[m.UnitField].(string)
			summary = formattedValue(v, m, unit)
		}
	}
	for _, key := range []string{"purpose", "description", "food", "location", "text", "name", "context"} {
		if text, ok := row.Values[key].(string); ok && text != "" {
			if summary != "" {
				summary += " · "
			}
			summary += text
			break
		}
	}
	if summary == "" {
		summary = "Record details"
	}
	return summary
}
func recordsLabel(n int, partial bool) string {
	if partial {
		return fmt.Sprintf("At least %d matching records read", n)
	}
	if n == 1 {
		return "1 matching record"
	}
	return fmt.Sprintf("%d matching records", n)
}

func filterQuery(c exploreControls) url.Values {
	q := url.Values{"period": {c.Period}}
	if c.Month != "" && c.Period == "month" {
		q.Set("month", c.Month)
	}
	if c.View != "" {
		q.Set("view", c.View)
	}
	if c.Subject != "" {
		q.Set("subject", c.Subject)
	}
	if c.From != "" {
		q.Set("from", c.From)
	}
	if c.To != "" {
		q.Set("to", c.To)
	}
	if c.Query != "" {
		q.Set("q", c.Query)
	}
	if c.Member != "" {
		q.Set("member", c.Member)
	}
	if c.Bucket != "" {
		q.Set("bucket", c.Bucket)
	}
	return q
}
func viewSeries(rows []readRecord, t TypeInfo, tz *time.Location, from time.Time, partial bool, bucket string) []valueSeries {
	m := metricFor(t)
	if m.Unsupported {
		return nil
	}
	if len(t.Numeric) == 0 && !m.Catalogue {
		m.Measure = MeasureCount
		m.Label = "Records"
	}
	if m.Measure != MeasureCount && len(t.Numeric) > 0 {
		return seriesForBucket(rows, t, tz, from, partial, bucket)
	}
	s := valueSeries{Key: "events", Label: "Records", Metric: m, Records: rows, Result: Result{Spec: Spec{Measure: MeasureCount}, Truncated: partial}}
	buckets := map[string]*Point{}
	for _, r := range rows {
		if r.When.IsZero() {
			s.Result.Truncated = true
			continue
		}
		label := bucketLabel(r.When.In(tz), bucket)
		if buckets[label] == nil {
			buckets[label] = &Point{Label: label}
		}
		buckets[label].Tally.Rows++
	}
	var labels []string
	for label := range buckets {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool {
		a, _ := bucketStart(labels[i], bucket, tz)
		b, _ := bucketStart(labels[j], bucket, tz)
		return a.Before(b)
	})
	for _, label := range labels {
		s.Result.Points = append(s.Result.Points, *buckets[label])
	}
	if len(rows) == 0 {
		return nil
	}
	return []valueSeries{s}
}
func (a *app) periodCards(ctx context.Context, sources []Source, t TypeInfo, w exploreWindow, c exploreControls) ([]card, error) {
	rows, cut, e := readTrackRecords(ctx, a.tessera, sources, t, w.From, w.To)
	if e != nil {
		return nil, e
	}
	var previous []readRecord
	prevCut := false
	if w.Comparable {
		previous, prevCut, e = readTrackRecords(ctx, a.tessera, sources, t, w.PreviousFrom, w.PreviousTo)
	}
	if e != nil {
		return nil, e
	}
	rows = filterRecords(rows, c.Member, c.Query)
	previous = filterRecords(previous, c.Member, c.Query)
	current := viewSeries(rows, t, a.tz, w.From, cut, w.Bucket)
	before := viewSeries(previous, t, a.tz, w.PreviousFrom, prevCut, w.Bucket)
	var cards []card
	if len(rows) == 0 && !cut && (c.Member != "" || c.Query != "") {
		return nil, nil
	}
	if len(current) == 0 {
		current = []valueSeries{{Records: rows, Metric: metricFor(t), Result: Result{Truncated: cut}}}
	}
	for _, s := range current {
		res := s.Result
		card := card{Type: t, Trend: &res, ValueView: true, MetricLabel: s.Label + " · " + metricMeasure(s.Metric)}
		counts := map[string]*Point{}
		for _, r := range s.Records {
			if counts[r.Dataset] == nil {
				counts[r.Dataset] = &Point{Label: r.Member}
			}
			counts[r.Dataset].Tally.Rows++
		}
		for _, p := range counts {
			card.PerStore.Points = append(card.PerStore.Points, *p)
		}
		prepareHomeCard(&card)
		card.Headline = formattedValue(seriesValue(s), s.Metric, s.Unit)
		card.HeadlineLabel = w.Label
		if card.Headline == "—" {
			card.HeadlineLabel = "No readable values · " + w.Label
		}
		var old *big.Rat
		for _, b := range before {
			if b.Key == s.Key {
				old = seriesValue(b)
			}
		}
		card.Comparison = comparison(seriesValue(s), old, s.Metric, s.Unit)
		if !w.Comparable {
			card.Comparison = w.ComparisonLabel
		}
		v := filterQuery(c)
		v.Del("bucket")
		v.Set("scope", "home")
		v.Set("type", t.ID)
		if s.Key != "" {
			v.Set("series", s.Key)
		}
		card.Link = "/tracks?" + v.Encode()
		linkChart(&card.Chart, v, w.Bucket)
		linkChart(&card.CompactChart, v, w.Bucket)
		if s.Label == "" {
			card.MetricLabel = "No chartable values in this period"
		}
		cards = append(cards, card)
	}
	return cards, nil
}

func requestedWindow(q url.Values, now time.Time) (exploreWindow, bool) {
	w := periodWindow(q.Get("period"), now)
	if q.Get("from") == "" && q.Get("to") == "" && q.Get("month") != "" && w.Key == "month" {
		from, err := time.ParseInLocation("2006-01", q.Get("month"), now.Location())
		current := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		if err != nil || from.Year() < 1 || from.After(current) {
			return w, false
		}
		if from.Equal(current) {
			return w, true
		}
		w.Current = false
		w.From = from
		w.To = from.AddDate(0, 1, 0)
		w.PreviousFrom = from.AddDate(0, -1, 0)
		w.PreviousTo = from
		w.Label = from.Format("January 2006")
		w.ComparisonLabel = "Compared with " + shortDateRange(w.PreviousFrom, w.PreviousTo) + " (previous full month)"
		return w, true
	}
	if q.Get("from") == "" && q.Get("to") == "" {
		return w, true
	}
	from, e := time.ParseInLocation("2006-01-02", q.Get("from"), now.Location())
	if e != nil {
		return w, false
	}
	to := now
	if q.Get("to") != "" {
		at, e := time.ParseInLocation("2006-01-02", q.Get("to"), now.Location())
		if e != nil {
			return w, false
		}
		to = at.AddDate(0, 0, 1)
		if to.After(now) {
			to = now
		}
	}
	if !from.Before(to) || to.Sub(from) > 20*366*24*time.Hour {
		return w, false
	}
	w.Key = "custom"
	w.Current = to.Equal(now)
	w.Label = "Selected dates"
	w.From = from
	w.To = to
	w.PreviousTo = from
	w.PreviousFrom = from.Add(-to.Sub(from))
	w.Comparable = true
	w.Bucket = "day"
	if to.Sub(from) > 62*24*time.Hour {
		w.Bucket = "month"
	}
	w.ComparisonLabel = "Compared with " + w.PreviousFrom.Format("2 Jan 2006, 15:04") + " – " + w.PreviousTo.Format("2 Jan 2006, 15:04") + " (previous interval of equal duration)"
	return w, true
}
