package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const rememberLifetime = 30 * 24 * time.Hour

type rememberedConnection struct {
	ClientID   string
	Website    bool
	Dataset    Dataset
	Administer bool
	Refresh    string
}
type rememberedSession struct {
	ID, Account, CSRF, Lang string
	Until                   time.Time
	Owner                   *rememberedConnection
	Website                 *rememberedConnection
	Connections             []rememberedConnection
}
type rememberStore struct {
	mu          sync.Mutex
	dir, origin string
	box         cipher.AEAD
}

func rememberFile(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:]) + ".session"
}
func sessionCookieName(origin string) string {
	sum := sha256.Sum256([]byte(origin))
	return "__Host-voptrack_console_" + hex.EncodeToString(sum[:8])
}
func openRememberStore(dir, origin string) (*rememberStore, error) {
	dir = filepath.Join(dir, ".browser-sessions")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, errors.New("cannot prepare browser sessions")
	}
	keypath := filepath.Join(dir, "key-v1")
	key, e := os.ReadFile(keypath)
	if os.IsNotExist(e) {
		key = make([]byte, 32)
		if _, e = rand.Read(key); e != nil {
			return nil, e
		}
		f, e := os.OpenFile(keypath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, errors.New("cannot create browser session key")
		}
		_, e = f.Write(key)
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil || closeErr != nil {
			return nil, errors.New("cannot save browser session key")
		}
	} else if e != nil {
		return nil, errors.New("cannot read browser session key")
	}
	if len(key) != 32 {
		return nil, errors.New("invalid browser session key")
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, errors.New("invalid browser session key")
	}
	box, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	return &rememberStore{dir: dir, origin: origin, box: box}, nil
}
func (s *rememberStore) readLocked(name string) (rememberedSession, error) {
	var doc rememberedSession
	f, e := os.Open(filepath.Join(s.dir, name))
	if e != nil {
		return doc, e
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, 1<<20))
	if e != nil || len(raw) < s.box.NonceSize() {
		return doc, errors.New("invalid browser session")
	}
	plain, e := s.box.Open(nil, raw[:s.box.NonceSize()], raw[s.box.NonceSize():], []byte(s.origin+"\x00"+name))
	if e != nil {
		return doc, errors.New("invalid browser session")
	}
	if json.Unmarshal(plain, &doc) != nil || rememberFile(doc.ID) != name || doc.Account == "" || doc.Until.IsZero() {
		return doc, errors.New("invalid browser session")
	}
	return doc, nil
}
func (s *rememberStore) writeLocked(doc rememberedSession) error {
	raw, e := json.Marshal(doc)
	if len(raw) > 1<<20 {
		return errors.New("browser session exceeds storage limit")
	}
	if e != nil {
		return e
	}
	nonce := make([]byte, s.box.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return e
	}
	name := rememberFile(doc.ID)
	data := s.box.Seal(nonce, nonce, raw, []byte(s.origin+"\x00"+name))
	f, e := os.CreateTemp(s.dir, ".write-")
	if e != nil {
		return errors.New("cannot save browser session")
	}
	temp := f.Name()
	defer os.Remove(temp)
	_, e = f.Write(data)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil {
		return errors.New("cannot save browser session")
	}
	if os.Rename(temp, filepath.Join(s.dir, name)) != nil {
		return errors.New("cannot save browser session")
	}
	d, e := os.Open(s.dir)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (s *rememberStore) remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := os.Remove(filepath.Join(s.dir, rememberFile(id)))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	d, e := os.Open(s.dir)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (s *rememberStore) update(id, slot, refresh string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, e := s.readLocked(rememberFile(id))
	if e != nil {
		return errors.New("remembered sign-in unavailable")
	}
	if !time.Now().Before(doc.Until) {
		return errors.New("remembered sign-in expired")
	}
	if slot == "website" && doc.Website != nil {
		doc.Website.Refresh = refresh
	} else if slot == "owner" && doc.Owner != nil {
		doc.Owner.Refresh = refresh
	} else {
		found := false
		for i := range doc.Connections {
			if doc.Connections[i].Dataset.ID == slot {
				doc.Connections[i].Refresh = refresh
				found = true
			}
		}
		if !found {
			return errors.New("remembered connection unavailable")
		}
	}
	return s.writeLocked(doc)
}
func (s *rememberStore) bind(id, slot string, c *connection) {
	c.persistRefresh = func(refresh string) error { return s.update(id, slot, refresh) }
}

// Caller holds sessions.mu. Lock connections before the store, so a concurrent
// refresh cannot be overwritten by an older snapshot. Refresh never takes sessions.mu.
func (s *rememberStore) save(sess *session) error {
	conns := credentialConnections(sess)
	for _, c := range conns {
		c.mu.Lock()
	}
	defer func() {
		for i := len(conns) - 1; i >= 0; i-- {
			conns[i].mu.Unlock()
		}
	}()
	return s.saveLocked(sess)
}
func (s *rememberStore) saveLocked(sess *session) error {
	doc := rememberedSession{ID: sess.ID, Account: sess.Account, CSRF: sess.CSRF, Lang: sess.Lang, Until: sess.RememberUntil}
	if sess.Owner != nil {
		doc.Owner = &rememberedConnection{Refresh: sess.Owner.tokens.Refresh, ClientID: sess.Owner.tokens.ClientID, Website: sess.Owner == sess.Website}
	}
	if sess.Website != nil {
		doc.Website = &rememberedConnection{Refresh: sess.Website.tokens.Refresh, ClientID: sess.Website.tokens.ClientID}
	}
	for _, c := range sess.Connections {
		saved := rememberedConnection{Dataset: c.Dataset, Administer: c.Administer, Refresh: c.tokens.Refresh, ClientID: c.tokens.ClientID, Website: c.authority == sess.Website && sess.Website != nil}
		doc.Connections = append(doc.Connections, saved)
	}
	for _, c := range doc.Connections {
		if !c.Website && c.Refresh == "" {
			return errors.New("remembered credential unavailable")
		}
	}
	if doc.Website != nil && (doc.Website.Refresh == "" || doc.Website.ClientID == "") {
		return errors.New("remembered website credential unavailable")
	}
	if doc.Owner != nil && !doc.Owner.Website && doc.Owner.Refresh == "" {
		return errors.New("remembered credential unavailable")
	}
	s.mu.Lock()
	e := s.writeLocked(doc)
	s.mu.Unlock()
	if e != nil {
		return e
	}
	if sess.Website != nil {
		s.bind(sess.ID, "website", sess.Website)
	}
	if sess.Owner != nil && sess.Owner != sess.Website {
		s.bind(sess.ID, "owner", sess.Owner)
	}
	for _, c := range sess.Connections {
		if c.authority == nil {
			s.bind(sess.ID, c.Dataset.ID, c)
		}
	}
	return nil
}
func (s *sessions) enableRemember(dir, origin string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	store, e := openRememberStore(dir, origin)
	if e != nil {
		return e
	}
	s.store = store
	s.cookie = sessionCookieName(origin)
	entries, e := os.ReadDir(store.dir)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".session") {
			continue
		}
		if len(s.byID) >= s.max {
			return errors.New("too many remembered sessions")
		}
		doc, e := store.readLocked(entry.Name())
		if e != nil {
			continue // A damaged or foreign-origin envelope never restores a sign-in.
		}
		if !time.Now().Before(doc.Until) {
			if e = store.remove(doc.ID); e != nil {
				return errors.New("cannot expire browser sessions")
			}
			continue
		}
		sess := &session{ID: doc.ID, Account: doc.Account, CSRF: doc.CSRF, Lang: doc.Lang, RememberUntil: doc.Until, LastSeen: time.Now(), Pending: map[string]pending{}, NeedsValidation: true}
		if doc.Website != nil {
			sess.Website = &connection{tokens: tokens{Refresh: doc.Website.Refresh, ClientID: doc.Website.ClientID}}
			store.bind(sess.ID, "website", sess.Website)
		}
		if doc.Owner != nil {
			sess.Owner = &connection{tokens: tokens{Refresh: doc.Owner.Refresh, ClientID: doc.Owner.ClientID}}
			if doc.Owner.Website {
				sess.Owner = sess.Website
			} else {
				store.bind(sess.ID, "owner", sess.Owner)
			}
		}
		for _, saved := range doc.Connections {
			c := &connection{Dataset: saved.Dataset, Administer: saved.Administer, tokens: tokens{Refresh: saved.Refresh, ClientID: saved.ClientID}}
			if saved.Website {
				c.authority = sess.Website
				if c.authority == nil {
					continue
				}
			}
			if c.authority == nil {
				store.bind(sess.ID, c.Dataset.ID, c)
			}
			sess.Connections = append(sess.Connections, c)
		}
		if sess.signedIn() {
			s.byID[sess.ID] = sess
		}
	}
	return nil
}
func (s *sessions) issueCookie(w http.ResponseWriter, sess *session) {
	c := &http.Cookie{Name: s.cookieName(), Value: sess.ID, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode}
	if !sess.RememberUntil.IsZero() {
		c.Expires = sess.RememberUntil
		c.MaxAge = int(time.Until(sess.RememberUntil).Seconds())
		if c.MaxAge < 1 {
			c.MaxAge = -1
		}
	}
	http.SetCookie(w, c)
}
func (s *sessions) complete(w http.ResponseWriter, sess *session, remember bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	conns := credentialConnections(sess)
	for _, c := range conns {
		c.mu.Lock()
	}
	defer func() {
		for i := len(conns) - 1; i >= 0; i-- {
			conns[i].mu.Unlock()
		}
	}()
	old := sess.ID
	if s.store != nil {
		if e := s.store.remove(old); e != nil {
			return errors.New("cannot update remembered sign-in")
		}
	}
	delete(s.byID, old)
	sess.ID = random(32)
	sess.CSRF = random(32)
	if remember {
		if sess.RememberUntil.IsZero() {
			sess.RememberUntil = time.Now().Add(rememberLifetime)
		}
	} else {
		sess.RememberUntil = time.Time{}
		for _, c := range conns {
			c.persistRefresh = nil
		}
	}
	s.byID[sess.ID] = sess
	if remember {
		if s.store == nil {
			return errors.New("remembered sign-in is not configured")
		}
		if e := s.store.saveLocked(sess); e != nil {
			delete(s.byID, sess.ID)
			return errors.New("cannot remember sign-in")
		}
	}
	s.issueCookie(w, sess)
	return nil
}
func rememberChoice(r *http.Request, sess *session) bool {
	if r.URL.Path == "/login" && r.Method == http.MethodPost {
		return r.PostFormValue("remember") == "on"
	}
	return !sess.RememberUntil.IsZero()
}
func (a *app) validateRemembered(ctx context.Context, sess *session) bool {
	all := credentialConnections(sess)
	if len(all) == 0 {
		return false
	}
	for _, c := range all {
		tok, e := c.token(ctx, a.tessera)
		if e != nil {
			return false
		}
		who, e := a.tessera.account(ctx, tok)
		if e != nil || who.Account != sess.Account || (c == sess.Owner && !who.ServerAdministrator) {
			return false
		}

		sess.PasswordSetBy = who.PasswordSetBy != nil
		if c != sess.Owner {
			c.Administer = who.Installation.Administer
		}
		if c == sess.Website {
			for _, projection := range sess.Connections {
				if projection.authority == c {
					projection.Administer = who.Installation.Administer
				}
			}
		}
	}
	return true
}
