---
schema: 1
kind: decision
pr: 58
title: Cluster phase 2 - the cluster channel
date: 2026-10-19
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Epic 31 phase 2 (#58): every worker holds a sealed WebSocket to a hub in the home's API. Logs and
metrics are files on the share (`<share>/maktaba/cluster/<ip>/…`), with a long-term archive on
the home. The DB is only a cache.

## What we learned
- A central scheduler was rejected: all work is split into queues, and each machine pulls jobs from the queues on the home.
