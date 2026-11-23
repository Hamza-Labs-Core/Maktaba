---
schema: 1
kind: incident
title: "The live home's updates to v0.3.140 and v0.3.158 failed on CREATE INDEX CONCURRENTLY behind a machine's copy; each put the whole dump back"
date: 2026-11-23
session: ad07accb-6baf-426a-bb99-37d4b1ff09b9
agent: claude-code
prs: [1538]
---

## What happened
- **2026-11-14, v0.3.135 → v0.3.140** (run 6f9e1803):
  - `0220_age_tiers.sql`'s `CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_library_age_tier_idx`
    failed with `canceling statement due to lock timeout (55P03)` three tries running
    (08:21–08:27Z);
  - the database step then put the 31 GB dump back, and the home was down until 14:40Z.
  - No trail event or fix followed. The update to v0.3.151 on 2026-11-18 passed with the same
    code, so the failure looked gone.
- **2026-11-23, v0.3.153 → v0.3.158** (run 70d21309):
  - The backup was taken while v0.3.153 served and ended at 08:12:28Z, before the switch.
  - The migrations began at 08:12:48Z, with the home's own api stopped.
  - **Try 1:** 0232's `video_search_docs_library_idx` hit the 2-minute lock timeout.
  - **Try 2:** recorded 0232 in 2.99 ms (the INVALID index left by try 1 was taken for done by
    `IF NOT EXISTS`), then failed on 0235's `search_terms_trgm`.
  - **Try 3:** took that index the same way and failed on `search_terms_term_prefix`.
  - Before each try the step ended 25–28 client sessions; a machine's sessions came back at once.
  - The 35 GB dump went back: 08:18:49Z to 11:02:37Z, 2 h 44 min with the library stopped. The
    update rolled back to v0.3.153.
- **The cause**, read on the live home (read-only `pg_stat_activity`, `pg_locks`,
  `pg_replication_slots`, the server's log):
  - The LAN machine's copy (192.168.50.94) kept falling more than 20 GB behind, and the home
    dropped it repeatedly (`cluster_copy_dropped`, 2026-11-20, 2026-11-23 01:04–03:19Z). Each
    time the machine copied the database anew.
  - A table sync's walsender holds a REPEATABLE READ snapshot for the whole COPY of a table. On
    the home at 13:00Z, three were in `COPY public.job_events`, `processing_jobs` and
    `transcript_segments` for 23–36 minutes, each with `backend_xmin` and a `virtualxid` lock.
  - A CREATE INDEX CONCURRENTLY waits for every older transaction in its database. That wait is
    a lock wait, so `lock_timeout` cancels it.
  - The database step's `EndSessionsSQL` deliberately leaves replication alone.
  - On 2026-11-18 the copy was streaming, not re-copying, so nothing held a snapshot.
  - Reproduced on a scratch PostgreSQL 18: a walsender in a REPEATABLE READ transaction makes
    the CIC wait on its `virtualxid`, get cancelled, and leave `indisvalid=f, indisready=t`.
    `pg_blocking_pids` names the walsender.
- **Leads that were not the cause:**
  - `pg_dump`'s snapshot: the backup ended before the switch.
  - The `idle in transaction` sessions in `ps` (the TV channel scheduler's long transactions,
    `INSERT INTO channel_programs`): they belong to the home's api, which does not run during the
    migrations.
- **#1538:**
  - The database step ends, while the migrations run, any session one of them has waited on for
    more than 10 s, and logs whom.
  - `maktaba-api migrate` names its sessions `maktaba-migrate`, and drops first an INVALID index
    a pending migration builds `CONCURRENTLY IF NOT EXISTS`.
  - A failed update goes back without its dump when that is safe: kept when additive, undone by
    the Down scripts when lossless. For the 2026-11-23 state (0231–0234 run, 0235 failed) the
    Down scripts would have taken seconds.
  - Update rehearsal scenario 19 holds a copy's walsender and a machine's session across an
    update with a CONCURRENTLY migration.

- **The independent review of #1538 found four problems, fixed in c49eab11f:**
  - **The rollback cleared the failed release's halt.** It rewrote a record already kept for the
    release before (`MarkRecordKept` always set `at`), and the halt waits for any change, so the
    failed release would have started updating again under its own rollback.
  - **The leftover cleanup skipped the leftover it was for.** It treated any lock on the index
    as a build in progress, and the planner locks every index of a table it plans against.
  - **The Down path was laxer for the migration that failed part way.** It allowed a NO
    TRANSACTION backfill there that it refuses in the migrations it undoes.
  - **A restore mark the watch set during the Down scripts could be overwritten** by the
    database step's save.

## What we learned
- A retry that runs the same thing again into the same wait proves nothing. Ending the
  sessions before each try did not reach what the wait was on (a walsender, and sessions that
  reconnect at once).
- End what a migration actually waits on (`pg_blocking_pids`), at the time it waits, rather
  than a guessed set of sessions beforehand.
- `CREATE INDEX CONCURRENTLY IF NOT EXISTS` is not idempotent after a cancel: the leftover
  INVALID index satisfies `IF NOT EXISTS`. Re-running a NO TRANSACTION file safely needs its
  INVALID leftovers dropped first.
- The rollback had keep and Down paths only for migrations that finished. A failure part way
  always restored the whole dump, the most expensive path, for the cheapest kind of failure.
- An update failure on the live home that "went away" on the next try still needs an event and
  a cause. Nov 14 and Nov 23 had one cause, nine days apart.
- The update rehearsal never ran a machine's copy, or any other session holding a snapshot,
  across the migrations. Every scenario's migrations had the database to themselves.

## Keep doing
- Read the live home's own state read-only (`pg_stat_activity`, `pg_locks`, the server's log
  lines around the failure) before choosing a fix; the backup-snapshot lead was ruled out by
  the timestamps.
- Reproduce a database-level cause on a scratch server before writing the fix.

## Stop doing
- Treating `lock_timeout` plus retries as enough for migrations while other machines keep
  using the database.
- Restoring a whole dump when the failed migrations are additive or lossless.

## Open
- The machine's copy on the live home keeps falling more than 20 GB behind and being made
  again; why it cannot keep up is not looked at here.
- The live home still runs v0.3.153. Its next window (2026-11-24, about 08:00Z) picks the
  newest unblocked release, v0.3.159, which has the old database step. A release with #1538
  must be tagged and at least an hour old (`min_release_age_hours: 1`) before it. Otherwise
  updates are deferred on the Updates page, or v0.3.154–v0.3.159 are marked prerelease (the
  pre-run recheck then withdraws them on the stable channel).
- `ChooseTarget` can still pick a release older than one blocked for `rolled_back`, which
  `Store.Block`'s comment says only a newer one may replace. Harmless here, since v0.3.159
  exists.
- The TV channel scheduler holds transactions for more than a minute between statements
  (`INSERT INTO channel_programs`); not a cause here, worth shortening.
