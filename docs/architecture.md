# Architecture

Voptrack Server is a deployment of [Tessera](https://github.com/ekarpov/tessera) with a web
interface and operating tools around it. This document describes what runs, who may read what,
and where each piece lives, so that you or your agent can run it or build a similar server of
your own. Start with [installation](installation.md); product operations are in the
[operations reference](../release/README.md). [Manual Tessera operations](operating.md)
cover separately assembled deployments.

## Quick guide

- Choose [Home or Research](#two-products) and its trust model before installation.
- Use [What runs](#what-runs) to identify the components.
- Read [Identity and trust](#identity-and-trust) before changing access.
- Agents can use the [implementation map](agents/architecture.md) to find code.

## The rule that divides the work

**Tessera owns protocol and server behavior; Voptrack owns product integration.**
A generally useful integration can still use supported interfaces here. Tessera owns the protocol,
accounts, stores, sharing, administration, backups and the reference server. It treats roles,
annotations, media bytes and `json` as opaque: stored and returned, never read. This repository
owns only what is about households, studies or Voptrack's vocabulary, plus the configuration and
tools that run Tessera as a product. The pinned Tessera version is the `tool` dependency in
`go.mod`; the dependency list is [tessera-dependencies.md](tessera-dependencies.md).

## Two products

Both are one Docker image and one Compose project; the client is the Voptrack app.

**Voptrack Home** is a server for a household. Every member has a private store that all their
devices record into, and chooses which tracks the household sees together. There is no shared
"family" store. The owner invites and suspends members, can stop a member's share of a track but
can never read a member's store, and cannot reset a member's password. Home runs in three shapes:
with the web Console, with the Console reachable only at home, and app-only (no web interface at
all; the owner's phone holds the server rights).

**Voptrack Research** is a server for studies. One store per participant, administered by the
researcher, named by a pseudonym Tessera generates. A study has versioned consent text, invite-only
or open joining, and tracks the researcher designs. Consent versions are immutable: editing publishes
a new one. Withdrawal freezes the researcher's reader, so nothing new is seen; erasing data is a
separate, explicit act. The first-release researcher website monitors collection through
counts, event dates and participation state; recorded values are available through authorized
CSV/JSON downloads. Analytical HTML dashboards are retired. The retained analytical panel CSV
is withheld until the researcher saves a suppression threshold for small groups (minimum
distinct contributing participants). Technical collection counts and raw study exports do not
require that analytical policy.

## What runs

| Part | Role |
| --- | --- |
| `tesserad` (Tessera) | Accounts, stores, sharing, OAuth, administration API, native backup scheduler. |
| Caddy | The only public listener (ports 80/443): TLS, certificates, routing. |
| Console (`cmd/console`) | The web interface. Go, server-rendered, no JavaScript, strict CSP. |
| Backup collector (`cmd/backupcollector`) | Retrieval-only credential; pulls completed backup bundles into an outbox. |
| Replicator (`cmd/backupreplicator`) | Optional; copies opaque bundles to an object store or another host. |
| `voptrack-init` (`deploy/`) | First run: password, invitations, owner device, CA, settings, recovery kit. |
| PostgreSQL or the native engine | Tessera's storage; both are supported and gated. |

## Identity and trust

- **Sign-in** is Tessera's OAuth (PKCE). The Console and the app are both OAuth clients: one account
  and password per person.
- **The Console reads with each person's own token** and administers through Tessera's API. It holds
  no credential able to read more than the signed-in person may. Aggregation across stores is a loop
  over what that person may read.
- **A personal store is private from everyone but its member**, including the owner. Each invitation
  creates the member's store, held by a sealed custodian account whose password is generated and
  discarded. Whoever runs the machine can still read the disk; encrypted storage protects the disk
  from others, not a member from the key holder.
- **Devices** join by invitation (`urn:tessera:params:oauth:grant-type:invitation`). A member who has
  joined on a device and set no password can be moved to a new phone by an owner-issued rebind
  invitation, so for such a member the owner can reach the store; setting a password from the app
  ends that. Invitations say so.
- **Study API** for the app: `GET /api/v1/studies/{slug}`, `POST …/join`, `GET`/`POST …/consent`,
  `POST …/withdraw`. Closed studies answer `410 study_closed`; stale consent versions `409 stale_version`.

## Discovery and local-network Homes

`/.well-known/voptrack` reports product, timezone, week start, whether a web interface exists and,
for local Homes, a `tls_pin`. A local-network Home makes its own CA root (EC P-256, 20 years) at
`init`; it is kept in the recovery kit, exported at `/.well-known/voptrack/ca.pem`, and the app
anchors on it. Links carry `pin=sha256/<base64 SPKI>`. Forward only ports 80 and 443 on a router;
the Console's own port (9443) is bound to localhost by default.

## The Console

Server-rendered Go with templates and a single stylesheet; charts are SVG rendered on the server. It
serves the Research study API in `cmd/console/api.go` for joining, consent and
withdrawal. Record protocol requests go to Tessera. `/connect-app` shows a QR
connect link containing server metadata and, for local TLS, a CA public-key pin;
it contains no credentials. Interface text lives in
`cmd/console` catalogues. A test requires every language to have every key. Research adds the study
and collection designer, participant administration, consent records, collection monitoring and exports.

## Backups and recovery

Tessera's native scheduler produces encrypted archives. Routine Console backup
retrieval and off-host replication do not receive the backup key. Host-exported
recovery kits contain sensitive unlocking material and must be kept separately
from archives; see the [exact kit paths](../release/README.md#recovery-material-and-kit-export).

1. **Collect.** The collector uses a retrieval-only credential to fetch completed bundles and
   evidence into an outbox. A local bundle alone does not establish host-loss protection.
2. **Copy.** Optionally the replicator copies the opaque bundles to an object store or another host
   and reads them back to verify bytes. Evidence of readback is not restore evidence.
3. **Recovery kit.** `init recovery-kit` and `take-backup` export the material needed to rebuild:
   committed kit, settings export, producer lineage. The kit and the backup key are kept apart.
4. **Preflight.** `voptrack-recovery-set` validates a selected set offline, restoring nothing.
5. **Guided recovery.** `voptrack-recovery` is a separate TLS application for a fresh host. It does
   not depend on the original server, Console or account database, and has no live-service authority.
6. **Assurance.** `release/recovery-assurance` runs the whole chain end to end as an acceptance runner.

Operator contracts: [release/off-machine](../release/off-machine/README.md),
[release/recovery-set](../release/recovery-set/README.md),
[release/guided-recovery](../release/guided-recovery/README.md),
[release/recovery-assurance](../release/recovery-assurance/README.md). A storage quota per store is
optional (`VOPTRACK_STORE_LIMIT_MB`); the server separately bounds memory per request.

## Vocabulary

`specs/voptrack-vocabulary-v1.json` and `specs/voptrack-tracks-v1.json` are **append-only**:
a changed entry retroactively alters what shipped data meant. `specs/check-vocabulary.py` enforces
this, and `make check` runs it. Format: [specs/voptrack-tracks-format.md](../specs/voptrack-tracks-format.md),
[specs/voptrack-vocabulary-v1.md](../specs/voptrack-vocabulary-v1.md).

## Repository layout

| Path | What |
| --- | --- |
| `cmd/`, `internal/` | Console, collector, replicator, recovery tools, `verify` (a deployment gate). |
| `deploy/` | Image build, example Compose and unit files, `voptrack-init`. |
| `release/` | The released Compose project, install guide, and a gate per feature. |
| `scenarios/` | Complete deployments with conformance gates. |
| `specs/` | Vocabulary, track catalogue, checks. |
| `ci/` | Local CI: `make ci`, `make ci-remote` on a Linux host. |

## Verification

Distribution uses GHCR with versioned installation and optional offline archives
on voptrack.app. [Human instructions](installation.md), the public
[agent runbook](install-agent.md) and the
[maintainer procedure](../release/distribution/README.md) describe that delivery.
Publication and fresh-host acceptance remain pending; packaging tests alone do
not qualify a public release.

Feature gates use disposable deployments and synthetic data. Their READMEs
record coverage, exclusions and defect-proof evidence; the presence of a gate
does not establish that it ran for the current source. `make check` runs source
checks; `make ci` runs the full suite on a clean committed tree. Select candidate
images explicitly for qualification.

## Upgrading Tessera

`make upgrade VERSION=vX.Y.Z` records the move in `go.mod`. Migrations are forward-only and there is
no downgrade: a server meeting a newer database refuses to start. Back up before and after, and treat
a rollback as a restore. Read Tessera's release notes first.
