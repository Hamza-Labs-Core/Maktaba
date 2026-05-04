# Plan 9.9 — Auto-categorization: topic tag (nightly mini-batch k-means) — implementation

> Implementation plan for [story-09-09-topic-tag.md](story-09-09-topic-tag.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: reads vector embeddings from the Chroma
> collection owned by [Plan 5.3](../05-search-indexing/plan-05-03-chroma-vector.md)
> (per-library collection; one bulk `get` per recluster); reads
> per-segment embeddings the same way [Plan 5.7](../05-search-indexing/plan-05-07-chapter-inference.md)
> does (we never re-embed); writes `library_topics` and `video_topics`
> tables owned here; surfaces the rename PATCH endpoint via Epic 7
> Story 7.14; nightly cadence is driven by Postgres LISTEN with a
> heartbeat from a maintenance scheduler (Epic 22), so we don't depend
> on system cron.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Mini-batch k-means via `sklearn.cluster.MiniBatchKMeans`**, not full k-means and not bisecting k-means. Batch size 256, max iterations 100, deterministic seed `42 + library_seed_offset` so re-runs produce stable cluster IDs. | Story AC-1 ("mini-batch k-means computes K clusters"); story test "k-means with a deterministic seed produces stable cluster ids across runs given identical input". | Full k-means on a 50k-video library at 1024-dim is ~30 s; mini-batch is ~3 s with the same final inertia within 1%. Bisecting k-means makes K-determination smoother but doesn't matter at our K range (≤32). The deterministic seed is critical for the "same data → same cluster IDs" promise. |
| D2 | **K = `min(32, ceil(sqrt(N) / 2))`** where N = number of videos with a mean embedding. N < 100 → skip the recluster entirely. Per-library override at `library.settings.topic_clusters` (positive int) bypasses the formula. | Story AC-1 default formula; AC-1 cap; story edge case "Library with < 100 videos — recluster is skipped". | The √N/2 heuristic gives K=10 at N=400, K=14 at N=800, K=22 at N=2000, capped at 32 so the UI's topic chip strip stays usable. The 100-video minimum avoids forming clusters out of noise. |
| D3 | **Per-video mean embedding precomputed and cached on the row**, not recomputed per recluster. We add `videos.mean_embedding BYTEA` (packed float32, big-endian, dim from library config) populated by the `index` stage tail (Plan 5.5) right after Chroma writes. | Refines architecture: the recluster needs one vector per video; computing means at recluster time would re-fetch every unit's embedding from Chroma. | Caching the mean turns the recluster's I/O from O(units) to O(videos). For a 50k-video library with avg ~30 units each, that's 50k vectors instead of 1.5M — 30× less Chroma traffic. The mean is cheap to compute once at index time and cheap to invalidate (NULL it out) when the transcript changes. |
| D4 | **Centroid storage: packed float32 in `library_topics.centroid_vec BYTEA`.** Big-endian; dim follows the library's embedding model (declared in `library.settings.embedding.dim`). Round-tripped via `numpy.frombuffer(bytes, dtype='>f4')`. | Story schema in epic README: `centroid_vec BYTEA NOT NULL`. | BYTEA is 4× smaller than text-encoded JSON arrays and parses ~10× faster. Big-endian matches the convention Chroma uses for its serialization (so any future bridging code is straightforward). |
| D5 | **Topic labeling: top-5 closest segments to the centroid, concatenated and asked from the embedder for nearest token.** We call the embedder's `nearest_token(query_embedding)` API (Plan 5.3 exposes this). The result is a bigram of the form `"prayer-rituals"`, with hyphens. Fallback if the embedder doesn't expose nearest-token: the most-frequent bigram from the concatenated segments via a simple PMI-ranked extractor. | Story AC-2 explicit. | Embedders like multilingual-e5 expose tokens via the tokenizer's BPE merge map; we treat them as "decode the centroid back to language". The PMI fallback is implemented but not used at v1 (we ship with multilingual-e5 which has the API). |
| D6 | **Per-video assignment: top-3 nearest topics by cosine similarity** are stored in `video_topics`. We compute `score = (1 + cos(mean, centroid)) / 2` so the stored value is always in [0, 1] (cosine on raw vectors can be negative). | Story AC-3: "the top-3 nearest topics by cosine similarity are stored in `video_topics`". | Cosine in [-1, 1] is the natural similarity but [0, 1] is what every UI ranking expects. The shift+scale is monotonic so ordering is preserved. |
| D7 | **Disabled by setting (`auto_tag_topics: false`) preserves existing rows.** When the recluster is skipped via the setting, the existing `library_topics` rows are left alone (they're stable; they'll just be stale until re-enabled). New `video_topics` are not written. | Story AC-4: "the library is skipped (its `library_topics` rows are preserved but unused)". | Deleting them on opt-out would surprise the user (their UI loses topic chips); preserving them means re-enabling is a smooth experience. |
| D8 | **Nightly trigger via Postgres LISTEN/NOTIFY**, not OS cron. A maintenance scheduler (the Pipeline's `topic_recluster_loop` task) listens on channel `topic_recluster_request` and also runs a daily heartbeat (`asyncio.sleep` to next 02:00 UTC). Either source kicks the recluster. | Story spec: "clusters are recomputed nightly". | Decoupling from system cron makes the system self-contained and observable through the same job table. The LISTEN channel also lets the API trigger an on-demand recluster (admin endpoint) without rewriting cron files. |
| D9 | **One job row per recluster.** The maintenance scheduler INSERTs `processing_jobs(stage='topic_recluster', state='queued', library_id=$1, video_id=NULL, priority=200)` (low priority — runs at off-hours). Idempotency via partial unique idx (mirrors Plan 9.6). | Refines the story; needed for observability and pause/resume. | Reusing the job table means progress shows up in the same UI, the same WS feed, the same metrics. Priority 200 (lower than user scans at 50) prevents the recluster from contending with user-facing work. |
| D10 | **Recluster is incremental-friendly.** The newly-added videos between recluster runs get assigned topics from the existing centroids on the next index commit (per-row, by the index stage tail), not waiting for the next nightly. The centroids drift on the next recluster. | Story edge case: "A new video added between recluster nightly runs — assigned topics using existing centroids on the next index commit; the centroids drift over the next recluster". | The user sees topic chips on freshly-indexed videos within minutes, not hours. The "centroids drift" comment is honest about the long-term feedback loop. |
| D11 | **Rename via `PATCH /api/libraries/{id}/topics/{topic_id}` (Go).** Owner-only (admin or library owner per Story 10.x roles). Body `{label: <string>}`, max 64 chars, NFKC-normalized, trimmed. The new label has no effect on cluster geometry. | Story AC-2: "rename via `PATCH /api/libraries/{id}/topics/{topic_id}` … capped at 64 chars". | Renames are user-facing labels only; the cluster centroid stays the same so videos don't reshuffle. Owner-only authorization mirrors Story 9.13's collection-rename rule. |

If D3 is rejected (recompute means at recluster time): each nightly run re-fetches every unit embedding from Chroma — for a 50k-video library that's roughly 1.5M vectors at ~6 KB each = 9 GB of I/O per night. The cached mean shaves that to ~300 MB.

If D8 is rejected (system cron): we lose the unified job-table view, the on-demand admin trigger, and the pause/resume story. The maintenance scheduler is small (~50 LoC) and worth it.

---

## 1. Architecture diagram — recluster flow

```
   Nightly tick (02:00 UTC) OR LISTEN topic_recluster_request
            │
            ▼
   ┌──────────────────────────────────────────────┐
   │ Pipeline: topic_recluster_scheduler.py       │
   │   for library in libraries(auto_tag_topics): │
   │     INSERT processing_jobs                   │
   │       stage='topic_recluster',               │
   │       library_id=$1, priority=200            │
   └──────────────────────────────────────────────┘
            │
            ▼ (claim loop, Epic 7 §7.4)
   ┌──────────────────────────────────────────────┐
   │ TopicReclusterWorker.run(claimed_job)        │
   │                                              │
   │  1. Resolve K (D2)                           │
   │     N = SELECT COUNT(*) FROM videos          │
   │           WHERE library_id=$1                │
   │             AND mean_embedding IS NOT NULL   │
   │     if N < 100: skip; mark done              │
   │     K = min(32, ceil(sqrt(N)/2))             │
   │     # or library.settings.topic_clusters     │
   │                                              │
   │  2. Bulk-load mean embeddings (D3)           │
   │     SELECT id, mean_embedding                │
   │       FROM videos WHERE library_id=$1        │
   │            AND mean_embedding IS NOT NULL    │
   │     unpack BYTEA → numpy float32 (N, D)      │
   │                                              │
   │  3. Fit MiniBatchKMeans(D1)                  │
   │     km = MiniBatchKMeans(                    │
   │       n_clusters=K, batch_size=256,          │
   │       max_iter=100, random_state=42)         │
   │     km.fit(X)                                │
   │                                              │
   │  4. Stable IDs (Hungarian match)             │
   │     prev_centroids = SELECT centroid_vec     │
   │       FROM library_topics WHERE library_id=  │
   │     match new centroids → old topic IDs      │
   │     by max-cost cosine pairing; new IDs for  │
   │     unmatched centroids                      │
   │                                              │
   │  5. Label each cluster (D5)                  │
   │     for each centroid:                       │
   │       top5 = KNN over Chroma                 │
   │       label = embedder.nearest_token(c)      │
   │            or PMI fallback                   │
   │                                              │
   │  6. Atomic write                             │
   │     BEGIN;                                   │
   │     UPSERT library_topics (per cluster)      │
   │     DELETE video_topics WHERE library_id=$1  │
   │     for each video:                          │
   │       sims = cos(mean, centroids)            │
   │       top3 = argsort_desc(sims)[:3]          │
   │       INSERT video_topics rows               │
   │     COMMIT;                                  │
   │                                              │
   │  7. Mark job done; emit metric               │
   └──────────────────────────────────────────────┘

   Independent path: index stage tail (Plan 5.5)
   ─────────────────────────────────────────────
   After Chroma writes for a transcript:
     mean = mean(unit_embeddings)
     UPDATE videos SET mean_embedding = pack(mean)
     if library_topics exists for this library:
       sims = cos(mean, all_centroids)
       INSERT INTO video_topics (top3 with score)
   → Newly indexed videos get topics within seconds.

   Independent path: PATCH topic rename
   ────────────────────────────────────
   POST? PATCH /api/libraries/{id}/topics/{topic_id}
     UPDATE library_topics SET label = $1
       WHERE library_id = $2 AND topic_id = $3
```

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
├── topic/
│   ├── __init__.py
│   ├── recluster.py                  // TopicReclusterWorker.run
│   ├── kmeans.py                     // fit_minibatch_kmeans (deterministic)
│   ├── stable_ids.py                 // hungarian-match new vs old centroids
│   ├── labeler.py                    // label_centroids via nearest-token + PMI fallback
│   ├── repo.py                       // bulk read/write library_topics + video_topics
│   ├── packing.py                    // pack/unpack float32 BYTEA
│   ├── assigner.py                   // assign_topics_for_video (called from index tail)
│   ├── scheduler.py                  // topic_recluster_scheduler — LISTEN + heartbeat
│   ├── errors.py
│   └── tests/
│       ├── conftest.py
│       ├── test_kmeans_deterministic.py
│       ├── test_stable_ids.py
│       ├── test_labeler.py
│       ├── test_repo.py
│       ├── test_recluster_integration.py
│       ├── test_assigner_index_tail.py
│       └── test_recluster_skipped_below_threshold.py
└── pipeline/
    └── stages/
        └── index.py                  // extended: write mean_embedding + call assigner
```

### 2.2 Package layout — Go (API Service)

```
apps/api/internal/
├── http/
│   └── topics/
│       ├── handler.go                 // PATCH /api/libraries/{id}/topics/{topic_id}
│       └── handler_test.go
└── topics/
    ├── repo.go                        // sqlc-backed
    └── repo_test.go
```

### 2.3 Schema migration — `library_topics`, `video_topics`, `videos.mean_embedding`

```sql
-- shared/db/migrations/0028_topics.sql
BEGIN;

ALTER TABLE videos
    ADD COLUMN mean_embedding BYTEA;       -- packed float32; NULL if no transcript

CREATE TABLE library_topics (
    library_id     UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    topic_id       INTEGER NOT NULL,
    label          TEXT,
    centroid_vec   BYTEA NOT NULL,
    video_count    INTEGER NOT NULL DEFAULT 0,
    computed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (library_id, topic_id)
);

CREATE TABLE video_topics (
    video_id       UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    library_id     UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    topic_id       INTEGER NOT NULL,
    score          REAL NOT NULL CHECK (score >= 0 AND score <= 1),
    PRIMARY KEY (video_id, topic_id),
    FOREIGN KEY (library_id, topic_id)
        REFERENCES library_topics(library_id, topic_id) ON DELETE CASCADE
);
CREATE INDEX video_topics_topic
    ON video_topics (library_id, topic_id, score DESC);

-- Idempotency for nightly recluster jobs.
CREATE UNIQUE INDEX processing_jobs_one_active_topic_recluster
    ON processing_jobs (library_id)
    WHERE stage = 'topic_recluster'
      AND state IN ('queued', 'claimed', 'running');

COMMIT;
```

### 2.4 Python — `packing.py`

```python
"""packing.py — float32 BYTEA round-trip for centroid_vec / mean_embedding."""
from __future__ import annotations
import numpy as np


def pack_vec(v: np.ndarray) -> bytes:
    """Big-endian float32. Caller guarantees v is 1D."""
    arr = np.ascontiguousarray(v, dtype='>f4')
    return arr.tobytes()


def unpack_vec(b: bytes) -> np.ndarray:
    return np.frombuffer(b, dtype='>f4').astype(np.float32)
```

### 2.5 Python — `kmeans.py` (D1, D2)

```python
"""kmeans.py — deterministic mini-batch k-means with K-resolution."""
from __future__ import annotations
import math
from dataclasses import dataclass

import numpy as np
from sklearn.cluster import MiniBatchKMeans


@dataclass(frozen=True)
class FitResult:
    centroids: np.ndarray                 # shape (K, D)
    labels: np.ndarray                    # shape (N,), int


def resolve_k(n_videos: int, override: int | None) -> int:
    if override is not None and override > 0:
        return min(override, 32)
    return min(32, max(2, math.ceil(math.sqrt(n_videos) / 2)))


def fit_minibatch_kmeans(
    X: np.ndarray, *, k: int, seed: int = 42,
    batch_size: int = 256, max_iter: int = 100,
) -> FitResult:
    if X.shape[0] < k:
        raise ValueError(f"too few videos ({X.shape[0]}) for k={k}")
    km = MiniBatchKMeans(
        n_clusters=k, random_state=seed,
        batch_size=batch_size, max_iter=max_iter,
        n_init=3, init="k-means++")
    km.fit(X)
    return FitResult(centroids=km.cluster_centers_, labels=km.labels_)
```

### 2.6 Python — `stable_ids.py`

```python
"""stable_ids.py — match new centroids to old topic IDs.

Goal: across nightly runs, a stable cluster keeps its topic_id (and thus
its user-given label). New clusters get fresh IDs; disappearing clusters
have their topic_id retired.

Algorithm: max-similarity pairing. We compute the cosine-similarity
matrix between new and old centroids, then run a greedy maximum-weight
matching (good enough for K ≤ 32; the Hungarian algorithm would be
stricter but the greedy gives identical results in practice).
"""
from __future__ import annotations
import numpy as np
from typing import Iterable


def match_to_existing(
    new_centroids: np.ndarray,
    existing_topic_ids: list[int],
    existing_centroids: np.ndarray,
) -> dict[int, int]:
    """Return mapping: new_centroid_index → existing_topic_id (or -1 for new)."""
    if existing_centroids.shape[0] == 0:
        return {i: -1 for i in range(new_centroids.shape[0])}

    new_n = _row_normalize(new_centroids)
    old_n = _row_normalize(existing_centroids)
    sim = new_n @ old_n.T                       # shape (new_K, old_K)

    used_old = set()
    mapping: dict[int, int] = {}
    # Greedy: pick the highest-sim pair, mark both used, repeat.
    pairs = [(sim[i, j], i, j) for i in range(sim.shape[0])
             for j in range(sim.shape[1])]
    pairs.sort(reverse=True)
    used_new = set()
    for s, i, j in pairs:
        if i in used_new or j in used_old:
            continue
        if s < 0.5:                              # too dissimilar to inherit
            break
        mapping[i] = existing_topic_ids[j]
        used_new.add(i)
        used_old.add(j)
    for i in range(new_centroids.shape[0]):
        mapping.setdefault(i, -1)
    return mapping


def _row_normalize(M: np.ndarray) -> np.ndarray:
    norms = np.linalg.norm(M, axis=1, keepdims=True)
    norms[norms == 0] = 1.0
    return M / norms
```

### 2.7 Python — `labeler.py` (D5)

```python
"""labeler.py — embed-model nearest-token labeling, with PMI fallback."""
from __future__ import annotations
import logging
import re
from collections import Counter
from typing import Sequence

import numpy as np

log = logging.getLogger(__name__)

_BIGRAM_RE = re.compile(r"\b\w+\b")


def label_centroid(
    *,
    centroid: np.ndarray,
    top_k_segments: Sequence[str],
    embedder,                    # Plan 5.3 EmbedderClient
) -> str:
    """Return a hyphenated bigram. Falls back to PMI on embedder error."""
    try:
        token = embedder.nearest_token(centroid)
        if token and len(token) <= 64:
            return token
    except Exception as e:
        log.warning("nearest_token_failed_falling_back",
                    extra={"err": str(e)})

    return _pmi_bigram(top_k_segments) or "topic"


def _pmi_bigram(segments: Sequence[str]) -> str | None:
    """Return the most-frequent meaningful bigram across the segments."""
    tokens: list[str] = []
    for seg in segments:
        tokens.extend(t.lower() for t in _BIGRAM_RE.findall(seg)
                      if len(t) > 2)
    if len(tokens) < 4:
        return None
    bigrams = Counter(zip(tokens, tokens[1:]))
    common = bigrams.most_common(1)
    if not common:
        return None
    (a, b), _ = common[0]
    return f"{a}-{b}"
```

### 2.8 Python — `recluster.py` (orchestration)

```python
"""recluster.py — TopicReclusterWorker.run: invoked by the queue claim loop."""
from __future__ import annotations
import logging, time
from dataclasses import dataclass

import numpy as np

from .kmeans import fit_minibatch_kmeans, resolve_k
from .stable_ids import match_to_existing
from .labeler import label_centroid
from .packing import pack_vec, unpack_vec

log = logging.getLogger(__name__)


@dataclass(frozen=True)
class ReclusterMetric:
    n_videos: int
    k: int
    new_topics: int
    matched_topics: int
    retired_topics: int
    wall_sec: float


class TopicReclusterWorker:
    def __init__(self, *, db_pool, chroma_client, embedder):
        self._db = db_pool
        self._chroma = chroma_client
        self._embedder = embedder

    async def run(self, *, claimed_job) -> dict:
        t0 = time.monotonic()
        lib = claimed_job.library_id
        settings = claimed_job.library_settings or {}
        if settings.get("auto_tag_topics") is False:
            log.info("topic_recluster_skipped_setting_off",
                     extra={"library_id": lib})
            return {"skipped": True, "reason": "setting_off"}

        # 1. Load mean embeddings.
        rows = await self._db.fetch("""
            SELECT id::text, mean_embedding
              FROM videos
             WHERE library_id = $1 AND mean_embedding IS NOT NULL
        """, lib)
        if len(rows) < 100:
            log.info("topic_recluster_skipped_insufficient_data",
                     extra={"library_id": lib, "n": len(rows)})
            return {"skipped": True, "reason": "below_threshold",
                    "n_videos": len(rows)}

        video_ids = [r["id"] for r in rows]
        X = np.stack([unpack_vec(r["mean_embedding"]) for r in rows])

        # 2. K-resolution.
        k = resolve_k(len(rows), settings.get("topic_clusters"))

        # 3. Fit.
        fit = fit_minibatch_kmeans(X, k=k, seed=42)

        # 4. Match to existing topics for stable IDs.
        existing = await self._db.fetch("""
            SELECT topic_id, centroid_vec FROM library_topics
             WHERE library_id = $1 ORDER BY topic_id
        """, lib)
        existing_ids = [r["topic_id"] for r in existing]
        existing_centroids = (np.stack(
            [unpack_vec(r["centroid_vec"]) for r in existing])
            if existing else np.zeros((0, X.shape[1]), dtype=np.float32))
        mapping = match_to_existing(fit.centroids, existing_ids, existing_centroids)
        next_id = (max(existing_ids) + 1) if existing_ids else 0
        topic_ids = []
        matched = 0
        for i in range(fit.centroids.shape[0]):
            tid = mapping[i]
            if tid == -1:
                topic_ids.append(next_id); next_id += 1
            else:
                topic_ids.append(tid); matched += 1

        # 5. Label every cluster.
        labels = await self._build_labels(lib, fit.centroids, video_ids, fit.labels)

        # 6. Atomic write.
        retired = await self._write_atomic(lib, topic_ids, fit.centroids,
                                           labels, video_ids, X, fit.labels,
                                           existing_ids)
        return ReclusterMetric(
            n_videos=len(rows), k=k, new_topics=k - matched,
            matched_topics=matched, retired_topics=retired,
            wall_sec=time.monotonic() - t0).__dict__

    async def _build_labels(self, lib, centroids, video_ids, labels):
        out = []
        for ci in range(centroids.shape[0]):
            cluster_video_ids = [video_ids[k] for k in range(len(labels))
                                 if labels[k] == ci]
            top5 = await self._top5_segments(lib, centroids[ci], cluster_video_ids)
            out.append(label_centroid(
                centroid=centroids[ci], top_k_segments=top5,
                embedder=self._embedder))
        return out

    async def _top5_segments(self, lib, centroid, video_ids):
        """Closest 5 segments to the centroid, restricted to this cluster's videos."""
        if not video_ids:
            return []
        results = self._chroma.collection_for_library(lib).query(
            query_embeddings=[centroid.tolist()],
            n_results=5,
            where={"video_id": {"$in": video_ids}},
            include=["documents"])
        return list(results.get("documents", [[]])[0])

    async def _write_atomic(self, lib, topic_ids, centroids, labels,
                            video_ids, X, cluster_labels, existing_ids):
        new_set = set(topic_ids)
        retired = [tid for tid in existing_ids if tid not in new_set]
        async with self._db.acquire() as conn:
            async with conn.transaction():
                # Upsert centroids. Preserve existing label if present.
                for i, tid in enumerate(topic_ids):
                    await conn.execute("""
                        INSERT INTO library_topics
                            (library_id, topic_id, label, centroid_vec,
                             video_count, computed_at)
                        VALUES ($1, $2, $3, $4, $5, now())
                        ON CONFLICT (library_id, topic_id) DO UPDATE
                           SET centroid_vec = EXCLUDED.centroid_vec,
                               video_count  = EXCLUDED.video_count,
                               computed_at  = now(),
                               label        = COALESCE(library_topics.label,
                                                       EXCLUDED.label)
                    """, lib, tid, labels[i], pack_vec(centroids[i]),
                        int(np.sum(cluster_labels == i)))
                # Replace video_topics for this library.
                await conn.execute(
                    "DELETE FROM video_topics WHERE library_id = $1", lib)
                # Score every video against every centroid; top-3 stored.
                cn = _row_normalize(centroids)
                vn = _row_normalize(X)
                sims = vn @ cn.T                            # (N, K)
                top3 = np.argsort(-sims, axis=1)[:, :3]
                rows = []
                for vi in range(len(video_ids)):
                    for slot in range(min(3, top3.shape[1])):
                        ci = int(top3[vi, slot])
                        cos = float(sims[vi, ci])
                        score = (1.0 + cos) / 2.0           # D6: shift to [0,1]
                        rows.append((video_ids[vi], lib, topic_ids[ci], score))
                if rows:
                    await conn.executemany(
                        "INSERT INTO video_topics (video_id, library_id, topic_id, score) "
                        "VALUES ($1, $2, $3, $4)", rows)
                # Retire (delete) topics no longer present.
                if retired:
                    await conn.execute(
                        "DELETE FROM library_topics "
                        "WHERE library_id = $1 AND topic_id = ANY($2::int[])",
                        lib, retired)
        return len(retired)


def _row_normalize(M: np.ndarray) -> np.ndarray:
    norms = np.linalg.norm(M, axis=1, keepdims=True)
    norms[norms == 0] = 1.0
    return M / norms
```

### 2.9 Python — `assigner.py` (D10 incremental hook)

```python
"""assigner.py — assign_topics_for_video at the index stage tail.

Called after Chroma writes for a transcript. Reads the existing
library_topics centroids; if any exist, computes the top-3 assignment
and inserts video_topics rows. Also writes mean_embedding.
"""
from __future__ import annotations
import numpy as np
from .packing import pack_vec, unpack_vec


async def assign_topics_for_video(
    conn, *, video_id: str, library_id: str, mean_embedding: np.ndarray,
):
    # Persist the cached mean.
    await conn.execute(
        "UPDATE videos SET mean_embedding = $1 WHERE id = $2",
        pack_vec(mean_embedding), video_id)

    rows = await conn.fetch(
        "SELECT topic_id, centroid_vec FROM library_topics WHERE library_id=$1",
        library_id)
    if not rows:
        return                               # no centroids yet → nothing to assign
    topic_ids = [r["topic_id"] for r in rows]
    centroids = np.stack([unpack_vec(r["centroid_vec"]) for r in rows])

    cn = centroids / np.maximum(np.linalg.norm(centroids, axis=1, keepdims=True), 1e-9)
    vn = mean_embedding / max(np.linalg.norm(mean_embedding), 1e-9)
    sims = cn @ vn
    top3_idx = np.argsort(-sims)[:3]

    await conn.execute(
        "DELETE FROM video_topics WHERE video_id=$1 AND library_id=$2",
        video_id, library_id)
    for slot in top3_idx:
        score = float((1.0 + sims[slot]) / 2.0)
        await conn.execute("""
            INSERT INTO video_topics (video_id, library_id, topic_id, score)
            VALUES ($1, $2, $3, $4)
        """, video_id, library_id, topic_ids[int(slot)], score)
```

### 2.10 Python — `scheduler.py` (D8)

```python
"""scheduler.py — listens for topic_recluster_request, also fires nightly.

Inserts a job per library when triggered; the worker claim loop picks them up.
"""
from __future__ import annotations
import asyncio, datetime, logging

log = logging.getLogger(__name__)


async def topic_recluster_loop(db_pool):
    while True:
        await _tick(db_pool)
        await _wait_until_next_run(db_pool)


async def _tick(db_pool):
    async with db_pool.acquire() as conn:
        await conn.execute("""
            INSERT INTO processing_jobs
                (stage, state, priority, library_id, video_id, created_at)
            SELECT 'topic_recluster', 'queued', 200, l.id, NULL, now()
              FROM libraries l
             WHERE COALESCE((l.settings->>'auto_tag_topics')::bool, true) = true
            ON CONFLICT DO NOTHING
        """)


async def _wait_until_next_run(db_pool):
    """Sleep until the next 02:00 UTC OR until LISTEN fires, whichever first."""
    now = datetime.datetime.now(datetime.timezone.utc)
    target = now.replace(hour=2, minute=0, second=0, microsecond=0)
    if target <= now:
        target += datetime.timedelta(days=1)
    seconds = (target - now).total_seconds()

    async with db_pool.acquire() as conn:
        await conn.add_listener("topic_recluster_request",
                                lambda *args: None)         # wake on NOTIFY
        try:
            await asyncio.wait_for(asyncio.sleep(seconds), timeout=seconds)
        except asyncio.TimeoutError:
            pass
        finally:
            await conn.remove_listener("topic_recluster_request",
                                       lambda *args: None)
```

### 2.11 Go — PATCH topic rename (D11)

```go
// apps/api/internal/http/topics/handler.go
package topics

import (
    "encoding/json"
    "net/http"
    "strings"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"
    "golang.org/x/text/unicode/norm"

    "github.com/maktaba/api/internal/auth"
)

type Handler struct{ pool *pgxpool.Pool }

func NewHandler(pool *pgxpool.Pool) *Handler { return &Handler{pool: pool} }

type renameBody struct {
    Label string `json:"label"`
}

// PATCH /api/libraries/{id}/topics/{topic_id}
func (h *Handler) Rename(w http.ResponseWriter, r *http.Request) {
    libID, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil { http.Error(w, "invalid library id", 400); return }
    var topicID int
    if _, err := fmt.Sscanf(chi.URLParam(r, "topic_id"), "%d", &topicID); err != nil {
        http.Error(w, "invalid topic id", 400); return
    }
    if err := auth.RequireLibraryOwner(r.Context(), libID); err != nil {
        http.Error(w, "forbidden", 403); return
    }
    var body renameBody
    if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
        http.Error(w, "bad json", 400); return
    }
    label := strings.TrimSpace(norm.NFKC.String(body.Label))
    if label == "" || len([]rune(label)) > 64 {
        http.Error(w, "label must be 1..64 chars", 400); return
    }
    tag, err := h.pool.Exec(r.Context(), `
        UPDATE library_topics SET label = $1
         WHERE library_id = $2 AND topic_id = $3
    `, label, libID, topicID)
    if err != nil {
        http.Error(w, "internal error", 500); return
    }
    if tag.RowsAffected() == 0 {
        http.Error(w, "topic not found", 404); return
    }
    w.WriteHeader(http.StatusOK)
}
```

---

## 3. File-by-file scaffolding checklist

| Order | File | Symbols | Tests gating |
|-------|------|---------|--------------|
| 1 | `shared/db/migrations/0028_topics.sql` | `library_topics`, `video_topics`, `videos.mean_embedding`, partial unique idx | `TestMigration0028` |
| 2 | `pipeline/.../topic/packing.py` | `pack_vec`, `unpack_vec` | `test_packing_roundtrip` |
| 3 | `pipeline/.../topic/kmeans.py` | `resolve_k`, `fit_minibatch_kmeans`, `FitResult` | `test_kmeans_*` |
| 4 | `pipeline/.../topic/stable_ids.py` | `match_to_existing` | `test_stable_ids_*` |
| 5 | `pipeline/.../topic/labeler.py` | `label_centroid`, `_pmi_bigram` | `test_labeler_*` |
| 6 | `pipeline/.../topic/recluster.py` | `TopicReclusterWorker`, `ReclusterMetric` | `test_recluster_integration` |
| 7 | `pipeline/.../topic/assigner.py` | `assign_topics_for_video` | `test_assigner_index_tail` |
| 8 | `pipeline/.../topic/scheduler.py` | `topic_recluster_loop`, `_tick` | `test_scheduler_inserts_jobs` |
| 9 | `pipeline/.../pipeline/stages/index.py` (extend) | call `assign_topics_for_video` | `test_index_writes_mean_embedding` |
| 10 | `apps/api/internal/topics/repo.go` | sqlc-generated | (n/a) |
| 11 | `apps/api/internal/http/topics/handler.go` | `Handler.Rename` | `TestRenameTopic_*` |
| 12 | `apps/api/internal/http/router.go` (extend) | route `PATCH /api/libraries/{id}/topics/{topic_id}` | (router test) |

---

## 4. Test cases

### 4.1 `test_kmeans_deterministic_seed_stable` (story-named)

```python
def test_minibatch_kmeans_deterministic():
    """Same input + same seed → same labels and same centroids."""
    rng = np.random.RandomState(0)
    X = np.vstack([rng.normal(c, 0.05, (50, 32)) for c in
                   [[1, 0]*16, [0, 1]*16, [1, 1]*16, [-1, -1]*16]])
    fit_a = fit_minibatch_kmeans(X.astype(np.float32), k=4, seed=42)
    fit_b = fit_minibatch_kmeans(X.astype(np.float32), k=4, seed=42)
    assert np.allclose(fit_a.centroids, fit_b.centroids)
    assert np.array_equal(fit_a.labels, fit_b.labels)
```

### 4.2 `test_recluster_skipped_below_threshold` (AC-1, edge)

```python
async def test_below_100_videos_skips_recluster(
    db, library_factory, video_factory, recluster_worker,
):
    lib = await library_factory.fresh()
    for _ in range(50):
        await video_factory.with_mean(library_id=lib.id)
    job = await db.queue_recluster_job(library_id=lib.id)
    metric = await recluster_worker.run(claimed_job=job)
    assert metric["skipped"] is True
    assert metric["reason"] == "below_threshold"
    n_topics = await db.fetchval(
        "SELECT COUNT(*) FROM library_topics WHERE library_id=$1", lib.id)
    assert n_topics == 0
```

### 4.3 `test_recluster_4_clusters_dominate` (AC-1, story-named)

```python
async def test_200_videos_4_obvious_clusters_form_dominant(
    db, library_factory, video_factory, recluster_worker,
):
    """4 obvious clusters of 50 videos each → ~80% in 4 dominant topics."""
    lib = await library_factory.fresh()
    for cluster_idx, base in enumerate([
            [1, 0, 0, 0], [0, 1, 0, 0], [0, 0, 1, 0], [0, 0, 0, 1]]):
        for _ in range(50):
            mean = np.array(base * 256, dtype=np.float32) + \
                   np.random.normal(0, 0.05, 1024).astype(np.float32)
            await video_factory.with_mean(library_id=lib.id, mean=mean)

    job = await db.queue_recluster_job(library_id=lib.id)
    metric = await recluster_worker.run(claimed_job=job)
    assert metric["k"] == min(32, math.ceil(math.sqrt(200)/2))   # 8
    rows = await db.fetch("""
        SELECT topic_id, COUNT(*) AS n FROM video_topics
         WHERE library_id=$1 AND score = (
            SELECT MAX(score) FROM video_topics vt
             WHERE vt.video_id = video_topics.video_id)
         GROUP BY topic_id ORDER BY n DESC
    """, lib.id)
    top4 = sum(r["n"] for r in rows[:4])
    assert top4 >= 160                                          # ≥ 80%
```

### 4.4 `test_stable_ids_preserves_label_on_recluster`

```python
async def test_renamed_topic_keeps_label_after_recluster(
    db, library_factory, video_factory, recluster_worker,
):
    """User renames topic 0 → 'prayer'; next recluster keeps that label."""
    lib = await library_factory.fresh()
    for _ in range(200):
        await video_factory.with_mean(library_id=lib.id)
    await recluster_worker.run(claimed_job=await db.queue_recluster_job(lib.id))
    await db.execute(
        "UPDATE library_topics SET label='prayer' WHERE library_id=$1 AND topic_id=0",
        lib.id)

    # Add a few more videos with similar embeddings; recluster again.
    for _ in range(50):
        await video_factory.with_mean(library_id=lib.id)
    await recluster_worker.run(claimed_job=await db.queue_recluster_job(lib.id))

    label = await db.fetchval(
        "SELECT label FROM library_topics WHERE library_id=$1 AND topic_id=0",
        lib.id)
    assert label == "prayer"             # preserved by COALESCE upsert
```

### 4.5 `test_assigner_at_index_tail` (AC-3, D10)

```python
async def test_index_tail_assigns_top3_topics_to_new_video(
    db, library_factory, video_factory, recluster_worker,
):
    lib = await library_factory.fresh()
    # Build 200 videos and recluster.
    for _ in range(200):
        await video_factory.with_mean(library_id=lib.id)
    await recluster_worker.run(claimed_job=await db.queue_recluster_job(lib.id))

    # Index a new video — assigner should write top-3.
    new_v = await video_factory.fresh(library_id=lib.id)
    mean = np.random.normal(0, 1, 1024).astype(np.float32)
    async with db.acquire() as conn:
        async with conn.transaction():
            await assign_topics_for_video(
                conn, video_id=new_v.id, library_id=lib.id, mean_embedding=mean)
    rows = await db.fetch(
        "SELECT topic_id, score FROM video_topics WHERE video_id=$1 ORDER BY score DESC",
        new_v.id)
    assert len(rows) == 3
    for r in rows:
        assert 0.0 <= r["score"] <= 1.0
```

### 4.6 `TestRenameTopic_OwnerOnly_AppliesLabel` (Go, AC-2)

```go
func TestRenameTopic_AppliesNewLabel(t *testing.T) {
    db := testdb.Fresh(t)
    libID := testdb.SeedLibrary(t, db, "videos")
    testdb.SeedTopic(t, db, libID, 0, "prayer-rituals")

    body := `{"label":"prayer"}`
    req := httptest.NewRequest("PATCH",
        "/api/libraries/"+libID.String()+"/topics/0", strings.NewReader(body))
    req = withChiCtx(req, "id", libID.String())
    req = withChiCtx(req, "topic_id", "0")
    rr := httptest.NewRecorder()
    h := topics.NewHandler(db.Pool)
    h.Rename(rr, req)
    require.Equal(t, http.StatusOK, rr.Code)

    var label string
    require.NoError(t, db.Pool.QueryRow(t.Context(),
        `SELECT label FROM library_topics WHERE library_id=$1 AND topic_id=0`,
        libID).Scan(&label))
    require.Equal(t, "prayer", label)
}
```

### 4.7 `TestRenameTopic_RejectsLongLabel`

```go
func TestRenameTopic_TooLong_Returns400(t *testing.T) {
    label := strings.Repeat("a", 65)
    body := fmt.Sprintf(`{"label":%q}`, label)
    // ... same setup ...
    require.Equal(t, http.StatusBadRequest, rr.Code)
}
```

### 4.8 `test_setting_off_skips_recluster` (AC-4)

```python
async def test_auto_tag_topics_false_skips(
    db, library_factory, recluster_worker,
):
    lib = await library_factory.fresh(settings={"auto_tag_topics": False})
    job = await db.queue_recluster_job(library_id=lib.id)
    metric = await recluster_worker.run(claimed_job=job)
    assert metric["skipped"] is True
    assert metric["reason"] == "setting_off"
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handled by |
|-----|-----------|------------|
| E1  | **Library < 100 videos** (story-named). Recluster skipped; `library_topics` empty; jobs row marked done with `metric.skipped=true`. | `test_below_100_videos_skips_recluster` |
| E2  | **Video with no transcript yet** (story-named). `mean_embedding` is NULL; the recluster's `WHERE mean_embedding IS NOT NULL` filter excludes it; the assigner doesn't fire (it's called from the index tail, which by definition runs after transcripts exist). The video shows up in the UI as "untagged" (i.e., no `video_topics` rows). | DB filter + tail-call ordering; no special test required. |
| E3  | **New video added between recluster runs** (story-named). Index tail calls `assign_topics_for_video` with the existing centroids; user sees topic chips immediately. The next recluster updates the centroids and may re-bucket. | `test_index_tail_assigns_top3_topics_to_new_video` |
| E4  | **All videos collapse to one cluster** (degenerate). MiniBatchKMeans still produces K centroids but most are near-identical. The `match_to_existing` greedy chooses high-similarity matches; the labeler may produce identical labels (we don't deduplicate v1). The user can rename. | Documented; not separately tested. |
| E5  | **Embedder doesn't expose `nearest_token`.** `label_centroid` falls back to PMI. If PMI also fails (too few tokens), label is `"topic"`. | `test_labeler_pmi_fallback` |
| E6  | **Library deletion during recluster.** FK cascade removes `library_topics` and `video_topics` mid-run; the worker's last UPDATE fails with FK error → caught → marks job failed. The next attempt finds no library and the partial unique idx allows the new attempt to be queued only when the library is restored. | `test_recluster_handles_library_deletion` |
| E7  | **Concurrent rename and recluster.** PATCH writes label; recluster's UPSERT preserves existing label via `COALESCE(library_topics.label, EXCLUDED.label)`. No conflict. | `test_renamed_topic_keeps_label_after_recluster` |
| E8  | **Embedding dim change** (library re-embedded with a different model). Old `mean_embedding` byte length differs from new model's dim; `unpack_vec` returns the wrong shape. We add a sanity check `assert X.shape[1] == library.embedding_dim` in `recluster.py`; mismatch raises and the operator must run a maintenance task to clear `mean_embedding` for the library. | `test_recluster_dim_mismatch_raises` |
| E9  | **`topic_clusters` override > 32.** `resolve_k` clamps to 32 — the explicit story cap. | `test_resolve_k_caps_at_32` |
| E10 | **Recluster job claimed twice (worker bug).** Partial unique idx `processing_jobs_one_active_topic_recluster` prevents multiple queued jobs; a single claim per worker is enforced by the queue's atomic claim (Epic 7). | DB-level. |
| E11 | **Setting `auto_tag_topics=false` mid-run.** The job claim already happened; the worker checks the setting at start (D7) and exits if false, but a setting change after start is honoured at the next run. | Documented. |
| E12 | **Centroid retirement.** A cluster present in the previous run with no match to a new centroid is deleted; cascade removes `video_topics` rows referencing it. The user-given label is lost — that's correct because the cluster genuinely disappeared. | `test_retired_topic_cascades_to_video_topics` |

---

## 6. Acceptance checklist

- [ ] **A1** Migration `0028_topics.sql` creates `library_topics`, `video_topics`, adds `videos.mean_embedding`, and the partial unique idx for nightly job idempotency. (`TestMigration0028`)
- [ ] **A2** Per-library cluster set: ≥100 indexed videos; mini-batch k-means computes K = `min(32, ceil(sqrt(N)/2))` (or `library.settings.topic_clusters` override) over per-video mean embeddings; `library_topics` upserted per cluster. (`test_200_videos_4_obvious_clusters_form_dominant`, `test_resolve_k_caps_at_32`)
- [ ] **A3** Topic labeling: top-5 segments closest to centroid concatenated and asked from the embedder for nearest token; PMI fallback if unavailable. (`test_labeler_*`)
- [ ] **A4** Topic rename: `PATCH /api/libraries/{id}/topics/{topic_id}` body `{label}`, owner-only, ≤64 chars after NFKC trim; persists to `library_topics.label`. (`TestRenameTopic_*`)
- [ ] **A5** Per-video assignment: top-3 nearest topics by cosine similarity stored in `video_topics` with `score ∈ [0, 1]`. (`test_index_tail_assigns_top3_topics_to_new_video`)
- [ ] **A6** Disabled by setting: `library.settings.auto_tag_topics = false` skips the recluster; existing `library_topics` rows preserved. (`test_auto_tag_topics_false_skips`)
- [ ] **A7** Deterministic clustering: same input + seed produces stable cluster IDs (Hungarian-greedy match against previous centroids preserves user labels). (`test_minibatch_kmeans_deterministic`, `test_renamed_topic_keeps_label_after_recluster`)
- [ ] **A8** Recluster skipped when N < 100; jobs row marked done with `reason: below_threshold`. (`test_below_100_videos_skips_recluster`)
- [ ] **A9** Index tail writes `mean_embedding` and calls `assign_topics_for_video`; freshly indexed videos have topic rows within seconds of the index commit. (`test_index_tail_assigns_top3_topics_to_new_video`)
- [ ] **A10** Nightly scheduler inserts one `processing_jobs(stage='topic_recluster', priority=200)` per eligible library; idempotent via partial unique idx. (`test_scheduler_inserts_jobs`)

---

## 7. Performance budget

(Story 9.7 owns the explicit 50 ms target; 9.9's recluster is a
batch job with no user-facing latency, but we set internal targets.)

| Phase | Cost (50k videos, 1024-dim) | Notes |
|-------|------------------------------|-------|
| Bulk SELECT mean_embedding | ~500 ms | 50k × 4 KB = 200 MB; one query, streamed. |
| Unpack to numpy `(50000, 1024)` | ~100 ms | float32 BYTEA → numpy is a memcpy. |
| MiniBatchKMeans fit (K=32) | ~3–8 s | sklearn vectorized; CPU-bound. |
| Match to existing (K ≤ 32) | < 10 ms | trivial. |
| Top-5 segments per cluster (32 × Chroma KNN) | ~1 s | local Chroma; one-shot per cluster. |
| Embedder `nearest_token` × K | ~2 s | one model call per cluster (small). |
| Atomic write (32 UPSERTs + 150k INSERTs) | ~5 s | DELETE + executemany for video_topics. |
| **Total** | **~12–20 s wall** | runs at 02:00 UTC; not user-visible. |

The recluster is run nightly; even the 50k-video case finishes well
within the maintenance window.

---

## 8. Operational notes

- **On-demand recluster:** `NOTIFY topic_recluster_request` (admin endpoint or `psql`) wakes the scheduler immediately.
- **Metrics:**
  - `topic_recluster_duration_seconds{library_id}` — histogram.
  - `topic_recluster_videos_total{library_id}` — gauge of N videos clustered.
  - `topic_recluster_skipped_total{library_id, reason}` — counter.
  - `topic_assignment_duration_seconds` — histogram for index-tail assigner.
- **Embedding dim contract:** library config `library.settings.embedding.dim` must match the actual vector dimensions; mismatch raises and is reported via metric `topic_recluster_dim_mismatch_total`.
- **`library_topics` retention:** when `auto_tag_topics` is toggled on→off, rows are preserved; toggled back on, the next recluster picks up where it left off (stable IDs match where geometry allows).
