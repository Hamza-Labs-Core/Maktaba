# Story 18.7 — Database query performance and N+1 prevention

The DB is the universal bottleneck candidate. Index everything that's hot
and forbid N+1 patterns by test, not by review.

## Acceptance criteria

- AC1. Every query in `shared/db/queries/` is covered by an `EXPLAIN
  ANALYZE` snapshot test; any query that becomes a sequential scan on a
  table > 10 k rows fails the test.
- AC2. The hot-path queries (`videos by library`, `segments by video`,
  `jobs claim`, `processing_jobs (state, finished_at)` for 24-hour
  counts, `processing_jobs MIN(created_at) WHERE state='pending'` for
  oldest-pending-job age) all complete in ≤ 5 ms on the reference
  fixture, backed by covering indexes named in the migration.
- AC3. A `db_query_count_total` metric is exported; the perf harness
  asserts a hard cap on per-request DB queries (e.g., `GET /api/videos`
  ≤ 3 queries regardless of result-set size).
- AC4. Postgres + SQLite both pass the same query suite (semantic parity
  test).

## Test cases

- TC1. Snapshot: `EXPLAIN ANALYZE` for each named query is captured under
  `tests/explain/`; any change in query plan kind (e.g., index → seq scan)
  fails CI.
- TC2. N+1 detector: a request that fetches 100 videos issues exactly 1
  videos query and 1 batched media-info query, not 100.
- TC3. Cross-engine: every sqlc-generated query has a Python equivalent
  in `pipeline/db/`; both produce the same rows on the test fixture.
- TC4. Stats coverage: `GET /api/queue/stats` (`done_24h` plus
  `oldest_pending_age`) hits index-only scans; the snapshot proves it.

## Edge cases

- EC1. SQLite lacks `EXPLAIN ANALYZE` — use `EXPLAIN QUERY PLAN` with a
  separate snapshot file.
- EC2. Empty tables: query plans differ on empty vs. populated; snapshots
  are taken against the seeded fixture only.
- EC3. Postgres planner instability: the snapshot stores `using
  index_X | using seq_scan`, not the full plan, to avoid noise.
