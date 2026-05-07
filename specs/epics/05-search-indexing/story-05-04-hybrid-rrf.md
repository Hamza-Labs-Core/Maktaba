# Story 5.4 — Hybrid retrieval with Reciprocal Rank Fusion

## Description

Two indexes need to merge into one ranking. RRF is chosen because it is
score-scale-agnostic and implementation-trivial.

> **Resolves REVIEW §1.4.d.** The latency budget here is **aligned with
> the NFR document** (`epics/04-nonfunctional.md` Stories 18.1 and
> 18.2): warm-cache p95 ≤ 500 ms at 100k segments / 15,000 h library;
> cold-cache p95 ≤ 800 ms (the cost of a model warm-up and cold
> embedding cache). The previous "200 ms" target was unachievable at
> production scale and has been retired.
>
> **Resolves REVIEW §2.5.a and §4.2.** Hits are scored at the
> **unit** level but presented in **segment coordinates**. The single
> reported `segment_id` is `unit.segment_ids[0]` (the first source
> segment for the unit, ordered by `seq`); the `start_sec` and
> `end_sec` are the unit's own bounds, which match
> `segment_ids[0].start_sec` for unit-aligned cases. Documented
> explicitly so consumers know which segment they're navigating to.

## Acceptance criteria

- A `search(query, mode, filters, limit)` API in
  `pipeline.search.engine`:
  - `mode = "fts"` → BM25-only, top-K from the FTS layer.
  - `mode = "semantic"` → cosine-only, top-K from Chroma.
  - `mode = "hybrid"` (default) → both, fused via RRF
    `score(d) = Σ_i 1 / (k + rank_i(d))` with `k = 60` (per Cormack
    et al.).
- Filters supported in v1: `library_id`, `video_id`, `language`,
  `speaker`, `min_duration_sec`, `max_duration_sec`, `created_after`,
  `created_before`. Filters are pushed down to both indexes — Chroma
  metadata filter, FTS via SQL `WHERE` (the `transcript_units(language)`
  index from [Story 5.1](story-05-01-unit-chunking.md) supports the
  language pushdown).
- Result shape matches `architecture §9.3`'s search response with
  per-hit `matches` array of `{segment_id, start_sec, end_sec, text,
  speaker}`. **`segment_id` resolution rule:** the unit's
  `segment_ids[0]` (first source segment by `seq`); `start_sec` and
  `end_sec` are the unit's bounds. Consumers needing the full segment
  fan-out can read `transcript_units.segment_ids` directly via the
  detail endpoint.
- Snippet highlighting wraps the matched span(s) in `<mark>` tags. For
  Arabic, highlighting is grapheme-aware (no splitting combining marks).
- **Latency targets** (resolves REVIEW §1.4.d, §6.5):
  - **Warm cache** (model loaded, recent query embeddings cached):
    p95 ≤ 500 ms for `limit ≤ 50` on a 15,000 h / 100k-segment fixture.
  - **Cold cache** (model just warmed, embedding cache empty):
    p95 ≤ 800 ms.
  - The two tiers are tested separately so regressions in one path
    cannot be hidden by the other.

## Test cases

- `test_rrf_combines_two_lists` — two synthetic ranked lists overlap on
  some docs → RRF score for shared docs > either-list-only docs.
- `test_filters_pushdown` — query with `language = 'ar'` returns only
  hits from `transcript_units` whose `language = 'ar'`; verified by
  inspecting the executed SQL and Chroma `where=`. The SQL plan must
  use `transcript_units_lang`.
- `test_snippet_highlight_arabic_grapheme_safe` — query containing a
  letter with combining marks; snippet does not split the cluster.
- `test_search_latency_warm_cache` — 1,000-query benchmark on the seed
  fixture with embeddings pre-cached; P95 ≤ 500 ms.
- `test_search_latency_cold_cache` — flush model & cache before each
  call (parameterized; smaller N to keep CI bounded); P95 ≤ 800 ms.
- `test_segment_id_resolution_uses_first_source_segment` — a unit
  spanning two segments; the response's `segment_id` equals
  `unit.segment_ids[0]` and `start_sec` matches that segment's
  start.
- `test_deep_link_resolves_to_segment` — a hit's `(video_id,
  start_sec)` pair, when followed via the API, returns a segment
  whose bounds contain that timestamp.

## Edge cases

- **Empty query.** Rejected at the API with HTTP 400; the engine never
  sees an empty string.
- **Query in a language the unit's index config doesn't know.** The FTS
  layer falls back to `simple` config; the semantic layer handles it
  natively. Cross-language hits are valid (English query, Arabic
  result) and tagged with `metadata.cross_language = true` in the
  response.
- **Ties in RRF.** Broken by `(start_sec ASC, segment_id ASC)`, making
  the result deterministic across calls.
- **Filter that excludes all hits from one of the indexes** (e.g.,
  `language = 'fr'` on a library that has only Arabic). The other
  index's results are returned as-is, no error.
- **Unit that maps to zero segments** (impossible by Story 5.1's
  invariant, but defensively handled). The engine drops the hit and
  logs `kind=orphan_unit`; alerting fires after N occurrences.
