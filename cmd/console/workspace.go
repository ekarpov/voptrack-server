package main

import (
	"net/http"
	"net/url"
	"sort"
)

type connectedTrack struct {
	Name, Family, URL string
	Stores            int
}
type workspaceData struct {
	Title, Subtitle, Scope string
	Tracks                 []connectedTrack
	Connected              []Dataset
	Skipped                []string
}

// My data is a directory of connected data, not automatically generated saved
// dashboards. Opening a Track computes supported values under the same sign-in.
func (a *app) personalWorkspace(w http.ResponseWriter, r *http.Request, sess *session, sources []Source, cat Catalog, skipped []string) {
	d := workspaceData{Title: "My data", Subtitle: "Tracks connected to your sign-in. This is separate from the household’s shared view.", Scope: "mine", Skipped: skipped}
	a.sessions.mu.Lock()
	for _, c := range sess.Connections {
		d.Connected = append(d.Connected, c.Dataset)
	}
	a.sessions.mu.Unlock()
	for _, typ := range cat.Types {
		if homeInfrastructure(typ.ID) {
			continue
		}
		d.Tracks = append(d.Tracks, connectedTrack{Name: trackTitle(typ), Family: trackFamily(typ.ID), Stores: len(typ.Stores), URL: "/tracks?" + url.Values{"scope": {"mine"}, "type": {typ.ID}, "period": {"month"}}.Encode()})
	}
	sort.Slice(d.Tracks, func(i, j int) bool { return d.Tracks[i].Name < d.Tracks[j].Name })
	a.render(w, r, sess, "workspace", 200, d)
}
