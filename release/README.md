# Install and operate Voptrack

Run a Voptrack Compose installation, choose its mode, and manage settings,
backups and upgrades. Use the quick guide to find the detailed procedure.

The current source pins Tessera v0.1.0. Start with fresh storage for the first
stable release. Use the operations guide from the same source revision as the
installation.

For the GHCR and downloadable archive paths, use the
[installation guide](../docs/installation.md), including its public
[agent installation runbook](../docs/install-agent.md). The
[distribution procedure](distribution/README.md) defines the matching downloads
and remaining publication requirements. The source-transfer procedure below is
for development operators, not the public archive installation path.

## Quick guide

1. For a published bundle, use the [installation guide](../docs/installation.md).
   For authorized source development, follow [What you need](#what-you-need)
   and [Start it](#start-it) below.
2. [Choose the product and Console mode](#choose-how-to-install) before first start.
3. Use [Home](household/README.md) or [Research](research/README.md) for daily tasks.
4. Retain [backup and recovery material](#backups) off-host before relying on it.
5. Read [Moving to a new version](#moving-to-a-new-version) before upgrading.

## Reference map

- Configuration: [first start](#what-happens-on-first-start),
  [local-network trust](#on-your-own-network), [settings](#settings).
- Recovery: [kit export](#recovery-material-and-kit-export),
  [off-machine copies](#off-machine-copies), [fresh host](#recover-onto-a-fresh-host).
- Lifecycle: [upgrades](#moving-to-a-new-version), [data volumes](#your-data),
  [quotas](#a-full-store), [qualification limits](#what-this-has-not-been-shown-to-do).

The remaining sections are the detailed reference. Archive installations must
use `-f compose.offline.yaml` on every Compose command; setup installations
must include their matching overlay too.

## What you need

- Docker Engine with the Compose plugin on a Linux-compatible host, with enough
  disk for records, retained backups and temporary recovery copies.
- Encrypted storage beneath Docker's volumes. `VOPTRACK_STORAGE_ENCRYPTED=yes`
  attests that fact; it does not encrypt anything.
- For public access, an owner-approved domain pointing to the host and ports
  80/443 reachable for Caddy's public certificate. For a local-only Home, use
  [your own network](#on-your-own-network); public access is unnecessary.
- A matching qualified Voptrack image and Compose files. For transferred-image
  verification, the installation host also needs **Python 3** and the matching
  `image_identity.py` verifier. For the local-CA fingerprint recipe below, it needs
  the **OpenSSL command-line tool**. Neither is supplied by the host's Docker CLI.
- Until publication, a source operator with access to the pinned private
  dependencies, Go, Bash, make and Python 3 builds the image:

```sh
make image IMAGE=voptrack-server:latest
```

This is a source build, not a registry pull. Do not infer qualification from an
old local tag after a failed build. Record the actual image identity and version.

### Transfer an image to another installation host

Docker stores can rewrite image IDs on load. A matching tag or version alone
cannot prove runtime identity. On the source host, select the **qualified** image,
freeze its ID, and retain its full inspection alongside the archive:

```sh
umask 077
image_id="$(docker image inspect voptrack-server:latest --format '{{.Id}}')" || exit 1
docker image inspect "$image_id" > source-image.json || exit 1
docker save --output voptrack-image.tar "$image_id" || exit 1
```

If qualification used another tag/ID, substitute it in the first command. Deliver
the archive, `source-image.json` and the matching
[image identity verifier](recovery-set/image_identity.py) as `image_identity.py`
to the installation host over a trusted channel. Keep the files private.

On the installation host:

```sh
umask 077
docker load --input voptrack-image.tar || exit 1
```

Copy the **loaded image ID** printed by Docker into the following placeholder.
If Docker reports only a loaded tag, resolve that tag with
`docker image inspect <reported-tag> --format '{{.Id}}'` first.

```sh
loaded_image=sha256:REPLACE_WITH_LOADED_ID
docker image inspect "$loaded_image" > loaded-image.json || exit 1
python3 image_identity.py source-image.json loaded-image.json > image-equivalence.json || exit 1
```

Both input JSON files must be the **full arrays from `docker image inspect`**.
`image-equivalence.json` is the resulting hash-based receipt; it cannot replace
either inspection input. The verifier compares ordered filesystem layers and the
complete runtime configuration. Use the verified **loaded** ID as `VOPTRACK_IMAGE`
in the installation's `.env` below; it may differ from the source ID.

## Start it

Create an empty installation directory and copy `release/compose.yaml` from the
same source revision into it as `compose.yaml`. Do not use a development `main`
download as a substitute for the matching file. On release day this step will
be replaced with the published version's pull instructions.

Persist settings so every later command uses the same deployment:

```sh
umask 077
cat > .env <<'ENV'
COMPOSE_PROJECT_NAME=voptrack-home
VOPTRACK_IMAGE=sha256:REPLACE_WITH_QUALIFIED_LOCAL_IMAGE_ID
VOPTRACK_DOMAIN=home.example.org
VOPTRACK_STORAGE_ENCRYPTED=yes
VOPTRACK_VARIANT=household
ENV
```

Replace the placeholder with the qualified local image ID (resolve a local
build tag with `docker image inspect <tag> --format '{{.Id}}'`, or use the
verified loaded ID from the transfer procedure). Set the domain or IP, choose
the product/Console mode below, and review bind addresses and network ranges.
Use a unique stable project name and check which ports are already in use.

Keep `.env` with the recovery configuration; do not store passwords or invitation
links in it. For published bundles, use the installation guide instead: their
Compose file already pins its images, so do not override `VOPTRACK_IMAGE`.

For an IP address with a local certificate, follow the [network settings](#on-your-own-network)
before starting. Start the stack and wait for it to be ready.

```sh
docker compose up -d --wait </dev/null
```

Retrieve the initial password in a private terminal.

```sh
docker compose run --rm -T --interactive=false --no-deps init password </dev/null
```

Read the initial owner password privately; never put it in logs or support
reports. Open the configured HTTPS origin and sign in as **owner**. Fresh
installations use the owned website's single sign-in form; a separate OAuth scope
picker is not expected. An existing/disabled policy can retain the ordinary
approval flow. App authorization and study participation consent stay separate.
See [website sign-in](../deploy/website-sign-in/README.md) before customizing
client IDs or grants; custom IDs do not inherit the default data ceiling.

Home opens on **Home**; **Manage Home** leads to members/invitations and
server/backups. Research opens on **Studies**, with **Overview**, **Participants**,
**Exports** and **Study settings** inside a study, and **Server and backups**
globally. A fresh installation can have no shared records or participants.
The Console is `/` and Tessera's administration interface is `/admin`.
Point the app at that HTTPS origin. On a local Home, establish CA trust first or
join from the app using its pinned invitation; never disable TLS verification.

## Choose how to install

Set the mode in `.env` **before first start**. Product and Console mode cannot
be switched on an existing deployment.

| Installation | Settings in addition to domain, image and encryption | Next step |
| --- | --- | --- |
| Personal server | `VOPTRACK_VARIANT=standard` | Sign in; connect the app |
| Home with Console | `VOPTRACK_VARIANT=household` | [Invite members and choose sharing](household/README.md) |
| Home, local network only | `VOPTRACK_VARIANT=household`, local domain/TLS and LAN bind | [Local CA and pinned join](#on-your-own-network) |
| Home, private Console | `VOPTRACK_VARIANT=household`, `VOPTRACK_CONSOLE_PRIVATE=yes` | [Keep Console at home](#home-console-at-home-only) |
| Home, app only | `VOPTRACK_VARIANT=household`, `VOPTRACK_CONSOLE=off` | [Host invitation and owner device](#home-no-web-interface) |
| Home, protected first-run setup | Fresh Home plus matching setup overlay | [Host-authorized setup](#protected-first-run-home-setup) |
| Research | `VOPTRACK_VARIANT=research` | [Create a study and publish consent](research/README.md) |
| Research, private group | `VOPTRACK_VARIANT=research`, stable IP or private name, local TLS and interface bind | [Set up local HTTPS and phone trust](#on-your-own-network) |
| Native installation | Separate operator configuration | [Deployment guide](../deploy/README.md); systemd path remains unexecuted |

### Which is safest

The server itself, Tessera, must be reachable wherever the app should sync,
including away from home if remote sync is required. A local-only Home syncs only
while its address is reachable, directly or through an operator-managed VPN. The Console is a second web application,
with sign-in sessions of its own. Every person's access is still checked by
Tessera, but it is more surface to attack.

- **Personal server** and **Home** put both on the internet behind one
  certificate. This is the simplest option, and it is what the gates prove in
  full.
- **Home, Console at home only** removes the Console from the internet:
  - The public address answers the app and Tessera's sign-in pages, and
    nothing else.
  - The Console answers on a port of its own (9443 by default), published only
    on the address you choose.
  - Members use the web interface from home, or from away through a VPN into
    the home network. The app keeps syncing from anywhere.
  - This is the choice for a household that wants the smallest internet-facing
    surface without giving anything up in the app.
- **Research** cannot keep the Console private: people read a study and join it
  there. The Console refuses to start if asked to.

### What a Home's owner can do with members' tracks

In every Home installation, the owner can **stop** what a member shares with the
household, one track at a time. The owner can never read a member's store or
delete from it. The member sees who stopped which track and when, and can share
it again. The first-release web Console does not offer Stop track sharing controls;
the retired web routes return 404. The supported server capability remains
available through Tessera's administration API, as the owner:
`POST /admin/api/v1/unshare` with `{"types": [...], "datasets": [...]}`.

This is set when Home first starts and cannot be turned off later, so every
member joins under it. Tessera requires that people are told, so every
invitation (from the Console or from `init invite`) comes with a line for the
owner to pass on.

### Home, Console at home only

```bash
# Set these in .env before first start, with the chosen domain:
# VOPTRACK_VARIANT=household
# VOPTRACK_CONSOLE_PRIVATE=yes
# VOPTRACK_CONSOLE_BIND=192.0.2.10  # replace with the server’s LAN address
docker compose up -d --wait
```

- **`VOPTRACK_CONSOLE_BIND`** is the machine's own address on the home network.
  Without it, the Console's port is published on `127.0.0.1` only, which is
  reachable from the machine itself and from nowhere else.
- **Forward only ports 80 and 443 on the router, never 9443.** That, and the bind
  address, are what keep the Console off the internet.
- **Open `https://home.example.org:9443/`** from inside the home. The hostname
  must lead to the machine from inside: either the router sends its public
  address back in ("NAT loopback", which most do), or your home DNS (or a hosts
  file) resolves the name to the machine's home address. The certificate is
  the same one the public address uses.
- **Signing in** goes through Tessera's page on the public address and comes back
  to the Console's port. The gate proves that crossing, and that the public
  address no longer serves the Console.
- **Change the port** with `VOPTRACK_CONSOLE_PORT`. Set `VOPTRACK_CONSOLE_URL`
  only if people reach the Console by another name.

Not shown yet: this on a real home network with a real router and domain. The
gate runs it on `localhost`, where "the home network" is the loopback address.

### Home, no web interface

```bash
# Set VOPTRACK_VARIANT=household and VOPTRACK_CONSOLE=off in .env first.
docker compose up -d --wait
```

There is no Console web interface; Tessera still serves its authentication pages.
No unattended service on the server can read across members: there is no Home service and no Console store, and the gate checks
that no service account exists at all. The Voptrack app does everything,
including the owner's tasks.

The owner's app issues invitations with its own sign-in (no secret involved,
and signing that app out cancels the invitations it issued that nobody used).
From the host, one link at a time, also works:

```bash
docker compose run --rm init invite
```

The link is used once, and the person chooses their name and password. The
command also prints, on its own line, what to tell the person with it.
Decided
at first start: a Console switched on later would find neither the Home
service nor its store made, so init refuses the change.

### Protected first-run Home setup

Use a **new** project and empty volumes. Copy the matching
`release/home-setup/compose.yaml` alongside `compose.yaml` as
`home-setup.compose.yaml`. Keep the reviewed `.env` hostname, image and routing.
For local access, the default setup listener is loopback HTTPS on port 9673;
when changing it, set both `VOPTRACK_SETUP_PORT` and matching
`VOPTRACK_SETUP_ORIGIN`.

```sh
docker compose -f compose.yaml -f home-setup.compose.yaml up -d setup
umask 077
docker compose -f compose.yaml -f home-setup.compose.yaml run --rm -T setup-admin access > home-setup-access.json
```

Keep that single-use authorization file private. Compare its certificate
fingerprint with the browser before entering its code. Review the Home mode,
name, time zone and encryption attestation; choose the owner password; keep
initial recovery material outside this machine and apart from archives. An HTTP
download does not prove custody or readiness. After setup completes:

```sh
docker compose -f compose.yaml -f home-setup.compose.yaml up -d --wait
```

Use both files for subsequent lifecycle commands for this installation. Preserve
an interrupted setup's journal and partial volumes; never delete markers to force
startup or rerun initialization. [Setup operations](home-setup/README.md) define
reauthorization and stopping semantics. Research uses operator installation and
its study wizard instead.

## What happens on first start

Init generates configuration, the internal certificate and the owner in named
volumes. The server mounts configuration read-only and cannot read the owner's
bootstrap password. Caddy terminates the external HTTPS connection. Restart
retains identity and records; settings changes require supported controlled
reinitialization. Existing targets are not a fresh install.

## On your own network

For Home or a private Research group, set `VOPTRACK_DOMAIN` to a reachable,
stable IP address or a private name. Set `VOPTRACK_TLS=local` before first start.
Bind the published ports to the intended interface. For same-host evaluation,
use `localhost` and the loopback binding.

At first start the deployment creates a self-signed root. Caddy uses it to
issue the HTTPS certificate for the configured address. Keep the root and
private key in the recovery kit. Export the root for phone trust and keep
the private key on the server. The [IP address and phone guide](../docs/install/local-home.md)
walks through network binding, verified HTTPS, Safari trust and Home app
invitations. Keep using the generated Compose file for offline installations.

Home app invitations allow supported app builds to approve the root separately
from system trust. Replacing the owner's device signs out its previous devices.
A Home member's rebind link gives its holder access to the member's store until
the member sets a password. Keep device and rebind links private.

For open Research recruitment, use a public domain so participants can reach the
website without installing a root. A custom DNS provider or VPN certificate
setup needs its own Caddy configuration.

## Settings

| Variable | Meaning |
| --- | --- |
| `VOPTRACK_IMAGE` | For a source installation, the qualified local image ID. Published bundles already pin images; do not override them. |
| `VOPTRACK_PUBLISH_PREFIX` | Host bind address plus colon; unset publishes on all interfaces. Use a LAN address for local-only Home or `127.0.0.1:` for same-host qualification. |
| `VOPTRACK_DOMAIN` | The server's name. A public hostname gets a publicly trusted certificate. A local one (`localhost`, an IP address, `*.local`, `*.internal`, `*.home.arpa`) gets one from this server's own CA; see "On your own network". |
| `VOPTRACK_TLS` | `auto` (default) decides from the name; `local` forces this server's own CA (needed for a name such as `home.lan`); `public` forces a public certificate. |
| `VOPTRACK_STORAGE_ENCRYPTED` | `yes` is **your statement** that the disk is encrypted. It encrypts nothing itself. |
| `VOPTRACK_PUBLIC_URL` | Only when the public address is not `https://<domain>`, e.g. a non-standard port. Must be `https://`; a plain `http://` address is refused, because conformant clients refuse it too. |
| `VOPTRACK_HTTP_PORT`, `VOPTRACK_HTTPS_PORT` | Host ports, default 80 and 443. |
| `VOPTRACK_VARIANT` | `standard` (default), `household` or `research`. Fixed at first start. |
| `VOPTRACK_NAME` | What the app calls this server. Tessera's discovery carries it, so the app can show it before anyone signs in, always next to the address and never instead of it (anyone can claim any name). The Console's connect-the-app link uses it too. Optional; at most 160 bytes, no quotes or backslashes. Change it and restart. |
| `VOPTRACK_TIMEZONE` | The household's clock: the day boundary for periods ("today", "this month") in the web dashboards and the app alike, e.g. `Europe/Helsinki`. Default `UTC`. Checked at start; served to the app at `/.well-known/voptrack`. Change it and restart. |
| `VOPTRACK_CONSOLE` | `off` runs Home (or a personal server) with no web interface at all; the app does everything. Fixed at first start. |
| `VOPTRACK_CONSOLE_PRIVATE` | `yes` keeps the Console off the public address (Home and standard only). |
| `VOPTRACK_CONSOLE_BIND`, `VOPTRACK_CONSOLE_PORT` | Where the Console's own port is published: address default `127.0.0.1`, port default `9443`. |
| `VOPTRACK_CONSOLE_URL` | Only when people reach a private Console by a name other than `https://<domain>:<port>`. |
| `VOPTRACK_STORE_LIMIT_MB` | A storage quota per store, in MiB. **Unset (the default) or `none` means no quota.** With one, a store refuses new records past it, and each member sees how full theirs is. New stores take it; see "A full store" below. |
| `VOPTRACK_BACKUP_EVERY_HOURS`, `VOPTRACK_BACKUP_KEEP` | How often the server backs itself up while running, and how many archives to keep. Defaults 24 and 7. |
| `VOPTRACK_PROXY_SUBNET`, `VOPTRACK_PROXY_ADDRESS`, `VOPTRACK_PROXY_POOL` | Set all three consistently before first start if the network collides with the host, LAN, VPN or another Docker network. |
| `VOPTRACK_WEBSITE_SIGN_IN` | `auto` by default: selects the owned-website form on fresh Console installs and preserves existing policy on ordinary init reruns. `off` uses ordinary OAuth; `on` explicitly enables the policy. See [website sign-in](../deploy/website-sign-in/README.md) before changes. |
| `VOPTRACK_WEBSITE_CLIENT_ID`, `VOPTRACK_WEBSITE_DATA_GRANTS`, `VOPTRACK_WEBSITE_ADMINISTER` | Advanced owned-website identity and explicit ceiling. Leave unset for fresh-install defaults. A custom client ID with empty data grants provides no participant/member data access. |

## Backups

Routine backups stay online. Tessera schedules encrypted archives; the separate
retrieval-only collector publishes completed immutable archive/evidence bundles.
The owner/operator can request backups and download available bundles on the
Console's Backups page. Current-person authority and approval are required;
ordinary Console has no backup key, bootstrap secret or CA private material.
App-only Home uses host operations instead.

Keep the exact selected backup and retirement evidence, independently trusted
producer lineage, the matching committed recovery kit, separately retained key
and genuine settings export. A newer selected evidence bundle can exclude later
erasures from an older backup; it cannot cover future erasures. Never select
"newest" by filename or substitute another kit. Preserve compatible kit revisions
as configuration and service material change.

### Recovery material and kit export

Prepare the current kit in Console, request sensitive export, and compare the
request and exact revision with the host operator. The host grants a five-minute,
one-use approval before download. The separate privileged component reads the
material; ordinary Console never gains its mounts. See the kit/runtime contract
for the host approval procedure. Keep complete revisions off-machine, securely
apart from archives; a completed download does not prove that custody.

### Off-machine copies

The optional [host-only copy worker](off-machine/README.md) automates immutable
copying and full destination readback from the collector outbox. It receives no
record store, kit or recovery key. Its separate read-only fetch identity stages
selected bytes for recovery. Preserve upload obligations during outages; do not
silently change destinations or enable remote deletion/expiry.

Real AWS/IAM and independent destination durability remain unaccepted. Configure
an external destination only after the owner's separate destination, credential,
payload and budget approval. Local fixture success is not provider acceptance.
The [operations guide](../docs/backup-recovery-operations.md) explains stale,
unknown, degraded and full-queue status without inferring protection from a timestamp.

### Host maintenance fallback

The following is an offline fallback, not the routine backup path. A local file
never proves off-machine protection. The host operator exports
recovery material while both services are stopped. Stop **both**, including the
Console, then keep them stopped until each command and promotion succeeds:

```bash
umask 077
set -C
docker compose stop console tesserad || exit 1
if docker compose run --rm -T init take-backup > voptrack-backup.tar.partial; then
  ln voptrack-backup.tar.partial voptrack-backup.tar && rm voptrack-backup.tar.partial || exit 1
else
  rm -f voptrack-backup.tar.partial; exit 1
fi
if docker compose run --rm -T init recovery-kit > voptrack-kit.tar.partial; then
  ln voptrack-kit.tar.partial voptrack-kit.tar && rm voptrack-kit.tar.partial || exit 1
else
  rm -f voptrack-kit.tar.partial; exit 1
fi
docker compose up -d --no-deps --wait tesserad console caddy || exit 1
```

Use new output names for subsequent exports; promotion refuses to overwrite an
existing final file. A failed/interrupted command may leave partial bytes. Do
not restore from them or rename them as a completed bundle. Discard the partial
file, inspect the reason, and explicitly retry while services remain stopped.
Do not let the last restart command hide an earlier export failure.

`take-backup` creates a **new completed offline snapshot**, never chooses an
online archive by age/name/header/size, and pairs that exact snapshot with the
current retirement fence under a deployment maintenance lock. Running/competing
supported producers are refused. The fence is current during this quiesced
export; later erasures require a new current fence/export. Keep it with each
backup. The separate sensitive kit contains the unlocking key, current service
and bootstrap material and the local CA private material. Export it after changes
to configuration requests or that material and keep it securely apart from archives; it contains no changing
retirement fence. Copy the completed files safely off-machine yourself.

### Recover onto a fresh host

Preserve the source, all selected inputs and every prior attempt. Use a new,
private target; never restore over an existing or partial deployment. For a committed recovery set, supply
independently held lineage and key material, then run [recovery-set preflight](recovery-set/README.md)
on the exact selection immediately before restoring. Its receipt verifies inputs;
it does not certify latest evidence, restore completion or physical custody.

For the maintenance fallback above, use its matching **host-exported** kit and
offline snapshot with the same qualified image and reviewed settings, before
first start. This compatibility path is distinct from committed-set preflight:

```sh
docker compose run --rm -T --no-deps init restore-kit < voptrack-kit.tar || exit 1
docker compose run --rm -T --no-deps init restore < voptrack-backup.tar || exit 1
docker compose up -d --no-deps --wait tesserad console caddy || exit 1
```

A validated browser-exported committed kit instead uses
`docker compose run --rm -T --no-deps init restore-committed-kit < recovery-kit.tar`.
Do not pass an untrusted browser kit to `restore-kit`, or a Console bundle directly
to `init restore`. For selected committed bundles, use [preflight](recovery-set/README.md)
and [guided recovery](guided-recovery/README.md), with their supported staging and
activation steps. Review host-specific configuration
and attest encryption on the new host. A saved old attestation is insufficient.

Guided recovery is a separate host-authorized HTTPS application with staged
read-only inputs, private persistent state and a target that does not yet exist.
It has neither a Docker socket nor normal deployment volumes. Keep the source
stopped during the drill. Uncertain mutations stop; preserve partial targets and
journals, and obtain approval for a fresh attempt rather than clearing state.
Activation, collector enrollment and cutover remain explicit host actions.
Follow [recovery operations](../docs/backup-recovery-operations.md) and the protected
application's instructions; a browser retry must never replay a restoration.

## Moving to a new version

Apply the [App Review freeze and app compatibility policy](../docs/requests/server-compatibility.md)
before any app-facing upgrade: two weeks' notice, separate-test-server qualification
and explicit app-team confirmation. The frozen review demos remain on rc.4
until the owner explicitly ends the freeze; a final release does not override it.

Read release notes, retain backups and matching recovery material before the
upgrade, and assess older-backup compatibility. Migrations are forward-only:
rollback means restoring onto fresh supported storage, never running the older
binary against upgraded storage. Alpha data is not a stable-release input.

Use the new version's matching Compose file and explicitly reviewed image in
`.env`. Until publication, load/build it; after publication, pull the selected tag.
For a published registry image, first run `docker compose pull || exit 1`.
Do not pull an unpublished locally loaded image ID. Once image preparation has
succeeded, keep the same project name, product and retained configuration:

```sh
docker compose stop console tesserad || exit 1
docker compose run --rm -T --no-deps init || exit 1
docker compose up -d --no-deps --wait tesserad console caddy || exit 1
docker compose up -d --no-deps --force-recreate --wait caddy || exit 1
```

Stop the update on any image preparation or pull failure. The final step refreshes
Caddy's generated routing.
For setup installations include both Compose files. Applying reviewed backup
schedule/retention changes uses the same quiesce/init/start sequence; export
material before lowering retention because startup may rotate older archives.
Back up again after upgrading and verify recovery with the supported new version.

## Your data

Docker volumes belong to the Compose project (`voptrack` by default).
`docker compose down` keeps them; **`docker compose down -v` destroys records and
recovery material**. Never use it for an update or to retry an uncertain restore.
Process receipts bind launch configuration and become stale after exit; they do
not establish writer health, a next scheduled backup or hot reload.

## A full store

By default a store has **no quota**: it grows as long as the disk does, and the
Console and the app show only its size. If you set `VOPTRACK_STORE_LIMIT_MB`,
new stores refuse new records past that size (`507 dataset_full`), and each
member sees how full theirs is, with a warning from 80%. To change the quota of
a store that exists, on the host (`0` removes it):

```bash
echo '{"max_stored_bytes": 536870912}' > quota.json
```

Both producers hold the maintenance guard, so stop them for the moment the
change is made (the store id is on the owner's `/admin`):

```bash
docker compose stop console tesserad || exit 1
```

```bash
docker compose run --rm -T --no-deps -v "$PWD/quota.json:/tmp/quota.json:ro" --entrypoint /usr/local/bin/tesserad init \
  -config /var/lib/voptrack-config/server.json -set-dataset-limits <store id> -limits-file /tmp/quota.json || exit 1
```

```bash
docker compose up -d --no-deps --wait tesserad console caddy || exit 1
```

Separately, the server never holds more than 256 MiB in memory for any one
request, so one large request cannot run a small machine out of memory. That
is not a quota and nobody sees it.

The release gate connects to a literal loopback IP over verified HTTPS and checks
the advertised OAuth issuer at that address. It exercises certificate selection
for IP clients, which omit the TLS server name.

## What this has not been shown to do

- Install from public GHCR or an archive downloaded from voptrack.app, or run
  agent installation from an unauthenticated public GitHub URL. Distribution
  tooling and integrity tests do not establish publication or fresh-host readiness.

- Operate on a real domain with a publicly trusted certificate, including the
  app's own certificate handling; localhost gates use synthetic TLS.
- Enforce real AWS/IAM permissions or establish independent off-machine durability,
  separate-host recovery acceptance or physical recovery-material custody.
- Pass independent language review, real-device OAuth or screen-reader testing.
- Qualify arbitrary filesystem hard-link/flock semantics, production latency or
  scale, or provide an independent privacy audit. Current acquisition is bounded
  to at most 256 required content files; tagged release notes must be checked
  again before adoption.
- Supply an App Review demo or repeatable reviewer access. Public-host setup and
  reviewer invitation/reset behavior still require owner decisions.

A source
feature or existing gate does not establish fresh qualification of this guide.
