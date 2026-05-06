# Epic 05 — Search & Indexing

**Phase.** Pipeline (M3 — Outputs).
**Owner.** Pipeline Service writes to both indexes
(`pipeline/src/maktaba_pipeline/search/`); the API Service reads (and
proxies semantic queries to Pipeline via gRPC `Embed`).

> **Goal.** Make every transcribed second searchable in two complementary
> ways — exact-phrase / proximity (Postgres `tsvector` or SQLite FTS5)
> and semantic (ChromaDB) — and fuse them into one ranked result set
> with language filters, deep-linkable timestamps, and snippet
> highlighting that gets Arabic right.

Source: [README](../../../specs/epics/05-search-indexing/README.md) ·
Architecture §3.6 (Indexer), §3.7 (Search Engine).

---

## Stories

| # | Title | Priority | Linear | Story | Plan |
|---|-------|----------|--------|-------|------|
| 5.1 | Search-unit chunking & schema | Gate | [HLB-29](../linear-map.md) | [story-05-01](../../../specs/epics/05-search-indexing/story-05-01-unit-chunking.md) | [plan-05-01](../../../specs/epics/05-search-indexing/plan-05-01-unit-chunking.md) |
| 5.2 | FTS5 / `tsvector` exact-phrase index | Core | [HLB-30](../linear-map.md) | [story-05-02](../../../specs/epics/05-search-indexing/story-05-02-fts-tsvector.md) | [plan-05-02](../../../specs/epics/05-search-indexing/plan-05-02-fts-tsvector.md) |
| 5.3 | ChromaDB vector index | Core | [HLB-31](../linear-map.md) | [story-05-03](../../../specs/epics/05-search-indexing/story-05-03-chroma-vector.md) | [plan-05-03](../../../specs/epics/05-search-indexing/plan-05-03-chroma-vector.md) |
| 5.4 | Hybrid retrieval with Reciprocal Rank Fusion | Core | [HLB-32](../linear-map.md) | [story-05-04](../../../specs/epics/05-search-indexing/story-05-04-hybrid-rrf.md) | [plan-05-04](../../../specs/epics/05-search-indexing/plan-05-04-hybrid-rrf.md) |
| 5.5 | Incremental indexing | Core | [HLB-33](../linear-map.md) | [story-05-05](../../../specs/epics/05-search-indexing/story-05-05-incremental-indexing.md) | [plan-05-05](../../../specs/epics/05-search-indexing/plan-05-05-incremental-indexing.md) |
| 5.6 | Search query suggestions | Polish | [HLB-34](../linear-map.md) | [story-05-06](../../../specs/epics/05-search-indexing/story-05-06-query-suggestions.md) | [plan-05-06](../../../specs/epics/05-search-indexing/plan-05-06-query-suggestions.md) |
| 5.7 | Chapter inference from transcripts | Polish | [HLB-35](../linear-map.md) | [story-05-07](../../../specs/epics/05-search-indexing/story-05-07-chapter-inference.md) | [plan-05-07](../../../specs/epics/05-search-indexing/plan-05-07-chapter-inference.md) |

> Linear IDs from [linear-map.md](../linear-map.md).

### Related mockups & diagrams

| Story | Mockup | Diagram |
|-------|--------|---------|
| 5.1 | — | [search-architecture.drawio](../../../specs/diagrams/search-architecture.drawio) · [entity-relationship.drawio](../../../specs/diagrams/entity-relationship.drawio) |
| 5.2, 5.3, 5.4 | [mockup-11-04-search-interface](../../../web/mockups/mockup-11-04-search-interface.html) · [mobile/search](../../../web/mockups/mobile/search.html) · [tv/search-tv](../../../web/mockups/tv/search-tv.html) | [search-architecture.drawio](../../../specs/diagrams/search-architecture.drawio) |
| 5.5 | [admin/job-pipeline.html](../../../web/mockups/admin/job-pipeline.html) | [search-architecture.drawio](../../../specs/diagrams/search-architecture.drawio) · [data-flow.drawio](../../../specs/diagrams/data-flow.drawio) |
| 5.6 | [mockup-11-04-search-interface](../../../web/mockups/mockup-11-04-search-interface.html) | [search-architecture.drawio](../../../specs/diagrams/search-architecture.drawio) |
| 5.7 | [mockup-11-02-video-detail](../../../web/mockups/mockup-11-02-video-detail.html) · [mockup-11-03-video-player](../../../web/mockups/mockup-11-03-video-player.html) | [search-architecture.drawio](../../../specs/diagrams/search-architecture.drawio) |

---

## DB tables owned

| Table | Purpose |
|-------|---------|
| `transcript_units` | Search units chunked from segments (~200 char target, 400 hard cap). Owned by Story 5.1; resolves REVIEW §1.1.h. The single source of truth for both FTS and vector indexes (resolves REVIEW §1.1.d). |
| `transcripts_fts` | Postgres VIEW over `transcript_units` (or SQLite FTS5 virtual table). Story 5.2. |
| `search_suggestion_terms` | Pre-computed 2-/3-/4-grams per library with frequencies. Story 5.6. |
| `chapters` | Inferred chapter boundaries with start/end seconds and language tag. Story 5.7. |
| `vector_index_dead_letter` | Retry queue for failed Chroma writes during the post-transcribe `index` stage. Story 5.5. |

`transcript_units.tsv` is `GENERATED ALWAYS AS` a `tsvector` via the
custom `arabic` text-search config (`maktaba_normalize()` + diacritic
strip). Story 5.1 also adds the index on `transcript_units(language)`
that resolves REVIEW §6.3.

---

## API endpoints owned

| Method · Path | Purpose | Story |
|---|---|---|
| `GET /v1/search?q&mode=fts\|semantic\|hybrid&library_id&limit` | Hybrid-by-default search; RRF fusion of FTS + vector hits. | 5.4 |
| `GET /api/search/suggest?q=<prefix>&library_id&limit` | Autocomplete from saved searches, speakers, and pre-computed n-grams. | 5.6 |

---

## gRPC services owned

| Service · RPC | Purpose |
|---|---|
| `Embed(text) → EmbedResponse{vector, model, dim}` | Query-time embedding via `intfloat/multilingual-e5-large` (in-process). |
| `Search(query, library_id, mode, limit) → SearchResponse{hits, took_ms, metadata}` | Search orchestration including RRF fusion and unit→segment resolution. |
| `Suggest(library_id, prefix, limit) → Suggestions` | Autocomplete. |

---

## LISTEN/NOTIFY channels

| Direction | Channel | Story |
|-----------|---------|-------|
| Consumer | `segments.committed` (Epic 3 Story 3.6) | 5.5 |
| Consumer | `transcripts.active_changed` | 5.5 (drops the stale collection on transcript swap) |
| Producer | `transcript_units_appended` | 5.3 D5 (signals the Chroma indexer that new units are ready to embed) |

---

## Dependencies

**Depends on.**
- Epic 03 Story 3.5 (active transcript registry) and Story 3.6
  (`segments.committed`).

**Depended on by.**
- Epic 07 (API) — `/v1/search` and suggestions endpoints proxy through.
- Epic 08 (Streaming) — `chapters` resource feeds HLS `EXT-X-DATERANGE`
  markers and the chapter sidebar (Plan 8.12).

---

## Key technical decisions

- **Two engines, one source of truth.** Both FTS and vector index
  `transcript_units`, never `transcript_segments` directly. Story 5.1
  owns the table; Stories 5.2 and 5.3 each maintain their own
  representation but share the same chunking.
- **Unit chunking.** Target ~200 chars / hard cap 400 chars to align with
  the embedding-model context window. Cuts on Unicode word boundaries.
- **FTS configuration.** Postgres uses a custom `arabic` text-search
  config (`maktaba_normalize` + diacritic strip); SQLite uses
  `tokenize='unicode61 remove_diacritics 2'`. Diacritic-insensitive
  matching is non-negotiable for Arabic.
- **ChromaDB persistent client.** Embedded, single-writer, one
  collection per library. Default embedding `intfloat/multilingual-e5-large`,
  with auto-downgrade to `multilingual-e5-base` on CPU-only hosts. HNSW
  parameters: `M=32`, `construction_ef=200`, `search_ef=80`.
- **RRF for fusion.** Reciprocal Rank Fusion with `k=60` (Cormack et al.
  default), tiebreak by `(start_sec, segment_id)`. Per-engine `K=100`
  feeds the fuser; the user gets `limit ≤ 50`.
- **Latency budget.** Warm-cache p95 ≤ 500 ms (Story 5.4 sets the
  canonical budget — resolves REVIEW §1.4.d).
- **Incremental indexing.** Live indexer listens on
  `segments.committed`, debounces 500 ms per `(transcript_id)`, idempotent
  upserts. FTS is updated immediately; Chroma writes are deferred to the
  post-transcribe `index` stage with a dead-letter retry path.
- **Chapter inference.** Cosine-distance dropout between 3-unit-smoothed
  centroids; default threshold 0.35; greedy merge below `min_chapter_sec`
  (default 180 s); language tagged from the first unit. Story 5.7
  resolves REVIEW §2.7.a.

---

## Libraries / dependencies introduced

- `chromadb` — embedded vector DB, persistent client.
- `sentence-transformers` — for `intfloat/multilingual-e5-large` and the
  `-base` fallback.
- `asyncpg` — async LISTEN/NOTIFY in the live indexer.
- `pg_trgm` (Postgres extension) — GIN index for the fuzzy suggestion
  fallback path.

---

## Test coverage summary

- **FTS:** exact-match, diacritic stripping (RTL-safe), proximity,
  language-specific stopwords, prefix queries with `pg_trgm` fallback.
- **Vector:** add/query, idempotent upsert, embedding-dim validation,
  multilingual cross-search, grapheme-aware truncation of long inputs.
- **Hybrid (RRF):** fusion correctness, filter pushdown, Arabic-grapheme
  snippet highlighting, unit→segment resolution, graceful engine
  degradation.
- **Indexer (incremental):** debounce coalescing, pause/cancel
  observation, advisory-lock re-entrancy, catch-up on boot, transcript
  swap cleanup.
- **Suggest:** Arabic / mixed-script prefix matching, saved-search dedup,
  speaker inclusion, p95 ≤ 50 ms, LRU cache.
- **Chapter inference:** boundary detection with smoothing, greedy
  merge, language tagging, isolation from main pipeline failures, no
  re-embedding.
