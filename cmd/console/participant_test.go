package main

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func participantTestApp(t *testing.T) *app {
	t.Helper()
	a := &app{cfg: config{Variant: "research"}, studies: &studyStore{docs: fileDocs{dir: t.TempDir()}}, sessions: &sessions{byID: map[string]*session{}, idle: time.Hour, anonIdle: time.Minute, max: 10}, pages: map[string]*template.Template{}}
	for _, page := range []string{"message", "participant_consent", "participant_studies", "study"} {
		tpl, err := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix}).ParseFS(assets, "templates/layout.html", "templates/"+page+".html")
		must(t, err)
		a.pages[page] = tpl
	}
	st := Study{Slug: "synthetic", Title: "Synthetic study", Status: "open", IssuerID: "study-synthetic", Consent: []ConsentVersion{{Version: 1, Text: "Synthetic consent", Published: time.Now()}}}
	raw, err := json.Marshal(st)
	must(t, err)
	must(t, a.studies.docs.put("studies/synthetic.json", raw))
	return a
}

func TestParticipantExpiredSessionCannotAct(t *testing.T) {
	a := participantTestApp(t)
	sess := &session{ID: "synthetic-cookie", CSRF: "synthetic-csrf", LastSeen: time.Now().Add(-2 * time.Hour), Connections: []*connection{{}}, Pending: map[string]pending{}}
	a.sessions.byID[sess.ID] = sess
	for _, method := range []string{"GET", "POST"} {
		req := httptest.NewRequest(method, "/study/synthetic/consent", strings.NewReader("csrf=synthetic-csrf&confirm=yes&version=1"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: a.sessions.cookieName(), Value: sess.ID})
		req.SetPathValue("slug", "synthetic")
		req.SetPathValue("action", "renew")
		out := httptest.NewRecorder()
		if method == "GET" {
			a.participantConsentPage(out, req)
			if out.Code != 303 || out.Header().Get("Location") != "/login?study=synthetic" {
				t.Fatal("expiry lost return path")
			}
		} else {
			a.participantConsentAction(out, req)
			if out.Code != 403 {
				t.Fatal("expired session could act")
			}
		}
	}
	if len(a.studies.consents("synthetic")) != 0 {
		t.Fatal("expired session wrote agreement")
	}
}

func TestParticipantOAuthCancelAndExpiredPending(t *testing.T) {
	a := participantTestApp(t)
	sess := &session{ID: "synthetic-cookie", CSRF: "synthetic-csrf", LastSeen: time.Now(), Pending: map[string]pending{"cancel": {Expires: time.Now().Add(time.Minute), ReturnStudy: "synthetic"}, "old": {Expires: time.Now().Add(-time.Minute), ReturnStudy: "synthetic"}}}
	a.sessions.byID[sess.ID] = sess
	for _, state := range []string{"cancel", "old"} {
		req := httptest.NewRequest("GET", "/auth/callback?state="+state+"&error=access_denied", nil)
		req.AddCookie(&http.Cookie{Name: a.sessions.cookieName(), Value: sess.ID})
		out := httptest.NewRecorder()
		a.callback(out, req)
		if state == "cancel" {
			if out.Code != 303 {
				t.Fatal("OAuth cancellation lost return")
			}
			destination := httptest.NewRequest("GET", out.Header().Get("Location"), nil)
			destination.AddCookie(&http.Cookie{Name: a.sessions.cookieName(), Value: sess.ID})
			landed := httptest.NewRecorder()
			a.routes().ServeHTTP(landed, destination)
			if landed.Code != http.StatusOK || landed.Header().Get("Location") != "" || !strings.Contains(landed.Body.String(), `href="/study/synthetic/consent"`) {
				t.Fatal("cancellation destination restarted OAuth or lost explicit retry")
			}
			if len(sess.Pending) != 1 {
				t.Fatal("cancellation destination began a new sign-in")
			}
		} else if out.Code != 400 {
			t.Fatal("expired OAuth state accepted")
		}
	}
	if len(sess.Pending) != 0 || len(a.studies.consents("synthetic")) != 0 {
		t.Fatal("OAuth return changed agreement or retained state")
	}
}

func TestParticipantTemplatesEscapeAndTranslate(t *testing.T) {
	a := participantTestApp(t)
	now := time.Now().UTC()
	d := participantData{Study: Study{Slug: "synthetic", Title: "Synthetic study", Consent: []ConsentVersion{{Version: 1, Text: "<script>synthetic</script>", Published: now}}}, Current: ConsentVersion{Version: 1, Text: "<script>synthetic</script>"}, Status: participantStatus{Consents: []participantAgreement{{Version: 1, At: now}}, Withdrawn: &now, AgreementNeeded: true}, Latest: participantAgreement{Version: 1, At: now}, Action: "rejoin", Write: true}
	for _, lang := range languages {
		req := httptest.NewRequest("GET", "/study/synthetic/consent/rejoin?lang="+url.QueryEscape(lang), nil)
		out := httptest.NewRecorder()
		sess := &session{CSRF: "synthetic-csrf", Connections: []*connection{{tokens: tokens{Access: "must-not-be-rendered", Refresh: "must-not-be-rendered"}}}}
		a.render(out, req, sess, "participant_consent", 200, d)
		html := out.Body.String()
		if strings.Contains(html, "<script>") || strings.Contains(html, "must-not-be-rendered") || !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(html, translator("en")("participant_rejoin_disclosure")) || !strings.Contains(html, "data-rejoin-confirmation") {
			t.Fatalf("unsafe or incomplete %s template", lang)
		}
	}
}

// A valid first study must not hide a later failed lookup. Only the API's
// explicit nonmembership response may be skipped.
func TestParticipantListingDoesNotHideLookupFailures(t *testing.T) {
	for _, failure := range []int{-1, 0, http.StatusUnauthorized, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(failure), func(t *testing.T) {
			a := participantTestApp(t)
			raw, err := json.Marshal(Study{Slug: "second", Title: "Second study", Status: "open", IssuerID: "study-second", Consent: []ConsentVersion{{Version: 1, Text: "Synthetic", Published: time.Now()}}})
			must(t, err)
			must(t, a.studies.docs.put("studies/second.json", raw))
			// Both studies have agreements so the second lookup happens even when
			// the first succeeded. Success with no history is separately checked below.
			if failure != -1 {
				for _, slug := range []string{"synthetic", "second"} {
					must(t, a.studies.recordConsent(slug, ConsentRecord{Account: "synthetic-person", Version: 1, At: time.Now()}))
				}
			}
			lookups := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/oauth/account":
					lookups++
					if failure > 0 && lookups == 3 {
						writeAPIError(w, failure, "unavailable")
						return
					}
					writeJSON(w, 200, map[string]string{"account": "synthetic-person"})
				case "/v1/datasets":
					writeJSON(w, 200, map[string]any{"datasets": []Dataset{{ID: "synthetic-store"}}})
				default:
					t.Errorf("unexpected upstream route: %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer upstream.Close()
			a.tessera = &Tessera{internal: upstream.URL, http: upstream.Client()}
			sess := &session{ID: "synthetic-cookie", Account: "synthetic-person", LastSeen: time.Now(), Connections: []*connection{{tokens: tokens{Access: "synthetic-only", Expires: time.Now().Add(time.Hour)}}}}
			a.sessions.byID[sess.ID] = sess
			req := httptest.NewRequest("GET", "/my-studies", nil)
			req.AddCookie(&http.Cookie{Name: a.sessions.cookieName(), Value: sess.ID})
			out := httptest.NewRecorder()
			a.participantStudies(out, req)
			if failure > 0 {
				// The unchanged API maps failure to authenticate to 401, even when
				// Tessera was unavailable. Either way it must not become nonmembership.
				if out.Code != 401 || strings.Contains(out.Body.String(), translator("en")("participant_none")) || strings.Contains(out.Body.String(), `href="/study/second/consent"`) || !strings.Contains(out.Body.String(), `href="/login"`) {
					t.Fatal("lookup failure was hidden or had no recovery link")
				}
			} else if failure == -1 {
				if out.Code != 200 || !strings.Contains(out.Body.String(), translator("en")("participant_none")) {
					t.Fatal("confirmed nonmembership did not yield empty state")
				}
			} else if out.Code != 200 || !strings.Contains(out.Body.String(), `href="/study/synthetic/consent"`) || !strings.Contains(out.Body.String(), `href="/study/second/consent"`) {
				t.Fatal("valid memberships were not listed")
			}
		})
	}
}
