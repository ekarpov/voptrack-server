# The bundled track catalog format

This document describes the historical `voptrack-tracks-v1.json` sketch. The
current generated app catalogue is maintained separately in
[catalogue/](../catalogue/README.md), with versioned manifests, exact installation
bodies and synthetic record vectors. Its format and compatibility rules are
documented there. The sketch remains unchanged under the append-only policy.

`voptrack-tracks-v1.json` declares the tracks Voptrack ships. It exists because **every platform must
create the same types**: a type ID and its field IDs are written into a dataset, so iOS and Android
disagreeing there gives one person two incompatible types for the same concept, in the same storage,
with no way to merge them afterwards.

It is validated by `specs/check-tracks.py`, which `make catalog-check` runs. The validator is the
definition; this document explains what it checks and why.

**Status: the catalog in this file is a sketch.** Its own `status` field says so. The authoritative
catalog is `BundledTrackCatalog.swift` in the client, and this file is not generated from it yet.
Until it is, the format and the validator are real and the contents are illustrative — which is the
useful half, because the format is what a second platform needs agreed before it writes anything.

**`make catalog-check` currently fails, on purpose.** Four findings, all the same one: the sketch
uses `allowed_units` and `unit_sharing`, and neither is registered in the vocabulary's
`annotation_keys`. That is open question 4 in the catalog's own `open` list — whether a product
choice about a track belongs in the vocabulary at all. Registering them would settle it by fiat, and
the vocabulary is append-only, so a wrong entry cannot be taken back. The check stays red until
someone decides.

This is also why `catalog-check` is a separate target rather than part of `make check`. A gate that
is permanently red is a gate everyone learns to ignore, and `make check` guards things that must
never break. It folds into `check` when the catalog is generated from the client rather than
sketched by hand, and the remaining findings are real failures instead of recorded questions.

## What the validator checks

### Against the Tessera protocol

These are not Voptrack's rules. A catalog that breaks one produces a type the server will refuse, so
catching it here is catching it before a migration rather than during one.

| Rule | Where it comes from |
| --- | --- |
| Type IDs match `^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$` | `pkg/protocol/identifiers.go`, `metaID` |
| Field IDs match `^[A-Za-z_][A-Za-z0-9_]*$` | same, `fieldID` |
| Neither is a reserved word, case-insensitively | the protocol's Appendix A |
| A field is never named `id` or `meta_id` | reserved by the record shape |
| Identifiers are 1–255 bytes | `ValidateIdentifier` |
| `storage_type` is one of `integer`, `float`, `text`, `boolean`, `date_time`, `media`, `json`, `blob`, `reference` | `pkg/protocol/value.go` |

There is no `number` type. A catalog saying `number` is the most likely first error, because it is
what a person means and not what the protocol has.

### Against the vocabulary

`voptrack-vocabulary-v1.json` is append-only and published; a catalog referring to something it does
not define is a catalog that means nothing on the other platform.

- Every role in `roles` exists in the vocabulary's `field_roles` or `type_roles`.
- Every `dimension` annotation exists in `dimensions`.
- Every unit in `unit` or `allowed_units` exists in `units`, and belongs to the field's `dimension`
  when one is declared. A `kg` under `pressure` is the error this catches.
- Every `meaning` exists in `meanings`.
- Every annotation key exists in `annotation_keys`.

### Structural

- No duplicate type IDs, and no duplicate field IDs within a type.
- `parent` names a type in this catalog, or is `null`.
- No inheritance cycle.
- **A subtype never redeclares a field it inherits.** Tessera merges effective metadata from the
  ancestry; redeclaring is the ambiguity that merge cannot resolve.
- A field declaring `unit_source: "record"` names a `unit_field` that exists in the same type.
- A field declaring `unit_source: "fixed"` names a `unit`.

## What the validator deliberately does not check

- **Localization keys.** `name_key` is resolved by each platform from its own catalogue; this file
  never carries display text.
- **Platform assets.** `icon` is a neutral name each platform maps. An SF Symbol means nothing on
  Android.
- **Whether a track is a good idea.** Product judgement is not mechanical.

## Evolution

Additive only, for the same reason the vocabulary is. A field's meaning is never changed in place:
add a field carrying the new meaning and mark the old one deprecated, so records written under the
old meaning keep it. A type ID, once shipped, exists in datasets forever.

The validator does not yet enforce append-only across versions — `check-vocabulary.py` does that for
the vocabulary and the same treatment belongs here once the contents are real rather than a sketch.
