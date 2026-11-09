# Protected guided fresh-host recovery

This separate host-started TLS application does not depend on the original server,
Console, sessions or account database. It is packaged as `voptrack-recovery`.
It has no Docker socket, collector enrollment, live-service launcher or cutover
action. Ordinary Console and collector mounts/permissions are unchanged.

## Host preparation

Use the exact qualified immutable image and published Tessera alpha.39. Keep the
original deployment stopped or unavailable. Prepare a **separate recovery hostname
and TLS certificate trusted by the operator's browser**; do not share the ordinary
Console's hostname/cookie scope. The application terminates TLS itself, requires
an exact configured Host and Origin, and never trusts forwarded protection headers.
Expose only the explicitly configured recovery endpoint. A loopback-published
listener with an operator-controlled tunnel is also suitable if the configured
HTTPS origin matches the browser endpoint.

Stage each unchanged selection according to [recovery-set](../recovery-set/README.md).
Independently retain the key and trusted producer lineage. The browser can select
only configured opaque set IDs and the fixed `fresh` target; no path, executable,
upload or command is accepted from it. Set labels are host-provided display names.

Provision private, persistent Linux directories for state and the **parent** of
the fresh target, owned by the dedicated recovery uid, with mode 0700. The target
leaf itself **must not exist**, even as an empty directory or symlink. State,
target parent and retained input/trust/key directories must be disjoint; symlink
ancestors are refused. All ancestors and mounts must remain controlled by the
operator. Do not run another process as this uid or start recovered services
during the attempt. Give this application only its staged read-only sources,
private state and a dedicated fresh-target parent. Confirm encrypted storage
before using these workflows; the flag in a kit is an attestation, not encryption.

Private JSON configuration (host paths below are container paths):

```json
{
  "Origin": "https://recovery.example:9543",
  "State": "/recovery-state",
  "Target": "/recovery-targets/fresh",
  "Sets": [{
    "ID": "home-retained",
    "Label": "Retained Home recovery set",
    "Directory": "/recovery-inputs",
    "TrustFile": "/recovery-trust/backup-lineage.json",
    "KeyFile": "/recovery-key/backup.key"
  }]
}
```

Mount this configuration read-only. With the application stopped, run the image's
`voptrack-recovery -config /host-config.json -mode authorize -token-file
/authorization/recovery-token` with the same source/state/target mounts and a
separate private authorization output directory. The output file must not exist.
This command writes a random 384-bit token privately and records only its digest,
ten-minute expiry and attempt budget in state. It never prints the token. Deliver
it to the operator using an existing protected host channel. Do not put it in
command arguments, environment, URLs, screenshots, logs or evidence.

Start `voptrack-recovery -config /host-config.json -listen 0.0.0.0:9543
-tls-cert /recovery-tls/server.crt -tls-key /recovery-tls/server.key` on the same
immutable image. Bind only the configured protected endpoint; Docker publication,
if used, is explicitly host-provisioned (for example loopback publication).
Use `docker run --rm` with persistent state/target bind mounts and mount the
base image’s unused `/var/lib/tessera` as a private tmpfs to avoid anonymous
volume retention. Use a read-only image, no capabilities, no privilege escalation and an ephemeral
private `/tmp`. No normal deployment volume or Docker socket belongs in this
application. Run as the dedicated non-root uid owning state and target parent.

The operator opens the configured HTTPS origin and enters the authorization in
the password form. There is no first-visitor claim. Five failed authentication
attempts exhaust that authorization; success consumes it durably before creating
one new session. Sessions expire at the earlier of fifteen minutes or the host
authorization deadline. Restart discards sessions. To regain observation after a
restart, stop the app and explicitly issue a **new** host authorization. Let any admitted work finish before stopping for a new authorization: stopping
the application cancels running children and leaves the attempt stopped. The host
can observe its private journal while the browser session is expired. Reissuing
authorization does not reset an accepted, stopped or completed operation.

## Browser journey and binding

Select an allowlisted source and the fixed fresh target. Validation privately
snapshots the bounded selection, kit, two bundles, settings, independent key and
lineage, then invokes the existing preflight and the unchanged pinned Tessera
acquisition verifier. Review shows source, target, attempt, four exact input
digests, checks, compatibility/coverage limits, steps and downtime. Custody,
latest settings and coverage after selected evidence remain unverified/unknown.

A five-minute confirmation belongs to that authenticated session, exact input
binding, fixed target and operation attempt. CSRF, strict Origin/Host checks and
Secure/HttpOnly/SameSite=Strict cookies apply to entry and every state change.
Selecting again invalidates the previous confirmation and snapshot. Refresh and
Continue are ordinary links; session expiry is checked again after the bounded
POST body read. No JavaScript, polling, percentages or secret
credentials appear in status pages.

Confirmation is consumed and `accepted` is fsynced before the background operation
can start. Losing the response or closing the browser does not cancel or reissue
it. Session expiry does not cancel already admitted work. The application makes
fresh private copies of the **original** allowlisted selection and authenticates
them again, compares the exact binding, authenticates the original private
snapshot again, then validates/extracts bounded allowlisted regular archive
members. A preflight receipt alone cannot initiate restore.

The target is reserved atomically with exclusive mkdir. Even an existing empty
target is refused. An exclusive deployment maintenance lock also prevents supported service launch
throughout offline execution. Lifetime locks cover both state and the target parent, so a
second state directory cannot bypass execution exclusion. Kit import uses the
existing committed-kit validator/importer. Offline producer/settings recovery and
record restore use fixed Tessera flags and structured arguments; child output is
suppressed. A newly generated private offline config contains only supported fixed
paths under the target and the independent trusted origin. Untrusted kit config
fields cannot redirect execution. The validated local/reserve backup slot and
genuine settings export are retained. The original canonical deployment config
stays in the target for separately reviewed host adoption.

## Durable state, interruption and cleanup

The private fsynced journal retains attempt identity, selection/trust/input
hashes, target binding, current phase and phase history. Each mutating stage is
persisted **before** its side effect. Target contents are synced between stages.
An interrupted `accepted`, target reservation, kit import, producer recovery,
record restore or output verification becomes `stopped` on restart. Nothing is
automatically resumed, repaired, cleared or overwritten. The target and journal
must remain stopped for operator inspection. Provision a different fresh target
and state for a new attempt; never reset the old journal to make a retry possible.

Successful offline completion reports only authenticated inputs, kit import,
successful supported offline commands, retained retirement evidence and durable
output-file checks. It does not claim live health, sign-in or record correctness
from command exit alone. The operator must separately launch isolated services,
verify health, accounts/permissions, producer epoch/effective settings, erasure
exclusion and saved Console state before considering cutover. Collector enrollment
and adoption remain separate deliberate actions.

Retained operator-staged inputs, independent key/trust and restored target material
are required inputs/outputs: this application never deletes them. Temporary
`snapshot`, `recheck` and `materials` directories live only in private state;
normal failure, success, abandoned review/session expiry and the next startup
remove them. Abrupt kill can leave private copies until startup; the host must
remove those three task-owned directories if the app will never restart.
Ephemeral `/tmp` contains verifier staging and disappears with the container.
Cleanup failure blocks success. No secure physical erasure is claimed.

The host must unlink its separate raw authorization output file after delivery
or on expiry/failure; consumption makes it unusable even if a stale copy survives.
Its digest/consumption/budget and non-secret operation journal remain for audit.
Do not publish private journals, recovery sets or qualification directories.

## Qualification

Run required `make check`, targeted race tests and the actual-image focused gate:

```sh
VOPTRACK_IMAGE=sha256:QUALIFIED_IMAGE \
VOPTRACK_GUIDED_INPUTS=/private/retained-parent \
VOPTRACK_GUIDED_DIR=/private/new-persistent-qualification \
make guided-recovery-check
```

Normal local CI wires this gate immediately after recovery-set qualification,
retaining its sets and guided outputs under a private persistent state directory.
The focused invocation above does not imply a full-suite run.

Inputs must contain the existing synthetic `household` and `research` retained
sets with source-removal receipts. The runner checks immutable image/version,
audits loopback ports 30943 and 30944 (override with `VOPTRACK_GUIDED_PORT` and
`VOPTRACK_GUIDED_RESTORE_PORT`), inventories resources before/after, creates no
subnet, never globally prunes and preserves all target directories. It refuses
existing qualification roots and colliding task names. Its random task-labeled
recovery containers and separately named verification containers/volumes are the
only resources it may remove.

Coverage:
- Actual-image authentication replay, invalid/expired authorization, bounded
  attempts, anonymous/unrelated sessions, CSRF, Host/Origin, path selection,
  exact confirmation fields and stale attempts.
- Wrong key/lineage, forged MAC with recomputed outer hashes, missing material,
  unsafe archive paths and oversized settings all fail before target mutation.
- Existing and partial targets remain untouched. Changed original inputs after
  review stop before reservation. Concurrent confirms admit one operation.
  A deliberately discarded successful browser response cannot repeat restoration.
- Home and Research guided targets are **not restored a second time**:
  `recovery-set/drill.py --prepared-root` explicitly starts those outputs in
  isolation and verifies producer epoch/settings, private-record isolation,
  later-erasure exclusion, sign-in and retained Console service/dashboard state.
  Any collector enrollment in this separate verification drill is explicit.
  The drill receives a run-private copy of the retained fixture, so new receipts
  and helper files cannot overwrite source evidence. The gate checks all original
  fixture filenames, bytes, modes and modification times before reporting success.
- Genuine initial backup-point evidence may be empty. Both Home and Research
  recover successfully with its authenticated zero-byte evidence, without silently
  substituting it for selected newer evidence or claiming later-erasure coverage.
- Native/race tests additionally cover expired sessions and confirmations, TLS
  refusal, duplicate/unknown/oversized forms, lifetime state/target locking,
  target changes, restart at every mutation boundary, cleanup and language keys.
- Isolated compiling fault candidates terminate after real durable stage writes;
  intact restart must stop without changing target hashes. The isolated candidate
  also returns failures after the first real kit write and injects command errors
  with partial-target markers at the producer/record stages, proving error handling, cleanup and preserved stopped targets. The fault qualifier
  audits separate loopback ports 31943/31944 (host overrides are available). Planted guard defects
  must fail the actual-image assertion, followed by intact replay. The qualifier
  also proves session expiry during a slow bounded POST using a five-second
  test-only session deadline, and rejection of valid empty evidence by an isolated
  nonempty-file defect. Neither session hook nor planted defect ships.
- `TestUIReviewCorpus` emits fourteen synthetic UI cases (including public refusal and long labels) in all seven languages.
  `ui-review.mjs` uses operator-supplied local Playwright/Axe/Chromium to check
  phone/desktop, light/dark, keyboard focus/order, accessibility and overflow.
  Review images as well as automated results.

To reproduce compiling defects, invoke `python3 release/guided-recovery/qualify-faults.py
QUALIFIED_COMMIT INTACT_IMAGE SYNTHETIC_INPUTS NEW_PRIVATE_ROOT` from the checkout.
It compiles and builds isolated candidates, observes the intended failures and
replays intact source. Keep its roots private; it never modifies shared source.
The qualifier also plants the Python-only receipt-overwrite defect in an archived
runner, compiles it, and exercises the unchanged immutable image against private
copies of synthetic fixtures. It must fail at `retained source fixture was modified`,
then pass with the intact runner. Append `--fixture-preservation-only` to run
only that affected proof; no runtime image input changes for this harness defect.

What this does not cover: arbitrary uploads, PostgreSQL-specific sets, generic
archive/version migration, physical power-loss/filesystem corruption, hostile
operators or processes with the recovery uid, independently durable off-machine
custody, provider replication, acknowledgement-coupled erasure durability,
production capacity, first-run Home setup, live adoption/cutover, affected-device
repair or automatic collector enrollment. Browser restoration correctly remains
offline; live checks are explicitly outstanding host actions.

## Fresh-install assurance integration

The opt-in [recovery-assurance runner](../recovery-assurance/README.md) connects
fresh Home/Research entry with native acquisition, destination fetch and guided
recovery. Its preservation mode keeps source and recovery volumes; source services
are stopped. This remains local synthetic qualification. Existing default gate
behavior is unchanged.
