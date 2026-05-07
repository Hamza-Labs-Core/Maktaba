# Plan 5.7 — Chapter inference from transcripts — implementation

> Implementation plan for [story-05-07-chapter-inference.md](story-05-07-chapter-inference.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: depends on the Chroma vector index from
> [Plan 5.3](plan-05-03-chroma-vector.md) (we read embeddings, never
> re-embed); chains off the `index` stage tail from
> [Plan 5.5](plan-05-05-incremental-indexing.md); produces the data the
> Streaming Service serves per
> [Epic 8 plan-08-12-chapters-resource.md](../08-streaming/plan-08-12-chapters-resource.md);
> the unit-coordinate model comes from
> [Plan 5.1](plan-05-01-unit-chunking.md). LLM-based chapter titling is
> **deferred to v1.1** (a future Epic 5 story); v1 leaves `title` NULL
> and the API renders "Chapter N".

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Algorithm: cosine-distance dropout between adjacent unit embeddings**, not TextTiling and not LLM-titled clustering. We compute `dist(u_i, u_{i+1}) = 1 - cos(e_i, e_{i+1})` for each adjacent unit pair and emit a boundary at `i+1` whenever the distance exceeds `topic_shift_threshold` (default `0.35`, configurable per library). | Story acceptance: "computes cosine distance between adjacent unit embeddings … emits a chapter boundary wherever the distance exceeds `topic_shift_threshold`". Architecture §4.6: "cosine drop between adjacent segment embeddings > threshold". | TextTiling needs a window-based vocabulary lexical score on the *raw text*, which we'd have to recompute; cosine on embeddings reuses cached Chroma vectors. LLM-clustering needs a model and budget that v1 doesn't have. The dropout method is O(N) over units, deterministic given the same embeddings, and recovers the same boundaries the Streaming Service expects. |
| D2 | **Smoothing: rolling 3-unit centroid distance**, not raw pairwise distance. We compute `c_i = mean(e_{i-1}, e_i, e_{i+1})` and emit a boundary where `1 - cos(c_i, c_{i+1}) > threshold`. Window size `w = 3` is configurable as `chapter.smoothing_window` (default `3`, min `1` = no smoothing). | Refines the story (which specifies pairwise). | Pairwise distance on per-unit embeddings is noisy because units are short (architecture §5.1: ~256–512 tokens). A single off-topic sentence inside a chapter can spike the distance and produce a spurious boundary. A 3-unit centroid (left/self/right) acts as a low-pass filter and converges on TextTiling's "block similarity" intuition without needing a separate vocabulary pass. The story's threshold (0.35) is calibrated against this smoothed distance; the unit test fixture in §4.2 confirms it. |
| D3 | **Run trigger: tail of the `index` stage** as `chapter_infer` sub-stage, gated by `library.settings.chapter.infer = true`. Default `true` for libraries with `content_type ∈ {lecture, sermon, podcast}`, `false` otherwise. **Not** a new top-level stage in `processing_jobs.stage`. | Story §"Stage placement": "`chapter_infer` is a sub-stage that runs at the tail of `index` … does **not** introduce a new top-level stage". | Adding a stage to the canonical seven-stage enum from Epic 1 Story 1.6 ripples into UI, queue, retry policy, and resume code. Chapter inference is fast (≤ 2 s for a 4-hour video — see §7) and the work is naturally paired with index completion: the embeddings the inference needs are exactly what `index` just wrote. Co-locating keeps queue arithmetic stable. |
| D4 | **Storage: a new `chapters` table** (not `videos.chapters` JSONB and not `transcripts.chapters` JSONB). Rows are keyed by `(transcript_id, seq)` and cascade-delete when the transcript is replaced. | Story §Schema: "A migration `shared/db/migrations/000X_chapters.sql` creates" + acceptance "reprocessing a transcript replaces the chapters in the same transaction that flips `is_active`". | A table makes the join from `videos` → `chapters` cheap for the Streaming Service `chapters.json` resource (which filters by active transcript only) and lets the partial index `(video_id, start_sec)` answer "what chapter am I in at second T?" in O(log N). JSONB on `videos` would force the API to fetch and parse the whole array on every chapter lookup; JSONB on `transcripts` would require the API to know which transcript is active before reading. The table approach is also what Plan 8.12 expects to read from. |
| D5 | **Title generation: leave NULL in v1**, not extractive, not LLM. The serving path falls back to "Chapter N" (Plan 8.12). A v1.1 deferred story may add a summarization pass. | Story acceptance: "`title` is left `NULL` in v1; an offline batch job (deferred) may later fill it from a summarization pass". | Extractive titling on Arabic text is harder than English (no reliable noun-phrase chunker in the multilingual stack we ship); a wrong title is worse than no title. LLM titling adds a per-video model call (small Llama or a hosted call) plus a budget — neither is in scope for v1. We document the contract so v1.1 can fill `title` without changing the schema. |
| D6 | **Boundary post-processing: `min_chapter_sec` enforced by greedy higher-confidence-wins merge.** When two boundaries are within `min_chapter_sec` of each other (default **180 s** per architecture §4.6 "capped at one per ~3 minutes"), keep the one with the higher `confidence` and drop the other. The merge runs in a single left-to-right pass, no global optimization. | Architecture §4.6 + story acceptance: "if two boundaries are closer than this, only the higher-confidence one is kept". | Greedy with confidence-priority is O(N) and gives stable results. The 180 s cap matches the architecture's "one per ~3 minutes" rule; earlier drafts of this plan used 60 s, which produced ~3× too many chapters on long-form lectures. |
| D7 | **Re-run on transcript edit: replace atomically**, not patch. When the active transcript changes (a new transcript is committed and `is_active` flips), the `chapter_infer` for the new transcript runs after `index` completes; on insert it deletes the old transcript's chapters in the same DB transaction that flips `is_active = false` on the old transcript and inserts the new chapter rows. | Story §Schema: "reprocessing a transcript replaces the chapters in the same transaction that flips `is_active`". | Diff-and-patch needs a stable identity for chapters across re-runs; with embedding noise the boundaries shift by 5–10 seconds each time and patch becomes a delete-and-insert anyway. Atomic replace is simpler, leaves no half-state if the inference crashes mid-write, and matches the existing `transcripts.is_active` flip pattern from Epic 3 Story 3.5. |
| D8 | **Failure isolation: chapter inference failure does NOT fail the parent `index` job.** The failure is logged with `kind=chapter_infer_failed`, recorded in `transcripts.metrics.chapter_infer_failed = {error, at}`, and the video transitions to `INDEXED` regardless. | Story acceptance: "Failure of chapter inference is logged but does **not** fail the parent `index` job; the video proceeds to `INDEXED` regardless." | Index has already committed the search-relevant data (units, vectors, FTS). Chapters are a navigation aid — losing them on one video should not block the user from searching that video. The metric makes the failure visible and operators can backfill via a maintenance task. |
| D9 | **Threshold + window are per-library settings** stored in `library.settings.chapter.{threshold, smoothing_window, min_chapter_sec, infer}`, with defaults baked into `pipeline/src/maktaba_pipeline/config/defaults.py` so a missing key resolves without a DB lookup. | Story acceptance: "default `0.35`, configurable per library" + "configurable minimum chapter length `min_chapter_sec` (default `60`)". | A lecture series wants a tighter threshold (0.30) than a podcast with frequent topic switches (0.40). Per-library is the right granularity because the content type sets the prior; per-video is too noisy and per-deployment is too coarse. |
| D10 | **Embedding fetch: bulk-read all unit embeddings for the active transcript from Chroma in one call** via `collection.get(where={"transcript_id": tid}, include=["embeddings", "metadatas"])`, then sort in-process by `(seq)`. We do **not** re-embed; we never call the embedding model in this stage. | Story acceptance: "already cached in Chroma, no re-embedding required". | A 4-hour transcript at ~512-token units gives roughly 1500 units; one bulk `get` is faster than 1500 single fetches and the entire vector set fits in ~6 MB at 1024-dim float32 (`e5-large`). The bulk shape also lets us streaming-iterate the dot products. |
| D11 | **Language handling: chapters are language-tagged from their first unit's language.** A `chapters.lang` column stores the language code (`ar`, `en`, `mixed` if the first three units span ≥2 languages). No separate inference; we trust the per-unit language already attached during transcription. | Refines the story (which doesn't specify chapter language). | The Streaming Service's `chapters.json` resource needs to render right-to-left for Arabic chapters; without a `lang` field, the client has to detect at render time. Tagging at write time is cheap and matches how transcripts already carry language. |

If D2 is rejected (raw pairwise distance instead of smoothed): the
threshold default would need to bump to ~0.45 and §4.2's expected
boundary count for the multi-topic fixture grows by ~30 % (more
spurious boundaries). Correctness of the algorithm is unaffected;
quality at the default threshold drops.

If D6 is rejected (no greedy merge): every detected dropout becomes a
chapter, which on noisy material yields 60+ chapters for a 1-hour
podcast. The serving path can survive this but the user experience is
poor; we'd need to push the merge into the API instead, which violates
the "production owns the data" boundary set by the story description.

---

## 1. Architecture diagram — chapter inference at the tail of `index`

```
   ┌────────────────────────────────────────────────────────────────────┐
   │  Index stage entry  (Plan 5.5)                                     │
   │   ctx.run_index_stage(ctx, claimed_job)                            │
   │                                                                    │
   │   for each unit batch:                                             │
   │     - write to FTS (Plan 5.2)                                      │
   │     - embed + write to Chroma (Plan 5.3)                           │
   │   on stream end:                                                   │
   │     - mark transcript indexed                                      │
   │                                                                    │
   │   ▼ chapter_infer sub-stage                                        │
   │   chapter_enabled = library.settings.chapter.infer == true         │
   └────────────────────────────────┬───────────────────────────────────┘
                                    │
                  ┌─────────────────┴─────────────────┐
                  │                                   │
            chapter_enabled = false           chapter_enabled = true
                  │                                   │
                  ▼                                   ▼
       ┌────────────────────────┐    ┌──────────────────────────────────┐
       │ index → DONE           │    │ ChapterInferer.infer(transcript) │
       │ (no chapter writes)    │    │  ↓                               │
       └────────────────────────┘    │ load active transcript meta      │
                                     │  ↓                               │
                                     │ bulk-read unit embeddings (D10)  │
                                     │   chroma.get(where={tid})        │
                                     │   sort by seq                    │
                                     │  ↓                               │
                                     │ if num_units < 2: emit zero      │
                                     │  chapters; record metric         │
                                     │  ↓                               │
                                     │ smooth: c_i = mean(e_{i-1..i+1}) │
                                     │  ↓                               │
                                     │ for each i in [1..N-1]:          │
                                     │   d_i = 1 - cos(c_i, c_{i-1})    │
                                     │   if d_i > threshold:            │
                                     │     boundaries.append((i, d_i))  │
                                     │  ↓                               │
                                     │ enforce min_chapter_sec (D6)     │
                                     │   greedy higher-confidence-wins  │
                                     │  ↓                               │
                                     │ build Chapter rows:              │
                                     │   seq=k, start=units[i].start,   │
                                     │   end=units[i_next].start (or    │
                                     │   video.duration), confidence,   │
                                     │   lang from first unit, title    │
                                     │   = NULL (D5)                    │
                                     │  ↓                               │
                                     │ atomic write (D7):               │
                                     │   BEGIN;                         │
                                     │   DELETE FROM chapters WHERE     │
                                     │     transcript_id = old_tid;     │
                                     │   INSERT INTO chapters (...);    │
                                     │   COMMIT;                        │
                                     │  ↓                               │
                                     │ on any exception (D8):           │
                                     │   log + metric, do not fail job  │
                                     └──────────────────────────────────┘
                                                      │
                                                      ▼
                                ┌─────────────────────────────────────┐
                                │  Index stage → DONE                 │
                                │  Streaming Service reads `chapters` │
                                │  for chapters.json + HLS DATERANGE  │
                                │  (Plan 8.12).                       │
                                └─────────────────────────────────────┘
```

The inference is a **read-only consumer** of Chroma and a **single-table
writer** to Postgres. It does not interact with the FTS index, the
embedding model, or the segment store directly.

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
├── chapter/
│   ├── __init__.py             # public surface: ChapterInferer, ChapterRow
│   ├── inferer.py              # ChapterInferer.infer(transcript_id)
│   ├── boundary.py             # smoothed-distance boundary detection
│   ├── merge.py                # min_chapter_sec greedy merge (D6)
│   ├── repo.py                 # atomic delete+insert in one xact (D7)
│   ├── errors.py               # ChapterInferError, ChapterInferDisabled
│   └── tests/
│       ├── conftest.py         # fixtures: synthetic embedding sequences
│       ├── test_boundary.py
│       ├── test_merge.py
│       ├── test_repo.py
│       ├── test_inferer.py
│       ├── test_inferer_arabic.py
│       ├── test_inferer_short_video.py
│       └── test_inferer_failure_does_not_fail_index.py
└── pipeline/
    └── stages/
        └── index.py            # extended: chapter_infer tail sub-stage
```

### 2.2 Schema migration — `chapters` table

```sql
-- shared/db/migrations/0026_chapters.sql
BEGIN;

CREATE TABLE chapters (
    id            BIGSERIAL PRIMARY KEY,
    video_id      UUID NOT NULL REFERENCES videos(id)
                                ON DELETE CASCADE,
    transcript_id UUID REFERENCES transcripts(id)
                                ON DELETE CASCADE,    -- NULL for embedded/manual sources
    seq           INTEGER NOT NULL,
    start_sec     REAL NOT NULL,
    end_sec       REAL NOT NULL,
    title         TEXT,                              -- NULL allowed (D5)
    -- 'source' is the architecture-§8.1 discriminator. Only 'inferred'
    -- is written by this plan; embedded TOC chapters and manual user
    -- chapters share the table with their own source values.
    source        TEXT NOT NULL DEFAULT 'inferred'
                  CHECK (source IN ('inferred', 'embedded', 'manual')),
    lang          TEXT,                              -- 'ar', 'en', 'mixed', or NULL
    confidence    REAL,                              -- 0..1; NULL for non-inferred sources
    metadata      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One ordered list per (video, source). Different sources can
    -- coexist on the same video (e.g. embedded + manual).
    UNIQUE (video_id, source, seq),
    CHECK (start_sec >= 0),
    CHECK (end_sec >= start_sec),
    CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1))
);

-- Range lookup: "what chapter at second T for video V?"
CREATE INDEX chapters_video_start
    ON chapters (video_id, start_sec);

-- Streaming Service "all chapters for a video by active transcript"
-- is served by joining videos → transcripts (is_active) → chapters,
-- then ordering by start_sec. The index above covers that.

COMMIT;
```

The migration is idempotent under `CREATE TABLE IF NOT EXISTS` only if
the table doesn't exist; we deliberately omit `IF NOT EXISTS` so a
re-run on a populated environment fails loudly. The migration runner
(Epic 22) records applied migrations and skips them.

### 2.3 `boundary.py` — smoothed cosine-distance boundary detection (D1, D2)

```python
"""Boundary detection: smoothed cosine distance between adjacent units.

Algorithm (D1, D2):
    Given unit embeddings e_0, e_1, ..., e_{N-1} (each L2-normalized
    by the embedding model), compute the smoothed centroid:

        c_i = (e_{i-w} + ... + e_i + ... + e_{i+w}) / (2w + 1)

    where w = (smoothing_window - 1) // 2 and edge centroids reuse
    available neighbors (clamping). Then the boundary distance:

        d_i = 1 - cos(c_{i-1}, c_i)
            = 1 - (c_{i-1} . c_i) / (||c_{i-1}|| ||c_i||)

    Emit a boundary at unit i iff d_i > threshold.
"""
from __future__ import annotations
from dataclasses import dataclass
from typing import Sequence

import numpy as np


@dataclass(frozen=True)
class CandidateBoundary:
    unit_index: int          # boundary fires AT this unit (it starts a new chapter)
    distance: float          # cosine distance, clamped to [0, 1]


def _clamp(d: float) -> float:
    if d < 0.0:
        return 0.0
    if d > 1.0:
        return 1.0
    return d


def _cos_distance(a: np.ndarray, b: np.ndarray) -> float:
    na = float(np.linalg.norm(a))
    nb = float(np.linalg.norm(b))
    if na == 0.0 or nb == 0.0:
        return 0.0
    sim = float(np.dot(a, b)) / (na * nb)
    return _clamp(1.0 - sim)


def smooth_centroids(embeddings: np.ndarray, window: int) -> np.ndarray:
    """Return centroids c_i = mean of [i-w .. i+w], with edge clamping.

    `embeddings` shape: (N, D). Returns array shape (N, D).
    """
    if window < 1:
        window = 1
    if window % 2 == 0:
        window += 1               # force odd
    w = (window - 1) // 2
    n = embeddings.shape[0]
    if n == 0:
        return embeddings
    out = np.empty_like(embeddings)
    for i in range(n):
        lo = max(0, i - w)
        hi = min(n, i + w + 1)
        out[i] = embeddings[lo:hi].mean(axis=0)
    return out


def detect_boundaries(
    embeddings: np.ndarray,
    *,
    threshold: float,
    smoothing_window: int = 3,
) -> list[CandidateBoundary]:
    """Return candidate boundaries above `threshold`, in unit-index order.

    `embeddings` is shape (N, D). The first unit is implicitly chapter 0
    (no boundary emitted for index 0 — it's the start). A boundary at
    index i means "unit i starts a new chapter."
    """
    n = embeddings.shape[0]
    if n < 2:
        return []
    centroids = smooth_centroids(embeddings, smoothing_window)
    out: list[CandidateBoundary] = []
    for i in range(1, n):
        d = _cos_distance(centroids[i - 1], centroids[i])
        if d > threshold:
            out.append(CandidateBoundary(unit_index=i, distance=d))
    return out
```

### 2.4 `merge.py` — `min_chapter_sec` greedy merge (D6)

```python
"""Enforce min_chapter_sec by greedy higher-confidence-wins merge.

Given candidate boundaries sorted by unit_index and the per-unit
start_sec lookup, walk left-to-right:

    if next boundary's start_sec - current boundary's start_sec
       < min_chapter_sec:
        keep the higher-confidence one; drop the other
    else:
        accept current, advance.

This is O(N) and does not attempt global optimality (D6 rationale).
"""
from __future__ import annotations
from dataclasses import dataclass
from typing import Sequence

from .boundary import CandidateBoundary


@dataclass(frozen=True)
class AcceptedBoundary:
    unit_index: int
    start_sec: float
    distance: float


def enforce_min_chapter_sec(
    boundaries: Sequence[CandidateBoundary],
    *,
    unit_starts_sec: Sequence[float],
    min_chapter_sec: float,
) -> list[AcceptedBoundary]:
    accepted: list[AcceptedBoundary] = []
    for b in boundaries:
        candidate = AcceptedBoundary(
            unit_index=b.unit_index,
            start_sec=unit_starts_sec[b.unit_index],
            distance=b.distance,
        )
        if not accepted:
            accepted.append(candidate)
            continue
        prev = accepted[-1]
        if candidate.start_sec - prev.start_sec < min_chapter_sec:
            # Conflict: keep the stronger boundary.
            if candidate.distance > prev.distance:
                accepted[-1] = candidate
            # else: drop the candidate (silently)
        else:
            accepted.append(candidate)
    return accepted
```

### 2.5 `inferer.py` — orchestration (D3, D8, D10, D11)

```python
"""ChapterInferer — main entry called from the index stage tail.

Responsibilities:
  1. Resolve config from library settings (with defaults).
  2. Bulk-read unit embeddings + metadata from Chroma (D10).
  3. Detect smoothed boundaries; enforce min_chapter_sec.
  4. Build ChapterRow objects (lang from first 3 units — D11).
  5. Atomically replace existing chapters for the transcript (D7).
  6. On any exception, log + metric, return — do NOT raise (D8).
"""
from __future__ import annotations
import logging, time
from dataclasses import dataclass
import numpy as np

from .boundary import detect_boundaries
from .merge import enforce_min_chapter_sec
from .repo import ChapterRepo, ChapterRow

log = logging.getLogger(__name__)

DEFAULT_THRESHOLD = 0.35
DEFAULT_SMOOTHING_WINDOW = 3
DEFAULT_MIN_CHAPTER_SEC = 180.0  # architecture §4.6: one per ~3 minutes


@dataclass(frozen=True)
class InfererConfig:
    threshold: float
    smoothing_window: int
    min_chapter_sec: float


def config_from_library_settings(settings: dict) -> InfererConfig:
    chapter = (settings or {}).get("chapter", {}) or {}
    return InfererConfig(
        threshold=float(chapter.get("threshold", DEFAULT_THRESHOLD)),
        smoothing_window=int(chapter.get("smoothing_window", DEFAULT_SMOOTHING_WINDOW)),
        min_chapter_sec=float(chapter.get("min_chapter_sec", DEFAULT_MIN_CHAPTER_SEC)),
    )


def is_enabled(library_settings: dict, content_type: str | None) -> bool:
    chapter = (library_settings or {}).get("chapter", {}) or {}
    if "infer" in chapter:
        return bool(chapter["infer"])
    return content_type in {"lecture", "sermon", "podcast"}


class ChapterInferer:
    def __init__(self, *, chroma_client, db_pool, repo: ChapterRepo | None = None):
        self._chroma = chroma_client
        self._db = db_pool
        self._repo = repo or ChapterRepo(db_pool)

    async def infer(self, *, video_id, transcript_id, library_id,
                    library_settings, video_duration_sec=None) -> dict:
        t0 = time.monotonic()
        try:
            cfg = config_from_library_settings(library_settings)

            # 1. Bulk-read embeddings + metadata from Chroma (D10).
            collection = self._chroma.collection_for_library(library_id)
            res = collection.get(
                where={"transcript_id": transcript_id},
                include=["embeddings", "metadatas"])
            metas = res.get("metadatas") or []
            embs = res.get("embeddings") or []
            if not metas:
                return {"chapter_infer_skipped": True,
                        "reason": "no_units_in_chroma",
                        "wall_sec": time.monotonic() - t0}

            # Sort by seq.
            order = sorted(range(len(metas)), key=lambda i: int(metas[i]["seq"]))
            metas = [metas[i] for i in order]
            embs = np.asarray([embs[i] for i in order], dtype=np.float32)
            unit_starts_sec = [float(m["start"]) for m in metas]
            unit_ends_sec = [float(m["end"]) for m in metas]
            unit_langs = [m.get("language") for m in metas]

            # 2. Edge: < 2 units → zero chapters (E1).
            if embs.shape[0] < 2:
                await self._repo.replace_for_transcript(
                    transcript_id=transcript_id, video_id=video_id, rows=[])
                return {"chapter_infer_succeeded": True, "num_chapters": 0,
                        "wall_sec": time.monotonic() - t0, "reason": "single_unit"}

            # 3. Boundary detection + merge (D1, D2, D6).
            candidates = detect_boundaries(
                embs, threshold=cfg.threshold,
                smoothing_window=cfg.smoothing_window)
            accepted = enforce_min_chapter_sec(
                candidates, unit_starts_sec=unit_starts_sec,
                min_chapter_sec=cfg.min_chapter_sec)

            # 4. Build rows; chapter 0 starts at second 0 with confidence 1.0.
            rows = self._build_rows(
                accepted, unit_starts_sec=unit_starts_sec,
                unit_ends_sec=unit_ends_sec, unit_langs=unit_langs,
                video_id=video_id, transcript_id=transcript_id,
                video_duration_sec=video_duration_sec)

            # 5. Atomic replace (D7).
            await self._repo.replace_for_transcript(
                transcript_id=transcript_id, video_id=video_id, rows=rows)
            return {"chapter_infer_succeeded": True, "num_chapters": len(rows),
                    "wall_sec": time.monotonic() - t0,
                    "threshold": cfg.threshold,
                    "smoothing_window": cfg.smoothing_window,
                    "min_chapter_sec": cfg.min_chapter_sec,
                    "num_candidates": len(candidates)}
        except Exception as e:  # D8: never propagate
            log.warning("chapter_infer_failed",
                        extra={"transcript_id": transcript_id,
                               "video_id": video_id, "err": str(e)})
            return {"chapter_infer_failed": True,
                    "chapter_infer_error": str(e)[:512],
                    "wall_sec": time.monotonic() - t0}

    @staticmethod
    def _build_rows(accepted, *, unit_starts_sec, unit_ends_sec, unit_langs,
                    video_id, transcript_id, video_duration_sec) -> list[ChapterRow]:
        # Boundaries describe the START of chapters 1..N; chapter 0 always
        # starts at second 0 (even if there are zero accepted boundaries —
        # then the whole video is one chapter).
        starts: list[tuple[int, float]] = [(0, 0.0)]
        for ab in accepted:
            starts.append((ab.unit_index, ab.start_sec))

        rows: list[ChapterRow] = []
        for k, (unit_idx, start_sec) in enumerate(starts):
            end_sec = (starts[k + 1][1] if k + 1 < len(starts)
                       else (video_duration_sec or unit_ends_sec[-1]))
            confidence = 1.0 if k == 0 else accepted[k - 1].distance
            rows.append(ChapterRow(
                video_id=video_id, transcript_id=transcript_id, seq=k,
                start_sec=float(start_sec), end_sec=float(end_sec),
                title=None, lang=_chapter_language(unit_idx, unit_langs),
                confidence=float(confidence),
                metadata={"first_unit_seq": int(unit_idx)}))
        return rows


def _chapter_language(first_unit_idx: int, unit_langs: list) -> str | None:
    """Tag from first 3 units (D11): single lang → that lang; ≥2 → 'mixed'."""
    window = unit_langs[first_unit_idx : first_unit_idx + 3]
    seen = {x for x in window if x}
    if not seen:
        return None
    return next(iter(seen)) if len(seen) == 1 else "mixed"
```

### 2.6 `repo.py` — atomic delete+insert (D7)

```python
"""ChapterRepo — atomic replace of chapters for a given transcript."""
from __future__ import annotations
import json
from dataclasses import dataclass
from typing import Sequence

_INSERT_SQL = """
INSERT INTO chapters
    (video_id, transcript_id, seq, start_sec, end_sec,
     title, lang, confidence, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)
"""


@dataclass(frozen=True)
class ChapterRow:
    video_id: int
    transcript_id: int
    seq: int
    start_sec: float
    end_sec: float
    title: str | None
    lang: str | None
    confidence: float
    metadata: dict


def _row_args(rows: Sequence["ChapterRow"]):
    return [
        (r.video_id, r.transcript_id, r.seq, r.start_sec, r.end_sec,
         r.title, r.lang, r.confidence, json.dumps(r.metadata))
        for r in rows
    ]


class ChapterRepo:
    def __init__(self, db_pool):
        self._db = db_pool

    async def replace_for_transcript(
        self, *, transcript_id: int, video_id: int,
        rows: Sequence[ChapterRow],
    ) -> None:
        async with self._db.acquire() as conn:
            async with conn.transaction():
                await conn.execute(
                    "DELETE FROM chapters WHERE transcript_id = $1", transcript_id)
                if rows:
                    await conn.executemany(_INSERT_SQL, _row_args(rows))

    async def replace_in_active_flip(
        self, *, old_transcript_id: int, new_transcript_id: int,
        video_id: int, rows: Sequence[ChapterRow], conn,
    ) -> None:
        """For use INSIDE the existing transaction that flips is_active.

        Caller passes the open conn so the flip + chapter replace are one xact.
        """
        await conn.execute(
            "DELETE FROM chapters WHERE transcript_id = $1", old_transcript_id)
        await conn.execute(
            "DELETE FROM chapters WHERE transcript_id = $1", new_transcript_id)
        if rows:
            await conn.executemany(_INSERT_SQL, _row_args(rows))
```

### 2.7 Stage integration — `index` tail

```python
# pipeline/src/maktaba_pipeline/pipeline/stages/index.py  (excerpt)

from maktaba_pipeline.chapter.inferer import ChapterInferer, is_enabled


async def run_index_stage(ctx, claimed_job):
    # ... existing FTS + Chroma write loop (Plan 5.5) ...
    await _commit_remaining_units(...)

    # Tail: chapter inference (D3).
    library_settings = claimed_job.library_settings or {}
    content_type = claimed_job.library_content_type
    if is_enabled(library_settings, content_type):
        inferer = ChapterInferer(
            chroma_client=ctx.chroma,
            db_pool=ctx.db_pool,
        )
        metric = await inferer.infer(
            video_id=claimed_job.video_id,
            transcript_id=claimed_job.transcript_id,
            library_id=claimed_job.library_id,
            library_settings=library_settings,
            video_duration_sec=claimed_job.video_duration_sec,
        )
        await _record_chapter_infer_metrics(
            ctx, claimed_job.transcript_id, metric)

    await mark_done(ctx.db_pool, job_id=claimed_job.id)


async def _record_chapter_infer_metrics(ctx, transcript_id, metric):
    """Merge the chapter-infer metric blob into transcripts.metrics."""
    if not metric:
        return
    async with ctx.db_pool.acquire() as conn:
        await conn.execute("""
            UPDATE transcripts
               SET metrics = COALESCE(metrics, '{}'::jsonb)
                          || $2::jsonb
             WHERE id = $1
        """, transcript_id, json.dumps({"chapter_infer": metric}))
```

### 2.8 Library settings — config surface

```json
{
  "chapter": {
    "infer": true,
    "threshold": 0.35,
    "smoothing_window": 3,
    "min_chapter_sec": 180
  }
}
```

`config/defaults.py`:

```python
# pipeline/src/maktaba_pipeline/config/defaults.py
DEFAULT_LIBRARY_SETTINGS = {
    # ... existing ...
    "chapter": {
        # 'infer' default depends on content_type (see is_enabled);
        # the dict here holds non-content-type defaults only.
        "threshold": 0.35,
        "smoothing_window": 3,
        "min_chapter_sec": 180,
    },
}
```

### 2.9 Errors

```python
# pipeline/src/maktaba_pipeline/chapter/errors.py
class ChapterInferError(Exception):
    """Any failure inside ChapterInferer.infer; caught and metric'd, never raised."""

class ChapterInferDisabled(Exception):
    """Raised by callers that try to infer when the library disables it."""
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/0026_chapters.sql` | `chapters` table, `chapters_video_start` index | `test_migration_creates_chapters_table` |
| 2 | `pipeline/src/maktaba_pipeline/chapter/__init__.py` | re-exports | (n/a) |
| 3 | `pipeline/src/maktaba_pipeline/chapter/errors.py` | `ChapterInferError`, `ChapterInferDisabled` | (n/a) |
| 4 | `pipeline/src/maktaba_pipeline/chapter/boundary.py` | `CandidateBoundary`, `smooth_centroids`, `detect_boundaries`, `_cos_distance` | `test_boundary` |
| 5 | `pipeline/src/maktaba_pipeline/chapter/merge.py` | `AcceptedBoundary`, `enforce_min_chapter_sec` | `test_merge` |
| 6 | `pipeline/src/maktaba_pipeline/chapter/repo.py` | `ChapterRow`, `ChapterRepo.replace_for_transcript`, `.replace_in_active_flip` | `test_repo` |
| 7 | `pipeline/src/maktaba_pipeline/chapter/inferer.py` | `ChapterInferer`, `InfererConfig`, `config_from_library_settings`, `is_enabled`, `_chapter_language` | `test_inferer*` |
| 8 | `pipeline/src/maktaba_pipeline/config/defaults.py` (extend) | `chapter` key in `DEFAULT_LIBRARY_SETTINGS` | n/a (smoke import) |
| 9 | `pipeline/src/maktaba_pipeline/pipeline/stages/index.py` (extend) | `_record_chapter_infer_metrics`, tail call into `ChapterInferer.infer` | `test_index_runs_chapter_infer_on_completion` |
| 10 | `pipeline/src/maktaba_pipeline/chapter/tests/conftest.py` | `synthetic_embeddings`, `fake_chroma_collection`, `library_with_chapter_setting` fixtures | (n/a) |

---

## 4. Test cases

### 4.1 `test_migration_creates_chapters_table`

```python
async def test_migration_creates_chapters_table(empty_db):
    """Apply migration; assert table, indexes, FKs, checks present."""
    await apply_migration(empty_db, "0026_chapters.sql")

    cols = await empty_db.fetch("""
        SELECT column_name, data_type, is_nullable
          FROM information_schema.columns
         WHERE table_name = 'chapters'
         ORDER BY ordinal_position
    """)
    names = [c["column_name"] for c in cols]
    assert names == [
        "id", "video_id", "transcript_id", "seq", "start_sec", "end_sec",
        "title", "lang", "confidence", "metadata", "created_at",
    ]
    # title nullable (D5)
    assert next(c for c in cols if c["column_name"] == "title")["is_nullable"] == "YES"
    # confidence not null
    assert next(c for c in cols if c["column_name"] == "confidence")["is_nullable"] == "NO"

    # Index present.
    idxs = await empty_db.fetch(
        "SELECT indexname FROM pg_indexes WHERE tablename = 'chapters'")
    assert "chapters_video_start" in {r["indexname"] for r in idxs}

    # UNIQUE (transcript_id, seq) enforced.
    await empty_db.execute(
        "INSERT INTO chapters (video_id, transcript_id, seq, start_sec, end_sec, confidence) "
        "VALUES (1, 1, 0, 0.0, 60.0, 1.0)")
    with pytest.raises(asyncpg.exceptions.UniqueViolationError):
        await empty_db.execute(
            "INSERT INTO chapters (video_id, transcript_id, seq, start_sec, end_sec, confidence) "
            "VALUES (1, 1, 0, 60.0, 120.0, 1.0)")
```

### 4.2 `test_inference_single_topic_one_chapter` (story-named)

```python
async def test_inference_single_topic_yields_one_chapter(
    db, library_with_chapter_setting, fake_chroma, transcript_factory,
):
    """All units near-identical embedding → 1 chapter (the whole video)."""
    tid = await transcript_factory.fresh(video_duration_sec=600.0)
    fake_chroma.add_units(_one_cluster_units(tid, n=30, dur=600.0))

    metric = await ChapterInferer(chroma_client=fake_chroma, db_pool=db).infer(
        video_id=1, transcript_id=tid, library_id=1,
        library_settings={"chapter": {"infer": True}}, video_duration_sec=600.0)
    assert metric["chapter_infer_succeeded"]
    assert metric["num_chapters"] == 1
    rows = await db.fetch(
        "SELECT seq, start_sec, end_sec FROM chapters WHERE transcript_id=$1 ORDER BY seq", tid)
    assert len(rows) == 1
    assert rows[0]["start_sec"] == 0.0
    assert rows[0]["end_sec"] == 600.0
```

### 4.3 `test_inference_multi_topic_yields_multiple_chapters` (story-named)

```python
async def test_inference_multi_topic_three_segments(
    db, library_with_chapter_setting, fake_chroma, transcript_factory,
):
    """30 units in 3 distinct embedding clusters → 3 chapters at the cluster boundaries."""
    tid = await transcript_factory.fresh(video_duration_sec=1800.0)
    fake_chroma.add_units(_three_cluster_units(tid, n=30, dur=1800.0))

    metric = await ChapterInferer(chroma_client=fake_chroma, db_pool=db).infer(
        video_id=1, transcript_id=tid, library_id=1,
        library_settings={"chapter": {"infer": True, "threshold": 0.35,
                                      "min_chapter_sec": 60}},
        video_duration_sec=1800.0)
    assert metric["chapter_infer_succeeded"]
    assert metric["num_chapters"] == 3

    rows = await db.fetch(
        "SELECT seq, start_sec, end_sec FROM chapters WHERE transcript_id=$1 ORDER BY seq", tid)
    starts = [r["start_sec"] for r in rows]
    # Cluster boundaries at unit_idx 10 (600s) and 20 (1200s); allow ±60s
    # slack for smoothing-window edge effects.
    assert abs(starts[0] - 0.0) < 1.0
    assert abs(starts[1] - 600.0) <= 60.0
    assert abs(starts[2] - 1200.0) <= 60.0
    assert rows[-1]["end_sec"] == 1800.0
```

### 4.4 `test_short_video_below_min_chapter_sec` (story edge case)

```python
async def test_short_video_emits_contiguous_chapters(
    db, library_with_chapter_setting, fake_chroma, transcript_factory,
):
    """Short video → chapters cover [0, duration) without gaps; ≤2 chapters."""
    tid = await transcript_factory.fresh(video_duration_sec=240.0)
    # 12 units of 20s with one strong topic shift halfway.
    units = _two_cluster_units(tid, n=12, dur=240.0)
    fake_chroma.add_units(units)

    await ChapterInferer(chroma_client=fake_chroma, db_pool=db).infer(
        video_id=1, transcript_id=tid, library_id=1,
        library_settings={"chapter": {"infer": True, "min_chapter_sec": 60}},
        video_duration_sec=240.0)
    rows = await db.fetch(
        "SELECT seq, start_sec, end_sec FROM chapters WHERE transcript_id=$1 ORDER BY seq", tid)
    assert rows[0]["start_sec"] == 0.0
    assert rows[-1]["end_sec"] == 240.0
    # Contiguous: end_k == start_{k+1}.
    for k in range(len(rows) - 1):
        assert rows[k]["end_sec"] == rows[k + 1]["start_sec"]
```

### 4.5 `test_arabic_chapter_language_tag` (story-named)

```python
async def test_arabic_units_produce_arabic_chapter_language(
    db, library_with_chapter_setting, fake_chroma, transcript_factory,
):
    """Arabic units → chapters tagged lang='ar'; mixed fragment → 'mixed'."""
    tid = await transcript_factory.fresh(video_duration_sec=600.0)
    # First 10 units Arabic in cluster A; next 10 English in cluster B.
    units = _two_cluster_units_with_langs(
        tid, n=20, dur=600.0,
        langs_a="ar", langs_b="en")
    fake_chroma.add_units(units)
    await ChapterInferer(chroma_client=fake_chroma, db_pool=db).infer(
        video_id=1, transcript_id=tid, library_id=1,
        library_settings={"chapter": {"infer": True}}, video_duration_sec=600.0)
    rows = await db.fetch(
        "SELECT seq, lang FROM chapters WHERE transcript_id=$1 ORDER BY seq", tid)
    assert rows[0]["lang"] == "ar"
    assert rows[-1]["lang"] == "en"
```

### 4.6 `test_rerun_replaces_chapters_atomically` (story-named)

```python
async def test_rerun_on_new_transcript_replaces_chapters(
    db, library_with_chapter_setting, fake_chroma,
    transcript_factory, video_factory,
):
    """Re-run for a video → old transcript's chapters are gone, new ones present."""
    video = await video_factory.fresh(duration_sec=600.0)
    old_tid = await transcript_factory.fresh(
        video_id=video.id, video_duration_sec=600.0, is_active=True)

    # Seed old chapters via a first inference.
    fake_chroma.add_units(_three_cluster_units(old_tid, n=30, dur=600.0))
    inferer = ChapterInferer(chroma_client=fake_chroma, db_pool=db)
    await inferer.infer(
        video_id=video.id, transcript_id=old_tid, library_id=1,
        library_settings={"chapter": {"infer": True}}, video_duration_sec=600.0)
    old_count = await db.fetchval(
        "SELECT COUNT(*) FROM chapters WHERE transcript_id=$1", old_tid)
    assert old_count >= 2

    # New transcript replaces old; flip is_active inside the same xact as chapter insert.
    new_tid = await transcript_factory.fresh(
        video_id=video.id, video_duration_sec=600.0, is_active=False)
    fake_chroma.add_units(_two_cluster_units(new_tid, n=20, dur=600.0))
    new_rows = build_rows_for_replace(...)  # via inferer + repo
    async with db.acquire() as conn:
        async with conn.transaction():
            await conn.execute(
                "UPDATE transcripts SET is_active = false WHERE id = $1", old_tid)
            await conn.execute(
                "UPDATE transcripts SET is_active = true WHERE id = $1", new_tid)
            await ChapterRepo(db).replace_in_active_flip(
                old_transcript_id=old_tid, new_transcript_id=new_tid,
                video_id=video.id, rows=new_rows, conn=conn)

    # Old chapters gone, new chapters present.
    assert await db.fetchval(
        "SELECT COUNT(*) FROM chapters WHERE transcript_id=$1", old_tid) == 0
    assert await db.fetchval(
        "SELECT COUNT(*) FROM chapters WHERE transcript_id=$1", new_tid) > 0
```

### 4.7 `test_inference_failure_does_not_fail_index_job` (story-named)

```python
async def test_chroma_get_failure_does_not_fail_index(
    db, broken_chroma, job_factory,
):
    """Chroma raises during inference → index job still completes; metric records failure."""
    job = await job_factory.fresh(video_duration_sec=600.0,
                                  library_settings={"chapter": {"infer": True}})

    metric = await ChapterInferer(chroma_client=broken_chroma, db_pool=db).infer(
        video_id=job.video_id, transcript_id=job.transcript_id,
        library_id=job.library_id, library_settings=job.library_settings,
        video_duration_sec=600.0)
    assert metric["chapter_infer_failed"] is True
    assert "chapter_infer_error" in metric

    await run_index_stage(ctx_with(broken_chroma=broken_chroma), job)
    assert await db.fetchval(
        "SELECT state FROM processing_jobs WHERE id=$1", job.id) == "done"
    t = await db.fetchrow(
        "SELECT metrics FROM transcripts WHERE id=$1", job.transcript_id)
    assert t["metrics"]["chapter_infer"]["chapter_infer_failed"] is True
```

### 4.8 `test_boundary_smoothing_suppresses_noise` (unit)

```python
def test_smoothed_distance_recovers_topic_shift_only():
    """A single off-topic unit inside a chapter should NOT trigger a boundary."""
    rng = np.random.RandomState(0)
    e_a = np.array([1, 0, 0] + [0]*1021, dtype=np.float32)
    embeddings = []
    for i in range(20):
        if i == 7:
            # noise spike: random direction
            v = rng.normal(0, 1, size=1024).astype(np.float32)
            embeddings.append(v / np.linalg.norm(v))
        else:
            v = e_a + rng.normal(0, 0.01, size=1024).astype(np.float32)
            embeddings.append(v / np.linalg.norm(v))
    embs = np.asarray(embeddings, dtype=np.float32)
    boundaries = detect_boundaries(embs, threshold=0.35, smoothing_window=3)
    # Smoothing absorbs the single spike → no boundaries.
    assert boundaries == []


def test_unsmoothed_distance_would_have_fired_on_noise():
    """Sanity: with smoothing_window=1 (no smoothing), the spike DOES fire."""
    rng = np.random.RandomState(0)
    e_a = np.array([1, 0, 0] + [0]*1021, dtype=np.float32)
    embeddings = []
    for i in range(20):
        if i == 7:
            v = rng.normal(0, 1, size=1024).astype(np.float32)
            embeddings.append(v / np.linalg.norm(v))
        else:
            v = e_a + rng.normal(0, 0.01, size=1024).astype(np.float32)
            embeddings.append(v / np.linalg.norm(v))
    embs = np.asarray(embeddings, dtype=np.float32)
    boundaries = detect_boundaries(embs, threshold=0.35, smoothing_window=1)
    indices = [b.unit_index for b in boundaries]
    assert 7 in indices
    assert 8 in indices
```

### 4.9 `test_merge_keeps_higher_confidence` (unit, D6)

```python
def test_merge_drops_weaker_close_boundary():
    cands = [
        CandidateBoundary(unit_index=2, distance=0.40),  # at start_sec 60
        CandidateBoundary(unit_index=3, distance=0.55),  # at start_sec 90 — wins
        CandidateBoundary(unit_index=10, distance=0.50), # at start_sec 300
    ]
    starts = [i * 30.0 for i in range(20)]
    accepted = enforce_min_chapter_sec(
        cands, unit_starts_sec=starts, min_chapter_sec=60.0)
    assert [a.unit_index for a in accepted] == [3, 10]


def test_merge_keeps_first_when_distances_tie():
    cands = [
        CandidateBoundary(unit_index=2, distance=0.40),
        CandidateBoundary(unit_index=3, distance=0.40),  # tie, but candidate.distance > prev.distance is FALSE
    ]
    starts = [i * 30.0 for i in range(20)]
    accepted = enforce_min_chapter_sec(
        cands, unit_starts_sec=starts, min_chapter_sec=60.0)
    assert [a.unit_index for a in accepted] == [2]
```

### 4.10 `test_disabled_per_library_is_a_noop` (story acceptance)

```python
async def test_chapter_infer_disabled_skips_db_writes(
    db, fake_chroma, transcript_factory,
):
    tid = await transcript_factory.fresh()
    fake_chroma.add_units(_three_cluster_units(tid, n=30, dur=600.0))

    # Library has chapter.infer=false — even with content_type=lecture.
    inferer = ChapterInferer(chroma_client=fake_chroma, db_pool=db)
    enabled = is_enabled({"chapter": {"infer": False}}, "lecture")
    assert enabled is False
    # Stage tail should not have called inferer at all in real code.
    # Direct call here just to assert the row count is zero.
    rows = await db.fetchval(
        "SELECT COUNT(*) FROM chapters WHERE transcript_id=$1", tid)
    assert rows == 0


def test_default_enabled_for_lecture_sermon_podcast():
    assert is_enabled({}, "lecture") is True
    assert is_enabled({}, "sermon") is True
    assert is_enabled({}, "podcast") is True
    assert is_enabled({}, "movie") is False
    assert is_enabled({}, None) is False
    # Explicit override always wins.
    assert is_enabled({"chapter": {"infer": False}}, "lecture") is False
    assert is_enabled({"chapter": {"infer": True}}, "movie") is True
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handled by |
|-----|-----------|------------|
| E1  | **Transcript with only one unit.** Chroma returns one row; `embeddings.shape[0] < 2` → `ChapterInferer.infer` returns `{chapter_infer_succeeded, num_chapters: 0, reason: "single_unit"}` and writes zero rows (via `repo.replace_for_transcript` with empty list). The Streaming Service `chapters.json` then renders the "no chapters" placeholder (Plan 8.12). | `test_inferer_single_unit` (extension of `test_inference_single_topic_one_chapter`) |
| E2  | **All units have identical embedding** (degenerate transcript — silence misclassified, or constant filler). Centroids are all equal; pairwise cosine distance is 0; zero boundaries detected; one chapter spanning the whole video. | `test_inference_single_topic_one_chapter` (the noise-around-one-vector case is functionally identical) |
| E3  | **Language switch mid-video.** Per-unit language metadata reflects the switch; `_chapter_language` looks at the first 3 units of each chapter to decide its tag. A chapter that straddles a language switch (the switch happens *inside* the chapter, not at the boundary) gets the tag of its first 3 units. We document this — chapter-internal language switches are not separated unless the embedding distance also exceeds threshold. | `test_arabic_chapter_language_tag` |
| E4  | **Very long video > 4 hours.** Worst-case unit count: 4h × 60min × 60s / (avg unit duration ~12s) ≈ 1200 units. Bulk Chroma `get` returns ~5 MB at float32×1024-dim; the cosine-distance loop is O(N) and runs in ~0.05 s on a modern CPU; the smoothing convolution is O(N×W×D) ≈ 1200×3×1024 ≈ 3.7M ops, ~0.1 s. Total wall-time budget: ≤ 2 s for a 4-hour video including DB roundtrips. We do **not** stream-process; in-memory is fine at this scale. | `test_long_video_under_2s` (perf, parameterized in CI; reference machine) |
| E5  | **No embeddings yet — Chroma is empty for the transcript.** Either index never ran (call ordering bug) or all writes failed. `ChapterInferer.infer` detects empty `metadatas`, returns `{chapter_infer_skipped: True, reason: "no_units_in_chroma"}`, writes zero rows. The metric makes the failure visible. The job still succeeds (D8). | `test_inferer_skips_when_chroma_empty` (extension of `test_inferer`) |
| E6  | **Highly fragmented transcripts** (many short units, e.g. word-level units instead of unit-level). Smoothing window absorbs most of the noise; `min_chapter_sec` (60 s default) caps the chapter count regardless of unit count. A 1-hour video with 1800 units (one per 2 s) still yields ≤ 60 chapters in the worst case. | Behaviour falls out of D2 + D6; no special code path. Explicit fixture: `test_short_units_min_chapter_sec_caps_chapter_count`. |
| E7  | **Library deletion mid-inference.** `chapters.video_id REFERENCES videos(id) ON DELETE CASCADE` and `chapters.transcript_id REFERENCES transcripts(id) ON DELETE CASCADE` (videos cascade from libraries via Story 1.6). If the library is deleted between the bulk Chroma read and the DB insert, the insert fails with FK violation; D8 catches it and the metric records the failure. No orphan rows. | DB-level enforcement; no code change. `test_inferer_handles_video_deleted_during_run` (extension). |
| E8  | **Threshold set to 0** (every adjacent pair becomes a boundary). Boundary-detection emits N-1 candidates; min_chapter_sec merge collapses them down to ⌊video_duration / min_chapter_sec⌋ chapters. The user gets what they asked for; no error. | Behaviour falls out of D6. |
| E9  | **Threshold set to ≥ 1.0** (no boundary can ever fire). Zero candidates, one chapter spanning the whole video (chapter 0). | Behaviour falls out of D1. |
| E10 | **Embedding dim mismatch** (e.g., a library was re-embedded with `e5-base` 768-dim partway through). `np.asarray([...])` raises `ValueError` because the rows have inconsistent shape. D8 catches it; the metric logs `chapter_infer_failed` with the dim mismatch error. The operator must reindex consistently (Story 5.3 already mandates a full re-index on model swap). | D8 catches; explicit test deferred to Story 5.5 where the model-swap path is implemented. |
| E11 | **Resume of an index job mid-tail.** The chapter inference is **idempotent**: it deletes existing rows for the transcript and reinserts. A resume that reaches the tail twice writes the same rows twice — same content, same `(transcript_id, seq)` keys; the delete-then-insert pattern is correct. We do **not** record a checkpoint inside chapter inference — the inference always runs end-to-end. | Tested implicitly by `test_rerun_replaces_chapters_atomically`. |
| E12 | **Concurrent inference for the same transcript** (two workers race on the index tail — should not happen under the queue claim model, but a defence-in-depth check). The first worker's transaction commits; the second's `DELETE … INSERT` runs against the just-inserted rows; final state is the second worker's chapters. The `(transcript_id, seq)` UNIQUE prevents partial overlap. No corruption; possible duplicate work. We rely on Epic 6 queue exactly-once claim semantics to prevent this in practice. | DB-level UNIQUE; no special code path. |

---

## 6. Acceptance checklist

- [ ] **A1** Migration `shared/db/migrations/0026_chapters.sql` creates the `chapters` table with `(id, video_id, transcript_id, seq, start_sec, end_sec, title NULLABLE, lang NULLABLE, confidence, metadata, created_at)`, the `(video_id, start_sec)` index, the `(transcript_id, seq)` UNIQUE constraint, and the `start_sec >= 0`, `end_sec >= start_sec`, `confidence ∈ [0,1]` CHECKs. (`test_migration_creates_chapters_table`)
- [ ] **A2** After `index` finishes for a transcript, `ChapterInferer.infer` runs (when enabled) and computes smoothed cosine distance between adjacent unit centroids using `chapter.smoothing_window` (default 3). It emits a boundary wherever the distance exceeds `chapter.threshold` (default 0.35), reading embeddings from Chroma via a single bulk `get` (no re-embedding). (`test_inference_multi_topic_three_segments`, `test_boundary`)
- [ ] **A3** Each detected chapter is recorded with `seq` (0-based, contiguous), `start_sec` (= the start of the boundary unit, or 0 for chapter 0), `end_sec` (= the start of the next chapter, or `video_duration` for the last chapter), `confidence` (the cosine distance at the boundary, clamped to [0,1]; chapter 0 gets confidence 1.0), and `metadata.first_unit_seq`. (`test_inference_multi_topic_three_segments`)
- [ ] **A4** `title` is left NULL in v1; the column allows NULL; the API renders "Chapter N" as a fallback. (Schema + Plan 8.12; `test_migration_creates_chapters_table` covers nullability.)
- [ ] **A5** `min_chapter_sec` (default 60) is enforced via greedy higher-confidence-wins merge (`merge.enforce_min_chapter_sec`); two boundaries within 60 s collapse to one. (`test_merge`)
- [ ] **A6** Per-library opt-in: `library.settings.chapter.infer = false` skips the inferer entirely; default is `true` for `content_type ∈ {lecture, sermon, podcast}`, `false` otherwise. (`test_disabled_per_library_is_a_noop`, `test_default_enabled_for_lecture_sermon_podcast`)
- [ ] **A7** Failure of chapter inference does **not** fail the parent `index` job: any exception inside `ChapterInferer.infer` is caught, logged with `kind=chapter_infer_failed`, recorded in `transcripts.metrics.chapter_infer.chapter_infer_failed = {error, ...}`, and the job transitions to DONE / video to INDEXED. (`test_chroma_get_failure_does_not_fail_index`)
- [ ] **A8** Re-run on a new transcript replaces the chapters atomically: `ChapterRepo.replace_in_active_flip` runs the DELETE (old transcript chapters) + INSERT (new transcript chapters) inside the same transaction that flips `is_active`. (`test_rerun_on_new_transcript_replaces_chapters`)
- [ ] **A9** Single-unit transcripts emit zero chapters and a metric reason `single_unit`; the API renders the "no chapters" placeholder. (`test_inferer_single_unit`)
- [ ] **A10** Empty Chroma read emits zero chapters and metric `reason: no_units_in_chroma`. The job still succeeds. (`test_inferer_skips_when_chroma_empty`)
- [ ] **A11** Chapters carry a language tag derived from the first 3 units of each chapter (`ar`, `en`, `mixed`, or NULL); Arabic-only chapters are tagged `lang='ar'`. (`test_arabic_units_produce_arabic_chapter_language`)
- [ ] **A12** Library settings JSON shape is `{"chapter": {"infer": bool, "threshold": float, "smoothing_window": int, "min_chapter_sec": float}}` with defaults baked into `pipeline/src/maktaba_pipeline/config/defaults.py` (no DB lookup for missing keys). (Smoke test on `config_from_library_settings`.)
- [ ] **A13** Performance: a 4-hour video with ~1200 units completes inference in ≤ 2 s wall time on the reference machine, including the bulk Chroma read and DB write. (`test_long_video_under_2s`, parameterized in CI.)
- [ ] **A14** No code path in this story writes to `videos`, `transcripts.chapters`, or any pre-existing JSONB chapter field — the table is the single source of truth. (Static check; lint rule on `transcripts.chapters` writes outside Epic 5.)
- [ ] **A15** No code path calls the embedding model from this stage — all vectors come from Chroma. (Static check via import linting; the `chapter` package does not import the embedding-model module.)

---

## 7. Performance budget

| Phase | Cost (4-hour video, ~1200 units) | Notes |
|-------|----------------------------------|-------|
| Bulk Chroma `get` (embeddings + metadatas) | ~50–100 ms | Local sqlite-backed Chroma; one round-trip. |
| Sort by `seq` in Python | ~5 ms | List of 1200 with int keys. |
| `smooth_centroids` | ~100 ms | 1200 × window=3 × 1024 dims = ~3.7 M float ops; numpy vectorized. |
| `detect_boundaries` cosine loop | ~30 ms | 1200 dot products on 1024-dim float32. |
| `enforce_min_chapter_sec` | < 1 ms | O(N) pass over the candidate list. |
| `_build_rows` | < 1 ms | List comprehension. |
| `ChapterRepo.replace_for_transcript` (DELETE + executemany INSERT) | ~50–200 ms | Depends on chapter count; typical 5–20 chapters. |
| **Total wall** | **≤ 500 ms** typical, ≤ 2 s worst-case | Well under any user-visible threshold; runs inside the index stage tail. |

The inference is bounded by Chroma's `get` and the DB roundtrip; the
math itself is negligible at this scale. We leave headroom by enforcing
A13 at 2 s.

---

## 8. Open questions and v1.1 roadmap

These are intentionally out of scope for v1; documented here so v1.1 can
pick them up without re-reading the story.

- **Title generation (D5).** v1.1 should add an offline batch job that
  picks the most-frequent noun phrase from each chapter's text or runs
  a cheap summarizer (e.g., a small Llama or Qwen variant). The
  schema is already title-NULLable; v1.1 becomes a backfill task.
- **Cross-language chapter titling.** When titles are added, the
  Arabic-side requires a different summarizer or tokenizer; the
  `lang` column (added in this story) tells the title job which path
  to take.
- **Adaptive thresholds per video.** v1 uses a single threshold per
  library. A future enhancement could fit a Gaussian to the per-video
  distance distribution and pick the dropout point as `μ + 2σ`. This
  would reduce the threshold-tuning burden for libraries with mixed
  content.
- **Speaker-aware boundaries.** When diarization (Story 3.9) is on, a
  speaker change is also a strong signal for a chapter boundary.
  v1.1 could mix a `0.5 × cosine_drop + 0.5 × speaker_change` score.
- **Manual chapter overrides.** A user might want to nudge or rename
  a chapter from the UI. v1 doesn't support this; the `chapters`
  table needs a `source TEXT NOT NULL DEFAULT 'inferred'` column and
  the inference must respect `source = 'manual'` rows on re-run.
  Schema migration is straightforward; we'll add it in the same
  v1.1 story.
