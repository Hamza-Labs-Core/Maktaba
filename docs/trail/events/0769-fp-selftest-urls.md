---
schema: 1
kind: pr
pr: 769
title: The first-party parity self-test keeps the test URL's sslmode; per-checkout dev API
date: 2026-11-07
session: 8687f2a4-808d-47dd-93b7-2c1167e0f385
prs: [750]
---

## What happened
- `test_parity_selftest.py` failed on main on a Mac whose `MAKTABA_TEST_PG_URL` ends in `?sslmode=disable`. The harness cut the URL at its last `/`, which dropped the query, and glued `&sslmode=disable` onto the database name of the plugin API's DSN, so lib/pq required TLS. The URLs are now parsed: the query is kept, and `sslmode=disable` is added only when the URL does not set it.
- The dev plugin API is now built per checkout, every time. A build that fails skips the test with its error.

## What we learned
- CI's test URL has no query, so a harness that rebuilds URLs by hand passes CI and fails on every machine that names its scratch server with one. asyncpg's default `sslmode=prefer` hid the dropped query on the Python side; lib/pq's default (require) showed it on the Go side.
- A test binary cached at one fixed temp path is shared by every worktree on the machine.

## Keep doing
- Run a harness's own tests with the URL forms in use on dev machines (with and without a query), not only CI's.

## Open
- `tests/engine/fixtures_db.py` also drops the URL's query when it names the template database; asyncpg's default makes that harmless today.
