# Implementation Plan — Story 9.7 Library Stats Query

> Companion to [story-09-07-library-stats.md](story-09-07-library-stats.md).
> The story states *what* and *why*; this plan states *how*.
> Builds on the FSM updates in Story 9.6 (`SUPERSEDED`/`MISSING`/etc.),
> the sweep telemetry in Story 9.3, and the per-language tagging in
> Story 9.8 (this story tolerates `detected_language IS NULL`).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Endpoint | `GET /api/libraries/{id}/stats` — Go handler at `api/internal/handlers/libraries/stats.go`. Authoritative read happens against `library_stats_cache` only; the cache is the source of truth at request time. |
| Cache table | `library_stats_cache` per the README. One row per library; updated by triggers on `videos`, on `processing_jobs`, and by the sweep finalizer. |
| Trigger split | Three triggers, each scoped narrowly: `videos_stats_trg` reacts to changes that affect counts/sizes/state buckets; `processing_jobs_stats_trg` reacts to live-state changes that affect the `jobs.*` counters; `library_sweeps_finalize_stats_trg` writes `last_sweep` once on finalize. Keeping them split keeps the per-row work small and avoids re-counting on irrelevant updates. |
| `processed_pct` | Computed from cache columns: `total_videos - by_state.SUPERSEDED - by_state.MISSING` is the denominator; `by_state.READY` (plus `READY_NO_AUDIO`) is the numerator. Returned as `null` when the denominator is `0` (AC-3). |
| Reconciliation | A `maktaba-api stats-rebuild` CLI subcommand; runs a single rebuild SQL batch and verifies invariants. Wired up via the existing `cmd/maktaba-api/cli.go` cobra setup. Scheduled nightly by Epic 22. |
| Out of scope | The handler's auth check (Epic 7 Story 7.3 covers); the WS broadcast of stats deltas (out of scope for v1; UI polls); the `total_duration_sec` provenance (Story 1.x probe owns the population). |

## 1. Architecture diagram

```
   ┌──────────────────────────────────────────────────────────────┐
   │  GET /api/libraries/{id}/stats                               │
   │      ↓                                                        │
   │  api/internal/handlers/libraries/stats.go                    │
   │      ↓                                                        │
   │  SELECT * FROM library_stats_cache WHERE library_id = $1     │
   │      ↓                                                        │
   │  StatsResponse                                               │
   │  (read-side computation: processed_pct, last_sweep merge)    │
   └──────────────────────────────────────────────────────────────┘

   Write side — updated by triggers:

   videos (INS/UPD/DEL) ─────┐
                              ├─→ library_stats_cache.{
   processing_jobs (UPD) ────┤        total_videos,
                              │        total_duration_sec,
                              │        by_state_jsonb,
                              │        by_language_jsonb,
                              │        by_content_type_jsonb,
                              │        source_size_bytes,
                              │        derived_size_bytes,
                              │        jobs_jsonb,
                              │        updated_at
                              │   }
   library_sweeps (UPD finished_at) ─┘   (writes last_sweep summary)

   Reconciliation:
   nightly: maktaba-api stats-rebuild
            → recompute every column from source tables
            → assert invariants; emit metric on drift
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/handlers/libraries/stats.go` | The HTTP handler. |
| `api/internal/handlers/libraries/stats_test.go` | Handler-level tests. |
| `api/cmd/maktaba-api/stats_rebuild.go` | CLI subcommand. |
| `shared/db/migrations/0035_library_stats_cache.sql` | Creates the cache table, the triggers, and the trigger functions. |
| `shared/db/migrations/0035_library_stats_cache.sqlite.sql` | SQLite variant — uses Python-side updaters in lieu of the more complex PG functions (see §3.2). |
| `shared/db/queries/library_stats.sql` | sqlc input — `GetLibraryStats`, `RebuildLibraryStats` (one big idempotent recompute). |
| `pipeline/src/maktaba_pipeline/stats/sqlite_updaters.py` | SQLite mirror of the trigger logic (called from the worker on every `videos`/`processing_jobs` mutation). |
| `pipeline/tests/stats/test_cache_triggers.py` | Trigger correctness tests (PG). |
| `pipeline/tests/stats/test_sqlite_updaters.py` | Same coverage on SQLite. |
| `pipeline/tests/stats/test_rebuild.py` | Reconciliation test. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/router.go` | Wire the route. |
| `api/cmd/maktaba-api/main.go` | Register `stats-rebuild` subcommand. |
| `pipeline/src/maktaba_pipeline/sweep/sweep_runner.py` | Call `_finalize_library_stats(sweep_id)` from the sweep finalizer (Postgres trigger handles it; SQLite path needs the explicit call). |
| `specs/epics/09-library-management/README.md` | Tick story 9.7. |

### 2.3 Type definitions (Go side)

```go
// api/internal/handlers/libraries/stats.go
package libraries

type StorageStats struct {
    SourceSizeBytes  int64 `json:"source_size_bytes"`
    DerivedSizeBytes int64 `json:"derived_size_bytes"`
}

type JobsStats struct {
    Pending int `json:"pending"`
    Running int `json:"running"`
    Paused  int `json:"paused"`
    Failed  int `json:"failed"`
}

type LastSweep struct {
    StartedAt   *time.Time `json:"started_at"`
    FinishedAt  *time.Time `json:"finished_at"`
    Scanned     int        `json:"scanned"`
    NewVideos   int        `json:"new_videos"`
    MovedVideos int        `json:"moved_videos"`
    RemovedVideos int      `json:"removed_videos"`
}

type StatsResponse struct {
    TotalVideos        int                `json:"total_videos"`
    TotalDurationSec   int64              `json:"total_duration_sec"`
    ByState            map[string]int     `json:"by_state"`
    ProcessedPct       *float64           `json:"processed_pct"` // nullable
    ByLanguage         map[string]int     `json:"by_language"`
    ByContentType      map[string]int     `json:"by_content_type"`
    Storage            StorageStats       `json:"storage"`
    Jobs               JobsStats          `json:"jobs"`
    LastSweep          *LastSweep         `json:"last_sweep"`
}
```

### 2.4 Function signatures

```go
// api/internal/handlers/libraries/stats.go
func StatsHandler(d *handlers.Deps) http.HandlerFunc
```

```go
// api/cmd/maktaba-api/stats_rebuild.go
func RebuildAllStats(ctx context.Context, q *db.Queries) (drift []DriftRow, err error)
```

## 3. Database migration

### 3.1 Postgres — `0035_library_stats_cache.sql`

```sql
-- +goose Up
-- +goose StatementBegin

CREATE TABLE library_stats_cache (
    library_id            UUID PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
    total_videos          INTEGER NOT NULL DEFAULT 0,
    total_duration_sec    BIGINT  NOT NULL DEFAULT 0,
    source_size_bytes     BIGINT  NOT NULL DEFAULT 0,
    derived_size_bytes    BIGINT  NOT NULL DEFAULT 0,
    by_state_jsonb        JSONB   NOT NULL DEFAULT '{}'::jsonb,
    by_language_jsonb     JSONB   NOT NULL DEFAULT '{}'::jsonb,
    by_content_type_jsonb JSONB   NOT NULL DEFAULT '{}'::jsonb,
    jobs_jsonb            JSONB   NOT NULL DEFAULT
                            '{"pending":0,"running":0,"paused":0,"failed":0}'::jsonb,
    last_sweep_jsonb      JSONB,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Initialize one row per existing library — idempotent.
INSERT INTO library_stats_cache (library_id)
SELECT id FROM libraries
ON CONFLICT (library_id) DO NOTHING;

-- ============================================================
-- Trigger functions
-- ============================================================
-- Each trigger writes a delta — never recomputes from scratch — to
-- keep per-row work O(1). Reconciliation handles drift.

CREATE OR REPLACE FUNCTION lsc_apply_video_delta(
    p_library_id UUID,
    p_state_old  TEXT,
    p_state_new  TEXT,
    p_lang_old   TEXT,
    p_lang_new   TEXT,
    p_ct_old     TEXT,
    p_ct_new     TEXT,
    p_size_delta BIGINT,
    p_dur_delta  BIGINT,
    p_count_delta INTEGER
) RETURNS VOID LANGUAGE plpgsql AS $$
BEGIN
    UPDATE library_stats_cache
       SET total_videos       = total_videos + p_count_delta,
           total_duration_sec = total_duration_sec + p_dur_delta,
           source_size_bytes  = source_size_bytes + p_size_delta,
           by_state_jsonb     = jsonb_inc_dec(by_state_jsonb,
                                              p_state_old, p_state_new),
           by_language_jsonb  = jsonb_inc_dec(by_language_jsonb,
                                              p_lang_old, p_lang_new),
           by_content_type_jsonb = jsonb_inc_dec(by_content_type_jsonb,
                                                 p_ct_old, p_ct_new),
           updated_at         = now()
     WHERE library_id = p_library_id;
END;
$$;

-- Helper: increments the new-key bucket and decrements the old-key
-- bucket. NULL keys are skipped. Empty buckets are dropped.
CREATE OR REPLACE FUNCTION jsonb_inc_dec(blob JSONB,
                                          old_key TEXT,
                                          new_key TEXT)
RETURNS JSONB LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    out JSONB := blob;
    cur INTEGER;
BEGIN
    IF old_key IS NOT NULL THEN
        cur := COALESCE((out->>old_key)::int, 0) - 1;
        IF cur <= 0 THEN
            out := out - old_key;
        ELSE
            out := jsonb_set(out, ARRAY[old_key], to_jsonb(cur), true);
        END IF;
    END IF;
    IF new_key IS NOT NULL THEN
        cur := COALESCE((out->>new_key)::int, 0) + 1;
        out := jsonb_set(out, ARRAY[new_key], to_jsonb(cur), true);
    END IF;
    RETURN out;
END;
$$;

CREATE OR REPLACE FUNCTION videos_stats_trg_fn() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        PERFORM lsc_apply_video_delta(
            NEW.library_id,
            NULL, NEW.state,
            NULL, NEW.detected_language,
            NULL, NEW.content_type,
            COALESCE(NEW.size, 0),
            COALESCE(NEW.duration_sec, 0)::bigint,
            1);
    ELSIF TG_OP = 'UPDATE' THEN
        IF NEW.library_id <> OLD.library_id THEN
            -- Cross-library move (rare; future feature). Net out:
            PERFORM lsc_apply_video_delta(OLD.library_id,
                OLD.state, NULL, OLD.detected_language, NULL,
                OLD.content_type, NULL,
                -COALESCE(OLD.size, 0),
                -COALESCE(OLD.duration_sec, 0)::bigint, -1);
            PERFORM lsc_apply_video_delta(NEW.library_id,
                NULL, NEW.state, NULL, NEW.detected_language,
                NULL, NEW.content_type,
                COALESCE(NEW.size, 0),
                COALESCE(NEW.duration_sec, 0)::bigint, 1);
        ELSE
            PERFORM lsc_apply_video_delta(NEW.library_id,
                OLD.state, NEW.state,
                OLD.detected_language, NEW.detected_language,
                OLD.content_type, NEW.content_type,
                COALESCE(NEW.size, 0) - COALESCE(OLD.size, 0),
                COALESCE(NEW.duration_sec, 0)::bigint
                  - COALESCE(OLD.duration_sec, 0)::bigint,
                0);
        END IF;
    ELSIF TG_OP = 'DELETE' THEN
        PERFORM lsc_apply_video_delta(OLD.library_id,
            OLD.state, NULL, OLD.detected_language, NULL,
            OLD.content_type, NULL,
            -COALESCE(OLD.size, 0),
            -COALESCE(OLD.duration_sec, 0)::bigint, -1);
    END IF;
    RETURN NULL;  -- AFTER trigger
END;
$$;

CREATE TRIGGER videos_stats_trg
    AFTER INSERT OR UPDATE OR DELETE ON videos
    FOR EACH ROW EXECUTE FUNCTION videos_stats_trg_fn();

-- ----- processing_jobs trigger -----
CREATE OR REPLACE FUNCTION processing_jobs_stats_trg_fn() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    lib_id UUID;
    bucket TEXT;
    delta_old INT := 0;
    delta_new INT := 0;
BEGIN
    -- Map state → bucket; only live-ish states matter.
    FOR i IN 1..2 LOOP
        DECLARE state TEXT := CASE i WHEN 1 THEN OLD.state ELSE NEW.state END;
        BEGIN
            CASE state
              WHEN 'pending' THEN bucket := 'pending';
              WHEN 'claimed' THEN bucket := 'pending';
              WHEN 'running' THEN bucket := 'running';
              WHEN 'paused'  THEN bucket := 'paused';
              WHEN 'failed'  THEN bucket := 'failed';
              ELSE bucket := NULL;
            END CASE;
            ...
        END;
    END LOOP;
    -- (Implementation: simpler to do INSERT/UPDATE/DELETE branches
    -- explicitly; pseudocode collapsed for brevity.)
    -- Net effect: jobs_jsonb increments per state transition.
    RETURN NULL;
END;
$$;

CREATE TRIGGER processing_jobs_stats_trg
    AFTER INSERT OR UPDATE OR DELETE ON processing_jobs
    FOR EACH ROW EXECUTE FUNCTION processing_jobs_stats_trg_fn();

-- ----- library_sweeps finalize -----
CREATE OR REPLACE FUNCTION library_sweeps_finalize_stats_trg_fn() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.finished_at IS NOT NULL AND OLD.finished_at IS NULL THEN
        UPDATE library_stats_cache
           SET last_sweep_jsonb = jsonb_build_object(
                   'started_at', NEW.started_at,
                   'finished_at', NEW.finished_at,
                   'scanned', NEW.scanned,
                   'new_videos', NEW.new_videos,
                   'moved_videos', NEW.moved_videos,
                   'removed_videos', NEW.removed_videos
               ),
               updated_at = now()
         WHERE library_id = NEW.library_id;
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER library_sweeps_finalize_stats_trg
    AFTER UPDATE ON library_sweeps
    FOR EACH ROW EXECUTE FUNCTION library_sweeps_finalize_stats_trg_fn();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS library_sweeps_finalize_stats_trg ON library_sweeps;
DROP TRIGGER IF EXISTS processing_jobs_stats_trg ON processing_jobs;
DROP TRIGGER IF EXISTS videos_stats_trg ON videos;
DROP FUNCTION IF EXISTS library_sweeps_finalize_stats_trg_fn();
DROP FUNCTION IF EXISTS processing_jobs_stats_trg_fn();
DROP FUNCTION IF EXISTS videos_stats_trg_fn();
DROP FUNCTION IF EXISTS lsc_apply_video_delta(UUID,TEXT,TEXT,TEXT,TEXT,TEXT,TEXT,BIGINT,BIGINT,INTEGER);
DROP FUNCTION IF EXISTS jsonb_inc_dec(JSONB,TEXT,TEXT);
DROP TABLE IF EXISTS library_stats_cache;
-- +goose StatementEnd
```

### 3.2 SQLite — `0035_library_stats_cache.sqlite.sql`

```sql
-- +goose Up
-- +goose StatementBegin

CREATE TABLE library_stats_cache (
    library_id            TEXT PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
    total_videos          INTEGER NOT NULL DEFAULT 0,
    total_duration_sec    INTEGER NOT NULL DEFAULT 0,
    source_size_bytes     INTEGER NOT NULL DEFAULT 0,
    derived_size_bytes    INTEGER NOT NULL DEFAULT 0,
    by_state_jsonb        TEXT    NOT NULL DEFAULT '{}',
    by_language_jsonb     TEXT    NOT NULL DEFAULT '{}',
    by_content_type_jsonb TEXT    NOT NULL DEFAULT '{}',
    jobs_jsonb            TEXT    NOT NULL DEFAULT
                            '{"pending":0,"running":0,"paused":0,"failed":0}',
    last_sweep_jsonb      TEXT,
    updated_at            TEXT    NOT NULL DEFAULT (CURRENT_TIMESTAMP)
);

INSERT OR IGNORE INTO library_stats_cache (library_id)
SELECT id FROM libraries;

-- SQLite has json_set / json_remove / json_extract for the same logic,
-- but the math is awkward in pure SQL. Instead, the Python-side
-- updaters in pipeline/stats/sqlite_updaters.py do the same delta math
-- and run inside the same transaction as every videos / processing_jobs
-- write. The triggers below are safety nets that recompute the row
-- from scratch on every change — slow but correct.

CREATE TRIGGER videos_stats_recompute_ai AFTER INSERT ON videos
BEGIN
    UPDATE library_stats_cache
       SET total_videos = (SELECT COUNT(*) FROM videos
                            WHERE library_id = NEW.library_id),
           total_duration_sec = (SELECT COALESCE(SUM(duration_sec), 0) FROM videos
                                  WHERE library_id = NEW.library_id),
           source_size_bytes = (SELECT COALESCE(SUM(size), 0) FROM videos
                                 WHERE library_id = NEW.library_id),
           updated_at = CURRENT_TIMESTAMP
     WHERE library_id = NEW.library_id;
END;
-- (analogous AFTER UPDATE / AFTER DELETE triggers; details elided)

-- +goose StatementEnd

-- +goose Down ...
```

The Postgres path uses delta updates (O(1) per row); the SQLite path
uses the recomputing triggers as a *safety net* — the
`pipeline/stats/sqlite_updaters.py` uses the delta path for the hot
write path, falling back to full recompute on any inconsistency.

## 4. Code scaffolding

### 4.1 Go handler

```go
// api/internal/handlers/libraries/stats.go
func StatsHandler(d *handlers.Deps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()
        id, err := uuid.Parse(chi.URLParam(r, "id"))
        if err != nil {
            handlers.WriteError(w, 400, "bad-library-id", "")
            return
        }
        row, err := d.Queries.GetLibraryStats(ctx, id)
        if errors.Is(err, pgx.ErrNoRows) {
            handlers.WriteError(w, 404, "library-not-found", "")
            return
        }
        resp := buildResponse(row)
        handlers.WriteJSON(w, 200, resp)
    }
}

func buildResponse(row db.LibraryStatsCacheRow) StatsResponse {
    var byState, byLang, byCT map[string]int
    _ = json.Unmarshal(row.ByStateJsonb, &byState)
    _ = json.Unmarshal(row.ByLanguageJsonb, &byLang)
    _ = json.Unmarshal(row.ByContentTypeJsonb, &byCT)

    var jobs JobsStats
    _ = json.Unmarshal(row.JobsJsonb, &jobs)

    var lastSweep *LastSweep
    if len(row.LastSweepJsonb) > 0 && string(row.LastSweepJsonb) != "null" {
        ls := LastSweep{}
        _ = json.Unmarshal(row.LastSweepJsonb, &ls)
        lastSweep = &ls
    }

    var processedPct *float64
    den := row.TotalVideos -
        byState["SUPERSEDED"] - byState["MISSING"]
    if den > 0 {
        n := byState["READY"] + byState["READY_NO_AUDIO"]
        v := math.Round(float64(n)/float64(den)*1e4) / 1e2  // 2-dp
        processedPct = &v
    }

    return StatsResponse{
        TotalVideos:      row.TotalVideos,
        TotalDurationSec: row.TotalDurationSec,
        ByState:          byState,
        ProcessedPct:     processedPct,
        ByLanguage:       byLang,
        ByContentType:    byCT,
        Storage: StorageStats{
            SourceSizeBytes:  row.SourceSizeBytes,
            DerivedSizeBytes: row.DerivedSizeBytes,
        },
        Jobs:      jobs,
        LastSweep: lastSweep,
    }
}
```

### 4.2 SQL — `GetLibraryStats`

```sql
-- name: GetLibraryStats :one
SELECT total_videos, total_duration_sec, source_size_bytes,
       derived_size_bytes, by_state_jsonb, by_language_jsonb,
       by_content_type_jsonb, jobs_jsonb, last_sweep_jsonb, updated_at
  FROM library_stats_cache
 WHERE library_id = $1;
```

### 4.3 `stats-rebuild` CLI

```go
// api/cmd/maktaba-api/stats_rebuild.go
type DriftRow struct {
    LibraryID uuid.UUID
    Field     string
    Cached    int64
    Actual    int64
}

func RebuildAllStats(ctx context.Context, q *db.Queries) ([]DriftRow, error) {
    libs, _ := q.ListLibraries(ctx)
    var drifts []DriftRow
    for _, l := range libs {
        before, _ := q.GetLibraryStats(ctx, l.ID)
        if err := q.RebuildLibraryStats(ctx, l.ID); err != nil {
            return nil, err
        }
        after, _ := q.GetLibraryStats(ctx, l.ID)
        if before.TotalVideos != after.TotalVideos {
            drifts = append(drifts, DriftRow{
                LibraryID: l.ID, Field: "total_videos",
                Cached: int64(before.TotalVideos), Actual: int64(after.TotalVideos),
            })
        }
        // ...same for the other columns
    }
    if len(drifts) > 0 {
        statsCorruptionTotal.WithLabelValues().Add(float64(len(drifts)))
    }
    return drifts, nil
}
```

The `RebuildLibraryStats` query is one big idempotent UPDATE
recomputing every column from `videos`, `processing_jobs`, the
sweep telemetry, and the derived-size scan (a sub-SELECT against
`transcripts` + `subtitle_files` + `media_features`). Concrete SQL
is several hundred lines; intentionally not inlined here, but the
test suite (§5.3) is the contract.

## 5. Test plan

### 5.1 Trigger correctness (`test_cache_triggers.py`)

| Test | What it pins |
|---|---|
| `test_insert_video_increments_counts` | New video → `total_videos` += 1; `by_state.DISCOVERED` += 1; `source_size_bytes` += size; `by_language` and `by_content_type` reflect inserted values (or stay empty for NULL). |
| `test_update_state_moves_bucket` | Video state DISCOVERED → PROBED → READY: `by_state` reflects the moves; bucket counts go to zero and are removed (no "PROBED": 0 entry). |
| `test_update_language_moves_bucket` | `detected_language` NULL → 'ar': `by_language.ar = 1`; later 'ar' → 'en': `by_language.ar = 0` (removed), `by_language.en = 1`. |
| `test_update_content_type_moves_bucket` | Same, for `content_type`. |
| `test_delete_video_decrements_counts` | DELETE → counts go down; state bucket bottoms out and is removed. |
| `test_size_change_propagates` | UPDATE `size` from 100 → 200 → `source_size_bytes` += 100. |
| `test_duration_change_propagates` | UPDATE `duration_sec` → `total_duration_sec` reflects the delta. |
| `test_processing_jobs_state_transitions` | INSERT pending → `jobs.pending = 1`; pending → running → `jobs.pending = 0, jobs.running = 1`; → done → both 0. |
| `test_library_sweep_finalize_writes_last_sweep` | UPDATE `library_sweeps.finished_at` from NULL → now() → `last_sweep_jsonb` populated. A non-finalize UPDATE (e.g., bumping `scanned` mid-run) does *not* write `last_sweep_jsonb`. |

### 5.2 Handler tests (`stats_test.go`)

| Test | What it pins |
|---|---|
| `TestStatsHandler_404OnUnknownLibrary` | Unknown UUID → 404. |
| `TestStatsHandler_EmptyLibrary_NullProcessedPct` | New library; cache row exists but counts are 0 → response.ProcessedPct == nil; LastSweep == nil. AC-3. |
| `TestStatsHandler_RoundsProcessedPctToTwoDecimals` | 1234 READY of 5678 → "21.73". |
| `TestStatsHandler_BucketsAddUp` | Cache row's `by_state` values sum to `total_videos`. (The trigger maintains this; the test reproves the read-side honors it.) |
| `TestStatsHandler_50KVideoLibrary_Under50ms` | Synthesize a 50k-row cache (no real videos); the GET endpoint serves in < 50 ms. AC-2. |
| `TestStatsHandler_LastSweepNullWhenNoSweep` | No sweep yet → `last_sweep == null`. |

### 5.3 Reconciliation tests (`test_rebuild.py`)

| Test | What it pins |
|---|---|
| `test_rebuild_corrects_drift` | Hand-edit a cache row to an obviously wrong value; run `stats-rebuild`; assert correct value restored; assert `DriftRow` returned. |
| `test_rebuild_idempotent` | Run `stats-rebuild` twice in a row — second run reports zero drift. |
| `test_rebuild_metric_increments_on_drift` | Cause drift; run rebuild; `maktaba_stats_cache_corruption_total` increments by the count of drifted columns. |
| `test_rebuild_during_writes` | Rebuild while another tx is mid-INSERT into `videos`: rebuild snapshot is consistent (uses repeatable-read isolation). |

### 5.4 Cross-dialect parity

Every test in §5.1 is parametrized for SQLite, where the Python-side
delta updaters take the place of the trigger function. The
"recompute" safety-net SQLite triggers fire on every write; the test
asserts both paths agree after each operation.

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Bulk INSERT 10k videos in one tx | Trigger fires per row → 10k `lsc_apply_video_delta` calls inside the tx. The cache row gets 10k UPDATEs but they coalesce because Postgres emits one final visible state at commit. Performance is dominated by the lsc UPDATEs, not the per-row work. The reconciliation job catches any drift; the live query stays fast (single-row read). | `test_bulk_insert_consistent` |
| Library deleted while stats requested | The cache row is FK-cascaded; `GetLibraryStats` returns ErrNoRows → 404. | `test_library_deleted_while_request_inflight` (handler) |
| Empty library (no videos) | Cache row exists with all zeros; ProcessedPct = null. | `TestStatsHandler_EmptyLibrary_NullProcessedPct` |
| Triggers disabled (e.g., in a recovery script) | Reconciliation is the backstop; nightly job restores correctness. | `test_rebuild_corrects_drift` |
| Drift between cache and actual | `stats-rebuild` detects and reports; metric fires. | §5.3 |
| Two-decimal rounding edge | `1/3` → "33.33"; banker's rounding deferred to Go's `math.Round` of `value*100` then divide; matches Python's `round(value, 2)` for our cases. | `test_processed_pct_rounds_consistently` |

## 7. Performance gates

| Test | Target |
|---|---|
| `BenchmarkStatsHandler_GetLibraryStats` | p99 < 50 ms on a synthesized 50k-row cache. AC-2. |
| `BenchmarkVideosTriggerOverhead` | INSERT throughput on `videos` is at most 20 % slower with triggers enabled vs disabled. |

## 8. Dependencies

| Dep | Source | Why |
|---|---|---|
| `videos` table state CHECK | Story 9.6 migration | Trigger relies on the canonical state list. |
| `processing_jobs` table | Story 6.1 | Trigger reads its `state` enum. |
| `library_sweeps` table | Story 9.3 | `last_sweep` source. |

## 9. Acceptance checklist

**Schema**
- [ ] `library_stats_cache` exists with all columns from the README.
- [ ] One row per existing library on migration apply.
- [ ] Three triggers wired (`videos`, `processing_jobs`, `library_sweeps`).

**Behaviour (story acceptance criteria)**
- [ ] AC-1: response shape matches §1 verbatim (every key in the story).
- [ ] AC-2: 50k-video library responds in < 50 ms; the read is single-row.
- [ ] AC-3: empty library returns zeros and `processed_pct == null`.

**Reliability**
- [ ] `stats-rebuild` recomputes every column from source tables and reports drift.
- [ ] `maktaba_stats_cache_corruption_total` metric exposed.

**Observability**
- [ ] Histogram `maktaba_stats_handler_duration_seconds`.
- [ ] Counter `maktaba_stats_cache_updates_total{trigger}` (one per trigger function).

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.7.
- [ ] API reference documents the response shape, the rounding rule, and the null-on-empty contract.
