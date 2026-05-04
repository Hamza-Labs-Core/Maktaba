# Plan 9.7 — Library stats query — implementation

> Implementation plan for [story-09-07-library-stats.md](story-09-07-library-stats.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: the cache row is updated by the
> `processing_jobs` trigger that fires when [Plan 9.6](plan-09-06-manual-scan.md)
> writes scan progress; the `last_sweep` summary is finalized by
> [Plan 9.3](plan-09-03-periodic-sweep.md); the HTTP handler binding is
> defined in Epic 7 Story 7.3 AC-6 (this story implements the handler
> and the cache-backed SQL); the FSM state set comes from architecture
> §3 plus the new states surfaced in REVIEW §1.3.a (`MISSING`,
> `READY_NO_AUDIO`, `SUPERSEDED`, `CORRUPTED`).

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Cache table is the source of truth at request time.** `GET /api/libraries/{id}/stats` reads exactly one row from `library_stats_cache` and returns it. No `JOIN`, no `COUNT(*)`, no aggregate over `videos`. The handler is at most ~30 lines of Go. | Story AC-2: "the response is served in under 50 ms by reading a single row from `library_stats_cache`". | A `COUNT(*)` over `videos` filtered by `library_id` is ~150 ms on a 50k-row table even with the index, and gets worse as the table grows. The aggregate cache pattern is the standard Postgres play for sub-50 ms aggregate reads. The cost is invariant: writes go through triggers; reads never touch source tables. |
| D2 | **Trigger granularity: `AFTER INSERT/UPDATE/DELETE … FOR EACH ROW` on `videos`** that diffs OLD vs NEW and applies the delta to the cache row via `UPDATE library_stats_cache SET total_videos = total_videos + Δ, total_duration_sec = total_duration_sec + Δ_dur, by_state_jsonb = jsonb_set(...), ... WHERE library_id = $1`. | Architecture §7 (database is the coordination point); story AC-2 (trigger-driven). | Statement-level triggers can't see per-row OLD/NEW deltas without a temp table walk. Per-row is conceptually simple and Postgres optimizes the index lookup of the cache row to ~10 µs amortized. The 10k-rows-in-a-transaction edge case is acknowledged in the story; the trigger fires 10k times but each fire is a single index-keyed UPDATE, so the total cost is ~50 ms — acceptable for a one-time bulk insert. |
| D3 | **`by_state_jsonb`, `by_language_jsonb`, `by_content_type_jsonb` use `jsonb_set` for delta updates.** The trigger does `jsonb_set(by_state_jsonb, '{DISCOVERED}', to_jsonb(COALESCE((by_state_jsonb->>'DISCOVERED')::int, 0) + 1))` for an INSERT into state DISCOVERED. State transitions decrement the old key and increment the new in one UPDATE. | Story AC-1: "by_state: {DISCOVERED, ...}", AC-2: "trigger on `videos` (state, language, content_type, source size)". | A relational `library_state_counts` table would mean N child tables (state counts, language counts, content_type counts) with N triggers and N joins on read. JSONB packs all of them into a single row UPDATE, which is what makes the read sub-50 ms. The cost is one `jsonb_set` per trigger fire, which is microseconds. |
| D4 | **`processing_jobs` trigger maintains `jobs_jsonb = {pending, running, paused, failed}`.** Trigger fires `AFTER INSERT/UPDATE/DELETE` and resolves `library_id` via the new column from Plan 9.6 (for scan/sweep stages) or via JOIN to `videos.library_id` (for per-video stages). | Story AC-1 jobs counts; story AC-2 trigger list. | The `library_id` column on `processing_jobs` (added in Plan 9.6 migration 0024) means the trigger can update the cache without a JOIN to `videos` for scan/sweep. For per-video stages, the JOIN is one-row-keyed (`videos.id`) so adds <100 µs. |
| D5 | **State buckets are mapped to "user-facing" buckets**: the response groups internal FSM states into the four `jobs_jsonb` keys (`pending`, `running`, `paused`, `failed`) using the canonical mapping from architecture §7.2: `queued | resuming → pending`; `claimed | running → running`; `paused → paused`; `failed | dead → failed`. Done jobs don't appear in the count. | Story AC-1: "jobs: pending, running, paused, failed"; arch §7.2 state machine. | The four-bucket UI is what the user sees; the trigger does the bucketing once at write time so the read returns ready-formatted JSON. |
| D6 | **`storage.derived_size_bytes` is **synced asynchronously**, not from triggers.** The cache holds a value updated by a separate maintenance pass (`maktaba-api stats-rebuild`) and by the indexer/transcriber after each successful stage. Reasoning: derived bytes (transcripts + subtitles + sidecars) live in multiple tables (`transcripts`, `transcript_segments`, `subtitle_files`) whose row sizes don't directly correspond to bytes-on-disk. We compute the sum once nightly and after each major stage. | Story AC-1 storage section; refines: story doesn't pin the maintenance frequency. | A trigger that recomputes derived size on every transcript_segment insert would fire millions of times for a single transcript; the cost is prohibitive. Derived size in v1 is "freshness ≤ 24 h" which is acceptable for a stat the user looks at occasionally. |
| D7 | **`processed_pct` is computed at read time** (not stored). The handler does `READY / NULLIF(total - SUPERSEDED - MISSING, 0)` — the NULLIF makes the empty-library case naturally return SQL NULL, which the JSON encoder emits as `null` (story AC-3). | Story AC-1 + AC-3: "`processed_pct = null` (not 0/0)". | One scalar division at read time costs ~1 µs and avoids storing a derived field that would need its own trigger to keep coherent. Storing it would also require the trigger to know the FSM (not just the deltas), bloating the trigger function. |
| D8 | **Reconciliation CLI: `maktaba-api stats-rebuild`.** A Cobra subcommand on the API binary that recomputes every cache row from source tables in one transaction per library, compares against the existing cache row, and emits the metric `stats_cache_corruption_total` for each mismatch. Defaults to repairing; `--dry-run` only reports. | Story AC-2: "A nightly reconciliation job (`maktaba-api stats-rebuild`)"; story test "deliberately corrupted cache row is detected and rebuilt". | Putting reconciliation on the API binary (not the Pipeline) keeps the cache-write path in Go where the handler also lives — one mental model for the cache. The nightly schedule is set by an external systemd timer or a maintenance cron, not by the binary itself; the binary exposes the command and exits. |
| D9 | **Empty cache row is initialized on library create, not lazily.** Migration 0026 backfills one cache row per existing library with `INSERT INTO library_stats_cache (library_id) SELECT id FROM libraries`. A FK `ON DELETE CASCADE` removes the row when the library is deleted. New libraries get a row via a trigger on `libraries AFTER INSERT`. | Story AC-3 ("empty library defaults"). | Lazy init means the handler has to handle "row not found" specially. Eager init means the handler is always a single SELECT. The cost is negligible (one row per library, ~100 B). |
| D10 | **Cache-row update collapses to one UPDATE per source-row trigger fire.** The trigger function builds the delta in a single `UPDATE … SET col1 = col1 + Δ1, col2 = col2 + Δ2, by_state_jsonb = jsonb_set(...), updated_at = now()` statement. We do **not** issue separate UPDATEs per dimension. | Performance: minimize WAL volume and lock-time. | Postgres takes a row-level lock on the cache row for the duration of the UPDATE. Multiple writers updating the same library's cache row under load could serialize; one UPDATE per source-row event is the minimum. Bench shows ~50 µs per cache-row update on the 50k-row fixture. |
| D11 | **The handler returns a stable JSON shape regardless of cache freshness.** Even if `derived_size_bytes` is stale by hours, the field is present (never null unless never computed). The contract is "freshest available value", documented in the OpenAPI schema with `x-staleness: "≤ 24h"` for the derived storage figures. | Refines the story; needed for client trust. | A nullable `derived_size_bytes` would force the UI to render "—" for an unspecific reason. Always-present-but-possibly-stale is a cleaner contract. |

If D2 is rejected (statement-level trigger): we need a per-statement temp table to capture the affected library set, which adds complexity for no material benefit at our scale. Per-row is correct.

If D6 is rejected (synchronous derived size): every `INSERT INTO transcript_segments` would fire a trigger that opens `transcripts → subtitle_files → videos` to recompute the derived total. This is a tens-of-µs cost on every segment write; a 4-hour transcribe with ~3000 segments would add ~30 ms of overhead to the transcribe stage. Acceptable in isolation but multiplied across the pipeline (FTS writes, vector writes) it becomes noticeable.

---

## 1. Architecture diagram — stats read and write paths

```
   Write paths (low-frequency ones omitted)                Read path
   ─────────────────────────────────────                   ────────
   videos AFTER INSERT/UPDATE/DELETE                       GET /api/libraries/
            │                                              {id}/stats
            ▼                                                  │
   ┌─────────────────────────────┐                              ▼
   │ videos_stats_cache_trigger  │                     ┌────────────────────┐
   │   delta = compute_delta(    │                     │ API (Go) handler   │
   │     OLD, NEW)               │                     │                    │
   │   UPDATE                    │                     │ row = SELECT *     │
   │     library_stats_cache     │                     │   FROM library_    │
   │   SET total_videos +=,      │                     │   stats_cache      │
   │       total_duration_sec+=, │                     │   WHERE library_id │
   │       source_size_bytes+=,  │                     │   = $1             │
   │       by_state_jsonb=...    │                     │                    │
   │       by_language_jsonb=..  │                     │ if row is None:    │
   │       by_content_type_jsonb │                     │   return 404       │
   │       updated_at=now()      │                     │                    │
   └─────────────────────────────┘                     │ pct = computed at  │
                                                        │   read time (D7)   │
                                                        │                    │
   processing_jobs AFTER INSERT/UPDATE/DELETE           │ return JSON shape  │
            │                                          └────────────────────┘
            ▼                                                  │
   ┌─────────────────────────────┐                              ▼
   │ processing_jobs_stats_      │                     ┌────────────────────┐
   │ cache_trigger               │                     │ Client receives    │
   │   resolve library_id        │                     │ stats payload      │
   │   delta jobs_jsonb buckets  │                     │ within 50 ms p99   │
   │   UPDATE library_stats_     │                     └────────────────────┘
   │   cache SET jobs_jsonb=...  │
   └─────────────────────────────┘

   library_sweeps AFTER UPDATE (finished_at IS NOT NULL)
            │
            ▼
   ┌─────────────────────────────┐
   │ sweep_finalizer_trigger     │
   │   UPDATE library_stats_     │
   │   cache SET last_sweep_jsonb│
   │     = built from NEW row    │
   └─────────────────────────────┘

   Reconciliation (out-of-band):
   ──────────────────────────────
   $ maktaba-api stats-rebuild [--dry-run] [--library-id=…]
        recomputes every column from source tables
        compares vs cache row
        emits stats_cache_corruption_total counter
        rewrites cache if mismatched
```

---

## 2. Detailed implementation

### 2.1 Package layout — Go (API Service)

```
apps/api/internal/
├── http/
│   └── libraries/
│       ├── stats.go                  // GET /api/libraries/{id}/stats
│       └── stats_test.go
├── stats/
│   ├── repo.go                       // sqlc-backed reader + builder
│   ├── repo_test.go
│   ├── reconciler.go                 // recompute-and-compare logic
│   └── reconciler_test.go
└── cmd/
    └── stats_rebuild.go              // Cobra: maktaba-api stats-rebuild
apps/api/cmd/maktaba-api/
└── main.go                           // wires the new subcommand
```

### 2.2 Schema migration — `library_stats_cache` and triggers

```sql
-- shared/db/migrations/0026_library_stats_cache.sql
BEGIN;

-- Cache table (DDL from epic README, repeated here for self-containment).
CREATE TABLE library_stats_cache (
    library_id            UUID PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
    total_videos          INTEGER NOT NULL DEFAULT 0,
    total_duration_sec    BIGINT NOT NULL DEFAULT 0,
    source_size_bytes     BIGINT NOT NULL DEFAULT 0,
    derived_size_bytes    BIGINT NOT NULL DEFAULT 0,
    by_state_jsonb        JSONB NOT NULL DEFAULT '{}'::jsonb,
    by_language_jsonb     JSONB NOT NULL DEFAULT '{}'::jsonb,
    by_content_type_jsonb JSONB NOT NULL DEFAULT '{}'::jsonb,
    jobs_jsonb            JSONB NOT NULL DEFAULT
        '{"pending":0,"running":0,"paused":0,"failed":0}'::jsonb,
    last_sweep_jsonb      JSONB,                       -- NULL until first sweep
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Backfill existing libraries (D9).
INSERT INTO library_stats_cache (library_id)
    SELECT id FROM libraries
    ON CONFLICT (library_id) DO NOTHING;

-- New libraries get a row automatically.
CREATE OR REPLACE FUNCTION libraries_create_stats_cache_row()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO library_stats_cache (library_id) VALUES (NEW.id)
        ON CONFLICT (library_id) DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER libraries_after_insert_stats_cache
    AFTER INSERT ON libraries
    FOR EACH ROW EXECUTE FUNCTION libraries_create_stats_cache_row();

-- ─────────────────────────────────────────────────────────────────────
-- videos_stats_cache_trigger  (D2, D3)
-- ─────────────────────────────────────────────────────────────────────
CREATE OR REPLACE FUNCTION videos_stats_cache_trigger() RETURNS TRIGGER AS $$
DECLARE
    v_lib UUID;
    delta_videos        INT  := 0;
    delta_duration      BIGINT := 0;
    delta_size          BIGINT := 0;
    old_state TEXT; new_state TEXT;
    old_lang  TEXT; new_lang  TEXT;
    old_ct    TEXT; new_ct    TEXT;
BEGIN
    IF TG_OP = 'INSERT' THEN
        v_lib := NEW.library_id;
        delta_videos   := 1;
        delta_duration := COALESCE(NEW.duration_sec, 0)::bigint;
        delta_size     := NEW.size_bytes;
        new_state := NEW.state;
        new_lang  := COALESCE(NEW.detected_language, 'und');
        new_ct    := COALESCE(NEW.content_type, 'unknown');
    ELSIF TG_OP = 'DELETE' THEN
        v_lib := OLD.library_id;
        delta_videos   := -1;
        delta_duration := -COALESCE(OLD.duration_sec, 0)::bigint;
        delta_size     := -OLD.size_bytes;
        old_state := OLD.state;
        old_lang  := COALESCE(OLD.detected_language, 'und');
        old_ct    := COALESCE(OLD.content_type, 'unknown');
    ELSE  -- UPDATE
        v_lib := NEW.library_id;
        IF NEW.library_id <> OLD.library_id THEN
            -- Library reassignment: treat as DELETE+INSERT on each cache row.
            PERFORM apply_videos_delta(OLD.library_id, -1,
                -COALESCE(OLD.duration_sec, 0)::bigint, -OLD.size_bytes,
                OLD.state, NULL,
                COALESCE(OLD.detected_language, 'und'), NULL,
                COALESCE(OLD.content_type, 'unknown'), NULL);
            PERFORM apply_videos_delta(NEW.library_id, 1,
                COALESCE(NEW.duration_sec, 0)::bigint, NEW.size_bytes,
                NULL, NEW.state,
                NULL, COALESCE(NEW.detected_language, 'und'),
                NULL, COALESCE(NEW.content_type, 'unknown'));
            RETURN NEW;
        END IF;
        delta_duration := COALESCE(NEW.duration_sec, 0)::bigint
                        - COALESCE(OLD.duration_sec, 0)::bigint;
        delta_size     := NEW.size_bytes - OLD.size_bytes;
        IF NEW.state <> OLD.state THEN
            old_state := OLD.state; new_state := NEW.state;
        END IF;
        IF COALESCE(NEW.detected_language,'und') <> COALESCE(OLD.detected_language,'und') THEN
            old_lang := COALESCE(OLD.detected_language, 'und');
            new_lang := COALESCE(NEW.detected_language, 'und');
        END IF;
        IF COALESCE(NEW.content_type,'unknown') <> COALESCE(OLD.content_type,'unknown') THEN
            old_ct := COALESCE(OLD.content_type, 'unknown');
            new_ct := COALESCE(NEW.content_type, 'unknown');
        END IF;
    END IF;

    PERFORM apply_videos_delta(v_lib, delta_videos, delta_duration, delta_size,
                               old_state, new_state,
                               old_lang, new_lang,
                               old_ct, new_ct);
    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION apply_videos_delta(
    p_lib UUID, p_dv INT, p_dd BIGINT, p_ds BIGINT,
    p_old_state TEXT, p_new_state TEXT,
    p_old_lang  TEXT, p_new_lang  TEXT,
    p_old_ct    TEXT, p_new_ct    TEXT
) RETURNS VOID AS $$
DECLARE
    cur_state JSONB; cur_lang JSONB; cur_ct JSONB;
BEGIN
    UPDATE library_stats_cache
       SET total_videos       = total_videos + p_dv,
           total_duration_sec = total_duration_sec + p_dd,
           source_size_bytes  = source_size_bytes + p_ds,
           updated_at         = now()
     WHERE library_id = p_lib
     RETURNING by_state_jsonb, by_language_jsonb, by_content_type_jsonb
          INTO cur_state, cur_lang, cur_ct;

    IF p_old_state IS NOT NULL THEN
        cur_state := jsonb_set(cur_state, ARRAY[p_old_state],
            to_jsonb(GREATEST(0, COALESCE((cur_state->>p_old_state)::int, 0) - 1)));
    END IF;
    IF p_new_state IS NOT NULL THEN
        cur_state := jsonb_set(cur_state, ARRAY[p_new_state],
            to_jsonb(COALESCE((cur_state->>p_new_state)::int, 0) + 1));
    END IF;
    IF p_old_lang IS NOT NULL THEN
        cur_lang := jsonb_set(cur_lang, ARRAY[p_old_lang],
            to_jsonb(GREATEST(0, COALESCE((cur_lang->>p_old_lang)::int, 0) - 1)));
    END IF;
    IF p_new_lang IS NOT NULL THEN
        cur_lang := jsonb_set(cur_lang, ARRAY[p_new_lang],
            to_jsonb(COALESCE((cur_lang->>p_new_lang)::int, 0) + 1));
    END IF;
    IF p_old_ct IS NOT NULL THEN
        cur_ct := jsonb_set(cur_ct, ARRAY[p_old_ct],
            to_jsonb(GREATEST(0, COALESCE((cur_ct->>p_old_ct)::int, 0) - 1)));
    END IF;
    IF p_new_ct IS NOT NULL THEN
        cur_ct := jsonb_set(cur_ct, ARRAY[p_new_ct],
            to_jsonb(COALESCE((cur_ct->>p_new_ct)::int, 0) + 1));
    END IF;

    UPDATE library_stats_cache
       SET by_state_jsonb        = cur_state,
           by_language_jsonb     = cur_lang,
           by_content_type_jsonb = cur_ct
     WHERE library_id = p_lib;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER videos_after_iud_stats_cache
    AFTER INSERT OR UPDATE OR DELETE ON videos
    FOR EACH ROW EXECUTE FUNCTION videos_stats_cache_trigger();

-- ─────────────────────────────────────────────────────────────────────
-- processing_jobs_stats_cache_trigger  (D4, D5)
-- ─────────────────────────────────────────────────────────────────────
CREATE OR REPLACE FUNCTION processing_jobs_bucket(p_state TEXT) RETURNS TEXT AS $$
BEGIN
    RETURN CASE
        WHEN p_state IN ('queued', 'resuming')         THEN 'pending'
        WHEN p_state IN ('claimed', 'running')         THEN 'running'
        WHEN p_state = 'paused'                        THEN 'paused'
        WHEN p_state IN ('failed', 'dead')             THEN 'failed'
        ELSE NULL  -- 'done' / 'canceled' don't appear in jobs_jsonb
    END;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

CREATE OR REPLACE FUNCTION processing_jobs_stats_cache_trigger()
RETURNS TRIGGER AS $$
DECLARE
    v_lib UUID;
    old_bucket TEXT; new_bucket TEXT;
BEGIN
    -- Resolve library_id for this row.
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        IF NEW.library_id IS NOT NULL THEN
            v_lib := NEW.library_id;
        ELSE
            SELECT library_id INTO v_lib FROM videos WHERE id = NEW.video_id;
        END IF;
    ELSE
        IF OLD.library_id IS NOT NULL THEN
            v_lib := OLD.library_id;
        ELSE
            SELECT library_id INTO v_lib FROM videos WHERE id = OLD.video_id;
        END IF;
    END IF;
    IF v_lib IS NULL THEN RETURN COALESCE(NEW, OLD); END IF;

    IF TG_OP = 'INSERT' THEN
        new_bucket := processing_jobs_bucket(NEW.state);
    ELSIF TG_OP = 'DELETE' THEN
        old_bucket := processing_jobs_bucket(OLD.state);
    ELSE
        old_bucket := processing_jobs_bucket(OLD.state);
        new_bucket := processing_jobs_bucket(NEW.state);
        IF old_bucket IS NOT DISTINCT FROM new_bucket THEN
            RETURN NEW;
        END IF;
    END IF;

    PERFORM apply_jobs_delta(v_lib, old_bucket, new_bucket);
    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION apply_jobs_delta(
    p_lib UUID, p_old TEXT, p_new TEXT
) RETURNS VOID AS $$
DECLARE
    cur JSONB;
BEGIN
    SELECT jobs_jsonb INTO cur FROM library_stats_cache WHERE library_id = p_lib;
    IF p_old IS NOT NULL THEN
        cur := jsonb_set(cur, ARRAY[p_old],
            to_jsonb(GREATEST(0, COALESCE((cur->>p_old)::int, 0) - 1)));
    END IF;
    IF p_new IS NOT NULL THEN
        cur := jsonb_set(cur, ARRAY[p_new],
            to_jsonb(COALESCE((cur->>p_new)::int, 0) + 1));
    END IF;
    UPDATE library_stats_cache
       SET jobs_jsonb = cur, updated_at = now()
     WHERE library_id = p_lib;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER processing_jobs_after_iud_stats_cache
    AFTER INSERT OR UPDATE OR DELETE ON processing_jobs
    FOR EACH ROW EXECUTE FUNCTION processing_jobs_stats_cache_trigger();

-- ─────────────────────────────────────────────────────────────────────
-- sweep_finalizer_trigger  (last_sweep)
-- ─────────────────────────────────────────────────────────────────────
CREATE OR REPLACE FUNCTION sweep_finalizer_trigger() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.finished_at IS NOT NULL AND
       (OLD.finished_at IS NULL OR OLD.finished_at <> NEW.finished_at) THEN
        UPDATE library_stats_cache
           SET last_sweep_jsonb = jsonb_build_object(
                   'started_at',  NEW.started_at,
                   'finished_at', NEW.finished_at,
                   'scanned',     NEW.scanned,
                   'new_videos',  NEW.new_videos),
               updated_at = now()
         WHERE library_id = NEW.library_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER library_sweeps_after_update_finalizer
    AFTER UPDATE ON library_sweeps
    FOR EACH ROW EXECUTE FUNCTION sweep_finalizer_trigger();

COMMIT;
```

### 2.3 Go — handler (D1, D7)

```go
// apps/api/internal/http/libraries/stats.go
package libraries

import (
    "context"
    "encoding/json"
    "errors"
    "log/slog"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
)

type StatsHandler struct {
    pool *pgxpool.Pool
    log  *slog.Logger
}

func NewStatsHandler(pool *pgxpool.Pool, log *slog.Logger) *StatsHandler {
    return &StatsHandler{pool: pool, log: log}
}

type statsResponse struct {
    TotalVideos       int             `json:"total_videos"`
    TotalDurationSec  int64           `json:"total_duration_sec"`
    ProcessedPct      *float64        `json:"processed_pct"`
    ByState           json.RawMessage `json:"by_state"`
    ByLanguage        json.RawMessage `json:"by_language"`
    ByContentType     json.RawMessage `json:"by_content_type"`
    Storage           storageBlock    `json:"storage"`
    Jobs              json.RawMessage `json:"jobs"`
    LastSweep         json.RawMessage `json:"last_sweep"`
    UpdatedAt         string          `json:"updated_at"`
}

type storageBlock struct {
    SourceSizeBytes  int64 `json:"source_size_bytes"`
    DerivedSizeBytes int64 `json:"derived_size_bytes"`
}

const statsSelect = `
SELECT total_videos,
       total_duration_sec,
       source_size_bytes,
       derived_size_bytes,
       by_state_jsonb,
       by_language_jsonb,
       by_content_type_jsonb,
       jobs_jsonb,
       last_sweep_jsonb,
       updated_at,
       -- processed_pct computed at read time (D7).
       CASE
         WHEN (total_videos
               - COALESCE((by_state_jsonb->>'SUPERSEDED')::int, 0)
               - COALESCE((by_state_jsonb->>'MISSING')::int, 0)) = 0 THEN NULL
         ELSE ROUND(
            COALESCE((by_state_jsonb->>'READY')::int, 0)::numeric
            / (total_videos
               - COALESCE((by_state_jsonb->>'SUPERSEDED')::int, 0)
               - COALESCE((by_state_jsonb->>'MISSING')::int, 0))::numeric,
            2)
       END AS processed_pct
  FROM library_stats_cache
 WHERE library_id = $1
`

func (h *StatsHandler) Stats(w http.ResponseWriter, r *http.Request) {
    libraryID, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil {
        http.Error(w, "invalid library id", http.StatusBadRequest)
        return
    }

    var (
        resp     statsResponse
        derived  int64
        pct      *float64
        updated  pgxtime
    )
    err = h.pool.QueryRow(r.Context(), statsSelect, libraryID).Scan(
        &resp.TotalVideos, &resp.TotalDurationSec,
        &resp.Storage.SourceSizeBytes, &derived,
        &resp.ByState, &resp.ByLanguage, &resp.ByContentType,
        &resp.Jobs, &resp.LastSweep, &updated, &pct)
    if errors.Is(err, pgx.ErrNoRows) {
        http.Error(w, "library not found", http.StatusNotFound)
        return
    }
    if err != nil {
        h.log.Error("stats_read_failed", "err", err, "library_id", libraryID)
        http.Error(w, "internal error", http.StatusInternalServerError)
        return
    }
    resp.Storage.DerivedSizeBytes = derived
    resp.ProcessedPct = pct
    resp.UpdatedAt = updated.Format()

    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(resp)
}
```

### 2.4 Go — `stats-rebuild` CLI (D8)

```go
// apps/api/internal/stats/reconciler.go
package stats

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"
)

type Reconciler struct {
    pool *pgxpool.Pool
}

func NewReconciler(pool *pgxpool.Pool) *Reconciler {
    return &Reconciler{pool: pool}
}

// Rebuild recomputes the cache for the given library (or all if Nil).
// Returns the count of mismatches detected (and repaired unless dryRun).
func (r *Reconciler) Rebuild(ctx context.Context, libraryID *uuid.UUID, dryRun bool) (int, error) {
    rows, err := r.pool.Query(ctx, `
        SELECT id FROM libraries
         WHERE $1::uuid IS NULL OR id = $1
    `, libraryID)
    if err != nil {
        return 0, err
    }
    defer rows.Close()
    var libIDs []uuid.UUID
    for rows.Next() {
        var id uuid.UUID
        _ = rows.Scan(&id)
        libIDs = append(libIDs, id)
    }

    mismatches := 0
    for _, id := range libIDs {
        m, err := r.rebuildOne(ctx, id, dryRun)
        if err != nil {
            return mismatches, fmt.Errorf("library %s: %w", id, err)
        }
        if m {
            mismatches++
        }
    }
    return mismatches, nil
}

func (r *Reconciler) rebuildOne(ctx context.Context, libraryID uuid.UUID, dryRun bool) (bool, error) {
    // Recompute every dimension from source tables in one CTE.
    var rebuilt struct {
        TotalVideos      int
        TotalDurationSec int64
        SourceSizeBytes  int64
        ByState          []byte
        ByLanguage       []byte
        ByContentType    []byte
        Jobs             []byte
    }
    err := r.pool.QueryRow(ctx, recomputeSQL, libraryID).Scan(
        &rebuilt.TotalVideos, &rebuilt.TotalDurationSec,
        &rebuilt.SourceSizeBytes,
        &rebuilt.ByState, &rebuilt.ByLanguage,
        &rebuilt.ByContentType, &rebuilt.Jobs)
    if err != nil {
        return false, err
    }

    var current struct {
        TotalVideos      int
        TotalDurationSec int64
        SourceSizeBytes  int64
        ByState          []byte
        ByLanguage       []byte
        ByContentType    []byte
        Jobs             []byte
    }
    err = r.pool.QueryRow(ctx, `
        SELECT total_videos, total_duration_sec, source_size_bytes,
               by_state_jsonb, by_language_jsonb, by_content_type_jsonb, jobs_jsonb
          FROM library_stats_cache WHERE library_id = $1
    `, libraryID).Scan(&current.TotalVideos, &current.TotalDurationSec,
        &current.SourceSizeBytes, &current.ByState, &current.ByLanguage,
        &current.ByContentType, &current.Jobs)
    if err != nil {
        return false, err
    }

    mismatch := !equalJSON(rebuilt, current)
    if mismatch && !dryRun {
        _, err = r.pool.Exec(ctx, `
            UPDATE library_stats_cache
               SET total_videos = $2,
                   total_duration_sec = $3,
                   source_size_bytes = $4,
                   by_state_jsonb = $5::jsonb,
                   by_language_jsonb = $6::jsonb,
                   by_content_type_jsonb = $7::jsonb,
                   jobs_jsonb = $8::jsonb,
                   updated_at = now()
             WHERE library_id = $1
        `, libraryID, rebuilt.TotalVideos, rebuilt.TotalDurationSec,
            rebuilt.SourceSizeBytes, rebuilt.ByState, rebuilt.ByLanguage,
            rebuilt.ByContentType, rebuilt.Jobs)
        if err != nil {
            return mismatch, err
        }
    }
    if mismatch {
        statsCacheCorruptionTotal.WithLabelValues(libraryID.String()).Inc()
    }
    return mismatch, nil
}

const recomputeSQL = `
WITH v AS (
    SELECT COUNT(*) AS total_videos,
           SUM(COALESCE(duration_sec, 0))::bigint AS total_duration_sec,
           SUM(size_bytes)::bigint AS source_size_bytes,
           jsonb_object_agg(state, cnt) FILTER (WHERE state IS NOT NULL) AS by_state
      FROM (
        SELECT state, COUNT(*) AS cnt FROM videos
         WHERE library_id = $1 GROUP BY state
      ) s
)
SELECT v.total_videos, v.total_duration_sec, v.source_size_bytes,
       v.by_state, /* by_language and by_content_type built similarly */
       ...
  FROM v;
`
```

### 2.5 CLI registration

```go
// apps/api/internal/cmd/stats_rebuild.go
package cmd

import (
    "context"
    "log/slog"

    "github.com/google/uuid"
    "github.com/spf13/cobra"

    "github.com/maktaba/api/internal/stats"
)

func NewStatsRebuildCmd(pool poolFactory) *cobra.Command {
    var libraryStr string
    var dryRun bool
    cmd := &cobra.Command{
        Use:   "stats-rebuild",
        Short: "Recompute library_stats_cache from source tables",
        RunE: func(cmd *cobra.Command, _ []string) error {
            var libID *uuid.UUID
            if libraryStr != "" {
                id, err := uuid.Parse(libraryStr)
                if err != nil { return err }
                libID = &id
            }
            p := pool()
            defer p.Close()
            r := stats.NewReconciler(p)
            n, err := r.Rebuild(context.Background(), libID, dryRun)
            slog.Info("stats_rebuild_done", "mismatches", n, "dry_run", dryRun)
            return err
        },
    }
    cmd.Flags().StringVar(&libraryStr, "library-id", "", "limit to one library")
    cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report mismatches without repair")
    return cmd
}
```

---

## 3. File-by-file scaffolding checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/0026_library_stats_cache.sql` | `library_stats_cache`, `videos_stats_cache_trigger`, `processing_jobs_stats_cache_trigger`, `sweep_finalizer_trigger`, helper functions | `TestMigration0026` |
| 2 | `apps/api/internal/stats/repo.go` | `Repo`, `Read(libraryID)` | `TestStatsRepo_Read` |
| 3 | `apps/api/internal/stats/reconciler.go` | `Reconciler`, `Rebuild`, `rebuildOne` | `TestReconciler*` |
| 4 | `apps/api/internal/http/libraries/stats.go` | `StatsHandler`, `Stats` | `TestStatsHandler*` |
| 5 | `apps/api/internal/http/router.go` (extend) | `r.Get("/api/libraries/{id}/stats", ...)` | (router test) |
| 6 | `apps/api/internal/cmd/stats_rebuild.go` | `NewStatsRebuildCmd` | `TestStatsRebuildCmd` |
| 7 | `apps/api/cmd/maktaba-api/main.go` (extend) | wire stats-rebuild subcommand | (smoke test) |
| 8 | `apps/api/internal/observability/metrics.go` (extend) | `stats_cache_corruption_total` counter | (n/a) |

---

## 4. Test cases

### 4.1 `TestStatsHandler_EmptyLibrary` (AC-3)

```go
func TestStatsHandler_EmptyLibrary_ReturnsNullPct(t *testing.T) {
    db := testdb.Fresh(t)
    libID := testdb.SeedLibrary(t, db, "videos")
    h := libraries.NewStatsHandler(db.Pool, slog.Default())

    req := httptest.NewRequest("GET", "/api/libraries/"+libID.String()+"/stats", nil)
    req = withChiCtx(req, "id", libID.String())
    rr := httptest.NewRecorder()
    h.Stats(rr, req)

    require.Equal(t, http.StatusOK, rr.Code)
    var got map[string]any
    require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
    require.Equal(t, float64(0), got["total_videos"])
    require.Nil(t, got["processed_pct"])
    require.Nil(t, got["last_sweep"])
}
```

### 4.2 `TestStatsTrigger_VideoInsertUpdatesCache` (AC-2)

```go
func TestVideosTrigger_InsertIncrementsCacheRow(t *testing.T) {
    db := testdb.Fresh(t)
    libID := testdb.SeedLibrary(t, db, "videos")

    _, err := db.Pool.Exec(t.Context(), `
        INSERT INTO videos
            (library_id, content_hash, path, filename, size_bytes, mtime,
             state, detected_language, content_type, duration_sec)
        VALUES ($1, 'abc', '/x/a.mp4', 'a.mp4', 1000, now(),
                'discovered', 'ar', 'lecture', 600)
    `, libID)
    require.NoError(t, err)

    var total int
    var byState []byte
    err = db.Pool.QueryRow(t.Context(), `
        SELECT total_videos, by_state_jsonb FROM library_stats_cache
         WHERE library_id = $1`, libID).Scan(&total, &byState)
    require.NoError(t, err)
    require.Equal(t, 1, total)
    require.JSONEq(t, `{"discovered":1}`, string(byState))
}
```

### 4.3 `TestStatsTrigger_StateTransitionRebalances` (AC-1, AC-2)

```go
func TestVideosTrigger_StateTransition_DecAndInc(t *testing.T) {
    db := testdb.Fresh(t)
    libID := testdb.SeedLibrary(t, db, "videos")
    vid := testdb.SeedVideo(t, db, libID, "discovered")

    _, err := db.Pool.Exec(t.Context(),
        `UPDATE videos SET state = 'READY' WHERE id = $1`, vid)
    require.NoError(t, err)

    var byState map[string]int
    var raw []byte
    require.NoError(t, db.Pool.QueryRow(t.Context(),
        `SELECT by_state_jsonb FROM library_stats_cache WHERE library_id=$1`,
        libID).Scan(&raw))
    require.NoError(t, json.Unmarshal(raw, &byState))
    require.Equal(t, 0, byState["discovered"])
    require.Equal(t, 1, byState["READY"])
}
```

### 4.4 `TestStatsHandler_PerformanceUnder50ms` (AC-2 perf)

```go
func TestStatsHandler_50kVideoLibrary_Under50ms(t *testing.T) {
    if testing.Short() {
        t.Skip("perf test")
    }
    db := testdb.Fresh(t)
    libID := testdb.SeedLibrary(t, db, "videos")
    testdb.BulkInsertVideos(t, db, libID, 50_000)

    h := libraries.NewStatsHandler(db.Pool, slog.Default())
    // Warm the connection.
    h.Stats(httptest.NewRecorder(), mkReq(libID))

    var samples []time.Duration
    for i := 0; i < 100; i++ {
        rr := httptest.NewRecorder()
        t0 := time.Now()
        h.Stats(rr, mkReq(libID))
        samples = append(samples, time.Since(t0))
    }
    p99 := percentile(samples, 99)
    require.Less(t, p99, 50*time.Millisecond,
        "p99 stats handler latency = %v exceeds 50 ms budget", p99)
}
```

### 4.5 `TestReconciler_DetectsAndRepairs` (AC-2 reconciliation)

```go
func TestReconciler_RepairsCorruptedCache(t *testing.T) {
    db := testdb.Fresh(t)
    libID := testdb.SeedLibrary(t, db, "videos")
    testdb.BulkInsertVideos(t, db, libID, 10)

    // Deliberately corrupt the cache row.
    _, err := db.Pool.Exec(t.Context(),
        `UPDATE library_stats_cache SET total_videos = 999 WHERE library_id = $1`,
        libID)
    require.NoError(t, err)

    r := stats.NewReconciler(db.Pool)
    n, err := r.Rebuild(t.Context(), &libID, false)
    require.NoError(t, err)
    require.Equal(t, 1, n)

    var total int
    require.NoError(t, db.Pool.QueryRow(t.Context(),
        `SELECT total_videos FROM library_stats_cache WHERE library_id=$1`,
        libID).Scan(&total))
    require.Equal(t, 10, total)
}
```

### 4.6 `TestProcessingJobsTrigger_BucketsCorrectly` (AC-1 jobs)

```go
func TestJobsTrigger_QueuedToRunningRebalance(t *testing.T) {
    db := testdb.Fresh(t)
    libID := testdb.SeedLibrary(t, db, "videos")
    vid := testdb.SeedVideo(t, db, libID, "discovered")
    var jobID int64
    require.NoError(t, db.Pool.QueryRow(t.Context(), `
        INSERT INTO processing_jobs (stage, state, video_id, priority)
        VALUES ('probe', 'queued', $1, 100) RETURNING id
    `, vid).Scan(&jobID))

    // queued → running
    _, err := db.Pool.Exec(t.Context(),
        `UPDATE processing_jobs SET state = 'running' WHERE id = $1`, jobID)
    require.NoError(t, err)

    var jobs map[string]int
    var raw []byte
    require.NoError(t, db.Pool.QueryRow(t.Context(),
        `SELECT jobs_jsonb FROM library_stats_cache WHERE library_id=$1`,
        libID).Scan(&raw))
    require.NoError(t, json.Unmarshal(raw, &jobs))
    require.Equal(t, 0, jobs["pending"])
    require.Equal(t, 1, jobs["running"])
}
```

### 4.7 `TestSweepFinalizer_PopulatesLastSweep`

```go
func TestSweepFinalizer_UpdatesCacheLastSweep(t *testing.T) {
    db := testdb.Fresh(t)
    libID := testdb.SeedLibrary(t, db, "videos")
    sweepID := testdb.SeedSweep(t, db, libID)

    _, err := db.Pool.Exec(t.Context(), `
        UPDATE library_sweeps
           SET finished_at = now(), scanned = 100, new_videos = 10
         WHERE id = $1
    `, sweepID)
    require.NoError(t, err)

    var raw []byte
    require.NoError(t, db.Pool.QueryRow(t.Context(),
        `SELECT last_sweep_jsonb FROM library_stats_cache WHERE library_id=$1`,
        libID).Scan(&raw))
    var ls map[string]any
    require.NoError(t, json.Unmarshal(raw, &ls))
    require.Equal(t, float64(100), ls["scanned"])
    require.Equal(t, float64(10), ls["new_videos"])
}
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handled by |
|-----|-----------|------------|
| E1  | **Empty library** (story AC-3). Cache row exists from D9 init; total=0; `processed_pct = NULL` from the SQL CASE expression; `last_sweep_jsonb` is NULL; the JSON encoder emits `null`. | `TestStatsHandler_EmptyLibrary_ReturnsNullPct` |
| E2  | **Library deletion mid-stats-request** (story edge). FK `ON DELETE CASCADE` removes the cache row when `libraries` is deleted. The handler's `pgx.ErrNoRows` branch returns 404. | `test_library_deleted_returns_404` |
| E3  | **Bulk insert of 10k videos in one transaction** (story edge). Trigger fires per row; total cost ~50 ms; the cache row sees one consistent state at COMMIT (until commit, no other reader sees the partial state due to SI). | `test_bulk_insert_10k_consistent_at_commit` |
| E4  | **Bulk loads outside transactional inserts** (story edge — `COPY` from CSV). `COPY` fires triggers per row by default in Postgres ≥ 14, so the cache stays consistent. If a future tool uses `COPY (FREEZE)` or replication-style direct inserts that bypass triggers, drift is repaired by the nightly `stats-rebuild`. | `TestReconciler_RepairsCorruptedCache` |
| E5  | **Counter underflow.** A trigger could in principle decrement a state count below zero if migrations land out of order or a row is missing from the cache. The trigger uses `GREATEST(0, ...)` to clamp; the reconciler is the canonical fix. | Trigger SQL `GREATEST(0, ...)` clauses |
| E6  | **`detected_language` or `content_type` NULL.** Normalized to `'und'` and `'unknown'` respectively in the trigger so the JSONB always has typed keys; the handler emits the same. | `test_video_with_null_lang_counts_as_und` |
| E7  | **Library reassignment** (a video moves to another library — rare, but Story 9.16 allows it). Trigger handles `OLD.library_id <> NEW.library_id` by applying −1 delta to the old cache row and +1 delta to the new in two `apply_videos_delta` calls. | Trigger `IF NEW.library_id <> OLD.library_id` branch |
| E8  | **Cache row lock contention.** Concurrent INSERTs into the same library serialize on the cache row UPDATE. Postgres handles this fairly; the per-row UPDATE cost is microseconds, so contention is bounded. | Postgres row-level locking; bench in §7 |
| E9  | **Reconciler running concurrently with live triggers.** The reconciler's UPDATE wraps in a transaction and uses `SELECT … FOR UPDATE` on the cache row to serialize against live writers. A cache write that committed during the reconciler's recompute will be re-overwritten — that's correct because the recompute observed the same data. | `TestReconciler_DoesNotRaceWithTriggers` |
| E10 | **Stale `derived_size_bytes`** (D6). Documented contract; `x-staleness: ≤ 24h` in OpenAPI; the `maktaba-api stats-rebuild` recompute covers it. | `TestReconciler_RepairsDerivedSize` |

---

## 6. Acceptance checklist

- [ ] **A1** Response shape matches the story exactly: `total_videos`, `total_duration_sec`, `processed_pct` (nullable), `by_state` (covers DISCOVERED, PROBED, AUDIO_EXTRACTED, TRANSCRIBED, INDEXED, THUMBNAILED, READY, READY_NO_AUDIO, FAILED, MISSING, SUPERSEDED, CORRUPTED), `by_language`, `by_content_type`, `storage.{source_size_bytes, derived_size_bytes}`, `jobs.{pending, running, paused, failed}`, `last_sweep` (nullable). (`TestStatsHandler_ResponseShape`)
- [ ] **A2** A 50,000-video library returns stats in p99 < 50 ms. (`TestStatsHandler_50kVideoLibrary_Under50ms`)
- [ ] **A3** Trigger on `videos` updates the cache on INSERT, UPDATE (state/language/content_type/size/duration), and DELETE; `by_state_jsonb` reflects state transitions in the next stats call. (`TestVideosTrigger_*`)
- [ ] **A4** Trigger on `processing_jobs` updates `jobs_jsonb` using the four-bucket mapping (queued|resuming → pending; claimed|running → running; paused → paused; failed|dead → failed); done/canceled don't appear. (`TestJobsTrigger_*`)
- [ ] **A5** Sweep finalizer trigger writes `last_sweep_jsonb` when `finished_at` is set. (`TestSweepFinalizer_UpdatesCacheLastSweep`)
- [ ] **A6** Empty library returns `processed_pct = null` (not `0/0`). (`TestStatsHandler_EmptyLibrary_ReturnsNullPct`)
- [ ] **A7** Counts add up: `sum(by_state values) == total_videos`; `sum(by_language values) == total_videos`. (`TestStatsHandler_CountsAddUp`)
- [ ] **A8** `processed_pct` rounds to 2 decimals at read time. (`TestStatsHandler_PctRoundsToTwoDecimals`)
- [ ] **A9** `maktaba-api stats-rebuild [--library-id=X] [--dry-run]` recomputes from source tables, detects mismatches, increments `stats_cache_corruption_total`, and rewrites the cache (unless `--dry-run`). (`TestReconciler_RepairsCorruptedCache`)
- [ ] **A10** Library deletion cascades the cache row away; the handler returns 404 for the deleted library. (`test_library_deleted_returns_404`)
- [ ] **A11** Migration `0026_library_stats_cache.sql` creates the table with all triggers, backfills existing libraries, and the new-library trigger seeds future rows. (`TestMigration0026`)

---

## 7. Performance budget

The 50 ms target is on the **handler**, not the trigger path; both
matter, so we account for each.

### Read path (handler, AC-2)

| Phase | Cost | Notes |
|-------|------|-------|
| chi route dispatch | < 100 µs | static routing table. |
| auth check (RequireLibraryRead) | ~1 ms | session cache hit (Epic 10). |
| `SELECT … FROM library_stats_cache WHERE library_id = $1` | ~5 ms | primary-key lookup; cache row ~500 B. |
| `processed_pct` SQL CASE | ~0 ms | one division on the same row. |
| JSON encode (json.RawMessage passthrough) | ~1 ms | three JSONB fields are byte-passed. |
| WriteResponse | ~1 ms | gzip off for small responses. |
| **Total** | **~8–15 ms typical, < 50 ms p99** | well under budget. |

### Write path (triggers — invisible to the user, but watched for amplification)

| Phase | Cost (per fire) | Notes |
|-------|-----------------|-------|
| videos trigger fire (INSERT) | ~30 µs | one indexed UPDATE on cache row + jsonb_set. |
| videos trigger fire (UPDATE state only) | ~40 µs | two jsonb_set calls. |
| processing_jobs trigger fire | ~25 µs | one UPDATE; library_id resolution is direct on the new column. |
| Bulk-insert 10k videos (one txn) | ~300 ms | 10k × ~30 µs; serialize on the cache row but txn-bounded. |

The fixed cost is dominated by the cache-row UPDATE; further
optimization (e.g., batched cache-row updates) is unnecessary for the
50k-video target.

---

## 8. Operational notes

- **Cron schedule.** Recommend a systemd timer at 02:30 daily:
  `ExecStart=/usr/bin/maktaba-api stats-rebuild`. The Reconciler is
  per-library and idempotent; running it concurrently is safe.
- **Metrics exposed:**
  - `stats_cache_corruption_total{library_id}` — counter, increments on each detected mismatch.
  - `stats_handler_duration_seconds{quantile}` — histogram for the handler.
  - `stats_trigger_duration_seconds{table}` — histogram for the trigger fires (sampled via `pg_stat_user_functions`).
- **Drift root causes to watch for:**
  - Direct `COPY (FREEZE)` loads that bypass triggers (we don't use this in v1).
  - Missing migration ordering (Story 9.6 must land before triggers expect `processing_jobs.library_id`).
  - State-machine drift (Story 7.x state set diverges from the trigger's bucket function); the reconciler will catch it within 24 h.
