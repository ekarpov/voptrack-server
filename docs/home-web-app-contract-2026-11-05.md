# Home web and app alignment, 5 November 2026

Status: dashboard UX in local review; common-track authoring is not implemented.
First authoring release: record only to compatible app-installed types in the
explicit common dataset. Web type installation is deferred. See the
[reply to the app team](requests/app-team-common-track-catalogue.md).
This records the owner's relayed app-team request and supersedes assumptions in the
visual prototype that a common Home dataset already exists. It does not change the
released deployment contract or authorize a Tessera protocol extension.

## Delivery order

1. **Read-only dashboard first.** Retain the unified household ledger, period and
   member filters, category breakdown, actual-value charts, attachment links and
   Pets/House/Car navigation. Read only the member datasets registered with this
   Home, never the iPhone plus its synchronized copy. Derive totals, charts and
   individual contributions from the same bounded response per dataset/type.
   Show computed-at time, non-answering stores, empty periods and reading limits.
   An empty period is a gap, not zero. A differing installed declaration is excluded
   and named, never reconciled. Latest readings stay separated by subject/register,
   currency, unit and epoch rather than becoming an average across people.
2. **Agree and test the cross-client contract below.** Consume the app's exported
   declarations, annotations and reduction rules; test the same vectors in both
   clients. The current display-only ID catalogue and legacy metric adapters are
   not a replacement for that export. Freeze new fixture schema creation meanwhile.
3. **Provision a common household dataset.** Operator/owner creates it through a
   supported Tessera operation once its authority and discovery are confirmed.
   Persist its qualified identity in the agreed Home connection descriptor; the
   app and web must discover the same identity, independently of display names.
   Never choose the first readable dataset or infer ownership from its name.
4. **Common subjects and app-installed Tracks.** In the first authoring release,
   the app installs types and required registrations. The web records only to
   compatible installed types in the explicitly identified common dataset; it
   creates no Track, parent, dependency or app-internal types. Subject/category
   object creation requires their compatible app-installed schemas and agreed
   reference/erasure rules. Web catalogue installation is a later opt-in slice,
   after exact-declaration, version and race tests. Members' read/write grants
   alone are never schema or administration authority. Existing declarations
   remain unchanged; an incompatible declaration requires a new type ID.
5. **Recording and history.** Read/write members create, edit and delete common
   records as themselves; read-only members only view. Web writes never use the
   Home service, custodian or owner's credentials on behalf of a member. App-managed
   copies remain read-only, including the member's own synchronized copy.
6. **Erasure and two-member interoperability.** Implement only after agreement on
   dependency removal and exact authority. Remove the relevant registration,
   subject/category references, media links, saved-view references and copy-related
   metadata under the agreed model, leaving no object-specific metadata behind.
   Exercise changes from web to app and app to web, revocation and partial failure.
   Do not ship a delete operation that silently leaves a partial graph behind.

### Common dataset authority and discovery proposal

| Principal | Common dataset authority | Web behavior |
| --- | --- | --- |
| Owner | Read, write, erase; explicit schema/administration authority for installation and membership | App installs types in the first release; web management/erasure requires separately verified authority |
| Writable member | Read and write only | Record and edit common records; no schema or membership changes |
| Read-only member | Read only | View history and dashboards |
| Home dashboard service | No new common-write grant | Never substitutes for the signed-in person's authority |

These are requested grants, not a claim that the current Tessera pin implements
this grouping. Confirm whether record deletion is included in write before
exposing Delete to writable members. App-managed member copies receive no web
mutation path, even when the person's general token can write their member store.

Proposed discovery is one versioned authenticated Home descriptor containing the
Home's stable identity and the exact common dataset ID. The operator supplies
that binding once, it is validated against the signed-in person's dataset listing,
and both clients retain it as part of that Home connection. The descriptor's
transport and ownership attestation need Tessera/app agreement; no new discovery
endpoint or unsigned name-based lookup is implemented here. Losing access to the
bound dataset produces an access state, never fallback to a different dataset.

## Requested export

Prefer a versioned JSON bundle: `manifest.json`, one immutable JSON declaration
per type, and shared dependencies. Please generate it through the **same app code
that installs types**, not a second handwritten catalogue or parsed Swift source.

The manifest should contain a format version, catalogue version, app source revision,
minimum reader version, each file's SHA-256 and dependency order. One track entry
should contain:

- The exact Tessera install payload, including ancestor types, IDs, parent IDs,
  ordered fields, field types and scales, roles and annotations at both levels.
  Include the canonical serialized payload bytes and their digest if literal
  byte equality, rather than structural JSON equality, is required.
- A versioned Voptrack presentation declaration: English name, family, allowed
  subject kinds, field order, labels, primary/secondary fields, input constraints,
  unit choices/defaults, precision, category reference, money scale/currency field,
  spending flag, compatible app icon identifier and semantic color key.
- The declared dashboard metrics and their default order: operation, field,
  filters, grouping identity, period policy, timestamp field, missing/invalid rules,
  currency/unit separation, rounding, counter/reset/epoch rules and any conversion
  policy. Count of observations must be distinguishable from count of completed
  chores or walks. Do not assume all event rows represent completion.
- Exact Subject, Category, Source, track registration and HomeCopyMarker schemas
  plus representative wire records and their English display-text/blob encodings.
  Supply any registrations required for a new type to appear as an app Track.
- Cross-client vectors: two members, unanswered member, empty period, real zero,
  mixed currency/units, weighted average, latest reading, reset epoch, custom
  category, same-named distinct subjects, shared subject, copied history and its
  update/deletion, read-only member and incompatible installed schema.

Unknown presentation/metric versions should keep history readable and disable
unsupported authoring/reductions. No guessed translation or replacement annotation
is installed. English is the initial UI language; locale assets remain for later.

## What Console can read today

It reads effective metadata, field roles and opaque JSON-valued annotations, typed record
values and qualified references through the pinned Tessera API. Portable
`meaning`, `unit_field`, `unit` annotations are partly consumed by existing metric
adapters. It does **not** yet decode the complete app library presentation,
spending, money-scale, default-metric or period-policy declarations. A schema alone
is insufficient to recreate those app-local declarations. The new dashboard uses
existing legacy adapters while export integration is pending; it is not certified
as the app's complete U111 implementation.

The Home reading slice fixes the previous string-only annotation decoder. It
retains strings, exact JSON numbers, booleans, objects, arrays and null; supported
string meanings keep their existing accessors. Reading an opaque value does not
establish its app-specific meaning. Full compatibility still needs validation of
the complete declaration and all ancestors against exported vectors. Effective
schema comparison is not a catalogue byte/hash or installation verifier.

The metadata reader now retains type roles and opaque JSON-valued annotations,
including exact numbers, plus field ownership and immutability. It does not write
or re-annotate installed metadata. It compares effective declarations before a
cross-store combination and excludes differing schemas.

## Tessera and app questions to settle together

- Can a household-owned dataset have the owner as administrator and explicit erase
  authority, an issuer/group of members as readers and writers, and a read-only
  membership variant? Which supported provision/update endpoints express this on
  the agreed pin? Confirm record deletion versus full-dataset erase authority.
- How are joins, suspension, leaving, revocation and owner transfer reflected in
  those grants? Group readers exist today; equivalent group writing and the
  read-only exception must be confirmed, not inferred from that feature.
- What authenticated discovery descriptor identifies the common dataset and its
  Home, and tells app/web that it is common rather than a member store? Existing
  connections must continue to work and dataset names must not become identity.
- How does a read-only viewer authoritatively identify **their own member dataset**
  for “your part”? The existing dataset listing and `/oauth/account` do not provide
  that relation to this Console. Do not guess from names or from write access.
- `HomeCopyMarker`'s app implementation explicitly says its type is never granted
  to the household. How can the web inspect copy provenance when acting as the
  member, without exposing another member's private source references? Safest first
  authoring scope: only the explicitly discovered common dataset; never member
  stores, regardless of whether a marker can be read.
- Confirm a permission-bound graph erasure procedure and partial-failure recovery.
  Keep protocol answers and any new protocol behavior in Tessera.

The current Home dashboard service reads the same explicitly shared types after
revalidating the viewer's active membership. Before common-dataset work, verify
that its visibility is equivalent to that person's household grants. Do not
extend this service to common writes or use it to evade read-only membership.

## Demo and acceptance

Local Home remains `https://localhost:8644`; Research remains `https://localhost:8744`.
The existing Sara/Ben synthetic datasets and read grants are retained; Lina stays
suspended, private Weight stays private, receipts and media remain intact. The
new dashboard can be exercised today, but it is not the app team's end-to-end
copy/common-dataset walk. No common dataset, copy marker or app catalogue has
been fabricated. The private fixture preflight refreshed only existing member
app tokens; it installed no new schemas, records or grants in this revision.

After export and grant/discovery agreement, the two-member acceptance walk is:

1. App A shares a local Track and its history; web shows exactly its Home copy.
2. App B shares the same type; combined and individual values agree with U111,
   including empty and unavailable member periods. Neither original is counted twice.
3. Owner uses the app to install common subjects, category schemas and catalogue Tracks. Both apps
   discover the same common dataset and the same qualified subject identities.
4. A records from the web; B sees and edits from the app; web sees the result.
5. A read-only member sees history but mutation attempts are refused server-side.
   Copied types remain unwritable even for an otherwise writable member.
6. Changing or deleting the iPhone original updates/removes its copy exactly once.
7. Revocation immediately ends access. Owner erasure removes the agreed record and
   metadata graph. Synthetic/private data boundaries and all existing grants survive.

Keep the strict CSP, current sign-in and Tessera pin. The pending branded login
and native OAuth fixes remain separate upstream work.
