---
schema: 1
kind: pr
pr: 676
title: testpg reads PGHOST for an empty host and leaves PGSSLMODE alone
date: 2026-11-06
session: f43ebb40-6f2c-4bbc-984a-059b86779c63
prs: [660]
---

## What happened
- Review fix for #660. `Normalize` counted an empty host as the local socket, so `postgres:///db` or `dbname=x` got `sslmode=disable` even with `PGHOST` naming a remote server, and an explicit `PGSSLMODE=require` was overridden. An empty host now reads `PGHOST`, and nothing is added when `PGSSLMODE` is set.

## What we learned
- Tests of code that reads `PGHOST` or `PGSSLMODE` clear them with `t.Setenv`, so a developer's own environment cannot change the result.
