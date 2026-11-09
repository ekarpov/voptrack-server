package main

import (
	"context"
	"github.com/ekarpov/voptrack-server/internal/runtimeobs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeExactBytesRestartFailureAndStaleness(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "server.json")
	receipt := filepath.Join(dir, "receipt.json")
	script := filepath.Join(dir, "server")
	captured := filepath.Join(dir, "loaded")
	raw := []byte(`{"backup":{"directory":"/var/lib/voptrack-backups","every_hours":24,"keep":7}}`)
	if e := os.WriteFile(config, raw, 0600); e != nil {
		t.Fatal(e)
	}
	body := "#!/bin/sh\ncat \"$2\" > " + captured + "\nsleep 0.12\n"
	if e := os.WriteFile(script, []byte(body), 0700); e != nil {
		t.Fatal(e)
	}
	run := func() runtimeobs.Receipt {
		t.Helper()
		if e := observedServer(context.Background(), config, receipt, script); e != nil {
			t.Fatal(e)
		}
		r, e := runtimeobs.Read(receipt, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	r := run()
	if r.State != "exited" || r.PID == 0 || r.ConfigSHA256 != runtimeobs.Digest(raw) {
		t.Fatal(r)
	}
	b, e := os.ReadFile(captured)
	if e != nil || string(b) != string(raw) {
		t.Fatal("receipt differs from loaded bytes")
	}
	next := run()
	if next.Instance == r.Instance {
		t.Fatal("restart reused instance")
	}
	stale, e := runtimeobs.Read(receipt, time.Now().Add(time.Minute))
	if e != nil || stale.State != "stale" {
		t.Fatal(stale, e)
	}
	if e := observedServer(context.Background(), config, receipt, filepath.Join(dir, "missing")); e == nil {
		t.Fatal("fixture")
	}
	failed, e := runtimeobs.Read(receipt, time.Now())
	if e != nil || failed.State != "failed" || failed.PID != 0 {
		t.Fatal(failed, e)
	}
}
func TestRuntimePinsConfigurationDuringConcurrentReplacement(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "server.json")
	receipt := filepath.Join(dir, "receipt.json")
	script := filepath.Join(dir, "server")
	captured := filepath.Join(dir, "loaded")
	ready := filepath.Join(dir, "ready")
	release := filepath.Join(dir, "release")
	raw := []byte(`{"backup":{"directory":"/var/lib/voptrack-backups","every_hours":24,"keep":7}}`)
	for path, b := range map[string][]byte{config: raw, script: []byte("#!/bin/sh\ntouch " + ready + "\nwhile [ ! -f " + release + " ]; do sleep 0.01; done\ncat \"$2\" > " + captured + "\n")} {
		if e := os.WriteFile(path, b, 0700); e != nil {
			t.Fatal(e)
		}
	}
	done := make(chan error, 1)
	go func() { done <- observedServer(context.Background(), config, receipt, script) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, e := os.Stat(ready); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not launch")
		}
		time.Sleep(time.Millisecond)
	}
	observedBefore, e := runtimeobs.Read(receipt, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e := observedServer(context.Background(), config, receipt, script); e == nil {
		t.Fatal("second wrapper took receipt ownership")
	}
	observedAfter, e := runtimeobs.Read(receipt, time.Now())
	if e != nil || observedAfter.Instance != observedBefore.Instance {
		t.Fatal("second wrapper replaced active receipt")
	}
	newer := []byte(`{"backup":{"directory":"/var/lib/voptrack-backups","every_hours":12,"keep":2}}`)
	if e := os.WriteFile(config, newer, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(release, nil, 0600); e != nil {
		t.Fatal(e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(captured)
	if e != nil || string(b) != string(raw) {
		t.Fatal("loaded replacement bytes")
	}
	r, e := runtimeobs.Read(receipt, time.Now())
	if e != nil || r.ConfigSHA256 != runtimeobs.Digest(b) || r.Values.EveryHours != 24 {
		t.Fatal("receipt describes another configuration", r, e)
	}
	if e := observedServer(context.Background(), config, receipt, script); e != nil {
		t.Fatal(e)
	}
	next, e := runtimeobs.Read(receipt, time.Now())
	if e != nil || next.ConfigSHA256 == r.ConfigSHA256 || next.Values.EveryHours != 12 {
		t.Fatal("new configuration launch not distinguished")
	}
}

func TestRuntimeNonzeroStartupRecordsFailure(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "server.json")
	receipt := filepath.Join(dir, "receipt.json")
	if e := os.WriteFile(config, []byte(`{"backup":{"directory":"/var/lib/voptrack-backups","every_hours":24,"keep":7}}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := observedServer(context.Background(), config, receipt, "/bin/false"); e == nil {
		t.Fatal("fixture")
	}
	r, e := runtimeobs.Read(receipt, time.Now())
	if e != nil || r.State != "failed" || r.PID == 0 {
		t.Fatal(r, e)
	}
}

func TestRuntimeBootstrapPinsExportAndRequiresTrustedDeployment(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "server.json")
	receipt := filepath.Join(dir, "receipt.json")
	script := filepath.Join(dir, "server")
	exportPath := filepath.Join(dir, "export.json")
	capture := filepath.Join(dir, "captured")
	raw := []byte(`{"backup":{"directory":"/var/lib/voptrack-backups","every_hours":24,"keep":7}}`)
	if e := os.WriteFile(config, raw, 0600); e != nil {
		t.Fatal(e)
	}
	deployment := "0123456789abcdef0123456789abcdef"
	exported := []byte(`{"version":1,"deployment":"` + deployment + `","every_hours":12,"keep":2,"revision":"` + deployment + `.0123456789abcdef0123456789abcdef.0123456789abcdef0123456789abcdef","exported_at":"2026-10-17T12:00:00Z"}`)
	if e := os.WriteFile(exportPath, exported, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(script, []byte("#!/bin/sh\n[ \"$3\" = -backup-settings-bootstrap ] || exit 2\ncat \"$4\" > "+capture+"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	for _, trusted := range []string{"", "ffffffffffffffffffffffffffffffff"} {
		if observedServerBootstrap(context.Background(), config, receipt, script, exportPath, trusted) == nil {
			t.Fatal("untrusted lineage accepted")
		}
	}
	if e := observedServerBootstrap(context.Background(), config, receipt, script, exportPath, deployment); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(capture)
	if e != nil || string(b) != string(exported) {
		t.Fatal("bootstrap bytes not pinned")
	}
}
