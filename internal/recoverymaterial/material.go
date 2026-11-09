// Package recoverymaterial coordinates every supported online secret writer.
// An unresolved remote mutation is durable and blocks new kit preparation.
package recoverymaterial

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/ekarpov/voptrack-server/internal/backupsettings"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type State struct {
	Revision string          `json:"revision"`
	Pending  map[string]bool `json:"pending"`
}

func ID() string {
	var b [24]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic("random source unavailable")
	}
	return hex.EncodeToString(b[:])
}
func Locked(dir string, fn func() error) error {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	f, e := os.OpenFile(filepath.Join(dir, ".recovery-material.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); e != nil {
		return e
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
func Read(dir string) (State, error) {
	var s State
	raw, e := os.ReadFile(filepath.Join(dir, ".recovery-material.json"))
	if os.IsNotExist(e) {
		return State{Revision: "initial", Pending: map[string]bool{}}, nil
	}
	if e != nil {
		return s, e
	}
	if e = json.Unmarshal(raw, &s); e != nil || s.Revision == "" || s.Pending == nil {
		return State{}, errors.New("invalid material state")
	}
	return s, nil
}

// Change holds the lock across the remote operation and all durable local writes.
// Retrying the same operation may reconcile its pending marker; a different
// successful operation never clears an earlier interrupted mutation.
func Change(dir, operation string, fn func() error) error {
	return ChangeIf(dir, operation, func(State) bool { return true }, fn)
}

// ChangeIf checks whether a mutation is needed under the same lock as its
// pending marker and durable writes. A skipped operation leaves state untouched.
func ChangeIf(dir, operation string, needed func(State) bool, fn func() error) error {
	return Locked(dir, func() error {
		s, e := Read(dir)
		if e != nil {
			return e
		}
		if !needed(s) {
			return nil
		}
		if e = preserve(dir); e != nil {
			return e
		}
		s.Pending[operation] = true
		s.Revision = ID()
		if e = backupsettings.WriteJSON(filepath.Join(dir, ".recovery-material.json"), s, 0600); e != nil {
			return e
		}
		if e = fn(); e != nil {
			return e
		}
		delete(s.Pending, operation)
		s.Revision = ID()
		return backupsettings.WriteJSON(filepath.Join(dir, ".recovery-material.json"), s, 0600)
	})
}

// Write atomically replaces a private file, syncing bytes and directory.
func Write(path string, raw []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".material-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(raw); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}

// Preserve current credentials before any remote change, even when no kit has
// yet been prepared. Older backups may contain the earlier issuer credential.
// Nothing here decides deletion/custodian policy or writes record documents.
func preserve(dir string) error {
	names := []string{}
	for _, sub := range []string{"", "studies"} {
		entries, e := os.ReadDir(filepath.Join(dir, sub))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		for _, entry := range entries {
			n := entry.Name()
			if (sub == "" && (n == "home.secret" || n == "analysis.secret" || n == "issuer-home-members.secret" || n == "console-state.secret" || n == "console-state.dataset")) || (sub == "studies" && strings.HasSuffix(n, ".issuer-secret")) {
				names = append(names, filepath.Join(sub, n))
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	revision := ID()
	for _, n := range names {
		source := filepath.Join(dir, n)
		f, e := os.OpenFile(source, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return e
		}
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
			f.Close()
			return errors.New("invalid recovery material")
		}
		raw, e := io.ReadAll(io.LimitReader(f, 65537))
		f.Close()
		if e != nil || len(raw) > 65536 {
			return errors.New("invalid recovery material")
		}
		target := filepath.Join(dir, ".recovery-history", revision, n)
		if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			return e
		}
		if e = Write(target, raw); e != nil {
			return e
		}
	}
	// Sync newly created ancestry as well as leaf files before the remote change.
	for _, path := range []string{filepath.Join(dir, ".recovery-history", revision, "studies"), filepath.Join(dir, ".recovery-history", revision), filepath.Join(dir, ".recovery-history"), dir} {
		d, e := os.Open(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		e = d.Sync()
		d.Close()
		if e != nil {
			return e
		}
	}

	return nil
}
