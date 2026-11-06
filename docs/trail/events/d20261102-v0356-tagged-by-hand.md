---
schema: 1
kind: release
title: v0.3.56 tagged by hand; tags stop waiting on iPhone/iPad shards
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [539, 577]
---

## What happened
- v0.3.56 tagged by hand at 655b565 (#539's final PR head, the same tree as main's merge commit 99e51114c). Its PR CI passed every check, and the update rehearsal passed on 99e51114c. Main CI on 99e51114c had failed three iPhone/iPad UI shards.
- Decided: tags no longer wait on those shards; #577 changed the tag guard to match.
