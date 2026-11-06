---
schema: 1
kind: decision
pr: 586
title: Epic 53 spec: logs, errors, problem reports, a UI that never goes dark
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 8ea67af5, 17 stories. Logs are written to per-component files by the supervisor. The search index is a separate SQLite file in a crash-isolated `logs` process, chosen over Postgres so search works when Postgres is down and logs stay out of backups and the cluster copy.
- A recovery page and status endpoint served by the supervisor (admin "recovery pass", because web sessions need Postgres), safe mode, and a CI fault test that blocks releases.
- Epic 50 edited so 53.17 supplies the cloud's logs, errors and metrics and 53.06 draws 50.11's banner.

## Open
- Three owner questions in README section 12 (answered in #588).
