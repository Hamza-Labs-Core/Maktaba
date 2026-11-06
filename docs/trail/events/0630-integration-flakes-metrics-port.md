---
schema: 1
kind: pr
pr: 630
title: Integration flakes: agent metrics file and real-Postgres ports
date: 2026-11-04
session: unknown
source: claude-trail.md
---

## What happened
- Merge 5f70c51ed. The cluster agent's metrics-file test waits until the lines it checks have been written instead of sleeping a fixed time a loaded runner could overrun.
- Real-Postgres tests get ports from `internal/testport` (below the outgoing-connection range, each held by a lock file), and a bundled Postgres that exits while starting puts its own last output in the error.
