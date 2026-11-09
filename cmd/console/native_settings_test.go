package main

import (
	"encoding/json"
	bs "github.com/ekarpov/voptrack-server/internal/backupsettings"
	sc "github.com/ekarpov/voptrack-server/internal/schedulerclient"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func nativeFixture() sc.Observation {
	now := time.Now().UTC()
	scope := strings.Repeat("a", 32) + "." + strings.Repeat("b", 32)
	revision := scope + "." + strings.Repeat("c", 32)
	return sc.Observation{InstanceID: strings.Repeat("d", 32), ObservedAt: now, HistoryScope: "process_instance", Configured: true, Available: true, Enabled: true, Reason: "ready", Settings: &sc.Settings{EveryHours: 24, Keep: 7, Revision: revision}, SettingsScope: &scope, Next: &now, NextReason: "scheduled", Retention: &sc.Retention{Retained: 7, Target: 7, State: "converged", Reason: "at_target"}, Export: &sc.Export{Version: 1, Deployment: strings.Repeat("a", 32), EveryHours: 24, Keep: 7, Revision: revision, ExportedAt: now}}
}
func TestNativeLostResponseSameIDAndStaleConfirmation(t *testing.T) {
	for _, variant := range []string{"household", "research"} {
		t.Run(variant, func(t *testing.T) {
			a, s, _, _, _, _ := backupTestApp(t)
			a.cfg.Variant = variant
			a.cfg.StateDir = t.TempDir()
			var mu sync.Mutex
			o := nativeFixture()
			var accepted sc.Operation
			puts := 0
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path == "/oauth/account" {
					writeJSON(w, 200, map[string]any{"server_administrator": true, "installation": map[string]bool{"administer": true}})
					return
				}
				if r.URL.Path == "/admin/api/v1/backup-scheduler" {
					assertNativeObservationLocked(t, a.cfg.StateDir)
					o.ObservedAt = time.Now().UTC()
					o.Export.ExportedAt = o.ObservedAt
					json.NewEncoder(w).Encode(o)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/admin/api/v1/backup-settings-operations/") {
					id := strings.TrimPrefix(r.URL.Path, "/admin/api/v1/backup-settings-operations/")
					if r.Method == "PUT" {
						raw, e := os.ReadFile(filepath.Join(a.cfg.StateDir, "backup-live.json"))
						var durable bs.Live
						if e != nil || json.Unmarshal(raw, &durable) != nil || durable.Current() == nil || durable.Current().ID != id {
							t.Error("PUT preceded durable exact ID")
						}
						var p sc.Payload
						json.NewDecoder(r.Body).Decode(&p)
						if durable.Current().Payload != p {
							t.Error("PUT payload differs from durable payload")
						}
						if accepted.ID != "" && accepted.ID != id {
							t.Error("replaced uncertain ID")
						}
						now := time.Now().UTC()
						accepted = sc.Operation{ID: id, Payload: p, State: "pending", AcceptedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour)}
						puts++
						conn, _, e := w.(http.Hijacker).Hijack()
						if e == nil {
							conn.Close()
						}
						return
					}
					if accepted.ID != "" {
						json.NewEncoder(w).Encode(accepted)
						return
					}
					writeJSON(w, 404, map[string]any{"error": map[string]string{"code": "not_found"}})
					return
				}
				w.WriteHeader(404)
			}))
			defer srv.Close()
			a.tessera = &Tessera{internal: srv.URL, http: srv.Client()}
			for _, path := range []string{"preview", "confirm", "reconcile"} {
				if w := backupRequest(a, s, "POST", "/backups/live/"+path, url.Values{"csrf": {"bad"}}); w.Code != 403 {
					t.Fatal("CSRF", path, w.Code)
				}
			}
			v := a.nativeData(httptest.NewRequest("GET", "/backups", nil), "ephemeral")
			form := url.Values{"csrf": {s.CSRF}, "effective_revision": {v.Observation.Settings.Revision}, "journal": {v.Journal}, "every": {"12"}, "keep": {"2"}}
			if w := backupRequest(a, s, "POST", "/backups/live/preview", form); w.Code != 200 || s.NativeSettings == nil {
				t.Fatal("preview", w.Code, w.Body.String())
			}
			nonce := s.NativeSettings.Nonce
			if w := backupRequest(a, s, "POST", "/backups/live/confirm", url.Values{"csrf": {s.CSRF}, "confirmation": {nonce}}); w.Code != 303 {
				t.Fatal("confirm", w.Code)
			}
			l, e := bs.ReadLive(a.cfg.StateDir)
			if e != nil || l.Current().State != "uncertain" {
				t.Fatal("lost acceptance", l, e)
			}
			id := l.Current().ID
			// New app object/session observes the durable ID without resubmitting.
			restarted := &app{cfg: a.cfg, tessera: a.tessera}
			v = restarted.nativeData(httptest.NewRequest("GET", "/backups", nil), "ephemeral")
			if v.Command == nil || v.Command.ID != id || v.Command.State != "pending" || puts != 1 {
				t.Fatal("restart reconciliation", v, puts)
			}
			if w := backupRequest(a, s, "POST", "/backups/live/confirm", url.Values{"csrf": {s.CSRF}, "confirmation": {nonce}}); w.Code != 409 {
				t.Fatal("confirmation replay")
			}
			if w := backupRequest(a, s, "POST", "/backups/live/preview", form); w.Code != 409 {
				t.Fatal("unresolved command replaced")
			}
		})
	}
}

func TestNativeStatesRenderAllLanguages(t *testing.T) {
	a, s, _, _, _, _ := backupTestApp(t)
	for _, variant := range []string{"household", "research"} {
		a.cfg.Variant = variant
		for _, lang := range languages {
			for _, state := range []string{"converged", "pending", "blocked", "degraded", "unavailable", "not_configured"} {
				o := nativeFixture()
				switch state {
				case "pending":
					o.Retention.State = "pending"
					o.Retention.Reason = "pruning"
				case "blocked":
					o.Retention.State = "blocked"
					o.Retention.Reason = "leases"
				case "degraded":
					o.Retention.State = "degraded"
					o.Retention.Reason = "cleanup_failed"
				case "unavailable":
					o.Available = false
					o.Settings = nil
					o.Export = nil
					o.SettingsScope = nil
					o.Retention = nil
					o.Next = nil
					o.Reason = "settings_unavailable"
					o.NextReason = "settings_unavailable"
				case "not_configured":
					o = sc.Observation{InstanceID: o.InstanceID, ObservedAt: o.ObservedAt, HistoryScope: "process_instance", Reason: "not_configured", NextReason: "not_configured"}
				}
				d := map[string]any{"Observed": time.Now().UTC(), "Native": nativeView{Observation: &o, Confirm: true, Every: 12, Keep: 2}, "Settings": backupSettingsView{}}
				w := httptest.NewRecorder()
				r := httptest.NewRequest("GET", "/backups?lang="+lang, nil)
				a.render(w, r, s, "backups", 200, d)
				if w.Code != 200 || !strings.Contains(w.Body.String(), messages["en"]["native_title"]) || strings.Contains(w.Body.String(), ">native_review<") {
					t.Fatal("native translation/render", variant, lang, state, w.Code)
				}
				if preview := os.Getenv("VOPTRACK_NATIVE_PREVIEW"); preview != "" {
					must(t, os.MkdirAll(preview, 0700))
					css, e := assets.ReadFile("static/console.css")
					must(t, e)
					for _, theme := range []string{"light", "dark"} {
						media := "@media (width: 0px)"
						if theme == "dark" {
							media = "@media all"
						}
						body := strings.Replace(w.Body.String(), `<link rel="stylesheet" href="/static/console.css">`, "<style>"+strings.Replace(string(css), "@media (prefers-color-scheme: dark)", media, 1)+"</style>", 1)
						must(t, os.WriteFile(filepath.Join(preview, variant+"-"+lang+"-"+state+"-"+theme+".html"), []byte(body), 0600))
					}
				}
			}
		}
	}
}

// Independent file descriptors make this a lock-ownership assertion, not a
// timing assertion. It covers the actual HTTP request, including confirmation.
func assertNativeObservationLocked(t *testing.T, dir string) bool {
	t.Helper()
	held := true
	for _, name := range []string{".recovery-material.lock", ".backup-settings.lock"} {
		f, e := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_RDWR, 0600)
		if e != nil {
			t.Error(e)
			return false
		}
		e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if e == nil {
			syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			t.Errorf("scheduler observation started outside %s", name)
			held = false
		} else if e != syscall.EWOULDBLOCK {
			t.Error(e)
			held = false
		}
		f.Close()
	}
	return held
}

func TestNativeObservationCannotOverwriteAcknowledgedSettings(t *testing.T) {
	a, _, _, _, _, _ := backupTestApp(t)
	a.cfg.StateDir = t.TempDir()
	old := nativeFixture()
	next := nativeFixture()
	next.Settings.Revision = sc.Scope(old.Settings.Revision) + "." + strings.Repeat("e", 32)
	next.Export.Revision = next.Settings.Revision
	next.Settings.EveryHours, next.Export.EveryHours = 12, 12
	entered, release := make(chan bool, 1), make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		held := assertNativeObservationLocked(t, a.cfg.StateDir)
		entered <- held
		<-release
		json.NewEncoder(w).Encode(old)
	}))
	defer srv.Close()
	a.tessera = &Tessera{internal: srv.URL, http: srv.Client()}
	observed := make(chan nativeView, 1)
	go func() { observed <- a.nativeData(httptest.NewRequest("GET", "/backups", nil), "ephemeral") }()
	locked := <-entered
	attempted, acknowledged := make(chan struct{}), make(chan error, 1)
	go func() {
		close(attempted)
		acknowledged <- a.withNative(func(l *bs.Live, save func() error) error {
			now := time.Now().UTC()
			p := sc.Payload{ExpectedRevision: old.Settings.Revision, EveryHours: 12, Keep: 7}
			if e := l.Begin(l.Fingerprint(), p, now); e != nil {
				return e
			}
			c := l.Current()
			c.State = "effective"
			c.Outcome = &sc.Operation{ID: c.ID, Payload: p, State: "effective", AcceptedAt: now, ExpiresAt: now.Add(time.Hour), EffectiveAt: &now, Revision: next.Settings.Revision}
			next.ObservedAt, next.Export.ExportedAt = now, now
			l.Observation = &next
			return save()
		})
	}()
	<-attempted
	// On the defective implementation, force R2 acknowledgement before the
	// paused R response returns. On the fixed path R owns the lock first.
	if !locked {
		must(t, <-acknowledged)
	}
	close(release)
	<-observed
	if locked {
		must(t, <-acknowledged)
	}
	l, e := bs.ReadLive(a.cfg.StateDir)
	must(t, e)
	if l.Observation == nil || l.Observation.Export.Revision != next.Export.Revision {
		t.Fatal("older observation overwrote acknowledged R2")
	}
	if _, e = l.CurrentExport(time.Now()); e != nil {
		t.Fatal("acknowledged fresh R2 unavailable", e)
	}
}

func TestNativeAcknowledgementAfterSnapshotCannotQualifyKit(t *testing.T) {
	for _, variant := range []string{"household", "research"} {
		t.Run(variant, func(t *testing.T) {
			a, _, _, _, _, _ := backupTestApp(t)
			a.cfg.StateDir, a.cfg.Variant = t.TempDir(), variant
			old := nativeFixture()
			applied := old.ObservedAt.Add(time.Millisecond)
			revision := sc.Scope(old.Settings.Revision) + "." + strings.Repeat("e", 32)
			var operation sc.Operation
			must(t, a.withNative(func(l *bs.Live, save func() error) error {
				p := sc.Payload{ExpectedRevision: old.Settings.Revision, EveryHours: 12, Keep: 7}
				if e := l.Begin(l.Fingerprint(), p, old.ObservedAt); e != nil {
					return e
				}
				l.Current().State = "pending"
				operation = sc.Operation{ID: l.Current().ID, Payload: p, State: "effective", AcceptedAt: old.ObservedAt, ExpiresAt: applied.Add(time.Hour), EffectiveAt: &applied, Revision: revision}
				return save()
			}))
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/admin/api/v1/backup-settings-operations/") {
					json.NewEncoder(w).Encode(operation)
					return
				}
				json.NewEncoder(w).Encode(old)
			}))
			defer srv.Close()
			a.tessera = &Tessera{internal: srv.URL, http: srv.Client()}
			r := httptest.NewRequest("GET", "/backups", nil)
			if a.kitNativeCurrent(r, "ephemeral") {
				t.Fatal("terminal R2 acknowledgement qualified earlier R export")
			}
			l, e := bs.ReadLive(a.cfg.StateDir)
			must(t, e)
			if l.Current().State != "effective" {
				t.Fatal("test did not acknowledge R2")
			}
		})
	}
}
