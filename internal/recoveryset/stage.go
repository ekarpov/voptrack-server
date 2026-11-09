package recoveryset

import (
	"context"
	"encoding/json"
	bc "github.com/ekarpov/voptrack-server/internal/backupcollector"
	"github.com/ekarpov/voptrack-server/internal/recoverykit"
	"os"
	"path/filepath"
)

// Stage snapshots bounded inputs into a new private directory. Ancestors are
// operator-controlled. Callers remove staging on every terminal outcome.
func Stage(ctx context.Context, o Options, dir string) (Options, Receipt, error) {
	zero := Receipt{}
	if os.Mkdir(dir, 0700) != nil {
		return Options{}, zero, ErrInvalid
	}
	raw, e := read(filepath.Join(o.Directory, "selection.json"), 4096)
	var sel Selection
	if e != nil || decode(raw, &sel) != nil || sel.Version != 1 {
		return Options{}, zero, ErrInvalid
	}
	if os.WriteFile(filepath.Join(dir, "selection.json"), raw, 0600) != nil {
		return Options{}, zero, ErrInvalid
	}
	for role, input := range map[string]Input{"backup": sel.Backup, "evidence": sel.Evidence, "kit": sel.Kit, "settings": sel.Settings} {
		limit := int64(bundleLimit)
		if role == "kit" {
			limit = recoverykit.MaxBytes
		}
		if role == "settings" {
			limit = 4096
		}
		if !filename.MatchString(input.File) || !shaPattern.MatchString(input.SHA256) || input.File == "selection.json" || input.File == "trust" || input.File == "key" {
			return Options{}, zero, ErrInvalid
		}
		if snapshot(filepath.Join(o.Directory, input.File), filepath.Join(dir, input.File), limit, input.SHA256) != nil {
			return Options{}, zero, ErrInvalid
		}
	}
	for name, path := range map[string]string{"trust": o.TrustFile, "key": o.KeyFile} {
		limit := int64(4096)
		if name == "key" {
			limit = 32
		}
		b, e := read(path, limit)
		if e != nil || os.WriteFile(filepath.Join(dir, name), b, 0600) != nil {
			return Options{}, zero, ErrInvalid
		}
	}
	staged := Options{dir, filepath.Join(dir, "trust"), filepath.Join(dir, "key"), o.Verifier}
	receipt, e := Preflight(ctx, staged)
	return staged, receipt, e
}

// Materials authenticates again and extracts allowlisted regular members with
// the same bounded parser. No shell extraction or browser path reaches ingestion.
func Materials(ctx context.Context, o Options, dir string) (kit, settings []byte, archive, evidence string, err error) {
	err = ErrInvalid
	if _, e := Preflight(ctx, o); e != nil {
		return
	}
	raw, e := read(filepath.Join(o.Directory, "selection.json"), 4096)
	if e != nil {
		return
	}
	var sel Selection
	if decode(raw, &sel) != nil {
		return
	}
	trust, e := read(o.TrustFile, 4096)
	if e != nil {
		return
	}
	var lineage struct{ Deployment string }
	if json.Unmarshal(trust, &lineage) != nil {
		return
	}
	if os.Mkdir(dir, 0700) != nil {
		return
	}
	find := func(input Input, kind, role string) (string, error) {
		dest := filepath.Join(dir, role)
		if e := verifyBundle(ctx, filepath.Join(o.Directory, input.File), dest, o.KeyFile, lineage.Deployment, role, o.Verifier); e != nil {
			return "", e
		}
		raw, e := read(filepath.Join(dest, "manifest.json"), 1<<20)
		if e != nil {
			return "", e
		}
		var m bc.Manifest
		if decode(raw, &m) != nil {
			return "", ErrInvalid
		}
		for _, a := range m.Operation.Artifacts {
			if a.Kind == kind {
				return filepath.Join(dest, a.ID), nil
			}
		}
		return "", ErrInvalid
	}
	archive, e = find(sel.Backup, "archive", "backup")
	if e != nil {
		return
	}
	evidence, e = find(sel.Evidence, "evidence", "evidence")
	if e != nil {
		return
	}
	kit, e = read(filepath.Join(o.Directory, sel.Kit.File), recoverykit.MaxBytes)
	if e != nil {
		return
	}
	settings, e = read(filepath.Join(o.Directory, sel.Settings.File), 4096)
	if e != nil {
		return
	}
	err = nil
	return
}
