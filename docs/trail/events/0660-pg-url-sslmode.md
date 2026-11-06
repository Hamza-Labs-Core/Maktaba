---
schema: 1
kind: pr
pr: 660
title: One MAKTABA_TEST_PG_URL works for every Go suite
date: 2026-11-06
session: t3-delegate-aa865d4e-422b-48fe-b7d8-1a0afef5db09
---

## What happened
- #644: the pipeline's test URL has no `sslmode`, and the Go suites failed against a Postgres without SSL. `shared/components/testpg` adds `sslmode=disable` only when the server is local and the URL sets none; every Go test that reads the URL goes through it.
- Reproduced on a throwaway Postgres 16 with no SSL: fails on main, passes on the branch, along with the api, supervisor and listener suites.

## What we learned
- The affected-tests gate blocked the helper until `pgtest.Base` had a real test of its own.
