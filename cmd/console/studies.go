package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Studies, for Voptrack Research. A study is what a person reads before they
// join: what it is for, what it collects, who sees it, how to leave, and the
// consent text they agree to.
//
// Consent is versioned and a published version never changes. Editing the text
// publishes a new version; the old one stays, because a record of somebody's
// consent must point at the exact words they agreed to, not at whatever the
// text says today.
//
// Studies, their consent records, withdrawals and join codes are Console
// documents (state.go): kept in the Console's own Tessera store, so the
// server's backups carry them. Issuer secrets are files beside them, never
// documents -- a backup should not carry a credential, and a lost one is
// replaced by rotating it.

type ConsentVersion struct {
	Editorial bool      `json:"editorial,omitempty"`
	Version   int       `json:"version"`
	Text      string    `json:"text"`
	Published time.Time `json:"published"`
}

type Study struct {
	// Setup is private Console draft state, never part of the public study API.
	Setup      *studySetup      `json:"setup,omitempty"`
	RunBy      string           `json:"run_by"`
	Slug       string           `json:"slug"`
	Title      string           `json:"title"`
	Summary    string           `json:"summary"`
	Collected  string           `json:"collected"`
	WhoSees    string           `json:"who_sees"`
	HowToLeave string           `json:"how_to_leave"`
	JoinMode   string           `json:"join_mode"` // "invite" or "open"
	Status     string           `json:"status"`    // "draft", "open", "closed"
	Consent    []ConsentVersion `json:"consent"`
	Updated    time.Time        `json:"updated"`
	// ClosedAt is when the researcher last closed it; reopening clears it.
	// Studies closed before it was recorded have none.
	ClosedAt *time.Time `json:"closed_at,omitempty"`
	// StudyID is the study's part of its tracks' type identifiers, derived
	// from the address once, when the study is created, and never again.
	StudyID string `json:"study_id,omitempty"`
	// Tracks as the researcher designed them. Fixed once the study has
	// opened: its issuer installs exactly their types in every participant's
	// store, and cannot be changed afterwards.
	Tracks []TrackDesign `json:"track_designs,omitempty"`
	// IssuerID is set when the study first opens. Its secret is kept in a
	// separate file, never in this one.
	IssuerID string `json:"issuer,omitempty"`
	Cap      int    `json:"cap,omitempty"`
}

// Current is the consent version a person joining now agrees to.
func (s Study) Current() (ConsentVersion, bool) {
	if len(s.Consent) == 0 {
		return ConsentVersion{}, false
	}
	return s.Consent[len(s.Consent)-1], true
}

var validSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

const maxStudyText = 20000

type studyStore struct {
	operation sync.Mutex // serializes definition changes and external first-opening operations
	mu        sync.Mutex
	docs      docStore
	dir       string // issuer secrets only: they are files, never documents
}

// path names a study's document.
func (s *studyStore) path(slug string) (string, error) {
	if !validSlug.MatchString(slug) {
		return "", errors.New("a study's address is 2 to 63 lowercase letters, digits and hyphens")
	}
	return "studies/" + slug + ".json", nil
}

func (s *studyStore) read(name string) ([]byte, error) {
	raw, ok, err := s.docs.get(name)
	if err == nil && !ok {
		err = os.ErrNotExist
	}
	return raw, err
}

func (s *studyStore) get(slug string) (Study, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(slug)
	if err != nil {
		return Study{}, false
	}
	raw, err := s.read(path)
	if err != nil {
		return Study{}, false
	}
	var st Study
	if json.Unmarshal(raw, &st) != nil {
		return Study{}, false
	}
	return st, true
}

func (s *studyStore) list() []Study {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, _ := s.docs.list("studies/")
	var out []Study
	for _, f := range files {
		if strings.Count(strings.TrimPrefix(f, "studies/"), ".") > 1 {
			continue // a study's consents or codes, not a study
		}
		raw, err := s.read(f)
		if err != nil {
			continue
		}
		var st Study
		if json.Unmarshal(raw, &st) == nil {
			out = append(out, st)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out
}

// StudyEdit is what the researcher submits. ConsentText, when it differs from
// the current version, publishes a new version; it never rewrites one.
type StudyEdit struct {
	Draft                                                                                *studySetup // HTML draft editor: persist private consent atomically, without publication
	Title, Summary, Collected, WhoSees, HowToLeave, JoinMode, Status, ConsentText, RunBy string
	Editorial                                                                            bool
}

func clean(s string) string { return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n")) }

func (s *studyStore) save(slug string, e StudyEdit, create bool) (Study, error) {
	s.operation.Lock()
	defer s.operation.Unlock()
	return s.saveInOperation(slug, e, create)
}

func (s *studyStore) saveInOperation(slug string, e StudyEdit, create bool) (Study, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(slug)
	if err != nil {
		return Study{}, err
	}
	var st Study
	raw, err := s.read(path)
	switch {
	case err == nil && create:
		return Study{}, errors.New("a study already has that address")
	case err == nil:
		if json.Unmarshal(raw, &st) != nil {
			return Study{}, errors.New("that study's file is unreadable")
		}
	case create && errors.Is(err, os.ErrNotExist):
		st = Study{Slug: slug, Status: "draft", StudyID: studyID(slug)}
	default:
		if !errors.Is(err, os.ErrNotExist) {
			return Study{}, errors.New("that study's file is unreadable")
		}
		return Study{}, errors.New("no such study")
	}
	if st.Setup != nil && st.Setup.Opening {
		if st.IssuerID == "" || e.Status != "open" {
			return Study{}, errors.New("setup_recovery")
		}
		st.Setup.Opening = false
	}
	for _, f := range []*string{&e.Title, &e.Summary, &e.Collected, &e.WhoSees, &e.HowToLeave, &e.ConsentText} {
		*f = clean(*f)
		if len(*f) > maxStudyText {
			return Study{}, errors.New("a field is longer than 20000 characters")
		}
	}
	e.RunBy = clean(e.RunBy)
	if utf8.RuneCountInString(e.RunBy) > 120 {
		return Study{}, errors.New("run by is longer than 120 characters")
	}
	st.RunBy = e.RunBy
	if e.Title == "" {
		return Study{}, errors.New("a study needs a title")
	}
	st.Title, st.Summary, st.Collected, st.WhoSees, st.HowToLeave = e.Title, e.Summary, e.Collected, e.WhoSees, e.HowToLeave
	if e.JoinMode == "open" {
		st.JoinMode = "open"
	} else {
		st.JoinMode = "invite"
	}
	if e.Draft != nil {
		draft := *e.Draft
		draft.ConsentText = clean(draft.ConsentText)
		if len(draft.ConsentText) > maxStudyText {
			return Study{}, errors.New("a field is longer than 20000 characters")
		}
		st.Setup = &draft
	}
	if e.ConsentText != "" {
		if cur, ok := st.Current(); !ok || cur.Text != e.ConsentText {
			st.Consent = append(st.Consent, ConsentVersion{Editorial: len(st.Consent) > 0 && e.Editorial, Version: len(st.Consent) + 1, Text: e.ConsentText, Published: time.Now().UTC()})
		}
	}
	switch e.Status {
	case "open":
		// A study cannot open for joining without everything a person must
		// read first -- above all, consent text to agree to -- nor without
		// an issuer, which the Console creates when it opens.
		if err := st.readyToOpen(); err != nil {
			return Study{}, err
		}
		if st.IssuerID == "" {
			return Study{}, errors.New("the study has no issuer yet")
		}
		st.Status, st.ClosedAt = "open", nil
	case "closed":
		if st.Status != "closed" {
			now := time.Now().UTC()
			st.ClosedAt = &now
		}
		st.Status = "closed"
	case "draft":
		if st.IssuerID != "" {
			return Study{}, errors.New("a study that has opened can be closed, not returned to draft")
		}
		st.Status = "draft"
	}
	st.Updated = time.Now().UTC()
	out, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return Study{}, err
	}
	return st, s.docs.put(path, out)
}

func (st Study) readyToOpen() error {
	if _, ok := st.Current(); !ok || st.Summary == "" || st.Collected == "" || st.WhoSees == "" || st.HowToLeave == "" {
		return errors.New("a study opens only with a summary, what it collects, who sees it, how to leave, and consent text")
	}
	if _, err := studyTypes(st.Tracks); err != nil {
		return err
	}
	return nil
}

// setIssuer records the study's issuer once it is created. The secret goes in
// its own 0600 file beside the study, never in the study's definition.
func (s *studyStore) setIssuer(slug, id, secret string, cap int) error {
	path, err := s.path(slug)
	if err != nil {
		return err
	}
	if err := s.writeSecret(slug, secret); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := s.read(path)
	if err != nil {
		return err
	}
	var st Study
	if err := json.Unmarshal(raw, &st); err != nil {
		return err
	}
	st.IssuerID, st.Cap = id, cap
	out, _ := json.MarshalIndent(st, "", "  ")
	return s.docs.put(path, out)
}

func (s *studyStore) secretPath(slug string) (string, error) {
	if !validSlug.MatchString(slug) {
		return "", errors.New("no such study")
	}
	return filepath.Join(s.dir, slug+".issuer-secret"), nil
}

func (s *studyStore) writeSecret(slug, secret string) error {
	path, err := s.secretPath(slug)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return recoverymaterial.Write(path, []byte(secret+"\n"))
}

func (s *studyStore) issuerSecret(slug string) (string, error) {
	path, err := s.secretPath(slug)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	return strings.TrimSpace(string(raw)), err
}

// Consent records: who agreed to which version, and when. Participants are
// pseudonyms the study's issuer generated; nothing else about them is kept.
type ConsentRecord struct {
	Account string    `json:"account"`
	Version int       `json:"version"`
	At      time.Time `json:"at"`
}

func (s *studyStore) sidecar(slug, kind string) (string, error) {
	path, err := s.path(slug)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(path, ".json") + "." + kind + ".json", nil
}

func (s *studyStore) readList(path string, into any) {
	raw, err := s.read(path)
	if err == nil {
		_ = json.Unmarshal(raw, into)
	}
}

func (s *studyStore) writeList(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return s.docs.put(path, raw)
}

// consentLog is where consent records are immutable, when there is one.
func (s *studyStore) consentLog() *tesseraDocs {
	if d, ok := s.docs.(*stateDocs); ok {
		return d.consentLog()
	}
	return nil
}

func (s *studyStore) recordConsent(slug string, c ConsentRecord) error {
	path, err := s.sidecar(slug, "consents")
	if err != nil {
		return err
	}
	if log := s.consentLog(); log != nil {
		return log.addConsent(slug, c)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []ConsentRecord
	s.readList(path, &list)
	return s.writeList(path, append(list, c))
}

func (s *studyStore) consents(slug string) []ConsentRecord {
	path, err := s.sidecar(slug, "consents")
	if err != nil {
		return nil
	}
	s.mu.Lock()
	var list []ConsentRecord // written as a list before consent records were their own
	s.readList(path, &list)
	s.mu.Unlock()
	if log := s.consentLog(); log != nil {
		list = append(list, log.consentsFor(slug)...)
	}
	return list
}

// Invitation codes for invite-only studies: the researcher makes a link, the
// Console's join page accepts it once. They are the Console's own codes, not
// Tessera invitations: a Tessera invitation is issued only after the person
// has read the study and consented.
func (s *studyStore) newCode(slug string) (string, error) {
	path, err := s.sidecar(slug, "codes")
	if err != nil {
		return "", err
	}
	code := random(18)
	s.mu.Lock()
	defer s.mu.Unlock()
	var codes []string
	s.readList(path, &codes)
	return code, s.writeList(path, append(codes, code))
}

// spendCode saves the spent marker before removing an active
// code. A failed removal cannot make it usable again, even after a restart.
func (s *studyStore) spendCode(slug, code string) bool {
	path, err := s.sidecar(slug, "codes")
	spentPath, _ := s.sidecar(slug, "codes-spent")
	if err != nil || code == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if used, err := s.codeIn(spentPath, code); err != nil || used {
		return false
	}
	var codes, spent []string
	s.readList(path, &codes)
	s.readList(spentPath, &spent)
	for i, c := range codes {
		if subtleEqual(c, code) {
			if s.writeList(spentPath, append(spent, code)) != nil {
				return false
			}
			return s.writeList(path, append(codes[:i], codes[i+1:]...)) == nil
		}
	}
	return false
}

// codeIn is called with the study lock held.
func (s *studyStore) codeIn(path, code string) (bool, error) {
	raw, err := s.read(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var codes []string
	if err := json.Unmarshal(raw, &codes); err != nil {
		return false, err
	}
	for _, c := range codes {
		if subtleEqual(c, code) {
			return true, nil
		}
	}
	return false, nil
}

func (s *studyStore) codeUsed(slug, code string) bool {
	path, err := s.sidecar(slug, "codes-spent")
	if err != nil || code == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	used, err := s.codeIn(path, code)
	return err == nil && used
}

func (s *studyStore) hasCode(slug, code string) bool {
	path, err := s.sidecar(slug, "codes")
	spentPath, _ := s.sidecar(slug, "codes-spent")
	if err != nil || code == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	used, err := s.codeIn(spentPath, code)
	if err != nil || used {
		return false
	}
	active, err := s.codeIn(path, code)
	return err == nil && active
}

func subtleEqual(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// Withdrawals: a participant who removed their data through the Console, and
// when. The study keeps their pseudonym and consent record -- what they agreed
// to stays true -- and nothing else about them. In the Console's own store each
// is an immutable record, like a consent (state.go).
type Withdrawal struct {
	Account string    `json:"account"`
	At      time.Time `json:"at"`
}

func (s *studyStore) recordWithdrawal(slug string, w Withdrawal) error {
	path, err := s.sidecar(slug, "withdrawals")
	if err != nil {
		return err
	}
	if log := s.consentLog(); log != nil {
		return log.addWithdrawal(slug, w)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []Withdrawal
	s.readList(path, &list)
	return s.writeList(path, append(list, w))
}

func (s *studyStore) withdrawalHistory(slug string) []Withdrawal {
	path, err := s.sidecar(slug, "withdrawals")
	if err != nil {
		return nil
	}
	s.mu.Lock()
	var list []Withdrawal // written as a list before withdrawals were their own records
	s.readList(path, &list)
	s.mu.Unlock()
	if log := s.consentLog(); log != nil {
		list = append(list, log.withdrawalsFor(slug)...)
	}
	return list
}

// withdrawFromStudies records, in every study the account consented to, that
// they removed their data. Called after an erase has succeeded.
func (s *studyStore) withdrawFromStudies(account string) {
	if account == "" {
		return
	}
	now := time.Now().UTC()
	for _, st := range s.list() {
		for _, c := range s.consents(st.Slug) {
			if c.Account == account {
				_ = s.recordWithdrawal(st.Slug, Withdrawal{Account: account, At: now})
				break
			}
		}
	}
}

// ID is the study's part of its type identifiers.
func (st Study) ID() string {
	if st.StudyID != "" {
		return st.StudyID
	}
	return studyID(st.Slug)
}

// saveTracks replaces a draft study's tracks. Once the study has opened they
// are fixed: every participant's store was made with them.
func (s *studyStore) saveTracks(slug string, tracks []TrackDesign, revision ...string) (Study, error) {
	s.operation.Lock()
	defer s.operation.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(slug)
	if err != nil {
		return Study{}, err
	}
	raw, err := s.read(path)
	if err != nil {
		return Study{}, errors.New("no such study")
	}
	var st Study
	if err := json.Unmarshal(raw, &st); err != nil {
		return Study{}, errors.New("that study's file is unreadable")
	}
	if st.IssuerID != "" || (st.Setup != nil && st.Setup.Opening) {
		return Study{}, errors.New("a study's tracks are fixed once it has opened: every participant's store was made with them")
	}
	// HTTP callers must supply the revision they displayed; trusted internal
	// callers may omit it. Compare while both operation and document locks are held.
	if len(revision) > 0 && (revision[0] == "" || revision[0] != setupRevision(st)) {
		return st, errors.New("setup_stale")
	}
	st.Tracks, st.Updated = tracks, time.Now().UTC()
	out, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return Study{}, err
	}
	return st, s.docs.put(path, out)
}

// LatestMaterial is the agreement required by the current consent text.
func (st Study) LatestMaterial() int {
	n := 0
	for _, v := range st.Consent {
		if !v.Editorial && v.Version > n {
			n = v.Version
		}
	}
	return n
}

type agreementHistory struct {
	consents    []ConsentRecord
	withdrawals []Withdrawal
}

func (h agreementHistory) latest(at time.Time) (ConsentRecord, time.Time) {
	var c ConsentRecord
	var w time.Time
	for _, v := range h.consents {
		if !v.At.After(at) && (c.At.IsZero() || !v.At.Before(c.At)) {
			c = v
		}
	}
	for _, v := range h.withdrawals {
		if !v.At.After(at) && v.At.After(w) {
			w = v.At
		}
	}
	return c, w
}

func (h agreementHistory) missing(st Study, at time.Time) bool {
	if at.IsZero() {
		return true
	}
	c, w := h.latest(at)
	required := 0
	for _, v := range st.Consent {
		if !v.Editorial && !v.Published.After(at) && v.Version > required {
			required = v.Version
		}
	}
	return c.At.IsZero() || w.After(c.At) || c.Version < required
}

func (s *studyStore) histories(slug string) map[string]agreementHistory {
	out := map[string]agreementHistory{}
	for _, c := range s.consents(slug) {
		h := out[c.Account]
		h.consents = append(h.consents, c)
		out[c.Account] = h
	}
	for _, w := range s.withdrawalHistory(slug) {
		h := out[w.Account]
		h.withdrawals = append(h.withdrawals, w)
		out[w.Account] = h
	}
	return out
}

// withdrawals reports only current withdrawals; all past records remain intact.
func (s *studyStore) withdrawals(slug string) map[string]time.Time {
	out := map[string]time.Time{}
	for account, h := range s.histories(slug) {
		c, w := h.latest(time.Now().UTC())
		if w.After(c.At) {
			out[account] = w
		}
	}
	return out
}
