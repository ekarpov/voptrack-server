// Package guidedrecovery implements the separate host-started recovery authority.
// It is deliberately independent of Console, its sessions and restored accounts.
package guidedrecovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
	rs "github.com/ekarpov/voptrack-server/internal/recoveryset"
)

var ErrRefused = errors.New("recovery refused; retain inputs and ask the host operator")
var idPattern = regexp.MustCompile("^[a-z][a-z0-9-]{0,47}$")

type Set struct{ ID, Label, Directory, TrustFile, KeyFile string }
type Config struct {
	Origin, State, Target string
	Sets                  []Set
}
type Stage struct {
	Phase string
	At    time.Time
}
type Journal struct {
	History []Stage

	Version                      int
	Target                       string
	AuthDigest                   string
	AuthExpires                  time.Time
	AuthConsumed                 bool
	AuthAttempts                 int
	Attempt, Set, Phase, Binding string
	Checked                      *rs.Receipt
	Updated                      time.Time
}
type Service struct {
	closed bool
	opened bool

	mu       sync.Mutex
	config   Config
	journal  Journal
	locks    []*os.File
	sessions map[string]*session
	now      func() time.Time
	run      func(context.Context, ...string) error
	ctx      context.Context
	cancel   context.CancelFunc
	workers  sync.WaitGroup
}

func digest(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func privateDir(path string) error {
	// Reject symlink ancestors: all roots must be owned/controlled by the operator.
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrRefused
	}
	for p := path; ; p = filepath.Dir(p) {
		st, e := os.Lstat(p)
		if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return ErrRefused
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
func lock(dir, name string) (*os.File, error) {
	f, e := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}

// Open holds lifetime locks on both state and target parent. A different state
// directory cannot bypass target exclusivity. A reserved target must not exist.
func Open(c Config) (*Service, error) {
	u, e := url.Parse(c.Origin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || len(c.Sets) == 0 || len(c.Sets) > 16 {
		return nil, ErrRefused
	}
	if privateDir(c.State) != nil || privateDir(filepath.Dir(c.Target)) != nil || !filepath.IsAbs(c.Target) || filepath.Clean(c.Target) != c.Target || c.Target == filepath.Dir(c.Target) {
		return nil, ErrRefused
	}
	// No overlap between retained sources, state and writable destination.
	roots := []string{c.State, filepath.Dir(c.Target)}
	seen := map[string]bool{}
	for _, v := range c.Sets {
		if !idPattern.MatchString(v.ID) || seen[v.ID] || len(v.Label) == 0 || len(v.Label) > 120 || privateDir(v.Directory) != nil || privateDir(filepath.Dir(v.TrustFile)) != nil || privateDir(filepath.Dir(v.KeyFile)) != nil {
			return nil, ErrRefused
		}
		seen[v.ID] = true
		roots = append(roots, v.Directory, filepath.Dir(v.TrustFile), filepath.Dir(v.KeyFile))
	}
	for i, a := range roots {
		for j, b := range roots {
			if i != j && (i < 2 || j < 2) && (a == b || within(a, b) || within(b, a)) {
				return nil, ErrRefused
			}
		}
	}
	s := &Service{config: c, sessions: map[string]*session{}, now: time.Now, run: command}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	for _, p := range []struct{ dir, name string }{{c.State, "application.lock"}, {filepath.Dir(c.Target), ".guided-recovery.lock"}} {
		f, e := lock(p.dir, p.name)
		if e != nil {
			s.Close()
			return nil, ErrRefused
		}
		s.locks = append(s.locks, f)
	}
	raw, e := os.ReadFile(filepath.Join(c.State, "journal.json"))
	if os.IsNotExist(e) {
		s.journal = Journal{Version: 1, Target: c.Target, Phase: "select"}
		e = s.save()
	} else if e == nil {
		e = json.Unmarshal(raw, &s.journal)
		if s.journal.Version != 1 || s.journal.Target != c.Target {
			e = ErrRefused
		}
	}
	if e != nil {
		s.Close()
		return nil, ErrRefused
	}
	switch s.journal.Phase {
	case "select", "refused", "review":
		if s.journal.Phase == "review" {
			s.journal.Phase = "refused"
		}
	case "complete", "stopped":
	case "accepted", "creating-target", "importing-kit", "recovering-producer", "restoring-records", "verifying":
		s.journal.Phase = "stopped"
	default:
		s.Close()
		return nil, ErrRefused
	}
	// A previous process never transfers browser authority or replays a mutation.
	if e = s.cleanup(); e == nil {
		e = s.save()
	}
	if e != nil {
		s.Close()
		return nil, ErrRefused
	}
	s.opened = true
	return s, nil
}
func within(a, b string) bool {
	r, e := filepath.Rel(a, b)
	return e == nil && r != ".." && !filepath.IsAbs(r) && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}
func (s *Service) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.workers.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	// Only a fully opened owner may clean temporary state. Failed lock acquisition
	// must never touch another process's files.
	if s.opened {
		if s.journal.Phase == "review" {
			s.journal.Phase = "refused"
			s.journal.Binding = ""
			s.journal.Checked = nil
		}
		if s.cleanup() != nil {
			s.journal.Phase = "stopped"
		}
		_ = s.save()
	}
	for _, f := range s.locks {
		_ = f.Close()
	}
	s.locks = nil
}

func (s *Service) save() error {
	s.journal.Updated = s.now().UTC()
	if len(s.journal.History) == 0 || s.journal.History[len(s.journal.History)-1].Phase != s.journal.Phase {
		s.journal.History = append(s.journal.History, Stage{s.journal.Phase, s.journal.Updated})
	}
	raw, e := json.Marshal(s.journal)
	if e != nil {
		return e
	}
	return recoverymaterial.Write(filepath.Join(s.config.State, "journal.json"), raw)
}
func (s *Service) cleanup() error {
	// Only fixed task-owned temporary names; retained inputs and targets survive.
	for _, n := range []string{"snapshot", "recheck", "materials"} {
		if e := os.RemoveAll(filepath.Join(s.config.State, n)); e != nil {
			return e
		}
	}
	return nil
}

// Authorize is host-only, while the application is stopped. Tokens are never
// printed. The token file must be private and separate from all served state.
func (s *Service) Authorize(tokenFile string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrRefused
	}
	token := recoverymaterial.ID() + recoverymaterial.ID()
	f, e := os.OpenFile(tokenFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return ErrRefused
	}
	if _, e = f.WriteString(token); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil {
		return ErrRefused
	}
	s.journal.AuthDigest = digest([]byte(token))
	s.journal.AuthExpires = s.now().Add(10 * time.Minute)
	s.journal.AuthConsumed = false
	s.journal.AuthAttempts = 0
	return s.save()
}
func (s *Service) set(id string) (Set, bool) {
	for _, v := range s.config.Sets {
		if id == v.ID {
			return v, true
		}
	}
	return Set{}, false
}
func (s *Service) options(v Set) rs.Options {
	return rs.Options{Directory: v.Directory, TrustFile: v.TrustFile, KeyFile: v.KeyFile, Verifier: "/usr/local/lib/tessera/verify_acquisition.py"}
}
func (s *Service) staged() rs.Options {
	return rs.Options{Directory: filepath.Join(s.config.State, "snapshot"), TrustFile: filepath.Join(s.config.State, "snapshot/trust"), KeyFile: filepath.Join(s.config.State, "snapshot/key"), Verifier: "/usr/local/lib/tessera/verify_acquisition.py"}
}
func binding(r rs.Receipt, target, attempt, set string) string {
	raw, _ := json.Marshal(struct {
		Selection, Trust     string
		Inputs               map[string]string
		Target, Attempt, Set string
	}{r.SelectionSHA256, r.TrustSHA256, r.Inputs, target, attempt, set})
	return digest(raw)
}
func absent(path string) bool { _, e := os.Lstat(path); return os.IsNotExist(e) }

// validate is called under mu, before any target mutation. A receipt is never
// sufficient execution authority; fresh copies are authenticated again at confirm.
func (s *Service) validate(ctx context.Context, id string) error {
	v, ok := s.set(id)
	if !ok || !absent(s.config.Target) {
		return ErrRefused
	}
	if s.journal.Phase != "select" && s.journal.Phase != "refused" && s.journal.Phase != "review" {
		return ErrRefused
	}
	if s.cleanup() != nil {
		return ErrRefused
	}
	s.journal.History = nil
	s.journal.Attempt = recoverymaterial.ID()
	s.journal.Set = id
	s.journal.Phase = "refused"
	s.journal.Binding = ""
	s.journal.Checked = nil
	if s.save() != nil {
		return ErrRefused
	}
	_, r, e := rs.Stage(ctx, s.options(v), filepath.Join(s.config.State, "snapshot"))
	if e != nil {
		_ = s.cleanup()
		return ErrRefused
	}
	s.journal.Binding = binding(r, s.config.Target, s.journal.Attempt, id)
	s.journal.Checked = &r
	s.journal.Phase = "review"
	if s.save() != nil {
		s.journal.Phase = "refused"
		_ = s.cleanup()
		return ErrRefused
	}
	return nil
}
func (s *Service) transition(phase string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.journal.Phase = phase
	if e := s.save(); e != nil {
		s.journal.Phase = "stopped"
		return e
	}
	return nil
}
func (s *Service) execute() {
	defer s.workers.Done()
	err := s.recover(s.ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.journal.Phase = "complete"
	if err != nil {
		s.journal.Phase = "stopped"
	}
	if s.cleanup() != nil {
		s.journal.Phase = "stopped"
	}
	if s.save() != nil {
		s.journal.Phase = "stopped"
	} // never render unsaved success
}
