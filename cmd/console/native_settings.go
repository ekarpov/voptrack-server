package main

import (
	"context"
	"errors"
	bs "github.com/ekarpov/voptrack-server/internal/backupsettings"
	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
	sc "github.com/ekarpov/voptrack-server/internal/schedulerclient"
	"net/http"
	"strconv"
	"time"
)

type nativeIntent struct {
	Payload                 sc.Payload
	Journal, Nonce, Account string
	Expires                 time.Time
}
type nativeView struct {
	Observation             *sc.Observation
	Command                 *bs.Command
	Journal, Message, Nonce string
	Confirm                 bool
	ExportCurrent           bool
	Every, Keep             int
}

func (a *app) schedulerClient() (*sc.Client, error) {
	return sc.New(a.tessera.internal, a.tessera.http)
}
func (a *app) observeNative(ctx context.Context, token string) (*sc.Observation, error) {
	c, e := a.schedulerClient()
	if e != nil {
		return nil, e
	}
	o, e := c.Observe(ctx, token, time.Now())
	if e != nil {
		return nil, e
	}
	return &o, nil
}
func (a *app) nativeData(r *http.Request, token string) nativeView {
	v := nativeView{Message: "native_unavailable"}
	e := a.withNative(func(l *bs.Live, save func() error) error {
		return a.nativeDataLocked(r, token, l, save, &v)
	})
	if e != nil {
		v.Message = "native_uncertain"
	}
	if o := v.Observation; o != nil && o.Settings != nil {
		v.Every = o.Settings.EveryHours
		v.Keep = o.Settings.Keep
	}
	return v
}

// Caller holds material then settings through observation, reconciliation and
// any final kit delivery check. Never invoke this without withNative.
func (a *app) nativeDataLocked(r *http.Request, token string, l *bs.Live, save func() error, v *nativeView) error {
	// Observe under the same material/settings locks as command admission
	// and acknowledgement; an older in-flight GET cannot overwrite R2.
	o, e := a.observeNative(r.Context(), token)
	if e != nil {
		return e
	}
	v.Observation = o
	v.Message = ""
	l.Observation = o
	if g, e := a.currentBackupGenerated(); e == nil {
		l.Destination = g.Destination
	}
	if command := l.Current(); command != nil && !command.Terminal() {
		c, e := a.schedulerClient()
		if e != nil {
			return e
		}
		op, e := c.Operation(r.Context(), command.ID, command.Payload, token, false)
		applyNativeResult(command, op, e)
	}
	v.Journal = l.Fingerprint()
	v.Command = l.Current()
	_, currentErr := l.CurrentExport(time.Now())
	v.ExportCurrent = currentErr == nil
	return save()
}

func applyNativeResult(c *bs.Command, o sc.Operation, e error) {
	var retained *sc.Refusal
	if o.Validate(c.ID, c.Payload) == nil && (e == nil || (errors.As(e, &retained) && retained.Code == "conflict" && retained.Operation != nil && o.State == "failed")) {
		c.State = o.State
		c.Reason = o.Failure
		c.Outcome = &o
		return
	}
	c.State = "uncertain"
	c.Reason = ""
	var refusal *sc.Refusal
	if errors.As(e, &refusal) {
		c.Reason = refusal.Reason
		switch refusal.Code {
		case "expired":
			c.State = "expired"
		case "conflict":
			if refusal.Reason == "request_scope_mismatch" {
				c.State = "scope_changed"
			}
		}
	}
}
func (a *app) nativePage(w http.ResponseWriter, r *http.Request, sess *session, token string, status int, v nativeView) {
	d := a.backupData(r, token)
	d["Native"] = v
	a.render(w, r, sess, "backups", status, d)
}
func (a *app) nativePreview(w http.ResponseWriter, r *http.Request) {
	sess, token := a.backupOperator(w, r)
	if sess == nil {
		return
	}
	v := a.nativeData(r, token)
	h, e1 := strconv.Atoi(r.Form.Get("every"))
	k, e2 := strconv.Atoi(r.Form.Get("keep"))
	p := sc.Payload{ExpectedRevision: r.Form.Get("effective_revision"), EveryHours: h, Keep: k}
	if e1 != nil || e2 != nil || !p.Valid() {
		v.Message = "backup_settings_invalid"
		a.nativePage(w, r, sess, token, 422, v)
		return
	}
	if v.Observation == nil || !v.Observation.Available || v.Observation.Settings == nil || v.Observation.PendingRequestID != nil || v.Observation.Settings.Revision != p.ExpectedRevision || v.Journal != r.Form.Get("journal") || (v.Command != nil && !v.Command.Terminal()) {
		v.Message = "backup_settings_stale"
		a.nativePage(w, r, sess, token, 409, v)
		return
	}
	v.Nonce = random(24)
	v.Confirm = true
	v.Every = h
	v.Keep = k
	a.sessions.mu.Lock()
	sess.NativeSettings = &nativeIntent{p, v.Journal, v.Nonce, sess.Account, time.Now().Add(5 * time.Minute)}
	a.sessions.mu.Unlock()
	a.nativePage(w, r, sess, token, 200, v)
}
func (a *app) nativeConfirm(w http.ResponseWriter, r *http.Request) {
	sess, token := a.backupOperator(w, r)
	if sess == nil {
		return
	}
	a.sessions.mu.Lock()
	intent := sess.NativeSettings
	sess.NativeSettings = nil
	a.sessions.mu.Unlock()
	if intent == nil || intent.Account != sess.Account || intent.Nonce != r.Form.Get("confirmation") || !time.Now().Before(intent.Expires) {
		a.backupMessage(w, r, sess, 409, "backup_settings_expired")
		return
	}
	e := a.withNative(func(l *bs.Live, save func() error) error {
		o, e := a.observeNative(r.Context(), token)
		if e != nil || !o.Available || o.Settings == nil || o.Settings.Revision != intent.Payload.ExpectedRevision || o.PendingRequestID != nil {
			return bs.ErrStale
		}
		if e := l.Begin(intent.Journal, intent.Payload, o.ObservedAt); e != nil {
			return e
		}
		l.Observation = o
		if g, e := a.currentBackupGenerated(); e == nil {
			l.Destination = g.Destination
		}
		if e := save(); e != nil {
			return e
		} // Exact ID + tuple is durable BEFORE any PUT.
		c, e := a.schedulerClient()
		if e != nil {
			return e
		}
		command := l.Current()
		op, err := c.Operation(r.Context(), command.ID, command.Payload, token, true)
		applyNativeResult(command, op, err)
		// An accepted/effective response is not an export or proof of pruning.
		return save()
	})
	if e != nil {
		a.backupMessage(w, r, sess, 409, "native_uncertain")
		return
	}
	http.Redirect(w, r, "/backups", http.StatusSeeOther)
}
func (a *app) nativeReconcile(w http.ResponseWriter, r *http.Request) {
	sess, token := a.backupOperator(w, r)
	if sess == nil {
		return
	}
	e := a.withNative(func(l *bs.Live, save func() error) error {
		command := l.Current()
		if command == nil || command.ID != r.Form.Get("request") || command.Terminal() {
			return bs.ErrStale
		}
		c, e := a.schedulerClient()
		if e != nil {
			return e
		}
		// Explicit POST retries the identical durable tuple. GET rendering only looks up.
		op, err := c.Operation(r.Context(), command.ID, command.Payload, token, true)
		applyNativeResult(command, op, err)
		return save()
	})
	if e != nil {
		a.backupMessage(w, r, sess, 409, "native_uncertain")
		return
	}
	http.Redirect(w, r, "/backups", http.StatusSeeOther)
}

// Same material lock ordering as kit preparation: material, then settings.
func (a *app) withNative(fn func(*bs.Live, func() error) error) error {
	return recoverymaterial.Locked(a.cfg.StateDir, func() error { return bs.WithLive(a.cfg.StateDir, fn) })
}
