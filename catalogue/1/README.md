# Voptrack Track catalogue, version 1
The app's ready-made Track types, for writers other than the app (the Voptrack Server household web UI).

**Layout.** `manifest.json`: the catalogue version, the source revision (git sha), every file with its SHA-256,
and `internalTypes`. `types/<type id>.json`: one document per type, dependencies included (the Track root,
the Track types, part types such as a workout's sets, and the types their reference fields reach: Subject,
Source, Category, plans). Each holds `installation` (the exact `POST datasets/{dataset}/meta` body the app sends,
as text and parsed), `fields` (meta_id, type, required, reference targets, and what the app reads each with),
`dependencies` (parents, parts, references) and, for a Track, `presentation` (headline metric, period rule,
spending flag, units, subject suggestions). `vectors/<type id>.json`: one record body exactly as the app's
remote encoder writes it (`POST datasets/{dataset}/objects/{type}`) for a fixture draft.

**Generated, never edited.** `bash tools/export-track-catalogue.sh catalogue/1` regenerates it from
`Packages/VoptrackCore/Sources/VoptrackTestSupport/TrackCatalogueExport.swift`: sorted keys, stable order, no
timestamps. `TrackCatalogueExportTests` fails when these files differ from source.

**Compatibility rule.** Within a version, installed types are only read, never re-declared. A web writer uses
a type only where the dataset already holds it in a compatible shape; on a mismatch it disables authoring and
never repairs the schema. A declaration change that alters an installed type needs a new type id; any change to
a type document needs a new catalogue version (`BundledTrackCatalog.catalogueVersion`, guarded by hash).

**internalTypes** (in the manifest, each with its reason) are the app's own bookkeeping and configuration:
a web writer never writes them, nor the root's `writeToken` field.

Tessera's specs and vectors remain the protocol authority; this catalogue only describes the app's types on it.
