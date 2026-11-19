---
schema: 1
kind: pr
pr: 1445
title: "A home's database upgrades from PostgreSQL 16 to 18 at the start, into a cluster of its own"
date: 2026-11-19
session: 753035df-3990-44b1-9dff-f305bbe9c3cd
agent: claude-code
prs: [1441, 1460, 1459]
---

## What happened
- #1445 merged as de02c2cdb. Epic 49 story 49.2.
  - The server runs the payload's `postgres-18/`.
  - At the start it upgrades `<state>/postgres` (16) into `<state>/postgres-18` with
    pg_upgrade: clone, else copy, never link.
  - The 16 cluster is left as it was, so every switch back starts the release before on it.
  - The 18 cluster has its own record, `state/database-pg18.json`.
  - When the upgrade can't run, the home stays on 16 and retries at each start.
  - Releases keep saying 16 in `payload.json` and `release.json`: shipped updaters refuse
    another major.
- Proof:
  - update-rehearsal scenario 17: a 16 home, an 18 release rolling back to 16, then an 18
    release committing;
  - `TestRealMajorUpgrade` on real binaries in the rehearsal kit (Linux, copy), and in the Mac
    (APFS `--clone`) and Windows (`--copy`) app jobs on their own payloads;
  - the five NAS container update legs.
- Three GLM-5.3 reviews (Kimi's quota ran out midway).
  - First: B1 and M1–M4, all fixed. The record now comes from the install's directories in
    every process; restores, storage moves, free ports.
  - Second: F1–F4, all fixed as majors: a leftover pg_upgrade server, the `.stale` room, a
    rolled-back home's record read from `pg_control`, a last step that never stops a start.
  - Third: no blockers or majors; its one minor (a restore with no 16 record) fixed.

## What we learned
- **pg_upgrade on a real home:**
  - It needs `wal_level=logical` for the old server (the machines' slots), and the app role's
    grants on the replication-slot functions taken off for it: 17 changed
    `pg_create_logical_replication_slot`'s signature, and the ACL restore fails.
  - Slots don't survive 16 → 18; machines re-copy.
- **The old cluster's record and directory must stay untouched.** Whatever switches back (the
  updater, the launcher's guard, the desktop app, a container's image fallback) runs old code,
  and that code reads only `<state>/postgres` and `state/database.json`.
- **Never store "which cluster is in use" as a process-wide setting.** The container
  entrypoint, an offline restore and the updater each derive it. It's now a pure function of
  the directories, and of the 16 cluster's `pg_control` (system id at 0, checkpoint at 32).
- **Every list of state folders must learn a new folder.** `stateFolders` lacked
  `postgres-18`, so a storage move would have left the live cluster behind.
- **Running `postgres --version` to choose arguments hung tests that use a stand-in program.**
  Read `PG_VERSION` or the folder's name instead.
- **Windows' psql ends rows with `\r\n`.** A database named `postgres\r` doesn't exist.
- **CI that seems silent:**
  - A `pull_request` workflow doesn't run on a PR that conflicts with main. Two 30-minute
    watches stayed silent; check `mergeable` first.
  - A plain YAML `run:` with `: ` in it makes GitHub reject the workflow with no jobs at all.

## Keep doing
- Give each new on-disk state a real-binaries test of its crash points: leftover servers,
  half-made directories, stale copies.

## Open
- #1457: stale comments, a hand-made 16 cluster, the rollback-switch window, an atomic marker
  write, leftover sweeping.
- 49.3 (#1459): machines follow an 18 home.
