---
schema: 1
kind: pr
pr: 693
title: Plugins part 2, the plugin API, media handles and plugin stages in the queue (Epic 54, 54.04-54.06)
date: 2026-11-06
session: e67d03b4-bb59-41b3-8c8f-ee3aad4592fc
prs: [683]
---

## What happened
- The plugin API (`api/internal/plugins`) on a unix socket only, with invocation tokens minted for this machine's own pipeline from `plugin_grants` and every call checked again (capability, scope, field allow-list, rate, sizes, quotas); attributed `plugin_writes`; media handles with budgets. The pipeline loads `plugin_installs` as extensions (no plugin code imported) into a new `plugins` pool with a per-plugin claim cap, and dispatches to maktaba-plugind with the new `plugin_*` failure causes. Migration 0194.

## What we learned
- Other epics took 0186-0193 while this branch was open (it moved 0186 → 0190 → 0191 → 0194): number a migration only when merging main. Each move also moves `SCHEMA_REV` (pipeline) and `SchemaRev` (API schema guard, streaming).
- A new table must keep `maktaba_worker`'s default grants (compat-prev's worker-group check runs the previous release, which re-makes the group): refuse a joined machine's writes with a `BEFORE` trigger instead, as slot 0190 does. A new table must also be decided for a full node's copy (`copyReviewed` or `cluster_copy_excluded()`, redefined from the newest list), a new failure cause needs a label in `handlers/queues/causes.go` and help in `failures.md` (and its class too), and a new pool needs the migration's `worker_pools` seed.
- The live Mac app's Postgres 16 binaries (`/Applications/Maktaba.app/Contents/Resources/server/postgres/bin`) start a scratch cluster for local pg tests: `initdb` into the scratchpad, `-k ''` (the scratchpad's socket path is over 103 bytes), a free port; never the app's data.
- `job_stages.stage` was capped at 40 characters by a CHECK; plugin queue names reach 51. A widened Postgres check is a constraint swap; the SQLite sibling cannot widen an inline check without rebuilding the table, so plugins need a Postgres home.
- The pipeline machine principal already exists as the supervisor's per-boot local token (`middleware.FromSupervisor`); a new public route must also be pinned in `TestDefaultPublicAllowlistIsPinned`.
- The component catalog's test refuses any `StageDef` not named by a string literal; run-time plugin stages are excused by folder (`maktaba_pipeline/plugins/`).

## Open
- The rest of Phase 1 (supervisor `plugins` role, 54.37 consumers, 54.38, 54.07-54.13): see #693's description and the branch `claude/epic54-phase1-lifecycle`.
