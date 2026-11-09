# Verified off-machine backup copies

This opt-in Voptrack integration preserves published Tessera alpha.39 and its
native scheduler. It copies opaque collector bundles using AWS SDK for Go v2.
The archive remains in Tessera's encrypted format; evidence and metadata retain
their native opaque formats. No backup key or kit is given to the upload worker.

**No AWS account, bucket, IAM enforcement or independent-host destination has
been qualified.** Qualification is limited to local synthetic testing. Do not
create paid resources, consult existing AWS credentials or transfer data outside
the local fixture during this task. Templates below require separate review and
explicit destination/payload/budget approval before provider acceptance.

## Components and authority

- The normal collector and Console keep their existing mounts and authority.
- Opting in enables collector capture into a private outbox BEFORE local bundle
  publication. Capture is fsynced and atomic. Producer catalog rotation cannot
  prevent replay of a capture-before-publication crash.
- The upload worker mounts only that outbox, its private state, a dedicated
  credential file, explicit approved settings companion and sanitized runtime
  output. No record store, producer state, recovery key/kit or Docker socket.
- Console reads only off-machine.json beside its existing runtime observation.
  Every status request requires current owner/operator approval. Members and
  participants gain no access and no browser destination configuration is added.
- The host-only fetch invocation uses a separately retained read-only S3 identity.
  Its separately mounted kit is used locally to stage the existing recovery-set
  format, never uploaded. Independent key/trust are supplied to preflight later.
- Normal worker operation never starts recovery, enrolls collectors, launches
  restored services or cuts over a deployment.

The optional compose overlay uses the existing bundles volume for a
.replication-outbox subdirectory. The collector gains no mount or S3 credential;
the worker mounts only that subdirectory using volume subpath, not the whole
collector publication directory. State has a separate private volume. Console's
existing runtime mount remains read-only. Normal compose remains unchanged.

## Host preparation — requires separate host and destination approval

Use an independently controlled AWS general-purpose S3 Standard bucket. Enable
versioning and Block Public Access; require TLS and no automatic lifecycle or
noncurrent-version expiration. Object Lock, remote deletion and other providers
are deferred. A local S3 service is a fault fixture, not a durability claim.

Review config.example.json, writer-policy.example.json,
reader-policy.example.json and bucket-policy.example.json. Replace every
placeholder. The dedicated writer has only prefix-scoped list/get/create; deny
deletes and bucket protection changes. Conditional creation is required by the
bucket policy; the client uses If-None-Match: *. The separately retained reader
may list/get only. Review the role's total effective permissions, not just this
example. No policy is applied automatically.

ExpectedBucketOwner is a required 12-digit account ID for AWS configuration and
is sent on object/list requests. Bucket, account, region, prefix, deployment and
outbox identity bind private worker state. Changing a destination requires
separate state/migration review; old successful acknowledgements cannot transfer
to another destination. Versioning helps an authorized operator recover prior
versions; this worker does not list/restore noncurrent versions automatically.

Provide a dedicated 0600 JSON credential file as credential.example.json. The
SDK receives only that explicit static provider; no profile, environment,
metadata or shared credentials discovery is used. Session tokens are supported.
Credential rotation/removal requires worker restart and issuer-side revocation
as applicable; changing a file does not revoke already issued credentials.

Provide private Linux state and outbox directories owned by the service uid,
mode 0700, with controlled non-symlink ancestors. State and outbox must be
disjoint. A separate sanitized runtime directory contains only public receipts.
In the supplied overlay:
- prepare .replication-outbox, mode 0700/uid 10001, inside bundles;
- mount replication-state at /var/lib/voptrack-replication (image-owned 0700);
- supply VOPTRACK_REPLICATION_CONFIG and VOPTRACK_REPLICATION_CREDENTIAL as
  explicit private host file paths;
- use the base compose file plus this overlay and the off-machine profile only
  after host provisioning and destination approval.

The image's unused /var/lib/tessera is tmpfs in the worker, avoiding anonymous
volume retention. Mount no normal deployment data there.

For a settings companion, stage an explicitly approved genuine public settings
export read-only and set SettingsFile to that in-container path. Its bounded,
strict public schema must match the deployment. It is privately snapshotted per
operation. It is never obtained with an unattended administrator credential.
A missing export does not stop copying opaque bundles, but recovery staging
requires a matching remote companion or an independently supplied genuine export.
Neither one proves the latest settings. Keep kit/key/trust in separate custody.

LocalFixture=true permits only an explicitly trusted HTTPS loopback endpoint
and CAFile for synthetic tests. It always disables an independence claim.
Production rejects endpoint/CA overrides. This mode never attests AWS semantics.

## Queue, publication and failure contract

Collector capture uses a short queue lock under its existing writer lock. Remote
network I/O never holds the collector writer lock. Exact packed bytes, manifest,
digest and operation identity are stored before local publication. A restart
finishes unpublished local captures without consulting the surviving producer
catalog. Local publication and remote verification are separate acknowledgements.
A remote acknowledgement cannot delete an uncompleted local handoff.

The collector's configured local usable-download count is unchanged. When
replication is enabled, all bounded catalog results are eligible rather than
only the newest local Keep per kind. Native retention/catalog availability is
still finite: a prolonged outage or queue-full condition can miss results never
collected. Do not claim complete producer history.

The pending-payload budget defaults to 5 GiB, minimum two 275 MiB bundles, with
an 80% warning. Metadata/history are separately bounded: 10,000 operations and
64 KiB per newly captured obligation; the worker retains small immutable job
records and settings snapshots. This is not a filesystem quota. Queue/metadata/
disk exhaustion fails closed, preserves pending work and existing usable
downloads, and requires host resolution or capacity changes. No pending work is
silently deleted. Only unpublished .capture-* temporary directories under the
exclusive queue lock can be reclaimed; all durable obligations remain.

Payload objects are content addressed; commit descriptors use a hash of the
operation identity under an opaque deployment namespace. No personal names,
record values or credentials are placed in object names or diagnostics.
The client fully GETs and hashes each payload/settings object after PUT,
including 412 conflicts and lost responses. A conflict never overwrites or
automatically repairs bytes. HEAD, ETag and upload success are not proof.

The immutable commit descriptor is written LAST and read back in full. There is
no cross-object transaction. Interrupted jobs reconcile exact identities and
bytes. Verified job state is fsynced before any queue release; a crash after that
write can release safely on restart. Completion history comes only from private,
destination-bound state, never the public status receipt.

Each request is bounded by 110 seconds; a job has a nine-minute context. One
worker holds a lifetime private-state lock. Scans run on startup and every minute.
Memory is streamed/bounded; current bundle limit is 275 MiB and no multipart
upload is used. Existing remote copies are re-read after 24 hours. Failed probes
produce unavailable/unknown, old verification becomes stale after 26 hours,
and a missing heartbeat becomes unknown after two minutes. Clock anomalies do
not create fresh success. The public status retains historical success separately
from current state and exposes no underlying error details.

The worker uses durable pending/uploading/verifying/committing/verified states.
It retries transfers, never restores or replays uncertain recovery mutations.
Normal local payload release happens only after both acknowledgements. All
completed/partial recovery targets and operator-staged material remain separate.

## Retention and loss window

All remote objects/evidence are retained; no remote delete or automatic expiry
is implemented or granted. The policy floor is 30 days plus the latest seven
snapshots and dependencies, not a maximum or a promise of implemented pruning.
Storage/cost can grow. Dependency-aware pruning with separate deletion authority
needs a later review. Never discard newer evidence merely because an older
archive remains.

Preserve the native backup cadence and retention unless an owner explicitly
changes them. One-hour cadence is recommended for newly configured protection.
The transfer target is 15 minutes after local publication within a measured
qualification envelope; it is not an outage guarantee. Data loss also includes
backup production and collection delays. Verification timestamps describe byte
checks, not record watermarks or newest source contents.

Erasure evidence has an independent potentially unbounded gap until a new
completed evidence result is collected and copied. No S/R ordering, mtime-derived
coverage, synchronous erasure durability or automatic evidence creation is added.
Copies, missing companion material, independent destination attestation and
actual recoverability are separate observations.

## Host recovery retrieval

With an independently retained read-only credential and matching configuration:

```sh
voptrack-replicator -config /private/recovery-config.json -mode list
voptrack-replicator -config /private/recovery-config.json -mode fetch \
  -backup OPAQUE_BACKUP_ID -evidence OPAQUE_EVIDENCE_ID \
  -target /private/new-staging -kit /independent/committed-kit.tar
```

An existing target, including an empty directory, is refused. Failure preserves
partial staging for inspection. If the descriptor has no settings companion,
add -settings /independent/genuine-settings-export.json. Fetch emits inputs/
with bounded fixed names and a selection manifest. It does not retrieve key or
trusted lineage, authenticate a metadata MAC, authorize restoration, start a
server or claim latest settings. Stage independently retained trust/key beside
it, run existing preflight, then use protected guided recovery. All descriptor
keys, lengths and hashes are revalidated; unsafe/incomplete descriptors fail.

Offline enrollment of already published local bundles is also host-only:
voptrack-collector -mode queue -bundles /private/published -outbox /private/outbox.
It uses no administrator, native retrieval or S3 credential.

## Qualification coverage and limits

make off-machine-backups-check requires VOPTRACK_IMAGE, VOPTRACK_OFF_MACHINE_INPUTS
and a fresh VOPTRACK_OFF_MACHINE_DIR. The local gate audits loopback port 30691
(override VOPTRACK_OFF_MACHINE_PORT), creates no subnet and uses only synthetic
credentials. Its native collector runs against a TLS fixture of the supported
public catalog/lease/artifact contract, using genuinely retained alpha.39 public
artifacts. The S3 fixture models conditional create, read-only/prefix/deletion
refusal, corruption and lost responses; it does not implement or qualify AWS IAM
or validate provider durability. No existing AWS credentials are read.

Coverage includes capture before local rotation; restart without a surviving
catalog; readback corruption with pending retention; immutable conflict; lost PUT
responses; read-only fetch after removing only disposable local runtime copies;
fresh-target/preflight and guided Home/Research restores with producer epoch/
settings, record isolation, newer-evidence erasure exclusion and saved Console
state. Source fixtures and prior evidence are fingerprinted and never rewritten.

Native/race tests cover acknowledgement/disk errors, destination binding,
public-status rebinding refusal, TLS/explicit credential selection, stale/future
receipts, unsafe descriptors, bounds and existing target refusal. UI corpus and
browser review cover owner/operator restrictions, both products, seven languages,
phone/desktop, light/dark, keyboard focus, accessibility and failure states.

Compile isolated planted runtime defects before building their actual images;
require intended assertion failures and intact replays. Retain identities and
all private logs/partial targets. Never globally prune or target original
deployment resources. CI uses private persistent roots and synthetic fixtures.

Not covered: real AWS/provider IAM or bucket-policy enforcement, separate-host or
physical independence, production capacity/cost, physical power loss/filesystem
corruption, indefinite offline backlog, newest-settings or acknowledged-erasure
durability, automatic key/kit custody, remote deletion, other providers,
PostgreSQL-specific recovery, automatic adoption/cutover or device repair.

### Review hardening

A populated worker journal with a missing binding is refused; it cannot create a
new account/bucket binding around existing verified acknowledgements. Binding
inputs are bounded regular files with no symlink following. Preserve damaged
state and restore its exact binding from trusted evidence, or arrange a separately
reviewed migration; do not remove journal files to bypass a refusal.

Every outbox lock establishes the parent-directory sync barrier before exposing
obligations, including restart after capture rename. A failed barrier refuses
offline enrollment without touching pending bytes; explicit retry after repairing
storage completes. The local actual-image gate covers missing/indirect binding,
original-binding retry, failed directory barriers and queue-only enrollment with
no retrieval/cloud credentials. Isolated compiling defects prove both assertions.
These syscall/process checks do not emulate physical power loss.

Status output must be outside worker state/outbox and cannot alias a credential
or settings input. Startup refuses such output/input overlap before any write.
Private lifetime locks must be regular files, and frozen settings are read with
bounded no-symlink validation on every unfinished transfer retry.

## Fresh-install assurance integration

The opt-in [recovery-assurance runner](../recovery-assurance/README.md) connects
fresh Home/Research entry with native acquisition, destination fetch and guided
recovery. Its preservation mode keeps source and recovery volumes; source services
are stopped. This remains local synthetic qualification. Existing default gate
behavior is unchanged.

## Local completion follow-up

The [completion matrix](../recovery-assurance/README.md#local-completion-matrix)
adds bounded near-limit synthetic transport, queue-full/outage/restart and all
Home-mode integration coverage. Its exact limits are listed there. AWS qualification
is deferred to the bottom of the backlog with pre-release tests.
