# Implementation Plan — Story 9.6 Manual Scan Trigger and Scan Progress

> Companion to [story-09-06-manual-scan.md](story-09-06-manual-scan.md).
> The story states *what* and *why*; this plan states *how*.
> Bridges Epic 7 Story 7.3 (the HTTP route) and Story 9.3 (the
> `SweepRunner` that does the work). Adds the SUPERSEDED state path
> and the `?rehash=true` mode.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| HTTP route | `POST /api/libraries/{id}/scan` — Go-side handler in `api/internal/handlers/libraries/scan.go`. The handler validates the library, parses `?rehash`/`?dry_run`, and calls `internal/jobs.Enqueue` with `stage='scan', priority=50, payload={library_id, reason="manual", rehash}`. |
| Worker | Reuses `pipeline/src/maktaba_pipeline/sweep/sweep_runner.py::run_sweep_job`. No new worker; the runner already accepts the `rehash` payload flag. This story extends the runner with the SUPERSEDED branch. |
| Progress shape | Reuses `processing_jobs.processed_seconds` (= files scanned) and `total_duration_seconds` (= files to scan). The `metrics` JSONB carries the structured counters (`new_videos`, `moved_videos`, `removed_videos`, `superseded_videos`). The §7.10 WS event already serializes from these columns; no new fields. |
| Estimated total | A `find -type f \\( -name '*.mp4' -o ... \\)` count at the start of the sweep populates `total_duration_seconds`. The walker emits `processed_seconds` updates at 1 Hz (already in Story 9.3 `_push_progress_loop`). |
| SUPERSEDED branch | Only when `rehash=true` AND the recomputed hash differs from the stored hash. Splits the row: old row keeps `id`, gets `state='SUPERSEDED'`; a new row is inserted with the new hash. Both point to the same `path` until the next sweep — but only the new row's `state` is alive. |
| Cancel | The sweep runner already polls `processing_jobs.cancel_requested` between batches (Story 6.4). Manual-scan cancel is the same mechanism. |
| Out of scope | The HTTP route's auth check (Epic 7 Story 7.3); the WS event format (Epic 7 Story 7.10); the route's rate-limiting (Epic 10 Story 10.5). |

## 1. Architecture diagram

```
   ┌──────────────────────────────────────────────────────────────┐
   │  Client → POST /api/libraries/{id}/scan?rehash=true          │
   └────────────────┬─────────────────────────────────────────────┘
                    │
                    ▼
   ┌──────────────────────────────────────────────────────────────┐
   │  api/internal/handlers/libraries/scan.go                     │
   │   1. parse query params: rehash bool, dry_run bool           │
   │   2. assert library exists, not deleted_at                   │
   │   3. enqueue(stage=scan, video_id=NULL, priority=50,         │
   │              payload={library_id, reason="manual",           │
   │                       rehash, dry_run, by_user})             │
   │   4. respond 202 Accepted with job_id (and outcome:          │
   │      "inserted" | "reused")                                  │
   │   5. WS broadcast `scan:queued` (Epic 7 Story 7.10)         │
   └────────────────┬─────────────────────────────────────────────┘
                    │ NOTIFY 'jobs.new' (Story 6.1)
                    ▼
   ┌──────────────────────────────────────────────────────────────┐
   │  ScanWorker dispatches → run_sweep_job(payload)              │
   │                                                              │
   │  PRE-WALK: estimate total                                    │
   │    total = await fast_count(roots, ignore)                   │
   │    UPDATE processing_jobs                                    │
   │       SET total_duration_seconds = $1                        │
   │     WHERE id = job.id                                        │
   │                                                              │
   │  WALK: as in Story 9.3 _process_one, plus:                   │
   │    if rehash and existing_row and existing_row.content_hash: │
   │       new_hash = await blake3_4mib_async(path, st.size)      │
   │       if new_hash != existing_row.content_hash:              │
   │           BEGIN TX                                           │
   │             UPDATE videos                                    │
   │                SET state='SUPERSEDED', updated_at=now()      │
   │              WHERE id = existing_row.id                      │
   │             INSERT new videos row(...) with new_hash         │
   │             enqueue(stage=probe, ...)                        │
   │           COMMIT                                             │
   │           progress.superseded_videos += 1                    │
   │       else: fast-path skip                                   │
   │                                                              │
   │  POST-WALK: same finalizer as Story 9.3 — finishes           │
   │            library_sweeps row, publishes LIBRARY_SWEEP_DONE. │
   └──────────────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/handlers/libraries/scan.go` | HTTP handler. |
| `api/internal/handlers/libraries/scan_test.go` | Handler tests per §6.1. |
| `pipeline/src/maktaba_pipeline/sweep/fast_count.py` | Pre-walk total estimator (single `find`-equivalent pass that only counts; no stat). |
| `pipeline/tests/sweep/test_rehash_supersede.py` | Integration tests per §6.2. |
| `pipeline/tests/sweep/test_progress_pacing.py` | Test that progress updates fire at ≈1 Hz. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/sweep/sweep_runner.py` | Add the `rehash` SUPERSEDED branch; add the pre-walk total count; honor `dry_run`. |
| `pipeline/src/maktaba_pipeline/sweep/sweep_runner.py` | Read `payload.dry_run` — when set, skip every DB write (just count the would-be operations into `progress`); the response WS message gets `dry_run=true`. |
| `api/internal/router.go` | Wire `POST /api/libraries/{id}/scan` to the new handler. |
| `shared/db/queries/videos.sql` | Add `MarkVideoSuperseded` (sets `state='SUPERSEDED'`). |
| `shared/db/migrations/0034_videos_superseded_state.sql` | Adds `'SUPERSEDED'` to the `videos.state` CHECK constraint (if it isn't already). |
| `specs/epics/09-library-management/README.md` | Tick story 9.6. |

### 2.3 Type definitions

```go
// api/internal/handlers/libraries/scan.go
package libraries

type ScanRequest struct {
    LibraryID uuid.UUID `swaggerignore:"true"` // from path
    Rehash    bool      `query:"rehash"`
    DryRun    bool      `query:"dry_run"`
}

type ScanResponse struct {
    JobID   int64  `json:"job_id"`
    Outcome string `json:"outcome"`     // "inserted" | "reused"
    Reason  string `json:"reason"`      // "manual"
    DryRun  bool   `json:"dry_run"`
}
```

```python
# pipeline/src/maktaba_pipeline/sweep/sweep_runner.py — payload extension
@dataclass(slots=True, frozen=True)
class SweepPayload:
    library_id: UUID
    reason: Literal["periodic", "manual", "watcher_boot_catchup"]
    rehash: bool = False
    dry_run: bool = False
    by_user: UUID | None = None
```

### 2.4 Function signatures

```go
// api/internal/handlers/libraries/scan.go
func ScanHandler(deps *handlers.Deps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // ...parse, validate, enqueue, respond...
    }
}
```

```python
# pipeline/src/maktaba_pipeline/sweep/fast_count.py
async def fast_count(roots: list[Path], ignore: IgnoreMatcher) -> int:
    """Single os.scandir pass that counts scannable files without stat'ing them."""
```

```python
# pipeline/src/maktaba_pipeline/sweep/sweep_runner.py — extension
async def _maybe_supersede(db, existing, path, st, *,
                           audit, progress, dry_run) -> bool:
    """Returns True if a supersede happened (caller continues to next file)."""
```

## 3. Database

### 3.1 `shared/db/migrations/0034_videos_superseded_state.sql`

```sql
-- +goose Up
-- +goose StatementBegin

-- Pipeline Story 1.5 introduced 'MISSING'; this story introduces
-- 'SUPERSEDED' (alongside 'READY_NO_AUDIO' and 'CORRUPTED' from
-- REVIEW §1.3.a — those are added here to keep the CHECK comprehensive).
ALTER TABLE videos
    DROP CONSTRAINT IF EXISTS videos_state_chk;

ALTER TABLE videos
    ADD CONSTRAINT videos_state_chk CHECK (
        state IN (
            'DISCOVERED', 'PROBED', 'AUDIO_EXTRACTED', 'TRANSCRIBED',
            'INDEXED', 'THUMBNAILED', 'READY', 'READY_NO_AUDIO',
            'FAILED', 'MISSING', 'SUPERSEDED', 'CORRUPTED'
        )
    );

-- Index for stats queries that filter by state (Story 9.7 reads this).
CREATE INDEX IF NOT EXISTS videos_state_lookup
    ON videos (library_id, state);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE videos DROP CONSTRAINT IF EXISTS videos_state_chk;
-- Restore the original CHECK from migration 0001:
ALTER TABLE videos
    ADD CONSTRAINT videos_state_chk CHECK (
        state IN (
            'DISCOVERED', 'PROBED', 'AUDIO_EXTRACTED',
            'TRANSCRIBED', 'INDEXED', 'THUMBNAILED', 'READY', 'FAILED'
        )
    );
DROP INDEX IF EXISTS videos_state_lookup;
-- +goose StatementEnd
```

### 3.2 `shared/db/queries/videos.sql`

```sql
-- name: MarkVideoSuperseded :exec
UPDATE videos
   SET state = 'SUPERSEDED', updated_at = now()
 WHERE id = $1;

-- name: SplitVideoForSupersede :one
WITH old AS (
    UPDATE videos SET state = 'SUPERSEDED', updated_at = now()
     WHERE id = $1
    RETURNING library_id, path, size
)
INSERT INTO videos (id, library_id, path, size, content_hash, state)
SELECT $2, library_id, path, $3, $4, 'DISCOVERED' FROM old
RETURNING id;
```

## 4. Code scaffolding

### 4.1 Go HTTP handler

```go
// api/internal/handlers/libraries/scan.go
package libraries

import (
    "encoding/json"
    "net/http"

    "github.com/google/uuid"
    "maktaba/api/internal/handlers"
    "maktaba/api/internal/jobs"
)

func ScanHandler(d *handlers.Deps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()
        libraryID, err := uuid.Parse(chi.URLParam(r, "id"))
        if err != nil {
            handlers.WriteError(w, 400, "bad-library-id", err.Error())
            return
        }

        rehash := r.URL.Query().Get("rehash") == "true"
        dryRun := r.URL.Query().Get("dry_run") == "true"

        lib, err := d.Queries.GetLibrary(ctx, libraryID)
        if errors.Is(err, pgx.ErrNoRows) {
            handlers.WriteError(w, 404, "library-not-found", "")
            return
        }
        if err != nil {
            handlers.WriteError(w, 500, "db-error", err.Error())
            return
        }
        if lib.DeletedAt.Valid {
            handlers.WriteError(w, 409, "library-deleted", "")
            return
        }

        actor := handlers.RequireUser(ctx)
        payload := map[string]any{
            "library_id": libraryID,
            "reason":     "manual",
            "rehash":     rehash,
            "dry_run":    dryRun,
            "by_user":    actor.ID,
        }

        res, err := jobs.Enqueue(ctx, d.Queries, jobs.EnqueueRequest{
            VideoID:  uuid.Nil,
            Stage:    "scan",
            Priority: 50,
            Payload:  payload,
        })
        if err != nil {
            handlers.WriteError(w, 500, "enqueue-failed", err.Error())
            return
        }

        d.WS.Broadcast("scan:queued", ScanResponse{
            JobID: res.ID, Outcome: res.Outcome,
            Reason: "manual", DryRun: dryRun,
        })

        handlers.WriteJSON(w, 202, ScanResponse{
            JobID: res.ID, Outcome: res.Outcome,
            Reason: "manual", DryRun: dryRun,
        })
    }
}
```

### 4.2 Pre-walk total counter

```python
# pipeline/src/maktaba_pipeline/sweep/fast_count.py
import os
from pathlib import Path

from ..ignore.matcher import IgnoreMatcher

async def fast_count(roots, ignore: IgnoreMatcher) -> int:
    n = 0
    visited: set[tuple[int,int]] = set()

    def _count(d: str):
        nonlocal n
        try:
            it = os.scandir(d)
        except (PermissionError, FileNotFoundError):
            return
        with it:
            for entry in it:
                if ignore.matches(entry.path):
                    continue
                try:
                    if entry.is_symlink():
                        # Symlink loops handled by visited set on real walker;
                        # here we just count once.
                        target = os.path.realpath(entry.path)
                        try:
                            st = os.stat(target)
                        except OSError:
                            continue
                        key = (st.st_dev, st.st_ino)
                        if key in visited:
                            continue
                        visited.add(key)
                    if entry.is_dir(follow_symlinks=True):
                        _count(entry.path)
                    elif entry.is_file(follow_symlinks=True) \
                            and ignore.is_supported_extension(entry.path):
                        n += 1
                except OSError:
                    continue

    for r in roots:
        _count(str(r))
    return n
```

The fast count is `O(num_files)` but does no `stat()` for files
(extension comes from the dirent name). On a 100k-file SSD library it
finishes in ~3 s.

### 4.3 SUPERSEDED branch in `sweep_runner._process_one`

```python
# pipeline/src/maktaba_pipeline/sweep/sweep_runner.py — diff
async def _process_one(db, library_id, path, st,
                       catalog_by_path, catalog_by_hash,
                       visited, progress, payload: SweepPayload,
                       audit) -> None:
    cat = catalog_by_path.get(str(path))

    if cat is not None and not payload.rehash \
            and cat.size == st.st_size \
            and abs(cat.mtime - st.st_mtime) < 1.0:
        return  # fast path

    # rehash mode against an existing row that has a stored hash:
    if payload.rehash and cat is not None and cat.content_hash is not None:
        new_hash = await blake3_4mib_async(path, st.st_size)
        if new_hash == cat.content_hash:
            # File unchanged in content; size+mtime were probably "drifted"
            # by a tool but the bytes didn't change. Treat as fast-path.
            return
        # Real content change → split.
        if payload.dry_run:
            progress.superseded_videos += 1
            return
        async with db.transaction():
            new_id = uuid4()
            await db.execute(
                "UPDATE videos SET state='SUPERSEDED', updated_at=now() "
                "WHERE id=$1", cat.id,
            )
            await db.execute(
                "INSERT INTO videos "
                "  (id, library_id, path, size, content_hash, state) "
                "VALUES ($1,$2,$3,$4,$5,'DISCOVERED')",
                new_id, library_id, str(path), st.st_size, new_hash,
            )
            await audit.write(
                category="library", event="video-superseded",
                library_id=library_id, video_id=new_id,
                payload={
                    "old_video_id": str(cat.id),
                    "old_hash": cat.content_hash.hex(),
                    "new_hash": new_hash.hex(),
                    "path": str(path),
                    "by_user": str(payload.by_user) if payload.by_user else None,
                },
            )
            await enqueue(db, video_id=new_id, stage=Stage.PROBE,
                          priority=100,
                          payload={"reason": "post_supersede"})
        progress.superseded_videos += 1
        return

    # Not in catalog (fresh path) — same as Story 9.3.
    ...
```

### 4.4 Pre-walk total population

```python
# pipeline/src/maktaba_pipeline/sweep/sweep_runner.py — boot block
total = await fast_count(roots_paths, ignore)
await db.execute(
    "UPDATE processing_jobs "
    "  SET total_duration_seconds = $1, "
    "      progress_updated_at = now() "
    "WHERE id = $2",
    float(total), job.id,
)
```

### 4.5 Dry-run respect

When `payload.dry_run = True`:

- `_process_one` increments `progress.*` counters but performs no DB writes.
- `_finalizer` writes the `library_sweeps` row with `errors_jsonb={"dry_run": true}`.
- The user-facing WS event includes `dry_run: true`.
- The advisory unique-in-flight index still applies → user sees the
  "what would happen" summary but cannot run two dry-runs concurrently.

## 5. Test plan

### 5.1 Go handler tests (`scan_test.go`)

| Test | What it pins |
|---|---|
| `TestScanHandler_AcceptsValidLibrary` | 202 response with `outcome: "inserted"`. |
| `TestScanHandler_404OnUnknownLibrary` | Unknown UUID → 404 `library-not-found`. |
| `TestScanHandler_409OnDeletedLibrary` | Library with `deleted_at IS NOT NULL` → 409 `library-deleted`. |
| `TestScanHandler_RehashFlagPropagatesToPayload` | `?rehash=true` → enqueued payload has `rehash: true`. |
| `TestScanHandler_DryRunFlagPropagatesToPayload` | `?dry_run=true` → enqueued payload has `dry_run: true`. |
| `TestScanHandler_SecondCallReturnsReused` | Two calls in quick succession → second returns `outcome: "reused"` with same `job_id`. |
| `TestScanHandler_PriorityIs50` | Audit the enqueue call: `Priority == 50`. |
| `TestScanHandler_BroadcastsScanQueued` | The WS hub gets a `scan:queued` event with the same payload as the response. |

### 5.2 Pipeline integration tests

`test_rehash_supersede.py`:

| Test | What it pins |
|---|---|
| `test_rehash_no_change_takes_fast_path` | Existing row with hash H; on-disk file with hash H → no DB writes; `progress.scanned += 1`; counter `maktaba_supersede_skipped_total`. |
| `test_rehash_changed_supersedes_old_row` | Existing row with hash H1; on-disk hash H2 → old row has `state='SUPERSEDED'`; new row inserted with hash H2 and `state='DISCOVERED'`; one probe job enqueued for the new row. AC-2. |
| `test_rehash_dry_run_no_writes` | `dry_run=true` → no UPDATE/INSERT; `progress.superseded_videos == 1`. |
| `test_rehash_audit_row_written` | Audit log contains a `library/video-superseded` row with both hashes. |
| `test_rehash_against_missing_file_is_noop` | Catalog row whose path is gone → MISSING transition (Story 9.3 path); rehash never runs. |
| `test_rehash_progress_counter_pacing` | A 1k-file rehash run reports `processed_seconds` ≈ 1 Hz. |

`test_progress_pacing.py`:

| Test | What it pins |
|---|---|
| `test_total_duration_seconds_set_at_start` | After fast_count, `total_duration_seconds == count_of_files`. AC-3. |
| `test_processed_seconds_monotonic` | Read `processed_seconds` 10× during a 5-s sweep; values are non-decreasing. |

### 5.3 Race tests

| Test | What it pins |
|---|---|
| `test_scan_race_with_watcher_event` | Watcher enqueues a scan for a path while a manual sweep is in flight; both call `enqueue` → unique partial index keeps it to one row, and `INSERT … ON CONFLICT (content_hash) DO UPDATE` (Story 9.4) handles the deterministic path resolution. AC-edge case "scan started while watcher events are in-flight". |
| `test_scan_canceled_preserves_partial_progress` | Cancel mid-walk; rows already inserted remain; the runner exits cleanly at the next batch boundary. |

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| `?rehash=true` against a library with 100k files | The pre-walk fast-count gives the user a total to track against; the walker hashes only files whose `(size, mtime)` already matched (the existing row had a hash) and *might* differ. The wall-clock cost is `100k × (mean hash time)` — at 4 MiB / file that is ~1 GiB total + 8 byte appends, well under 100 ms (Story 9.4 §6.1's perf gate). | Documented; perf gate inherits from Story 9.4. |
| Manual scan + watcher event for the same new file | The watcher's `enqueue` and the sweep's `enqueue` both call the idempotent helper; one wins. The other returns `outcome='reused'`. Both DB writes converge through Story 9.4's upsert. | `test_scan_race_with_watcher_event` |
| Cancel via Epic 7 Story 7.12 mid-walk | `cancel_requested=true` is polled at every batch boundary in the walker (Story 6.4). On stop: in-flight DB writes complete; the `library_sweeps` row gets `finished_at=now()`, `errors_jsonb={"canceled":true}`. Already-inserted videos remain. | `test_scan_canceled_preserves_partial_progress` |
| Manual scan called while a periodic sweep is running | The partial-unique index `library_sweeps_one_in_flight` rejects the second `INSERT`. Worker logs `sweep_skipped_lock_busy`. Job is marked DONE with no work. The user-facing toast says "scan already in progress; queued". | Story 9.3 `test_single_flight_drops_concurrent_tick` |
| `dry_run=true` collides with `rehash=true` | Allowed combination — counts what would change without doing it. | `test_rehash_dry_run_no_writes` |
| `total_duration_seconds` updated mid-walk by an unrelated heartbeat | The runner sets it once at the start; downstream heartbeats in the worker only touch `processed_seconds` and `last_heartbeat_at`. Documented in Story 6.3's plan. | n/a (cross-story) |
| File on disk smaller than 8 MiB but stored row has a hash from when it was 16 MiB | `content_hash` mismatch → SUPERSEDED branch fires. The size/mtime fast path was already bypassed because `cat.size != st.st_size`. | `test_rehash_changed_supersedes_old_row` (variant: also vary size) |

## 7. Configuration

Reuses the same effective-settings keys as Stories 9.3 and 9.4. No new
keys for this story.

## 8. Dependencies

| Dep | Source | Why |
|---|---|---|
| Story 9.3 `run_sweep_job` | required | Worker entry point. |
| Story 9.4 `blake3_4mib_async` | required | Per-file hash for the SUPERSEDED branch. |
| Story 6.1 `enqueue` | required | Idempotent enqueue. |
| Story 6.3 progress columns | already in schema | `processed_seconds`, `total_duration_seconds`, `progress_updated_at`. |

## 9. Acceptance checklist

**Code**
- [ ] `api/internal/handlers/libraries/scan.go` ships and is wired in `router.go`.
- [ ] `pipeline/src/maktaba_pipeline/sweep/fast_count.py` returns a count in O(num_files) without `stat`-ing files.
- [ ] `_process_one` honors `rehash` and `dry_run`; SUPERSEDED branch writes audit row and enqueues a probe.
- [ ] `total_duration_seconds` is set once at run start.
- [ ] WS broadcast `scan:queued` fires from the handler.

**Migration**
- [ ] `0034_videos_superseded_state.sql` extends `videos_state_chk` to include `SUPERSEDED`, `READY_NO_AUDIO`, `MISSING`, `CORRUPTED`.
- [ ] `videos_state_lookup` index exists.

**Behaviour (story acceptance criteria)**
- [ ] AC-1: default mode applies the size+mtime fast path; new/changed files enqueue.
- [ ] AC-2: `?rehash=true` recomputes hashes; mismatched files are split via SUPERSEDED.
- [ ] AC-3: progress reports use `processed_seconds`/`total_duration_seconds`; the WS event shape is unchanged.

**Observability**
- [ ] Counter `maktaba_supersede_total{outcome=split|skipped}` exported.
- [ ] Counter `maktaba_manual_scan_requests_total{rehash, dry_run}` exported.
- [ ] Histogram `maktaba_fast_count_duration_seconds` for the pre-walk count.

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.6.
- [ ] API reference documents `?rehash=true`, `?dry_run=true`, the 202 response shape, and the `scan:queued` WS event.
