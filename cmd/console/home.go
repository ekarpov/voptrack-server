package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Home is a Tessera service that holds no store. Each member makes it a reader
// of the tracks they choose in their own store -- from the Sharing page, in one
// request, so it never reads anything they did not choose. Home's dashboards
// read through it, one store at a time, with a token fetched by the service's
// own secret for that store.
//
// Which stores to ask is kept here: the Console records a store when its member
// shares something with Home and forgets it when they share nothing. A store
// listed here that Tessera no longer lets Home read -- its member stopped
// sharing some other way, or erased it -- simply answers nothing, and the panel
// names it as not answering rather than counting it as empty.

type homeRegistry struct {
	mu   sync.Mutex
	docs docStore
}

const homeSources = "home-sources.json"

func (h *homeRegistry) load() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.read()
}

func (h *homeRegistry) read() []string {
	ids, _ := h.readChecked()
	return ids
}

// Call with h.mu held. Writers must not turn an unreadable registry into a
// replacement that silently loses other stores.
func (h *homeRegistry) readChecked() ([]string, error) {
	raw, found, err := h.docs.get(homeSources)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	var ids []string
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			return nil, errors.New("invalid Home registry")
		}
		seen[id] = true
	}
	return ids, nil
}

func (h *homeRegistry) set(dataset string, sharing bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ids, err := h.readChecked()
	if err != nil {
		return err
	}
	kept := ids[:0]
	for _, id := range ids {
		if id != dataset {
			kept = append(kept, id)
		}
	}
	if sharing {
		kept = append(kept, dataset)
	}
	sort.Strings(kept)
	raw, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	return h.docs.put(homeSources, raw)
}

// serviceTokens caches a service's per-store tokens until shortly before they
// expire. Home and the study-analysis reader each have one.
//
// Each store has its own lock, held while its token is fetched, so requests
// for the same store wait for one fetch rather than making several -- and a
// slow fetch for one store never delays a cached token for another. The
// cache-wide lock only guards the map.
type serviceTokens struct {
	mu         sync.Mutex
	byID       map[string]*tokenSlot
	service    string
	secretFile string
	secret     string
}

type tokenSlot struct {
	mu  sync.Mutex
	tok tokens
}

func newServiceTokens(service, secretFile string) *serviceTokens {
	return &serviceTokens{byID: map[string]*tokenSlot{}, service: service, secretFile: secretFile}
}

func (c *serviceTokens) slot(dataset string) *tokenSlot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.byID[dataset]
	if !ok {
		s = &tokenSlot{}
		c.byID[dataset] = s
	}
	return s
}

func (c *serviceTokens) readSecret() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.secret == "" {
		raw, err := os.ReadFile(c.secretFile)
		if err != nil {
			return "", err
		}
		c.secret = strings.TrimSpace(string(raw))
	}
	return c.secret, nil
}

func (c *serviceTokens) token(ctx context.Context, t *Tessera, dataset string) (string, error) {
	s := c.slot(dataset)
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Until(s.tok.Expires) > 30*time.Second {
		return s.tok.Access, nil
	}
	secret, err := c.readSecret()
	if err != nil {
		return "", err
	}
	tok, err := t.serviceToken(ctx, c.service, secret, dataset)
	if err != nil {
		s.tok = tokens{}
		return "", err
	}
	s.tok = tok
	return tok.Access, nil
}

func (c *serviceTokens) forget(dataset string) {
	s := c.slot(dataset)
	s.mu.Lock()
	s.tok = tokens{}
	s.mu.Unlock()
}

func (a *app) homeToken(ctx context.Context, dataset string) (string, error) {
	return a.homeCache.token(ctx, a.tessera, dataset)
}

// forgetHomeToken drops Home's cached token for a store. Unsharing everything
// removes Home as a reader, which revokes its tokens there -- so a token cached
// before that is dead, even after the member shares again. (Changing which
// tracks are shared does not revoke: the same token sees the new choice at
// once. Dropping the cache on every save is simply cheaper than telling the
// two apart.)
func (a *app) forgetHomeToken(dataset string) { a.homeCache.forget(dataset) }

// homeSources are the stores Home reads, each named as its member named it.
func (a *app) homeSources(ctx context.Context) []Source {
	if a.cfg.HomeService == "" {
		return nil
	}
	var out []Source
	for _, id := range a.homeReg.load() {
		id := id
		name := "Household member"
		if token, err := a.homeToken(ctx, id); err == nil {
			if listed, err := a.tessera.datasets(ctx, token); err == nil && len(listed) == 1 {
				if listed[0].DisplayName != "" {
					name = listed[0].DisplayName
				}
			}
		}
		out = append(out, Source{Dataset: id, Name: name, Home: true,
			Token:  func(ctx context.Context) (string, error) { return a.homeToken(ctx, id) },
			Forget: func() { a.forgetHomeToken(id) }})
	}
	return out
}
