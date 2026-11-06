---
schema: 1
kind: pr
pr: 539
title: Libraries accessibility audit: warm-up and lazy posters
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [566]
---

## What happened
- #566, folded into #539. Two causes.
- A shard's first app launch on a new simulator spends 13-36 s setting up the XCTest automation session and stays slow for 1-2 min; PlayerUITestCase now warms up once per process.
- On the iPad root, rows build cards and fetch posters lazily, and the audit reaches every element, so posters loaded mid-audit. `loadEveryPage` now scrolls each row to its end and back and waits until no more posters are asked for.
