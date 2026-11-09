package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	bs "github.com/ekarpov/voptrack-server/internal/backupsettings"
	"github.com/ekarpov/voptrack-server/internal/recoverykit"
	"github.com/ekarpov/voptrack-server/internal/runtimeobs"
	"io"
	"net"
	"net/http"
	"time"
)

var kitDeliveries = make(chan struct{}, 2)

const kitBrowserWriteTimeout = 30 * time.Second

type kitIntent struct {
	ID, Revision string
	Expires      time.Time
}

func kitBinding(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:24]) }
func (a *app) kitCall(ctx context.Context, method, path string, in any) (*http.Response, error) {
	raw, _ := json.Marshal(in)
	req, e := http.NewRequestWithContext(ctx, method, "http://kit"+path, bytes.NewReader(raw))
	if e != nil {
		return nil, e
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", a.cfg.KitSocket)
	}}
	defer transport.CloseIdleConnections()
	return (&http.Client{Transport: transport, Timeout: 30 * time.Second}).Do(req)
}
func (a *app) recoveryData(r *http.Request, d map[string]any) {
	a.offMachineData(d)
	if a.cfg.RuntimeFile != "" {
		if receipt, e := runtimeobs.Read(a.cfg.RuntimeFile, time.Now()); e == nil {
			d["Runtime"] = receipt
		}
	}
	if a.cfg.KitSocket == "" {
		return
	}
	d["KitEnabled"] = true
	d["Kit"] = recoverykit.Metadata{State: "unknown"}
	response, e := a.kitCall(r.Context(), "GET", "/status", nil)
	if e != nil {
		return
	}
	defer response.Body.Close()
	var m recoverykit.Metadata
	if response.StatusCode == 200 && json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&m) == nil {
		d["Kit"] = m
	}
}
func (a *app) kitPage(w http.ResponseWriter, r *http.Request, sess *session, token, message string, status int) {
	d := a.backupData(r, token)
	d["KitMessage"] = message
	a.sessions.mu.Lock()
	if sess.Kit != nil {
		d["KitIntent"] = *sess.Kit
	}
	a.sessions.mu.Unlock()
	a.render(w, r, sess, "backups", status, d)
}
func (a *app) kitPrepare(w http.ResponseWriter, r *http.Request) { a.kitAction(w, r, false) }
func (a *app) kitRequest(w http.ResponseWriter, r *http.Request) { a.kitAction(w, r, true) }
func (a *app) kitAction(w http.ResponseWriter, r *http.Request, request bool) {
	sess, token := a.backupOperator(w, r)
	if sess == nil {
		return
	}
	if a.cfg.KitSocket == "" {
		a.kitPage(w, r, sess, token, "kit_unavailable", 503)
		return
	}
	if a.cfg.BackupSettingsFile != "" && !a.kitNativeCurrent(r, token) {
		a.kitPage(w, r, sess, token, "native_unavailable", 409)
		return
	}
	path := "/prepare"
	var in any
	if request {
		path = "/request"
		in = map[string]string{"Session": kitBinding(sess.ID), "Operator": kitBinding(sess.Account)}
	}
	response, e := a.kitCall(r.Context(), "POST", path, in)
	if e != nil {
		a.kitPage(w, r, sess, token, "kit_unavailable", 503)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		a.kitPage(w, r, sess, token, "kit_unavailable", 409)
		return
	}
	if request {
		var intent kitIntent
		if json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&intent) != nil || !recoverykit.ValidID(intent.ID) || !recoverykit.ValidID(intent.Revision) {
			a.kitPage(w, r, sess, token, "kit_unavailable", 503)
			return
		}
		a.sessions.mu.Lock()
		sess.Kit = &intent
		a.sessions.mu.Unlock()
	}
	a.kitPage(w, r, sess, token, "", 200)
}
func (a *app) kitDownload(w http.ResponseWriter, r *http.Request) {
	sess, token := a.backupOperator(w, r)
	if sess == nil {
		return
	}
	select {
	case kitDeliveries <- struct{}{}:
		defer func() { <-kitDeliveries }()
	default:
		a.kitPage(w, r, sess, token, "kit_unavailable", 503)
		return
	}
	a.sessions.mu.Lock()
	var intent kitIntent
	if sess.Kit != nil {
		intent = *sess.Kit
	}
	a.sessions.mu.Unlock()
	if intent.ID == "" || r.Form.Get("request") != intent.ID || !time.Now().Before(intent.Expires) {
		a.kitPage(w, r, sess, token, "kit_unavailable", 403)
		return
	}
	if a.cfg.BackupSettingsFile != "" && !a.kitNativeCurrent(r, token) {
		a.kitPage(w, r, sess, token, "native_unavailable", 409)
		return
	}
	response, e := a.kitCall(r.Context(), "POST", "/deliver", map[string]string{"ID": intent.ID, "Session": kitBinding(sess.ID), "Operator": kitBinding(sess.Account)})
	if e != nil {
		a.kitPage(w, r, sess, token, "kit_unavailable", 503)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		a.kitPage(w, r, sess, token, "kit_unavailable", 403)
		return
	}
	// The constrained service consumed its grant. Buffer the bounded private
	// response before browser headers; never stage a secret in Console storage.
	raw, e := io.ReadAll(io.LimitReader(response.Body, recoverykit.MaxBytes+1))
	if e != nil || len(raw) == 0 || len(raw) > recoverykit.MaxBytes || int64(len(raw)) != response.ContentLength {
		a.kitPage(w, r, sess, token, "kit_unavailable", 503)
		return
	}
	// Revalidate live operator authorization again after preparation/transfer.
	if current, _ := a.backupOperator(w, r); current == nil {
		return
	}
	deliveryUnavailable := false
	deliver := func() {
		a.sessions.mu.Lock()
		sess.Kit = nil
		a.sessions.mu.Unlock()
		started, err := writeKitBrowser(w, raw)
		deliveryUnavailable = err != nil && !started
	}
	if a.cfg.BackupSettingsFile == "" {
		deliver()
		if deliveryUnavailable {
			http.Error(w, "recovery kit unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	// Keep final observation, freshness/revision checks and emission under the
	// mutation locks. A local acknowledgement cannot slip between check and
	// browser headers, including while the buffered kit is being written.
	e = a.withNative(func(l *bs.Live, save func() error) error {
		var v nativeView
		if err := a.nativeDataLocked(r, token, l, save, &v); err != nil {
			return err
		}
		files, err := recoverykit.Validate(raw)
		var export struct {
			Revision string `json:"revision"`
		}
		if err != nil || !v.ExportCurrent || v.Observation == nil || v.Observation.Export == nil || json.Unmarshal(files["voptrack-bootstrap/backup-settings-export.json"], &export) != nil || export.Revision != v.Observation.Export.Revision {
			return recoverykit.ErrUnavailable
		}
		// A stalled reader holds the acknowledgement fence for at most the
		// bounded write interval. The consumed grant is never restored.
		deliver()
		return nil
	})
	if e != nil {
		a.kitPage(w, r, sess, token, "kit_stale", 409)
	} else if deliveryUnavailable {
		// Deadline failure happened before sensitive headers. Error emission is
		// outside the mutation fence because this writer cannot bound writes.
		http.Error(w, "recovery kit unavailable", http.StatusServiceUnavailable)
	}

}

func (a *app) kitNativeCurrent(r *http.Request, token string) bool {
	v := a.nativeData(r, token)
	return v.Message == "" && v.ExportCurrent && v.Observation != nil && v.Observation.Available && v.Observation.Export != nil && v.Observation.PendingRequestID == nil && (v.Command == nil || v.Command.Terminal())
}

// writeKitBrowser bounds the time a slow reader can retain the mutation fence.
// Unsupported deadline writers fail before any sensitive response headers.
func writeKitBrowser(w http.ResponseWriter, raw []byte) (bool, error) {
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(kitBrowserWriteTimeout)); err != nil {
		return false, err
	}
	defer controller.SetWriteDeadline(time.Time{})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Disposition", `attachment; filename="voptrack-recovery-kit.tar"`)
	_, err := w.Write(raw) // Successful writes do not establish custody.
	if err == nil {
		err = controller.Flush()
	} // Include net/http buffering in the deadline.
	return true, err
}
