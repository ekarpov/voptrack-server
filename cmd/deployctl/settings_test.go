package main

import (
	"encoding/json"
	"encoding/pem"
	bs "github.com/ekarpov/voptrack-server/internal/backupsettings"
	sc "github.com/ekarpov/voptrack-server/internal/schedulerclient"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHostObservationUsesCurrentAuthorityAndNeverPersistsToken(t *testing.T) {
	now := time.Now().UTC()
	scope := strings.Repeat("a", 32) + "." + strings.Repeat("b", 32)
	revision := scope + "." + strings.Repeat("c", 32)
	o := sc.Observation{InstanceID: strings.Repeat("d", 32), ObservedAt: now, HistoryScope: "process_instance", Configured: true, Available: true, Enabled: true, Reason: "ready", Settings: &sc.Settings{EveryHours: 12, Keep: 2, Revision: revision}, SettingsScope: &scope, Next: &now, NextReason: "scheduled", Retention: &sc.Retention{Retained: 2, Target: 2, State: "converged", Reason: "at_target"}, Export: &sc.Export{Version: 1, Deployment: strings.Repeat("a", 32), EveryHours: 12, Keep: 2, Revision: revision, ExportedAt: now}}
	state := t.TempDir()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/api/v1/backup-scheduler" || r.Header.Get("Authorization") != "Bearer ephemeral-administrator" {
			w.WriteHeader(403)
			w.Write([]byte(`{"error":{"code":"permission_denied"}}`))
			return
		}
		for _, name := range []string{".recovery-material.lock", ".backup-settings.lock"} {
			f, e := os.OpenFile(filepath.Join(state, name), os.O_CREATE|os.O_RDWR, 0600)
			if e != nil {
				t.Error(e)
				w.WriteHeader(500)
				return
			}
			e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			if e == nil {
				syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				t.Errorf("host observation started outside %s", name)
			} else if e != syscall.EWOULDBLOCK {
				t.Error(e)
			}
			f.Close()
		}
		json.NewEncoder(w).Encode(o)
	}))
	defer server.Close()
	inputs := t.TempDir()
	ca := filepath.Join(inputs, "ca")
	token := filepath.Join(inputs, "token")
	generated := filepath.Join(inputs, "generated")
	for path, b := range map[string][]byte{ca: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), token: []byte("ephemeral-administrator\n")} {
		if e := os.WriteFile(path, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := bs.WriteJSON(generated, bs.Generated{Values: bs.Values{Destination: "local", EveryHours: 24, Keep: 7}, RequestRevision: "none", At: now}, 0600); e != nil {
		t.Fatal(e)
	}
	if e := observeSettings(t.Context(), server.URL, ca, token, state, generated); e != nil {
		t.Fatal(e)
	}
	live, e := bs.ReadLive(state)
	if e != nil || live.Observation.Settings.Revision != revision {
		t.Fatal(live, e)
	}
	entries, e := os.ReadDir(state)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		raw, e := os.ReadFile(filepath.Join(state, entry.Name()))
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(raw), "ephemeral-administrator") {
			t.Fatal("credential persisted")
		}
	}
	os.WriteFile(token, []byte("revoked"), 0600)
	if observeSettings(t.Context(), server.URL, ca, token, state, generated) == nil {
		t.Fatal("revoked authority used")
	}
	after, e := bs.ReadLive(state)
	if e != nil || after.Observation.Settings.Revision != revision {
		t.Fatal("failed observation destroyed prior state", e)
	}
}
