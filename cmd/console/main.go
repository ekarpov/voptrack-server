// Command console is Voptrack Console: the web interface of Voptrack Home and,
// later, Voptrack Research. This first slice is analytics dashboards over
// aggregated data -- a person's own stores, and Home: the tracks household
// members chose to share.
//
// It is an OAuth client of the Tessera server beside it, like the Voptrack
// app. It reads with the signed-in person's own tokens, or with Home's for Home
// dashboards, and so can never show anybody more than Tessera lets them read.
package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ekarpov/voptrack-server/internal/websignin"
)

//go:embed templates/*.html static/*
var assets embed.FS

type config struct {
	WebsiteClientID    string // dedicated owned-website identity from generated config (legacy env fallback)
	Listen             string // plain HTTP inside the deployment; Caddy terminates TLS
	PublicURL          string
	TesseraURL         string
	TesseraCA          string
	StateDir           string
	HomeService        string // the Home service's account; empty means no Home here
	HomeSecret         string // path to its secret, which only the Console can read
	TimeZone           string
	IssuerID           string // the member-invitation issuer; empty means no invitations here
	IssuerSecret       string // path to its secret, which only the Console can read
	ServerName         string // what the app calls this server; display text only
	TLSPinFile         string // the local CA's pin, written by init; empty file for a public server
	Variant            string // standard, household or research: which product this is
	Analysis           string // the study-analysis service each study's issuer names as reader
	AnalysisSecret     string // path to its secret, which only the Console can read
	KitSocket          string
	RuntimeFile        string
	BackupSettingsFile string // sanitized generated configuration receipt, never keys
	BundleDir          string // collector publication volume only, read-only
	BackupDir          string // the backups volume, mounted read-only; empty means not shown
	StateService       string // the console-state service holding the Console's own store; empty keeps files
	StateSecret        string // path to its secret, which only the Console can read
	StateDataset       string // path to its store's identifier, written by init
	// ConsoleURL is where people open the Console. It is the public address
	// unless the Console is kept on the home network (ConsolePrivate): then
	// it has a port of its own, which the router does not forward, and only
	// Tessera -- what the app talks to -- is public.
	ConsoleURL     string
	ConsolePrivate bool
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	if os.Getenv("VOPTRACK_CONSOLE") == "off" {
		// Home without its web interface: the app does everything. The
		// container stays so the deployment is one compose file, but it holds
		// no credential, registers with nobody and answers only its health
		// check; Caddy does not route to it either.
		mux := http.NewServeMux()
		mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
		log.Printf("console: off (VOPTRACK_CONSOLE=off); the app does everything")
		log.Fatal((&http.Server{Addr: env("CONSOLE_LISTEN", ":8080"), Handler: mux, ReadHeaderTimeout: 10 * time.Second}).ListenAndServe())
	}
	cfg := config{
		WebsiteClientID:    os.Getenv("VOPTRACK_WEBSITE_CLIENT_ID"),
		Listen:             env("CONSOLE_LISTEN", ":8080"),
		PublicURL:          os.Getenv("VOPTRACK_PUBLIC_URL"),
		TesseraURL:         env("TESSERA_URL", "https://tesserad:8443"),
		TesseraCA:          os.Getenv("TESSERA_CA_FILE"),
		StateDir:           env("CONSOLE_STATE_DIR", "/var/lib/voptrack-console"),
		HomeService:        os.Getenv("HOME_SERVICE"),
		HomeSecret:         os.Getenv("HOME_SECRET_FILE"),
		TimeZone:           env("VOPTRACK_TIMEZONE", "UTC"),
		IssuerID:           os.Getenv("ISSUER_ID"),
		IssuerSecret:       os.Getenv("ISSUER_SECRET_FILE"),
		ServerName:         os.Getenv("VOPTRACK_NAME"),
		TLSPinFile:         os.Getenv("TLS_PIN_FILE"),
		Variant:            env("VOPTRACK_VARIANT", "standard"),
		Analysis:           os.Getenv("ANALYSIS_SERVICE"),
		AnalysisSecret:     os.Getenv("ANALYSIS_SECRET_FILE"),
		BackupDir:          os.Getenv("BACKUP_DIR"),
		BundleDir:          os.Getenv("BACKUP_BUNDLE_DIR"),
		KitSocket:          os.Getenv("RECOVERY_KIT_SOCKET"),
		RuntimeFile:        os.Getenv("RUNTIME_OBSERVATION_FILE"),
		BackupSettingsFile: os.Getenv("BACKUP_SETTINGS_FILE"),
		StateService:       os.Getenv("STATE_SERVICE"),
		StateSecret:        os.Getenv("STATE_SECRET_FILE"),
		StateDataset:       os.Getenv("STATE_DATASET_FILE"),
	}
	if cfg.PublicURL == "" && os.Getenv("VOPTRACK_DOMAIN") != "" {
		// The same rule init uses for the issuer, so the two cannot disagree.
		cfg.PublicURL = "https://" + os.Getenv("VOPTRACK_DOMAIN")
	}
	if !strings.HasPrefix(cfg.PublicURL, "https://") {
		log.Fatal("console: VOPTRACK_PUBLIC_URL must be the https address people use")
	}
	cfg.ConsolePrivate = os.Getenv("VOPTRACK_CONSOLE_PRIVATE") == "yes"
	cfg.ConsoleURL = strings.TrimRight(os.Getenv("VOPTRACK_CONSOLE_URL"), "/")
	if cfg.ConsoleURL == "" {
		cfg.ConsoleURL = cfg.PublicURL
		if cfg.ConsolePrivate {
			u, _ := url.Parse(cfg.PublicURL)
			cfg.ConsoleURL = "https://" + u.Hostname() + ":" + env("VOPTRACK_CONSOLE_PORT", "9443")
		}
	}
	if cfg.ConsolePrivate && cfg.Variant == "research" {
		// Participants read a study and join it through the Console, so a
		// study server's Console cannot be kept off the internet.
		log.Fatal("console: VOPTRACK_CONSOLE_PRIVATE is for Home; a study's pages must be public")
	}
	websiteID, err := websignin.ClientID(websignin.PublicFile, cfg.ConsoleURL, cfg.WebsiteClientID)
	if err != nil {
		log.Fatal("console: invalid generated website sign-in configuration")
	}
	cfg.WebsiteClientID = websiteID
	app, err := newApp(cfg)
	if err != nil {
		log.Fatalf("console: %v", err)
	}
	server := &http.Server{Addr: cfg.Listen, Handler: app.routes(),
		ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	log.Printf("console: serving %s", cfg.ConsoleURL)
	log.Fatal(server.ListenAndServe())
}

type app struct {
	cfg      config
	tessera  *Tessera
	sessions *sessions
	pages    map[string]*template.Template
	tz       *time.Location
	saved    *savedStore
	studies  *studyStore
	policies *policyStore

	homeReg       *homeRegistry
	homeCache     *serviceTokens
	analysisCache *serviceTokens
	docs          *stateDocs
	moderationMu  sync.Mutex    // serializes Console moderation, including previews
	exporting     chan struct{} // one study export at a time
}

func newApp(cfg config) (*app, error) {
	t, err := newTessera(cfg.TesseraURL, cfg.PublicURL, cfg.ConsoleURL, cfg.TesseraCA)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return nil, err
	}
	// The server may still be starting; registration is retried rather than
	// failing the Console on a race it did not cause.
	var regErr error
	for i := 0; i < 30; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		regErr = t.register(ctx, cfg.StateDir)
		cancel()
		if regErr == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if regErr != nil {
		return nil, regErr
	}
	t.websiteID = cfg.WebsiteClientID
	tz, err := time.LoadLocation(cfg.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("VOPTRACK_TIMEZONE %q: %w", cfg.TimeZone, err)
	}
	docs := &stateDocs{files: fileDocs{dir: cfg.StateDir}, t: t, service: cfg.StateService, secretFile: cfg.StateSecret,
		datasetFile: cfg.StateDataset}
	// The server is answering: registration above waited for it.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	docs.ensure(ctx)
	cancel()
	a := &app{cfg: cfg, tessera: t, sessions: newSessions(), tz: tz, pages: map[string]*template.Template{}, docs: docs, exporting: make(chan struct{}, 1),
		policies: &policyStore{docs: docs}, saved: &savedStore{docs: docs}, studies: &studyStore{docs: docs, dir: filepath.Join(cfg.StateDir, "studies")},
		homeReg:   &homeRegistry{docs: docs},
		homeCache: newServiceTokens(cfg.HomeService, cfg.HomeSecret), analysisCache: newServiceTokens(cfg.Analysis, cfg.AnalysisSecret)}
	if err := a.sessions.enableRemember(cfg.StateDir, cfg.ConsoleURL); err != nil {
		return nil, err
	}
	a.sessions.validate = a.validateRemembered
	for _, page := range []string{"landing", "overview", "workspace", "manage", "login", "track", "record", "panel", "message", "members", "dashboard", "leave", "confirm", "connect", "studies", "study_edit", "study", "sharing", "join", "participants", "backups", "study_tracks", "participant_studies", "participant_consent", "study_policy", "save_study_panel", "study_setup", "moderation", "research_monitor", "research_exports"} {
		tpl, err := template.New("layout.html").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix, "formErrorKey": formErrorKey, "catalogueLabel": catalogueLabel, "resultRows": resultRows, "designerProblem": designerProblem, "chartLabelVisible": chartLabelVisible, "chartLabelText": chartLabelText, "localizeName": localizeName, "localizePanelTitle": localizePanelTitle}).
			ParseFS(assets, "templates/layout.html", "templates/chart.html", "templates/design_fields.html", "templates/"+page+".html")
		if err != nil {
			return nil, err
		}
		a.pages[page] = tpl
	}
	return a, nil
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /{$}", selectionPage(a.overview))
	mux.HandleFunc("GET /home", selectionPage(a.overview))
	mux.HandleFunc("GET /mine", selectionPage(a.overview))
	mux.HandleFunc("GET /tracks", selectionPage(a.trackPage))
	mux.HandleFunc("GET /record", a.recordPage)
	mux.HandleFunc("GET /attachment", a.attachment)
	mux.HandleFunc("GET /manage", a.manageHome)
	mux.HandleFunc("GET /login", a.login)
	mux.HandleFunc("POST /login", a.login)
	mux.HandleFunc("POST /connect", a.connect)
	mux.HandleFunc("GET /auth/callback", a.callback)
	mux.HandleFunc("POST /logout", a.logout)
	mux.HandleFunc("GET /panel", a.panel)
	mux.HandleFunc("GET /panel.csv", a.panelCSV)
	mux.HandleFunc("GET /members", a.members)
	mux.HandleFunc("GET /backups", a.backupsPage)
	mux.HandleFunc("GET /backups/archive/{name}", a.downloadArchive)
	mux.HandleFunc("GET /backups/bundle/{id}", a.downloadBundle)
	mux.HandleFunc("GET /account/password", a.accountPage("password"))
	mux.HandleFunc("GET /account/delete", a.accountPage("delete"))
	mux.HandleFunc("GET "+accountBack, a.accountReturn)
	mux.HandleFunc("POST /backups/kit/prepare", a.kitPrepare)
	mux.HandleFunc("POST /backups/kit/request", a.kitRequest)
	mux.HandleFunc("POST /backups/kit/download", a.kitDownload)
	mux.HandleFunc("POST /backups/now", a.backupNow)
	mux.HandleFunc("POST /backups/live/preview", a.nativePreview)
	mux.HandleFunc("POST /backups/live/confirm", a.nativeConfirm)
	mux.HandleFunc("POST /backups/live/reconcile", a.nativeReconcile)
	mux.HandleFunc("POST /backups/settings/preview", a.backupSettingsPreview)
	mux.HandleFunc("POST /backups/settings/confirm", a.backupSettingsConfirm)
	mux.HandleFunc("POST /members/{account}/{action}", a.memberAction)
	mux.HandleFunc("GET /connect-app", a.connectApp)
	mux.HandleFunc("GET /sharing", a.sharing)
	mux.HandleFunc("POST /sharing", a.saveSharing)
	mux.HandleFunc("GET /studies/{slug}/setup/{stage}", a.setupPage)
	mux.HandleFunc("POST /studies/{slug}/setup/{stage}", a.setupAction)
	mux.HandleFunc("GET /studies", a.studiesPage)
	mux.HandleFunc("POST /studies", a.createStudy)
	mux.HandleFunc("GET /studies/{slug}/policy", a.studyPolicyPage)
	mux.HandleFunc("POST /studies/{slug}/policy", a.saveStudyPolicy)
	mux.HandleFunc("GET /studies/{slug}/edit", a.editStudyPage)
	mux.HandleFunc("POST /studies/{slug}/edit", a.editStudy)
	mux.HandleFunc("GET /studies/{slug}/tracks", a.tracksPage)
	mux.HandleFunc("GET /mine/{dataset}/export.json", a.exportMine)
	mux.HandleFunc("GET /api/v1/studies/{slug}", a.apiStudy)
	mux.HandleFunc("POST /api/v1/studies/{slug}/join", a.apiJoin)
	mux.HandleFunc("GET /api/v1/studies/{slug}/consent", a.apiConsent)
	mux.HandleFunc("POST /api/v1/studies/{slug}/consent", a.apiAgree)
	mux.HandleFunc("POST /api/v1/studies/{slug}/withdraw", a.apiWithdraw)
	mux.HandleFunc("GET /studies/{slug}/export.csv", a.exportStudy("csv"))
	mux.HandleFunc("GET /studies/{slug}/export.json", a.exportStudy("json"))
	mux.HandleFunc("POST /studies/{slug}/tracks", a.saveTracks)
	mux.HandleFunc("GET /study/{slug}", a.studyPage)
	mux.HandleFunc("GET /my-studies", a.participantStudies)
	mux.HandleFunc("GET /study/{slug}/consent", a.participantConsentPage)
	mux.HandleFunc("GET /study/{slug}/consent/{action}", a.participantConsentPage)
	mux.HandleFunc("POST /study/{slug}/consent/{action}", a.participantConsentAction)
	mux.HandleFunc("GET /study/{slug}/join", a.joinPage)
	mux.HandleFunc("POST /study/{slug}/join", a.join)
	mux.HandleFunc("GET /studies/{slug}/participants", selectionPage(a.participantsPage))
	mux.HandleFunc("POST /studies/{slug}/participants/{account}/{action}", a.participantAction)
	mux.HandleFunc("GET /studies/{slug}/dashboard", selectionPage(a.studyDashboard))
	mux.HandleFunc("GET /studies/{slug}/exports", a.researchExports)
	mux.HandleFunc("POST /studies/{slug}/codes", a.newJoinCode)
	mux.HandleFunc("GET /leave", a.leave)
	mux.HandleFunc("POST /leave/start", a.leaveStart)
	mux.HandleFunc("GET /leave/confirm", a.leaveConfirmPage)
	mux.HandleFunc("POST /leave/confirm", a.leaveConfirm)
	mux.HandleFunc("POST /leave/cancel", a.leaveCancel)
	// Saved analytical dashboards are retained in storage, but not exposed by
	// the first-release collection UI. Their engine remains source-tested.
	mux.HandleFunc("POST /members/invite", a.inviteMember)
	return secure(mux)
}

// secure sets the headers every page carries. No script may run, nothing may
// frame the Console, and forms may only go to this origin -- which covers the
// sign-in pages, because Caddy serves them from the same hostname.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

type view struct {
	StudyContext  *researchStudyContext
	HomeHousehold *householdView
	HomeSection   string
	HomeLinks     map[string]string
	T             func(string) string
	Lang          string
	Languages     []string
	LanguageURLs  map[string]string
	SignedIn      bool
	IsOwner       bool
	Research      bool
	ServerName    string
	HasPersonal   bool
	Managing      bool
	CSRF          string
	Path          string
	Data          any
	// PasswordSetBy: the signed-in person's password was set by somebody
	// else, so every page suggests choosing their own.
	PasswordSetBy bool
}

func (a *app) render(w http.ResponseWriter, r *http.Request, sess *session, page string, status int, data any) {
	chosen := r.URL.Query().Get("lang")
	if sess != nil {
		if chosen != "" {
			sess.Lang = language(r, chosen)
		}
		chosen = sess.Lang
	}
	lang := language(r, chosen)
	languageURLs := make(map[string]string, len(languages))
	query := url.Values{}
	sourceQuery := r.URL.Query()
	// Preserve page selection and invitation/error context, not arbitrary
	// parameters or unvalidated success notices such as backups' "taken".
	for _, key := range []string{"scope", "type", "field", "measure", "group", "period", "date", "from", "to", "tz", "code", "error"} {
		if values, ok := sourceQuery[key]; ok {
			query[key] = values
		}
	}
	if slot := sourceQuery.Get("slot"); slot == "local" || slot == "reserve" {
		query.Set("slot", slot)
	}
	languagePath := ""
	if r.Method != http.MethodGet && page == "message" {
		languagePath = "/"
		query = url.Values{}
	}
	if r.Method != http.MethodGet && page == "moderation" {
		languagePath = "/home/moderation"
		query = url.Values{}
	}
	if r.Method != http.MethodGet && page == "backups" {
		languagePath = "/backups"
		query = url.Values{}
	}
	for _, code := range languages {
		query.Set("lang", code)
		languageURLs[code] = languagePath + "?" + query.Encode()
	}
	homeSection := ""
	if r.URL.Path == "/" || r.URL.Path == "/home" || (r.URL.Query().Get("scope") == "home" && (r.URL.Path == "/tracks" || r.URL.Path == "/record")) {
		homeSection = homePrimarySection(householdSection(r.URL.Query()))
	}
	if a.cfg.Variant != "research" && r.URL.Query().Get("scope") == "home" && (r.URL.Path == "/tracks" || r.URL.Path == "/record") {
		homeSection = "directory"
	}
	v := view{HomeLinks: homeNavigation(r.URL.Query()), HomeSection: homeSection, T: translator(lang), Lang: lang, Languages: languages, LanguageURLs: languageURLs, Path: r.URL.Path, Data: data,
		Research: a.cfg.Variant == "research", ServerName: a.cfg.ServerName,
		Managing: r.URL.Path == "/manage" || r.URL.Path == "/members" || strings.HasPrefix(r.URL.Path, "/backups") || strings.HasPrefix(r.URL.Path, "/home/moderation")}
	if v.Research && page != "message" && sess != nil && sess.Owner != nil {
		v.StudyContext = a.researchStudyContext(r)
	}
	if home, ok := data.(overviewData); ok {
		v.HomeHousehold = home.Household
	}
	if sess != nil {
		v.SignedIn = sess.signedIn()
		v.HasPersonal = len(sess.Connections) > 0
		v.IsOwner = sess.Owner != nil
		v.CSRF = sess.CSRF
		v.PasswordSetBy = sess.PasswordSetBy
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := a.pages[page].Execute(w, v); err != nil {
		log.Printf("console: rendering %s: %v", page, err)
	}
}

func (a *app) fail(w http.ResponseWriter, r *http.Request, sess *session, status int, key string, err error) {
	if err != nil {
		// Logged without anything a record contains: the path and the
		// error's shape, never a value, a token or a person.
		// The route's pattern, never its path: a path can name a person.
		log.Printf("console: %s: %v", r.Pattern, err)
	}
	a.render(w, r, sess, "message", status, map[string]string{"Key": key})
}

// ---- signing in ------------------------------------------------------------

func (a *app) login(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, true)
	if sess == nil {
		a.fail(w, r, nil, http.StatusServiceUnavailable, "busy", nil)
		return
	}
	if r.Method == http.MethodGet && a.tessera.websiteID != "" {
		slug := r.URL.Query().Get("study")
		if st, ok := a.studies.get(slug); a.cfg.Variant == "research" && ok && st.Status != "draft" {
			a.startParticipantSignIn(w, r, sess, slug, "")
		} else {
			a.startWebsiteSignIn(w, r, sess, pending{})
		}
		return
	}
	if r.Method == http.MethodGet {
		a.render(w, r, sess, "login", 200, map[string]string{"Study": r.URL.Query().Get("study")})
		return
	}
	if !sess.checkCSRF(r) {
		a.fail(w, r, sess, 403, "expired", nil)
		return
	}
	slug := r.URL.Query().Get("study")
	if st, ok := a.studies.get(slug); a.cfg.Variant == "research" && ok && st.Status != "draft" {
		a.startParticipantSignIn(w, r, sess, slug, "")
		return
	}
	if a.tessera.websiteID != "" {
		a.startWebsiteSignIn(w, r, sess, pending{})
		return
	}
	a.startSignIn(w, r, sess, "")
}

// connect adds another store. Tessera approves one store per sign-in, so a
// person with a store of their own and a share of Family signs in once for
// each. It is a POST with the session's token so another site cannot start it.
func (a *app) connect(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess == nil || !sess.checkCSRF(r) {
		a.fail(w, r, sess, http.StatusForbidden, "expired", nil)
		return
	}
	a.startSignIn(w, r, sess, "")
}

func (a *app) startSignIn(w http.ResponseWriter, r *http.Request, sess *session, erase string) {
	state, verifier := random(24), random(48)
	a.sessions.addPending(sess, state, pending{Verifier: verifier, Expires: time.Now().Add(10 * time.Minute), Erase: erase, Remember: rememberChoice(r, sess)})
	http.Redirect(w, r, a.tessera.authorizeURL(state, verifier), http.StatusSeeOther)
}

func (a *app) callback(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess == nil {
		a.fail(w, r, nil, http.StatusBadRequest, "expired", nil)
		return
	}
	sess.authMu.Lock()
	defer sess.authMu.Unlock()
	// A sign-out completed while this callback waited: do not resurrect it.
	a.sessions.mu.Lock()
	active := a.sessions.byID[sess.ID] == sess
	a.sessions.mu.Unlock()
	if !active {
		a.fail(w, r, nil, http.StatusBadRequest, "expired", nil)
		return
	}

	state := r.URL.Query().Get("state")
	a.sessions.mu.Lock()
	p, ok := sess.Pending[state]
	delete(sess.Pending, state)
	a.sessions.mu.Unlock()
	if !ok || time.Now().After(p.Expires) {
		a.fail(w, r, sess, http.StatusBadRequest, "expired", nil)
		return
	}
	if r.URL.Query().Get("error") != "" {
		// Cancellation must end at a public page: the protected status page
		// would immediately start OAuth again for a signed-out participant.
		target := "/"
		if p.ReturnStudy != "" {
			target = "/study/" + p.ReturnStudy
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	var tok tokens
	var err error
	if p.Website {
		tok, err = a.tessera.redeemWebsite(ctx, r.URL.Query().Get("code"), p.Verifier)
	} else {
		tok, err = a.tessera.redeem(ctx, r.URL.Query().Get("code"), p.Verifier)
	}
	var refused *apiError
	if !p.Website && errors.As(err, &refused) && refused.Code == "invalid_client" {
		// Tessera reclaimed our registration. Register again; the person
		// signs in once more, which is all this costs them.
		a.tessera.forgetRegistration(a.cfg.StateDir)
		if rerr := a.tessera.register(ctx, a.cfg.StateDir); rerr != nil {
			a.fail(w, r, sess, http.StatusBadGateway, "error", rerr)
			return
		}
		a.fail(w, r, sess, http.StatusOK, "expired", nil)
		return
	}
	if err != nil {
		a.fail(w, r, sess, http.StatusBadGateway, "error", err)
		return
	}
	l, err := a.tessera.listing(ctx, tok.Access)
	if err != nil {
		a.fail(w, r, sess, http.StatusBadGateway, "error", fmt.Errorf("listing after sign-in: %v", err))
		return
	}
	if p.Erase != "" {
		a.eraseApproved(w, r, sess, p.Erase, tok, l)
		return
	}
	// Who this is, stated by Tessera rather than inferred (alpha.14).
	who, err := a.tessera.account(ctx, tok.Access)
	if err != nil {
		a.fail(w, r, sess, http.StatusBadGateway, "error", fmt.Errorf("asking who signed in: %v", err))
		return
	}
	if p.ParticipantAccount != "" && p.ParticipantAccount != who.Account {
		_ = a.tessera.revokeTokens(ctx, tok)
		http.Redirect(w, r, participantReturn(p)+"?error=account", http.StatusSeeOther)
		return
	}
	if p.Website {
		a.websiteApproved(w, r, sess, p, tok, l, who)
		return
	}
	a.sessions.mu.Lock()
	var dropped []*connection
	if sess.Account != "" && sess.Account != who.Account {
		// Somebody else signed in in this browser. Nothing of the previous
		// person's carries over: their stores, their owner rights, the
		// invitations they issued, a removal they had begun.
		dropped = append(dropped, credentialConnections(sess)...)
		if sess.Owner != nil {
			dropped = append(dropped, sess.Owner)
		}
		sess.RememberUntil = time.Time{}
		sess.Connections, sess.Owner, sess.Invited, sess.Website = nil, nil, nil, nil
		sess.Moderation = nil
		sess.BackupSettings = nil
		sess.NativeSettings = nil
	}
	sess.Account = who.Account
	sess.PasswordSetBy = who.PasswordSetBy != nil
	a.sessions.mu.Unlock()
	if len(dropped) > 0 {
		a.discardErase(ctx, sess)
		for _, c := range dropped {
			_ = a.tessera.revokeTokens(ctx, c.tokens)
		}
	}
	// The owner -- a household's, or a study's researcher -- administers the
	// server and holds no store of their own.
	if len(l.Datasets) == 0 && who.ServerAdministrator {
		a.sessions.mu.Lock()
		sess.Owner = &connection{tokens: tok}
		a.sessions.mu.Unlock()
		a.docs.ensure(ctx) // in case the server was not answering at start
		if a.cfg.Variant == "research" {
			a.replaceLostSecrets(ctx, tok.Access)
		}
		if err := a.sessions.complete(w, sess, p.Remember); err != nil {
			a.fail(w, r, nil, 503, "error", nil)
			return
		}
		target := "/studies"
		if a.cfg.Variant != "research" {
			target = "/home"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	if len(l.Datasets) != 1 {
		a.fail(w, r, sess, http.StatusBadGateway, "error", fmt.Errorf("listing after sign-in: %d stores", len(l.Datasets)))
		return
	}
	conn := &connection{Dataset: l.Datasets[0], tokens: tok, Administer: who.Installation.Administer}
	a.sessions.mu.Lock()
	// Connecting a store already connected replaces its tokens rather than
	// counting it twice.
	kept := sess.Connections[:0]
	for _, c := range sess.Connections {
		if c.Dataset.ID != conn.Dataset.ID {
			kept = append(kept, c)
		}
	}
	sess.Connections = append(kept, conn)
	a.sessions.mu.Unlock()
	if err := a.sessions.complete(w, sess, p.Remember); err != nil {
		a.fail(w, r, nil, 503, "error", nil)
		return
	}
	http.Redirect(w, r, participantReturn(p), http.StatusSeeOther)
}

func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess != nil && sess.checkCSRF(r) {
		sess.authMu.Lock()
		defer sess.authMu.Unlock()
		if !a.sessions.drop(w, sess) {
			a.fail(w, r, sess, 503, "error", nil)
			return
		}
	}
	a.render(w, r, nil, "message", http.StatusOK, map[string]string{"Key": "signed_out"})
}

// ---- sources ---------------------------------------------------------------

func (a *app) mine(sess *session) []Source {
	a.sessions.mu.Lock()
	defer a.sessions.mu.Unlock()
	var out []Source
	for _, c := range sess.Connections {
		c := c
		out = append(out, Source{Dataset: c.Dataset.ID, Name: c.Dataset.DisplayName,
			Token: func(ctx context.Context) (string, error) { return c.token(ctx, a.tessera) }})
	}
	return out
}

func (a *app) scopeSources(r *http.Request, sess *session, scope string) []Source {
	if scope == "home" {
		// Home reads with its own credential, so the viewer's right to see it
		// is checked against Tessera on every request: a suspended member, or
		// a revoked sign-in, stops seeing Home at once, not when their Console
		// session ends.
		if !a.viewerCurrent(r, sess) {
			return nil
		}
		if a.docs != nil {
			a.docs.ensure(r.Context())
		} // Recover discovery after a transient startup outage.
		return a.homeSources(r.Context())
	}
	if slug, ok := studySlug(scope); ok {
		if !a.isOwner(r, sess) {
			return nil // a study's participants are its researcher's alone
		}
		return a.studySources(r.Context(), sess, slug)
	}
	return a.mine(sess)
}

// ---- dashboards ------------------------------------------------------------

type card struct {
	Comparison    string
	Type          TypeInfo
	PerStore      Result
	PerChart      Chart
	PerLink       string
	Trend         *Result
	Chart         Chart
	Link          string
	Measure       string
	Field         string
	Category      string
	CategoryName  string
	Headline      string
	HeadlineLabel string
	People        int
	Records       int64
	CompactChart  Chart
	ValueView     bool
	MetricLabel   string
	ActivityOnly  bool
	Incomplete    bool
}

type overviewData struct {
	Household        *householdView
	Controls         exploreControls
	AllURL           string
	StudyTitle       string
	SavedError       bool
	Saved            []Dashboard
	Scope            string
	Status           string
	K                int
	Connected        []Dataset
	Cards            []card
	Skipped          []string
	HomeReady        bool
	Categories       []homeCategory
	SelectedCategory string
	TotalTracks      int
	PeriodLabel      string
}

func (a *app) overview(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess == nil || !sess.signedIn() {
		if a.tessera.websiteID != "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		a.render(w, r, sess, "landing", http.StatusOK, nil)
		return
	}
	if a.cfg.Variant == "research" {
		target := "/my-studies"
		if a.isOwner(r, sess) {
			target = "/studies"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	scope := "mine"
	if a.cfg.Variant != "research" && r.URL.Path != "/mine" {
		scope = "home"
	} else if r.URL.Path == "/home" {
		scope = "home"
	} else if len(sess.Connections) == 0 {
		http.Redirect(w, r, "/members", http.StatusSeeOther)
		return
	}

	a.overviewFor(w, r, sess, scope)
}

func (a *app) overviewFor(w http.ResponseWriter, r *http.Request, sess *session, scope string) {
	if slug, ok := studySlug(scope); ok {
		a.researchOverview(w, r, sess, slug)
		return
	}
	sources := a.scopeSources(r, sess, scope)
	data := overviewData{Scope: scope, HomeReady: a.cfg.HomeService != "" && len(a.homeReg.load()) > 0}
	a.sessions.mu.Lock()
	for _, c := range sess.Connections {
		data.Connected = append(data.Connected, c.Dataset)
	}
	a.sessions.mu.Unlock()
	if key, ok := a.dashboardKey(sess, scope); a.cfg.Variant == "research" && ok {
		var err error
		data.Saved, err = a.saved.load(key)
		data.SavedError = err != nil
	}
	cat, skipped := catalog(r.Context(), a.tessera, sources)
	data.Skipped = skipped
	now := time.Now().In(a.tz)
	yearAgo := time.Date(now.Year()-1, now.Month()+1, 1, 0, 0, 0, 0, a.tz)
	window, validWindow := requestedWindow(r.URL.Query(), now)
	if scope == "home" && !validWindow {
		a.fail(w, r, sess, 422, "error", nil)
		return
	}
	if scope == "home" {
		data.Controls = controls(r.URL.Query(), sources, window, "/home")
	}
	if scope == "home" {
		household, err := a.buildHousehold(r, sources, cat, window, len(skipped) > 0)
		if err != nil {
			a.fail(w, r, sess, 502, "error", nil)
			return
		}
		data.Household = household
		a.render(w, r, sess, "overview", 200, data)
		return
	}
	if a.cfg.Variant != "research" && scope == "mine" {
		a.personalWorkspace(w, r, sess, sources, cat, skipped)
		return
	}
	for _, t := range cat.Types {
		if scope == "home" && homeInfrastructure(t.ID) {
			continue
		}
		if scope == "home" {
			cards, err := a.periodCards(r.Context(), sources, t, window, data.Controls)
			if err != nil {
				a.fail(w, r, sess, 502, "error", err)
				return
			}
			data.Cards = append(data.Cards, cards...)
			continue
		}
		c := card{Type: t}
		spec := Spec{Type: t.ID, Measure: MeasureCount, Group: "store", TZ: a.tz}
		res, err := compute(r.Context(), a.tessera, sources, spec)
		if err != nil {
			a.fail(w, r, sess, http.StatusBadGateway, "error", err)
			return
		}
		c.PerStore, c.PerChart, c.PerLink = res, buildChart(res, false), panelLink(scope, spec)
		if len(t.Numeric) > 0 && len(t.Dates) > 0 {
			f := t.Numeric[0]
			trend := Spec{Type: t.ID, Field: f.ID, Measure: defaultMeasure(f), Group: "period", Period: "month",
				TZ: a.tz, From: yearAgo, Date: t.Dates[0].ID}
			if scope == "home" && !homePlainNumber(t) {
				trend.Field = ""
				trend.Measure = MeasureCount
				c.ActivityOnly = true
			}
			tr, err := compute(r.Context(), a.tessera, sources, trend)
			if err != nil {
				a.fail(w, r, sess, http.StatusBadGateway, "error", err)
				return
			}
			c.Trend, c.Chart, c.Link = &tr, buildChart(tr, true), panelLink(scope, trend)
			c.Measure, c.Field = "m_"+string(trend.Measure), fieldName(f)
		}
		if scope == "home" {
			if c.Trend == nil && len(t.Dates) > 0 {
				spec := Spec{Type: t.ID, Measure: MeasureCount, Group: "period", Period: "month", TZ: a.tz, From: yearAgo, Date: t.Dates[0].ID}
				tr, err := compute(r.Context(), a.tessera, sources, spec)
				if err != nil {
					a.fail(w, r, sess, http.StatusBadGateway, "error", err)
					return
				}
				c.Trend, c.Link, c.Measure, c.ActivityOnly = &tr, panelLink(scope, spec), "m_count", true
			}
			prepareHomeCard(&c)
			c.Link = "/tracks?" + url.Values{"scope": {"home"}, "type": {t.ID}}.Encode()
		}
		data.Cards = append(data.Cards, c)
	}
	if scope == "home" {
		prepareHomeOverview(&data, r.URL.Query().Get("category"), now)
		data.PeriodLabel = data.Controls.Range
		base := filterQuery(data.Controls)
		base.Del("bucket")
		data.AllURL = "/home?" + base.Encode()
		for i := range data.Categories {
			v := qCopy(base)
			v.Set("category", data.Categories[i].ID)
			data.Categories[i].URL = "/home?" + v.Encode()
		}
	}
	a.render(w, r, sess, "overview", http.StatusOK, data)
}

func fieldName(f Field) string {
	if f.Name != "" {
		return f.Name
	}
	return f.ID
}

func panelLink(scope string, s Spec) string {
	q := url.Values{"scope": {scope}, "type": {s.Type}, "measure": {string(s.Measure)}, "group": {s.Group}}
	if s.Field != "" {
		q.Set("field", s.Field)
	}
	if s.Period != "" {
		q.Set("period", s.Period)
	}
	if s.Date != "" {
		q.Set("date", s.Date)
	}
	if !s.From.IsZero() {
		q.Set("from", s.From.Format("2006-01-02"))
	}
	if !s.To.IsZero() {
		to := s.To
		if _, study := studySlug(scope); study {
			to = to.In(s.TZ).AddDate(0, 0, -1)
		}
		q.Set("to", to.Format("2006-01-02"))
	}
	if s.TZ != nil {
		q.Set("tz", s.TZ.String())
	}
	return "/panel?" + q.Encode()
}

// parseSpec reads a panel from its query string, and refuses anything the
// type does not have rather than passing a name through to the server.
func (a *app) parseSpec(r *http.Request, cat Catalog) (Spec, TypeInfo, error) {
	return a.parseSpecValues(r.URL.Query(), cat)
}

func (a *app) parseSpecValues(q url.Values, cat Catalog) (Spec, TypeInfo, error) {
	t, ok := cat.find(q.Get("type"))
	if !ok {
		return Spec{}, t, errors.New("no such track")
	}
	s := Spec{Type: t.ID, Measure: MeasureCount, Group: "store", TZ: a.tz}
	if f := q.Get("field"); f != "" {
		found := false
		for _, n := range t.Numeric {
			found = found || n.ID == f
		}
		if !found {
			return s, t, errors.New("no such value")
		}
		s.Field = f
	}
	if m := q.Get("measure"); validMeasure(m) {
		s.Measure = Measure(m)
	}
	if s.Field == "" {
		s.Measure = MeasureCount
	}
	switch g := q.Get("group"); {
	case g == "store" || g == "none" || g == "period":
		s.Group = g
	case g == "spread" && s.Field != "":
		s.Group, s.Measure = g, MeasureCount // how many records fall at each value
	case g == "cohort" && strings.HasPrefix(q.Get("scope"), studyScopePrefix):
		s.Group = g // only a study's participants have consent versions
	case strings.HasPrefix(g, "field:"):
		id := strings.TrimPrefix(g, "field:")
		for _, f := range t.Groups {
			if f.ID == id {
				s.Group = g
			}
		}
	}
	switch p := q.Get("period"); p {
	case "day", "week", "month":
		s.Period = p
	default:
		s.Period = "month"
	}
	if len(t.Dates) > 0 {
		s.Date = t.Dates[0].ID
		for _, d := range t.Dates {
			if d.ID == q.Get("date") {
				s.Date = d.ID
			}
		}
	}
	if s.Group == "period" && s.Date == "" {
		s.Group = "store"
	}
	if tz := q.Get("tz"); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			s.TZ = loc
		}
	}
	if v := q.Get("from"); v != "" {
		if d, err := time.ParseInLocation("2006-01-02", v, s.TZ); err == nil {
			s.From = d
		}
	}
	if v := q.Get("to"); v != "" {
		if d, err := time.ParseInLocation("2006-01-02", v, s.TZ); err == nil {
			s.To = d.AddDate(0, 0, 1) // "to" a date includes that whole day
		}
	}
	return s, t, nil
}

type panelData struct {
	NumericAllowed bool
	SavedError     bool
	StudyTitle     string
	CanSave        bool
	Saved          []Dashboard
	Query          string
	Scope          string
	Type           TypeInfo
	Spec           Spec
	Result         Result
	Chart          Chart
	CompactChart   Chart
	Rows           []panelRow
	CSV            string
	Back           string
	FromText       string
	ToText         string
	TZ             string
	Measures       []string
}

type panelRow struct {
	URL    string
	Label  string
	Value  string
	Count  int64
	Status string
}

func (a *app) panelResult(w http.ResponseWriter, r *http.Request) (*session, string, Spec, TypeInfo, Result, bool) {
	sess := a.sessions.get(w, r, false)
	if sess == nil || !sess.signedIn() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return nil, "", Spec{}, TypeInfo{}, Result{}, false
	}
	scope := r.URL.Query().Get("scope")
	if len(r.URL.Query()["scope"]) > 1 {
		a.fail(w, r, sess, 400, "no_dashboard", nil)
		return nil, "", Spec{}, TypeInfo{}, Result{}, false
	}
	if slug, study := studySlug(scope); study {
		if _, ok := a.researcher(w, r); !ok {
			return nil, "", Spec{}, TypeInfo{}, Result{}, false
		}
		return a.researchPanelResult(w, r, sess, slug)
	} else if scope != "home" {
		if scope != "" && scope != "mine" {
			a.fail(w, r, sess, 400, "no_dashboard", nil)
			return nil, "", Spec{}, TypeInfo{}, Result{}, false
		}
		scope = "mine"
	}
	sources := a.scopeSources(r, sess, scope)
	cat, _ := catalog(r.Context(), a.tessera, sources)
	spec, t, err := a.parseSpec(r, cat)
	if err != nil {
		a.fail(w, r, sess, http.StatusNotFound, "no_tracks", nil)
		return nil, "", spec, t, Result{}, false
	}
	if a.cfg.Variant != "research" && spec.Field != "" && !homePlainNumber(t) {
		return sess, scope, spec, t, Result{Spec: spec, Unsupported: true}, true
	}
	res, err := compute(r.Context(), a.tessera, sources, spec)
	if err != nil {
		a.fail(w, r, sess, http.StatusBadGateway, "error", err)
		return nil, "", spec, t, Result{}, false
	}
	return sess, scope, spec, t, res, true
}

func (a *app) panel(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Variant == "research" {
		http.NotFound(w, r)
		return
	}
	a.analyticalPanel(w, r)
}

// Retained analytical renderer for engine regression tests, not a Research browser route.
func (a *app) analyticalPanel(w http.ResponseWriter, r *http.Request) {
	sess, scope, spec, t, res, ok := a.panelResult(w, r)
	if !ok {
		return
	}
	d := panelData{Scope: scope, Type: t, Spec: spec, Result: res, Chart: buildChart(res, spec.Group == "period"),
		CompactChart: buildCompactChart(res, spec.Group == "period"),
		CSV:          strings.Replace(panelLink(scope, spec), "/panel?", "/panel.csv?", 1), TZ: spec.TZ.String(),
		Measures: []string{"count", "sum", "average", "minimum", "maximum"}}
	d.Back = a.personalHome()
	if scope == "home" {
		d.Back = "/home"
	}
	if slug, ok := studySlug(scope); ok {
		d.Back = "/studies/" + slug + "/dashboard"
		st, _ := a.studies.get(slug)
		d.StudyTitle = st.Title
	}
	if key, ok := a.dashboardKey(sess, scope); a.cfg.Variant == "research" && ok && a.saved != nil && a.canEditDashboards(r, sess, scope) {
		d.CanSave = true
		var err error
		d.Saved, err = a.saved.load(key)
		d.SavedError = err != nil
		d.Query = strings.TrimPrefix(panelLink(scope, spec), "/panel?")
	}
	if !spec.From.IsZero() {
		d.FromText = spec.From.Format("2006-01-02")
	}
	if !spec.To.IsZero() {
		d.ToText = spec.To.AddDate(0, 0, -1).Format("2006-01-02")
	}
	d.NumericAllowed = a.cfg.Variant == "research" || homePlainNumber(t)
	if !d.NumericAllowed {
		d.Measures = []string{"count"}
	}
	d.Rows = resultRows(res)
	if a.cfg.Variant != "research" {
		d.Chart = buildChartSized(res, spec.Group == "period", 720, 180)
		d.CompactChart = buildChartSized(res, spec.Group == "period", 320, 180)
	}
	a.render(w, r, sess, "panel", http.StatusOK, d)
}

// panelCSV exports exactly what the panel shows, at full precision, with the
// question it answers in the first lines so a file on its own still says what
// it is.
func (a *app) panelCSV(w http.ResponseWriter, r *http.Request) {
	_, _, spec, _, res, ok := a.panelResult(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="voptrack-panel.csv"`)
	out := csv.NewWriter(w)
	if res.Research {
		writeResearchCSV(out, res)
		out.Flush()
		return
	}
	out.Write([]string{"# track", spec.Type})
	out.Write([]string{"# measure", string(spec.Measure), spec.Field})
	out.Write([]string{"# grouped by", spec.Group, spec.Period, spec.TZ.String()})
	if len(res.Skipped) > 0 {
		names := []string{"# not included, did not answer"}
		for _, n := range res.Skipped {
			names = append(names, cell(n))
		}
		out.Write(names)
	}
	out.Write([]string{"group", "value", "records", "values"})
	for _, p := range res.Points {
		out.Write([]string{cell(p.Label), exact(p.Tally.Value(spec.Measure, spec.Field)),
			fmt.Sprint(p.Tally.Rows), fmt.Sprint(p.Tally.Values)})
	}
	out.Flush()
}

// cell keeps a label from being read as a formula. Group labels and store
// names are whatever people typed, and a spreadsheet opening this file would
// run one beginning with "=" -- so those gain a leading apostrophe, which every
// spreadsheet treats as "this is text".
func cell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// ---- members ---------------------------------------------------------------

type membersData struct {
	Enabled bool
	Invited []Invitation
	Error   string
	Members []AccountInfo
}

// viewerCurrent asks Tessera whether the session's person may still sign in:
// the owner through isOwner, a member through any one of their connections,
// which must still name the account the session is for.
func (a *app) viewerCurrent(r *http.Request, sess *session) bool {
	if sess == nil {
		return false
	}
	if sess.Website != nil {
		token, err := sess.Website.token(r.Context(), a.tessera)
		if err != nil {
			return false
		}
		who, err := a.tessera.account(r.Context(), token)
		return err == nil && who.Account == sess.Account
	}
	if sess.Owner != nil {
		return a.isOwner(r, sess)
	}
	a.sessions.mu.Lock()
	conns := append([]*connection(nil), sess.Connections...)
	account := sess.Account
	a.sessions.mu.Unlock()
	for _, c := range conns {
		token, err := c.token(r.Context(), a.tessera)
		if err != nil {
			continue
		}
		if who, err := a.tessera.account(r.Context(), token); err == nil && who.Account == account {
			return true
		}
	}
	return false
}

// isOwner re-asks Tessera rather than trusting the session: an owner who has
// been suspended, or whose sign-in was revoked, stops being able to invite at
// once, not when their session ends.
func (a *app) isOwner(r *http.Request, sess *session) bool {
	if sess == nil || sess.Owner == nil {
		return false
	}
	token, err := sess.Owner.token(r.Context(), a.tessera)
	if err != nil {
		return false
	}
	// Asked again every time: a suspended owner, or a revoked sign-in, stops
	// being the owner at once rather than when the session ends.
	who, err := a.tessera.account(r.Context(), token)
	return err == nil && who.ServerAdministrator && (sess.Owner != sess.Website || who.Installation.Administer)
}

func (a *app) members(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess == nil || !sess.signedIn() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !a.isOwner(r, sess) {
		a.fail(w, r, sess, http.StatusForbidden, "owner_only", nil)
		return
	}
	a.sessions.mu.Lock()
	d := membersData{Enabled: a.cfg.IssuerID != "", Invited: append([]Invitation(nil), sess.Invited...)}
	a.sessions.mu.Unlock()
	d.Error = r.URL.Query().Get("error")
	d.Members = a.householdMembers(r, sess)
	a.render(w, r, sess, "members", http.StatusOK, d)
}

func (a *app) inviteMember(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess == nil || !sess.checkCSRF(r) {
		a.fail(w, r, sess, http.StatusForbidden, "expired", nil)
		return
	}
	if !a.isOwner(r, sess) {
		a.fail(w, r, sess, http.StatusForbidden, "owner_only", nil)
		return
	}
	if a.cfg.IssuerID == "" {
		a.fail(w, r, sess, http.StatusNotFound, "invitations_off", nil)
		return
	}
	secret, err := os.ReadFile(a.cfg.IssuerSecret)
	if err != nil {
		a.fail(w, r, sess, http.StatusServiceUnavailable, "invitations_off", err)
		return
	}
	inv, err := a.tessera.invite(r.Context(), a.cfg.IssuerID, strings.TrimSpace(string(secret)))
	if err != nil {
		var api *apiError
		if errors.As(err, &api) && api.Code == "invitation_cap_reached" {
			http.Redirect(w, r, "/members?error=cap", http.StatusSeeOther)
			return
		}
		a.fail(w, r, sess, http.StatusBadGateway, "error", err)
		return
	}
	a.sessions.mu.Lock()
	inv.RedeemURL = withPin(inv.RedeemURL, a.tlsPin())
	sess.Invited = append([]Invitation{inv}, sess.Invited...)
	a.sessions.mu.Unlock()
	http.Redirect(w, r, "/members", http.StatusSeeOther)
}

// ---- saved dashboards ------------------------------------------------------

// personalKey names whose personal dashboards these are: the account that
// signed in, as Tessera states it. (Before alpha.14 Tessera did not say, and
// the key was the set of connected stores.) The owner connects no store and
// has only Home's dashboards.
func (a *app) personalKey(sess *session) string {
	a.sessions.mu.Lock()
	defer a.sessions.mu.Unlock()
	if sess.Account == "" || len(sess.Connections) == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(sess.Account))
	return "acct-" + hex.EncodeToString(sum[:12])
}

func (a *app) dashboardKey(sess *session, scope string) (string, bool) {
	if scope == "home" {
		return "home", true
	}
	if _, study := studySlug(scope); study {
		if _, found := a.studies.get(strings.TrimPrefix(scope, studyScopePrefix)); found {
			return "study-" + strings.TrimPrefix(scope, studyScopePrefix), true
		}
		return "", false
	}
	if scope != "mine" {
		return "", false
	}
	if key := a.personalKey(sess); key != "" {
		return key, true
	}
	return "", false
}

// canEditDashboards: your own dashboards are yours to change; the household's
// are the owner's, so that members do not overwrite one another's.
func (a *app) canEditDashboards(r *http.Request, sess *session, scope string) bool {
	if _, study := studySlug(scope); study {
		return a.cfg.Variant == "research" && a.isOwner(r, sess)
	}
	if scope == "home" {
		return a.isOwner(r, sess)
	}
	return a.personalKey(sess) != ""
}

func (a *app) addToDashboard(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess == nil || !sess.checkCSRF(r) {
		a.fail(w, r, sess, http.StatusForbidden, "expired", nil)
		return
	}
	panel, scope, err := cleanPanel(r.PostFormValue("panel"))
	if err != nil {
		a.fail(w, r, sess, http.StatusBadRequest, "error", err)
		return
	}
	if _, study := studySlug(scope); study {
		a.addToStudyDashboard(w, r, sess, panel, scope)
		return
	}
	key, ok := a.dashboardKey(sess, scope)
	if !ok || !a.canEditDashboards(r, sess, scope) {
		a.fail(w, r, sess, http.StatusForbidden, "owner_only", nil)
		return
	}
	id, err := a.saved.addPanel(key, r.PostFormValue("name"), panel)
	if errors.Is(err, errFull) {
		a.fail(w, r, sess, http.StatusConflict, "dashboards_full", nil)
		return
	}
	if err != nil {
		a.fail(w, r, sess, http.StatusBadRequest, "dashboard_name_err", nil)
		return
	}
	http.Redirect(w, r, "/dashboards/"+scope+"/"+id, http.StatusSeeOther)
}

type dashboardPanel struct {
	CompactChart Chart
	Title        string
	Link         string
	Chart        Chart
	Rows         []panelRow
	Result       Result
	Gone         bool // the track or value no longer exists for this viewer
}

type dashboardData struct {
	StudyTitle string
	Token      string
	Scope      string
	Board      Dashboard
	Panels     []dashboardPanel
	CanEdit    bool
	Back       string
}

func (a *app) dashboard(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess == nil || !sess.signedIn() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	scope := r.PathValue("scope")
	if _, study := studySlug(scope); study {
		a.studySavedDashboard(w, r)
		return
	}
	if scope != "home" && scope != "mine" {
		a.fail(w, r, sess, http.StatusNotFound, "no_tracks", nil)
		return
	}
	key, ok := a.dashboardKey(sess, scope)
	board, found := a.saved.find(key, r.PathValue("id"))
	if !ok || !found {
		// Somebody else's personal dashboard is not found, not forbidden:
		// its existence is theirs.
		a.fail(w, r, sess, http.StatusNotFound, "no_dashboard", nil)
		return
	}
	sources := a.scopeSources(r, sess, scope)
	cat, _ := catalog(r.Context(), a.tessera, sources)
	d := dashboardData{Scope: scope, Board: board, CanEdit: a.canEditDashboards(r, sess, scope), Back: a.personalHome()}
	if scope == "home" {
		d.Back = "/home"
	}
	t := translator(language(r, sess.Lang))
	for _, raw := range board.Panels {
		q, _ := url.ParseQuery(raw)
		spec, info, err := a.parseSpecValues(q, cat)
		if err != nil {
			d.Panels = append(d.Panels, dashboardPanel{Gone: true})
			continue
		}
		res := Result{Spec: spec, Unsupported: true}
		if a.cfg.Variant == "research" || spec.Field == "" || homePlainNumber(info) {
			res, err = compute(r.Context(), a.tessera, sources, spec)
		}
		if err != nil {
			a.fail(w, r, sess, http.StatusBadGateway, "error", err)
			return
		}
		title := info.Name + " · " + t("m_"+string(spec.Measure))
		for _, f := range info.Numeric {
			if f.ID == spec.Field {
				title = info.Name + " · " + fieldName(f) + " · " + t("m_"+string(spec.Measure))
			}
		}
		p := dashboardPanel{Title: title, Link: panelLink(scope, spec), Chart: buildChartSized(res, spec.Group == "period", 720, 180), Result: res}
		p.CompactChart = buildChartSized(res, spec.Group == "period", 320, 180)
		for _, pt := range res.Points {
			p.Rows = append(p.Rows, panelRow{Label: pt.Label, Value: display(pt.Tally.Value(spec.Measure, spec.Field), 2), Count: pt.Tally.Rows})
		}
		d.Panels = append(d.Panels, p)
	}
	a.render(w, r, sess, "dashboard", http.StatusOK, d)
}

func (a *app) removeFromDashboard(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess == nil || !sess.checkCSRF(r) {
		a.fail(w, r, sess, http.StatusForbidden, "expired", nil)
		return
	}
	scope := r.PathValue("scope")
	if _, study := studySlug(scope); study {
		a.removeFromStudyDashboard(w, r)
		return
	}
	key, ok := a.dashboardKey(sess, scope)
	if !ok || !a.canEditDashboards(r, sess, scope) {
		a.fail(w, r, sess, http.StatusForbidden, "owner_only", nil)
		return
	}
	index := -1
	fmt.Sscan(r.PostFormValue("index"), &index)
	if err := a.saved.removePanel(key, r.PathValue("id"), index); err != nil {
		a.fail(w, r, sess, http.StatusNotFound, "no_dashboard", nil)
		return
	}
	if _, still := a.saved.find(key, r.PathValue("id")); still {
		http.Redirect(w, r, "/dashboards/"+scope+"/"+r.PathValue("id"), http.StatusSeeOther)
		return
	}
	back := a.personalHome()
	if scope == "home" {
		back = "/home"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// ---- removing my data ------------------------------------------------------

// The Console never holds the power to erase. A person who wants their data
// gone signs in again for exactly that, and ticks "Erase this dataset" on
// Tessera's own consent screen; the Console shows what erasing would destroy,
// erases only after they confirm, and revokes that permission the moment it is
// used, declined, or left unconfirmed.

func (a *app) connected(sess *session) []Dataset {
	a.sessions.mu.Lock()
	defer a.sessions.mu.Unlock()
	var out []Dataset
	for _, c := range sess.Connections {
		out = append(out, c.Dataset)
	}
	return out
}

func (a *app) leave(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess == nil || !sess.signedIn() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	a.render(w, r, sess, "leave", http.StatusOK, a.connected(sess))
}

func (a *app) leaveStart(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess == nil || !sess.checkCSRF(r) {
		a.fail(w, r, sess, http.StatusForbidden, "expired", nil)
		return
	}
	target := r.PostFormValue("dataset")
	for _, d := range a.connected(sess) {
		if d.ID == target {
			a.startSignIn(w, r, sess, target)
			return
		}
	}
	a.fail(w, r, sess, http.StatusNotFound, "no_tracks", nil)
}

func (a *app) discardErase(ctx context.Context, sess *session) {
	a.sessions.mu.Lock()
	e := sess.Erasing
	sess.Erasing = nil
	a.sessions.mu.Unlock()
	if e != nil {
		_ = a.tessera.revoke(ctx, e.Token)
	}
}

func (a *app) eraseApproved(w http.ResponseWriter, r *http.Request, sess *session, target string, tok tokens, l Listing) {
	ctx := r.Context()
	// The sign-in must reach exactly the store they chose, with erase
	// approved. Anything else -- another store, erase left unticked -- and
	// the permission is revoked unused.
	if len(l.Datasets) != 1 || l.Datasets[0].ID != target || !l.Datasets[0].Access.Erase {
		_ = a.tessera.revokeTokens(ctx, tok)
		a.fail(w, r, sess, http.StatusOK, "erase_not_allowed", nil)
		return
	}
	preview, err := a.tessera.erasePreview(ctx, tok.Access, target)
	if err != nil {
		_ = a.tessera.revokeTokens(ctx, tok)
		a.fail(w, r, sess, http.StatusBadGateway, "error", err)
		return
	}
	a.discardErase(ctx, sess) // an earlier, unconfirmed one is superseded
	a.sessions.mu.Lock()
	sess.Erasing = &erasing{Dataset: l.Datasets[0], Token: tok.Access, Preview: preview, Expires: time.Now().Add(10 * time.Minute)}
	a.sessions.mu.Unlock()
	http.Redirect(w, r, "/leave/confirm", http.StatusSeeOther)
}

func (a *app) pendingErase(r *http.Request, sess *session) *erasing {
	a.sessions.mu.Lock()
	e := sess.Erasing
	a.sessions.mu.Unlock()
	if e != nil && time.Now().After(e.Expires) {
		a.discardErase(r.Context(), sess)
		return nil
	}
	return e
}

func (a *app) leaveConfirmPage(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	e := a.pendingErase(r, sess)
	if e == nil {
		a.fail(w, r, sess, http.StatusOK, "expired", nil)
		return
	}
	a.render(w, r, sess, "confirm", http.StatusOK, e)
}

func (a *app) leaveConfirm(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess == nil || !sess.checkCSRF(r) {
		a.fail(w, r, sess, http.StatusForbidden, "expired", nil)
		return
	}
	e := a.pendingErase(r, sess)
	if e == nil || r.PostFormValue("dataset") != e.Dataset.ID {
		a.fail(w, r, sess, http.StatusOK, "expired", nil)
		return
	}
	err := a.tessera.erase(r.Context(), e.Token, e.Dataset.ID)
	a.discardErase(r.Context(), sess)
	if err != nil {
		a.fail(w, r, sess, http.StatusBadGateway, "error", err)
		return
	}
	if a.studies != nil {
		a.studies.withdrawFromStudies(sess.Account)
	}
	// The store's identity is gone; so is its connection.
	a.sessions.mu.Lock()
	kept := sess.Connections[:0]
	for _, c := range sess.Connections {
		if c.Dataset.ID != e.Dataset.ID {
			kept = append(kept, c)
		}
	}
	sess.Connections = kept
	a.sessions.mu.Unlock()
	a.render(w, r, sess, "message", http.StatusOK, map[string]string{"Key": "erased"})
}

func (a *app) leaveCancel(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess != nil && sess.checkCSRF(r) {
		a.discardErase(r.Context(), sess)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// connectApp shows the code the Voptrack app scans to find this server. It is
// public: it carries the server's address and nothing else.
func (a *app) connectApp(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	link, err := connectLink(a.cfg.PublicURL, a.cfg.ServerName, productOf(a.cfg.Variant), a.tlsPin())
	if err != nil {
		a.fail(w, r, sess, http.StatusInternalServerError, "error", err)
		return
	}
	code, err := qrSVG(link)
	if err != nil {
		a.fail(w, r, sess, http.StatusInternalServerError, "error", err)
		return
	}
	u, _ := url.Parse(a.cfg.PublicURL)
	a.render(w, r, sess, "connect", http.StatusOK, map[string]any{"Link": link, "QR": code, "Host": u.Host})
}

// ---- studies (Voptrack Research) -------------------------------------------

func (a *app) researcher(w http.ResponseWriter, r *http.Request) (*session, bool) {
	sess := a.sessions.get(w, r, false)
	if a.cfg.Variant != "research" {
		a.fail(w, r, sess, http.StatusNotFound, "no_dashboard", nil)
		return sess, false
	}
	if sess == nil || !sess.signedIn() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return sess, false
	}
	if !a.isOwner(r, sess) {
		a.fail(w, r, sess, http.StatusForbidden, "owner_only", nil)
		return sess, false
	}
	return sess, true
}

func (a *app) studiesPage(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.researcher(w, r)
	if !ok {
		return
	}
	studies, partial := a.monitorStudies()
	a.render(w, r, sess, "studies", http.StatusOK, map[string]any{"Studies": studies, "Unavailable": partial, "Error": r.URL.Query().Get("error")})
}

func (a *app) createStudy(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.researcher(w, r)
	if !ok {
		return
	}
	if !sess.checkCSRF(r) {
		a.fail(w, r, sess, http.StatusForbidden, "expired", nil)
		return
	}
	slug := strings.TrimSpace(r.PostFormValue("slug"))
	if _, err := a.studies.save(slug, StudyEdit{Title: r.PostFormValue("title")}, true); err != nil {
		a.render(w, r, sess, "studies", http.StatusUnprocessableEntity, map[string]any{"Studies": a.studies.list(), "Error": err.Error(), "Title": r.PostFormValue("title"), "Slug": r.PostFormValue("slug")})
		return
	}
	http.Redirect(w, r, "/studies/"+slug+"/setup/basics", http.StatusSeeOther)
}

func (a *app) editStudyPage(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.researcher(w, r)
	if !ok {
		return
	}
	st, found := a.studies.get(r.PathValue("slug"))
	if !found {
		a.fail(w, r, sess, http.StatusNotFound, "no_study", nil)
		return
	}
	cur, _ := st.Current()
	if st.Status == "draft" {
		cur.Text = setupDraft(st).ConsentText
	}
	a.render(w, r, sess, "study_edit", http.StatusOK, map[string]any{"Study": st, "Current": cur, "Revision": setupRevision(st), "Editorial": st.Status == "draft" && setupDraft(st).Editorial, "Error": r.URL.Query().Get("error")})
}

func (a *app) editStudy(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.researcher(w, r)
	if !ok {
		return
	}
	if !sess.checkCSRF(r) {
		a.fail(w, r, sess, http.StatusForbidden, "expired", nil)
		return
	}
	a.studies.operation.Lock()
	defer a.studies.operation.Unlock()
	slug := r.PathValue("slug")
	f := r.PostFormValue
	edit := StudyEdit{Title: f("title"), Summary: f("summary"), Collected: f("collected"),
		WhoSees: f("who_sees"), HowToLeave: f("how_to_leave"), JoinMode: f("join_mode"),
		ConsentText: f("consent"), RunBy: f("run_by"), Editorial: f("material") != "yes"}
	current, loadErr := a.studies.loadSetup(slug)
	if loadErr != nil {
		status, key := http.StatusServiceUnavailable, "setup_unavailable"
		if errors.Is(loadErr, os.ErrNotExist) {
			status, key = http.StatusNotFound, "no_study"
		}
		a.fail(w, r, sess, status, key, nil)
		return
	}
	// Check the form's snapshot before choosing draft versus published semantics.
	// An old draft form must never become a consent-publication request.
	if f("revision") == "" || f("revision") != setupRevision(current) {
		submitted := current
		submitted.Title, submitted.RunBy, submitted.Summary = edit.Title, edit.RunBy, edit.Summary
		submitted.Collected, submitted.WhoSees, submitted.HowToLeave, submitted.JoinMode = edit.Collected, edit.WhoSees, edit.HowToLeave, edit.JoinMode
		cur, _ := current.Current()
		cur.Text = edit.ConsentText
		a.render(w, r, sess, "study_edit", http.StatusConflict, map[string]any{"Study": submitted, "Current": cur, "Revision": f("revision"), "Editorial": edit.Editorial, "Error": "setup_stale"})
		return
	}
	if current.Status == "draft" {
		a.editDraftStudy(w, r, sess, current, edit)
		return
	}
	target := "/studies/" + slug + "/edit"
	// Everything is saved first, as it stands; only then is the study opened.
	// Opening for the first time creates its issuer from the tracks just saved,
	// which is why they cannot change afterwards.
	st, err := a.studies.saveInOperation(slug, edit, false)

	if err == nil && f("status") == "open" && st.IssuerID == "" {
		err = a.openStudy(r, sess, st)
	}
	if err == nil && f("status") != st.Status {
		// Closing disables the study's issuer, so nobody can join -- not even
		// with an invitation already handed out; reopening enables it again.
		if st.IssuerID != "" && (f("status") == "closed" || st.Status == "closed") {
			err = a.setStudyIssuer(r, sess, st.IssuerID, f("status") == "closed")
		}
		if err == nil {
			edit.Status = f("status")
			_, err = a.studies.saveInOperation(slug, edit, false)
		}
	}
	if err != nil {
		target += "?error=" + url.QueryEscape(err.Error())
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// studyPage is public: what a person reads before deciding to join. A draft
// is visible only to the researcher, so a half-written study cannot be read.
func (a *app) studyPage(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	st, found := a.studies.get(r.PathValue("slug"))
	if a.cfg.Variant != "research" || !found || (st.Status == "draft" && !a.isOwner(r, sess)) {
		a.fail(w, r, sess, http.StatusNotFound, "no_study", nil)
		return
	}
	cur, _ := st.Current()
	// Built here from the study's own fields, so it is safe to mark as a URL:
	// html/template otherwise refuses any scheme but http, https and mailto.
	var app template.URL
	if st.Status == "open" && st.JoinMode == "open" {
		app = template.URL(studyAppLink(a.cfg.PublicURL, st.Title, st.Slug, "", a.tlsPin()))
	}
	a.render(w, r, sess, "study", http.StatusOK, map[string]any{"Study": st, "Current": cur, "AppLink": app})
}

// openStudy creates the study's invitation issuer, as the researcher, through
// Tessera's administration API. Each participant who joins gets a store of
// their own: made with the study's tracks, administered by the researcher,
// readable by the study-analysis service, and named after a pseudonym Tessera
// generates -- the participant never types a name, so they cannot type their
// real one. They may read, write and erase their own store, and never change
// its tracks.
const studyCap = 1000

func (a *app) openStudy(r *http.Request, sess *session, st Study) error {
	if err := st.readyToOpen(); err != nil {
		return err
	}
	if a.cfg.Analysis == "" {
		return errors.New("this server has no study-analysis service")
	}
	types, err := studyTypes(st.Tracks)
	if err != nil {
		return err
	}
	token, err := sess.Owner.token(r.Context(), a.tessera)
	if err != nil {
		return err
	}
	who, err := a.tessera.account(r.Context(), token)
	if err != nil || !who.ServerAdministrator || !who.Installation.Administer {
		return errors.New("opening a study needs the researcher's sign-in with Administer on your behalf approved")
	}
	var policy IssuerPolicy
	policy.ID = "study-" + st.Slug
	policy.Template.Administrator = who.Account
	policy.Template.DisplayName = "{account}"
	policy.Template.Types = types
	policy.Template.Readers = append(policy.Template.Readers, struct {
		Account string `json:"account"`
	}{Account: a.cfg.Analysis})
	policy.Grants = map[string]bool{"read": true, "write": true, "erase": true}
	policy.Cap = studyCap
	policy.AssignNames = true
	// A participant who deletes their account takes their records with them.
	policy.Template.EraseWithAccount = true
	return a.provisionStudyIssuer(r, token, st, policy)
}

func (a *app) openStudyFor(w http.ResponseWriter, r *http.Request) (*session, Study, bool) {
	sess := a.sessions.get(w, r, true)
	if sess == nil {
		a.fail(w, r, nil, http.StatusServiceUnavailable, "busy", nil)
		return nil, Study{}, false
	}
	st, found := a.studies.get(r.PathValue("slug"))
	if a.cfg.Variant != "research" || !found || st.Status != "open" {
		a.fail(w, r, sess, http.StatusNotFound, "no_study", nil)
		return sess, st, false
	}
	return sess, st, true
}

func (a *app) joinPage(w http.ResponseWriter, r *http.Request) {
	sess, st, ok := a.openStudyFor(w, r)
	if !ok {
		return
	}
	code := r.URL.Query().Get("code")
	if st.JoinMode != "open" && !a.studies.hasCode(st.Slug, code) {
		message := "join_invite_only"
		if a.studies.codeUsed(st.Slug, code) {
			message = "join_code_used"
		}
		a.fail(w, r, sess, http.StatusForbidden, message, nil)
		return
	}
	cur, _ := st.Current()
	// Build only from the validated deployment origin and study fields, as on
	// the public study page. GET must not redeem or spend the invitation.
	appLink := template.URL(studyAppLink(a.cfg.PublicURL, st.Title, st.Slug, code, a.tlsPin()))
	a.render(w, r, sess, "join", http.StatusOK, map[string]any{"Study": st, "Current": cur, "Code": code, "AppLink": appLink})
}

// join records consent to the exact version the person read, then issues
// their Tessera invitation and sends them to set a password. Their account
// name is a pseudonym Tessera chose; the Console keeps only that pseudonym,
// the version and the time.
func (a *app) join(w http.ResponseWriter, r *http.Request) {
	sess, st, ok := a.openStudyFor(w, r)
	if !ok {
		return
	}
	if !sess.checkCSRF(r) {
		a.fail(w, r, sess, http.StatusForbidden, "expired", nil)
		return
	}
	if r.PostFormValue("agree") != "yes" {
		http.Redirect(w, r, "/study/"+st.Slug+"/join?code="+url.QueryEscape(r.PostFormValue("code")), http.StatusSeeOther)
		return
	}
	version, _ := strconv.Atoi(r.PostFormValue("version"))
	inv, refusal, err := a.joinStudy(r.Context(), st, version, r.PostFormValue("code"))
	switch {
	case refusal == "stale_version":
		// Consent is to the version on the page they read. If it changed
		// while they read, they read it again.
		http.Redirect(w, r, "/study/"+st.Slug+"/join?code="+url.QueryEscape(r.PostFormValue("code")), http.StatusSeeOther)
	case refusal == "code_used":
		a.fail(w, r, sess, http.StatusForbidden, "join_code_used", nil)
	case refusal == "invite_only":
		a.fail(w, r, sess, http.StatusForbidden, "join_invite_only", nil)
	case refusal == "study_full":
		a.fail(w, r, sess, http.StatusConflict, "study_full", nil)
	case err != nil:
		a.fail(w, r, sess, http.StatusBadGateway, "error", err)
	default:
		http.Redirect(w, r, inv.RedeemURL, http.StatusSeeOther)
	}
}

// joinStudy is joining, whether from the web page or the app: consent to the
// version the person was shown, a single-use code when the study is
// invite-only, then an invitation from the study's issuer and the consent
// record under the pseudonym it names. A refusal is a word, not an error.
func (a *app) joinStudy(ctx context.Context, st Study, version int, code string) (StudyInvitation, string, error) {
	cur, _ := st.Current()
	if version != cur.Version {
		return StudyInvitation{}, "stale_version", nil
	}
	if st.JoinMode != "open" && !a.studies.spendCode(st.Slug, code) {
		if a.studies.codeUsed(st.Slug, code) {
			return StudyInvitation{}, "code_used", nil
		}
		return StudyInvitation{}, "invite_only", nil
	}
	secret, err := a.studies.issuerSecret(st.Slug)
	if err != nil {
		return StudyInvitation{}, "", err
	}
	inv, err := a.tessera.inviteParticipant(ctx, st.IssuerID, secret)
	if err != nil {
		var api *apiError
		if errors.As(err, &api) && api.Code == "invitation_cap_reached" {
			return StudyInvitation{}, "study_full", nil
		}
		return StudyInvitation{}, "", err
	}
	if err := a.studies.recordConsent(st.Slug, ConsentRecord{Account: inv.Account, Version: cur.Version, At: time.Now().UTC()}); err != nil {
		return StudyInvitation{}, "", err
	}
	return inv, "", nil
}

func (a *app) participantsPage(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.researcher(w, r)
	if !ok {
		return
	}
	st, found := a.studies.get(r.PathValue("slug"))
	if !found {
		a.fail(w, r, sess, http.StatusNotFound, "no_study", nil)
		return
	}
	a.monitorPage(w, r, sess, st, true)
}

// newJoinCode makes a single-use link for an invite-only study.
func (a *app) newJoinCode(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.researcher(w, r)
	if !ok {
		return
	}
	if !sess.checkCSRF(r) {
		a.fail(w, r, sess, http.StatusForbidden, "expired", nil)
		return
	}
	slug := r.PathValue("slug")
	code, err := a.studies.newCode(slug)
	if err != nil {
		a.fail(w, r, sess, http.StatusInternalServerError, "error", err)
		return
	}
	link := a.cfg.ConsoleURL + "/study/" + slug + "/join?code=" + url.QueryEscape(code)
	app := ""
	if st, ok := a.studies.get(slug); ok {
		app = studyAppLink(a.cfg.PublicURL, st.Title, slug, code, a.tlsPin())
	}
	http.Redirect(w, r, "/studies/"+slug+"/participants?link="+url.QueryEscape(link)+"&app="+url.QueryEscape(app), http.StatusSeeOther)
}

func (a *app) setStudyIssuer(r *http.Request, sess *session, issuer string, disabled bool) error {
	token, err := sess.Owner.token(r.Context(), a.tessera)
	if err != nil {
		return err
	}
	if err := a.tessera.setIssuerDisabled(r.Context(), token, issuer, disabled); err != nil {
		return fmt.Errorf("Tessera refused to change the study's issuer: %v", err)
	}
	return nil
}

// householdMembers are the people who joined: every person account except the
// owner and the sealed custodian. Services -- Home -- are not members.
func (a *app) householdMembers(r *http.Request, sess *session) []AccountInfo {
	token, err := sess.Owner.token(r.Context(), a.tessera)
	if err != nil {
		return nil
	}
	all, err := a.tessera.accounts(r.Context(), token)
	if err != nil {
		return nil
	}
	var out []AccountInfo
	for _, acc := range all {
		if acc.Kind == "person" && acc.Account != sess.Account && acc.Account != "custodian" {
			out = append(out, acc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Account < out[j].Account })
	return out
}

// memberAction suspends or reinstates a member: a lost phone, or somebody who
// should not be signing in for now. Only these two: resetting a member's
// password would let the owner sign in as them and read their store, and
// removing a member leaves their records behind until Tessera can erase a store
// its sealed custodian co-holds -- both raised with Tessera.
func (a *app) memberAction(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess == nil || !sess.checkCSRF(r) {
		a.fail(w, r, sess, http.StatusForbidden, "expired", nil)
		return
	}
	if !a.isOwner(r, sess) {
		a.fail(w, r, sess, http.StatusForbidden, "owner_only", nil)
		return
	}
	action, account := r.PathValue("action"), r.PathValue("account")
	if action != "suspend" && action != "reinstate" {
		a.fail(w, r, sess, http.StatusNotFound, "error", nil)
		return
	}
	member := false
	for _, m := range a.householdMembers(r, sess) {
		member = member || m.Account == account
	}
	if !member {
		a.fail(w, r, sess, http.StatusNotFound, "error", nil)
		return
	}
	token, err := sess.Owner.token(r.Context(), a.tessera)
	if err == nil {
		err = a.tessera.accountAction(r.Context(), token, account, action)
	}
	if err != nil {
		a.fail(w, r, sess, http.StatusBadGateway, "error", err)
		return
	}
	http.Redirect(w, r, "/members", http.StatusSeeOther)
}

// accountPage sends the person to Tessera's own page for changing their
// password or deleting their account. The Console never sees the password.
func (a *app) accountPage(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, a.tessera.accountPage(kind), http.StatusSeeOther)
	}
}

// accountReturn is where Tessera's account pages come back to. Both changing
// a password and deleting an account revoke every sign-in the person holds,
// so the session is ended here rather than failing on its next request.
func (a *app) accountReturn(w http.ResponseWriter, r *http.Request) {
	sess := a.sessions.get(w, r, false)
	if sess != nil {
		a.sessions.drop(w, sess)
	}
	a.render(w, r, nil, "message", http.StatusOK, map[string]string{"Key": "account_back"})
}

// tlsPin is the pin a local Home's links carry, or "" for a server with a
// publicly trusted certificate. Read each time: init rewrites it on start.
func (a *app) tlsPin() string {
	if a.cfg.TLSPinFile == "" {
		return ""
	}
	raw, err := os.ReadFile(a.cfg.TLSPinFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
