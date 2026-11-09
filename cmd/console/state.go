package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// What the Console itself keeps -- studies, consent records, withdrawals, join
// codes, saved dashboards, which stores share with Home -- lives in a Tessera
// store of its own, so the server's backups carry it and a restore brings it
// back. It is held by a service, console-state, that init provisions with a
// store and nothing else; the Console reaches it with that service's secret,
// the way Home and the study-analysis reader reach theirs.
//
// Each document is one record: a name ("studies/sleep.json") and its JSON.
// The Console is the only writer, so an index of every document is read once
// and kept in memory, and every write goes to Tessera before the index changes.
//
// init makes that store with the service (-new-dataset, Tessera alpha.17)
// and writes its identifier to a file beside the service's secret; nobody
// else holds it, the owner included. A deployment without it -- the standard
// variant, or one made before -- keeps documents as files in the state
// directory, as they always were; when the store appears, any files are
// copied into it and the store is used from then on.

type docStore interface {
	get(name string) ([]byte, bool, error)
	put(name string, raw []byte) error
	list(prefix string) ([]string, error)
}

// fileDocs keeps documents as files under a directory.
type fileDocs struct{ dir string }

func (f fileDocs) path(name string) string { return filepath.Join(f.dir, filepath.FromSlash(name)) }

func (f fileDocs) get(name string) ([]byte, bool, error) {
	raw, err := os.ReadFile(f.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return raw, err == nil, err
}

func (f fileDocs) put(name string, raw []byte) error {
	path := f.path(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (f fileDocs) list(prefix string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(f.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		rel, _ := filepath.Rel(f.dir, path)
		if name := filepath.ToSlash(rel); strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	sort.Strings(out)
	return out, err
}

// The documents the Console keeps, and nothing else in the state directory:
// the OAuth registration and the issuer secrets stay files. A secret is not
// something a backup should carry, and a lost one is replaced by rotating it.
var docPrefixes = []string{"research-policies/", "studies/", "dashboards/", "home-sources.json"}

const docType = "voptrack.console.document"

type docEntry struct {
	id  string
	raw []byte
}

// consentType holds consent records, one per agreement. Every field is
// immutable (Tessera alpha.20): once a person's agreement is written, nobody
// -- the Console, or anybody holding its credentials -- can change who agreed,
// to which version, or when. Only erasing the store removes one.
const consentType = "voptrack.console.consent"

// withdrawalType holds withdrawals the same way: who left a study, and when,
// each its own record, every field immutable.
const withdrawalType = "voptrack.console.withdrawal"

// tesseraDocs keeps documents as records in the console-state store.
type tesseraDocs struct {
	t       *Tessera
	tokens  *serviceTokens
	dataset string

	mu          sync.Mutex
	index       map[string]docEntry
	consents    map[string][]ConsentRecord // by study
	withdrawals map[string][]Withdrawal    // by study
}

func (d *tesseraDocs) token(ctx context.Context) (string, error) {
	return d.tokens.token(ctx, d.t, d.dataset)
}

// define installs a type unless the store has it already.
func (d *tesseraDocs) define(ctx context.Context, token string, def map[string]any) error {
	raw, _ := json.Marshal(map[string]any{"type": def})
	err := d.t.call(ctx, "POST", "/v1/datasets/"+url.PathEscape(d.dataset)+"/meta", token, "application/json", bytes.NewReader(raw), 201, nil)
	var api *apiError
	if err != nil && (!errors.As(err, &api) || api.Status != 409) {
		return fmt.Errorf("defining %s: %w", def["id"], err)
	}
	return nil
}

// each reads every record of a type, page by page, handing over its id and
// its values by field.
func (d *tesseraDocs) each(ctx context.Context, token, typeID string, fields []string, fn func(id string, v map[string]json.RawMessage)) error {
	base := "/v1/datasets/" + url.PathEscape(d.dataset)
	var begun struct {
		Cursor string `json:"cursor"`
	}
	if err := d.t.post(ctx, base+"/objects/"+typeID+":page", token,
		map[string]any{"query": map[string]any{"fields": fields}, "page_size": 500}, &begun); err != nil {
		return err
	}
	for cursor := begun.Cursor; cursor != ""; {
		var page struct {
			Meta struct {
				Fields []struct {
					ID string `json:"id"`
				} `json:"fields"`
			} `json:"meta"`
			Records []struct {
				ID struct {
					ID string `json:"id"`
				} `json:"id"`
				Values []struct {
					Value json.RawMessage `json:"value"`
				} `json:"values"`
			} `json:"records"`
			NextCursor *string `json:"next_cursor"`
		}
		if err := d.t.call(ctx, "POST", base+"/pages/"+url.PathEscape(cursor)+":next", token, "", nil, 200, &page); err != nil {
			return err
		}
		for _, r := range page.Records {
			v := map[string]json.RawMessage{}
			for i, f := range page.Meta.Fields {
				if i < len(r.Values) {
					v[f.ID] = r.Values[i].Value
				}
			}
			fn(r.ID.ID, v)
		}
		cursor = ""
		if page.NextCursor != nil {
			cursor = *page.NextCursor
		}
	}
	return nil
}

// load defines the Console's types if the store lacks them and reads every
// document and consent record into memory.
func (d *tesseraDocs) load(ctx context.Context) error {
	token, err := d.token(ctx)
	if err != nil {
		return err
	}
	field := func(meta, id, name, typ string, immutable bool) map[string]any {
		f := map[string]any{"id": id, "meta_id": meta, "name": name, "type": typ}
		if immutable {
			f["immutable"] = true
		}
		return f
	}
	if err := d.define(ctx, token, map[string]any{"id": docType, "name": "Voptrack Console document", "fields": []map[string]any{
		field(docType, "name", "Name", "text", false), field(docType, "body", "Body", "json", false)}}); err != nil {
		return err
	}
	if err := d.define(ctx, token, map[string]any{"id": consentType, "name": "Consent", "fields": []map[string]any{
		field(consentType, "study", "Study", "text", true), field(consentType, "account", "Pseudonym", "text", true),
		field(consentType, "version", "Consent version", "integer", true), field(consentType, "at", "Agreed", "date_time", true)}}); err != nil {
		return err
	}
	if err := d.define(ctx, token, map[string]any{"id": withdrawalType, "name": "Withdrawal", "fields": []map[string]any{
		field(withdrawalType, "study", "Study", "text", true), field(withdrawalType, "account", "Pseudonym", "text", true),
		field(withdrawalType, "at", "Withdrew", "date_time", true)}}); err != nil {
		return err
	}
	index := map[string]docEntry{}
	err = d.each(ctx, token, docType, []string{"name", "body"}, func(id string, v map[string]json.RawMessage) {
		var name, body string
		_ = json.Unmarshal(v["name"], &name)
		_ = json.Unmarshal(v["body"], &body)
		if name != "" {
			index[name] = docEntry{id: id, raw: []byte(body)}
		}
	})
	if err != nil {
		return err
	}
	consents := map[string][]ConsentRecord{}
	err = d.each(ctx, token, consentType, []string{"study", "account", "version", "at"}, func(id string, v map[string]json.RawMessage) {
		var study, account, at string
		var version json.Number
		_ = json.Unmarshal(v["study"], &study)
		_ = json.Unmarshal(v["account"], &account)
		_ = json.Unmarshal(v["version"], &version)
		_ = json.Unmarshal(v["at"], &at)
		n, _ := version.Int64()
		when, _ := time.Parse(time.RFC3339Nano, at)
		consents[study] = append(consents[study], ConsentRecord{Account: account, Version: int(n), At: when})
	})
	if err != nil {
		return err
	}
	for _, list := range consents {
		sort.SliceStable(list, func(i, j int) bool { return list[i].At.Before(list[j].At) })
	}
	withdrawals := map[string][]Withdrawal{}
	err = d.each(ctx, token, withdrawalType, []string{"study", "account", "at"}, func(id string, v map[string]json.RawMessage) {
		var study, account, at string
		_ = json.Unmarshal(v["study"], &study)
		_ = json.Unmarshal(v["account"], &account)
		_ = json.Unmarshal(v["at"], &at)
		when, _ := time.Parse(time.RFC3339Nano, at)
		withdrawals[study] = append(withdrawals[study], Withdrawal{Account: account, At: when})
	})
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.index, d.consents, d.withdrawals = index, consents, withdrawals
	d.mu.Unlock()
	return nil
}

// addConsent writes one agreement as its own immutable record.
func (d *tesseraDocs) addConsent(study string, c ConsentRecord) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	token, err := d.token(ctx)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"fields": map[string]any{
		"study":   map[string]any{"type": "text", "value": study},
		"account": map[string]any{"type": "text", "value": c.Account},
		"version": map[string]any{"type": "integer", "value": c.Version},
		"at":      map[string]any{"type": "date_time", "value": c.At.UTC().Format("2006-01-02T15:04:05.000Z")},
	}})
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.t.call(ctx, "POST", "/v1/datasets/"+url.PathEscape(d.dataset)+"/objects/"+consentType, token,
		"application/json", bytes.NewReader(body), 201, nil); err != nil {
		return fmt.Errorf("recording a consent: %w", err)
	}
	c.At = c.At.UTC().Truncate(time.Millisecond) // as stored
	d.consents[study] = append(d.consents[study], c)
	return nil
}

// addWithdrawal writes one withdrawal as its own immutable record.
func (d *tesseraDocs) addWithdrawal(study string, w Withdrawal) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	token, err := d.token(ctx)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"fields": map[string]any{
		"study":   map[string]any{"type": "text", "value": study},
		"account": map[string]any{"type": "text", "value": w.Account},
		"at":      map[string]any{"type": "date_time", "value": w.At.UTC().Format("2006-01-02T15:04:05.000Z")},
	}})
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.t.call(ctx, "POST", "/v1/datasets/"+url.PathEscape(d.dataset)+"/objects/"+withdrawalType, token,
		"application/json", bytes.NewReader(body), 201, nil); err != nil {
		return fmt.Errorf("recording a withdrawal: %w", err)
	}
	w.At = w.At.UTC().Truncate(time.Millisecond) // as stored
	d.withdrawals[study] = append(d.withdrawals[study], w)
	return nil
}

func (d *tesseraDocs) withdrawalsFor(study string) []Withdrawal {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Withdrawal(nil), d.withdrawals[study]...)
}

func (d *tesseraDocs) consentsFor(study string) []ConsentRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]ConsentRecord(nil), d.consents[study]...)
}

func (d *tesseraDocs) get(name string) ([]byte, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.index[name]
	return e.raw, ok, nil
}

func (d *tesseraDocs) list(prefix string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for name := range d.index {
		if strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (d *tesseraDocs) put(name string, raw []byte) error {
	if !json.Valid(raw) {
		return errors.New("a Console document must be JSON")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	token, err := d.token(ctx)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	base := "/v1/datasets/" + url.PathEscape(d.dataset) + "/objects/" + docType
	fields := map[string]any{"body": map[string]any{"type": "json", "value": string(raw)}}
	if e, ok := d.index[name]; ok {
		body, _ := json.Marshal(map[string]any{"fields": fields})
		if err := d.t.call(ctx, "PATCH", base+"/"+url.PathEscape(e.id), token, "application/json", bytes.NewReader(body), 200, nil); err != nil {
			return fmt.Errorf("saving a Console document: %w", err) // never its name: a dashboard's names its owner
		}
		d.index[name] = docEntry{id: e.id, raw: raw}
		return nil
	}
	fields["name"] = map[string]any{"type": "text", "value": name}
	body, _ := json.Marshal(map[string]any{"fields": fields})
	// A write answers the objects it made, identified within this store.
	var created struct {
		Objects []struct {
			ID string `json:"id"`
		} `json:"objects"`
	}
	if err := d.t.call(ctx, "POST", base, token, "application/json", bytes.NewReader(body), 201, &created); err != nil {
		return fmt.Errorf("saving a Console document: %w", err) // never its name: a dashboard's names its owner
	}
	if len(created.Objects) != 1 || created.Objects[0].ID == "" {
		return errors.New("saving a Console document: the server did not say which record it made")
	}
	d.index[name] = docEntry{id: created.Objects[0].ID, raw: raw}
	return nil
}

// stateDocs is the Console's document store: files until the console-state
// store is known, then that store.
type stateDocs struct {
	files fileDocs
	t     *Tessera
	// service and secretFile are the console-state service; empty on a
	// deployment that has none, which keeps files for good.
	service, secretFile string
	datasetFile         string // the store's identifier, written by init

	mu     sync.Mutex
	remote *tesseraDocs
}

func (s *stateDocs) current() docStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remote != nil {
		return s.remote
	}
	return s.files
}

// consentLog is the Console's store when it is in use, for consent records;
// nil while documents are still files.
func (s *stateDocs) consentLog() *tesseraDocs {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remote
}

func (s *stateDocs) get(name string) ([]byte, bool, error) { return s.current().get(name) }
func (s *stateDocs) put(name string, raw []byte) error     { return s.current().put(name, raw) }
func (s *stateDocs) list(prefix string) ([]string, error)  { return s.current().list(prefix) }

// dataset is the console-state store's identifier, if init made one.
func (s *stateDocs) dataset() string {
	if s.datasetFile == "" {
		return ""
	}
	raw, err := os.ReadFile(s.datasetFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// ensure uses the store if it is known and not yet in use: at start, and
// again at the owner's sign-in if the server was not answering at start.
func (s *stateDocs) ensure(ctx context.Context) {
	dataset := s.dataset()
	if dataset == "" {
		return
	}
	if err := s.use(ctx, dataset); err != nil {
		log.Printf("console: reading the Console's documents from the server: %v", err)
	}
}

// use switches to the console-state store: reads it, copies in any document
// only the files hold, and from then on keeps documents there. Safe to call
// again; a failure leaves files in use and is retried on the next call.
func (s *stateDocs) use(ctx context.Context, dataset string) error {
	if s.service == "" || dataset == "" {
		return nil
	}
	s.mu.Lock()
	if s.remote != nil && s.remote.dataset == dataset {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	remote := &tesseraDocs{t: s.t, dataset: dataset, tokens: newServiceTokens(s.service, s.secretFile)}
	if err := remote.load(ctx); err != nil {
		return err
	}
	for _, prefix := range docPrefixes {
		names, _ := s.files.list(prefix)
		for _, name := range names {
			if _, held, _ := remote.get(name); held {
				continue // the store is authoritative once it holds a document
			}
			raw, ok, err := s.files.get(name)
			if err != nil || !ok {
				continue
			}
			if err := remote.put(name, raw); err != nil {
				return err
			}
		}
	}
	s.mu.Lock()
	s.remote = remote
	s.mu.Unlock()
	log.Printf("console: keeping the Console's documents in the server")
	return nil
}
