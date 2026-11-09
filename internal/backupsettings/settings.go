// Package backupsettings stores only a constrained deployment backup request.
// It contains no credentials and never starts or controls host processes.
package backupsettings

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

var ErrStale = errors.New("settings changed")

type Values struct {
	Destination string `json:"destination"`
	EveryHours  int    `json:"every_hours"`
	Keep        int    `json:"keep"`
}

func (v Values) Valid() bool {
	return (v.Destination == "local" || v.Destination == "reserve") && v.EveryHours >= 1 && v.EveryHours <= 8760 && v.Keep >= 1 && v.Keep <= 1000
}
func (v Values) Directory() string {
	if v.Destination == "reserve" {
		return "/var/lib/voptrack-backups/reserve"
	}
	return "/var/lib/voptrack-backups"
}

type Request struct {
	Values
	Revision string `json:"revision"`
}
type Generated struct {
	Values
	RequestRevision string    `json:"request_revision"`
	At              time.Time `json:"generated_at"`
}

func locked(dir string, exclusive bool, fn func() error) error {
	f, err := os.OpenFile(filepath.Join(dir, ".backup-settings.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	op := syscall.LOCK_SH
	if exclusive {
		op = syscall.LOCK_EX
	}
	if err = syscall.Flock(int(f.Fd()), op); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
func read(dir string) (Request, error) {
	var r Request
	raw, err := os.ReadFile(filepath.Join(dir, "backup-settings.json"))
	if os.IsNotExist(err) {
		return Request{Revision: "none"}, nil
	}
	if err != nil {
		return r, err
	}
	if json.Unmarshal(raw, &r) != nil || !r.Valid() || !validRevision(r.Revision) || r.Revision == "none" {
		return Request{}, errors.New("invalid backup settings")
	}
	return r, nil
}
func Read(dir string) (r Request, err error) {
	err = locked(dir, false, func() error { var e error; r, e = read(dir); return e })
	return
}
func WriteJSON(path string, v any, mode os.FileMode) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".backup-settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if _, err = f.Write(raw); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func Save(dir, expected string, v Values) error {
	if !v.Valid() {
		return errors.New("invalid backup settings")
	}
	return locked(dir, true, func() error {
		old, err := read(dir)
		if err != nil {
			return err
		}
		if old.Revision != expected {
			return ErrStale
		}
		var nonce [16]byte
		if _, err = rand.Read(nonce[:]); err != nil {
			return err
		}
		return WriteJSON(filepath.Join(dir, "backup-settings.json"), Request{v, hex.EncodeToString(nonce[:])}, 0600)
	})
}
func ReadGenerated(path string) (g Generated, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return g, err
	}
	if json.Unmarshal(raw, &g) != nil || !g.Valid() || !validRevision(g.RequestRevision) || g.At.IsZero() {
		return Generated{}, errors.New("invalid generated backup settings")
	}
	return g, nil
}

func validRevision(s string) bool {
	if s == "none" {
		return true
	}
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
