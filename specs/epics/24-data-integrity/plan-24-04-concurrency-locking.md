# Implementation Plan — Story 24.4 Concurrency and locking

> Companion to [story-24-04-concurrency-locking.md](story-24-04-concurrency-locking.md).
> Story states *what* and *why*; this plan states *how*.
> Job claim with `SELECT ... FOR UPDATE SKIP LOCKED` follows
> [architecture.md §7.3](../../architecture.md). Watch-progress contract
> from [Epic 7 Story 7.11](../07-api-server/plan-07-11-watch-progress-sync.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Job claim | `SELECT ... FOR UPDATE SKIP LOCKED` in a tx; `ORDER BY priority DESC, created_at ASC`. |
| Watch progress | Last-writer-wins per `(user, video)`; *no* monotonicity check. Server-side debounce (1 s) deduplicates bursts. |
| Chroma single-writer | Startup peer-detection — second writer process refuses with a clear error; consistent with [Epic 19 Story 19.4](../19-scalability/story-19-04-pipeline-scale-out.md). |
| Postgres advisory locks | `pg_advisory_xact_lock` for per-resource serialization (per-GPU, per-cache-eviction). Released on tx commit/rollback or connection close. |
| Out of scope | Job state machine (architecture §7); pipeline scale-out (19.4); audit log content (21.6). |

## 1. Architecture diagram

```
   ┌─────────────────────────────┐
   │ JobClaimer.Claim()          │
   │  BEGIN;                     │
   │   SELECT … FOR UPDATE SKIP  │
   │      LOCKED ORDER BY pr,ts  │
   │   UPDATE state=RUNNING      │
   │  COMMIT;                    │
   └──────────────┬──────────────┘
                  │
   ┌──────────────▼──────────────┐
   │ stage runs                  │
   │   pg_advisory_xact_lock(N)  │ for GPU 0 etc.
   └─────────────────────────────┘

   POST /api/watch-progress
   ┌─────────────────────────────┐
   │ debounce (1 s)              │
   │ UPSERT (user, video)        │
   │ audit_log row               │
   └─────────────────────────────┘

   pipeline boot
   ┌─────────────────────────────┐
   │ chroma peer detect          │
   │  open lock-file in chroma_dir │
   │  refuse if held             │
   └─────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/pipeline/claim.py` | The claim loop. |
| `pipeline/src/maktaba_pipeline/pipeline/advisory.py` | Wrappers around pg_advisory_xact_lock. |
| `pipeline/src/maktaba_pipeline/search/chroma_lock.py` | Peer-detect on the embedded ChromaDB store. |
| `api/internal/http/watch_progress.go` | Watch-progress endpoint with debounce. |
| `api/internal/store/watch_progress.go` | Storage UPSERT. |
| Tests — `tests/integration/concurrency_*.py`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `shared/db/queries/processing_jobs.sql` | Adds the SKIP LOCKED variant. |
| `pipeline/src/maktaba_pipeline/pipeline/runner.py` | Wraps stages that touch shared resources in advisory locks. |

### 2.3 Job claim

`shared/db/queries/processing_jobs.sql`:

```sql
-- name: ClaimNextJob :one
WITH next AS (
    SELECT id
    FROM processing_jobs
    WHERE state = 'QUEUED'
      AND (run_after IS NULL OR run_after <= now())
    ORDER BY priority DESC, created_at ASC
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE processing_jobs
SET state = 'RUNNING',
    claimed_at = now(),
    claimed_by = $1
FROM next
WHERE processing_jobs.id = next.id
RETURNING processing_jobs.*;
```

`claim.py`:

```python
async def claim(db, worker_id: str) -> Job | None:
    async with db.transaction():
        row = await db.fetch_one("...ClaimNextJob...", worker_id)
        return Job.from_row(row) if row else None
```

### 2.4 Advisory locks

`advisory.py`:

```python
import hashlib

@asynccontextmanager
async def advisory_xact(db, namespace: str, key: str):
    """Hold a Postgres advisory lock for the current transaction."""
    n = _hash32(namespace)
    k = _hash32(key)
    await db.execute("SELECT pg_advisory_xact_lock($1, $2)", n, k)
    yield

def _hash32(s: str) -> int:
    return int.from_bytes(hashlib.blake2b(s.encode(), digest_size=4).digest(), "big", signed=True)
```

Stage runners use `advisory_xact("gpu", "0")` to serialize transcribe
work across workers when `MAKTABA_GPU_DEVICES=0` is shared. Locks
release on tx commit/rollback automatically (PG semantics).

### 2.5 Chroma single-writer

`chroma_lock.py`:

```python
import os
import errno
from pathlib import Path

LOCK_FILE_NAME = ".maktaba.chroma.writer.pid"

class ChromaPeerExists(RuntimeError): ...

def acquire(chroma_dir: Path) -> Path:
    """Take an exclusive write-lock on the embedded chroma dir.

    Returns the lock-file path. Holds an exclusive flock for the
    process lifetime; the kernel releases on exit/crash so a hung
    writer doesn't block forever.
    """
    chroma_dir.mkdir(parents=True, exist_ok=True)
    lock = chroma_dir / LOCK_FILE_NAME
    fd = os.open(lock, os.O_RDWR | os.O_CREAT)
    try:
        import fcntl
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError as e:
        if e.errno in (errno.EWOULDBLOCK, errno.EACCES):
            raise ChromaPeerExists(
                f"Another pipeline process is writing to {chroma_dir}. "
                "Pipeline horizontal scale-out is bounded for embedded ChromaDB; "
                "see Story 19.4."
            ) from e
        raise
    os.write(fd, str(os.getpid()).encode())
    return lock
```

Called once on startup. Failure aborts the boot. Documented in
operations as the "second writer refused" condition.

### 2.6 Watch progress

`api/internal/store/watch_progress.go`:

```go
type WatchStore struct {
    db        *db.Queries
    debouncer *Debouncer  // 1 s coalescing
    audit     *audit.Writer
}

func (s *WatchStore) Put(ctx context.Context, userID, videoID uuid.UUID, posSec float64) error {
    s.audit.Append(ctx, audit.Event{
        Category: audit.CategoryActivity,
        Action:   "watch.put",
        Actor:    audit.Actor{User: userID.String()},
        Resource: audit.Resource{Type: "video", ID: videoID.String()},
        Detail:   map[string]any{"position_sec": posSec},
    })
    s.debouncer.Coalesce(string(userID[:])+string(videoID[:]), func() {
        // Last-writer-wins; no monotonicity check (story AC2).
        _ = s.db.UpsertWatchProgress(ctx, db.UpsertWatchProgressParams{
            UserID:      userID,
            VideoID:     videoID,
            PositionSec: posSec,
            UpdatedAt:   time.Now(),
        })
    })
    return nil
}
```

The debouncer coalesces writes to one persisted UPSERT per second per
key (matches Epic 7.11). Every received POST emits an audit row;
audit captures the full sequence even when the persisted state only
shows the last value (EC4).

### 2.7 Stale-resource handling

When the video has been deleted, the upsert hits a FK violation; the
handler logs a structured warning with `category=stale_resource` and
returns 200 OK (the client cannot meaningfully distinguish a write to
a recently-deleted video):

```go
if errors.Is(err, db.ErrFkViolation) {
    slog.Info("watch_progress.stale_resource",
        "user", userID, "video", videoID)
    return nil  // documented EC3 — drop, don't error.
}
```

## 3. Test plan

### 3.1 Watch-progress race (TC1)

| Test | What it pins |
|---|---|
| `TestRaceProgressLastValueWins` | 10 concurrent POSTs with positions [5, 10, 15, ..., 50] in random order; the final stored value is whichever arrived last at the server (recorded by audit log). |
| `TestAuditLogRecordsAllPosts` | Same scenario; audit_log shows 10 rows in arrival order. |

### 3.2 Rewind accepted (TC2)

| Test | What it pins |
|---|---|
| `TestRewindFromHigherToLower` | POST 30 → store 30; POST 10 → store 10; no `seek` flag required, no rejection. |
| `TestRapidScrub` | 100 POSTs over 5 s with positions oscillating 5..30..5..30; debouncer persists ~5 rows; the final value matches the last POST. |

### 3.3 Advisory lock release on crash (TC3)

| Test | What it pins |
|---|---|
| `TestAdvisoryLockReleasesOnCrash` | Acquire lock, kill the holder; the next acquirer succeeds within the connection-timeout window (idle-in-tx or terminated). |
| `TestAdvisoryReleaseOnExplicitUnlock` | Acquire and release in same tx; the next acquirer succeeds immediately. |
| `TestAdvisoryHeartbeatReaper` (EC2) | Stale advisory holder (no heartbeat for > 3× period) is reaped; the holder's tx is terminated by a janitor query the reaper runs. |

### 3.4 Chroma single-writer (TC4)

| Test | What it pins |
|---|---|
| `TestSecondPipelineRefusesChromaPath` | Boot pipeline #1; boot pipeline #2 with same `chroma_dir`; #2 raises `ChromaPeerExists` and exits. |
| `TestKillFirstReleasesLock` | Kill #1; #2 boots successfully (kernel released the flock). |
| `TestSingleWriterMatches19_4_TC4` | Aligns with Story 19.4 TC4 fixture. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| SKIP LOCKED with priority queues (EC1) | The claim query orders by `priority DESC, created_at ASC` BEFORE the SKIP LOCKED filter, so concurrent claimers each pick the highest-priority unclaimed row. | `TestPriorityPreservedAcrossClaimers` |
| Long-held advisory lock (EC2) | Stage runner heartbeats (`processing_jobs.heartbeat_at`); if heartbeat is > 3× the period stale, the reaper terminates the connection holding the lock via `pg_terminate_backend`. | `TestAdvisoryHeartbeatReaper` |
| Watch-progress for deleted video (EC3) | FK violation → drop with `category=stale_resource` log; client sees 200. | `TestWatchProgressDeletedVideo` |
| Debounce window crossing tx (EC4) | Each persisted write is its own tx; the debouncer coalesces *POSTs*, not segments. The audit log records every POST regardless. | `TestDebouncePersistsOnePerSecond` |
| Two workers tied on priority+timestamp | Tie broken by row physical order (Postgres deterministic given indexes); both still get unique rows due to SKIP LOCKED. | `TestTiebreakDeterministicByIndex` |
| Advisory lock space exhaustion | Postgres advisory lock space is 2^32 × 2^32 (signed int4 namespace + key); namespacing via blake2b hash makes collisions negligible. | n/a |
| Chroma write under server-mode | The peer-detect check is bypassed when `chroma.mode=server`; documented as deferred until the server-mode deployment is supported (Story 19.4). | `TestServerModeBypassesLocalLock` |
| Watch-progress upsert under high concurrency | Postgres `INSERT ... ON CONFLICT (user_id, video_id) DO UPDATE` is internally serialized per row; throughput per row is ~thousands/s. The debouncer caps the actual rate at 1/s/row. | `TestUpsertHighConcurrency` |
| `claimed_by` revealed in stuck job | The claim writes the worker hostname; `processing_jobs.claimed_by` lets ops attribute orphaned RUNNING jobs to a specific worker. | n/a |
| Job with `run_after` in future | Skipped by the claim query; the next worker poll picks it up after the timestamp. | `TestRunAfterDeferral` |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `asyncpg` | already | DB tx + advisory locks. |
| `fcntl` (Python) | stdlib | flock for chroma. |
| `time/rate` (Go) | already | Debouncer. |

## 6. Acceptance checklist

**Job claim**
- [ ] `SELECT … FOR UPDATE SKIP LOCKED` with priority+timestamp ordering.
- [ ] Tx wraps the SELECT + UPDATE.

**Watch progress**
- [ ] Last-writer-wins; no `seek` flag.
- [ ] Audit log captures every POST.
- [ ] Server-side debounce 1 s.

**Chroma**
- [ ] Lock file with flock under `chroma_dir`.
- [ ] Second writer refused with documented error.

**Advisory locks**
- [ ] `pg_advisory_xact_lock` used for per-resource serialization.
- [ ] Reaper terminates stale holders past heartbeat window.
