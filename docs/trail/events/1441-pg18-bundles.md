---
schema: 1
kind: pr
pr: 1441
title: "Every payload carries PostgreSQL 18 with pg_textsearch, beside the 16 the server runs"
date: 2026-11-19
session: 753035df-3990-44b1-9dff-f305bbe9c3cd
agent: claude-code
prs: [1446, 1445]
---

## What happened
- #1441 merged as 309f8e68a. Epic 49 story 49.1: the Mac, Linux x86_64 and aarch64, and
  Windows payloads carry PostgreSQL 18.7 in `postgres-18/`. pgvector 0.8.7 and pg_textsearch
  1.5.1 are built against it from pinned source tags. The server still runs the 16 in
  `postgres/`. pgbundle preloads pg_textsearch whenever an install's `lib/` has it, merged into a
  household's own `shared_preload_libraries` as written.
- `tools/release/pg18-check.sh`, run by each `check.sh`, proves the 18 tree on every platform:
  the version, the preload, the extensions, a BM25 query answered through the index, the schema
  migrating, and the payload's own 16 cluster upgraded by `pg_upgrade`. Two Kimi K3.1 Pro
  reviews. The first found a quoted preload list corrupted by the merge, and a BM25 check that
  ignored psql's exit status; both were fixed. The second found nothing.

## What we learned
- Shipped updaters refuse a release whose `payload.json` `postgres` or `release.json`
  `app_postgres_major` names another major (`pg_major_change`). The image's start refuses a
  cluster another major made (`pg_major`). A bundle that swapped 16 for 18 would have stranded
  every home, and broken every container restarted on the new image. So 18 ships beside 16, and
  those fields keep saying 16.
- The theseus-rs 18 builds carry LLVM (118 MB on the Mac), PL/Python with its own Python, and,
  on Linux aarch64, libstdc++, libexpat and libcrypt built for glibc 2.36. Those go; the
  baseline machine's copies are taken.
- pg_textsearch 1.5.1's `Makefile.win` lacks six objects of its Makefile. nmake takes an
  `OBJS=` on the command line over the file's. Its `AGENTS.md` is a symlink, which MSYS tar
  cannot make.
- A plain YAML `run:` line with `: ` in it (`--- PASS: X`) makes GitHub reject the whole
  workflow. The run has 0 jobs and never shows in `gh pr checks`. Run actionlint or a YAML
  parser before pushing.
- The Windows runner image rolled from Visual C++ 14.51.36247.0 to 14.52.37029.0 mid-day.
  #1446 accepts either.

## Keep doing
- Prove a new Postgres major's tree on each built payload, with a 16 → 18 `pg_upgrade` of the
  payload's own cluster.

## Open
- 49.2 (#1445): the switch to running 18, the upgrade and its rollback. Open: the idle memory
  budget is a decision, and the machines' test (49.3) has not run.
