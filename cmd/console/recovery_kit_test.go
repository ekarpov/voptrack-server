package main

import (
	"errors"
	"fmt"
	"github.com/ekarpov/voptrack-server/internal/backupsettings"
	"github.com/ekarpov/voptrack-server/internal/recoverykit"
	"github.com/ekarpov/voptrack-server/internal/runtimeobs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestKitBrowserAuthorityAndDelivery(t *testing.T) {
	a, s, _, denied, unapproved, _ := backupTestApp(t)
	socket := filepath.Join(t.TempDir(), "kit.sock")
	listener, e := net.Listen("unix", socket)
	must(t, e)
	a.cfg.KitSocket = socket
	var deliveries, requests atomic.Int32
	id, revision := strings.Repeat("a", 48), strings.Repeat("b", 48)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			fmt.Fprint(w, `{"state":"ready","revision":"`+revision+`"}`)
		case "/request":
			requests.Add(1)
			fmt.Fprintf(w, `{"ID":%q,"Revision":%q,"Expires":%q}`, id, revision, time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano))
		case "/deliver":
			deliveries.Add(1)
			w.Header().Set("Content-Length", "13")
			fmt.Fprint(w, "synthetic-kit")
		default:
			w.WriteHeader(409)
		}
	})}
	go server.Serve(listener)
	defer server.Close()
	for _, path := range []string{"/backups/kit/prepare", "/backups/kit/request", "/backups/kit/download"} {
		if w := backupRequest(a, nil, "POST", path, nil); w.Code != 303 {
			t.Fatal("signed out", w.Code)
		}
		for _, flag := range []*atomic.Bool{denied, unapproved} {
			flag.Store(true)
			if w := backupRequest(a, s, "POST", path, url.Values{"csrf": {s.CSRF}}); w.Code != 403 {
				t.Fatal("revoked", w.Code)
			}
			flag.Store(false)
		}
		if w := backupRequest(a, s, "POST", path, url.Values{"csrf": {"wrong"}}); w.Code != 403 {
			t.Fatal("csrf", w.Code)
		}
	}
	if deliveries.Load() != 0 || requests.Load() != 0 {
		t.Fatal("unauthorized component access")
	}
	form := url.Values{"csrf": {s.CSRF}}
	w := backupRequest(a, s, "POST", "/backups/kit/request", form)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "kit-admin approve "+id+" "+revision) {
		t.Fatal(w.Code, w.Body.String())
	}
	other := *s
	other.ID = "other-session"
	other.Kit = nil
	a.sessions.byID[other.ID] = &other
	form.Set("request", id)
	if w = backupRequest(a, &other, "POST", "/backups/kit/download", form); w.Code != 403 {
		t.Fatal("cross session", w.Code)
	}
	w = backupRequest(a, s, "POST", "/backups/kit/download", form)
	if w.Code != 200 || w.Body.String() != "synthetic-kit" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = backupRequest(a, s, "POST", "/backups/kit/download", form); w.Code != 403 || deliveries.Load() != 1 {
		t.Fatal("replay")
	}
}
func TestKitRuntimeLanguagesAndPreviews(t *testing.T) {
	a, s, _, _, _, _ := backupTestApp(t)
	preview := os.Getenv("VOPTRACK_KIT_PREVIEW")
	for _, variant := range []string{"household", "research"} {
		a.cfg.Variant = variant
		for _, lang := range languages {
			for _, state := range []string{"ready", "unknown", "stale", "unavailable"} {
				d := map[string]any{"Observed": time.Now().UTC(), "Storage": "backup_none", "Settings": backupSettingsView{}, "KitEnabled": true, "Kit": recoverykit.Metadata{State: state, Revision: strings.Repeat("c", 48), Compatibility: strings.Repeat("d", 64)}, "KitIntent": kitIntent{ID: strings.Repeat("e", 48), Revision: strings.Repeat("c", 48), Expires: time.Now().Add(time.Minute)}, "Runtime": runtimeobs.Receipt{State: "running", Instance: strings.Repeat("f", 48), ConfigSHA256: strings.Repeat("a", 64), Observed: time.Now().UTC(), Values: backupsettings.Values{Destination: "local", EveryHours: 24, Keep: 7}}}
				w := httptest.NewRecorder()
				r := httptest.NewRequest("GET", "/backups?lang="+lang, nil)
				a.render(w, r, s, "backups", 200, d)
				body := w.Body.String()
				for _, key := range []string{"runtime_title", "runtime_scope", "kit_title", "kit_readiness", "kit_approval", "kit_" + state} {
					if strings.Contains(body, ">"+key+"<") {
						t.Fatal("missing translation", lang, key)
					}
				}
				if preview != "" {
					must(t, os.MkdirAll(preview, 0700))
					css, e := assets.ReadFile("static/console.css")
					must(t, e)
					for _, theme := range []string{"light", "dark"} {
						media := "@media (width: 0px)"
						if theme == "dark" {
							media = "@media all"
						}
						styled := strings.Replace(body, `<link rel="stylesheet" href="/static/console.css">`, "<style>"+strings.Replace(string(css), "@media (prefers-color-scheme: dark)", media, 1)+"</style>", 1)
						must(t, os.WriteFile(filepath.Join(preview, variant+"-"+lang+"-"+state+"-"+theme+".html"), []byte(styled), 0600))
					}
				}
			}
		}
	}
}

// A pipe with no reader deterministically stalls, independent of TCP buffer size.
type stalledKitWriter struct {
	conn    net.Conn
	header  http.Header
	armed   chan time.Time
	writing chan struct{}
}

func (w *stalledKitWriter) Header() http.Header { return w.header }
func (w *stalledKitWriter) WriteHeader(int)     {}
func (w *stalledKitWriter) Write(b []byte) (int, error) {
	close(w.writing)
	return w.conn.Write(b)
}
func (w *stalledKitWriter) SetWriteDeadline(d time.Time) error {
	if !d.IsZero() {
		w.armed <- d
		// Exercise the real net.Conn deadline without a thirty-second test.
		d = time.Now().Add(50 * time.Millisecond)
	}
	return w.conn.SetWriteDeadline(d)
}
func TestKitStalledReaderReleasesMutationFence(t *testing.T) {
	a, _, _, _, _, _ := backupTestApp(t)
	a.cfg.StateDir = t.TempDir()
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	w := &stalledKitWriter{left, make(http.Header), make(chan time.Time, 1), make(chan struct{})}
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- a.withNative(func(_ *backupsettings.Live, _ func() error) error {
			_, err := writeKitBrowser(w, []byte("synthetic-kit"))
			return err
		})
	}()
	select {
	case <-w.writing:
	case <-time.After(2 * time.Second):
		t.Fatal("delivery did not enter write")
	}
	var deadline time.Time
	select {
	case deadline = <-w.armed:
	case <-time.After(2 * time.Second):
		t.Fatal("browser write deadline was not armed")
	}
	if deadline.Before(start) || deadline.After(start.Add(kitBrowserWriteTimeout+time.Second)) {
		t.Fatal("unbounded browser deadline")
	}
	acquired := make(chan error, 1)
	go func() { acquired <- a.withNative(func(_ *backupsettings.Live, _ func() error) error { return nil }) }()
	select {
	case err := <-done:
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatalf("expected stalled write deadline: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled delivery retained mutation locks")
	}
	select {
	case err := <-acquired:
		must(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("subsequent mutation remains blocked")
	}
}
func TestKitDeadlineUnsupportedFailsBeforeHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	started, err := writeKitBrowser(w, []byte("synthetic-kit"))
	if started || !errors.Is(err, http.ErrNotSupported) {
		t.Fatal("unsupported deadline writer accepted")
	}
	if len(w.Header()) != 0 || w.Body.Len() != 0 {
		t.Fatal("sensitive response started without deadline")
	}
}

// Exercise ResponseController against the actual net/http response writer, not
// only a writer stub. net.Pipe guarantees a no-reader block without kernel buffer
// assumptions; this one test deliberately uses the production timeout.
func TestKitStalledHTTPReaderReleasesMutationFence(t *testing.T) {
	a, _, _, _, _, _ := backupTestApp(t)
	a.cfg.StateDir = t.TempDir()
	serverConn, browserConn := net.Pipe()
	defer browserConn.Close()
	listener := &singleKitListener{conn: make(chan net.Conn, 1), closed: make(chan struct{})}
	listener.conn <- serverConn
	done := make(chan error, 1)
	entered := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		done <- a.withNative(func(_ *backupsettings.Live, _ func() error) error {
			close(entered)
			_, err := writeKitBrowser(w, []byte("synthetic-kit"))
			return err
		})
	})}
	defer server.Close()
	go server.Serve(listener)
	_, err := fmt.Fprint(browserConn, "GET / HTTP/1.1\r\nHost: synthetic\r\n\r\n")
	must(t, err)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP delivery did not enter mutation fence")
	}
	acquired := make(chan error, 1)
	go func() { acquired <- a.withNative(func(_ *backupsettings.Live, _ func() error) error { return nil }) }()
	select {
	case <-acquired:
		t.Fatal("HTTP delivery released acknowledgement fence while write pending")
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case err := <-done:
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatalf("expected actual HTTP write timeout: %v", err)
		}
	case <-time.After(kitBrowserWriteTimeout + 3*time.Second):
		t.Fatal("actual HTTP reader retained mutation fence")
	}
	select {
	case err := <-acquired:
		must(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP timeout did not release mutation fence")
	}
}

type singleKitListener struct {
	conn   chan net.Conn
	closed chan struct{}
}

func (l *singleKitListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conn:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *singleKitListener) Close() error   { close(l.closed); return nil }
func (l *singleKitListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
