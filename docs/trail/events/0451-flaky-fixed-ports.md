---
schema: 1
kind: pr
pr: 451
title: Flaky - integration servers on fixed ports
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Integration servers collided on fixed ports 18080/19100 and 18081/19101 (api and streaming). They
now take OS-picked ports (#451).
