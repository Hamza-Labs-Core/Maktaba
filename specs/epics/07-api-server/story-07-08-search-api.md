# Story 7.8 — Search API (FTS, semantic, hybrid)

`POST /api/search` from §9.3, plus `GET /api/search/suggest`. Hybrid =
RRF over FTS + Chroma per §3.7.

**AC-1 — Hybrid mode is the default.**
- **Given** a request `POST /api/search` with body `{q: "الحمد لله"}` and no
  `mode`,
- **When** processed,
- **Then** the API runs FTS5 (or `tsvector @@`) and gRPC-calls Pipeline's
  `Embed` for the query, queries Chroma top-K (default K=50), fuses with
  RRF, applies user filters, and returns the response shape from §9.3
  with `took_ms` populated.

**AC-2 — FTS-only and semantic-only modes.**
- **Given** `{q, mode: "fts"}`, **Then** Chroma is not called (no embedding
  cost) and `took_ms` reflects only the FTS path.
- **Given** `{q, mode: "semantic"}`, **Then** FTS is not called.

**AC-3 — Filter shape.**
- **Given** filters `{language: ["ar"], library_id: ["…"], duration_sec:
  {gte: 1800}, speaker: ["sheikh-a"], date: {gte: "2024-01-01"}}`,
- **When** any combination is applied,
- **Then** results respect every filter and the SQL/Chroma queries push
  the filters down (no in-memory filtering except for the final dedup
  across the two sources).

**AC-4 — Suggest is fast.**
- **Given** `GET /api/search/suggest?q=الحم` (Arabic prefix),
- **When** the request is sent,
- **Then** the response is `{suggestions: [...]}` with up to 10 items, p99
  latency under 80 ms on a 100,000-segment fixture, sourced from the FTS
  prefix index only (no Chroma call).

**AC-5 — Highlight markers.**
- **Given** any FTS hit,
- **When** rendered,
- **Then** matched terms in `text` are wrapped `<mark>...</mark>`, the
  surrounding excerpt is at most 240 characters, and right-to-left text is
  bidi-isolated.

**AC-6 — Unit→segment coordinate mapping.**
- **Given** that hits are scored at the `transcript_units` level (Pipeline
  Story 5.1) but the response shape from architecture §9.3 returns
  `matches[].segment_id`,
- **When** a unit spans multiple segments,
- **Then** the reported `segment_id` is `unit.segment_ids[0]` (the first
  segment whose timestamp range overlaps the unit start), and
  `start_sec`/`end_sec` are taken from that segment. This rule is
  documented in the API reference.

**AC-7 — Performance budget (warm cache).**
- **Given** the 100,000-segment performance fixture and a warm
  query-embedding cache,
- **When** a hybrid search is executed,
- **Then** p95 latency ≤ **500 ms**, p99 ≤ 800 ms, p50 ≤ 250 ms (matches
  the NFR Epic 18 budget). Cold-cache p95 may exceed budget by the
  embedding-model load time and is reported separately as
  `cold_search_p95_ms`.

**Test cases:**
- Unit: RRF fusion with k=60 produces deterministic order on a synthetic
  pair of ranked lists.
- Unit: `mode=hybrid` with empty FTS results falls back to pure semantic
  scoring (no `NaN`/division-by-zero).
- Integration: an Arabic query containing diacritics matches segments
  without diacritics (FTS5 `unicode61 remove_diacritics 2` proven by a
  fixture).
- Integration: an English query against an Arabic transcript finds
  matches via Chroma's cross-language embeddings (cross-language fixture
  with `multilingual-e5-large`).
- Integration: search response includes `took_ms` decomposed as
  `took_ms.fts`, `took_ms.semantic`, `took_ms.fusion` for observability.
- Performance: warm-cache p95 under 500 ms, cold-cache p95 measured and
  reported separately on the 100,000-segment fixture.

**Edge cases:**
- `q` is empty or whitespace — return `400 invalid-query`. Test case:
  POST `{q: "   "}` → 400.
- `q` is 50,000 characters — capped at 1024 by the validator. Test case:
  oversize body → 400 with `detail: "q must be ≤1024 chars"`.
- Pipeline gRPC is down — `mode=hybrid` degrades to `mode=fts` and the
  response carries `degraded: true` with reason `embedding-unavailable`.
  Test case: kill Pipeline, run hybrid search → 200 with `degraded:
  true`.
- Chroma returns a segment id that no longer exists (deleted between
  embed and serve) — silently dropped from results. Test case: insert a
  Chroma row, delete the underlying segment, search → no error, no hit.
- A filter on `speaker: ["unknown-3"]` where the speaker was renamed
  between request and response — the filter is by `speaker_id`, not
  name; renames don't break in-flight queries.
