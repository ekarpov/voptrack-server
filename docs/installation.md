# Install using prepared Docker images

This path downloads a ready-made Home or Research server. You do not need Go,
source code or a GitHub account. Docker runs the server and keeps its data between
restarts. Use the small download to fetch images from GitHub Packages, or the
larger offline download to load the images from files.

Choose the exact published release for this installation. The download fixes
the image versions for you.
Start with fresh data. Alpha and release-candidate data are not supported inputs.

Use the same Bash terminal through the numbered steps so the chosen values stay
available.

## 1. Prepare the host

Follow [Prepare a new Ubuntu host](install/host.md). The prepared images target
Linux x86-64 (`amd64`). A build for ARM is not an ARM runtime qualification.
Check encrypted storage, the address, ports 80/443 and a free Docker network range.
Choose a domain for public HTTPS or a reachable IP for a private installation.
These commands assume one new installation on the host. For a literal IP,
choose a bundle whose Compose file includes the `default_sni` setting.

## 2. Download and check a release

Choose the release, product and download type. Use the exact tag from
[GitHub Releases](https://github.com/ekarpov/voptrack-server/releases).

```sh
set -eu
read -r -p 'Published release tag: ' version
read -r -p 'Product (home or research): ' product
case "$product" in home|research) ;; *) exit 1;; esac
read -r -p 'Download type (install or offline): ' kind
case "$kind" in install|offline) ;; *) exit 1;; esac
asset="voptrack-${product}-${version}-linux-amd64-${kind}.tar.gz"
base="https://github.com/ekarpov/voptrack-server/releases/download/$version"
```

Create a new directory and download the archive and its checksums.

```sh
umask 077
mkdir -m 700 "voptrack-${product}-${version}-download"
cd "voptrack-${product}-${version}-download"
curl --fail --location --proto '=https' "$base/SHA256SUMS" -o SHA256SUMS
curl --fail --location --proto '=https' "$base/$asset" -o "$asset"
```

Check the selected archive before opening it.

```sh
awk -v name="$asset" '$2 == name { print }' SHA256SUMS > selected.sha256
test "$(wc -l < selected.sha256)" -eq 1
sha256sum --check selected.sha256
```

Extract the verified archive and check the files inside.

```sh
tar -xzf "$asset"
cd "voptrack-${product}-${version}-linux-amd64"
sha256sum --check SHA256SUMS
```

Obtain checksums from that same trusted GitHub release. They detect damaged files;
they do not protect against someone taking over the release account. Keep the
download, `release.json`, notices and checksum files for recovery.

## 3. Set your address

The download already fixes Home or Research and the exact image to run. Do not
change its image or product. Use a new project name and fresh volumes.
Enter a hostname or IP without `https://`. Use a public domain for public HTTPS
or an IPv4 address with a local certificate.

```sh
read -r -p 'Domain or stable IPv4 address: ' address
read -r -p 'Certificate type (public for a public domain, local for an IP): ' tls
case "$tls" in public|local) ;; *) echo 'Choose public or local'; exit 1;; esac
read -r -p 'Server name: ' server_name
read -r -p 'Time zone, for example Europe/Helsinki: ' timezone
```

After checking that Docker stores its data on encrypted storage, save the settings.

```sh
cp .env.example .env
chmod 600 .env
python3 - "$product" "$address" "$tls" "$server_name" "$timezone" <<'PY'
from pathlib import Path
import sys
product, address, tls, name, zone = sys.argv[1:]
for value in (address, name, zone):
    if not value or any(c in value for c in '\r\n$#"\\'):
        raise SystemExit('Use nonempty plain values without quotes, backslashes, $ or #')
Path('.env').write_text(f'COMPOSE_PROJECT_NAME=voptrack-{product}\nVOPTRACK_DOMAIN={address}\nVOPTRACK_TLS={tls}\nVOPTRACK_NAME={name}\nVOPTRACK_TIMEZONE={zone}\nVOPTRACK_STORAGE_ENCRYPTED=yes\n')
PY
```

Set the last line to `yes` only after confirming encrypted storage beneath Docker.
If you chose `local`, follow the network settings in the
[IP address and private certificate guide](install/local-home.md) before starting.
Keep Research’s Console enabled and reachable by its participants. For open
recruitment, use a public domain.
Home's web Console is on by default; the optional private or app-only modes must
be chosen before first start using the [operations guide](../release/README.md#choose-how-to-install).

## 4. Start the server

### From GitHub Packages

For the small `install` download, select the Compose file and check it.

```sh
compose_file=compose.yaml
sudo docker compose -f "$compose_file" config --quiet </dev/null
```

Download the images listed in the file.

```sh
sudo docker compose -f "$compose_file" pull </dev/null
```

Start the server and wait for it to become ready.

```sh
sudo docker compose -f "$compose_file" up -d --wait </dev/null
```

No registry login is required for the public images. If a pull is denied, check
release availability; do not request private credentials or use an unrelated tag.

### From the offline download

The offline download includes both Voptrack and Caddy. It needs no registry access
for loading or starting. A public HTTPS address still needs internet access for
certificate issuance.

Verify and load the images in the archive.

```sh
sudo python3 load.py
```

Select the generated Compose file and check it.

```sh
compose_file=compose.offline.yaml
sudo docker compose -f "$compose_file" config --quiet </dev/null
```

Start using the loaded images.

```sh
sudo docker compose -f "$compose_file" up -d --wait --pull never </dev/null
```

The loader checks the files, host type and loaded images before writing the
startup file. Keep `image-equivalence.json`. Use `compose.offline.yaml` on **every**
Docker Compose command for this installation, including after a new login shell:

```sh
compose_file=compose.offline.yaml
```

Do not use the online file by mistake or change the no-pull setting. If loading
fails, keep the existing files and installation for inspection. Do not delete data.

## 5. Check and sign in

Check the services using the `compose_file` selected above.

```sh
sudo docker compose -f "$compose_file" ps </dev/null
```

For a public certificate, check both HTTPS endpoints. For a local certificate,
export the root, verify HTTPS and set up phone trust using the
[IP address guide](install/local-home.md#2-start-the-server-and-export-its-certificate).

```sh
curl --fail --silent --show-error "https://$address/.well-known/tessera"
curl --fail --silent --show-error "https://$address/.well-known/oauth-authorization-server"
```

Retrieve the initial password in your private terminal.

```sh
sudo docker compose -f "$compose_file" run --rm -T --interactive=false --no-deps init password </dev/null
```

Open your HTTPS address and sign in as `owner`. Home opens Home; use **Manage
Home** for invitations and backups. Research opens Studies; create a study,
publish its information and explicitly open joining when ready. Participants
must give study consent; researcher sign-in does not test participant access.
Neither product offers Saved views in this first release.

Connect the app and check access with the actual app version you will use. Never
paste passwords, invitation links, tokens or backup keys in a public issue or
agent chat. Agent installation instructions are in [the image runbook](install-agent.md),
also included as `INSTALL-AGENT.md`.

## 6. Keep it running and keep a recovery copy

From the installation directory, test a normal stop and start:

```sh
sudo docker compose -f "$compose_file" stop </dev/null
sudo docker compose -f "$compose_file" up -d --wait </dev/null
```

Keep the same project name, `.env`, Compose file and volumes. Arrange a host
restart when it will not interrupt other work. Save backups, recovery material
and a separately kept key away from the server; test recovery into a fresh target.
Follow the [backup and recovery guide](../release/README.md#backups) at the source
revision in `release.json`. A copy on this host alone does not protect against
losing the host. Keep `licenses.tar.gz` when redistributing an image or bundle.

Before upgrades, keep a verified backup, read the release notes and use matching
new files. Migrations only go forward; rollback means restoring a compatible
backup. `docker compose down` retains volumes; **`down -v` deletes them**.

## What has been tested

The release report records exact source, image and gate results. Synthetic checks
cover the deployment and recovery flows. They do not prove public DNS/TLS on your
host, physical backup custody, native ARM behavior or compatibility with every
phone. Report the checks you actually performed; do not claim an unrun test passed.
