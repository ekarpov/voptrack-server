package main

import (
	"net/http"
	"time"
)

func (a *app) startWebsiteSignIn(w http.ResponseWriter, r *http.Request, sess *session, p pending) {
	state, verifier := random(24), random(48)
	p.Website, p.Verifier, p.Expires = true, verifier, time.Now().Add(10*time.Minute)
	a.sessions.addPending(sess, state, p)
	http.Redirect(w, r, a.tessera.websiteAuthorizeURL(state, verifier), http.StatusSeeOther)
}

func (a *app) websiteApproved(w http.ResponseWriter, r *http.Request, sess *session, p pending, tok tokens, l Listing, who Account) {
	// Replace all dataset projections as one installation. Refresh rotation is
	// shared, rather than independently spending the same refresh token per store.
	a.sessions.mu.Lock()
	old := credentialConnections(sess)
	sess.Connections, sess.Owner = nil, nil
	sess.Website = &connection{tokens: tok}
	sess.Account, sess.PasswordSetBy = who.Account, who.PasswordSetBy != nil
	sess.Invited, sess.Moderation, sess.BackupSettings, sess.NativeSettings, sess.Kit = nil, nil, nil, nil, nil
	sess.RememberUntil = time.Time{}
	if who.ServerAdministrator && who.Installation.Administer {
		sess.Owner = sess.Website
	}
	for _, d := range l.Datasets {
		sess.Connections = append(sess.Connections, &connection{Dataset: d, Administer: who.Installation.Administer, authority: sess.Website})
	}
	a.sessions.mu.Unlock()
	a.discardErase(r.Context(), sess)
	for _, c := range old {
		c.mu.Lock()
		previous := c.tokens
		c.mu.Unlock()
		_ = a.tessera.revokeTokens(r.Context(), previous)
	}
	if err := a.sessions.complete(w, sess, tok.ConsoleSession.KeepSignedIn); err != nil {
		a.fail(w, r, nil, 503, "error", nil)
		return
	}
	a.docs.ensure(r.Context())
	target := participantReturn(p)
	if p.ReturnStudy == "" && who.ServerAdministrator && a.cfg.Variant == "research" {
		target = "/studies"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
