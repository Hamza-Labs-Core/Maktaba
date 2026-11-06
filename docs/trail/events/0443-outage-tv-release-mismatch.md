---
schema: 1
kind: incident
pr: 443
title: TV held on the old release - release_mismatch
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [446]
---

## What happened
On the live v0.3.44 update, TV change #58 was held with `release_mismatch` and the TV stayed on
v0.3.39 streaming. A running TV slot asked to run another release kept the old one.

Why: three owners (supervisor, staging, switchover) each moved slots and nobody reconciled them.
Fix: every bind or adoption restarts a running slot whose release differs from the one it was told
to run (`matchBinding`, #443, #446). Rehearsal scenario 11 (#446) fails on pre-#443 code.

## What we learned
- When several components can move the same state, one of them has to reconcile it on every change.
