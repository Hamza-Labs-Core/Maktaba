---
schema: 1
kind: incident
title: "A failed update put the whole 34 GB dump back twice, though its migrations' Down scripts could undo it: the database step had no Down path"
date: 2026-11-24
session: 5572804f-8358-4da5-861b-2ec0d0a662fd
agent: claude-code
prs: [1598]
---

## What happened
- **The rule since #831 (2026-11-08):** a failed update is undone by its migrations' Down
  scripts, and the dump goes back last.
- **The updates broke it:**
  - **2026-11-23, 0.3.153 → 0.3.158 (run 70d21309):** the whole dump was put back. Down from
    08:18:49Z to 11:02:35Z.
  - **2026-11-24, 0.3.153 → 0.3.157 (run f18be9ee):** the whole dump was put back again. The
    restore started at 08:20:06Z and was still building indexes at 10:50Z.
  - Both failed the same way: 0235's `CREATE INDEX CONCURRENTLY … search_terms_term_prefix`
    was cancelled by its lock timeout, with 0231 to 0234 already run.
- **Which process ran what (server.log, launcher.log):**
  - 0.3.153 prepared the update, kept its payload in `updates/payloads/0.3.153`, took the dump
    while it served, and restarted.
  - The new release then ran the database step, the restore, the watch and the rollback: on
    11-23 0.3.158, pid 57166; on 11-24 0.3.157, pid 98502, `launcher_start` at 08:13:53Z from
    `/Applications/Maktaba.app`.
- **Cause (identical code in v0.3.153, v0.3.157 and v0.3.158):**
  - `cmd/maktaba-server/serve_appbundle.go:1077-1095`: a failed migration with a saved dump
    went straight to `g.restore`, with no Down step.
  - The machine's rollback tries `migrateDown` (`internal/updater/machine.go:1480-1481`,
    `schemadown.go:179`) only when `Records.Migrated(version)` is true. The gate had already
    rewritten the record to restored, so neither `keepSchema` nor `migrateDown` ran, and
    neither logged anything.
- **The Down scripts would have worked:** `DownToFrom` on the kept 0.3.153 folder and the real
  0.3.157 and 0.3.158 folders returned `ok=true, schema=0230`.
- **#1538 (main, first in v0.3.161) added a Down path to the database step.** It picks it for
  the home's exact case, but still put the dump back:
  - for a failure inside 0232 or 0234: a `DROP INDEX` inside a function's format string was
    read as destructive;
  - whenever the release before's folder or the slot was not known.
- **#1598:**
  - `migrate undo-failed` undoes the migration that failed part way with its own Down script.
  - The way back goes to the slot recorded before the migrations (`schema_before`).
  - The dump goes back only when the Down scripts fail, a completed migration lost data they
    cannot give back, or the way back is unknown. Each case is logged at level=error and named
    on the Updates page.
  - migration-lint rules 9 and 10, and the rehearsal's Down-after-partial-Up test on
    PostgreSQL 18.

## What we learned
- A rule enforced in one code path (the machine's rollback) is not enforced. The path that
  actually ran was the database step's own failure branch, and it never consulted the rule.
- The new release's code runs the migrations and their way back. So a fix protects updates
  *to* a release that has it, whatever the release before. The exception is the launcher
  switching back on its own (`cameBackOld`), where the old release's start puts the dump back.
- Reproducing the decision functions on the real kept folders took seconds and settled the
  diagnosis.

## Keep doing
- Check every way back against "Down scripts first, the dump last" with the real migration
  folders of the releases involved.

## Stop doing
- Treating "the migration failed part way" as a reason to put the dump back: the failed
  migration's own Down script undoes it.

## Open
- The launcher's own switch-back after a boot loop (`cameBackOld`) still marks the dump to be
  put back by the old release's start. Only releases that carry the Down path can avoid it.
- Nine old NO TRANSACTION Downs (0003 to 0224) re-add a constraint unguarded. They are
  harmless now, since every home is past them.
- The 2026-11-24 restore was still running when #1598 opened. The home reaches the fixed way
  back only by installing a release with #1598.
