package backupsettings

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	sc "github.com/ekarpov/voptrack-server/internal/schedulerclient"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Live contains only public commands and sanitized observations, never authority.
// Unresolved commands are retained even after their provisional remote expiry.
type Live struct {
	Destination string          `json:"destination,omitempty"`
	Commands    []Command       `json:"commands"`
	Observation *sc.Observation `json:"observation"`
}
type Command struct {
	ID      string        `json:"id"`
	Payload sc.Payload    `json:"payload"`
	State   string        `json:"state"`
	Reason  string        `json:"reason,omitempty"`
	Outcome *sc.Operation `json:"outcome,omitempty"`
}

func (c Command) Terminal() bool {
	return c.State == "effective" || c.State == "failed" || c.State == "expired" || c.State == "scope_changed"
}
func (v Live) Current() *Command {
	if len(v.Commands) == 0 {
		return nil
	}
	return &v.Commands[len(v.Commands)-1]
}
func (v Live) Fingerprint() string {
	b, _ := json.Marshal(v.Commands)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func readLive(dir string) (Live, error) {
	var v Live
	f, e := os.OpenFile(filepath.Join(dir, "backup-live.json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(e) {
		return v, nil
	}
	if e != nil {
		return v, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 || info.Mode().Perm()&0077 != 0 {
		return v, errors.New("invalid live settings journal file")
	}
	b, e := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if e != nil {
		return v, e
	}
	if len(b) > 4<<20 || json.Unmarshal(b, &v) != nil || len(v.Commands) > 1024 {
		return Live{}, errors.New("invalid live settings journal")
	}
	for i, c := range v.Commands {
		if !sc.ValidID(c.ID) || !c.Payload.Valid() || (!c.Terminal() && i != len(v.Commands)-1) {
			return Live{}, errors.New("invalid live command")
		}
		switch c.State {
		case "queued", "uncertain", "pending", "effective", "failed", "expired", "scope_changed":
		default:
			return Live{}, errors.New("invalid live command state")
		}
		if c.Outcome != nil && c.Outcome.Validate(c.ID, c.Payload) != nil {
			return Live{}, errors.New("invalid live outcome")
		}
	}
	if v.Observation != nil && v.Observation.Validate(v.Observation.ObservedAt) != nil {
		return Live{}, errors.New("invalid saved observation")
	}
	return v, nil
}
func ReadLive(dir string) (v Live, e error) {
	e = locked(dir, false, func() error { var err error; v, err = readLive(dir); return err })
	return
}

// WithLive serializes all local administrators and holds the lock through the
// request/result write. The callback must call save BEFORE any mutation request.
func WithLive(dir string, fn func(*Live, func() error) error) error {
	return locked(dir, true, func() error {
		v, e := readLive(dir)
		if e != nil {
			return e
		}
		return fn(&v, func() error { return WriteJSON(filepath.Join(dir, "backup-live.json"), v, 0600) })
	})
}
func (v *Live) Begin(expected string, p sc.Payload, now time.Time) error {
	if v.Fingerprint() != expected || !p.Valid() || len(v.Commands) >= 1024 {
		return ErrStale
	}
	if c := v.Current(); c != nil && !c.Terminal() {
		return ErrStale
	}
	id, e := sc.NewID(sc.Scope(p.ExpectedRevision), now)
	if e != nil {
		return e
	}
	v.Commands = append(v.Commands, Command{ID: id, Payload: p, State: "queued"})
	return nil
}

// CurrentExport is the common readiness/preparation/delivery invariant. A
// terminal command is not evidence that the saved observation includes it.
// Inspect the whole retained command history: a later rejected command must
// not conceal an earlier effective acknowledgement. Revisions are opaque;
// later external revisions are allowed, but known superseded ones are not.
func (v Live) CurrentExport(now time.Time) (*sc.Export, error) {
	o := v.Observation
	if o == nil || o.Validate(now) != nil || !o.Available || o.PendingRequestID != nil || o.Export == nil {
		return nil, ErrStale
	}
	for _, c := range v.Commands {
		if !c.Terminal() {
			return nil, ErrStale
		}
		if c.State != "effective" {
			continue
		}
		ack := c.Outcome
		if ack == nil || ack.State != "effective" || ack.Validate(c.ID, c.Payload) != nil || ack.EffectiveAt == nil || o.ObservedAt.Before(*ack.EffectiveAt) {
			return nil, ErrStale
		}
		if o.Export.Revision == c.Payload.ExpectedRevision {
			return nil, ErrStale
		}
		if o.Export.Revision == ack.Revision {
			if o.Export.EveryHours != c.Payload.EveryHours || o.Export.Keep != c.Payload.Keep {
				return nil, ErrStale
			}
		} else if o.ObservedAt.Equal(*ack.EffectiveAt) {
			// Equal timestamps do not order different opaque revisions.
			return nil, ErrStale
		}
	}
	return o.Export, nil
}

// StartupValues reconciles only a credential-free authoritative observation.
// Durable Tessera policy remains authoritative even if the generated defaults
// are older. Uncertainty blocks init rather than generating a silent rollback.
func StartupValues(dir string, v Values) (Values, error) {
	live, e := ReadLive(dir)
	if e != nil {
		return Values{}, e
	}
	if c := live.Current(); c != nil && !c.Terminal() {
		return Values{}, errors.New("unresolved live settings; reconcile with current administrator")
	}
	if live.Destination != "" && live.Destination != v.Destination {
		return v, nil
	}
	for _, c := range live.Commands {
		if c.State == "effective" {
			if live.Observation == nil {
				return Values{}, errors.New("effective export not yet observed")
			}
			// Startup uses retained acknowledged values, not a claim that this
			// observation is fresh now. Apply the same acknowledgement fence.
			if _, e := live.CurrentExport(live.Observation.ObservedAt); e != nil {
				return Values{}, errors.New("effective export not yet observed")
			}
			break
		}
	}
	if o := live.Observation; o != nil {
		if !o.Available || o.Settings == nil || o.PendingRequestID != nil {
			return Values{}, errors.New("effective settings unavailable")
		}
		if o.Settings.EveryHours < 1 || o.Settings.EveryHours > 8760 || o.Settings.Keep > 1000 {
			return Values{}, errors.New("effective settings outside product bounds")
		}
		v.EveryHours = o.Settings.EveryHours
		v.Keep = o.Settings.Keep
	}
	return v, nil
}
