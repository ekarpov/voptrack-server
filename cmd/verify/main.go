// Command verify proves a deployment answers the protocol correctly, rather
// than merely that it started.
//
// It runs Tessera's own conformance suite — the same one the reference server
// is held to — against a live server over its real URL. A deployment that
// passes has been shown to honour every capability it advertises; a scenario
// that ends without this has only shown that a process is listening.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ekarpov/tessera/pkg/conformance"
)

// A suite has three honest outcomes against a fixture with no refused token,
// not two, and conflating them is how a gate comes to certify less than it
// appears to.
type suite struct {
	name string
	run  func(context.Context, conformance.CoreFixture) error
	// needsRefused: cannot run at all. Its refusal assertions receive 401
	// where they require 403, so the suite fails for the wrong reason.
	// Provision one with `-add-person NAME -grants write`, then `-add-token`:
	// an automation token intersects with what the principal holds, so a
	// person granted only write yields a token with only write.
	needsRefused bool
	// partialWithoutRefused: runs and passes, but skips its own read-grant
	// assertions internally. A pass here is narrower than a pass with one,
	// and reporting it as a plain pass would overstate the evidence.
	partialWithoutRefused bool
}

func main() {
	url := flag.String("url", "", "base URL of the server, e.g. https://tessera.example.org")
	token := flag.String("token", "", "bearer token with read, write and schema")
	refused := flag.String("refused-token", "", "bearer token with write and NOT read; enables the core suite")
	caFile := flag.String("ca", "", "PEM file to trust in addition to the system roots, for a private CA")
	runSuites := flag.Bool("conformance", true, "run Tessera's suites, which WRITE to the dataset; false runs only the read-only deployment checks")
	isolation := flag.Bool("isolation", false, "check that a dataset the caller may not use is concealed rather than refused")
	foreign := flag.String("foreign-dataset", "", "another user's dataset id; asserts it is indistinguishable from one naming nothing")
	sharedToken := flag.String("shared-token", "", "a second person's token on the same dataset; checks that a shared store is actually shared")
	signInUser := flag.String("signin-user", "", "a person who should be able to sign in and reach their own records")
	signInPw := flag.String("signin-password-file", "", "file holding that person's password")
	goneUser := flag.String("offboarded-user", "", "a person who has been removed; must authenticate and reach nothing")
	gonePw := flag.String("offboarded-password-file", "", "file holding that person's password")
	adminUser := flag.String("admin-user", "", "administrator account; enables the administration checks")
	adminPwFile := flag.String("admin-password-file", "", "file holding that account's password — never passed as an argument, where ps would show it")
	timeout := flag.Duration("timeout", 2*time.Minute, "overall deadline")
	flag.Parse()

	// -token is only needed by the checks that use it. Signing in obtains its
	// own, and the administration checks authenticate as a person, so
	// demanding one there turns a usable command into a puzzle.
	needsToken := *runSuites || *isolation || *foreign != "" || *sharedToken != ""
	if *url == "" {
		fmt.Fprintln(os.Stderr, "verify: -url is required")
		os.Exit(2)
	}
	if needsToken && *token == "" {
		fmt.Fprintln(os.Stderr, "verify: -token is required for the conformance suites, isolation and sharing;\n"+
			"        pass -conformance=false and omit them to run only the checks that authenticate as a person")
		os.Exit(2)
	}

	client, err := httpClient(*caFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify: %v\n", err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	fixture := conformance.CoreFixture{URL: *url, Token: *token, RefusedToken: *refused, Client: client}

	suites := []suite{
		{name: "core", run: conformance.CheckCore, needsRefused: true},
		{name: "paging", run: conformance.CheckPaging},
		{name: "recovery", run: conformance.CheckRecovery, needsRefused: true},
		{name: "content", run: conformance.CheckContent},
		{name: "optional reads", run: conformance.CheckOptionalReads, partialWithoutRefused: true},
	}

	if !*runSuites {
		suites = nil
	} else {
		// Said every time rather than in documentation, because the damage is
		// silent and permanent: the suites define types, write records and
		// upload content, and remove none of it. Pointed at a deployment
		// holding real records they leave synthetic artefacts in it forever.
		fmt.Println("The conformance suites WRITE to this dataset and clean nothing up.")
		fmt.Println("Point them at a disposable one. -conformance=false runs only the")
		fmt.Println("read-only checks, which are safe against a live deployment.")
		fmt.Println()
	}

	failed, skipped, partial, ran := 0, 0, 0, len(suites)
	for _, s := range suites {
		if s.needsRefused && *refused == "" {
			fmt.Printf("  SKIP  %-16s needs -refused-token; did not run\n", s.name)
			skipped++
			continue
		}
		if err := s.run(ctx, fixture); err != nil {
			fmt.Printf("  FAIL  %-16s %v\n", s.name, err)
			failed++
			continue
		}
		if s.partialWithoutRefused && *refused == "" {
			fmt.Printf("  ok*   %-16s read-grant assertions skipped without -refused-token\n", s.name)
			partial++
			continue
		}
		fmt.Printf("  ok    %s\n", s.name)
	}

	if *sharedToken != "" {
		ran++
		fmt.Println()
		if err := checkShared(ctx, client, *url, *token, *sharedToken); err != nil {
			fmt.Printf("  FAIL  %-16s %v\n", "sharing", err)
			failed++
		} else {
			fmt.Printf("  ok    %s\n", "sharing")
		}
	}

	if *isolation || *foreign != "" {
		ran++
		fmt.Println()
		if err := checkIsolation(ctx, client, *url, *token, *foreign); err != nil {
			fmt.Printf("  FAIL  %-16s %v\n", "isolation", err)
			failed++
		} else {
			fmt.Printf("  ok    %s\n", "isolation")
		}
	}

	// The thing a multi-user deployment is for, and the only check here that
	// authenticates as a person rather than with a token an operator minted.
	if *signInUser != "" && *signInPw != "" {
		ran++
		fmt.Println()
		if err := checkSignIn(ctx, *url, client.Transport, *signInUser, *signInPw, true); err != nil {
			fmt.Printf("  FAIL  %-16s %v\n", "signing in", err)
			failed++
		} else {
			fmt.Printf("  ok    %s\n", "signing in")
		}
	}

	// Offboarding is only real if it survives the person trying again.
	if *goneUser != "" && *gonePw != "" {
		ran++
		fmt.Println()
		if err := checkSignIn(ctx, *url, client.Transport, *goneUser, *gonePw, false); err != nil {
			fmt.Printf("  FAIL  %-16s %v\n", "offboarding", err)
			failed++
		} else {
			fmt.Printf("  ok    %s\n", "offboarding")
		}
	}

	// The administration surface is this deployment's, not the protocol's, so
	// Tessera's suite says nothing about it. It is still something we ship and
	// therefore something the gate should check.
	if *adminUser != "" && *adminPwFile != "" {
		ran++
		fmt.Println()
		if err := checkAdmin(ctx, client, *url, *adminUser, *adminPwFile); err != nil {
			fmt.Printf("  FAIL  %-16s %v\n", "administration", err)
			failed++
		} else {
			fmt.Printf("  ok    %s\n", "administration")
		}
	}

	// Counted over everything that ran, not over the conformance suites alone:
	// a gate that reports six checks as five is miscounting itself.
	fmt.Printf("\n%d passed, %d partial, %d failed, %d skipped\n",
		ran-failed-skipped-partial, partial, failed, skipped)

	if skipped > 0 || partial > 0 {
		fmt.Println("\nA skipped suite is a gap in the evidence, not a success, and a partial")
		fmt.Println("one certifies less than it appears to. Supply -refused-token — a principal")
		fmt.Println("with write and NOT read. Provision one with:")
		fmt.Println("    tesserad -config … -add-person gate-refused -grants write -password-file …")
		fmt.Println("    tesserad -config … -add-token  gate-refused -secret-output …")
		fmt.Println("Tessera before v0.1.0-alpha.3 cannot create one: provisioning granted")
		fmt.Println("read+write+schema with no way to ask for less.")
	}
	if failed > 0 {
		os.Exit(1)
	}
}

// checkShared asserts the other half of a household: that a store two people
// share really is shared, in both directions.
//
// Isolation alone would be satisfied by a server that let nobody see
// anything. What a household sells is that what you put in the common store
// reaches the other person -- so this writes as each and reads as the other,
// rather than checking that a grant exists.
func checkShared(ctx context.Context, client *http.Client, base, first, second string) error {
	call := func(token, method, path string, body any) (int, []byte, error) {
		var reader io.Reader
		if body != nil {
			encoded, err := json.Marshal(body)
			if err != nil {
				return 0, nil, err
			}
			reader = bytes.NewReader(encoded)
		}
		req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, raw, err
	}

	dataset := func(token, who string) (string, error) {
		status, raw, err := call(token, "GET", "/v1/datasets", nil)
		if err != nil {
			return "", err
		}
		var listing struct {
			Datasets []struct {
				ID string `json:"id"`
			} `json:"datasets"`
		}
		if status != 200 || json.Unmarshal(raw, &listing) != nil || len(listing.Datasets) == 0 {
			return "", fmt.Errorf("%s cannot list the shared dataset: %d", who, status)
		}
		return listing.Datasets[0].ID, nil
	}

	firstDataset, err := dataset(first, "the first person")
	if err != nil {
		return err
	}
	secondDataset, err := dataset(second, "the second person")
	if err != nil {
		return err
	}
	if firstDataset != secondDataset {
		return fmt.Errorf("the two are not on one store: %s and %s — a household shares one, "+
			"and two people each seeing a different one is not sharing", firstDataset, secondDataset)
	}

	// A type of its own, so a conformance run in the same dataset cannot be
	// mistaken for this passing.
	typeID := "com.voptrack.gate.shared"
	meta := map[string]any{"id": typeID, "parent_id": nil, "name": "Shared gate", "fields": []any{
		map[string]any{"id": "note", "meta_id": typeID, "name": "Note", "type": "text"}}}
	if status, raw, err := call(first, "POST", "/v1/datasets/"+firstDataset+"/meta", map[string]any{"type": meta}); err != nil {
		return err
	} else if status != 201 && status != 409 {
		return fmt.Errorf("defining the shared type: %d %s", status, raw)
	}

	// Each writes, and the other must see it. Both directions, because a
	// grant can be asymmetric and the failure would look like nothing.
	for _, direction := range []struct{ writer, reader, who, note string }{
		{first, second, "the second person", "written by the first"},
		{second, first, "the first person", "written by the second"},
	} {
		body := map[string]any{"operations": []any{map[string]any{
			"op": "insert", "type": typeID,
			"fields": map[string]any{"note": map[string]string{"type": "text", "value": direction.note}}}}}
		if status, raw, err := call(direction.writer, "POST", "/v1/datasets/"+firstDataset+":transact", body); err != nil {
			return err
		} else if status != 200 {
			return fmt.Errorf("writing %q: %d %s", direction.note, status, raw)
		}
		status, raw, err := call(direction.reader, "POST",
			"/v1/datasets/"+firstDataset+"/objects/"+typeID+":count", map[string]any{})
		if err != nil {
			return err
		}
		var counted struct {
			Count int `json:"count"`
		}
		if status != 200 || json.Unmarshal(raw, &counted) != nil {
			return fmt.Errorf("%s cannot read the shared store: %d %s", direction.who, status, raw)
		}
		if counted.Count == 0 {
			return fmt.Errorf("%s sees nothing after a write by the other — the store is not shared", direction.who)
		}
	}
	return nil
}

// checkIsolation asserts the property a multi-user deployment actually sells:
// that one person cannot learn anything about another's data, including
// whether it exists.
//
// The subtle half is that a dataset the caller may not use must be
// INDISTINGUISHABLE from an identity naming nothing. Asserting merely "an
// error" or "403 or 404" would pass while the server leaked existence, which
// is the whole point of the answer. So the foreign probe is compared against a
// nonexistent-identity probe rather than against a constant.
func checkIsolation(ctx context.Context, client *http.Client, base, token, foreign string) error {
	probe := func(dataset string) (int, string, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", base+"/v1/datasets/"+dataset+"/meta", nil)
		if err != nil {
			return 0, "", err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		if err != nil {
			return 0, "", err
		}
		var decoded struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &decoded)
		return resp.StatusCode, decoded.Error.Code, nil
	}

	// An identity naming nothing. The baseline every other answer is compared
	// against, and load-bearing on its own: if this ever became not_found,
	// concealment would be gone and nothing else here would notice.
	nowhere := make([]byte, 16)
	if _, err := rand.Read(nowhere); err != nil {
		return err
	}
	absentStatus, absentCode, err := probe("s-" + hex.EncodeToString(nowhere))
	if err != nil {
		return err
	}
	if absentStatus != 404 || absentCode != "unavailable" {
		return fmt.Errorf("an identity naming nothing answered %d %q, expected 404 \"unavailable\" — "+
			"concealment is gone and every other isolation check rests on it", absentStatus, absentCode)
	}

	if foreign == "" {
		return nil
	}
	foreignStatus, foreignCode, err := probe(foreign)
	if err != nil {
		return err
	}
	if foreignStatus != absentStatus || foreignCode != absentCode {
		return fmt.Errorf("another user's dataset answered %d %q while one naming nothing answered %d %q — "+
			"the difference tells a caller the dataset exists",
			foreignStatus, foreignCode, absentStatus, absentCode)
	}
	return nil
}

// checkAdmin exercises the administration surface over the deployment's real
// URL. It asserts content and not only status, because a broken template can
// serve a truncated page under 200 — a failure Tessera hit this week, and one
// a status-only check cannot see.
func checkAdmin(ctx context.Context, client *http.Client, base, user, passwordFile string) error {
	raw, err := os.ReadFile(passwordFile)
	if err != nil {
		return fmt.Errorf("reading -admin-password-file: %w", err)
	}
	password := strings.TrimSpace(string(raw))

	get := func(path, as, secret string) (int, string, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", base+path, nil)
		if err != nil {
			return 0, "", err
		}
		if as != "" {
			req.SetBasicAuth(as, secret)
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, string(body), err
	}

	// Protected at all, and the refusal actually checks the password rather
	// than merely the presence of a header.
	if status, _, err := get("/admin", "", ""); err != nil {
		return err
	} else if status != 401 {
		return fmt.Errorf("unauthenticated /admin expected 401, received %d", status)
	}
	if status, _, err := get("/admin", user, password+"-wrong"); err != nil {
		return err
	} else if status != 401 {
		return fmt.Errorf("/admin with a wrong password expected 401, received %d", status)
	}

	for _, page := range []struct{ path, expect string }{
		{"/admin", "Watermark"},
		{"/admin/principals", "Principals"},
		{"/admin/authorizations", "Authorizations"},
		{"/admin/schema", "Schema"},
	} {
		status, body, err := get(page.path, user, password)
		if err != nil {
			return err
		}
		if status != 200 {
			return fmt.Errorf("%s expected 200, received %d", page.path, status)
		}
		if !strings.Contains(body, page.expect) {
			return fmt.Errorf("%s answered 200 but does not contain %q — a truncated or wrong page", page.path, page.expect)
		}
	}
	return nil
}

func httpClient(caFile string) (*http.Client, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	if caFile == "" {
		return client, nil
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("reading -ca: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no certificate found in %s", caFile)
	}
	// Verification stays on. A deployment check that disabled it would be
	// asserting the deployment is correct while ignoring the part of it that
	// most often is not.
	client.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	return client, nil
}
