---
schema: 1
kind: decision
pr: 148
title: Live install is Maktaba.app with bundled Postgres
date: 2026-10-23
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [158]
---

## What happened
Everything runs as one Mac installation, Maktaba.app, with Postgres bundled because it is small
(Epic 36 spec, #148). The home Mac moved in with `migrate-from-dev` (#158) on 2026-10-23.
The old Docker Postgres was stopped, not deleted.

## Open
- The Docker Postgres removal was never finalized.
