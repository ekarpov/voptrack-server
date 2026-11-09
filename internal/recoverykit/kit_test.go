package recoverykit

import (
	"archive/tar"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	bs "github.com/ekarpov/voptrack-server/internal/backupsettings"
	"github.com/ekarpov/voptrack-server/internal/homesetup"
	"github.com/ekarpov/voptrack-server/internal/identitybrand"
	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
	sc "github.com/ekarpov/voptrack-server/internal/schedulerclient"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T) Store {
	t.Helper()
	s := Store{Root: t.TempDir(), Private: t.TempDir()}
	write := func(n string, b []byte) {
		p := s.path(n)
		must(t, os.MkdirAll(filepath.Dir(p), 0700))
		must(t, os.WriteFile(p, b, 0600))
	}
	k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, e)
	der, e := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}, &x509.Certificate{SerialNumber: big.NewInt(1)}, &k.PublicKey, k)
	must(t, e)
	pk, e := x509.MarshalECPrivateKey(k)
	must(t, e)
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	key := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: pk})
	for _, n := range []string{"voptrack-ca/root", "voptrack-config/server"} {
		write(n+".crt", cert)
		write(n+".key", key)
	}
	for n, v := range map[string]string{"voptrack-public/voptrack.json": `{"product":"home","timezone":"UTC","web_interface":true}`, "voptrack-public/maintenance.lock": "", "voptrack-config/backup.key": string(bytes.Repeat([]byte{3}, 32)), "voptrack-config/server.json": `{"engine":"sqlite","data_dir":"/var/lib/tessera","tls_key":"/var/lib/voptrack-config/server.key","tls_certificate":"/var/lib/voptrack-config/server.crt","encrypted_storage_confirmed":true,"backup":{"directory":"/var/lib/voptrack-backups","key_file":"/var/lib/voptrack-config/backup.key","every_hours":24,"keep":7}}`, "voptrack-bootstrap/owner-password": "synthetic-password", "voptrack-bootstrap/variant": "household\n", "voptrack-bootstrap/console": "on\n", "voptrack-bootstrap/recovery-lineage": recoverymaterial.ID() + "\n", "voptrack-collector/lineage.json": `{"deployment":"0123456789abcdef0123456789abcdef","origin":"https://tesserad:8443"}`, "voptrack-console/home.secret": "home-secret", "voptrack-console/issuer-home-members.secret": "issuer-secret", "voptrack-console/console-state.secret": "state-secret", "voptrack-console/console-state.dataset": "dataset", "voptrack-console/studies/synthetic.issuer-secret": "study-secret"} {
		write(n, []byte(v))
	}
	refreshFixtureSettings(t, s)
	return s
}
func TestCommittedKitAuthorityAndFreshRestore(t *testing.T) {
	s := fixture(t)
	must(t, os.MkdirAll(s.path("voptrack-console/.browser-sessions"), 0700))
	must(t, os.WriteFile(s.path("voptrack-console/.browser-sessions/key-v1"), []byte("synthetic-session-key"), 0600))
	must(t, os.WriteFile(s.path("voptrack-console/.browser-sessions/synthetic.session"), []byte("synthetic-session-envelope"), 0600))
	m, e := s.Prepare()
	must(t, e)
	if s.Status().State != "ready" {
		t.Fatal(s.Status())
	}
	session, operator := recoverymaterial.ID(), recoverymaterial.ID()
	q, e := s.Request(session, operator)
	must(t, e)
	if _, e = s.Deliver(q.ID, session, operator); e == nil {
		t.Fatal("ordinary authority exported secrets")
	}
	if e = s.Approve(q.ID, recoverymaterial.ID()); e == nil {
		t.Fatal("wrong revision approved")
	}
	must(t, s.Approve(q.ID, m.Revision))
	for _, ids := range [][2]string{{recoverymaterial.ID(), operator}, {session, recoverymaterial.ID()}} {
		if _, e = s.Deliver(q.ID, ids[0], ids[1]); e == nil {
			t.Fatal("cross session/operator delivery")
		}
	}
	raw, e := s.Deliver(q.ID, session, operator)
	must(t, e)
	if _, e = s.Deliver(q.ID, session, operator); e == nil {
		t.Fatal("replay")
	}
	files, e := Validate(raw)
	must(t, e)
	for name := range files {
		if strings.Contains(name, ".browser-sessions") {
			t.Fatal("portable recovery kit included remembered browser credentials")
		}
	}
	if string(files["voptrack-console/studies/synthetic.issuer-secret"]) != "study-secret" {
		t.Fatal("lost live material")
	}
	fresh := t.TempDir()
	must(t, Restore(fresh, raw))
	if e = Restore(fresh, raw); e == nil {
		t.Fatal("overwrote deployment")
	}
	q, e = s.Request(session, operator)
	must(t, e)
	must(t, s.Approve(q.ID, m.Revision))
	must(t, s.Revoke(q.ID))
	if _, e = s.Deliver(q.ID, session, operator); e == nil {
		t.Fatal("revoked approval delivered")
	}
	q, e = s.Request(session, operator)
	must(t, e)
	must(t, s.Approve(q.ID, m.Revision))
	now := q.Expires
	s.Now = func() time.Time { return now }
	if _, e = s.Deliver(q.ID, session, operator); e == nil {
		t.Fatal("expired approval delivered")
	}
	refreshFixtureSettings(t, s)
	if _, e = s.Request(session, operator); e != nil {
		t.Fatal("expired request cleanup blocked a fresh approval", e)
	}
}
func TestPendingChangesAndFailedPreparationPreservePrior(t *testing.T) {
	s := fixture(t)
	m, e := s.Prepare()
	must(t, e)
	previous, e := s.readRevision(m)
	must(t, e)
	q, e := s.Request(recoverymaterial.ID(), recoverymaterial.ID())
	must(t, e)
	must(t, s.Approve(q.ID, m.Revision))
	dir := s.path("voptrack-console")
	e = recoverymaterial.Change(dir, "issuer:synthetic", func() error {
		must(t, recoverymaterial.Write(s.path("voptrack-console/studies/synthetic.issuer-secret"), []byte("new-secret")))
		return errors.New("ambiguous remote completion")
	})
	if e == nil {
		t.Fatal("fixture")
	}
	if s.Status().State != "unknown" {
		t.Fatal("pending mutation called ready")
	}
	if _, e = s.Prepare(); e == nil {
		t.Fatal("pending mutation snapshotted")
	}
	unchanged, e := s.readRevision(m)
	must(t, e)
	if !bytes.Equal(previous, unchanged) {
		t.Fatal("previous revision damaged")
	}
	if _, e = s.Deliver(q.ID, q.Session, q.Operator); e == nil {
		t.Fatal("pending material export")
	}
	must(t, recoverymaterial.Change(dir, "other", func() error { return nil }))
	if _, e = s.Prepare(); e == nil {
		t.Fatal("unrelated mutation cleared uncertainty")
	}
	must(t, recoverymaterial.Change(dir, "issuer:synthetic", func() error { return nil }))
	if s.Status().State != "stale" {
		t.Fatal("stale kit called ready")
	}
	next, e := s.Prepare()
	must(t, e)
	if next.Revision == m.Revision {
		t.Fatal("reused old revision")
	}
	if _, e = s.Deliver(q.ID, q.Session, q.Operator); e == nil {
		t.Fatal("stale approval exported newer kit")
	}
	must(t, os.Remove(s.path("voptrack-ca/root.key")))
	if _, e = s.Prepare(); e == nil {
		t.Fatal("incomplete kit prepared")
	}
	current, e := s.current()
	must(t, e)
	if current.Revision != next.Revision {
		t.Fatal("failed preparation replaced kit")
	}
}
func TestMaterialLockMakesSnapshotCoherent(t *testing.T) {
	s := fixture(t)
	_, e := s.Prepare()
	must(t, e)
	entered, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if e := recoverymaterial.Change(s.path("voptrack-console"), "issuer", func() error {
			must(t, recoverymaterial.Write(s.path("voptrack-console/home.secret"), []byte("new-home")))
			close(entered)
			<-release
			return recoverymaterial.Write(s.path("voptrack-console/issuer-home-members.secret"), []byte("new-issuer"))
		}); e != nil {
			t.Error(e)
		}
	}()
	<-entered
	done := make(chan Metadata, 1)
	go func() {
		m, e := s.Prepare()
		if e != nil {
			t.Error(e)
		}
		done <- m
	}()
	select {
	case <-done:
		t.Fatal("snapshot copied mid-transaction")
	case <-time.After(40 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	m := <-done
	raw, e := s.readRevision(m)
	must(t, e)
	files, e := Validate(raw)
	must(t, e)
	if string(files["voptrack-console/home.secret"]) != "new-home" || string(files["voptrack-console/issuer-home-members.secret"]) != "new-issuer" {
		t.Fatal("mixed material snapshot")
	}
}
func TestCorruptWrongAndUnsafeKits(t *testing.T) {
	s := fixture(t)
	m, e := s.Prepare()
	must(t, e)
	raw, e := s.readRevision(m)
	must(t, e)
	corrupt := bytes.Clone(raw)
	pos := bytes.Index(corrupt, []byte("home-secret"))
	if pos < 0 {
		t.Fatal("fixture")
	}
	corrupt[pos] ^= 1
	for _, b := range [][]byte{[]byte("not a kit"), raw[:len(raw)/2], corrupt} {
		fresh := t.TempDir()
		if e = Restore(fresh, b); e == nil {
			t.Fatal("invalid kit restored")
		}
		entries, e := os.ReadDir(fresh)
		must(t, e)
		if len(entries) != 0 {
			t.Fatal("validation wrote target")
		}
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	must(t, tw.WriteHeader(&tar.Header{Name: "../../escape", Typeflag: tar.TypeReg, Size: 1}))
	_, e = tw.Write([]byte("x"))
	must(t, e)
	must(t, tw.Close())
	if _, e = Validate(buf.Bytes()); e == nil {
		t.Fatal("unsafe path accepted")
	}
	// A replaced committed file is never offered, even at the old revision.
	must(t, os.WriteFile(filepath.Join(s.Private, m.Revision+".tar"), corrupt, 0600))
	if s.Status().State == "ready" {
		t.Fatal("corrupt kit available")
	}
}
func TestOldMaterialRetainedWithoutPriorPreparedKit(t *testing.T) {
	s := fixture(t)
	must(t, recoverymaterial.Change(s.path("voptrack-console"), "issuer:synthetic", func() error {
		return recoverymaterial.Write(s.path("voptrack-console/studies/synthetic.issuer-secret"), []byte("replacement-secret"))
	}))
	m, e := s.Prepare()
	must(t, e)
	raw, e := s.readRevision(m)
	must(t, e)
	files, e := Validate(raw)
	must(t, e)
	found := false
	for n, b := range files {
		if strings.Contains(n, "/.recovery-history/") && strings.HasSuffix(n, "synthetic.issuer-secret") && string(b) == "study-secret" {
			found = true
		}
	}
	if !found {
		t.Fatal("older material lost before the first kit preparation")
	}
}
func TestIncompatibleConfigurationPreservesCommittedRevision(t *testing.T) {
	s := fixture(t)
	prior, e := s.Prepare()
	must(t, e)
	path := s.path("voptrack-config/server.json")
	raw, e := os.ReadFile(path)
	must(t, e)
	raw = bytes.Replace(raw, []byte(`"sqlite"`), []byte(`"postgres"`), 1)
	must(t, os.WriteFile(path, raw, 0600))
	if _, e = s.Prepare(); e == nil {
		t.Fatal("incompatible engine prepared")
	}
	current, e := s.current()
	must(t, e)
	if current.Revision != prior.Revision {
		t.Fatal("incompatible kit displaced prior usable revision")
	}
	if s.Status().State != "stale" {
		t.Fatal("incompatible generated config called current")
	}
}
func TestProductTimezoneChangeInvalidatesPreparedKit(t *testing.T) {
	s := fixture(t)
	previous, e := s.Prepare()
	must(t, e)
	must(t, os.WriteFile(s.path("voptrack-public/voptrack.json"), []byte(`{"product":"home","timezone":"Europe/Helsinki","web_interface":true}`), 0600))
	if s.Status().State != "stale" {
		t.Fatal("product change without server-config change stayed ready")
	}
	current, e := s.Prepare()
	must(t, e)
	if current.Configuration == previous.Configuration || current.Revision == previous.Revision {
		t.Fatal("product configuration was omitted from revision")
	}
	raw, e := s.readRevision(current)
	must(t, e)
	files, e := Validate(raw)
	must(t, e)
	if !bytes.Contains(files["voptrack-public/voptrack.json"], []byte("Europe/Helsinki")) {
		t.Fatal("product settings lost")
	}
}

func TestRequestRemovalRequiresDirectorySync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approval.json")
	must(t, os.WriteFile(path, []byte("synthetic approval"), 0600))
	syncFailure := errors.New("directory sync failed")
	called := false
	err := removeAndSync(path, func() error {
		called = true
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("sync preceded unlink", err)
		}
		return syncFailure
	})
	if !called || !errors.Is(err, syncFailure) {
		t.Fatal("acknowledged deletion without durable directory sync", err)
	}
	called = false
	if err := removeAndSync(path, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("unlink failure ignored", err)
	}
}

func refreshFixtureSettings(t *testing.T, s Store) {
	t.Helper()
	now := s.now()
	scope := "0123456789abcdef0123456789abcdef." + strings.Repeat("b", 32)
	revision := scope + "." + strings.Repeat("c", 32)
	o := sc.Observation{InstanceID: strings.Repeat("d", 32), ObservedAt: now, HistoryScope: "process_instance", Configured: true, Available: true, Enabled: true, Reason: "ready", Settings: &sc.Settings{EveryHours: 24, Keep: 7, Revision: revision}, SettingsScope: &scope, Next: &now, NextReason: "scheduled", Retention: &sc.Retention{Retained: 7, Target: 7, State: "converged", Reason: "at_target"}, Export: &sc.Export{Version: 1, Deployment: "0123456789abcdef0123456789abcdef", EveryHours: 24, Keep: 7, Revision: revision, ExportedAt: now}}
	must(t, bs.WithLive(s.path("voptrack-console"), func(v *bs.Live, save func() error) error { v.Observation = &o; return save() }))
}

func TestHomeSetupChoicesSurviveCommittedKitAndRemainBound(t *testing.T) {
	s := fixture(t)
	choices := homesetup.Choices{Version: 1, Name: "Synthetic Home", TimeZone: "Europe/Helsinki", Mode: "private", Encrypted: true}
	raw, _ := json.Marshal(choices)
	must(t, os.WriteFile(s.path("voptrack-bootstrap/home-setup.json"), raw, 0600))
	must(t, os.WriteFile(s.path("voptrack-bootstrap/home-password-retired"), []byte("initial password retired\n"), 0600))
	config, _ := s.read("voptrack-config/server.json")
	var c map[string]any
	must(t, json.Unmarshal(config, &c))
	c["display_name"] = choices.Name
	config, _ = json.Marshal(c)
	must(t, os.WriteFile(s.path("voptrack-config/server.json"), config, 0600))
	must(t, os.WriteFile(s.path("voptrack-public/voptrack.json"), []byte(`{"product":"home","timezone":"Europe/Helsinki","web_interface":true}`), 0600))
	m, e := s.Prepare()
	must(t, e)
	kit, e := s.readRevision(m)
	must(t, e)
	files, e := Validate(kit)
	must(t, e)
	if !bytes.Equal(files["voptrack-bootstrap/home-setup.json"], raw) {
		t.Fatal("setup decisions missing from kit")
	}
	target := t.TempDir()
	must(t, Restore(target, kit))
	restored, e := homesetup.ReadChoices(filepath.Join(target, "var/lib/voptrack-bootstrap/home-setup.json"))
	must(t, e)
	if restored != choices {
		t.Fatal("restoration lost Home setup choices")
	}
	choices.Mode = "off"
	raw, _ = json.Marshal(choices)
	must(t, os.WriteFile(s.path("voptrack-bootstrap/home-setup.json"), raw, 0600))
	if _, e = s.Prepare(); e == nil {
		t.Fatal("setup/console mismatch admitted")
	}
	previous, e := s.readRevision(m)
	must(t, e)
	if !bytes.Equal(previous, kit) {
		t.Fatal("failed setup snapshot replaced prior kit")
	}
}

func TestBrandingSurvivesKitAndPendingInputIsNotReady(t *testing.T) {
	s := fixture(t)
	input := []byte(`{"name":"Voptrack Demo","light":{"accent":"#0C6A61","surface":"#F2F2F7","text":"#111418"},"dark":{"accent":"#3FB8A9","surface":"#000000","text":"#F2F2F7"}}`)
	native, e := identitybrand.Native(input)
	must(t, e)
	raw, e := os.ReadFile(s.path("voptrack-config/server.json"))
	must(t, e)
	var cfg map[string]json.RawMessage
	must(t, json.Unmarshal(raw, &cfg))
	cfg["identity_brand"] = native
	raw, e = json.Marshal(cfg)
	must(t, e)
	must(t, os.WriteFile(s.path("voptrack-config/server.json"), raw, 0600))
	must(t, os.WriteFile(s.path("voptrack-config/identity-brand.json"), input, 0600))
	refreshFixtureSettings(t, s)
	m, e := s.Prepare()
	must(t, e)
	session, operator := recoverymaterial.ID(), recoverymaterial.ID()
	q, e := s.Request(session, operator)
	must(t, e)
	must(t, s.Approve(q.ID, m.Revision))
	archive, e := s.Deliver(q.ID, session, operator)
	must(t, e)
	files, e := Validate(archive)
	must(t, e)
	if !bytes.Equal(files["voptrack-config/identity-brand.json"], input) {
		t.Fatal("operator input missing from kit")
	}
	fresh := t.TempDir()
	must(t, Restore(fresh, archive))
	restored, e := os.ReadFile(filepath.Join(fresh, "var/lib/voptrack-config/identity-brand.json"))
	must(t, e)
	if !bytes.Equal(restored, input) {
		t.Fatal("branding lost on restore")
	}
	must(t, os.WriteFile(s.path("voptrack-config/identity-brand.json"), []byte(`{"name":"Changed Demo"}`), 0600))
	if s.Status().State == "ready" {
		t.Fatal("pending branding reported ready")
	}
}
