package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestStagedExportMissingFenceReplacementAndInterrupt(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "archive.enc")
	if err := os.WriteFile(archive, []byte("completed"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if stagedTar(&out, dir, []string{"archive.enc", "dataset.db.retired"}) == nil || out.Len() != 0 {
		t.Fatal("missing fence emitted bundle")
	}
	fence := filepath.Join(dir, "dataset.db.retired")
	if err := os.WriteFile(fence, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if stagedTar(brokenWriter{}, dir, []string{"archive.enc", "dataset.db.retired"}) == nil {
		t.Fatal("interrupted output reported success")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(stagedTar(contextWriter{ctx, &out}, dir, []string{"archive.enc"}), context.Canceled) {
		t.Fatal("cancelled export succeeded")
	}
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fence, archive); err != nil {
		t.Fatal(err)
	}
	if stagedTar(&out, dir, []string{"archive.enc", "dataset.db.retired"}) == nil || out.Len() != 0 {
		t.Fatal("replacement symlink accepted")
	}
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("completed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := stagedTar(&out, dir, []string{"archive.enc", "dataset.db.retired"}); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(&out)
	for _, name := range []string{"archive.enc", "dataset.db.retired"} {
		h, err := tr.Next()
		if err != nil || h.Name != name {
			t.Fatal(h, err)
		}
	}
}

func TestMaintenanceExcludesLiveAndConcurrentProducers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "maintenance.lock")
	first, err := maintenanceAt(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if other, e := maintenanceAt(path, true); e == nil {
		other.Close()
		t.Fatal("writer entered maintenance")
	}
	first.Close()
	live, err := maintenanceAt(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if exclusive, e := maintenanceAt(path, false); e == nil {
		exclusive.Close()
		t.Fatal("maintenance allowed a live producer")
	}
	second, err := maintenanceAt(path, true)
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
	live.Close()
	exclusive, err := maintenanceAt(path, false)
	if err != nil {
		t.Fatal(err)
	}
	exclusive.Close()
}
