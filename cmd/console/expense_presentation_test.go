package main

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestExpenseMonthWindowsAndComparisonDates(t *testing.T) {
	tz, err := time.LoadLocation("Europe/Helsinki")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 5, 19, 3, 0, 0, tz)
	old, ok := requestedWindow(url.Values{"period": {"month"}, "month": {"2026-10"}}, now)
	if !ok || !old.From.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, tz)) || !old.To.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, tz)) || shortDateRange(old.PreviousFrom, old.PreviousTo) != "1–30 Sep" {
		t.Fatal("historical month or previous full month is wrong")
	}
	if old.Current {
		t.Fatal("a historical full month was marked in progress")
	}
	current, ok := requestedWindow(url.Values{"month": {"2026-11"}}, now)
	if !ok || !current.To.Equal(now) || shortDateRange(current.PreviousFrom, current.PreviousTo) != "1–5 Oct" {
		t.Fatal("current month lost its matching elapsed comparison")
	}
	for _, value := range []string{"2026-12", "not-a-month", "2026-13"} {
		if _, ok := requestedWindow(url.Values{"month": {value}}, now); ok {
			t.Fatalf("invalid or future month accepted: %q", value)
		}
	}
	custom, ok := requestedWindow(url.Values{"month": {"2026-10"}, "from": {"2026-09-02"}, "to": {"2026-09-03"}}, now)
	if custom.Current {
		t.Fatal("a completed custom range was marked in progress")
	}
	if !ok || custom.Key != "custom" || shortDateRange(custom.From, custom.To) != "2–3 Sep" {
		t.Fatal("custom dates did not override month browsing")
	}
	partial, ok := requestedWindow(url.Values{"from": {"2026-11-01"}, "to": {"2026-11-05"}}, now)
	if !ok || !partial.Current {
		t.Fatal("a custom range ending today lost its current cutoff")
	}

}
func TestExpensePresentationTiesSummaryToRangeAndPreservesFilters(t *testing.T) {
	now := time.Date(2026, 11, 5, 19, 3, 0, 0, time.UTC)
	q := url.Values{"view": {"expenses"}, "month": {"2026-10"}, "period": {"month"}, "member": {"member"}, "q": {"bread"}, "currency": {"EUR"}, "spend": {"category"}, "bucket": {"2026-10-01"}, "offset": {"20"}}
	window, _ := requestedWindow(q, now)
	d := householdView{Section: "expenses", Change: "+EUR 10.00 vs previous period", Controls: exploreControls{Members: []exploreOption{{ID: "member", Name: "Member", Selected: true}}}}
	prepareExpensePresentation(&d, q, window, now)
	if d.ExpenseRange != "1–31 Oct" || d.ExpenseComparison != "+EUR 10.00 vs 1–30 Sep" || d.ExpenseScope != "Member" || d.MonthLabel != "October 2026" {
		t.Fatalf("ambiguous historical period: %+v", d)
	}
	next, _ := url.Parse(d.NextMonth)
	for key, want := range map[string]string{"month": "2026-11", "member": "member", "q": "bread", "currency": "EUR", "spend": "category"} {
		if next.Query().Get(key) != want {
			t.Errorf("month navigation lost %s", key)
		}
	}
	if next.Query().Get("bucket") != "" || next.Query().Get("offset") != "" {
		t.Fatal("month navigation kept a stale record selection")
	}
	current := periodWindow("month", now)
	prepareExpensePresentation(&d, url.Values{}, current, now)
	if d.NextMonth != "" || !strings.Contains(d.ExpenseRange, "so far") || !strings.Contains(d.ExpenseComparison, "1–5 Oct") {
		t.Fatal("current month is not distinguished from a complete month")
	}
	custom, _ := requestedWindow(url.Values{"from": {"2025-10-01"}, "to": {"2025-10-07"}}, now)
	prepareExpensePresentation(&d, url.Values{}, custom, now)
	if d.ExpenseRange != "1–7 Oct 2025" {
		t.Fatal("custom range lost its year")
	}
	c := controls(q, nil, window, "/tracks")
	if filterQuery(c).Get("month") != "2026-10" {
		t.Fatal("Track drill-down lost the selected month")
	}
	for _, period := range c.Periods {
		u, _ := url.Parse(period.URL)
		if u.Query().Get("month") != "" {
			t.Fatal("quick period switch kept a stale calendar month")
		}
	}
}
