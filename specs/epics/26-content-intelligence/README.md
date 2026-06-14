# Epic 26 — Content Intelligence: Auto-Classification, Grouping & Web Enrichment

> **Status:** spec. **Source:** `specs/epics/26-content-intelligence/`.
> **Anchors:** [`architecture.md` §5 (Library Management)](../../architecture.md#5-library-management),
> [§7 (Batch Processing)](../../architecture.md#7-batch-processing),
> [§9 (API Design)](../../architecture.md#9-api-design).

## Goal

Maktaba can already scan, transcribe, index, and search a library
(Epics 01–05). What it **cannot** do is tell the user *what* their
videos are. A folder of `S01E02.720p.x265.mkv` files is, to the current
product, 4 000 opaque rows in `videos`. The user still has to name the
show, group the episodes, find the poster, and write the synopsis by
hand.

Epic 26 closes that gap. It turns raw files into **identified,
grouped, enriched** library entities by combining three signal sources
the server already has or can cheaply acquire:

1. **The filename** — `Show.Name.S01E02.720p.mkv` already encodes show,
   season, episode, resolution, codec, and release group. A parser
   recovers all of it, deterministically, with zero network and zero
   model.
2. **The transcript** — Epic 03 already produces a full transcript and
   (opt-in) speaker diarization. We already cluster topics
   ([Story 9.9](../09-library-management/README.md), `library_topics`)
   and classify content type ([Story 9.10](../09-library-management/README.md),
   `content_type.py`). Epic 26 extends those into **entities** (people,
   places, organizations) and a richer per-video **classification** that
   feeds grouping.
3. **The web** — TMDb, OMDb, YouTube, MusicBrainz, and Wikidata fill
   the gaps the local signals can't: the official title, the synopsis,
   the cast, the poster, the rating. Enrichment results live in their
   own table and **never overwrite a field the user edited**.

These signals feed two grouping outputs — **series** (episodes of the
same show) and **smart collections** (dynamic, query-based groupings by
genre, content type, language, decade, or speaker) — and two
search-surface outputs — **YouTube results inline in search** and
**web context cards** on the video and series pages.

### The non-goals

The cloud relay (Epic 25) is **not** involved. All classification,
grouping, and enrichment runs **on the user's server**; the only
outbound traffic is to the public metadata APIs, and that is opt-in per
library. The cloud never sees the user's library, and Epic 26 adds no
cloud-side state. (Epic 25's README listed "cloud-side metadata mirror"
as a "future Epic 26" idea; we deliberately reject that framing — the
metadata is the *server's*, not the cloud's.)

## How Epic 26 fits the existing pipeline

The canonical seven-stage pipeline
([Epic 01 Story 1.6](../01-scanner/story-01-06-video-state-machine.md))
is `scan → audio → transcribe → subtitle_gen → index → thumbnail`. Epic
26 adds **one new top-level stage, `classify`**, between `index` and
`thumbnail`, plus an **out-of-band `enrich` job** that does not gate the
`videos` state machine (enrichment touches the network and must not
block a video from reaching `READY`).

```
scan → audio → transcribe → subtitle_gen → index → classify → thumbnail
                                                       │
                                                       │ (on classify done, fire-and-forget)
                                                       ▼
                                               enrich  (own queue, rate-limited,
                                                        retried, never blocks READY)
                                                       │
                                                       ▼
                                       series-detect + auto-collection refresh
                                          (debounced library-level passes)
```

`classify` is local-only and deterministic-ish (parser + transcript
signals); it is safe to put on the critical path. `enrich` is network,
non-deterministic, and rate-limited; it is off the path. Series
detection and auto-collection building are **library-level** passes that
run debounced after a batch of videos finish `classify`/`enrich`, not
per-video.

## Stories

### Feature 1 — Content Intelligence (auto-classification & grouping)

| #     | Story                                                       | Service(s) | Summary |
|-------|------------------------------------------------------------|------------|---------|
| 26.1  | [Title parser](story-26-01-title-parser.md)                | pipeline   | Deterministic filename → {show, season, episode, year, resolution, codec, release group}; Latin + Arabic patterns. |
| 26.2  | [Transcript topic & entity extraction](story-26-02-transcript-topic-extraction.md) | pipeline | Extends `video_topics`; adds entities (PER/LOC/ORG), language, refined content type. Reuses the e5 embedder + Chroma. |
| 26.3  | [Series detection](story-26-03-series-detection.md)        | pipeline   | Groups episodes into `series` by parsed-name similarity, speaker overlap, poster + topic similarity. |
| 26.4  | [Auto-collection builder](story-26-04-auto-collection-builder.md) | pipeline + api + web | Dynamic smart collections by topic/type/language/decade/speaker; user accept/dismiss/rename. Extends Epic 7 `collections`. |
| 26.5  | [Web metadata enrichment](story-26-05-web-metadata-enrichment.md) | pipeline + api | TMDb/OMDb/YouTube/MusicBrainz/Wikidata adapters → `media_metadata_enrichment`; never overwrites user edits. |
| 26.6  | [Enrichment review UI](story-26-06-enrichment-ui.md)       | api + web  | "We found this might be X" with confidence; accept/dismiss/manual-search; batch-accept a whole series. |
| 26.7  | [Background enrichment pipeline](story-26-07-background-enrichment-pipeline.md) | pipeline | The `classify` stage + `enrich` job queue: classify → match → enrich → group; rate limits, caching, re-enrich. |

### Feature 2 — Smart Search with Web Context

| #     | Story                                                       | Service(s) | Summary |
|-------|------------------------------------------------------------|------------|---------|
| 26.8  | [YouTube search integration](story-26-08-youtube-search-integration.md) | api + web | Search also queries YouTube; "From YouTube" section; import matched metadata to local videos. |
| 26.9  | [Web context cards](story-26-09-web-context-cards.md)      | api + web  | Enriched videos show rating, cast, synopsis, "More like this" from the library. |
| 26.10 | [Cross-library series view](story-26-10-series-view.md)    | api + web  | Series browser across all libraries: season/episode grids, watch progress, missing-episode detection. |

## Key technical decisions

- **Parser is pure and deterministic, in its own module.** The title
  parser ([26.1](story-26-01-title-parser.md)) is a pure function:
  `parse(filename) -> ParsedTitle`. No DB, no network, no model. This
  makes it trivially testable against a large fixture corpus and lets
  every other story (series detection, enrichment matching) call it as a
  library. **Rationale:** filename parsing is the single highest-signal,
  lowest-cost classifier we have; it must be rock-solid and fast.

- **`classify` is a new top-level stage; `enrich` is not.** Local
  classification is cheap, deterministic-ish, and offline — safe on the
  critical path as a real stage with a `CLASSIFIED` state. Web
  enrichment is slow, networked, and rate-limited — it gets its own
  out-of-band job that **never blocks the video reaching `READY`**.
  **Rationale:** a video with no internet, or a rate-limited TMDb key,
  must still become watchable. (See
  [26.7](story-26-07-background-enrichment-pipeline.md).)

- **Enrichment never overwrites user edits.** Results land in
  `media_metadata_enrichment` (a *staging* table), not directly on
  `videos`. Promotion to `videos.title`/`description`/`poster_path`
  happens only on **explicit user accept** (or auto-accept above a
  high-confidence threshold the user opts into). A `videos.metadata`
  flag records which fields are user-owned and are never touched.
  **Rationale:** the user's correction is ground truth; an API match is
  a suggestion. (Mirrors the
  [`recommendation_dismissals`](../14-discovery/README.md) accept/hide
  pattern from Story 14.7.)

- **Reuse the embedder and Chroma; do not ship a new model.** Topic and
  entity work ([26.2](story-26-02-transcript-topic-extraction.md))
  reuses the e5-large embedder
  ([Plan 5.3](../05-search-indexing/plan-05-03-chroma-vector.md)) and the
  unit embeddings already cached in Chroma. Entity extraction uses a
  lightweight multilingual NER model (gazetteer-backed for Arabic);
  topic assignment reuses the `library_topics` centroid model from Story
  9.9. **Rationale:** zero new GPU footprint for topics; one small CPU
  model for NER.

- **Series and auto-collections are library-level debounced passes.**
  They are not per-video. A `series-detect` and an `auto-collection`
  pass run after a batch settles (debounced ~30 s, or on demand). They
  read the per-video classification + enrichment and (re)write the
  grouping tables transactionally. **Rationale:** grouping is a global
  operation — adding one episode can rename the whole series or move a
  collection's membership; doing it per-video would thrash.

- **Smart collections extend Epic 7, they don't fork it.** Auto-built
  collections are `collections` rows with `is_smart = true` and an
  `origin = 'auto'` discriminator (new column), reusing the existing
  `smart_query` JSONB shape and the existing collection serving path.
  The only new surface is *suggestion* lifecycle (suggested → accepted →
  dismissed). **Rationale:** the web UI, API, and serving logic for
  smart collections already exist; auto-built ones are just rows the
  server proposes.

- **Web metadata adapters share one rate-limited, cached fetch core.**
  TMDb, OMDb, YouTube, MusicBrainz, and Wikidata each get a thin adapter
  over a common `WebClient` that owns: per-provider token-bucket rate
  limiting, on-disk response caching with TTL, retries with backoff, and
  a kill switch. **Rationale:** every provider has the same failure
  modes (429, 5xx, quota exhaustion); solving them once keeps each
  adapter to ~100 lines of mapping logic.

- **API keys are server-side settings, never shipped.** Provider API
  keys (TMDb, OMDb, YouTube Data, etc.) are entered by the operator in
  Settings and stored via the existing secret store
  ([`api/internal/secret`](../../../api/internal/secret)). A provider
  with no key configured is simply skipped. **Rationale:** we cannot
  ship our own keys (quota + ToS); enrichment degrades gracefully to
  whatever the operator has enabled.

- **Identifiers, not just strings.** Enrichment stores the provider's
  stable id (`tmdb:movie:603`, `imdb:tt0133093`, `mbid:...`,
  `wikidata:Q...`, `youtube:videoId`) alongside the mapped fields, so a
  re-enrich is an idempotent refresh by id, not a re-search.
  **Rationale:** searches drift; ids don't. Re-enrich must be stable.

- **All classification/grouping is per-library opt-in.** A library has
  `settings.classify.enabled`, `settings.enrich.enabled`, and
  per-provider toggles. A research/lecture library can run topic+entity
  extraction with TMDb off; a film library runs the opposite. Defaults
  follow the library's declared `content_type`.

## API surface (new, on the local API service)

```
# Parsed titles & classification (read; written by pipeline)
GET    /api/videos/{id}/classification          # parsed title + topics + entities + content type

# Series
GET    /api/series                              # across libraries; ?library_id= to scope
GET    /api/series/{id}
PATCH  /api/series/{id}                         # rename / edit (user override)
GET    /api/series/{id}/episodes                # ordered season/episode grid
POST   /api/series/{id}/merge                   # merge two detected series
POST   /api/series/{id}/split                   # split an over-grouped series
GET    /api/series/{id}/missing                 # missing-episode detection (26.10)

# Auto-collections / suggestions
GET    /api/collections/suggestions             # server-proposed smart collections
POST   /api/collections/suggestions/{id}/accept # promote to a real collection
POST   /api/collections/suggestions/{id}/dismiss
PATCH  /api/collections/suggestions/{id}        # rename before accept

# Enrichment
GET    /api/videos/{id}/enrichment              # candidate matches + confidence
POST   /api/videos/{id}/enrichment/accept       # promote a candidate's fields to the video
POST   /api/videos/{id}/enrichment/dismiss
POST   /api/videos/{id}/enrichment/search       # manual re-search {query, year, provider}
POST   /api/series/{id}/enrichment/accept-all   # batch-accept episode matches
POST   /api/videos/{id}/enrichment/reenrich     # force a fresh fetch by stored id
GET    /api/videos/{id}/context                 # web context card payload (26.9)

# Web-augmented search
GET    /api/search?q=...&include=youtube        # adds a "From YouTube" result block (26.8)
POST   /api/videos/{id}/import-youtube          # copy a YouTube match's metadata locally
```

## DB schema (new tables / alters — local `shared/db/migrations/`)

The local migration sequence is at slot **0072**; Epic 26 claims
**0073–0080**. Every slot ships the dual `*.sql` + `*.sqlite.sql` pair
the runner expects. (See
[`shared/db/migrations/MANIFEST.md`](../../../shared/db/migrations/MANIFEST.md).)

| Slot | Story | File | Tables / changes |
|------|-------|------|------------------|
| 0073 | 26.1 | `0073_media_parsed_titles.sql` | `media_parsed_titles` (1:1 with `videos`: show, season, episode, year, resolution, codec, release_group, edition, confidence, parser_version) |
| 0074 | 26.2 | `0074_media_classification.sql` | `video_classification` (content_type, language, scores, model_version), `media_entities` (canonical), `video_entities` (M:N + offsets) |
| 0075 | 26.3 | `0075_series.sql` | `series`, `series_episodes` (M:N video↔series with season/episode), series override flags |
| 0076 | 26.4 | `0076_auto_collections.sql` | ALTER `collections` ADD `origin`, `auto_rule`, `dismissed_at`; new `collection_suggestions` |
| 0077 | 26.5 | `0077_media_metadata_enrichment.sql` | `media_metadata_enrichment` (per video×provider candidates), `web_metadata_cache` (raw provider responses, TTL) |
| 0078 | 26.6 | `0078_enrichment_decisions.sql` | `enrichment_decisions` (accept/dismiss audit), `media_field_provenance` (which video fields are user-owned) |
| 0079 | 26.7 | `0079_classify_enrich_jobs.sql` | ALTER `videos` state enum (+`classified`); `enrich_jobs` queue table; ALTER `processing_jobs` stage (+`classify`) |
| 0080 | 26.8 | `0080_youtube_imports.sql` | `youtube_search_cache`, `youtube_imports` (audit of imported metadata) |

26.9 and 26.10 add **no migrations** — they are read paths over the
tables above plus existing watch-progress (`play_state`/`watch_history`)
and `series` rows.

## Reused infrastructure (do not rebuild)

| Need | Existing component | Owner |
|------|--------------------|-------|
| Sentence embeddings | e5-large embedder, `pipeline/.../search/embedder.py` | Epic 5 |
| Vector store | ChromaDB, `pipeline/.../search/` (cached unit embeddings) | Epic 5 |
| Hybrid search (FTS + vector + RRF) | `pipeline/.../search/hybrid.py`, `api/.../handlers/search` | Epic 5 |
| Topic clusters | `library_topics` / `video_topics` (slot 0046) | Story 9.9 |
| Content-type classifier | `pipeline/.../library_mgmt/content_type.py` | Story 9.10 |
| Speaker diarization + voiceprints | `pipeline/.../stt/diarization.py`, `speakers` (slots 0035/0048) | Story 9.11 |
| Smart collections | `collections` / `collection_items` (slot 0033), `api/.../handlers/collections` | Story 7.14 |
| Tags taxonomy | `tags` / `video_tags` (slot 0034) | Story 7.14 |
| Accept/dismiss persistence pattern | `recommendation_dismissals` (slot 0067) | Story 14.7 |
| Job queue + state machine | `processing_jobs` (slot 0002), `orchestrator/advance.py`, `pipeline/runner.py` | Epics 1/6 |
| Secret storage (API keys) | `api/internal/secret` | Epic 10 |
| Web design system | `web/design-system/components/` | Epic 17 |

## Threat & abuse model (summary)

| Concern | Mitigation |
|---------|------------|
| SSRF via enrichment URLs | Only hard-coded provider hosts are reachable; no user-supplied URL is fetched. Poster/backdrop downloads go through an allow-listed CDN host check. |
| API key leakage | Keys live in the secret store, are never returned by any API, never logged, never sent to the cloud. |
| Quota exhaustion / cost blowup | Per-provider token-bucket + daily cap + on-disk cache; a tripped breaker pauses that provider, not the pipeline. |
| Poisoned metadata (wrong match auto-applied) | Auto-accept is off by default; below the threshold everything is a *suggestion* requiring user action; provenance table makes every applied field reversible. |
| Image bombs in posters | Size + dimension caps; content-type sniff; re-encode through the existing thumbnail path. |
| PII in entities | Entities are public figures by construction (NER over published content); no cross-video identity linking beyond the user's own library. |

## Out of scope (v1)

- **Face recognition / visual actor identification.** Series detection
  uses *poster* similarity (a cheap perceptual hash) and audio
  speaker overlap, not face recognition in frames.
- **Subtitle-based translation of metadata.** Enrichment fetches in the
  library's locale where the provider supports it; it does not translate
  synopses itself.
- **Music fingerprinting (AcoustID/Chromaprint).** MusicBrainz matching
  in v1 is by parsed artist/title text, not acoustic fingerprint.
- **Writing back to providers** (rating, watchlist sync). Read-only.
- **Cloud-side enrichment or a shared metadata cache across servers.**
  Each server enriches its own library. (Explicitly rejecting the Epic
  25 "cloud metadata mirror" placeholder.)
- **Cross-library de-duplication of identical files** (that is Epic 24).

## See also

- [Epic 03 — Transcription](../03-transcription/README.md) (transcripts + diarization)
- [Epic 05 — Search & Indexing](../05-search-indexing/README.md) (embedder, Chroma, hybrid search)
- [Epic 07 — API Server](../07-api-server/README.md) (collections, tags)
- [Epic 09 — Library Management](../09-library-management/README.md) (topics, content-type, speakers)
- [Epic 14 — Discovery / Recommendations](../14-discovery/README.md) (dismissal pattern)
- [`architecture.md` §5](../../architecture.md#5-library-management), [§7](../../architecture.md#7-batch-processing), [§9](../../architecture.md#9-api-design)
