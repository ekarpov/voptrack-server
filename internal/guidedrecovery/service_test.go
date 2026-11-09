package guidedrecovery

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
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

func fixture(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{"state", "targets", "inputs", "trust", "key"} {
		if e := os.Mkdir(filepath.Join(root, p), 0700); e != nil {
			t.Fatal(e)
		}
	}
	s, e := Open(Config{Origin: "https://recovery.example", State: filepath.Join(root, "state"), Target: filepath.Join(root, "targets/fresh"), Sets: []Set{{"home", "Synthetic Home", filepath.Join(root, "inputs"), filepath.Join(root, "trust/lineage"), filepath.Join(root, "key/key")}}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	tokenPath := filepath.Join(root, "authorization")
	if e = s.Authorize(tokenPath); e != nil {
		t.Fatal(e)
	}
	token, e := os.ReadFile(tokenPath)
	if e != nil {
		t.Fatal(e)
	}
	return s, string(token)
}
func request(s *Service, method, path string, cookie *http.Cookie, fields url.Values) *httptest.ResponseRecorder {
	body := ""
	if fields != nil {
		body = fields.Encode()
	}
	r := httptest.NewRequest(method, "https://recovery.example"+path, strings.NewReader(body))
	r.TLS = &tls.ConnectionState{Version: tls.VersionTLS13}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if method == "POST" {
		r.Header.Set("Origin", "https://recovery.example")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func start(t *testing.T, s *Service) (*http.Cookie, *session) {
	t.Helper()
	w := request(s, "GET", "/", nil, nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	c := w.Result().Cookies()[0]
	return c, s.sessions[c.Value]
}
func login(t *testing.T, s *Service, token string) (*http.Cookie, *session) {
	t.Helper()
	c, v := start(t, s)
	w := request(s, "POST", "/auth", c, url.Values{"csrf": {v.CSRF}, "token": {token}})
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	c = w.Result().Cookies()[0]
	return c, s.sessions[c.Value]
}
func TestAuthorityIsolationReplayExpiryAndBudget(t *testing.T) {
	s, token := fixture(t)
	c, session := login(t, s, token)
	if strings.Contains(request(s, "GET", "/", nil, nil).Body.String(), "Synthetic Home") {
		t.Fatal("anonymous source disclosure")
	}
	if strings.Contains(request(s, "GET", "/", c, nil).Body.String(), token) {
		t.Fatal("authorization leaked")
	}
	other, pre := start(t, s)
	if w := request(s, "POST", "/auth", other, url.Values{"csrf": {pre.CSRF}, "token": {token}}); w.Code != 403 {
		t.Fatal("replay accepted")
	}
	if w := request(s, "POST", "/validate", other, url.Values{"csrf": {pre.CSRF}, "set": {"home"}, "target": {"fresh"}}); w.Code != 403 {
		t.Fatal("unrelated session admitted")
	}
	s.mu.Lock()
	session.Expires = s.now().Add(-time.Second)
	s.mu.Unlock()
	if w := request(s, "POST", "/validate", c, url.Values{"csrf": {session.CSRF}, "set": {"home"}, "target": {"fresh"}}); w.Code != 403 {
		t.Fatal("expired session admitted")
	}
	s2, token2 := fixture(t)
	c2, v2 := start(t, s2)
	for i := 0; i < 5; i++ {
		if w := request(s2, "POST", "/auth", c2, url.Values{"csrf": {v2.CSRF}, "token": {"wrong"}}); w.Code != 403 {
			t.Fatal("wrong auth admitted")
		}
	}
	if request(s2, "POST", "/auth", c2, url.Values{"csrf": {v2.CSRF}, "token": {token2}}).Code != 403 {
		t.Fatal("auth budget bypass")
	}
	s3, token3 := fixture(t)
	c3, v3 := start(t, s3)
	s3.journal.AuthExpires = s3.now().Add(-time.Second)
	if request(s3, "POST", "/auth", c3, url.Values{"csrf": {v3.CSRF}, "token": {token3}}).Code != 403 {
		t.Fatal("expired host auth")
	}
}
func TestTransportOriginHostCSRFAndBody(t *testing.T) {
	s, token := fixture(t)
	c, v := login(t, s, token)
	for _, change := range []func(*http.Request){
		func(r *http.Request) { r.TLS = nil },
		func(r *http.Request) { r.Host = "attacker.example" },
		func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") },
		func(r *http.Request) { r.Header.Del("Origin") },
		func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
	} {
		r := httptest.NewRequest("POST", "https://recovery.example/validate", strings.NewReader(url.Values{"csrf": {v.CSRF}}.Encode()))
		r.TLS = &tls.ConnectionState{Version: tls.VersionTLS13}
		r.Header.Set("Origin", "https://recovery.example")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(c)
		change(r)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("unsafe request admitted", w.Code)
		}
	}
	for _, fields := range []url.Values{
		{"csrf": {"wrong"}}, {"csrf": {v.CSRF}, "command": {"arbitrary"}},
		{"csrf": {v.CSRF, v.CSRF}}, {"csrf": {v.CSRF}, "set": {strings.Repeat("x", 5000)}},
	} {
		if request(s, "POST", "/validate", c, fields).Code < 400 {
			t.Fatal("unsafe form admitted")
		}
	}
}
func review(t *testing.T, s *Service, v *session) url.Values {
	t.Helper()
	s.journal.Phase = "review"
	s.journal.Attempt = "attempt"
	s.journal.Set = "home"
	s.journal.Binding = "bound"
	v.Confirm = "single-use"
	v.Binding = "bound"
	v.ConfirmExpires = s.now().Add(time.Minute)
	if e := s.save(); e != nil {
		t.Fatal(e)
	}
	return url.Values{"csrf": {v.CSRF}, "confirm": {v.Confirm}, "binding": {"bound"}, "attempt": {"attempt"}, "set": {"home"}, "target": {"fresh"}}
}
func TestConfirmationBindingAndConcurrentSubmissions(t *testing.T) {
	for _, field := range []string{"csrf", "confirm", "binding", "attempt", "set", "target", "expired", "unrelated"} {
		t.Run(field, func(t *testing.T) {
			s, token := fixture(t)
			c, v := login(t, s, token)
			form := review(t, s, v)
			if field == "expired" {
				v.ConfirmExpires = s.now().Add(-time.Second)
			} else if field == "unrelated" {
				c, _ = start(t, s)
			} else {
				form.Set(field, "changed")
			}
			if w := request(s, "POST", "/confirm", c, form); w.Code < 400 {
				t.Fatal("changed binding admitted", field)
			}
			if !absent(s.config.Target) {
				t.Fatal("target touched")
			}
		})
	}
	s, token := fixture(t)
	c, v := login(t, s, token)
	form := review(t, s, v)
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- request(s, "POST", "/confirm", c, form).Code }()
	}
	wg.Wait()
	close(codes)
	accepted := 0
	for code := range codes {
		if code == 303 {
			accepted++
		} else if code != 409 {
			t.Fatal(code)
		}
	}
	s.workers.Wait()
	if accepted != 1 || !absent(s.config.Target) || s.journal.Phase != "stopped" {
		t.Fatal("duplicate execution or false success", accepted, s.journal.Phase)
	}
	if !absent(filepath.Join(s.config.State, "snapshot")) {
		t.Fatal("temporary secrets retained")
	}
}
func TestJournalCrashStagesAndTargetPreservation(t *testing.T) {
	for _, phase := range []string{"accepted", "creating-target", "importing-kit", "recovering-producer", "restoring-records", "verifying", "complete", "stopped"} {
		t.Run(phase, func(t *testing.T) {
			s, _ := fixture(t)
			c := s.config
			s.journal.Phase = phase
			if e := os.Mkdir(c.Target, 0700); e != nil {
				t.Fatal(e)
			}
			marker := filepath.Join(c.Target, "preserve")
			os.WriteFile(marker, []byte("partial"), 0600)
			if e := s.save(); e != nil {
				t.Fatal(e)
			}
			s.Close()
			restarted, e := Open(c)
			if e != nil {
				t.Fatal(e)
			}
			defer restarted.Close()
			expected := "stopped"
			if phase == "complete" {
				expected = "complete"
			}
			if restarted.journal.Phase != expected {
				t.Fatal("interruption replayed", phase)
			}
			if e = restarted.validate(context.Background(), "home"); e == nil {
				t.Fatal("existing target accepted")
			}
			b, e := os.ReadFile(marker)
			if e != nil || string(b) != "partial" {
				t.Fatal("target modified")
			}
			if len(restarted.sessions) != 0 {
				t.Fatal("restart recovered browser authority")
			}
		})
	}
}
func TestDurableAuthAndProcessTargetExclusivity(t *testing.T) {
	s, token := fixture(t)
	login(t, s, token)
	if _, e := Open(s.config); e == nil {
		t.Fatal("second process accepted")
	}
	c := s.config
	c.State = t.TempDir()
	if _, e := Open(c); e == nil {
		t.Fatal("different state bypassed target lock")
	}
	c = s.config
	s.Close()
	restarted, e := Open(c)
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Close()
	if !restarted.journal.AuthConsumed {
		t.Fatal("authority consumption not durable")
	}
	changed := c
	changed.Target = filepath.Join(filepath.Dir(c.Target), "other")
	restarted.Close()
	if _, e := Open(changed); e == nil {
		t.Fatal("changed target reused journal")
	}
}
func TestExpiryCleansTemporaryCopiesAndTranslations(t *testing.T) {
	s, token := fixture(t)
	c, v := login(t, s, token)
	review(t, s, v)
	dir := filepath.Join(s.config.State, "snapshot")
	os.Mkdir(dir, 0700)
	os.WriteFile(filepath.Join(dir, "secret"), []byte("synthetic"), 0600)
	v.Expires = s.now().Add(-time.Second)
	s.Expire()
	if !absent(dir) || s.journal.Phase != "refused" {
		t.Fatal("expiry failed cleanup")
	}
	for _, l := range languages {
		c, v = loginForView(t, s)
		s.journal.Phase = "review"
		s.journal.Set = "home"
		s.journal.Binding = "bound"
		s.journal.Attempt = "attempt"
		w := request(s, "GET", "/?lang="+l, c, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "<html lang=\""+l+"\">") || strings.Contains(w.Body.String(), "<script") {
			t.Fatal("invalid localized view", l)
		}
		for key, value := range translations["en"] {
			if value == "" || translations[l][key] == "" {
				t.Fatal("missing translation", l, key)
			}
		}
		_ = v
	}
	raw, e := json.Marshal(s.journal)
	if e != nil || strings.Contains(string(raw), token) {
		t.Fatal("raw authorization persisted")
	}
}
func loginForView(t *testing.T, s *Service) (*http.Cookie, *session) {
	c, v := start(t, s)
	v.Authorized = true
	return c, v
}

func TestInvalidJournalAndChangedTargetAreNeverRewritten(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		s, _ := fixture(t)
		c := s.config
		s.Close()
		path := filepath.Join(c.State, "journal.json")
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		if invalid {
			raw = append(raw, []byte("invalid trailing bytes")...)
			os.WriteFile(path, raw, 0600)
		} else {
			c.Target = filepath.Join(filepath.Dir(c.Target), "changed")
		}
		if got, e := Open(c); e == nil {
			got.Close()
			t.Fatal("invalid journal/configuration accepted")
		}
		after, e := os.ReadFile(path)
		if e != nil || string(after) != string(raw) {
			t.Fatal("failed startup rewrote retained journal")
		}
	}
}

func TestRefusedPagesOfferSafeLocalizedNavigation(t *testing.T) {
	s, _ := fixture(t)
	c, v := start(t, s)
	w := request(s, "POST", "/auth", c, url.Values{"csrf": {v.CSRF}, "token": {"do-not-echo"}, "lang": {"fi"}})
	if w.Code != 403 || strings.Contains(w.Body.String(), "do-not-echo") || strings.Contains(w.Body.String(), "Synthetic Home") || !strings.Contains(w.Body.String(), tr("fi", "refresh")) || strings.Contains(w.Body.String(), "<form") {
		t.Fatal("unsafe or missing refusal navigation")
	}
}

type advancingReader struct {
	io.Reader
	advance func()
}

func (r *advancingReader) Read(b []byte) (int, error) {
	if r.advance != nil {
		f := r.advance
		r.advance = nil
		f()
	}
	return r.Reader.Read(b)
}
func TestSessionExpiryWhileReadingBodyCannotAdmitRecovery(t *testing.T) {
	s, token := fixture(t)
	c, v := login(t, s, token)
	form := review(t, s, v)
	clock := s.now()
	s.now = func() time.Time { return clock }
	v.Expires = clock.Add(time.Second)
	body := &advancingReader{Reader: strings.NewReader(form.Encode()), advance: func() { clock = clock.Add(2 * time.Second) }}
	r := httptest.NewRequest("POST", "https://recovery.example/confirm", body)
	r.TLS = &tls.ConnectionState{Version: tls.VersionTLS13}
	r.Header.Set("Origin", "https://recovery.example")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(c)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 || !absent(s.config.Target) {
		t.Fatal("expired body read admitted recovery", w.Code)
	}
	for _, stage := range s.journal.History {
		if stage.Phase == "accepted" {
			t.Fatal("expired request became durable admission")
		}
	}
}
