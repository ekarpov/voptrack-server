package backupsettings

import (
	sc "github.com/ekarpov/voptrack-server/internal/schedulerclient"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestLiveDurabilityConcurrencyAndStartup(t *testing.T) {
	dir := t.TempDir()
	revision := strings.Repeat("a", 32) + "." + strings.Repeat("b", 32) + "." + strings.Repeat("c", 32)
	p := sc.Payload{ExpectedRevision: revision, EveryHours: 12, Keep: 2}
	var l Live
	fingerprint := l.Fingerprint()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			results <- WithLive(dir, func(v *Live, save func() error) error {
				if e := v.Begin(fingerprint, p, time.Now()); e != nil {
					return e
				}
				return save()
			})
		})
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if e != ErrStale {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
	saved, e := ReadLive(dir)
	if e != nil || saved.Current().Payload != p {
		t.Fatal(saved, e)
	}
	id := saved.Current().ID
	// A different session/restart reads the same unresolved request; time alone
	// cannot expire it or make the next command admissible.
	if _, e = StartupValues(dir, Values{"local", 24, 7}); e == nil {
		t.Fatal("unresolved command permitted init")
	}
	if e = WithLive(dir, func(v *Live, save func() error) error {
		v.Current().State = "pending"
		if v.Begin(v.Fingerprint(), p, time.Now().Add(9*24*time.Hour)) != ErrStale {
			t.Fatal("pending replaced after provisional expiry")
		}
		return save()
	}); e != nil {
		t.Fatal(e)
	}
	saved, _ = ReadLive(dir)
	if saved.Current().ID != id {
		t.Fatal("identity changed")
	}
	if e = WithLive(dir, func(v *Live, save func() error) error {
		v.Current().State = "effective"
		v.Destination = "local"
		v.Observation = &sc.Observation{Available: true, Settings: &sc.Settings{EveryHours: 12, Keep: 2}}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	// Test precedence independently of journal shape validation.
	var observation = sc.Observation{InstanceID: strings.Repeat("d", 32), ObservedAt: time.Now().UTC(), HistoryScope: "process_instance", Configured: false, Reason: "not_configured", NextReason: "not_configured"}
	if e = WithLive(dir, func(v *Live, save func() error) error {
		v.Current().State = "effective"
		v.Observation = &observation
		return save()
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = StartupValues(dir, Values{"local", 24, 7}); e == nil {
		t.Fatal("unavailable policy permitted init")
	}
}

func TestCorruptLiveJournalFileFailsClosed(t *testing.T) {
	for _, kind := range []string{"oversize", "fifo", "symlink", "public"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "backup-live.json")
			switch kind {
			case "oversize":
				f, e := os.Create(path)
				if e != nil {
					t.Fatal(e)
				}
				f.Chmod(0600)
				f.Truncate(5 << 20)
				f.Close()
			case "fifo":
				if e := syscall.Mkfifo(path, 0600); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				target := filepath.Join(t.TempDir(), "other")
				os.WriteFile(target, []byte(`{}`), 0600)
				if e := os.Symlink(target, path); e != nil {
					t.Fatal(e)
				}
			case "public":
				os.WriteFile(path, []byte(`{}`), 0644)
				if e := os.Chmod(path, 0644); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := ReadLive(dir); e == nil {
				t.Fatal("invalid journal file accepted")
			}
		})
	}
}
