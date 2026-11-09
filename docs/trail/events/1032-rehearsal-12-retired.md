---
schema: 1
kind: pr
pr: 1032
title: Update rehearsal scenario 12 expects a retired component to have no row
date: 2026-11-08
session: 848393e1-ea22-4a25-a90f-b930df232381
---

## What happened
- Main's update rehearsal failed scenario 12 (run 42486851467 at 38ae35d89) on `ext.dizquetv = "", want "installed"`. The Plex sync removal (3da6b9dc9) retired `ext.dizquetv` (`shared/components/retired.go`). After v0.3.106 shipped without it, the rehearsal's previous release had no row for it, but the scenario's expected list still named it.
- The failure blocked every tag, because the release session tags only rehearsed shas. It also blocked every PR whose merge gate runs the rehearsal (#998, #1025).
- Scenario 12 now checks that every id in `components.RetiredComponents` has no row after the update. Its check that no component row goes in an update leaves retired ones out, so it holds whether or not the previous release seeded the row. Merged as 56456b922 with the rehearsal green on 4acd7f69d.
- Two sessions opened the same fix within a minute (#1031 and #1032). #1031 was closed as the duplicate because it lacked the second change.

## What we learned
- A removal passes its own rehearsal while the previous release still has the component, then breaks the rehearsal one release later, when a release without it becomes the previous one. A removal should update the rehearsal's expected components in the same PR, and check the retired list rather than name each id.
- When a gate fails the same way on main, check main's latest rehearsal run before debugging the PR's branch.

## Open
- A session that finds main's rehearsal red should say so on the thread that owns the release before opening a fix, so two sessions don't race to fix it.
