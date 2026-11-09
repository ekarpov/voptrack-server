package backupsettings

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestCASAndValidation(t *testing.T) {
	dir := t.TempDir()
	v := Values{"local", 24, 7}
	r, e := Read(dir)
	if e != nil || r.Revision != "none" {
		t.Fatal(r, e)
	}
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); out <- Save(dir, "none", v) }()
	}
	wg.Wait()
	close(out)
	successes := 0
	for err := range out {
		if err == nil {
			successes++
		} else if err != ErrStale {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatal(successes)
	}
	r, e = Read(dir)
	if e != nil || r.Values != v {
		t.Fatal(r, e)
	}
	for _, bad := range []Values{{"../../private", 1, 1}, {"local", 0, 1}, {"local", 1, 0}, {"reserve", 8761, 1}, {"local", 1, 1001}} {
		if Save(dir, r.Revision, bad) == nil {
			t.Fatal("accepted invalid settings", bad)
		}
	}
	if e = os.WriteFile(filepath.Join(dir, "backup-settings.json"), []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Read(dir); e == nil {
		t.Fatal("corrupt request accepted")
	}
}
