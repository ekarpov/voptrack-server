package backupcollector

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestInterruptedRetentionKeepsIntactListings(t *testing.T) {
	f := newFixture(3)
	c := collectorFor(t, f)
	if e := c.Scan(t.Context()); e != nil {
		t.Fatal(e)
	}
	old := f.ops[0].ID
	newer := f.ops[2].ID
	// Reproduce the old in-place RemoveAll crash boundary.
	if e := os.Remove(filepath.Join(c.Store.Dir, old, "backup.tar")); e != nil {
		t.Fatal(e)
	}
	all, e := c.Store.List()
	if e != nil || len(all) != 2 {
		t.Fatalf("interrupted retention hid intact bundles: count=%d error=%v", len(all), e)
	}
	lock, e := c.Store.Writer()
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	// Also reproduce a crash after atomic retirement and partial deletion.
	retired, e := c.Store.retire(old)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(c.Store.Dir, old)); !os.IsNotExist(e) {
		t.Fatal("retired bundle still published")
	}
	if e = c.Store.Cleanup(); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(retired); !os.IsNotExist(e) {
		t.Fatal("interrupted retirement not reclaimed")
	}
	if e = c.Store.Prune(1); e != nil {
		t.Fatal(e)
	}
	pin, e := c.Store.Pin(newer)
	if e != nil {
		t.Fatal("intact newest backup lost", e)
	}
	pin.Close()
}

func TestCleanupReconcilesOldInPlaceRetentionCrash(t *testing.T) {
	f := newFixture(1)
	c := collectorFor(t, f)
	if e := c.Scan(t.Context()); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(c.Store.Dir, f.ops[0].ID)
	if e := os.Remove(filepath.Join(dir, "backup.tar")); e != nil {
		t.Fatal(e)
	}
	lock, e := c.Store.Writer()
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	if e = c.Store.Cleanup(); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(dir); !os.IsNotExist(e) {
		t.Fatal("legacy partial retirement remains published")
	}
}

func TestPublishedBundleIntegrityBeforeAvailability(t *testing.T) {
	for _, mode := range []string{"artifact", "header", "padding", "trailing", "manifest"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(3)
			c := collectorFor(t, f)
			if e := c.Scan(t.Context()); e != nil {
				t.Fatal(e)
			}
			id := f.ops[0].ID
			path := filepath.Join(c.Store.Dir, id, "backup.tar")
			raw, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "artifact":
				// First artifact follows the tar manifest, whose padded size is in its header.
				var size int64
				if _, e = fmt.Sscanf(string(raw[124:135]), "%o", &size); e != nil {
					t.Fatal(e)
				}
				offset := 512 + ((size+511)/512)*512 + 512
				raw[offset] ^= 1
			case "header":
				raw[100] ^= 1
			case "padding":
				raw[len(raw)-1] ^= 1
			case "trailing":
				raw = append(raw, 0)
			case "manifest":
				raw[512] ^= 1
			}
			if e = os.WriteFile(path, raw, 0600); e != nil {
				t.Fatal(e)
			}
			if p, e := c.Store.Pin(id); e == nil {
				p.Close()
				t.Fatal("corrupt published tar accepted")
			}
			all, e := c.Store.List()
			if e != nil || len(all) != 2 {
				t.Fatal("corruption hid independent usable bundles", len(all), e)
			}
			for _, b := range all {
				if b.Operation.ID == id {
					t.Fatal("corrupt bundle advertised")
				}
			}
		})
	}
}

func TestRescanRetriesPinnedRetentionAndReducedKeep(t *testing.T) {
	for _, mode := range []string{"pinned", "reduced"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(1)
			c := collectorFor(t, f)
			if e := c.Scan(t.Context()); e != nil {
				t.Fatal(e)
			}
			old := f.ops[0].ID
			pin, e := c.Store.Pin(old)
			if e != nil {
				t.Fatal(e)
			}
			before, e := io.ReadAll(pin)
			if e != nil {
				t.Fatal(e)
			}
			newer := newFixture(3)
			f.ops = append(f.ops, newer.ops[2])
			for k, v := range newer.data {
				f.data[k] = v
			}
			if mode == "pinned" {
				c.Keep = 1
			}
			if e = c.Scan(t.Context()); e != nil {
				t.Fatal(e)
			}
			if _, e = os.Stat(filepath.Join(c.Store.Dir, old)); e != nil {
				t.Fatal("active pin lost")
			}
			pin.Seek(0, 0)
			after, _ := io.ReadAll(pin)
			if sha256.Sum256(before) != sha256.Sum256(after) {
				t.Fatal("pin bytes changed")
			}
			pin.Close()
			gets := f.gets
			c.Keep = 1
			// No publication or new catalog result; this must retry deferred retention.
			if e = c.Scan(t.Context()); e != nil {
				t.Fatal(e)
			}
			if f.gets != gets {
				t.Fatal("rescan recollected completed bundles")
			}
			if _, e = os.Stat(filepath.Join(c.Store.Dir, old)); !os.IsNotExist(e) {
				t.Fatal("successful rescan did not reconcile retention")
			}
		})
	}
}
