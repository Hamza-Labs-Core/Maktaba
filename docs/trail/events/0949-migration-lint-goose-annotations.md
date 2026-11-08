---
schema: 1
kind: pr
pr: 949
title: migration-lint refuses goose annotations out of order
date: 2026-11-08
session: b0ea7fd9-6e6d-4c4f-ab23-200b28d0b3bd
prs: [925]
---

## What happened
- migration-lint read only a migration's SQL text, never its `-- +goose` lines. A file goose cannot parse, such as #925's `0215_fp_roles.sql` with a block opened twice, passed `lint / lint` and failed only in the jobs that run `goose up`.
- New check `goose-annotations` (tools/migration-lint/goose.go, dependency-free). `Up` comes first and once, `Down` at most once and after `Up`, a `StatementBegin` opens only inside a section and never inside an open block, and every block is closed. All 402 migrations pass. Run against the broken 0215 file, it names the same line goose refused.

## What we learned
- When a migration moves to a new slot, write it again from its generator (`roles.py sql`, the header) instead of slicing the old file. A slicing script that matches lines starting with `--` also catches the goose annotations.
