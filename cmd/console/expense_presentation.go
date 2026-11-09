package main

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// The same explicit range governs the total, trend, category and member views.
func shortDateRange(from, to time.Time) string {
	if to.Hour() == 0 && to.Minute() == 0 && to.Second() == 0 && to.Nanosecond() == 0 {
		to = to.Add(-time.Nanosecond)
	}
	if from.Year() == to.Year() && from.Month() == to.Month() {
		if from.Day() == to.Day() {
			return from.Format("2 Jan")
		}
		return fmt.Sprintf("%d–%s", from.Day(), to.Format("2 Jan"))
	}
	return from.Format("2 Jan 2006") + " – " + to.Format("2 Jan 2006")
}
func prepareExpensePresentation(d *householdView, q url.Values, w exploreWindow, now time.Time) {
	d.NextMonth = ""
	d.ExpenseRange = shortDateRange(w.From, w.To)
	end := w.To.Add(-time.Nanosecond)
	if w.Key == "custom" && w.From.Year() == end.Year() && w.From.Month() == end.Month() {
		d.ExpenseRange += " " + w.From.Format("2006")
	}
	if w.Current {
		d.ExpenseRange += " so far"
	}
	if w.Key == "all" {
		d.ExpenseRange = "All history through " + w.To.Format("2 Jan 2006")
	}

	d.TrendTitle = "Daily spending"
	if w.Bucket == "month" {
		d.TrendTitle = "Monthly spending"
	}
	if w.Bucket == "hour" {
		d.TrendTitle = "Hourly spending"
	}
	if d.Section == "expenses" {
		d.Subtitle = d.ExpenseRange
	}
	d.ExpenseScope = "All members"
	for _, option := range d.Controls.Members {
		if option.Selected && option.ID != "" {
			d.ExpenseScope = option.Name
		}
	}
	if d.Change != "" {
		d.ExpenseComparison = strings.Replace(d.Change, "vs previous period", "vs "+shortDateRange(w.PreviousFrom, w.PreviousTo), 1)
	}
	if w.Key != "month" {
		return
	}
	d.MonthValue = w.From.Format("2006-01")
	d.MonthLabel = w.From.Format("January 2006")
	d.MonthMax = now.Format("2006-01")
	link := func(at time.Time) string {
		v := houseQuery(q)
		for _, key := range []string{"from", "to", "bucket"} {
			v.Del(key)
		}
		v.Set("period", "month")
		v.Set("month", at.Format("2006-01"))
		return "/home?" + v.Encode()
	}
	d.PreviousMonth = link(w.From.AddDate(0, -1, 0))
	next := w.From.AddDate(0, 1, 0)
	if !next.After(time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())) {
		d.NextMonth = link(next)
	}
}
