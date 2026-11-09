package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"syscall"
	"time"
)

// Only the pinned writer's timestamped encrypted archive namespace is exposed.
// A header check identifies the format; it cannot authenticate ciphertext.
var archiveName = regexp.MustCompile(`^tessera-backup-[0-9]{8}T[0-9]{6}\.[0-9]{9}Z\.enc$`)

var archiveDeliveries = make(chan struct{}, 2) // bound concurrent encrypted snapshots

const archiveHeader = "TESSERA-BACKUP-1\n"

type archive struct {
	Name     string
	Modified time.Time
	Size     int64
	Version  string
}

func archiveVersion(info os.FileInfo) string {
	st := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d:%d", st.Dev, st.Ino, info.Size(), info.ModTime().UnixNano()))))
}

// O_NOFOLLOW refuses even in-mount links; O_NONBLOCK prevents a replacement
// FIFO from blocking before f.Stat can refuse it. Root also confines lookup.
func openArchive(root *os.Root, name string) (*os.File, os.FileInfo, error) {
	if !archiveName.MatchString(name) {
		return nil, nil, os.ErrNotExist
	}
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() {
		return nil, nil, os.ErrNotExist
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, os.ErrNotExist
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) || info.Size() < int64(len(archiveHeader)+12+16) || info.Size() > (256<<20)+512 {
		f.Close()
		return nil, nil, os.ErrNotExist
	}
	header := make([]byte, len(archiveHeader))
	if _, err := io.ReadFull(f, header); err != nil || string(header) != archiveHeader {
		f.Close()
		return nil, nil, os.ErrNotExist
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

func listArchives(root *os.Root) ([]archive, string) {
	// Mode-zero storage is administratively unavailable even to a root-run host
	// check. Do not let root bypass turn this into a misleading readiness claim.
	st, e := root.Stat(".")
	if e != nil || st.Mode().Perm()&0444 == 0 || st.Mode().Perm()&0111 == 0 {
		return nil, "backup_storage_unavailable"
	}
	folder, err := root.Open(".")
	if err != nil {
		return nil, "backup_storage_unavailable"
	}
	defer folder.Close()
	entries, err := folder.ReadDir(-1)
	if err != nil {
		return nil, "backup_storage_unavailable"
	}
	out := []archive{}
	for _, e := range entries {
		if !archiveName.MatchString(e.Name()) {
			continue
		}
		f, info, err := openArchive(root, e.Name())
		// A rotating or unreadable candidate makes the observation incomplete.
		if err != nil {
			return nil, "backup_storage_unavailable"
		}
		f.Close()
		out = append(out, archive{Name: e.Name(), Modified: info.ModTime().UTC(), Size: info.Size(), Version: archiveVersion(info)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	if len(out) == 0 {
		return out, "backup_none"
	}
	return out, "backup_storage_readable"
}

// Require current server rights AND approval on this installation for every
// observation/action. No service credential supplies operator authority.
func (a *app) backupOperator(w http.ResponseWriter, r *http.Request) (*session, string) {
	sess := a.sessions.get(w, r, false)
	if sess == nil || !sess.signedIn() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return nil, ""
	}
	if sess.Owner != nil && (r.Method != "POST" || sess.checkCSRF(r)) {
		token, err := sess.Owner.token(r.Context(), a.tessera)
		if err == nil {
			who, err := a.tessera.account(r.Context(), token)
			if err == nil && who.ServerAdministrator && who.Installation.Administer {
				return sess, token
			}
		}
	}
	a.fail(w, r, sess, 403, "owner_only", nil)
	return nil, ""
}

type operatorOverview struct {
	ServerAdministrator bool `json:"server_administrator"`
	Server              *struct {
		LastBackupAt *time.Time `json:"last_backup_at"`
	} `json:"server"`
	Datasets []struct {
		ID    string `json:"id"`
		Usage *struct {
			Records  int64  `json:"records"`
			Bytes    int64  `json:"bytes"`
			MaxBytes *int64 `json:"max_bytes"`
		} `json:"usage"`
	} `json:"datasets"`
}

func (a *app) backupsPage(w http.ResponseWriter, r *http.Request) {
	sess, token := a.backupOperator(w, r)
	if sess == nil {
		return
	}
	a.render(w, r, sess, "backups", 200, a.backupData(r, token))
}
func (a *app) backupData(r *http.Request, token string) map[string]any {
	slot := r.URL.Query().Get("slot")
	if slot != "local" && slot != "reserve" {
		slot = "local"
		if g, err := a.currentBackupGenerated(); err == nil {
			slot = g.Destination
		}
	}
	list, state := a.listSlot(slot)
	d := map[string]any{"Archives": list, "Storage": state, "ArchiveSlot": slot, "Observed": time.Now().UTC()}
	var overview operatorOverview
	if err := a.tessera.call(r.Context(), "GET", "/admin/api/v1/overview", token, "", nil, 200, &overview); err == nil && overview.ServerAdministrator && overview.Server != nil {
		d["Overview"] = overview
		d["Latest"] = overview.Server.LastBackupAt
	}
	var discovery struct {
		Server struct {
			Version string `json:"version"`
		} `json:"server"`
	}
	if err := a.tessera.call(r.Context(), "GET", "/.well-known/tessera", "", "", nil, 200, &discovery); err == nil {
		d["Version"] = discovery.Server.Version
	}
	d["Settings"] = a.backupSettingsView()
	d["Native"] = a.nativeData(r, token)
	a.recoveryData(r, d)
	if a.cfg.BundleDir != "" {
		a.onlineData(r, token, d)
	}
	return d
}

func (a *app) backupNow(w http.ResponseWriter, r *http.Request) {
	sess, token := a.backupOperator(w, r)
	if sess == nil {
		return
	}
	if a.cfg.BundleDir != "" {
		a.onlineBackupNow(w, r, sess, token)
		return
	}
	name, err := a.tessera.backupNow(r.Context(), token)
	if err != nil || !archiveName.MatchString(name) {
		var refused *apiError
		if errors.As(err, &refused) {
			key := ""
			switch refused.Code {
			case "not_supported":
				key = "backup_not_supported"
			case "dataset_unavailable", "permission_denied", "authentication_failed", "invalid_request":
				key = "backup_failed"
			}
			if key != "" {
				a.backupMessage(w, r, sess, 502, key)
				return
			}
		}
		// The writer may have completed before the response was lost. Never retry.
		a.backupMessage(w, r, sess, 502, "backup_unknown")
		return
	}
	a.backupMessage(w, r, sess, 200, "backup_reported")
}

func (a *app) downloadArchive(w http.ResponseWriter, r *http.Request) {
	sess, _ := a.backupOperator(w, r)
	if sess == nil {
		return
	}
	reject := func() { a.backupMessage(w, r, sess, 404, "backup_download_unavailable") }
	if a.cfg.BackupDir == "" {
		reject()
		return
	}
	slot := r.URL.Query().Get("slot")
	if slot == "" {
		slot = "local"
		if g, e := a.currentBackupGenerated(); e == nil {
			slot = g.Destination
		}
	}
	if slot != "local" && slot != "reserve" {
		reject()
		return
	}
	root, err := a.openSlotRoot(slot)
	if err != nil {
		reject()
		return
	}
	defer root.Close()
	f, info, err := openArchive(root, r.PathValue("name"))
	if err != nil {
		reject()
		return
	}
	defer f.Close()
	if r.URL.Query().Get("version") != archiveVersion(info) {
		reject()
		return
	}
	select {
	case archiveDeliveries <- struct{}{}:
		defer func() { <-archiveDeliveries }()
	default:
		a.backupMessage(w, r, sess, 503, "backup_download_unavailable")
		return
	}
	// Snapshot encrypted bytes before headers: reject a growing/truncated source
	// without delivering a normal-length partial archive. No keys are involved.
	snapshot, err := os.CreateTemp("", "voptrack-archive-*")
	if err != nil {
		reject()
		return
	}
	if err := os.Remove(snapshot.Name()); err != nil {
		snapshot.Close()
		reject()
		return
	}
	defer snapshot.Close()
	if _, err = io.CopyN(snapshot, f, info.Size()); err != nil {
		reject()
		return
	}
	after, err := f.Stat()
	if err != nil || archiveVersion(after) != archiveVersion(info) {
		reject()
		return
	}
	if _, err = snapshot.Seek(0, io.SeekStart); err != nil {
		reject()
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+r.PathValue("name")+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	// The open descriptor survives unlink/rename rotation. Bound delivery to the
	// observed size. Short writes/reads abort the response, never a success page.
	if _, err := io.CopyN(w, snapshot, info.Size()); err != nil {
		panic(http.ErrAbortHandler)
	}
}

func (a *app) backupMessage(w http.ResponseWriter, r *http.Request, sess *session, status int, key string) {
	a.render(w, r, sess, "message", status, map[string]string{"Key": key, "Title": "server_status", "Retry": "/backups", "RetryLabel": "server_status"})
}
