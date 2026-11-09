package backupcollector

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	ac "github.com/ekarpov/voptrack-server/internal/acquisitionclient"
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

type fixture struct {
	mu                    sync.Mutex
	ops                   []ac.Operation
	data                  map[string][]byte
	fail, corrupt, denied bool
	gets, leases          int
}

func newFixture(n int) *fixture {
	f := &fixture{data: map[string][]byte{}}
	for i := 0; i < n; i++ {
		kind := "backup"
		if i%2 == 1 {
			kind = "evidence"
		}
		op := ac.Operation{ID: fmt.Sprintf("%d-%032x", time.Now().Unix(), i+1), Kind: kind, State: "complete", AcceptedAt: time.Now().Add(time.Duration(i) * time.Second).UTC(), ExpiresAt: time.Now().Add(time.Hour).UTC()}
		pair := ac.Pair{Version: 1, Deployment: "trusted-test-deployment", R: "opaque-r"}
		if kind == "backup" {
			pair.S = "opaque-s"
		}
		add := func(kind string, data []byte) ac.Artifact {
			id := fmt.Sprintf("%032x", i*3+len(op.Artifacts)+1)
			a := ac.Artifact{ID: id, Kind: kind, Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
			f.data[id] = data
			op.Artifacts = append(op.Artifacts, a)
			return a
		}
		if kind == "backup" {
			pair.Artifacts = append(pair.Artifacts, add("archive", []byte("synthetic encrypted test bytes")))
		}
		pair.Artifacts = append(pair.Artifacts, add("evidence", []byte{}))
		op.Pair = &pair
		raw, _ := json.Marshal(pair)
		meta, _ := json.Marshal(map[string]string{"payload": base64.StdEncoding.EncodeToString(raw), "mac": strings.Repeat("a", 64)})
		add("metadata", meta)
		f.ops = append(f.ops, op)
	}
	return f
}
func (f *fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer retrieval-test" || f.denied {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":{"code":"authentication_failed"}}`)
		return
	}
	if r.URL.Path == "/admin/api/v1/backup-operations" {
		start := 0
		if r.URL.Query().Get("cursor") != "" {
			start = 50
		}
		end := min(start+50, len(f.ops))
		next := ""
		if end < len(f.ops) {
			next = "second-page"
		}
		json.NewEncoder(w).Encode(ac.Page{Results: f.ops[start:end], Next: next})
		return
	}
	if strings.Contains(r.URL.Path, "/leases/") {
		f.leases++
		if f.fail {
			w.WriteHeader(410)
			fmt.Fprint(w, `{"error":{"code":"expired"}}`)
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		json.NewEncoder(w).Encode(map[string]any{"id": parts[len(parts)-1], "expires_at": time.Now().Add(5 * time.Minute)})
		return
	}
	if strings.Contains(r.URL.Path, "/artifacts/") {
		if r.Header.Get("Tessera-Backup-Lease") == "" {
			w.WriteHeader(403)
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		raw := append([]byte(nil), f.data[parts[len(parts)-1]]...)
		if f.corrupt && len(raw) > 0 {
			raw[0] ^= 1
		}
		f.gets++
		w.Header().Set("Content-Length", fmt.Sprint(len(raw)))
		w.Write(raw)
		return
	}
	// The collector must never attempt operation mutation or general API reads.
	w.WriteHeader(403)
}
func collectorFor(t *testing.T, f *fixture) Collector {
	t.Helper()
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	client, e := ac.New(srv.URL, srv.Client())
	if e != nil {
		t.Fatal(e)
	}
	return Collector{Client: client, Store: Store{Dir: filepath.Join(t.TempDir(), "bundles")}, Token: "retrieval-test", Deployment: "trusted-test-deployment", Origin: srv.URL, Keep: 2}
}
func TestCatalogRescanRestartAndIndependentEvidence(t *testing.T) {
	f := newFixture(52)
	c := collectorFor(t, f)
	if e := c.Scan(t.Context()); e != nil {
		t.Fatal(e)
	}
	all, e := c.Store.List()
	if e != nil || len(all) != 4 {
		t.Fatal("bounded publication", len(all), e)
	}
	gets := f.gets
	// New process/client state; durable bundles deduplicate a full paginated scan.
	c2 := c
	if e = c2.Scan(t.Context()); e != nil {
		t.Fatal(e)
	}
	if f.gets != gets {
		t.Fatal("duplicate artifact retrieval")
	}
	for _, b := range all {
		if b.Authentication != "https-transport; metadata-mac-required-before-restore" {
			t.Fatal("false MAC claim")
		}
		file, e := c.Store.Pin(b.Operation.ID)
		if e != nil {
			t.Fatal(e)
		}
		tr := tar.NewReader(file)
		members := 0
		for {
			_, e = tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			members++
		}
		file.Close()
		if members != len(b.Operation.Artifacts)+1 {
			t.Fatal("incomplete bundle")
		}
	}
}
func TestFailurePreservesPreviousBundleAndCleansCrashStage(t *testing.T) {
	for _, mode := range []string{"corrupt", "expired", "revoked", "provenance", "publication"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(1)
			c := collectorFor(t, f)
			if e := c.Scan(t.Context()); e != nil {
				t.Fatal(e)
			}
			prior := f.ops[0].ID
			newer := newFixture(3)
			f.ops = append(f.ops, newer.ops[2])
			for k, v := range newer.data {
				f.data[k] = v
			}
			crash := filepath.Join(c.Store.Dir, ".stage-crashed")
			os.Mkdir(crash, 0700)
			os.WriteFile(filepath.Join(crash, "partial"), []byte("partial"), 0600)
			switch mode {
			case "corrupt":
				f.corrupt = true
			case "expired":
				f.fail = true
			case "revoked":
				f.denied = true
			case "provenance":
				c.Deployment = "wrong-deployment"
			case "publication":
				collision := filepath.Join(c.Store.Dir, f.ops[1].ID)
				os.Mkdir(collision, 0700)
				// An existing tar collision must never be overwritten. An empty
				// directory is now correctly reclaimed as interrupted old pruning.
				os.WriteFile(filepath.Join(collision, "backup.tar"), []byte("collision"), 0600)
			}
			if e := c.Scan(t.Context()); e == nil {
				t.Fatal("false success")
			}
			if _, e := os.Stat(crash); !os.IsNotExist(e) {
				t.Fatal("crash staging retained")
			}
			pin, e := c.Store.Pin(prior)
			if e != nil {
				t.Fatal("previous usable bundle lost", e)
			}
			pin.Close()
			if _, e := c.Store.Pin(f.ops[1].ID); e == nil {
				t.Fatal("failed output published")
			}
		})
	}
}
func TestDownloadPinProtectsExactBytesDuringPrune(t *testing.T) {
	f := newFixture(1)
	c := collectorFor(t, f)
	if e := c.Scan(t.Context()); e != nil {
		t.Fatal(e)
	}
	id := f.ops[0].ID
	pin, e := c.Store.Pin(id)
	if e != nil {
		t.Fatal(e)
	}
	defer pin.Close()
	before, _ := io.ReadAll(pin)
	newer := newFixture(5)
	f.ops = append(f.ops, newer.ops[4])
	for k, v := range newer.data {
		f.data[k] = v
	}
	c.Keep = 1
	if e = c.Scan(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Store.Manifest(id); e != nil {
		t.Fatal("pinned bundle removed")
	}
	pin.Seek(0, 0)
	after, _ := io.ReadAll(pin)
	if string(before) != string(after) {
		t.Fatal("substituted bytes")
	}
	pin.Close()
	if e = c.Store.Prune(1); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Store.Pin(id); e == nil {
		t.Fatal("unpinned old result retained")
	}
}
func TestWriterExclusionAndMetadataMismatch(t *testing.T) {
	f := newFixture(1)
	c := collectorFor(t, f)
	lock, e := c.Store.Writer()
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Scan(t.Context()); e == nil {
		t.Fatal("concurrent writer admitted")
	}
	lock.Close()
	// Transport-consistent metadata still must match the selected descriptor.
	id := f.ops[0].Artifacts[2].ID
	raw := []byte(`{"payload":"e30=","mac":"` + strings.Repeat("a", 64) + `"}`)
	f.data[id] = raw
	f.ops[0].Artifacts[2].Size = int64(len(raw))
	f.ops[0].Artifacts[2].SHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
	if e = c.Scan(t.Context()); e == nil {
		t.Fatal("mismatched metadata published")
	}
}

func TestRenewalLossCancelsTransferAndPublishesNothing(t *testing.T) {
	f := newFixture(1)
	var renewals int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/leases/") && r.Method == "PUT" {
			renewals++
			if renewals > 1 {
				w.WriteHeader(401)
				fmt.Fprint(w, `{"error":{"code":"authentication_failed"}}`)
				return
			}
		}
		if strings.Contains(r.URL.Path, "/artifacts/") {
			w.Header().Set("Content-Length", fmt.Sprint(f.ops[0].Artifacts[0].Size))
			w.WriteHeader(200)
			w.Write([]byte("x"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		f.ServeHTTP(w, r)
	}))
	defer srv.Close()
	client, e := ac.New(srv.URL, srv.Client())
	if e != nil {
		t.Fatal(e)
	}
	c := Collector{Client: client, Store: Store{Dir: filepath.Join(t.TempDir(), "bundles")}, Token: "retrieval-test", Deployment: "trusted-test-deployment", Origin: srv.URL, Keep: 1, renewInterval: 10 * time.Millisecond}
	if e = c.Scan(t.Context()); e == nil {
		t.Fatal("renewal loss reported success")
	}
	if renewals < 2 {
		t.Fatal("renewal guard was not exercised")
	}
	all, e := c.Store.List()
	if e != nil || len(all) != 0 {
		t.Fatal("partial transfer published", e)
	}
}

func TestMalformedMetadataMACSyntaxIsNotPublished(t *testing.T) {
	f := newFixture(1)
	c := collectorFor(t, f)
	payload, _ := json.Marshal(f.ops[0].Pair)
	raw, _ := json.Marshal(map[string]string{"payload": base64.StdEncoding.EncodeToString(payload), "mac": strings.Repeat("z", 64)})
	a := &f.ops[0].Artifacts[len(f.ops[0].Artifacts)-1]
	f.data[a.ID] = raw
	a.Size = int64(len(raw))
	a.SHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
	if e := c.Scan(t.Context()); e == nil {
		t.Fatal("malformed MAC syntax published despite transport-consistent bytes")
	}
}
