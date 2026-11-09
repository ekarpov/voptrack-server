package backupcollector

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	ac "github.com/ekarpov/voptrack-server/internal/acquisitionclient"
)

type Collector struct {
	Client                    *ac.Client
	Store                     Store
	Token, Deployment, Origin string
	Keep                      int
	Outbox                    *Outbox
	renewInterval             time.Duration
}

// Scan is bounded, serialized across processes and always starts a full rescan.
// It never follows private producer paths and never invokes Start or Cancel.
func (c *Collector) Scan(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if c.Client == nil || c.Token == "" || c.Deployment == "" || c.Keep < 1 || c.Keep > 32 {
		return ac.ErrInvalid
	}
	lock, e := c.Store.Writer()
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = c.Store.Cleanup(); e != nil {
		return e
	}
	if c.Outbox != nil {
		// Captured-before-publication work must not depend on the producer retaining
		// its catalog entry. Complete local handoff from exact durable captured bytes.
		pending, e := c.Outbox.Records()
		if e != nil {
			return e
		}
		for _, r := range pending {
			if r.LocalPublished {
				continue
			}
			if r.Manifest.Operation.Pair == nil || r.Manifest.Operation.Pair.Deployment != c.Deployment {
				return ErrProvenance
			}
			if _, e = c.Outbox.ReplayLocal(r.Manifest.Operation.ID, c.Store, r.Manifest.Operation, c.Origin); e != nil {
				return e
			}
		}
	}
	cursor := ""
	seenCursors := map[string]bool{}
	seen := map[string]bool{}
	var results []ac.Operation
	complete := false
	for page := 0; page < 24; page++ {
		p, e := c.Client.Catalog(ctx, c.Token, cursor)
		if e != nil {
			return e
		}
		for _, op := range p.Results {
			if seen[op.ID] {
				continue
			}
			seen[op.ID] = true
			if op.Pair.Deployment != c.Deployment {
				return ErrProvenance
			}
			results = append(results, op)
			if len(results) > 1024 {
				return ac.ErrInvalid
			}
		}
		if p.Next == "" {
			complete = true
			break
		}
		if seenCursors[p.Next] {
			return ac.ErrInvalid
		}
		seenCursors[p.Next] = true
		cursor = p.Next
	}
	if !complete {
		return ac.ErrInvalid
	}
	// Local retention uses admission time only as a storage policy, never as a
	// retirement freshness claim. Full scans discover every retained result.
	sort.Slice(results, func(i, j int) bool { return results[i].AcceptedAt.After(results[j].AcceptedAt) })
	counts := map[string]int{}
	for _, op := range results {
		counts[op.Kind]++
		if counts[op.Kind] > c.Keep && c.Outbox == nil {
			continue
		}
		if old, e := c.Store.Manifest(op.ID); e == nil {
			if !reflect.DeepEqual(old.Operation, op) || old.Origin != c.Origin {
				return ErrProvenance
			}
			pin, err := c.Store.Pin(op.ID)
			if err != nil {
				return err
			}
			pin.Close()
			if c.Outbox != nil {
				if e = c.Outbox.Capture(filepath.Join(c.Store.Dir, op.ID), old); e != nil {
					return e
				}
				if e = c.Outbox.LocalPublished(op.ID); e != nil {
					return e
				}
			}
			continue
		}
		if c.Outbox != nil {
			found, e := c.Outbox.ReplayLocal(op.ID, c.Store, op, c.Origin)
			if e != nil {
				return e
			}
			if found {
				continue
			}
		}
		if e = c.collect(ctx, op); e != nil {
			return e
		}
		if e = c.Store.Prune(c.Keep); e != nil {
			return e
		}
	}
	// Retry pins skipped during prior publication and apply retention reductions
	// even when every catalog result was already collected (or has rotated out).
	return c.Store.Prune(c.Keep)
}

func (c *Collector) collect(parent context.Context, op ac.Operation) error {
	ctx, cancel := context.WithTimeout(parent, 9*time.Minute)
	defer cancel()
	lease, e := ac.RandomID()
	if e != nil {
		return e
	}
	if _, e = c.Client.Lease(ctx, op.ID, lease, c.Token, false); e != nil {
		return e
	}
	defer func() {
		release, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_, _ = c.Client.Lease(release, op.ID, lease, c.Token, true)
	}()
	// Renewal failure cancels transfer; all staging remains private and is removed.
	renewed := make(chan error, 1)
	stop := make(chan struct{})
	go func() {
		interval := c.renewInterval
		if interval <= 0 {
			interval = time.Minute
		}
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				renewed <- ctx.Err()
				return
			case <-stop:
				renewed <- nil
				return
			case <-tick.C:
				if _, e := c.Client.Lease(ctx, op.ID, lease, c.Token, false); e != nil {
					cancel()
					renewed <- e
					return
				}
			}
		}
	}()
	defer func() { close(stop); <-renewed }()
	dir, e := os.MkdirTemp(c.Store.Dir, ".stage-")
	if e != nil {
		return ErrStorage
	}
	defer os.RemoveAll(dir)
	for _, a := range op.Artifacts {
		f, e := regular(filepath.Join(dir, a.ID), os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if e != nil {
			return e
		}
		transfer, done := context.WithTimeout(ctx, 110*time.Second)
		e = c.Client.Download(transfer, op.ID, lease, c.Token, a, f)
		done()
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return ErrStorage
		}
	}
	if e = checkMetadata(dir, op); e != nil {
		return e
	}
	if ctx.Err() != nil {
		return ac.ErrUnknown
	}
	m := Manifest{1, op, c.Origin, time.Now().UTC(), "https-transport; metadata-mac-required-before-restore"}
	if e = pack(dir, m); e != nil {
		return e
	}
	if c.Outbox != nil {
		if e = c.Outbox.Capture(dir, m); e != nil {
			return e
		}
	}
	// Existing publication must never be overwritten, even after a restart.
	final := filepath.Join(c.Store.Dir, op.ID)
	if _, e = os.Lstat(final); !os.IsNotExist(e) {
		return ErrStorage
	}
	if os.Rename(dir, final) != nil {
		return ErrStorage
	}
	if e = syncDir(c.Store.Dir); e != nil {
		return e
	}
	if c.Outbox != nil {
		return c.Outbox.LocalPublished(op.ID)
	}
	return nil
}
func checkMetadata(dir string, op ac.Operation) error {
	for _, a := range op.Artifacts {
		if a.Kind != "metadata" {
			continue
		}
		f, e := regular(filepath.Join(dir, a.ID), os.O_RDONLY)
		if e != nil {
			return e
		}
		raw, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		f.Close()
		if e != nil || len(raw) > 1<<20 {
			return ac.ErrInvalid
		}
		var envelope struct {
			Payload string `json:"payload"`
			MAC     string `json:"mac"`
		}
		if json.Unmarshal(raw, &envelope) != nil {
			return ac.ErrInvalid
		}
		decoded, e := base64.StdEncoding.DecodeString(envelope.Payload)
		if e != nil {
			return ac.ErrInvalid
		}
		var pair ac.Pair
		mac, macErr := hex.DecodeString(envelope.MAC)
		if json.Unmarshal(decoded, &pair) != nil || !reflect.DeepEqual(pair, *op.Pair) || macErr != nil || len(mac) != 32 {
			return ErrProvenance
		}
		return nil
	}
	return ac.ErrInvalid
}
