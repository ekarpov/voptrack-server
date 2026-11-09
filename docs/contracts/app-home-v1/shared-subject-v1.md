# Shared subject, contract v1 (`com.voptrack.shared_subject`)

The subject a
store shares with its readers: a person, pet or thing named by an opaque id, a display name and a
kind. Nothing else. First writer: the iPhone app, into a member's own Home store, for a pet's or a
thing's Track the member shares with the household. First reader: the Voptrack Server household
UI ("Rex" page). The contract is not Home-specific: any writer may put these documents in a store
it holds, and any reader with a grant may read them.

Tessera is the protocol authority ([Tessera v1 specification](https://github.com/ekarpov/tessera/blob/v0.1.0/specs/tessera-protocol-v1.md)). This
contract only names a type and its fields on top of it; it adds no protocol behaviour. Tessera
defines no subject concept, no role names and no cross-store identity ("Tessera defines no role
names and no annotation keys"), so there is nothing there to align with beyond its general rules,
which this contract follows: values are tagged JSON, absent is `null`, object ids are
server-assigned and never reused, grants are per concrete type, references do not cascade.

## Type

A root type (`parent_id: null`), not a subtype of `com.voptrack.subject`. A subtype would inherit
`avatar` and `details` (the fields that hold photos, birthdays and notes), and a query of
`com.voptrack.subject` with descendants would return shared documents to every reader of the full
subject type. Granting `com.voptrack.subject` itself was rejected on 5 Nov because a type grant
exposes every subject in the store, including full ones.

| Field | Tessera type | Required | Meaning |
| --- | --- | --- | --- |
| `contract_version` | `integer` | yes | `1`. The contract version the writer followed. |
| `subject_id` | `text` | yes | The opaque id. See [Identity](#identity). |
| `name` | `text` | yes | Display name, as the writer resolved it. Not unique: two subjects can both be "Rex". |
| `category` | `text` | yes | `person`, `pet` or `thing`. Readers treat another value as unknown, not as an error. |
| `kind_text` | `text` | no | Free-text kind as the person wrote it: "Dog", "Car". `null` where none. |

Never written, in any version: photo or avatar, birthday or age, notes, custom details, the
writer's own ids for the subject, anything about the writer's device.

## Identity

- `subject_id` is opaque, stable and **scoped to the store that holds the document**. Readers
  MUST NOT parse it or derive meaning from its shape. v1 writers produce 32 lowercase hex characters;
  a later writer may produce another shape.
- It does not change when the subject is renamed or its kind changes. The document is updated
  in place.
- Two subjects never share an id within a store. Two members' subjects are two documents in two
  stores, even with the same name: no merge across members or stores.
- The iPhone app derives it as HMAC-SHA256, keyed with 32 random bytes the device keeps for that
  one store, over the subject's reference on the device; the first 16 bytes, in hex. Without the
  key it is not reversible to the device's id, and two stores' ids for the same subject are
  unrelated. The key goes when the store's last copy stops, when the device leaves the store, and
  when the store is erased. Sharing again after that gives a new id.
- The Tessera object id of the document is a separate thing: server-assigned, and never reused.
  A record names the document by Tessera reference (below); a durable cross-store link names it by
  `(dataset_id, subject_id)`, because a document deleted and written again gets a new object id.
- Erasure resets everything. Tessera mints a new dataset identity on erase, so no pair from before
  an erase can match one after it.

## Who names it

- **A copied record.** In a Home copy of a pet's or a thing's Track, the record's `subject` field
  is a Tessera reference to the document:
  `{"dataset_id": <the store>, "object_id": <its object id>, "meta_id": "com.voptrack.shared_subject"}`.
  The person's own Tracks keep naming the member's `com.voptrack.subject` as before.
  **Note for catalogue readers:** catalogue/1 lists `com.voptrack.subject` as the only target of
  `com.voptrack.track.subject`. That describes a Home store's own Tracks. A copy's records may name
  `com.voptrack.shared_subject` instead. Tessera does not check reference targets.
- **A label** (`com.voptrack.home_copy_label`, docs/contracts/home-copy-label-v1.md) carries the
  same `subject_id` and `kind_text` as plain text, so a page groups by them without a second read.

## Lifecycle

The writer is the only one who writes or deletes the document. For the iPhone app:

| Event | Effect |
| --- | --- |
| First record of the subject's first shared Track is copied | Inserted. Recorded on the device before it is sent, under an idempotency key. A lost answer is found where it landed and never written twice. |
| Subject renamed, kind or category changed | Updated in place at the next sync; `subject_id` and object id unchanged. |
| The last shared Track naming it stops sharing, or is deleted | Deleted, after the records that named it. |
| The member leaves the Home (disconnects the store) | Deleted while the store can be reached (best effort; see below). |
| The store is erased | Gone with everything else; the device forgets its key and links. |
| A sync is interrupted | The next sync converges: inserts what is missing, updates what differs, deletes what no copy names. |

Deletion is explicit: the document is removed with a Tessera delete, never marked. A reader with
a type grant sees it gone (`404 not_found` on a direct read, absent from queries). A record that
still names it, for a moment between two writes, resolves it as `unavailable`, which Tessera
requires every client to tolerate.

**Known limit.** Leaving while the store cannot be reached leaves the document there, with the
copied records, which leaving does not delete either. Erasing the store removes both.

## Access

The member grants the household group read on `com.voptrack.shared_subject` in their own store
(`PUT /admin/api/v1/datasets/{d}/readers/issuer:home-members`, the whole type list), with the
first copy into that store, and revokes it with the last, exactly as for the labels. No server
deployment, type registration or new grant kind is needed: the member already holds `schema` on
their store and already grants per type.

## Evolution rules

1. `contract_version` names the version a writer followed. A reader accepts any version: version
   N+1 only adds to N, so a v1 reader reads a v2 document as v1.
2. Fields are only ever added, as optional (`null` on older documents). A field is never removed,
   renamed, retyped or given a new meaning. Tessera permits adding a field and rejects a type change.
3. Readers ignore fields they do not know.
4. `subject_id` never changes meaning: it names one subject in one store for as long as the
   document exists, and is never reused for another.
5. Deletions are explicit (a Tessera delete), never inferred from a missing field or a flag.
6. A change that cannot follow these rules is a new type id, not a new version.

## Room left, not built

- **Attachments** (a photo of Rex, later): a separate type in the same store whose record names
  the document by Tessera reference, plus `subject_id` as text. Granted on its own, so a reader can
  hold the subject without the photos. Nothing is added to this type.
- **"Same pet" across members or stores**: a separate link type, e.g. `com.voptrack.same_as`,
  whose record names two subjects as `(dataset_id, subject_id)` pairs (Tessera references may name
  another dataset; one the reader cannot see resolves as `unavailable`). Ids are never rewritten
  to merge, and a deleted end makes the link dangle rather than change its meaning.
- **A structured kind** next to `kind_text`: a new optional field, e.g. `kind_code`, from a
  vocabulary agreed later. `kind_text` stays as written. (The label's existing `kind` field holds
  the broad category, so the new field must not be called `kind`.)
- **Recognition by role**: Tessera roles (`"roles": ["subject"]` on the type, roles on fields)
  can be added later as a schema update, which touches no stored record.

## Examples

Synthetic, no real data: `docs/contracts/examples/shared-subject-v1.*.json`.

- `shared-subject-v1.meta.json`: the type as the app installs it (`POST /v1/datasets/{d}/meta`).
- `shared-subject-v1.insert.json`: the insert (`POST /v1/datasets/{d}:transact`).
- `shared-subject-v1.update.json`: a rename that clears the kind.
- `shared-subject-v1.read.json`: a record set as a household reader gets it.
- `shared-subject-v1.copied-record.json`: a copied weight record naming the document.
- `shared-subject-v1.grant.json`: the member's grant to the household group.
