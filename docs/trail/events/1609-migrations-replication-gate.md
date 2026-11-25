---
schema: 1
kind: pr
pr: 1609
title: "Before migrating: wait out the machines' table syncs, pause their copies, build indexes plainly"
date: 2026-11-24
session: 1d933885-c614-466d-9dec-f738a7b6a527
agent: claude-code
prs: [1538, 1598, 1601]
---

## What happened
Research item 5 (E3, E4, E8):
- **The scheduler holds an automatic run** while a table sync's slot (`pg_<sub>_sync_<table>_<system>`) is on the home (`CopyingBlocker`, `machine_copying`).
- **The database step:**
  1. waits up to 10 minutes for the table syncs to end;
  2. keeps the machines' copies out: `pg_hba.conf` rejects `/^maktaba_rep_`, the server reloads it, and their sessions are ended until none is left;
  3. lets them in again once the database's record says the schema is settled. Not while it says `migrating`, `restoring` or `held`, nor after a rollback's Down script failed.

  The pause is a marker in the data directory that `pg_hba.conf` is written from. It outlives a kill, and a release from before it drops the rejects at its start.
- **Offline migrations:** with the library stopped, `maktaba-api migrate up` (`MAKTABA_MIGRATE_OFFLINE=1`) drops `CONCURRENTLY` from the top-level `CREATE`/`DROP INDEX` statements of each Up section. Function bodies, `DO` blocks, strings and Down sections are left as they are. The rehearsal on a copy sets the variable too.
- **Update rehearsal 19** gains:
  - a leftover table sync slot that holds nothing up;
  - the copy's role set `LOGIN` all along, kept out and then let back in;
  - the plain builds logged.

## What we learned
- **The app role `maktaba` is itself a member of `maktaba_replicas`** (it made the group).
  - The first version paused every login member, including the app role. The migrations then failed with `role "maktaba" is not permitted to log in`.
  - The auto-update e2e (`replica / updates`) caught it. The gate's unit tests stub the database and could not.
  - Only `maktaba_rep_*` (slot 0147's names) is kept out, never the app role or the superuser.
  - A real-Postgres test (opt-in `MAKTABA_TEST_PG_BIN`) checks it. The Mac app's bundled Postgres 16 binaries serve for that locally, with a temp data directory: the live home is not touched.
- **A plain `CREATE INDEX` takes `SHARE`.** That does not conflict with the `ACCESS SHARE` a copy's snapshot holds, so the build no longer waits on old snapshots at all.
- **The guard sweep already treats table sync slots made with pgoutput as Maktaba's,** so a rehearsal can make one by hand without the home dropping it.
- **A pause around one call ends with that call.** The pause was first inside `migrationGate.migrate`, so the copies logged back in just before #1598's Down scripts ran. It now ends on the record's state, not on a return.
- **A role flag cannot carry a pause** when the machines can ask the home to set it: `cluster_node_replica_grant` sets `LOGIN`, and every machine asks for its copy again and again. The review found it; the rehearsal could not, since its copy never asked. `pg_hba.conf` is the server's own.
- **A test budget breach was this PR's own test** (`TestPlainIndexBuildsOnEveryMigration`, 1.24 s under the race detector), first taken for the runner's load. Read which test a budget failure names before saying whose it is.
- **Table sync slots are permanent.** Counting them all let one dead machine's leftover hold every automatic update back; only active ones count.

## Keep doing
- Run the e2e that brings a real home up for any change that alters roles or logins. A stubbed gate test passes with any role list.

## Open
- A copy that comes back up to 30 minutes late, and no upper bound on the copying blocker: #1667.
- The copies are not paused on an operator's own Postgres; the session ending and lock timeout bound it there.
- The Down scripts that #1598 runs are not rewritten offline.
