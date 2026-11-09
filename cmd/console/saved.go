package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Saved dashboards are lists of panel questions, never answers. Each panel is
// a query string, re-validated and recomputed with the viewer's own access every
// time it is shown -- so a dashboard saved by one person and opened by another
// shows the second person only what they may read.
//
// They are keyed by who owns them:
//
//   acct-<hash>      a member's personal dashboards, keyed by their account
//   home             the household's, which everyone views and the owner edits
//   study-<slug>     one validated study's, managed only by its researcher
//
// They are Console documents (state.go), kept in the Console's own Tessera
// store, so a restored server has them back.

type Dashboard struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Panels  []string  `json:"panels"` // each a panel query string, without the leading "?"
	Updated time.Time `json:"updated"`
}

const (
	maxDashboards = 50
	maxPanels     = 24
	maxName       = 80
)

var errFull = errors.New("limit reached")

type savedStore struct {
	mu   sync.Mutex
	docs docStore
}

var validKey = regexp.MustCompile(`^(home|acct-[0-9a-f]{24}|study-[a-z0-9][a-z0-9-]{1,62})$`)

func (s *savedStore) path(key string) (string, error) {
	if !validKey.MatchString(key) {
		return "", errors.New("invalid dashboard owner")
	}
	return "dashboards/" + key + ".json", nil
}

func (s *savedStore) load(key string) ([]Dashboard, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(key)
}

func (s *savedStore) read(key string) ([]Dashboard, error) {
	path, err := s.path(key)
	if err != nil {
		return nil, err
	}
	raw, ok, err := s.docs.get(path)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	var out []Dashboard
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if strings.HasPrefix(key, "study-") {
		ids := map[string]bool{}
		for _, d := range out {
			if d.ID == "" || strings.ContainsAny(d.ID, "/?#") || ids[d.ID] || strings.TrimSpace(d.Name) == "" || len([]rune(d.Name)) > maxName || len(d.Panels) > maxPanels {
				return nil, errors.New("invalid dashboard document")
			}
			ids[d.ID] = true
		}
		if len(out) > maxDashboards {
			return nil, errors.New("invalid dashboard document")
		}
	}
	return out, nil
}

// update applies change under the lock and writes atomically, so two people
// saving at once cannot interleave into a corrupt file.
func (s *savedStore) update(key string, change func([]Dashboard) ([]Dashboard, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.read(key)
	if err != nil {
		return err
	}
	list, err = change(list)
	if err != nil {
		return err
	}
	sort.SliceStable(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	path, _ := s.path(key)
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return s.docs.put(path, raw)
}

// addPanel adds a panel to the named dashboard, creating it if needed.
func (s *savedStore) addPanel(key, name, panel string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > maxName {
		return "", errors.New("a dashboard needs a name of at most 80 characters")
	}
	id := ""
	err := s.update(key, func(list []Dashboard) ([]Dashboard, error) {
		for i := range list {
			if strings.EqualFold(list[i].Name, name) {
				for _, p := range list[i].Panels {
					if p == panel {
						id = list[i].ID
						return list, nil // already there; saving twice is not an error
					}
				}
				if len(list[i].Panels) >= maxPanels {
					return nil, errFull
				}
				list[i].Panels = append(list[i].Panels, panel)
				list[i].Updated = time.Now().UTC()
				id = list[i].ID
				return list, nil
			}
		}
		if len(list) >= maxDashboards {
			return nil, errFull
		}
		id = random(9)
		return append(list, Dashboard{ID: id, Name: name, Panels: []string{panel}, Updated: time.Now().UTC()}), nil
	})
	return id, err
}

func (s *savedStore) removePanel(key, id string, index int) error {
	return s.update(key, func(list []Dashboard) ([]Dashboard, error) {
		for i := range list {
			if list[i].ID == id {
				if index < 0 || index >= len(list[i].Panels) {
					return nil, errors.New("no such panel")
				}
				list[i].Panels = append(list[i].Panels[:index], list[i].Panels[index+1:]...)
				list[i].Updated = time.Now().UTC()
				if len(list[i].Panels) == 0 {
					return append(list[:i], list[i+1:]...), nil // an empty dashboard is gone
				}
				return list, nil
			}
		}
		return nil, errors.New("no such dashboard")
	})
}

func (s *savedStore) find(key, id string) (Dashboard, bool) {
	list, err := s.load(key)
	if err != nil {
		return Dashboard{}, false
	}
	for _, d := range list {
		if d.ID == id {
			return d, true
		}
	}
	return Dashboard{}, false
}

// cleanPanel keeps only the parameters a panel understands, so a saved panel
// cannot carry anything else along -- and so the same question saved twice is
// recognised as the same.
func cleanPanel(raw string) (string, string, error) {
	q, err := url.ParseQuery(raw)
	if err != nil {
		return "", "", err
	}
	scope := q.Get("scope")
	if scope == "" {
		scope = "mine"
	}
	if scope != "home" && scope != "mine" {
		if _, ok := studySlug(scope); !ok {
			return "", "", errors.New("invalid panel scope")
		}
	}
	for _, values := range q {
		if len(values) != 1 {
			return "", "", errors.New("ambiguous panel parameter")
		}
	}
	out := url.Values{"scope": {scope}}
	for _, k := range []string{"type", "field", "measure", "group", "period", "date", "from", "to", "tz"} {
		if v := q.Get(k); v != "" {
			if len(v) > 300 {
				return "", "", errors.New("panel parameter too long")
			}
			out.Set(k, v)
		}
	}
	if out.Get("type") == "" {
		return "", "", errors.New("a panel needs a track")
	}
	return out.Encode(), scope, nil
}

// A removal identifies the displayed board version, not just a movable index.
func dashboardToken(d Dashboard) string {
	raw, _ := json.Marshal(d)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func (s *savedStore) removeStudyPanel(key, id, token string, index int) error {
	return s.update(key, func(list []Dashboard) ([]Dashboard, error) {
		for i := range list {
			d := &list[i]
			if d.ID != id {
				continue
			}
			if token == "" || dashboardToken(*d) != token || index < 0 || index >= len(d.Panels) {
				return nil, errDashboardStale
			}
			d.Panels = append(d.Panels[:index], d.Panels[index+1:]...)
			d.Updated = time.Now().UTC()
			if len(d.Panels) == 0 {
				return append(list[:i], list[i+1:]...), nil
			}
			return list, nil
		}
		return nil, errDashboardStale
	})
}

var errDashboardStale = errors.New("dashboard changed")
