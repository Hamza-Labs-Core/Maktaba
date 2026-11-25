---
schema: 1
kind: pr
pr: 1679
title: "The TV launcher's e2e lost its saved home to its own init script, which touched a file:// page's localStorage too early"
date: 2026-11-24
session: 8fc89d92-c76f-4cb4-b0eb-8036123ea989
agent: claude-code
prs: [1442, 1453, 1607]
---

## What happened
- `web/e2e/launcher/launcher.ui.spec.ts` ("probe, code sign-in by bearer, play, leave") failed now
  and then on PRs with no TV change (#1442, #1453, #1607 run 45493721971): after `page.reload()`
  the saved home's `tv-frame` was not there.
- **Cause (the test).** Its `addInitScript` read and wrote `maktaba.launcher.lang` at document
  start. On a `file://` page Chromium can answer so early an access with an empty store for the
  document's life. With the built launcher under load: 482 of 600 lost with the init script,
  0 of 2,950 without it.
- **#1679** takes the language from the context's `locale`. It also fixes two other races the
  load runs showed: contexts closed while `/tv/` was being fetched or the network log awaited a
  response, and the probe's 700 ms against a starved mock. 1,760 of 1,760 under load, against
  151 of 240 on main.

## What we learned
- Two explanations looked settled and were wrong: "only a reload over a cross-process iframe"
  (a new page loses it too) and "Chromium 156, not 143" (141 and 143 lose it under load). The
  first fix, closing the page and opening a new one, still failed 42 of 880.
- One "0 of 300" that supported the first fix had run without its early read: an exported
  variable doesn't outlive a shell call. A control that can't fail proves nothing.
- The odds depend on how early storage is touched: 1.6–11 % at document start on a bare page,
  9 of 500 at `DOMContentLoaded` or `load`, 0 of 500 from a `defer` script, 0 of 500 over http.
- `page.unrouteAll({ behavior: "wait" })` hands pending requests back, so a handler still in
  `route.fetch` then fails with "Route is already handled".

## Keep doing
- In a `file://` e2e, set state by the browser's own means (locale, the UI), not by an init
  script that touches `localStorage`.
- Answer mocks from disk (`route.fulfill({ path })`) and log from synchronous events
  (`response`), so a finished test leaves nothing running.
- Run the fix under the same load as the failure before believing it.

## Stop doing
- Explaining a flake from a minimal repro without running the product through the same steps.

## Open
- Why Chromium answers with an empty store was not traced in its source, and no TV was tested.
- `webos.ui.spec.ts` and `tizen-avplay.ui.spec.ts` still seed `localStorage` from an init script.
  They load the page once, so nothing is read by a second document. `webos.ui.spec.ts` still
  answers `/tv/` by `route.fetch`.
