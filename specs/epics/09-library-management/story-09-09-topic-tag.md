# Story 9.9 — Auto-categorization: topic tag

After `INDEXED`, each video is tagged with its top-K nearest cluster
centroids in the library's vector space; clusters are recomputed nightly
(§5.2).

**AC-1 — Per-library cluster set.**
- **Given** a library with ≥100 indexed videos,
- **When** the nightly recluster runs,
- **Then** mini-batch k-means computes K clusters (default
  `topic_clusters = sqrt(N)/2`, capped at 32) over per-video mean
  embeddings, and a `library_topics` row is upserted per cluster
  (schema in [README.md](README.md)).

**AC-2 — Topic labeling and rename.**
- **Given** a freshly-formed cluster,
- **When** the labeler runs,
- **Then** the top-5 segments closest to the centroid are concatenated
  and asked from the embedder for nearest token — the resulting bigram is
  the human-readable label (e.g., `"prayer-rituals"`). The user can
  rename via `PATCH /api/libraries/{id}/topics/{topic_id}` with body
  `{label: <string>}`. Rename is owner-only (admin or library owner)
  and capped at 64 chars.

**AC-3 — Per-video assignment.**
- **Given** a video with a mean embedding,
- **When** scored against the library's centroids,
- **Then** the top-3 nearest topics by cosine similarity are stored in
  `video_topics` (schema in [README.md](README.md)).

**AC-4 — Disabled by setting.**
- **Given** library setting `auto_tag_topics: false`,
- **When** the recluster runs,
- **Then** the library is skipped (its `library_topics` rows are
  preserved but unused).

**Test cases:**
- Unit: k-means with a deterministic seed produces stable cluster ids
  across runs given identical input.
- Integration: a fixture with 200 videos in 4 obvious clusters → K=14
  centroids form, the 4 dominant ones contain ~80% of videos.
- Integration: relabeling a topic via PATCH propagates to UI immediately.

**Edge cases:**
- Library with < 100 videos — recluster is skipped (insufficient data);
  topics remain empty until threshold is crossed.
- A video with no transcript yet — no topic assignment; the video appears
  under "untagged".
- A new video added between recluster nightly runs — assigned topics
  using existing centroids on the next index commit; the centroids
  drift over the next recluster.
