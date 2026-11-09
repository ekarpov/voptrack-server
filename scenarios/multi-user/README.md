# Many users, one server

One server, one dataset per person, nobody sharing. The ordinary self-hosted
shape: an owner runs it and adds people as they arrive. Sharing between people
is a different scenario and deliberately not this one.

**Verified on 2026-09-28 against Tessera v0.1.0-alpha.26**: nine checks green,
including a person signing in, offboarding, and isolation against a real second
user's dataset.

## Running it

```sh
VOPTRACK_STORAGE_ENCRYPTED=yes VOPTRACK_DOMAIN=tracks.example.org make scenario-multi-user-up
make scenario-multi-user-add-user alice
make scenario-multi-user-verify
```

`VOPTRACK_STORAGE_ENCRYPTED` is **your attestation, not a setting**. It encrypts
nothing; it records that you arranged it. On a multi-user server the records on
that volume belong to other people, so the attestation is somebody else's
interest rather than your own.

Bring-up provisions the owner and prints where its password file is, never the
password. Sign in at `/admin`.

## Adding people

```sh
make scenario-multi-user-add-user NAME=alice
```

Creates a dataset, provisions Alice on it with read, write and schema, and
prints where her password lives. She sees her dataset and cannot learn that
anyone else's exists — the gate checks exactly that.

This needed Tessera v0.1.0-alpha.4. Before it, no credential a server operator
could mint was able to create a dataset at all: `-add-token` and `-add-service`
capped at read, write and schema, and creation required `erase`. The owner is
now provisioned with `-may-create-datasets`, a server-level right granted on
the account and separate from `erase`, because creating destroys nothing.

## The owner must hold a dataset

`AuthenticateAdministrator` requires `erase` on *some* dataset — an
administrator is a person who may erase somewhere. So a purely administrative
owner with no data of their own is not expressible, and the owner here keeps
the dataset the server creates for itself.

**And administration is scoped to what you can erase.** alpha.4 changed this:
an administrator sees and acts on datasets where their account holds a current
`erase` grant, not on all of them.

That is narrower than it was and still wide here, because an authorization
reaches the dataset it was approved for **plus anything created through it**.
The owner creates every user's dataset, so the owner holds `read` and `erase`
on all of them. A user's dataset is private from other users, not from the
owner.

This is settled and intended. We reported it as a contradiction between two
rules that could not both hold; Tessera amended the authorization rule rather
than the creation one, on the grounds that the reach is consented to through
`create_dataset` and the defects it caused came from the reach being invisible.
So read "their own dataset" as *not visible to other users*.

## What the gate proves

`make scenario-multi-user-verify` stands up a **disposable twin**, runs nine
checks against it, and destroys it. Tessera's suites define types, write
records and upload content and remove none of it, so they must never be pointed
at a deployment holding other people's records.

Five are Tessera's conformance suite. The sixth is the administration surface.
The seventh is **isolation**, which is what a multi-user server actually sells
and which no protocol suite covers:

- A dataset the caller may not use must be **indistinguishable** from an
  identity naming nothing — both `404 unavailable`.

**Offboarding** checks that somebody whose grants were emptied authenticates
and reaches nothing. It does not exercise the credential half — that person
never signed in, so has no authorization to revoke — which `remove-user` does
and which is verified by hand rather than in the gate.

**Signing in** is the one that authenticates as a **person** rather than with a
token an operator minted. It registers a client, walks the two-step consent with
a fresh token read from each page, exchanges the code with PKCE, and checks that
what comes back reaches exactly the dataset approved and nothing else. Every
other check proves something about the server; this one proves somebody can use
it.

The check compares the two answers rather than asserting a constant. A gate
that accepted "403 or 404" would pass while the server leaked existence, which
is exactly the property alpha.3 introduced. With `-foreign-dataset` it compares
a real second user's dataset; without one it checks the baseline the comparison
rests on, so a regression from `unavailable` to `not_found` fails here.

## Checking a live deployment

The gate above is safe because it runs against a twin. To check a **running**
deployment without writing to it:

```sh
go run ./cmd/verify -url https://tracks.example.org -token TOKEN \
  -conformance=false -isolation -admin-user owner -admin-password-file FILE
```

`-conformance=false` skips the suites that write and runs only the read-only
checks.

## What this scenario does not cover

- **A real domain and certificate.** Caddy's Let's Encrypt path needs port 80
  reachable, correct DNS, and is rate limited.
