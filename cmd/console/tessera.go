package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ekarpov/voptrack-server/internal/backupsettings"
	"github.com/ekarpov/voptrack-server/internal/recoverymaterial"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Tessera is the Console's only way to data. Everything it shows is read with
// the signed-in person's own token (or Home's, for Home dashboards), so the
// Console can never show more than Tessera allows the reader. It holds no
// credential of its own that can read anything.
type Tessera struct {
	// internal is how the Console reaches the server inside the deployment;
	// public is the address a browser and the app use. OAuth redirects and
	// the issuer are public; API calls go internal.
	internal, public string
	// console is the Console's own origin, where Tessera sends people back
	// after signing in; the public address unless the Console is private.
	console   string
	http      *http.Client
	clientID  string
	websiteID string
}

func newTessera(internal, public, console, caFile string) (*Tessera, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("reading the server's certificate: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("the server's certificate file holds no certificate")
		}
	}
	return &Tessera{
		internal: strings.TrimRight(internal, "/"),
		public:   strings.TrimRight(public, "/"),
		console:  strings.TrimRight(console, "/"),
		http: &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}},
	}, nil
}

// register makes the Console a public OAuth client of this server, once. The
// identifier is kept in the state directory so a restart does not register a
// new client every time; losing it only costs a fresh registration.
func (t *Tessera) register(ctx context.Context, stateDir string) error {
	path := t.registrationPath(stateDir)
	if raw, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(raw)) > 0 {
		t.clientID = string(bytes.TrimSpace(raw))
		// A crash after durable registration-file publication can be safely
		// reconciled without creating another remote registration.
		return recoverymaterial.Locked(stateDir, func() error {
			st, e := recoverymaterial.Read(stateDir)
			if e != nil {
				return e
			}
			if !st.Pending["oauth-registration"] {
				return nil
			}
			delete(st.Pending, "oauth-registration")
			st.Revision = recoverymaterial.ID()
			return backupsettings.WriteJSON(filepath.Join(stateDir, ".recovery-material.json"), st, 0600)
		})
	}
	return recoverymaterial.Change(stateDir, "oauth-registration", func() error {
		body, _ := json.Marshal(map[string]any{
			"client_name":                "Voptrack Console",
			"redirect_uris":              []string{t.console + "/auth/callback", t.console + accountBack},
			"token_endpoint_auth_method": "none",
			"grant_types":                []string{"authorization_code", "refresh_token"},
			"response_types":             []string{"code"},
		})
		var registered struct {
			ClientID string `json:"client_id"`
		}
		if err := t.call(ctx, "POST", "/oauth/register", "", "application/json", bytes.NewReader(body), 201, &registered); err != nil {
			return fmt.Errorf("registering the Console with the server: %w", err)
		}
		t.clientID = registered.ClientID
		return recoverymaterial.Write(path, []byte(t.clientID+"\n"))
	})
}

// registrationFile names the cached registration. Its name changes whenever
// the registration's redirect URIs do, so a Console that needs a new one
// registers again instead of using a client that lacks it.
const registrationFile = "oauth-client-2"

// registrationPath is the cached registration for this Console origin: a
// Console moved to another origin (made private, or given a new port) needs
// a registration with its new redirect URIs, and gets one.
func (t *Tessera) registrationPath(stateDir string) string {
	if t.console == t.public {
		return filepath.Join(stateDir, registrationFile)
	}
	sum := sha256.Sum256([]byte(t.console))
	return filepath.Join(stateDir, fmt.Sprintf("%s-%x", registrationFile, sum[:4]))
}

// accountBack is where Tessera's account pages offer the way back. It must be
// a registered redirect URI, or Tessera shows no way back at all.
const accountBack = "/account/back"

// accountPage is one of Tessera's own pages for a person's account --
// "password" or "delete" -- with the way back to the Console. The password
// is typed there, never here.
func (t *Tessera) accountPage(kind string) string {
	q := url.Values{"client_id": {t.clientID}, "return_to": {t.console + accountBack}}
	return t.public + "/oauth/account/" + kind + "?" + q.Encode()
}

// authorizeURL is where a browser goes to sign in. It is the public address:
// the person's browser, not the Console, talks to the sign-in pages.
func (t *Tessera) authorizeURL(state, verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {t.clientID},
		"redirect_uri":          {t.console + "/auth/callback"},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		// The Console needs explicit per-login choices, including administration.
		// Do not let prior consent skip these choices or silently drop privileges.
		"prompt": {"consent"},
		"scope":  {"read write"},
	}
	return t.public + "/oauth/authorize?" + q.Encode()
}

type consoleSession struct {
	Origin        string `json:"origin"`
	KeepSignedIn  bool   `json:"keep_signed_in"`
	MaxAgeSeconds int    `json:"max_age_seconds"`
}

func (t *Tessera) websiteAuthorizeURL(state, verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {t.websiteID}, "redirect_uri": {t.console + "/auth/callback"}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}
	return t.public + "/oauth/authorize?" + q.Encode()
}

type tokens struct {
	ClientID       string
	ConsoleSession *consoleSession
	Access         string
	Refresh        string
	Expires        time.Time
}

func (t *Tessera) exchange(ctx context.Context, form url.Values) (tokens, error) {
	return t.exchangeClient(ctx, form, t.clientID)
}

func (t *Tessera) exchangeClient(ctx context.Context, form url.Values, clientID string) (tokens, error) {
	form.Set("client_id", clientID)
	var answer struct {
		AccessToken    string          `json:"access_token"`
		RefreshToken   string          `json:"refresh_token"`
		ExpiresIn      int             `json:"expires_in"`
		ConsoleSession *consoleSession `json:"console_session"`
	}
	err := t.call(ctx, "POST", "/oauth/token", "", "application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()), 200, &answer)
	if err != nil {
		return tokens{}, err
	}
	life := time.Duration(answer.ExpiresIn) * time.Second
	if life <= 0 {
		life = 5 * time.Minute
	}
	return tokens{ClientID: clientID, ConsoleSession: answer.ConsoleSession, Access: answer.AccessToken, Refresh: answer.RefreshToken,
		Expires: time.Now().Add(life)}, nil
}

func (t *Tessera) redeem(ctx context.Context, code, verifier string) (tokens, error) {
	return t.exchange(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {t.console + "/auth/callback"}, "code_verifier": {verifier}})
}

func (t *Tessera) refresh(ctx context.Context, refresh string) (tokens, error) {
	return t.refreshClient(ctx, refresh, t.clientID)
}
func (t *Tessera) refreshClient(ctx context.Context, refresh, clientID string) (tokens, error) {
	if clientID == "" {
		clientID = t.clientID
	}
	return t.exchangeClient(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}, clientID)
}
func (t *Tessera) redeemWebsite(ctx context.Context, code, verifier string) (tokens, error) {
	tok, err := t.exchangeClient(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {t.console + "/auth/callback"}, "code_verifier": {verifier}}, t.websiteID)
	if err != nil {
		return tokens{}, err
	}
	c := tok.ConsoleSession
	if c == nil || c.Origin != t.console || (c.KeepSignedIn && c.MaxAgeSeconds != int(rememberLifetime.Seconds())) || (!c.KeepSignedIn && c.MaxAgeSeconds != 0) {
		_ = t.revokeTokens(ctx, tok)
		return tokens{}, errors.New("invalid website session handoff")
	}
	return tok, nil
}
func (t *Tessera) revokeTokens(ctx context.Context, tok tokens) error {
	clientID := tok.ClientID
	if clientID == "" {
		clientID = t.clientID
	}
	form := url.Values{"token": {tok.Access}, "client_id": {clientID}}
	return t.call(ctx, "POST", "/oauth/revoke", "", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), 200, nil)
}

// apiError keeps the protocol's code, because the Console answers some of them
// differently: a store that stopped answering is not the same as an empty one.
type apiError struct {
	Status int
	Code   string
}

func (e *apiError) Error() string { return fmt.Sprintf("server answered %d %s", e.Status, e.Code) }

func (t *Tessera) call(ctx context.Context, method, path, token, contentType string, body io.Reader, want int, into any) error {
	req, err := http.NewRequestWithContext(ctx, method, t.internal+path, body)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := t.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != want {
		var problem struct {
			Error json.RawMessage `json:"error"`
		}
		code := ""
		if json.Unmarshal(raw, &problem) == nil && len(problem.Error) > 0 {
			var nested struct {
				Code string `json:"code"`
			}
			if json.Unmarshal(problem.Error, &nested) == nil && nested.Code != "" {
				code = nested.Code
			} else {
				_ = json.Unmarshal(problem.Error, &code) // OAuth errors are a bare string
			}
		}
		return &apiError{Status: resp.StatusCode, Code: code}
	}
	if into == nil {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // sums of integers and fixed decimals must not pass through a float
	return dec.Decode(into)
}

func (t *Tessera) post(ctx context.Context, path, token string, body any, into any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return t.call(ctx, "POST", path, token, "application/json", bytes.NewReader(raw), 200, into)
}

// Dataset is one store as the listing describes it to this token. A token is
// scoped to one dataset, so a listing holds at most one.
type Dataset struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Access      struct {
		Read  bool `json:"read"`
		Write bool `json:"write"`
		Erase bool `json:"erase"`
	} `json:"access"`
}

// Listing is what a token may reach: its stores, and any server rights.
type Listing struct {
	ServerAccess struct {
		CreateDataset bool `json:"create_dataset"`
	} `json:"server_access"`
	Datasets []Dataset `json:"datasets"`
}

func (t *Tessera) listing(ctx context.Context, token string) (Listing, error) {
	var l Listing
	err := t.call(ctx, "GET", "/v1/datasets", token, "", nil, 200, &l)
	return l, err
}

func (t *Tessera) datasets(ctx context.Context, token string) ([]Dataset, error) {
	l, err := t.listing(ctx, token)
	return l.Datasets, err
}

// Invitation is a one-time link for a person to create their account.
type Invitation struct {
	RedeemURL string    `json:"redeem_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// invite asks Tessera for an invitation as the member-invitation issuer. The
// issuer is bounded by Tessera -- each redemption creates a store of the
// invitee's own, held by the sealed custodian, with fixed grants and a cap --
// so the Console holding its secret cannot grant anything else.
func (t *Tessera) invite(ctx context.Context, issuer, secret string) (Invitation, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", t.internal+"/oauth/invitations", nil)
	if err != nil {
		return Invitation{}, err
	}
	req.SetBasicAuth(issuer, secret)
	resp, err := t.http.Do(req)
	if err != nil {
		return Invitation{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		var problem struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &problem)
		return Invitation{}, &apiError{Status: resp.StatusCode, Code: problem.Error.Code}
	}
	var inv Invitation
	return inv, json.Unmarshal(raw, &inv)
}

type Field struct {
	Immutable         bool                       `json:"immutable,omitempty"`
	MetaID            string                     `json:"meta_id,omitempty"`
	OpaqueAnnotations map[string]json.RawMessage `json:"-"`
	Scale             *int                       `json:"scale,omitempty"`
	ID                string                     `json:"id"`
	Name              string                     `json:"name"`
	Type              string                     `json:"type"`
	Annotations       map[string]string          `json:"annotations,omitempty"`
	Roles             []string                   `json:"roles,omitempty"`
}

type Meta struct {
	OpaqueAnnotations map[string]json.RawMessage `json:"-"`
	Roles             []string                   `json:"roles,omitempty"`
	Annotations       map[string]string          `json:"annotations,omitempty"`
	ID                string                     `json:"id"`
	Name              string                     `json:"name"`
	ParentID          *string                    `json:"parent_id"`
	Fields            []Field                    `json:"fields"`
}

func (t *Tessera) types(ctx context.Context, token, dataset string) ([]Meta, error) {
	var answer struct {
		Types []Meta `json:"types"`
	}
	err := t.call(ctx, "GET", "/v1/datasets/"+url.PathEscape(dataset)+"/meta", token, "", nil, 200, &answer)
	return answer.Types, err
}

// Aggregate is one group of an aggregate answer. Numbers stay as json.Number:
// an integer sum can exceed a float's exact range and a fixed sum is a string.
type Aggregate struct {
	Group      json.RawMessage `json:"group"`
	RowCount   json.Number     `json:"row_count"`
	ValueCount json.Number     `json:"value_count"`
	Sum        any             `json:"sum"`
	Minimum    any             `json:"minimum"`
	Maximum    any             `json:"maximum"`
}

func (t *Tessera) aggregate(ctx context.Context, token, dataset, typeID, field, groupBy string, filter any) ([]Aggregate, error) {
	body := map[string]any{}
	if field != "" {
		body["field"] = field
	}
	if groupBy != "" {
		body["group_by"] = groupBy
	}
	if filter != nil {
		body["filter"] = filter
	}
	var answer struct {
		Aggregates []Aggregate `json:"aggregates"`
	}
	err := t.post(ctx, "/v1/datasets/"+url.PathEscape(dataset)+"/objects/"+url.PathEscape(typeID)+":aggregate", token, body, &answer)
	return answer.Aggregates, err
}

// Row is a record's values in the order its fields were requested.
type Row []struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

// rows reads every matching record page by page, up to limit rows. It exists
// for what Tessera deliberately will not aggregate -- time buckets, which need
// a timezone -- and it stops at the limit rather than reading a store whole.
func (t *Tessera) rows(ctx context.Context, token, dataset, typeID string, fields []string, filter any, limit int) ([]Row, bool, error) {
	query := map[string]any{"fields": fields}
	if filter != nil {
		query["filter"] = filter
	}
	base := "/v1/datasets/" + url.PathEscape(dataset)
	var begun struct {
		Cursor string `json:"cursor"`
	}
	if err := t.post(ctx, base+"/objects/"+url.PathEscape(typeID)+":page", token,
		map[string]any{"query": query, "page_size": 1000}, &begun); err != nil {
		return nil, false, err
	}
	var out []Row
	cursor := begun.Cursor
	for cursor != "" {
		var page struct {
			Meta struct {
				Fields []struct {
					ID string `json:"id"`
				} `json:"fields"`
			} `json:"meta"`
			Records []struct {
				Values Row `json:"values"`
			} `json:"records"`
			NextCursor *string `json:"next_cursor"`
		}
		// :next takes no body at all; an empty JSON object is refused.
		if err := t.call(ctx, "POST", base+"/pages/"+url.PathEscape(cursor)+":next", token, "", nil, 200, &page); err != nil {
			return out, false, err
		}
		// A record set's field order is the server's to choose, so values are
		// put back into the order they were asked for.
		order, err := projection(fields, page.Meta.Fields)
		if err != nil {
			return out, false, err
		}
		for _, r := range page.Records {
			row := make(Row, len(fields))
			for i, at := range order {
				if at < len(r.Values) {
					row[i] = r.Values[at]
				}
			}
			out = append(out, row)
			if len(out) >= limit {
				return out, true, nil
			}
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	return out, false, nil
}

// Preview is what erasing a store would destroy, asked before doing it.
type Preview struct {
	Objects    json.Number `json:"object_count"`
	Types      json.Number `json:"type_count"`
	OthersLose json.Number `json:"affected_principals"`
}

func (t *Tessera) erasePreview(ctx context.Context, token, dataset string) (Preview, error) {
	var p Preview
	err := t.post(ctx, "/v1/datasets/"+url.PathEscape(dataset)+":erase-preview", token, map[string]any{}, &p)
	return p, err
}

// erase destroys every record and type in the store. What remains is a new,
// empty store with a new identity, and grants do not carry over.
func (t *Tessera) erase(ctx context.Context, token, dataset string) error {
	return t.post(ctx, "/v1/datasets/"+url.PathEscape(dataset)+":erase", token, map[string]any{"confirm": dataset}, nil)
}

// revoke ends an authorization the Console obtained, so a permission granted
// for one action does not outlive it.
func (t *Tessera) revoke(ctx context.Context, token string) error {
	form := url.Values{"token": {token}, "client_id": {t.clientID}}
	return t.call(ctx, "POST", "/oauth/revoke", "", "application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()), 200, nil)
}

// Account is who a token belongs to, from GET /oauth/account (Tessera
// alpha.14). It never names a dataset or another account.
type Account struct {
	Account             string `json:"account"`
	Kind                string `json:"kind"`
	ServerAdministrator bool   `json:"server_administrator"`
	Installation        struct {
		Administer bool `json:"administer"`
	} `json:"installation"`
	// PasswordSetBy is present while the current password was set by
	// somebody else (alpha.16), so the person can be told to choose their own.
	PasswordSetBy *struct {
		Account string    `json:"account"`
		At      time.Time `json:"at"`
	} `json:"password_set_by"`
}

func (t *Tessera) account(ctx context.Context, token string) (Account, error) {
	var a Account
	err := t.call(ctx, "GET", "/oauth/account", token, "", nil, 200, &a)
	return a, err
}

// serviceToken fetches a token for a service on one named dataset, with no
// consent screen: client_credentials, the service's own secret, and the
// dataset. It carries whatever that service may read there -- for Home, only
// the tracks the store's member shared.
func (t *Tessera) serviceToken(ctx context.Context, service, secret, dataset string) (tokens, error) {
	form := url.Values{"grant_type": {"client_credentials"}, "dataset": {dataset}}
	req, err := http.NewRequestWithContext(ctx, "POST", t.internal+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return tokens{}, err
	}
	req.SetBasicAuth(service, secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.http.Do(req)
	if err != nil {
		return tokens{}, err
	}
	defer resp.Body.Close()
	var answer struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&answer)
	if resp.StatusCode != 200 || answer.AccessToken == "" {
		return tokens{}, &apiError{Status: resp.StatusCode, Code: answer.Error}
	}
	life := time.Duration(answer.ExpiresIn) * time.Second
	if life <= 0 {
		life = 5 * time.Minute
	}
	return tokens{Access: answer.AccessToken, Expires: time.Now().Add(life)}, nil
}

// Readers: who may read which tracks of a store, managed by the store's
// administrator with their own token (Tessera alpha.14's administration API).

func (t *Tessera) readerTypes(ctx context.Context, token, dataset, reader string) ([]string, error) {
	var r struct {
		Types []string `json:"types"`
	}
	err := t.call(ctx, "GET", "/admin/api/v1/datasets/"+url.PathEscape(dataset)+"/readers/"+url.PathEscape(reader), token, "", nil, 200, &r)
	var api *apiError
	if errors.As(err, &api) && api.Status == 404 {
		return nil, nil // not a reader: nothing shared
	}
	return r.Types, err
}

// setReader makes reader able to read exactly these types, in one request, so
// it never reads anything in between. No types removes the reader entirely.
func (t *Tessera) setReader(ctx context.Context, token, dataset, reader string, types []string) error {
	path := "/admin/api/v1/datasets/" + url.PathEscape(dataset) + "/readers/" + url.PathEscape(reader)
	if len(types) == 0 {
		err := t.call(ctx, "DELETE", path, token, "", nil, 204, nil)
		var api *apiError
		if errors.As(err, &api) && api.Status == 404 {
			return nil // already not a reader
		}
		return err
	}
	raw, err := json.Marshal(map[string]any{"types": types})
	if err != nil {
		return err
	}
	return t.call(ctx, "PUT", path, token, "application/json", bytes.NewReader(raw), 204, nil)
}

// administered is what this token's account administers, from the
// administration API: for a member, their own store.
func (t *Tessera) administered(ctx context.Context, token string) ([]Dataset, error) {
	var o struct {
		Datasets []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"datasets"`
	}
	if err := t.call(ctx, "GET", "/admin/api/v1/overview", token, "", nil, 200, &o); err != nil {
		return nil, err
	}
	var out []Dataset
	for _, d := range o.Datasets {
		out = append(out, Dataset{ID: d.ID, DisplayName: d.DisplayName})
	}
	return out, nil
}

// IssuerPolicy is a study's invitation issuer, created through the
// administration API as the researcher (a server administrator).
type IssuerPolicy struct {
	ID       string `json:"id"`
	Template struct {
		Administrator string    `json:"administrator"`
		DisplayName   string    `json:"display_name"`
		Types         []TypeDef `json:"types"`
		Readers       []struct {
			Account string `json:"account"`
		} `json:"readers"`
		// EraseWithAccount: deleting the person's account erases the store
		// this template made them, although its administrator co-holds it.
		EraseWithAccount bool `json:"erase_with_account"`
	} `json:"template"`
	Grants      map[string]bool `json:"grants"`
	Cap         int             `json:"cap"`
	AssignNames bool            `json:"assign_names"`
}

// createIssuer returns the new issuer's secret, which Tessera shows once.
func (t *Tessera) createIssuer(ctx context.Context, token string, p IssuerPolicy) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	var answer struct {
		Secret string `json:"secret"`
	}
	err = t.call(ctx, "POST", "/admin/api/v1/issuers", token, "application/json", bytes.NewReader(raw), 201, &answer)
	return answer.Secret, err
}

// StudyInvitation carries the pseudonym Tessera generated for the participant.
type StudyInvitation struct {
	RedeemURL string `json:"redeem_url"`
	Account   string `json:"account"`
}

func (t *Tessera) inviteParticipant(ctx context.Context, issuer, secret string) (StudyInvitation, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", t.internal+"/oauth/invitations", nil)
	if err != nil {
		return StudyInvitation{}, err
	}
	req.SetBasicAuth(issuer, secret)
	resp, err := t.http.Do(req)
	if err != nil {
		return StudyInvitation{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		var problem struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &problem)
		return StudyInvitation{}, &apiError{Status: resp.StatusCode, Code: problem.Error.Code}
	}
	var inv StudyInvitation
	if err := json.Unmarshal(raw, &inv); err != nil || inv.Account == "" {
		return StudyInvitation{}, errors.New("the invitation carried no assigned name")
	}
	return inv, nil
}

// heldBy maps each account the administrator can see to the datasets it
// holds, from the administration API's overview. For a researcher, that
// includes every participant, each holding exactly their own store.
func (t *Tessera) heldBy(ctx context.Context, token string) (map[string][]string, error) {
	var o struct {
		Accounts []struct {
			Account  string                     `json:"account"`
			Datasets map[string]json.RawMessage `json:"datasets"`
		} `json:"accounts"`
	}
	if err := t.call(ctx, "GET", "/admin/api/v1/overview", token, "", nil, 200, &o); err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, a := range o.Accounts {
		for d := range a.Datasets {
			out[a.Account] = append(out[a.Account], d)
		}
	}
	return out, nil
}

// setIssuerDisabled stops or restarts a study's issuer.
// Disabling also stops invitations already handed out from being redeemed, so
// closing a study closes it to everyone at once.
func (t *Tessera) setIssuerDisabled(ctx context.Context, token, issuer string, disabled bool) error {
	raw, _ := json.Marshal(map[string]bool{"disabled": disabled})
	return t.call(ctx, "PATCH", "/admin/api/v1/issuers/"+url.PathEscape(issuer), token, "application/json",
		bytes.NewReader(raw), 200, nil)
}

// forgetRegistration drops the cached client identifier. Tessera may reclaim a
// registration never used for a sign-in within an hour once its registry is
// full, and answers invalid_client for it; the Console then registers again.
func (t *Tessera) forgetRegistration(stateDir string) {
	_ = os.Remove(t.registrationPath(stateDir))
	t.clientID = ""
}

// AccountInfo is one account as the administration API's overview shows it.
type AccountInfo struct {
	Account   string                     `json:"account"`
	Kind      string                     `json:"kind"`
	Suspended bool                       `json:"suspended"`
	Datasets  map[string]json.RawMessage `json:"datasets"`
}

func (t *Tessera) accounts(ctx context.Context, token string) ([]AccountInfo, error) {
	var o struct {
		Accounts []AccountInfo `json:"accounts"`
	}
	err := t.call(ctx, "GET", "/admin/api/v1/overview", token, "", nil, 200, &o)
	return o.Accounts, err
}

// accountAction suspends or reinstates an account (204). Suspension stops
// every credential the account holds at once and keeps them; reinstating
// restores exactly what it had. Neither gives the administrator any access to
// the account's data.
func (t *Tessera) accountAction(ctx context.Context, token, account, action string) error {
	return t.call(ctx, "POST", "/admin/api/v1/accounts/"+url.PathEscape(account)+"/"+action, token,
		"application/json", strings.NewReader("{}"), 204, nil)
}

// backupNow asks the server to take a backup while running (a server
// administrator's action). It answers the new archive's file name.
func (t *Tessera) backupNow(ctx context.Context, token string) (string, error) {
	var answer struct {
		Backup string `json:"backup"`
	}
	err := t.call(ctx, "POST", "/admin/api/v1/backups", token, "application/json", strings.NewReader("{}"), 201, &answer)
	return answer.Backup, err
}

// rotateIssuerSecret replaces an issuer's secret, answering the new one (a
// server administrator's action). The old secret stops working at once.
func (t *Tessera) rotateIssuerSecret(ctx context.Context, token, issuer string) (string, error) {
	var answer struct {
		Secret string `json:"secret"`
	}
	err := t.call(ctx, "POST", "/admin/api/v1/issuers/"+url.PathEscape(issuer)+"/secret", token,
		"application/json", strings.NewReader("{}"), 200, &answer)
	return answer.Secret, err
}

// projection maps each asked-for field to its position in a record set whose
// order the server chose. A page with no fields described (an empty one) maps
// in request order.
func projection(asked []string, got []struct {
	ID string `json:"id"`
}) ([]int, error) {
	order := make([]int, len(asked))
	if len(got) == 0 {
		for i := range order {
			order[i] = i
		}
		return order, nil
	}
	at := map[string]int{}
	for i, f := range got {
		at[f.ID] = i
	}
	for i, id := range asked {
		j, ok := at[id]
		if !ok {
			return nil, fmt.Errorf("the server's answer lacks the field %q", id)
		}
		order[i] = j
	}
	return order, nil
}

// latest answers a field's greatest value in a type, by sorting descending
// and reading one record: Tessera aggregates only numbers, and a newest date
// is what a researcher asks of a participant. Empty when there is none.
func (t *Tessera) latest(ctx context.Context, token, dataset, typeID, field string) (string, error) {
	var answer struct {
		Records []struct {
			Values Row `json:"values"`
		} `json:"records"`
	}
	query := map[string]any{"fields": []string{field},
		"result_sort": []map[string]any{{"field_id": field, "ascending": false}}}
	err := t.post(ctx, "/v1/datasets/"+url.PathEscape(dataset)+"/objects/"+url.PathEscape(typeID)+":query", token,
		map[string]any{"query": query, "limit": 1}, &answer)
	if err != nil || len(answer.Records) == 0 || len(answer.Records[0].Values) == 0 {
		return "", err
	}
	v, _ := answer.Records[0].Values[0].Value.(string)
	return v, nil
}

// StoreUsage is how full a store is: its records' size against its limit, as
// the administration overview reports it to the store's administrator.
type StoreUsage struct {
	Records  int64 `json:"records"`
	Bytes    int64 `json:"bytes"`
	MaxBytes int64 `json:"max_bytes"`
}

// Percent is how full, rounded down; 0 when the limit is unknown.
func (u StoreUsage) Percent() int64 {
	if u.MaxBytes <= 0 {
		return 0
	}
	return u.Bytes * 100 / u.MaxBytes
}

// usage reads one store's usage from the overview; false when not reported.
func (t *Tessera) usage(ctx context.Context, token, dataset string) (StoreUsage, bool) {
	var o struct {
		Datasets []struct {
			ID    string      `json:"id"`
			Usage *StoreUsage `json:"usage"`
		} `json:"datasets"`
	}
	if err := t.call(ctx, "GET", "/admin/api/v1/overview", token, "", nil, 200, &o); err != nil {
		return StoreUsage{}, false
	}
	for _, d := range o.Datasets {
		if d.ID == dataset && d.Usage != nil {
			return *d.Usage, true
		}
	}
	return StoreUsage{}, false
}
