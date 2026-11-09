package acquisitionclient

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Intent deliberately has no token/password/key field. Acquired means producer
// completion; published is saved only after observing a complete pinned local bundle.
type Intent struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	State   string    `json:"state"`
	Created time.Time `json:"created_at"`
}

var ErrBusy = errors.New("backup intent journal busy")
var ErrJournal = errors.New("backup intent journal unavailable")

// Journal serializes processes, uses a private directory and commits before a
// caller may perform PUT. It has no scheduling or authorization policy.
type Journal struct {
	mu   sync.Mutex
	dir  string
	lock *os.File
}

func OpenJournal(dir string) (*Journal, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, ErrJournal
	}
	st, e := os.Lstat(dir)
	if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 {
		return nil, ErrJournal
	}
	f, e := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if e != nil {
		return nil, ErrJournal
	}
	if info, statErr := f.Stat(); statErr != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, ErrJournal
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, ErrBusy
	}
	// Reclaim only our interrupted atomic-write temporaries while holding the
	// process lock. Durable current pointers and intent IDs are never guessed.
	entries, err := os.ReadDir(dir)
	if err != nil {
		f.Close()
		return nil, ErrJournal
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".intent-") || strings.HasPrefix(entry.Name(), ".current-") {
			if os.Remove(filepath.Join(dir, entry.Name())) != nil {
				f.Close()
				return nil, ErrJournal
			}
		}
	}
	return &Journal{dir: dir, lock: f}, nil
}
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.lock == nil {
		return nil
	}
	e := j.lock.Close()
	j.lock = nil
	return e
}
func (j *Journal) Create(kind string, now time.Time) (Intent, error) {
	if kind != "backup" && kind != "evidence" {
		return Intent{}, ErrInvalid
	}
	id, e := NewID(now)
	if e != nil {
		return Intent{}, e
	}
	v := Intent{id, kind, "queued", now.UTC()}
	if e = j.Save(v); e != nil {
		return Intent{}, e
	}
	return v, nil
}
func (j *Journal) Save(v Intent) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.lock == nil {
		return ErrJournal
	}
	if !validIntent(v) {
		return ErrInvalid
	}
	if !ValidID(v.ID) || (v.Kind != "backup" && v.Kind != "evidence") {
		return ErrInvalid
	}
	switch v.State {
	case "queued", "creating", "unknown", "failed", "cancelled", "expired", "acquired", "publishing", "published":
	default:
		return ErrInvalid
	}
	raw, e := json.Marshal(v)
	if e != nil {
		return ErrJournal
	}
	f, e := os.CreateTemp(j.dir, ".intent-")
	if e != nil {
		return ErrJournal
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(raw); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil {
		return ErrJournal
	}
	if e = os.Rename(name, filepath.Join(j.dir, v.ID+".json")); e != nil {
		return ErrJournal
	}
	d, e := os.Open(j.dir)
	if e != nil {
		return ErrJournal
	}
	defer d.Close()
	if d.Sync() != nil {
		return ErrJournal
	}
	return nil
}
func (j *Journal) Load(id string) (Intent, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.lock == nil {
		return Intent{}, ErrJournal
	}
	if !ValidID(id) {
		return Intent{}, ErrInvalid
	}
	f, e := os.OpenFile(filepath.Join(j.dir, id+".json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return Intent{}, ErrJournal
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 4096 || info.Mode().Perm()&0077 != 0 {
		return Intent{}, ErrJournal
	}
	var v Intent
	raw, readErr := io.ReadAll(io.LimitReader(f, 4097))
	if readErr != nil || len(raw) > 4096 || json.Unmarshal(raw, &v) != nil || v.ID != id || !validIntent(v) {
		return Intent{}, ErrJournal
	}
	return v, nil
}

func validIntent(v Intent) bool {
	if !ValidID(v.ID) || v.Created.IsZero() || (v.Kind != "backup" && v.Kind != "evidence") {
		return false
	}
	switch v.State {
	case "queued", "creating", "unknown", "failed", "cancelled", "expired", "acquired", "publishing", "published":
		return true
	}
	return false
}

// ForgetOthers bounds the single-current manual journal after its pointer is
// durably replaced. The caller must already hold this Journal's process lock.
func (j *Journal) ForgetOthers(id string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.lock == nil || !ValidID(id) {
		return ErrJournal
	}
	entries, e := os.ReadDir(j.dir)
	if e != nil {
		return ErrJournal
	}
	for _, entry := range entries {
		name := entry.Name()
		if len(name) < 6 || name[len(name)-5:] != ".json" {
			continue
		}
		other := name[:len(name)-5]
		if other != id && ValidID(other) {
			if os.Remove(filepath.Join(j.dir, name)) != nil {
				return ErrJournal
			}
		}
	}
	dir, e := os.Open(j.dir)
	if e != nil {
		return ErrJournal
	}
	defer dir.Close()
	if dir.Sync() != nil {
		return ErrJournal
	}
	return nil
}
