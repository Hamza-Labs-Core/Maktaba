---
schema: 1
kind: pr
pr: 680
title: A catch-up is queued once, and a non-leader forgets an off time that is over
date: 2026-11-06
session: b705488f-6ddd-4a03-8d08-60545e9b7ffa
prs: [659]
---

## What happened
- A review of #659 found two holes in the leader hand-off. An off time a runner read from the status rows at start was not in `_off_mine`, so the "another leader already reopened it" check skipped it, and with three workers a second catch-up could follow the first. And a worker that lost the lock kept reporting `off_since` for a component that was on.
- Every entry in a pass's `back` set is now checked against `job_stages` before the write: only the stages this write opens again are caught up, and `_off_mine` is gone. A pass that does not lead drops the off times the database says are over.

## What we learned
- Two workers in a test do not show the bug: it needs a third that starts while the component is off, so that its off time comes from the status rows and not from its own write.
- In a test, losing the leader lock is a monkeypatch of `engine.leader.leader` to an async context manager that yields False. On SQLite the runner always leads.

## Keep doing
- Decide from the database's state at the moment of the write, not from what this process remembers doing: here, the `job_stages` rows say whether the stages are still off.
