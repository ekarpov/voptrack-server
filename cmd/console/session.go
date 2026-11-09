package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

// Ordinary sessions stay in memory. Opt-in remembered sessions persist only
// encrypted credentials; tokens never reach browser cookies.

const cookieName = "voptrack_console"

type pending struct {
	Website            bool
	Remember           bool
	Verifier           string
	Expires            time.Time
	ReturnStudy        string // validated slug; navigation only, never a pending agreement
	ParticipantAccount string // approval must return as the same participant
	// Erase marks a sign-in started to remove a store's data, naming the
	// store. Its permission is used for that one action and then revoked; it
	// never becomes a connection.
	Erase string
}

// erasing is a person part-way through removing their data: they have
// approved erase and seen the preview, and not yet confirmed.
type erasing struct {
	Dataset Dataset
	Token   string
	Preview Preview
	Expires time.Time
}

type connection struct {
	authority *connection // dataset projections share one rotating website credential
	Dataset   Dataset
	// Administer: this sign-in was approved to administer on the person's
	// behalf -- which, for a member's own store, is what managing sharing
	// with Home needs. Approved on Tessera's consent screen, never assumed.
	Administer     bool
	persistRefresh func(string) error
	mu             sync.Mutex
	tokens         tokens
}

type session struct {
	authMu          *sync.Mutex // serialize callback identity/persistence changes and sign-out
	Website         *connection // dedicated account sign-in, including accounts without datasets
	RememberUntil   time.Time
	NeedsValidation bool
	validationMu    *sync.Mutex
	ID              string
	CSRF            string
	Lang            string
	LastSeen        time.Time
	Pending         map[string]pending // OAuth state -> PKCE verifier
	Connections     []*connection      // one per store the person has connected
	// Owner is the household owner's sign-in: server rights and no store.
	// Nil for everybody else.
	Owner   *connection
	Account string       // who signed in, from /oauth/account
	Invited []Invitation // issued in this session, so the owner can copy them again
	// PasswordSetBy: somebody else set this person's current password.
	PasswordSetBy  bool
	Kit            *kitIntent
	NativeSettings *nativeIntent
	BackupSettings *backupSettingsIntent
	Moderation     *moderationIntent // ephemeral, session-bound, consumed before execution
	Erasing        *erasing
}

func (sess *session) signedIn() bool {
	return len(sess.Connections) > 0 || sess.Owner != nil || sess.Website != nil
}

type sessions struct {
	cookie   string
	store    *rememberStore
	validate func(context.Context, *session) bool
	mu       sync.Mutex
	byID     map[string]*session
	idle     time.Duration
	// anonIdle is how long a session nobody signed in to lives: anyone may
	// make one by opening /login, so it must not last the signed-in twelve
	// hours.
	anonIdle time.Duration
	max      int // sessions held at once; beyond it, new ones are refused
}

const (
	maxSessions = 20000
	maxPending  = 4 // sign-ins begun and not finished, per session
)

func newSessions() *sessions {
	s := &sessions{cookie: cookieName, byID: map[string]*session{}, idle: 12 * time.Hour, anonIdle: 30 * time.Minute, max: maxSessions}
	go func() {
		for range time.Tick(time.Minute) {
			s.sweep(time.Now())
		}
	}()
	return s
}

func (s *sessions) expired(sess *session, now time.Time) bool {
	if !sess.RememberUntil.IsZero() {
		return !now.Before(sess.RememberUntil)
	}
	limit := s.idle
	if !sess.signedIn() {
		limit = s.anonIdle
	}
	return now.Sub(sess.LastSeen) > limit
}

// sweep drops expired sessions and sign-ins nobody finished. It runs in the
// background, so a request never pays for the whole map.
func (s *sessions) sweep(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.byID {
		if s.expired(sess, now) {
			if s.store != nil && s.store.remove(id) != nil {
				continue
			}
			delete(s.byID, id)
			continue
		}
		for state, p := range sess.Pending {
			if now.After(p.Expires) {
				delete(sess.Pending, state)
			}
		}
	}
}

// addPending records a sign-in begun in this session, keeping at most
// maxPending: the oldest unfinished one gives way.
func (s *sessions) addPending(sess *session, state string, p pending) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(sess.Pending) >= maxPending {
		oldest := ""
		for k, v := range sess.Pending {
			if oldest == "" || v.Expires.Before(sess.Pending[oldest].Expires) {
				oldest = k
			}
		}
		delete(sess.Pending, oldest)
	}
	sess.Pending[state] = p
}

func random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // no randomness is no security; do not continue
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// get returns the request's session, creating one when create is set. An
// expired or unknown cookie is treated as none. When the Console already holds
// as many sessions as it will, creating answers nil: the caller says the
// Console is busy, rather than letting anonymous requests grow it without end.
func (s *sessions) get(w http.ResponseWriter, r *http.Request, create bool) *session {
	s.mu.Lock()
	now := time.Now()
	if c, e := r.Cookie(s.cookieName()); e == nil {
		if sess, ok := s.byID[c.Value]; ok {
			if !s.expired(sess, now) {
				sess.LastSeen = now
				if sess.authMu == nil {
					sess.authMu = &sync.Mutex{}
				}
				if sess.validationMu == nil {
					sess.validationMu = &sync.Mutex{}
				}
				s.mu.Unlock()
				sess.validationMu.Lock()
				defer sess.validationMu.Unlock()
				if sess.NeedsValidation {
					if s.validate == nil || !s.validate(r.Context(), sess) {
						s.drop(w, sess)
						return nil
					}
					sess.NeedsValidation = false
				}
				return sess
			}
			if s.store != nil {
				s.store.remove(sess.ID)
			}
			delete(s.byID, c.Value)
		}
	}
	if !create || len(s.byID) >= s.max {
		s.mu.Unlock()
		return nil
	}
	sess := &session{authMu: &sync.Mutex{}, ID: random(32), CSRF: random(32), LastSeen: now, Pending: map[string]pending{}}
	s.byID[sess.ID] = sess
	s.mu.Unlock()
	s.issueCookie(w, sess)
	return sess
}
func (s *sessions) drop(w http.ResponseWriter, sess *session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store != nil {
		if e := s.store.remove(sess.ID); e != nil {
			return false
		}
	}
	delete(s.byID, sess.ID)
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	return true
}

func (sess *session) checkCSRF(r *http.Request) bool {
	got := r.PostFormValue("csrf")
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRF)) == 1
}

// token returns a usable access token for a connection, refreshing it shortly
// before it expires. Tessera's access tokens last minutes and its refresh
// tokens weeks, so a dashboard left open keeps working without a sign-in.
func (c *connection) token(ctx context.Context, t *Tessera) (string, error) {
	if c.authority != nil {
		return c.authority.token(ctx, t)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Until(c.tokens.Expires) > 30*time.Second {
		return c.tokens.Access, nil
	}
	fresh, err := t.refreshClient(ctx, c.tokens.Refresh, c.tokens.ClientID)
	if err != nil {
		return "", err
	}
	if fresh.Refresh == "" {
		fresh.Refresh = c.tokens.Refresh
	}
	if c.persistRefresh != nil {
		if e := c.persistRefresh(fresh.Refresh); e != nil {
			c.tokens = tokens{}
			return "", e
		}
	}
	c.tokens = fresh
	return c.tokens.Access, nil
}

func (s *sessions) cookieName() string {
	if s.cookie == "" {
		return cookieName
	}
	return s.cookie
}

// credentialConnections locks/persists each installation once even when several
// dataset projections (and the administrator view) use its rotating token.
func credentialConnections(sess *session) []*connection {
	all := append([]*connection(nil), sess.Connections...)
	all = append(all, sess.Owner, sess.Website)
	seen := map[*connection]bool{}
	var out []*connection
	for _, c := range all {
		if c == nil {
			continue
		}
		if c.authority != nil {
			c = c.authority
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}
