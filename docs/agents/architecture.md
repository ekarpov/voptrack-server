# Agent guide: architecture

Use this map to locate a change. The [human architecture guide](../architecture.md)
explains the products and privacy model; [AGENTS.md](../../AGENTS.md) defines
invariants that apply to every change.

## Quick guide

1. Decide whether the change concerns protocol behavior or Voptrack semantics.
2. Find the owning component below and inspect its callers and tests.
3. Trace the credential and data boundaries before changing a request flow.
4. Check the relevant release gate's coverage and exclusions.

## Component map

| Concern | Implementation | Contract or checks |
| --- | --- | --- |
| Protocol, accounts, storage, sharing, OAuth | Pinned external Tessera tool in `go.mod` | [Dependency ledger](../tessera-dependencies.md); never edit Tessera here |
| Web UI and study API | `cmd/console/`, templates and static CSS | [Console](../../release/console/README.md), [Research](../../release/research/README.md) |
| Bootstrap, generated configuration and maintenance | `deploy/voptrack-init`, `cmd/deployctl/` | [Operations](../../release/README.md), `release/verify.sh` |
| First-run Home wizard | `cmd/homesetup/`, `internal/homesetup/` | [Setup contract](../../release/home-setup/README.md) |
| Collection and scheduler access | `cmd/backupcollector/`, `internal/backupcollector/`, `internal/schedulerclient/` | [Backup observations](../backup-recovery-operations.md) |
| Opaque off-host replication | `cmd/backupreplicator/`, `internal/backupreplication/` | [Off-machine contract](../../release/off-machine/README.md) |
| Kit, input selection and restoration | `cmd/recoverykit/`, `cmd/recoveryset/`, `cmd/recovery/`, matching `internal/` packages | [Recovery set](../../release/recovery-set/README.md), [guided recovery](../../release/guided-recovery/README.md) |
| Supported acquisition transport | `internal/acquisitionclient/` | [Transport contract](../../internal/acquisitionclient/README.md) |
| Vocabulary and illustrative tracks | `specs/` | Append-only JSON; [vocabulary](../../specs/voptrack-vocabulary-v1.md) |
| Generated app catalogue | `catalogue/` | [Version selection and provenance](../../catalogue/README.md) |
| Image and qualification | `deploy/Containerfile`, `ci/`, `release/`, `scenarios/` | [CI scope](../../ci/README.md) |

## Authority and data flow

Caddy routes HTTPS to Tessera and the Console. Tessera authenticates people and
checks record access. The Console uses the person's OAuth authority for reads
and administration; a new UI must not introduce a broader credential. The
Console also serves the product study API in `cmd/console/api.go`; protocol
requests remain Tessera's responsibility.

Home's private member stores use sealed custodians. Selected tracks are shared
with Home; a member who has not set a password remains subject to owner-issued
device rebind. Host control permits disk access. Research participant stores are
researcher-administered and use versioned consent. Withdrawal freezes sharing;
erasure is separate. Preserve these distinctions in UI, API and tests.

The collector retrieves completed artifacts. The replicator transfers opaque
bytes without recovery keys or record-store access. Recovery is a separate
host-authorized application on a fresh target. A copy receipt, input preflight
and successful restore establish different things; preserve their boundaries.
