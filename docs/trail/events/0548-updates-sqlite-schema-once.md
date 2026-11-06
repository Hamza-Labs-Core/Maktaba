---
schema: 1
kind: pr
pr: 548
title: api/internal/updates back under the 30 s budget
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge c428badb0. `api/internal/updates` broke the 30 s unit budget (31.2 s, PR #532 run 41493179001).
- Cause: per-test SQLite setup, about 40 CREATE statements each (SQLite re-reads its schema after each), plus a file lock taken and released on every statement (61 % of CPU).
- Fix (test-only, `db_test.go`): build the schema once per run and copy the file into each test's temp dir; hold the lock for the test's life (exclusive locking); synchronous=OFF and memory journal in the DSN so they survive a reopen. Locally 13.1 s to 5.0 s; CI 31.2 s to under 12.2 s; API unit total 3m55s to 2m53s.

## What we learned
- SQLite re-reads its schema after each CREATE and takes a file lock per statement; building a test schema once and copying the file is much cheaper.
