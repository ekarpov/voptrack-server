# Voptrack Track catalogue, version 2
Same layout, rules and generator as [version 1](../1/README.md); read that first. Version 1 stays as published and is never edited.

**What changed.** Every Track document's `presentation` gains what the household UI needs to read a period figure:
- `reads`: what the Track's period figure is. `"latest"` (the newest reading: weight, a balance), `"sum"` (amounts added
  up: steps, spending, distance, a meter's consumption), `"count"` (how many records or doses) or `"average"` (the
  period's mean: pulse, a night's sleep), or `"runtime"` where the declaration cannot decide it (see `readsReason`).
- `readsReason`: null, or why `reads` is `"runtime"`. Only Walking, Hiking and Cycling: they lead with distance once
  their records carry one, else with time.
- `readsAsLevel`: the page's own flag. A level's page draws lines and a count of readings, not bars.
- `periodRule.spans`: the four chips, Week, Month, Year and All, each with its policy (`current` calendar period so far,
  or `allHistory`). `periodRule.kind` is still `calendarSoFar` for every Track.

**How `reads` is decided.** From the declaration alone, by the code the app runs (`TrackReads`): the type's headline
metric (`defaultCollectionMetrics`, led by the metric that measures the page's lead), and where the type declares none,
the page's own branches (`TrackReads.pageReading`). A test checks it against the app's runtime path for every Track type.
A level that leads with its latest value in Home's card (weight) reads `"latest"`, though its page shows an average.

**Not changed.** Types, fields, installation bodies and vectors are the same shapes as in version 1. Each document's
`catalogueVersion` is 2, so every document hash differs from version 1's. A writer pinned to version 1 keeps working.
