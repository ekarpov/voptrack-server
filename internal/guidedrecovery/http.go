package guidedrecovery

import (
	"crypto/subtle"
	"crypto/tls"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
)

type session struct {
	CSRF             string
	Expires          time.Time
	Authorized       bool
	Confirm, Binding string
	ConfirmExpires   time.Time
}

func same(a, b string) bool {
	return len(a) > 0 && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func (s *Service) expire() {
	now := s.now()
	authorized := false
	for id, v := range s.sessions {
		if !now.Before(v.Expires) {
			delete(s.sessions, id)
		} else if v.Authorized {
			authorized = true
		}
	}
	if !authorized && s.journal.Phase == "review" {
		s.journal.Phase = "refused"
		s.journal.Binding = ""
		s.journal.Checked = nil
		if s.cleanup() != nil || s.save() != nil {
			s.journal.Phase = "stopped"
		}
	}
}
func (s *Service) Expire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.expire()
	}
}
func (s *Service) cookie(w http.ResponseWriter, id string, expiry time.Time) {
	http.SetCookie(w, &http.Cookie{Name: "__Host-recovery", Value: id, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: expiry})
}
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	origin, _ := url.Parse(s.config.Origin)
	// TLS must terminate here; forwarded headers never establish protection.
	if r.TLS == nil || r.TLS.Version < tls.VersionTLS12 || r.Host != origin.Host || r.URL.RawQuery != "" && r.Method != "GET" {
		publicError(w, language(r.URL.Query().Get("lang")), http.StatusForbidden)
		return
	}
	if r.Method != "GET" && r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		publicError(w, language(r.URL.Query().Get("lang")), 503)
		return
	}
	s.expire()
	lang := language(r.URL.Query().Get("lang"))
	var sess *session
	cookie, e := r.Cookie("__Host-recovery")
	if e == nil {
		sess = s.sessions[cookie.Value]
	}
	if r.Method == "GET" {
		if r.URL.Path != "/" {
			w.WriteHeader(404)
			return
		}
		if sess == nil {
			if len(s.sessions) >= 64 {
				publicError(w, lang, 429)
				return
			}
			id := recoverymaterial.ID()
			sess = &session{CSRF: recoverymaterial.ID(), Expires: s.now().Add(5 * time.Minute)}
			s.sessions[id] = sess
			s.cookie(w, id, sess.Expires)
		}
		s.render(w, lang, sess, "")
		return
	}
	// Authentication itself is CSRF protected using a pre-authentication session.
	if r.Header.Get("Origin") != s.config.Origin || r.Header.Get("Sec-Fetch-Site") == "cross-site" || sess == nil {
		publicError(w, lang, 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.ParseForm() != nil {
		publicError(w, lang, 400)
		return
	}
	lang = language(r.PostForm.Get("lang"))
	// Admission checks the time after the bounded body read, too. A request that
	// starts before expiry must not gain authority by finishing after it.
	if !s.now().Before(sess.Expires) {
		s.expire()
		publicError(w, lang, 403)
		return
	}

	for key, values := range r.PostForm {
		if len(values) != 1 || !strings.Contains("|csrf|lang|token|set|target|confirm|attempt|binding|", "|"+key+"|") {
			publicError(w, lang, 400)
			return
		}
	}
	if !same(r.PostForm.Get("csrf"), sess.CSRF) {
		publicError(w, lang, 403)
		return
	}
	if r.URL.Path == "/auth" {
		if sess.Authorized || s.journal.AuthDigest == "" || s.journal.AuthConsumed || !s.now().Before(s.journal.AuthExpires) || s.journal.AuthAttempts >= 5 {
			publicError(w, lang, 403)
			return
		}
		s.journal.AuthAttempts++
		valid := same(digest([]byte(r.PostForm.Get("token"))), s.journal.AuthDigest)
		if valid {
			s.journal.AuthConsumed = true
		}
		// A lost authentication response still consumes authority. Restart never
		// restores that browser session; the host must issue new authorization.
		if s.save() != nil || !valid {
			publicError(w, lang, 403)
			return
		}
		delete(s.sessions, cookie.Value)
		id := recoverymaterial.ID()
		expiry := s.now().Add(15 * time.Minute)
		if expiry.After(s.journal.AuthExpires) {
			expiry = s.journal.AuthExpires
		}
		s.sessions[id] = &session{CSRF: recoverymaterial.ID(), Expires: expiry, Authorized: true}
		s.cookie(w, id, expiry)
		http.Redirect(w, r, "/?lang="+lang, 303)
		return
	}
	if !sess.Authorized {
		publicError(w, lang, 403)
		return
	}
	switch r.URL.Path {
	case "/select":
		if s.journal.Phase != "review" && s.journal.Phase != "select" && s.journal.Phase != "refused" {
			publicError(w, lang, 409)
			return
		}
		sess.Confirm = ""
		sess.Binding = ""
		s.journal.Phase = "select"
		s.journal.Binding = ""
		s.journal.Checked = nil
		if s.cleanup() != nil || s.save() != nil {
			s.journal.Phase = "stopped"
			s.renderError(w, lang, sess)
			return
		}
	case "/validate":
		if r.PostForm.Get("target") != "fresh" || s.validate(r.Context(), r.PostForm.Get("set")) != nil {
			sess.Confirm = ""
			s.renderError(w, lang, sess)
			return
		}
		sess.Confirm = ""
		sess.Binding = ""
	case "/confirm":
		if s.journal.Phase != "review" || !same(sess.Confirm, r.PostForm.Get("confirm")) || !s.now().Before(sess.ConfirmExpires) || sess.Binding != s.journal.Binding || r.PostForm.Get("binding") != s.journal.Binding || r.PostForm.Get("attempt") != s.journal.Attempt || r.PostForm.Get("set") != s.journal.Set || r.PostForm.Get("target") != "fresh" {
			publicError(w, lang, 409)
			return
		}
		sess.Confirm = ""
		s.journal.Phase = "accepted"
		if s.save() != nil {
			s.journal.Phase = "stopped"
			s.renderError(w, lang, sess)
			return
		}
		s.workers.Add(1)
		go s.execute()
	case "/logout":
		delete(s.sessions, cookie.Value)
		s.cookie(w, "", s.now().Add(-time.Hour))
		s.expire()
	default:
		w.WriteHeader(404)
		return
	}
	http.Redirect(w, r, "/?lang="+lang, 303)
}

type verifiedInput struct{ Label, SHA256 string }
type view struct {
	ErrorOnly bool

	Inputs []verifiedInput

	Lang, CSRF, Phase, Attempt, Set, Label, Confirm, Binding, Error string
	Authorized                                                      bool
	Sets                                                            []Set
	Text                                                            map[string]string
}

// Refused requests never render private state or echo submitted values.
func publicError(w http.ResponseWriter, lang string, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = page.Execute(w, view{Lang: lang, Text: translations[lang], Error: tr(lang, "refused"), ErrorOnly: true})
}
func (s *Service) renderError(w http.ResponseWriter, lang string, sess *session) {
	w.WriteHeader(400)
	s.render(w, lang, sess, tr(lang, "refused"))
}
func (s *Service) render(w http.ResponseWriter, lang string, sess *session, err string) {
	v := view{Lang: lang, CSRF: sess.CSRF, Authorized: sess.Authorized, Text: translations[lang], Error: err}
	if sess.Authorized {
		v.Phase = s.journal.Phase
		v.Attempt = s.journal.Attempt
		v.Set = s.journal.Set
		v.Sets = s.config.Sets
		if set, ok := s.set(v.Set); ok {
			v.Label = set.Label
		}
		if v.Phase == "review" {
			if sess.Confirm == "" || sess.Binding != s.journal.Binding || !s.now().Before(sess.ConfirmExpires) {
				sess.Confirm = recoverymaterial.ID()
				sess.Binding = s.journal.Binding
				sess.ConfirmExpires = s.now().Add(5 * time.Minute)
			}
			if s.journal.Checked != nil {
				for _, role := range []string{"backup", "evidence", "kit", "settings"} {
					v.Inputs = append(v.Inputs, verifiedInput{tr(lang, role), s.journal.Checked.Inputs[role]})
				}
			}
			v.Confirm = sess.Confirm
			v.Binding = sess.Binding
		}
	}
	_ = page.Execute(w, v)
}

var page = template.Must(template.New("recovery").Funcs(template.FuncMap{"langs": func() []string { return languages }}).Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Text.title}}</title>
<style>
:root{color-scheme:light dark;--bg:#f3f5f4;--paper:#fff;--ink:#172c2b;--muted:#445956;--line:#aabbb5;--accent:#16675b}
@media(prefers-color-scheme:dark){:root{--bg:#121d1c;--paper:#1b2a28;--ink:#f1f6f3;--muted:#bfd0c8;--line:#60766f;--accent:#9ae2c5}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font:1rem/1.6 system-ui,sans-serif}
main{max-width:50rem;margin:2rem auto;padding:2rem;overflow-wrap:anywhere;background:var(--paper);border:1px solid var(--line);border-radius:1rem}
nav{display:flex;flex-wrap:wrap;gap:.8rem}a{color:var(--accent)}h1{font-size:clamp(1.6rem,5vw,2.3rem);line-height:1.2}h2{font-size:1.3rem}
label{display:block;font-weight:600;margin-top:1rem}input,select,button{font:inherit;max-width:100%;padding:.7rem;border-radius:.4rem;border:1px solid var(--line);background:var(--paper);color:var(--ink)}
select,input{width:100%}button{margin-top:1rem;cursor:pointer;font-weight:650;color:var(--accent)}:focus-visible{outline:3px solid var(--accent);outline-offset:4px}
.notice{padding:1rem;border-left:4px solid var(--accent);background:var(--bg)}.error{border:2px solid var(--accent);padding:1rem}code{overflow-wrap:anywhere}small{color:var(--muted)}
@media(max-width:36rem){main{margin:0;padding:1.2rem;border-radius:0;min-height:100vh}button{width:100%}}
</style></head><body><main><nav aria-label="{{.Text.language}}">{{range $l:=langs}}<a href="/?lang={{$l}}" lang="{{$l}}">{{$l}}</a>{{end}}</nav>
<h1>{{.Text.title}}</h1><p>{{.Text.scope}}</p>{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
{{if .ErrorOnly}}<p><a href="/?lang={{.Lang}}">{{.Text.refresh}}</a></p>
{{else if not .Authorized}}
<h2>{{.Text.enter}}</h2><form action="/auth" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="lang" value="{{.Lang}}"><label for="token">{{.Text.authorization}}</label><input id="token" name="token" type="password" autocomplete="off" required maxlength="96"><button>{{.Text.enter}}</button></form>
{{else}}
{{if or (eq .Phase "select") (eq .Phase "refused")}}
<h2>{{.Text.select}}</h2><form action="/validate" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="lang" value="{{.Lang}}"><label for="set">{{.Text.source}}</label><select name="set" id="set">{{range .Sets}}<option value="{{.ID}}">{{.Label}}</option>{{end}}</select><label for="target">{{.Text.target}}</label><select name="target" id="target"><option value="fresh">{{.Text.fresh}}</option></select><p>{{.Text.preserve}}</p><button>{{.Text.validate}}</button></form>
{{else if eq .Phase "review"}}
<h2>{{.Text.review}}</h2><dl><dt>{{.Text.source}}</dt><dd>{{.Label}}</dd><dt>{{.Text.target}}</dt><dd>{{.Text.fresh}}</dd><dt>{{.Text.attempt}}</dt><dd><code>{{.Attempt}}</code></dd></dl>
<p class="notice">{{.Text.verified}}</p><dl>{{range .Inputs}}<dt>{{.Label}}</dt><dd><small>SHA-256</small> <code>{{.SHA256}}</code></dd>{{end}}</dl><p>{{.Text.limits}}</p><p>{{.Text.steps}}</p><p>{{.Text.downtime}}</p><p>{{.Text.cutover}}</p>
<form action="/confirm" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="lang" value="{{.Lang}}"><input type="hidden" name="confirm" value="{{.Confirm}}"><input type="hidden" name="attempt" value="{{.Attempt}}"><input type="hidden" name="binding" value="{{.Binding}}"><input type="hidden" name="set" value="{{.Set}}"><input type="hidden" name="target" value="fresh"><button>{{.Text.confirm}}</button></form><small>{{.Text.expires}}</small><form action="/select" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="lang" value="{{.Lang}}"><button>{{.Text.select}}</button></form>
{{else}}
<h2>{{index .Text .Phase}}</h2><p>{{.Text.attempt}}: <code>{{.Attempt}}</code></p><p>{{.Text.source}}: {{.Label}} · {{.Text.target}}: {{.Text.fresh}}</p>
{{if eq .Phase "complete"}}<p class="notice">{{.Text.completed}}</p><p>{{.Text.outstanding}}</p><p>{{.Text.cutover}}</p>
{{else if eq .Phase "stopped"}}<p class="notice">{{.Text.stopped_help}}</p>
{{else}}<p>{{.Text.running}}</p>{{end}}
{{end}}
<p><a href="/?lang={{.Lang}}">{{.Text.refresh}}</a></p>
<form action="/logout" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="lang" value="{{.Lang}}"><button>{{.Text.logout}}</button></form>
{{end}}</main></body></html>`))
