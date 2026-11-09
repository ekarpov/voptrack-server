package main

import (
	"context"
	"errors"
	"fmt"
	ac "github.com/ekarpov/voptrack-server/internal/acquisitionclient"
	bc "github.com/ekarpov/voptrack-server/internal/backupcollector"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The Console never has the collector credential or the backup key. It reads
// published bundles and uses only the current person's ephemeral operator token.
func (a *app) onlineClient() (*ac.Client, error) { return ac.New(a.tessera.internal, a.tessera.http) }
func (a *app) manual(ctx context.Context, token string, create bool) (string, error) {
	journal, e := ac.OpenJournal(filepath.Join(a.cfg.StateDir, "backup-intents"))
	if e != nil {
		return "unknown", e
	}
	defer journal.Close()
	pointer := filepath.Join(a.cfg.StateDir, "backup-intents", "current")
	raw, e := os.ReadFile(pointer)
	var intent ac.Intent
	if e == nil {
		intent, e = journal.Load(strings.TrimSpace(string(raw)))
	}
	if e != nil && !os.IsNotExist(e) {
		return "unknown", e
	}
	// A durable local publication can outlive producer rotation. Remember that
	// it was observed so later local retention cannot turn it back into pending.
	if !create && intent.ID != "" {
		store := bc.Store{Dir: a.cfg.BundleDir}
		if _, err := store.Manifest(intent.ID); err == nil {
			if pin, err := store.Pin(intent.ID); err == nil {
				pin.Close()
				intent.State = "published"
				if err = journal.Save(intent); err != nil {
					return "unknown", err
				}
				return "ready", nil
			}
		}
	}
	terminal := intent.State == "published" || intent.State == "acquired" || intent.State == "failed" || intent.State == "cancelled" || intent.State == "expired"
	if !create {
		if intent.ID == "" {
			return "", nil
		}
		if terminal && intent.State != "acquired" {
			return intent.State, nil
		}
		// An uncollected complete result may have expired. Observe the same ID;
		// never leave an old producer result labelled as preparing indefinitely.
	}
	if create && (intent.ID == "" || terminal) {
		intent, e = journal.Create("backup", time.Now())
		if e != nil {
			return "unknown", e
		}
		f, e := os.CreateTemp(filepath.Dir(pointer), ".current-")
		if e != nil {
			return "unknown", e
		}
		defer os.Remove(f.Name())
		_, e = f.WriteString(intent.ID)
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil || ce != nil {
			return "unknown", ac.ErrJournal
		}
		if os.Rename(f.Name(), pointer) != nil {
			return "unknown", ac.ErrJournal
		}
		d, e := os.Open(filepath.Dir(pointer))
		if e != nil {
			return "unknown", e
		}
		e = d.Sync()
		d.Close()
		if e != nil {
			return "unknown", e
		}
	}
	if e = journal.ForgetOthers(intent.ID); e != nil {
		return "unknown", e
	}
	client, e := a.onlineClient()
	if e != nil {
		return "unknown", e
	}
	op, e := client.Status(ctx, intent.ID, intent.Kind, token)
	var refused *ac.Refusal
	// Only an explicit POST can submit/replay, and only with the already durable ID.
	submitted := false
	if create && errors.As(e, &refused) && refused.Code == "not_found" {
		submitted = true
		op, e = client.Start(ctx, intent.ID, intent.Kind, token)
	}
	state := "unknown"
	if e == nil {
		switch op.State {
		case "running":
			state = "creating"
		case "complete":
			state = "acquired"
		case "failed", "cancelled", "expired":
			state = op.State
		}
	}
	// A clear admission refusal is a failed request. An authorization or
	// transport failure while merely observing an existing operation is unknown.
	if submitted && errors.As(e, &refused) && refused.Code != "request_failed" {
		state = "failed"
	}
	if errors.As(e, &refused) && refused.Code == "expired" {
		state = "expired"
	}
	intent.State = state
	if saveErr := journal.Save(intent); saveErr != nil {
		return "unknown", saveErr
	}
	return state, e
}
func (a *app) onlineBackupNow(w http.ResponseWriter, r *http.Request, sess *session, token string) {
	state, _ := a.manual(r.Context(), token, true)
	key := "backup_online_uncertain"
	switch state {
	case "creating":
		key = "backup_online_creating"
	case "acquired":
		key = "backup_online_preparing"
	case "failed":
		key = "backup_failed"
	case "cancelled":
		key = "backup_online_cancelled"
	case "expired":
		key = "backup_online_expired"
	}
	a.backupMessage(w, r, sess, 200, key)
}
func (a *app) onlineData(r *http.Request, token string, d map[string]any) {
	d["Online"] = true
	d["CollectorState"] = "backup_online_status_unknown"
	if status, e := (bc.Store{Dir: a.cfg.BundleDir}).Status(); e == nil {
		switch status.State {
		case "preparing":
			d["CollectorState"] = "backup_online_preparing"
		case "idle":
			d["CollectorState"] = ""
		case "unavailable":
			d["CollectorState"] = "backup_online_collection_failed"
		}
	}
	d["BundleState"] = "backup_online_unavailable"
	bundles, e := (bc.Store{Dir: a.cfg.BundleDir}).List()
	if e == nil {
		d["BundleState"] = "backup_online_none"
		var backups []bc.Bundle
		var evidence []bc.Bundle
		for _, b := range bundles {
			if b.Operation.Kind == "backup" {
				backups = append(backups, b)
			} else {
				evidence = append(evidence, b)
			}
		}
		d["Bundles"] = backups
		d["Evidence"] = evidence
		if len(backups) > 0 {
			d["BundleState"] = "backup_online_ready"
		}
	}
	if state, e := a.manual(r.Context(), token, false); e == nil || state == "unknown" {
		switch state {
		case "creating":
			d["ManualState"] = "backup_online_creating"
		case "acquired":
			if d["CollectorState"] != "backup_online_collection_failed" {
				d["ManualState"] = "backup_online_preparing"
			}
		case "unknown":
			d["ManualState"] = "backup_online_uncertain"
		case "failed":
			d["ManualState"] = "backup_failed"
		case "cancelled":
			d["ManualState"] = "backup_online_cancelled"
		case "expired":
			d["ManualState"] = "backup_online_expired"
		}
	}
}
func (a *app) downloadBundle(w http.ResponseWriter, r *http.Request) {
	sess, _ := a.backupOperator(w, r)
	if sess == nil {
		return
	}
	select {
	case archiveDeliveries <- struct{}{}:
		defer func() { <-archiveDeliveries }()
	default:
		a.backupMessage(w, r, sess, 503, "backup_download_unavailable")
		return
	}
	store := bc.Store{Dir: a.cfg.BundleDir}
	id := r.PathValue("id")
	m, e := store.Manifest(id)
	if e != nil {
		a.backupMessage(w, r, sess, 404, "backup_download_unavailable")
		return
	}
	f, e := store.Pin(id)
	if e != nil {
		a.backupMessage(w, r, sess, 404, "backup_download_unavailable")
		return
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		a.backupMessage(w, r, sess, 404, "backup_download_unavailable")
		return
	}
	// Inode pin is acquired before headers. No range or newest-result fallback.
	w.Header().Set("Content-Type", "application/x-tar")
	if m.Operation.Kind == "evidence" {
		w.Header().Set("Content-Disposition", `attachment; filename="voptrack-evidence.tar"`)
	} else {
		w.Header().Set("Content-Disposition", `attachment; filename="voptrack-backup.tar"`)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprint(st.Size()))
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(2 * time.Minute))
	if _, e = io.CopyN(w, f, st.Size()); e != nil {
		panic(http.ErrAbortHandler)
	}
}
