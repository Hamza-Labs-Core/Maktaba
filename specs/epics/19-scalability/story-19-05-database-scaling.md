# Story 19.5 — Database scaling and failover

Postgres is the single source of truth and the WS bus. Plan its growth
and recovery.

## Acceptance criteria

- AC1. The schema sustains 1 M segments + 50 k videos with all queries
  in the perf budget ([Story 18.7](../18-performance/story-18-07-database-query-performance.md)).
- AC2. A streaming-replica Postgres can be added with documented setup
  steps (`pg_basebackup` + `recovery.conf`); read-only replica handles
  search queries (eventual consistency tolerated).
- AC3. Daily logical backup (`pg_dump`) runs as a documented systemd /
  launchd job, retains N=14 days, and a one-line restore script is
  tested in CI.
- AC4. Migration safety: `goose up` against a 30 TB-class fixture
  completes in ≤ 60 s; long-running migrations are forbidden by a
  pre-merge lint.

## Test cases

- TC1. Restore drill: take a fresh dump, restore into a temp DB, run
  the catalog smoke test against it; all videos and segments round-trip.
- TC2. Read-replica search: configure the API to read from the replica
  with a 5 s lag tolerance; search results match primary within tolerance.
- TC3. Migration size: attempt a `CREATE INDEX` on a 1 M row table; the
  pre-merge lint flags it for `CREATE INDEX CONCURRENTLY`.

## Edge cases

- EC1. `pg_dump` on a busy primary slows transcribe writes — the cron
  pins the dump to a low-traffic window and uses `--jobs` for parallelism.
- EC2. Replica falls behind by > 60 s — the API stops routing search to
  it and pages an alert.
- EC3. SQLite path: there is no replica story; backup is a `VACUUM
  INTO` snapshot and the alert about replica lag is a no-op.
