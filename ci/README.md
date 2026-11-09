# Qualification tools

`make ci` runs the full local suite on a clean committed tree; `make ci-remote`
runs it on the configured shared host. Use the validation sequence below to choose the scope. Host settings and credentials stay untracked.

## Quick guide

- Documentation only: check links, command consistency and preserved policies.
- Code or deployment slice: run focused checks and the relevant feature gate.
- Preview a targeted CI plan: `python3 ci/runner.py --plan --steps console-check`.
- Release milestone or Tessera pin: full clean-commit suite, `make ci` or
  `make ci-remote`, as required by the [publication checklist](../release/distribution/README.md).

Targeted results are partial. The detailed coverage and exclusion ledger follows;
keep it in step with gate changes. Public distribution additionally needs the
[maintainer publication review](../release/distribution/README.md).

## Release distribution

`make distribution-check` (also in `make check`) tests matched registry/archive
bundles, clean-source/CI gating, pinned image selection, architecture checks,
cross-store runtime identity and offline pull refusal. Tests corrupt an archive,
alter the proxy runtime configuration, supply the wrong architecture, and add an
unpackaged third image, then assert the intended refusal. Real Compose parses
both generated files; Docker image operations are otherwise mocked.

What this does not cover: publication or anonymous GHCR pulls, website delivery,
image execution, native ARM, fresh-host installation, public TLS or real-client
acceptance. See the [distribution checklist](../release/distribution/README.md)
for the release-day qualification and owner publication stops.

## Image selection proof

`make image-selection-check` uses real Docker Compose parsing against temporary
copies whose application-image default is deliberately nonexistent. It invokes
all ordinary release gates, both dev launchers and every scenario launcher,
intercepting Docker before any container mutation. Both the local default and an
explicit candidate image must be exported and selected for every application
service. A planted missing export demonstrably selects the poison image.

CI builds the selected local tag and freezes the actual verified image ID before
container gates. Strict recovery/setup gates continue requiring an explicit
immutable image. The shipped Compose default is unchanged until release day.

What this does not cover: full gate execution, public-domain TLS, provider
permissions, real-device behavior or guide-only installation. A passing parser
proof is not a full-suite result. Full poisoned-default qualification is recorded
separately with its exact source snapshot and image. Preserve occupied listeners;
use existing per-gate port overrides (for example `VOPTRACK_HOUSEHOLD_HTTP_PORT`
and `VOPTRACK_PRIVATE_HTTP_PORT`) rather than stopping another service.

## Remote host settings

Keep host and identity settings in the ignored `ci/remote.local.env` file or
provide them through the environment. Set `VOPTRACK_CI_HOST` and
`VOPTRACK_CI_KEY` to the SSH destination and local identity file. Set `VOPTRACK_CI_ROOT`
to a private working directory owned by that SSH account on the remote host. The lock defaults to `heavy.lock` inside that directory.
Set `VOPTRACK_CI_LOCK` to the existing shared lock when using a shared host.

## Publication checker

`python3 specs/check-public.py --report REPORT.md`
produces owner decisions for tracked working-tree files and all reachable commit
messages. `make public-check PUBLIC_REF=<squashed-release-commit>` instead scans
that immutable tree/history. Exit 0 means no lexical candidates, 1 means review
candidates remain and 2 means incomplete/unreadable inputs. No category is silently
approved. It is not wired into ordinary `make check` pending per-file owner decisions.

The synthetic tests plant internal coordination markers, a local path, a host
address and a key name, observe the intended failures, restore and pass. They also
check selected commit messages/trees and fail-closed missing inputs/symlinks.
Diagnostics contain locations/categories, not matched contents.

What this does not cover: binary contents, arbitrary identifiers/secrets, every
hostname or address notation, or semantic privacy/publication review. Private
network examples and scanner fixtures may be intentional; the owner must approve
any narrow documented exceptions before release wiring. The inventory and
internal history do not belong in a public release merely because this tool exists.

## Native backup wrapper

[Wrapper coverage and limits](../deploy/README.md#native-backup-wrapper-coverage)
cover stopped snapshot/evidence capture, publication, retention, restart failures
and synthetic defect controls. `make check` selects these Python tests.
`make backup-check` uses the real pinned SQLite backup/restore through the wrapper
with simulated systemd control; it does not qualify a native service installation.

## PostgreSQL cleanup ownership

The native PostgreSQL gate uses a unique name and run label. Cleanup removes
only its matching labelled container, including after a failed start; it never
preemptively removes an existing fixed-name container. Fake-CLI tests exercise
owned/foreign cleanup and detect a planted legacy deletion. These tests do not
qualify PostgreSQL restore behavior; `make postgres-check` remains that gate.

## Runtime and targeted qualification

The full manifest remains the same 23 gates. make ci / make ci-remote use
two bounded workers after serial prerequisites, with the image's immutable ID
frozen before dependent gates. Recovery export → guided recovery → destination
recovery remains ordered. Guided and off-machine recovery take host-wide Docker
preservation inventories, so they run exclusively after draining active work.
Their preservation assertions remain unchanged; other runtime gates retain two
workers. Protocol scenarios run after the functional gates.
Use CI_WORKERS=1 for serial diagnosis; two workers require two CPUs and at least
1.5 GiB available memory. The host's heavy-work lock remains in force.

    make ci-remote CI_STEPS=console-check
    python3 ci/runner.py --plan --steps console-check

Targeted results are **partial**, even if the explicit selection contains all
gates. They stay in the run directory and cannot write a full CI note or authorize
release/distribution. Tag/pin/release milestones still require a complete
clean-commit full run.

Every run has its own tracked source bundle, checkout, logs and metrics. Ignored
credentials and mutable local Git/notes are not copied. Gate project names and
state roots are run-unique. Runtime workers borrow the existing CI Home/Research
dev-listener bundles, drawing any duplicate listener from the other already
reserved CI ports, with disjoint proxy subnets/addresses/pools; they do not use
production listeners. Port overlap, including recovery target ports, is refused.
No external listeners, global cleanup or production rate-limit change is added.

The image preflight checks exact pin, bootstrap executability and refusal without
the storage attestation and packaged operator-logo readability before expensive workflows. New receipts and guards read
the pin from go.mod; dated evidence is not rewritten. A failed prerequisite marks
dependents SKIP and keeps the full result failed.

result.json contains elapsed gate time, worker concurrency, HTTP/retry counts
and actual wait seconds. Static lifecycle phases are recorded separately.
No URL, query string, username, token, payload or record value is accepted by the
metrics API. HTTP attempts are bounded, respect Retry-After and have finite
timeouts; intentional limiter coverage and the kit's 15-second stale horizon
remain. Only unchanged, live-validated setup-operator sessions are reused;
participant grants, fresh-authentication and recovery-target checks are not cached.

Verification: focused tests execute real local child processes to prove two
workers, dependency skips and partial/full separation; overlap and invalid-pin/
bootstrap controls fail closed. The existing poisoned-Compose proof remains.
Actual-image preflight controls and the full two-worker run are recorded for the
qualified commit. Unit/parser proofs alone do not establish image execution or
native device acceptance. Timing metrics are not a privacy audit of old gate logs.

Immutable remote workspaces are compacted only after completion and successful
result retrieval. Ownership and path checks prevent touching other jobs; remote
logs and receipts remain. Interrupted or unretrieved jobs remain for diagnosis.
An early prerequisite failure retains the runner log without creating a full
qualification. Cleanup never prunes shared Docker/Go caches or retained recovery
sets. Workspace unit tests exercise incomplete-job and symlink refusals.
The per-job module proxy can hard-link checksum-identical files from the existing
host proxy. Rsync never writes those links in place; changed modules receive new
files, and Go still validates go.sum. Compaction only unlinks the job's entries,
leaving the host dependency store intact. Source trees and results remain isolated.
Recovery-source snapshot fixtures also always create fresh post-snapshot
administrator authority: it must be rejected by the restored earlier snapshot.
Setup caching must never cross that boundary. The actual recovery drill checks
this refusal; it caught and rejected the initial over-broad optimization.
Remote publication checks only the current job's freshly retrieved results,
so an early failure cannot republish a previous run's local summary.
Disposable gate wrappers exit through their cleanup trap on TERM/INT, including
when the scheduler cancels their process group. Cancellation cleanup is checked
against real owned Compose resources; it does not authorize sweeping other
projects or globally pruning caches.

## Product image preparation

`make release-image PRODUCT=home|research IMAGE=...` loads a usable single-platform
image by default; publication is explicit. Full qualification is still required
for the exact commit. Product packages pin both product environment entries and
copy the dependency notices from the exact application image. Focused packaging
tests cover mismatched product labels, offline-only local preparation, notices,
identity and existing archive refusals. Fake Docker responses are not runtime
evidence; actual product-image and missing-notice refusal checks are recorded in
the release report. Public registry access, native ARM and fresh-host/device
acceptance remain separate. No new protocol assertion is added locally.
