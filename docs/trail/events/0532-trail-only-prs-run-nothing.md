---
schema: 1
kind: pr
pr: 532
title: A PR that changes only the trail runs no CI job
date: 2026-11-02
session: unknown
source: claude-trail.md
---

## What happened
- Rule: trail updates go in their own PRs, with no CI and no tests. Every code PR appending to the trail made every open PR conflict on each merge (a main merge and a CI restart each).
- #532 (merge 091ebe1e): the `changes` job has a `repo` output, false when only docs/claude-trail.md changed. lint, pr-body-check and affected-tests are gated by it, so a trail-only PR runs only `changes` and `ci-success`. CodeQL default setup (cannot be filtered, not required) and the labeler still run. A push to main still ran full CI (the tag guard needs it; changed in #553).
- Dropped at the time: one file per change under docs/trail/.

## Keep doing
- Trail entries go only in separate trail-only PRs, never in code PRs.
