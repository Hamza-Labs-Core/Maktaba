---
schema: 1
kind: decision
pr: 483
title: Story agents stop editing the trail and changelog
date: 2026-11-01
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [463, 465, 471, 472, 473, 474, 475, 476, 477, 479, 480]
---

## What happened
The Epic 48 lead wrote the trail and `CHANGELOG.md` entries for a batch of merged PRs from their PR bodies, in one docs PR.

## Keep doing
- Story agents do not edit the trail file or `CHANGELOG.md`; the lead batches those entries from merged PR bodies, so open PRs stop conflicting.
