---
schema: 1
kind: pr
pr: 516
title: affected-tests: changed code needs its tests changed
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- #516 merged at 65d09858 once lint, affected-tests, CodeQL, unit, integration, android-apps and apple-apps (Apple apps, Mac UI) passed on 7b93f4d0.
- The new gate blocked first pushes of later PRs (#533, #536, #543) until real tests were added in the mapped test files.

## Open
- The first coverage-map run on main was not checked at merge.
