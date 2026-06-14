# Story 26.2 — Transcript topic & entity extraction

## Description

After STT completes ([Epic 03](../03-transcription/README.md)) and the
transcript is indexed ([Epic 05](../05-search-indexing/README.md)),
analyse the transcript to produce a richer per-video classification than
the rules-based content-type guess from
[Story 9.10](../09-library-management/README.md). This story extends —
not replaces — the existing topic clustering
([Story 9.9](../09-library-management/README.md), `library_topics` /
`video_topics`) with three additions:

1. **Refined content type** — `lecture | sermon | interview |
   documentary | film | music_video | podcast | news | unknown`, scored,
   combining the existing audio-feature classifier signals with
   transcript-derived signals (e.g. dialogue density, monologue ratio,
   question cadence).
2. **Named entities** — people (PER), places (LOC), organizations (ORG)
   mentioned in the transcript, deduped to canonical `media_entities`
   rows and linked to the video with mention counts and offsets.
3. **Primary topic/genre + language** — the dominant `library_topics`
   cluster(s) for this video (reusing the Story 9.9 centroid model) and
   the dominant spoken language(s) (already on `videos.detected_language`
   from STT; this story records the *distribution* for mixed-language
   content).

This runs inside the new `classify` stage
([Story 26.7](story-26-07-background-enrichment-pipeline.md)) and writes
`video_classification` + `media_entities` + `video_entities`.

## Models & reuse

- **Embeddings:** reuse the e5-large embedder and the unit embeddings
  already cached in Chroma
  ([Plan 5.3](../05-search-indexing/plan-05-03-chroma-vector.md)). No
  re-embedding.
- **Topic assignment:** reuse `library_topics` centroids (Story 9.9);
  assign by nearest-centroid over the video's mean unit embedding.
- **Content type:** reuse `content_type.py` audio features (Story 9.10)
  and add transcript features; keep the rules-based fallback when no
  transcript exists (no-audio videos).
- **NER:** a lightweight multilingual model (CPU, ~250 MB) registered in
  the existing model registry (`pipeline/.../models/registry.py`),
  with an Arabic **gazetteer** fallback for robustness on Arabic text.
  No GPU required; runs on the indexed transcript text in batches.

## Acceptance criteria

- `classify` produces one `video_classification` row per video with:
  `content_type`, `content_type_scores` (JSONB map), `primary_topic_id`
  (nullable FK into `library_topics`), `topic_scores` (JSONB),
  `language_dist` (JSONB code→fraction), `model_version`.
- Entities are extracted per transcript, deduped within the video,
  upserted into `media_entities` (canonical by `(kind, name_norm)`,
  reusing the NFC+casefold `name_norm` convention from `tags`), and
  linked via `video_entities` with `mention_count` and a small sample of
  character offsets (capped, JSONB).
- Topic assignment matches Story 9.9 semantics: a video with no
  confident cluster gets `primary_topic_id = NULL` and is eligible for
  the next clustering recompute.
- The content-type result **upgrades** the Story 9.10 guess when the
  transcript gives a higher-confidence answer, but records the prior
  guess in `content_type_scores.prior` for auditability.
- For **no-transcript videos** (no audio / STT skipped), the stage still
  runs: content type falls back to the audio-feature rules, entities are
  empty, topic is assigned from any available signal or left NULL. The
  stage **succeeds** (it must not block the state machine).
- All work reuses cached embeddings; the stage performs **zero**
  embedding-model calls and **zero** network calls.
- Re-running `classify` with a higher `model_version` replaces the
  classification and re-links entities in one transaction.
- The stage is opt-in per library via `settings.classify.entities` and
  `settings.classify.topics` (default on for libraries with transcripts).

## Test cases

- `test_classification_row_written` — fixture transcript → one
  `video_classification` row with all columns populated.
- `test_entities_deduped_and_linked` — transcript mentioning "Cairo"
  3× and "القاهرة" 2× → canonical LOC entity(ies) with correct
  `mention_count`; `name_norm` dedup verified.
- `test_topic_assignment_uses_existing_centroids` — with seeded
  `library_topics`, the video is assigned the nearest centroid; no new
  embedding call (assert embedder mock not invoked).
- `test_content_type_upgrade_records_prior` — a video the rules
  classifier called `unknown` but the transcript scores `lecture` →
  `content_type=lecture`, `content_type_scores.prior="unknown"`.
- `test_no_transcript_falls_back` — no-audio video → stage succeeds,
  entities empty, content type from audio rules.
- `test_mixed_language_distribution` — bilingual transcript →
  `language_dist={"ar":0.6,"en":0.4}` (±0.05).
- `test_arabic_ner_gazetteer` — Arabic transcript with no model hit →
  gazetteer recovers known PER/LOC entities.
- `test_reclassify_replaces_atomically` — bump `model_version`; old
  classification + entity links replaced in one transaction, no orphans.
- `test_classify_no_network` — network access stubbed to raise; stage
  still completes (proves locality).

## Edge cases

- **Empty/near-empty transcript** (music video, silence). Entities
  empty; content type leans on audio features; topic NULL. No error.
- **Entity explosion** (a 4-hour lecture naming hundreds of people).
  `video_entities` is capped at the top-N by mention count (default 200);
  the cap is recorded in `video_classification.metadata`.
- **Centroid model not yet computed** for a young library. Topic
  assignment is skipped (`primary_topic_id=NULL`); the video is picked up
  by the next Story 9.9 recompute without a re-`classify`.
- **Entity that is also a show name** (e.g. a person's name = the film
  title). Entities and parsed-title are independent; series detection
  (26.3) decides precedence, not this story.
- **NER model missing on the box.** Falls back to the gazetteer +
  embedding-only topic; logs `kind=ner_model_absent`; stage still
  succeeds with `entities=[]`.
- **Transcript reprocessed.** When `transcripts.is_active` flips
  (Epic 3 Story 3.5), `classify` re-runs for that video and replaces the
  classification atomically, same as chapters
  ([Story 5.7](../05-search-indexing/story-05-07-chapter-inference.md)).
