package recoveryset

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	ac "github.com/ekarpov/voptrack-server/internal/acquisitionclient"
	bc "github.com/ekarpov/voptrack-server/internal/backupcollector"
	bs "github.com/ekarpov/voptrack-server/internal/backupsettings"
	"github.com/ekarpov/voptrack-server/internal/recoverykit"
	sc "github.com/ekarpov/voptrack-server/internal/schedulerclient"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T, variant string) Options {
	t.Helper()
	root := t.TempDir()
	private := t.TempDir()
	write := func(n string, b []byte) {
		must(t, os.MkdirAll(filepath.Dir(filepath.Join(root, "var/lib", n)), 0700))
		must(t, os.WriteFile(filepath.Join(root, "var/lib", n), b, 0600))
	}
	key := bytes.Repeat([]byte{3}, 32)
	deployment := strings.Repeat("a", 32)
	lineage := []byte(`{"deployment":"` + deployment + `","origin":"https://tesserad:8443"}`)
	settings := []byte(`{"version":1,"deployment":"` + deployment + `","every_hours":24,"keep":7,"revision":"` + deployment + `.` + strings.Repeat("b", 32) + `.` + strings.Repeat("c", 32) + `","exported_at":"2026-10-18T00:00:00Z"}`)
	k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, e)
	der, e := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: big.NewInt(1)}, &x509.Certificate{SerialNumber: big.NewInt(1)}, &k.PublicKey, k)
	must(t, e)
	pk, e := x509.MarshalECPrivateKey(k)
	must(t, e)
	for _, n := range []string{"voptrack-ca/root", "voptrack-config/server"} {
		write(n+".crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
		write(n+".key", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: pk}))
	}
	product := "home"
	if variant == "research" {
		product = "research"
	}
	for n, v := range map[string][]byte{
		"voptrack-public/voptrack.json":     []byte(`{"product":"` + product + `","timezone":"UTC","web_interface":true}`),
		"voptrack-config/backup.key":        key,
		"voptrack-config/server.json":       []byte(`{"engine":"sqlite","data_dir":"/var/lib/tessera","tls_key":"/var/lib/voptrack-config/server.key","tls_certificate":"/var/lib/voptrack-config/server.crt","encrypted_storage_confirmed":true,"backup":{"directory":"/var/lib/voptrack-backups","key_file":"/var/lib/voptrack-config/backup.key","every_hours":24,"keep":7}}`),
		"voptrack-bootstrap/owner-password": []byte("synthetic"), "voptrack-bootstrap/variant": []byte(variant), "voptrack-bootstrap/console": []byte("on"), "voptrack-bootstrap/recovery-lineage": []byte(strings.Repeat("d", 48)),
		"voptrack-collector/lineage.json": lineage, "voptrack-bootstrap/backup-settings-export.json": settings,
		"voptrack-console/home.secret": []byte("synthetic"), "voptrack-console/issuer-home-members.secret": []byte("synthetic"), "voptrack-console/analysis.secret": []byte("synthetic"), "voptrack-console/console-state.secret": []byte("synthetic"), "voptrack-console/console-state.dataset": []byte("synthetic"),
	} {
		write(n, v)
	}
	write("voptrack-public/maintenance.lock", []byte{})
	var export sc.Export
	must(t, json.Unmarshal(settings, &export))
	now := time.Now().UTC()
	export.ExportedAt = now
	scope := sc.Scope(export.Revision)
	observation := sc.Observation{InstanceID: strings.Repeat("d", 32), ObservedAt: now, HistoryScope: "process_instance", Configured: true, Available: true, Enabled: true, Reason: "ready", Settings: &sc.Settings{EveryHours: 24, Keep: 7, Revision: export.Revision}, SettingsScope: &scope, Next: &now, NextReason: "scheduled", Retention: &sc.Retention{Retained: 7, Target: 7, State: "converged", Reason: "at_target"}, Export: &export}
	must(t, bs.WithLive(filepath.Join(root, "var/lib/voptrack-console"), func(v *bs.Live, save func() error) error { v.Observation = &observation; return save() }))
	s := recoverykit.Store{Root: root, Private: private}
	m, e := s.Prepare()
	must(t, e)
	session, operator := strings.Repeat("e", 48), strings.Repeat("f", 48)
	q, e := s.Request(session, operator)
	must(t, e)
	must(t, s.Approve(q.ID, m.Revision))
	kit, e := s.Deliver(q.ID, session, operator)
	must(t, e)
	dir := t.TempDir()
	trustDir := t.TempDir()
	keyDir := t.TempDir()
	var selection Selection
	selection.Version = 1
	for _, role := range []string{"backup", "evidence", "kit", "settings"} {
		data := kit
		if role == "settings" {
			data = settings
		}
		if role == "backup" || role == "evidence" {
			data = bundle(t, deployment, key, role)
		}
		input := Input{role + ".dat", hash(data)}
		must(t, os.WriteFile(filepath.Join(dir, input.File), data, 0600))
		switch role {
		case "backup":
			selection.Backup = input
		case "evidence":
			selection.Evidence = input
		case "kit":
			selection.Kit = input
		case "settings":
			selection.Settings = input
		}
	}
	raw, e := json.Marshal(selection)
	must(t, e)
	must(t, os.WriteFile(filepath.Join(dir, "selection.json"), raw, 0600))
	trust := filepath.Join(trustDir, "lineage.json")
	keyPath := filepath.Join(keyDir, "backup.key")
	must(t, os.WriteFile(trust, lineage, 0600))
	must(t, os.WriteFile(keyPath, key, 0600))
	module, e := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/ekarpov/tessera").Output()
	must(t, e)
	return Options{dir, trust, keyPath, filepath.Join(strings.TrimSpace(string(module)), "tools/verify_acquisition.py")}
}
func bundle(t *testing.T, deployment string, key []byte, kind string) []byte {
	t.Helper()
	data := map[string][]byte{}
	var artifacts []ac.Artifact
	kinds := []string{"evidence"}
	if kind == "backup" {
		kinds = []string{"archive", "evidence"}
	}
	for i, k := range kinds {
		id := strings.Repeat(string(rune('a'+i)), 32)
		raw := []byte("synthetic-" + k)
		artifacts = append(artifacts, ac.Artifact{ID: id, Kind: k, Size: int64(len(raw)), SHA256: hash(raw)})
		data[id] = raw
	}
	pair := ac.Pair{Version: 1, Deployment: deployment, R: "opaque-boundary", Artifacts: artifacts}
	if kind == "backup" {
		pair.S = "opaque-snapshot"
	}
	raw, e := json.Marshal(pair)
	must(t, e)
	mac := hmac.New(sha256.New, key)
	mac.Write(append([]byte("TESSERA-ACQUISITION-1\n"), raw...))
	envelope, e := json.Marshal(map[string]string{"payload": base64.StdEncoding.EncodeToString(raw), "mac": hex.EncodeToString(mac.Sum(nil))})
	must(t, e)
	mid := strings.Repeat("c", 32)
	data[mid] = envelope
	artifacts = append(artifacts, ac.Artifact{ID: mid, Kind: "metadata", Size: int64(len(envelope)), SHA256: hash(envelope)})
	op := ac.Operation{ID: "1-" + strings.Repeat("d", 32), Kind: kind, State: "complete", Pair: &pair, Artifacts: artifacts}
	raw, e = json.Marshal(bc.Manifest{Version: 1, Operation: op})
	must(t, e)
	data["manifest.json"] = raw
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, b := range data {
		must(t, tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(b)), Mode: 0600}))
		_, e = tw.Write(b)
		must(t, e)
	}
	must(t, tw.Close())
	return buf.Bytes()
}
func TestProductsAndReadOnlyReceipt(t *testing.T) {
	for _, variant := range []string{"household", "research"} {
		t.Run(variant, func(t *testing.T) {
			o := fixture(t, variant)
			before, e := os.ReadDir(o.Directory)
			must(t, e)
			r, e := Preflight(context.Background(), o)
			must(t, e)
			if r.Status != "inputs-verified-not-restored" || r.Custody != "unverified" || r.Coverage != "unknown-after-selected-evidence" || len(r.Inputs) != 4 || r.CheckedAt.After(time.Now()) {
				t.Fatal("misleading receipt")
			}
			after, e := os.ReadDir(o.Directory)
			must(t, e)
			if len(before) != len(after) {
				t.Fatal("modified retained inputs")
			}
		})
	}
}
func changeSelection(t *testing.T, o Options, edit func(*Selection)) {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join(o.Directory, "selection.json"))
	must(t, e)
	var s Selection
	must(t, json.Unmarshal(raw, &s))
	edit(&s)
	raw, e = json.Marshal(s)
	must(t, e)
	must(t, os.WriteFile(filepath.Join(o.Directory, "selection.json"), raw, 0600))
}
func TestRefusals(t *testing.T) {
	for _, scenario := range []string{"wrong-key", "wrong-lineage", "missing", "changed", "traversal", "duplicate", "symlink", "fifo", "oversized", "settings-foreign", "bad-mac", "cancelled", "verifier-missing"} {
		t.Run(scenario, func(t *testing.T) {
			o := fixture(t, "household")
			ctx := context.Background()
			switch scenario {
			case "wrong-key":
				must(t, os.WriteFile(o.KeyFile, bytes.Repeat([]byte{8}, 32), 0600))
			case "wrong-lineage":
				must(t, os.WriteFile(o.TrustFile, []byte(`{"deployment":"`+strings.Repeat("f", 32)+`","origin":"https://tesserad:8443"}`), 0600))
			case "missing":
				must(t, os.Remove(filepath.Join(o.Directory, "kit.dat")))
			case "changed":
				must(t, os.WriteFile(filepath.Join(o.Directory, "backup.dat"), []byte("corrupt"), 0600))
			case "traversal":
				changeSelection(t, o, func(s *Selection) { s.Backup.File = "../backup.dat" })
			case "duplicate":
				changeSelection(t, o, func(s *Selection) { s.Evidence = s.Backup })
			case "symlink", "fifo":
				must(t, os.Remove(filepath.Join(o.Directory, "backup.dat")))
				if scenario == "symlink" {
					must(t, os.Symlink(o.KeyFile, filepath.Join(o.Directory, "backup.dat")))
				} else {
					must(t, syscall.Mkfifo(filepath.Join(o.Directory, "backup.dat"), 0600))
				}
			case "oversized":
				f, e := os.Create(filepath.Join(o.Directory, "backup.dat"))
				must(t, e)
				must(t, f.Truncate(bundleLimit+1))
				must(t, f.Close())
			case "settings-foreign":
				raw := []byte(`{"version":1}`)
				must(t, os.WriteFile(filepath.Join(o.Directory, "settings.dat"), raw, 0600))
				changeSelection(t, o, func(s *Selection) { s.Settings.SHA256 = hash(raw) })
			case "bad-mac":
				raw := bundle(t, strings.Repeat("a", 32), bytes.Repeat([]byte{8}, 32), "backup")
				must(t, os.WriteFile(filepath.Join(o.Directory, "backup.dat"), raw, 0600))
				changeSelection(t, o, func(s *Selection) { s.Backup.SHA256 = hash(raw) })
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "verifier-missing":
				o.Verifier = filepath.Join(t.TempDir(), "missing")
			}
			r, e := Preflight(ctx, o)
			if e != ErrInvalid || r.Status != "" {
				t.Fatal("invalid set received success")
			}
		})
	}
}

func TestUnsafeBundleMembers(t *testing.T) {
	for _, scenario := range []string{"path", "link", "duplicate", "extra", "truncated", "manifest-pair"} {
		t.Run(scenario, func(t *testing.T) {
			o := fixture(t, "research")
			raw, e := os.ReadFile(filepath.Join(o.Directory, "backup.dat"))
			must(t, e)
			if scenario == "truncated" {
				raw = raw[:len(raw)/2]
			} else {
				reader := tar.NewReader(bytes.NewReader(raw))
				var out bytes.Buffer
				writer := tar.NewWriter(&out)
				var firstHeader *tar.Header
				var firstRaw []byte
				for {
					h, e := reader.Next()
					if e == io.EOF {
						break
					}
					must(t, e)
					b, e := io.ReadAll(reader)
					must(t, e)
					if firstHeader == nil {
						copy := *h
						firstHeader = &copy
						firstRaw = bytes.Clone(b)
					}
					if scenario == "manifest-pair" && h.Name == "manifest.json" {
						var m bc.Manifest
						must(t, json.Unmarshal(b, &m))
						m.Operation.Pair.R = "forged-unbound-boundary"
						b, e = json.Marshal(m)
						must(t, e)
						h.Size = int64(len(b))
					}
					must(t, writer.WriteHeader(h))
					_, e = writer.Write(b)
					must(t, e)
				}
				h := &tar.Header{Name: "unexpected", Mode: 0600}
				switch scenario {
				case "path":
					h.Name = "../escape"
				case "link":
					h.Name = strings.Repeat("f", 32)
					h.Typeflag = tar.TypeSymlink
					h.Linkname = "manifest.json"
				case "duplicate":
					h = firstHeader
				}
				if scenario != "manifest-pair" {
					must(t, writer.WriteHeader(h))
					if scenario == "duplicate" {
						_, e = writer.Write(firstRaw)
						must(t, e)
					}
				}
				must(t, writer.Close())
				raw = out.Bytes()
			}
			must(t, os.WriteFile(filepath.Join(o.Directory, "backup.dat"), raw, 0600))
			changeSelection(t, o, func(s *Selection) { s.Backup.SHA256 = hash(raw) })
			r, e := Preflight(context.Background(), o)
			if e != ErrInvalid || r.Status != "" {
				t.Fatal("unsafe bundle received success")
			}
		})
	}
}
