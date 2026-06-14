# Plan 26.3 — Series detection — implementation

> Implementation plan for [story-26-03-series-detection.md](story-26-03-series-detection.md).
> Self-contained. Cross-links: consumes `media_parsed_titles`
> ([Plan 26.1](plan-26-01-title-parser.md)), `video_classification`
> ([Plan 26.2](plan-26-02-transcript-topic-extraction.md)), diarization
> `speakers`/voiceprints ([Story 9.11](../09-library-management/README.md),
> slots 0035/0048), and `videos.poster_path` thumbnails. Triggered as a
> debounced library pass by
> [Plan 26.7](plan-26-07-background-enrichment-pipeline.md). Writes slot
> 0075. API endpoints (Go) for merge/split/edit are owned here.

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **Library-level pass over a union-find, not per-video.** Build candidate pairs, score them, union above threshold, materialise components as series. | Grouping is global: one new episode can rename a series or merge two. Union-find is O(α·E) and idempotent. |
| D2 | **Weighted score, name-dominant.** `score = w_name·name_sim + w_spk·speaker_overlap + w_poster·poster_sim + w_topic·topic_overlap`. Defaults: name 0.55, speaker 0.25, poster 0.12, topic 0.08; `series_link_threshold` set so a clean name match alone clears it. | Story AC: name alone suffices; others rescue/raise. Linear weights are tunable per library and inspectable. |
| D3 | **Candidate generation by normalised-show-name blocking**, not all-pairs. Block on `name_norm(show_name)` prefix; only compute the expensive speaker/poster signals within and across adjacent blocks. | All-pairs is O(N²); a 10k library would be 50M pairs. Blocking by show name keeps it near-linear; cross-block checks (for garbled names) are gated by a cheap poster/speaker prefilter. |
| D4 | **User overrides are sticky via flags**, honoured by the pass. `series.is_user_named`, `series_episodes.is_user_override`; merges/splits set them. The pass fills only around overrides. | Story AC: automation never reverts a human decision. |
| D5 | **Poster similarity = dHash (perceptual), 64-bit, Hamming distance.** Computed from the existing `poster_path` thumbnail; cached on the video row metadata. No face recognition. | Cheap, deterministic, no model; story explicitly scopes out faces. |
| D6 | **Speaker overlap = Jaccard over each video's set of matched `speaker_id`s** (from diarization `segment_speakers`). | Reuses Story 9.11 output directly; set overlap is the natural "same recurring cast" measure. |
| D7 | **Movies excluded from series** (`kind != episode` and no S/E). They flow to collections (26.4) instead. | Story scope: series = episodic only in v1. |
| D8 | **Atomic rebuild within a transaction, preserving overrides and pinned series.** Detected (non-override) series are recomputed; user series are kept and only extended. | Idempotence + safety: a crashed pass leaves the prior grouping intact. |

If D3 (blocking) is rejected for all-pairs: correctness is identical but
the pass becomes unusable on large libraries — rejected on performance.

---

## 1. Package layout (Pipeline Service)

```
pipeline/src/maktaba_pipeline/classify/series/
├── __init__.py
├── pass_.py             # run_series_detect(library_id) — the debounced entry (D1, D8)
├── candidates.py        # name-blocking + cross-block prefilter (D3)
├── signals.py           # name_sim, speaker_overlap, poster_sim, topic_overlap (D2,D5,D6)
├── unionfind.py         # disjoint-set
├── materialize.py       # components → series/series_episodes, override-aware (D4,D8)
├── repo.py              # reads parsed/classification/speakers; writes series tables
└── tests/
    ├── test_signals.py
    ├── test_candidates.py
    ├── test_pass.py
    ├── test_overrides.py
    └── test_idempotent.py
```

The Go API side (merge/split/edit/list endpoints) lives in
`api/internal/handlers/series/`.

## 2. The pass (`pass_.py`, D1/D8)

```python
async def run_series_detect(conn, library_id, *, weights, threshold):
    vids = await repo.load_episodic_videos(conn, library_id)   # parsed + class + speakers + poster hash
    blocks = candidates.block_by_show(vids)                     # D3
    pairs  = candidates.candidate_pairs(blocks, vids)
    uf = UnionFind(v.id for v in vids)
    for a, b in pairs:
        if signals.score(vids[a], vids[b], weights) >= threshold:
            uf.union(a, b)
    components = uf.components()
    async with conn.transaction():                             # D8
        await materialize.apply(conn, library_id, components, vids)
```

`materialize.apply` (D4): for each component, find an existing
user-named series among its members; if present, extend it and keep its
name; else upsert a detected series. Episodes carry `(season, episode)`
from the parser; `is_user_override` rows are never moved.

## 3. Signals (`signals.py`)

```python
def name_sim(a, b) -> float:           # token-set ratio on name_norm(show_name)
def speaker_overlap(a, b) -> float:    # Jaccard of matched speaker_id sets (D6)
def poster_sim(a, b) -> float:         # 1 - hamming(dhash_a, dhash_b)/64 (D5)
def topic_overlap(a, b) -> float:      # cosine of topic_scores vectors (D2)

def score(a, b, w) -> float:
    return (w.name   * name_sim(a, b)
          + w.spk    * speaker_overlap(a, b)
          + w.poster * poster_sim(a, b)
          + w.topic  * topic_overlap(a, b))
```

Disambiguation (story edge: same name, different show): when `name_sim`
is high but `poster_sim` low **and** years differ, the pair is *not*
unioned (a penalty term drops it below threshold), yielding two series
with year-suffixed names.

## 4. Data model — migration slot 0075

```sql
-- Slot 0075 (Epic 26 / Story 26.3)
CREATE TABLE IF NOT EXISTS series (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id    UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    name_norm     TEXT NOT NULL,
    year          INTEGER,
    poster_path   TEXT,
    description   TEXT,                        -- NULL until enriched (26.5)
    season_count  INTEGER NOT NULL DEFAULT 0,
    episode_count INTEGER NOT NULL DEFAULT 0,
    is_user_named BOOLEAN NOT NULL DEFAULT false,   -- D4
    is_pinned     BOOLEAN NOT NULL DEFAULT false,   -- survives GC
    metadata      JSONB NOT NULL DEFAULT '{}'::jsonb,  -- numbering mode, external ids
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (library_id, name_norm, COALESCE(year, 0))
);

CREATE TABLE IF NOT EXISTS series_episodes (
    series_id        UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    video_id         UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    season           INTEGER,
    episode          INTEGER,
    absolute_number  INTEGER,
    is_user_override BOOLEAN NOT NULL DEFAULT false,   -- D4
    added_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (video_id),                            -- a video is in at most one series
    UNIQUE (series_id, season, episode)
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS series_episodes_series_order_idx
    ON series_episodes (series_id, season, episode);
CREATE INDEX CONCURRENTLY IF NOT EXISTS series_library_idx
    ON series (library_id);
```

`video.metadata.poster_dhash` (set by `signals` on first computation) is
stored in the existing `videos.metadata` JSONB — no schema change for the
hash.

## 5. API contract (Go, `api/internal/handlers/series/`)

```
GET   /api/series                  → list (cross-library; 26.10)
GET   /api/series/{id}             → one series + counts
PATCH /api/series/{id}             → rename/edit; sets is_user_named=true
GET   /api/series/{id}/episodes    → ordered grid (26.10)
POST  /api/series/{id}/merge       → {other_series_id}; union + set overrides
POST  /api/series/{id}/split       → {video_ids[]}; new series + overrides
```

Merge: reparent the other series' episodes, set `is_user_override`, mark
the survivor `is_pinned`, delete the empty series. Split: create a new
`is_user_named` series from the given videos, set overrides.

## 6. Files to create / modify

**Create:** `pipeline/.../classify/series/*`, `api/internal/handlers/series/*`,
the two migration files.

**Modify:**
- `api/internal/router` — register the `series` routes.
- `shared/db/migrations/MANIFEST.md` — slot 0075.
- The debounced trigger is wired in
  [Plan 26.7](plan-26-07-background-enrichment-pipeline.md).

## 7. Dependencies

- **26.1** parsed titles, **26.2** classification (topics), **Story
  9.11** diarization speakers, **Epic 08** poster thumbnails.
- Diarization may be **off** for a library → `speaker_overlap` returns 0;
  detection degrades to name+poster+topic (story AC).

## 8. Test strategy

Synthetic fixtures: clean multi-season set; garbled-name set with shared
voiceprints (rescue); remake-vs-original (poster/year split). Idempotence
test runs the pass twice and asserts zero row churn. Override tests
rename/move then re-run and assert preservation.

## 9. Performance

Blocking (D3) keeps candidate pairs ~O(N·k). Poster dHash is computed
once and cached. A 10k episodic library completes a full pass in
seconds; incremental passes touch only changed blocks.
