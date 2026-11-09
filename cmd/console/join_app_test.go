package main

import (
	"encoding/json"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func joinLinkApp(t *testing.T) (*app, *session) {
	t.Helper()
	a, sess, remote := policyHTTPApp(t)
	t.Cleanup(remote.Close)
	a.cfg.PublicURL = "https://synthetic.example"
	a.cfg.Variant = "research"
	tpl, err := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix}).ParseFS(assets, "templates/layout.html", "templates/join.html")
	must(t, err)
	a.pages["join"] = tpl
	st, ok := a.studies.get("synthetic")
	if !ok {
		t.Fatal("missing synthetic study")
	}
	st.JoinMode = "invite"
	st.Title = `Study "quoted" & <text>`
	raw, err := json.Marshal(st)
	must(t, err)
	must(t, a.studies.docs.put("studies/synthetic.json", raw))
	return a, sess
}

func TestStudyJoinAppLinkPreservesInvitation(t *testing.T) {
	for _, local := range []bool{false, true} {
		name := "public"
		if local {
			name = "local"
		}
		t.Run(name, func(t *testing.T) {
			a, sess := joinLinkApp(t)
			pin := ""
			if local {
				pin = "sha256/Ab+c/d=="
				a.cfg.TLSPinFile = filepath.Join(t.TempDir(), "pin")
				must(t, os.WriteFile(a.cfg.TLSPinFile, []byte(pin), 0600))
			}
			code, err := a.studies.newCode("synthetic")
			must(t, err)
			for attempt := 0; attempt < 2; attempt++ {
				w := savedRequest(a, sess, "GET", "/study/synthetic/join?code="+url.QueryEscape(code), nil)
				if w.Code != http.StatusOK {
					t.Fatal("valid invitation page refused")
				}
				match := regexp.MustCompile(`href="([^"]+)" data-app-link`).FindStringSubmatch(w.Body.String())
				if len(match) != 2 {
					t.Fatal("invitation page lacks app join link")
				}
				link, err := url.Parse(html.UnescapeString(match[1]))
				must(t, err)
				q := link.Query()
				if link.Scheme != "voptrack" || link.Host != "connect" || q.Get("server") != a.cfg.PublicURL || q.Get("product") != "study" || q.Get("study") != "synthetic" || q.Get("code") != code || q.Get("pin") != pin {
					t.Fatal("app link lost its validated study, invitation or trust context")
				}
				st, _ := a.studies.get("synthetic")
				if q.Get("name") != st.Title || strings.Contains(w.Body.String(), "#ZgotmplZ") {
					t.Fatal("app link was filtered or incorrectly escaped")
				}
				if !a.studies.hasCode("synthetic", code) || a.studies.codeUsed("synthetic", code) || len(a.studies.consents("synthetic")) != 0 {
					t.Fatal("reading the app invitation consumed it or recorded consent")
				}
			}
			if !a.studies.spendCode("synthetic", code) {
				t.Fatal("synthetic invitation could not be spent")
			}
			for _, value := range []string{code, "unknown", ""} {
				w := savedRequest(a, sess, "GET", "/study/synthetic/join?code="+url.QueryEscape(value), nil)
				if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "data-app-link") {
					t.Fatal("invalid or spent invitation exposed an app link")
				}
			}
		})
	}
}

func TestOpenStudyJoinAppLinkAndUnavailableStudy(t *testing.T) {
	a, sess := joinLinkApp(t)
	st, _ := a.studies.get("synthetic")
	st.JoinMode = "open"
	raw, err := json.Marshal(st)
	must(t, err)
	must(t, a.studies.docs.put("studies/synthetic.json", raw))
	w := savedRequest(a, sess, "GET", "/study/synthetic/join", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "data-app-link") {
		t.Fatal("open study lacks app join")
	}
	for _, status := range []string{"closed", "draft"} {
		st.Status = status
		raw, err = json.Marshal(st)
		must(t, err)
		must(t, a.studies.docs.put("studies/synthetic.json", raw))
		w = savedRequest(a, sess, "GET", "/study/synthetic/join", nil)
		if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "data-app-link") {
			t.Fatal("unavailable study exposed app join")
		}
	}
}
