package recoveryset

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStagedInputsRemainBoundAndMaterialsAreVerified(t *testing.T) {
	o := fixture(t, "household")
	dir := filepath.Join(t.TempDir(), "snapshot")
	staged, receipt, e := Stage(context.Background(), o, dir)
	must(t, e)
	if receipt.Status != "inputs-verified-not-restored" {
		t.Fatal("incorrect staged receipt")
	}
	// Modifying retained originals cannot silently change the private snapshot.
	raw, e := os.ReadFile(filepath.Join(o.Directory, "selection.json"))
	must(t, e)
	must(t, os.WriteFile(filepath.Join(o.Directory, "selection.json"), []byte("{}"), 0600))
	current, e := Preflight(context.Background(), staged)
	must(t, e)
	if current.SelectionSHA256 != receipt.SelectionSHA256 {
		t.Fatal("snapshot moved with originals")
	}
	kit, settings, archive, evidence, e := Materials(context.Background(), staged, filepath.Join(t.TempDir(), "members"))
	must(t, e)
	if len(kit) == 0 || len(settings) == 0 {
		t.Fatal("material missing")
	}
	for _, p := range []string{archive, evidence} {
		st, e := os.Stat(p)
		must(t, e)
		if !st.Mode().IsRegular() {
			t.Fatal("unsafe extracted member")
		}
	}
	must(t, os.WriteFile(filepath.Join(o.Directory, "selection.json"), raw, 0600))
	// Tampering with the private snapshot itself must fail re-authentication.
	must(t, os.WriteFile(staged.KeyFile, make([]byte, 32), 0600))
	output := filepath.Join(t.TempDir(), "members")
	if _, _, _, _, e = Materials(context.Background(), staged, output); e == nil {
		t.Fatal("modified snapshot accepted")
	}
	if _, e = os.Stat(output); !os.IsNotExist(e) {
		t.Fatal("extracted before verification")
	}
}
func TestStageRefusesExistingDestination(t *testing.T) {
	o := fixture(t, "research")
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "preserve"), []byte("unchanged"), 0600))
	if _, _, e := Stage(context.Background(), o, dir); e == nil {
		t.Fatal("existing staging overwritten")
	}
	b, e := os.ReadFile(filepath.Join(dir, "preserve"))
	must(t, e)
	if string(b) != "unchanged" {
		t.Fatal("staging changed")
	}
}
