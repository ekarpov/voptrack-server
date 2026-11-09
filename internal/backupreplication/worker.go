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
	"sort"
	"syscall"
	"time"

	bc "github.com/ekarpov/voptrack-server/internal/backupcollector"
	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
	sc "github.com/ekarpov/voptrack-server/internal/schedulerclient"
)

type Member struct {
	Key, SHA256 string
	Size        int64
}
type Descriptor struct {
	Version              int
	ID, Deployment, Kind string
	Payload              Member
	Settings             *Member
	Published            time.Time
}
type Job struct {
	Version    int
	Descriptor Descriptor
	Phase      string
	Verified   time.Time
}
type Worker struct {
	outboxLock *os.File
	Config     Config
	Objects    Objects
	Now        func() time.Time
	lock       *os.File
}

func sum(b []byte) string             { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func (c Config) namespace() string    { return c.Prefix + bc.OpaqueID(c.Deployment) + "/" }
func (c Config) key(id string) string { return c.namespace() + "commits/" + id + ".json" }
func (c Config) binding() string {
	raw, _ := json.Marshal([]string{c.Region, c.Bucket, c.Prefix, c.Deployment, c.Endpoint, c.Outbox, c.ExpectedBucketOwner})
	return sum(raw)
}
func Open(c Config, objects Objects) (*Worker, error) {
	if c.validate() != nil || objects == nil || bc.PrivateDirectory(c.State) != nil || bc.PrivateDirectory(c.Outbox) != nil {
		return nil, ErrUnavailable
	}
	if c.StatusFile != "" && (!filepath.IsAbs(c.StatusFile) || filepath.Clean(c.StatusFile) != c.StatusFile || filepath.Base(c.StatusFile) != "off-machine.json" || bc.PrivateDirectory(filepath.Dir(c.StatusFile)) != nil) {
		return nil, ErrUnavailable
	}
	if c.StatusFile != "" && (within(c.State, c.StatusFile) || within(c.Outbox, c.StatusFile) || samePathOrFile(c.StatusFile, c.CredentialFile) || samePathOrFile(c.StatusFile, c.SettingsFile)) {
		return nil, ErrUnavailable
	}
	if c.State == c.Outbox || within(c.State, c.Outbox) || within(c.Outbox, c.State) {
		return nil, ErrUnavailable
	}
	f, e := os.OpenFile(filepath.Join(c.State, "worker.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if e != nil {
		return nil, ErrUnavailable
	}
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() || syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, ErrUnavailable
	}
	w := &Worker{Config: c, Objects: objects, Now: time.Now, lock: f}
	qlock, e := os.OpenFile(filepath.Join(c.Outbox, ".uploader-lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if e != nil {
		w.Close()
		return nil, ErrUnavailable
	}
	st, e := qlock.Stat()
	if e != nil || !st.Mode().IsRegular() || syscall.Flock(int(qlock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		qlock.Close()
		w.Close()
		return nil, ErrUnavailable
	}
	w.outboxLock = qlock
	path := filepath.Join(c.State, "binding.json")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		// Binding is created only for a genuinely fresh journal. Lost metadata
		// must not rebind existing acknowledgements to another account/bucket.
		entries, err := os.ReadDir(c.State)
		if err != nil {
			w.Close()
			return nil, ErrUnavailable
		}
		for _, entry := range entries {
			if entry.Name() != "worker.lock" {
				w.Close()
				return nil, ErrConflict
			}
		}
		raw, _ := json.Marshal(c.binding())
		e = recoverymaterial.Write(path, raw)
	} else if err != nil {
		e = ErrUnavailable
	} else {
		var binding string
		if bc.ReadPrivateJSON(path, &binding) != nil || binding != c.binding() {
			e = ErrConflict
		}
	}
	if e != nil {
		w.Close()
		return nil, e
	}
	return w, nil
}

// Status is a presentation output, never a private input or journal file.
func samePathOrFile(output, input string) bool {
	if input == "" {
		return false
	}
	absolute, err := filepath.Abs(input)
	if err != nil || absolute == output {
		return true
	}
	a, ae := os.Stat(output)
	b, be := os.Stat(input)
	return ae == nil && be == nil && os.SameFile(a, b)
}
func within(a, b string) bool {
	r, e := filepath.Rel(a, b)
	return e == nil && (r == "." || len(r) > 0 && r != ".." && !filepath.IsAbs(r) && !(len(r) >= 3 && r[:3] == "../"))
}
func (w *Worker) Close() {
	if w.outboxLock != nil {
		w.outboxLock.Close()
		w.outboxLock = nil
	}
	if w.lock != nil {
		w.lock.Close()
		w.lock = nil
	}
}
func (w *Worker) save(id string, j Job) error {
	raw, e := json.Marshal(j)
	if e != nil {
		return ErrUnavailable
	}
	return recoverymaterial.Write(filepath.Join(w.Config.State, id+".json"), raw)
}
func (w *Worker) job(r bc.Obligation) (j Job, e error) {
	if r.Queued.After(w.Now().Add(5 * time.Minute)) {
		return j, ErrConflict
	}
	id := bc.OpaqueID(r.Manifest.Operation.ID)
	path := filepath.Join(w.Config.State, id+".json")
	if _, err := os.Lstat(path); err == nil {
		if bc.ReadPrivateJSON(path, &j) != nil || j.Version != 1 || j.Descriptor.ID != id || !j.Descriptor.Published.Equal(r.Queued) || j.Descriptor.Payload.SHA256 != r.SHA256 || j.Descriptor.Payload.Size != r.Size || j.Descriptor.Kind != r.Manifest.Operation.Kind || r.Manifest.Operation.Pair == nil || r.Manifest.Operation.Pair.Deployment != w.Config.Deployment || w.valid(j.Descriptor) != nil {
			return j, ErrConflict
		}
		return j, nil
	} else if !os.IsNotExist(err) {
		return j, ErrUnavailable
	}
	if r.Manifest.Operation.Pair == nil || r.Manifest.Operation.Pair.Deployment != w.Config.Deployment {
		return j, ErrConflict
	}
	j = Job{Version: 1, Phase: "pending", Descriptor: Descriptor{Version: 1, ID: id, Deployment: bc.OpaqueID(w.Config.Deployment), Kind: r.Manifest.Operation.Kind, Published: r.Queued, Payload: Member{w.Config.namespace() + "payloads/" + r.SHA256, r.SHA256, r.Size}}}
	if w.Config.SettingsFile != "" {
		raw, err := readSettings(w.Config.SettingsFile, w.Config.Deployment)
		if err != nil {
			return j, err
		}
		h := sum(raw)
		j.Descriptor.Settings = &Member{w.Config.namespace() + "settings/" + h, h, int64(len(raw))}
		if recoverymaterial.Write(filepath.Join(w.Config.State, id+".settings"), raw) != nil {
			return j, ErrUnavailable
		}
	}
	if e = w.valid(j.Descriptor); e != nil {
		return j, e
	}
	e = w.save(id, j)
	return
}
func readSettings(path, deployment string) ([]byte, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, ErrUnavailable
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > 4096 {
		return nil, ErrUnavailable
	}
	raw, e := io.ReadAll(io.LimitReader(f, 4097))
	if e != nil || len(raw) > 4096 {
		return nil, ErrUnavailable
	}
	var v sc.Export
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&v) != nil || d.Decode(&extra) != io.EOF || !v.Valid() || v.Deployment != deployment {
		return nil, ErrUnavailable
	}
	return raw, nil
}
func (w *Worker) valid(d Descriptor) error {
	if d.Version != 1 || !hexID.MatchString(d.ID) || d.Deployment != bc.OpaqueID(w.Config.Deployment) || (d.Kind != "backup" && d.Kind != "evidence") || !hexID.MatchString(d.Payload.SHA256) || d.Payload.Size < 1 || d.Payload.Size > bc.MaxBundleBytes || d.Payload.Key != w.Config.namespace()+"payloads/"+d.Payload.SHA256 || d.Published.IsZero() {
		return ErrConflict
	}
	if d.Settings != nil && (!hexID.MatchString(d.Settings.SHA256) || d.Settings.Size < 1 || d.Settings.Size > 4096 || d.Settings.Key != w.Config.namespace()+"settings/"+d.Settings.SHA256) {
		return ErrConflict
	}
	return nil
}
func (w *Worker) verify(ctx context.Context, m Member) error {
	stream, e := w.Objects.Get(ctx, m.Key)
	if e != nil {
		return e
	}
	defer stream.Close()
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(stream, m.Size+1))
	if e != nil {
		return ErrUnavailable
	}
	if n != m.Size || hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		return ErrConflict
	}
	return nil
}
func (w *Worker) put(ctx context.Context, m Member, body io.ReadSeeker) error {
	if _, e := body.Seek(0, 0); e != nil {
		return ErrUnavailable
	}
	e := w.Objects.Put(ctx, m.Key, body, m.Size)
	// Every response, including a timeout after a successful PUT, is uncertain
	// until the same remote object's complete bytes have been read back.
	verified := w.verify(ctx, m)
	if verified == nil {
		return nil
	}
	if verified == ErrMissing && e != nil && e != ErrExists {
		return ErrUnavailable
	}
	return verified
}
func (w *Worker) verifyCommit(ctx context.Context, d Descriptor) error {
	raw, _ := json.Marshal(d)
	return w.verify(ctx, Member{w.Config.key(d.ID), sum(raw), int64(len(raw))})
}
func (w *Worker) transfer(ctx context.Context, r bc.Obligation, j *Job) error {
	if j.Phase == "verified" {
		if j.Verified.IsZero() || j.Verified.After(w.Now().Add(5*time.Minute)) {
			return ErrConflict
		}
		if w.Now().Sub(j.Verified) < 24*time.Hour {
			return nil
		}
		if e := w.verify(ctx, j.Descriptor.Payload); e != nil {
			return e
		}
		if j.Descriptor.Settings != nil {
			if e := w.verify(ctx, *j.Descriptor.Settings); e != nil {
				return e
			}
		}
		if e := w.verifyCommit(ctx, j.Descriptor); e != nil {
			return e
		}
		j.Verified = w.Now().UTC()
		return w.save(j.Descriptor.ID, *j)
	}
	q := bc.Outbox{Dir: w.Config.Outbox, Budget: w.Config.Budget}
	f, e := q.Payload(r)
	if e != nil {
		return ErrUnavailable
	}
	j.Phase = "uploading"
	if e = w.save(j.Descriptor.ID, *j); e != nil {
		f.Close()
		return e
	}
	e = w.put(ctx, j.Descriptor.Payload, f)
	f.Close()
	if e != nil {
		return e
	}
	j.Phase = "verifying"
	if e = w.save(j.Descriptor.ID, *j); e != nil {
		return e
	}
	if j.Descriptor.Settings != nil {
		raw, e := readSettings(filepath.Join(w.Config.State, j.Descriptor.ID+".settings"), w.Config.Deployment)
		if e != nil || int64(len(raw)) != j.Descriptor.Settings.Size || sum(raw) != j.Descriptor.Settings.SHA256 {
			return ErrConflict
		}
		if e = w.put(ctx, *j.Descriptor.Settings, bytes.NewReader(raw)); e != nil {
			return e
		}
	}
	j.Phase = "committing"
	if e = w.save(j.Descriptor.ID, *j); e != nil {
		return e
	}
	raw, _ := json.Marshal(j.Descriptor)
	if e = w.put(ctx, Member{w.Config.key(j.Descriptor.ID), sum(raw), int64(len(raw))}, bytes.NewReader(raw)); e != nil {
		return e
	}
	j.Phase = "verified"
	j.Verified = w.Now().UTC()
	// This durable state is the acknowledgement. Never release queue bytes first.
	return w.save(j.Descriptor.ID, *j)
}
func (w *Worker) Scan(ctx context.Context) error {
	q := bc.Outbox{Dir: w.Config.Outbox, Budget: w.Config.Budget}
	records, e := q.Records()
	if e != nil {
		var status bc.ReplicationStatus
		_ = bc.ReadPrivateJSON(filepath.Join(w.Config.State, "summary.json"), &status)
		status.Version = 1
		status.Label = w.Config.Label
		status.State = "unavailable"
		status.Observed = w.Now().UTC()
		_ = w.status(status)
		return ErrUnavailable
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Queued.Before(records[j].Queued) })
	status := bc.ReplicationStatus{Version: 1, Label: w.Config.Label, State: "unprotected", Observed: w.Now().UTC(), Budget: w.Config.Budget, IndependentDestination: w.Config.IndependentDestination && !w.Config.LocalFixture}
	if status.Budget == 0 {
		status.Budget = bc.DefaultOutboxBudget
	}
	{
		// Public status is presentation only, never destination/history authority.
		// Open has already bound this private state to the exact destination.
		var old bc.ReplicationStatus
		if bc.ReadPrivateJSON(filepath.Join(w.Config.State, "summary.json"), &old) == nil {
			status.LastVerified = old.LastVerified
			status.LastBackup = old.LastBackup
			status.LastEvidence = old.LastEvidence
		}
	}
	failed := false
	for _, r := range records {
		if q.HasPayload(r.Manifest.Operation.ID) {
			status.PendingBytes += r.Size
		}
		j, err := w.job(r)
		if err == nil && j.Phase == "verified" {
			if r.Manifest.Operation.Kind == "backup" && j.Verified.After(status.LastBackup) {
				status.LastBackup = j.Verified
			}
			if r.Manifest.Operation.Kind == "evidence" && j.Verified.After(status.LastEvidence) {
				status.LastEvidence = j.Verified
			}
		} else {
			status.Pending++
			if status.OldestPending.IsZero() {
				status.OldestPending = r.Queued
			}
		}
	}
	status.State = "copying"
	if len(records) == 0 {
		status.State = "unprotected"
	}
	if e = w.status(status); e != nil {
		return e
	}
	for _, r := range records {
		j, err := w.job(r)
		if err == nil {
			opCtx, cancel := context.WithTimeout(ctx, 9*time.Minute)
			err = w.transfer(opCtx, r, &j)
			cancel()
		}
		if err != nil {
			failed = true
			status.State = "unavailable"
			_ = w.status(status)
			continue
		}
		// An existing verified job may have crashed between acknowledgement and release.
		if e = q.Release(r); e != nil {
			failed = true
			continue
		}
		if j.Verified.After(status.LastVerified) {
			status.LastVerified = j.Verified
		}
		if r.Manifest.Operation.Kind == "backup" && j.Verified.After(status.LastBackup) {
			status.LastBackup = j.Verified
		}
		if r.Manifest.Operation.Kind == "evidence" && j.Verified.After(status.LastEvidence) {
			status.LastEvidence = j.Verified
		}
		if j.Descriptor.Settings != nil {
			status.SettingsAvailable = true
		}
		status.Observed = w.Now().UTC()
		_ = w.status(status)
	}
	status.Pending = 0
	status.PendingBytes = 0
	status.OldestPending = time.Time{}
	for _, r := range records {
		if q.HasPayload(r.Manifest.Operation.ID) {
			status.PendingBytes += r.Size
		}
		var j Job
		if bc.ReadPrivateJSON(filepath.Join(w.Config.State, bc.OpaqueID(r.Manifest.Operation.ID)+".json"), &j) != nil || j.Phase != "verified" {
			status.Pending++
			if status.OldestPending.IsZero() {
				status.OldestPending = r.Queued
			}
		}
	}
	status.State = "verified"
	if status.LastBackup.IsZero() {
		status.State = "unprotected"
	}
	if !status.LastVerified.IsZero() && w.Now().Sub(status.LastVerified) > 26*time.Hour {
		status.State = "stale"
	}
	if status.PendingBytes >= status.Budget-status.Budget/5 || (!status.OldestPending.IsZero() && w.Now().Sub(status.OldestPending) > 15*time.Minute) {
		status.State = "degraded"
	}
	if failed {
		status.State = "unavailable"
	}
	status.Observed = w.Now().UTC()
	if e = w.status(status); e != nil {
		return e
	}
	if failed {
		return ErrUnavailable
	}
	return nil
}
func (w *Worker) status(s bc.ReplicationStatus) error {
	raw, _ := json.Marshal(s)
	if e := recoverymaterial.Write(filepath.Join(w.Config.State, "summary.json"), raw); e != nil {
		return e
	}
	if w.Config.StatusFile == "" {
		return nil
	}
	return recoverymaterial.Write(w.Config.StatusFile, raw)
}
