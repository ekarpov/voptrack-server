package backupreplication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	bc "github.com/ekarpov/voptrack-server/internal/backupcollector"
	"github.com/ekarpov/voptrack-server/internal/recoverykit"
	rs "github.com/ekarpov/voptrack-server/internal/recoveryset"
)

func (w *Worker) descriptor(ctx context.Context, id string) (d Descriptor, e error) {
	if !hexID.MatchString(id) {
		return d, ErrUnavailable
	}
	stream, e := w.Objects.Get(ctx, w.Config.key(id))
	if e != nil {
		return d, e
	}
	defer stream.Close()
	raw, e := io.ReadAll(io.LimitReader(stream, 65537))
	if e != nil || len(raw) > 65536 {
		return d, ErrUnavailable
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var extra any
	if dec.Decode(&d) != nil || dec.Decode(&extra) != io.EOF || d.ID != id || w.valid(d) != nil {
		return d, ErrConflict
	}
	return d, nil
}
func (w *Worker) List(ctx context.Context) ([]Descriptor, error) {
	keys, e := w.Objects.List(ctx, w.Config.namespace()+"commits/")
	if e != nil {
		return nil, e
	}
	out := []Descriptor{}
	for _, key := range keys {
		name := filepath.Base(key)
		if len(name) != 69 || name[64:] != ".json" {
			return nil, ErrConflict
		}
		id := name[:64]
		if key != w.Config.key(id) {
			return nil, ErrConflict
		}
		d, e := w.descriptor(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, nil
}
func (w *Worker) fetch(ctx context.Context, m Member, to string) error {
	stream, e := w.Objects.Get(ctx, m.Key)
	if e != nil {
		return e
	}
	defer stream.Close()
	f, e := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return ErrUnavailable
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(stream, m.Size+1))
	se := f.Sync()
	ce := f.Close()
	if e != nil || se != nil || ce != nil {
		return ErrUnavailable
	}
	if n != m.Size || hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		return ErrConflict
	}
	return nil
}

// Fetch is an explicit HOST recovery helper, not the unattended uploader.
// Its separately mounted kit is never sent to Objects and no backup key/trust
// is read here. Existing preflight with independent material is still required.
func Fetch(ctx context.Context, c Config, objects Objects, backup, evidence, target, kitPath, settingsPath string) error {
	if c.validate() != nil || objects == nil || !filepath.IsAbs(target) || filepath.Clean(target) != target || bc.PrivateDirectory(filepath.Dir(target)) != nil {
		return ErrUnavailable
	}
	w := Worker{Config: c, Objects: objects}
	b, e := w.descriptor(ctx, backup)
	if e != nil || b.Kind != "backup" {
		return ErrUnavailable
	}
	r, e := w.descriptor(ctx, evidence)
	if e != nil {
		return ErrUnavailable
	}
	// Refuse even an existing empty staging directory. Partial outputs survive.
	if os.Mkdir(target, 0700) != nil {
		return ErrUnavailable
	}
	input := filepath.Join(target, "inputs")
	if os.Mkdir(input, 0700) != nil {
		return ErrUnavailable
	}
	if e = w.fetch(ctx, b.Payload, filepath.Join(input, "backup.tar")); e != nil {
		return e
	}
	if e = w.fetch(ctx, r.Payload, filepath.Join(input, "evidence.tar")); e != nil {
		return e
	}
	var settings []byte
	if settingsPath != "" {
		settings, e = readSettings(settingsPath, c.Deployment)
	} else if b.Settings != nil {
		e = w.fetch(ctx, *b.Settings, filepath.Join(input, "settings.json"))
		if e == nil {
			settings, e = readSettings(filepath.Join(input, "settings.json"), c.Deployment)
		}
	} else {
		return ErrUnavailable
	}
	if e != nil {
		return ErrUnavailable
	}
	if settingsPath != "" && os.WriteFile(filepath.Join(input, "settings.json"), settings, 0600) != nil {
		return ErrUnavailable
	}
	f, e := os.OpenFile(kitPath, os.O_RDONLY|syscallNoFollowNonblock, 0)
	if e != nil {
		return ErrUnavailable
	}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		f.Close()
		return ErrUnavailable
	}
	kit, e := io.ReadAll(io.LimitReader(f, recoverykit.MaxBytes+1))
	f.Close()
	if e != nil || len(kit) > recoverykit.MaxBytes {
		return ErrUnavailable
	}
	if _, e = recoverykit.Validate(kit); e != nil {
		return ErrUnavailable
	}
	if os.WriteFile(filepath.Join(input, "kit.tar"), kit, 0600) != nil {
		return ErrUnavailable
	}
	selected := rs.Selection{Version: 1, Backup: rs.Input{File: "backup.tar", SHA256: b.Payload.SHA256}, Evidence: rs.Input{File: "evidence.tar", SHA256: r.Payload.SHA256}, Kit: rs.Input{File: "kit.tar", SHA256: sum(kit)}, Settings: rs.Input{File: "settings.json", SHA256: sum(settings)}}
	raw, _ := json.Marshal(selected)
	if os.WriteFile(filepath.Join(input, "selection.json"), raw, 0600) != nil {
		return ErrUnavailable
	}
	// Every emitted file and directory must be durable before successful staging.
	for _, name := range []string{"backup.tar", "evidence.tar", "settings.json", "kit.tar", "selection.json"} {
		f, e := os.Open(filepath.Join(input, name))
		if e != nil {
			return ErrUnavailable
		}
		e = f.Sync()
		f.Close()
		if e != nil {
			return ErrUnavailable
		}
	}
	for _, path := range []string{input, target, filepath.Dir(target)} {
		f, e := os.Open(path)
		if e != nil {
			return ErrUnavailable
		}
		e = f.Sync()
		f.Close()
		if e != nil {
			return ErrUnavailable
		}
	}
	return nil
}
