---
schema: 1
kind: decision
pr: 56
title: Cluster phase 1 - one home, machines pull jobs
date: 2026-10-18
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Epic 31 phase 1 (#56): the home holds the DB and queues. Machines find it over mDNS, join with a
pairing code, get a least-privilege Postgres role, and pull jobs by capability.
