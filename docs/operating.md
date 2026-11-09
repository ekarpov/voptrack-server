# Running a Voptrack server

This is the manual Tessera path for a separately assembled deployment. For the
Voptrack product's Console, scheduled backups and supported maintenance, use the
[operations reference](../release/README.md). The native/systemd installation
path remains unexecuted; this guide is not qualification evidence.

## Quick guide

1. Prepare the toolchain, encrypted data storage and TLS material below.
2. Configure, provision with the server stopped, then start it: [First run](#first-run).
3. Read the authority differences before [adding people and services](#adding-people-and-services).
4. Design and verify recovery before relying on [native backups](#backups).

Prerequisites: Go 1.27.0 (pinned in `mise.toml`), CGO and a C compiler for SQLite, a TLS certificate
and key, and an encrypted volume for the data directory. The server refuses to start unless the
configuration confirms that last one — the flag records an operator's promise, it does not encrypt
anything.

## First run

**1. Write the configuration.** It is not in git, because a committed configuration is one somebody
runs unchanged.

```sh
cp deploy/server.example.json deploy/server.json
```

Edit `issuer` (an HTTPS origin, no path), `listen`, `data_dir`, and the TLS certificate and key
paths. Set `encrypted_storage_confirmed` only when the volume actually is encrypted.

**2. Provision the first administrator, with the server stopped.** The engine holds an exclusive
lock, so a provisioning command beside a running server is refused with `dataset_unavailable` —
this is not a race to lose, it simply will not run.

```sh
umask 077
printf 'a password of at least twelve bytes' > /secure/initial-password
go tool tesserad -config deploy/server.json \
    -add-person owner -grants read,write,schema,erase \
    -password-file /secure/initial-password
```

An administrator must be a **person** principal entitled to erase. A service principal is refused
from the administration surface, because administering is an interactive act.

**`-grants` is not optional here, though it looks it.** It defaults to `read,write`, so an account
provisioned without it holds no erase, is therefore not an administrator, and is refused at `/admin`
with `401` — having been created successfully, with no indication anything is wrong until you try to
sign in. Earlier Tessera versions granted everything and this flag did not exist; a runbook written
against one of those produces an account that cannot administer.

**3. Start it.**

```sh
make serve
```

For a real deployment run it under the unit in `deploy/`, which confines the process to its own
user, drops every capability, and gives it read-only access to its configuration and TLS key.

**4. Open `/admin`** over HTTPS and sign in as that account.

## What the administration surface gives you

| Page | Shows |
| --- | --- |
| `/admin` | Dataset identity and replacement, engine, watermark, schema and view versions, content store, every capability as advertised or withheld, and the declared bounds |
| `/admin/principals` | Accounts and services with their grants and live authorization counts, and registered clients as public or confidential |
| `/admin/authorizations` | One row per installation: its grants, live token count and state, and the actions below |
| `/admin/schema` | Effective metadata — inheritance, fields and who declares them, roles and annotation keys |

**Withdrawing grants and revoking credentials are different actions, and the distinction matters.**
Withdrawal removes dataset access and leaves the credentials authenticating, so the holder can still
recover the outcome of a write it made while it could. Revocation ends the credentials themselves.
Neither touches another installation of the same account.

Every mutation spends a single-use CSRF token, so a resubmitted form cannot act twice. No page can
print a password, a service secret or a token value — those are not stored in a form the pages can
read. The surface reads the dataset through a handle carrying `read` only, so administration cannot
write, change schema or erase, and no record value appears on any page.

## Adding people and services

Each command needs the server stopped, for the same lock reason as the first run.

```sh
go tool tesserad -config deploy/server.json \
    -add-person alice -grants read,write,schema -password-file /secure/alice
go tool tesserad -config deploy/server.json -add-service importer -secret-output /secure/importer
go tool tesserad -config deploy/server.json -add-token alice -secret-output /secure/alice-token
```

`schema` is there because a Voptrack client defines its own types over the protocol; without it the
app connects and then cannot create the types it needs. `erase` is deliberately absent — give it
only to someone who should be able to destroy the dataset, which is also what makes them an
administrator.

A service is its own principal with its own grants and no interactive sign-in. An automation token is
long-lived and separately revocable, and **never carries erase** whatever its person holds, so an
administrator's automation token cannot administer. Grants otherwise intersect with the person's,
which is why a person granted only `write` yields a write-only token.

Once a server holds more than one dataset, every one of these needs `-dataset` naming which — a
storage with one has a single possible answer, and one with several has no default worth guessing.

## Backups

This section covers the native offline CLI backup, which requires the server
stopped. The product also has an online scheduler; use its supported
[collection and recovery paths](../release/README.md#backups) for Home or Research.

`deploy/tesserad-backup.example.sh` keeps the dedicated native service stopped
through snapshot and retirement-fence capture, publishes evidence before the
archive, then restarts and confirms the service before retention. Failures are
nonzero; prior pairs are not pruned for an export/restart failure. See the
[native wrapper requirements and failure behavior](../deploy/README.md#backups)
before scheduling it. It is not the product's bound recovery export.

**Restoring needs three things, not two.** The archive, the backup key, and the **retirement
fence** in the data directory. `tesserad -restore` reads it and refuses without it; verified, and
the refusal is `dataset_unavailable`, which names the wrong thing and will not tell you what is
missing.

**The fence's filename depends on the engine**: `dataset.db.retired` on SQLite and
`postgres-fence.retired` on PostgreSQL. This section named only the first until
`deploy/verify-postgres.sh` ran, and the consequence was not a confusing error — the example backup
script tested for the SQLite name and skipped the copy when it was absent, so a PostgreSQL
deployment backed itself up successfully every night and kept nothing it could restore from.

```sh
tesserad -config /etc/tessera/server.json -restore ARCHIVE \
    -backup-key-file KEY -retirement-fence /var/lib/tessera/dataset.db.retired
#                                                          postgres-fence.retired on PostgreSQL
```

It restores into a **fresh** data directory and refuses to overwrite a live one, so point the
configuration at a new path. On PostgreSQL the **database** must be fresh too, and Tessera's own
change list says disposable rather than merely fresh: a restore refuses a database that already
holds a deployment, so a second restore into the same one fails where the first succeeded. Both
halves are asserted by the gate.

The fence is an independent ledger of what has been retired, and it exists so that a snapshot of a
dataset somebody has since erased cannot be brought back. That only works if the fence is
**current**: restoring with one older than the truth can resurrect a retired dataset, which is the
thing it prevents. The example now copies the fence while the service is
stopped, beside the exact snapshot. This copy covers erasures through capture,
not future erasures after restart. Preserve current supported retirement evidence;
do not assume an old archive-side copy remains current.

Copy all three off the machine, and not to the same place. The archive is encrypted in process and
the key is the only way back.

**A deployment with nobody provisioned cannot be backed up.** The archive includes `identity.json`,
which does not exist until the first person or service is added, and the attempt fails with
`dataset_unavailable`. Provision the administrator before scheduling the job, or the first night
will fail for a reason the message does not give.

## Upgrading

Read the exact target version's release notes and assess backup compatibility
before changing the pin. Historical alpha migration notes do not describe the
current pin's complete behavior. Retain verified recovery material before and
after upgrading.

```sh
make version                       # which Tessera this deployment runs
make upgrade VERSION=v0.2.0        # deliberate, and recorded in go.mod
```

Migrations are forward-only and run at startup. **There is no downgrade**: a server meeting a
database from a newer version refuses to start and leaves the schema untouched, so rolling the
version forward again recovers the deployment. Plan a rollback as "restore the backup taken before
the upgrade", never as "pin the old version", and take the backup first.

Tessera is pre-release. Read its release notes before moving — behaviour may change between
versions, and `make version` is what tells you where you are.

## What this server does not do

It holds records and answers queries about them. It does not review them, decide whether they are
true, move them across an ownership boundary, or synchronise anything. It never reads Voptrack's
vocabulary: roles and annotations are stored and returned, never interpreted. If a question is about
what a server must do, it is answered in Tessera; if it is about what this deployment runs, here.
