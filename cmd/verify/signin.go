package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// checkSignIn proves the thing a multi-user deployment exists to provide: that
// a person given a password can reach their own records.
//
// Every other check here authenticates with an automation token an operator
// minted, which is convenient and proves nothing about people. A person's only
// route from a password to their data is the authorization flow, so this walks
// it: register a client, sign in, approve, exchange the code, and read.
//
// It is deliberately a whole-journey check rather than a set of steps. Each
// step passing while the journey fails is the failure mode worth catching.
var csrfField = regexp.MustCompile(`name="csrf"\s+value="([^"]+)"`)

// expectAccess false inverts it: the person authenticates but must reach
// nothing. That is what offboarding looks like from outside, and it is worth
// checking separately because the two halves of offboarding fail differently
// — revoking credentials alone lets them sign in again, and emptying grants
// alone leaves live tokens working.
func checkSignIn(ctx context.Context, base string, transport http.RoundTripper, user, passwordFile string, expectAccess bool) error {
	raw, err := os.ReadFile(passwordFile)
	if err != nil {
		return fmt.Errorf("reading -signin-password-file: %w", err)
	}
	password := strings.TrimSpace(string(raw))

	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	// Its own client: the flow is cookie-bound, and the final redirect goes to
	// an application scheme that must not be followed — the code is in the
	// Location header, and following it would only fail obscurely.
	client := &http.Client{Jar: jar, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	post := func(path string, form url.Values) (int, string, *http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", base+path, strings.NewReader(form.Encode()))
		if err != nil {
			return 0, "", nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			return 0, "", nil, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, string(body), resp, err
	}

	// A public client, registered the way any installation would be.
	const redirect = "com.voptrack.gate:/oauth/callback"
	registration, err := json.Marshal(map[string]any{
		"client_name": "Voptrack deployment gate", "redirect_uris": []string{redirect},
		"token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code"},
		"response_types": []string{"code"}})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/oauth/register", strings.NewReader(string(registration)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	var registered struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(body, &registered); err != nil || registered.ClientID == "" {
		return fmt.Errorf("registering a client: %d %s", resp.StatusCode, body)
	}

	// PKCE. A fresh verifier per attempt, challenge is base64url-unpadded
	// SHA-256 over its ASCII bytes.
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return err
	}
	verifier := base64.RawURLEncoding.EncodeToString(seed)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state := base64.RawURLEncoding.EncodeToString(seed[:16])

	query := url.Values{"response_type": {"code"}, "client_id": {registered.ClientID},
		"redirect_uri": {redirect}, "state": {state},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "prompt": {"consent"}}
	// No dataset named: this is a first connection, which is the ordinary case
	// since alpha.4 and the one a client cannot avoid — it cannot know an
	// identity before it holds a token.
	signInReq, err := http.NewRequestWithContext(ctx, "GET", base+"/oauth/authorize?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	page, err := client.Do(signInReq)
	if err != nil {
		return err
	}
	signInBody, _ := io.ReadAll(io.LimitReader(page.Body, 1<<20))
	page.Body.Close()
	if page.StatusCode != 200 {
		return fmt.Errorf("the sign-in page answered %d", page.StatusCode)
	}
	flow := valueOf(string(signInBody), "flow")
	first := csrfField.FindStringSubmatch(string(signInBody))
	if flow == "" || first == nil {
		return fmt.Errorf("the sign-in page carries no flow or csrf field")
	}

	// Step one: authenticate. Answers with the approval screen rather than a
	// redirect.
	status, approval, _, err := post("/oauth/authorize", url.Values{
		"flow": {flow}, "csrf": {first[1]}, "username": {user},
		"password": {password}, "decision": {"continue"}})
	if err != nil {
		return err
	}
	// A redirect here would mean the server issued a code on the strength of a
	// password alone, without ever showing what was being approved. That is a
	// different failure from the page being broken and deserves saying so:
	// the shape of the answer is the assertion, not any text on the page.
	if status == 303 {
		return fmt.Errorf("signing in as %q returned a callback without asking for consent — "+
			"a code was issued with nothing approved", user)
	}
	if status != 200 {
		return fmt.Errorf("signing in as %q answered %d, expected the approval screen", user, status)
	}

	// The second step carries its OWN single-use token. Reusing the first is
	// refused, which is the point, so it is read again from this page.
	second := csrfField.FindStringSubmatch(approval)
	if second == nil {
		return fmt.Errorf("the approval screen carries no csrf field")
	}
	if second[1] == first[1] {
		return fmt.Errorf("the approval screen reuses the sign-in token; each step must carry its own")
	}
	dataset := valueOf(approval, "dataset")
	if !expectAccess {
		// Offboarded: authenticating is fine, reaching something is not.
		if dataset != "" {
			return fmt.Errorf("%q was offboarded and the approval screen still offers dataset %s", user, dataset)
		}
		return nil
	}
	if dataset == "" {
		return fmt.Errorf("the approval screen offers no dataset for %q — the account reaches none", user)
	}

	// Step two: approve. Read and write only; this is a person, not an owner.
	status, _, redirected, err := post("/oauth/authorize", url.Values{
		"flow": {flow}, "csrf": {second[1]}, "decision": {"allow"},
		"dataset": {dataset}, "read": {"yes"}, "write": {"yes"}})
	if err != nil {
		return err
	}
	if status != 303 {
		return fmt.Errorf("approving answered %d, expected a redirect carrying the code", status)
	}
	location, err := url.Parse(redirected.Header.Get("Location"))
	if err != nil {
		return err
	}
	returned := location.Query()
	if returned.Get("state") != state {
		return fmt.Errorf("the callback state does not match the request; an unsolicited callback")
	}
	code := returned.Get("code")
	if code == "" {
		return fmt.Errorf("the callback carries no code: %s", returned.Encode())
	}

	status, tokenBody, _, err := post("/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "client_id": {registered.ClientID},
		"code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}})
	if err != nil {
		return err
	}
	var issued struct {
		AccessToken string `json:"access_token"`
	}
	if status != 200 || json.Unmarshal([]byte(tokenBody), &issued) != nil || issued.AccessToken == "" {
		return fmt.Errorf("exchanging the code: %d %s", status, tokenBody)
	}

	// The whole point: the person can now reach their own records, and only
	// those. A token from a dataset-scoped authorization lists exactly the one
	// it was granted.
	listing, err := http.NewRequestWithContext(ctx, "GET", base+"/v1/datasets", nil)
	if err != nil {
		return err
	}
	listing.Header.Set("Authorization", "Bearer "+issued.AccessToken)
	final, err := client.Do(listing)
	if err != nil {
		return err
	}
	finalBody, _ := io.ReadAll(io.LimitReader(final.Body, 1<<20))
	final.Body.Close()
	var listed struct {
		Datasets []struct {
			ID string `json:"id"`
		} `json:"datasets"`
	}
	if final.StatusCode != 200 || json.Unmarshal(finalBody, &listed) != nil {
		return fmt.Errorf("reading as %q: %d %s", user, final.StatusCode, finalBody)
	}
	if len(listed.Datasets) != 1 || listed.Datasets[0].ID != dataset {
		return fmt.Errorf("%q signed in and reached %d dataset(s); expected exactly the one approved (%s)",
			user, len(listed.Datasets), dataset)
	}
	return nil
}

// valueOf pulls a form field's value out of the rendered page. The pages are
// server-rendered and fixed, so a parser would be ceremony; a wrong match
// fails the check rather than passing it.
func valueOf(page, field string) string {
	for _, pattern := range []string{
		`name="` + field + `"\s+value="([^"]+)"`,
		`<option\s+value="([^"]+)"`,
	} {
		if field == "dataset" && !strings.Contains(page, `name="dataset"`) {
			return ""
		}
		if m := regexp.MustCompile(pattern).FindStringSubmatch(page); m != nil {
			return m[1]
		}
	}
	return ""
}
