# Epic 05 — Search & Indexing: Spec-vs-Implementation Gap Analysis

**Verdict:** Search is a FTS-only facade over `transcript_segments`; the entire unit-chunking, semantic/Chroma, hybrid-RRF, incremental-indexing, and chapter-inference machinery exists as unwired library code with no runtime path. **0 of 7 stories are complete.**

---

## Method

Every AC was traced from the spec to code, then from code back to a real runtime entry point (HTTP handler in `api/internal/handlers/search/search.go`, gRPC server in `pipeline/.../grpc_server.py`, or the stage dispatcher in `pipeline/.../runtime.py`). Existence alone was not accepted; reachability and behavioral satisfaction were verified.

Key structural facts established:

- The pipeline `INDEX` stage handler is `_placeholder_handler("index")` — logs and marks the job `done`, runs no indexing (`pipeline/src/maktaba_pipeline/runtime.py:194`, `:218-235`). `run()` is invoked with **no** `dispatch_overrides` (`pipeline/src/maktaba_pipeline/__main__.py:118`), so the placeholder is the only INDEX behavior in production.
- `serve_grpc(addr=...)` is called with **no** `service`/`embedder` (`pipeline/src/maktaba_pipeline/__main__.py:112`). `PipelineService()` defaults `embedder=None`; `embed()` then raises `RuntimeError("embedder backend not configured")` (`grpc_server.py:79-80`). No embedding model (`sentence-transformers` / e5) exists anywhere in the repo.
- API `realClient.Embed` returns `ErrNotImplemented` (`api/internal/grpcclients/pipeline/realclient.go:53-55`).
- `Deps.SearchSemantic` is never assigned (no `SearchSemantic:` / `SearchSemantic =` site in non-test code); the handler is built `&search.Handler{DB: d.DB, Semantic: d.SearchSemantic}` (`api/internal/router/p6.go:66`), so `Semantic` is always `nil` and the semantic/hybrid arms silently degrade to FTS-only (`search.go:187`).
- All FTS / vector / RRF code (pipeline `search/*.py`, API `search.go`) keys on `transcript_segments`, not `transcript_units`. The `transcript_units` table is created but **never read or written** by any non-test code.

---

## Story 5.1 — Search-unit chunking & schema

| AC | Status | Evidence |
|----|--------|----------|
| Migration `000X_transcript_units.sql` with exact columns | **partial** | `shared/db/migrations/0050_transcript_units.sql:18-31`. Table exists but **schema diverges from spec**: `id BIGSERIAL` (spec ok); columns `video_id`, `segment_id`, `unit_index` instead of spec's `seq`, `segment_ids JSONB` (ordered list), `indexed_at TIMESTAMPTZ`, `metadata JSONB`, `language NOT NULL`. Spec mandates `segment_ids JSONB` (the unit→[segment] mapping); impl has a single nullable `segment_id BIGINT` — the multi-segment mapping required by 5.1 and 5.4 is **impossible to store**. |
| Index `transcript_units_lang ON (language)` (REVIEW §6.3) | **missing** | `0050:35-47` creates `_video_idx`, `_segment_idx`, `_time_idx`. No `language` index → 5.4 filter-pushdown AC cannot use it. |
| Partial index `transcript_units_indexed_at_null` | **missing** | No `indexed_at` column exists, so the partial index that 5.5's incremental claim depends on cannot exist. `0050` has no such index. |
| Indexer produces 1–3-sentence ~200-char units (cap 400) | **missing** | No chunking algorithm anywhere. `search/units.py` is only a `TranscriptUnit` dataclass + `upsert_units` SQL helper (`units.py:27-97`); contains no sentence segmentation, length targeting, or cap logic. |
| Arabic sentence boundaries on `[.!?؟।]`+newline | **missing** | No sentence-splitting code exists in the repo. |
| `unit → ordered list[segment_id]` stored in `segment_ids` JSONB | **missing** | Schema has no `segment_ids` column (see above). `units.py:45` `segment_id: int \| None` is single-valued. |
| `ON DELETE CASCADE` from transcripts | **complete** | `0050:20` `REFERENCES transcripts(id) ON DELETE CASCADE`. |

**Story verdict: missing** (table shell present but schema wrong, no chunker, no required indexes).

---

## Story 5.2 — FTS5 / `tsvector` index (unit-backed)

| AC | Status | Evidence |
|----|--------|----------|
| Postgres `tsv` generated column on `transcript_units` + GIN + `pg_trgm` | **missing** | `0050` creates no `tsv` column on `transcript_units`. The only tsvector is `transcript_segments.search_tsv` (`0016_transcript_segments_fts.sql:40-47`) — wrong table per the story's explicit "Resolves REVIEW §1.1.d" mandate. No `pg_trgm` / `gin_trgm_ops` index anywhere. |
| `language_to_regconfig` (ar→arabic, en→english, und→simple) | **missing** | No such function/mapping. `0016` uses a single static `maktaba_search` config (`COPY = simple` + `unaccent`, `0016:30-33`); language is never consulted. No `shared/db/tsearch/` Arabic dictionary files exist. |
| SQLite contentless FTS5 named `transcripts_fts` over `transcript_units` | **missing** | FTS5 table is `transcript_segments_fts`, `content='transcript_segments'` (`0016_..sqlite.sql:6-9`) — wrong name **and** wrong source table. AC's "same logical name on both engines" is violated. |
| `unicode61 remove_diacritics 2` tokenizer | **partial** | Tokenizer line present in `0016 sqlite` but on the segments table. Not on units. |
| Triggers keep FTS in sync on units INSERT/UPDATE/DELETE | **missing** | Triggers exist for `transcript_segments` (`0016 sqlite:16-38`), none for `transcript_units`. |
| Postgres Arabic text-search config in `shared/db/tsearch/` | **missing** | Directory does not exist; config is generic `simple`+`unaccent`. |
| 15,000 h backfill < 30 min | **missing** | No backfill job exists. |

**Story verdict: missing** (FTS exists but over the wrong table with the wrong name and no language/Arabic config; story's central REVIEW §1.1.d resolution is unimplemented).

---

## Story 5.3 — ChromaDB vector index

| AC | Status | Evidence |
|----|--------|----------|
| One Chroma collection per library `library-<id>`, `hnsw:space=cosine` | **stub** | `embedder.make_collection(name, ...)` calls `get_or_create_collection(name=name, embedding_function=...)` (`embedder.py:140-143`) — **no** `metadata={"hnsw:space":"cosine"}`, and no caller passes `library-<id>` (no caller at all). Chroma defaults to L2, not cosine. |
| `intfloat/multilingual-e5-large` default, configurable, e5-base on CPU, recorded in `metrics.embedding_model_actual` | **missing** | Config key exists (`library_mgmt/config.py:105` `"embedding": {"model": "intfloat/multilingual-e5-large"...}`) but is never read by any embedding loader. No model loading code (`SentenceTransformer` absent repo-wide). No GPU/CPU auto-select. No `metrics.embedding_model_actual` write. |
| Indexer adds Chroma row `id="{transcript_id}:{seq}"`, documents, metadatas | **unwired** | `index_segments` / `embed_id_for` exist (`embedder.py:76-78,166-193`) and produce `{transcript_id}:{seq}`, but **nothing calls them** — the INDEX stage is the placeholder handler. Also keyed on segment `seq`, not unit `seq`. |
| `Embed(text)` gRPC RPC encodes a query | **stub/unwired** | RPC registered (`grpc_server.py:133-134`) but `embedder` is never injected (`__main__.py:112`), so it always raises `RuntimeError`. API side `realClient.Embed` returns `ErrNotImplemented` (`realclient.go:54`). End-to-end dead. |
| ≥200 units/s on Apple Silicon | **missing** | No embedding execution path to benchmark. |
| Library-delete Chroma cleanup hook | **missing** | No cleanup hook; no nightly orphan task. |

**Story verdict: unwired** (algorithmic helpers present, zero runtime path, no embedding model).

---

## Story 5.4 — Hybrid retrieval with RRF

| AC | Status | Evidence |
|----|--------|----------|
| `search(query, mode, filters, limit)` in `pipeline.search.engine` | **missing** | No `pipeline/.../search/engine.py` module exists. The API does its own search in Go (`search.go:117`); the spec'd Pipeline engine entry point is absent. |
| `mode=fts` BM25-only | **partial** | `search.go:196-197` returns FTS hits, but FTS uses `ts_rank`/`ts_rank_cd` (`search.go:248`, `fts.py:126`), **not BM25**, and queries `transcript_segments` not units. |
| `mode=semantic` cosine-only | **unwired** | `search.go:187` gated on `h.Semantic != nil`; `Semantic` is always nil (never assigned). `mode=semantic` returns empty `semHits` → empty result, no error. |
| `mode=hybrid` RRF k=60 | **partial/degraded** | `RRFuse(...,60)` exists and is correct (`search.go:201,470-498`), but with `semHits` always empty hybrid == FTS-only. RRF keys on `segment_id`, not unit. |
| Filters: library_id, video_id, language, speaker, min/max_duration, created_before/after; pushed to both indexes; SQL plan uses `transcript_units_lang` | **partial** | Only `library_id` + `language` pushed (`search.go:223-238`); language filters `v.detected_language` (video), not `transcript_units.language`. `speaker`, duration, date ranges parsed into `Filters` (`search.go:45-63`) but **never applied to SQL**. No `transcript_units_lang` index exists; Chroma `where=` filter only `video_id` (`embedder.py:213`). |
| Result shape with per-hit `matches` array `{segment_id,start_sec,end_sec,text,speaker}`; `segment_id = segment_ids[0]` | **missing** | `Hit` (`search.go:75-82`) has flat `segment_id`/`snippet`, **no `matches` array**, no `speaker`. unit→segment resolution rule unimplemented (no units in play). |
| `<mark>` snippet highlighting; Arabic grapheme-aware | **partial** | `highlightSnippet` wraps `<mark>` (`search.go:503-551`) but byte-slices (`s[start:end]`, `:521-525`) — **not grapheme/rune-safe**; will split UTF-8 Arabic and combining marks. Violates the explicit grapheme-aware AC. |
| Warm p95 ≤500 ms / cold p95 ≤800 ms, tested separately | **missing** | No latency benchmark for the two tiers; no embedding/cold path to measure. |
| Empty query → HTTP 400 | **complete** | `search.go:124-132` returns 400 for empty `q`. |
| Cross-language `metadata.cross_language=true` | **missing** | Not implemented; no per-hit metadata. |
| RRF ties broken by `(start_sec, segment_id)` | **partial** | Tie-break is `segment_id` only (`search.go:492-493`), not `(start_sec ASC, segment_id ASC)`. |
| Orphan-unit drop + `kind=orphan_unit` log | **missing** | No unit resolution, so N/A but unimplemented. |

**Story verdict: partial** (a degraded FTS-only Go path serves results; the spec'd unit-backed, dual-index, segment-coordinate, grapheme-safe behavior is largely missing).

---

## Story 5.5 — Incremental indexing

| AC | Status | Evidence |
|----|--------|----------|
| `index` stage runs when `transcribe` reaches `done`; indexes only `indexed_at IS NULL` units | **missing** | INDEX stage is the placeholder (`runtime.py:194`); it indexes nothing. No `indexed_at` column exists (Story 5.1) so the claim query is impossible. |
| Live indexer subscribes `LISTEN segments.committed` / 5 s poll; re-chunks; writes FTS only | **missing** | No live-indexer task. `segments.committed` notify is referenced in `stt/segment_commit.py` (producer side) but no search-side subscriber re-chunks into units. |
| Pause-aware chunking (stop on paused/cancelled, resume on running) (REVIEW §4.7) | **missing** | No live chunker exists to be pause-aware. |
| Re-process: old transcript units deleted from FTS+Chroma in the same txn that flips `is_active=false` | **missing** | No code deletes units/Chroma rows on transcript deactivation. `0012_transcripts_is_active` flips the flag but no unit/Chroma cleanup is tied to it. |

**Story verdict: missing** (no indexing stage, no live indexer, no pause-awareness, no reindex cleanup).

---

## Story 5.6 — Search query suggestions

| AC | Status | Evidence |
|----|--------|----------|
| `GET /api/search/suggest?q=` returns ≤8 ranked from saved searches + speakers + n-grams | **partial** | Endpoint exists (`search.go:319-345`) but returns **≤10** rows (`LIMIT 10`, `:329`) not 8, and **only from `search_history`** — no speaker names, no offline n-gram source. AC's three sources reduced to one. Pipeline `suggest.py` adds recency weighting (`suggest.py:128-196`) but the **API does not call it** (API runs its own raw `ORDER BY hits DESC`, `search.go:329`) — two divergent implementations. |
| P95 ≤ 50 ms | **missing** | No latency test/guarantee. |
| Arabic prefix via `pg_trgm` GIN / FTS5 `MATCH 'al*'` | **missing** | API uses `query_norm LIKE $1` (`search.go:328`); no `pg_trgm` GIN on `query_norm`, no FTS5 prefix. Falls back on a btree LIKE. |
| Empty corpus → saved searches only, no error | **partial** | Returns `[]` (degrades gracefully, `search.go:332-334`) but never includes saved searches as the AC's empty-corpus fallback specifies. |

**Story verdict: partial** (a minimal history-prefix endpoint exists; speaker/n-gram sources, trigram path, and latency target absent; the API/pipeline implementations diverge).

---

## Story 5.7 — Chapter inference from transcripts

| AC | Status | Evidence |
|----|--------|----------|
| `chapter_infer` is a sub-stage at the tail of `index`, **not** a new top-level `processing_jobs.stage` | **violated** | `0049_chapter_infer_stage.sql:22-26` adds `'chapter_infer'` to the `processing_jobs_stage_check` CHECK constraint as a **top-level stage** — the exact thing Story 5.7 forbids ("does **not** introduce a new top-level stage … keep the canonical seven-stage enum"). |
| Migration `000X_chapters.sql` with spec columns (`transcript_id`, `confidence`, `UNIQUE(transcript_id,seq)`, `chapters_video_start`) | **partial** | `0032_chapters.sql:15-32` table exists but **schema diverges**: no `transcript_id` column, no `confidence` column, `title TEXT NOT NULL` (spec: nullable), `UNIQUE(video_id,seq)` (spec: `(transcript_id,seq)`), index `chapters_video_idx (video_id,seq)` not spec's `chapters_video_start (video_id,start_sec)`. Has an extra `source` enum. |
| Cosine distance between adjacent unit embeddings; boundary when > `topic_shift_threshold` (0.35) | **unwired** | `library_mgmt/chapter.py:81-143` `infer_chapters` implements windowed cosine-drop detection (default `0.35`, `:51`) — but it consumes per-**segment** embeddings (`Segment` dataclass, `:55-67`), not cached Chroma unit embeddings, and **no code calls `infer_chapters`** (only referenced in its own module + tests). Not wired into the `index` tail. |
| Record seq/start/end/confidence | **missing** | `InferredChapter` has no `confidence` field (`chapter.py:70-79`); chapters table has no `confidence` column. No writer inserts inferred chapters into the `chapters` table (no `INSERT INTO chapters ... 'inferred'` in non-test code). |
| `title` NULL in v1, serving falls back to "Chapter N" | **partial** | `title_for_chapter` returns `"Chapter N"` fallback (`chapter.py:163`), but the `chapters.title` column is `NOT NULL` (`0032:21`) — cannot store NULL as the AC specifies. |
| `min_chapter_sec` default 60 suppresses close boundaries | **complete (lib only)** | `chapter.py:51,120-121` enforces `min_chapter_sec` default 60.0. Correct but unreachable (no caller). |
| Opt-in per library via `library.settings.infer_chapters` (default by content_type) | **missing** | `config.py:67,97` has a `chapter_inference` bool default `True` for all, not the content-type-conditional default the AC specifies, and not consulted by any inference invocation (there is none). |
| Inference failure does not fail `index` job | **missing** | No integration between inference and the index job (index job is a placeholder); cannot satisfy. |

**Story verdict: unwired/violated** (algorithm exists as orphan library code, never called, never writes to a schema-mismatched `chapters` table, and the stage-placement AC is actively contradicted by migration 0049).

---

## Top gaps by impact

1. **Semantic / Chroma / hybrid arm is dead end-to-end (Stories 5.3, 5.4).** No embedding model anywhere; `serve_grpc` injects no embedder so `Embed` RPC always raises `RuntimeError`; `realClient.Embed` returns `ErrNotImplemented`; `Deps.SearchSemantic` is never assigned so the API's semantic/hybrid modes silently collapse to FTS-only. The "hybrid (FTS + embedding RRF)" promise of the epic is unfulfilled — `mode=hybrid` and `mode=semantic` return FTS-only or empty results with no error. *(file refs: `pipeline/.../__main__.py:112`, `grpc_server.py:79-80`, `api/.../realclient.go:53-55`, `api/.../router/p6.go:66`, `search.go:187`)*

2. **The INDEX stage is a no-op placeholder (Story 5.5).** `runtime.py:194` maps `Stage.INDEX` to `_placeholder_handler` and `__main__.py:118` registers no override. Nothing ever chunks segments into units, embeds, or writes Chroma. `transcript_units` is created by migration but never written or read by production code. Every downstream story (5.1 chunking, 5.3 vectors, 5.4 hybrid, 5.7 chapters) is starved at the root.

3. **All search is over `transcript_segments`, not `transcript_units` — the epic's central REVIEW §1.1.d resolution is unimplemented (Stories 5.1, 5.2, 5.4).** Pipeline `fts.py:127`, API `search.go:249`, and Chroma `embedder.py` all key on segments. The unit-level scoring + segment-coordinate presentation contract (and the `segment_ids[0]` resolution rule) is structurally impossible because `transcript_units` has a single nullable `segment_id`, not the spec'd ordered `segment_ids JSONB`.

4. **Chapter inference is orphan code with a contradicted stage placement and a mismatched schema (Story 5.7).** `infer_chapters` is never called; migration `0049` adds `chapter_infer` as a forbidden top-level stage; `chapters` table lacks `transcript_id`/`confidence` and has `title NOT NULL` against the spec's nullable-title contract; no code writes inferred chapters.

5. **Arabic correctness gaps (Stories 5.2, 5.4).** No language→regconfig mapping or Arabic tsearch dictionary (single static `simple`+`unaccent` config); SQLite FTS5 table misnamed (`transcript_segments_fts` vs spec `transcripts_fts`); `highlightSnippet` byte-slices instead of rune/grapheme-slicing (`search.go:521-525`), violating the explicit grapheme-aware highlighting AC and corrupting Arabic snippets.

### Single worst gap

The semantic + hybrid retrieval arm is wholly non-functional in any runtime path: no embedding model is loaded anywhere, the pipeline `Embed` gRPC handler is never given an embedder (raises `RuntimeError`), `realClient.Embed` returns `ErrNotImplemented`, and the API's `SearchSemantic` dependency is never assigned — so `mode=hybrid`/`mode=semantic` silently degrade to FTS-only over `transcript_segments`. The epic's core deliverable (FTS + embedding RRF fusion) does not exist end to end.
