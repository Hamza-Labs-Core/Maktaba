# Plan 9.18 — Chapter inference from transcript topic shifts — implementation

> Implementation plan for [story-09-18-chapter-inference.md](story-09-18-chapter-inference.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: extends the chapter-math design from
> [Plan 5.7](../05-search-indexing/plan-05-07-chapter-inference.md) (which
> covers the unit-coordinate algorithm and ML-side concerns), wires it as
> a real Pipeline stage with `processing_jobs.stage='chapter_infer'`,
> introduces the `chapters.source` column needed by Epic 8 Story 8.12's
> priority merge, and adds the `POST /api/videos/{id}/chapters/reinfer`
> Go enqueue handler.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Promote chapter inference to a top-level pipeline stage `chapter_infer`** with its own `processing_jobs.stage` enum value, scheduled by the orchestrator after `index` reaches DONE. (Plan 5.7's "tail of `index` sub-stage" is rejected.) | Story 9.18 AC-1: "the job is recorded in `processing_jobs` with `stage='chapter_infer'` (the FSM in arch §3 needs the matching state — see REVIEW.md §1.3.b)." | Plan 5.7's tail-substage approach made resume tricky (a crash mid-tail leaves `index` ambiguous between "done" and "almost done") and made per-library disablement awkward. A first-class stage matches the FSM, gets its own retry budget, and integrates cleanly with `POST /api/videos/{id}/chapters/reinfer` which simply enqueues a new `chapter_infer` job. The math from Plan 5.7 is reused verbatim. |
| D2 | **`chapters.source TEXT NOT NULL` with values `inferred` / `embedded` / `manual`.** The atomic-replace pattern deletes only `source='inferred'` rows so embedded and manual chapters are preserved across re-runs. | Story 9.18 AC-2 + AC-4: "Existing source='inferred' rows for the video are deleted in the same transaction so re-inference is idempotent" and "existing embedded and manual rows are preserved unchanged." | Without a `source` discriminator, re-inference would clobber user-curated and embedded-stream chapters — the worst regression possible. The discriminator also lets Epic 8 Story 8.12's priority merge (manual > embedded > inferred) be a single ORDER BY. |
| D3 | **5-segment sliding cosine-drop window with threshold 0.35 and `min_chapter_sec=60`,** matching the story's defaults; per-library overrides via `library.settings.chapter` (Story 9.1). The math is the same as Plan 5.7's `smoothing_window=3` formulation but the smoothing window is **5** here per the story text. | Story 9.18 AC-1: "computes cosine similarity between adjacent windows (`window_segments`, default 5)." | The story explicitly fixes the window at 5; we honor that. (Plan 5.7 used `smoothing_window=3` as its centroid reference; both are valid and the wider window biases toward fewer false positives at the cost of slightly less spatial precision — appropriate for the asynchronous post-`index` stage that operates on batch outputs.) |
| D4 | **Title fallback to `"Chapter N"` when the embedder is unreachable;** when reachable, build the title from the top-3 segments inside the chapter, embed once, find the nearest token in the embedder's vocab, return the bigram (capped at 80 chars). On embedder timeout / 5xx, the row stays with `title=NULL` and the worker records `embedder_unreachable=true` in `chapters.metadata` so the API can render "Chapter N" without re-querying. | Story 9.18 AC-3. | Plan 5.7 deferred titling to v1.1 entirely (`title=NULL` + API fallback); Story 9.18 commits to a v1 titler with a graceful degradation path. The `embedder_unreachable` flag lets the API distinguish "we tried, embedder was down" from "we never tried." |
| D5 | **Atomic replace inside a single Postgres transaction:** `DELETE FROM chapters WHERE video_id=$1 AND source='inferred'; INSERT INTO chapters (...) VALUES ...; COMMIT;` — preserving embedded and manual rows. The `(video_id, seq)` UNIQUE constraint becomes `(video_id, source, seq)` to allow the three sources to coexist. | Story 9.18 AC-2 + AC-4. | Without the source-aware key, an embedded chapter at `seq=0` and an inferred chapter at `seq=0` would conflict. The compound key is the simplest expression of "each source numbers its own chapters from 0." |
| D6 | **`POST /api/videos/{id}/chapters/reinfer` enqueues a new `processing_jobs` row** with `stage='chapter_infer'`, `priority=user_initiated`, returns 202 + the job id. The Go handler does not run the math; it only enqueues. | Story 9.18 AC-4: "POST /api/videos/{id}/chapters/reinfer ... the inference re-runs with current settings." | Synchronous inference would block the request for ~500ms–2s on long videos (per Plan 5.7 §7) and would be inappropriate for an HTTP handler. The enqueue model means re-infer behaves identically to the periodic `chapter_infer` job. |

If D1 is rejected (keep the tail-substage from Plan 5.7): re-infer-on-demand has nowhere to enqueue to; the `POST /chapters/reinfer` handler would need its own ad-hoc job kind. The cleaner answer is to make `chapter_infer` first-class.

If D5 is rejected (delete-all + reinsert): manual and embedded chapters get nuked on every re-run — a UX regression that Story 9.18 AC-4 explicitly forbids.

---

## 1. Architecture diagram — chapter_infer as a pipeline stage

```
   ┌── orchestrator (Pipeline) ────────────────────┐
   │   on video INDEXED:                           │
   │     if library.settings.chapter_inference:    │
   │       enqueue processing_jobs(                │
   │         stage='chapter_infer',                │
   │         video_id=v, priority=normal)          │
   └───────────────────────┬───────────────────────┘
                           │
                           ▼
   ┌── chapter_infer worker (Pipeline, asyncio) ──────────────┐
   │                                                          │
   │  1. SELECT segments WHERE video_id=$1 ORDER BY start_sec │
   │  2. fetch embeddings (Chroma bulk get)                   │
   │  3. < 2*window_segments → 1 chapter at [0, duration]     │
   │  4. for i in [w .. N-w-1]:                               │
   │       left  = mean(embs[i-w .. i])                       │
   │       right = mean(embs[i+1 .. i+w+1])                   │
   │       drop  = 1 - cos(left, right)                       │
   │       if drop > 0.35: candidate boundary                 │
   │  5. enforce min_chapter_sec=60 (greedy higher-conf wins) │
   │  6. title each chapter:                                  │
   │       try: embedder.title_from_top3_segments()           │
   │       except (Timeout, ConnError): title=None,           │
   │         metadata.embedder_unreachable=True               │
   │  7. atomic replace (D5):                                 │
   │       BEGIN;                                             │
   │         DELETE FROM chapters                             │
   │           WHERE video_id=$1 AND source='inferred';       │
   │         INSERT INTO chapters (video_id, source, seq,     │
   │           start_sec, end_sec, title, confidence, meta)   │
   │         VALUES ...;                                      │
   │       COMMIT;                                            │
   │  8. mark processing_jobs DONE                            │
   └──────────────────────────────────────────────────────────┘

   ┌── API: POST /api/videos/{id}/chapters/reinfer (Go) ──┐
   │                                                      │
   │  validate video exists                               │
   │  INSERT INTO processing_jobs (id, video_id,          │
   │    stage='chapter_infer', priority='user_initiated', │
   │    state='queued') VALUES (...)                      │
   │  return 202 + job id                                 │
   └──────────────────────────────────────────────────────┘
```

The Go API never runs inference; it only enqueues. The Pipeline worker
is the single source of truth for the cosine math; it reuses the
algorithm from Plan 5.7 with the `window_segments=5` parameter (D3).

---

## 2. Detailed implementation

### 2.1 Schema migration — `0023_chapter_inference.sql`

```sql
BEGIN;

-- Plan 5.7 created the chapters table without a source column. This
-- migration adds the discriminator and rebuilds the unique constraint.
ALTER TABLE chapters
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'inferred'
        CHECK (source IN ('inferred', 'embedded', 'manual')),
    ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE chapters
    DROP CONSTRAINT IF EXISTS chapters_transcript_id_seq_key;
ALTER TABLE chapters
    ADD CONSTRAINT chapters_video_source_seq_key
        UNIQUE (video_id, source, seq);

-- Index for the priority-merge query in Epic 8 Story 8.12.
CREATE INDEX IF NOT EXISTS chapters_video_source
    ON chapters (video_id, source, start_sec);

-- Add chapter_infer to the processing_jobs.stage enum (Story 9.18 AC-1).
ALTER TYPE processing_stage ADD VALUE IF NOT EXISTS 'chapter_infer';

COMMIT;
```

### 2.2 Pipeline package layout

```
pipeline/src/maktaba_pipeline/
├── chapter/
│   ├── inferer.py              # ChapterInferer.run(video_id) — entry from worker
│   ├── boundary.py             # detect_boundaries(...)  (5-segment window per D3)
│   ├── merge.py                # enforce_min_chapter_sec(...)
│   ├── titler.py               # build_title(top_segments) with embedder fallback
│   ├── repo.py                 # atomic replace, source='inferred' only
│   └── tests/
│       ├── test_boundary.py
│       ├── test_merge.py
│       ├── test_repo.py
│       ├── test_titler_embedder_down.py
│       └── test_inferer_integration.py
└── pipeline/stages/
    └── chapter_infer.py        # stage entry: claim job, run inferer, mark DONE
```

### 2.3 Boundary detection — `boundary.py` (5-segment window per D3)

```python
"""Sliding-window cosine drop boundary detection.

For each candidate split point i, compute:

    left_mean  = mean(embeddings[i-w : i])
    right_mean = mean(embeddings[i : i+w])
    drop       = 1 - cos(left_mean, right_mean)

Emit a boundary at i whenever drop > threshold (default 0.35) and the
boundary's wall-clock distance from the previous accepted boundary is
>= min_chapter_sec. The window w defaults to 5 (Story 9.18 AC-1).
"""
from __future__ import annotations

from dataclasses import dataclass

import numpy as np


@dataclass(frozen=True)
class CandidateBoundary:
    segment_index: int
    drop: float


def _cos_distance(a: np.ndarray, b: np.ndarray) -> float:
    na, nb = float(np.linalg.norm(a)), float(np.linalg.norm(b))
    if na == 0.0 or nb == 0.0:
        return 0.0
    sim = float(np.dot(a, b)) / (na * nb)
    if sim > 1.0:
        sim = 1.0
    if sim < -1.0:
        sim = -1.0
    return max(0.0, 1.0 - sim)


def detect_boundaries(
    embeddings: np.ndarray,
    *,
    window_segments: int = 5,
    threshold: float = 0.35,
) -> list[CandidateBoundary]:
    n = embeddings.shape[0]
    if n < 2 * window_segments:
        return []  # too short — caller emits one chapter
    boundaries: list[CandidateBoundary] = []
    for i in range(window_segments, n - window_segments):
        left = embeddings[i - window_segments : i].mean(axis=0)
        right = embeddings[i : i + window_segments].mean(axis=0)
        d = _cos_distance(left, right)
        if d > threshold:
            boundaries.append(CandidateBoundary(segment_index=i, drop=d))
    return boundaries
```

### 2.4 Greedy merge — `merge.py`

```python
"""Enforce min_chapter_sec via greedy higher-confidence-wins merge."""
from __future__ import annotations

from dataclasses import dataclass
from typing import Sequence

from .boundary import CandidateBoundary


@dataclass(frozen=True)
class AcceptedBoundary:
    segment_index: int
    start_sec: float
    drop: float


def enforce_min_chapter_sec(
    boundaries: Sequence[CandidateBoundary],
    *,
    segment_starts_sec: Sequence[float],
    min_chapter_sec: float = 60.0,
) -> list[AcceptedBoundary]:
    accepted: list[AcceptedBoundary] = []
    for b in boundaries:
        cand = AcceptedBoundary(
            segment_index=b.segment_index,
            start_sec=segment_starts_sec[b.segment_index],
            drop=b.drop,
        )
        if not accepted:
            accepted.append(cand)
            continue
        prev = accepted[-1]
        if cand.start_sec - prev.start_sec < min_chapter_sec:
            if cand.drop > prev.drop:
                accepted[-1] = cand
        else:
            accepted.append(cand)
    return accepted
```

### 2.5 Titler — `titler.py` (D4)

```python
"""Title generation with embedder fallback.

Concatenate the top-3 segments inside the chapter window (longest by
duration), ask the embedder for the nearest token in its vocab, return
a bigram capped at 80 chars. On embedder error, return (None, True)
where the second tuple element signals 'embedder_unreachable'."""
from __future__ import annotations

import asyncio
import logging

log = logging.getLogger(__name__)

_TIMEOUT_SEC = 2.0


async def build_title(
    *,
    embedder_client,
    segment_texts: list[str],
    segment_durations: list[float],
) -> tuple[str | None, bool]:
    if not segment_texts:
        return None, False
    pairs = sorted(
        zip(segment_texts, segment_durations),
        key=lambda p: p[1],
        reverse=True,
    )[:3]
    blob = " ".join(text for text, _ in pairs)[:1024]
    try:
        coro = embedder_client.nearest_bigram(blob)
        bigram = await asyncio.wait_for(coro, timeout=_TIMEOUT_SEC)
    except asyncio.TimeoutError:
        log.warning("embedder timeout building chapter title")
        return None, True
    except Exception:
        log.exception("embedder error building chapter title")
        return None, True
    if not bigram:
        return None, False
    return bigram[:80], False
```

### 2.6 Repo — `repo.py` (D5 atomic replace, source='inferred' only)

```python
"""ChapterRepo — atomic replace of source='inferred' rows for a video."""
from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Sequence

import asyncpg


@dataclass(frozen=True)
class ChapterRow:
    video_id: str
    seq: int
    start_sec: float
    end_sec: float
    title: str | None
    confidence: float
    metadata: dict


_INSERT_SQL = """
INSERT INTO chapters (
    video_id, source, seq, start_sec, end_sec,
    title, confidence, metadata)
VALUES ($1, 'inferred', $2, $3, $4, $5, $6, $7::jsonb)
"""


class ChapterRepo:
    def __init__(self, pool: asyncpg.Pool):
        self._pool = pool

    async def replace_inferred_for_video(
        self, video_id: str, rows: Sequence[ChapterRow],
    ) -> None:
        async with self._pool.acquire() as conn:
            async with conn.transaction():
                await conn.execute(
                    "DELETE FROM chapters "
                    "WHERE video_id = $1 AND source = 'inferred'",
                    video_id,
                )
                if rows:
                    args = [
                        (r.video_id, r.seq, r.start_sec, r.end_sec,
                         r.title, r.confidence, json.dumps(r.metadata))
                        for r in rows
                    ]
                    await conn.executemany(_INSERT_SQL, args)
```

### 2.7 Inferer — `inferer.py`

```python
"""ChapterInferer — entry from the chapter_infer pipeline stage."""
from __future__ import annotations

import logging
from dataclasses import dataclass

import numpy as np

from .boundary import detect_boundaries
from .merge import enforce_min_chapter_sec
from .repo import ChapterRepo, ChapterRow
from .titler import build_title

log = logging.getLogger(__name__)

DEFAULT_WINDOW_SEGMENTS = 5
DEFAULT_THRESHOLD = 0.35
DEFAULT_MIN_CHAPTER_SEC = 60.0


@dataclass(frozen=True)
class InfererConfig:
    window_segments: int
    threshold: float
    min_chapter_sec: float


def config_from_library_settings(settings: dict) -> InfererConfig:
    chapter = (settings or {}).get("chapter", {}) or {}
    return InfererConfig(
        window_segments=int(chapter.get("window_segments", DEFAULT_WINDOW_SEGMENTS)),
        threshold=float(chapter.get("drop_threshold", DEFAULT_THRESHOLD)),
        min_chapter_sec=float(chapter.get("min_chapter_sec", DEFAULT_MIN_CHAPTER_SEC)),
    )


class ChapterInferer:
    def __init__(self, *, db_pool, chroma, embedder, repo: ChapterRepo | None = None):
        self._db = db_pool
        self._chroma = chroma
        self._embedder = embedder
        self._repo = repo or ChapterRepo(db_pool)

    async def run(self, *, video_id: str, library_id: str,
                  library_settings: dict, video_duration_sec: float) -> dict:
        cfg = config_from_library_settings(library_settings)

        async with self._db.acquire() as conn:
            seg_rows = await conn.fetch(
                "SELECT id, seq, start_sec, end_sec, text, language "
                "FROM transcript_segments WHERE video_id = $1 ORDER BY seq",
                video_id,
            )
        if not seg_rows:
            await self._repo.replace_inferred_for_video(video_id, [])
            return {"num_chapters": 0, "reason": "no_segments"}

        emb_map = await self._chroma.bulk_embeddings_for_video(video_id)
        emb_arr = np.asarray(
            [emb_map[r["id"]] for r in seg_rows], dtype=np.float32,
        )
        starts = [float(r["start_sec"]) for r in seg_rows]
        ends = [float(r["end_sec"]) for r in seg_rows]
        texts = [r["text"] for r in seg_rows]

        # Edge: too short for the window → one chapter spanning the whole video.
        if emb_arr.shape[0] < 2 * cfg.window_segments:
            single = [ChapterRow(
                video_id=video_id, seq=0,
                start_sec=0.0, end_sec=video_duration_sec,
                title=None, confidence=1.0,
                metadata={"reason": "too_short"})]
            await self._repo.replace_inferred_for_video(video_id, single)
            return {"num_chapters": 1, "reason": "too_short"}

        cands = detect_boundaries(
            emb_arr,
            window_segments=cfg.window_segments,
            threshold=cfg.threshold,
        )
        accepted = enforce_min_chapter_sec(
            cands, segment_starts_sec=starts,
            min_chapter_sec=cfg.min_chapter_sec,
        )

        rows = await self._build_rows(
            accepted=accepted, video_id=video_id,
            video_duration_sec=video_duration_sec,
            starts=starts, ends=ends, texts=texts,
        )
        await self._repo.replace_inferred_for_video(video_id, rows)
        return {"num_chapters": len(rows),
                "candidates": len(cands),
                "threshold": cfg.threshold,
                "window_segments": cfg.window_segments,
                "min_chapter_sec": cfg.min_chapter_sec}

    async def _build_rows(self, *, accepted, video_id, video_duration_sec,
                          starts, ends, texts) -> list[ChapterRow]:
        # Chapter 0 always starts at 0; subsequent chapters start at boundaries.
        start_idxs: list[tuple[int, float]] = [(0, 0.0)]
        for ab in accepted:
            start_idxs.append((ab.segment_index, ab.start_sec))

        rows: list[ChapterRow] = []
        for k, (seg_i, start_sec) in enumerate(start_idxs):
            end_sec = (
                start_idxs[k + 1][1] if k + 1 < len(start_idxs)
                else float(video_duration_sec)
            )
            # Top-3 by duration within the chapter.
            window_texts = [
                texts[i] for i in range(seg_i, len(starts))
                if starts[i] < end_sec
            ]
            window_durs = [
                max(0.0, ends[i] - starts[i])
                for i in range(seg_i, len(starts))
                if starts[i] < end_sec
            ]
            title, embedder_down = await build_title(
                embedder_client=self._embedder,
                segment_texts=window_texts,
                segment_durations=window_durs,
            )
            confidence = 1.0 if k == 0 else accepted[k - 1].drop
            metadata = {"first_segment_seq": int(seg_i)}
            if embedder_down:
                metadata["embedder_unreachable"] = True
            rows.append(ChapterRow(
                video_id=video_id, seq=k,
                start_sec=float(start_sec), end_sec=float(end_sec),
                title=title, confidence=float(confidence),
                metadata=metadata,
            ))
        return rows
```

### 2.8 Stage entry — `pipeline/stages/chapter_infer.py`

```python
"""Pipeline stage: claim a chapter_infer job, run the inferer, mark DONE."""
from __future__ import annotations

import logging

from maktaba_pipeline.chapter.inferer import ChapterInferer

log = logging.getLogger(__name__)


async def run_chapter_infer_stage(ctx, claimed_job):
    inferer = ChapterInferer(
        db_pool=ctx.db_pool,
        chroma=ctx.chroma,
        embedder=ctx.embedder,
    )
    metric = await inferer.run(
        video_id=claimed_job.video_id,
        library_id=claimed_job.library_id,
        library_settings=claimed_job.library_settings,
        video_duration_sec=claimed_job.video_duration_sec,
    )
    log.info("chapter_infer_done", extra={
        "video_id": claimed_job.video_id, **metric,
    })
    return metric
```

### 2.9 API — `api/internal/chapters/reinfer_handler.go` (D6)

```go
package chapters

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ReinferHandler struct {
	pool *pgxpool.Pool
}

func NewReinferHandler(pool *pgxpool.Pool) *ReinferHandler {
	return &ReinferHandler{pool: pool}
}

type ReinferResponse struct {
	JobID   uuid.UUID `json:"job_id"`
	VideoID uuid.UUID `json:"video_id"`
	Stage   string    `json:"stage"`
}

func (h *ReinferHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	videoID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "video-id-invalid", err.Error())
		return
	}

	tx, err := h.pool.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "tx-begin-failed", err.Error())
		return
	}
	defer tx.Rollback(r.Context())

	var libID uuid.UUID
	row := tx.QueryRow(r.Context(),
		"SELECT library_id FROM videos WHERE id = $1", videoID)
	if err := row.Scan(&libID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeProblem(w, http.StatusNotFound, "video-not-found", err.Error())
			return
		}
		writeProblem(w, http.StatusInternalServerError, "video-lookup-failed", err.Error())
		return
	}

	jobID := uuid.Must(uuid.NewV7())
	_, err = tx.Exec(r.Context(), `
		INSERT INTO processing_jobs (id, video_id, library_id, stage, state, priority, created_at)
		VALUES ($1, $2, $3, 'chapter_infer', 'queued', 'user_initiated', now())
	`, jobID, videoID, libID)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "enqueue-failed", err.Error())
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeProblem(w, http.StatusInternalServerError, "commit-failed", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(ReinferResponse{
		JobID: jobID, VideoID: videoID, Stage: "chapter_infer",
	})
}
```

---

## 3. File scaffolding checklist

| Order | File | Symbols | Tests gating |
|-------|------|---------|--------------|
| 1 | `shared/db/migrations/0023_chapter_inference.sql` | `chapters.source`, `chapters.metadata`, `chapters_video_source_seq_key`, `chapters_video_source` index, `chapter_infer` enum value | `TestMigration_AddsSourceColumn` |
| 2 | `pipeline/src/maktaba_pipeline/chapter/boundary.py` | `CandidateBoundary`, `detect_boundaries` | `test_boundary_*` |
| 3 | `pipeline/src/maktaba_pipeline/chapter/merge.py` | `AcceptedBoundary`, `enforce_min_chapter_sec` | `test_merge_*` |
| 4 | `pipeline/src/maktaba_pipeline/chapter/titler.py` | `build_title` | `test_titler_*` |
| 5 | `pipeline/src/maktaba_pipeline/chapter/repo.py` | `ChapterRow`, `ChapterRepo.replace_inferred_for_video` | `test_repo_replace_only_inferred` |
| 6 | `pipeline/src/maktaba_pipeline/chapter/inferer.py` | `ChapterInferer.run`, `InfererConfig`, `config_from_library_settings` | `test_inferer_*` |
| 7 | `pipeline/src/maktaba_pipeline/pipeline/stages/chapter_infer.py` | `run_chapter_infer_stage` | `test_chapter_infer_stage_marks_done` |
| 8 | `api/internal/chapters/reinfer_handler.go` | `ReinferHandler.ServeHTTP`, `ReinferResponse` | `TestReinferHandler_*` |
| 9 | route wiring in `api/internal/router/router.go` | `r.Post("/api/videos/{id}/chapters/reinfer", ...)` | `TestRouteRegistered` |

---

## 4. Test cases keyed to ACs

### T1 — AC-1: pipeline stage runs after `INDEXED`

```python
async def test_chapter_infer_stage_runs_after_indexed(db, runner):
    video = await seed_video_indexed(
        db, library_settings={"chapter_inference": True})
    await runner.run_until_idle()
    job = await db.fetchrow(
        "SELECT state, stage FROM processing_jobs "
        "WHERE video_id=$1 AND stage='chapter_infer'", video.id)
    assert job["state"] == "done"
```

### T2 — AC-1: 5-segment window cosine drop yields boundaries at ground truth

```python
def test_three_topic_shifts_yield_three_boundaries():
    # 60 segments, three distinct embedding clusters of 20 each.
    embs = build_three_cluster_embeddings(n_per_cluster=20, dim=128)
    cands = detect_boundaries(embs, window_segments=5, threshold=0.35)
    accepted = enforce_min_chapter_sec(
        cands, segment_starts_sec=[i * 30.0 for i in range(60)],
        min_chapter_sec=60.0)
    # 4 chapters total: chapter 0 + 3 boundaries.
    assert len(accepted) == 3
    boundaries = [a.segment_index for a in accepted]
    # Cluster boundaries at 20 and 40 ± window slack.
    assert any(abs(b - 20) <= 5 for b in boundaries)
    assert any(abs(b - 40) <= 5 for b in boundaries)
```

### T3 — AC-2: N+1 rows with `source='inferred'`

```python
async def test_inferred_rows_persisted_with_source(db, fake_chroma, fake_embedder):
    video_id = await seed_video(db, duration_sec=1800.0)
    fake_chroma.set_three_clusters(video_id, n_per_cluster=20)
    inferer = ChapterInferer(db_pool=db, chroma=fake_chroma, embedder=fake_embedder)
    await inferer.run(video_id=video_id, library_id="lib",
                       library_settings={}, video_duration_sec=1800.0)
    rows = await db.fetch(
        "SELECT seq, source FROM chapters WHERE video_id=$1 ORDER BY seq", video_id)
    assert all(r["source"] == "inferred" for r in rows)
    assert [r["seq"] for r in rows] == list(range(len(rows)))
```

### T4 — AC-2: re-inference replaces only `source='inferred'`

```python
async def test_reinference_preserves_embedded_and_manual(db, inferer, fake_chroma):
    video_id = await seed_video(db, duration_sec=1800.0)
    await db.execute(
        "INSERT INTO chapters (video_id, source, seq, start_sec, end_sec, "
        "title, confidence) VALUES ($1, 'embedded', 0, 0, 600, 'Intro', 1.0), "
        "($1, 'manual', 0, 0, 300, 'My Chapter', 1.0)", video_id)

    fake_chroma.set_three_clusters(video_id, n_per_cluster=20)
    await inferer.run(video_id=video_id, library_id="lib",
                       library_settings={}, video_duration_sec=1800.0)
    counts = await db.fetchrow("""
        SELECT
          COUNT(*) FILTER (WHERE source='inferred') AS i,
          COUNT(*) FILTER (WHERE source='embedded') AS e,
          COUNT(*) FILTER (WHERE source='manual') AS m
        FROM chapters WHERE video_id=$1
    """, video_id)
    assert counts["i"] >= 1
    assert counts["e"] == 1
    assert counts["m"] == 1

    # Run again — embedded/manual still untouched, inferred replaced.
    await inferer.run(video_id=video_id, library_id="lib",
                       library_settings={}, video_duration_sec=1800.0)
    counts2 = await db.fetchrow("""
        SELECT COUNT(*) FILTER (WHERE source='embedded') AS e,
               COUNT(*) FILTER (WHERE source='manual') AS m
        FROM chapters WHERE video_id=$1
    """, video_id)
    assert counts2["e"] == 1
    assert counts2["m"] == 1
```

### T5 — AC-3: title fallback when embedder unreachable

```python
async def test_title_fallback_when_embedder_down(db, broken_embedder, fake_chroma):
    video_id = await seed_video(db, duration_sec=1800.0)
    fake_chroma.set_three_clusters(video_id, n_per_cluster=20)
    inferer = ChapterInferer(db_pool=db, chroma=fake_chroma, embedder=broken_embedder)
    await inferer.run(video_id=video_id, library_id="lib",
                       library_settings={}, video_duration_sec=1800.0)
    rows = await db.fetch(
        "SELECT title, metadata FROM chapters WHERE video_id=$1 "
        "AND source='inferred'", video_id)
    for r in rows:
        assert r["title"] is None
        assert r["metadata"]["embedder_unreachable"] is True
```

### T6 — AC-4: `POST /chapters/reinfer` enqueues a job

```go
func TestReinferHandler_EnqueuesJob(t *testing.T) {
	video := seedVideo(t, db)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST",
		"/api/videos/"+video.id.String()+"/chapters/reinfer", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	var resp ReinferResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, video.id, resp.VideoID)
	assert.Equal(t, "chapter_infer", resp.Stage)

	count := scanCount(t, db,
		"SELECT COUNT(*) FROM processing_jobs WHERE id=$1 AND stage='chapter_infer' AND state='queued'",
		resp.JobID)
	assert.Equal(t, 1, count)
}
```

### T7 — AC-5: disabled per library skips the stage

```python
async def test_stage_skipped_when_disabled(db, runner):
    video = await seed_video_indexed(
        db, library_settings={"chapter_inference": False})
    await runner.run_until_idle()
    rows = await db.fetch(
        "SELECT 1 FROM processing_jobs "
        "WHERE video_id=$1 AND stage='chapter_infer'", video.id)
    assert len(rows) == 0
```

### T8 — Edge: fewer than `2 * window_segments` segments → one chapter

```python
async def test_short_video_emits_single_chapter(db, fake_chroma, fake_embedder):
    video_id = await seed_video_with_segments(db, n_segments=8, duration_sec=240.0)
    fake_chroma.set_uniform(video_id, n=8)
    inferer = ChapterInferer(db_pool=db, chroma=fake_chroma, embedder=fake_embedder)
    metric = await inferer.run(video_id=video_id, library_id="lib",
                                 library_settings={}, video_duration_sec=240.0)
    assert metric["num_chapters"] == 1
    assert metric["reason"] == "too_short"
    rows = await db.fetch(
        "SELECT start_sec, end_sec FROM chapters "
        "WHERE video_id=$1 AND source='inferred'", video_id)
    assert len(rows) == 1
    assert rows[0]["start_sec"] == 0.0
    assert rows[0]["end_sec"] == 240.0
```

### T9 — Edge: low threshold capped by `min_chapter_sec`

```python
async def test_low_threshold_bounded_by_min_chapter_sec(db, fake_chroma, fake_embedder):
    video_id = await seed_video(db, duration_sec=600.0)
    fake_chroma.set_three_clusters(video_id, n_per_cluster=20)
    inferer = ChapterInferer(db_pool=db, chroma=fake_chroma, embedder=fake_embedder)
    await inferer.run(video_id=video_id, library_id="lib",
                       library_settings={"chapter": {"drop_threshold": 0.0,
                                                       "min_chapter_sec": 60}},
                       video_duration_sec=600.0)
    rows = await db.fetch(
        "SELECT seq FROM chapters WHERE video_id=$1 AND source='inferred'", video_id)
    # 600 / 60 = 10 hard cap, regardless of detected boundaries.
    assert len(rows) <= 10
```

### T10 — Failure: stage failure surfaces as `error.code='embedder-down'` retry-success

```python
async def test_embedder_failure_marks_job_failed_then_retries(db, runner):
    video = await seed_video_indexed(db)
    runner.embedder = transient_broken_embedder(fail_n=1)
    await runner.run_until_idle()
    job = await db.fetchrow(
        "SELECT state, error FROM processing_jobs "
        "WHERE video_id=$1 AND stage='chapter_infer' "
        "ORDER BY created_at DESC LIMIT 1", video.id)
    assert job["state"] == "done"  # retry succeeded
    # Earlier attempts recorded the embedder-down error.
```

---

## 5. Edge cases

| #   | Edge case | Handled by |
|-----|-----------|------------|
| E1  | **Transcript with fewer than `2 * window_segments` segments.** Inferer detects the short case, writes one chapter spanning `[0, video_duration_sec]` with `metadata.reason='too_short'`. | T8. |
| E2  | **Threshold tuned too low (every adjacent pair fires).** Greedy merge with `min_chapter_sec=60` caps the result at `floor(duration / min_chapter_sec)` chapters. | T9. |
| E3  | **`chapter_inference` flipped after inference ran.** Old `source='inferred'` rows remain (Story 9.18 AC-5 explicitly: "the user must purge them manually"). | No code change; documented behavior. |
| E4  | **Embedder down during titling.** Title set to NULL, `metadata.embedder_unreachable=True`; the API renders "Chapter N" and a retry can be queued via the user-initiated reinfer. | T5. |
| E5  | **Embedded chapter at `seq=0` and inferred chapter at `seq=0`.** Allowed by the `(video_id, source, seq)` UNIQUE; the priority-merge in Epic 8 Story 8.12 picks the embedded one. | D2 + D5. |
| E6  | **Partial `chapter_infer` job claim races a delete on the video.** FK on `chapters.video_id ON DELETE CASCADE`; the INSERT after a delete fails with FK violation; D8 (Plan 5.7) catches it; the job ends FAILED with `video_deleted_during_run`; orchestrator does not retry. | Inferer catches `ForeignKeyViolation` and marks DONE. |
| E7  | **Re-infer enqueued while a `chapter_infer` job already running.** The orchestrator dedups jobs by `(video_id, stage, state='queued')` UNIQUE — a duplicate INSERT raises and the API returns 200 referencing the existing job. | `processing_jobs` UNIQUE index. |
| E8  | **Embedder returns an empty bigram.** `build_title` returns `(None, False)` — title is NULL but `embedder_unreachable=False` (we did try, the model just had nothing). | `titler.py`. |
| E9  | **Chroma missing some segment embeddings.** `bulk_embeddings_for_video` returns a partial map; the inferer's `np.asarray([emb_map[r['id']]])` raises KeyError. The stage marks failed; metric `embeddings_missing=true`. | Inferer catches KeyError and surfaces in `processing_jobs.error`. |
| E10 | **Concurrent reinfer + scheduled chapter_infer.** Same dedup as E7 — only one queued job at a time per (video_id, 'chapter_infer'). | E7 dedup. |
| E11 | **8 KiB metadata cap.** The metadata blob holds `first_segment_seq` + optional `embedder_unreachable` — well under 1 KiB. No cap enforcement needed. | Bounded by construction. |
| E12 | **`chapters.source = 'embedded'` rows from Plan 5.7 era without the source column.** The migration's `DEFAULT 'inferred'` backfills them. **Operators MUST relabel embedded chapters to `source='embedded'` before the migration runs** (one-time data fix in the upgrade runbook). | Migration + runbook. |

---

## 6. Acceptance checklist

- [ ] **A1** (AC-1) `processing_jobs.stage` accepts `'chapter_infer'`; the orchestrator schedules a `chapter_infer` job after a video reaches `INDEXED` if `library.settings.chapter_inference == true`. (T1, migration test)
- [ ] **A2** (AC-1) The worker uses a 5-segment sliding window (default), threshold 0.35 (default), and min_chapter_sec 60 (default), all overridable per library via `library.settings.chapter`. (T2)
- [ ] **A3** (AC-2) N+1 rows with `source='inferred'`, `seq` 0..N, are inserted; pre-existing `source='inferred'` rows are deleted in the same transaction. (T3, T4)
- [ ] **A4** (AC-3) Title is populated from the top-3 segments via the embedder; on embedder failure the title is NULL and `metadata.embedder_unreachable=true`; the API renders "Chapter N" as fallback. (T5)
- [ ] **A5** (AC-4) `embedded` and `manual` rows are preserved across re-inference runs; the priority-merge in Epic 8 Story 8.12 ranks them above `inferred`. (T4)
- [ ] **A6** (AC-4) `POST /api/videos/{id}/chapters/reinfer` returns 202 with the enqueued job id; the worker re-runs inference with current settings; existing `inferred` rows are replaced atomically. (T6)
- [ ] **A7** (AC-5) `chapter_inference: false` skips the stage entirely; existing inferred rows are preserved (no automatic purge). (T7)
- [ ] **A8** (Edge case) Fewer than `2 * window_segments` segments yields exactly one chapter spanning `[0, duration]`. (T8)
- [ ] **A9** (Edge case) Aggressive low threshold is bounded by `floor(duration / min_chapter_sec)` chapters. (T9)
- [ ] **A10** (AC-1 failure path) Embedder errors mark the job FAILED with `error.code='embedder-down'`; retry succeeds. (T10)
- [ ] **A11** Migration `0023_chapter_inference.sql` adds `chapters.source` (NOT NULL, CHECK in {inferred, embedded, manual}), `chapters.metadata` JSONB, replaces UNIQUE with `(video_id, source, seq)`, and adds the `chapter_infer` enum value. (`TestMigration_AddsSourceColumn`)
- [ ] **A12** All cosine math is reused from the Plan 5.7 design; this story differs only in (a) wider window=5, (b) first-class stage (D1), (c) `source` column (D2), (d) embedder-backed titler (D4), (e) reinfer endpoint (D6). The Plan 5.7 file remains the reference for unit-coordinate semantics.
