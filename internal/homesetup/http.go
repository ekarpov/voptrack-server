package homesetup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"fmt"
	bc "github.com/ekarpov/voptrack-server/internal/backupcollector"
	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"rsc.io/qr"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

type session struct {
	CSRF, Grant, Confirm, Binding         string
	Expires, ConfirmExpires               time.Time
	Authorized, Downloaded, KeyDownloaded bool
}

func same(a, b string) bool {
	return len(a) > 0 && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func (s *Service) grant() (g grant, e error) {
	e = bc.ReadPrivateJSON(filepath.Join(s.Config.State, "authorization.json"), &g)
	return
}
func (s *Service) authorized(v *session) bool {
	if v == nil || !v.Authorized || !s.now().Before(v.Expires) {
		return false
	}
	g, e := s.grant()
	return e == nil && g.Consumed && g.ID == v.Grant && s.now().Before(g.Expires)
}
func cookie(w http.ResponseWriter, id string, expiry time.Time) {
	http.SetCookie(w, &http.Cookie{Name: "__Host-home-setup", Value: id, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: expiry})
}
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	lang := language(r.URL.Query().Get("lang"))
	deny := func(status int) {
		w.WriteHeader(status)
		_ = page.Execute(w, view{Lang: lang, T: messages[lang], Error: messages[lang]["refused"]})
	}
	origin, _ := url.Parse(s.Config.Origin)
	if r.TLS == nil || r.TLS.Version < tls.VersionTLS12 || r.Host != origin.Host || (r.Method != "GET" && r.Method != "POST") {
		deny(403)
		return
	}
	if len(r.URL.Query()) > 1 || (len(r.URL.Query()) == 1 && len(r.URL.Query()["lang"]) != 1) || (r.Method == "POST" && r.URL.RawQuery != "") {
		deny(400)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := control(s.Config.State, func() error {
		for id, v := range s.sessions {
			if !s.now().Before(v.Expires) {
				delete(s.sessions, id)
			}
		}
		var v *session
		id := ""
		if c, e := r.Cookie("__Host-home-setup"); e == nil {
			id = c.Value
			v = s.sessions[id]
		}
		if r.Method == "GET" {
			if r.URL.Path != "/" {
				deny(404)
				return nil
			}
			if v == nil {
				if len(s.sessions) >= 64 {
					deny(429)
					return nil
				}
				id = recoverymaterial.ID()
				v = &session{CSRF: recoverymaterial.ID(), Expires: s.now().Add(5 * time.Minute)}
				s.sessions[id] = v
				cookie(w, id, v.Expires)
			}
			s.render(w, lang, v, "", nil)
			return nil
		}
		if r.Header.Get("Origin") != s.Config.Origin || r.Header.Get("Sec-Fetch-Site") == "cross-site" || v == nil {
			deny(403)
			return nil
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.ParseForm() != nil {
			deny(400)
			return nil
		}
		lang = language(r.PostForm.Get("lang"))
		if !s.now().Before(v.Expires) || !same(v.CSRF, r.PostForm.Get("csrf")) {
			deny(403)
			return nil
		}
		for k, values := range r.PostForm {
			if len(values) != 1 || !strings.Contains("|csrf|lang|code|name|timezone|mode|encrypted|binding|confirm|password|repeat|custody|kitsha|", "|"+k+"|") {
				deny(400)
				return nil
			}
		}
		if r.URL.Path == "/auth" {
			g, e := s.grant()
			if e != nil || g.Consumed || g.Attempts >= 5 || !s.now().Before(g.Expires) {
				deny(403)
				return nil
			}
			g.Attempts++
			valid := same(digest([]byte(r.PostForm.Get("code"))), g.Digest)
			if valid {
				g.Consumed = true
				g.Code = ""
			}
			if writeJSON(filepath.Join(s.Config.State, "authorization.json"), g) != nil || !valid {
				deny(403)
				return nil
			}
			delete(s.sessions, id)
			id = recoverymaterial.ID()
			expiry := s.now().Add(15 * time.Minute)
			if g.Expires.Before(expiry) {
				expiry = g.Expires
			}
			v = &session{CSRF: recoverymaterial.ID(), Grant: g.ID, Expires: expiry, Authorized: true}
			s.sessions[id] = v
			cookie(w, id, expiry)
			http.Redirect(w, r, "/?lang="+lang, 303)
			return nil
		}
		if !s.authorized(v) {
			deny(403)
			return nil
		}
		if s.bound() != nil {
			s.journal.Phase = "stopped"
			_ = s.save()
			deny(409)
			return nil
		}
		switch r.URL.Path {
		case "/settings":
			if s.journal.Phase != "draft" && s.journal.Phase != "review" {
				deny(409)
				return nil
			}
			c := Choices{Version: 1, Name: r.PostForm.Get("name"), TimeZone: r.PostForm.Get("timezone"), Mode: r.PostForm.Get("mode"), Encrypted: r.PostForm.Get("encrypted") == "yes"}
			if !c.Valid() {
				w.WriteHeader(400)
				s.render(w, lang, v, messages[lang]["invalid"], &c)
				return nil
			}
			s.journal.Choices = c
			s.journal.Phase = "review"
			raw, _ := json.Marshal(struct {
				Choices                 Choices
				Attempt, Origin, Public string
				Roots                   map[string]identity
			}{c, s.journal.Attempt, s.Config.Origin, s.Config.PublicURL, s.journal.Roots})
			s.journal.Binding = digest(raw)
			v.Confirm = ""
			if s.save() != nil {
				return ErrRefused
			}
		case "/back":
			if s.journal.Phase != "review" {
				deny(409)
				return nil
			}
			s.journal.Phase = "draft"
			s.journal.Binding = ""
			v.Confirm = ""
			if s.save() != nil {
				return ErrRefused
			}
		case "/apply":
			if s.journal.Phase != "review" || !same(v.Confirm, r.PostForm.Get("confirm")) || !s.now().Before(v.ConfirmExpires) || v.Binding != s.journal.Binding || r.PostForm.Get("binding") != s.journal.Binding {
				deny(409)
				return nil
			}
			if s.Config.freshPending(s.journal.Attempt) != nil {
				s.journal.Phase = "stopped"
				_ = s.save()
				deny(409)
				return nil
			}
			password := r.PostForm.Get("password")
			if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 12 || len(password) > 1024 || strings.TrimSpace(password) != password || strings.ContainsAny(password, "\r\n\x00") || !same(password, r.PostForm.Get("repeat")) {
				w.WriteHeader(400)
				s.render(w, lang, v, messages[lang]["invalid"], nil)
				return nil
			}
			// Consume execution authority durably before any target mutation.
			v.Confirm = ""
			s.journal.Phase = "accepted"
			if s.save() != nil {
				return ErrRefused
			}
			s.workers.Add(1)
			go s.execute(password)
		case "/kit", "/key":
			if s.journal.Phase != "ready" {
				deny(409)
				return nil
			}
			raw, e := s.material()
			if e != nil {
				return e
			}
			if r.URL.Path == "/key" {
				// Derive the separate copy from the same immutable export.
				raw, e = materialKey(raw)
				if e != nil {
					return e
				}
			}
			if !s.authorized(v) {
				deny(403)
				return nil
			}
			name := "initial-home-recovery-material.tar"
			if r.URL.Path == "/key" {
				name = "home-backup.key"
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", "attachment; filename="+name)
			n, e := w.Write(raw)
			if e == nil && n == len(raw) {
				if r.URL.Path == "/kit" {
					v.Downloaded = true
				} else {
					v.KeyDownloaded = true
				}
			}
			return nil
		case "/finish":
			if s.journal.Phase != "ready" || !v.Downloaded || !v.KeyDownloaded || r.PostForm.Get("custody") != "yes" || s.journal.KitHash == "" || r.PostForm.Get("kitsha") != s.journal.KitHash {
				deny(409)
				return nil
			}
			s.journal.Phase = "complete"
			if s.save() != nil {
				return ErrRefused
			}
			if s.release() != nil {
				return ErrRefused
			}
			s.render(w, lang, v, "", nil)
			s.doneOnce.Do(func() { close(s.done) })
			return nil
		case "/logout":
			delete(s.sessions, id)
			cookie(w, "", s.now().Add(-time.Hour))
			http.Redirect(w, r, "/?lang="+lang, 303)
			return nil
		default:
			deny(404)
			return nil
		}
		http.Redirect(w, r, "/?lang="+lang, 303)
		return nil
	})
	if e != nil {
		deny(503)
	}
}

func (s *Service) material() ([]byte, error) {
	path := filepath.Join(s.Config.State, "initial-kit.tar")
	if s.journal.KitHash != "" {
		raw, e := readRegular(path, 8<<20)
		if e != nil || digest(raw) != s.journal.KitHash {
			return nil, ErrRefused
		}
		return raw, nil
	}
	if s.Config.Root != "/" {
		return nil, ErrRefused
	}
	var out limitedBuffer
	out.max = 8 << 20
	ctx, cancel := context.WithTimeout(s.ctx, time.Minute)
	defer cancel()
	if command(ctx, nil, &out, "/usr/local/bin/voptrack-deploy", "recovery-kit") != nil {
		return nil, ErrRefused
	}
	raw := out.Bytes()
	if _, e := materialKey(raw); e != nil {
		return nil, ErrRefused
	}
	if recoverymaterial.Write(path, raw) != nil {
		return nil, ErrRefused
	}
	s.journal.KitHash = digest(raw)
	if s.save() != nil {
		return nil, ErrRefused
	}
	return raw, nil
}

func materialKey(kit []byte) ([]byte, error) {
	tr := tar.NewReader(bytes.NewReader(kit))
	var key []byte
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, ErrRefused
		}
		if h.Name != "var/lib/voptrack-config/backup.key" {
			continue
		}
		if key != nil || h.Typeflag != tar.TypeReg || h.Size != 32 {
			return nil, ErrRefused
		}
		key, e = io.ReadAll(io.LimitReader(tr, 33))
		if e != nil || len(key) != 32 {
			return nil, ErrRefused
		}
	}
	if len(key) != 32 {
		return nil, ErrRefused
	}
	return key, nil
}

type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.max-b.Len() {
		return 0, ErrRefused
	}
	return b.Buffer.Write(p)
}
func readRegular(path string, max int64) ([]byte, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > max {
		return nil, ErrRefused
	}
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil || int64(len(b)) > max {
		return nil, ErrRefused
	}
	return b, nil
}

type view struct {
	QRPath                                                                              string
	QRSize                                                                              int
	Lang, Error, CSRF, Phase, Confirm, Binding, PublicURL, KitHash, AppLink, ConsoleURL string
	T                                                                                   map[string]string
	Authorized, Downloaded                                                              bool
	Choices                                                                             Choices
}

func (s *Service) render(w http.ResponseWriter, lang string, v *session, message string, choices *Choices) {
	data := view{Lang: lang, T: messages[lang], Error: message, CSRF: v.CSRF, Authorized: s.authorized(v)}
	if data.Authorized {
		data.Phase = s.journal.Phase
		data.Choices = s.journal.Choices
		if choices != nil {
			data.Choices = *choices
		}
		if data.Choices.TimeZone == "" {
			data.Choices.TimeZone = "UTC"
		}
		if data.Choices.Mode == "" {
			data.Choices.Mode = "public"
		}
		data.PublicURL = s.Config.PublicURL
		data.KitHash = s.journal.KitHash
		data.Downloaded = v.Downloaded && v.KeyDownloaded
		if data.Phase == "review" {
			if v.Confirm == "" || v.Binding != s.journal.Binding || !s.now().Before(v.ConfirmExpires) {
				v.Confirm = recoverymaterial.ID()
				v.Binding = s.journal.Binding
				v.ConfirmExpires = s.now().Add(5 * time.Minute)
			}
			data.Confirm = v.Confirm
			data.Binding = v.Binding
		}
		u, _ := url.Parse(s.Config.PublicURL)
		data.ConsoleURL = s.Config.PublicURL
		if data.Choices.Mode == "private" {
			u.Host = net.JoinHostPort(u.Hostname(), env("VOPTRACK_CONSOLE_PORT", "9443"))
			data.ConsoleURL = u.String()
		}
		q := url.Values{"server": {s.Config.PublicURL}, "name": {data.Choices.Name}, "product": {"home"}}
		if raw, e := readRegular(at(s.Config.Root, "var/lib/voptrack-public/tls-pin"), 512); e == nil && strings.TrimSpace(string(raw)) != "" {
			q.Set("pin", strings.TrimSpace(string(raw)))
		}
		data.AppLink = "voptrack://connect?" + strings.ReplaceAll(q.Encode(), "+", "%20")
		if data.Phase == "complete" {
			if code, err := qr.Encode(data.AppLink, qr.M); err == nil {
				var path strings.Builder
				for y := 0; y < code.Size; y++ {
					for x := 0; x < code.Size; x++ {
						if code.Black(x, y) {
							fmt.Fprintf(&path, "M%d %dh1v1h-1z", x+4, y+4)
						}
					}
				}
				data.QRPath = path.String()
				data.QRSize = code.Size + 8
			}
		}

	}
	_ = page.Execute(w, data)
}
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

var page = template.Must(template.New("setup").Funcs(template.FuncMap{"languages": func() []string { return languages }, "appURL": func(s string) template.URL { return template.URL(s) }}).Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.T.title}}</title><style>
:root{color-scheme:light dark;--bg:#f3f5f4;--paper:#fff;--ink:#172c2b;--muted:#445956;--line:#aabbb5;--accent:#16675b}@media(prefers-color-scheme:dark){:root{--bg:#121d1c;--paper:#1b2a28;--ink:#f1f6f3;--muted:#bfd0c8;--line:#60766f;--accent:#9ae2c5}}
.qr{display:block;background:white;color:black;width:18rem;max-width:100%;height:auto}*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font:1rem/1.6 system-ui,sans-serif}main{max-width:50rem;margin:2rem auto;padding:2rem;overflow-wrap:anywhere;background:var(--paper);border:1px solid var(--line);border-radius:1rem}nav{display:flex;flex-wrap:wrap;gap:1rem}a{color:var(--accent)}h1{font-size:clamp(1.6rem,5vw,2.3rem);line-height:1.2}h2{font-size:1.3rem}label{display:block;font-weight:600;margin-top:1rem}input,select,button{font:inherit;max-width:100%;padding:.7rem;border-radius:.4rem;border:1px solid var(--line);background:var(--paper);color:var(--ink)}input:not([type=checkbox]),select{width:100%}button{margin-top:1rem;cursor:pointer;font-weight:650;color:var(--accent)}:focus-visible{outline:3px solid var(--accent);outline-offset:4px}.notice{border:1px solid var(--line);padding:1rem}.muted{color:var(--muted)}@media(max-width:40rem){main{margin:.5rem;padding:1rem}}
</style></head><body><main><nav aria-label="{{.T.language}}">{{range languages}}<a href="/?lang={{.}}" lang="{{.}}" hreflang="{{.}}">{{.}}</a>{{end}}</nav><h1>{{.T.title}}</h1>
{{if .Error}}<p role="alert">{{.Error}}</p>{{end}}
{{if not .Authorized}}{{if .CSRF}}<p>{{.T.access}}</p><form method="post" action="/auth"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="lang" value="{{.Lang}}"><label for="code">{{.T.code}}</label><input id="code" name="code" type="password" autocomplete="off" required maxlength="48"><button>{{.T.continue}}</button></form>{{end}}
{{else}}
<p>{{.T.address}}: {{.PublicURL}}</p>
{{if eq .Phase "draft"}}<h2>{{.T.choices}}</h2><p>{{.T.fixed}}</p><form method="post" action="/settings"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="lang" value="{{.Lang}}">
<label for="name">{{.T.name}}</label><input id="name" name="name" value="{{.Choices.Name}}" required maxlength="80" autocomplete="organization">
<label for="timezone">{{.T.timezone}}</label><input id="timezone" name="timezone" value="{{.Choices.TimeZone}}" required maxlength="80">
<label for="mode">{{.T.mode}}</label><select id="mode" name="mode"><option value="public" {{if eq .Choices.Mode "public"}}selected{{end}}>{{.T.public}}</option><option value="private" {{if eq .Choices.Mode "private"}}selected{{end}}>{{.T.private}}</option><option value="off" {{if eq .Choices.Mode "off"}}selected{{end}}>{{.T.off}}</option></select>
<label><input type="checkbox" name="encrypted" value="yes" required {{if .Choices.Encrypted}}checked{{end}}> {{.T.encrypted}}</label><p class="muted">{{.T.attestation}}</p><button>{{.T.review}}</button></form>
{{else if eq .Phase "review"}}<h2>{{.T.review}}</h2><dl><dt>{{.T.name}}</dt><dd>{{.Choices.Name}}</dd><dt>{{.T.timezone}}</dt><dd>{{.Choices.TimeZone}}</dd><dt>{{.T.mode}}</dt><dd>{{index .T .Choices.Mode}}</dd></dl><p>{{.T.fixed}}</p><p class="notice">{{.T.mutation}}</p>
<form method="post" action="/apply"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="lang" value="{{.Lang}}"><input type="hidden" name="binding" value="{{.Binding}}"><input type="hidden" name="confirm" value="{{.Confirm}}">
<label for="password">{{.T.password}}</label><input id="password" name="password" type="password" required minlength="12" maxlength="1024" autocomplete="new-password"><label for="repeat">{{.T.repeat}}</label><input id="repeat" name="repeat" type="password" required minlength="12" maxlength="1024" autocomplete="new-password"><button>{{.T.create}}</button></form>
<form method="post" action="/back"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="lang" value="{{.Lang}}"><button>{{.T.back}}</button></form>
{{else if eq .Phase "ready"}}<h2>{{.T.material}}</h2><p class="notice">{{.T.material_scope}}</p>
<form method="post" action="/kit"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="lang" value="{{.Lang}}"><button>{{.T.kit}}</button></form>
<form method="post" action="/key"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="lang" value="{{.Lang}}"><button>{{.T.key}}</button></form>
{{if .KitHash}}<p>SHA-256: {{.KitHash}}</p>{{end}}
{{if .Downloaded}}<form method="post" action="/finish"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="lang" value="{{.Lang}}"><input type="hidden" name="kitsha" value="{{.KitHash}}"><label><input type="checkbox" name="custody" value="yes" required> {{.T.custody}}</label><p class="muted">{{.T.attestation}}</p><button>{{.T.finish}}</button></form>{{end}}
{{else if eq .Phase "complete"}}<h2>{{.T.complete}}</h2><p>{{.T.closing}}</p><p><a href="{{appURL .AppLink}}">{{.T.connect}}</a></p>{{if .QRPath}}<svg class="qr" role="img" aria-label="{{.T.connect}}" viewBox="0 0 {{.QRSize}} {{.QRSize}}"><path d="{{.QRPath}}" fill="currentColor"/></svg>{{end}}{{if ne .Choices.Mode "off"}}<p><a href="{{.ConsoleURL}}/members">{{.T.members}}</a></p><p><a href="{{.ConsoleURL}}/backups">{{.T.backups}}</a></p>{{end}}
{{else if eq .Phase "stopped"}}<h2>{{.T.stopped}}</h2><p>{{.T.preserve}}</p>
{{else}}<h2>{{.T.working}}</h2><p>{{.T.preserve}}</p>{{end}}
<p><a href="/?lang={{.Lang}}">{{.T.refresh}}</a></p>
<form method="post" action="/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="lang" value="{{.Lang}}"><button>{{.T.logout}}</button></form>
{{end}}</main></body></html>`))
