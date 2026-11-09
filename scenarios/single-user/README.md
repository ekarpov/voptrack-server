# Single user, one server

One person, one server, one dataset. The smallest thing that is a real
deployment rather than a development server: public TLS, a provisioned
credential, and a conformance gate that proves the deployment answers the
protocol rather than merely that a process is listening.

**Verified on 2026-09-28 against Tessera v0.1.0-alpha.26**, on `localhost` with
Caddy's internal CA: seven checks, all green.

## Getting the server

Three routes. They differ only in where the binary comes from; the
configuration, the proxy and the gate are identical.

| Route | When | Needs |
| --- | --- | --- |
| **Pull the image** | the default | nothing but Docker |
| **Build the image** | you want to build what you run | Docker, an SSH agent with access to Tessera |
| **Build the binary** | no containers at all — see [deploy](../../deploy/README.md) | Go 1.27, CGO, a C compiler |

While Tessera is private, building either way needs access to it. When Tessera
and this repository are published the build routes need nothing special, and
pulling stops needing a token.

```sh
# Build it yourself, from the repository root. No key is needed: the Go
# modules come from your own Go cache (see deploy/README.md).
make image
```

Set `VOPTRACK_IMAGE` to use a published image instead.

## Running it

```sh
VOPTRACK_STORAGE_ENCRYPTED=yes VOPTRACK_DOMAIN=tracks.example.org make scenario-single-user-up
make scenario-single-user-verify
```

Bring-up provisions an administrator and prints where its password is, never the
password itself — a printed one ends up in scrollback, in a screen recording and
in whatever captured the build log. Sign in at `/admin` with the account `owner`.
Move that file somewhere private, or change the password, before this is a
deployment anybody relies on.

`VOPTRACK_STORAGE_ENCRYPTED` is **your attestation, not a setting**. It does not
encrypt anything. The server refuses to start unless the configuration says
storage is encrypted, and that flag records that you arranged it. Records live
on the volume in the clear if you did not, and nothing downstream catches a
false attestation.

Leave `VOPTRACK_DOMAIN` unset and everything runs on `localhost` with Caddy's
own CA, which is how the gate is tested. Set it to a real hostname that already
resolves to the machine and Caddy obtains a Let's Encrypt certificate with no
other change.

## What the gate proves, and what it deliberately does not

`make scenario-single-user-verify` stands up a **disposable twin** of this
deployment — same image, same compose, same Caddyfile, same configuration
template, its own volumes and ports — runs seven checks against it over HTTPS with
TLS verification enabled, and destroys it.

Five are Tessera's own conformance suite, the one its reference server is held
to: core, paging, recovery, content and optional reads. The sixth is the
administration surface, which is this deployment's rather than the protocol's:
that `/admin` is protected, that a wrong password is refused, and that all four
pages answer with their actual content rather than a truncated page under 200.

**Signing in** is the one that authenticates as a **person** rather than with a
token an operator minted. It registers a client, walks the two-step consent
with a fresh token read from each page, exchanges the code with PKCE, and
checks that what comes back reaches exactly the dataset approved and nothing
else. Every other check here proves something about the server; this one proves
somebody can use it.

**Why a twin and not this deployment.** `CoreFixture` says in its own
documentation that it must address a disposable synthetic dataset. It does: it
creates types and records and leaves them behind. Pointed at a real deployment
it writes synthetic conformance artefacts into the operator's data permanently,
and the second run fails with `409` because the fixtures already exist. That was
how this scenario worked at first, and running it twice is the only thing that
shows it.

So what the gate proves is that **this recipe produces a conformant
deployment**. It says nothing about the records in a running instance, and is
not meant to. The upside is that it is repeatable and safe to run whenever, and
the real deployment's watermark is provably unchanged afterwards.

## Provisioning people

Provisioning takes the same exclusive database lock as the server. **Stop it
first** — running both is refused with `dataset_unavailable`, not queued.

```sh
docker compose -f scenarios/single-user/compose.yaml stop tesserad
docker compose -f scenarios/single-user/compose.yaml run --rm --no-deps tesserad \
  -config /etc/tessera/server.json -add-person you \
  -grants read,write,schema,erase -password-file /secure/initial
docker compose -f scenarios/single-user/compose.yaml start tesserad
```

Then open `/admin` over HTTPS and sign in. `-grants` defaults to `read,write`, so an account
created without it is not an administrator and is refused at `/admin`. See
[operating](../../docs/operating.md).

## How it is put together, and why

**Caddy terminates public TLS and re-encrypts to the server.** `tesserad`
refuses to start without a certificate and key, so it has no plaintext listener
to proxy to. The internal hop skips verification — it is container-to-container
on a private network against a self-signed certificate generated at init, and
the alternative Caddy offers there is not "verified" but "unencrypted".

**The issuer is the public address.** OAuth clients authenticate against what
their browser reaches, not against the container's internal name.

**The container has a real healthcheck.** Without one, `docker compose up
--wait` reports `Healthy` for a crash-looping container, because absent a
healthcheck it treats running as healthy. That happened while this scenario was
being written: the server was restart-looping on a rejected configuration and
compose called it healthy. The check verifies against the server's own
certificate, so a broken TLS configuration fails it.

## What the verification run does not cover

The stack, the proxy, the provisioning and the gate were exercised on one
machine against `localhost`. These were **not**:

- **A real domain and a real certificate.** Caddy's Let's Encrypt path behaves
  differently from its internal CA: it needs port 80 reachable from the
  internet, correct DNS, and it is rate limited.
- **The firewall.** Nothing here configures one. The compose publishes 80 and
  443 on all interfaces.
- **Backups.** See [operating](../../docs/operating.md); they stop the server,
  which a nightly job must account for.
- **Restart survival on a real host**, reboots, or disk pressure.
- **This instance's own records.** The gate verifies a twin, by necessity — see
  above. Nothing here checks that your live data is intact.
