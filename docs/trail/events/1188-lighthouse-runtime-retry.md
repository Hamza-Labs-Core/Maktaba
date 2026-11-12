---
schema: 1
kind: pr
pr: 1188
title: "maktaba.tv's Lighthouse gate measures again a run Lighthouse could not finish, never a low score"
date: 2026-11-12
session: 112f87b9-bc63-4785-afc3-8b932c0159a5
agent: claude-code
---

## What happened
- On #1183, `website / lighthouse` went red with no page under the bar: `lhci collect` stopped
  on Lighthouse's own runtime error `NO_NAVSTART` ("Something went wrong with recording the trace
  over your page load. Please run Lighthouse again.") at `/legal/terms/`, run 3, and
  `scripts/lighthouse.mjs` exited 2.
- #1188 merged as 1f8a666d6. `measure()` now reads the runtime error's code from lhci's output
  (or from a kept lhr's `runtimeError`), and measures again the URLs the stopped collect left
  unmeasured (the one it stopped on and those after it, their partial runs dropped, the others
  kept with `--additive`), up to two more times, logging each retry to the console and
  `lhci.log`. Any other collect failure stops at once; a score under the bar is collected once
  and judged, never retried. Six tests with a fake collect cover it.

## What we learned
- Lighthouse's runtime errors (`NO_NAVSTART`, `NO_FCP`) are no measurement: lhci stops the whole
  collect on one, so a gate that treats a failed collect as a failed page reds pull requests that
  changed nothing measured.

## Keep doing
- Retry only what is not a measurement, and say so in the log; a real score fails on its first
  judgement.

## Stop doing
- Treating a crashed Lighthouse run as a page under the bar.

## Open
- None.
