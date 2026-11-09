package backupreplication

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestS3ExplicitCredentialTLSConditionalCreateAndReadback(t *testing.T) {
	var mu sync.Mutex
	objects := map[string][]byte{}
	unsafe := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !strings.Contains(r.Header.Get("Authorization"), "Credential=synthetic-explicit/") || r.Header.Get("X-Amz-Expected-Bucket-Owner") != "000000000000" {
			unsafe = true
			w.WriteHeader(403)
			return
		}
		if r.Method == "PUT" {
			if r.Header.Get("If-None-Match") != "*" {
				unsafe = true
				w.WriteHeader(403)
				return
			}
			if _, ok := objects[r.URL.Path]; ok {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(412)
				io.WriteString(w, "<Error><Code>PreconditionFailed</Code></Error>")
				return
			}
			raw, _ := io.ReadAll(r.Body)
			objects[r.URL.Path] = raw
			w.WriteHeader(200)
			return
		}
		if r.Method == "GET" {
			if raw, ok := objects[r.URL.Path]; ok {
				w.Write(raw)
				return
			}
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(404)
			io.WriteString(w, "<Error><Code>NoSuchKey</Code></Error>")
			return
		}
		unsafe = true
		w.WriteHeader(403)
	}))
	defer server.Close()
	root := t.TempDir()
	ca := filepath.Join(root, "ca.crt")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	creds := filepath.Join(root, "credential.json")
	raw, _ := json.Marshal(credential{AccessKeyID: "synthetic-explicit", SecretAccessKey: "synthetic-private-fixture-only"})
	os.WriteFile(creds, raw, 0600)
	t.Setenv("AWS_ACCESS_KEY_ID", "ambient-must-not-be-used")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "ambient-must-not-be-used")
	c := Config{ExpectedBucketOwner: "000000000000", Version: 1, Label: "Synthetic", Region: "us-east-1", Bucket: "synthetic-backups", Prefix: "test/", Deployment: strings.Repeat("a", 32), CredentialFile: creds, LocalFixture: true, Endpoint: server.URL, CAFile: ca}
	store, e := NewS3(c)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if e = store.Put(ctx, "test/payload", strings.NewReader("opaque synthetic bytes"), 22); e != nil {
		t.Fatal("explicit TLS write failed", e)
	}
	body, e := store.Get(ctx, "test/payload")
	if e != nil {
		t.Fatal(e)
	}
	got, _ := io.ReadAll(body)
	body.Close()
	if string(got) != "opaque synthetic bytes" {
		t.Fatal("SDK payload framing changed bytes")
	}
	if store.Put(ctx, "test/payload", strings.NewReader("replacement"), 11) != ErrExists {
		t.Fatal("conditional overwrite not refused")
	}
	mu.Lock()
	if unsafe {
		t.Fatal("ambient credential or unexpected method used")
	}
	mu.Unlock()
	c.CredentialFile = filepath.Join(root, "missing")
	if _, e = NewS3(c); e == nil {
		t.Fatal("missing explicit credential fell back to ambient")
	}
	c.LocalFixture = false
	c.CredentialFile = creds
	if _, e = NewS3(c); e == nil {
		t.Fatal("unqualified external endpoint accepted")
	}
}
