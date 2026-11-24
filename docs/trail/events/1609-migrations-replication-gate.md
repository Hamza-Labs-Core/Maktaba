---
schema: 1
kind: pr
pr: 1609
title: "Before migrating: wait out the machines' table syncs, pause their copies, build indexes plainly"
date: 2026-11-24
session: 1d933885-c614-466d-9dec-f738a7b6a527
agent: claude-code
prs: [1538, 1601]
---

## What happened
Research item 5 (E3, E4, E8):
- **The scheduler holds an automatic run** while a table sync's slot (`pg_<sub>_sync_<table>_<system>`) is on the home (`CopyingBlocker`, `machine_copying`).
- **The database step:**
  1. waits up to 10 minutes for the table syncs to end;
  2. pauses the machines' copies, setting `NOLOGIN` on each `maktaba_rep_*` member of `maktaba_replicas` and ending its sessions;
  3. lets them back in after the migrations, failed or not.

  The roles are first written to `state/copies-paused.json`, so a server stopped mid-migration lets them back in at its next start.
- **Offline migrations:** with the library stopped, `maktaba-api migrate up` (`MAKTABA_MIGRATE_OFFLINE=1`) drops `CONCURRENTLY` from the top-level `CREATE`/`DROP INDEX` statements of each Up section. Function bodies, `DO` blocks, strings and Down sections are left as they are. The rehearsal on a copy sets the variable too.
- **Update rehearsal 19** gains:
  - a table sync's slot to wait out;
  - the copy's role in the group, paused and then let back in;
  - the plain builds logged.

## What we learned
- **The app role `maktaba` is itself a member of `maktaba_replicas`** (it made the group).
  - The first version paused every login member, including the app role. The migrations then failed with `role "maktaba" is not permitted to log in`.
  - The auto-update e2e (`replica / updates`) caught it. The gate's unit tests stub the database and could not.
  - The list is now only `maktaba_rep_*` (slot 0147's names), never the app role, the superuser or `current_user`.
  - A real-Postgres test (opt-in `MAKTABA_TEST_PG_BIN`) checks it. The Mac app's bundled Postgres 16 binaries serve for that locally, with a temp data directory: the live home is not touched.
- **A plain `CREATE INDEX` takes `SHARE`.** That does not conflict with the `ACCESS SHARE` a copy's snapshot holds, so the build no longer waits on old snapshots at all.
- **The guard sweep already treats table sync slots made with pgoutput as Maktaba's,** so a rehearsal can make one by hand without the home dropping it.

## Keep doing
- Run the e2e that brings a real home up for any change that alters roles or logins. A stubbed gate test passes with any role list.

## Open
- The copies are not paused on an operator's own Postgres; the session ending and lock timeout bound it there.
- The Down scripts that #1598 runs are not rewritten offline.
