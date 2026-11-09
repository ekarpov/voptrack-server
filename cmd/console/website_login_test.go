package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebsiteAuthorizationHasNoPermissionOrPersistenceParameters(t *testing.T) {
	tt := &Tessera{public: "https://identity.invalid", console: "https://home.invalid", clientID: "ordinary", websiteID: "operator-website"}
	u, e := url.Parse(tt.websiteAuthorizeURL("synthetic-state", "synthetic-verifier"))
	must(t, e)
	q := u.Query()
	for _, k := range []string{"scope", "dataset", "shared_reads", "remember", "prompt"} {
		if q.Has(k) {
			t.Fatalf("website requested %s", k)
		}
	}
	if q.Get("client_id") != "operator-website" || q.Get("redirect_uri") != "https://home.invalid/auth/callback" || q.Get("state") != "synthetic-state" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Fatal("website binding missing")
	}
	ordinary, _ := url.Parse(tt.authorizeURL("state", "verifier"))
	if ordinary.Query().Get("client_id") != "ordinary" || ordinary.Query().Get("prompt") != "consent" || ordinary.Query().Get("scope") != "read write" {
		t.Fatal("ordinary consent changed")
	}
}

func TestWebsitePersistenceOnlyFromValidatedCodeExchange(t *testing.T) {
	for _, tc := range []struct {
		name            string
		handoff         any
		valid, remember bool
	}{
		{"checked", consoleSession{"https://home.invalid", true, 2592000}, true, true},
		{"unchecked", consoleSession{"https://home.invalid", false, 0}, true, false},
		{"missing", nil, false, false},
		{"wrong-origin", consoleSession{"https://other.invalid", true, 2592000}, false, false},
		{"too-long", consoleSession{"https://home.invalid", true, 2592001}, false, false},
		{"unchecked-age", consoleSession{"https://home.invalid", false, 2592000}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				must(t, r.ParseForm())
				if r.Form.Get("client_id") != "operator-website" {
					t.Error("wrong client at exchange/revocation")
				}
				if r.URL.Path == "/oauth/revoke" {
					w.Write([]byte(`{}`))
					return
				}
				if r.Form.Get("code_verifier") != "synthetic-verifier" || r.Form.Get("redirect_uri") != "https://home.invalid/auth/callback" {
					t.Error("code not bound")
				}
				json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-access", "refresh_token": "synthetic-refresh", "expires_in": 300, "console_session": tc.handoff})
			}))
			defer srv.Close()
			tt := &Tessera{http: srv.Client(), internal: srv.URL, console: "https://home.invalid", websiteID: "operator-website"}
			tok, e := tt.redeemWebsite(context.Background(), "synthetic-code", "synthetic-verifier")
			if (e == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, e)
			}
			if tc.valid && (tok.ConsoleSession.KeepSignedIn != tc.remember || tok.ClientID != "operator-website") {
				t.Fatal("unbound persistence/client")
			}
		})
	}
}

func TestWebsiteRememberedDatasetsShareRefreshAfterRestart(t *testing.T) {
	for _, owner := range []bool{false, true} {
		t.Run(map[bool]string{false: "member", true: "owner"}[owner], func(t *testing.T) {
			s := newSessions()
			dir := t.TempDir()
			must(t, s.enableRemember(dir, "https://home.invalid"))
			sess := s.get(httptest.NewRecorder(), httptest.NewRequest("GET", "https://home.invalid/login", nil), true)
			sess.Account = "synthetic"
			sess.Website = &connection{tokens: tokens{Refresh: "synthetic-refresh", ClientID: "operator-website"}}
			if owner {
				sess.Owner = sess.Website
			}
			for _, id := range []string{"one", "two"} {
				sess.Connections = append(sess.Connections, &connection{Dataset: Dataset{ID: id}, authority: sess.Website})
			}
			must(t, s.complete(httptest.NewRecorder(), sess, true))
			deadline := sess.RememberUntil
			restored := newSessions()
			must(t, restored.enableRemember(dir, "https://home.invalid"))
			got := restored.byID[sess.ID]
			if got == nil || len(got.Connections) != 2 || len(credentialConnections(got)) != 1 || (owner && got.Owner != got.Website) {
				t.Fatal("lost shared website credential")
			}
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				must(t, r.ParseForm())
				if r.Form.Get("client_id") != "operator-website" || r.Form.Get("refresh_token") != "synthetic-refresh" {
					t.Error("wrong refresh identity")
				}
				calls.Add(1)
				w.Write([]byte(`{"access_token":"synthetic-next","refresh_token":"synthetic-rotated","expires_in":300}`))
			}))
			defer srv.Close()
			tt := &Tessera{http: srv.Client(), internal: srv.URL, clientID: "ordinary"}
			var wg sync.WaitGroup
			for _, c := range got.Connections {
				wg.Add(1)
				go func(c *connection) {
					defer wg.Done()
					access, e := c.token(context.Background(), tt)
					if e != nil || access != "synthetic-next" {
						t.Error("refresh failed")
					}
				}(c)
			}
			wg.Wait()
			if calls.Load() != 1 || !got.RememberUntil.Equal(deadline) {
				t.Fatal("refresh spent twice or extended persistence")
			}
			again := newSessions()
			must(t, again.enableRemember(dir, "https://home.invalid"))
			if again.byID[sess.ID].Website.tokens.Refresh != "synthetic-rotated" {
				t.Fatal("rotation not durable")
			}
			if !restored.drop(httptest.NewRecorder(), got) {
				t.Fatal("logout failed")
			}
			final := newSessions()
			must(t, final.enableRemember(dir, "https://home.invalid"))
			if final.byID[sess.ID] != nil {
				t.Fatal("logged out session restored")
			}
		})
	}
}

func TestWebsiteLoginSkipsIntroAndIgnoresPostedRemember(t *testing.T) {
	a := &app{sessions: newSessions(), tessera: &Tessera{public: "https://identity.invalid", console: "https://home.invalid", websiteID: "operator-website"}, studies: &studyStore{}}
	w := httptest.NewRecorder()
	a.login(w, httptest.NewRequest("GET", "https://home.invalid/login?remember=yes", nil))
	if w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), "https://identity.invalid/oauth/authorize?") {
		t.Fatal("login did not open credential form")
	}
	for _, sess := range a.sessions.byID {
		for _, p := range sess.Pending {
			if !p.Website || p.Remember || time.Until(p.Expires) > 11*time.Minute {
				t.Fatal("unbound pending flow")
			}
		}
	}
}

func TestWebsiteApprovalReplacesViewsAndClearsSensitiveIntents(t *testing.T) {
	for _, admin := range []bool{false, true} {
		t.Run(map[bool]string{false: "policy-without-administration", true: "policy-with-administration"}[admin], func(t *testing.T) {
			s := newSessions()
			must(t, s.enableRemember(t.TempDir(), "https://home.invalid"))
			sess := s.get(httptest.NewRecorder(), httptest.NewRequest("GET", "https://home.invalid/login", nil), true)
			sess.Kit = &kitIntent{}
			sess.Moderation = &moderationIntent{}
			sess.NativeSettings = &nativeIntent{}
			sess.BackupSettings = &backupSettingsIntent{}
			sess.Invited = []Invitation{{}}
			who := Account{Account: "synthetic", ServerAdministrator: true}
			who.Installation.Administer = admin
			a := &app{sessions: s, tessera: &Tessera{}, docs: &stateDocs{}}
			tok := tokens{Access: "synthetic-access", Refresh: "synthetic-refresh", Expires: time.Now().Add(time.Minute), ClientID: "operator-website", ConsoleSession: &consoleSession{Origin: "https://home.invalid", KeepSignedIn: true, MaxAgeSeconds: 2592000}}
			w := httptest.NewRecorder()
			a.websiteApproved(w, httptest.NewRequest("GET", "https://home.invalid/auth/callback", nil), sess, pending{}, tok, Listing{Datasets: []Dataset{{ID: "one"}, {ID: "two"}}}, who)
			if w.Code != 303 || len(sess.Connections) != 2 || len(credentialConnections(sess)) != 1 || (sess.Owner != nil) != admin || sess.Kit != nil || sess.Moderation != nil || sess.BackupSettings != nil || sess.NativeSettings != nil || len(sess.Invited) != 0 {
				t.Fatal("website sign-in retained confirmation state or lost policy/datasets")
			}
			if time.Until(sess.RememberUntil) < 29*24*time.Hour {
				t.Fatal("checked session was not remembered")
			}
		})
	}
}

func TestWebsiteCallbacksSerializePersistence(t *testing.T) {
	s := newSessions()
	must(t, s.enableRemember(t.TempDir(), "https://home.invalid"))
	sess := s.get(httptest.NewRecorder(), httptest.NewRequest("GET", "https://home.invalid/login", nil), true)
	originalID := sess.ID
	for _, state := range []string{"first", "second"} {
		s.addPending(sess, state, pending{Website: true, Verifier: "synthetic-verifier", Expires: time.Now().Add(time.Minute)})
	}
	firstStarted, secondStarted, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			must(t, r.ParseForm())
			code := r.Form.Get("code")
			if code == "first" {
				close(firstStarted)
				<-release
			} else {
				close(secondStarted)
			}
			keep := code == "first"
			age := 0
			if keep {
				age = 2592000
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-" + code, "refresh_token": "synthetic-refresh-" + code, "expires_in": 300, "console_session": consoleSession{"https://home.invalid", keep, age}})
		case "/v1/datasets":
			w.Write([]byte(`{"datasets":[]}`))
		case "/oauth/account":
			w.Write([]byte(`{"account":"synthetic"}`))
		case "/oauth/revoke":
			w.Write([]byte(`{}`))
		default:
			t.Error("unexpected identity request")
			http.Error(w, "unexpected", 500)
		}
	}))
	defer srv.Close()
	a := &app{sessions: s, tessera: &Tessera{http: srv.Client(), internal: srv.URL, console: "https://home.invalid", websiteID: "operator-website"}, docs: &stateDocs{}}
	var wg sync.WaitGroup
	responses := []*httptest.ResponseRecorder{httptest.NewRecorder(), httptest.NewRecorder()}
	call := func(i int, state string) {
		defer wg.Done()
		r := httptest.NewRequest("GET", "https://home.invalid/auth/callback?state="+state+"&code="+state, nil)
		r.AddCookie(&http.Cookie{Name: s.cookieName(), Value: originalID})
		a.callback(responses[i], r)
	}
	wg.Add(1)
	go call(0, "first")
	<-firstStarted
	wg.Add(1)
	go call(1, "second")
	select {
	case <-secondStarted:
		t.Error("parallel sign-ins overlapped identity/persistence changes")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	if responses[0].Code != 303 || responses[1].Code != 303 || !sess.RememberUntil.IsZero() {
		t.Fatal("last validated checkbox did not determine session policy")
	}
	if len(responses[1].Result().Cookies()) != 1 || responses[1].Result().Cookies()[0].MaxAge != 0 {
		t.Fatal("unchecked completion used another sign-in's persistence")
	}
	restarted := newSessions()
	must(t, restarted.enableRemember(s.store.dir+"/..", "https://home.invalid"))
	if len(restarted.byID) != 0 {
		t.Fatal("earlier checked completion survived later unchecked sign-in")
	}
}
