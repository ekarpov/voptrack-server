package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func rememberedFixture(t *testing.T) (*sessions, *session, string) {
	t.Helper()
	dir := t.TempDir()
	s := newSessions()
	must(t, s.enableRemember(dir, "https://synthetic.invalid"))
	sess := s.get(httptest.NewRecorder(), httptest.NewRequest("GET", "https://synthetic.invalid/login", nil), true)
	sess.Account = "synthetic"
	sess.Owner = &connection{tokens: tokens{Access: "synthetic-access", Refresh: "synthetic-refresh", Expires: time.Now().Add(time.Hour)}}
	sess.Pending["transient"] = pending{Verifier: "must-not-persist"}
	sess.Invited = []Invitation{{}}
	sess.Erasing = &erasing{Token: "synthetic-erase"}
	sess.Moderation = &moderationIntent{}
	sess.Kit = &kitIntent{}
	sess.BackupSettings = &backupSettingsIntent{}
	sess.NativeSettings = &nativeIntent{}
	w := httptest.NewRecorder()
	old := sess.ID
	must(t, s.complete(w, sess, true))
	if sess.ID == old || len(w.Result().Cookies()) != 1 {
		t.Fatal("authentication did not rotate session")
	}
	c := w.Result().Cookies()[0]
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.MaxAge < 29*24*3600 || c.MaxAge > 30*24*3600 || c.Domain != "" {
		t.Fatal("unsafe remembered cookie")
	}
	return s, sess, dir
}
func TestRememberSurvivesRestartAndOnlyPersistsCredentials(t *testing.T) {
	s, sess, dir := rememberedFixture(t)
	raw, e := os.ReadFile(filepath.Join(s.store.dir, rememberFile(sess.ID)))
	must(t, e)
	for _, secret := range []string{sess.ID, "synthetic", "must-not-persist"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("plaintext credential/session persisted")
		}
	}
	restored := newSessions()
	must(t, restored.enableRemember(dir, "https://synthetic.invalid"))
	var checked bool
	restored.validate = func(context.Context, *session) bool { checked = true; return true }
	req := httptest.NewRequest("GET", "https://synthetic.invalid/home", nil)
	req.AddCookie(&http.Cookie{Name: restored.cookie, Value: sess.ID})
	got := restored.get(httptest.NewRecorder(), req, false)
	if got == nil || !checked || len(got.Pending) != 0 || len(got.Invited) != 0 || got.Erasing != nil || got.Moderation != nil || got.Kit != nil || got.BackupSettings != nil || got.NativeSettings != nil || got.Owner.tokens.Access != "" || got.Owner.tokens.Refresh != "synthetic-refresh" || !got.RememberUntil.Equal(sess.RememberUntil) {
		t.Fatal("restart restored transient authority or lost the remembered login")
	}
	if !restored.drop(httptest.NewRecorder(), got) {
		t.Fatal("logout failed")
	}
	again := newSessions()
	must(t, again.enableRemember(dir, "https://synthetic.invalid"))
	again.validate = restored.validate
	if again.get(httptest.NewRecorder(), req, false) != nil {
		t.Fatal("logout replay survived restart")
	}
}
func TestRememberAbsoluteExpiryAndOrdinarySessionPolicy(t *testing.T) {
	s, sess, _ := rememberedFixture(t)
	deadline := sess.RememberUntil
	sess.LastSeen = deadline.Add(-time.Minute)
	if s.expired(sess, deadline.Add(-time.Second)) || !s.expired(sess, deadline) {
		t.Fatal("remembered lifetime was sliding or lacked a hard expiry")
	}
	ordinary := &session{Owner: &connection{}, LastSeen: time.Now().Add(-13 * time.Hour)}
	if !s.expired(ordinary, time.Now()) {
		t.Fatal("ordinary session lifetime expanded")
	}
	w := httptest.NewRecorder()
	must(t, s.complete(w, sess, false))
	if w.Result().Cookies()[0].MaxAge != 0 || !sess.RememberUntil.IsZero() {
		t.Fatal("unchecked sign-in persisted")
	}
}
func TestRememberRevocationAndRefreshRotation(t *testing.T) {
	s, sess, dir := rememberedFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"access_token": "rotated-access", "refresh_token": "rotated-refresh", "expires_in": 300})
	}))
	defer server.Close()
	sess.Owner.tokens.Expires = time.Time{}
	_, e := sess.Owner.token(context.Background(), &Tessera{internal: server.URL, http: server.Client()})
	must(t, e)
	restored := newSessions()
	must(t, restored.enableRemember(dir, "https://synthetic.invalid"))
	if restored.byID[sess.ID].Owner.tokens.Refresh != "rotated-refresh" {
		t.Fatal("rotating refresh token was not durable")
	}
	restored.validate = func(context.Context, *session) bool { return false }
	req := httptest.NewRequest("GET", "https://synthetic.invalid/home", nil)
	req.AddCookie(&http.Cookie{Name: s.cookie, Value: sess.ID})
	if restored.get(httptest.NewRecorder(), req, false) != nil {
		t.Fatal("revoked credential restored access")
	}
	if _, e = os.Stat(filepath.Join(s.store.dir, rememberFile(sess.ID))); !os.IsNotExist(e) {
		t.Fatal("rejected login remained durable")
	}
}
func TestRememberNamespacesAndTamperResistance(t *testing.T) {
	s, sess, dir := rememberedFixture(t)
	if sessionCookieName("https://localhost:8644") == sessionCookieName("https://localhost:8744") {
		t.Fatal("local Consoles share a cookie")
	}
	other, e := openRememberStore(dir, "https://other.invalid")
	must(t, e)
	if _, e = other.readLocked(rememberFile(sess.ID)); e == nil {
		t.Fatal("credential accepted under a different origin")
	}
	raw, e := os.ReadFile(filepath.Join(s.store.dir, rememberFile(sess.ID)))
	must(t, e)
	raw[len(raw)-1] ^= 1
	must(t, os.WriteFile(filepath.Join(s.store.dir, rememberFile(sess.ID)), raw, 0600))
	if _, e = s.store.readLocked(rememberFile(sess.ID)); e == nil {
		t.Fatal("modified credential accepted")
	}
}
func TestRefreshPersistenceFailureNeverUsesUndurableToken(t *testing.T) {
	c := &connection{tokens: tokens{Refresh: "synthetic"}, persistRefresh: func(string) error { return errors.New("synthetic write failure") }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"access_token": "fresh", "refresh_token": "rotated", "expires_in": 300})
	}))
	defer server.Close()
	if tok, e := c.token(context.Background(), &Tessera{internal: server.URL, http: server.Client()}); e == nil || tok != "" {
		t.Fatal("used a token whose rotation could not be persisted")
	}
}

func TestRememberRestorationRechecksTesseraAuthority(t *testing.T) {
	for _, mode := range []string{"revoked", "other-account", "lost-owner"} {
		t.Run(mode, func(t *testing.T) {
			_, sess, dir := rememberedFixture(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/oauth/token" {
					writeJSON(w, 200, map[string]any{"access_token": "synthetic", "refresh_token": "rotated", "expires_in": 300})
					return
				}
				if mode == "revoked" {
					w.WriteHeader(401)
					return
				}
				account := "synthetic"
				if mode == "other-account" {
					account = "different"
				}
				writeJSON(w, 200, map[string]any{"account": account, "server_administrator": mode != "lost-owner"})
			}))
			defer server.Close()
			restored := newSessions()
			must(t, restored.enableRemember(dir, "https://synthetic.invalid"))
			a := &app{tessera: &Tessera{internal: server.URL, http: server.Client()}}
			restored.validate = a.validateRemembered
			r := httptest.NewRequest("GET", "https://synthetic.invalid/home", nil)
			r.AddCookie(&http.Cookie{Name: restored.cookieName(), Value: sess.ID})
			if restored.get(httptest.NewRecorder(), r, false) != nil {
				t.Fatal("restored authority bypassed live Tessera denial")
			}
		})
	}
}
func TestRememberLoginChoiceIsOptInAndCSRFProtected(t *testing.T) {
	a, s, server := policyHTTPApp(t)
	tpl, e := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix}).ParseFS(assets, "templates/layout.html", "templates/login.html")
	must(t, e)
	a.pages["login"] = tpl
	s.Pending = map[string]pending{}
	defer server.Close()
	w := savedRequest(a, s, "GET", "/login", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Keep me signed in") || strings.Contains(w.Body.String(), "checked") {
		t.Fatal("web sign-in did not offer unchecked opt-in")
	}
	w = savedRequest(a, s, "POST", "/login", url.Values{"remember": {"on"}})
	if w.Code != 403 {
		t.Fatal("remember sign-in accepted forged POST")
	}
	w = savedRequest(a, s, "POST", "/login", url.Values{"csrf": {s.CSRF}, "remember": {"on"}})
	if w.Code != 303 {
		t.Fatal("valid choice did not start OAuth")
	}
	u, e := url.Parse(w.Header().Get("Location"))
	must(t, e)
	if !s.Pending[u.Query().Get("state")].Remember || !s.RememberUntil.IsZero() {
		t.Fatal("opt-in lost or remembered before successful authentication")
	}
}
func TestExpiredRememberedLoginDoesNotSurviveRestart(t *testing.T) {
	s, sess, dir := rememberedFixture(t)
	sess.RememberUntil = time.Now().Add(-time.Second)
	s.mu.Lock()
	must(t, s.store.save(sess))
	s.mu.Unlock()
	restarted := newSessions()
	must(t, restarted.enableRemember(dir, "https://synthetic.invalid"))
	if len(restarted.byID) != 0 {
		t.Fatal("expired durable session restored")
	}
	if _, e := os.Stat(filepath.Join(s.store.dir, rememberFile(sess.ID))); !os.IsNotExist(e) {
		t.Fatal("expired credential retained")
	}
}

func TestConcurrentRefreshAndSessionSaveKeepNewestCredential(t *testing.T) {
	s, sess, _ := rememberedFixture(t)
	var n atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"access_token": "synthetic", "refresh_token": fmt.Sprint("synthetic-", n.Add(1)), "expires_in": 300})
	}))
	defer server.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 12; i++ {
			sess.Owner.mu.Lock()
			sess.Owner.tokens.Expires = time.Time{}
			sess.Owner.mu.Unlock()
			if _, e := sess.Owner.token(context.Background(), &Tessera{internal: server.URL, http: server.Client()}); e != nil {
				t.Error("concurrent refresh failed")
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 12; i++ {
			s.mu.Lock()
			e := s.store.save(sess)
			s.mu.Unlock()
			if e != nil {
				t.Error("concurrent save failed")
			}
		}
	}()
	wg.Wait()
	doc, e := s.store.readLocked(rememberFile(sess.ID))
	must(t, e)
	if doc.Owner.Refresh != sess.Owner.tokens.Refresh {
		t.Fatal("a stale session snapshot replaced the rotated refresh credential")
	}
}
