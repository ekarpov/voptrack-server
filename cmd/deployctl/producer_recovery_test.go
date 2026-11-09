package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProducerRecoveryPinsInputsAndUsesIndependentIdentity(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config")
	export := filepath.Join(dir, "export")
	executable := filepath.Join(dir, "cli")
	capture := filepath.Join(dir, "capture")
	if e := os.WriteFile(config, []byte("original config"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(export, []byte("original export"), 0600); e != nil {
		t.Fatal(e)
	}
	script := "#!/bin/sh\nset -eu\n[ \"$1\" = -config ]\n[ \"$3\" = -recover-backup-producer ]\n[ \"$4\" = -expected-backup-producer ]\n[ \"$5\" = independent ]\n[ \"$6\" = -backup-settings-bootstrap ]\nprintf changed > " + config + "\nprintf changed > " + export + "\ncat \"$2\" \"$7\" > " + capture + "\n"
	if e := os.WriteFile(executable, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	if e := recoverProducer(context.Background(), config, executable, export, ""); e == nil {
		t.Fatal("missing independent identity accepted")
	}
	if e := recoverProducer(context.Background(), config, executable, export, "independent"); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(capture)
	if e != nil || string(b) != "original configoriginal export" {
		t.Fatal("mutable input passed to child", e)
	}
}
func TestProducerRecoveryRefusesUnsafeExport(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config")
	export := filepath.Join(dir, "export")
	link := filepath.Join(dir, "link")
	os.WriteFile(config, []byte("{}"), 0600)
	os.WriteFile(export, []byte(strings.Repeat("x", 4097)), 0600)
	os.Symlink(export, link)
	for _, p := range []string{export, link, dir} {
		if e := recoverProducer(context.Background(), config, "/bin/true", p, "independent"); e == nil {
			t.Fatal("unsafe export accepted", p)
		}
	}
}
