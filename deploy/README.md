# Low-level deployment

Use this guide when assembling Tessera manually. For the complete Voptrack
product, use [installation](../docs/installation.md) or the source-based
[operations reference](../release/README.md). Public distribution remains pending.

## Quick guide

| Path | Purpose and status |
| --- | --- |
| Product Compose | Preferred Voptrack shape: [matching image and Compose](../release/README.md) |
| Native/systemd | Example files only; the installation path has not been executed |
| Custom PostgreSQL Compose | Low-level server configuration; see [Containers](#containers) |
| Kubernetes | No chart or qualified recipe; single-writer constraints apply |

For manual first provisioning, see [Tessera operations](../docs/operating.md).
TLS, encrypted storage, permissions and recovery remain operator responsibilities.

## A single binary under systemd — never executed

This path is unqualified. Review `deploy/tesserad.service` and configure the
host before enabling anything. Required steps are:

1. Build the pinned dependency, not a local `cmd/tesserad`:

   ```sh
   mkdir -p build
   go build -o build/tesserad github.com/ekarpov/tessera/cmd/tesserad
   ```

2. Install it as `/usr/local/bin/tesserad`, create the unprivileged `tessera`
   service user and a private `0700` data directory at `/var/lib/tessera`.
3. Adapt `deploy/server.example.json` to `/etc/tessera/server.json`: HTTPS issuer,
   listener, data directory, TLS paths and confirmed encrypted storage.
4. Install credential files as `0600`, owned by the service user. A root-owned
   key or `0640 root:tessera` is insufficient. Verify directory traversal access.
5. Provision the administrator while the server is stopped, following the manual
   guide. Install/review the service unit before starting it.

The unit drops capabilities and limits writes to its state directory. It cannot
bind privileged port 443; use a reverse proxy or a high listener port. Offline
provisioning takes the server's exclusive database lock. Do not run it beside a
live server or bypass `dataset_unavailable`.

## Containers

Build from the repository root after fetching dependencies:

```sh
make image IMAGE=voptrack-server:latest
```

[Source setup](../docs/development.md#build-from-source) covers dependency access.
`deploy/modproxy.sh` supplies a temporary proxy from the host Go cache; no SSH
key is passed to Docker. Never load a key into a shared ssh-agent for this build.
Tessera is built from `go.mod`'s pin, while Console and helpers are built from
this repository. SQLite needs CGO and a C compiler in the build stage.

A failed build may leave an older tag. Verify the actual candidate's version
and image identity before installing or running gates.

`deploy/compose.example.yaml` is a custom PostgreSQL example, separate from the
product Compose project. It needs host configuration, TLS material, a database
password file, DSN and writable data directory. Set both configuration fields:

```json
  "engine": "postgres",
  "postgres_dsn_file": "/etc/tessera/postgres.dsn"
```

Supply the database password through its file/secret mount. The TLS key and DSN
must be private regular files with mode `0600`. Match the container UID/GID to
the files' owner using `VOPTRACK_UID` and `VOPTRACK_GID`; defaults are `10001`.
The bind-mounted data directory must be writable by that identity and mode `0700`.
Linux enforces this ownership even where Docker Desktop may hide a mismatch.

The example drops capabilities, uses a read-only root filesystem and mounts
configuration/credentials read-only. SQLite requires removing the database
service/dependency and selecting `engine: sqlite`; it is a custom adaptation.

## Kubernetes

There is no chart or qualified installation recipe. The engine is a single
writer with an exclusive lock. Never overlap old and new writers during a
rollout; a second process can fail with `dataset_unavailable`. Storage, rollout,
identity, TLS and restore procedures need separate design and validation.

## Backups

For the product's routine online scheduler, collector and supported recovery
paths, use [Backups](../release/README.md#backups).

`deploy/tesserad-backup.example.sh` is a low-level **offline** systemd example.
It now keeps the service stopped until both the snapshot and retirement evidence
are captured and published. It restarts the service before pruning old complete
pairs, and reports stop, export, restart or retention failure through a nonzero
exit. This is a native archive/fence pair, not a product recovery bundle or kit.

Run it only for a dedicated native deployment with the configured service active
and no other producer, provisioner or external restart controller. It refuses an
inactive service rather than unexpectedly starting one. `systemctl show` must
confirm the stop and the restart. The service must stay stopped throughout the
capture; locking the output directory does not exclude independently managed
writers or a second job using another destination.

The host needs GNU coreutils (including `ln -T`), `flock` from util-linux and a
private destination supporting same-filesystem hard links. For example, after
reviewing the service, configuration and encrypted storage:

```sh
TESSERA_CONFIG=/etc/tessera/server.json \
TESSERA_BACKUP_KEY=/etc/tessera/backup.key \
TESSERA_DATA_DIR=/var/lib/tessera \
TESSERA_BACKUP_DIR=/var/backups/tessera \
TESSERA_SERVICE=tesserad.service \
TESSERA_BACKUP_KEEP=14 \
sh deploy/tesserad-backup.example.sh
```

Run as an identity permitted to control that service and read the protected
files. Never put the key itself into an environment variable. Match the data path
to the configuration. Keep service diagnostics private.

Output is the completed archive path; its evidence is `<archive>.retired`, both
mode `0600`. Evidence is linked first and the archive last without overwriting
existing paths. Same-destination jobs are serialized by `.backup.lock`; keep that
file in place. A same-second name collision refuses before stopping the service.
Handled failures/signals remove private staging and attempt restart; a failed
restart is reported, preserves completed output and prevents retention. SIGKILL
or host failure can still leave staging, an orphan evidence file or a stopped
service: inspect these and restore service explicitly, never promote partials.
Publication order alone is not a power-loss/fsync durability guarantee.

Retention defaults to 14 complete pairs, ordered by UTC filename. `KEEP` must be
1–999999 with no leading zeros. Incomplete exports and symlinks are preserved;
the just-produced pair is retained even after clock rollback. Retention begins
only after complete publication and confirmed restart. A retention error fails
the run and leaves the new pair available; it does not roll back earlier pruning.

The fence covers erasures up to the stopped capture, not erasures after restart.
Keep current retirement evidence independently, retain the key separately and
verify restoration; a successful local export alone is not host-loss protection.

A native restore requires the archive, separate key and current retirement
fence: `dataset.db.retired` for SQLite or `postgres-fence.retired` for PostgreSQL.
A stale fence can resurrect erased data. Preserve current evidence and use fresh
storage; a PostgreSQL restore also needs a disposable database. See
[manual backup details](../docs/operating.md#backups) for scope and limitations.

## Upgrading

Read the exact target version's release notes, retain verified recovery material
before and after upgrading, and assess older-backup compatibility. Migrations
are forward-only. Rollback means restoring onto fresh supported storage; never
run an older binary against an upgraded database. Product installations follow
the [upgrade procedure](../release/README.md#moving-to-a-new-version).

## Native backup wrapper coverage

`python3 -m unittest discover -s deploy -p test_native_backup.py` executes the
actual shell wrapper with synthetic systemctl/Tessera and filesystem-failure
controls. It covers SQLite/PostgreSQL fence selection, missing/ambiguous evidence,
stop confirmation, inactive/unreadable service state, archive/copy/permission/
publication errors, no-clobber output, invalid retention, safe pair pruning,
newline-containing paths, clock rollback, same-destination exclusion, INT/TERM
cleanup and visible
restart failure. Planted early-restart and hidden-restart-failure variants must
violate those assertions. These tests are also selected by `make check`.

`make backup-check` now takes the real pinned Tessera SQLite snapshot through
this wrapper and restores from its published archive and copied fence. It also
checks missing-fence refusal, startup and administrator access on the restored
isolated target. Only systemd control is simulated; no host service is stopped.

What this does not cover: actual systemd installation/restart behavior, a wrapper
restore on PostgreSQL (the separate `make postgres-check` tests native engine
restore), independently managed writers, power loss/fsync, SIGKILL recovery,
arbitrary filesystems, off-host custody or product recovery-kit acceptance. The
systemd installation recipe remains unexecuted. This shell-only change is not
part of the application image, so no image qualification is claimed.

Local validation on 2026-11-06: all 20 deployment Python tests passed (17 native
wrapper tests and three PostgreSQL cleanup tests), including the planted-defect
controls. `make backup-check` passed with Tessera v0.1.0-rc.4: wrapper snapshot,
copied-fence restore, restored startup and administrator access. Shell syntax,
local documentation links and diff whitespace also passed. No real systemd
service or PostgreSQL wrapper restore was exercised.
