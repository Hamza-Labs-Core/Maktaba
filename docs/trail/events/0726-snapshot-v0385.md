---
schema: 1
kind: pr
pr: 726
title: Trail snapshot v0.3.85
date: 2026-11-06
session: 0ed2826a-4596-47da-969d-511eda5a9303
---

## What happened
- A scheduled session built `docs/trail/snapshots/v0.3.85.md` from v0.3.84, folding in the events for #699, #700, #701 and #704, and copied it to `current.md`. The tag release process now lives in the Keep doing item on automatic tags.

## What we learned
- On a trail-only PR, every CI job is skipped except the repo lint, affected-tests and CodeQL. CodeQL's rust analysis took the longest, well over ten minutes, so wait for it, not the lint, before merging.
- Two other worktrees on trail-snapshot branches (`t3/trail-snapshots-new-tags`, `t3/trail-snapshots-catch-up`) were present at the same time. Before building, check origin/main's `snapshots/` again right before opening the PR, so two sessions do not snapshot the same tag.
