# Implementation Plan — Story 18.7 Database Query Performance & N+1 Prevention

> Companion to [story-18-07-database-query-performance.md](story-18-07-database-query-performance.md).
> EXPLAIN snapshots, covering indexes, hard query-count caps, Postgres+SQLite parity.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Query source | `shared/db/queries/*.sql` (sqlc input). Each named query gets a snapshot. |
| Snapshot dir | `tests/explain/{engine}/{query_name}.txt` — one per engine. |
| Plan-kind extraction | Parse `EXPLAIN`/`EXPLAIN QUERY PLAN` output to `using index | using seq scan`, normalise. |
| Query count metric | `db_query_count_total{handler}` — middleware increments per-request. |
| Cross-engine parity | Python `pipeline/db/queries.py` mirrors sqlc-generated queries; parity test runs both engines on the same fixture. |
| Out of scope | Schema design (Epic 5/9); migration tooling (Epic 22 devops). |

## 1. Project layout

```
shared/db/
├── queries/
│   ├── videos.sql            # named queries (-- name: ListByLibrary :many ...)
│   ├── segments.sql
│   ├── jobs.sql              # `claim_next`, `recent_done_24h`, `oldest_pending_age`
│   └── search.sql
├── migrations/
│   └── 00xx_indexes_perf.sql
└── sqlc.yaml

pipeline/db/
└── queries.py                # SQL strings; parity-tested against shared/db/queries

tests/explain/
├── postgres/                 # *.txt snapshots
├── sqlite/
└── snapshot_test.go

tests/db/
├── n_plus_one_test.go
├── parity_test.go
└── query_count_middleware_test.go

api/internal/middleware/
├── querycount.go             # per-request DB counter
└── querycount_test.go
```

## 2. Named queries (excerpt)

```sql
-- name: ListVideosByLibrary :many
SELECT id, library_id, title, duration_s, created_at
FROM videos
WHERE library_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListSegmentsByVideo :many
SELECT id, video_id, ts_start, ts_end, text
FROM segments
WHERE video_id = ANY($1::text[])
ORDER BY video_id, ts_start;

-- name: ClaimNextJob :one
UPDATE processing_jobs
   SET state = 'running', started_at = now()
 WHERE id = (
   SELECT id FROM processing_jobs
    WHERE state = 'pending' AND scheduled_at <= now()
    ORDER BY priority DESC, scheduled_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
 )
RETURNING *;

-- name: QueueStats24h :one
SELECT
  (SELECT COUNT(*) FROM processing_jobs WHERE state='done'    AND finished_at >= now() - interval '24 hours') AS done_24h,
  (SELECT COUNT(*) FROM processing_jobs WHERE state='failed'  AND finished_at >= now() - interval '24 hours') AS failed_24h,
  (SELECT EXTRACT(EPOCH FROM (now() - MIN(created_at)))::bigint
     FROM processing_jobs WHERE state='pending') AS oldest_pending_age_s;
```

## 3. Covering indexes (migration excerpt)

```sql
-- migrations/00xx_indexes_perf.sql
CREATE INDEX IF NOT EXISTS videos_library_id_created_at_idx
  ON videos (library_id, created_at DESC) INCLUDE (id, title, duration_s);

CREATE INDEX IF NOT EXISTS segments_video_id_ts_start_idx
  ON segments (video_id, ts_start) INCLUDE (id, ts_end, text);

-- For ClaimNextJob (priority DESC, scheduled_at) covering by state predicate:
CREATE INDEX IF NOT EXISTS processing_jobs_pending_priority_idx
  ON processing_jobs (priority DESC, scheduled_at)
  WHERE state = 'pending';

-- For QueueStats24h done_24h / failed_24h:
CREATE INDEX IF NOT EXISTS processing_jobs_state_finished_at_idx
  ON processing_jobs (state, finished_at);

-- For oldest_pending_age MIN scan:
CREATE INDEX IF NOT EXISTS processing_jobs_pending_created_at_idx
  ON processing_jobs (created_at)
  WHERE state = 'pending';
```

SQLite mirrors these with the partial-index syntax it shares. Index naming: `<table>_<columns>_idx` (or with `_partial` suffix for partial indexes).

## 4. Snapshot generator

```go
// tests/explain/snapshot_test.go
//go:build pg || sqlite

func TestExplainSnapshots(t *testing.T) {
    cases := loadNamedQueries(t)        // parses queries/*.sql for `-- name:` blocks
    for _, c := range cases {
        c := c
        t.Run(c.Name, func(t *testing.T) {
            plan := runExplain(t, c)             // engine-aware: ANALYZE on PG, QUERY PLAN on SQLite
            kind := extractKind(plan)            // "using index_X" or "using seq_scan"
            snapPath := filepath.Join("tests/explain", engineName(), c.Name+".txt")
            assertSnapshot(t, snapPath, kind)
            for _, table := range largeTables {
                if strings.Contains(kind, "seq_scan_on:"+table) {
                    t.Fatalf("query %s does seq_scan on %s (>10k rows): %s", c.Name, table, kind)
                }
            }
        })
    }
}
```

`largeTables` is `tests/explain/large_tables.txt` listing every table whose seeded fixture has > 10 k rows.

EC3: snapshots store the canonicalised kind string (`using_index:videos_library_id_created_at_idx`), not the verbose plan.

## 5. EXPLAIN kind extractor

```go
func extractKind(plan string) string {
    var parts []string
    sc := bufio.NewScanner(strings.NewReader(plan))
    re := regexp.MustCompile(`(?:Index Scan|Index Only Scan).*on (\w+).*using (\w+)`)
    seq := regexp.MustCompile(`Seq Scan on (\w+)`)
    for sc.Scan() {
        if m := re.FindStringSubmatch(sc.Text()); m != nil {
            parts = append(parts, fmt.Sprintf("using_index:%s", m[2]))
        } else if m := seq.FindStringSubmatch(sc.Text()); m != nil {
            parts = append(parts, fmt.Sprintf("seq_scan_on:%s", m[1]))
        }
    }
    sort.Strings(parts)
    return strings.Join(parts, "|")
}
```

SQLite variant parses `EXPLAIN QUERY PLAN` lines like `SCAN segments USING INDEX segments_video_id_ts_start_idx`.

## 6. Query-count middleware

```go
// api/internal/middleware/querycount.go
type ctxKey string
const cntKey ctxKey = "qcnt"

func WithQueryCount(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        var c int64
        ctx := context.WithValue(r.Context(), cntKey, &c)
        next.ServeHTTP(w, r.WithContext(ctx))
        metrics.DBQueryCount.WithLabelValues(routeName(r)).Observe(float64(c))
        if cap, ok := caps[routeName(r)]; ok && c > cap {
            metrics.DBQueryCountBreach.WithLabelValues(routeName(r)).Inc()
            slog.Warn("query count cap exceeded", "route", routeName(r), "n", c, "cap", cap)
        }
    })
}

// db wrapper increments via:
func IncQuery(ctx context.Context) {
    if v, ok := ctx.Value(cntKey).(*int64); ok && v != nil { atomic.AddInt64(v, 1) }
}
```

The sqlc-generated `Queries` are wrapped so each call to `Query`/`QueryRow`/`Exec` calls `IncQuery(ctx)` before running.

Caps registered per route:

```go
var caps = map[string]int64{
    "GET /api/videos":     3,        // 1 list + 1 batched media-info + 1 count
    "GET /api/videos/:id": 2,
    "POST /api/search":    4,        // FTS + chroma + meta + count
}
```

## 7. N+1 detector

```go
// tests/db/n_plus_one_test.go
func TestVideosListIs1Query(t *testing.T) {
    seed100Videos(t, db)
    var n int64
    ctx := context.WithValue(t.Context(), cntKey, &n)
    _, err := api.ListVideos(ctx, ListVideosIn{LibraryID: lib})
    require.NoError(t, err)
    require.LessOrEqual(t, n, int64(2), "expected <=2 queries (videos + batched media), got %d", n)
}
```

## 8. Cross-engine parity

```go
// tests/db/parity_test.go
func TestPostgresSQLiteParity(t *testing.T) {
    pg := newPGFixture(t)
    sl := newSQLiteFixture(t)
    seedSame(t, pg, sl)

    queries := []string{"ListVideosByLibrary", "ListSegmentsByVideo", "QueueStats24h", "OldestPendingAge"}
    for _, q := range queries {
        rowsPG := runNamed(t, pg, q)
        rowsSL := runNamed(t, sl, q)
        if diff := cmp.Diff(rowsPG, rowsSL); diff != "" {
            t.Errorf("%s mismatch (-pg +sqlite):\n%s", q, diff)
        }
    }
}
```

Python parity (`pipeline/db/queries.py`) is checked by spawning a small Go helper that round-trips the Python query string through pg and SQLite, since sqlc isn't used by Python.

## 9. Test cases

| TC | Maps to | Notes |
|---|---|---|
| TC1 snapshot per query | AC1 | `tests/explain/snapshot_test.go`. |
| TC2 N+1 detector | AC3 | `tests/db/n_plus_one_test.go`. |
| TC3 cross-engine parity | AC4 | `tests/db/parity_test.go`. |
| TC4 stats-coverage | AC2 | snapshot for `QueueStats24h` asserts `using_index:processing_jobs_state_finished_at_idx` and `using_index:processing_jobs_pending_created_at_idx`; both must be Index Only Scans. |

## 10. Edge cases

| Case | Source | Handling |
|---|---|---|
| EC1 SQLite EXPLAIN ANALYZE missing | story | `EXPLAIN QUERY PLAN`; separate snapshot dir. |
| EC2 empty-table plan differs | story | All snapshots taken against seeded fixture only. |
| EC3 PG planner instability | story | Snapshot stores canonicalised kind, not plan. |
| Index newly added but unused | impl | `pg_stat_user_indexes.idx_scan` checked nightly; index with 0 scans flagged in a metric. |
| sqlc regeneration drift | impl | `sqlc generate` runs in CI; dirty diff fails. |

## 11. CI integration

- PR job runs snapshot, N+1, parity, query-count tests in ~3 min.
- Snapshot diff failures show a unified diff: previous kind vs. new kind plus the named query SQL.

## 12. Dependencies

- Epic 5 search-indexing (FTS schema).
- Epic 6 job-queue (processing_jobs schema).
- Story 18.1 (budgets — query-count caps complement endpoint budgets).
- Story 21.2 (`db_query_count_total` metric).
