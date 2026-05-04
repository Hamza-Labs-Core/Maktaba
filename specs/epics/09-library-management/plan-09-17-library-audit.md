# Implementation Plan — Story 9.17 Library Audit Log

> Companion to [story-09-17-library-audit.md](story-09-17-library-audit.md).
> The story states *what* and *why*; this plan states *how*.

## 0. SUPERSEDED

**Status: SUPERSEDED.** This plan's `audit_log` schema has been
replaced by [plan-21-06](../21-observability/plan-21-06-audit-log.md)'s
canonical schema (architecture §8.6.1). Per `PLAN_REVIEW_18_24.md` §1.4,
plan-21-06 is now the single owner of the `audit_log` table shape.

This plan's migration now `DROP TABLE IF EXISTS audit_log` (see §3.1
below) so plan-21-06's migration is the sole creator. Library-audit
**category-specific application logic** — rotation reports, partition
management, the `GET /api/libraries/{id}/audit` reader — remains owned
here, but operates against plan-21-06's table shape.

**Canonical column names** (used by every section below):

| Was (this plan, deprecated) | Now (plan-21-06 canonical) |
|---|---|
| `actor_user_id` | `actor_user` |
| `ip` | `actor_ip` |
| `ts` | `created_at` |
| Primary key `(ts, id)` | `(id, created_at)` |
| Time column `ts` | `created_at` |
| `payload_jsonb` | `payload` |
| Categories `('library','security')` | `('library','security','device','admin','auth','data','config','keys','job')` |

The reader endpoint (`GET /api/libraries/{id}/audit`) and event
constructors continue to work because they restrict to
`category='library'`, which is still in the canonical enum.

## 0.1 Scope and placement (post-supersede)

| Concern | Decision |
|---|---|
| Schema | **Owned by plan-21-06.** This plan's migration now `DROP TABLE IF EXISTS audit_log` (see §3.1) so plan-21-06's migration is the sole creator. The table shape (columns, types, partitioning) is plan-21-06's. |
| Partitioning | Plan-21-06 owns monthly partitions; this plan's library-audit retention/rotation operates against the same partition layout. |
| Writers | `api/internal/audit/writer.go` (Go) and `pipeline/src/maktaba_pipeline/audit/writer.py` (Python) are owned by plan-21-06. Library callers continue to use them via the `WriteLibrary` helper. |
| HTTP route | `GET /api/libraries/{id}/audit?cursor=&limit=` — returns `category='library'` rows for the given library, newest-first, with cursor pagination. Owner/admin-only. **Owned here.** |
| Retention | Library-audit retention reports + admin UI **owned here**; the underlying detach/archive primitives are owned by plan-21-06 / Epic 22. |
| Out of scope | `audit_log` table schema (now plan-21-06); device/security/data category writes (their owning epics). |

## 1. Architecture diagram

```
   Writers (every category):
     Go (handlers)               Python (workers)
        ↓                              ↓
     audit.Writer.Write(...)       AuditWriter.write(...)
        ↓                              ↓
     INSERT INTO audit_log (...) VALUES ($1,$2,...,$N::jsonb)
        ↓
     Postgres routing (PARTITION BY RANGE):
        → audit_log_YYYY_MM (created on first insert in that month
                              by the trigger function)

   Reader:
     GET /api/libraries/{id}/audit?cursor=...
        ↓
     SELECT id, created_at, event, actor_user_id, video_id, payload_jsonb
       FROM audit_log
      WHERE category = 'library'
        AND library_id = $1
        AND (created_at, id) < ($cursor_ts, $cursor_id)
      ORDER BY created_at DESC, id DESC
      LIMIT $page_size
     → cursor = encode(last_row.created_at, last_row.id)

   Append-only enforcement:
     BEFORE UPDATE / BEFORE DELETE → RAISE EXCEPTION
       (defined once on the parent; inherited by partitions)

   Retention (Epic 22 cron, nightly):
     for part in pg_inherits where parent='audit_log':
        if part.range_end < now() - audit_retention_days:
            COPY part TO 's3://…/audit_archive/{part_name}.csv'
            DETACH PARTITION
            DROP TABLE part
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/audit/writer.go` | `Writer.Write(ctx, Event)`. Library-events struct: `LibraryEvent` per the Story-9.15/9.11 callers. |
| `api/internal/audit/library_events.go` | Typed event constructors (e.g., `Delete`, `Purge`, `RootsRuntimeOverlap`). |
| `api/internal/handlers/libraries/audit.go` | The GET endpoint. |
| `pipeline/src/maktaba_pipeline/audit/writer.py` | `AuditWriter.write(...)`. |
| `pipeline/tests/audit/test_writer.py` | Test the no-block-on-failure semantic. |
| `shared/db/migrations/0045_audit_log.sql` | Parent + first 3 partitions + triggers + indexes. |
| `shared/db/migrations/0045_audit_log.sqlite.sql` | SQLite variant — non-partitioned but otherwise identical schema. |
| `shared/db/queries/audit.sql` | sqlc input. |
| `api/cmd/maktaba-api/audit_archive.go` | The archive/trim CLI subcommand. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/router.go` | Wire `GET /api/libraries/{id}/audit`. |
| All callers (Stories 9.4, 9.6, 9.11, 9.13, 9.14, 9.15, 9.16) | Use the audit writer instead of inlining INSERTs. |
| `specs/epics/09-library-management/README.md` | Tick story 9.17. |

### 2.3 Type definitions

```go
// api/internal/audit/writer.go
package audit

type Category string

// Canonical: category IN ('library','security','device','admin').
const (
    CategoryLibrary  Category = "library"
    CategorySecurity Category = "security"
    CategoryDevice   Category = "device"
    CategoryAdmin    Category = "admin"
)

type Event struct {
    ID          uuid.UUID  // v7 (time-ordered)
    Category    Category
    Event       string
    ActorUserID *uuid.UUID
    LibraryID   *uuid.UUID
    VideoID     *uuid.UUID
    IP          *netip.Addr
    UserAgent   *string
    Payload     map[string]any
}

type LibraryEvent struct {
    Event       string
    LibraryID   uuid.UUID
    ActorUserID uuid.UUID
    VideoID     *uuid.UUID
    Payload     map[string]any
}

type Writer interface {
    Write(ctx context.Context, e Event) error  // never blocks; never propagates
    WriteLibrary(ctx context.Context, e LibraryEvent) error
}
```

```go
// api/internal/handlers/libraries/audit.go
type AuditEntry struct {
    ID         uuid.UUID `json:"id"`
    CreatedAt  time.Time `json:"created_at"`
    Event      string    `json:"event"`
    Actor      *uuid.UUID `json:"actor_user_id"`
    VideoID    *uuid.UUID `json:"video_id"`
    Payload    json.RawMessage `json:"payload"`
}

type AuditPage struct {
    Items      []AuditEntry `json:"items"`
    NextCursor *string      `json:"next_cursor,omitempty"`
}
```

## 3. Database migration

### 3.1 Postgres — `0045_audit_log.sql` (SUPERSEDED, DROP-only)

Per §0, plan-21-06 is the sole creator of `audit_log`. This plan's
migration is now **drop-only** so plan-21-06's migration (which runs
later in the manifest) is the sole `CREATE TABLE`. If a fresh DB never
had this plan's prior `CREATE` apply, the DROP IF EXISTS is a no-op.

```sql
-- +goose Up
-- +goose StatementBegin

-- 0045 is now a no-op-on-fresh-DB / cleanup-on-existing-DB migration.
-- Plan-21-06's migration (runs later) creates the canonical audit_log.
-- This DROP guards against an upgrader who applied an older 0045 that
-- created the deprecated shape; the canonical plan-21-06 migration
-- then creates the correct one.
DROP TABLE IF EXISTS audit_log CASCADE;
DROP FUNCTION IF EXISTS audit_log_no_mutation() CASCADE;
DROP FUNCTION IF EXISTS audit_log_ensure_next_month_partition() CASCADE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- No-op: there is nothing to recreate; restore via plan-21-06's down.
-- +goose StatementEnd
```

### 3.2 SQLite variant

Same shape: drop-only, with plan-21-06's SQLite variant as the sole creator.

### 3.3 sqlc queries (canonical column names)

The reader query continues to operate against plan-21-06's table; the
column names are updated to plan-21-06's canonical shape:

```sql
-- name: InsertLibraryAudit :exec
-- Inserts use plan-21-06's column names. category='library' is the
-- only category this plan emits.
INSERT INTO audit_log (id, category, event, actor_user, actor_ip,
                       target_id, target_kind, payload, dedupe_key,
                       created_at)
VALUES (gen_random_uuid(), 'library', $1, $2, $3, $4, 'library',
        $5::jsonb, $6, now());

-- name: ListLibraryAudit :many
SELECT id, created_at, event, actor_user, target_id, payload
  FROM audit_log
 WHERE category = 'library'
   AND target_id = $1::text                 -- library_id rendered as text
   AND target_kind = 'library'
   AND (created_at, id) < ($2::timestamptz, $3::uuid)
 ORDER BY created_at DESC, id DESC
 LIMIT $4;
```

Note the column renames vs the original draft: `actor_user` (not
`actor_user_id`), `actor_ip` (not `ip`), `created_at` (not `ts`),
`payload` (not `payload_jsonb`). These match plan-21-06's table
exactly.

## 4. Code scaffolding

### 4.1 Go writer — non-blocking by design

```go
// api/internal/audit/writer.go
type writer struct {
    pool   *pgxpool.Pool
    queue  chan Event              // buffered; back-pressure metric on drop
    logger *slog.Logger
    failed prometheus.Counter
}

func NewWriter(pool *pgxpool.Pool, log *slog.Logger) Writer {
    w := &writer{
        pool:   pool,
        queue:  make(chan Event, 1024),
        logger: log,
        failed: auditWriteFailedTotal,
    }
    go w.runLoop(context.Background())
    return w
}

func (w *writer) Write(ctx context.Context, e Event) error {
    if e.ID == uuid.Nil {
        e.ID, _ = uuid.NewV7()
    }
    select {
    case w.queue <- e:
        return nil
    default:
        // Non-blocking: queue full → counter and dropped log.
        w.failed.Inc()
        w.logger.Warn("audit_drop_queue_full", "category", e.Category,
            "event", e.Event)
        return nil
    }
}

func (w *writer) runLoop(ctx context.Context) {
    q := dbq.New(w.pool)
    for {
        select {
        case <-ctx.Done():
            return
        case e := <-w.queue:
            payload, _ := json.Marshal(e.Payload)
            err := q.InsertAudit(ctx, dbq.InsertAuditParams{
                ID: e.ID, Category: string(e.Category), Event: e.Event,
                ActorUserID: e.ActorUserID, LibraryID: e.LibraryID,
                VideoID: e.VideoID, Ip: e.IP, UserAgent: e.UserAgent,
                PayloadJsonb: payload,
            })
            if err != nil {
                w.failed.Inc()
                w.logger.Warn("audit_insert_failed", "err", err,
                    "category", e.Category, "event", e.Event)
            }
        }
    }
}
```

The "best-effort" property is enforced at two levels: queue full is a
non-blocking drop; insert error is logged but not returned. The
calling code never has to wrap the call in error handling.

### 4.2 Library-event constructors

```go
// api/internal/audit/library_events.go
func (w *writer) WriteLibrary(ctx context.Context, e LibraryEvent) error {
    return w.Write(ctx, Event{
        Category:    CategoryLibrary,
        Event:       e.Event,
        ActorUserID: &e.ActorUserID,
        LibraryID:   &e.LibraryID,
        VideoID:     e.VideoID,
        Payload:     e.Payload,
    })
}
```

### 4.3 Reader — `GET /api/libraries/{id}/audit`

```go
func AuditHandler(d *handlers.Deps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()
        libID, _ := uuid.Parse(chi.URLParam(r, "id"))

        actor := handlers.RequireUser(ctx)
        if !d.AuthZ.IsLibraryAdmin(ctx, actor.ID, libID) {
            handlers.WriteError(w, 403, "forbidden", "")
            return
        }

        size := parsePageSize(r, 50)
        cursorAt, cursorID, err := decodeCursor(r.URL.Query().Get("cursor"))
        if err != nil { handlers.WriteError(w, 400, "bad-cursor", ""); return }

        rows, err := d.Queries.ListLibraryAudit(ctx, dbq.ListLibraryAuditParams{
            LibraryID: libID, CursorCreatedAt: cursorAt, CursorID: cursorID,
            Limit: int32(size + 1),
        })
        if err != nil { handlers.WriteError(w, 500, "list-failed", err.Error()); return }

        var nextCursor *string
        if len(rows) > size {
            last := rows[size]
            c := encodeCursor(last.CreatedAt, last.ID)
            nextCursor = &c
            rows = rows[:size]
        }

        items := make([]AuditEntry, 0, len(rows))
        for _, r := range rows {
            items = append(items, AuditEntry{
                ID: r.ID, CreatedAt: r.CreatedAt, Event: r.Event,
                Actor: r.ActorUserID, VideoID: r.VideoID,
                Payload: r.PayloadJsonb,
            })
        }
        handlers.WriteJSON(w, 200, AuditPage{Items: items, NextCursor: nextCursor})
    }
}
```

### 4.4 Python writer — symmetric semantics

```python
# pipeline/src/maktaba_pipeline/audit/writer.py
import asyncio, json, logging
from uuid import uuid4
from uuid_extensions import uuid7  # for time-ordered ids


class AuditWriter:
    def __init__(self, db, *, queue_size: int = 1024) -> None:
        self._db = db
        self._queue: asyncio.Queue = asyncio.Queue(maxsize=queue_size)
        self._task = asyncio.create_task(self._run())

    async def write(self, *, category, event, payload=None,
                    library_id=None, video_id=None, actor_user_id=None) -> None:
        try:
            self._queue.put_nowait({
                "id": uuid7(),
                "category": category, "event": event,
                "library_id": library_id, "video_id": video_id,
                "actor_user_id": actor_user_id,
                "payload_jsonb": json.dumps(payload or {}),
            })
        except asyncio.QueueFull:
            audit_write_failed_total.inc()
            logging.warning("audit_drop_queue_full event=%s", event)

    async def _run(self) -> None:
        while True:
            e = await self._queue.get()
            try:
                await self._db.execute(
                    "INSERT INTO audit_log "
                    "  (id, created_at, category, event, actor_user_id, "
                    "   library_id, video_id, dedupe_key, payload_jsonb) "
                    "VALUES ($1, now(), $2, $3, $4, $5, $6, $7, $8::jsonb)",
                    e["id"], e["category"], e["event"],
                    e["actor_user_id"], e["library_id"], e["video_id"],
                    e.get("dedupe_key"), e["payload_jsonb"],
                )
            except Exception:
                audit_write_failed_total.inc()
                logging.exception("audit_insert_failed event=%s", e["event"])
```

### 4.5 Retention CLI

```go
// api/cmd/maktaba-api/audit_archive.go
var auditArchiveCmd = &cobra.Command{
    Use: "audit-archive",
    Short: "Detach and archive audit_log partitions older than retention_days.",
    RunE: func(cmd *cobra.Command, args []string) error {
        days, _ := cmd.Flags().GetInt("days")
        // 1. List partitions older than the cutoff.
        cutoff := time.Now().AddDate(0, 0, -days)
        parts, err := dbq.New(pool).ListAuditPartitionsOlderThan(ctx, cutoff)
        if err != nil { return err }
        for _, p := range parts {
            // 2. COPY to archive (S3 or local)
            if err := archive.CopyTable(ctx, pool, p.Name); err != nil { return err }
            // 3. DETACH; DROP
            _, err := pool.Exec(ctx,
                fmt.Sprintf("ALTER TABLE audit_log DETACH PARTITION %s", p.Name))
            if err != nil { return err }
            _, err = pool.Exec(ctx, fmt.Sprintf("DROP TABLE %s", p.Name))
            if err != nil { return err }
        }
        return nil
    },
}
```

## 5. Test plan

### 5.1 Append-only enforcement (`test_audit_appendonly.py`)

| Test | What it pins |
|---|---|
| `test_update_raises` | Insert one row; UPDATE → exception, message contains `append-only`. AC-1. |
| `test_delete_raises` | Insert; DELETE → exception. AC-1. |
| `test_truncate_raises` | TRUNCATE → exception (BEFORE TRUNCATE trigger added in §3.1 — addendum). |
| `test_insert_succeeds` | INSERT → row visible. |

### 5.2 Reader tests (`audit_test.go`)

| Test | What it pins |
|---|---|
| `TestList_NewestFirst` | 3 events at t1<t2<t3 → response order: t3, t2, t1. AC-2. |
| `TestList_RespectsLibraryScope` | Two libraries; A and B audit rows; GET A returns only A rows. AC-2. |
| `TestList_RespectsCategoryFilter` | A `category='security'` row is in the table; GET library audit returns 0 such rows. |
| `TestList_CursorPagination` | 25 rows, page_size=10 → 3 pages with stable cursor; last `next_cursor=null`. AC-2 + Epic 7 cursor primitive. |
| `TestList_ForbiddenForNonAdmin` | Non-admin user → 403. |
| `TestList_PayloadShape` | Stored payload `{"x":1}` round-trips; numeric types preserved (int vs float). |

### 5.3 Writer tests

| Test | What it pins |
|---|---|
| `TestWriter_DropsWhenQueueFull` | Synthesize back-pressure (slow DB); 2000 calls; counter `audit_write_failed_total` exceeds zero; the calls themselves do NOT block. |
| `TestWriter_NoErrorPropagation` | Force the inner INSERT to fail (rename the table); writer logs but its `Write` returns nil. |
| `TestWriter_UUIDv7Ordered` | 1000 inserts in tight sequence; ids sort the same as `created_at`. |

### 5.4 Retention tests

| Test | What it pins |
|---|---|
| `test_partition_detach_after_retention` | Set retention to 1 day; create a partition with `created_at < now() - 1 day`; run `audit-archive`; partition is detached and dropped; live table no longer contains those rows. AC-3. |
| `test_partition_creation_at_month_boundary` | Run `audit_log_ensure_next_month_partition()` once; INSERT with `created_at` in next month succeeds (would otherwise fail with "no partition"). |
| `test_archive_copy_includes_all_columns` | The archived CSV contains every column; subsequent restore via COPY FROM yields equal rows. |

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| DB temporarily unavailable | Writer logs and increments `audit_write_failed_total`; calls don't block. | `TestWriter_NoErrorPropagation` |
| User-supplied content in payload | The payload is parameterized JSONB; no injection. CHECK on `octet_length <= 8 KiB`. | Documented |
| Month-boundary INSERT before partition exists | The cron precreates next-month partitions; if the cron lags, the next-month partition is *also* created at deploy time so first writes never miss. | `test_partition_creation_at_month_boundary` |
| Restore archived data temporarily | `audit-restore --partition audit_log_2025_03` re-attaches a previously archived partition. Read-only; the no-mutation triggers still apply. | Out of scope for this story; ops doc covers. |
| API filter for cross-library admin view | Out of scope here; Epic 10 Story 10.16 surfaces the global view. | Documented |

## 7. Configuration

| Key | Default | Effect |
|---|---|---|
| `audit_retention_days` | 365 | Used by `audit-archive` cron. |
| `audit_writer_queue_size` | 1024 | Buffer between callers and the inserter. |
| `audit_payload_max_bytes` | 8 KiB | DB CHECK. |

## 8. Dependencies

| Dep | Source | Why |
|---|---|---|
| Postgres ≥ 13 | architecture | Range partitioning. |
| `uuid_extensions` (Python) or stdlib v7 helper (Go) | required | Time-ordered UUIDs. |
| Story 9.15, 9.16, 9.13, 9.14, 9.11, 9.6, 9.4 | required | Callers of `WriteLibrary`. |

## 9. Acceptance checklist

**Schema (now owned by plan-21-06; this plan is supersede-only)**
- [ ] This plan's `0045_audit_log.sql` is **DROP-only** (see §3.1).
- [ ] plan-21-06's migration is the sole creator of `audit_log` with the canonical column shape (`actor_user`, `actor_ip`, `created_at`, `payload`, `dedupe_key`, etc.).
- [ ] BEFORE UPDATE / BEFORE DELETE triggers raise `append-only` (owned by plan-21-06).
- [ ] Three partitions exist on migration apply (current + 2 future) (owned by plan-21-06).

**Code (this plan owns)**
- [ ] `GET /api/libraries/{id}/audit` is wired and admin-only.
- [ ] Library-event writers (Go + Python) call plan-21-06's `WriteLibrary` helper using canonical column names.
- [ ] All callers use the writers (no inline `INSERT INTO audit_log`).

**Behaviour (story acceptance criteria)**
- [ ] AC-1: UPDATE/DELETE on the table raises (verified against plan-21-06's table).
- [ ] AC-2: GET endpoint returns library-scoped, newest-first, paginated.
- [ ] AC-3: nightly cron detaches and archives partitions older than `audit_retention_days` (cron job owned here; partition primitives owned by plan-21-06).

**Observability**
- [ ] Counter `audit_write_failed_total{reason=queue_full|db_error}`.
- [ ] Counter `audit_inserts_total{category, event}`.
- [ ] Histogram `audit_writer_queue_depth` (sampled gauge).

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.17.
- [ ] Operations doc covers retention, partitions, archive restore.
