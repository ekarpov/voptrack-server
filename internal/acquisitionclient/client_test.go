package acquisitionclient

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(id string) Operation {
	data := []byte("encrypted synthetic artifact")
	a := Artifact{strings.Repeat("1", 32), "archive", int64(len(data)), fmt.Sprintf("%x", sha256.Sum256(data))}
	e := Artifact{strings.Repeat("2", 32), "evidence", 0, fmt.Sprintf("%x", sha256.Sum256(nil))}
	m := Artifact{strings.Repeat("3", 32), "metadata", 2, fmt.Sprintf("%x", sha256.Sum256([]byte("{}")))}
	return Operation{ID: id, Kind: "backup", State: "complete", Pair: &Pair{Version: 1, Deployment: "synthetic-deployment", S: "opaque-s", R: "opaque-r", Artifacts: []Artifact{a, e}}, Artifacts: []Artifact{a, e, m}}
}
func TestLostAcceptanceUsesDurableSameID(t *testing.T) {
	ctx := t.Context()
	now := time.Now()
	dir := filepath.Join(t.TempDir(), "journal")
	journal, err := OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := journal.Create("backup", now)
	if err != nil {
		t.Fatal(err)
	}
	intent.State = "unknown"
	if err = journal.Save(intent); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	accepted := 0
	first := true
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/api/v1/backup-operations/"+intent.ID || r.Header.Get("Authorization") != "Bearer synthetic-test-token" {
			t.Error("unexpected route or authority")
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "PUT" {
			var v struct{ Kind string }
			json.NewDecoder(r.Body).Decode(&v)
			if v.Kind != "backup" {
				w.WriteHeader(409)
				return
			}
			if first {
				first = false
				accepted++
				conn, _, e := w.(http.Hijacker).Hijack()
				if e != nil {
					t.Error(e)
					return
				}
				conn.Close()
				return
			}
			w.WriteHeader(202)
		}
		json.NewEncoder(w).Encode(fixture(intent.ID))
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Start(ctx, intent.ID, intent.Kind, "synthetic-test-token"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("lost response: %v", err)
	}
	journal.Close()
	journal, err = OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	resumed, err := journal.Load(intent.ID)
	if err != nil || resumed != intent {
		t.Fatal("intent did not survive restart", err)
	}
	op, err := client.Status(ctx, resumed.ID, resumed.Kind, "synthetic-test-token")
	if err != nil || op.State != "complete" {
		t.Fatal("reconciliation failed", err)
	}
	if _, err = client.Start(ctx, resumed.ID, resumed.Kind, "synthetic-test-token"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if accepted != 1 {
		t.Fatal("reconciliation created duplicate operation")
	}
	raw, err := os.ReadFile(filepath.Join(dir, intent.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("synthetic-test-token")) {
		t.Fatal("journal retained a credential")
	}
}
func TestRedirectNeverForwardsCredential(t *testing.T) {
	hits := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			hits++
			w.WriteHeader(500)
			return
		}
		// Same trusted origin: removing the redirect guard must reach this target,
		// rather than appearing safe merely because a second server's CA is untrusted.
		http.Redirect(w, r, "/redirected", 307)
	}))
	defer server.Close()
	c, _ := New(server.URL, server.Client())
	id, _ := NewID(time.Now())
	_, e := c.Start(t.Context(), id, "backup", "synthetic-test-token")
	if e == nil || hits != 0 {
		t.Fatal("redirect followed or accepted")
	}
	for _, origin := range []string{"http://example.test", "https://user:password@example.test", "https://example.test/?token=x", "https://example.test/path", "https://example.test/#token"} {
		if _, e = New(origin, server.Client()); e == nil {
			t.Fatal("accepted unsafe origin")
		}
	}
}

func TestDownloadRequiresExactBytesAndPropagatesSinkFailure(t *testing.T) {
	id, _ := NewID(time.Now())
	op := fixture(id)
	a := op.Artifacts[0]
	payload := []byte("encrypted synthetic artifact")
	var mode string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Tessera-Backup-Lease") != strings.Repeat("a", 32) {
			w.WriteHeader(403)
			return
		}
		if mode == "expired" {
			w.WriteHeader(409)
			io.WriteString(w, `{"error":{"code":"expired"}}`)
			return
		}
		if mode == "short" {
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			w.Write(payload[:5])
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		if mode == "changed" {
			w.Write(bytes.Repeat([]byte("x"), len(payload)))
			return
		}
		w.Write(payload)
	}))
	defer server.Close()
	c, _ := New(server.URL, server.Client())
	for _, m := range []string{"valid", "short", "changed", "expired", "full"} {
		t.Run(m, func(t *testing.T) {
			mode = m
			var out io.Writer = io.Discard
			if m == "full" {
				out = failingSink{}
			}
			err := c.Download(t.Context(), id, strings.Repeat("a", 32), "synthetic-test-token", a, out)
			if (m == "valid") != (err == nil) {
				t.Fatalf("download validity mismatch: %v", err)
			}
		})
	}
}

type failingSink struct{}

func (failingSink) Write([]byte) (int, error) { return 0, errors.New("synthetic disk full") }
func TestInvalidPairNeverAccepted(t *testing.T) {
	id, _ := NewID(time.Now())
	for _, mode := range []string{"missing-evidence", "duplicate-member", "wrong-pair", "foreign-id", "traversal", "oversized", "opaque-reverse-labels"} {
		op := fixture(id)
		switch mode {
		case "missing-evidence":
			op.Artifacts = op.Artifacts[:1]
		case "duplicate-member":
			op.Artifacts[1] = op.Artifacts[0]
		case "wrong-pair":
			op.Pair.Artifacts[0] = Artifact{}
		case "foreign-id":
			op.ID = "0-" + strings.Repeat("f", 32)
		case "traversal":
			op.Artifacts[0].ID = "../escape"
		case "oversized":
			op.Artifacts[0].Size = 1 << 40
		case "opaque-reverse-labels":
			op.Pair.S = "zzzz"
			op.Pair.R = "aaaa"
		}
		err := validate(op, id, "backup")
		if mode == "opaque-reverse-labels" {
			if err != nil {
				t.Fatal("opaque labels were ordered", err)
			}
		} else if err == nil {
			t.Fatal("accepted invalid pair", mode)
		}
	}
}
func TestRefusalNeverIncludesServerDetailsOrURL(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		io.WriteString(w, `{"code":"unexpected-sensitive-content","details":"synthetic-secret"}`)
	}))
	defer server.Close()
	c, _ := New(server.URL, server.Client())
	id, _ := NewID(time.Now())
	_, e := c.Status(t.Context(), id, "backup", "synthetic-test-token")
	if e == nil || e.Error() != "backup acquisition refused: request_failed" {
		t.Fatalf("unsafe refusal: %v", e)
	}
}
func TestJournalExclusionAndConfinement(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	j, e := OpenJournal(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	if second, e := OpenJournal(dir); !errors.Is(e, ErrBusy) {
		if second != nil {
			second.Close()
		}
		t.Fatal("journal excluded no competing writer")
	}
	if _, e = j.Load("../escape"); !errors.Is(e, ErrInvalid) {
		t.Fatal("unsafe lookup accepted")
	}
	v, e := j.Create("backup", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	v.State = "completed"
	if j.Save(v) == nil {
		t.Fatal("journal pretended to publish bundle")
	}
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte(`{}`), 0600)
	p := filepath.Join(dir, v.ID+".json")
	os.Remove(p)
	os.Symlink(outside, p)
	if _, e = j.Load(v.ID); e == nil {
		t.Fatal("followed a journal symlink")
	}
	public := filepath.Join(t.TempDir(), "public")
	os.Mkdir(public, 0755)
	if e := os.Chmod(public, 0755); e != nil {
		t.Fatal(e)
	}
	if opened, e := OpenJournal(public); e == nil {
		opened.Close()
		t.Fatal("accepted public intent directory")
	}
}

func TestExactLeaseMethodsAndCancellationCompletionRace(t *testing.T) {
	id, _ := NewID(time.Now())
	lease := strings.Repeat("a", 32)
	var methods []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		if strings.Contains(r.URL.Path, "/leases/") {
			expiry := time.Now().Add(5 * time.Minute)
			if r.Method == "DELETE" {
				expiry = time.Time{}
			}
			json.NewEncoder(w).Encode(map[string]any{"id": lease, "expires_at": expiry})
			return
		}
		// Cancellation may legitimately observe a completed producer operation.
		json.NewEncoder(w).Encode(fixture(id))
	}))
	defer server.Close()
	c, _ := New(server.URL, server.Client())
	for _, release := range []bool{false, false, true} {
		if _, e := c.Lease(t.Context(), id, lease, "synthetic-test-token", release); e != nil {
			t.Fatal(e)
		}
	}
	op, e := c.Cancel(t.Context(), id, "backup", "synthetic-test-token")
	if e != nil || op.State != "complete" {
		t.Fatal("completion race was lost", e)
	}
	suffix := " /admin/api/v1/backup-operations/" + id
	want := []string{"PUT" + suffix + "/leases/" + lease, "PUT" + suffix + "/leases/" + lease, "DELETE" + suffix + "/leases/" + lease, "DELETE" + suffix}
	if fmt.Sprint(methods) != fmt.Sprint(want) {
		t.Fatal("wrong supported HTTP methods")
	}
}
func TestJournalRefusesInvalidRecoveredIntentAndClosedWriter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	j, e := OpenJournal(dir)
	if e != nil {
		t.Fatal(e)
	}
	v, e := j.Create("evidence", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(dir, v.ID+".json")
	for _, raw := range []string{`{"id":"` + v.ID + `","kind":"evidence","state":"completed"}`, `{"id":"` + v.ID + `","kind":"unsafe","state":"queued"}`} {
		if os.WriteFile(file, []byte(raw), 0600) != nil {
			t.Fatal("fixture write")
		}
		if _, e = j.Load(v.ID); e == nil {
			t.Fatal("invalid recovered intent accepted")
		}
	}
	if e = j.Close(); e != nil {
		t.Fatal(e)
	}
	if e = j.Save(v); !errors.Is(e, ErrJournal) {
		t.Fatal("closed writer used without lock")
	}
}
