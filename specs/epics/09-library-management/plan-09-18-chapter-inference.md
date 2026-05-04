# Implementation Plan — Story 9.18 Chapter Inference

> Companion to [story-09-18-chapter-inference.md](story-09-18-chapter-inference.md).
> The story states *what* and *why*; this plan states *how*.
> Builds on Story 9.9 (embeddings infrastructure), Epic 5
> (segment-level embeddings), and Epic 8 Story 8.12 (chapter
> delivery — out of scope here).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Pipeline stage | New stage `chapter_infer`, enqueued by the indexer commit when `library.chapter_inference = True` AND no embedded/manual chapters cover the entire video. The job runs in the Pipeline; one job per video; idempotent. |
| Algorithm | Sliding-window cosine drop. For each segment boundary `i`, compute the mean embedding of the last `window_segments` segments and the next `window_segments` segments; the cosine *drop* is `1 - dot(prev_mean, next_mean)` (both unit-norm). A boundary is emitted when the drop exceeds `chapter_drop_threshold` (default 0.35) AND the time since the previous boundary is at least `min_chapter_sec` (default 60). |
| Title generation | Top-3 segments inside the chapter window are concatenated; the embedder produces a 1–4 word slug as in Story 9.9's labeler. Capped at 80 chars. Falls back to `"Chapter N"` when the embedder is unavailable. |
| Output | `chapters` table with `source='inferred'`, `seq` from 0. Existing `source='inferred'` rows for the video are deleted before insert (idempotent re-inference). |
| Suppression | `embedded` and `manual` chapters take precedence; inferred boundaries falling inside an existing range are filtered out *before* insert. The merge order is fixed in Epic 8 Story 8.12 AC-1. |
| Manual reinfer | `POST /api/videos/{id}/chapters/reinfer` enqueues `chapter_infer` with `payload.force=True` (refreshes against current settings). |
| Out of scope | The HLS DATERANGE / JSON delivery format (Epic 8 Story 8.12); the FSM transitions for `chapter_infer` (Pipeline Story 1.5 owns; this story uses the existing `processing_jobs.stage` slot). |

## 1. Architecture diagram

```
   indexer.commit(video_id) (Epic 5)
      ↓ if library.chapter_inference: enqueue(chapter_infer, video_id, p=200)
      ↓ also enqueue(topic_assign) (Story 9.9) and (categorize) (Story 9.10)

   chapter_infer worker:
      ├─ load library_settings
      ├─ if not settings.chapter_inference: skip; counter
      ├─ load existing chapters: embedded + manual (do NOT touch);
      │     load existing inferred (will be replaced)
      ├─ load segments ordered by start_sec
      │     (id, start_sec, end_sec, embedding[768] BYTEA)
      ├─ if len(segments) < 2 * window_segments:
      │     emit single chapter spanning whole video
      │     return
      ├─ cosine_drops = sliding_window_cosine_drops(
      │     segments, window=window_segments)
      ├─ raw_boundaries = [
      │     i for i, drop in enumerate(cosine_drops)
      │     if drop >= chapter_drop_threshold
      │  ]
      ├─ filtered = enforce_min_chapter_sec(raw_boundaries, segments,
      │                                     min_chapter_sec)
      ├─ filtered = drop_in_existing_ranges(filtered, embedded+manual)
      ├─ chapters_out = build_chapters(filtered, segments, video.duration)
      ├─ for ch in chapters_out:
      │     ch.title = labeler.label(library_id, segments_in(ch))
      │                 OR f"Chapter {ch.seq + 1}"
      ├─ BEGIN TX
      │     DELETE FROM chapters WHERE video_id=$1 AND source='inferred'
      │     INSERT INTO chapters (id, video_id, source, seq,
      │                           start_sec, end_sec, title)
      │     VALUES (...) -- one per chapter
      │ COMMIT
      └─ NOTIFY 'video.chapters_updated', {video_id, n: len(chapters_out)}
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/chapters/__init__.py` | Re-exports. |
| `pipeline/src/maktaba_pipeline/chapters/infer.py` | The algorithm + worker. |
| `pipeline/src/maktaba_pipeline/chapters/cosine.py` | `sliding_window_cosine_drops` numerics. |
| `pipeline/tests/chapters/test_cosine.py` | Numerical correctness per §6.1. |
| `pipeline/tests/chapters/test_infer.py` | Algorithm tests per §6.2. |
| `pipeline/tests/chapters/test_worker_integration.py` | Worker / DB tests per §6.3. |
| `api/internal/handlers/videos/reinfer.go` | `POST /api/videos/{id}/chapters/reinfer`. |
| `shared/db/migrations/0046_chapters_source.sql` | Adds the `source` column + CHECK if missing; index on `(video_id, source, seq)`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/jobs/dispatcher.py` | Register `chapter_infer` stage. |
| `pipeline/src/maktaba_pipeline/index/commit.py` | After commit, enqueue `chapter_infer` (alongside `topic_assign`, `categorize`). |
| `shared/db/migrations/0010_processing_jobs.sql` | Add `'chapter_infer'` to stage CHECK list (idempotent revision). |
| `api/internal/router.go` | Wire `POST /api/videos/{id}/chapters/reinfer`. |
| `specs/epics/09-library-management/README.md` | Tick story 9.18. |

### 2.3 Type definitions

```python
# pipeline/src/maktaba_pipeline/chapters/infer.py
from dataclasses import dataclass

WINDOW_SEGMENTS_DEFAULT = 5
DROP_THRESHOLD_DEFAULT  = 0.35
MIN_CHAPTER_SEC_DEFAULT = 60
TITLE_MAX_CHARS         = 80


@dataclass(slots=True, frozen=True)
class InferredChapter:
    seq: int
    start_sec: float
    end_sec: float
    title: str
```

## 3. Database migration

`shared/db/migrations/0046_chapters_source.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

-- Architecture sketches `chapters(id, video_id, seq, start_sec, end_sec,
-- title)`. Add `source` to disambiguate the three origins.
ALTER TABLE chapters
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'embedded'
        CHECK (source IN ('embedded','manual','inferred'));

-- Re-inference deletes by (video_id, source='inferred'); add an index.
CREATE INDEX IF NOT EXISTS chapters_video_source_seq
    ON chapters (video_id, source, seq);

-- Title length cap (defensive; the worker also caps).
ALTER TABLE chapters
    ADD CONSTRAINT IF NOT EXISTS chapters_title_len_chk
    CHECK (title IS NULL OR char_length(title) <= 80);

-- Time-range validity.
ALTER TABLE chapters
    ADD CONSTRAINT IF NOT EXISTS chapters_time_chk
    CHECK (start_sec >= 0 AND end_sec > start_sec);

-- A non-overlap safety check at the DB level is intentionally absent;
-- the worker is the source of truth for non-overlap inside `inferred`,
-- and `inferred` is allowed to coexist with `embedded`/`manual`.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS chapters_video_source_seq;
ALTER TABLE chapters DROP CONSTRAINT IF EXISTS chapters_time_chk;
ALTER TABLE chapters DROP CONSTRAINT IF EXISTS chapters_title_len_chk;
ALTER TABLE chapters DROP COLUMN IF EXISTS source;
-- +goose StatementEnd
```

## 4. Code scaffolding

### 4.1 `cosine.sliding_window_cosine_drops`

```python
# pipeline/src/maktaba_pipeline/chapters/cosine.py
import numpy as np

def sliding_window_cosine_drops(
    embeddings: np.ndarray,   # shape (N, D), unit-norm
    window: int,
) -> np.ndarray:
    """Return drops of length N - 2*window + 1.
    drops[k] = 1 - cos(mean(embeddings[k-window:k]),
                       mean(embeddings[k:k+window]))
    Assigns drops to *boundary k*, where boundary k is between
    segment (k-1) and segment k.
    """
    if embeddings.ndim != 2:
        raise ValueError("expected (N, D) array")
    n, d = embeddings.shape
    if n < 2 * window:
        return np.zeros(0, dtype=np.float32)

    # Cumulative sum trick: O(N) instead of O(N*window).
    cum = np.zeros((n + 1, d), dtype=np.float32)
    cum[1:] = np.cumsum(embeddings, axis=0)

    def window_mean(start: int, end: int) -> np.ndarray:
        m = cum[end] - cum[start]
        n_in = end - start
        m /= n_in
        # renormalize
        norm = np.linalg.norm(m) or 1.0
        m /= norm
        return m

    drops = np.empty(n - 2 * window + 1, dtype=np.float32)
    for k in range(window, n - window + 1):
        prev = window_mean(k - window, k)
        nxt  = window_mean(k, k + window)
        drops[k - window] = 1.0 - float(np.dot(prev, nxt))
    return drops
```

### 4.2 The worker

```python
# pipeline/src/maktaba_pipeline/chapters/infer.py
async def run_chapter_infer_job(db, job, *, labeler=None,
                                 bus=None) -> None:
    video_id = job.video_id
    payload = job.payload or {}
    force = bool(payload.get("force", False))

    settings = await effective_for(db, await _library_for_video(db, video_id))
    if not settings.chapter_inference and not force:
        chapter_infer_skipped_total.labels(reason="disabled").inc()
        return

    window = settings.unknown.get("window_segments", WINDOW_SEGMENTS_DEFAULT)
    threshold = settings.unknown.get("chapter_drop_threshold", DROP_THRESHOLD_DEFAULT)
    min_sec = settings.unknown.get("min_chapter_sec", MIN_CHAPTER_SEC_DEFAULT)

    segments = await db.fetch(
        "SELECT id, start_sec, end_sec, embedding "
        "  FROM transcript_segments "
        " WHERE video_id=$1 AND embedding IS NOT NULL "
        " ORDER BY start_sec",
        video_id,
    )
    duration = await db.fetchval(
        "SELECT duration_sec FROM videos WHERE id=$1", video_id)
    if duration is None:
        chapter_infer_skipped_total.labels(reason="no_duration").inc()
        return

    if len(segments) < 2 * window:
        chapters_out = [InferredChapter(seq=0, start_sec=0.0,
                                        end_sec=duration, title="Chapter 1")]
    else:
        embs = np.stack([unpack_float32(r["embedding"], 768)
                         for r in segments])
        # Each embedding is already unit-norm in our index; defensive renorm.
        norms = np.linalg.norm(embs, axis=1, keepdims=True)
        norms[norms == 0] = 1.0
        embs /= norms

        drops = sliding_window_cosine_drops(embs, window)
        raw = [k + window for k, d in enumerate(drops) if d >= threshold]

        # Enforce min_chapter_sec by collapsing close boundaries.
        existing = await db.fetch(
            "SELECT start_sec, end_sec, source FROM chapters "
            "  WHERE video_id=$1 AND source IN ('embedded','manual') "
            "  ORDER BY start_sec",
            video_id,
        )
        boundaries = _enforce_min_sec(raw, segments, min_sec)
        boundaries = _drop_in_existing(boundaries, segments, existing)

        chapters_out = _build_chapters(boundaries, segments, duration)

    if labeler is not None:
        for ch in list(chapters_out):
            try:
                title = await labeler.label_for_chapter(
                    video_id, segments, ch)
            except LabelerUnavailable:
                title = f"Chapter {ch.seq + 1}"
            ch.title = title[:TITLE_MAX_CHARS] or f"Chapter {ch.seq + 1}"

    async with db.transaction():
        await db.execute(
            "DELETE FROM chapters WHERE video_id=$1 AND source='inferred'",
            video_id,
        )
        for ch in chapters_out:
            await db.execute(
                "INSERT INTO chapters "
                "  (id, video_id, source, seq, start_sec, end_sec, title) "
                "VALUES ($1, $2, 'inferred', $3, $4, $5, $6)",
                uuid4(), video_id, ch.seq,
                ch.start_sec, ch.end_sec, ch.title,
            )
    if bus is not None:
        bus.publish("video.chapters_updated",
                    {"video_id": str(video_id), "n": len(chapters_out)})
```

### 4.3 Helpers

```python
def _enforce_min_sec(boundaries, segments, min_sec):
    out = []
    last_end = 0.0
    for k in boundaries:
        b_t = float(segments[k]["start_sec"])
        if b_t - last_end < min_sec:
            continue
        out.append(k)
        last_end = b_t
    return out


def _drop_in_existing(boundaries, segments, existing):
    out = []
    for k in boundaries:
        b_t = float(segments[k]["start_sec"])
        if any(e["start_sec"] <= b_t < e["end_sec"] for e in existing):
            continue
        out.append(k)
    return out


def _build_chapters(boundaries, segments, duration):
    # boundaries are *segment indices* whose start_sec begins a chapter.
    # We always emit a chapter starting at 0.0.
    starts = [0.0] + [float(segments[k]["start_sec"]) for k in boundaries]
    ends = starts[1:] + [duration]
    return [
        InferredChapter(seq=i, start_sec=s, end_sec=e, title=f"Chapter {i + 1}")
        for i, (s, e) in enumerate(zip(starts, ends))
    ]
```

### 4.4 Reinfer endpoint

```go
// api/internal/handlers/videos/reinfer.go
func ReinferHandler(d *handlers.Deps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        videoID, _ := uuid.Parse(chi.URLParam(r, "id"))
        res, err := jobs.Enqueue(r.Context(), d.Queries, jobs.EnqueueRequest{
            VideoID: videoID, Stage: "chapter_infer",
            Priority: 100,
            Payload:  map[string]any{"force": true},
        })
        if err != nil { handlers.WriteError(w, 500, "enqueue-failed", err.Error()); return }
        handlers.WriteJSON(w, 202, map[string]any{
            "job_id": res.ID, "outcome": res.Outcome,
        })
    }
}
```

## 5. Test plan

### 5.1 `test_cosine.py`

| Test | What it pins |
|---|---|
| `test_uniform_embeddings_zero_drops` | All segments unit-equal → drops are zero. |
| `test_step_function_one_drop` | Half identical to A, half identical to B; one large drop at midpoint. |
| `test_drop_position` | Synthesize known boundary at index 50 (window 5) → drops[45] is the maximum. |
| `test_window_smaller_than_segments_returns_zero_length` | N < 2*window → empty array (no drops). |
| `test_normalization_idempotent` | Running with already-unit-norm vs unnormalized inputs → same result (within 1e-6). |

### 5.2 `test_infer.py`

| Test | What it pins |
|---|---|
| `test_three_topic_shifts_three_boundaries` | A 200-segment fixture with 3 obvious topic shifts → exactly 4 chapters. AC-1 + integration. |
| `test_uniform_transcript_one_chapter` | Embeddings all very similar → 0 boundaries → 1 chapter spanning the video. AC edge case + AC-1. |
| `test_min_chapter_sec_collapses_close_boundaries` | Re-run with `min_chapter_sec=120` on a fixture that produced 60 s chapters → fewer boundaries; closest boundaries dropped. AC-4 / story integration. |
| `test_existing_embedded_range_suppresses_inferred` | Pre-state: embedded chapter [100..200] s; raw inferred boundary at 150 s → suppressed. AC-4. |
| `test_existing_manual_range_suppresses_inferred` | Same as above with `source='manual'`. AC-4. |
| `test_min_chapter_sec_does_not_ignore_first_boundary` | First raw boundary at 5 s — inferred chapter "Chapter 1" still spans 0..5? No — `_enforce_min_sec` requires `b_t - last_end >= min_sec` where `last_end` starts at 0. So a boundary at 5 s with `min_sec=60` is dropped. The first chapter spans 0..next-boundary. Pinned. |
| `test_short_video_one_chapter` | 8 segments, window=5 → `len < 2*window` → one chapter. |
| `test_drop_threshold_zero_caps_at_min_sec` | If threshold is so low every adjacent pair qualifies, `min_chapter_sec` enforces ceiling = `duration / min_sec` chapters. Edge case from story. |

### 5.3 Worker / DB integration (`test_worker_integration.py`)

| Test | What it pins |
|---|---|
| `test_run_inserts_chapters` | Run worker; `chapters` table has the expected rows with `source='inferred'`, `seq` 0..N. |
| `test_rerun_replaces_inferred_atomically` | Run twice; `inferred` rows from the first run are gone after the second; embedded/manual rows untouched. AC-2 idempotent. |
| `test_disabled_short_circuits` | `library.chapter_inference=False` → no DB writes; counter `chapter_infer_skipped_total{reason=disabled}`. AC-5. |
| `test_force_overrides_disabled` | `payload.force=True` → runs even when disabled. (Used by `/reinfer`.) |
| `test_labeler_unavailable_falls_back_to_chapter_n` | Stub labeler raises; titles become `"Chapter N"`. AC-3 fallback. |
| `test_failure_marks_job_failed_with_error_code` | Embedder unreachable AND no segment embeddings → worker raises; processing_jobs row → `state='failed'`, `error.code='embedder-down'`. AC test from story. |
| `test_video_chapters_updated_notify` | After a successful run, listener on `video.chapters_updated` receives a payload with `video_id` and `n`. |

### 5.4 API endpoint test

`reinfer_test.go`:

| Test | What it pins |
|---|---|
| `TestReinfer_EnqueuesForceJob` | POST → 202 with `job_id`; the enqueued payload includes `force=true` and `priority=100`. |
| `TestReinfer_404OnUnknownVideo` | Unknown video → 404 (handler should validate the video). |
| `TestReinfer_AdminOnly` | Non-admin → 403 (auth at the route level, owned by Epic 10). |

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Transcript with < 2 * window_segments | Single chapter spanning whole video. AC edge case. | `test_short_video_one_chapter` |
| Cosine drop tuned too low | `min_chapter_sec` ceilings the count. AC edge case. | `test_drop_threshold_zero_caps_at_min_sec` |
| Setting flipped after inference ran | Old `inferred` rows preserved; user must reinfer or purge. | Documented |
| Embedded/manual chapter overlaps inferred | Priority merge in Epic 8 Story 8.12 AC-1; this story prevents inferred from inserting in the embedded/manual range. | `test_existing_embedded_range_suppresses_inferred` |
| Concurrent reinfer + indexer commit | Both enqueue `chapter_infer`; idempotent unique partial index from Story 6.1 collapses to one job; the second one returns `outcome='reused'`. | `enqueue` already pinned in Story 6.1 |
| Embedder down | Centroid math runs without embedder; only titles fall back to `"Chapter N"`. | `test_labeler_unavailable_falls_back_to_chapter_n` |
| Segment embeddings missing | Fall back to single-chapter span (effectively `len < 2*window`). | `test_short_video_one_chapter` (variant) |

## 7. Configuration

| Key | Default | Effect |
|---|---|---|
| `chapter_inference` (per library) | True | Enables/disables. |
| `window_segments` (forward-compat key) | 5 | Window size for cosine drops. Reserved in `library_settings.schema.json`. |
| `chapter_drop_threshold` (forward-compat key) | 0.35 | Drop value above which a boundary is emitted. |
| `min_chapter_sec` (forward-compat key) | 60 | Minimum chapter duration. |

These last three are *not* recognized top-level keys in v1; they're
read from `effective.unknown` (Story 9.1's forward-compat surface).
A future Story-9.1 schema bump promotes them to first-class.

## 8. Dependencies

| Dep | Source | Why |
|---|---|---|
| `transcript_segments.embedding` | Epic 5 Story 5.5 | Source of cosine drops. |
| `videos.duration_sec` | Epic 1 Story 1.7 | End of last chapter. |
| Story 9.9 labeler | required | Title generation fallback. |
| `numpy` | already pinned | Numerics. |

## 9. Acceptance checklist

**Code**
- [ ] `pipeline/src/maktaba_pipeline/chapters/` package created.
- [ ] Indexer commit enqueues `chapter_infer` post-INDEXED.
- [ ] `POST /api/videos/{id}/chapters/reinfer` wired and admin-checked.

**Migration**
- [ ] `chapters.source` column with CHECK on the 3-value enum.
- [ ] `chapters_video_source_seq` index for delete-by-source.

**Behaviour (story acceptance criteria)**
- [ ] AC-1: stage runs after INDEXED when enabled; cosine drops + min duration produce boundaries.
- [ ] AC-2: existing inferred rows replaced atomically.
- [ ] AC-3: titles via labeler with `"Chapter N"` fallback.
- [ ] AC-4: embedded/manual ranges suppress inferred boundaries.
- [ ] AC-5: `chapter_inference: false` skips the stage; existing inferred rows preserved.

**Observability**
- [ ] Counter `chapter_infer_runs_total{outcome=ok|skipped|failed}`.
- [ ] Histogram `chapter_infer_duration_seconds`.
- [ ] Counter `chapter_infer_boundaries_emitted_total`.

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.18.
- [ ] Operator handbook covers the three knobs (`window_segments`, `chapter_drop_threshold`, `min_chapter_sec`) and how to surface them via library settings.
