package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHomeReadRecoversServerDiscoveryAfterStartupFailure(t *testing.T) {
	a, s, old := policyHTTPApp(t)
	old.Close()
	available := false
	loads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth/account":
			writeJSON(w, 200, map[string]any{"account": "synthetic", "server_administrator": true})
		case r.URL.Path == "/oauth/token":
			if !available {
				writeJSON(w, 503, map[string]any{"error": map[string]any{"code": "unavailable"}})
				return
			}
			writeJSON(w, 200, map[string]any{"access_token": "reader", "expires_in": 300})
		case r.URL.Path == "/v1/datasets":
			writeJSON(w, 200, map[string]any{"datasets": []any{}})
		case strings.HasSuffix(r.URL.Path, "/meta"):
			w.WriteHeader(409)
		case strings.HasSuffix(r.URL.Path, ":page"):
			typ := strings.TrimSuffix(filepath.Base(r.URL.Path), ":page")
			writeJSON(w, 200, map[string]any{"cursor": typ})
		case strings.HasSuffix(r.URL.Path, ":next"):
			rows := []any{}
			if strings.Contains(r.URL.Path, docType) {
				loads++
				rows = append(rows, map[string]any{"id": map[string]any{"id": "registry"}, "values": []any{map[string]any{"value": homeSources}, map[string]any{"value": "[\"one\",\"two\"]"}}})
			}
			writeJSON(w, 200, map[string]any{"meta": map[string]any{"fields": []any{map[string]any{"id": "name"}, map[string]any{"id": "body"}}}, "records": rows})
		default:
			t.Errorf("unexpected discovery request: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, homeSources), []byte(`["one"]`), 0600))
	must(t, os.WriteFile(filepath.Join(dir, "dataset"), []byte("state"), 0600))
	must(t, os.WriteFile(filepath.Join(dir, "secret"), []byte("synthetic"), 0600))
	a.tessera = &Tessera{internal: server.URL, http: server.Client()}
	a.docs = &stateDocs{t: a.tessera, files: fileDocs{dir: dir}, service: "console-state", secretFile: filepath.Join(dir, "secret"), datasetFile: filepath.Join(dir, "dataset")}
	a.homeReg = &homeRegistry{docs: a.docs}
	a.cfg.HomeService = "home"
	a.homeCache = newServiceTokens("home", "")
	a.homeCache.secret = "synthetic"
	a.docs.ensure(context.Background())
	if len(a.homeReg.load()) != 1 {
		t.Fatal("startup fallback fixture is wrong")
	}
	available = true
	sources := a.scopeSources(httptest.NewRequest("GET", "/home", nil), s, "home")
	if len(sources) != 2 || loads != 1 {
		t.Fatalf("server discovery did not recover: sources=%d loads=%d", len(sources), loads)
	}
	_ = a.scopeSources(httptest.NewRequest("GET", "/home", nil), s, "home")
	if loads != 1 {
		t.Fatal("healthy document store was reloaded unnecessarily")
	}
}

func TestRetiredHomeSavedViewsNeverReadDefinitions(t *testing.T) {
	a, s, old := policyHTTPApp(t)
	old.Close()
	a.cfg.Variant = "household"
	a.saved = nil
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	a.tessera = &Tessera{internal: server.URL, http: server.Client()}
	for _, scope := range []string{"home", "mine"} {
		w := savedRequest(a, s, "GET", "/views?scope="+scope, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("retired library reached for %s: %d", scope, w.Code)
		}
	}
}
