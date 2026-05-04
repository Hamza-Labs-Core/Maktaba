# Implementation Plan — Story 9.17 Library Audit Log

> Companion to [story-09-17-library-audit.md](story-09-17-library-audit.md).
> The story states *what* and *why*; this plan states *how*.
> Owns the canonical `audit_log` table (jointly with Epic 10
> Story 10.16) and the `category='library'` API surface.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Schema | One `audit_log` table per the Epic 9 README, partitioned by RANGE on `ts`. Append-only via BEFORE UPDATE/DELETE triggers. |
| Partitioning | Monthly partitions named `audit_log_YYYY_MM`. A trigger function `audit_log_route_to_partition()` ensures the parent has the right child for each new INSERT (creating one on-demand). The partition-management cron (Epic 22) precreates the next 3 months. |
| Writers | `api/internal/audit/writer.go` (Go) and `pipeline/src/maktaba_pipeline/audit/writer.py` (Python). Both insert into the parent `audit_log`; Postgres routes to the right partition. Best-effort: writes never block the calling tx and never raise on failure. |
| HTTP route | `GET /api/libraries/{id}/audit?cursor=&limit=` — returns `category='library'` rows for the given library, newest-first, with cursor pagination. Owner/admin-only. |
| Retention | Nightly trim job detaches partitions older than `audit_retention_days` (default 365), copies them to `s3://…/audit_archive/` (or local archive dir in single-host mode), and DROPs them from the live DB. Implementation lives with Epic 22; this story specifies the contract. |
| Out of scope | The security-category writes (Epic 10 Story 10.16); the archive-storage backend choice (operator config). |

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
     SELECT id, ts, event, actor_user_id, video_id, payload_jsonb
       FROM audit_log
      WHERE category = 'library'
        AND library_id = $1
        AND (ts, id) < ($cursor_ts, $cursor_id)
      ORDER BY ts DESC, id DESC
      LIMIT $page_size
     → cursor = encode(last_row.ts, last_row.id)

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

const (
    CategoryLibrary  Category = "library"
    CategorySecurity Category = "security"
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
    TS         time.Time `json:"ts"`
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

### 3.1 Postgres — `0045_audit_log.sql`

```sql
-- +goose Up
-- +goose StatementBegin

CREATE TABLE audit_log (
    id              UUID NOT NULL,
    ts              TIMESTAMPTZ NOT NULL DEFAULT now(),
    category        TEXT NOT NULL CHECK (category IN ('library','security')),
    event           TEXT NOT NULL CHECK (char_length(event) BETWEEN 1 AND 64),
    actor_user_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    library_id      UUID REFERENCES libraries(id) ON DELETE SET NULL,
    video_id        UUID REFERENCES videos(id) ON DELETE SET NULL,
    ip              INET,
    user_agent      TEXT CHECK (char_length(user_agent) <= 1024),
    payload_jsonb   JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (octet_length(payload_jsonb::text) <= 8 * 1024),
    PRIMARY KEY (ts, id)         -- composite for partitioning
) PARTITION BY RANGE (ts);

-- Append-only triggers — defined on the parent, inherited by children.
CREATE OR REPLACE FUNCTION audit_log_no_mutation() RETURNS trigger AS $$
BEGIN RAISE EXCEPTION 'audit_log is append-only'; END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_no_mutation();
CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_no_mutation();

-- Indexes — created on each partition by a helper. The parent gets
-- "logical" indexes that Postgres propagates.
CREATE INDEX audit_log_lookup
    ON audit_log (category, ts DESC);
CREATE INDEX audit_log_actor
    ON audit_log (actor_user_id, ts DESC) WHERE actor_user_id IS NOT NULL;
CREATE INDEX audit_log_library
    ON audit_log (library_id, ts DESC) WHERE library_id IS NOT NULL;

-- Bootstrap the current and next 2 monthly partitions.
DO $$
DECLARE
    cur_start DATE := date_trunc('month', now())::date;
    nxt       DATE;
    i         INT;
BEGIN
    FOR i IN 0..2 LOOP
        nxt := (cur_start + (i || ' month')::interval)::date;
        EXECUTE format(
          'CREATE TABLE IF NOT EXISTS audit_log_%s '
          'PARTITION OF audit_log '
          'FOR VALUES FROM (%L) TO (%L)',
          to_char(nxt, 'YYYY_MM'),
          nxt,
          (nxt + interval '1 month')::date
        );
    END LOOP;
END$$;

-- A small helper that the cron (Epic 22) calls to keep the next-month
-- partition pre-created so writes never blow up on month boundary.
CREATE OR REPLACE FUNCTION audit_log_ensure_next_month_partition() RETURNS VOID
LANGUAGE plpgsql AS $$
DECLARE
    nxt DATE := (date_trunc('month', now()) + interval '1 month')::date;
BEGIN
    EXECUTE format(
      'CREATE TABLE IF NOT EXISTS audit_log_%s '
      'PARTITION OF audit_log '
      'FOR VALUES FROM (%L) TO (%L)',
      to_char(nxt, 'YYYY_MM'),
      nxt,
      (nxt + interval '1 month')::date
    );
END;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS audit_log_ensure_next_month_partition();
DROP TRIGGER IF EXISTS audit_log_no_delete ON audit_log;
DROP TRIGGER IF EXISTS audit_log_no_update ON audit_log;
DROP FUNCTION IF EXISTS audit_log_no_mutation();
DROP TABLE IF EXISTS audit_log CASCADE;
-- +goose StatementEnd
```

### 3.2 SQLite variant

SQLite does not partition. The variant uses a single `audit_log` table
with the same columns, the same CHECKs, and the same INSERT-only
triggers. The retention job DELETEs by `ts` instead of detaching.
For a single-host SQLite deployment, this is acceptable.

### 3.3 sqlc queries

```sql
-- name: InsertAudit :exec
INSERT INTO audit_log (id, ts, category, event, actor_user_id,
                       library_id, video_id, ip, user_agent, payload_jsonb)
VALUES ($1, now(), $2, $3, $4, $5, $6, $7, $8, $9::jsonb);

-- name: ListLibraryAudit :many
SELECT id, ts, event, actor_user_id, video_id, payload_jsonb
  FROM audit_log
 WHERE category = 'library'
   AND library_id = $1
   AND (ts, id) < ($2::timestamptz, $3::uuid)
 ORDER BY ts DESC, id DESC
 LIMIT $4;
```

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
        cursorTs, cursorID, err := decodeCursor(r.URL.Query().Get("cursor"))
        if err != nil { handlers.WriteError(w, 400, "bad-cursor", ""); return }

        rows, err := d.Queries.ListLibraryAudit(ctx, dbq.ListLibraryAuditParams{
            LibraryID: libID, CursorTs: cursorTs, CursorID: cursorID,
            Limit: int32(size + 1),
        })
        if err != nil { handlers.WriteError(w, 500, "list-failed", err.Error()); return }

        var nextCursor *string
        if len(rows) > size {
            last := rows[size]
            c := encodeCursor(last.Ts, last.ID)
            nextCursor = &c
            rows = rows[:size]
        }

        items := make([]AuditEntry, 0, len(rows))
        for _, r := range rows {
            items = append(items, AuditEntry{
                ID: r.ID, TS: r.Ts, Event: r.Event,
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
                    "  (id, ts, category, event, actor_user_id, "
                    "   library_id, video_id, payload_jsonb) "
                    "VALUES ($1, now(), $2, $3, $4, $5, $6, $7::jsonb)",
                    e["id"], e["category"], e["event"],
                    e["actor_user_id"], e["library_id"], e["video_id"],
                    e["payload_jsonb"],
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
| `TestWriter_UUIDv7Ordered` | 1000 inserts in tight sequence; ids sort the same as ts. |

### 5.4 Retention tests

| Test | What it pins |
|---|---|
| `test_partition_detach_after_retention` | Set retention to 1 day; create a partition with `ts < now() - 1 day`; run `audit-archive`; partition is detached and dropped; live table no longer contains those rows. AC-3. |
| `test_partition_creation_at_month_boundary` | Run `audit_log_ensure_next_month_partition()` once; INSERT with ts in next month succeeds (would otherwise fail with "no partition"). |
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

**Schema**
- [ ] `audit_log` exists with the columns documented in the README.
- [ ] BEFORE UPDATE / BEFORE DELETE triggers raise `append-only`.
- [ ] Three partitions exist on migration apply (current + 2 future).

**Code**
- [ ] Go and Python audit writers exist; never block; never raise.
- [ ] `GET /api/libraries/{id}/audit` is wired and admin-only.
- [ ] All callers use the writers (no inline `INSERT INTO audit_log`).

**Behaviour (story acceptance criteria)**
- [ ] AC-1: UPDATE/DELETE on the table raises.
- [ ] AC-2: GET endpoint returns library-scoped, newest-first, paginated.
- [ ] AC-3: nightly cron detaches and archives partitions older than `audit_retention_days`.

**Observability**
- [ ] Counter `audit_write_failed_total{reason=queue_full|db_error}`.
- [ ] Counter `audit_inserts_total{category, event}`.
- [ ] Histogram `audit_writer_queue_depth` (sampled gauge).

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.17.
- [ ] Operations doc covers retention, partitions, archive restore.
