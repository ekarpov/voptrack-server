# Home Console coverage

`make console-check` creates a disposable Home and drives server-rendered forms
with cookie jars and real Tessera OAuth approvals. Synthetic data only. The
owner invites Sara and Ben; members install and record two tracks, choose what
Home reads, use personal/Home panels and CSV, export their own
data, suspend/reinstate, erase and delete an account, and use connect-app links.
The private Console and Research have separate gates.

## Home UI review — 8 November 2026

Home summarizes spending and named household items without the combined technical
entry table. Navigation places that log under Browse records, alongside
activity and household-item browsing. The sidebar has one Manage Home entry;
Add to household is a separate destination. Household and track readers use the
same script-free selection form, with presets, a specific month, custom dates,
members. Search is a separate form as described below. Named household readers also offer their existing About
selector; Spending offers its category selector. Submissions redirect to canonical
URLs, discarding inactive dates and record pagination. Legacy URLs still work.

Server and backup observations are organized into a short summary and disclosures.
Unknown observations remain unknown. Detailed recovery/custody warnings and all
existing backup confirmation, reconciliation and authorization checks remain.

### Independent entry search — 8 November 2026

Search is a separate, always-visible GET form above the selection disclosure,
with a magnifying glass, Search button and explicit applied-query status.
Home, Spending, named household readers, track readers and Browse records share
it. Search preserves the applied period, member, item, category and reader
context; selection submissions retain the applied query. Clear search removes
only the query and stale record pagination/bucket selection. Reset filters keeps
the query. Search alone does not mark the date selection as Filtered.

Focused source tests exercise both forms, preset/calendar/custom dates,
independent reset URLs and query escaping. Synthetic production-template review
covers desktop and narrow phone layouts, active/no-match states and long queries.
The HTTP release runner continues to exercise the existing deployment.
This does not establish real-phone qualification, a new search index, live search
suggestions or expanded read authority. Search remains bounded to readable entries in the current view.

### Retired Home Saved views — 8 November 2026

Saved views, its library and create/open/remove routes are absent in non-Research
Console deployments. Sidebar, Home summaries, My data, track pages and panel
readers no longer offer saved-view links or save forms. Existing stored
definitions are preserved; retirement performs no deletion or migration.
Research saved dashboards retain their routes, authority and lifecycle coverage.

The Home HTTP gate asserts 404 for library/dashboard reads and save/remove POSTs
as owner, member and signed out, and checks the owner navigation and panel save
controls. The prior Home personal/household saved-dashboard lifecycle scenario
is intentionally retired; this gate no longer claims it. Existing panel totals,
CSV, private-record and session-isolation checks remain. Source tests prove
stored definitions remain byte-for-byte unchanged and qualify Research's
existing saved-dashboard lifecycle. The check covers retirement with stored definitions intact.

### Retired owner moderation surface

The owner requested removing web Stop track sharing and leaving it to the phone.
The three production routes and owner links are removed. The gate now asserts
404 for direct GET/POST requests by the owner, member and signed-out browser,
and checks that owner pages no longer link there. Member sharing/unsharing and
re-sharing still qualify Home/app access through the existing supported flow.

The previous owner moderation scenario is intentionally retired: this gate no
longer claims live owner previews, nonce replay, whole-store exceptions, stopped
notices or final-track invalidation through those removed Console forms.
Historical handler-level security tests remain until the unused legacy handlers
are removed; those handlers are not registered by the production router.
Member stopped-share notices continue to use only the member's credential and
are covered by source tests. No mutation or grant is changed by this UI removal.

### Limits

Real phones, screen-reader certification, independent linguistic review, real
domain/TLS installation and cross-client atomicity are outside this HTTP gate.
Visual review uses synthetic desktop/mobile fixtures and embedded production
templates/assets. No Tessera pin, protocol, OAuth, CSP or JavaScript changes.

## Server status and backup readiness

The owner gate checks the live version, server-reported last success,
archive-only recovery wording, authenticated encrypted attachment bytes and
no-store, member download denial and Back up now followed by refreshed status.
This gate
does not prove ciphertext authenticity, complete exports, external storage,
writer reachability or runtime configuration. Owner web moderation is retired as described above.
## Release-one dashboard presentation

Home's default signed-in route now presents shared tracks; `/mine` retains
personal dashboards. Owner administration is secondary under Manage Home.
Categories are display-only mappings of the 72 bundled app identities inspected
at iOS `b1c55f5a39c7`; unknown tracks remain in General. No metadata or access is
changed by filtering. Source tests cover category fallback/filtering, escaping,
English-only output, incomplete highlights and avoiding automatic value charts
for uninterpreted minor units/counters. Deferred translation catalogs and their
completeness checks remain. Native setup/recovery and Tessera-owned OAuth locale
behavior are outside this slice.

These are source/presentation checks, not a new deployment or real-device gate.
Screen-reader and general app measurement compatibility remain unverified. Existing member sharing and backup authority checks remain in place.

## Values, records and attachments

The next presentation slice charts exact app money values in major units,
separately by currency. Quantities stay separate by their supplied unit and,
where required, food or meter identity. Meter/valuation series show readings,
not invented consumption or sums of balances. Recent-record reads are bounded
and incomplete views are explicit. Individual records retain qualified identity
and response-field projection order. Study scopes are not accepted by these raw-record routes; Research suppression
continues to govern its aggregate panels.

Attachment links re-read the authorized parent record on every request. Both
native media fields and readable app MediaAttachment/Media metadata are supported.
Denied metadata/content is not fetched with broader owner credentials. Transfer
credentials remain inside Console; the supported local provider must resolve to
the configured internal or public Tessera origin (public native URLs use the
trusted internal route) and redirects are refused. Raster previews and
bounded original downloads are no-store. External content providers, large-file
streaming/ranges and attachment editing are not covered by this slice.

Source tests cover currency precision and separation, unit/food/meter partitioning,
projection identity, attachment revocation/tampering and refusal of foreign
transfer origins. These tests do not establish real remote-provider delivery or
app-to-Home sharing of every Media relationship.

## Period exploration and comparison slice

Home and track pages offer Today/Week/Month/Year periods and custom dates.
The primary value covers the selected interval, with previous-period differences
calculated from exact, unit-separated values. Preset comparisons use the same
local calendar cutoff; shorter-month/leap-day cutoffs are explicitly unavailable.
Custom dates compare the previous interval of equal duration. Missing values,
zero baselines and incomplete reads never produce invented percentage changes.
Averages are weighted by values, not by chart buckets.

Chart points and chart-value table links narrow the record list. Member and
case-insensitive text/decoded-note search apply before both chart aggregation and
comparison. Series selection also narrows the records. Paging and record-back
links preserve the active filters; all-records mode retains entries without
chartable units. Attachment badges count authorized media/relation links, without
resolving or downloading bytes. Badge relation reads are bounded; incomplete
metadata is marked. A linked file may remain unreadable when opened, and the
existing parent/content authorization checks still apply.

Focused source coverage includes local cutoffs/DST/leap dates, weighted values,
missing/zero comparisons, member/search intersections, chart link state, custom
dates, and attachment deduplication/qualified-subject isolation. This is bounded
Console-side search over currently readable records, not a full-text index or
attachment-content/OCR search. Research study aggregates retain their existing
suppression path. Full screen-reader qualification and large-dataset performance
remain outside the local synthetic-demo evidence.

## Remembered website sign-in

The Console entry page offers an unchecked “Keep me signed in” choice for up to
30 days. Successful authentication rotates the session identifier/CSRF token;
ordinary sessions retain the existing 12-hour idle policy and session cookie.
Remembered sessions have a fixed expiry, an opaque Secure/HttpOnly/SameSite=Lax
host cookie, and origin-specific cookie names (including different localhost
ports). Browser cookies never contain OAuth access or refresh tokens.

Only refresh credentials and minimal sign-in metadata persist, in AES-GCM
origin-bound envelopes under the private `.browser-sessions` directory. A local
key remains in that directory; protection depends on host access controls, not
resistance to a compromised host. Refresh rotation is saved before the new token
is used. Restored sessions refresh and recheck their account/authority against
Tessera before access. Logout removes the durable session; expiry and revocation
require signing in again. No transient confirmation, consent, erase, moderation
or invitation intent is restored. Existing live authorization checks remain.

Tessera database backups and portable recovery kits exclude this directory and
its key. An operator's external full-volume snapshot is outside that guarantee;
remove the browser-session directory after restoring such a snapshot to require
fresh sign-ins. Do not copy this directory into deployment images or reports.

Focused tests cover cookie attributes, session-ID rotation, fixed expiry,
unchecked behavior, restart restore, minimal encrypted state, refresh rotation
and write failure, logout replay, live revocation/account/owner checks, origin
isolation/tampering and recovery-kit exclusion. Browser restart and Console
restart were exercised locally. Browser-profile restoration across operating
systems and external snapshot recovery remain separate checks.

## Compact website sign-in

The rc.4 first-party integration and focused test coverage are documented in
[operator setup](../../deploy/website-sign-in/README.md). The existing HTTP release
gates explicitly opt out to qualify the public OAuth flow and its consent checks. They
do not by themselves qualify the optional single-form browser path or upgrade
an existing daemon.

The fallback-flow HTTP drivers submit the CSRF-protected Console introduction
with persistence unchecked before testing Tessera credential and consent forms.
This covers the existing fallback introduction; the default owned-website path
has its separate qualification below.

The synthetic Weight fixture uses a fixed decimal kilogram field so its exact
average remains a supported plain-number saved question. It does not bypass
Home's existing refusal to combine unqualified floating-point/unit-bearing
types. App declarations are unchanged.

`VOPTRACK_IMAGE=<verified-image> python3 release/console/website_signin.py`
creates a disposable Home on loopback 17753/17790 and checks the optional compact
flow with the actual image: exact website policy generated by init, default-off
checkbox even with a forged query choice, one credential submission, no consent
picker, strict CSP, checked-only restart persistence and logout. It removes only
its own disposable project. It does not prove Research navigation, real-device
behavior, production certificates or rc.2-to-rc.4 data migration.

Account-deletion return checks the exact origin-derived `__Host-` cookie name
when asserting that the Console session was cleared. The older shared localhost
cookie name is no longer the sign-in cookie.

## App catalogue v2 reader

The pinned app export now drives qualified Home read modes. Counts keep their
individual numeric record values; latest readings stay separated by subject,
unit and meter epoch. Catalogue-incompatible or unsupported app-specific
calculations keep raw history without invented totals. App-internal fields and
bookkeeping types are excluded from normal browsing/search. All history has no
previous-period comparison. No type is installed, re-annotated or reconciled.

The focused Go tests consume app vectors and verify schema-drift refusal, exact
money, latest/count behavior, unit and meter-mode partitioning, internal-field
search exclusion and All-history bounds. The normal release gate retains generic
synthetic types; it does not qualify all 72 app readers or web authoring.

## First-release named Home browsing

The everyday Home surface uses a functional heading and selected period, relevant
spending, named household items and dated activity. Quiet months retain known
shared pets/things. A separate latest-recorded section uses original dates and
declared latest reductions; it is not a period total. Add explains the app sharing
flow. Empty pet/car/house modules and routine marketing slogans are absent.

The adapter pins app dev `dd0fd9a0d3b6ed93fd9eb4f7830a5586affed65a`:
[shared subject](../../docs/contracts/app-home-v1/shared-subject-v1.md) and
[copy label](../../docs/contracts/app-home-v1/home-copy-label-v1.md).
It accepts additive fields and integer contract versions, including historical
missing/null label versions. Track catalogue v2 matching remains independent.
Identity is (member store, opaque subject_id). Names do not merge identities;
canonical record amounts remain the only input to totals. Label figures,
duplicates and orphan labels cannot add spending or events. Reads retain the
existing source authority, bounded paging and incomplete-state notices.

Focused Go coverage includes pinned example hashes, rename/same-name identity,
schema drift, additive versions, missing/conflicting/orphan labels, quiet/search
periods, canonical totals, partial inventory and refusal of private/cross-store
context reads. Detail and history paths preserve selected identity and filters.
Production-template synthetic snapshots were reviewed at desktop and 390/320px
phone widths. These static snapshots qualify presentation only.

The existing HTTP gate now checks shared/unshared activity visibility in
`/home?view=directory`, where activities remain discoverable in quiet periods.
Its original status, shared Spending presence and unshared Weight absence
assertions remain. Home intentionally no longer displays empty activity cards.
The export, session and backup gate remains; owner moderation was retired on 8 November 2026 as described above.

This does not qualify a real iPhone, full app-to-server copying, every shared
subject grant lifecycle, screen-reader certification or large household latency.
No new web writes, permissions or private app types are introduced. First-release
copies provide no photos/videos, projects, plans, savings or sync-completeness
signal; bill fields are shown as recorded without derived paid/overdue status.
The future visual-history prototype is separate. Existing authorized attachment
routes retain their own parent/content checks and do not imply app media sharing.


## Default owned-website policy

Fresh Console-enabled Home, Research and Personal deployments select branded
single-form sign-in. Existing/custom/absent policies survive an ordinary init;
explicit off keeps the consent-based fallback. Generated non-secret identity/
origin settings keep Console aligned with the actual server configuration.
See [operator controls](../../deploy/website-sign-in/README.md).

`website_defaults.py` qualifies actual-image defaults across all three variants,
private Console origins, one credential submission, unchecked remember, exact
authority ceiling, separately registered web-client consent, init reruns and a
legacy/opt-out fixture. No credential or returned record values are printed.
The existing Console/Research/private-Console HTTP gates explicitly select
`VOPTRACK_WEBSITE_SIGN_IN=off` to keep qualifying that fallback and its consent
boundaries. The shared Console recovery helper also understands compact completion
without changing its separate registered-app consent helper.

This is a prospective Voptrack deployment default, not a new Tessera protocol
contract or permission for all third-party web clients. It does not upgrade or
reconfigure existing App Review/local demos. Native app consent and study/exchange
consent remain separate. Source checks do not prove a browser trusts a locally
generated CA; the isolated user demo reuses the previously trusted Home CA.
