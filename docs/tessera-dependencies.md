# Tessera dependencies

What Voptrack Server needs from Tessera, all through its supported public interfaces. Nothing here
copies Tessera code or relies on its internals. A needed change to how a server behaves is a request
to Tessera; the smallest request and the local alternative are recorded in the table below.

## Interfaces in use

| Need | Tessera supplies |
| --- | --- |
| Sign-in for the app and the Console | OAuth with PKCE |
| Administration without holding members' data | The administration API; a sealed custodian account holds each member store |
| Joining a household or a study | Invitations, including the device-redemption grant and rebind invitations |
| Household sharing the owner can stop but not read | Group shares and `moderate_sharing` on the member issuer |
| Withdrawal that keeps what was sent | Frozen reader shares |
| A storage quota separate from the memory bound | Optional `max_stored_bytes`; `max_materialized_bytes` bounds memory per request |
| Backups | Native scheduler, acquisition API and completed-backup catalog, retrieval-only collector credentials |
| Recovery | Settings export, offline producer and settings recovery commands, the standalone acquisition verifier |
| Native browser completion and repeated-submit recovery | rc.3+ validated Return to app link and bounded Start again flow; the demo CSP override is absent |
| Compact first-party website sign-in | rc.4 operator-provisioned identity, exact HTTPS origin/callback, current-authority ceiling and code-exchange-bound remember choice; Voptrack deployment adapter is opt-in |
| Consent defaults requested by a native client | rc.4 requested-grant hints, capped by current account grants and explicit approval |
| Long-lived app/server protocol compatibility | v0.1.0 specifies highest shared numeric major, at least 12 calendar months of overlap extended while released clients need v1, and discovery deprecation; deployment acceptance remains separate |

## Open items

| Need | Smallest request | Local alternative |
| --- | --- | --- |
| iOS return after invitation account creation | Retain exact registered-return checks and strict CSP; no server fix indicated by the 2026-11-06 app-team retest | App team reports native callback, completed study setup and synthetic write/sync/read-back PASS on iOS Simulator; interrupted-join recovery and resumed scope remain pending. [Dated findings and retest](requests/tessera-native-invitation-completion.md) |
| Lost-device management from the web (list and revoke devices) | A server administrator can list and revoke a member's installations without holding the data | The owner issues a rebind invitation; revocation is a host command (`-revoke-installations`) |
| Administering device-joined members from the web | Grant the owner account-management rights over members who joined on a device | Host command line |
| Atomic confirmation of share moderation | Bind execution to the inspected share state | One-use confirmation with a recheck, disclosed to the owner |
| Share discovery beyond the known-source registry | Authorised discovery of group-only or unregistered shares | The Console's registry of known sources |
| Tessera's own pages are English only in v0.1.0 | Localisation upstream | The Console and app follow the same English-only first release |

## Moving the pin

First apply the [App Review freeze and app compatibility policy](requests/server-compatibility.md).
Changing a source pin does not authorize upgrading an app-facing server.

`make upgrade VERSION=vX.Y.Z`. Read Tessera's release notes and spec/vector diffs first; assess behavior
changes even within protocol v1. Migrations are forward-only. Back up before and after.

## Native OAuth browser qualification

Tessera v0.1.0-rc.2 sends `form-action 'self'` on its identity pages. The app
team reported that WebKit blocked the post-Allow redirect to its registered
`com.voprak.voptrack:/oauth/callback` with “form action not allowed by CSP”.
Earlier HTTP OAuth gates did not exercise this browser enforcement.

The temporary demo ingress adjustment uses Caddy's response-header replacement
only on `/oauth/authorize`. Live checks on both demo variants confirmed that the
known app scheme was added and every other CSP directive was preserved. Discovery
was unchanged; verification consumed no invitations and did not sign in accounts.
This is deployment evidence, not WebKit or real-device acceptance. The general
identity-page behavior belongs in Tessera; no upstream implementation is changed
or copied here. Remove the workaround after adopting and qualifying an upstream
fix, and retain browser coverage for consent completion.

### App-team receipt, 2026-11-04 evening

The app team reported that Home sign-in completed in its Debug iOS simulator
after the ingress adjustment: the native callback reached the app and the
connection status became Connected. This is consumer-reported Debug simulator
evidence, separate from server-side HTTP checks. It does not establish physical
device or Release review-candidate acceptance.

The same report reproduced an expired-page dead end after tapping Allow twice.
Expiry itself should remain enforced; the upstream request is recoverable
navigation or a securely bounded retry, not removal of expiry. Starting again
from the app remains the local recovery procedure.

Release review-candidate checks remain pending for synthetic record display,
Research sign-in, fresh invitation joining, and leave/erase using a separate
destructive-test account. Review accounts and reserved invitations must not be
used for destructive tests. No account identifiers, credentials, invitation
material or private infrastructure addresses are recorded here.


## Compact first-party website sign-in — rc.4, 2026-11-06

The pinned rc.4 now supports an operator-provisioned first-party Console, exact
origin/callback binding, current-authority ceilings and a code-exchange-only
remember choice. This is available upstream; it is no longer an upstream feature
request. Voptrack deployment is opt-in. Qualification is recorded with the
implementation. See
[compact website sign-in](../deploy/website-sign-in/README.md). Older rc.2
findings below describe the historical baseline.

## Historical website sign-in review — rc.2, 2026-11-05

The owner requested a website-quality login and a remember-me option. Inspection
of the demo pin (Tessera v0.1.0-rc.2) confirms its identity pages own the credential
and consent forms, with embedded CSS covered by the page CSP hash. No supported
branding hook was found in the inspected pin. Actual identity-page styling and
website-specific authorization presentation therefore require the small upstream
interfaces above; no upstream files were edited or request sent.

Console session policy is a separate local concern. Its current opaque Secure,
HttpOnly, SameSite=Lax cookie has no persistent expiry, its authenticated session
expires after 12 hours idle, and all session/token state is in memory. Restarting
Console invalidates every sign-in. Extending only the browser cookie would not
provide reliable remembered sign-in.

Recommended local slice: an unchecked “Keep me signed in on this device” choice,
with a proposed 30-day maximum subject to Tessera token expiry/revocation; opaque
browser cookies; protected durable refresh-token/session state; and logout,
expiry, credential refresh, account suspension and restart coverage. Retain
current live authority checks and require normal reauthorization when grants
expire or are revoked. Persistent login state must be excluded from portable
backups/restores, and transient confirmation/erase/moderation intents must never
be restored. The local Home/Research demos also need separate cookie namespaces
because browser cookies are scoped by host/path, not localhost port. The [Console guide](../release/console/README.md) describes the implementation
and validation limits. The branded
identity-page request is ready in [the team request](requests/tessera-branded-web-login.md),
prepared for owner review and not sent.

## Common household Tracks

Updated 5 November 2026.

Proposed first web authoring release records only to compatible **app-installed**
types in an explicitly bound common dataset. The local dashboard review remains
read-only; a common dataset and its authoring flows are not yet implemented or
qualified. See the [app-team reply](requests/app-team-common-track-catalogue.md)
and [Home contract plan](home-web-app-contract-2026-11-05.md).

| Need | Minimum guarantee to qualify/request if absent | Local alternative and scope |
| --- | --- | --- |
| A household-owned dataset with writable and read-only members | Supported owner-authorized dataset provisioning and membership grants; clarify record delete versus dataset erase and membership revocation | Owner-managed explicit per-person grants where supported; no assumed group-write feature, no service/owner token fallback for member writes |
| App and web discover the same common dataset | Authenticated binding between a Home identity and its exact dataset ID | Operator-configured explicit binding validated against the person's accessible datasets; no first-dataset or name inference |
| Complete common Subject/Track erasure | Supported dependency removal order, authority and failure/retry guarantees sufficient to leave no object-specific metadata | Keep deletion disabled until qualified; do not report a partial graph cleanup as success |
| Future idempotent catalogue installation under a race | Create/conflict/read-back behavior sufficient to verify the complete declaration and ancestors without mutation | First version has no web installer; app installs types. Use supported operations later only after race qualification |

These are unqualified integration requirements, not claims that Tessera lacks the
interfaces. Inspect and exercise supported public interfaces before requesting
upstream additions. No request has been sent automatically. The Home reading slice
now retains opaque JSON annotations, field ownership and immutability. Complete
catalogue byte/hash verification and missing app-library semantics remain **local
integration work**, not Tessera storage limitations. No new annotation keys, schema
variants or protocol behavior are introduced by this plan.
### rc.3 consumer qualification

The rc.3 qualification selected published Tessera `v0.1.0-rc.3` at
`55827d2e55b5533d106f56f1c48ae1e90693cca0`. Its browser response to successful
consent is a Return to app link; duplicate submissions can offer Start again
with fresh sign-in and consent. Non-HTML callers retain the existing redirect.
The candidate adds no migrations, dependencies or configuration.

Upstream reports HTTP/unit and real WebKit coverage separately from pending iOS
app dispatch. Voptrack must qualify the image and completion/recovery with the
edge override disabled. Keep the rc.2 rollback image, backup and edge settings.
Simulator acceptance of rc.3 must not be inferred from earlier rc.2 workaround
success; permanent workaround removal remains conditional on that acceptance.


### rc.4 consumer qualification

The pin selects published Tessera `v0.1.0-rc.4` at
`5691f26bf50f52b625b18fb2b9e2fb9959b59d3b`. The optional OAuth `scope`
parameter is now a requested-grant **UI hint**, not authority. Apps should send
`scope=read write` for study recording, preserve it after invitation account
creation and recovery, and check dataset `access.write` before installation.
No hints still selects only held Read and Insert; it does not imply Write.
Schema, Erase, administration, creation and shared reads remain explicit.

The Console requests Read/Write hints and `prompt=consent` to retain its
per-login dataset and administration choices. Automated drivers that submit
specific checkbox choices also force review. Native clients may use rc.4's
bounded consent reuse; they must accept either completion or a review page.
No first-party Console policy or identity branding is enabled by this upgrade.

The registry advances to format 5. Rollback requires restoring the pre-upgrade
snapshot and recovery kit; an rc.3 binary must never open storage written by
rc.4. The demo ingress CSP workaround was already removed during rc.3
qualification and must remain absent.

A study invitation stays single-use after its join request is accepted. After an
account exists, the app must reauthorize that same participant and resume the
pending study installation, preserving the study/account context. It must not
POST join again or use plain storage connection as a replacement study join.
If needed, the participant uses their saved password or the server's recovery
code flow. App-side resume UI and actual device acceptance remain separate
from server and HTTP evidence.

The app team reported Release Home sign-in, Weight creation/write/read-back,
Research sign-in and invitation-to-app dispatch on rc.3. They also reported
that declining Save Password allowed the /joined return. These receipts do not
qualify rc.4's native dispatch, permission selection or leave/erase.


The first clean Linux rc.4 image exposed a deployment packaging defect:
`voptrack-init` inherited a non-executable source mode. The Containerfile now
sets its mode explicitly, so startup does not depend on Windows or archive file
metadata. The failed image was not deployed; qualification is repeated for the
corrected image.

### Operator identity branding on rc.4 (2026-11-05)

The documented `identity_brand` interface supports the operator name, one local
validated PNG, and accent/surface/text palettes. Voptrack's opt-in deployment
adapter and recovery-kit input use that interface; no Tessera implementation is
copied. Native clients retain the Return to app completion and strict CSP.

Separate light/dark logos, card colors, accent-text colors and font overrides
are unavailable. These are optional upstream enhancements, not App Review
blockers. The local alternative is the owner-approved light mark on white,
default system typography, the supplied accessible light accent-text as the
single accent, and existing derived card styles. No web-client IDs are allowed
by the demo branding configuration. See `deploy/identity-brand/README.md`.

## Home life-tracking read capabilities

The shared_subject and home_copy_label contracts define stable identity and
label binding. The recorded app development revision is
`dd0fd9a0d3b6ed93fd9eb4f7830a5586affed65a` under
[app Home v1 contracts](contracts/app-home-v1/shared-subject-v1.md), with
[copy-label rules](contracts/app-home-v1/home-copy-label-v1.md) and byte hashes.
This verifies dev source availability, not a released app or phone interoperability.
The read-only Console adapter now uses these exact contracts; source tests cover
store-scoped identity, additive evolution, canonical figures, missing/duplicate
labels and access boundaries. See the [Console guide](../release/console/README.md).
These are resolved app integration contracts, not requests for Tessera changes. Media, projects, plans, savings and sync status are explicitly
outside first-release sharing; retain the historical table below as audit evidence,
not an instruction to build against private types or request those features now.
Accept additive subject/label contract versions and unknown optional fields;
preserve separate exact catalogue validation and current authority checks.

The table records integration requirements and local alternatives. The
[app catalogue](../catalogue/README.md) explains which exports are pinned.

| Need | Minimum contract to verify/request | Local alternative |
| --- | --- | --- |
| Stable named household inventory | Readable qualified object identity and record binding, name/kind payload version, rename/removal/revocation rules; explicit shared discovery coverage | Independent bounded Subject reads when granted; contextual event labels otherwise; never merge by name or claim recent scans are complete |
| Consume copied event labels | Exact HomeCopyLabel declaration/version and vectors, copied-record binding and lifecycle; label text is not stable pet identity | Later exact-ID metadata adapter; keep canonical record figures and preserve unknown custom types; no new grants or catalogue-v2 edits |
| Visual history from iPhone copies | Explicit permission-bound copying/sharing of Media, relations and content, dates/captions, updates/deletions and cross-store policy | Existing authorized shared attachments only; phone-copy media is omitted by the inspected copy path; never read the private phone source |
| Compact previews and video | Supported thumbnail/poster discovery and bounded authorized content/range behavior; browser/CSP qualification | Current PNG/JPEG/GIF/WebP original-byte previews and bounded downloads; no inferred app-internal poster IDs or promise of streaming |
| Projects and goals | Exact organization/plan/registration declarations, payload versions and semantic vectors, readable bindings and explicit sharing | Dated supported histories without invented grouping/progress; the v2 manifest lists many organization/media types as internal, not exported declarations |
| Upcoming care and bills | Explicit due/status/completion and occurrence/calendar rules with readable plan inputs | Display recorded facts/dates without guessed overdue, paid, complete or savings states |

Stable identity, media copying and project/plan sharing are primarily app
integration questions. Local Console discovery, diagnostics and presentation can
use existing interfaces where authorized. Do not contact teams automatically,
introduce broader readers or treat an installed type as a shared permission.

## First-party website completion document

Observed on pinned Tessera v0.1.0-rc.4, 7 November 2026. Browser HTML sign-in
finishes with a zero-delay refresh document and fallback return link, rather
than an unconditional HTTP 303. The visible return page can briefly appear in
the browser.

Minimum upstream capability: a supported way for an operator-owned, validated
same-origin website callback to complete without rendering a visible return
document, retaining exact callback validation, state/PKCE, console-session origin
binding, CSP and safe cross-origin/native fallbacks. This is a request, not a
claim about shipped behavior. No request has been sent.

Local alternative: retain the supported zero-delay document and fix native local
networking so navigation is fast. Do not strip Accept headers, rewrite OAuth HTML
or relax CSP to force API-only redirects. No Tessera pin change is authorized by
this finding; frozen servers and the app-team compatibility process still apply.

## v0.1.0 consumer qualification — 2026-11-08

The source pin selects published Tessera `v0.1.0`, commit
`6367a78c8ee89b97acbb14c8680b22d961bac8fb`. The annotated tag and published
release were initially verified through authenticated GitHub access. Tessera is
now public; anonymous GitHub, Go module proxy and checksum lookup were verified
during server delivery preparation. The [published release entry](https://github.com/ekarpov/tessera/releases/tag/v0.1.0)
records publication; the tagged source release notes intentionally retain their
pre-publication wording.

Compared with rc.4, record protocol v1 gains explicit numeric-major negotiation,
overlap and deprecation contracts. Existing vectors are byte-identical;
`version-selection.json` adds 17 cases and `version-discovery.json` adds 21.
Restore-time identity reconciliation revokes archived credentials for absent
datasets and disables affected fixed-policy issuers; unaffected rights remain.
There is no new storage migration. Final requires fresh storage: pre-release
databases, identity registries and content directories are unsupported inputs.
Do not treat this pin as an rc.4 in-place upgrade path.

The upstream release reports backend, integration, fault, browser and native
operator qualification with stated limits. Its iOS/walk/device and first-party
Console-client waivers are unrun cases, not Voptrack acceptance. Voptrack's
`cmd/verify` imports upstream `CheckCore`, which now negotiates and validates
discovery metadata; Console uses a configured local v1 endpoint and does not
implement a general remote-server version selector. No client negotiation or
real-device acceptance is inferred from a dependency update.

See the [first server release preparation report](releases/v0.1.0.md)
for Voptrack qualification and remaining stops. No deployment, app-team notice,
test-invitation delivery or demo change is authorized by the source pin.
