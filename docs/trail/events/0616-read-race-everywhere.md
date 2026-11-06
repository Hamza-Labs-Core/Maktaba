---
schema: 1
kind: pr
pr: 616
title: A change made while a page is read again is kept, everywhere
date: 2026-11-03
session: unknown
source: claude-trail.md
prs: [613]
---

## What happened
- Merge 0ae136a64. The race #613 fixed on Android's video page was also on the Apple Player video page, the series pages on Apple and Android, the web library grid, and both Remote apps: a change saved while the page's read was in flight was undone on screen while the home kept it.
- The fix keeps the changes made since the read began; in the Remotes, a read that began before an action's save is dropped.
- `VideoPage`, `SeriesModel`, `LoadProblem`, `loading` and `RemoteModel` moved into MaktabaKit so `swift test` covers them, each with a latch test that failed before its fix.
- The Player UI test warm-up waits longer, records what it hits as an expected failure and goes on, since a new iPad simulator's slow first launch had been failing each shard's first test.
