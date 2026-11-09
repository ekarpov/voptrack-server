package main

import (
	"strconv"
	"strings"
)

// Presentation helpers interpret existing display data only. Validation,
// authorization and API error responses remain in their original handlers.
func formErrorKey(problem string) string {
	switch problem {
	case "setup_stale", "setup_recovery", "setup_publish_needed", "setup_unavailable":
		return problem
	case "a study's address is 2 to 63 lowercase letters, digits and hyphens":
		return "form_slug_invalid"
	case "a study already has that address":
		return "form_slug_used"
	case "a study needs a title":
		return "form_title_required"
	case "run by is longer than 120 characters":
		return "form_run_by_long"
	case "a field is longer than 20000 characters":
		return "form_text_long"
	case "a study that has opened can be closed, not returned to draft":
		return "form_no_draft"
	case "a study opens only with a summary, what it collects, who sees it, how to leave, and consent text":
		return "form_content_required"
	case "a study needs at least one track":
		return "form_tracks_required"
	case "every track needs a name":
		return "form_track_name"
	case "no such study":
		return "no_study"
	}
	switch {
	case strings.Contains(problem, "only numbers have a unit"):
		return "form_numeric_only"
	case strings.Contains(problem, "choose what kind of value"):
		return "form_value_kind"
	case strings.Contains(problem, "a number either adds up"):
		return "form_value_meaning"
	case strings.Contains(problem, "has two values called"):
		return "form_duplicate_values"
	case strings.HasPrefix(problem, "two tracks are called"):
		return "form_duplicate_tracks"
	case strings.Contains(problem, "records no values"):
		return "form_track_values"
	case strings.Contains(problem, "has a value with no name"):
		return "form_value_name"
	case strings.Contains(problem, "Administer on your behalf"):
		return "form_open_permission"
	case strings.Contains(problem, "tracks are fixed once"):
		return "study_tracks_locked"
	default:
		return "form_save_error"
	}
}

func catalogueLabel(id string) string {
	if id == "com.voptrack.track.weight" {
		return "catalogue_weight"
	}
	return id
}

// resultRows formats values already computed for the page, including gaps
// and results too large to draw. The SVG is not the source of table data.
func resultRows(res Result) []panelRow {
	rows := make([]panelRow, 0, len(res.Points))
	for _, p := range res.Points {
		rows = append(rows, panelRow{Label: p.Label, Value: display(pointValue(res, p), 2), Count: p.Tally.Rows, Status: p.Status})
	}
	return rows
}

func designerProblem(problem, track, field string, forField bool) string {
	key := formErrorKey(problem)
	if !forField {
		if key == "form_track_name" && track == "" {
			return key
		}
		if strings.Contains(problem, strconv.Quote(track)) &&
			(key == "form_track_values" || key == "form_duplicate_tracks") {
			return key
		}
		return ""
	}
	if field == "" {
		return ""
	}
	if strings.Contains(problem, strconv.Quote(track)) && strings.Contains(problem, strconv.Quote(field)) {
		switch key {
		case "form_numeric_only", "form_value_kind", "form_value_meaning", "form_duplicate_values", "form_value_name":
			return key
		}
	}
	return ""
}

// Keep dense axis labels legible without dropping any plotted point.
func chartLabelVisible(index, count int) bool {
	if count <= 8 || index == 0 || index == count-1 {
		return true
	}
	step := (count + 7) / 8
	return index%step == 0 && count-1-index >= step
}

func chartLabelText(label string) string {
	runes := []rune(label)
	if len(runes) <= 12 {
		return label
	}
	return string(runes[:11]) + "…"
}

// Resolve the app's known catalogue display keys without changing metadata.
// User-authored names are returned unchanged.
func localizeName(name string, translate func(string) string) string {
	switch name {
	case "model.subject":
		return translate("model_subject")
	case "model.source":
		return translate("model_source")
	case "strings.track.weight", "strings.track.weight.field.weight":
		return translate("catalogue_weight")
	case "strings.track.weight.field.unit":
		return translate("value_unit")
	default:
		for _, identity := range homeTrackIdentities {
			if name == identity.Key {
				return identity.Name
			}
		}
		if strings.HasPrefix(name, "model.track.field.") {
			switch strings.TrimPrefix(name, "model.track.field.") {
			case "subject":
				return "Subject"
			case "source":
				return "Source"
			case "plan":
				return "Plan"
			case "planPeriod":
				return "Plan period"
			case "timestamp":
				return "When"
			}
		}
		if (strings.HasPrefix(name, "model.") || strings.HasPrefix(name, "strings.track.")) && strings.Contains(name, ".field.") {
			_, field, _ := strings.Cut(name, ".field.")
			var words []rune
			for i, c := range []rune(field) {
				if c == '_' {
					words = append(words, ' ')
					continue
				}
				if i > 0 && c >= 'A' && c <= 'Z' {
					words = append(words, ' ')
					c += 32
				}
				if i == 0 && c >= 'a' && c <= 'z' {
					c -= 32
				}
				words = append(words, c)
			}
			return string(words)
		}
		return name
	}
}

func localizePanelTitle(panel dashboardPanel, translate func(string) string) string {
	prefix, _, ok := strings.CutLast(panel.Title, " · ")
	if !ok {
		return localizeName(panel.Title, translate)
	}
	parts := strings.Split(prefix, " · ")
	for i := range parts {
		parts[i] = localizeName(parts[i], translate)
	}
	return strings.Join(parts, " · ") + " · " + translate("m_"+string(panel.Result.Spec.Measure))
}
