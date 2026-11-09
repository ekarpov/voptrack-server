# App Track catalogue

Version **2** is the current generated app catalogue. Version 1 is retained for
consumers pinned to that version. Each contains 72 Track types, the root,
parts and referenced types (80 definitions total), and 75 synthetic record vectors.
Read [version 1](1/README.md) for the format and compatibility rules, then
[version 2](2/README.md) for the presentation additions.

These are byte-for-byte exports from the Voptrack app repository, not Tessera
protocol definitions. [source.json](source.json) records the copied source commit,
each export's own source revision, and the SHA-256 of each manifest. Manifests
bind every type document and vector to its SHA-256. Preserve those revisions;
the export revision can precede the commit from which the files were copied.

Do not edit generated version directories or translate them into the old sketch.
Refresh from a new app export, preserving already-published versions. Consumers
must select a version explicitly, check installed type compatibility and respect
internal types and fields. Importing this reference data does not install or
change dataset types, authorize a web writer, or add Console functionality.

The historical `specs/voptrack-tracks-v1.json` is a separate illustrative sketch,
with different type IDs and format. It and the shared vocabulary remain
append-only. `make catalog-check` still validates that sketch and intentionally
reports its unresolved annotations; it does not validate these app exports.

## Console reader

The Go package explicitly pins version 2 and its manifest hash. It embeds only
that version, verifies every type/vector hash at startup, and exposes no install
or write operation. The Console uses the exported read modes only against a
compatible effective installed declaration; conflicting declarations withhold
combined summaries. Historical missing optional root bookkeeping (`addedAt`,
`writeToken`) is tolerated for read-only presentation, never for authoring.

`make check` includes catalogue integrity tests. Console tests exercise the app
record vectors, count/latest/money interpretation, unit and odometer-mode
separation, schema drift, internal fields, and All-history behavior. A separate
actual-image integrity negative control is recorded in the review receipt; these
checks do not qualify a web installer, writer, reference creation, canonical-unit
conversion, duration/overlap or reset-aware counter algorithms.
