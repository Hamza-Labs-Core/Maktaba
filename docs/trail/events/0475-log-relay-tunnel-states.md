---
schema: 1
kind: pr
pr: 475
title: The relay records tunnel states in order
date: 2026-11-01
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Flake fixed at the root (relay e2e): the relay records a home online before it is routable and offline after removal, one at a time per slug.
