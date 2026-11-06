---
schema: 1
kind: pr
pr: 577
title: Tag guard no longer waits on the iPhone/iPad Player UI shards
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge d3f00845. `tag-guard.sh` judges a failed CI run by its jobs: all must be success or skipped except `IGNORED_CI_JOBS` (the iPhone/iPad shards) and `ci-success`. Mac UI, the UI test builds and cancellations still block, and a run in progress is still waited for. Each release prints the ignored failures as a warning.

## Open
- Remove `IGNORED_CI_JOBS` once the iPhone/iPad shards are fixed.
