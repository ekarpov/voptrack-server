# Build and develop Voptrack

Use this guide for a source checkout and disposable synthetic fixtures. For an
installation from a published bundle, use [installation](installation.md).
Read [repository rules](../AGENTS.md) before changing code.

## Quick guide

1. Install the toolchain below and fetch the pinned dependencies.
2. Build with `make image IMAGE=voptrack-server:latest`.
3. Choose a protocol, Home or Research fixture under [Development](#development).
4. Run the checks appropriate to the change; see [Checks](#checks).

## Build from source

Use Go **1.27.0** (pinned in `mise.toml`), CGO with a C compiler for SQLite, Bash,
make and Python 3. Image builds also need Docker with BuildKit and Compose.
On Windows, run repository commands in Ubuntu WSL; Go is at `/usr/local/go/bin`.

```sh
git clone https://github.com/ekarpov/voptrack-server.git
cd voptrack-server
```

Both this repository and Tessera are public. No GitHub account, SSH key or
private-module setting is needed to fetch the pinned source dependencies.

Build the deployment image:

```sh
go mod download
make version
make image IMAGE=voptrack-server:latest
```

`make image` gathers dependencies from the host's Go module cache into a temporary
proxy, builds the image and prints the image's Tessera version. No SSH key is passed
into Docker. A failed build can leave an older tag; verify the actual image identity
before installation or qualification.

To build native binaries:

```sh
mkdir -p build
go build -o build/tesserad github.com/ekarpov/tessera/cmd/tesserad
go build -o build/voptrack-console ./cmd/console
```

The server is a pinned dependency, not a local `cmd/tesserad`. See
[deployment details](../deploy/README.md) for native configuration; the systemd installation
path remains unexecuted.

## Development

Use synthetic data only. For client protocol integration without the full product stack:

```sh
make dev-content
```

This starts Tessera at `http://127.0.0.1:8081` with attachments enabled. The bearer token
is written to `.tessera-dev/token`; use its path, never put its value in logs or URLs.
`make dev` starts records-only mode. Stop the server before `make dev-reset`, which
retires development data and seeds a replacement.

For the Console and app workflows, first build the image, then select it explicitly:

```sh
VOPTRACK_IMAGE=voptrack-server:latest make home-dev
# Or:
VOPTRACK_IMAGE=voptrack-server:latest make research-dev
```

| Target | Local endpoint | Private fixture directory |
| --- | --- | --- |
| `make home-dev` | `https://localhost:8544` | `.voptrack-home-dev/` (`home.json`, CA and owner password file) |
| `make home-app-dev` | `https://localhost:8564` (app only) | `.voptrack-home-app-dev/` |
| `make home-quota-dev` | `https://localhost:8574` (app only, 2 MiB quota) | `.voptrack-home-quota-dev/` |
| `make research-dev` | `https://localhost:8444` | `.voptrack-research-dev/` (`participant.json`, `participant-2.json`, CA and owner password file) |

Trust the generated `ca.crt` in the development client; keep TLS verification enabled.
Fixture files contain credentials and must stay private and untracked. Corresponding
`*-dev-token` targets renew fixture credentials. `home-dev-links` and
`home-app-dev-links` create fresh invitations; replacing an owner-device link signs
out earlier owner devices. Corresponding `*-dev-down` targets **delete development
volumes and fixture files**.

### Checks

```sh
make check
make catalog-check
```

`make check` runs source checks, unit tests, Go vet, the pinned server version and
append-only vocabulary validation. `make catalog-check` is separate: it intentionally
reports unresolved catalogue questions and is not a release-readiness claim.

For deployment changes, build the image and run the relevant disposable gate with
`VOPTRACK_IMAGE` explicitly selected, for example `make console-check`,
`make home-app-check` or `make research-check`. Each gate's README records its scope
and limits. `make ci` runs the full suite on a clean committed tree; `make ci-remote`
uses the configured shared Linux host. Results are recorded in
`git notes --ref=ci show`. GitHub Actions is disabled. Release tags and Tessera pin
changes require the full suite; routine changes use appropriate local checks.

Tessera owns protocol behavior and conformance. Keep product semantics here and record
missing upstream capabilities in [Tessera dependencies](tessera-dependencies.md).
The vocabulary and track catalogue are append-only; do not change existing entries.
