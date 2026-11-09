package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
)

func TestCompletedIssuerWriteReconcilesPendingOnSignIn(t *testing.T) {
	a, _, old := setupHTTPApp(t)
	defer old.Close()
	a.studies.dir = filepath.Join(a.cfg.StateDir, "studies")
	interrupted := errors.New("interrupted after completed writes")
	if err := recoverymaterial.Change(a.cfg.StateDir, "issuer:synthetic", func() error {
		must(t, a.studies.setIssuer("synthetic", "study-synthetic", "completed-secret", studyCap))
		return interrupted
	}); !errors.Is(err, interrupted) {
		t.Fatal(err)
	}
	// A different interrupted operation must not be silently reconciled.
	_ = recoverymaterial.Change(a.cfg.StateDir, "other-operation", func() error { return interrupted })
	calls, fail := 0, true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/admin/api/v1/issuers/study-synthetic/secret" || r.Header.Get("Authorization") != "Bearer approved-researcher" {
			t.Error("rotation did not use supported endpoint and current researcher authority")
			w.WriteHeader(403)
			return
		}
		if fail {
			w.WriteHeader(503)
			return
		}
		writeJSON(w, 200, map[string]string{"secret": "reconciled-secret"})
	}))
	defer srv.Close()
	a.tessera = &Tessera{internal: srv.URL, http: srv.Client()}
	a.replaceLostSecrets(context.Background(), "approved-researcher")
	state, err := recoverymaterial.Read(a.cfg.StateDir)
	must(t, err)
	secret, err := a.studies.issuerSecret("synthetic")
	must(t, err)
	if calls != 1 || !state.Pending["issuer:synthetic"] || secret != "completed-secret" {
		t.Fatal("failed recovery lost pending intent or saved material", calls, state)
	}
	fail = false
	a.replaceLostSecrets(context.Background(), "approved-researcher")
	state, err = recoverymaterial.Read(a.cfg.StateDir)
	must(t, err)
	secret, err = a.studies.issuerSecret("synthetic")
	must(t, err)
	if calls != 2 || state.Pending["issuer:synthetic"] || !state.Pending["other-operation"] || secret != "reconciled-secret" {
		t.Fatal("completed issuer remained pending or cleared unrelated intent", calls, state)
	}
	revision := state.Revision
	a.replaceLostSecrets(context.Background(), "approved-researcher")
	state, err = recoverymaterial.Read(a.cfg.StateDir)
	must(t, err)
	if calls != 2 || state.Revision != revision {
		t.Fatal("healthy issuer rotated on subsequent sign-in")
	}
	preserved := false
	must(t, filepath.WalkDir(filepath.Join(a.cfg.StateDir, ".recovery-history"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == "synthetic.issuer-secret" {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if string(raw) == "completed-secret\n" {
				preserved = true
			}
		}
		return nil
	}))
	if !preserved {
		t.Fatal("reconciliation discarded material needed by older backups")
	}
}
