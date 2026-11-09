// Package recoveryset validates retained recovery inputs without touching a
// deployment. Authentication is delegated to Tessera's published verifier.
package recoveryset

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"syscall"
	"time"

	ac "github.com/ekarpov/voptrack-server/internal/acquisitionclient"
	bc "github.com/ekarpov/voptrack-server/internal/backupcollector"
	"github.com/ekarpov/voptrack-server/internal/recoverykit"
	sc "github.com/ekarpov/voptrack-server/internal/schedulerclient"
)

var ErrInvalid = errors.New("recovery set refused; retain inputs and review prerequisites")
var artifactName = regexp.MustCompile(`^[0-9a-f]{32}$`)
var filename = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)
var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var deploymentPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

const bundleLimit = 275 << 20

type Input struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}
type Selection struct {
	Version  int   `json:"version"`
	Backup   Input `json:"backup"`
	Evidence Input `json:"evidence"`
	Kit      Input `json:"kit"`
	Settings Input `json:"settings"`
}
type Receipt struct {
	Version         int               `json:"version"`
	CheckedAt       time.Time         `json:"checked_at"`
	SelectionSHA256 string            `json:"selection_sha256"`
	TrustSHA256     string            `json:"trust_sha256"`
	Inputs          map[string]string `json:"inputs_sha256"`
	Status          string            `json:"status"`
	Custody         string            `json:"off_machine_custody"`
	Coverage        string            `json:"later_erasure_coverage"`
}

// Options are trusted host paths, never HTTP parameters. Directory and its
// ancestors must be controlled by the operator. Input basenames cannot escape it.
type Options struct{ Directory, TrustFile, KeyFile, Verifier string }

func read(path string, limit int64) ([]byte, error) {
	f, e := open(path)
	if e != nil {
		return nil, ErrInvalid
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, ErrInvalid
	}
	return b, nil
}
func open(path string) (*os.File, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, ErrInvalid
	}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, ErrInvalid
	}
	return f, nil
}
func hash(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func decode(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return ErrInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func snapshot(from, to string, limit int64, expected string) error {
	f, e := open(from)
	if e != nil {
		return ErrInvalid
	}
	defer f.Close()
	out, e := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return ErrInvalid
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(out, h), io.LimitReader(f, limit+1))
	ce := out.Close()
	if e != nil || ce != nil || n > limit || hex.EncodeToString(h.Sum(nil)) != expected {
		return ErrInvalid
	}
	return nil
}

// Preflight snapshots selected files privately, verifies the complete selection,
// and removes staging on every exit. A receipt is evidence of these bytes only:
// it is neither restore authorization, a custody attestation nor latest policy.
func Preflight(ctx context.Context, o Options) (Receipt, error) {
	var zero Receipt
	raw, e := read(filepath.Join(o.Directory, "selection.json"), 4096)
	if e != nil {
		return zero, ErrInvalid
	}
	var sel Selection
	if decode(raw, &sel) != nil || sel.Version != 1 {
		return zero, ErrInvalid
	}
	trust, e := read(o.TrustFile, 4096)
	if e != nil {
		return zero, ErrInvalid
	}
	var lineage struct {
		Deployment string `json:"deployment"`
		Origin     string `json:"origin"`
	}
	if decode(trust, &lineage) != nil || !deploymentPattern.MatchString(lineage.Deployment) {
		return zero, ErrInvalid
	}
	key, e := read(o.KeyFile, 32)
	if e != nil || len(key) != 32 {
		return zero, ErrInvalid
	}
	private, e := os.MkdirTemp("", "voptrack-preflight-")
	if e != nil {
		return zero, ErrInvalid
	}
	defer os.RemoveAll(private)
	inputs := map[string]Input{"backup": sel.Backup, "evidence": sel.Evidence, "kit": sel.Kit, "settings": sel.Settings}
	seen := map[string]bool{}
	hashes := map[string]string{}
	for role, input := range inputs {
		if !filename.MatchString(input.File) || !shaPattern.MatchString(input.SHA256) || seen[input.File] || input.File == "selection.json" {
			return zero, ErrInvalid
		}
		seen[input.File] = true
		limit := int64(bundleLimit)
		if role == "kit" {
			limit = recoverykit.MaxBytes
		}
		if role == "settings" {
			limit = 4096
		}
		if snapshot(filepath.Join(o.Directory, input.File), filepath.Join(private, role), limit, input.SHA256) != nil {
			return zero, ErrInvalid
		}
		hashes[role] = input.SHA256
	}
	kit, e := read(filepath.Join(private, "kit"), recoverykit.MaxBytes)
	if e != nil {
		return zero, ErrInvalid
	}
	files, e := recoverykit.Validate(kit)
	if e != nil {
		return zero, ErrInvalid
	}
	if subtle.ConstantTimeCompare(key, files["voptrack-config/backup.key"]) != 1 {
		return zero, ErrInvalid
	}
	var kitLineage struct {
		Deployment string `json:"deployment"`
		Origin     string `json:"origin"`
	}
	if decode(files["voptrack-bootstrap/backup-lineage.json"], &kitLineage) != nil || kitLineage != lineage {
		return zero, ErrInvalid
	}
	settings, e := read(filepath.Join(private, "settings"), 4096)
	if e != nil {
		return zero, ErrInvalid
	}
	var export sc.Export
	if decode(settings, &export) != nil || !export.Valid() || export.Deployment != lineage.Deployment {
		return zero, ErrInvalid
	}
	if os.WriteFile(filepath.Join(private, "key"), key, 0600) != nil {
		return zero, ErrInvalid
	}
	for _, role := range []string{"backup", "evidence"} {
		if verifyBundle(ctx, filepath.Join(private, role), filepath.Join(private, role+"-members"), filepath.Join(private, "key"), lineage.Deployment, role, o.Verifier) != nil {
			return zero, ErrInvalid
		}
	}
	if ctx.Err() != nil {
		return zero, ErrInvalid
	}
	return Receipt{1, time.Now().UTC(), hash(raw), hash(trust), hashes, "inputs-verified-not-restored", "unverified", "unknown-after-selected-evidence"}, nil
}

func verifyBundle(ctx context.Context, path, dir, key, deployment, kind, verifier string) error {
	if os.Mkdir(dir, 0700) != nil {
		return ErrInvalid
	}
	f, e := open(path)
	if e != nil {
		return ErrInvalid
	}
	defer f.Close()
	tr := tar.NewReader(f)
	members := map[string]string{}
	sizes := map[string]int64{}
	var total int64
	for {
		hdr, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return ErrInvalid
		}
		if hdr.Typeflag != tar.TypeReg || hdr.Size < 0 || hdr.Size > bundleLimit || members[hdr.Name] != "" || len(members) >= 4 {
			return ErrInvalid
		}
		if hdr.Name != "manifest.json" && !artifactName.MatchString(hdr.Name) {
			return ErrInvalid
		}
		if hdr.Name == "manifest.json" && hdr.Size > 1<<20 {
			return ErrInvalid
		}
		total += hdr.Size
		if total > bundleLimit {
			return ErrInvalid
		}
		dest, e := os.OpenFile(filepath.Join(dir, hdr.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return ErrInvalid
		}
		h := sha256.New()
		_, e = io.Copy(io.MultiWriter(dest, h), tr)
		ce := dest.Close()
		if e != nil || ce != nil {
			return ErrInvalid
		}
		members[hdr.Name] = hex.EncodeToString(h.Sum(nil))
		sizes[hdr.Name] = hdr.Size
	}
	raw, e := read(filepath.Join(dir, "manifest.json"), 1<<20)
	if e != nil {
		return ErrInvalid
	}
	var manifest bc.Manifest
	if decode(raw, &manifest) != nil || manifest.Version != 1 {
		return ErrInvalid
	}
	op := manifest.Operation
	// A backup pair may also supply its own evidence; an evidence-only selection
	// is usual for later erasures. Opaque S/R values are never ordered by time.
	if !ac.ValidID(op.ID) || op.State != "complete" || op.Pair == nil || op.Pair.Version != 1 || op.Pair.Deployment != deployment || op.Pair.R == "" {
		return ErrInvalid
	}
	if (kind == "backup" && op.Kind != "backup") || (op.Kind != "backup" && op.Kind != "evidence") || (op.Kind == "backup" && op.Pair.S == "") || (op.Kind == "evidence" && op.Pair.S != "") {
		return ErrInvalid
	}
	expected := 3
	if op.Kind == "evidence" {
		expected = 2
	}
	if len(op.Artifacts) != expected || len(members) != expected+1 {
		return ErrInvalid
	}
	kinds := map[string]bool{}
	ids := map[string]bool{}
	var metadata string
	var paired []ac.Artifact
	for _, a := range op.Artifacts {
		if ac.ValidateArtifact(a) != nil || ids[a.ID] || kinds[a.Kind] || members[a.ID] != a.SHA256 || sizes[a.ID] != a.Size {
			return ErrInvalid
		}
		ids[a.ID] = true
		kinds[a.Kind] = true
		if a.Kind == "metadata" {
			metadata = a.ID
		} else {
			paired = append(paired, a)
		}
	}
	if !kinds["metadata"] || !kinds["evidence"] || (op.Kind == "backup") != kinds["archive"] || len(paired) != len(op.Pair.Artifacts) {
		return ErrInvalid
	}
	pairIDs := map[string]bool{}
	for _, a := range op.Pair.Artifacts {
		found := false
		for _, p := range paired {
			if a == p {
				found = true
				break
			}
		}
		if !found || pairIDs[a.ID] {
			return ErrInvalid
		}
		pairIDs[a.ID] = true
	}

	raw, e = read(filepath.Join(dir, metadata), 1<<20)
	if e != nil {
		return ErrInvalid
	}
	var envelope struct {
		Payload string `json:"payload"`
		MAC     string `json:"mac"`
	}
	if decode(raw, &envelope) != nil {
		return ErrInvalid
	}
	payload, e := base64.StdEncoding.Strict().DecodeString(envelope.Payload)
	if e != nil {
		return ErrInvalid
	}
	var pair ac.Pair
	if decode(payload, &pair) != nil || !reflect.DeepEqual(pair, *op.Pair) {
		return ErrInvalid
	}
	cmd := exec.CommandContext(ctx, "python3", verifier, "--metadata", filepath.Join(dir, metadata), "--key-file", key, "--deployment", deployment)
	// Verifier output is deliberately suppressed: no paths or identities escape.
	if cmd.Run() != nil {
		return ErrInvalid
	}
	return nil
}
