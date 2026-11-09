# Swift standalone OAuth

This runbook describes the standalone server's authentication surface. It does not change the
contract. Use disposable synthetic data.

**Current against Tessera v0.1.0-alpha.26**, which this deployment pins. Two things changed at
alpha.4 and both are below: consent is two screens, and a service names its dataset in every token
request. alpha.5 changed nothing a client sends or receives.

**alpha.6 does change one thing for a client**: `aggregation`,
`distinct_references` and `text_matching` are required operations now, so a
client checking `capabilities` for any of them should delete the check. See
[the record integration guide](swift-integration.md).

**alpha.15 adds two things a client must handle.** A sign-in under load may answer `503
temporarily_unavailable` with `Retry-After`, at the sign-in step of `/oauth/authorize` and at
`/oauth/account/delete`: treat it as busy and retry; it says nothing about the password. And once
the client registry is full, a registration never used for a sign-in within an hour may be
reclaimed, answering `400 invalid_client`: a client caching its `client_id` must register again on
that. The Console does.

**alpha.16 adds account pages and one behaviour a client must handle.** Changing a password
(`/oauth/account/password`) or recovering one (`/oauth/account/recover`, with the recovery code
shown once when an invitation is redeemed) revokes every installation the account holds, including
the app's: expect `401` on the next request and send the person to sign in again. A person holds
at most 256 live installations, and approving another signs out their oldest. The account pages
take `client_id` and `return_to`, and offer the way back only when `return_to` is one of that
client's registered redirect URIs. After a person erases a store, they hold its replacement under
a new identity; everyone else's hold ends.

**alpha.9 changes something that belongs in this runbook specifically.** A client must not speak
`http://` to any URL the protocol hands it, except to a loopback host, and **`authorization_servers`
is named explicitly** — because there the destination is chosen by the far end rather than by the
person, and a server naming `http://192.168.1.10` as its authorization server sends a client
somewhere plaintext by its own say-so. **Check before the request, not on the response.** The rule
exists in this form because a consumer found their OAuth discovery had followed such an issuer and
then validated the metadata it had already fetched — the guard ran after the fetch it existed to
prevent, which is the sequence this runbook walks. An entry a client must refuse is an entry it
cannot use, so the first-usable rule passes over it rather than failing.

Compare scheme and host case-insensitively: `HTTP://` matches neither `"http"` nor `"https"` exactly,
so a guard written as *refuse `http`* admits a plaintext URL, while `http://LocalHost` is valid
loopback that a permit-`"localhost"` guard refuses and takes every development run with it. Loopback
is the address rather than its spelling — `127.0.0.0/8`, `::1` however written, `localhost`. The
[record integration guide](swift-integration.md) states all three rules together.

The Swift client reports both consumer gates pass against alpha.19 at `6f4cc376` (2026-09-27; see
[where the consumer gates stand](#evidence-to-record-from-the-independent-consumer) below). That is their report rather than
something verified here. The vector gate remains closed and frozen by `edf30a7`.

## Repeatable backend gate

The Go integration tests moved to Tessera with the server, and were renamed to client-neutral
names there. Run them from a Tessera checkout:

```sh
make integration-auth   # TestClientAuthenticationIntegration, in internal/standalone
make integration        # TestClientIntegration, in internal/devserver
```

From this repository, `make check` confirms the pinned server builds and reports its version, and
that the vocabulary is still append-only. It does not run the integration tests — there is no Go
code here to test.

`integration-auth` runs `TestClientAuthenticationIntegration` against `standalone.Open`, its complete
HTTP mux, SQLite and identity persistence, and a real loopback TLS listener. The HTTP client trusts
the test certificate; verification is enabled. Temporary storage is removed after the test. It does
not use `.tessera-dev`, real accounts or a public issuer. It reopens the runtime and TLS listener at
the same origin to check persistence. This tests the composition, not command-line TLS configuration
or an independent Swift transport/decoder.

Coverage includes discovery/authorization metadata, public registration, explicit consent and CSRF,
S256 exchange, exact callback and verifier refusals, code replay, dataset discovery, record write/read,
refresh rotation, same-client installations, refresh reuse, independent revocation, owner-only
committed/failed receipts, unknown status without grants, byte-exact replay and conflicting bytes,
grant withdrawal, and service token renewal/receipt continuity across restart. A final query checks
that replay did not duplicate records. Token responses must prohibit caching. Test failures omit
credentials, authorization query strings and response bodies.

Provisioning uses the local authority API. Complete dataset-grant withdrawal goes through the
administration surface at `/admin/authorizations` over HTTPS, like every other client operation and
recovery assertion here. Withdrawal deliberately preserves valid credentials while removing grants;
`/oauth/revoke` instead invalidates the installation's credentials entirely. No production-only test
endpoint is added.

## Start a disposable server for Swift

Requires Go 1.27.0, CGO, a C compiler and OpenSSL. In a shell from this repository:

```sh
export TESSERA_AUTH_DIR="$(mktemp -d /tmp/tessera-auth.XXXXXX)"
chmod 700 "$TESSERA_AUTH_DIR"
umask 077
cat > "$TESSERA_AUTH_DIR/tls.conf" <<'CONFIG'
[req]
distinguished_name = dn
x509_extensions = server
prompt = no
[dn]
CN = localhost
[server]
subjectAltName = DNS:localhost,IP:127.0.0.1
basicConstraints = critical,CA:TRUE
keyUsage = critical,digitalSignature,keyEncipherment,keyCertSign
extendedKeyUsage = serverAuth
CONFIG
openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
  -config "$TESSERA_AUTH_DIR/tls.conf" \
  -keyout "$TESSERA_AUTH_DIR/tls.key" -out "$TESSERA_AUTH_DIR/tls.crt"
cat > "$TESSERA_AUTH_DIR/server.json" <<CONFIG
{
  "listen": "127.0.0.1:8443",
  "issuer": "https://localhost:8443",
  "data_dir": "$TESSERA_AUTH_DIR/data",
  "tls_certificate": "$TESSERA_AUTH_DIR/tls.crt",
  "tls_key": "$TESSERA_AUTH_DIR/tls.key",
  "engine": "sqlite",
  "encrypted_storage_confirmed": true
}
CONFIG
python3 - <<'PY'
import getpass, os
with open(os.path.join(os.environ['TESSERA_AUTH_DIR'], 'password'), 'x') as f:
    f.write(getpass.getpass('Synthetic account password (at least 12 bytes): '))
PY
go build -o "$TESSERA_AUTH_DIR/tesserad" github.com/ekarpov/tessera/cmd/tesserad
"$TESSERA_AUTH_DIR/tesserad" -config "$TESSERA_AUTH_DIR/server.json" \
  -add-person synthetic-owner -grants read,write,schema,erase \
  -password-file "$TESSERA_AUTH_DIR/password"
"$TESSERA_AUTH_DIR/tesserad" -config "$TESSERA_AUTH_DIR/server.json"
```

Run this on an encrypted local volume: the configuration flag confirms that operator prerequisite;
it does not encrypt storage. Trust this temporary certificate explicitly in the test machine's user
keychain and the simulator/device trust store used by both the browser and URLSession. Keep normal
hostname and certificate validation enabled; do not implement a trust-all URLSession delegate. Remove
the temporary trust entry after testing. A physical device needs a reachable HTTPS hostname, matching
certificate and issuer/listen configuration; its `localhost` is not the development machine.

Stop the process before provisioning or deleting the disposable directory. Reusing the directory
preserves registrations, credentials and receipts. Starting with a fresh directory creates a fresh
dataset. See [self-hosting](https://github.com/ekarpov/tessera/blob/main/docs/self-hosting.md) for service provisioning and protected deployment files.

## Swift authorization sequence

1. Fetch `https://localhost:8443/.well-known/tessera`, then the advertised issuer's
   `/.well-known/oauth-authorization-server`. Require the expected HTTPS issuer; use its returned
   authorization, registration, token and revocation endpoints.
2. Register a public client using POST JSON to `/oauth/register`:

   ```json
   {"client_name":"Synthetic Swift client","redirect_uris":["com.example.tessera:/oauth/callback"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"],"response_types":["code"]}
   ```

   Save `client_id` with the issuer and installation configuration. Registration provides no access
   and returns no client secret. Configure this exact callback in the test app's URL scheme handling;
   use an app-owned reverse-domain scheme, not the example, in an actual client. Claimed HTTPS
   callbacks are preferred where available. Loopback HTTP callbacks must use an IP loopback address;
   only the port may differ from registration. Token exchange repeats the actual request callback.
3. Generate a fresh cryptographically random state and PKCE verifier for each attempt. The verifier
   is 43–128 unreserved ASCII characters. Compute base64url-without-padding of SHA-256 over its ASCII
   bytes. Retain issuer, callback, state and verifier for that single pending request.
4. Open the authorization endpoint with `ASWebAuthenticationSession` (external user agent), using
   URLComponents/URLQueryItem to encode `response_type=code`, `client_id`, `redirect_uri`, `state`,
   `code_challenge` and `code_challenge_method=S256`. Do not collect the account password in the app.

   **Do not send `dataset`.** Since v0.1.0-alpha.4 it is optional, and omitting it is the ordinary
   path for a first connection — a client cannot know a dataset identity before it holds a token,
   which is the circularity the parameter being optional resolves. Naming one is for the case where
   a person followed an invitation carrying it.

   **Consent is two screens, and the app does not see the difference.** The person signs in on the
   first and approves on the second, which shows the unverified client name, the callback, the
   permission checkboxes, and either the named dataset or a selector over what their account may
   reach. For the record scenario select read, write and schema; erase is not selected or inherited
   by default. The browser walks both steps and hands back one callback, so a real user agent needs
   no change for this.

   **A headless test double does need changing**, and this is the part that surprises people. Each
   step carries its **own single-use CSRF token**: one read from the sign-in page is refused at the
   approval step, so the second must be read from the approval response. A double that posts one
   form becomes a double that reads a response to learn what to send next, which is a different
   shape rather than a longer one. `cmd/verify/signin.go` in this repository is a working
   implementation of the whole dance if you want one to read.

   **A redirect from the first step would mean consent was never asked** — a code issued with
   nothing approved. Assert the shape of that answer rather than any text on the page.
5. On callback, validate the callback URI, exact state and `iss` against the saved attempt **before**
   using the code. Cancellation returns `error=access_denied`. Clear the pending attempt on completion
   or cancellation. Reject unsolicited or mismatched callbacks.
6. POST form-urlencoded to the token endpoint with `grant_type=authorization_code`, `client_id`,
   `code`, `redirect_uri` and `code_verifier`. A successful response contains `token_type=Bearer`,
   `expires_in=300`, `access_token` and `refresh_token`. There is no Basic client secret for this flow.
7. Keep the access token in memory. Store the refresh token in platform protected credential storage
   (Keychain, scoped to this installation/issuer/client). Never put tokens or verifier/code values in
   logs, analytics, URLs or ordinary preferences. Retain the exact pending mutation bytes, key, method,
   target and dataset in protected local storage until reconciliation finishes.
8. Send `Authorization: Bearer <access_token>` to `GET /v1/datasets`; discover the ID rather than
   hard-coding it. Follow the [record integration guide](swift-integration.md) to define a type and transact.
   The standalone server starts without the development fixture, so create the required schema first.

## Renewal, revocation and recovery transitions

| Event | Expected transition |
| --- | --- |
| Access token expires / API returns `401` | Serialize refresh for the installation; POST `grant_type=refresh_token`, `client_id`, `refresh_token`. Atomically replace the stored refresh token, then retry once. |
| Refresh returns `invalid_grant` | Require interactive sign-in; preserve unresolved mutation evidence. Do not loop refresh or blindly resubmit a write with a fresh key. |
| Old refresh token reused | Server revokes that family, including access tokens from its successful rotation. Other installations remain usable. Parallel client refreshes must be prevented. |
| Refresh response lost | Do not assume the old refresh token is still usable; replay can revoke the family. Require reauthentication if recovery fails. |
| Disconnect installation | POST form `client_id` and its refresh `token` to `/oauth/revoke`; on success discard local credentials. Both refresh and existing access tokens for this installation stop working. |
| Write response lost, valid owner credentials | POST `/v1/datasets/{d}/operations/{key}:status`; refresh preserves ownership. `committed` means do not create again; `failed` retains the original error. |
| Dataset grants withdrawn, credentials still valid | Discovery omits the dataset. Owner status/exact replay still reports the committed identities and original watermark, with `result_withheld: true` and no stored body. |
| Credential family revoked | API/status returns `401`; revoked credentials cannot recover receipts. A new sign-in creates a different authorization, even for the same person/client, and does not inherit receipt ownership. |
| Another installation asks for a retained receipt | `403 permission_denied`, even with full dataset access. |
| No retained claim | Any authenticated caller can receive `unknown`, without grants or watermark. This does not prove a request cannot still commit. |
| Same key, changed serialized bytes | `409 conflict`; retain exact bytes rather than reconstructing JSON. |

A service uses its separately provisioned secret via HTTP Basic with `grant_type=client_credentials`,
and since v0.1.0-alpha.4 **must name its `dataset` in every token request** — there is no exception
for a storage holding exactly one. Inferring it was a latent transition: a credential provisioned
when one dataset existed kept working until somebody created a second, then failed at a moment
unrelated to any change in the service holding it.

Note the error shape differs from the rest of the protocol. A refused token request answers
`{"error": "invalid_grant"}` — RFC 6749's bare string, with **no `code` member** — so a client
dispatching on `error.code` finds nothing there and needs a separate mapping for this endpoint.
Naming a dataset the account does not hold is `400 invalid_grant`, where it was once
`503 temporarily_unavailable`; anything retrying on 503 was retrying a permanent refusal.

It receives no refresh token; request another short-lived access token as needed. Renewals and server
restart preserve that service authorization's receipt ownership. Revoking the authorization also
disables the secret. Service credentials do not belong in a distributed Swift app.

## Evidence to record from the independent consumer

The Go TLS scenario passes the backend side only. Record the Swift repository/revision, test command,
platform, backend revision, exact shared-vector revision, callback type, trust configuration, and
results for the sequence and negative cases above. Run both existing record/content flows and shared
value vectors. The Swift vector consumer is complete at `35ad924`. The frozen vectors are append-only and existing
cases are not edited; do not claim full conformance from this backend gate.

**alpha.23 and alpha.24, for a client.**
- A sign-in may ask for `shared_reads=true` beside `dataset`, and read every store shared with the
  person, including later shares, without asking again.
- A suspended account that proves who it is gets `account_suspended`: tell the person to ask the
  owner, never send them to sign in again.
- The invitation page leads back to the app with `client_id` and `return_to`, which must be a
  registered redirect URI of that client.
- In alpha.24, a page sequence while data changes gives the protocol's guarantee, not a snapshot.
  A schema change to the paged type ends it with `410 cursor_expired`: start again.

**alpha.20 adds immutable fields**, nothing else. A field may declare `"immutable": true`. Once
it holds a value, changing or clearing it answers `409 conflict` with `details.reason:
"immutable_field"`. Nothing changes for a type without the flag. The Voptrack app declares none.

**alpha.19 changes two things a client can see.** After an erase, the erasing installation's
`access` on the replacement is exactly what it held before, where it used to gain every right. And a
sign-in in progress when the account's password changes gets `400 invalid_request` on approval: start
the sign-in again.

**alpha.17 and alpha.18 change nothing a client sends or receives.** The
recovery code is marked `data-recovery-code` on the pages that show it, and the
rest is performance and a server-side security fix.

**Where the consumer gates stand, and whose word each rests on.** On 2026-09-27 the Swift client
reported that both of its gates pass against Tessera **v0.1.0-alpha.18** (`1d731f0`), with
voptrack-ios at **`fb87ac6f`**. They ran serially on an iPhone 17 Pro simulator through
`tools/sim-test.sh`:

- **HTTP:** `make dev-content` on 127.0.0.1:8081. `TesseraIntegrationTests` and
  `TesseraContractTests` ran with `ERASABLE=yes`: 12 tests, 0 skipped, 0 failed. The erase test mints
  a new identity.
- **OAuth:** a disposable tesserad on `https://localhost:8443`, set up from this runbook with a
  synthetic owner. `TesseraOAuthIntegrationTests` ran 3 of 3:
  - a full sign-in (discovery, registration, PKCE, consent, callback, exchange, authenticated read);
  - refresh rotation, where the old token fails;
  - revocation.
- **Trust:** the self-signed certificate is pinned as the test's only anchor, with normal hostname
  and validity checks. No keychain or simulator trust store was changed.
- **Callback:** the redirect is reported, not followed. A headless user agent drives the flow.

This is the first independent run since `fb6752e4` on alpha.8. It is their report, relayed here;
nothing in this repository re-ran it.

**Rerun against alpha.19 (`2d68beb`), also 2026-09-27, with the same setup: both pass.** voptrack-ios
was at `6f4cc376` plus one test-helper commit, `a5491c99` (pushed with their next push).
- **HTTP:** 12 of 12, run after `make dev-reset`, with the opt-in erase test last.
- **OAuth:** 3 of 3.
- **alpha.19's two visible changes:** the eraser keeping its grants cannot affect the client, which
  never reads the replacement's grants. The `400` on approval after a password change mid-sign-in
  is **not covered**: the suite has no case for it.
- **A test defect surfaced and was fixed on the client's side.** The headless helper form-encoded
  through `URLComponents`, which leaves `+` unescaped, so a password containing `+` failed with
  `401 access_denied`. That was the helper, not the server.

**OAuth rerun against alpha.20, also 2026-09-27: 4 of 4.** voptrack-ios was at `2eec8543`, not
yet pushed, with this repository at `efd9fdf`. It includes a new case,
`aPasswordChangedMidSignInEndsThatSignIn`, which closes the gap above:
1. it begins a sign-in and authenticates;
2. it changes the password through `POST /oauth/account/password`;
3. approval answers `400 invalid_request`;
4. a fresh sign-in with the new password succeeds.

The HTTP rerun on alpha.20 and an immutable-field case are still to come. The app's client cannot
declare `immutable`, so that case will be a raw define-type test.

What *is* verified here, on 2026-09-28 against v0.1.0-alpha.26: `make backup-check` and the four
scenario gates, in CI on every push. `cmd/verify/signin.go` drives this whole authorization
sequence headlessly against a running deployment, so the flow below is exercised rather than
described — but by a Go driver, which is not an independent Swift consumer and does not substitute
for one.

Backend verification recorded on 2026-09-17 against the then-current version: all three commands
above passed, including race-enabled SQLite conformance tests. PostgreSQL database and restore
cases were skipped without configured test DSNs.
