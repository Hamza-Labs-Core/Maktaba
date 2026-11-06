---
schema: 1
kind: pr
pr: 472
title: CI apt installs fail over a stalled mirror
date: 2026-11-01
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Host apt installs in CI get retries, timeouts and an 8-minute step limit, after stalled mirrors hung jobs for 30 minutes.
