package main

import (
	"encoding/json"
	bc "github.com/ekarpov/voptrack-server/internal/backupcollector"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOffMachineStatusRequiresCurrentOwnerApproval(t *testing.T) {
	a, s, _, denied, unapproved, _ := backupTestApp(t)
	dir := t.TempDir()
	a.cfg.RuntimeFile = filepath.Join(dir, "server.json")
	status := bc.ReplicationStatus{Version: 1, Label: "Protected synthetic destination", State: "verified", Observed: time.Now(), LastVerified: time.Now(), LastBackup: time.Now(), Budget: bc.DefaultOutboxBudget}
	raw, _ := json.Marshal(status)
	must(t, os.WriteFile(filepath.Join(dir, "off-machine.json"), raw, 0600))
	w := backupRequest(a, s, "GET", "/backups", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), status.Label) {
		t.Fatal("owner status missing")
	}
	for _, which := range []string{"anonymous", "not-admin", "not-approved"} {
		if which == "not-admin" {
			denied.Store(true)
		}
		if which == "not-approved" {
			unapproved.Store(true)
		}
		who := s
		if which == "anonymous" {
			who = nil
		}
		w = backupRequest(a, who, "GET", "/backups", nil)
		if strings.Contains(w.Body.String(), status.Label) || w.Code == 200 {
			t.Fatal("unauthorized destination disclosure", which)
		}
		denied.Store(false)
		unapproved.Store(false)
	}
}
func TestOffMachineLanguagesAndReviewCorpus(t *testing.T) {
	a, s, _, _, _, _ := backupTestApp(t)
	dir := os.Getenv("VOPTRACK_OFF_MACHINE_UI_DIR")
	now := time.Now().UTC()
	for _, variant := range []string{"household", "research"} {
		a.cfg.Variant = variant
		for _, lang := range languages {
			for _, state := range []string{"disabled", "unprotected", "unknown", "copying", "verified", "unavailable", "degraded", "stale"} {
				status := bc.ReplicationStatus{Version: 1, Label: strings.Repeat("x", 80), State: state, Observed: now, LastVerified: now, LastBackup: now, LastEvidence: now, Pending: 2, PendingBytes: 5000000000, Budget: bc.DefaultOutboxBudget, OldestPending: now.Add(-time.Hour)}
				if state == "verified" {
					status.Pending = 0
					status.PendingBytes = 0
					status.OldestPending = time.Time{}
					status.IndependentDestination = true
					status.SettingsAvailable = true
				}
				if state == "disabled" {
					status = bc.ReplicationStatus{State: "disabled"}
				}
				if state == "unprotected" {
					status.LastBackup = time.Time{}
					status.LastEvidence = time.Time{}
					status.LastVerified = time.Time{}
				}
				d := map[string]any{"Observed": now, "Storage": "backup_none", "Settings": backupSettingsView{}, "OffMachine": status}
				w := httptest.NewRecorder()
				r := httptest.NewRequest("GET", "/backups?lang="+lang, nil)
				a.render(w, r, s, "backups", 200, d)
				body := w.Body.String()
				for _, key := range []string{"title", state, "scope", "material", "refresh"} {
					if messages["en"]["off_machine_"+key] == "" || !strings.Contains(body, messages["en"]["off_machine_"+key]) {
						t.Fatal("missing localized status", lang, key)
					}
				}
				if dir != "" {
					must(t, os.MkdirAll(filepath.Join(dir, "html"), 0700))
					css, e := assets.ReadFile("static/console.css")
					must(t, e)
					styled := strings.Replace(body, `<link rel="stylesheet" href="/static/console.css">`, "<style>"+string(css)+"</style>", 1)
					must(t, os.WriteFile(filepath.Join(dir, "html", variant+"-"+lang+"-"+state+".html"), []byte(styled), 0600))
				}
			}
		}
	}
}
