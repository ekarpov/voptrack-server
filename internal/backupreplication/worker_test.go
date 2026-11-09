package backupreplication

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	sc "github.com/ekarpov/voptrack-server/internal/schedulerclient"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	ac "github.com/ekarpov/voptrack-server/internal/acquisitionclient"
	bc "github.com/ekarpov/voptrack-server/internal/backupcollector"
)

type memoryObjects struct {
	mu     sync.Mutex
	data   map[string][]byte
	puts   []string
	fault  string
	commit func()
}

func (m *memoryObjects) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, ok := m.data[key]
	if !ok {
		return nil, ErrMissing
	}
	raw = bytes.Clone(raw)
	if m.fault == "read-corrupt" && strings.Contains(key, "/payloads/") {
		raw[0] ^= 1
	}
	return io.NopCloser(bytes.NewReader(raw)), nil
}
func (m *memoryObjects) Put(_ context.Context, key string, r io.ReadSeeker, n int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.data[key]; ok {
		return ErrExists
	}
	raw, e := io.ReadAll(r)
	if e != nil || int64(len(raw)) != n {
		return ErrUnavailable
	}
	m.data[key] = raw
	m.puts = append(m.puts, key)
	if strings.Contains(key, "/commits/") && m.commit != nil {
		m.commit()
	}
	if m.fault == "lost-response" {
		return ErrUnavailable
	}
	return nil
}
func (m *memoryObjects) List(_ context.Context, prefix string) (out []string, e error) {
	for key := range m.data {
		if strings.HasPrefix(key, prefix) {
			out = append(out, key)
		}
	}
	return
}
func fixtureWorker(t *testing.T) (*Worker, bc.Outbox, bc.Obligation, *memoryObjects) {
	t.Helper()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	queue := filepath.Join(root, "queue")
	state := filepath.Join(root, "state")
	source := filepath.Join(root, "published")
	for _, p := range []string{queue, state, source} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	deployment := strings.Repeat("a", 32)
	now := time.Now().UTC().Truncate(time.Second)
	op := ac.Operation{ID: fmt.Sprintf("%d-%032x", now.Unix(), 1), Kind: "backup", State: "complete", AcceptedAt: now, ExpiresAt: now.Add(time.Hour)}
	op.Pair = &ac.Pair{Version: 1, Deployment: deployment, S: "opaque-s", R: "opaque-r"}
	members := map[string][]byte{}
	add := func(kind string, raw []byte) {
		id := fmt.Sprintf("%032x", len(op.Artifacts)+1)
		h := sha256.Sum256(raw)
		a := ac.Artifact{ID: id, Kind: kind, Size: int64(len(raw)), SHA256: fmt.Sprintf("%x", h)}
		op.Artifacts = append(op.Artifacts, a)
		members[id] = raw
		if kind != "metadata" {
			op.Pair.Artifacts = append(op.Pair.Artifacts, a)
		}
	}
	add("archive", []byte("synthetic ciphertext"))
	add("evidence", nil)
	raw, _ := json.Marshal(op.Pair)
	meta, _ := json.Marshal(map[string]string{"payload": base64.StdEncoding.EncodeToString(raw), "mac": strings.Repeat("0", 64)})
	add("metadata", meta)
	manifest := bc.Manifest{Version: 1, Operation: op, Origin: "https://synthetic.example", Collected: now, Authentication: "https-transport; metadata-mac-required-before-restore"}
	raw, _ = json.MarshalIndent(manifest, "", "  ")
	members["manifest.json"] = raw
	f, e := os.Create(filepath.Join(source, "backup.tar"))
	if e != nil {
		t.Fatal(e)
	}
	tw := tar.NewWriter(f)
	for _, name := range append([]string{"manifest.json"}, op.Artifacts[0].ID, op.Artifacts[1].ID, op.Artifacts[2].ID) {
		b := members[name]
		if e = tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(b)), ModTime: now}); e != nil {
			t.Fatal(e)
		}
		tw.Write(b)
	}
	tw.Close()
	f.Close()
	q := bc.Outbox{Dir: queue}
	if e = q.Capture(source, manifest); e != nil {
		t.Fatal(e)
	}
	if e = q.LocalPublished(op.ID); e != nil {
		t.Fatal(e)
	}
	r, e := q.Record(op.ID)
	if e != nil {
		t.Fatal(e)
	}
	objects := &memoryObjects{data: map[string][]byte{}}
	c := Config{ExpectedBucketOwner: "000000000000", Version: 1, Label: "Synthetic destination", Region: "us-east-1", Bucket: "synthetic-backups", Prefix: "test/", Deployment: deployment, Outbox: queue, State: state, StatusFile: filepath.Join(root, "off-machine.json")}
	w, e := Open(c, objects)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(w.Close)
	return w, q, r, objects
}
func TestReadbackFailureCannotPublishOrRelease(t *testing.T) {
	w, q, r, m := fixtureWorker(t)
	m.fault = "read-corrupt"
	if w.Scan(t.Context()) == nil {
		t.Fatal("corrupt readback was accepted")
	}
	if !q.HasPayload(r.Manifest.Operation.ID) {
		t.Fatal("failed verification released obligation")
	}
	status, _ := bc.ReadReplicationStatus(w.Config.StatusFile, time.Now())
	if status.PendingBytes != r.Size {
		t.Fatal("pending payload bytes counted incorrectly")
	}
	for key := range m.data {
		if strings.Contains(key, "/commits/") {
			t.Fatal("completion published before readback")
		}
	}
	m.fault = ""
	w.Close()
	w, e := Open(w.Config, m)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	if e = w.Scan(t.Context()); e != nil {
		t.Fatal("intact replay failed", e)
	}
	if q.HasPayload(r.Manifest.Operation.ID) {
		t.Fatal("verified obligation not released")
	}
}
func TestLostResponsesReconcileExactImmutableBytes(t *testing.T) {
	w, q, r, m := fixtureWorker(t)
	m.fault = "lost-response"
	if e := w.Scan(t.Context()); e != nil {
		t.Fatal("successful writes with lost responses did not reconcile", e)
	}
	if q.HasPayload(r.Manifest.Operation.ID) {
		t.Fatal("verified payload not released")
	}
	n := len(m.puts)
	if e := w.Scan(t.Context()); e != nil || len(m.puts) != n {
		t.Fatal("verified scan duplicated writes")
	}
	if !strings.Contains(m.puts[len(m.puts)-1], "/commits/") {
		t.Fatal("descriptor was not published last")
	}
}
func TestExistingWrongBytesAreNeverOverwritten(t *testing.T) {
	w, q, r, m := fixtureWorker(t)
	key := w.Config.namespace() + "payloads/" + r.SHA256
	m.data[key] = []byte("conflicting immutable bytes")
	if w.Scan(t.Context()) == nil {
		t.Fatal("conflict reported verified")
	}
	if string(m.data[key]) != "conflicting immutable bytes" || !q.HasPayload(r.Manifest.Operation.ID) {
		t.Fatal("conflict overwritten or obligation lost")
	}
}
func TestAcknowledgementFailureAndRestartRetainPendingBytes(t *testing.T) {
	w, q, r, m := fixtureWorker(t)
	state := w.Config.State
	m.commit = func() {
		m.commit = nil
		// Actual remote commit succeeds, but the subsequent durable local ack fails.
		os.Rename(state, state+"-preserved")
		os.Mkdir(state, 0700)
		os.Mkdir(filepath.Join(state, bc.OpaqueID(r.Manifest.Operation.ID)+".json"), 0700)
	}
	if w.Scan(t.Context()) == nil || !q.HasPayload(r.Manifest.Operation.ID) {
		t.Fatal("uncertain acknowledgement lost queue bytes")
	}
	w.Close()
	os.Rename(state, state+"-failed")
	os.Rename(state+"-preserved", state)
	restarted, e := Open(w.Config, m)
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Close()
	if e = restarted.Scan(t.Context()); e != nil || q.HasPayload(r.Manifest.Operation.ID) {
		t.Fatal("restart failed exact reconciliation", e)
	}
}
func TestRevalidationFailureAndDestinationBinding(t *testing.T) {
	w, q, r, m := fixtureWorker(t)
	if e := w.Scan(t.Context()); e != nil {
		t.Fatal(e)
	}
	if q.HasPayload(r.Manifest.Operation.ID) {
		t.Fatal("payload not released")
	}
	now := time.Now().Add(25 * time.Hour)
	w.Now = func() time.Time { return now }
	m.fault = "read-corrupt"
	if w.Scan(t.Context()) == nil {
		t.Fatal("stale corrupted remote copy accepted")
	}
	s, _ := bc.ReadReplicationStatus(w.Config.StatusFile, now)
	if s.State != "unavailable" {
		t.Fatal("failure erased or hid historical state", s.State)
	}
	w.Close()
	changed := w.Config
	changed.Prefix = "another/"
	if other, e := Open(changed, m); e == nil {
		other.Close()
		t.Fatal("destination silently rebound durable acknowledgements")
	}
}
func TestUnsafeDescriptorsAndExistingFetchTargetsRefused(t *testing.T) {
	w, _, r, m := fixtureWorker(t)
	if e := w.Scan(t.Context()); e != nil {
		t.Fatal(e)
	}
	id := bc.OpaqueID(r.Manifest.Operation.ID)
	d, e := w.descriptor(t.Context(), id)
	if e != nil {
		t.Fatal(e)
	}
	for _, partial := range []bool{false, true} {
		target := filepath.Join(filepath.Dir(w.Config.State), fmt.Sprintf("existing-%t", partial))
		os.Mkdir(target, 0700)
		if partial {
			os.WriteFile(filepath.Join(target, "preserve"), []byte("partial staging"), 0600)
		}
		if Fetch(t.Context(), w.Config, m, id, id, target, "", "") == nil {
			t.Fatal("existing fetch target accepted")
		}
		entries, _ := os.ReadDir(target)
		want := 0
		if partial {
			want = 1
			raw, _ := os.ReadFile(filepath.Join(target, "preserve"))
			if string(raw) != "partial staging" {
				t.Fatal("partial staging modified")
			}
		}
		if len(entries) != want {
			t.Fatal("existing staging changed")
		}
	}
	d.Payload.Key = "../escape"
	raw, _ := json.Marshal(d)
	m.data[w.Config.key(id)] = raw
	target := filepath.Join(t.TempDir(), "fetch")
	if Fetch(t.Context(), w.Config, m, id, id, target, "", "") == nil {
		t.Fatal("unconfined descriptor accepted")
	}
	if _, e = os.Lstat(target); !os.IsNotExist(e) {
		t.Fatal("unsafe descriptor touched staging")
	}
}

func TestPublicSuccessCannotRebindToFreshWorkerDestination(t *testing.T) {
	w, _, _, m := fixtureWorker(t)
	if e := w.Scan(t.Context()); e != nil {
		t.Fatal(e)
	}
	c := w.Config
	w.Close()
	c.State = filepath.Join(t.TempDir(), "state")
	c.Outbox = filepath.Join(t.TempDir(), "queue")
	os.Mkdir(c.State, 0700)
	os.Mkdir(c.Outbox, 0700)
	c.Prefix = "different/"
	c.Label = "Different synthetic destination"
	next, e := Open(c, m)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	if e = next.Scan(t.Context()); e != nil {
		t.Fatal(e)
	}
	s, _ := bc.ReadReplicationStatus(c.StatusFile, time.Now())
	if s.State != "unprotected" || !s.LastBackup.IsZero() || !s.LastVerified.IsZero() {
		t.Fatal("public success was inherited by a different destination")
	}
}

func TestOutboxWorkerLifetimeExclusionAndFutureCapture(t *testing.T) {
	w, q, r, m := fixtureWorker(t)
	c := w.Config
	c.State = filepath.Join(t.TempDir(), "other-state")
	os.Mkdir(c.State, 0700)
	if other, e := Open(c, m); e == nil {
		other.Close()
		t.Fatal("different state bypassed outbox worker exclusion")
	}
	// Future capture time must not create a fresh remote success.
	r.Queued = w.Now().Add(time.Hour)
	raw, _ := json.Marshal(r)
	os.WriteFile(filepath.Join(q.Dir, r.Manifest.Operation.ID, "obligation.json"), raw, 0600)
	if w.Scan(t.Context()) == nil || !q.HasPayload(r.Manifest.Operation.ID) {
		t.Fatal("future capture was admitted or pending bytes lost")
	}
	if len(m.puts) != 0 {
		t.Fatal("future capture mutated remote objects")
	}
}

// A missing binding is not an empty state. Acknowledgements must never be
// rebound merely because that one file was lost or replaced with a symlink.
func TestMissingOrIndirectBindingCannotReuseAcknowledgements(t *testing.T) {
	for _, mode := range []string{"missing", "symlink", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			w, q, r, m := fixtureWorker(t)
			payload, e := q.Payload(r)
			if e != nil {
				t.Fatal(e)
			}
			raw, e := io.ReadAll(payload)
			payload.Close()
			if e != nil {
				t.Fatal(e)
			}
			if e = w.Scan(t.Context()); e != nil {
				t.Fatal(e)
			}
			w.Close()
			binding := filepath.Join(w.Config.State, "binding.json")
			if e = os.Rename(binding, binding+"-preserved"); e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "symlink":
				e = os.Symlink(binding+"-preserved", binding)
			case "oversized":
				e = os.WriteFile(binding, bytes.Repeat([]byte("x"), (2<<20)+1), 0600)
			}
			if e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(q.Dir, r.Manifest.Operation.ID, "backup.tar")
			if e = os.WriteFile(path, raw, 0600); e != nil {
				t.Fatal(e)
			}
			c := w.Config
			if mode == "missing" {
				c.ExpectedBucketOwner = "111111111111"
				c.Bucket = "another-bucket"
			}
			if other, e := Open(c, m); e == nil {
				other.Close()
				t.Fatal("missing or indirect binding admitted existing acknowledgements")
			}
			after, e := os.ReadFile(path)
			if e != nil || !bytes.Equal(after, raw) {
				t.Fatal("binding refusal modified pending bytes")
			}
		})
	}
}

func TestWorkerRefusesSpecialLifetimeLock(t *testing.T) {
	w, _, _, m := fixtureWorker(t)
	w.Close()
	path := filepath.Join(w.Config.State, "worker.lock")
	if e := os.Rename(path, path+"-preserved"); e != nil {
		t.Fatal(e)
	}
	if e := syscall.Mkfifo(path, 0600); e != nil {
		t.Fatal(e)
	}
	if other, e := Open(w.Config, m); e == nil {
		other.Close()
		t.Fatal("non-regular lifetime lock accepted")
	}
}
func TestFrozenSettingsRefuseIndirectOrOversizedSnapshot(t *testing.T) {
	for _, mode := range []string{"symlink", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			w, q, r, m := fixtureWorker(t)
			export := sc.Export{Version: 1, Deployment: w.Config.Deployment, Revision: w.Config.Deployment + "." + strings.Repeat("b", 32) + "." + strings.Repeat("c", 32), EveryHours: 1, Keep: 7, ExportedAt: time.Now().UTC()}
			raw, _ := json.Marshal(export)
			if !export.Valid() {
				t.Fatal("invalid synthetic export fixture")
			}
			w.Config.SettingsFile = filepath.Join(filepath.Dir(w.Config.State), "approved-export.json")
			if e := os.WriteFile(w.Config.SettingsFile, raw, 0600); e != nil {
				t.Fatal(e)
			}
			j, e := w.job(r)
			if e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(w.Config.State, j.Descriptor.ID+".settings")
			if e = os.Rename(path, path+"-preserved"); e != nil {
				t.Fatal(e)
			}
			if mode == "symlink" {
				e = os.Symlink(filepath.Base(path)+"-preserved", path)
			} else {
				e = os.WriteFile(path, bytes.Repeat([]byte("x"), 4097), 0600)
			}
			if e != nil {
				t.Fatal(e)
			}
			if w.Scan(t.Context()) == nil || !q.HasPayload(r.Manifest.Operation.ID) {
				t.Fatal("indirect/oversized settings admitted or pending bytes lost")
			}
			for k := range m.data {
				if strings.Contains(k, "/commits/") {
					t.Fatal("unsafe settings published completion")
				}
			}
		})
	}
}

func TestStatusOutputCannotReplacePrivateInputsOrJournal(t *testing.T) {
	for _, mode := range []string{"credential", "credential-alias", "settings", "journal", "outbox"} {
		t.Run(mode, func(t *testing.T) {
			w, q, _, m := fixtureWorker(t)
			w.Close()
			c := w.Config
			original := []byte("preserved private input")
			switch mode {
			case "credential":
				c.CredentialFile = c.StatusFile
			case "settings":
				c.SettingsFile = c.StatusFile
			case "journal":
				c.StatusFile = filepath.Join(c.State, "off-machine.json")
			case "outbox":
				c.StatusFile = filepath.Join(q.Dir, "off-machine.json")
			}
			if e := os.WriteFile(c.StatusFile, original, 0600); e != nil {
				t.Fatal(e)
			}
			if mode == "credential-alias" {
				c.CredentialFile = filepath.Join(filepath.Dir(c.StatusFile), "credential-alias.json")
				if e := os.Link(c.StatusFile, c.CredentialFile); e != nil {
					t.Fatal(e)
				}
			}
			if other, e := Open(c, m); e == nil {
				other.Close()
				t.Fatal("status output overlaps private input or durable journal")
			}
			after, e := os.ReadFile(c.StatusFile)
			if e != nil || !bytes.Equal(after, original) {
				t.Fatal("private input modified on output refusal")
			}
		})
	}
}
