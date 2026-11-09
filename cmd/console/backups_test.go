package main

import (
	"bytes"
	"github.com/ekarpov/voptrack-server/internal/backupsettings"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const testArchive = "tessera-backup-20261015T120000.000000000Z.enc"

func archiveBytes() []byte { return []byte(archiveHeader + strings.Repeat("x", 100)) }
func backupTestApp(t *testing.T) (*app, *session, *atomic.Int64, *atomic.Bool, *atomic.Bool, *atomic.Bool) {
	t.Helper()
	a, s, old := policyHTTPApp(t)
	old.Close()
	a.cfg.BackupDir = t.TempDir()
	var count atomic.Int64
	var denied, unapproved, unavailable atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/account":
			writeJSON(w, 200, map[string]any{"server_administrator": !denied.Load(), "installation": map[string]bool{"administer": !unapproved.Load()}})
		case "/.well-known/tessera":
			writeJSON(w, 200, map[string]any{"server": map[string]string{"version": "alpha.33-verified"}})
		case "/admin/api/v1/overview":
			if unavailable.Load() {
				w.WriteHeader(503)
				return
			}
			writeJSON(w, 200, map[string]any{"server_administrator": true, "server": map[string]any{"last_backup_at": "2020-01-02T03:04:05Z"}, "datasets": []any{map[string]any{"id": "only-administered", "usage": map[string]int{"bytes": 42, "records": 3}}}})
		case "/admin/api/v1/backups":
			count.Add(1)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	a.tessera = &Tessera{internal: srv.URL, http: srv.Client()}
	for _, page := range []string{"backups"} {
		tpl, err := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix}).ParseFS(assets, "templates/layout.html", "templates/"+page+".html")
		must(t, err)
		a.pages[page] = tpl
	}
	return a, s, &count, &denied, &unapproved, &unavailable
}
func backupRequest(a *app, s *session, method, path string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if s != nil {
		r.AddCookie(&http.Cookie{Name: a.sessions.cookieName(), Value: s.ID})
	}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(deadlineRecorder{w}, r)
	return w
}
func TestBackupOperatorBoundariesAndUnknownMutation(t *testing.T) {
	for _, variant := range []string{"household", "research"} {
		t.Run(variant, func(t *testing.T) {
			a, s, count, denied, unapproved, _ := backupTestApp(t)
			a.cfg.Variant = variant
			for _, methodPath := range [][2]string{{"GET", "/backups"}, {"GET", "/backups/archive/" + testArchive}, {"POST", "/backups/now"}, {"POST", "/backups/settings/preview"}, {"POST", "/backups/settings/confirm"}, {"POST", "/backups/live/preview"}, {"POST", "/backups/live/confirm"}, {"POST", "/backups/live/reconcile"}} {
				method, path := methodPath[0], methodPath[1]
				if w := backupRequest(a, nil, method, path, nil); w.Code != 303 {
					t.Fatal("signed out", w.Code)
				}
				saved := s.Owner
				s.Owner = nil
				s.Connections = []*connection{{Dataset: Dataset{ID: "member-private"}}}
				if w := backupRequest(a, s, method, path, url.Values{"csrf": {s.CSRF}}); w.Code != 403 {
					t.Fatal("member/participant", w.Code)
				}
				s.Owner = saved
				s.Connections = nil
				for _, flag := range []*atomic.Bool{denied, unapproved} {
					flag.Store(true)
					if w := backupRequest(a, s, method, path, url.Values{"csrf": {s.CSRF}}); w.Code != 403 {
						t.Fatal("revoked/unapproved", w.Code)
					}
					flag.Store(false)
				}
			}
			if w := backupRequest(a, s, "POST", "/backups/now", url.Values{"csrf": {"bad"}}); w.Code != 403 {
				t.Fatal("CSRF")
			}
			w := backupRequest(a, s, "POST", "/backups/now", url.Values{"csrf": {s.CSRF}})
			if w.Code != 502 || count.Load() != 1 || !strings.Contains(w.Body.String(), "No automatic retry") {
				t.Fatal("uncertain result retried or misreported", count.Load(), w.Body.String())
			}
		})
	}
}
func TestBackupStatusSourcesAndStorageStates(t *testing.T) {
	a, s, _, _, _, unavailable := backupTestApp(t)
	if _, state := a.archives(); state != "backup_none" {
		t.Fatal(state)
	}
	empty := backupRequest(a, s, "GET", "/backups", nil)
	if !strings.Contains(empty.Body.String(), "2020-01-02 03:04:05 UTC") || !strings.Contains(empty.Body.String(), "No recognized encrypted archives") {
		t.Fatal("empty local storage erased server success", empty.Body.String())
	}

	must(t, os.WriteFile(filepath.Join(a.cfg.BackupDir, testArchive), archiveBytes(), 0600))
	w := backupRequest(a, s, "GET", "/backups?taken=forged", nil)
	for _, want := range []string{"2020-01-02 03:04:05 UTC", "alpha.33-verified", "only-administered", "42 / 3", "archive alone is incomplete"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatal("missing", want, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), "forged") {
		t.Fatal("forged success")
	}
	unavailable.Store(true)
	w = backupRequest(a, s, "GET", "/backups", nil)
	if strings.Contains(w.Body.String(), "data-latest") || !strings.Contains(w.Body.String(), "Unavailable") {
		t.Fatal("inferred success")
	}
	must(t, os.Chmod(a.cfg.BackupDir, 0000))
	if _, state := a.archives(); state != "backup_storage_unavailable" {
		t.Fatal("unreadable conflated", state)
	}
	must(t, os.Chmod(a.cfg.BackupDir, 0700))
	a.cfg.BackupDir = filepath.Join(a.cfg.BackupDir, "missing")
	if _, state := a.archives(); state != "backup_storage_unavailable" {
		t.Fatal("missing conflated", state)
	}
	a.cfg.BackupDir = ""
	if _, state := a.archives(); state != "backups_off" {
		t.Fatal(state)
	}
}
func TestArchiveConfinementReplacementAndRotation(t *testing.T) {
	a, s, _, _, _, _ := backupTestApp(t)
	path := filepath.Join(a.cfg.BackupDir, testArchive)
	must(t, os.WriteFile(path, archiveBytes(), 0600))
	list, state := a.archives()
	if state != "backup_storage_readable" || len(list) != 1 {
		t.Fatal(state)
	}
	link := "/backups/archive/" + testArchive + "?version=" + list[0].Version
	w := backupRequest(a, s, "GET", link, nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), archiveBytes()) || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("download", w.Code)
	}
	for _, name := range []string{"secret", "dataset.db.retired", "tessera-backup-forged.enc", "../secret"} {
		root, err := os.OpenRoot(a.cfg.BackupDir)
		must(t, err)
		f, _, err := openArchive(root, name)
		root.Close()
		if err == nil {
			f.Close()
			t.Fatal("unauthorized name", name)
		}
	}
	root, err := os.OpenRoot(a.cfg.BackupDir)
	must(t, err)
	defer root.Close()
	opened, _, err := openArchive(root, testArchive)
	must(t, err)
	defer opened.Close()
	must(t, os.Remove(path))
	// Pinned descriptor survives writer rotation and cannot switch to replacement.
	buf := make([]byte, len(archiveBytes()))
	_, err = opened.Read(buf)
	must(t, err)
	if !bytes.Equal(buf, archiveBytes()) {
		t.Fatal("rotation changed descriptor")
	}
	must(t, os.WriteFile(path, archiveBytes(), 0600))
	must(t, os.Chtimes(path, time.Now(), time.Now().Add(time.Minute)))
	if w := backupRequest(a, s, "GET", link, nil); w.Code != 404 {
		t.Fatal("replacement accepted", w.Code)
	}
	must(t, os.Remove(path))
	outside := filepath.Join(t.TempDir(), "private")
	must(t, os.WriteFile(outside, archiveBytes(), 0600))
	must(t, os.Symlink(outside, path))
	if w := backupRequest(a, s, "GET", link, nil); w.Code != 404 {
		t.Fatal("symlink escape", w.Code)
	}
	must(t, os.Remove(path))
	must(t, os.Mkdir(path, 0700))
	if w := backupRequest(a, s, "GET", link, nil); w.Code != 404 {
		t.Fatal("directory", w.Code)
	}
	must(t, os.Remove(path))
	must(t, os.WriteFile(path, []byte("not an archive"), 0600))
	if w := backupRequest(a, s, "GET", link, nil); w.Code != 404 {
		t.Fatal("invalid header", w.Code)
	}
}

func TestBackupLanguagesAndPreview(t *testing.T) {
	a, s, _, _, _, unavailable := backupTestApp(t)
	must(t, os.WriteFile(filepath.Join(a.cfg.BackupDir, testArchive), archiveBytes(), 0600))
	preview := os.Getenv("VOPTRACK_BACKUP_PREVIEW")
	setupBackupSettings(t, a)
	for _, variant := range []string{"household", "research"} {
		a.cfg.Variant = variant
		for _, lang := range languages {
			for _, state := range []string{"success", "unavailable", "error", "reported", "confirmation", "invalid", "pending"} {
				unavailable.Store(state == "unavailable")
				w := httptest.NewRecorder()
				if state == "error" || state == "reported" {
					key := "backup_unknown"
					if state == "reported" {
						key = "backup_reported"
					}
					a.render(w, httptest.NewRequest("GET", "/backups?lang="+lang, nil), s, "message", 200, map[string]string{"Key": key, "Title": "server_status", "Retry": "/backups", "RetryLabel": "server_status"})
				} else if state == "confirmation" || state == "invalid" {
					s.Lang = lang
					form := settingsForm(a, s)
					if state == "invalid" {
						form.Set("every", "bad")
					}
					w = backupRequest(a, s, "POST", "/backups/settings/preview?lang="+lang, form)
				} else {
					if state == "pending" {
						cur, err := backupsettings.Read(a.cfg.StateDir)
						must(t, err)
						must(t, backupsettings.Save(a.cfg.StateDir, cur.Revision, backupsettings.Values{Destination: "reserve", EveryHours: 12, Keep: 1}))
					}
					w = backupRequest(a, s, "GET", "/backups?lang="+lang, nil)
					if state == "pending" {
						must(t, os.Remove(filepath.Join(a.cfg.StateDir, "backup-settings.json")))
					}
				}
				if (w.Code != 200 && !(state == "invalid" && w.Code == 422)) || !strings.Contains(w.Body.String(), `lang="en"`) {
					t.Fatal(variant, lang, state)
				}
				if (state == "confirmation" || state == "invalid") && strings.Contains(w.Body.String(), `?lang=`) {
					t.Fatal("language link would GET a POST-only settings route")
				}
				if preview != "" {
					must(t, os.MkdirAll(preview, 0700))
					for _, theme := range []string{"light", "dark"} {
						css, err := assets.ReadFile("static/console.css")
						must(t, err)
						cssText := string(css)
						if theme == "light" {
							cssText = strings.Replace(cssText, "@media (prefers-color-scheme: dark)", "@media (width: 0px)", 1)
						} else {
							cssText = strings.Replace(cssText, "@media (prefers-color-scheme: dark)", "@media all", 1)
						}
						body := strings.Replace(w.Body.String(), `<link rel="stylesheet" href="/static/console.css">`, "<style>"+cssText+"</style>", 1)
						must(t, os.WriteFile(filepath.Join(preview, variant+"-"+lang+"-"+state+"-"+theme+".html"), []byte(body), 0600))
					}
				}
			}
		}
	}
}

type interruptedArchiveWriter struct{ *httptest.ResponseRecorder }

func (w interruptedArchiveWriter) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }
func TestArchiveInterruptedDelivery(t *testing.T) {
	a, s, _, _, _, _ := backupTestApp(t)
	must(t, os.WriteFile(filepath.Join(a.cfg.BackupDir, testArchive), archiveBytes(), 0600))
	list, _ := a.archives()
	r := httptest.NewRequest("GET", "/backups/archive/"+testArchive+"?version="+list[0].Version, nil)
	r.AddCookie(&http.Cookie{Name: a.sessions.cookieName(), Value: s.ID})
	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Fatalf("partial delivery was not aborted: %v", got)
		}
	}()
	a.routes().ServeHTTP(interruptedArchiveWriter{httptest.NewRecorder()}, r)
}
func TestArchiveConcurrentRotation(t *testing.T) {
	a, s, _, _, _, _ := backupTestApp(t)
	path := filepath.Join(a.cfg.BackupDir, testArchive)
	must(t, os.WriteFile(path, archiveBytes(), 0600))
	list, _ := a.archives()
	link := "/backups/archive/" + testArchive + "?version=" + list[0].Version
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = os.Rename(path, path+".old")
			_ = os.WriteFile(path, archiveBytes(), 0600)
		}
	}()
	for i := 0; i < 50; i++ {
		w := backupRequest(a, s, "GET", link, nil)
		if w.Code != 404 && (w.Code != 200 || !bytes.Equal(w.Body.Bytes(), archiveBytes())) {
			t.Fatal("rotation switched delivery", w.Code)
		}
	}
	wg.Wait()
}

func TestBackupExplicitFailureVersusUnknown(t *testing.T) {
	for _, code := range []string{"dataset_unavailable", "not_supported", "permission_denied", ""} {
		t.Run(code, func(t *testing.T) {
			a, s, _, _, _, _ := backupTestApp(t)
			var calls atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/oauth/account" {
					writeJSON(w, 200, map[string]any{"server_administrator": true, "installation": map[string]bool{"administer": true}})
					return
				}
				calls.Add(1)
				writeJSON(w, 503, map[string]any{"error": map[string]string{"code": code}})
			}))
			defer srv.Close()
			a.tessera = &Tessera{internal: srv.URL, http: srv.Client()}
			w := backupRequest(a, s, "POST", "/backups/now", url.Values{"csrf": {s.CSRF}})
			if w.Code != 502 || calls.Load() != 1 || strings.Contains(w.Body.String(), "outcome could not be confirmed") != (code == "") {
				t.Fatal("failure conflated", w.Body.String())
			}
		})
	}
}
func TestArchiveRejectsFIFOAndInternalSymlink(t *testing.T) {
	a, s, _, _, _, _ := backupTestApp(t)
	path := filepath.Join(a.cfg.BackupDir, testArchive)
	must(t, syscall.Mkfifo(path, 0600))
	if w := backupRequest(a, s, "GET", "/backups/archive/"+testArchive, nil); w.Code != 404 {
		t.Fatal("FIFO")
	}
	must(t, os.Remove(path))
	must(t, os.WriteFile(filepath.Join(a.cfg.BackupDir, "private"), archiveBytes(), 0600))
	must(t, os.Symlink("private", path))
	if w := backupRequest(a, s, "GET", "/backups/archive/"+testArchive, nil); w.Code != 404 {
		t.Fatal("internal symlink")
	}
}

func setupBackupSettings(t *testing.T, a *app) {
	a.cfg.StateDir = t.TempDir()
	a.cfg.BackupSettingsFile = filepath.Join(t.TempDir(), "backup-settings.json")
	must(t, backupsettings.WriteJSON(a.cfg.BackupSettingsFile, backupsettings.Generated{Values: backupsettings.Values{Destination: "local", EveryHours: 24, Keep: 7}, RequestRevision: "none", At: time.Now().UTC()}, 0644))
	must(t, os.MkdirAll(filepath.Join(a.cfg.BackupDir, "reserve"), 0700))
}
func settingsForm(a *app, s *session) url.Values {
	v := a.backupSettingsView()
	return url.Values{"csrf": {s.CSRF}, "revision": {v.Revision}, "generation": {v.Generation}, "destination": {"local"}, "every": {"12"}, "keep": {"1"}}
}
func TestBackupSettingsReviewCASExpiryAndAuthority(t *testing.T) {
	for _, variant := range []string{"household", "research"} {
		t.Run(variant, func(t *testing.T) {
			a, s, _, denied, _, _ := backupTestApp(t)
			a.cfg.Variant = variant
			setupBackupSettings(t, a)
			must(t, os.WriteFile(filepath.Join(a.cfg.BackupDir, testArchive), archiveBytes(), 0600))
			form := settingsForm(a, s)
			invalid := settingsForm(a, s)
			invalid.Set("every", "bad")
			w := backupRequest(a, s, "POST", "/backups/settings/preview", invalid)
			if w.Code != 422 || !strings.Contains(w.Body.String(), `value="bad"`) {
				t.Fatal("invalid values lost", w.Code)
			}
			invalid.Set("every", "12")
			invalid.Set("destination", "../../private")
			if w = backupRequest(a, s, "POST", "/backups/settings/preview", invalid); w.Code != 422 {
				t.Fatal("arbitrary destination")
			}
			noCSRF := settingsForm(a, s)
			noCSRF.Set("csrf", "bad")
			if w = backupRequest(a, s, "POST", "/backups/settings/preview", noCSRF); w.Code != 403 {
				t.Fatal("csrf")
			}
			w = backupRequest(a, s, "POST", "/backups/settings/preview", form)
			if w.Code != 200 || !strings.Contains(w.Body.String(), testArchive) || !strings.Contains(w.Body.String(), "saving deletes nothing") {
				t.Fatal("retention preview", w.Body.String())
			}
			before, err := backupsettings.Read(a.cfg.StateDir)
			must(t, err)
			if before.Revision != "none" {
				t.Fatal("preview changed settings")
			}
			nonce := s.BackupSettings.Nonce
			confirm := url.Values{"csrf": {s.CSRF}, "confirmation": {nonce}}
			denied.Store(true)
			if w = backupRequest(a, s, "POST", "/backups/settings/confirm", confirm); w.Code != 403 {
				t.Fatal("revoked confirm")
			}
			denied.Store(false)
			s.BackupSettings.Expires = time.Now().Add(-time.Second)
			if w = backupRequest(a, s, "POST", "/backups/settings/confirm", confirm); w.Code != 409 {
				t.Fatal("expired")
			}
			backupRequest(a, s, "POST", "/backups/settings/preview", form)
			confirm.Set("confirmation", s.BackupSettings.Nonce)
			must(t, os.Chtimes(filepath.Join(a.cfg.BackupDir, testArchive), time.Now(), time.Now().Add(time.Minute)))
			if w = backupRequest(a, s, "POST", "/backups/settings/confirm", confirm); w.Code != 409 {
				t.Fatal("stale archives")
			}
			backupRequest(a, s, "POST", "/backups/settings/preview", form)
			confirm.Set("confirmation", s.BackupSettings.Nonce)
			if w = backupRequest(a, s, "POST", "/backups/settings/confirm", confirm); w.Code != 200 || !strings.Contains(w.Body.String(), "Request saved.") {
				t.Fatal("confirm", w.Body.String())
			}
			saved, err := backupsettings.Read(a.cfg.StateDir)
			must(t, err)
			if saved.EveryHours != 12 || saved.Keep != 1 {
				t.Fatal(saved)
			}
			generated, err := backupsettings.ReadGenerated(a.cfg.BackupSettingsFile)
			must(t, err)
			if generated.EveryHours != 24 || generated.Keep != 7 {
				t.Fatal("changed generated/runtime settings")
			}
			if _, err = os.Stat(filepath.Join(a.cfg.BackupDir, testArchive)); err != nil {
				t.Fatal("save deleted archive")
			}
			if w = backupRequest(a, s, "POST", "/backups/settings/confirm", confirm); w.Code != 409 {
				t.Fatal("replay")
			}
			if w = backupRequest(a, s, "POST", "/backups/settings/preview", form); w.Code != 409 {
				t.Fatal("stale settings")
			}
		})
	}
}

func TestBackupDestinationCannotEscapeMount(t *testing.T) {
	a, s, _, _, _, _ := backupTestApp(t)
	setupBackupSettings(t, a)
	outside := t.TempDir()
	path := filepath.Join(outside, testArchive)
	must(t, os.WriteFile(path, archiveBytes(), 0600))
	info, err := os.Stat(path)
	must(t, err)
	must(t, os.Remove(filepath.Join(a.cfg.BackupDir, "reserve")))
	must(t, os.Symlink(outside, filepath.Join(a.cfg.BackupDir, "reserve")))
	w := backupRequest(a, s, "GET", "/backups/archive/"+testArchive+"?slot=reserve&version="+archiveVersion(info), nil)
	if w.Code != 404 || bytes.Equal(w.Body.Bytes(), archiveBytes()) {
		t.Fatal("destination symlink escaped mount")
	}
	w = backupRequest(a, s, "GET", "/backups?slot=reserve", nil)
	if strings.Contains(w.Body.String(), "data-archive") || !strings.Contains(w.Body.String(), "Archive storage is unavailable") {
		t.Fatal("destination symlink listed outside mount")
	}
	form := settingsForm(a, s)
	form.Set("destination", "reserve")
	if w = backupRequest(a, s, "POST", "/backups/settings/preview", form); w.Code != 503 {
		t.Fatal("unreadable destination preview accepted")
	}
}

func TestMaintenanceIntentNotAppliedWhenNativePolicyOverridesDefaults(t *testing.T) {
	a, _, _, _, _, _ := backupTestApp(t)
	a.cfg.StateDir = t.TempDir()
	a.cfg.BackupSettingsFile = filepath.Join(t.TempDir(), "generated.json")
	requested := backupsettings.Values{Destination: "local", EveryHours: 12, Keep: 1}
	must(t, backupsettings.Save(a.cfg.StateDir, "none", requested))
	r, e := backupsettings.Read(a.cfg.StateDir)
	must(t, e)
	generated := backupsettings.Generated{Values: backupsettings.Values{Destination: "local", EveryHours: 12, Keep: 2}, RequestRevision: r.Revision, At: time.Now().UTC()}
	must(t, backupsettings.WriteJSON(a.cfg.BackupSettingsFile, generated, 0600))
	if !a.backupSettingsView().Pending {
		t.Fatal("desired/generated value drift hidden by matching local revision")
	}
}

// ResponseRecorder has no connection; model supported server deadlines.
type deadlineRecorder struct{ *httptest.ResponseRecorder }

func (deadlineRecorder) SetWriteDeadline(time.Time) error { return nil }
