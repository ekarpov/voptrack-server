package main

import (
	"fmt"
	"net/http"
	"sort"
	"time"
)

// Sharing: a member chooses which of their tracks Home may read, and changes
// their mind. It needs their own store connected with "Administer on your
// behalf" approved on Tessera's consent screen, because making somebody a
// reader of a store is administering it. Without that the page says how,
// rather than failing.

type shareTrack struct {
	ID, Name       string
	Shared         bool
	Infrastructure bool
}

type sharingData struct {
	Enabled bool
	Store   Dataset
	Tracks  []shareTrack
	Saved   bool
	Error   string
	Stopped []stoppedShare
	// Usage is how full the member's store is. Past its limit a store
	// refuses writes until the owner raises it on the host, so a member is
	// told well before (Full, from 80%).
	Usage     StoreUsage
	HasUsage  bool
	UsageText string
	Full      bool
}

// ownAdministered finds the member's own store among their connections: one
// approved for administering, which the administration API confirms they
// administer.
func (a *app) ownAdministered(r *http.Request, sess *session) (*connection, bool) {
	a.sessions.mu.Lock()
	conns := append([]*connection(nil), sess.Connections...)
	a.sessions.mu.Unlock()
	for _, c := range conns {
		if !c.Administer {
			continue
		}
		token, err := c.token(r.Context(), a.tessera)
		if err != nil {
			continue
		}
		mine, err := a.tessera.administered(r.Context(), token)
		if err != nil {
			continue
		}
		for _, d := range mine {
			if d.ID == c.Dataset.ID {
				return c, true
			}
		}
	}
	return nil, false
}

func (a *app) sharing(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess == nil || !sess.signedIn() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	a.sharingView(w, r, sess, "", http.StatusOK)
}

func (a *app) sharingFailure(w http.ResponseWriter, r *http.Request, sess *session) {
	a.sharingView(w, r, sess, "sharing_failed", http.StatusBadGateway)
}

// Mutation failures retain the already validated form, even if subsequent
// discovery is unavailable. No second mutation or speculative notice is made.
func (a *app) sharingSnapshotFailure(w http.ResponseWriter, r *http.Request, sess *session, store Dataset, types []Meta) {
	d := sharingData{Enabled: true, Store: store, Error: "sharing_failed"}
	selected := map[string]bool{}
	for _, id := range r.PostForm["track"] {
		selected[id] = true
	}
	for _, t := range types {
		d.Tracks = append(d.Tracks, shareTrack{ID: t.ID, Name: trackTitle(TypeInfo{ID: t.ID, Name: t.Name}), Shared: selected[t.ID], Infrastructure: homeInfrastructure(t.ID)})
	}
	sort.Slice(d.Tracks, func(i, j int) bool { return d.Tracks[i].Name < d.Tracks[j].Name })
	a.render(w, r, sess, "sharing", http.StatusBadGateway, d)
}

func (a *app) sharingView(w http.ResponseWriter, r *http.Request, sess *session, problem string, status int) {
	d := sharingData{Enabled: a.cfg.HomeService != "", Saved: problem == "" && r.URL.Query().Get("saved") != "", Error: problem}
	if !d.Enabled {
		a.render(w, r, sess, "sharing", status, d)
		return
	}
	conn, ok := a.ownAdministered(r, sess)
	if !ok {
		a.render(w, r, sess, "sharing", status, d)
		return
	}
	token, err := conn.token(r.Context(), a.tessera)
	if err != nil {
		a.fail(w, r, sess, http.StatusBadGateway, "error", nil)
		return
	}
	types, err := a.tessera.types(r.Context(), token, conn.Dataset.ID)
	if err != nil {
		a.fail(w, r, sess, http.StatusBadGateway, "error", nil)
		return
	}
	// The household's group grant is the truth: a member's app shares by
	// writing it, without the Console. Where there is no issuer (a Home made
	// before group readers), the Home reader is all there is.
	reader := a.cfg.HomeService
	if a.cfg.IssuerID != "" {
		reader = householdGroup(a.cfg.IssuerID)
	}
	shared, err := a.tessera.readerTypes(r.Context(), token, conn.Dataset.ID, reader)
	if err != nil {
		a.fail(w, r, sess, http.StatusBadGateway, "error", nil)
		return
	}
	stops, err := a.tessera.stoppedShares(r.Context(), token, conn.Dataset.ID)
	if err != nil {
		a.fail(w, r, sess, http.StatusBadGateway, "sharing_failed", nil)
		return
	}
	names := map[string]string{}
	for _, t := range types {
		names[t.ID] = t.Name
	}
	for _, stop := range stops {
		stop.Name = names[stop.Type]
		stop.Time = stop.At
		if at, e := time.Parse(time.RFC3339, stop.At); e == nil {
			stop.Time = at.In(a.tz).Format("2006-01-02 15:04 MST")
		}
		d.Stopped = append(d.Stopped, stop)
	}
	on := map[string]bool{}
	for _, t := range shared {
		on[t] = true
	}
	if problem != "" {
		on = map[string]bool{}
		for _, id := range r.PostForm["track"] {
			on[id] = true
		}
	}
	d.Store = conn.Dataset
	if u, ok := a.tessera.usage(r.Context(), token, conn.Dataset.ID); ok {
		// No quota is the default (Tessera alpha.30): then only the size, and
		// never "nearly full".
		d.Usage, d.HasUsage = u, true
		d.UsageText = sizeText(u.Bytes)
		if u.MaxBytes > 0 {
			d.Full = u.Percent() >= 80
			d.UsageText = fmt.Sprintf("%s / %s (%d%%)", sizeText(u.Bytes), sizeText(u.MaxBytes), u.Percent())
		}
	}
	for _, t := range types {
		d.Tracks = append(d.Tracks, shareTrack{ID: t.ID, Name: trackTitle(TypeInfo{ID: t.ID, Name: t.Name}), Shared: on[t.ID], Infrastructure: homeInfrastructure(t.ID)})
	}
	sort.Slice(d.Tracks, func(i, j int) bool { return d.Tracks[i].Name < d.Tracks[j].Name })
	a.render(w, r, sess, "sharing", status, d)
}

func (a *app) saveSharing(w http.ResponseWriter, r *http.Request) {
	a.moderationMu.Lock()
	defer a.moderationMu.Unlock()
	sess := a.sessions.get(w, r, false)
	if sess == nil || !sess.checkCSRF(r) {
		a.fail(w, r, sess, http.StatusForbidden, "expired", nil)
		return
	}
	if a.cfg.HomeService == "" {
		a.fail(w, r, sess, http.StatusNotFound, "no_home", nil)
		return
	}
	conn, ok := a.ownAdministered(r, sess)
	if !ok || conn.Dataset.ID != r.PostFormValue("dataset") {
		a.fail(w, r, sess, http.StatusForbidden, "sharing_needs_admin", nil)
		return
	}
	token, err := conn.token(r.Context(), a.tessera)
	if err != nil {
		a.sharingFailure(w, r, sess)
		return
	}
	// Only tracks that exist in this store, and each at most once: a form is
	// what a caller supplies, and what was rendered is no part of what is
	// accepted.
	types, err := a.tessera.types(r.Context(), token, conn.Dataset.ID)
	if err != nil {
		a.sharingFailure(w, r, sess)
		return
	}
	exists := map[string]bool{}
	for _, t := range types {
		exists[t.ID] = true
	}
	_ = r.ParseForm()
	chosen := map[string]bool{}
	var list []string
	for _, id := range r.PostForm["track"] {
		if !exists[id] || chosen[id] {
			continue
		}
		chosen[id] = true
		list = append(list, id)
	}
	sort.Strings(list)
	// The household first -- what every member's app reads, through
	// Tessera's group readers (alpha.23): everyone the Home issuer made, later
	// members included. Then the Home service, which only the web dashboards
	// read. Both name the same tracks, so the app and the web never disagree.
	if a.cfg.IssuerID != "" {
		if err := a.tessera.setReader(r.Context(), token, conn.Dataset.ID, householdGroup(a.cfg.IssuerID), list); err != nil {
			a.sharingSnapshotFailure(w, r, sess, conn.Dataset, types)
			return
		}
	}
	err = a.tessera.setReader(r.Context(), token, conn.Dataset.ID, a.cfg.HomeService, list)
	a.forgetHomeToken(conn.Dataset.ID) // removing Home revokes its tokens; a type change does not, but a fresh token is cheap
	if err != nil {
		a.sharingSnapshotFailure(w, r, sess, conn.Dataset, types)
		return
	}
	if err := a.homeReg.set(conn.Dataset.ID, len(list) > 0); err != nil {
		a.sharingSnapshotFailure(w, r, sess, conn.Dataset, types)
		return
	}
	http.Redirect(w, r, "/sharing?saved=1", http.StatusSeeOther)
}

// householdGroup is the reader that is everyone the Home issuer made.
func householdGroup(issuer string) string { return "issuer:" + issuer }

// sizeText is a byte count for a person: KiB, MiB or GiB, one decimal.
func sizeText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
}
