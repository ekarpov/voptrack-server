package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ekarpov/voptrack-server/internal/backupsettings"
)

type backupSettingsIntent struct {
	Values                                        backupsettings.Values
	Revision, Generated, Archives, Nonce, Account string
	Expires                                       time.Time
}
type backupSettingsView struct {
	Ready, Pending, Confirm                                        bool
	Generated                                                      backupsettings.Generated
	Every, Keep, Destination, Revision, Generation, Nonce, Message string
	Removed                                                        []archive
}

func generation(g backupsettings.Generated) string {
	raw, _ := json.Marshal(g)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func (a *app) backupSettingsView() backupSettingsView {
	v := backupSettingsView{}
	if a.cfg.BackupSettingsFile == "" {
		return v
	}
	g, err := backupsettings.ReadGenerated(a.cfg.BackupSettingsFile)
	if err != nil {
		return v
	}
	r, err := backupsettings.Read(a.cfg.StateDir)
	if err != nil {
		return v
	}
	values := g.Values
	if r.Revision != "none" {
		values = r.Values
	}
	v.Ready = true
	v.Pending = r.Revision != g.RequestRevision || (r.Revision != "none" && r.Values != g.Values)
	v.Generated = g
	v.Every = strconv.Itoa(values.EveryHours)
	v.Keep = strconv.Itoa(values.Keep)
	v.Destination = values.Destination
	v.Revision = r.Revision
	v.Generation = generation(g)
	return v
}
func (a *app) openSlotRoot(slot string) (*os.Root, error) {
	if slot != "local" && slot != "reserve" {
		return nil, os.ErrNotExist
	}
	base, err := os.OpenRoot(a.cfg.BackupDir)
	if err != nil {
		return nil, err
	}
	if slot == "local" {
		return base, nil
	}
	defer base.Close()
	before, err := base.Lstat("reserve")
	if err != nil || !before.IsDir() {
		return nil, os.ErrNotExist
	}
	nested, err := base.OpenRoot("reserve")
	if err != nil {
		return nil, err
	}
	after, err := nested.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		nested.Close()
		return nil, os.ErrNotExist
	}
	return nested, nil
}
func (a *app) listSlot(slot string) ([]archive, string) {
	if a.cfg.BackupDir == "" {
		return nil, "backups_off"
	}
	root, err := a.openSlotRoot(slot)
	if err != nil {
		return nil, "backup_storage_unavailable"
	}
	defer root.Close()
	return listArchives(root)
}
func (a *app) archives() ([]archive, string) {
	slot := "local"
	if a.cfg.BackupSettingsFile != "" {
		g, err := a.currentBackupGenerated()
		if err != nil {
			return nil, "backup_storage_unavailable"
		}
		slot = g.Destination
	}
	return a.listSlot(slot)
}

func archiveSignature(list []archive) string {
	var b strings.Builder
	for _, ar := range list {
		b.WriteString(ar.Name)
		b.WriteString(ar.Version)
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(b.String())))
}
func (a *app) settingsResult(w http.ResponseWriter, r *http.Request, sess *session, token string, status int, v backupSettingsView) {
	d := a.backupData(r, token)
	d["Settings"] = v
	a.render(w, r, sess, "backups", status, d)
}
func (a *app) backupSettingsPreview(w http.ResponseWriter, r *http.Request) {
	sess, token := a.backupOperator(w, r)
	if sess == nil {
		return
	}
	v := a.backupSettingsView()
	if !v.Ready {
		a.backupMessage(w, r, sess, 503, "backup_settings_unavailable")
		return
	}
	baseline := v
	v.Every = r.Form.Get("every")
	v.Keep = r.Form.Get("keep")
	v.Destination = r.Form.Get("destination")
	h, e1 := strconv.Atoi(v.Every)
	k, e2 := strconv.Atoi(v.Keep)
	values := backupsettings.Values{Destination: v.Destination, EveryHours: h, Keep: k}
	if e1 != nil || e2 != nil || !values.Valid() {
		v.Message = "backup_settings_invalid"
		a.settingsResult(w, r, sess, token, 422, v)
		return
	}
	if r.Form.Get("revision") != baseline.Revision || r.Form.Get("generation") != baseline.Generation {
		v.Message = "backup_settings_stale"
		a.settingsResult(w, r, sess, token, 409, v)
		return
	}
	list, state := a.listSlot(values.Destination)
	if state != "backup_none" && state != "backup_storage_readable" {
		v.Message = "backup_storage_unavailable"
		a.settingsResult(w, r, sess, token, 503, v)
		return
	}
	v.Confirm = true
	v.Nonce = random(24)
	// A successful next backup itself occupies one retention slot. This is an
	// explicitly provisional list, never a deletion or atomic writer reservation.
	if len(list) >= k {
		v.Removed = list[k-1:]
	}
	a.sessions.mu.Lock()
	sess.BackupSettings = &backupSettingsIntent{Values: values, Revision: baseline.Revision, Generated: baseline.Generation, Archives: archiveSignature(list), Nonce: v.Nonce, Account: sess.Account, Expires: time.Now().Add(5 * time.Minute)}
	a.sessions.mu.Unlock()
	a.settingsResult(w, r, sess, token, 200, v)
}
func (a *app) backupSettingsConfirm(w http.ResponseWriter, r *http.Request) {
	sess, token := a.backupOperator(w, r)
	if sess == nil {
		return
	}
	a.sessions.mu.Lock()
	intent := sess.BackupSettings
	sess.BackupSettings = nil
	a.sessions.mu.Unlock()
	v := a.backupSettingsView()
	if intent == nil || intent.Account != sess.Account || r.Form.Get("confirmation") != intent.Nonce || !time.Now().Before(intent.Expires) {
		v.Message = "backup_settings_expired"
		a.settingsResult(w, r, sess, token, 409, v)
		return
	}
	list, state := a.listSlot(intent.Values.Destination)
	if !v.Ready || v.Revision != intent.Revision || v.Generation != intent.Generated || (state != "backup_none" && state != "backup_storage_readable") || archiveSignature(list) != intent.Archives {
		v.Message = "backup_settings_stale"
		a.settingsResult(w, r, sess, token, 409, v)
		return
	}
	if err := backupsettings.Save(a.cfg.StateDir, intent.Revision, intent.Values); err != nil {
		v.Message = "backup_settings_stale"
		a.settingsResult(w, r, sess, token, 409, v)
		return
	}
	// No server process, config file or archive is changed by this HTTP action.
	v = a.backupSettingsView()
	v.Message = "backup_settings_saved"
	a.settingsResult(w, r, sess, token, 200, v)
}

func (a *app) currentBackupGenerated() (backupsettings.Generated, error) {
	return backupsettings.ReadGenerated(a.cfg.BackupSettingsFile)
}
