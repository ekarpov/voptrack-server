# Research aggregate suppression

Implemented in the Voptrack Console on `dev_webui`, 2026-10-14.
This is study-specific presentation policy, not a Tessera permission or API change.
Pinned server: Tessera `v0.1.0-alpha.33`.

## Policy and persistence

The existing authorized Research owner explicitly saves a threshold on
`/studies/{slug}/policy`. Five is a suggestion only; no study receives a
threshold implicitly. Missing policy withholds analytics, including direct panel
CSV URLs. Participant administration and raw study exports remain available.

Input is 1–10 ASCII digits whose integer value is between 2 and 2147483647.
The upper bound is the portable signed 32-bit storage/input limit, not a privacy
rule or an enrolment limit. Fractions, signs, whitespace and overflow are refused.
K above enrolment is permitted and may withhold all results. Disabling is absent.

Policies can change on draft, open and closed studies. Lowering requires the
explicit confirmation checkbox. Each save checks the submitted revision under the
policy-store lock; stale forms show the current policy and preserve entered K for
a fresh explicit submission. A failed save changes nothing. Policy changes never
publish consent or edit recruitment, disclosures or tracks.

Each policy is an independent Console document:
`research-policies/{slug}.json`, containing `version: 1`, integer `k`,
positive `revision` and UTC `changed` time. Existing document persistence,
backup and reconstruction apply; the namespace is included in file-to-state-store
migration. The public study/consent JSON contracts are unchanged.

## Contribution and calculation

Participant identity is the study account from consent records, not a store
display name or a record ID. Each group holds a set of these accounts; repeated
records, agreement history and repeated identities cannot inflate its size.

- Record count: at least one matching record.
- Numeric count, sum, average, minimum and maximum: at least one non-null numeric
  value entering that group. Zero, negative and cancelling values contribute.
- Numeric count counts values; record count counts records. Neither is the
  suppression contributor count.
- Ungrouped: union of identities for that question.
- Participant/store: one contributor for a nonempty participant group; suppressed
  under every permitted K.
- Consent cohort: latest explicitly agreed version for all that participant's
  readable records, not consent at each record's timestamp.
- Period: independent sets per local day/week/month. Weeks start Monday.
- Text/reference: independent sets per full group identity, including the null
  grouping value. References retain metadata, dataset and object identity.
- Exact distribution: independent sets per exact numeric value. Release the
  whole distribution only when every category passes K. More than twelve
  distinct values invokes the existing adaptive-range format; that format is
  unavailable in this slice, irrespective of contributor counts.

Date filtering precedes both tallying and counting. From is inclusive; the shown
To date includes the whole local day; the internal window is half-open. The
existing millisecond upper-bound conversion, timezone/DST rules and exact
rational arithmetic remain. A missing date cannot enter a period or dated filter.
A missing numeric value does not contribute to a numeric measure. A zero sum
with sufficient contributors is a real released zero.

Successful empty and all-null numeric calculations have distinct internal
outcomes, separate from incomplete reads. Both have zero contributing
participants and therefore receive the same public `suppressed` state.
Neither HTML nor CSV discloses an exact zero through an alternative empty/no-value
message. Released counts of zero cannot bypass K.

Readable-data eligibility is unchanged: retained frozen-share records after
withdrawal, suspended participants' readable records and records with historical
agreement gaps remain eligible. No new consent exclusion is introduced. Erasure
and rejoining retain existing semantics. Raw exports retain their existing
`consent_missing` flags and participant/status rows.

## Release and presentation

One release model serves overview cards, detailed panels, tables, chart geometry
and panel CSV. Raw tally values, identity sets, skipped participant names and
hidden category labels do not reach Research templates. Catalog store-name
lists are also stripped before rendering.

Suppressed period labels remain calendar labels; points are gaps, with no line
across them. Sensitive suppressed categories are collapsed to one generic row,
without their labels or exact number. No grouped totals, subtotals or percentages
are added. Chart scales, SVG titles and accessible descriptions use released
values only. Tables use short translated status labels, with the explanation
above them. Research numeric panels omit auxiliary record/value counts.

Failed source discovery, ambiguous participant/store mapping, catalog failure,
skipped store, failed later page, unknown identity, inconsistent numeric
aggregate and truncated rows withhold the whole affected panel as `unavailable`.
This is different from small-group suppression. Retry/narrow-window guidance
remains visible. Adaptive ranges use the distinct `unsupported` explanation.
Home/personal calculations and raw-export discovery retain their prior behavior.

## Panel CSV format: Research suppression version 1

Home/personal panel CSV is unchanged. Raw study CSV/JSON is unchanged.
Research panel CSV preserves the original four columns, spelling and order,
and appends one stable English column:

```csv
group,value,records,values,status
```

`value` remains the selected measure at existing export precision.
`records` and `values` retain their original meanings. For a released
fieldless record-count panel they retain the corresponding counts. For all
numeric-field panels both auxiliary columns are blank. Every withheld row has
blank numeric cells; never a sentinel zero. Suppressed sensitive group labels
are blank; calendar labels remain on suppressed period rows. CSV formula
escaping for labels remains in force.

Possible row statuses: `released`, `suppressed`, `unconfigured`,
`unavailable`, `unsupported`. A wholly withheld panel has one generic row.
Mixed period/category panels contain released rows and the allowed suppressed
rows. Exact distributions are all released or wholly withheld.

Existing `# track`, `# measure`, `# grouped by` metadata remains.
Two additive variable-length metadata records precede the header:

```text
# research suppression,1,K,revision,panel_status
# window,date_field,from_utc,to_utc,inclusive/exclusive
```

K and revision are decimal integers, or empty when unavailable/unconfigured.
Bounds are RFC3339Nano UTC instants or empty for an unbounded side; upper is
exclusive. Panel status uses the same codes as rows; `suppressed` may mean
a mixture of released and suppressed groups. Readers should skip records whose
first cell starts `#`, then use the header. No skipped-store names, hidden
counts or labels are exported.

Downloads recompute under current policy/access. A download made after edits,
renewal or policy changes may differ from an earlier displayed panel.

## Limits and ownership

This is safer aggregate presentation, not anonymity or a new access boundary.
Researchers retain raw-data access and per-participant administration/completeness.
Totals, overlapping filters, adjacent periods, repeated observations, extreme
values, contribution dominance and external knowledge can reveal information.
Independent ungrouped panels can permit subtraction; no query budget or
complementary suppression across separate requests is implemented. A minimum or
maximum can belong to a single participant even when K contributors qualify.

Fixed bins, additional cohorts, shared dashboards
and stronger study-specific disclosure policies are deferred Voptrack work.
Only missing generic server capabilities belong upstream in Tessera.
No common cross-store snapshot is established by the current read loop.

## Verification scope

Console tests cover all measures/groupings around K, prolific contributors,
null/zero/negative/cancelling values, weighted averages, filtered periods and DST,
unknown identities, skipped/failed pages, actual truncation, distribution modes,
scrubbed HTML/SVG/CSV, validation/failed/stale saves, permissions/CSRF and
file/remote document reconstruction. Synthetic rendering covers seven languages.

The disposable Research gate checks explicit configuration, K=3 with two people
and three records, release at K=2, policy lowering/staleness/CSRF, filtered days,
monthly sums, CSV precision/columns, hidden participant/cohort/distribution labels,
unchanged raw exports/consent lifecycle and policy restoration after loss of
Console files. Its record-counting defect proof is recorded in the workflow plan.

The gate checks reconstruction from the surviving Tessera state store and, with
saved study dashboards, full backup-archive restoration onto fresh volumes.
File-to-store policy migration is included in the existing namespace mechanism;
tests check inclusion and file/remote reconstruction rather than a dedicated
full deployment migration scenario. Real-device OAuth, assistive-technology
testing, independent linguistic/ethics review and real study deployment remain
outside this slice.

Saved study dashboards use this same Research input, answer and release path.
Only panel definitions are persisted in `dashboards/study-{slug}.json`, separately
from `home` and account keys. Policy thresholds are read again for every view
and panel CSV request. Missing policy keeps authorized definition management
available, while answers remain withheld. Stored query scopes must match the
containing study; neither computation nor links can follow a different scope.
The Research gate now also restores these definitions and policy from an archive
onto fresh disposable volumes using the existing recovery commands.
