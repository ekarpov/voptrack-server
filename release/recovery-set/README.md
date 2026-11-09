# Recovery-set preflight and independent-host drill

`voptrack-recovery-set` is a host-invoked, offline preflight. It validates selected
retained bytes before an operator attempts recovery. It never restores, starts
services, provisions accounts, mounts Docker, or changes a target. Ordinary
Console and the collector receive no keys or additional authority.

## Inputs and invocation

Keep an operator-controlled directory with `selection.json` and four distinct
regular files. Input names are single basenames of at most 64 characters. The
selection has version 1 and exactly `backup`, `evidence`, `kit`, and `settings`:

```json
{"version":1,"backup":{"file":"backup.tar","sha256":"<64 lowercase hex>"},"evidence":{"file":"evidence.tar","sha256":"<64 lowercase hex>"},"kit":{"file":"kit.tar","sha256":"<64 lowercase hex>"},"settings":{"file":"backup-settings-export.json","sha256":"<64 lowercase hex>"}}
```

- Backup and evidence are unchanged Console/collector bundle downloads. Evidence
  may be from an evidence-only operation or a complete backup. Select it explicitly;
  opaque snapshot/retirement labels and collection times are not ordered here.
- Kit is an unchanged committed kit export. Retain compatible revisions, including
  earlier service material needed by older archives. The separately retained key
  must equal its backup key. Validation does not decrypt or inspect the archive.
- Settings is a genuine acknowledged public settings export, retained separately.
  It must describe the independently trusted producer. It need not match an older
  kit's settings revision. Its unsigned contents cannot establish latest policy.
- Independently retain `backup-lineage.json` (`deployment` and HTTPS `origin`)
  from a trusted observation and the 32-byte backup key outside the input directory.
  Do not derive expected identity from an untrusted archive, export or kit.

On the recovery host, use the qualified immutable image. Example for Linux/WSL:

```sh
umask 077
docker run --rm --network none --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true --user "$(id -u):$(id -g)" \
  --tmpfs "/tmp:uid=$(id -u),mode=0700" \
  -v "$PWD/inputs:/recovery-inputs:ro" \
  -v "$PWD/trust:/recovery-trust:ro" \
  -v "$PWD/key:/recovery-key:ro" \
  --entrypoint voptrack-recovery-set IMAGE_DIGEST > preflight-receipt.json
```

Directories and their ancestors must be controlled by the operator. Final input
symlinks and special files are refused. Read-only mounts and private staging
prevent accidental mutation; selection hashes detect substitution while reading.
Staging is bounded and removed on success, refusal or normal cancellation. An
abrupt kill can leave a private temporary directory; use ephemeral `/tmp` as above.

## What success establishes

The command checks the four exact selection digests; bounded kit members, keys,
configuration and product-required material; independent lineage and key pairing;
public settings format/producer; exact bundle membership, sizes and hashes; and
manifest agreement with authenticated metadata. It invokes the unchanged published
`tools/verify_acquisition.py` from the pinned Tessera module. The image packages
that dependency utility and Python; Voptrack owns no replacement MAC algorithm.

Exit 0 emits a JSON receipt with check time, selection/trust/input digests and
`inputs-verified-not-restored`. Failure exits 1 with a fixed message and no success
receipt. Preserve the receipt privately; hashes can correlate retained material.
A receipt is informational, not reusable restore authorization. Keep the selected
files immutable and re-run preflight immediately before offline recovery; changing
any input invalidates the earlier receipt. Supported maintenance and fresh-target
restore restrictions still apply. An incomplete target must remain stopped and
must not be overwritten by retry.

Custody stays `unverified` and later-erasure coverage stays
`unknown-after-selected-evidence`. Authentication cannot prove that the operator
selected the latest evidence/settings, that storage is physically independent,
that every archived service credential matches the selected kit, or that every
historical archive is compatible. A fresh-target drill provides separate executed
compatibility evidence. No alpha migration or downgrade guarantee is added.

## Gate and drill

After building and checking the image's own Tessera version, run:

```sh
VOPTRACK_IMAGE=IMAGE_DIGEST make recovery-set-check
```

Audit loopback ports 30743/30744/30780/30843 and subnet `10.219.91.0/24` first;
`VOPTRACK_RECOVERY_SET_*` overrides are in `verify.sh`. The gate creates disposable
Home and Research sources, uses supported CLI/API/Console actions, seeds a legacy Console document through
the public record API with the retained Console-state service credential, exports a committed approved kit and genuine public bundles,
then removes each source and its volumes before restoring. Set
`VOPTRACK_RECOVERY_SET_DIR` to a fresh persistent private directory to retain the
synthetic sets for separate-host replay. These files contain synthetic credentials;
never publish, print or commit them.

`drill.py RETAINED_VARIANT IMAGE_DIGEST TASK_PREFIX PORT` needs only retained
files, the image, and repository browser helpers. It checks preflight, recomputes
untrusted hashes around a forged MAC and requires refusal, restores into fresh
volumes, refuses repeated kit import, recovers producer/settings before record
restore/startup, verifies new epoch, private-record isolation and later erasure,
then explicitly enrolls a keyless collector and collects a completed backup.
Retained Console credentials and archived dashboard records must remain usable.
The accepted first-release UI no longer creates personal saved dashboards; the
fixture preserves the same restored-record assertion without calling that retired
form. This tests archive retention, not saved-view UI availability.
Only task-named containers/volumes are removed. No global pruning is used.

`--negative-only` stops after actual-image valid/forged-MAC checks, enabling the
compiling omitted-verifier defect proof. A full ordinary local run proves isolated
fresh-volume recovery on the same Docker host. Independent-host acceptance must
add a separate-host replay and retain host/daemon distinction, transfer/readback
hashes, image identity, source-removal and result receipts. It cannot be inferred
from a second volume or an operator's acknowledgement.

For the explicitly approved configured test host, invoke the separate-host
procedure with `bash release/recovery-set/remote.sh RETAINED_PARENT IMAGE_DIGEST`.
It uses scoped SSH credentials from the existing private CI configuration. The
runner verifies OS/daemon distinction and exact retained-input readback. If image
IDs change across Docker stores, it requires identical platform, every ordered
filesystem-layer digest and the complete runtime configuration before execution;
`runtime-image.json` retains both IDs and a configuration hash. A version string
or successful load alone never establishes image equivalence.

The gate covers local and independent-host Home/Research drills, an omitted-verifier
proof and intact replay. Original local-container lifecycle
exceptions remain disclosed separately from the passing restoration assertions.

## Coverage limits

This is synthetic Home/Research, SQLite and supported offline recovery. No new
browser recovery UI, provider replication, physical custody attestation,
acknowledgement-coupled erasure durability, production availability/scale, Swift,
QR or PostgreSQL-specific recovery-set result is claimed. No existing deployment
is upgraded, repaired or cut over. Arbitrary browser-upload extraction and general
restore orchestration remain later work.

The [guided recovery application](../guided-recovery/README.md) reuses these inputs
and assertions. Its explicit `--prepared-root` drill mode verifies an already
restored guided target without importing/restoring again; ordinary drill mode
retains its original kit/producer/restore path.

## Fresh-install assurance integration

The opt-in [recovery-assurance runner](../recovery-assurance/README.md) connects
fresh Home/Research entry with native acquisition, destination fetch and guided
recovery. Its preservation mode keeps source and recovery volumes; source services
are stopped. This remains local synthetic qualification. Existing default gate
behavior is unchanged.
