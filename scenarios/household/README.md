# A household

This is a legacy shared-store protocol scenario, with owner access that differs
from current Voptrack Home. For Home installation and private member stores, use
the [Home guide](../../release/household/README.md). See the
[scenario index](../README.md) before selecting this example.

One shared store everyone in the household can read and write, and a personal
store each that nobody else can reach.

**Verified on 2026-09-28 against Tessera v0.1.0-alpha.26**: nine checks green — a
member signing in, sharing in both directions, and isolation against a real
personal store.

## Privacy here is by placement, not by permission

What you put in the shared store is shared. Everyone who can write it can read
**all** of it — that is what a shared store is, not a compromise. What you keep
in your own store, nobody else can reach, and the two are different datasets.

There is no per-record sharing and there cannot be one. A Tessera grant is over
a dataset, and a writer must hold a full view of what it writes to: a
restricted view is enforced read-only, because a writer holding one could not
see the record it just inserted. So "share this reading with my partner but not
that one" is not expressible, and the honest answer is to decide where a record
goes when you write it.

That is worth knowing before you build habits on it. If you want per-person
audiences within one store, this is the wrong shape and Tessera's row-predicate
views — filed, unbuilt — are the right one.

## Running it

```sh
VOPTRACK_STORAGE_ENCRYPTED=yes VOPTRACK_DOMAIN=home.example.org make scenario-household-up
make scenario-household-add-member NAME=alice
make scenario-household-verify
```

`VOPTRACK_STORAGE_ENCRYPTED` is your attestation that the volume is encrypted.
It encrypts nothing. Here the volume holds your family's records, which is a
stronger reason to mean it than your own would be.

## Adding people

```sh
make scenario-household-add-member NAME=alice
```

Alice gets two stores: `read,write` on the shared one — never `erase`, never
`schema`, which stay with whoever curates it — and a personal store of her own
with `read,write,schema`, because that one is hers to shape.

Adding an existing person to a second dataset extends them rather than
replacing them, so the two grants compose.

This needed Tessera v0.1.0-alpha.4, which made dataset creation reachable from
a credential an operator can actually mint. **Read the next section before you
rely on the personal stores.**

## The owner can read every personal store

**A member's personal store is not private from the owner, and this is by
design rather than a defect.** The owner creates it, and an authorization
reaches the dataset it was approved for *plus anything created through it* —
creation being the only thing that widens reach. So the owner holds `read` and
`erase` on every personal store, with the token they already have.

We reported this as a contradiction: Tessera's authorization rule said an
authorization is over at most one dataset, while its creation rule said the
creator becomes the new dataset's administrator and sees it listed. Both were
requirements and they could not both hold. It was settled by amending the
authorization rule, not the creation one — the reach is intended, and the three
security defects it produced came from the reach being **invisible** rather
than from its existing. The protocol now states it, a withdrawal must remove
access to everything an authorization reaches, and a consent screen offering
dataset creation must say the installation will reach what it creates.

**So this does not go away.** Read "personal store" as *not visible to other
members*, not as *not visible to anyone*. The owner is not other members.

If members need privacy from whoever runs the server, this scenario is the
wrong shape and no future version of it will be the right one. What would fit
is each person running their own server and sharing into a common one — a
different arrangement, not a setting.

## Somebody can destroy the shared store

An administrator is a person holding `erase` on some dataset — that is what the
administration surface checks. The owner holds it on the shared store, so the
person who administers the household can also erase everyone's shared records.

That is a real property of this arrangement, not an oversight. Pick the person
you would trust with it, and note that `erase` and *administering* cannot
currently be separated.

## What the gate proves

`make scenario-household-verify` stands up a **disposable twin**, runs nine
checks and destroys it. Never this deployment: the suites write and clean
nothing up, and here the records would be a family's.

Five are Tessera's conformance suite; one is the administration surface; one is
isolation. The eighth is **sharing**, which is what this scenario sells:

- Two members are on one store, and each can read what the other wrote.

Checked in **both directions**, by writing as each and reading as the other,
because a grant can be asymmetric and the failure would look like nothing.
Isolation alone would be satisfied by a server that let nobody see anything;
this is the half that says the sharing works.

**Signing in** is the one that authenticates as a **person** rather than with a
token an operator minted. It registers a client, walks the two-step consent
with a fresh token read from each page, exchanges the code with PKCE, and
checks that what comes back reaches exactly the dataset approved and nothing
else. Every other check here proves something about the server; this one proves
somebody can use it.

**Isolation is checked against a real second dataset**, not against a baseline.
The gate creates a personal store over the protocol and proves a credential for
the shared store cannot reach it. This paragraph used to say the opposite — that
the check ran against its own baseline *for want of creation* — and that has been
untrue since the gate was changed to create one, which the scenario's own comment
dates to alpha.4.

What is **not** covered is the shape where every member has a personal store of
their own and the shared store is additional. Two members on one store is what
this scenario is. Per-member stores are creatable at the version pinned here, so
the reason is that nobody has built that arrangement — not that anything blocks
it.

## What this does not cover

- **A real domain and certificate**, firewalls, reboots, backups.
- **A member's own personal store.** One is created here for the isolation
  check, so creation is proven; what is not covered is a household arranged as
  one personal store per member plus a shared one.
- **Anything about who a record is about.** A shared store holding readings
  from several people needs each record to say whose it is, and for every
  member's device to agree on that identity. That is Voptrack's modelling
  problem, not the deployment's, and nothing here solves it.
