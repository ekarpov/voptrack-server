# Install by cloning and building

This path downloads the public Voptrack source and builds the server on your own
host. Docker then runs Home or Research. Both this repository and Tessera are
public; you need no GitHub account or private repository credentials.

The commands below are for a fresh Linux x86-64 host. They use a selected release
so its source, settings and guides stay together. Choose a published release tag
for installation or an exact reviewed commit for a source trial. Start with
fresh storage.

Use the same Bash terminal through the numbered steps so the chosen values stay
available.

## 1. Prepare the host

Follow [Prepare a new Ubuntu host](install/host.md). Confirm encrypted storage,
your address, ports and free network range. Then install the build tools:

```sh
sudo apt-get update
sudo apt-get install -y git make gcc libc6-dev python3 curl ca-certificates
```

If Go 1.27.3 is already installed, skip to the next section. Otherwise, download
the checksum list and select the entry for the Linux x86-64 archive.

```sh
set -eu
build_tools=$(mktemp -d)
curl -fsSL 'https://go.dev/dl/?mode=json&include=all' -o "$build_tools/go.json"
python3 - "$build_tools/go.json" > "$build_tools/go.sha256" <<'PY'
import json, sys
release = next(r for r in json.load(open(sys.argv[1])) if r['version'] == 'go1.27.3')
f = next(f for f in release['files'] if f['filename'] == 'go1.27.3.linux-amd64.tar.gz')
print(f['sha256'] + '  go1.27.3.linux-amd64.tar.gz')
PY
```

Download the Go archive and check its hash before extracting it.

```sh
curl -fsSL https://go.dev/dl/go1.27.3.linux-amd64.tar.gz -o "$build_tools/go1.27.3.linux-amd64.tar.gz"
(cd "$build_tools" && sha256sum --check go.sha256)
```

Install Go under your user directory and check that this terminal can find it.

```sh
mkdir -p "$HOME/.local/voptrack-build-tools"
test ! -e "$HOME/.local/voptrack-build-tools/go"
tar -xzf "$build_tools/go1.27.3.linux-amd64.tar.gz" -C "$HOME/.local/voptrack-build-tools"
export PATH="$HOME/.local/voptrack-build-tools/go/bin:$PATH"
go version
```

Keep the
`export PATH=...` line in each terminal used for building. An ARM source build
needs its own Go archive and runtime checks; it is outside this x86 recipe.

## 2. Clone a chosen version and build

Run in Bash. Enter the exact published tag, or a reviewed full commit for a trial:

```sh
set -eu
read -r -p 'Release tag or reviewed commit: ' version
git clone https://github.com/ekarpov/voptrack-server.git
cd voptrack-server
git checkout --detach "$version"
git rev-parse HEAD
```

Download the public dependencies and check the Tessera version in the source.

```sh
go mod download
make version
```

Build the image and check the version inside it.

```sh
sudo env "PATH=$PATH" make image IMAGE=voptrack-source:local </dev/null
sudo docker run --rm --network none --tmpfs /var/lib/tessera --entrypoint /usr/local/bin/tesserad voptrack-source:local -version </dev/null
```

The last two version checks must say `v0.1.0`. The build downloads dependencies
on the host and copies only the needed files into Docker. No credential is put
in an image. A failed build is a stop; do not use an older image with the same name.

## 3. Configure Home or Research

Choose a product and address. Enter a hostname or IP without `https://`. Use a
public domain for public HTTPS or a stable IPv4 address with a local certificate.

```sh
read -r -p 'Product (home or research): ' product
case "$product" in home) variant=household;; research) variant=research;; *) exit 1;; esac
read -r -p 'Domain or stable IPv4 address: ' address
read -r -p 'Certificate type (public for a public domain, local for an IP): ' tls
case "$tls" in public|local) ;; *) echo 'Choose public or local'; exit 1;; esac
read -r -p 'Server name: ' server_name
read -r -p 'Time zone, for example Europe/Helsinki: ' timezone
read -r -p 'Fresh installation directory (absolute path): ' install_dir
case "$install_dir" in /*) ;; *) echo 'Use an absolute path'; exit 1;; esac
```

Create the installation directory and copy the Compose file from your checkout.

```sh
mkdir -m 700 "$install_dir"
cp release/compose.yaml "$install_dir/compose.yaml"
git rev-parse HEAD > "$install_dir/source-revision.txt"
```

Pin the built image and selected product in the copied Compose file.

```sh
image_id=$(sudo docker image inspect --format '{{.Id}}' voptrack-source:local)
python3 - "$install_dir/compose.yaml" "$variant" "$image_id" <<'PY'
from pathlib import Path
import sys
p=Path(sys.argv[1]); s=p.read_text()
s=s.replace('image: ${VOPTRACK_IMAGE:-voptrack-server:latest}', 'image: '+sys.argv[3]+'\n  pull_policy: never')
s=s.replace('VOPTRACK_VARIANT: ${VOPTRACK_VARIANT:-standard}', 'VOPTRACK_VARIANT: '+sys.argv[2])
p.write_text(s)
PY
```

After checking that Docker stores its data on encrypted storage, save the settings.

```sh
cd "$install_dir"
umask 077
printf 'COMPOSE_PROJECT_NAME=voptrack-%s\nVOPTRACK_DOMAIN=%s\nVOPTRACK_TLS=%s\nVOPTRACK_NAME=%s\nVOPTRACK_TIMEZONE=%s\nVOPTRACK_STORAGE_ENCRYPTED=yes\n' "$product" "$address" "$tls" "$server_name" "$timezone" > .env
chmod 600 .env
```

Use simple names without newlines, dollar signs or quote characters in this
copy-and-paste example. Set `VOPTRACK_STORAGE_ENCRYPTED=yes` only after checking
the host's storage. The product is fixed on first start. If you chose `local`, follow the network
settings in the [IP address and private certificate guide](install/local-home.md)
before starting. For open Research recruitment, choose a public domain.

## 4. Start and check

Check the Compose file, then start the server and wait for it to become ready.

```sh
sudo docker compose config --quiet </dev/null
sudo docker compose up -d --wait </dev/null
sudo docker compose ps </dev/null
```

For a public certificate, check both HTTPS endpoints. For a local certificate,
export the root, verify HTTPS and set up phone trust using the
[IP address guide](install/local-home.md#2-start-the-server-and-export-its-certificate).

```sh
curl --fail --silent --show-error "https://$address/.well-known/tessera"
curl --fail --silent --show-error "https://$address/.well-known/oauth-authorization-server"
```

If startup fails, keep the files and volumes for inspection.

Retrieve the initial password in your own terminal:

```sh
sudo docker compose run --rm -T --interactive=false --no-deps init password </dev/null
```

Open your HTTPS address and sign in as `owner`. Home opens Home and **Manage
Home**; Research opens Studies. Connect the app and, for Research, check a
participant's join flow separately. Do not post passwords or invitation links in
issues or chat. Save your data and recovery material away from this host before
relying on it; follow the [backup steps](../release/README.md#backups).

## Stop or restart

From the installation directory:

```sh
sudo docker compose stop </dev/null
sudo docker compose up -d --wait </dev/null
```

Keep `.env`, `compose.yaml` and the volumes. Never use `down -v` unless you intend
to delete the installation. Before rebuilding for an upgrade, keep a working
backup and recovery material and read that release's notes. Data changes only
move forward; going back means restoring a compatible backup.

For an agent, use [the source installation runbook](install-source-agent.md).
