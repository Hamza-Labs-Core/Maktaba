---
schema: 1
kind: pr
pr: 1300
title: "Main's wiki database and help mockups regenerated after #1243"
date: 2026-11-14
session: 43ed6e31-0740-4d65-b657-61c1a1d3ba1b
agent: claude-code
---

## What happened
- #1300 merged as 3cd6a2c82. Two generated outputs on main were out of date, so two checks
  failed on every pull request that ran them: `wiki` (`generate_wiki.py --check`, 253 endpoints
  counted where `openapi.yaml` lists 254) and `unit` (`make test-tokens` → `mockups.test.mjs`:
  `help.en.html` and `help.ar.html` said 40 help topics where the page has 41).
- Bisected over main's first-parent merges: `ee9dfad96` (#1277) passed both, `279388e03` (#1243,
  the Plugins page) failed both, and every merge after it inherited the failures. #1243, #1261
  (cast grant) and #1277 (TV launcher) each regenerated these outputs against a main that lacked
  the others' additions; each passed its own CI, and the merged result was never re-checked.
- The 58.07 session found the wiki half first and carried the fix in #1267; the mockup half
  surfaced as #1267's `unit` failure. The fix is generator output only (`generate_wiki.py`,
  `mockups/make.mjs`). #1300 was held for a while so #1240 (which also regenerated the wiki)
  could land first, then merged at once when the mockup half showed it was failing every PR's
  `unit` job.

## What we learned
- A generated file that counts things (endpoints, help topics) goes stale whenever two pull
  requests that each add one merge in turn: both pass on their own, the sum is never checked.
  Nothing here re-runs CI on the merged result, so main can be red in a check that no pull
  request's own run shows.
- Bisect main's first-parent merges with the check itself (`--check`, the node test) to find the
  merge that broke it in seconds; a failing check on many unrelated pull requests at once points
  at main, not at the pull requests.
- Two pull requests carrying byte-identical regenerated lines merge cleanly; ones that each
  count their own additions conflict on the count line, and the fix is to regenerate, not to
  pick a side.

## Keep doing
- When an unrelated check fails on a pull request, run it on origin/main before rerunning it.

## Open
- A merge queue (or a scheduled `--check` of the generated outputs on main) would catch this the
  moment it happens instead of on the next pull requests' runs.
