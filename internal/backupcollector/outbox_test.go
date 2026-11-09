package backupcollector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOutboxCapturesBeforeRotationAndSurvivesCollectorRestart(t *testing.T) {
	f := newFixture(8)
	c := collectorFor(t, f)
	c.Keep = 1
	q := Outbox{Dir: t.TempDir()}
	if e := os.Chmod(q.Dir, 0700); e != nil {
		t.Fatal(e)
	}
	c.Outbox = &q
	if e := c.Scan(t.Context()); e != nil {
		t.Fatal(e)
	}
	all, e := c.Store.List()
	if e != nil || len(all) != 2 {
		t.Fatal("local retention changed")
	}
	queued, e := q.Records()
	if e != nil || len(queued) != 8 {
		t.Fatal("rotation lost durable obligations")
	}
	for _, r := range queued {
		p, e := q.Payload(r)
		if e != nil {
			t.Fatal("queue did not retain exact original bytes")
		}
		p.Close()
		if !r.LocalPublished {
			t.Fatal("local publication acknowledgement missing")
		}
	}
	gets := f.gets
	restarted := c
	if e = restarted.Scan(t.Context()); e != nil {
		t.Fatal(e)
	}
	if f.gets != gets {
		t.Fatal("restart recollected already captured rotated results")
	}
}
func TestOutboxCaptureCrashReplaysExactLocalPublication(t *testing.T) {
	c := collectorFor(t, newFixture(1))
	if e := c.Scan(t.Context()); e != nil {
		t.Fatal(e)
	}
	all, _ := c.Store.List()
	m := all[0].Manifest
	q := Outbox{Dir: t.TempDir()}
	if e := os.Chmod(q.Dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := q.Capture(filepath.Join(c.Store.Dir, m.Operation.ID), m); e != nil {
		t.Fatal(e)
	}
	r, e := q.Record(m.Operation.ID)
	if e != nil {
		t.Fatal(e)
	}
	// Remote acknowledgement alone must not discard a not-yet-published local handoff.
	if e = q.Release(r); e != nil || !q.HasPayload(m.Operation.ID) {
		t.Fatal("premature payload release")
	}
	fresh := Store{Dir: filepath.Join(t.TempDir(), "bundles")}
	lock, e := fresh.Writer()
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	found, e := q.ReplayLocal(m.Operation.ID, fresh, m.Operation, m.Origin)
	if !found || e != nil {
		t.Fatal("capture crash could not replay local publication", e)
	}
	before, _ := os.ReadFile(filepath.Join(c.Store.Dir, m.Operation.ID, "backup.tar"))
	after, _ := os.ReadFile(filepath.Join(fresh.Dir, m.Operation.ID, "backup.tar"))
	if !bytes.Equal(before, after) {
		t.Fatal("replay changed collected time or payload")
	}
	r, _ = q.Record(m.Operation.ID)
	if !r.LocalPublished {
		t.Fatal("publication acknowledgement not durable")
	}
	if e = q.Release(r); e != nil || q.HasPayload(m.Operation.ID) {
		t.Fatal("acknowledged payload not released")
	}
	// A tombstone is retained, preventing re-enqueue after local rotation.
	if e = q.Capture(filepath.Join(c.Store.Dir, m.Operation.ID), m); e != nil || q.HasPayload(m.Operation.ID) {
		t.Fatal("acknowledged result was queued twice")
	}
}
func TestOutboxRefusalPreservesExistingDownloads(t *testing.T) {
	c := collectorFor(t, newFixture(4))
	c.Keep = 1
	if e := c.Scan(t.Context()); e != nil {
		t.Fatal(e)
	}
	all, _ := c.Store.List()
	before := map[string][]byte{}
	for _, b := range all {
		before[b.Operation.ID], _ = os.ReadFile(filepath.Join(c.Store.Dir, b.Operation.ID, "backup.tar"))
	}
	c.Outbox = &Outbox{Dir: t.TempDir(), Budget: 1}
	if c.Scan(t.Context()) == nil {
		t.Fatal("invalid capacity accepted")
	}
	for id, raw := range before {
		after, e := os.ReadFile(filepath.Join(c.Store.Dir, id, "backup.tar"))
		if e != nil || !bytes.Equal(raw, after) {
			t.Fatal("outbox error changed existing usable download")
		}
	}
}
func TestReplicationStatusRejectsStaleFutureAndInvalidReceipt(t *testing.T) {
	now := time.Now()
	path := filepath.Join(t.TempDir(), "status")
	write := func(s ReplicationStatus) {
		raw, _ := json.Marshal(s)
		if e := os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	base := ReplicationStatus{Version: 1, Label: "Synthetic destination", State: "verified", Observed: now, LastVerified: now, Budget: DefaultOutboxBudget}
	for _, test := range []struct {
		modify func(*ReplicationStatus)
		want   string
	}{
		{func(s *ReplicationStatus) { s.Observed = now.Add(-3 * time.Minute) }, "unknown"},
		{func(s *ReplicationStatus) { s.LastVerified = now.Add(-27 * time.Hour) }, "stale"},
		{func(s *ReplicationStatus) { s.LastVerified = now.Add(time.Hour) }, "unknown"},
		{func(s *ReplicationStatus) { s.Pending = -1 }, "unknown"},
	} {
		s := base
		test.modify(&s)
		write(s)
		v, _ := ReadReplicationStatus(path, now)
		if v.State != test.want {
			t.Fatal("misleading status", v.State, test.want)
		}
	}
}

func TestOutboxCaptureReplayDoesNotRequireSurvivingProducerCatalog(t *testing.T) {
	source := newFixture(1)
	c := collectorFor(t, source)
	if e := c.Scan(t.Context()); e != nil {
		t.Fatal(e)
	}
	all, _ := c.Store.List()
	m := all[0].Manifest
	q := Outbox{Dir: filepath.Join(t.TempDir(), "queue")}
	os.Mkdir(q.Dir, 0700)
	if e := q.Capture(filepath.Join(c.Store.Dir, m.Operation.ID), m); e != nil {
		t.Fatal(e)
	}
	original, _ := os.ReadFile(filepath.Join(c.Store.Dir, m.Operation.ID, "backup.tar"))
	source.mu.Lock()
	source.ops = nil
	gets := source.gets
	source.mu.Unlock()
	// The old publication is not mounted by this new collector instance.
	c.Store = Store{Dir: filepath.Join(t.TempDir(), "fresh-bundles")}
	c.Outbox = &q
	if e := c.Scan(t.Context()); e != nil {
		t.Fatal(e)
	}
	restored, e := os.ReadFile(filepath.Join(c.Store.Dir, m.Operation.ID, "backup.tar"))
	if e != nil || !bytes.Equal(original, restored) {
		t.Fatal("rotated producer catalog prevented durable capture replay")
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.gets != gets {
		t.Fatal("replay downloaded producer data")
	}
}

func TestPublishedBeforeAcknowledgementReconcilesWithoutCatalog(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			source := newFixture(1)
			c := collectorFor(t, source)
			if e := c.Scan(t.Context()); e != nil {
				t.Fatal(e)
			}
			all, _ := c.Store.List()
			m := all[0].Manifest
			q := Outbox{Dir: filepath.Join(t.TempDir(), "queue")}
			os.Mkdir(q.Dir, 0700)
			if e := q.Capture(filepath.Join(c.Store.Dir, m.Operation.ID), m); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(c.Store.Dir, m.Operation.ID, "backup.tar")
			if corrupt {
				if e := os.WriteFile(path, []byte("preserved partial publication"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			before, _ := os.ReadFile(path)
			source.mu.Lock()
			source.ops = nil
			source.mu.Unlock()
			c.Outbox = &q
			e := c.Scan(t.Context())
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("existing publication overwritten")
			}
			r, _ := q.Record(m.Operation.ID)
			if corrupt {
				if e == nil || r.LocalPublished || !q.HasPayload(m.Operation.ID) {
					t.Fatal("partial publication was admitted or lost")
				}
			} else if e != nil || !r.LocalPublished {
				t.Fatal("publication-before-ack crash did not reconcile", e)
			}
		})
	}
}

func TestOutboxRefusesObservationWhenDirectoryBarrierFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission failure requires unprivileged uid")
	}
	q := Outbox{Dir: t.TempDir()}
	if e := os.Chmod(q.Dir, 0300); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(q.Dir, 0700)
	called := false
	e := q.Locked(func() error { called = true; return nil })
	if e == nil || called {
		t.Fatal("outbox exposed namespace without successful directory durability barrier")
	}
}
