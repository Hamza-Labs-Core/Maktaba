# Plan 26.2 — Transcript topic & entity extraction — implementation

> Implementation plan for [story-26-02-transcript-topic-extraction.md](story-26-02-transcript-topic-extraction.md).
> Self-contained. Cross-links: reuses the e5 embedder + cached Chroma
> vectors ([Plan 5.3](../05-search-indexing/plan-05-03-chroma-vector.md)),
> the `library_topics` centroids
> ([Story 9.9](../09-library-management/README.md), slot 0046), and the
> audio-feature classifier
> ([`content_type.py`](../../../pipeline/src/maktaba_pipeline/library_mgmt/content_type.py),
> Story 9.10). Runs inside the `classify` stage
> ([Plan 26.7](plan-26-07-background-enrichment-pipeline.md)). Writes
> slot 0074.

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **Topic assignment reuses Story 9.9 centroids**, nearest-centroid over the video's mean unit embedding read from Chroma. No new clustering here. | The clustering model already exists and is recomputed by Story 9.9; this story is an *assignment* consumer, not a trainer. Avoids two competing topic models. |
| D2 | **Content type = late fusion of audio rules (9.10) + transcript features.** Keep `content_type.py` as the audio-feature stage; add transcript features (dialogue density, monologue ratio, question cadence, named-entity density) and combine with a small weighted rule that *upgrades* the guess only when more confident. | Reuses a shipped, zero-footprint classifier; transcript signals are additive, not a replacement. Recording the `prior` keeps it auditable (story AC). |
| D3 | **NER = one lightweight multilingual CPU model + an Arabic gazetteer fallback.** Registered in the existing `models/registry.py`; runs on indexed transcript text in batches; gazetteer recovers known entities when the model is absent or low-confidence on Arabic. | Story constraint: "lightweight classifier", no GPU. A gazetteer makes Arabic robust without a heavy AraBERT dependency, and degrades gracefully when no model is installed. |
| D4 | **Entities deduped with the `tags` `name_norm` convention** (NFC + casefold) into canonical `media_entities (kind, name_norm)`; linked via `video_entities` with `mention_count` + capped offset samples. | Consistency with the existing tag dedup; canonical ids let context cards (26.9) cross-link by id, not string (homonym-safe). |
| D5 | **Zero embedding calls, zero network.** Read embeddings via `collection.get(where={"video_id": vid}, include=["embeddings"])`; NER is local. | Story AC: locality is testable (`test_classify_no_network`). Keeps `classify` fast and on the critical path. |
| D6 | **Atomic replace on reclassify**, keyed by `model_version`. | Same pattern as chapters (Plan 5.7); no orphaned entity links. |
| D7 | **Top-N entity cap (default 200) by mention count**, recorded in `metadata`. | Prevents a 4-hour lecture from writing thousands of `video_entities` rows. |

If D1 is rejected (re-cluster per video): we'd duplicate Story 9.9's
model, double the compute, and risk topic-id drift between the two —
rejected.

---

## 1. Package layout

```
pipeline/src/maktaba_pipeline/classify/
├── topics/
│   ├── __init__.py
│   ├── assign.py            # nearest-centroid assignment (D1)
│   └── features.py          # transcript-derived content-type features (D2)
├── entities/
│   ├── __init__.py
│   ├── ner.py               # model wrapper + batching (D3)
│   ├── gazetteer.py         # Arabic/known-entity fallback (D3)
│   ├── canonical.py         # name_norm dedup → media_entities upsert (D4)
│   └── data/ar_gazetteer.txt
├── classifier.py            # orchestrates topics + entities + content-type fusion
├── repo.py                  # (extends 26.1 repo) writes video_classification, entities
└── tests/
    ├── test_assign.py
    ├── test_features.py
    ├── test_ner.py
    ├── test_canonical.py
    ├── test_classifier.py
    └── test_classifier_no_network.py
```

## 2. Topic assignment (`assign.py`, D1)

```python
def assign_topic(video_id, library_id, *, chroma, centroids) -> TopicAssignment:
    vecs = chroma.get(where={"video_id": video_id}, include=["embeddings"])["embeddings"]
    if not vecs:
        return TopicAssignment(primary_topic_id=None, scores={})
    mean = np.mean(np.asarray(vecs, dtype=np.float32), axis=0)
    mean /= (np.linalg.norm(mean) or 1.0)
    # centroids: {topic_id: unit-norm centroid_vec} from library_topics (slot 0046)
    sims = {tid: float(mean @ c) for tid, c in centroids.items()}
    if not sims:
        return TopicAssignment(primary_topic_id=None, scores={})
    primary = max(sims, key=sims.get)
    if sims[primary] < TOPIC_FLOOR:        # mirror Story 9.9 confidence floor
        primary = None
    return TopicAssignment(primary_topic_id=primary, scores=sims)
```

Centroids are loaded from `library_topics.centroid_vec` (packed
float32, as Story 9.9 wrote them) once per library per pass.

## 3. Content-type fusion (`features.py` + `classifier.py`, D2)

Transcript features computed from the active transcript's units:
`dialogue_density` (speaker turns/min from diarization, when present),
`monologue_ratio`, `question_rate` (interrogative cadence),
`entity_density` (entities/min). These are combined with the audio
features `content_type.classify()` already consumes. The fused result
upgrades the audio guess only when its score exceeds it; the prior is
stored:

```python
audio = content_type.classify(audio_features)          # Story 9.10
text  = score_from_transcript(transcript_features)     # new
final, scores = fuse(audio, text)                      # max-confidence wins
scores["prior"] = audio.label
```

No-transcript videos skip `text` and use `audio` directly (AC).

## 4. NER + canonicalisation (D3, D4, D7)

```python
def extract_entities(transcript_text, lang) -> list[RawEntity]:
    spans = []
    if ner.available():
        spans = ner.run(transcript_text)               # batched, CPU
    spans += gazetteer.scan(transcript_text, lang)      # always; cheap
    return dedupe_spans(spans)

def canonicalize(conn, video_id, raw: list[RawEntity], *, cap=200):
    counted = top_n_by_mentions(raw, cap)               # D7
    for ent in counted:
        eid = upsert_media_entity(conn, ent.kind, name_norm(ent.text), ent.text)
        link_video_entity(conn, video_id, eid, ent.mention_count, ent.offset_sample)
```

`name_norm` reuses the exact NFC+casefold helper the `tags` migration
documents (extract it to `shared`/`classify/entities/canonical.py` if not
already shared).

## 5. Data model — migration slot 0074

`shared/db/migrations/0074_media_classification.sql` (+ `.sqlite.sql`):

```sql
-- Slot 0074 (Epic 26 / Story 26.2)
CREATE TABLE IF NOT EXISTS video_classification (
    video_id            UUID PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    content_type        TEXT NOT NULL DEFAULT 'unknown',
    content_type_scores JSONB NOT NULL DEFAULT '{}'::jsonb,    -- includes "prior"
    primary_topic_id    INTEGER,                                -- FK pair below
    library_id          UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    topic_scores        JSONB NOT NULL DEFAULT '{}'::jsonb,
    language_dist       JSONB NOT NULL DEFAULT '{}'::jsonb,
    model_version       INTEGER NOT NULL DEFAULT 1,
    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,
    classified_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (library_id, primary_topic_id)
        REFERENCES library_topics(library_id, topic_id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS media_entities (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind       TEXT NOT NULL CHECK (kind IN ('person','place','org','work','other')),
    name       TEXT NOT NULL,                 -- display
    name_norm  TEXT NOT NULL,                 -- NFC + casefold
    metadata   JSONB NOT NULL DEFAULT '{}'::jsonb,  -- wikidata id once enriched
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kind, name_norm)
);

CREATE TABLE IF NOT EXISTS video_entities (
    video_id      UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    entity_id     UUID NOT NULL REFERENCES media_entities(id) ON DELETE CASCADE,
    mention_count INTEGER NOT NULL DEFAULT 1,
    offsets       JSONB NOT NULL DEFAULT '[]'::jsonb,   -- capped sample
    PRIMARY KEY (video_id, entity_id)
);

CREATE INDEX CONCURRENTLY IF NOT EXISTS video_entities_entity_idx
    ON video_entities (entity_id, video_id);          -- "videos with this person" (26.9)
CREATE INDEX CONCURRENTLY IF NOT EXISTS video_classification_topic_idx
    ON video_classification (library_id, primary_topic_id);
```

(Each `CREATE`/`CREATE INDEX` wrapped in its own goose
`StatementBegin/End`; `CONCURRENTLY` only in the Postgres file.)

## 6. Repo write (atomic replace, D6)

```python
async def write_classification(conn, video_id, result):
    async with conn.transaction():
        await conn.execute("DELETE FROM video_entities WHERE video_id=$1", video_id)
        await conn.execute("""INSERT INTO video_classification (...) VALUES (...)
                              ON CONFLICT (video_id) DO UPDATE SET ...""", ...)
        for ent in result.entities:
            await upsert_and_link(conn, video_id, ent)
```

## 7. Files to create / modify

**Create:** `pipeline/.../classify/topics/`, `.../entities/`,
`classifier.py`, the two migration files, the gazetteer data file.

**Modify:**
- `classify/repo.py` — add classification + entity writers.
- `pipeline/.../models/registry.py` — register the NER model (download
  + storage via the existing `downloader.py`/`storage.py`).
- `shared/db/migrations/MANIFEST.md` — register slot 0074.

## 8. Dependencies

- **26.1** (parser writes first in the same `classify` stage; not a hard
  ordering for *this* code, but co-located).
- **Story 9.9** `library_topics` (slot 0046) — centroids source.
- **Story 9.10** `content_type.py` — audio classifier.
- **Epic 5** Chroma + embedder (read-only).
- Runtime: a small multilingual NER model (CPU); `numpy` (already a dep).

## 9. API contract

Read-only surface owned by 26.9, but the shape produced here:
`GET /api/videos/{id}/classification` →
`{content_type, content_type_scores, primary_topic, topic_scores,
language_dist, entities:[{id,kind,name,mention_count}], model_version}`.

## 10. Test strategy

Centroid-assignment uses seeded `library_topics`; NER uses a stubbed
model + the gazetteer; `test_classifier_no_network` stubs sockets to
raise and asserts success. Atomic-replace test asserts no orphan
`video_entities` after a `model_version` bump.
