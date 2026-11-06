---
schema: 1
kind: pr
pr: 688
title: Epic 53 story 53.11 — the Logs, Errors and diagnostics settings pages
date: 2026-11-06
session: ad829843-879e-4c35-b9bb-b62a3b316df1
---

## What happened
- The web Logs page searches the index (filters in the address, Follow a request, Lines around this, a live tail that pauses while the admin reads). It falls back to the files view with a notice.
- New Errors pages, with the new-group count in the menu.
- Diagnostics settings: retention and the cap through the supervisor's `/config/logs`, and detailed logs for an hour through a per-role level file.

## What we learned
- The `logs` role passed an empty `[logs]` to the index, so every configured retention was silently ignored. A setting that a separate process consumes needs a test from the file to that process.
- SIGUSR1 cannot express "debug until 11:00" and has no Windows. A small level file, polled by each logger, can, and it also reaches a process started later.
- Probing the index first and falling back on a malformed answer kept the old page's 30 tests passing unchanged but one, which now expects the probe call first.
- The App-level route test hit a generic mock answer and showed that a settings page must refuse to render a document that is not the settings document.

## Open
- The native Remote apps' Errors and Logs screens (story 53.11 §4).
- 53.12 dashboards and 53.13 performance budgets come next. 53.14–53.16 follow.
