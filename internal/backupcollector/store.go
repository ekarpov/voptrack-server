// Package backupcollector collects complete Tessera results without producer,
// record or key authority. A bundle authenticates its transport provenance, not
// the metadata MAC; the supported key-based verifier is mandatory before restore.
package backupcollector

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	ac "github.com/ekarpov/voptrack-server/internal/acquisitionclient"
)

var ErrStorage = errors.New("backup collection storage unavailable")
var ErrProvenance = errors.New("backup deployment provenance mismatch")

const maxBundle int64 = 275 << 20

type Manifest struct {
	Version        int          `json:"version"`
	Operation      ac.Operation `json:"operation"`
	Origin         string       `json:"trusted_https_origin"`
	Collected      time.Time    `json:"collected_at"`
	Authentication string       `json:"authentication"`
}

type Store struct{ Dir string }
type Bundle struct {
	Manifest
	Size int64
}

func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return ErrStorage
	}
	defer f.Close()
	if f.Sync() != nil {
		return ErrStorage
	}
	return nil
}
func regular(path string, flags int) (*os.File, error) {
	f, e := os.OpenFile(path, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if e != nil {
		return nil, ErrStorage
	}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, ErrStorage
	}
	return f, nil
}
func (s Store) Init() error {
	if os.MkdirAll(s.Dir, 0700) != nil {
		return ErrStorage
	}
	st, e := os.Lstat(s.Dir)
	if e != nil || !st.IsDir() || st.Mode().Perm()&0077 != 0 {
		return ErrStorage
	}
	return nil
}
func (s Store) lock(name string, exclusive bool) (*os.File, error) {
	f, e := regular(filepath.Join(s.Dir, name), os.O_CREATE|os.O_RDWR)
	if e != nil {
		return nil, e
	}
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	if syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, ErrStorage
	}
	return f, nil
}

// Writer is a process-wide lock; only this owner may collect or reclaim staging.
func (s Store) Writer() (*os.File, error) {
	if e := s.Init(); e != nil {
		return nil, e
	}
	return s.lock(".writer", true)
}
func (s Store) Manifest(id string) (Manifest, error) {
	var m Manifest
	if !ac.ValidID(id) {
		return m, ErrStorage
	}
	root, e := os.OpenRoot(s.Dir)
	if e != nil {
		return m, ErrStorage
	}
	defer root.Close()
	f, e := root.OpenFile(id+"/manifest.json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return m, ErrStorage
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return m, ErrStorage
	}
	raw, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &m) != nil || m.Version != 1 || m.Operation.ID != id || m.Operation.State != "complete" || ac.Validate(m.Operation) != nil || m.Authentication != "https-transport; metadata-mac-required-before-restore" {
		return m, ErrStorage
	}
	return m, nil
}
func (s Store) List() ([]Bundle, error) {
	entries, e := os.ReadDir(s.Dir)
	if e != nil {
		return nil, ErrStorage
	}
	var out []Bundle
	for _, v := range entries {
		if !ac.ValidID(v.Name()) {
			continue
		}
		f, e := s.Pin(v.Name())
		if e != nil {
			// A retired, damaged or legacy partially deleted entry must not hide
			// independent intact backups. Pin verifies before availability.
			continue
		}
		m, e := s.Manifest(v.Name())
		if e != nil {
			f.Close()
			continue
		}
		st, e := f.Stat()
		f.Close()
		if e != nil {
			return nil, ErrStorage
		}
		out = append(out, Bundle{m, st.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Operation.AcceptedAt.After(out[j].Operation.AcceptedAt) })
	return out, nil
}

// Pin takes a shared lock on the exact immutable tar inode. Pruning obtains an
// exclusive lock on that inode, so it cannot replace or remove an active download.
func (s Store) Pin(id string) (*os.File, error) {
	if !ac.ValidID(id) {
		return nil, ErrStorage
	}
	root, e := os.OpenRoot(s.Dir)
	if e != nil {
		return nil, ErrStorage
	}
	defer root.Close()
	f, e := root.OpenFile(id+"/backup.tar", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, ErrStorage
	}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > maxBundle || syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, ErrStorage
	}
	m, e := s.Manifest(id)
	if e != nil || verifyPacked(f, m) != nil {
		f.Close()
		return nil, ErrStorage
	}
	// Readers complete the parent-directory durability barrier themselves.
	// A collector killed after rename cannot expose an unflushed publication;
	// the pinned inode also prevents pruning between this barrier and delivery.
	parent, err := root.Open(".")
	if err != nil {
		f.Close()
		return nil, ErrStorage
	}
	err = parent.Sync()
	closeErr := parent.Close()
	if err != nil || closeErr != nil {
		f.Close()
		return nil, ErrStorage
	}
	return f, nil
}

// verifyPacked checks the original member digests and the complete canonical tar,
// including its embedded manifest, headers, padding and end markers. This uses
// bounded memory and also supports bundles published before this check existed.
// The same locked descriptor is rewound for delivery; no pathname is reopened.
// Local integrity does not replace the key-based metadata MAC check at restore.
func verifyPacked(f *os.File, m Manifest) error {
	raw, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return ErrStorage
	}
	actual, canonical := sha256.New(), sha256.New()
	input := io.TeeReader(io.LimitReader(f, maxBundle+1), actual)
	tr := tar.NewReader(input)
	tw := tar.NewWriter(canonical)
	names := []string{"manifest.json"}
	for _, a := range m.Operation.Artifacts {
		names = append(names, a.ID)
	}
	for i, name := range names {
		h, e := tr.Next()
		if e != nil || h.Name != name || h.Typeflag != tar.TypeReg {
			return ErrStorage
		}
		size := int64(len(raw))
		if i > 0 {
			size = m.Operation.Artifacts[i-1].Size
		}
		if h.Size != size {
			return ErrStorage
		}
		if tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: size, ModTime: m.Collected}) != nil {
			return ErrStorage
		}
		if i == 0 {
			member, e := io.ReadAll(io.LimitReader(tr, size+1))
			if e != nil || !bytes.Equal(member, raw) {
				return ErrStorage
			}
			if _, e = tw.Write(member); e != nil {
				return ErrStorage
			}
		} else {
			digest := sha256.New()
			if n, e := io.Copy(io.MultiWriter(tw, digest), tr); e != nil || n != size || fmt.Sprintf("%x", digest.Sum(nil)) != m.Operation.Artifacts[i-1].SHA256 {
				return ErrStorage
			}
		}
	}
	if _, e = tr.Next(); e != io.EOF {
		return ErrStorage
	}
	// tar.Reader stops at the end markers; include any unexpected trailing bytes.
	if _, e = io.Copy(io.Discard, input); e != nil || tw.Close() != nil || !bytes.Equal(actual.Sum(nil), canonical.Sum(nil)) {
		return ErrStorage
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return ErrStorage
	}
	return nil
}

// retire removes a directory from the published namespace atomically, then
// durably records that removal BEFORE any member can be deleted. The caller
// holds Writer and, for a complete bundle, the tar's exclusive inode lock.
func (s Store) retire(id string) (string, error) {
	retired := filepath.Join(s.Dir, ".retired-"+id)
	if os.Rename(filepath.Join(s.Dir, id), retired) != nil {
		return "", ErrStorage
	}
	if syncDir(s.Dir) != nil {
		return "", ErrStorage
	}
	return retired, nil
}

func (s Store) Cleanup() error {
	entries, e := os.ReadDir(s.Dir)
	if e != nil {
		return ErrStorage
	}
	for _, v := range entries {
		// Reconcile the old in-place deletion crash state as well as new
		// retirements. No new publisher runs while this caller owns Writer.
		if ac.ValidID(v.Name()) && v.IsDir() {
			if _, e := os.Lstat(filepath.Join(s.Dir, v.Name(), "backup.tar")); os.IsNotExist(e) {
				if _, e = s.retire(v.Name()); e != nil {
					return e
				}
			}
		}
	}
	// A previous process may have died between rename and the directory sync.
	// Establish that barrier before deleting any recovered retirement's members.
	if syncDir(s.Dir) != nil {
		return ErrStorage
	}
	// Rescan so legacy entries retired above are reclaimed in this pass too.
	entries, e = os.ReadDir(s.Dir)
	if e != nil {
		return ErrStorage
	}
	for _, v := range entries {
		if strings.HasPrefix(v.Name(), ".stage-") || strings.HasPrefix(v.Name(), ".status-") || strings.HasPrefix(v.Name(), ".retired-") {
			if os.RemoveAll(filepath.Join(s.Dir, v.Name())) != nil {
				return ErrStorage
			}
		}
	}
	return syncDir(s.Dir)
}

// Prune keeps backup and evidence results independently; the caller holds Writer.
func (s Store) Prune(keep int) error {
	if keep < 1 || keep > 32 {
		return ErrStorage
	}
	all, e := s.List()
	if e != nil {
		return e
	}
	counts := map[string]int{}
	for _, b := range all {
		kind := b.Operation.Kind
		counts[kind]++
		if counts[kind] <= keep {
			continue
		}
		f, e := regular(filepath.Join(s.Dir, b.Operation.ID, "backup.tar"), os.O_RDONLY)
		if e != nil {
			return e
		}
		if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
			f.Close()
			continue
		}
		retired, retireErr := s.retire(b.Operation.ID)
		if retireErr != nil {
			f.Close()
			return retireErr
		}
		e = os.RemoveAll(retired)
		f.Close()
		if e != nil {
			return ErrStorage
		}
	}
	return syncDir(s.Dir)
}
func pack(dir string, m Manifest) error {
	raw, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return ErrStorage
	}
	if os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0600) != nil {
		return ErrStorage
	}
	names := []string{"manifest.json"}
	for _, a := range m.Operation.Artifacts {
		names = append(names, a.ID)
	}
	f, e := regular(filepath.Join(dir, "backup.tar"), os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if e != nil {
		return e
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	for _, name := range names {
		member, e := regular(filepath.Join(dir, name), os.O_RDONLY)
		if e != nil {
			return e
		}
		st, e := member.Stat()
		if e == nil {
			e = tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: st.Size(), ModTime: m.Collected})
			if e == nil {
				_, e = io.CopyN(tw, member, st.Size())
			}
		}
		member.Close()
		if e != nil {
			return ErrStorage
		}
	}
	if tw.Close() != nil || f.Sync() != nil {
		return ErrStorage
	}
	// Only the immutable tar and its bounded listing manifest remain locally.
	for _, a := range m.Operation.Artifacts {
		if os.Remove(filepath.Join(dir, a.ID)) != nil {
			return ErrStorage
		}
	}
	mf, e := regular(filepath.Join(dir, "manifest.json"), os.O_RDONLY)
	if e != nil {
		return e
	}
	e = mf.Sync()
	mf.Close()
	if e != nil {
		return ErrStorage
	}
	return syncDir(dir)
}
