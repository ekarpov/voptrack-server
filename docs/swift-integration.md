# Swift integration

> This repository is now Voptrack's deployment of
> [Tessera](https://github.com/ekarpov/tessera) rather than an implementation of it. `make dev`
> and `make dev-content` still start the same development server, from a pinned Tessera version.
> Protocol questions are answered in the Tessera repository; the commands below are unchanged.

The backend is ready for synthetic record and attachment integration. This guide describes the
current implementation, not additional normative rules. The wire decisions for results, watermarks, aliases and receipt visibility are resolved in `f43824a`;
remaining [contract questions](https://github.com/ekarpov/tessera/blob/main/docs/tessera-spec-review.md) concern embedding, lifecycle, sharing and content edge cases. The independent vector gate is complete. The Swift client reports its HTTP integration gate run on
Tessera v0.1.0-alpha.4 with its suite green at `feaa91eb` — their report, not verified here. A
later run is recorded in Tessera's own plan: Swift `d9062ebf` against v0.1.0-alpha.7, twelve of
twelve, with the version read from the server's discovery document rather than taken from either
side — which caught a build made from the wrong checkout before anything was recorded against it.
That is Tessera's record, not this repository's verification either.

## Transport, and it governs every request below

Three rules landed across **v0.1.0-alpha.7, alpha.8 and alpha.9**, and they are client obligations:
this repository's gates test servers, so nothing here can catch a client that gets them wrong. They
are listed first because they decide whether a request may be sent at all.

**Never `http://`, except to a loopback host.** A client must not speak plaintext to a Tessera
server anywhere else. A private or link-local address is not loopback: `http://192.168.1.10` and
`http://storage.local` are exactly what this forbids, because every other machine on that network is
a third party. It forbids no deployment this repository ships — the self-hosted server refuses to
start without a certificate and key, and the development server is plaintext on loopback with
synthetic data, which is the exemption and the reason it exists.

**It covers every URL the protocol hands you, `authorization_servers` included, and you check before
the request rather than on the response.** Alpha.8 stated the rule for a URL a client is handed;
alpha.9 widened it, because in discovery the destination is chosen by the far end rather than by the
person, and a server naming `http://192.168.1.10` as its authorization server sends a client
somewhere plaintext by its own say-so. A consumer found their OAuth discovery had followed such an
issuer and then validated the metadata it had already fetched — the guard ran after the fetch it
existed to prevent.

**Compare scheme and host case-insensitively, and treat loopback as an address rather than a
spelling.** A URL type may not normalise them, and both directions fail:

- `HTTP://` matches neither `"http"` nor `"https"` under an exact comparison, so a guard written as
  *refuse `http`* **admits a plaintext URL**.
- `http://LocalHost` is a valid loopback URL that a guard permitting only `"localhost"` **refuses**,
  taking every development run with it.

Loopback means `127.0.0.0/8`, `::1` however written, and `localhost`. The asymmetry is deliberate: a
client that refuses an unusual spelling of a loopback address stays conformant, because refusing a
loopback address costs a developer a confusing error and accepting a non-loopback one costs somebody
their records.

## Since alpha.9: four more client obligations

Landed across **v0.1.0-alpha.10, alpha.11 and alpha.12**. As with transport, this
repository's gates test servers and catch none of these; the change lists linked
from [operating](operating.md#upgrading) are the source.

**`fixed`, an exact decimal, and the vector gate fails until you implement it.**
Carried as a JSON **string** (`{"type": "fixed", "value": "-1234.56"}`) so it never
passes through a `Double`; a field declaring it must declare a `scale`. Only the
canonical form is accepted — `1.50`, `1.0`, `01`, `-0`, `+1`, `1e5` are refused
with `400 invalid_field_value`, and the server refuses rather than normalises, so
canonicalise before sending. Equality is then string equality. The shared vectors
grew from 130 to 156 cases for it, so a client running the vector gate is red
against these tags until `fixed` is in. This is the type for money: a sum over a
`fixed` field is exact and returned as a string, which replaces reducing rows
locally.

**Content URIs must be `https` unless loopback — check before fetching.** The
resolved read URI and the transfer URI are both credentials: whoever holds one can
read those bytes for its lifetime. A plaintext one is refused before the request,
not while validating the response. Our deployments issue `https` ones; the
content conformance check now asserts it and passes.

**`507 dataset_full` is a new error code.** A write to a dataset that has outgrown
its `max_materialized_bytes` fails with it, `details.reason:
"materialization_limit"` and the bound in `details.maximum`. It is not the
caller's mistake and not transient: no retry and no smaller request clears it, and
only the operator can. A read past the same bound fails `422
query_result_limit_exceeded` with the same `reason`, so one branch handles the
bound while the code says which side failed. **If errors are mapped exhaustively,
add this one.** On every deployment in this repository the bound is 64 MiB per
dataset and cannot be configured — see the plan.

**A type-scoped share is now held on the account** (alpha.12). A guest account's
every sign-in reads only the shared types; before alpha.12 a new sign-in read
everything. A client listing a guest's storage should expect queries on other
types to be refused `403`, from every device.

## Start and reset

From this repository, with Go 1.27.0, CGO and a C compiler:

```sh
make dev-content
```

Base URL: `http://127.0.0.1:8081`. Read the bearer token from `.tessera-dev/token` and send it as
`Authorization: Bearer <token>`. The server prints only the file location. Do not commit the token,
put it in a URL, or include it in client diagnostics. This local synthetic authorization permits
read/write/schema/erase. `make dev` runs the same record server without content enabled.

For an isolated directory and another port:

```sh
go tool tesserad -dev -dev-content -listen 127.0.0.1:8082 -data-dir "$(mktemp -d)"
```

`go tool` runs the Tessera version pinned in `go.mod`. There is no `./cmd/tesserad` in this
repository — the server lives in Tessera and is consumed as a tool dependency.

Restart preserves credentials, dataset/store IDs, records, watermarks and recovery receipts. Fixtures
are seeded only for a fresh dataset or an explicit reset; restarting never overwrites client edits.
If your directory predates this fixture, stop the process and run:

```sh
make dev-reset
make dev-content
```

**Reset retires all development records and attachments**, creates fresh dataset/store identities,
and seeds the replacement. The local credential remains usable; previously granted authorizations
are revoked. Reset uses protocol erasure, preserves retirement evidence and queues physical cleanup.
It refuses a database held by a running server. For a custom directory use
`go run ./cmd/tesserad -dev -dev-content -dev-reset -data-dir <directory>`.

The development listener is loopback-only. Use the [HTTPS standalone deployment](https://github.com/ekarpov/tessera/blob/main/docs/self-hosting.md)
for a physical device or for OAuth/PKCE integration. A local Swift target must permit its explicit
HTTP development endpoint under its platform networking configuration; do not disable TLS validation
for the HTTPS deployment.

## Home round trip

`make home-dev` (after `make image`) runs the real Home stack on
`https://localhost:8544` (the Console's own port is 9445). The owner sets up a
household through the Console, as a person would:
- **Sara and Ben** record exactly the inputs of `specs/vectors/home-aggregation-v1.json`, and share
  Spending with Home but not Weight.
- **Lina** records 999 in Spending and 70.5 in Weight, shares nothing, and is suspended. None of her
  records may ever appear in a household answer.

`.voptrack-home-dev/`, not committed, holds:
- `ca.crt`: trust it in the simulator as for research.
- `home.json`: each member's account, password, recovery code, store, OAuth client (redirect
  `com.voprak.voptrack:/oauth/callback`) and tokens; who shares what; who is suspended; and the
  household answers, taken from the vectors.

Checked 2026-09-27: the Console's Home panel answers Sara 12.8 and Ben 100.25, as the vectors say.
Sara's token reads her store, and Lina's is refused.

**Sharing is the household group reader**, `readers/issuer:home-members` (Tessera alpha.23). The
Console's Sharing page writes it together with the `home` service reader, which only the web
dashboards read. A member's app reads the household with one sign-in: its own store plus
`shared_reads=true` in the authorization request. With `shared_reads`, Tessera's chooser (alpha.25)
offers only the person's own stores, named and marked "(yours)". Picking by a known id still works. `make home-dev-token` signs every member in again (not Lina, who cannot), because
refresh tokens rotate. `make home-dev-down` deletes everything. CI runs all three.

**The aggregation vectors**, `specs/vectors/home-aggregation-v1.json`, are the Console's own answers.
The inputs, ten cases and the rules are stated in the file, and `TestHomeVectors` recomputes every
case with the Console's arithmetic. Import it as test data for the app's Home aggregation.

## Research round trip

`make research-dev` (after `make image`) runs the real Research stack on
`https://localhost:8444`, which is Tessera, the Console and Caddy. The script
designs a study in the Console's track designer with the same tracks as the
app's `study-sleep` vector, plus Weight from the catalogue. It opens the study,
and one synthetic participant joins. Everything the app needs is in
`.voptrack-research-dev/`, which is not committed:

- `ca.crt`: Caddy's local root. Trust it in the simulator with
  `xcrun simctl keychain booted add-root-cert .voptrack-research-dev/ca.crt`.
- `participant.json`: the server, the participant's pseudonym and password,
  their recovery code, their store, the OAuth client and redirect URI, an
  access token, a refresh token, and the types installed in the store.

- `participant-2.json`: a second participant in the same shape, whose store a
  withdrawal test may erase. Run that test last, and reset afterwards with
  `make research-dev-down && make research-dev`. The first participant's store
  is never erased, so recording tests always have the study's types.

The participant holds read, write and erase on their store, and never schema:
defining a type answers `403`. The store holds `com.voptrack.track`,
`study.app_roundtrip.sleep` and `com.voptrack.track.weight`. Metadata is
effective, so the study track's fields begin with the root's, and annotations
come back exactly as installed. Checked 2026-09-26: a refresh, then a record
with `timestamp`, `hours_slept` and `hours_slept_unit`, answered `201`.

**Run by the app, 2026-09-27: passes.** voptrack-ios `2d212732` against Tessera
alpha.16 on research-dev. The app read `/meta` and resolved
`study.app_roundtrip.sleep` from its annotations. It registered the track,
recorded `hours_slept` 7.25 h with `napped` false, and read it back. A type the
store lacks was refused before any request. The first run found a client
defect: capabilities were read before the store was opened, so no Tessera
store was ever a write destination. It was fixed at `2d212732`. The opt-in
erase test on participant 2 has not been run. This is the app maintainers'
report, relayed here.

**Run again by the app, 2026-09-27, after study stores gained Subject and Source: passes, 2 of 2.**
Tessera alpha.25, voptrack-server `419eeec`:
- `GET /meta` lists all five types, and the study type's annotations read back exactly.
- Finishing the join created a "Participant" subject and a "Voptrack" source in the store, and
  registered the study's tracks with no schema write.
- A record read back naming both, inside the participant's store.
- Adding a type the store lacks made no `/meta` write.
- Erasing participant 2's store read as leaving the study. participant-2 was then replaced by a new
  participant in the same study.

Not yet exercised live: the browser half (the API join, redeeming with `return_to`, then signing in),
which needs a person at the account page. The API shapes are covered by scripted tests. This is the
app maintainers' report, relayed here.

Refresh tokens rotate, so whoever refreshes must keep the new one.
`make research-dev-token` signs both participants in again and rewrites
their tokens. `make research-dev-down` deletes everything. CI runs all three, so the
seeding cannot go stale.

## Fixture

Discover identifiers; never assume their values or ordering. `GET /.well-known/tessera` and
`GET /.well-known/tessera-content` advertise the record/content protocols. `GET /v1/datasets`
returns the dataset ID, current content store, capabilities, watermark, schema version and access.

| Type | Contents |
| --- | --- |
| `com.example.note` | One synthetic note, a text `body`, an opaque role and annotation. |
| `com.example.entry` | Subtype of note, one record with inherited body/annotations, integer `9007199254740993`, float negative zero, opaque JSON text and (when content is enabled at seeding) a three-byte `text/plain` attachment and a 12x6 PNG `picture`. |

The inherited `example.layout` annotation value is exactly
`{ "z": 1e0, "a": "\u0061" }`. Keep the raw JSON value bytes. The attachment contains three bytes,
`abc`, with content type `text/plain`. Its media value is `store//item`; neither component is fixed.
Roles and annotation keys here are test data, with no server-defined meaning.

Read `GET /v1/datasets/{d}/meta/com.example.entry`, then:

```http
POST /v1/datasets/{d}/objects/com.example.entry:query
Content-Type: application/json

{"query":{"include_versions":true}}
```

Decode `records[].values` by the corresponding `meta.fields` positions. Do not funnel integer
values through binary64. Preserve signed zero and JSON-typed string contents. A single-object read
always includes its version; query versions are opt-in. Poll dataset listing for changes rather than
querying and discarding records.

## Transaction and version round trip

Create a neutral link type:

```http
POST /v1/datasets/{d}/meta
Content-Type: application/json

{"type":{"id":"com.example.link","fields":[{"id":"parent","meta_id":"com.example.link","type":"reference"}]}}
```

Then send one transaction with a newly generated idempotency key. Retain the exact serialized request
bytes, method and target until its outcome is known:

```http
POST /v1/datasets/{d}:transact
Idempotency-Key: <fresh UUID>
Content-Type: application/json

{"operations":[{"op":"insert","type":"com.example.note","fields":{"body":{"type":"text","value":"Parent"}},"as":"parent"},{"op":"insert","type":"com.example.link","fields":{"parent":{"type":"reference","value":"$parent"}}}]}
```

Read identities from `results[].objects`. Read the parent using
`GET /v1/datasets/{d}/objects/com.example.note/{id}`. Send an `expect` operation with that record's
`version`, followed by an `update` of its body in the same transaction. The transaction succeeds;
submitting the stale expectation as a new attempt fails with 409. A version mismatch means re-read.

When a response is lost, ask `POST /v1/datasets/{d}/operations/{key}:status`. A committed response
carries the retained result when current authority permits it. An exact replay with the same key must
not create another record. Different request bytes with that key must conflict. Exercise recovery
across a server restart, too. Do not interpret `unknown` as proof that a request cannot still commit.

## Optional reads

**Since Tessera v0.1.0-alpha.6, `aggregation`, `distinct_references` and `text_matching` are
required operations rather than capabilities.** No deployment can withhold them, `optional_reads`
naming one is refused at startup, and a client checking `capabilities` for any of the three should
delete the check rather than invert it. They were optional on the assumption a server might find
them expensive, which the contract already answers for every other required operation: declare a
bound and fail past it explicitly. What settled it is that a client compensating for their absence
does not get an equivalent answer — filtering after reading returns a narrowed result set that looks
exactly like a complete one.

Capabilities that *are* optional are still read from the list rather than assumed, and a withheld
one answers `403 not_supported`, never a partial result. `locales` is not advertised and is
refused.

Three aggregation details settled at **alpha.7** and **alpha.8**, none of which changes an existing
answer:

- **`group_by` may name a reference field**, so a summary per subject is one request rather than one
  per subject. The group is the bare qualified identity that distinct references already returns, and
  a server compares the **whole triple** — two objects agreeing on `object_id` under different
  `meta_id`s are different objects. Every other field type is still refused with
  `400 invalid_field`, timestamps included: a time bucket needs a timezone, a day boundary, daylight
  saving and a week start, and a storage protocol shipping those has decided what a month is.
- **`max_aggregate_groups` is declared in a dataset's `limits`**, still 32. It was always 32 and
  never advertised, so read it rather than assuming either the number or that it is unbounded.
  Exceeding it refuses the whole request with `422 query_result_limit_exceeded`.
- **`details.maximum` names the bound that was exceeded**, accompanying `group_limit` and
  `distinct_reference_limit`, and is absent for `sum_overflow` because nothing was declared there.
  It was previously in an example and in no sentence, which left it to be modelled from observed
  behaviour — and it was modelled as always required.

```http
POST /v1/datasets/{d}/objects/com.example.note:aggregate
Content-Type: application/json

{"group_by":"body"}
```

`field` is optional and names an `integer` or `float` field; omitted, the answer is a count alone.
`group_by` names a `text` field. The fixture note has only the text `body`, so define a type with a
numeric field to exercise `sum`, `minimum` and `maximum`.
An ungrouped aggregate always answers once, even over no rows. `sum`, `minimum` and `maximum` are
`null` — not `0` — whenever `value_count` is 0, and a client that reads them as zero reports an
average of nothing. Rows whose `group_by` value is null form one group with `"group": null`; it is a
real group and counts toward the 32-group limit. Integer sums keep their exact spelling beyond 2⁵³,
so decode them the way you decode integer values, not through binary64.

`:distinct-references` takes `field` and `maximum_count` and returns `{"references": […]}`, each a
`{"dataset_id","object_id","meta_id"}` triple sorted by that qualified identity. Exceeding
`maximum_count` fails with `422 query_result_limit_exceeded`; the list is never truncated, so a short
answer is always complete. A null stored reference fails the request rather than being skipped.

`text_contains` is a literal, case-sensitive substring. Wildcards have no special meaning and it
never matches a null.

For pagination send `{"query":{},"page_size":1}` to
`POST /v1/datasets/{d}/objects/com.example.note:page`, then POST to
`/v1/datasets/{d}/pages/{cursor}:next` until `next_cursor` is null. Replay a cursor and check identical
results. Release the sequence with `DELETE /v1/datasets/{d}/pages/{cursor}`. Cursors are opaque.

## Attachment round trip

1. Read `GET /v1/stores` and its limits; use the dataset's current `content_store_id` for new imports.
2. POST `/v1/stores/{s}/imports` with
   `{"operation_id":"<fresh unique value>","content_type":"text/plain","byte_count":3,"maximum_bytes":3}`.
3. PATCH the returned absolute `url` with bytes `abc` and `Content-Range: bytes 0-2/3`.
   Treat that URL as a short-lived credential; do not log it. Replay identical bytes to check resume.
4. POST `/v1/stores/{s}/imports/{upload_id}:commit`. Preserve the returned `item_id`.
5. Write `{"type":"media","value":"<s>//<item_id>"}` into a media field in a record transaction.
6. POST `/v1/stores/{s}/items/{item_id}:resolve` with `{"representation":"original"}`, then GET the
   returned URL and compare the bytes.
7. Read `capabilities` on the store. It advertises `thumbnails`: resolve the entry's `picture` field
   with `{"representation":"thumbnail","maximum_pixel_size":4}` and GET the result. The fixture image
   is a 12x6 PNG, so that returns a 4x2 PNG — a distinct resource, never the original bytes. The
   text `attachment` in the same record cannot be rendered, so asking for its thumbnail fails
   `415 unsupported_representation`; `current` is not advertised at all and fails
   `403 not_supported`. Do not treat those two as interchangeable. `ranges` is not advertised.
8. On disposable data, POST `/v1/stores/{s}:erase` with `{"confirm":"<s>"}`. It takes no
   `Idempotency-Key`; sending one fails `400 invalid_request` and erases nothing. The dataset advertises
   a replacement store. The existing record's media value must remain unchanged; resolving against
   its old store must return absence, never redirect to the replacement.

## Executable backend gate

```sh
make integration
make check
```

`make integration` creates its own temporary data directory and HTTP listener. It checks discovery,
fixture byte fidelity, aliased references, discarding a committed response followed by restart/status,
version guards, page replay, upload replay, content-only retirement and reset. It never accesses
`.tessera-dev`. It is a Go HTTP test, **not** evidence that a Swift decoder has passed.

## Standalone authentication gate

Run `make integration-auth` for the standalone composition over trusted local TLS, including PKCE,
consent, rotation, independent revocation and authenticated-owner recovery across restart. Follow
[the Swift OAuth runbook](swift-oauth.md) for a disposable server, callbacks, credential handling and
expected client transitions. This remains backend evidence, not an independent Swift pass.

## Independent consumer status

The shared vectors are frozen by `edf30a7`, with Go `1de0bfe` and Swift `35ad924` as the independent
consumers. A pass means matching a case or recording an accepted divergence naming its cause.
Swift passes 47/47 identifier cases and 189/194 value/comparison cases; the five platform JSON-reader
divergences are documented in the [vector README](https://github.com/ekarpov/tessera/blob/main/specs/vectors/README.md). The vendored README
was updated in the Swift repository at `5e78c0f`.

Existing cases must not be edited or removed; new cases may be added. This includes preserving the
integer-to-float representability boundaries. The `$` prefix reservation belongs in server-minting
coverage, not these identifier fixtures: `$foo` remains valid to receive.

| Evidence | Status |
| --- | --- |
| Go vector consumer | Complete at `1de0bfe`; `go test ./pkg/protocol`. |
| Swift vector consumer | Complete at `35ad924`, with five documented divergences. |
| Backend HTTP scenario | `make integration`; passing implementation gate. |
| Standalone OAuth backend scenario | `make integration-auth`; passing Go TLS integration gate. |
| Swift OAuth consumer revision and test command | Pending client-side run. |
| Swift HTTP integration revision and test command | Pending client-side run. |

Run the record/content flows above through the Swift transport and decoder and record that client
revision and command separately. Frozen vectors do not certify HTTP or engine conformance. HTTP
contract readiness is separate: `f43824a` fixes schema results, watermark placement/replay,
alias grammar and receipt visibility. Schema field removal uses `confirm` equal to the type ID,
not a boolean; malformed operation members and unresolved aliases return `invalid_request` with
an operation index. Names declared with `as` match `^[A-Za-z_][A-Za-z0-9_]{0,63}$` and are insert-only.
Content edge cases, lifecycle idempotency and sharing scope remain in the review.
