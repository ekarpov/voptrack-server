// Package recoverykit owns private, immutable recovery-material revisions.
// It never creates backups or mutates Tessera, and holds no human credentials.
package recoverykit

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/ekarpov/voptrack-server/internal/backupsettings"
	"github.com/ekarpov/voptrack-server/internal/identitybrand"
	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
	sc "github.com/ekarpov/voptrack-server/internal/schedulerclient"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const MaxBytes = 8 << 20

var ErrUnavailable = errors.New("recovery kit unavailable")

type Store struct {
	Root, Private string
	Now           func() time.Time
}
type Metadata struct {
	SettingsRevision string    `json:"settings_revision,omitempty"`
	Revision         string    `json:"revision"`
	State            string    `json:"state"`
	Material         string    `json:"material_revision"`
	Configuration    string    `json:"configuration"`
	Compatibility    string    `json:"compatibility"`
	Prepared         time.Time `json:"prepared_at"`
	SHA256           string    `json:"sha256"`
	Size             int       `json:"size"`
}
type manifest struct {
	Format   string            `json:"format"`
	Metadata Metadata          `json:"metadata"`
	Members  map[string]string `json:"members"`
}

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s Store) path(name string) string { return filepath.Join(s.Root, "var/lib", name) }
func digest(raw []byte) string          { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func ValidID(id string) bool {
	if len(id) != 48 {
		return false
	}
	_, e := hex.DecodeString(id)
	return e == nil
}

// All local callers obey the same fixed ordering: maintenance, material, kit.
// The exporter joins the shared maintenance guard; it never bypasses EX users.
func (s Store) locked(fn func() error) error {
	f, e := os.OpenFile(filepath.Join(s.path("voptrack-public"), "maintenance.lock"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return e
	}
	defer f.Close()
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); e != nil {
		return e
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return recoverymaterial.Locked(s.path("voptrack-console"), func() error { return recoverymaterial.Locked(s.Private, fn) })
}
func (s Store) basis() (Metadata, error) {
	m := Metadata{State: "unknown"}
	st, e := recoverymaterial.Read(s.path("voptrack-console"))
	if e != nil {
		return m, e
	}
	if len(st.Pending) > 0 {
		return m, ErrUnavailable
	}
	m.Material = st.Revision
	export, e := s.settingsExport()
	if e != nil {
		return m, e
	}
	if export != nil {
		m.SettingsRevision = export.Revision
	}
	c, e := s.read("voptrack-config/server.json")
	if e != nil {
		return m, e
	}
	var brandInput []byte
	if _, err := os.Lstat(s.path("voptrack-config/identity-brand.json")); err == nil {
		brandInput, e = s.read("voptrack-config/identity-brand.json")
		if e != nil {
			return m, e
		}
	} else if !os.IsNotExist(err) {
		return m, ErrUnavailable
	}
	if !identitybrand.Matches(c, brandInput) {
		return m, ErrUnavailable
	}
	product, e := s.read("voptrack-public/voptrack.json")
	if e != nil {
		return m, e
	}
	m.Configuration = digest(append(append(c, 0), product...))
	if raw, err := s.read("voptrack-bootstrap/home-setup.json"); err == nil {
		retired, err := s.read("voptrack-bootstrap/home-password-retired")
		if err != nil {
			return m, ErrUnavailable
		}
		m.Configuration = digest(append(append(append([]byte(m.Configuration), 0), raw...), retired...))
	} else if _, err := os.Lstat(s.path("voptrack-bootstrap/home-setup.json")); !os.IsNotExist(err) {
		return m, ErrUnavailable
	}

	lineage, e := s.read("voptrack-bootstrap/recovery-lineage")
	if e != nil || !ValidID(strings.TrimSpace(string(lineage))) {
		return m, ErrUnavailable
	}
	k, e := s.read("voptrack-config/backup.key")
	if e != nil || len(k) != 32 {
		return m, ErrUnavailable
	}
	// Opaque compatibility basis covers deployment lineage, fixed key, product,
	// and service store. The authoritative archive/evidence MAC remains separate.
	producer, e := s.read("voptrack-collector/lineage.json")
	if e != nil || !validLineage(producer) {
		return m, ErrUnavailable
	}
	variant, e := s.read("voptrack-bootstrap/variant")
	if e != nil {
		return m, e
	}
	caCert, e := s.read("voptrack-ca/root.crt")
	if e != nil {
		return m, e
	}
	caKey, e := s.read("voptrack-ca/root.key")
	if e != nil {
		return m, e
	}
	if _, e = tls.X509KeyPair(caCert, caKey); e != nil {
		return m, e
	}
	serverCert, e := s.read("voptrack-config/server.crt")
	if e != nil {
		return m, e
	}
	serverKey, e := s.read("voptrack-config/server.key")
	if e != nil {
		return m, e
	}
	if _, e = tls.X509KeyPair(serverCert, serverKey); e != nil {
		return m, e
	}
	m.Compatibility = digest(append(append(append(append(append(lineage, k...), variant...), producer...), caCert...), serverCert...))
	return m, nil
}
func (s Store) read(name string) ([]byte, error) {
	root, e := os.OpenRoot(s.Root)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	path := filepath.Join("var/lib", name)
	info, e := root.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Size() > MaxBytes {
		return nil, ErrUnavailable
	}
	f, e := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return nil, ErrUnavailable
	}
	b, e := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if e != nil || len(b) > MaxBytes {
		return nil, ErrUnavailable
	}
	return b, nil
}
func (s Store) current() (Metadata, error) {
	var m Metadata
	raw, e := os.ReadFile(filepath.Join(s.Private, "current.json"))
	if e != nil {
		return m, e
	}
	if json.Unmarshal(raw, &m) != nil || !ValidID(m.Revision) || m.Size <= 0 || m.Size > MaxBytes || len(m.SHA256) != 64 {
		return Metadata{}, ErrUnavailable
	}
	return m, nil
}
func (s Store) status() Metadata {
	m, e := s.current()
	if e != nil {
		return Metadata{State: "unknown"}
	}
	b, e := s.basis()
	if e != nil {
		m.State = "unknown"
		return m
	}
	if b.SettingsRevision != m.SettingsRevision || b.Material != m.Material || b.Configuration != m.Configuration || b.Compatibility != m.Compatibility {
		m.State = "stale"
		return m
	}
	raw, e := s.readRevision(m)
	if e != nil || len(raw) != m.Size || digest(raw) != m.SHA256 {
		m.State = "unavailable"
		return m
	}
	m.State = "ready"
	return m
}
func (s Store) Status() Metadata {
	m := Metadata{State: "unknown"}
	_ = s.locked(func() error { m = s.status(); return nil })
	return m
}
func (s Store) readRevision(m Metadata) ([]byte, error) {
	f, e := os.OpenFile(filepath.Join(s.Private, m.Revision+".tar"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() != int64(m.Size) {
		return nil, ErrUnavailable
	}
	return io.ReadAll(io.LimitReader(f, MaxBytes+1))
}
func (s Store) Prepare() (m Metadata, err error) {
	err = s.locked(func() error {
		var e error
		m, e = s.basis()
		if e != nil {
			return e
		}
		if current := s.status(); current.State == "ready" {
			m = current
			return nil
		}
		names := []string{"voptrack-public/voptrack.json", "voptrack-config/backup.key", "voptrack-config/server.json", "voptrack-config/server.crt", "voptrack-config/server.key", "voptrack-bootstrap/owner-password", "voptrack-bootstrap/variant", "voptrack-bootstrap/console", "voptrack-bootstrap/recovery-lineage", "voptrack-ca/root.crt", "voptrack-ca/root.key"}
		for _, name := range []string{"voptrack-bootstrap/home-setup.json", "voptrack-bootstrap/home-password-retired", "voptrack-config/identity-brand.json"} {
			if _, err := os.Lstat(s.path(name)); err == nil {
				names = append(names, name)
			} else if !os.IsNotExist(err) {
				return ErrUnavailable
			}
		}
		// Explicit recovery files only. Records, jobs, sessions, fences, temporary
		// writes and human bearer credentials are never selected from Console.
		entries, e := os.ReadDir(s.path("voptrack-console"))
		if e != nil {
			return e
		}
		for _, entry := range entries {
			n := entry.Name()
			if strings.HasSuffix(n, ".secret") || n == "console-state.dataset" || strings.HasPrefix(n, "oauth-client-") {
				names = append(names, "voptrack-console/"+n)
			}
		}
		studies, e := os.ReadDir(s.path("voptrack-console/studies"))
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		for _, entry := range studies {
			if strings.HasSuffix(entry.Name(), ".issuer-secret") {
				names = append(names, "voptrack-console/studies/"+entry.Name())
			}
		}
		// Retained earlier credentials remain independently usable with older
		// backups; the kit never silently drops them during a new preparation.
		history := s.path("voptrack-console/.recovery-history")
		e = filepath.WalkDir(history, func(path string, d os.DirEntry, err error) error {
			if os.IsNotExist(err) && path == history {
				return nil
			}
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(s.path("voptrack-console"), path)
			if err != nil {
				return err
			}
			n := "voptrack-console/" + filepath.ToSlash(relative)
			if !allowed(n) {
				return ErrUnavailable
			}
			names = append(names, n)
			if len(names) > 4096 {
				return ErrUnavailable
			}
			return nil
		})
		if e != nil {
			return e
		}
		// App-only Home keeps its issuer in bootstrap, not Console.
		off, e := s.read("voptrack-bootstrap/console")
		if e != nil {
			return e
		}
		variant, e := s.read("voptrack-bootstrap/variant")
		if e != nil {
			return e
		}
		required := []string{}
		if strings.TrimSpace(string(variant)) == "household" {
			if strings.TrimSpace(string(off)) == "off" {
				required = append(required, "voptrack-bootstrap/issuer-home-members.secret")
			} else {
				required = append(required, "voptrack-console/home.secret", "voptrack-console/issuer-home-members.secret")
			}
		}
		if strings.TrimSpace(string(off)) == "on" && (strings.TrimSpace(string(variant)) == "household" || strings.TrimSpace(string(variant)) == "research") {
			required = append(required, "voptrack-console/console-state.secret", "voptrack-console/console-state.dataset")
		}
		if strings.TrimSpace(string(variant)) == "research" {
			required = append(required, "voptrack-console/analysis.secret")
		}
		names = append(names, required...)
		sort.Strings(names)
		producer, e := s.read("voptrack-collector/lineage.json")
		if e != nil || !validLineage(producer) {
			return ErrUnavailable
		}
		members := map[string][]byte{"voptrack-bootstrap/backup-lineage.json": producer}
		export, e := s.settingsExport()
		if e != nil {
			return e
		}
		if export != nil {
			raw, _ := json.Marshal(export)
			members["voptrack-bootstrap/backup-settings-export.json"] = raw
			names = append(names, "voptrack-bootstrap/backup-settings-export.json")
		}
		hashes := map[string]string{"voptrack-bootstrap/backup-lineage.json": digest(producer)}
		if export != nil {
			hashes["voptrack-bootstrap/backup-settings-export.json"] = digest(members["voptrack-bootstrap/backup-settings-export.json"])
		}
		total := 0
		for _, n := range names {
			if _, ok := members[n]; ok {
				continue
			}
			raw, e := s.read(n)
			if e != nil || len(raw) == 0 {
				return ErrUnavailable
			}
			total += len(raw)
			if total > MaxBytes/2 {
				return ErrUnavailable
			}
			members[n] = raw
			hashes[n] = digest(raw)
		}
		if _, e = tls.X509KeyPair(members["voptrack-ca/root.crt"], members["voptrack-ca/root.key"]); e != nil {
			return ErrUnavailable
		}
		if _, e = tls.X509KeyPair(members["voptrack-config/server.crt"], members["voptrack-config/server.key"]); e != nil {
			return ErrUnavailable
		}
		m.Revision = recoverymaterial.ID()
		m.Prepared = s.now()
		m.State = "ready"
		desc, _ := json.Marshal(manifest{Format: "voptrack-recovery-kit-1", Metadata: m, Members: hashes})
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		write := func(n string, b []byte) error {
			if e := tw.WriteHeader(&tar.Header{Name: "var/lib/" + n, Mode: 0600, Size: int64(len(b)), Typeflag: tar.TypeReg}); e != nil {
				return e
			}
			_, e := tw.Write(b)
			return e
		}
		if e = write("voptrack-bootstrap/backup-lineage.json", producer); e != nil {
			return e
		}
		previous := ""
		for _, n := range names {
			if n == previous {
				continue
			}
			previous = n
			if e = write(n, members[n]); e != nil {
				return e
			}
		}
		if e = write("voptrack-bootstrap/kit-manifest.json", desc); e != nil {
			return e
		}
		if e = tw.Close(); e != nil {
			return e
		}
		if _, e = Validate(buf.Bytes()); e != nil {
			return e
		}
		if buf.Len() > MaxBytes {
			return ErrUnavailable
		}
		m.Size = buf.Len()
		m.SHA256 = digest(buf.Bytes())
		// Commit immutable bytes first, then atomically advance current. A failed
		// preparation never changes the previous usable kit; revisions are retained.
		if e = recoverymaterial.Write(filepath.Join(s.Private, m.Revision+".tar"), buf.Bytes()); e != nil {
			return e
		}
		return backupsettings.WriteJSON(filepath.Join(s.Private, "current.json"), m, 0600)
	})
	return
}

type Request struct {
	ID       string    `json:"id"`
	Session  string    `json:"session"`
	Operator string    `json:"operator"`
	Revision string    `json:"revision"`
	Action   string    `json:"action"`
	Expires  time.Time `json:"expires"`
	Approved bool      `json:"approved"`
}

func (s Store) requestPath(id string) string { return filepath.Join(s.Private, "request-"+id+".json") }
func (s Store) load(id string) (r Request, e error) {
	if !ValidID(id) {
		return r, ErrUnavailable
	}
	raw, e := os.ReadFile(s.requestPath(id))
	if e != nil {
		return r, e
	}
	if json.Unmarshal(raw, &r) != nil || r.ID != id || r.Action != "export-kit" {
		return Request{}, ErrUnavailable
	}
	return
}
func (s Store) Request(session, operator string) (r Request, err error) {
	if !ValidID(session) || !ValidID(operator) {
		return r, ErrUnavailable
	}
	err = s.locked(func() error {
		m := s.status()
		if m.State != "ready" {
			return ErrUnavailable
		}
		entries, e := os.ReadDir(s.Private)
		if e != nil {
			return e
		}
		active := 0
		for _, v := range entries {
			if strings.HasPrefix(v.Name(), "request-") && strings.HasSuffix(v.Name(), ".json") {
				id := strings.TrimSuffix(strings.TrimPrefix(v.Name(), "request-"), ".json")
				old, e := s.load(id)
				if e == nil && !s.now().Before(old.Expires) {
					if e = os.Remove(s.requestPath(id)); e != nil {
						return e
					}
				} else {
					active++
				}
			}
		}
		if active >= 32 {
			return ErrUnavailable
		}
		r = Request{ID: recoverymaterial.ID(), Session: session, Operator: operator, Revision: m.Revision, Action: "export-kit", Expires: s.now().Add(5 * time.Minute)}
		return backupsettings.WriteJSON(s.requestPath(r.ID), r, 0600)
	})
	return
}
func (s Store) Approve(id, revision string) error {
	return s.locked(func() error {
		r, e := s.load(id)
		if e != nil {
			return e
		}
		m := s.status()
		if !s.now().Before(r.Expires) || m.State != "ready" || r.Revision != m.Revision || r.Revision != revision {
			return ErrUnavailable
		}
		r.Approved = true
		return backupsettings.WriteJSON(s.requestPath(id), r, 0600)
	})
}
func (s Store) Revoke(id string) error {
	return s.locked(func() error {
		if !ValidID(id) {
			return ErrUnavailable
		}
		return s.removeRequest(id)
	})
}
func (s Store) Deliver(id, session, operator string) (raw []byte, err error) {
	err = s.locked(func() error {
		r, e := s.load(id)
		if e != nil {
			return e
		}
		if !r.Approved || r.Session != session || r.Operator != operator || !s.now().Before(r.Expires) {
			return ErrUnavailable
		}
		m := s.status()
		if m.State != "ready" || m.Revision != r.Revision {
			return ErrUnavailable
		}
		raw, e = s.readRevision(m)
		if e != nil || len(raw) != m.Size || digest(raw) != m.SHA256 {
			return ErrUnavailable
		}
		// Consume durably before emitting any byte. Failed or interrupted delivery
		// needs a fresh host approval and never records external custody.
		if !s.now().Before(r.Expires) {
			return ErrUnavailable
		}
		return s.removeRequest(id)
	})
	return
}

// removeRequest acknowledges consumption or revocation only after the unlink
// is durable. Open the directory first so an open failure leaves approval intact.
func (s Store) removeRequest(id string) error {
	d, err := os.Open(s.Private)
	if err != nil {
		return err
	}
	defer d.Close()
	return removeAndSync(s.requestPath(id), d.Sync)
}

func removeAndSync(path string, syncDirectory func() error) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncDirectory()
}

// Current readiness always requires a fresh supported policy observation.
// Older kit bytes remain retained when observation is unavailable.
func (s Store) settingsExport() (*sc.Export, error) {
	live, e := backupsettings.ReadLive(s.path("voptrack-console"))
	if e != nil {
		return nil, e
	}
	export, e := live.CurrentExport(s.now())
	if e != nil {
		return nil, ErrUnavailable
	}
	raw, e := s.read("voptrack-collector/lineage.json")
	if e != nil {
		return nil, e
	}
	var lineage struct {
		Deployment string `json:"deployment"`
	}
	if json.Unmarshal(raw, &lineage) != nil || lineage.Deployment != export.Deployment {
		return nil, ErrUnavailable
	}
	return export, nil
}
