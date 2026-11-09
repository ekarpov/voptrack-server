# Home copy label, contract v1 (`com.voptrack.home_copy_label`)

Owner decisions 6 Nov 2026 (docs/decisions.md, "Household group in Tracks" addition, option a, and
"Minimal shared subject in Home"). One label beside each record the iPhone copies into a member's
own Home store, for a Track the household page splits (spending, or a count or latest value about a
pet or a thing). It says in plain text and numbers what the copy can no longer link to: its
category, its pet or thing, its currency. This one is truly Home-only (it describes a Home copy),
hence the name. Tessera is the protocol authority; this only names a type and fields on it.

## Type

Root type, app-owned, in the member's own Home store. Not a Track type: catalogue/1 and its hash do
not change, and it is not listed there. Granted to the household group with the store's first copy
and revoked with its last, together with `com.voptrack.shared_subject`.

| Field | Tessera type | Meaning |
| --- | --- | --- |
| `contract_version` | `integer` | `1`. Added 7 Nov; labels written before it have `null`, read as v1. |
| `record` | `reference` | The copied record, in the same store. |
| `track_type` | `text` | The copied record's Track type id. |
| `at` | `date_time` | The record's moment, for the page's period. |
| `amount` | `integer` | Spending: minor units. |
| `currency` | `text` | Spending: the record's currency code; empty where it names none. |
| `category` | `text` | Spending: the category's name; empty where it has none. |
| `subject` | `text` | The pet's or thing's display name (absent for the person's own Track). |
| `kind` | `text` | The broad category: `person`, `pet` or `thing`. Same values as the shared subject's `category`. |
| `subject_id` | `text` | The shared subject's `subject_id` (docs/contracts/shared-subject-v1.md). Group by this, not by `subject`, for a lasting per-member page. |
| `kind_text` | `text` | The shared subject's free-text kind ("Dog"); absent where none. |
| `value` | `float` | Latest values: the reading. |
| `unit` | `text` | Latest values: its unit. |
| `note` | `text` | The record's note, trimmed; absent where empty. |

`kind` predates the shared subject and keeps its meaning (broad category). The free-text kind is
`kind_text` in both contracts, and a structured kind added later takes a new name (`kind_code`),
never `kind`.

## Lifecycle

Written, updated and deleted by the same sync as its record. A label whose record goes is deleted
before the record. Stopping sharing deletes every label of that copy. A rename of the category or
subject reaches the labels at the next sync; `subject_id` does not change with it.

## Evolution rules

The shared subject's rules apply unchanged: `contract_version` names the version followed and a
reader accepts any; fields are only added, optional; readers ignore unknown fields; ids never change
meaning; deletions are explicit Tessera deletes. A change that cannot follow them is a new type id.

## Examples

Synthetic: `docs/contracts/examples/home-copy-label-v1.insert.json` (a pet's weight) and
`home-copy-label-v1.expense-insert.json` (an expense). Both are `POST /v1/datasets/{d}:transact`
bodies.
