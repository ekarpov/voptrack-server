package main

import (
	"net/http"
)

// The new form submits all date inputs. Canonicalize it once so unused dates
// cannot override a newly selected preset. Legacy bookmarked URLs keep their
// existing interpretation; subsequent links contain only the active date mode.
func selectionPage(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if !q.Has("selection") {
			next(w, r)
			return
		}
		mode := q.Get("selection")
		if len(q["selection"]) != 1 {
			http.Error(w, "Choose one period.", http.StatusBadRequest)
			return
		}
		switch mode {
		case "today", "week", "month", "year", "all":
			q.Del("from")
			q.Del("to")
			q.Del("month")
			q.Set("period", mode)
		case "calendar":
			q.Del("from")
			q.Del("to")
			q.Set("period", "month")
			if q.Get("month") == "" {
				http.Error(w, "Choose a month.", http.StatusBadRequest)
				return
			}
		case "custom":
			q.Del("month")
			q.Set("period", "custom")
			if q.Get("from") == "" {
				http.Error(w, "Choose a start date.", http.StatusBadRequest)
				return
			}
		default:
			http.Error(w, "Choose a valid period.", http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/home" || r.URL.Path == "/" {
			switch q.Get("view") {
			case "pets", "house", "car", "other", "activity", "health", "learning":
				q.Set("view", "subjects")
			}
		}
		q.Del("selection")
		q.Del("bucket")
		q.Del("offset")
		target := *r.URL
		target.RawQuery = q.Encode()
		http.Redirect(w, r, target.RequestURI(), http.StatusSeeOther)
	}
}
