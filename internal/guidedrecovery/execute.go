package guidedrecovery

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ekarpov/voptrack-server/internal/recoverykit"
	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
	rs "github.com/ekarpov/voptrack-server/internal/recoveryset"
	sc "github.com/ekarpov/voptrack-server/internal/schedulerclient"
)

func command(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "/usr/local/bin/tesserad", args...)
	// No inherited interactive input, command construction, diagnostics or secrets
	// in logs. The child sees only fixed offline flags and private host paths.
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/nonexistent"}
	return cmd.Run()
}
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func syncTree(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return ErrRefused
		}
		if d.IsDir() {
			return syncDir(path)
		}
		f, e := os.OpenFile(path, os.O_RDONLY|syscallNoFollow, 0)
		if e != nil {
			return e
		}
		defer f.Close()
		st, e := f.Stat()
		if e != nil || !st.Mode().IsRegular() {
			return ErrRefused
		}
		return f.Sync()
	})
}
func (s *Service) recover(ctx context.Context) error {
	// Accepted is already durable. Browser disconnects cannot cancel or replay it.
	v, ok := s.set(s.journal.Set)
	if !ok {
		return ErrRefused
	}
	_, r, e := rs.Stage(ctx, s.options(v), filepath.Join(s.config.State, "recheck"))
	if e != nil || binding(r, s.config.Target, s.journal.Attempt, v.ID) != s.journal.Binding {
		return ErrRefused
	}
	staged := s.staged()
	r, e = rs.Preflight(ctx, staged)
	if e != nil || binding(r, s.config.Target, s.journal.Attempt, v.ID) != s.journal.Binding {
		return ErrRefused
	}
	kit, settings, archive, evidence, e := rs.Materials(ctx, staged, filepath.Join(s.config.State, "materials"))
	if e != nil {
		return ErrRefused
	}
	raw, e := os.ReadFile(staged.TrustFile)
	if e != nil {
		return e
	}
	var lineage struct{ Deployment, Origin string }
	if json.Unmarshal(raw, &lineage) != nil {
		return ErrRefused
	}
	var exported sc.Export
	if json.Unmarshal(settings, &exported) != nil || !exported.Valid() {
		return ErrRefused
	}
	files, e := recoverykit.Validate(kit)
	if e != nil {
		return e
	}
	// Ignore all kit configuration except the validated local/reserve slot. The
	// temporary offline config is built from supported fields, never arbitrary paths.
	var original struct{ Backup struct{ Directory string } }
	if json.Unmarshal(files["voptrack-config/server.json"], &original) != nil {
		return ErrRefused
	}
	base := filepath.Join(s.config.Target, "var/lib")
	back := filepath.Join(base, "voptrack-backups")
	if original.Backup.Directory == "/var/lib/voptrack-backups/reserve" {
		back = filepath.Join(back, "reserve")
	}
	config := map[string]any{
		"listen": "127.0.0.1:8443", "issuer": lineage.Origin, "engine": "sqlite", "content": true,
		"data_dir": filepath.Join(base, "tessera"), "tls_certificate": filepath.Join(base, "voptrack-config/server.crt"),
		"tls_key": filepath.Join(base, "voptrack-config/server.key"), "encrypted_storage_confirmed": true,
		"backup": map[string]any{"directory": back, "key_file": filepath.Join(base, "voptrack-config/backup.key"), "every_hours": exported.EveryHours, "keep": exported.Keep},
	}
	configRaw, e := json.Marshal(config)
	if e != nil {
		return e
	}
	privateConfig := filepath.Join(s.config.State, "materials/offline.json")
	if e = recoverymaterial.Write(privateConfig, configRaw); e != nil {
		return e
	}
	privateSettings := filepath.Join(s.config.State, "materials/settings.json")
	if e = recoverymaterial.Write(privateSettings, settings); e != nil {
		return e
	}
	if ctx.Err() != nil || !absent(s.config.Target) {
		return ErrRefused
	}
	if e = s.transition("creating-target"); e != nil {
		return e
	}
	// Atomic reservation: even an existing empty directory is refused, untouched.
	if e = os.Mkdir(s.config.Target, 0700); e != nil {
		return e
	}
	if e = syncDir(filepath.Dir(s.config.Target)); e != nil {
		return e
	}
	if e = s.transition("importing-kit"); e != nil {
		return e
	}
	// Join the existing deployment maintenance contract before kit import.
	// The importer explicitly permits this lock in an otherwise fresh public dir.
	public := filepath.Join(base, "voptrack-public")
	if e = os.MkdirAll(public, 0700); e != nil {
		return e
	}
	maintenance, e := lock(public, "maintenance.lock")
	if e != nil {
		return e
	}
	defer maintenance.Close()
	if e = recoverykit.Restore(s.config.Target, kit); e != nil {
		return e
	}
	for _, p := range []string{filepath.Join(base, "tessera"), back} {
		if e = os.MkdirAll(p, 0700); e != nil {
			return e
		}
	}
	if e = recoverymaterial.Write(filepath.Join(base, "voptrack-bootstrap/backup-settings-export.json"), settings); e != nil {
		return e
	}
	if e = recoverymaterial.Write(filepath.Join(base, "voptrack-public/server.crt"), files["voptrack-config/server.crt"]); e != nil {
		return e
	}
	if e = syncTree(s.config.Target); e != nil {
		return e
	}
	if e = s.transition("recovering-producer"); e != nil {
		return e
	}
	if e = s.run(ctx, "-config", privateConfig, "-recover-backup-producer", "-expected-backup-producer", lineage.Deployment, "-backup-settings-bootstrap", privateSettings); e != nil {
		return e
	}
	if e = syncTree(s.config.Target); e != nil {
		return e
	}
	if e = s.transition("restoring-records"); e != nil {
		return e
	}
	if e = s.run(ctx, "-config", privateConfig, "-restore", archive, "-backup-key-file", filepath.Join(base, "voptrack-config/backup.key"), "-retirement-fence", evidence); e != nil {
		return e
	}
	// Retain the selected independent evidence with the restored deployment, as
	// required by the supported host restore workflow.
	if e = copyPrivate(evidence, filepath.Join(base, "tessera/dataset.db.retired")); e != nil {
		return e
	}
	if e = s.transition("verifying"); e != nil {
		return e
	}
	// Evidence is opaque and may be empty; the native restore validates it.
	for _, name := range []string{"tessera/dataset.db", "tessera/dataset.db.retired", "voptrack-config/backup.key", "voptrack-console/console-state.dataset", "voptrack-public/maintenance.lock"} {
		// app-only Home kits intentionally have no Console state.
		if name == "voptrack-console/console-state.dataset" && files[name] == nil {
			continue
		}
		st, e := os.Lstat(filepath.Join(base, name))
		if e != nil || !st.Mode().IsRegular() || (st.Size() == 0 && name != "voptrack-public/maintenance.lock" && name != "tessera/dataset.db.retired") {
			return ErrRefused
		}
	}
	if ctx.Err() != nil {
		return ErrRefused
	}
	return syncTree(s.config.Target)
}
func copyPrivate(from, to string) error {
	f, e := os.Open(from)
	if e != nil {
		return e
	}
	defer f.Close()
	// Evidence is already bounded and authenticated; avoid loading the full pair.
	out, e := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|syscallNoFollow, 0600)
	if e != nil {
		return e
	}
	_, e = io.Copy(out, f)
	if e == nil {
		e = out.Sync()
	}
	ce := out.Close()
	if e != nil {
		return e
	}
	return ce
}
