# Voptrack Research

A server for studies. A researcher describes a study and designs what
participants record. People read the study, consent, and join under a pseudonym
they never have to invent. They record with the Voptrack app, and the researcher
monitors collection counts, recorded-date activity and participant status.
Authorized CSV/JSON downloads provide study values for analysis outside the
website. Everything is done from the Console or the app.

**Status: development.** No image is published yet. `make research-check`
exercises a disposable local deployment; public-host and device qualification
remain separate.

## Quick guide

1. [Install Research](../../docs/installation.md) with a public HTTPS Console.
   For current source, use [development](../../docs/development.md).
2. Sign in as **owner**, the researcher. Fresh installations use Tessera's
   single owned-website form and open **Studies**; a separate OAuth scope picker
   is not expected. Ordinary app OAuth and study consent remain separate.
3. [Create a study](#create-a-study), define collections and complete participant information.
4. Publish consent, then explicitly open the study for recruitment.
5. Monitor collection and participant status; export CSV or JSON for analysis.
   The retained analytical CSV endpoint still requires a suppression policy,
   but technical collection monitoring does not.

## Set it up

Use the installation guide's persistent settings and matching image/Compose
revision. Research needs a public Console because people read studies and join
there. Product and Console mode are fixed at first start. See
[deployment options](../README.md#choose-how-to-install) for other shapes.

## Create a study

On **Studies**, choose **Create study** and give it a title and an address:
`sleep-mood` becomes `https://study.example.org/study/sleep-mood`. Then follow the saved setup stages: Basics, Collection, Information and consent,
Recruitment and Review. Save draft stays on the stage; Save and go back/continue
saves first. Progress links show only saved values. Drafts resume from Studies.
Complete what a person reads before joining:
- what the study is for;
- what it collects;
- who sees it;
- how to leave;
- the consent text they agree to.

A study cannot open until all of these are written.

**Consent is versioned.** Researchers mark material changes; editorial wording
edits remain covered by the preceding material agreement. Participants can
renew agreement through My studies in the Console or the API while the study is open. Covered current
versions and retries write nothing. **Withdrawing keeps what was sent and shows
the study nothing new** (a frozen share); agreeing again shows it everything.
The gate checks both kinds of edits, stale and fresh agreement, idempotent
retries, withdrawal (the researcher keeps 6 hours and does not see the 7.5
recorded afterwards), rejoining (both again), a read-only sign-in refused,
Remove my data then rejoin refused, spent versus unknown codes, closed-study
answers including `410 study_closed` with `closed_at`, and export flags for an
agreement gap.

Setup saves unpublished text privately. On Review, **Publish consent** creates a
new immutable version; **Open study** is a separate explicit action. Changed draft
text needs another explicit publication before opening. The existing editor for
open/closed studies still publishes changed consent on Save. Each version keeps
the old one exactly as it was. Each participant's record points at the words they
agreed to, not at whatever the text says today. A consent record cannot be changed once written, by
anyone: who agreed, to which version and when are fixed by the server itself.

**Joining** is either **open** (anyone with the address) or **by invitation**.
For invitation-only studies, **Participants** makes single-use links. The invitation page also offers
**Join with the Voptrack app**, carrying the same study and invitation code.
Opening or reloading that page does not consume the invitation. The app shows
consent and joins through the study API; using the separate web form creates a
web account instead. The app must register the `voptrack://` URL scheme.


## Design the tracks

In saved setup use **Collection**; for an existing study open **Study settings →
Collection definitions**. The page is titled **Collections**. Build what
participants record the way the Voptrack app's track composer does:
- a track has a name and values;
- each value is a number, a whole number, text, yes/no, or a date and time;
- a number can carry a unit, and say whether it adds up to a total or is a
  reading.

You can also add tracks from Voptrack's catalogue, such as Weight, used exactly
as the app defines them. You never write a schema. **Show the schema** is there
if you want to see what the app will receive.

When a participant records, the app dates it. Every study track is dated by
when the thing happened. Collection activity counts use that recorded date,
not server upload time or last app use.

**Tracks are fixed once the study opens.** Every participant's store is made
with them.

## Participants

A person reads the study page and chooses **Join**, then agrees to the current
consent text. Next they choose a password on the server's own page. Their account
name is a **pseudonym the server chooses**, like `p-7Qkylwqm`, so nobody can
type their real name by mistake. They are shown a **recovery code** once; tell
them to keep it, because nobody can reset a participant's password.

They connect the Voptrack app from **Connect the app**, and find exactly the
study's tracks. They can record into them and cannot create others.

On **Participants**, the researcher sees each pseudonym, the consent version
they agreed to, when they joined, and their status: taking part, suspended,
withdrawn or account deleted.

**Suspend** refuses every device of theirs at once and changes nothing in their
store; **Reinstate** undoes it.

## Collection monitoring and exports

The researcher lands on Studies. Opening a live study shows Overview,
Participants, Exports and Study settings. Drafts resume their saved setup.
Overview shows distinct recorded enrolments and lifecycle states, period-filtered
record/participant counts, recorded-date activity and collection coverage.
Search is separate from period, status, activity and collection filters. Counts
and dates are operational metadata; no recorded measurement values are queried
for or displayed in these pages. Latest recorded event is not upload, sync,
last sign-in or app-use time. There is no implied expected schedule or completion
percentage.

Participant history is deduplicated by study pseudonym. Failed discovery,
missing readability, count errors, deadlines and bounded trend reads are visible
as unavailable/incomplete information, rather than confirmed zero activity.
Participant pages without an activity filter query at most 20 displayed people;
activity filtering and overview use a bounded concurrent scan. Counts and
trends are not an atomic cross-store snapshot. Future or invalid latest dates
are labelled in technical details.

Exports provides deliberate Download CSV and Download JSON actions without
previewing raw values. CSV keeps one value per row; JSON groups participant and
track records and includes published consent. Pseudonyms do not make downloads
anonymous. Withdrawal can leave pre-withdrawal records readable; erasure is a
separate operation. The existing `consent_missing` flag uses event timestamps.
Discovery/unknown participant-state failures now refuse an export instead of
silently omitting sources. Files are served only after private generation;
busy, incomplete-read and size-limit responses explain that no file was served.
Formats are unchanged; export-in-parts and background jobs are not provided.

Results, Explore and saved analytical dashboards are outside this first-release
browser UI. Direct `/panel` and saved-dashboard UI routes return 404. Definitions
remain in storage. Suppressed analytical `/panel.csv` retains its existing policy
contract and explicit researcher authority; it is not an HTML result-page bypass.
The policy engine and historical definition/renderer lifecycle remain source-tested.

### Collection UI qualification and limits

The Research HTTP gate checks the technical-only overview, exact operational
counts, distinct enrolments, participant technical counts, dedicated export
controls, retired value-page routes, researcher/participant/signed-out boundaries
and existing raw CSV/JSON formats. Existing setup, immutable consent, renewal,
withdrawal/rejoin, suspension, erasure, pseudonymous accounts, policy validation,
analytical CSV suppression/precision, backups, lost-Console-state recovery and
full archive restore scenarios remain.

The previous HTML analytics and saved-dashboard create/reopen/remove UI assertions
are intentionally retired. Reconstructed overview and private draft/consent/policy
state are checked after restore; this runner no longer claims newly created saved
boards reopen through a retired UI. Engine-level source tests retain the old
suppression and definition security checks. Monitoring source tests verify only
count/timestamp projection requests, deduplication, independent controls,
unavailable discovery and export refusal. Synthetic desktop/mobile production
renders complement actual-image HTTP qualification.

This does not establish real-phone interoperability, large-study latency,
anonymity, a server-receipt/sync signal, completion of a study schedule or a
common snapshot. The per-participant/collection record limit and file-size limit
remain enforced; no resumable or partial export UI is claimed. Existing API and
participant-rights contracts and Tessera pin are unchanged. Compiled gate proof
and final evidence are recorded in the implementation review.

## Who can see what

| | A participant's store | Other participants' stores | The study dashboard |
| --- | --- | --- | --- |
| That participant | everything | **nothing** | **nothing** |
| The researcher | authorized study exports and technical monitoring, read only | the same within the study | technical collection statistics |

The researcher reads through a separate reader account that each participant's
store is made with. It can read, never write.

**Said plainly: whoever runs the machine can read the database files.**

## Leaving

A participant can first **download their data**: every record in their store,
as JSON.

A participant signs in to the Console and chooses **Remove my data**. They tick
**Erase this dataset** on the server's own screen, see what will be destroyed,
and confirm. Their participant list entry becomes **withdrawn**, dated. Erasing
from the app has the same effect, undated.

**Delete my account**, beside it, happens on the server's own page. Their store
goes with the account.

Erased data stays inside the server's backup archives until they are replaced
(a week at the default schedule), and in any copy of an archive kept elsewhere.
Restoring a backup does not bring it back. Say which applies in the study's
"how to leave" text: erasing cannot recall an export already taken.

## Backups

The server backs itself up while running, at start and every 24 hours, keeping
seven. **Backups**, in the Console, lists them and has **Back up now**. A restore
needs three things: an archive, the backup key, and the retirement fence. Copy
them off this machine, and not to the same place. See
[the release README](../README.md#backups).

Studies, consent records, withdrawals and saved dashboards are kept in a store
of the Console's own on the server, which nobody else holds, so they are in
every archive. The Console's volume holds only what init writes once at
install: the services' secrets and that store's identifier. **Copy that volume
once, after the first start**; it does not change afterwards. Nobody can replace
those secrets from the web, by design: one of them reads every participant's
store. After a restore, the Console reads its store as it starts; sign in once
as the researcher so each study's invitation secret, which is not kept, is
replaced.

## Participant consent UI coverage

The disposable gate also drives the HTML consent routes with participants’ own
OAuth approvals: signed-out return navigation, an unrelated researcher account,
forged CSRF on all actions, read-only refusal, account-bound write approval,
no agreement on OAuth return, explicit confirmation, stale consent text,
material versus editorial changes, repeated renewal, withdrawal retaining old
records and showing later edits, rejoining disclosure before confirmation and
visibility of intervening records, erasure refusal and the new-participant option,
and closed-study status, refused renewal and permitted withdrawal. Unit tests
cover session and pending OAuth expiry, cancellation through its public landing
page (no automatic sign-in retry), failed membership lookups versus confirmed
nonmembership, escaping and all
seven translation sets. The JSON API checks and immutable-history checks remain.

The write-approval check is proved with a compiling planted Console defect that
bypasses the HTML write guard. The newly built image must fail at the read-only
renewal assertion. The source and image are restored afterward. Two review regression checks were
also proved by separately restoring the original cancellation redirect and the
silent lookup-discard loop: each planted Console compiled and its matching unit
test failed at the intended assertion. Both sources and proof binaries were
restored/removed before the fixed build.

## Suppression coverage

The Research gate now proves explicit policy configuration, threshold validation
and retained invalid input, forged-CSRF/participant refusal, stale updates,
confirmed lowering, closed-study changes, two participants with three records
below K=3, release at K=2, weighted averages, filtered days, monthly sums and
consistent panel CSV precision/status. Participant labels and small latest-consent
cohorts stay hidden; an exact distribution with a small category is wholly
withheld. Existing raw CSV/JSON, immutable consent/withdrawal, suspension,
erasure, rejoining and JSON API checks remain. Policy K/revision survive Console
file loss and state-store reconstruction.

Unit tests additionally cover all measure/group boundaries, repeated/prolific
contributors, null/zero/negative values, DST/filter semantics, failed discovery,
catalog, later pages and actual row truncation, unsupported distributions,
scrubbed chart geometry/accessible descriptions and CSV, failed policy saves and
file/remote policy reconstruction. These fault cases are synthetic HTTP tests,
not planted Tessera faults in the disposable deployment.

## Historical saved study dashboard coverage

Before the collection-first UI, the Research gate created a board before policy existed and verified duplicate-save,
append, overview discovery, reopen, inclusive To-date links and current-policy
suppression/release, then removes panels including the final panel. Repeated
removal with an old board version cannot delete the panel that shifted into its
index. Direct unauthorized list/read/add/remove routes, forged scope and CSRF
are checked. The raw-export, study JSON API and consent lifecycle checks remain.

That former HTTP scenario reopened definitions after Console file loss/restart and after exporting the
existing recovery kit and encrypted archive, destroying the disposable volumes,
restoring onto fresh volumes and starting the deployment. Policy and consent
reconstruction are checked in that restored deployment as well. This exercises
existing recovery operations; it adds no recovery implementation.

Unit HTTP tests add separate-study isolation, wrong variant, signed-out,
participant and revoked researcher authority, forged submitted/stored queries,
policy raised/lowered after saving, HTML/SVG/accessible-text and CSV release
safety, failed discovery versus removed fields, failed writes (including removal),
corrupt documents, retained form values and DST date-window round trips.
Synthetic rendering covers mixed release states, missing policy, unavailable,
unsupported, empty and validation/storage-error pages in all seven languages.

## Guided setup coverage

The Research gate now drives creation, every saved stage, Back/Next, resuming,
field preservation, retained stale input, forged CSRF, private drafts, no consent
publication on navigation or repeated review GET, explicit publication and
identical retry, explicit opening and invitation-only completion links. It leaves
an unfinished study with unpublished consent, reconstructs it after Console file
loss/restart, then carries it through the existing encrypted full archive restore
onto fresh volumes. It resumes, publishes and opens that restored draft.
Existing consent, suppression, saved dashboards, exports and close/reopen checks
remain in the same gate.

Console tests add invalid/duplicate addresses and retained creation values,
invalid title/organiser/join mode, incomplete designs, stage ownership, immutable
legacy consent, editorial publication, changed unpublished text blocking opening,
stale publication/opening, changes through the actual legacy editor/designer HTTP
routes, missing-revision refusal, failed read/write/publication, missing versus
unreadable documents, all route authorization/CSRF cases and private API content.
Concurrent track save/first opening and lost issuer responses, secret-file,
issuer-document and final-opening write failures are synthetic HTTP/storage
fault tests. They prove safe retries and retained track locks; they do not claim
fault injection into Tessera itself.

The legacy full draft form also saves unpublished consent atomically and carries
a revision. Its first-open request reaches Review without publishing or opening.
Open/closed study editing keeps the existing consent publication and close/reopen
semantics. Tests cover failed/stale legacy draft saves with retained text and
unchanged pre-existing published history. A failed durable opening-intent write
is checked to issue no external creation request.

Visual review used credential-free synthetic pages: eleven states (six stages,
incomplete readiness, stale conflict, validation, storage and opening recovery),
English/German/French/Spanish/Portuguese/Finnish/Russian, light/dark, and 375px
and 1440px viewports: 308 settled browser combinations. DOM geometry, labels,
error references and setup button/progress-link targets (at least 44px) passed.
Representative stage/error/completion screenshots were inspected in every
language; keyboard Tab focus, the existing responsive designer and long text
were reviewed. This is browser emulation, not a real-device OAuth run,
screen-reader session, independent linguistic review or ethics review.

Issuer recovery uses existing issuer listing/secret rotation with
Console-owned durable intent. An ID already present without that intent is
refused and requires operator help: the listing does not expose full schemas.
Recovery assumes the operator has not independently replaced that issuer.

Native WSL Docker: Ubuntu 22.04.5 LTS, `/var/lib/docker`. Disposable proxy
subnets/pools/addresses used 10.117.60 (Research), .61 (Home) and .62 (private);
Home Console port 9446 and private HTTP port 7681 avoided occupied host ports.
An initial build hit the unavailable Windows credential helper; builds then used
an explicitly empty temporary Docker client configuration without changing user
credentials/settings. An initial private run used the wrong override variable;
the successful run used `VOPTRACK_PRIVATE_HTTP_PORT=7681`. Preview serving was
temporary and stopped after review. Shared-host CI remains waived for this UI
branch; release and pin CI rules are unchanged. No commit or push.

Changed files for this slice:

- `cmd/console/setup.go`, `setup_issuer.go`, `setup_test.go`: private draft
  transitions, revision checks, issuer recovery, HTTP/fault/concurrency tests.
- `cmd/console/main.go`, `studies.go`, `research.go`: route integration,
  serialized definition mutations, atomic legacy draft saves and secret recovery.
- `cmd/console/presentation.go`, `presentation_test.go`, `i18n.go`: safe error
  presentation, shared template parsing and all seven translation sets.
- `cmd/console/templates/study_setup.html`, `design_fields.html`,
  `study_tracks.html`, `study_edit.html`, `studies.html`: staged UI, shared
  existing designer fields, resume links and guarded legacy draft editing.
- `cmd/console/static/console.css`: progress/action targets, long text and
  responsive designer presentation.
- `release/research/drive.py`, `reopen.py`, `verify.sh`, `README.md`:
  actual-image journey/restoration checks.

Stale forms: every legacy study-editor POST now checks the
submitted revision before choosing draft versus published-study behavior. A
stale draft tab submitted after opening returns 409 with its original token and
entered text; it cannot publish consent or overwrite the opened study. A failed
initial study read stops the request instead of falling through to the published
save path. Legacy designer forms also carry revisions, checked under both store
locks; stale or missing tokens retain the submitted design and require reload.
Fresh published-study edits and the existing opened-track lock remain supported.

Regression HTTP tests cover both review reproductions, missing tokens, retained
input/reload links, successful fresh submissions, and a transient initial-read
failure. Temporary Go overlays restored each defect separately: both compiled
with `go build`, and the targeted tests failed at their expected HTTP-status
assertions (303/200 instead of 409, and 303 instead of 503 for the read failure).
The overlays and proof binaries were removed; repository source and container
images were never replaced by these planted variants. The deployment gate now
submits the revision rendered by each editable form; its existing assertions
and restoration scope are unchanged.

The Research development seeder follows the same guided setup stages, submits
fresh revisions for each save, and separately publishes consent and opens the
study before joining its two synthetic app participants. The `research-dev` CI
step checks their installed track types, distinct stores and refreshed tokens.

## What this has not been shown to do

- **Participant browser interaction and real-phone interoperability.** The gate
  uses HTTP cookie jars, not a real phone or browser. The 8 November collection UI
  review additionally qualified researcher sign-in and navigation on a live local
  synthetic demo, plus desktop/mobile layout. Participant browser access on that
  custom website remains outside this review; see the implementation evidence.
- **Share status with read-only approval.** Consent GET does not identify erasure.
  The existing owner-readers route requires Write approval, so the UI can identify
  a missing analysis share after that approval. Read-only viewers see the erasure
  explanation conditionally until they approve Write. No permission is assumed.
- **Run a real study.** The gate runs on `localhost` with a certificate Caddy
  issues to itself, and synthetic participants.
- **Server write time in export flags.** Tessera alpha.28 exposes no write
  timestamp in queries; `consent_missing` uses the participant-supplied Track
  timestamp. It cannot prove when a write reached the server.
- **Stronger disclosure control.** No cross-request subtraction protection,
  contribution dominance limit, shared-dashboard release mechanism or common
  cross-store snapshot. Suppression does not restrict raw access or guarantee
  anonymity. Fixed distribution bins and historical cohorts are deferred.
- **A full file-to-store migration scenario.** State-store reconstruction and
  archive restoration include policy and private setup state within existing
  study documents. Historical runs also created and reopened saved dashboard
  definitions; the collection-first runner no longer exposes that browser UI.
  Dedicated deployment migration from legacy files remains unit-covered by
  the existing document namespace mechanism, rather than a separate gate.
- **An ethics review.** Nothing here has been reviewed by one. The consent
  wording, what "leave" means for data already analysed, and how long data is
  kept are the study's to decide and to write down.
- **The app's half.** The app records into study tracks against the same
  contract the designer follows, and is being tested against this server. Its
  maintainers are finishing it.

## Invitation landing-page regression coverage

Console route tests check the app link for valid invitations and open studies,
exact study/code propagation, escaped names and local-CA pins, repeated GET
without code consumption or consent writes, and refusal for unknown/spent codes
and closed/draft studies. These are HTTP/rendering checks; they do not prove
OS URL-scheme dispatch or the complete iOS study-join journey.


### rc.4 sign-in and interrupted setup

For app recording, retain `scope=read write` in the normal PKCE authorization
request after creating an invitation account. These are checkbox hints; inspect
the resulting dataset access before writing. Read-only approval is valid and
must be handled as insufficient access, without redeeming another invitation.
No Schema or whole-dataset Erase approval is needed for ordinary study records.

The study invitation and account-creation invitation remain single-use. If a
later app installation fails, reauthorize the same pseudonymous account and
resume the pending study, using its existing consent/status endpoint and dataset.
Keep the study association; connecting a plain storage is not study recovery.
The app owns the resume interface and preservation of that pending context.

The existing headless drivers request `prompt=consent` when exercising explicit
grant selections. Native consent reuse and actual iOS/password-manager behavior
are separate acceptance checks; headless gate success does not qualify them.

The HTTP driver submits the default public-client Console introduction with
its CSRF token and persistence unchecked. Its existing explicit Tessera consent
assertions remain separate; this does not qualify first-party browser sign-in.

The post-restart/recovery helper submits the same default Console introduction
with its CSRF token before its existing identity and persisted-study assertions.


The main HTTP scenario explicitly selects `VOPTRACK_WEBSITE_SIGN_IN=off` to
qualify the consent-based fallback. Fresh owned-website defaults and exact
origin binding are checked separately by `release/console/website_defaults.py`.
This does not change study consent or private Console exposure rules.

### First-release fixture alignment (2026-11-08)

The browser fixture handles configured first-party Console sign-in as well as
ordinary OAuth consent. Private Console checks still require the public
authorization origin and exact private callback, then Home's accepted landing
page. Research setup still creates and opens its study through the wizard.
No remembered-session, real-device or public-host acceptance is added.
