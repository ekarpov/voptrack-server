package main

import (
	"encoding/json"
	ac "github.com/ekarpov/voptrack-server/internal/acquisitionclient"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOnlineManualLostResponseNeverCreatesSecondID(t *testing.T) {
	a, s, _, _, _, _ := backupTestApp(t)
	a.cfg.StateDir = t.TempDir()
	a.cfg.BundleDir = filepath.Join(t.TempDir(), "bundles")
	var mu sync.Mutex
	accepted := ""
	puts := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/account" {
			writeJSON(w, 200, map[string]any{"server_administrator": true, "installation": map[string]bool{"administer": true}})
			return
		}
		mu.Lock()
		defer mu.Unlock()
		id := strings.TrimPrefix(r.URL.Path, "/admin/api/v1/backup-operations/")
		if r.Method == "PUT" {
			// Prove ID/pointer were persisted before accepting any network mutation.
			raw, e := os.ReadFile(filepath.Join(a.cfg.StateDir, "backup-intents", "current"))
			if e != nil || string(raw) != id {
				t.Error("operation submitted before durable intent")
			}
			if accepted != "" && id != accepted {
				t.Error("second mutation ID")
			}
			accepted = id
			puts++
			conn, _, e := w.(http.Hijacker).Hijack()
			if e == nil {
				conn.Close()
			}
			return
		}
		if accepted == "" {
			w.WriteHeader(404)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "not_found"}})
			return
		}
		if id != accepted {
			t.Error("lookup did not reconcile same ID")
		}
		json.NewEncoder(w).Encode(ac.Operation{ID: id, Kind: "backup", State: "running"})
	}))
	defer server.Close()
	a.tessera = &Tessera{internal: server.URL, http: server.Client()}
	if w := backupRequest(a, s, "POST", "/backups/now", url.Values{"csrf": {"bad"}}); w.Code != 403 {
		t.Fatal("CSRF")
	}
	for i := 0; i < 3; i++ {
		w := backupRequest(a, s, "POST", "/backups/now", url.Values{"csrf": {s.CSRF}})
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if puts != 1 {
		t.Fatal("reconciliation submitted second operation", puts)
	}
}

func TestUncollectedCompletedManualReconcilesExpiry(t *testing.T) {
	a, s, _, _, _, _ := backupTestApp(t)
	a.cfg.StateDir = t.TempDir()
	a.cfg.BundleDir = filepath.Join(t.TempDir(), "bundles")
	journal, e := ac.OpenJournal(filepath.Join(a.cfg.StateDir, "backup-intents"))
	must(t, e)
	intent, e := journal.Create("backup", time.Now())
	must(t, e)
	intent.State = "acquired"
	must(t, journal.Save(intent))
	journal.Close()
	must(t, os.WriteFile(filepath.Join(a.cfg.StateDir, "backup-intents", "current"), []byte(intent.ID), 0600))
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/account" {
			writeJSON(w, 200, map[string]any{"server_administrator": true, "installation": map[string]bool{"administer": true}})
			return
		}
		w.WriteHeader(410)
		w.Write([]byte(`{"error":{"code":"expired"}}`))
	}))
	defer srv.Close()
	a.tessera = &Tessera{internal: srv.URL, http: srv.Client()}
	state, e := a.manual(t.Context(), "ephemeral-test", false)
	if state != "expired" {
		t.Fatal("completed producer remained pending", state, e)
	}
	_ = s
}

func TestExplicitAdmissionRefusalIsNotAnUncertainAcceptedJob(t *testing.T) {
	a, _, _, _, _, _ := backupTestApp(t)
	a.cfg.StateDir = t.TempDir()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code, status := "not_found", 404
		if r.Method == "PUT" {
			code, status = "limit_exceeded", 429
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code}})
	}))
	defer srv.Close()
	a.tessera = &Tessera{internal: srv.URL, http: srv.Client()}
	state, e := a.manual(t.Context(), "ephemeral-test", true)
	if state != "failed" || e == nil {
		t.Fatal("clear admission refusal misreported", state, e)
	}
}
