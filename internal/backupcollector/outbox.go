package backupcollector

// The outbox is a keyless, durable handoff. A collector captures exact packed
// bytes BEFORE local publication can expose them to rotation. Network workers
// never take Store.Writer or hold the queue lock during remote I/O.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	ac "github.com/ekarpov/voptrack-server/internal/acquisitionclient"
	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
)

const DefaultOutboxBudget int64 = 5 << 30
const MaxBundleBytes int64 = maxBundle

type Outbox struct {
	Dir    string
	Budget int64
}
type Obligation struct {
	Version        int
	Manifest       Manifest
	SHA256         string
	Size           int64
	Queued         time.Time
	LocalPublished bool
}

func (q Outbox) limit() int64 {
	if q.Budget == 0 {
		return DefaultOutboxBudget
	}
	return q.Budget
}
func PrivateDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrStorage
	}
	for p := path; ; p = filepath.Dir(p) {
		st, e := os.Lstat(p)
		if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return ErrStorage
		}
		if p == path && st.Mode().Perm()&0077 != 0 {
			return ErrStorage
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
func (q Outbox) Locked(fn func() error) error {
	if q.limit() < 2*maxBundle || PrivateDirectory(q.Dir) != nil {
		return ErrStorage
	}
	f, e := regular(filepath.Join(q.Dir, ".queue-lock"), os.O_CREATE|os.O_RDWR)
	if e != nil {
		return e
	}
	defer f.Close()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX) != nil {
		return ErrStorage
	}
	// A prior process may have stopped after capture rename but before syncing
	// this parent. Observers must establish that barrier before publication or
	// acknowledgement can permit any payload to be released.
	if syncDir(q.Dir) != nil {
		return ErrStorage
	}
	return fn()
}
func ReadPrivateJSON(path string, v any) error {
	f, e := regular(path, os.O_RDONLY)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || st.Size() < 1 || st.Size() > 2<<20 {
		return ErrStorage
	}
	d := json.NewDecoder(io.LimitReader(f, (2<<20)+1))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return ErrStorage
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ErrStorage
	}
	return nil
}
func (q Outbox) record(id string) (Obligation, error) {
	var r Obligation
	if !ac.ValidID(id) || ReadPrivateJSON(filepath.Join(q.Dir, id, "obligation.json"), &r) != nil {
		return r, ErrStorage
	}
	if r.Version != 1 || r.Manifest.Operation.ID != id || r.Manifest.Operation.Pair == nil || r.Size < 1 || r.Size > maxBundle || len(r.SHA256) != 64 || r.Queued.IsZero() {
		return Obligation{}, ErrStorage
	}
	if _, e := hex.DecodeString(r.SHA256); e != nil {
		return Obligation{}, ErrStorage
	}
	return r, nil
}
func (q Outbox) Record(id string) (r Obligation, e error) {
	e = q.Locked(func() error { r, e = q.record(id); return e })
	return
}
func (q Outbox) Records() (out []Obligation, e error) {
	e = q.Locked(func() error {
		entries, e := os.ReadDir(q.Dir)
		if e != nil {
			return ErrStorage
		}
		if len(entries) > 20002 {
			return ErrStorage
		}
		for _, v := range entries {
			if !ac.ValidID(v.Name()) {
				continue
			}
			r, e := q.record(v.Name())
			if e != nil {
				return e
			}
			out = append(out, r)
			if len(out) > 10000 {
				return ErrStorage
			}
		}
		return nil
	})
	return
}
func (q Outbox) Payload(r Obligation) (*os.File, error) {
	if !ac.ValidID(r.Manifest.Operation.ID) {
		return nil, ErrStorage
	}
	f, e := regular(filepath.Join(q.Dir, r.Manifest.Operation.ID, "backup.tar"), os.O_RDONLY)
	if e != nil {
		return nil, e
	}
	st, e := f.Stat()
	if e != nil || st.Size() != r.Size {
		f.Close()
		return nil, ErrStorage
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_SH) != nil {
		f.Close()
		return nil, ErrStorage
	}
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil || hex.EncodeToString(h.Sum(nil)) != r.SHA256 {
		f.Close()
		return nil, ErrStorage
	}
	if _, e = f.Seek(0, 0); e != nil {
		f.Close()
		return nil, ErrStorage
	}
	if verifyPacked(f, r.Manifest) != nil {
		f.Close()
		return nil, ErrStorage
	}
	_, e = f.Seek(0, 0)
	if e != nil {
		f.Close()
		return nil, ErrStorage
	}
	return f, nil
}

// Capture is called with collector Writer held, before local rename. A retry
// must use the first immutable captured bytes, including their collected time.
func (q Outbox) Capture(dir string, m Manifest) error {
	return q.Locked(func() error {
		if old, e := q.record(m.Operation.ID); e == nil {
			if !reflect.DeepEqual(old.Manifest.Operation, m.Operation) || old.Manifest.Origin != m.Origin {
				return ErrProvenance
			}
			return nil
		} else if _, e = os.Lstat(filepath.Join(q.Dir, m.Operation.ID)); !os.IsNotExist(e) {
			return ErrStorage
		}
		if !ac.ValidID(m.Operation.ID) || m.Operation.Pair == nil {
			return ErrStorage
		}
		f, e := regular(filepath.Join(dir, "backup.tar"), os.O_RDONLY)
		if e != nil {
			return e
		}
		defer f.Close()
		if verifyPacked(f, m) != nil {
			return ErrStorage
		}
		st, e := f.Stat()
		if e != nil {
			return ErrStorage
		}
		entries, e := os.ReadDir(q.Dir)
		if e != nil {
			return ErrStorage
		}
		var used int64
		history := 0
		for _, v := range entries {
			// No network operation holds this lock. An interrupted unpublished capture
			// is never a durable obligation; reclaim only our fixed temporary namespace.
			if strings.HasPrefix(v.Name(), ".capture-") {
				if !v.IsDir() {
					return ErrStorage
				}
				if os.RemoveAll(filepath.Join(q.Dir, v.Name())) != nil {
					return ErrStorage
				}
				continue
			}
			if !ac.ValidID(v.Name()) {
				continue
			}
			history++
			if history >= 10000 {
				return ErrStorage
			}
			if st, e := os.Lstat(filepath.Join(q.Dir, v.Name(), "backup.tar")); e == nil {
				if !st.Mode().IsRegular() {
					return ErrStorage
				}
				if st.Size() > q.limit()-used {
					return ErrStorage
				}
				used += st.Size()
			} else if !os.IsNotExist(e) {
				return ErrStorage
			}
		}
		if st.Size() > q.limit()-used {
			return ErrStorage
		}
		stage, e := os.MkdirTemp(q.Dir, ".capture-")
		if e != nil {
			return ErrStorage
		}
		defer os.RemoveAll(stage)
		if _, e = f.Seek(0, 0); e != nil {
			return ErrStorage
		}
		out, e := regular(filepath.Join(stage, "backup.tar"), os.O_CREATE|os.O_EXCL|os.O_WRONLY)
		if e != nil {
			return e
		}
		h := sha256.New()
		n, e := io.Copy(io.MultiWriter(out, h), io.LimitReader(f, maxBundle+1))
		se := out.Sync()
		ce := out.Close()
		if e != nil || se != nil || ce != nil || n != st.Size() {
			return ErrStorage
		}
		r := Obligation{Version: 1, Manifest: m, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n, Queued: time.Now().UTC()}
		raw, _ := json.Marshal(r)
		if len(raw) > 65536 {
			return ErrStorage
		}
		if recoverymaterial.Write(filepath.Join(stage, "obligation.json"), raw) != nil {
			return ErrStorage
		}
		if syncDir(stage) != nil || os.Rename(stage, filepath.Join(q.Dir, m.Operation.ID)) != nil {
			return ErrStorage
		}
		return syncDir(q.Dir)
	})
}

// ReplayLocal finishes a capture-before-publication crash without re-downloading
// or changing packed bytes. Published tombstones avoid re-queuing rotated results.
func (q Outbox) ReplayLocal(id string, s Store, op ac.Operation, origin string) (found bool, e error) {
	e = q.Locked(func() error {
		r, err := q.record(id)
		if err != nil {
			if _, e := os.Lstat(filepath.Join(q.Dir, id)); os.IsNotExist(e) {
				return nil
			}
			return ErrStorage
		}
		found = true
		if !reflect.DeepEqual(r.Manifest.Operation, op) || r.Manifest.Origin != origin {
			return ErrProvenance
		}
		if r.LocalPublished {
			return nil
		}
		// A crash can follow publication rename/fsync but precede the outbox ack.
		// Reconcile only the exact existing publication; never replace a target.
		if _, err := os.Lstat(filepath.Join(s.Dir, id)); err == nil {
			m, err := s.Manifest(id)
			if err != nil || !reflect.DeepEqual(m, r.Manifest) {
				return ErrProvenance
			}
			f, err := s.Pin(id)
			if err != nil {
				return err
			}
			h := sha256.New()
			n, err := io.Copy(h, f)
			f.Close()
			if err != nil || n != r.Size || hex.EncodeToString(h.Sum(nil)) != r.SHA256 {
				return ErrProvenance
			}
			return q.localPublished(id)
		} else if !os.IsNotExist(err) {
			return ErrStorage
		}
		f, e := q.Payload(r)
		if e != nil {
			return e
		}
		defer f.Close()
		stage, e := os.MkdirTemp(s.Dir, ".stage-")
		if e != nil {
			return ErrStorage
		}
		defer os.RemoveAll(stage)
		out, e := regular(filepath.Join(stage, "backup.tar"), os.O_CREATE|os.O_EXCL|os.O_WRONLY)
		if e != nil {
			return e
		}
		_, e = io.Copy(out, f)
		se := out.Sync()
		ce := out.Close()
		if e != nil || se != nil || ce != nil {
			return ErrStorage
		}
		raw, _ := json.MarshalIndent(r.Manifest, "", "  ")
		if recoverymaterial.Write(filepath.Join(stage, "manifest.json"), raw) != nil {
			return ErrStorage
		}
		if _, e = os.Lstat(filepath.Join(s.Dir, id)); !os.IsNotExist(e) {
			return ErrStorage
		}
		if syncDir(stage) != nil || os.Rename(stage, filepath.Join(s.Dir, id)) != nil || syncDir(s.Dir) != nil {
			return ErrStorage
		}
		return q.localPublished(id)
	})
	return
}
func (q Outbox) localPublished(id string) error {
	r, e := q.record(id)
	if e != nil {
		return e
	}
	r.LocalPublished = true
	raw, _ := json.Marshal(r)
	return recoverymaterial.Write(filepath.Join(q.Dir, id, "obligation.json"), raw)
}
func (q Outbox) LocalPublished(id string) error {
	return q.Locked(func() error { return q.localPublished(id) })
}

// Release removes only a remotely acknowledged payload, never its identity or
// obligation. The caller has durably verified the destination binding first.
// Requiring local publication also preserves capture-before-rename replay.
func (q Outbox) Release(r Obligation) error {
	return q.Locked(func() error {
		current, e := q.record(r.Manifest.Operation.ID)
		if e != nil {
			return e
		}
		if current.SHA256 != r.SHA256 {
			return ErrProvenance
		}
		if !current.LocalPublished {
			return nil
		}
		path := filepath.Join(q.Dir, r.Manifest.Operation.ID, "backup.tar")
		f, e := regular(path, os.O_RDONLY)
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			if _, err := os.Lstat(path); os.IsNotExist(err) {
				return nil
			}
			return ErrStorage
		}
		defer f.Close()
		if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
			return nil
		}
		if os.Remove(path) != nil {
			return ErrStorage
		}
		return syncDir(filepath.Dir(path))
	})
}
func (q Outbox) HasPayload(id string) bool {
	if !ac.ValidID(id) {
		return false
	}
	st, e := os.Lstat(filepath.Join(q.Dir, id, "backup.tar"))
	return e == nil && st.Mode().IsRegular()
}

// No arbitrary namespace is accepted in remote object names.
func OpaqueID(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}
func SafeLabel(s string) bool {
	return len(s) > 0 && len(s) <= 80 && !strings.ContainsAny(s, "\r\n\x00")
}
