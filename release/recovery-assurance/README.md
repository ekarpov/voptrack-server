# Recovery assurance — reviewed local integration

Run from a clean current dev baseline on a fresh slice branch, retaining Tessera
alpha.39 and every existing qualification artifact. This is an acceptance runner,
not a product, deployment action or AWS provisioning service.

    VOPTRACK_IMAGE=sha256:VERIFIED_IMAGE \
    VOPTRACK_ASSURANCE_DIR=/private/new-assurance-run \
      make recovery-assurance-check

The directory must not exist. Docker, cached caddy:2-alpine, the matching
alpha.39 Voptrack image and local OpenSSL are prerequisites. The runner uses
loopback ports 31021–31024, 30691 and 30943–30944, and subnet 10.199.230.0/24;
it checks collisions first. Never point it at existing source volumes or evidence.
Runtime/image matching is an operator prerequisite; image version is checked
again by every reused actual-image gate. Pin checks alone do not establish
Voptrack source identity.

## What it exercises

- Home selects public mode by default; `VOPTRACK_ASSURANCE_HOME_MODE=private`
  uses its private listener, and `off` uses native app authority without Console
  service credentials. The completion matrix below requires all three modes.
  Home enters through protected browser setup, using the privately retrieved
  one-use code and certificate fingerprint. It selects fixed choices, sets a
  private random owner password, and downloads initial material with custody
  attestation. Those initial downloads are not a completed recovery set.
- Research uses operator initialization and its existing study wizard, explicit
  consent publication, native pseudonymous joining and a synthetic participant
  record. No alternate study implementation is introduced.
- Both produce genuine native backup and later retirement-evidence operations,
  collect them through the real retrieval-only collector into the durable outbox,
  and export a committed kit through session/revision-bound host approval.
  Key and producer trust are retained separately from the transfer worker.
- Source containers and networks stop before transfer/fetch. All original source
  volumes remain. Recovery mounts no source volumes, native retrieval token,
  uploader state or writer credential. This models unavailable services, not
  destruction of the source host or physical independent custody.
- Actual uploader/reader binaries copy the selected real outbox obligations to a
  local TLS object fixture, verify full GET bytes, reconcile lost replies, then
  fetch using the separate read-only identity. The fixture does not validate AWS
  request signatures, IAM, bucket policies or provider durability.
- Existing negative gates cover denied writes, corruption, immutable conflicts,
  missing destination binding, publication acknowledgement and failed sync barriers.
  Guided recovery checks wrong/missing kit, wrong key/trust, foreign/missing settings,
  changed inputs, pre-existing/substituted targets, confirmation replay and
  concurrent/lost-response confirmation. Prior compiled crash-boundary proofs
  are preserved; new runtime changes require affected actual-image fault proofs.
- Fresh guided targets are explicitly started for correctness verification:
  owner/member authentication, records, record isolation, newer-evidence erasure
  exclusion, producer epoch/settings, saved Console state and keyless collector
  re-enrollment. Research also verifies its real participant store, study
  document and consent. Existing targets are never reused for a new attempt.

All credentials and synthetic private state stay in mode-0700 evidence roots.
Logs are private; do not paste bodies, field values, identities or credentials.
The uploader sees opaque bundles/public settings and explicit synthetic fixture
credentials only. Ordinary Console mounts and per-person authority are unchanged.

## Retention, recovery windows and evidence

Keep native schedules/retention unchanged in existing deployments. Fresh synthetic
fixtures use a one-hour interval and keep eight native results. All fixture remote
objects and all source/recovery/failed targets remain. No remote delete or bucket
expiry is implemented. The policy floor remains 30 days plus seven successful
recoverable sets and their dependencies; it is not implemented pruning.

Report observed copy duration, never a universal 75-minute loss bound. Backup
production, collection, WAN outages and independent erasure-evidence gaps remain.
A successful copy is not proof of a current complete set or latest erasure coverage.
A valid older settings export can be an explicit recovery selection; no timestamp
can establish that it is the latest setting of an unavailable source.

The result matrix records local integration verified separately from AWS/IAM
pending, separate-host recovery pending and physical custody unverified. Missing,
mismatched or incomplete evidence must refuse the aggregate result. The report
independently requires the native-publication proof, exact operation/kind/manifest
and size/SHA256 binding across approved inputs, source outbox, retained local
publication, acknowledged copied queue and fetched/guided inputs. An acknowledgement
may release the copied pending payload; it cannot erase its provenance record.
Source variant and entry workflow must also match the product. Before/after
Docker inventories and all private inputs, logs, source images and partial targets
are retained. Only task-named containers/networks are removed; volumes are not
pruned. Whole-window lifecycle/network exceptions must be reported explicitly.

## External acceptance remains gated

No AWS endpoint, ambient credentials, paid resource or external transfer is used.
Real acceptance requires separate owner approval of destination configuration and
credential FILE PATHS, allowed prefix, synthetic payload/budget scope and a
separate host. Never paste credential values. Until those exist, AWS/IAM,
separate-host replay, physical independence/custody, actual power loss,
production capacity and real app/device onboarding remain pending.

The initial local completion implementation stopped for review. The owner has
authorized the follow-up review, fixes, affected checks and direct source merge
into local dev after acceptance. Push, deployment and cleanup remain separate.

## Local completion matrix

    VOPTRACK_IMAGE=sha256:VERIFIED_IMAGE \
    VOPTRACK_ASSURANCE_DIR=/private/new-completion-run \
      make backup-recovery-completion-check

The parent directory must exist and the run directory must not. This sequences
private, app-only and public Home, each with a fresh Research source and recovery.
Each chain performs all existing input/material/target refusal checks. A selected
Home mode binds source and recovery receipts; app-only has no saved Console state
to recover and must retain absent Home/Console service credentials and blocked
Console routing. Explicit post-restore operator init regenerates derived routing
before live verification. The restoration service itself does not activate it.

App-only fixture preparation uses the existing host-only `voptrack-deploy
observe-settings` command with an explicit synthetic host-approved administrator
token staged only in the one-shot container's tmpfs. Only the public native
observation is retained; no new helper or Console credentials are installed.
The committed kit uses the existing local socket and exact host approval.
This does not deliver a new production app-only browser kit-export workflow;
the existing guarded host maintenance export remains its operator fallback.

The capacity scenario is transport-only modeled data: three bundles each contain
a 256 MiB synthetic archive and 16 MiB evidence, below the 275 MiB packed limit.
A 550 MiB queue must refuse the third capture, preserve existing usable evidence
and pending bytes through a destination outage, then drain and collect the refused
result on restart. Collector/uploader containers run with a 384 MiB memory limit,
no swap allowance and two CPUs. The fixture server's memory is outside that bound.
Read-only discovery and retained immutable commit descriptors must bind all seven
captured obligations. The aggregate independently reads bounded, non-symlink
receipts and rechecks destination binding, verified private jobs, discovered
operations, packed payloads and settings bytes after acknowledgement. Missing
resource-envelope fields cannot establish acceptance. All objects and failed
runs remain. These synthetic bundles have no valid native MAC and establish
transport capacity only, never native producer or restore capacity.

This measures one bounded local envelope, not WAN throughput, production load,
disk-full/power-loss durability or a maximum recovery time. Existing compiled
crash-boundary proofs retain their original scope; no new runtime/retry behavior
is introduced. AWS qualification is at the bottom of the pre-release backlog.
Separate-host recovery and physical custody remain unverified by local results.
See the
[operator recovery guidance](../../docs/backup-recovery-operations.md).
