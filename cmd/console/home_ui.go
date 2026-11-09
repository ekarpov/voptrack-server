package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type homeCategory struct {
	ID, Name, URL string
	Count         int
}

var homeCategories = []homeCategory{{"money", "Money", "", 0}, {"house", "House", "", 0}, {"activity", "Activity", "", 0}, {"health", "Health", "", 0}, {"pets", "Pets", "", 0}, {"learning", "Learning", "", 0}, {"general", "General", "", 0}}

func homeInfrastructure(id string) bool {
	if catalogueNonTrack(appTracks, id) {
		return true
	}
	if strings.HasPrefix(id, "com.voptrack.media") {
		return true
	}
	switch id {
	case sharedSubjectType, homeCopyLabelType, "com.voptrack.home_copy", "com.voptrack.category", "com.voptrack.category_assignment", "com.voptrack.subject", "com.voptrack.source", "com.voptrack.track", "com.voptrack.track.exercise_set", "com.voptrack.track.run_split", "com.voptrack.track.activity_segment", "com.voptrack.track.workout_effort", "com.voptrack.track.gps_position":
		return true
	}
	if strings.HasPrefix(id, "com.voptrack.") {
		_, known := appTracks.Documents[id]
		return !known
	}
	return false
}

// Without the app's complete measurement contract, do not average money minor
// units or counters, or combine values carrying record-specific units. Bare
// fixed decimals remain the existing Console's exact-money convention.
func homePlainNumber(t TypeInfo) bool {
	if len(t.Numeric) == 0 || t.Numeric[0].Type != "fixed" {
		return false
	}
	for _, f := range t.Groups {
		if strings.Contains(strings.ToLower(f.ID), "unit") || strings.Contains(strings.ToLower(f.ID), "currency") {
			return false
		}
	}
	return true
}

func prepareHomeCard(c *card) {
	c.People = 0
	c.Records = 0
	c.Category = "general"
	if known, ok := homeTrackIdentities[c.Type.ID]; ok {
		c.Category = known.Category
		if c.Type.Name == "" || c.Type.Name == known.Key {
			c.Type.Name = known.Name
		}
	}
	for _, category := range homeCategories {
		if category.ID == c.Category {
			c.CategoryName = category.Name
		}
	}
	for _, p := range c.PerStore.Points {
		if p.Tally.Rows > 0 {
			c.People++
			c.Records += p.Tally.Rows
		}
	}
	c.Headline, c.HeadlineLabel = strconv.FormatInt(c.Records, 10), "Shared records · all time"
	c.Incomplete = len(c.PerStore.Skipped) > 0 || c.PerStore.Truncated
	if c.Incomplete {
		c.Headline = "—"
		c.HeadlineLabel = "Some shared data is unavailable"
	}
	if c.Trend != nil {
		c.Chart = buildChart(*c.Trend, metricLine(metricFor(c.Type)))
		c.CompactChart = buildCompactChart(*c.Trend, metricLine(metricFor(c.Type)))
		c.Incomplete = c.Incomplete || len(c.Trend.Skipped) > 0 || c.Trend.Truncated
		if c.Incomplete {
			c.Headline = "—"
			c.HeadlineLabel = "Some shared data is unavailable"
			return
		}
		for i := len(c.Trend.Points) - 1; i >= 0; i-- {
			p := c.Trend.Points[i]
			v := pointValue(*c.Trend, p)
			if v == nil {
				continue
			}
			c.Headline = display(v, 2)
			month := p.Label
			if at, err := time.Parse("2006-01", month); err == nil {
				month = at.Format("January 2006")
			}
			c.HeadlineLabel = "Latest month with records · " + month
			return
		}
	} else {
		c.Chart = c.PerChart
		c.CompactChart = buildCompactChart(c.PerStore, false)
		c.Link = c.PerLink
	}
}

func prepareHomeOverview(d *overviewData, selected string, now time.Time) {
	seen := map[string]bool{}
	for _, c := range d.Cards {
		seen[c.Type.ID] = true
	}
	d.TotalTracks = len(seen)
	d.PeriodLabel = now.AddDate(-1, 1, 0).Format("Jan 2006") + " – " + now.Format("Jan 2006")
	counts := map[string]int{}
	seen = map[string]bool{}
	for _, c := range d.Cards {
		if !seen[c.Category+"/"+c.Type.ID] {
			counts[c.Category]++
			seen[c.Category+"/"+c.Type.ID] = true
		}
	}
	for _, category := range homeCategories {
		if counts[category.ID] > 0 {
			category.Count = counts[category.ID]
			d.Categories = append(d.Categories, category)
			if category.ID == selected {
				d.SelectedCategory = selected
			}
		}
	}
	if d.SelectedCategory != "" {
		var filtered []card
		for _, c := range d.Cards {
			if c.Category == selected {
				filtered = append(filtered, c)
			}
		}
		d.Cards = filtered
	}
}

func (a *app) personalHome() string {
	if a.cfg.Variant == "research" {
		return "/"
	}
	return "/mine"
}
func (a *app) manageHome(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if !a.isOwner(r, sess) {
		a.fail(w, r, sess, http.StatusForbidden, "owner_only", nil)
		return
	}
	a.render(w, r, sess, "manage", http.StatusOK, nil)
}
