# Try it

A throwaway Voptrack server on your own machine, with something in it, in one
command. For deciding whether you want to run one before you commit a host to
it.

```sh
make try
make try-down
```

About twelve seconds from nothing to a running server with two track types and
a fortnight of readings, and a `/admin` you can sign in to.

**Verified on 2026-09-28 against Tessera v0.1.0-alpha.26.**

## This is not a deployment

It listens on localhost, its certificate is one Caddy issued to itself, and
`make try-down` destroys the volume. Everything in it is synthetic.

It is the same image, the same compose and the same server as
[single-user](../single-user/README.md), configured for throwing away. When you
want one that keeps things, use that.

Two differences worth knowing, because both are deliberate:

**Your browser will warn about the certificate.** Nothing has vouched for it —
Caddy issued it to itself for localhost. Accept it here; do not learn the habit.

**`encrypted_storage_confirmed` is set for you.** Everywhere else that flag is
an attestation you have to make, because it protects records that are yours or
somebody else's. Here nothing real ever enters the volume and `try-down`
destroys it, so there is nothing for the attestation to be about. The other
scenarios make you state it, and they should.

## What it puts in

Two types shaped the way Voptrack's own are — a value with a unit, and a
two-value reading sharing one unit — and fourteen days of readings for each.
Deterministic, so two runs look the same and a screenshot stays true.

They are named `com.example.evaluation.*` and are **not** the bundled
catalogue. That catalogue is still a sketch with open questions recorded in
[the format](../../specs/voptrack-tracks-format.md), and seeding a sketch into
a demonstration would suggest it is more settled than it is.

`seed.py` is a hundred lines against the protocol over HTTP, so it also serves
as the shortest worked example of defining a type and writing records. Running
it twice is safe: a type that exists answers `409`, which is the right answer
and not a failure.

## What to look at

| | |
| --- | --- |
| `/admin` | Dataset identity, engine, watermark, every capability advertised or withheld, the declared bounds |
| `/admin/schema` | The two types, their fields and roles, as the server resolved them |
| `/admin/principals` | The one account, its grants, its live authorizations |
| `/admin/authorizations` | One row per installation |

The records themselves are deliberately not on any page: the administration
surface reads through a handle carrying `read` only and shows no record value
anywhere. To see the data, query it — `seed.py` shows how, and
`POST /v1/datasets/{d}/objects/{type}:count` is the one-liner.

## What this does not tell you

Whether it works on a real host. There is no domain, no real certificate, no
firewall, no backup, and nothing has restarted. For that, the
[single-user](../single-user/README.md) scenario ends in a conformance gate;
this one ends in something to look at.
