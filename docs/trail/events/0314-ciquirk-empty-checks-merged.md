---
schema: 1
kind: incident
pr: 314
title: A red PR merged on an empty check list
date: 2026-10-25
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [313]
---

## What happened
#313 merged before its checks had started: the merge script read an empty check list as green. Its
lint and integration jobs then failed on main, fixed in #314.

## What we learned
- Main has no branch protection, so auto-merge merges at once.

## Keep doing
- Confirm checks are present and green yourself before merging.
