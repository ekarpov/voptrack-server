package homesetup

import (
	"archive/tar"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (Config, *Service) {
	t.Helper()
	root := t.TempDir()
	os.Chmod(root, 0700)
	state := filepath.Join(root, "control")
	os.Mkdir(state, 0700)
	for _, n := range roots {
		os.MkdirAll(filepath.Join(root, "var/lib", n), 0700)
	}
	c := Config{State: state, Root: root, Origin: "https://localhost:9673", PublicURL: "https://home.example"}
	s, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return c, s
}
func call(s *Service, method, path string, cookie *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	var input strings.Reader
	if form != nil {
		input = *strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, s.Config.Origin+path, &input)
	r.TLS = &tls.ConnectionState{Version: tls.VersionTLS13}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if method == "POST" {
		r.Header.Set("Origin", s.Config.Origin)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func field(t *testing.T, w *httptest.ResponseRecorder, name string) string {
	t.Helper()
	m := regexp.MustCompile("name=\"" + name + "\" value=\"([^\"]*)\"").FindStringSubmatch(w.Body.String())
	if len(m) != 2 {
		t.Fatal("missing form field", name)
	}
	return m[1]
}
func auth(t *testing.T, s *Service) (*http.Cookie, string) {
	t.Helper()
	a, e := Access(s.Config.State)
	if e != nil {
		t.Fatal(e)
	}
	w := call(s, "GET", "/", nil, nil)
	c := w.Result().Cookies()[0]
	csrf := field(t, w, "csrf")
	w = call(s, "POST", "/auth", c, url.Values{"csrf": {csrf}, "lang": {"en"}, "code": {a["code"]}})
	if w.Code != 303 {
		t.Fatal("host authorization failed")
	}
	c = w.Result().Cookies()[0]
	w = call(s, "GET", "/", c, nil)
	return c, field(t, w, "csrf")
}
func settings(csrf string) url.Values {
	return url.Values{"csrf": {csrf}, "lang": {"en"}, "name": {"Synthetic Home"}, "timezone": {"Europe/Helsinki"}, "mode": {"off"}, "encrypted": {"yes"}}
}
func review(t *testing.T, s *Service, c *http.Cookie, csrf string) (string, string) {
	t.Helper()
	w := call(s, "POST", "/settings", c, settings(csrf))
	if w.Code != 303 {
		t.Fatal("settings rejected")
	}
	w = call(s, "GET", "/", c, nil)
	return field(t, w, "confirm"), field(t, w, "binding")
}
func TestHostAuthorityCSRFReplayAndChoiceBinding(t *testing.T) {
	_, s := fixture(t)
	a, _ := Access(s.Config.State)
	w := call(s, "GET", "/", nil, nil)
	pre := w.Result().Cookies()[0]
	csrf := field(t, w, "csrf")
	r := httptest.NewRequest("POST", s.Config.Origin+"/auth", strings.NewReader(url.Values{"csrf": {csrf}, "code": {a["code"]}}.Encode()))
	r.TLS = &tls.ConnectionState{Version: tls.VersionTLS13}
	r.AddCookie(pre)
	r.Header.Set("Origin", "https://elsewhere.example")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	denied := httptest.NewRecorder()
	s.ServeHTTP(denied, r)
	if denied.Code != 403 {
		t.Fatal("cross-origin authorization admitted")
	}
	w = call(s, "POST", "/auth", pre, url.Values{"csrf": {"wrong"}, "code": {a["code"]}})
	if w.Code != 403 {
		t.Fatal("CSRF admitted")
	}
	c, csrf := auth(t, s)
	confirm, binding := review(t, s, c, csrf)
	w = call(s, "POST", "/apply", c, url.Values{"csrf": {csrf}, "binding": {"changed"}, "confirm": {confirm}, "password": {"synthetic-owner-password"}, "repeat": {"synthetic-owner-password"}})
	if w.Code != 409 || s.journal.Phase != "review" {
		t.Fatal("changed review binding admitted")
	}
	w = call(s, "GET", "/", nil, nil)
	other := w.Result().Cookies()[0]
	w = call(s, "POST", "/auth", other, url.Values{"csrf": {field(t, w, "csrf")}, "code": {a["code"]}})
	if w.Code != 403 {
		t.Fatal("single-use host capability replayed")
	}
	s.now = func() time.Time { return time.Now().Add(6 * time.Minute) }
	w = call(s, "POST", "/apply", c, url.Values{"csrf": {csrf}, "binding": {binding}, "confirm": {confirm}, "password": {"synthetic-owner-password"}, "repeat": {"synthetic-owner-password"}})
	if w.Code != 409 {
		t.Fatal("expired confirmation admitted")
	}
}
func TestExistingAndChangedTargetsArePreserved(t *testing.T) {
	c, s := fixture(t)
	cookie, csrf := auth(t, s)
	confirm, binding := review(t, s, cookie, csrf)
	sentinel := filepath.Join(c.Root, "var/lib/tessera/preserve")
	original := []byte("synthetic existing target")
	os.WriteFile(sentinel, original, 0600)
	w := call(s, "POST", "/apply", cookie, url.Values{"csrf": {csrf}, "binding": {binding}, "confirm": {confirm}, "password": {"synthetic-owner-password"}, "repeat": {"synthetic-owner-password"}})
	if w.Code != 409 || s.journal.Phase != "stopped" {
		t.Fatal("new target contents after preview admitted")
	}
	raw, _ := os.ReadFile(sentinel)
	if string(raw) != string(original) {
		t.Fatal("existing target changed")
	}
	s.Close()
	freshControl := filepath.Join(c.Root, "different-control")
	os.Mkdir(freshControl, 0700)
	c.State = freshControl
	if other, e := New(c); e == nil {
		other.Close()
		t.Fatal("new setup state bypassed populated target refusal")
	}
	raw, _ = os.ReadFile(sentinel)
	if string(raw) != string(original) {
		t.Fatal("freshness refusal modified target")
	}
}
func TestInterruptedMutationCannotBecomeRetryable(t *testing.T) {
	c, s := fixture(t)
	s.journal.Phase = "password"
	if s.save() != nil {
		t.Fatal("save failed")
	}
	s.Close()
	restarted, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Close()
	if restarted.journal.Phase != "stopped" || Authorize(c.State) == nil {
		t.Fatal("interrupted initialization became retryable")
	}
	if _, e = os.Stat(at(c.Root, PendingFile)); e != nil {
		t.Fatal("interruption removed service startup guard")
	}
}
func TestFinishRequiresDeliveryAndCustodyAndClosesAuthority(t *testing.T) {
	c, s := fixture(t)
	cookie, csrf := auth(t, s)
	s.journal.Phase = "ready"
	var material bytes.Buffer
	tw := tar.NewWriter(&material)
	tw.WriteHeader(&tar.Header{Name: "var/lib/voptrack-config/backup.key", Mode: 0600, Size: 32, Typeflag: tar.TypeReg})
	tw.Write(make([]byte, 32))
	tw.Close()
	raw := material.Bytes()
	s.journal.KitHash = digest(raw)
	s.save()
	os.WriteFile(filepath.Join(c.State, "initial-kit.tar"), raw, 0600)
	os.WriteFile(at(c.Root, "var/lib/voptrack-config/backup.key"), bytes.Repeat([]byte{1}, 32), 0600)
	form := url.Values{"csrf": {csrf}, "lang": {"en"}, "kitsha": {s.journal.KitHash}, "custody": {"yes"}}
	if call(s, "POST", "/finish", cookie, form).Code != 409 {
		t.Fatal("custody skipped required downloads")
	}
	for _, path := range []string{"/kit", "/key"} {
		w := call(s, "POST", path, cookie, url.Values{"csrf": {csrf}})
		if w.Code != 200 || w.Header().Get("Content-Type") != "application/octet-stream" {
			t.Fatal("authorized initial export failed")
		}
		if path == "/key" && !bytes.Equal(w.Body.Bytes(), make([]byte, 32)) {
			t.Fatal("key download mixed material generations")
		}
	}
	form.Set("kitsha", "changed")
	if call(s, "POST", "/finish", cookie, form).Code != 409 {
		t.Fatal("custody receipt not bound to material")
	}
	form.Set("kitsha", s.journal.KitHash)
	if call(s, "POST", "/finish", cookie, form).Code != 200 {
		t.Fatal("completion failed")
	}
	if _, e := os.Lstat(at(c.Root, PendingFile)); !os.IsNotExist(e) {
		t.Fatal("completed setup did not release service guard")
	}
	if Authorize(c.State) == nil {
		t.Fatal("completed setup reauthorized")
	}
	s.Close()
	again, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	defer again.Close()
	if !again.Complete() || again.ReleaseCompleted() != nil {
		t.Fatal("completion reconciliation failed")
	}
}
func TestChoicesAndLaunchGuardFailClosed(t *testing.T) {
	c, s := fixture(t)
	if LaunchAllowed(at(c.Root, PendingFile)) == nil {
		t.Fatal("pending setup allowed service launch")
	}
	valid := Choices{1, "Home", "UTC", "private", true}
	for _, change := range []func(*Choices){func(c *Choices) { c.TimeZone = "Local" }, func(c *Choices) { c.TimeZone = "../UTC" }, func(c *Choices) { c.Name = "bad\"name" }, func(c *Choices) { c.Mode = "unknown" }, func(c *Choices) { c.Encrypted = false }} {
		v := valid
		change(&v)
		if v.Valid() {
			t.Fatal("unsafe setup settings accepted")
		}
	}
	raw, _ := json.Marshal(valid)
	if _, e := DecodeChoices(raw); e != nil {
		t.Fatal(e)
	}
	if _, e := DecodeChoices(append(raw, []byte(" {}")...)); e == nil {
		t.Fatal("extra settings document accepted")
	}
	if len(messages) != 7 {
		t.Fatal("languages missing")
	}
	for _, l := range languages {
		for _, k := range keys {
			if messages[l][k] == "" {
				t.Fatal("translation missing", l, k)
			}
		}
	}
	s.Close()
}
func TestSetupUICorpus(t *testing.T) {
	_, s := fixture(t)
	cookie, _ := auth(t, s)
	v := s.sessions[cookie.Value]
	dir := os.Getenv("VOPTRACK_HOME_SETUP_UI_DIR")
	for _, l := range languages {
		for _, phase := range []string{"auth", "draft", "review", "accepted", "ready", "stopped", "complete"} {
			s.journal.Phase = phase
			s.journal.Choices = Choices{1, "Synthetic Home / " + strings.Repeat("x", 60), "Europe/Helsinki", "public", true}
			s.journal.KitHash = strings.Repeat("a", 64)
			s.journal.Binding = strings.Repeat("b", 64)
			v.Downloaded = true
			v.KeyDownloaded = true
			w := httptest.NewRecorder()
			if phase == "auth" {
				s.render(w, l, &session{CSRF: "synthetic-csrf"}, "", nil)
			} else {
				s.render(w, l, v, "", nil)
			}
			if !strings.Contains(w.Body.String(), messages[l]["title"]) {
				t.Fatal("localized title absent", l, phase)
			}
			if dir != "" {
				os.MkdirAll(filepath.Join(dir, "html"), 0700)
				if e := os.WriteFile(filepath.Join(dir, "html", "home-"+l+"-"+phase+".html"), w.Body.Bytes(), 0600); e != nil {
					t.Fatal(e)
				}
			}
		}
	}
}

func TestHostReauthorizationAndAbsoluteExpiry(t *testing.T) {
	c, s := fixture(t)
	cookie, csrf := auth(t, s)
	if Authorize(c.State) != nil {
		t.Fatal("explicit host reauthorization failed")
	}
	if call(s, "POST", "/settings", cookie, settings(csrf)).Code != 403 {
		t.Fatal("new host grant left old browser authorized")
	}
	cookie, csrf = auth(t, s)
	s.now = func() time.Time { return time.Now().Add(16 * time.Minute) }
	if call(s, "POST", "/settings", cookie, settings(csrf)).Code != 403 {
		t.Fatal("absolute session expiry bypassed")
	}
}
func TestSubstitutedRootsAndCompletionMarkerAreRefused(t *testing.T) {
	c, s := fixture(t)
	original := at(c.Root, "var/lib/tessera")
	if os.Rename(original, original+"-preserved") != nil || os.Mkdir(original, 0700) != nil {
		t.Fatal("fixture rename failed")
	}
	if s.bound() == nil {
		t.Fatal("substituted data root accepted")
	}
	s.Close()
	if restarted, e := New(c); e == nil {
		restarted.Close()
		t.Fatal("restart rebound existing attempt")
	}
	if _, e := os.Stat(original + "-preserved"); e != nil {
		t.Fatal("original target removed")
	}
	c, s = fixture(t)
	s.journal.Phase = "complete"
	if s.save() != nil {
		t.Fatal("save failed")
	}
	if os.WriteFile(at(c.Root, PendingFile), []byte("different-attempt"), 0600) != nil {
		t.Fatal("fixture write failed")
	}
	if s.ReleaseCompleted() == nil {
		t.Fatal("different attempt guard removed")
	}
	raw, _ := os.ReadFile(at(c.Root, PendingFile))
	if string(raw) != "different-attempt" {
		t.Fatal("foreign guard changed")
	}
	outside := filepath.Join(c.Root, "outside")
	os.WriteFile(outside, []byte(s.journal.Attempt), 0600)
	os.Remove(at(c.Root, PendingFile))
	os.Symlink(outside, at(c.Root, PendingFile))
	if s.ReleaseCompleted() == nil {
		t.Fatal("indirect guard accepted")
	}
	if _, e := os.Stat(outside); e != nil {
		t.Fatal("indirect guard target removed")
	}
}

func TestCompletionMustEstablishJournalDurabilityBeforeReleasingStartup(t *testing.T) {
	c, s := fixture(t)
	s.journal.Phase = "complete"
	if s.save() != nil {
		t.Fatal("save failed")
	}
	called := false
	e := s.releaseAfterStateSync(func(path string) error {
		called = true
		if path != c.State {
			t.Fatal("wrong durability barrier")
		}
		if _, e := os.Stat(at(c.Root, PendingFile)); e != nil {
			t.Fatal("guard removed before journal sync")
		}
		return ErrRefused
	})
	if e == nil || !called {
		t.Fatal("failed journal durability barrier ignored")
	}
	if LaunchAllowed(at(c.Root, PendingFile)) == nil {
		t.Fatal("sync failure released startup")
	}
	if s.release() != nil || LaunchAllowed(at(c.Root, PendingFile)) != nil {
		t.Fatal("durable retry failed")
	}
}

func TestConcurrentConfirmationConsumesExecutionAuthorityOnce(t *testing.T) {
	_, s := fixture(t)
	cookie, csrf := auth(t, s)
	confirm, binding := review(t, s, cookie, csrf)
	form := url.Values{"csrf": {csrf}, "binding": {binding}, "confirm": {confirm}, "password": {"synthetic-owner-password"}, "repeat": {"synthetic-owner-password"}}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- call(s, "POST", "/apply", cookie, form).Code }()
	}
	wg.Wait()
	close(codes)
	accepted := 0
	for c := range codes {
		if c == 303 {
			accepted++
		} else if c != 409 {
			t.Fatal("unexpected confirmation response", c)
		}
	}
	s.workers.Wait()
	if accepted != 1 || s.journal.Phase != "stopped" {
		t.Fatal("execution authority was replayed")
	}
}

func TestRestartRequiresNewHostApprovalEvenForUnusedGrant(t *testing.T) {
	for _, phase := range []string{"draft", "review", "ready"} {
		t.Run(phase, func(t *testing.T) {
			c, s := fixture(t)
			s.journal.Phase = phase
			if s.save() != nil {
				t.Fatal("save failed")
			}
			if _, e := Access(c.State); e != nil {
				t.Fatal("initial host code unavailable")
			}
			s.Close()
			next, e := New(c)
			if e != nil {
				t.Fatal(e)
			}
			defer next.Close()
			if _, e = Access(c.State); e == nil {
				t.Fatal("restart preserved unused host grant")
			}
			if Authorize(c.State) != nil {
				t.Fatal("explicit host approval failed")
			}
			if _, e = Access(c.State); e != nil {
				t.Fatal("new approved code unavailable")
			}
		})
	}
}

func TestCompletedSetupDoesNotLockFutureHostRoutingConfiguration(t *testing.T) {
	c, s := fixture(t)
	s.journal.Phase = "complete"
	if s.save() != nil {
		t.Fatal("save failed")
	}
	s.Close()
	c.PublicURL = "https://replacement-host.example"
	c.Origin = "https://localhost:9674"
	next, e := New(c)
	if e != nil {
		t.Fatal("completed setup blocked host routing update", e)
	}
	defer next.Close()
	if !next.Complete() || next.ReleaseCompleted() != nil || Authorize(c.State) == nil {
		t.Fatal("completed host update reopened setup")
	}
}

func TestMissingCompletionGuardStillRequiresDurableParent(t *testing.T) {
	c, s := fixture(t)
	s.journal.Phase = "complete"
	if s.save() != nil {
		t.Fatal("save failed")
	}
	path := at(c.Root, PendingFile)
	if os.Remove(path) != nil {
		t.Fatal("fixture unlink failed")
	}
	// Inject the failed directory sync: chmod cannot force it when CI runs as root.
	called := false
	if s.releaseAfterSync(syncDir, func(parent string) error {
		called = true
		if parent != filepath.Dir(path) {
			t.Fatal("wrong guard durability barrier")
		}
		return ErrRefused
	}) == nil || !called {
		t.Fatal("missing guard bypassed failed parent sync")
	}
	if s.release() != nil {
		t.Fatal("durable guard-absence retry failed")
	}
}
