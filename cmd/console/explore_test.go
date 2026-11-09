package main

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestComparableWindowsUseSameLocalCutoff(t *testing.T) {
	tz, e := time.LoadLocation("Europe/Helsinki")
	must(t, e)
	now := time.Date(2026, 11, 5, 14, 37, 0, 0, tz)
	for _, key := range []string{"today", "week", "month", "year"} {
		w := periodWindow(key, now)
		if !w.Comparable || w.PreviousTo.Hour() != 14 || w.PreviousTo.Minute() != 37 || !w.To.Equal(now) || !w.PreviousFrom.Before(w.PreviousTo) {
			t.Fatal("bad elapsed cutoff", key)
		}
		if key == "week" && w.From.Weekday() != time.Monday {
			t.Fatal("week did not start on Monday")
		}
		if key == "month" && (w.PreviousTo.Month() != time.October || w.PreviousTo.Day() != 5) {
			t.Fatal("wrong previous month cutoff")
		}
	}
	if periodWindow("month", time.Date(2026, 3, 31, 12, 0, 0, 0, tz)).Comparable {
		t.Fatal("short month pretended comparable")
	}
	if periodWindow("year", time.Date(2024, 2, 29, 12, 0, 0, 0, tz)).Comparable {
		t.Fatal("leap day pretended comparable")
	}
	if periodWindow("today", time.Date(2026, 3, 30, 3, 30, 0, 0, tz)).Comparable {
		t.Fatal("nonexistent DST cutoff pretended comparable")
	}
	// The repeated autumn hour must remain two distinct selectable buckets.
	first := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC).In(tz)
	second := first.Add(time.Hour)
	if bucketLabel(first, "hour") == bucketLabel(second, "hour") {
		t.Fatal("repeated hours collapsed")
	}
	for _, at := range []time.Time{first, second} {
		start, e := bucketStart(bucketLabel(at, "hour"), "hour", tz)
		if e != nil || at.Before(start) || !at.Before(nextBucket(start, "hour")) {
			t.Fatal("hour link lost its offset")
		}
	}
}
func TestPeriodValuesAreWeightedAndDoNotInventComparisons(t *testing.T) {
	s := valueSeries{Metric: valueMetric{Measure: MeasureAverage, Field: "value"}}
	for _, values := range [][]int64{{10}, {20, 30, 40}} {
		p := Point{}
		for _, v := range values {
			p.Tally.addValue(big.NewRat(v, 1))
		}
		s.Result.Points = append(s.Result.Points, p)
	}
	if v := seriesValue(s); v == nil || v.Cmp(big.NewRat(25, 1)) != 0 {
		t.Fatal("averaged bucket averages")
	}
	if text := comparison(big.NewRat(5, 1), big.NewRat(0, 1), valueMetric{}, ""); strings.Contains(text, "%") {
		t.Fatal("invented percent from zero")
	}
	if text := comparison(nil, big.NewRat(10, 1), valueMetric{}, ""); !strings.Contains(text, "unavailable") {
		t.Fatal("invented missing current value")
	}
	s.Result.Truncated = true
	if seriesValue(s) != nil {
		t.Fatal("partial series yielded a confident headline")
	}
}
func TestMemberSearchAndSeriesSelectionShareTheSameRows(t *testing.T) {
	fields := []Field{{ID: "purpose", Type: "text"}, {ID: "unit", Type: "text"}}
	rows := []readRecord{syntheticValueRecord(1, map[string]any{"purpose": "Synthetic Groceries", "amount": json.Number("120"), "currency": "EUR"}), syntheticValueRecord(2, map[string]any{"purpose": "Other", "amount": json.Number("500"), "currency": "USD"}), syntheticValueRecord(3, map[string]any{"purpose": "Synthetic Groceries", "amount": json.Number("300"), "currency": "EUR"})}
	for i := range rows {
		rows[i].Fields = fields
	}
	rows[2].Dataset = "two"
	filtered := filterRecords(rows, memberKey("one"), "GROCERIES")
	if len(filtered) != 1 || filtered[0].ID != rows[0].ID {
		t.Fatal("search/member intersection wrong")
	}
	typ := catalogueTypeForTest(t, "com.voptrack.track.expense")
	series := viewSeries(filtered, typ, time.UTC, time.Time{}, false, "day")
	if len(series) != 1 || len(series[0].Records) != 1 || seriesValue(series[0]).Cmp(big.NewRat(6, 5)) != 0 {
		t.Fatal("chart differs from filtered records")
	}
	q := url.Values{"period": {"year"}, "member": {memberKey("one")}, "q": {"groceries"}, "type": {typ.ID}, "series": {series[0].Key}, "offset": {"30"}}
	c := buildChart(series[0].Result, true)
	linkChart(&c, q, "day")
	if !c.Interactive || len(c.Dots) != 1 {
		t.Fatal("no chart targets")
	}
	u, e := url.Parse(c.Dots[0].URL)
	must(t, e)
	v := u.Query()
	if v.Get("q") != "groceries" || v.Get("member") != memberKey("one") || v.Get("bucket") != "2026-11-01" || v.Get("offset") != "" || u.Fragment != "records" {
		t.Fatal("chart link lost filters")
	}
}
func TestCustomRangeAndInvalidDates(t *testing.T) {
	now := time.Date(2026, 11, 5, 15, 0, 0, 0, time.UTC)
	w, ok := requestedWindow(url.Values{"from": {"2026-10-01"}, "to": {"2026-10-31"}}, now)
	if !ok || w.To.Day() != 1 || w.To.Month() != time.November || w.To.Sub(w.From) != w.PreviousTo.Sub(w.PreviousFrom) {
		t.Fatal("custom interval mismatch")
	}
	if _, ok := requestedWindow(url.Values{"from": {"2026-12-01"}}, now); ok {
		t.Fatal("future/reversed range accepted")
	}
}

func TestAttachmentBadgesDeduplicateAuthorizedLinks(t *testing.T) {
	var cursorCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-reader" {
			t.Error("badge used another authority")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/meta"):
			writeJSON(w, 200, map[string]any{"types": []Meta{{ID: "com.voptrack.media_attachment"}}})
		case strings.HasSuffix(r.URL.Path, ":page"):
			cursorCalls++
			writeJSON(w, 200, map[string]any{"cursor": "synthetic"})
		case strings.HasSuffix(r.URL.Path, ":next"):
			fields := []Field{{ID: "subject", Type: "reference"}, {ID: "media", Type: "reference"}}
			writeJSON(w, 200, recordSet{Meta: Meta{Fields: fields}, Records: []wireRecord{
				{ID: recordIdentity{"relation", "one"}, Type: "com.voptrack.media_attachment", Values: Row{{Value: objectReference{"one", "receipt", "first"}}, {Value: objectReference{"one", "com.voptrack.media.image", "picture"}}}},
				{ID: recordIdentity{"foreign", "one"}, Type: "com.voptrack.media_attachment", Values: Row{{Value: objectReference{"private", "receipt", "first"}}, {Value: objectReference{"one", "com.voptrack.media.image", "other"}}}},
			}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	a := &app{tessera: &Tessera{internal: server.URL, http: server.Client()}}
	src := Source{Dataset: "one", Token: func(context.Context) (string, error) { return "synthetic-reader", nil }}
	row := readRecord{Dataset: "one", Type: "receipt", ID: "first", Fields: []Field{{ID: "photo", Type: "media"}, {ID: "document", Type: "media"}}, Values: map[string]any{"photo": "[\"one\",\"com.voptrack.media.image\",\"picture\"]", "document": "store//document"}}
	badges := a.attachmentBadges(context.Background(), []Source{src}, []readRecord{row})
	if cursorCalls != 1 || badges[recordMapKey(row)] != "2 attachments" {
		t.Fatal("badge counted duplicates or a foreign subject")
	}
}

func TestRepeatedHourChartsKeepChronologicalOrder(t *testing.T) {
	tz, e := time.LoadLocation("Europe/Helsinki")
	must(t, e)
	first := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	rows := []readRecord{syntheticValueRecord(1, map[string]any{"value": json.Number("10")}), syntheticValueRecord(2, map[string]any{"value": json.Number("20")})}
	rows[0].When = first
	rows[1].When = second
	series := seriesForBucket(rows, TypeInfo{ID: "custom", Numeric: []Field{{ID: "value", Type: "float"}}}, tz, first, false, "hour")
	if len(series) != 1 || len(series[0].Result.Points) != 2 || series[0].Result.Points[0].Label != bucketLabel(first.In(tz), "hour") {
		t.Fatal("DST hour chart reversed or collapsed")
	}
}
