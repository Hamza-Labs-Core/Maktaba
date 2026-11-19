---
schema: 1
kind: pr
pr: 1460
title: "The Core home's idle memory is recorded, no longer held to a budget (N8 removed)"
date: 2026-11-19
session: 753035df-3990-44b1-9dff-f305bbe9c3cd
agent: claude-code
prs: [1445]
---

## What happened
- #1460 merged as a4c115364. N8, the Mac Core home's idle-memory budget (`idle_rss_mb` ≤ 700
  MB, Epic 51 story 51.17), is removed: it measured only an empty home.
- `bundle_size.py` no longer checks it, and `baseline.json` no longer names it.
  `core-home.sh` still records the figure in `core-home.json`, and the budgets print it as
  an `info` line. N7 and N9 are unchanged.
- On PostgreSQL 18 (#1445) the Core home idled at 712.6 to 749.1 MB; on 16, main measured 640
  to 683 MB. `io_method=sync` took 18's three I/O workers away (about 9 MB). The rest is each
  backend's larger footprint (ICU linked in, a larger binary) across about 40 idle
  connections. GLM-5.3 approved #1460; its one minor is issue #1461.

## What we learned
- On one runner, the idle figure moves by about 20 MB between runs of the same commit.
- Turn a budget's figure into an information line before dropping it, so its trend stays
  visible.

## Open
- Two test comments still cite the removed budget: issue #1461.
