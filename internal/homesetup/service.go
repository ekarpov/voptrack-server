package homesetup

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	bc "github.com/ekarpov/voptrack-server/internal/backupcollector"
	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type Config struct{ State, Root, Origin, PublicURL string }
type identity struct{ Dev, Ino uint64 }
type Journal struct {
	Version                                             int
	Attempt, Origin, PublicURL, Phase, Binding, KitHash string
	Roots                                               map[string]identity
	Choices                                             Choices
}
type grant struct {
	ID, Code, Digest string
	Expires          time.Time
	Consumed         bool
	Attempts         int
}
type Service struct {
	Config   Config
	mu       sync.Mutex
	journal  Journal
	sessions map[string]*session
	lock     *os.File
	ctx      context.Context
	cancel   context.CancelFunc
	workers  sync.WaitGroup
	done     chan struct{}
	doneOnce sync.Once
	now      func() time.Time
}

func (c Config) validate() error {
	if bc.PrivateDirectory(c.State) != nil || !origin(c.Origin, true) || !origin(c.PublicURL, false) || !filepath.IsAbs(c.Root) || filepath.Clean(c.Root) != c.Root {
		return ErrRefused
	}
	for _, name := range roots {
		p := at(c.Root, "var/lib/"+name)
		if _, e := os.Lstat(p); e != nil {
			return ErrRefused
		}
		if name != "voptrack-public" && bc.PrivateDirectory(p) != nil {
			return ErrRefused
		}
		st, e := os.Lstat(p)
		if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return ErrRefused
		}
		if st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
			return ErrRefused
		}
		if c.State == p || within(c.State, p) || within(p, c.State) {
			return ErrRefused
		}
	}
	return nil
}
func within(a, b string) bool {
	r, e := filepath.Rel(a, b)
	return e == nil && (r == "." || r != ".." && !filepath.IsAbs(r) && len(r) > 0 && !(len(r) >= 3 && r[:3] == "../"))
}
func fileLock(path string) (*os.File, error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if e != nil {
		return nil, ErrRefused
	}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, ErrRefused
	}
	return f, nil
}
func control(state string, fn func() error) error {
	if bc.PrivateDirectory(state) != nil {
		return ErrRefused
	}
	f, e := fileLock(filepath.Join(state, "state.lock"))
	if e != nil {
		return e
	}
	defer f.Close()
	return fn()
}
func (c Config) ids() (map[string]identity, error) {
	m := map[string]identity{}
	seen := map[identity]bool{}
	state, err := os.Stat(c.State)
	if err != nil {
		return nil, ErrRefused
	}
	st := state.Sys().(*syscall.Stat_t)
	seen[identity{st.Dev, st.Ino}] = true
	for _, n := range roots {
		st, e := os.Lstat(at(c.Root, "var/lib/"+n))
		if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return nil, ErrRefused
		}
		v := st.Sys().(*syscall.Stat_t)
		id := identity{v.Dev, v.Ino}
		if seen[id] {
			return nil, ErrRefused
		}
		seen[id] = true
		m[n] = id
	}
	return m, nil
}
func (c Config) fresh() error { return c.freshPending("") }
func (c Config) freshPending(attempt string) error {
	for _, n := range roots {
		entries, e := os.ReadDir(at(c.Root, "var/lib/"+n))
		if e != nil {
			return ErrRefused
		}
		for _, v := range entries {
			if n == "voptrack-public" && v.Name() == "home-setup-pending" && attempt != "" {
				raw, e := readRegular(at(c.Root, PendingFile), 128)
				if e != nil || string(raw) != attempt {
					return ErrRefused
				}
				continue
			}
			if n == "voptrack-public" && v.Name() == "maintenance.lock" {
				if !v.Type().IsRegular() {
					return ErrRefused
				}

				continue
			}
			return ErrRefused
		}
	}
	return nil
}
func (s *Service) save() error {
	return writeJSON(filepath.Join(s.Config.State, "journal.json"), s.journal)
}
func New(c Config) (s *Service, e error) {
	if e = c.validate(); e != nil {
		return nil, e
	}
	lock, e := fileLock(filepath.Join(c.State, "serve.lock"))
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	s = &Service{Config: c, lock: lock, ctx: ctx, cancel: cancel, sessions: map[string]*session{}, done: make(chan struct{}), now: time.Now}
	e = control(c.State, func() error {
		path := filepath.Join(c.State, "journal.json")
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			// Never infer freshness from one missing marker or overwrite partial state.
			entries, err := os.ReadDir(c.State)
			if err != nil {
				return ErrRefused
			}
			for _, v := range entries {
				if v.Name() != "serve.lock" && v.Name() != "state.lock" {
					return ErrRefused
				}
			}
			if c.fresh() != nil {
				return ErrRefused
			}
			ids, err := c.ids()
			if err != nil {
				return err
			}
			s.journal = Journal{Version: 1, Attempt: recoverymaterial.ID(), Origin: c.Origin, PublicURL: c.PublicURL, Phase: "draft", Roots: ids}
			if e := recoverymaterial.Write(at(c.Root, PendingFile), []byte(s.journal.Attempt)); e != nil {
				return e
			}
			if e := s.save(); e != nil {
				return e
			}
			if e := certificate(c); e != nil {
				return e
			}
			return issue(c.State, s.now())
		}
		if bc.ReadPrivateJSON(path, &s.journal) != nil || s.journal.Version != 1 || (s.journal.Phase != "complete" && (s.journal.Origin != c.Origin || s.journal.PublicURL != c.PublicURL)) || s.bound() != nil {
			return ErrRefused
		}
		switch s.journal.Phase {
		case "draft", "review":
			s.journal.Phase = "draft"
			s.journal.Binding = ""
			if e := s.save(); e != nil {
				return e
			}
			return writeJSON(filepath.Join(c.State, "authorization.json"), grant{Consumed: true})
		case "accepted", "provisioning", "password":
			s.journal.Phase = "stopped"
			return s.save()
		case "ready":
			return writeJSON(filepath.Join(c.State, "authorization.json"), grant{Consumed: true})
		case "stopped", "complete":
			return nil
		default:
			return ErrRefused
		}
	})
	if e != nil {
		s.Close()
		return nil, e
	}
	return s, nil
}
func (s *Service) bound() error {
	if s.Config.validate() != nil {
		return ErrRefused
	}
	m, e := s.Config.ids()
	if e != nil || len(m) != len(s.journal.Roots) {
		return ErrRefused
	}
	for n, v := range m {
		if s.journal.Roots[n] != v {
			return ErrRefused
		}
	}
	return nil
}
func issue(state string, now time.Time) error {
	code := recoverymaterial.ID()
	return writeJSON(filepath.Join(state, "authorization.json"), grant{ID: recoverymaterial.ID(), Code: code, Digest: digest([]byte(code)), Expires: now.Add(30 * time.Minute)})
}
func Authorize(state string) error {
	return control(state, func() error {
		var j Journal
		if bc.ReadPrivateJSON(filepath.Join(state, "journal.json"), &j) != nil || (j.Phase != "draft" && j.Phase != "review" && j.Phase != "ready") {
			return ErrRefused
		}
		return issue(state, time.Now())
	})
}
func Access(state string) (out map[string]string, e error) {
	e = control(state, func() error {
		var j Journal
		var g grant
		if bc.ReadPrivateJSON(filepath.Join(state, "journal.json"), &j) != nil || bc.ReadPrivateJSON(filepath.Join(state, "authorization.json"), &g) != nil || g.Consumed || g.Attempts >= 5 || g.Code == "" || !time.Now().Before(g.Expires) || (j.Phase != "draft" && j.Phase != "review" && j.Phase != "ready") {
			return ErrRefused
		}
		raw, e := os.ReadFile(filepath.Join(state, "tls.crt"))
		if e != nil {
			return ErrRefused
		}
		b, _ := pem.Decode(raw)
		if b == nil {
			return ErrRefused
		}
		out = map[string]string{"origin": j.Origin, "certificate_sha256": digest(b.Bytes), "code": g.Code}
		return nil
	})
	return
}
func certificate(c Config) error {
	u, _ := url.Parse(c.Origin)
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		return e
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return e
	}
	cert := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Voptrack protected Home setup"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(365 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		cert.IPAddresses = []net.IP{ip}
	} else {
		cert.DNSNames = []string{u.Hostname()}
	}
	der, e := x509.CreateCertificate(rand.Reader, &cert, &cert, &key.PublicKey, key)
	if e != nil {
		return e
	}
	if e = recoverymaterial.Write(filepath.Join(c.State, "tls.key"), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})); e != nil {
		return e
	}
	return recoverymaterial.Write(filepath.Join(c.State, "tls.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
func (s *Service) TLS() (*tls.Config, error) {
	v, e := tls.LoadX509KeyPair(filepath.Join(s.Config.State, "tls.crt"), filepath.Join(s.Config.State, "tls.key"))
	if e != nil {
		return nil, e
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{v}}, nil
}
func (s *Service) Done() <-chan struct{} { return s.done }
func (s *Service) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	s.workers.Wait()
	if s.lock != nil {
		s.lock.Close()
		s.lock = nil
	}
}
func command(ctx context.Context, env []string, out io.Writer, name string, args ...string) error {
	c := exec.CommandContext(ctx, name, args...)
	c.Env = append(os.Environ(), env...)
	c.Stdout = out
	c.Stderr = io.Discard
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process == nil {
			return nil
		}
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	c.WaitDelay = 2 * time.Second
	return c.Run()
}
func (s *Service) step(phase string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return control(s.Config.State, func() error {
		if s.bound() != nil {
			return ErrRefused
		}
		s.journal.Phase = phase
		return s.save()
	})
}
func (s *Service) execute(password string) {
	defer s.workers.Done()
	success := false
	defer func() {
		if !success {
			_ = s.step("stopped")
		}
	}()
	if s.Config.Root != "/" || s.bound() != nil {
		return
	}
	f, e := fileLock(at(s.Config.Root, "var/lib/voptrack-public/maintenance.lock"))
	if e != nil {
		return
	}
	defer f.Close()
	if s.Config.freshPending(s.journal.Attempt) != nil {
		return
	}
	if s.step("provisioning") != nil {
		return
	}
	if writeJSON(at(s.Config.Root, SettingsFile), s.journal.Choices) != nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 3*time.Minute)
	defer cancel()
	if command(ctx, []string{"VOPTRACK_MAINTENANCE_HELD=yes"}, io.Discard, "/usr/local/bin/voptrack-init", "init") != nil {
		return
	}
	if s.step("password") != nil {
		return
	}
	temp, e := os.CreateTemp("", "home-owner-*")
	if e != nil {
		return
	}
	defer os.Remove(temp.Name())
	if _, e = temp.WriteString(password); e != nil {
		temp.Close()
		return
	}
	if temp.Close() != nil {
		return
	}
	password = ""
	if command(ctx, nil, io.Discard, "/usr/local/bin/tesserad", "-config", "/var/lib/voptrack-config/server.json", "-reset-password", "owner", "-password-file", temp.Name()) != nil {
		return
	}
	if recoverymaterial.Write(at(s.Config.Root, "var/lib/voptrack-bootstrap/home-password-retired"), []byte("initial password retired\n")) != nil {
		return
	}
	if s.step("ready") != nil {
		return
	}
	success = true
}
func (s *Service) release() error { return s.releaseAfterStateSync(syncDir) }
func (s *Service) releaseAfterStateSync(syncState func(string) error) error {
	return s.releaseAfterSync(syncState, syncDir)
}
func (s *Service) releaseAfterSync(syncState, syncParent func(string) error) error {
	if s.bound() != nil {
		return ErrRefused
	}
	// A visible journal may follow a rename whose parent sync failed in the
	// previous process. Establish its durability before releasing startup.
	if syncState(s.Config.State) != nil {
		return ErrRefused
	}
	path := at(s.Config.Root, PendingFile)
	raw, e := readRegular(path, 128)
	if os.IsNotExist(e) {
		return syncParent(filepath.Dir(path))
	}
	if e != nil || string(raw) != s.journal.Attempt {
		return ErrRefused
	}
	if os.Remove(path) != nil {
		return ErrRefused
	}
	return syncParent(filepath.Dir(path))
}

func (s *Service) Complete() bool { return s.journal.Phase == "complete" }
func (s *Service) ReleaseCompleted() error {
	if !s.Complete() {
		return ErrRefused
	}
	return s.release()
}

func Status(state string) (string, error) {
	phase := ""
	e := control(state, func() error {
		var j Journal
		if bc.ReadPrivateJSON(filepath.Join(state, "journal.json"), &j) != nil {
			return ErrRefused
		}
		phase = j.Phase
		return nil
	})
	return phase, e
}
