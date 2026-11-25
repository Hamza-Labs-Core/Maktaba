---
schema: 1
kind: pr
pr: 1737
title: "v0.3.163 was never ready on a database v0.3.162 had migrated: readiness asked goose for the migration applied last"
date: 2026-11-25
session: 72be8c47-3733-4379-9431-ce8fcdcdab2c
agent: claude-code
---

## What happened
- QA's third run from main (45574270526, v0.3.163) migrated, came up healthy, and was never ready. The node
  restored v0.3.162 by itself, its one migration undone by its Down script.
- `/readyz` compared goose's current version with the newest file. Goose's current version is the migration
  **applied last**. #1661's `00040002` is a lower slot than 580001, so it was last, and readiness said
  `migrations_behind` for ever. A new database is migrated in order: CI and every rehearsal passed.
- The same comparison said "at head" with a lower slot still missing.
- **The way back had the same fault.** The review of #1737 deployed it over v0.3.162 on a full node, wrote one
  claim with its retry key, and rolled back: the schema was kept, v0.3.162 took the late version for the
  schema's and answered 503, and after 175 s the node was held with everything stopped. #1748 has `Up`,
  `Undo` and `Down` record the newest version last, which those releases read; CI now migrates the last
  release's database as well as a new one.
- The refused release's log was gone with its container, and the run said only "not healthy and ready": the cause
  was read from the code. #1708 now keeps the last lines and names the probe.

## What we learned
- A migration that lands below the database's version (the README allows it) is a case only an updated
  database shows. A test of "up to date" must start from the release before, not from nothing.
- A rollback that works also hides what failed: keep the failed side's words before it goes.
- "The previous release runs on the kept schema" is a claim about the previous release's own checks: it is
  tested with that release, not with this one.

## Keep doing
- Deploy each release to QA from the one QA serves.

## Open
- #1748. A release with #1737, for QA's full pass.
