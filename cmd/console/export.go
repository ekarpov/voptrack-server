package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// A study's export: every record of every participant, under their pseudonym,
// with the consent version they agreed to and their status. Read through the
// study-analysis service, exactly as the dashboard reads, so it holds nothing
// the researcher's dashboard could not show. Participants who withdrew or
// deleted their account are listed with no records: their stores are empty or
// gone.
//
// The CSV has one value per row, which suits any set of tracks and loads into
// any analysis tool; the JSON groups records by participant and track.

// exportLimit bounds one store's records in one track. An export that would
// be cut short fails instead: a silently partial dataset is worse than none.
const exportLimit = 1 << 20

var errExportSize = errors.New("export size limit reached")

// exportFields are the fields of a track worth exporting: when it happened,
// then its own values. The Track root's subject, source and plan fields refer
// into the participant's own device and mean nothing to a researcher.
func exportFields(t TypeDef) []TypeField {
	var out []TypeField
	for _, f := range trackRoot().Fields {
		if f.ID == "timestamp" || f.ID == "endTimestamp" {
			out = append(out, f)
		}
	}
	return append(out, t.Fields...)
}

type exportedTrack struct {
	Name    string           `json:"name"`
	Records []map[string]any `json:"records"`
}

type exportedParticipant struct {
	Pseudonym      string                    `json:"pseudonym"`
	ConsentVersion int                       `json:"consent_version"`
	Joined         time.Time                 `json:"joined"`
	Status         string                    `json:"status"`
	Tracks         map[string]*exportedTrack `json:"tracks"`
}

type studyExport struct {
	Study        string                `json:"study"`
	Title        string                `json:"title"`
	ExportedAt   time.Time             `json:"exported_at"`
	Consent      []ConsentVersion      `json:"consent_versions"`
	Participants []exportedParticipant `json:"participants"` // written one at a time, never held
}

// exportPlan is what an export knows before reading any records: the study,
// its tracks, and each participant's consent and status. Small; records are
// read one participant at a time.
type exportPlan struct {
	head      studyExport
	types     []TypeDef
	people    []exportedParticipant
	sources   map[string]Source
	histories map[string]agreementHistory
}

func (a *app) planExport(ctx context.Context, sess *session, st Study) (exportPlan, error) {
	if err := a.monitorHistoryAvailable(st.Slug); err != nil {
		return exportPlan{}, err
	}
	types, err := studyTracks(st.Tracks)
	if err != nil {
		return exportPlan{}, err
	}
	plan := exportPlan{head: studyExport{Study: st.Slug, Title: st.Title, ExportedAt: time.Now().UTC(), Consent: st.Consent},
		types: types, sources: map[string]Source{}, histories: a.studies.histories(st.Slug)}
	states := a.participantStates(ctx, sess, st.Slug)
	sources, err := a.discoverStudySources(ctx, sess, st.Slug)
	if err != nil {
		return exportPlan{}, fmt.Errorf("study discovery unavailable")
	}
	for _, state := range states {
		if state.Key == "state_unknown" {
			return exportPlan{}, fmt.Errorf("participant status unavailable")
		}
	}
	for _, src := range sources {
		plan.sources[src.Name] = src
	}
	// The version each person agreed to last, and when they first joined.
	latest := map[string]ConsentRecord{}
	joined := map[string]time.Time{}
	var order []string
	for _, c := range a.studies.consents(st.Slug) {
		if _, seen := joined[c.Account]; !seen {
			joined[c.Account] = c.At
			order = append(order, c.Account)
		}
		latest[c.Account] = c
	}
	sort.Strings(order)
	for _, account := range order {
		plan.people = append(plan.people, exportedParticipant{Pseudonym: account, ConsentVersion: latest[account].Version,
			Joined: joined[account], Status: states[account].Key})
	}
	return plan, nil
}

// readParticipant fills in one participant's records, and nothing else is
// held while it is written out.
func (a *app) readParticipant(ctx context.Context, plan exportPlan, p exportedParticipant) (exportedParticipant, error) {
	p.Tracks = map[string]*exportedTrack{}
	src, ok := plan.sources[p.Pseudonym]
	// A withdrawn participant's source exists only while the study's reader
	// still holds their store, frozen at withdrawal: what they sent before.
	if !ok || (p.Status != "state_active" && p.Status != "state_suspended" && p.Status != "state_withdrawn") {
		return p, nil
	}
	token, err := src.Token(ctx)
	if err != nil {
		return p, fmt.Errorf("reading a participant's store: %w", err)
	}
	for _, t := range plan.types {
		fields := exportFields(t)
		ids := make([]string, len(fields))
		for i, f := range fields {
			ids[i] = f.ID
		}
		rows, cut, err := a.tessera.rows(ctx, token, src.Dataset, t.ID, ids, nil, exportLimit)
		if err != nil {
			return p, fmt.Errorf("reading a participant's store: %w", err)
		}
		if cut {
			return p, fmt.Errorf("record limit reached: %w", errExportSize)
		}
		track := &exportedTrack{Name: t.Name}
		for _, row := range rows {
			rec := map[string]any{}
			for i, f := range fields {
				rec[f.ID] = row[i].Value
			}
			at, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(rec["timestamp"]))
			rec["consent_missing"] = plan.histories[p.Pseudonym].missing(Study{Consent: plan.head.Consent}, at)
			track.Records = append(track.Records, rec)
		}
		p.Tracks[t.ID] = track
	}
	return p, nil
}

// statusName is the export's word for a participant's state: stable English,
// not the page's translation, so an analysis script can rely on it.
func statusName(key string) string {
	switch key {
	case "state_active":
		return "taking part"
	case "state_suspended":
		return "suspended"
	case "state_withdrawn":
		return "withdrawn"
	case "state_gone":
		return "account deleted"
	}
	return "unknown"
}

// maxExportBytes bounds one export file. A study past it is exported in parts.
const maxExportBytes = 4 << 30

type cappedWriter struct {
	w io.Writer
	n int64
}

func (c *cappedWriter) Write(b []byte) (int, error) {
	if c.n += int64(len(b)); c.n > maxExportBytes {
		return 0, errExportSize
	}
	return c.w.Write(b)
}

func (a *app) exportStudy(format string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { a.writeExport(w, r, format) }
}

// writeExport streams the study into a private temporary file, one
// participant at a time, and serves the file only once it is complete: memory
// holds one participant's records at most, and a failure part-way never hands
// out a partial dataset. One export runs at a time.
func (a *app) writeExport(w http.ResponseWriter, r *http.Request, format string) {
	sess, ok := a.researcher(w, r)
	if !ok {
		return
	}
	st, found := a.studies.get(r.PathValue("slug"))
	if !found || st.IssuerID == "" {
		a.fail(w, r, sess, http.StatusNotFound, "no_study", nil)
		return
	}
	select {
	case a.exporting <- struct{}{}:
		defer func() { <-a.exporting }()
	default:
		a.render(w, r, sess, "research_exports", http.StatusServiceUnavailable, researchExportData{Study: st, ExportError: "Another export is running. Wait for it to finish, then try again."})
		return
	}
	dir := filepath.Join(a.cfg.StateDir, "exports")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		a.fail(w, r, sess, http.StatusInternalServerError, "error", err)
		return
	}
	f, err := os.CreateTemp(dir, "study-*")
	if err != nil {
		a.fail(w, r, sess, http.StatusInternalServerError, "error", err)
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := a.streamExport(r.Context(), sess, st, format, &cappedWriter{w: f}); err != nil {
		message := "The study could not be read completely. No file was served. Try again after checking collection availability."
		if errors.Is(err, errExportSize) {
			message = "This export reached a record or file-size limit. No file was served. Exporting selected parts is not available in this version."
		}
		a.render(w, r, sess, "research_exports", http.StatusBadGateway, researchExportData{Study: st, ExportError: message})
		return
	}
	name := "voptrack-study-" + st.Slug + "." + format
	if format == "json" {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeContent(w, r, name, time.Time{}, f)
}

func (a *app) streamExport(ctx context.Context, sess *session, st Study, format string, out io.Writer) error {
	plan, err := a.planExport(ctx, sess, st)
	if err != nil {
		return err
	}
	if format == "json" {
		head, _ := json.Marshal(plan.head) // participants follow, one at a time
		if _, err := out.Write(append(bytes.TrimSuffix(head, []byte("null}")), '[')); err != nil {
			return err
		}
		for i, p := range plan.people {
			p, err := a.readParticipant(ctx, plan, p)
			if err != nil {
				return err
			}
			p.Status = statusName(p.Status)
			raw, _ := json.Marshal(p)
			if i > 0 {
				raw = append([]byte{','}, raw...)
			}
			if _, err := out.Write(raw); err != nil {
				return err
			}
		}
		_, err := out.Write([]byte("]}\n"))
		return err
	}
	cw := csv.NewWriter(out)
	cw.Write([]string{"participant", "consent_version", "status", "track", "record", "field", "type", "value", "consent_missing"})
	for _, p := range plan.people {
		p, err := a.readParticipant(ctx, plan, p)
		if err != nil {
			return err
		}
		status := statusName(p.Status)
		if len(p.Tracks) == 0 {
			cw.Write([]string{cell(p.Pseudonym), fmt.Sprint(p.ConsentVersion), status, "", "", "", "", "", ""})
			continue
		}
		for _, t := range plan.types {
			track := p.Tracks[t.ID]
			if track == nil {
				continue
			}
			for n, rec := range track.Records {
				for _, f := range exportFields(t) {
					v, ok := rec[f.ID]
					if !ok || v == nil {
						continue
					}
					text := fmt.Sprint(v)
					if f.Type == "text" {
						text = cell(text) // a spreadsheet must not run it
					}
					cw.Write([]string{cell(p.Pseudonym), fmt.Sprint(p.ConsentVersion), status, t.ID,
						fmt.Sprint(n + 1), f.ID, f.Type, text, fmt.Sprint(rec["consent_missing"])})
				}
			}
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// ---- a person's own store ----------------------------------------------------

// A person takes their data with them: every track and record in a store they
// connected, read with their own token, as JSON. Media values come as the
// references the protocol gives, not their bytes.

type storeExport struct {
	Store      string                      `json:"store"`
	Name       string                      `json:"name"`
	ExportedAt time.Time                   `json:"exported_at"`
	Tracks     []Meta                      `json:"tracks"`
	Records    map[string][]map[string]any `json:"records"`
}

func (a *app) exportMine(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess == nil || !sess.signedIn() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	var conn *connection
	a.sessions.mu.Lock()
	for _, c := range sess.Connections {
		if c.Dataset.ID == r.PathValue("dataset") {
			conn = c
		}
	}
	a.sessions.mu.Unlock()
	if conn == nil {
		a.fail(w, r, sess, http.StatusNotFound, "no_tracks", nil)
		return
	}
	ctx := r.Context()
	token, err := conn.token(ctx, a.tessera)
	if err != nil {
		a.fail(w, r, sess, http.StatusBadGateway, "error", err)
		return
	}
	metas, err := a.tessera.types(ctx, token, conn.Dataset.ID)
	if err != nil {
		a.fail(w, r, sess, http.StatusBadGateway, "error", err)
		return
	}
	parents := map[string]bool{}
	for _, m := range metas {
		if m.ParentID != nil {
			parents[*m.ParentID] = true
		}
	}
	out := storeExport{Store: conn.Dataset.ID, Name: conn.Dataset.DisplayName, ExportedAt: time.Now().UTC(),
		Tracks: metas, Records: map[string][]map[string]any{}}
	for _, m := range metas {
		if parents[m.ID] {
			continue // records live under the tracks that inherit it
		}
		ids := make([]string, len(m.Fields))
		for i, f := range m.Fields {
			ids[i] = f.ID
		}
		rows, cut, err := a.tessera.rows(ctx, token, conn.Dataset.ID, m.ID, ids, nil, exportLimit)
		if err == nil && cut {
			err = fmt.Errorf("more than %d records in one track", exportLimit)
		}
		if err != nil {
			a.fail(w, r, sess, http.StatusBadGateway, "error", err)
			return
		}
		for _, row := range rows {
			rec := map[string]any{}
			for i, id := range ids {
				if row[i].Value != nil {
					rec[id] = row[i].Value
				}
			}
			out.Records[m.ID] = append(out.Records[m.ID], rec)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="voptrack-my-data.json"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}
