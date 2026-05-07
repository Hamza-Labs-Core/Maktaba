# Implementation Plan — Story 9.9 Auto-categorization: Topic Tag

> Companion to [story-09-09-topic-tag.md](story-09-09-topic-tag.md).
> The story states *what* and *why*; this plan states *how*.
> Builds on the embedder pipeline (Epic 5), Story 9.1's
> `auto_tag_topics` flag, and feeds Story 9.18 (chapter inference).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Recluster job | A nightly per-library job at `stage='topic_recluster'`, scheduled by Epic 22's cron; reuses the job-queue. Runs in the Pipeline; one job per library; `priority=300` (background). |
| Per-video assignment | A *separate* fast pass at `stage='topic_assign'` triggered by the indexer's commit, OR by the recluster's tail (it reassigns every video against the new centroids). Single function `assign_topics(video_id)` — used by both paths. |
| Mean embedding | A single 768-dim vector per video, computed as the L2-normalized mean of segment embeddings. Stored on `videos.mean_embedding BYTEA` (added here). |
| K-means impl | `sklearn.cluster.MiniBatchKMeans` with `random_state` derived from the library id (deterministic across runs). |
| Centroid storage | `library_topics.centroid_vec BYTEA` — `numpy.float32[K]` packed little-endian, 768 dims × 4 B = 3 KiB per cluster. K ≤ 32 → ≤ 96 KiB per library. |
| Labeling | Top-3 segments closest to the centroid get concatenated; embedder is asked for the nearest token bigram (a small Hugging Face MLM head) to produce a 1–4 word slug; bounded at 64 chars. The label generator lives behind a feature flag — when the embedder is unavailable, fallback to `cluster-{topic_id}`. |
| Out of scope | The PATCH endpoint owner-only check (Epic 7 Story 7.14 enforces); the schema for `library_topics`/`video_topics` themselves (already in the Epic 9 README); the indexing pipeline that populates segment embeddings (Epic 5). |

## 1. Architecture diagram

```
   Nightly cron (Epic 22)
        ↓ enqueue per active library
   stage=topic_recluster, payload={library_id}
        ↓
   topic_recluster.run(library_id)
        ├─ if not library.auto_tag_topics: skip; emit metric.
        ├─ if count(indexed videos) < 100: skip; emit metric.
        ├─ collect mean embeddings:
        │     SELECT id, mean_embedding FROM videos
        │      WHERE library_id = $1 AND state IN ('indexed','ready','ready_no_audio')
        ├─ K = min(int(sqrt(N)/2), 32); K = max(K, 2)
        ├─ kmeans = MiniBatchKMeans(n_clusters=K, random_state=hash(library_id),
        │                           batch_size=4096, max_iter=200, n_init=3)
        ├─ kmeans.fit(stack(means))
        ├─ for tid, centroid in enumerate(kmeans.cluster_centers_):
        │       UPSERT library_topics (library_id, topic_id, centroid_vec,
        │                              video_count, computed_at)
        ├─ delete library_topics rows whose topic_id ≥ K (cluster shrunk)
        ├─ for each video:
        │       assign_topics(video_id, kmeans=kmeans)
        └─ NOTIFY 'library.topics_updated', {library_id, K}

   indexer.commit(video_id)
        ↓
   stage=topic_assign, payload={video_id}
        ↓
   topic_assign.run(video_id)
        ├─ load library's centroids
        ├─ compute video.mean_embedding (if not present)
        ├─ score = cosine_similarity(mean, centroids)
        ├─ TOP_K = 3
        ├─ DELETE FROM video_topics WHERE video_id = $1
        ├─ INSERT INTO video_topics (video_id, library_id, topic_id, score)
        │   VALUES ... (top 3)
        └─ publish(VIDEO_TOPICS_UPDATED, {video_id})  # constant per 09-01 §2.5
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/topics/__init__.py` | Re-exports. |
| `pipeline/src/maktaba_pipeline/topics/recluster.py` | `run_recluster(library_id)` — full nightly job. |
| `pipeline/src/maktaba_pipeline/topics/assign.py` | `assign_topics(video_id)` — fast per-video pass. |
| `pipeline/src/maktaba_pipeline/topics/labeler.py` | Centroid → human-readable label via MLM head. |
| `pipeline/src/maktaba_pipeline/topics/vec.py` | `pack_float32`, `unpack_float32` — BYTEA round-trip. |
| `pipeline/tests/topics/test_recluster.py` | Unit + integration tests per §6.1. |
| `pipeline/tests/topics/test_assign.py` | Per-video tests per §6.2. |
| `pipeline/tests/topics/test_labeler.py` | Labeler tests per §6.3. |
| `shared/db/migrations/0037_topics.sql` | `library_topics`, `video_topics`, `videos.mean_embedding`. |
| `shared/db/migrations/0037b_transcript_units.sql` | Owns the canonical `transcript_units` table (architecture). The closest "indexer-side" plan in Epic 9 owns this migration since the indexer is the writer. |
| `shared/db/queries/topics.sql` | sqlc input — `UpsertTopic`, `DeleteTopicsAbove`, `ListVideoTopics`, `RenameTopic`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/index/commit.py` | After commit, enqueue `topic_assign` for the video (if library.auto_tag_topics). |
| `pipeline/src/maktaba_pipeline/jobs/dispatcher.py` | Add `topic_recluster` and `topic_assign` stages. |
| `shared/db/migrations/0037a_processing_jobs_stage_topic.sql` | New migration that ALTERs the `processing_jobs.stage` CHECK to include `topic_recluster`, `topic_assign`. Does **not** edit Epic 6's `0010_processing_jobs.sql` — that migration is immutable once shipped. Renumber sequentially after the last Epic 6 migration. |
| `api/internal/handlers/libraries/topics.go` | The PATCH `/api/libraries/{id}/topics/{topic_id}` rename; owner/admin check. |
| `specs/epics/09-library-management/README.md` | Tick story 9.9. |

### 2.3 Type definitions

```python
# pipeline/src/maktaba_pipeline/topics/recluster.py
from __future__ import annotations
import math
from dataclasses import dataclass

import numpy as np
from sklearn.cluster import MiniBatchKMeans

EMBED_DIM = 768
MAX_K = 32
MIN_VIDEOS_FOR_RECLUSTER = 100
TOP_K_TOPICS_PER_VIDEO = 3


@dataclass(slots=True, frozen=True)
class Centroid:
    topic_id: int
    vec: np.ndarray            # shape (EMBED_DIM,) dtype float32
    label: str | None
    video_count: int
```

```sql
-- shared/db/queries/topics.sql
-- name: UpsertTopic :exec
INSERT INTO library_topics (library_id, topic_id, label, centroid_vec,
                            video_count, computed_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (library_id, topic_id) DO UPDATE
   SET centroid_vec = EXCLUDED.centroid_vec,
       video_count  = EXCLUDED.video_count,
       computed_at  = now();
   -- Note: label is *not* overwritten by recluster — user renames stick.

-- name: DeleteTopicsAbove :exec
DELETE FROM library_topics
 WHERE library_id = $1 AND topic_id >= $2;

-- name: ListVideoTopics :many
SELECT vt.topic_id, vt.score, lt.label
  FROM video_topics vt
  JOIN library_topics lt USING (library_id, topic_id)
 WHERE vt.video_id = $1
 ORDER BY vt.score DESC;

-- name: RenameTopic :exec
UPDATE library_topics
   SET label = LEFT($3, 64)
 WHERE library_id = $1 AND topic_id = $2;
```

## 3. Database migration

`shared/db/migrations/0037_topics.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

-- Mean embedding stored once per video; populated by the recluster
-- run (and by topic_assign when missing).
ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS mean_embedding BYTEA;

ALTER TABLE videos
    ADD CONSTRAINT videos_mean_embedding_len_chk
    CHECK (mean_embedding IS NULL
           OR octet_length(mean_embedding) = 768 * 4);

-- library_topics — schema as in Epic 9 README.
CREATE TABLE library_topics (
    library_id     UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    topic_id       INTEGER NOT NULL,
    label          TEXT,
    centroid_vec   BYTEA NOT NULL,
    video_count    INTEGER NOT NULL DEFAULT 0,
    computed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (library_id, topic_id),
    CONSTRAINT library_topics_centroid_len_chk
        CHECK (octet_length(centroid_vec) = 768 * 4),
    CONSTRAINT library_topics_label_len_chk
        CHECK (label IS NULL OR char_length(label) <= 64),
    CONSTRAINT library_topics_topic_id_chk
        CHECK (topic_id >= 0 AND topic_id < 32)
);

CREATE TABLE video_topics (
    video_id    UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    library_id  UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    topic_id    INTEGER NOT NULL,
    score       REAL NOT NULL,
    PRIMARY KEY (video_id, topic_id),
    FOREIGN KEY (library_id, topic_id)
        REFERENCES library_topics(library_id, topic_id) ON DELETE CASCADE,
    CONSTRAINT video_topics_score_chk CHECK (score >= -1.0 AND score <= 1.0)
);

CREATE INDEX video_topics_topic
    ON video_topics (library_id, topic_id, score DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS video_topics;
DROP TABLE IF EXISTS library_topics;
ALTER TABLE videos DROP COLUMN IF EXISTS mean_embedding;
-- +goose StatementEnd
```

The `library_topics_topic_id_chk CHECK (topic_id < 32)` makes
the per-library row count bounded; the K-shrunk delete in
`run_recluster` uses `DeleteTopicsAbove`.

### 3.1 `transcript_units` migration (canonical, owned here)

`shared/db/migrations/0037b_transcript_units.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

-- transcript_units is the canonical reranker/index unit (architecture).
-- One row per (transcript, segment, sequence within segment); used by the
-- index stage as the rerank target. Owned by the indexer-side plan
-- (this plan); referenced by Stories 9.18 and downstream search.
CREATE TABLE transcript_units (
    id              BIGSERIAL PRIMARY KEY,
    transcript_id   UUID    NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    video_id        UUID    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    segment_id      BIGINT  NOT NULL REFERENCES transcript_segments(id) ON DELETE CASCADE,
    seq             INTEGER NOT NULL,
    start_sec       REAL    NOT NULL,
    end_sec         REAL    NOT NULL,
    text            TEXT    NOT NULL,
    language        TEXT,
    CONSTRAINT transcript_units_time_chk CHECK (start_sec >= 0 AND end_sec > start_sec)
);

CREATE INDEX transcript_units_video         ON transcript_units (video_id, start_sec);
CREATE INDEX transcript_units_segment_seq   ON transcript_units (segment_id, seq);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS transcript_units;
-- +goose StatementEnd
```

## 4. Code scaffolding

### 4.1 `vec.pack_float32` / `unpack_float32`

```python
# pipeline/src/maktaba_pipeline/topics/vec.py
import numpy as np

def pack_float32(v: np.ndarray) -> bytes:
    if v.dtype != np.float32:
        v = v.astype(np.float32, copy=False)
    return v.tobytes(order="C")

def unpack_float32(b: bytes, dim: int) -> np.ndarray:
    arr = np.frombuffer(b, dtype=np.float32, count=dim).copy()
    if arr.size != dim:
        raise ValueError(f"expected {dim} floats, got {arr.size}")
    return arr
```

### 4.2 `run_recluster`

```python
# pipeline/src/maktaba_pipeline/topics/recluster.py
async def run_recluster(db, library_id: UUID, *, settings,
                        labeler=None, bus=None) -> None:
    if not settings.auto_tag_topics:
        topic_recluster_skipped_total.labels(reason="disabled").inc()
        return

    rows = await db.fetch(
        "SELECT id, mean_embedding FROM videos "
        " WHERE library_id = $1 "
        "   AND state IN ('indexed','ready','ready_no_audio') "
        "   AND mean_embedding IS NOT NULL",
        library_id,
    )
    if len(rows) < MIN_VIDEOS_FOR_RECLUSTER:
        topic_recluster_skipped_total.labels(reason="under_min").inc()
        return

    means = np.stack([unpack_float32(r["mean_embedding"], EMBED_DIM)
                      for r in rows])
    K = max(2, min(MAX_K, int(math.sqrt(len(rows)) / 2)))
    seed = int(hashlib.blake2s(str(library_id).encode(),
                                digest_size=4).hexdigest(), 16)

    km = MiniBatchKMeans(
        n_clusters=K, random_state=seed,
        batch_size=4096, max_iter=200, n_init=3, reassignment_ratio=0.005,
    )
    km.fit(means)

    counts = np.bincount(km.labels_, minlength=K)

    for tid in range(K):
        centroid = km.cluster_centers_[tid].astype(np.float32)
        # Normalize so cosine via dot is well-behaved at assign time.
        n = np.linalg.norm(centroid) or 1.0
        centroid /= n

        existing_label = await db.fetchval(
            "SELECT label FROM library_topics WHERE library_id=$1 AND topic_id=$2",
            library_id, tid,
        )
        new_label = existing_label
        if existing_label is None and labeler is not None:
            try:
                new_label = await labeler.label(library_id, tid, centroid)
            except LabelerUnavailable:
                new_label = None

        await db.execute(
            "INSERT INTO library_topics "
            "  (library_id, topic_id, label, centroid_vec, video_count, computed_at)"
            "VALUES ($1,$2,$3,$4,$5, now()) "
            "ON CONFLICT (library_id, topic_id) DO UPDATE SET "
            "   centroid_vec = EXCLUDED.centroid_vec, "
            "   video_count  = EXCLUDED.video_count, "
            "   computed_at  = now()",
            library_id, tid, new_label, pack_float32(centroid),
            int(counts[tid]),
        )

    # Delete shrunken-K rows so we never have stale clusters.
    await db.execute(
        "DELETE FROM library_topics WHERE library_id=$1 AND topic_id >= $2",
        library_id, K,
    )

    # Reassign every video; the assign function reads centroids fresh.
    for r in rows:
        await assign_topics(db, video_id=r["id"], library_id=library_id)

    if bus is not None:
        # Channel constant from pipeline/db/pubsub.py (see 09-01 §2.5).
        bus.publish(LIBRARY_TOPICS_UPDATED,
                    {"library_id": str(library_id), "K": K})
```

### 4.3 `assign_topics`

```python
# pipeline/src/maktaba_pipeline/topics/assign.py
async def assign_topics(db, *, video_id: UUID,
                        library_id: UUID | None = None) -> None:
    if library_id is None:
        library_id = await db.fetchval(
            "SELECT library_id FROM videos WHERE id=$1", video_id)

    centroids = await db.fetch(
        "SELECT topic_id, centroid_vec FROM library_topics "
        " WHERE library_id=$1 ORDER BY topic_id",
        library_id,
    )
    if not centroids:
        return  # library has no centroids yet; assign is a no-op

    mean = await _ensure_mean_embedding(db, video_id)
    if mean is None:
        return

    centroid_matrix = np.stack(
        [unpack_float32(r["centroid_vec"], EMBED_DIM) for r in centroids])
    scores = centroid_matrix @ mean   # cosine because both unit-norm

    top = np.argsort(scores)[::-1][:TOP_K_TOPICS_PER_VIDEO]
    rows = [(int(centroids[i]["topic_id"]), float(scores[i])) for i in top]

    async with db.transaction():
        await db.execute(
            "DELETE FROM video_topics WHERE video_id=$1", video_id)
        for tid, sc in rows:
            await db.execute(
                "INSERT INTO video_topics "
                "  (video_id, library_id, topic_id, score) "
                "VALUES ($1,$2,$3,$4)",
                video_id, library_id, tid, sc,
            )
```

### 4.4 `_ensure_mean_embedding`

Per architecture §8.4, segment embeddings live **only in ChromaDB** —
there is no `transcript_segments.embedding` column. The mean is computed
from a ChromaDB query for the video's segments and cached on
`videos.mean_embedding` (BYTEA, 768 × 4 bytes).

```python
from ..chroma import chromadb_client

async def _ensure_mean_embedding(db, video_id: UUID) -> np.ndarray | None:
    row = await db.fetchrow(
        "SELECT mean_embedding, library_id FROM videos WHERE id=$1", video_id)
    if row is None:
        return None
    if row["mean_embedding"] is not None:
        return unpack_float32(row["mean_embedding"], EMBED_DIM)

    # Pull this video's segment embeddings from ChromaDB (per-library
    # collection, architecture §8.4). Each Chroma item carries the
    # segment_id in metadata; we filter by video_id.
    coll = chromadb_client.collection(row["library_id"])
    res = coll.get(where={"video_id": str(video_id)},
                   include=["embeddings"])
    embeddings = res.get("embeddings") or []
    if not embeddings:
        return None
    arr = np.asarray(embeddings, dtype=np.float32)
    mean = arr.mean(axis=0)
    n = np.linalg.norm(mean) or 1.0
    mean /= n
    await db.execute(
        "UPDATE videos SET mean_embedding=$1 WHERE id=$2",
        pack_float32(mean), video_id,
    )
    return mean
```

### 4.5 Labeler

```python
# pipeline/src/maktaba_pipeline/topics/labeler.py
class LabelerUnavailable(RuntimeError): ...


class TopicLabeler:
    def __init__(self, embedder, max_label_chars: int = 64): ...

    async def label(self, library_id, topic_id, centroid_vec) -> str | None:
        # 1. find the 5 segments closest to centroid in this library
        rows = await self._find_top_segments(library_id, centroid_vec, k=5)
        if not rows:
            return None
        text = " ".join(r["text"] for r in rows)
        try:
            tokens = await self._embedder.top_tokens(text, k=2)
        except (TimeoutError, ConnectionError) as e:
            raise LabelerUnavailable from e
        slug = "-".join(tokens).lower()[:self._max]
        return slug or None
```

`_embedder.top_tokens` is the existing wrapper (Epic 5 Story 5.3). When
the embedder is down, the labeler raises `LabelerUnavailable` and
`run_recluster` falls back to `cluster-{tid}` per AC's robustness goal.

## 5. Test plan

### 5.1 `test_recluster.py`

| Test | What it pins |
|---|---|
| `test_skipped_when_disabled` | `auto_tag_topics=False` → counter `topic_recluster_skipped_total{reason=disabled}` increments; no DB writes. AC-4. |
| `test_skipped_when_under_threshold` | 99 indexed videos → no recluster; counter `topic_recluster_skipped_total{reason=under_min}`. Edge case from story. |
| `test_K_formula` | 100 videos → K=5; 200 → K=7; 1024 → K=16; 4096 → K=32 (capped). |
| `test_deterministic_seed` | Same library, same data, two runs in a row produce identical centroids byte-for-byte. AC-1 stable cluster ids. |
| `test_obvious_clusters_dominate` | Synthetic 200-video fixture in 4 clusters → 80% of videos land in 4 of the 14 clusters. Story integration test verbatim. |
| `test_topic_count_reflects_assignments` | After recluster, `library_topics.video_count` per row sums to ≤ N (top-K counts so each video is counted up to TOP_K times in `video_topics`, but `video_count` in `library_topics` is the *primary* cluster count from `kmeans.labels_`). |
| `test_label_preserved_on_rerun` | User-renamed topic → recluster does NOT overwrite label. (DO UPDATE in UpsertTopic excludes label.) |
| `test_label_assigned_when_unset` | Centroid with no label → labeler invoked; label written; second recluster preserves it. |
| `test_label_falls_back_when_embedder_down` | Stub `_embedder.top_tokens` to raise; centroid persists; label is None; metric `topic_label_unavailable_total` increments. |
| `test_K_shrink_deletes_higher_topics` | Run with N=200 (K=7) → run with N=100 (K=5); rows for topic_id ∈ {5, 6} deleted; FK cascade clears `video_topics`. |

### 5.2 `test_assign.py`

| Test | What it pins |
|---|---|
| `test_assigns_top_3` | Library with 5 centroids; one video → exactly 3 `video_topics` rows; scores descending. AC-3. |
| `test_assign_replaces_old_rows` | Pre-state has 3 rows for video; assign re-runs; old rows gone; 3 new rows. |
| `test_video_with_no_segments_no_op` | No segment embeddings → mean cannot be computed → no `video_topics` rows; no error. |
| `test_assign_when_no_centroids_yet_noop` | Library with 50 videos (under threshold) → no centroids → assign returns silently. |
| `test_assign_uses_unit_norm_cosine` | Centroids and means stored unit-norm; dot product equals cosine; scores ∈ [-1, 1]. CHECK constraint enforces. |

### 5.3 `test_labeler.py`

| Test | What it pins |
|---|---|
| `test_label_truncated_to_64_chars` | A long bigram → 64-char-truncated. |
| `test_label_lowercased_with_dash_separator` | "Prayer Rituals" → `"prayer-rituals"`. |
| `test_label_returns_none_when_no_segments` | Empty cluster → None. |

### 5.4 PATCH endpoint test

`api/internal/handlers/libraries/topics_test.go`:

| Test | What it pins |
|---|---|
| `TestRenameTopic_OwnerOnly` | Non-owner / non-admin → 403. AC-2. |
| `TestRenameTopic_64CharCap` | Body `label` 100 chars → stored as 64 (LEFT). |
| `TestRenameTopic_PropagatesViaWS` | After rename → WS broadcast `library:topics:renamed`. |

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Library with < 100 videos | Recluster skipped; `library_topics` rows preserved (from prior runs) but stale; `video_topics` from prior runs preserved. The user's UI will show the old labels. Documented; not tested. | Documented |
| Video with no transcript yet | `_ensure_mean_embedding` returns None; `video_topics` empty. The video appears "untagged" in the UI per the story. | `test_video_with_no_segments_no_op` |
| New video added between recluster runs | The indexer commit enqueues `topic_assign`; the video gets assigned against the *current* centroids; the next recluster re-fits. | `test_assign_uses_existing_centroids` |
| K shrinks (library shrinks below sqrt threshold) | `DeleteTopicsAbove` removes higher topic_ids; FK cascades clear `video_topics` for those rows; next assign reassigns the videos against the smaller K. | `test_K_shrink_deletes_higher_topics` |
| Embedder unavailable mid-recluster | Centroids still write (centroid math has no embedder dependency); only labels fall back to None; `topic_label_unavailable_total` increments. | `test_label_falls_back_when_embedder_down` |
| Concurrent two recluster jobs for the same library | The job queue's per-`(video_id, stage)` unique-live partial index doesn't apply (we use `video_id=NULL`). We add `pg_try_advisory_xact_lock(hash('topic_recluster:'||library_id::text))`; the second worker drops on `not lock_acquired` and returns DONE silently. | `test_recluster_single_flight` |
| User renames a topic while recluster is mid-run | Recluster's UPSERT excludes label; user's name survives. Race: PATCH → UPSERT (no label change) → label persists. | `test_label_preserved_on_rerun` |

## 7. Configuration

| Key | Default | Effect |
|---|---|---|
| `auto_tag_topics` (per library) | True | Disable to skip recluster + assign for the library. |
| `topic_clusters` formula | `min(int(sqrt(N)/2), 32)` | Hard-coded in `recluster.py`; not a knob in v1. |
| `min_videos_for_recluster` | 100 | Constant; insufficient-data guard. |
| `top_k_topics_per_video` | 3 | Constant; matches AC-3. |

## 8. Dependencies

| Dep | Version | Why |
|---|---|---|
| `scikit-learn` | ≥ 1.4 | `MiniBatchKMeans` |
| `numpy` | ≥ 1.26 | Vector ops |
| Embedder client | Epic 5 Story 5.3 | Labeler |
| ChromaDB per-library collection | architecture §8.4 | Source for mean embedding (segment embeddings live in Chroma, not Postgres). |

## 9. Acceptance checklist

**Code**
- [ ] `pipeline/src/maktaba_pipeline/topics/` package created with the four modules.
- [ ] Indexer commit enqueues `topic_assign` for every newly-indexed video.
- [ ] Nightly cron enqueues `topic_recluster` per library.

**Migration**
- [ ] `library_topics`, `video_topics`, `videos.mean_embedding` exist with the documented constraints.

**Behaviour (story acceptance criteria)**
- [ ] AC-1: K computed by formula; centroids persist; `library_topics` upserted.
- [ ] AC-2: labels generated when missing; preserved across reruns; rename via PATCH respected and capped at 64 chars.
- [ ] AC-3: every video gets top-3 `video_topics` rows.
- [ ] AC-4: `auto_tag_topics: false` short-circuits recluster.

**Performance**
- [ ] 1000-video recluster runs in < 30 s on the standard CI fixture.
- [ ] Per-video assign is < 50 ms p99 (cosine is matrix-multiply).

**Observability**
- [ ] Counter `topic_recluster_runs_total{outcome}`.
- [ ] Counter `topic_recluster_skipped_total{reason}`.
- [ ] Counter `topic_label_unavailable_total`.
- [ ] Histogram `topic_recluster_duration_seconds{library_id}`.

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.9.
- [ ] Operator handbook documents the 100-video minimum and the "labels stick across runs" behaviour.
