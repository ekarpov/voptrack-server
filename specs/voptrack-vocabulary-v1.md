# Voptrack tracking vocabulary, version 1

**Status: draft.** This document is **not part of Tessera.** Tessera defines no role names and no
annotation keys, ever — it stores both and never reads them. This is the agreement *between products*
about what those strings mean, so that a program which has never seen your type can still use it.

Nothing here is enforced by any server. A typo produces a type nobody recognizes, not an error.

**This document is authoritative.** Voptrack's iOS and Android apps implement *from* it rather than
authoring it — with two platforms, neither may own the constants. The names, dimensions and exact
scales are also published as data in [`voptrack-vocabulary-v1.json`](voptrack-vocabulary-v1.json), which is what an
implementation should generate from: a hand-copied `lb = 0.45359237` is how two platforms quietly
disagree. **A consumer MUST fail on an entry marked `scale_pending` rather than assume a value.**

## Rules

- **Append-only.** A name, once published, is never removed and never repurposed. Correcting a mistake
  means publishing a new name and deprecating the old one; the meaning of stored data must not change
  because this document changed.
- **Unprefixed names are shared.** `value`, `timestamp`, `mass` belong to every product.
- **A product-specific name carries a reverse-DNS prefix** — `com.voptrack.exercise_set` — and needs no
  agreement from anyone. Promote it here only when a second product needs to read it.
- **Recognition, not validation.** A client decides whether a type satisfies a contract it cares about.
  A type carrying none of these names is perfectly valid and simply isn't recognized.
- **Annotate what crosses a boundary.** Types that never leave one app need no roles at all.

## Field roles

The structural roles of a record. Transcribed from `TrackFieldRole`, which already treats these as a
stable, string-backed, extensible set.

| Role | Meaning |
| --- | --- |
| `subject` | Who or what the record is about. |
| `source` | Where the record came from — a device, an import, manual entry. |
| `timestamp` | When the record applies. |
| `end_timestamp` | The end of the period a record covers, where it has one. |
| `value` | The measured or recorded value. A type may carry several (systolic and diastolic). |
| `unit` | The unit of a value in the same record — see **Units** below. |

A field may carry more than one role, and a role may appear on more than one field. Neither is an
error; recognition finds candidates and the client chooses between them.

## Measurement meaning

What a value *is*, which decides how it may be combined. Transcribed from
`TrackCollectionMetric.Meaning`, including which aggregations each permits — that table is the whole
point, because summing a sample is as wrong as averaging a counter.

| Meaning | Permits | Notes |
| --- | --- | --- |
| `sample` | latest, minimum, maximum, average | A reading at a moment. Summing is meaningless. |
| `additive` | latest, minimum, maximum, sum, average | Quantities that accumulate. |
| `counter` | latest | A running total that only increases; differences are what matter. |
| `interval` | — | A duration between two points; uses its own typed operations. |
| `valuation` | latest | A point-in-time worth. |
| `categorical` | — | A label, not a quantity. |
| `money` | — | An amount with a currency. |
| `money_valuation` | — | A point-in-time monetary worth. |

Aggregation, where permitted, means observation-weighted mean for `average`, excluding missing values.

**`meaning` is an explicit annotation and MUST NOT be inferred.** A dimension says what a number
measures, never how it may be combined: two `mass` fields can be a `sample` (a weight reading) and an
`additive` (food eaten today), and summing the first is as wrong as averaging a counter. A field with
a `dimension` and no `meaning` is **not recognized as a measurement** — which is the safe failure,
because the alternative is a plausible chart of a meaningless number.

## Dimensions and units

A measured value's unit belongs to exactly one dimension, and every unit declares an **exact scale** to
that dimension's base unit. Publishing the scales is the point: two products converting pounds to
kilograms with different constants produce numbers that disagree for no visible reason.

**A scale converts to the dimension's base unit**: `value_in_base = value × scale`. The base unit has
a scale of exactly 1. Scales are **exact decimal literals**, not approximations to be rounded for
display — `lb` is 0.45359237 by definition of the pound, and a consumer MUST parse the value as
written rather than through a lossy intermediate. A unit with no published scale MUST be treated as an
error, never as 1.

| Dimension | Base | Unit | Scale to base |
| --- | --- | --- | --- |
| `count` | `meter_tick` | `meter_tick` | 1 — a generic tick, implying no physical conversion |
| `time` | `s` | `s` | 1 |
| | | `h` | 3600 |
| `mass` | `kg` | `kg` | 1 |
| | | `g` | 0.001 |
| | | `mg` | 0.000001 |
| | | `lb` | 0.45359237 |
| `length` | `m` | `m` | 1 |
| | | `cm` | 0.01 |
| | | `in` | 0.0254 |
| | | `km` | 1000 |
| | | `mi` | 1609.344 |
| `pressure` | `kPa` | `kPa` | 1 |
| | | `mmHg` | 0.133322387415 |
| `frequency` | `bpm` | `bpm` | 1 |
| `angle` | `deg` | `deg` | 1 |
| `volume` | `L` | `L` | 1 |
| | | `mL` | 0.001 |
| | | `fl_oz_us` | 0.0295735295625 |
| `energy` | `kJ` | `kJ` | 1 |
| | | `kcal` | 4.184 |

Transcribed from `BuiltInPhysicalUnits.definitions`. Note that `pressure` is based on **`kPa`**, not
`mmHg`; an earlier draft of this table inferred the wrong base.

### Where a unit lives

A measured field declares **how its unit is found**, and this is what tells a reader whether the column
can be added up as it stands:

| Annotation | Meaning |
| --- | --- |
| `unit_source: "record"` | Each record carries its own unit, in the field named by `unit_field`. **The default for measured fields.** |
| `unit_source: "fixed"` | One unit for the whole type, named by `unit`. |

Per-record units make a record self-describing, so no later edit to this document or to a schema can
retroactively change what a stored number meant.

**A reader must not aggregate or range-compare a `record`-sourced field without accounting for the
unit.** Group by the unit field, or convert. That rule is the only protection there is: the server
cannot help, because it never interprets any of this.

Dimensionless values declare `dimension: "count"` **explicitly**. An absent dimension means
*unspecified*, not *unitless* — which is what lets a client refuse to treat an unannotated field as a
measurement.

## Bookkeeping keys

Annotations carry anything a client wants stored alongside a type, not only meaning.

| Key | On | Meaning |
| --- | --- | --- |
| `revision` | type | A value the authoring client changes whenever it changes the type. Only meaningful where a single writer maintains it. |
| `superseded_by` | field | **Reserved.** Names the field that replaces this one. |
| `deprecated` | field | **Reserved.** Closed for writing, open for reading — the field keeps its roles so stored records stay findable and correctly interpreted. |

`superseded_by` and `deprecated` are reserved now so two products don't invent different names; the
mechanism is only needed once something has shipped and a meaning has to be corrected without
rewriting history.

## Type roles

Thin, deliberately. Only names two products need to exchange belong here.

| Role | Meaning |
| --- | --- |
| `track` | A series of records about one subject over time. |
| `plan` | An intention or schedule, possibly delivered by someone else. |

> **Open.** Whether `subject`, `person`, `project` and `folder` belong here depends on whether a second
> product needs to read them. Until then they stay `com.voptrack.*` and cost nothing.

## Recognition contracts

What a client must find before treating a type as something it understands. **A contract is a minimum,
never an exact match:** extra fields, extra roles and annotations a client does not know are ordinary
and MUST be preserved rather than dropped — by anything that copies, exports or re-serialises an
object. Discarding what you do not recognize is how a shared dataset degrades to the intersection of
its readers.

### Track

| Requires | |
| --- | --- |
| A field with role `timestamp` | of storage type `date_time` |
| At least one field with role `value` | carrying both `dimension` and `meaning` |

A type missing either is not a Track. It is not invalid — it is a type this client cannot chart, and
another client may understand it perfectly.

Where a `value` field has `unit_source: "record"`, the field named by `unit_field` MUST exist and carry
role `unit`. A reader MUST NOT aggregate or range-compare such a field without grouping by that unit
or converting.

### Delivery

| Role | On a field of type | Meaning |
| --- | --- | --- |
| `delivery.delivered_at` | `date_time` | When the object was offered. **Ordinary and queryable** — roles are not queryable, so discovering new deliveries is a predicate on this field. Required. |
| `delivery.supersedes` | `reference` | The earlier delivery this one replaces. Optional. |
| `delivery.source` | `reference` | On the **recipient's copy**: the object it was copied from. Required on a copy, absent on the original. |

These are **field roles**, not annotation keys — they identify which field plays a part, which is what
roles are for. An earlier draft listed them under annotations; that was wrong.

**An accepted copy must stand alone.** It carries values, not references into the sender's dataset, and
**its attachments must be its own** — a copy pointing at the sender's bytes stops resolving the moment
the sender revokes access, which is exactly the loss copying exists to prevent. Copying an object with
attachments therefore means copying the content too.

### Grant request

A type with role `grant.request` is an inert request for access. It creates nothing; only the owner
acting on it creates a grant. Anyone may write one in their own dataset, and it carries no authority
whatsoever.

## Governance

**Append-only is enforced, not trusted.** `specs/check-vocabulary.py` runs in `make check` and fails
on a removed entry, a changed scale, a changed meaning — anything but an addition and a clarified
`doc` string. That is the one rule everything else rests on: a dataset records `dimension: mass` and
nothing records which version of this document was meant, so a changed entry retroactively alters what
every stored record says. No reviewer catches a changed constant reliably; a diff check does.

**The version lives in the artifact**, not in a repository tag. A consumer pins `"version"` and can
check it at runtime, so a stale copy is a startup error rather than a silent mismatch. Additions bump
the minor; nothing is ever removed, so nothing forces a major. If something truly must go, that is a
v2 published alongside v1, and both stay valid for as long as data written under v1 exists.

**A name is promoted only when a second product implements it.** Product-specific concepts stay under
a reverse-DNS prefix and need nobody's agreement. Moving one to the unprefixed shared set requires an
actual second implementation, not an expectation of one.

**Disagreement resolves to staying namespaced.** If two products want different semantics for one
concept, they keep two prefixed names. A compromise name that each implements slightly differently is
worse than two honest ones, and append-only makes it permanent.

## Ownership

**Today this is Voptrack's**, published here because it is the one Voptrack repository that is not a
platform: iOS and Android both implement from it, and neither may own the constants.

**If a second product adopts it, that changes.** A vocabulary two products depend on needs a neutral home, a
named release owner and joint review before either can claim compatibility — otherwise "shared" means
"whoever edited it last". Product-specific concepts stay namespaced under a reverse-DNS prefix and need
no agreement from anyone.

Until that is arranged, treat this document as Voptrack's published position, not a joint standard.

## Open questions

1. Prefixing: `value` versus `track.value`. Unprefixed reads better and risks collision with a product
   that means something else by it; the rule above chooses unprefixed for the shared set and
   reverse-DNS otherwise.
2. Whether `allowed_units` is a product choice about a track or a fact about units. It currently lives
   with the track definitions rather than here.
3. A named release owner and review process, per **Ownership** above.
