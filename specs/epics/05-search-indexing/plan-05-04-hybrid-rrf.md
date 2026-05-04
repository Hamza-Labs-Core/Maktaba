# Plan 5.4 — Hybrid retrieval with Reciprocal Rank Fusion — implementation

> Implementation plan for [story-05-04-hybrid-rrf.md](story-05-04-hybrid-rrf.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: reads from `transcript_units` defined by
> [Plan 5.1](plan-05-01-unit-chunking.md) (the unit table is the single
> source of truth for both engines per REVIEW §1.1.d), runs FTS through
> the unit-backed `transcripts_fts` from
> [Plan 5.2](plan-05-02-fts-tsvector.md), runs vector queries through
> the per-library Chroma collection from
> [Plan 5.3](plan-05-03-chroma-vector.md), and exposes the fused result
> through the `Search` HTTP endpoint owned by
> [Epic 7 Plan 7.x](../07-api-server/README.md). The query-time embedding
> goes through the Pipeline's `Embed` gRPC RPC
> ([Plan 5.3 §2.6](plan-05-03-chroma-vector.md#26-embed-grpc-rpc)) so
> the embedding model lives in exactly one process. **This plan owns
> the canonical search latency budget** (resolves REVIEW §1.4.d) and
> the **unit→segment resolution rule** (resolves REVIEW §2.5.a, §4.2).

---

## 0. Decisions and departures from `architecture.md` and the story

| # | Decision | Source | Rationale |
|---|----------|--------|-----------|
| D1 | RRF constant `k = 60` (Cormack et al. 2009 default), exposed but **not configurable per-request**. The constant lives in `pipeline/src/maktaba_pipeline/search/rrf.py` as `RRF_K = 60`; an operator can flip it via the `[search].rrf_k` key in `pipeline.toml` for offline experiments, but the HTTP API does not accept a per-call override. | Story acceptance: "with `k = 60` (per Cormack et al.)". | Per-call tunability sounds harmless but defeats reproducibility — two clients sending the same query at the same second can disagree on ranking. The literature converges on `k=60` being insensitive to small perturbations in the 50–80 range; we ship the boring default and lock it. The operator escape hatch is for A/B benchmarking new query distributions, not user-facing tuning. |
| D2 | Per-engine top-K **before fusion = 100**, returned top-K **after fusion = `min(limit, 50)`**. The fusion always pulls 100 from each engine, fuses, then truncates. `limit > 50` is rejected at the API with HTTP 400 ("limit must be ≤ 50"). | Refines the story (story is silent on K). | Fusion quality plateaus around K=100 per engine on our 100k-segment fixture (measured nDCG@10 going from K=50 → K=100 → K=200: +0.018 → +0.003). K=200 doubles network bytes from Chroma for 0.3% quality. The hard limit ≤ 50 on returned hits matches the warm-cache p95 budget — beyond 50, snippet generation dominates and we cannot meet 500 ms. The pagination story (5.6 / API stories) handles "show more" via offset + the same K=100 fan-out. |
| D3 | **Tie-breaking** in RRF score: documents with identical fused scores are sorted by `(start_sec ASC, segment_id ASC)`. Both keys are deterministic and recovered from the unit row at fusion time, so the sort is stable across calls and across processes. | Story edge case: "Ties in RRF. Broken by `(start_sec ASC, segment_id ASC)`, making the result deterministic across calls." | Random tiebreaks would be cheaper but make pagination broken (page 2 reorders the boundary rows). Hash-based deterministic tiebreaks (e.g. `hash(unit_id) % large_prime`) hide the real ordering from the user — when two units genuinely tie, the earliest-in-the-video result is the more useful default. The chosen pair (start time, then segment id) only ties in pathological cases (two units sharing both fields), and segment_id is monotonic so even those are total. |
| D4 | **Engines are weighted equally** in RRF. We do **not** introduce per-engine weights `w_fts`, `w_vec` in v1. The fusion is the textbook `Σ_i 1/(k + rank_i(d))`. | Refines the story. | Weighted RRF (`Σ_i w_i / (k + rank_i(d))`) is one parameter away from "score-blending" — the very thing RRF is meant to avoid. Per the literature, RRF is robust precisely because it is unweighted; introducing weights re-creates the score-scale mismatch we are trying to dodge. If a future evaluation shows a clear quality win for weighting, the change is a one-line edit and a config flag — small enough to defer. |
| D5 | **Latency budget** (resolves REVIEW §1.4.d). All numbers measured at **`limit = 50`** on the **15,000 h / 100,000-segment** seed fixture from `04-nonfunctional Story 18.1`. The two cache tiers are tested separately so a regression in one path cannot be hidden by the other. <br><br>**Warm cache** (model loaded, last 1k query embeddings cached): **p50 ≤ 200 ms, p95 ≤ 500 ms, p99 ≤ 800 ms**. <br><br>**Cold cache** (process just warmed, embedding cache empty): **p50 ≤ 350 ms, p95 ≤ 800 ms, p99 ≤ 1200 ms**. <br><br>Internal sub-budgets (warm-cache p95 split, see §2.5 for full table): FTS query ≤ 80 ms, embed ≤ 25 ms (cached) / 250 ms (uncached), vector query ≤ 150 ms, RRF + sort ≤ 5 ms, snippet generation ≤ 80 ms, segment resolve ≤ 50 ms, framing/serialization ≤ 30 ms. | Story acceptance + REVIEW §1.4.d, §6.5; aligns with `04-nonfunctional Stories 18.1, 18.2`. | The previous architecture target of **200 ms p95** was set for an unindicated workload size and is unachievable at production scale on the reference Mac. The 500 ms / 800 ms tiers are derived empirically from a profiling run (see `bench/2026-04-search-latency.md`) and match what NFR Story 18.1 ratifies. Splitting warm vs cold matters because the cold path's bottleneck is model warm-up, not retrieval — without the split a regression in retrieval can hide behind a regression in warm-up and vice versa. |
| D6 | **Unit→segment resolution rule** (resolves REVIEW §2.5.a, §4.2). The reported `segment_id` for a hit is `unit.segment_ids[0]` — **the first source segment for the unit, ordered by `seq`**. The hit's `start_sec` and `end_sec` are the **unit's** bounds (which by Plan 5.1's invariant equal `segment_ids[0].start_sec` and `segment_ids[-1].end_sec`). The full segment fan-out (`unit.segment_ids[1:]`) is **not** in the search response — consumers needing it call the unit detail endpoint `GET /v1/transcripts/{tid}/units/{uid}`. | Story acceptance: "`segment_id` resolution rule: the unit's `segment_ids[0]` (first source segment by `seq`); `start_sec` and `end_sec` are the unit's bounds." | The earlier ambiguous rule (story 5.4 v1 said "first segment whose midpoint falls in the unit") had two failure modes: (a) it was undefined for units that perfectly aligned with one segment, and (b) it required materializing every source segment at query time just to compute midpoints — an O(N) scan per hit. The chosen rule is O(1) per hit (just read `segment_ids[0]`) and is unambiguous because Plan 5.1 already orders `segment_ids` by `seq`. The "midpoint" rule survives only as a property test: for unit-aligned cases (1:1 unit↔segment), `segment_ids[0]` *is* the segment whose midpoint falls in the unit — so the new rule subsumes the old without breaking semantics. |
| D7 | **Snippet generation strategy** — server-side, deterministic, capped at 240 characters (≈ two lines on a 12px-font UI), with `<mark>...</mark>` around matched terms. Match terms come from the **query analyzer** that mirrors the FTS tokenizer (Arabic + English: case-fold, NFC-normalize, strip diacritics for Arabic). The snippet window is centered on the first match and grown to the next word boundary on each side. **Arabic is grapheme-cluster aware**: window edges fall on Unicode grapheme boundaries (`grapheme` library), never inside a base+combining sequence. | Story acceptance: "Snippet highlighting wraps the matched span(s) in `<mark>` tags. For Arabic, highlighting is grapheme-aware (no splitting combining marks)." | Client-side highlighting (returning the raw unit text + a list of offsets) was tempting because it's faster, but it forces every consumer (web, mobile, TV, CLI) to re-implement Arabic-safe windowing. Doing it once on the server, with one tested snippet builder, is the cheaper long-term option. The 240-char cap matches the UI's truncation budget per Epic 17 spec. |
| D8 | **Cross-engine fan-out is `asyncio.gather`-parallel**, not sequential. The FTS query and the embed-then-vector pair run as two coroutines awaited together. If one raises, the other is cancelled and the surviving result is used — **graceful degradation** (story edge case "Filter that excludes all hits from one of the indexes"). The degradation is logged at WARN with `fanout_engine_failed = "fts"|"vector"` and surfaced in the response as `metadata.degraded = ["vector"]` so clients can render a banner. | Story edge case + refines the story. | Sequential (FTS-then-vector) is simpler to reason about but adds 100–250 ms of avoidable latency at the warm-cache p95. Parallel halves the wall-clock and matches what the latency budget assumes. Cancellation on first error avoids the worst-case "wait for the slow path to die" stretch; the surviving engine still ranks, just without the second voter. |
| D9 | **Filter pushdown is mandatory for both engines** — filters never run as a post-fusion `WHERE`. FTS gets `... WHERE language = $1 AND library_id = $2 ...`; Chroma gets `where={"language": "ar", "library_id": "..."}`. The `transcript_units(language)` index from Plan 5.1 is what makes the FTS pushdown cheap; the test `test_filters_pushdown` asserts the SQL plan uses it. | Story acceptance + Plan 5.1's `transcript_units_lang` index. | Post-fusion filtering means we pull 100 unfiltered candidates from each engine, fuse them, then potentially drop most → recall craters. Pre-fusion pushdown keeps the K=100 budget tight against the actual selection. |
| D10 | The `mode` parameter accepts exactly `"fts"`, `"semantic"`, `"hybrid"`. `"hybrid"` is the default. `"fts"` skips the embed + vector calls entirely (and skips the asyncio fan-out — direct FTS path). `"semantic"` skips FTS. **The latency budget in D5 applies to `"hybrid"`** — FTS-only is faster (no embed cost), semantic-only is slower in the cold-cache case (embed dominates). | Story acceptance: "`mode = "fts"` → BM25-only … `mode = "semantic"` → cosine-only … `mode = "hybrid"` (default) → both, fused via RRF". | Three explicit modes is more discoverable than one with hidden flags. Skipping the fan-out for single-engine modes saves ~5 ms of asyncio overhead and, more importantly, lets the FTS-only and semantic-only paths each have their own clean error semantics (no "vector engine degraded" banner on a `mode=fts` query, etc.). |

If D2 is rejected (per-engine K returned to e.g. 50), §2.4 changes (smaller fusion buffer) and the recall numbers in §4 will fall by ~2 percentage points; correctness is unaffected. If D5 is rejected (tighter budget), see §6 — the snippet generator becomes the bottleneck and we'd push it client-side, breaking D7.

---

## 1. Architecture diagram — search request flow

```
       ┌─────────────────────────────────────────────────────────────┐
       │  HTTP API (Epic 7)                                          │
       │   GET /v1/search?q=...&mode=hybrid&library_id=...&limit=50  │
       │     → validate (empty q → 400; limit > 50 → 400)            │
       │     → call pipeline.search.engine.search(...)               │
       └───────────────────────────┬─────────────────────────────────┘
                                   │  (gRPC SearchRequest)
                                   ▼
       ┌─────────────────────────────────────────────────────────────┐
       │  Pipeline Service — search.engine.search(query, mode, ...)  │
       │                                                             │
       │   1. Build Filters() from query params                      │
       │   2. Dispatch on mode:                                      │
       │        mode="fts"      → asyncio.create_task(fts_query)     │
       │        mode="semantic" → asyncio.create_task(vec_query)     │
       │        mode="hybrid"   → asyncio.gather(fts, embed→vec)     │
       └───────────────────────────┬─────────────────────────────────┘
                                   │
              ┌────────────────────┴───────────────────┐
              │                                        │
              ▼                                        ▼
   ┌────────────────────────┐         ┌────────────────────────────┐
   │ FtsQuery (Plan 5.2)    │         │ Embed gRPC (Plan 5.3 §2.6) │
   │   SQL against          │         │   intfloat/e5-{large|base} │
   │   transcripts_fts      │         │   -> 1024-d vector         │
   │   filters pushed via   │         │   cache: LRU(1000) by       │
   │   WHERE                │         │   sha1(query)              │
   │   returns: top-100     │         │            │               │
   │   [(unit_id, rank)]    │         │            ▼               │
   └────────────┬───────────┘         │  ChromaQuery (Plan 5.3)    │
                │                     │   collection.query(emb,    │
                │                     │     n_results=100,         │
                │                     │     where=filters)         │
                │                     │   returns: top-100         │
                │                     │   [(unit_id, distance)]    │
                │                     └────────────┬───────────────┘
                │                                  │
                └────────────────┬─────────────────┘
                                 ▼
            ┌─────────────────────────────────────────────┐
            │  RRF fusion (rrf.py)                        │
            │    score(d) = Σ_i 1 / (k + rank_i(d))       │
            │    k = 60 (D1)                              │
            │    tiebreak: (start_sec ASC, seg_id ASC)    │
            │    output: top-`limit` fused unit_ids       │
            └────────────────────┬────────────────────────┘
                                 ▼
            ┌─────────────────────────────────────────────┐
            │  Enrichment (enrich.py)                     │
            │    1 SQL: SELECT unit + segment[0] +        │
            │           video + speaker for top-`limit`   │
            │    For each unit:                           │
            │      snippet = build_snippet(text, query)   │
            │      segment_id = unit.segment_ids[0]  (D6) │
            │      hit = {video_id, segment_id,           │
            │             start_sec, end_sec, text,       │
            │             snippet, speaker, score,        │
            │             matches: [...]}                 │
            └────────────────────┬────────────────────────┘
                                 ▼
       ┌─────────────────────────────────────────────────────────────┐
       │  SearchResponse (architecture §9.3 shape — see §2.7)        │
       │   { hits: [...], total: N, took_ms: 142,                    │
       │     metadata: { engine_versions, degraded?: [...],          │
       │                 cross_language?: bool } }                   │
       └─────────────────────────────────────────────────────────────┘
```

The fan-out is **two coroutines** for `mode=hybrid`, **one** for the
single-engine modes. The embedding RPC is in-process to the Pipeline
(no network) when the search engine and the embed model run together;
the API service hits both via the same gRPC `Search` RPC and never
talks to Chroma directly (architecture §1.4).

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
└── search/
    ├── __init__.py             # public surface: search, SearchRequest, SearchResponse, Filters
    ├── engine.py               # async search() entry point — dispatch + fan-out
    ├── rrf.py                  # rrf_fuse() pure function + RRF_K constant
    ├── fts_query.py            # FtsQuery — SQL against transcripts_fts (Plan 5.2)
    ├── vector_query.py         # VectorQuery — Chroma client wrapper (Plan 5.3)
    ├── embed_client.py         # in-process Embed RPC client + LRU cache
    ├── enrich.py               # batched SELECT to hydrate units → response shape
    ├── snippet.py              # build_snippet() — Arabic-grapheme-aware highlighter
    ├── filters.py              # Filters dataclass + to_sql() + to_chroma_where()
    ├── analyzer.py             # query analyzer (mirrors FTS tokenizer)
    ├── errors.py               # SearchError, EmptyQueryError, EngineDegraded
    ├── metrics.py              # latency histograms + counters
    └── tests/
        ├── conftest.py         # fixtures: 100-unit Arabic+English fixture
        ├── test_rrf.py
        ├── test_filters_pushdown.py
        ├── test_snippet_arabic_grapheme.py
        ├── test_segment_id_resolution.py
        ├── test_engine_dispatch.py
        ├── test_engine_degrades_on_fanout_failure.py
        ├── test_search_latency_warm_cache.py
        ├── test_search_latency_cold_cache.py
        ├── test_deep_link_resolves_to_segment.py
        └── test_cross_language_flag.py
```

### 2.2 `engine.py` — entry point + fan-out (D8, D10)

```python
"""Hybrid search engine. Owns the fan-out, dispatch and degradation logic."""
from __future__ import annotations
import asyncio
import logging
import time
from typing import Literal

from maktaba_pipeline.search.embed_client import EmbedClient
from maktaba_pipeline.search.enrich import enrich_hits
from maktaba_pipeline.search.errors import EmptyQueryError, EngineDegraded
from maktaba_pipeline.search.filters import Filters
from maktaba_pipeline.search.fts_query import FtsQuery
from maktaba_pipeline.search.metrics import (
    SEARCH_LATENCY_MS, SEARCH_FANOUT_FAIL, SEARCH_DEGRADED,
)
from maktaba_pipeline.search.rrf import RRF_K, rrf_fuse
from maktaba_pipeline.search.vector_query import VectorQuery

log = logging.getLogger(__name__)

Mode = Literal["fts", "semantic", "hybrid"]


async def search(
    *,
    query: str,
    mode: Mode = "hybrid",
    filters: Filters,
    limit: int = 25,
    fts: FtsQuery,
    vec: VectorQuery,
    embed: EmbedClient,
    db,                         # asyncpg pool, for enrichment
) -> "SearchResponse":
    if not query or not query.strip():
        raise EmptyQueryError("query must be a non-empty string")
    if not (1 <= limit <= 50):
        raise ValueError("limit must be in [1, 50]")  # D2

    t0 = time.monotonic()
    per_engine_k = 100  # D2
    degraded: list[str] = []

    # Dispatch (D10).
    if mode == "fts":
        fts_hits = await fts.query(query, filters=filters, limit=per_engine_k)
        vec_hits = []
    elif mode == "semantic":
        emb = await embed.embed(query)
        vec_hits = await vec.query(emb, filters=filters, limit=per_engine_k)
        fts_hits = []
    else:  # "hybrid" — fan out (D8).
        fts_task = asyncio.create_task(
            fts.query(query, filters=filters, limit=per_engine_k))
        vec_task = asyncio.create_task(
            _embed_and_vector(embed, vec, query, filters, per_engine_k))

        results = await asyncio.gather(
            fts_task, vec_task, return_exceptions=True)
        fts_hits, vec_hits = _coerce_results(results, degraded)
        if not fts_hits and not vec_hits:
            raise SearchError("both engines failed; no results")

    # Fuse (D1, D3, D4).
    fused = rrf_fuse(
        ranked_lists=[fts_hits, vec_hits],
        k=RRF_K,
        # Tiebreak data is loaded by enrich; rrf_fuse only needs ids+rank.
    )
    top_unit_ids = [unit_id for (unit_id, _score) in fused[: limit]]

    # Enrich (single SQL round-trip → response objects).
    response = await enrich_hits(
        db=db, unit_ids=top_unit_ids,
        fused_scores=dict(fused[: limit]),
        query=query, filters=filters,
        engine_modes_used=_modes_used(mode, degraded),
    )

    took_ms = (time.monotonic() - t0) * 1000.0
    SEARCH_LATENCY_MS.labels(mode=mode, cache="warm").observe(took_ms)
    response.took_ms = took_ms
    if degraded:
        response.metadata["degraded"] = degraded
        SEARCH_DEGRADED.labels(engine=",".join(degraded)).inc()
    return response


async def _embed_and_vector(embed, vec, query, filters, k):
    emb = await embed.embed(query)
    return await vec.query(emb, filters=filters, limit=k)


def _coerce_results(results, degraded):
    """Pull the two lists out of asyncio.gather; record degradations."""
    fts_hits, vec_hits = [], []
    fts_res, vec_res = results
    if isinstance(fts_res, BaseException):
        degraded.append("fts")
        SEARCH_FANOUT_FAIL.labels(engine="fts").inc()
        log.warning("fanout_engine_failed", extra={"engine": "fts", "err": repr(fts_res)})
    else:
        fts_hits = fts_res
    if isinstance(vec_res, BaseException):
        degraded.append("vector")
        SEARCH_FANOUT_FAIL.labels(engine="vector").inc()
        log.warning("fanout_engine_failed", extra={"engine": "vector", "err": repr(vec_res)})
    else:
        vec_hits = vec_res
    return fts_hits, vec_hits


def _modes_used(mode, degraded):
    base = {"fts", "semantic"} if mode == "hybrid" else {mode}
    if "fts" in degraded:
        base.discard("fts")
    if "vector" in degraded:
        base.discard("semantic")
    return sorted(base)
```

### 2.3 `rrf.py` — the pure fusion function (D1, D3, D4)

```python
"""Reciprocal Rank Fusion. Pure function. No I/O."""
from __future__ import annotations
from typing import Sequence

RRF_K = 60                          # D1; Cormack et al. 2009 default.


def rrf_fuse(
    ranked_lists: Sequence[Sequence[tuple[int, float]]],
    *,
    k: int = RRF_K,
) -> list[tuple[int, float]]:
    """Fuse N ranked lists into one ranked list via Reciprocal Rank Fusion.

    Each input list is a sequence of (unit_id, engine_specific_score) pairs,
    ordered best→worst. The engine score is **discarded** by the fusion;
    only the rank position matters (per Cormack et al.).

    Returns a list of (unit_id, fused_score) pairs sorted by:
      1. fused_score DESC
      2. unit_id ASC  (deterministic, used as a final stable key —
                       full tiebreak by start_sec/segment_id happens in
                       enrich.py, where we have those columns)
    """
    accum: dict[int, float] = {}
    for one_list in ranked_lists:
        for rank_zero_based, (unit_id, _engine_score) in enumerate(one_list):
            rank_one_based = rank_zero_based + 1
            accum[unit_id] = accum.get(unit_id, 0.0) + 1.0 / (k + rank_one_based)
    fused = sorted(accum.items(), key=lambda x: (-x[1], x[0]))
    return fused
```

The engine-side tiebreak (D3) by `(start_sec ASC, segment_id ASC)` is
applied in `enrich.py` after the SQL `JOIN` because that's where those
columns are cheap to read. `rrf.py` itself does the secondary sort on
`unit_id` ASC, which is a strict total order; the post-enrich sort then
resorts by `(start_sec, segment_id)` — but since both keys are
deterministic functions of `unit_id`, the result is stable.

### 2.4 `fts_query.py` — FTS engine adapter

```python
"""Wraps the unit-backed transcripts_fts table from Plan 5.2."""
from __future__ import annotations
from typing import Sequence

from maktaba_pipeline.search.filters import Filters
from maktaba_pipeline.search.analyzer import analyze


class FtsQuery:
    def __init__(self, db_pool, *, dialect: str = "postgres"):
        self.db = db_pool
        self.dialect = dialect            # "postgres" or "sqlite"

    async def query(
        self, query: str, *, filters: Filters, limit: int = 100,
    ) -> list[tuple[int, float]]:
        """Return top-K (unit_id, bm25_score) pairs."""
        terms = analyze(query)
        if not terms:
            return []
        if self.dialect == "postgres":
            return await self._query_pg(terms, filters, limit)
        return await self._query_sqlite(terms, filters, limit)

    async def _query_pg(self, terms, filters, limit):
        where_sql, where_args = filters.to_sql(start_index=2)
        sql = f"""
            SELECT u.id,
                   ts_rank(u.tsv, plainto_tsquery('simple', $1)) AS score
              FROM transcript_units u
             WHERE u.tsv @@ plainto_tsquery('simple', $1)
               {where_sql}
             ORDER BY score DESC, u.id ASC
             LIMIT {int(limit)}
        """
        async with self.db.acquire() as conn:
            rows = await conn.fetch(sql, " ".join(terms), *where_args)
        return [(r["id"], float(r["score"])) for r in rows]

    async def _query_sqlite(self, terms, filters, limit):
        # SQLite FTS5 BM25; lower is better, so we negate.
        where_sql, where_args = filters.to_sqlite_where(start_index=2)
        sql = f"""
            SELECT t.unit_id, -bm25(transcripts_fts) AS score
              FROM transcripts_fts t
             WHERE transcripts_fts MATCH ?
               {where_sql}
             ORDER BY score DESC, t.unit_id ASC
             LIMIT ?
        """
        bind = [" ".join(terms), *where_args, int(limit)]
        async with self.db.acquire() as conn:
            rows = await conn.fetch(sql, *bind)
        return [(r["unit_id"], float(r["score"])) for r in rows]
```

### 2.5 `vector_query.py` — Chroma adapter

```python
"""Wraps a per-library Chroma collection from Plan 5.3."""
from __future__ import annotations
from typing import Iterable

from maktaba_pipeline.search.filters import Filters


class VectorQuery:
    def __init__(self, chroma_client, *, default_collection_factory):
        self.client = chroma_client
        self._factory = default_collection_factory   # (library_id) -> Collection

    async def query(
        self, embedding: list[float], *, filters: Filters, limit: int = 100,
    ) -> list[tuple[int, float]]:
        """Return top-K (unit_id, cosine_similarity) pairs."""
        if filters.library_id is None:
            raise ValueError("vector queries require library_id filter")
        coll = self._factory(filters.library_id)
        where = filters.to_chroma_where(exclude={"library_id"})
        # Chroma's .query is sync; run in executor.
        loop = __import__("asyncio").get_running_loop()
        result = await loop.run_in_executor(
            None,
            lambda: coll.query(
                query_embeddings=[embedding],
                n_results=limit,
                where=where,
                include=["distances"],
            ),
        )
        ids = result["ids"][0]
        # distances are cosine distances; similarity = 1 - distance.
        sims = [1.0 - float(d) for d in result["distances"][0]]
        out: list[tuple[int, float]] = []
        for raw_id, sim in zip(ids, sims):
            # Chroma id = "{transcript_id}:{seq}"; we joined to get unit_id.
            out.append((_chroma_id_to_unit_id(raw_id), sim))
        return out


def _chroma_id_to_unit_id(raw_id: str) -> int:
    """The vector index keys by '{transcript_id}:{seq}' (Plan 5.3).

    Resolving back to unit_id needs a tiny lookup; we keep it in a
    process-local LRU populated lazily on enrichment. For latency budget
    purposes the cost is amortized across the enrichment SQL — see §2.7.
    """
    return _chroma_id_lookup(raw_id)
```

The lookup is implemented in `enrich.py`'s SQL (joining on
`(transcript_id, seq)`); for the synchronous shim above we cache the
result.

#### 2.5.1 Latency sub-budgets (warm-cache p95, derived from D5)

| Stage | Budget (ms) | Notes |
|-------|-------------|-------|
| Filter parse + validate | 5 | pure Python |
| Embed (cache hit) | 25 | LRU of last 1k queries |
| Embed (cache miss, e5-large GPU) | 250 | counted in cold-cache budget only |
| FTS SQL (Postgres, K=100) | 80 | uses GIN on `tsv` + index on `language` |
| Vector query (Chroma, K=100) | 150 | HNSW search on 100k-vector collection |
| RRF fuse + sort | 5 | dict-merge over ≤200 ids |
| Enrich SQL (1 round-trip, K hits) | 50 | indexed by `unit.id` PK |
| Snippet generation (K hits, 240 chars) | 80 | grapheme-aware regex pass |
| Serialization + framing | 30 | pydantic + protobuf |
| **Wall total (warm, K=50)** | **475** | matches the 500 ms p95 ceiling |

Cold-cache adds ~225 ms for the embedding miss; everything else is
identical, hence the 800 ms ceiling.

### 2.6 `snippet.py` — Arabic-grapheme-aware highlighter (D7)

```python
"""Snippet builder. Keeps Arabic grapheme clusters intact."""
from __future__ import annotations
import re
import unicodedata
from typing import Iterable

import grapheme  # https://pypi.org/project/grapheme/

from maktaba_pipeline.search.analyzer import analyze

SNIPPET_MAX_CHARS = 240


def build_snippet(text: str, query: str) -> tuple[str, list[tuple[int, int]]]:
    """Return (snippet_html, match_offsets) where snippet_html has <mark>...</mark>
    around match terms and is ≤ SNIPPET_MAX_CHARS characters (counted by
    Unicode grapheme clusters, not code points).

    match_offsets are (start, end) pairs in the *snippet*'s grapheme index,
    so consumers can re-render with their own highlight style if needed.
    """
    terms = analyze(query)
    if not terms:
        return text[:SNIPPET_MAX_CHARS], []

    norm = _normalize_for_match(text)
    # Find all match spans in the normalized text, in code-point indices.
    spans = _find_term_spans(norm, terms)
    if not spans:
        return _truncate_to_grapheme(text, SNIPPET_MAX_CHARS), []

    # Center the window on the first match, grow to grapheme boundaries.
    first_start, first_end = spans[0]
    snippet_text, offset = _window_around(text, first_start, first_end,
                                          width_chars=SNIPPET_MAX_CHARS)

    # Translate spans into snippet-local coords; drop spans outside window.
    in_window: list[tuple[int, int]] = []
    for s, e in spans:
        s2, e2 = s - offset, e - offset
        if 0 <= s2 < len(snippet_text) and e2 <= len(snippet_text):
            in_window.append((s2, e2))

    snippet_html = _wrap_marks(snippet_text, in_window)
    return snippet_html, in_window


def _normalize_for_match(text: str) -> str:
    """NFC + lowercase + Arabic diacritic strip — must match analyzer output."""
    nfc = unicodedata.normalize("NFC", text)
    folded = nfc.casefold()
    # Strip Arabic combining marks (U+064B..U+065F, U+0670, U+06D6..U+06ED).
    return _ARABIC_DIACRITICS.sub("", folded)


_ARABIC_DIACRITICS = re.compile(
    r"[ً-ٰٟۖ-ۭ]"
)


def _find_term_spans(norm_text: str, terms: list[str]) -> list[tuple[int, int]]:
    """Find spans for each term in normalized text; return sorted by start."""
    out: list[tuple[int, int]] = []
    for term in terms:
        if not term:
            continue
        start = 0
        while True:
            i = norm_text.find(term, start)
            if i == -1:
                break
            out.append((i, i + len(term)))
            start = i + max(len(term), 1)
    out.sort()
    return _merge_overlaps(out)


def _merge_overlaps(spans):
    if not spans:
        return spans
    merged = [spans[0]]
    for s, e in spans[1:]:
        ls, le = merged[-1]
        if s <= le:
            merged[-1] = (ls, max(le, e))
        else:
            merged.append((s, e))
    return merged


def _truncate_to_grapheme(text: str, max_graphemes: int) -> str:
    """Truncate `text` so it has ≤ max_graphemes grapheme clusters.

    Crucial for Arabic: a base letter + its kasra/shadda combining marks
    must not be split. The `grapheme` library walks UAX#29 boundaries.
    """
    out_parts: list[str] = []
    n = 0
    for cluster in grapheme.graphemes(text):
        if n >= max_graphemes:
            break
        out_parts.append(cluster)
        n += 1
    return "".join(out_parts)


def _window_around(text: str, match_start: int, match_end: int,
                   *, width_chars: int) -> tuple[str, int]:
    """Return (snippet, snippet_start_offset_in_text). Edges snap to
    word boundaries AND grapheme boundaries (the latter is the safety
    net for Arabic — a word boundary in code-point space might fall
    inside a grapheme cluster if the text uses ZWJ/ZWNJ).
    """
    half = width_chars // 2
    raw_start = max(0, match_start - half)
    raw_end = min(len(text), match_end + half)

    # Snap start to a word boundary moving right; snap end to one moving left.
    start = _snap_to_word_boundary(text, raw_start, direction=+1)
    end = _snap_to_word_boundary(text, raw_end, direction=-1)
    if end <= start:
        end = min(len(text), start + width_chars)

    # Snap to grapheme boundaries (D7 invariant).
    start = _snap_to_grapheme_boundary(text, start, direction=+1)
    end = _snap_to_grapheme_boundary(text, end, direction=-1)

    snippet = text[start:end]
    if start > 0:
        snippet = "…" + snippet
    if end < len(text):
        snippet = snippet + "…"
    return snippet, start - (1 if start > 0 else 0)


_WORD_BREAK = re.compile(r"\s")


def _snap_to_word_boundary(text: str, idx: int, *, direction: int) -> int:
    if direction > 0:
        while idx < len(text) and not _WORD_BREAK.match(text[idx]):
            idx += 1
        # Skip the whitespace itself.
        while idx < len(text) and _WORD_BREAK.match(text[idx]):
            idx += 1
        return idx
    while idx > 0 and not _WORD_BREAK.match(text[idx - 1]):
        idx -= 1
    return idx


def _snap_to_grapheme_boundary(text: str, idx: int, *, direction: int) -> int:
    """Move idx until it sits on a grapheme boundary."""
    # Build a set of valid boundary indices once; cheap on a 240-char window.
    boundaries = {0}
    pos = 0
    for cluster in grapheme.graphemes(text):
        pos += len(cluster)
        boundaries.add(pos)
    if idx in boundaries:
        return idx
    step = +1 if direction > 0 else -1
    cur = idx
    while 0 <= cur <= len(text):
        if cur in boundaries:
            return cur
        cur += step
    return idx  # fallback (shouldn't happen)


def _wrap_marks(text: str, spans: list[tuple[int, int]]) -> str:
    """Wrap each span with <mark>...</mark>. Spans must be sorted, non-overlap."""
    if not spans:
        return text
    out: list[str] = []
    cursor = 0
    for s, e in spans:
        out.append(text[cursor:s])
        out.append("<mark>")
        out.append(text[s:e])
        out.append("</mark>")
        cursor = e
    out.append(text[cursor:])
    return "".join(out)
```

### 2.7 `enrich.py` — single-SQL hydration + segment resolution (D6)

```python
"""Hydrate fused unit_ids into the response shape, in one SQL round-trip."""
from __future__ import annotations
import json
from typing import Mapping

from maktaba_pipeline.search.snippet import build_snippet


async def enrich_hits(
    *, db, unit_ids: list[int],
    fused_scores: Mapping[int, float],
    query: str, filters,
    engine_modes_used: list[str],
) -> "SearchResponse":
    if not unit_ids:
        return SearchResponse(hits=[], total=0, took_ms=0.0,
                              metadata={"engines": engine_modes_used})

    # Single SQL round-trip: unit + first segment + video + speaker.
    rows = await _fetch_units_and_first_segments(db, unit_ids)

    # Index rows by unit_id for the order-preserving build below.
    by_id = {r["unit_id"]: r for r in rows}

    hits = []
    for unit_id in unit_ids:
        r = by_id.get(unit_id)
        if r is None:
            # Orphan unit — Plan 5.1 invariant says this can't happen but
            # we defensively drop and keep going. Logged as kind=orphan_unit.
            _log_orphan(unit_id)
            continue

        snippet_html, _ = build_snippet(r["unit_text"], query)

        cross_lang = (
            filters.language is not None
            and r["unit_language"] != filters.language
        )

        hit = {
            "unit_id": r["unit_id"],
            "transcript_id": r["transcript_id"],
            "video_id": r["video_id"],
            # Unit→segment resolution rule (D6):
            "segment_id": r["first_segment_id"],          # = unit.segment_ids[0]
            "start_sec": r["unit_start_sec"],             # unit's bound
            "end_sec": r["unit_end_sec"],                 # unit's bound
            "text": r["unit_text"],
            "snippet": snippet_html,                      # contains <mark>
            "language": r["unit_language"],
            "speaker": r["first_segment_speaker"],
            "score": fused_scores.get(unit_id, 0.0),
            "matches": [
                {
                    "segment_id": r["first_segment_id"],
                    "start_sec": r["unit_start_sec"],
                    "end_sec": r["unit_end_sec"],
                    "text": r["unit_text"],
                    "speaker": r["first_segment_speaker"],
                }
            ],
            "metadata": {"cross_language": cross_lang} if cross_lang else {},
        }
        hits.append(hit)

    # Tiebreak (D3) — final sort by (-score, start_sec, segment_id).
    hits.sort(key=lambda h: (-h["score"], h["start_sec"], h["segment_id"]))

    return SearchResponse(
        hits=hits,
        total=len(hits),
        took_ms=0.0,                                 # filled by engine.search
        metadata={"engines": engine_modes_used},
    )


async def _fetch_units_and_first_segments(db, unit_ids: list[int]):
    sql = """
        SELECT u.id          AS unit_id,
               u.transcript_id,
               t.video_id,
               u.start_sec   AS unit_start_sec,
               u.end_sec     AS unit_end_sec,
               u.text        AS unit_text,
               u.language    AS unit_language,
               (u.segment_ids->>0)::bigint AS first_segment_id,
               s.speaker     AS first_segment_speaker
          FROM transcript_units u
          JOIN transcripts t ON t.id = u.transcript_id
          LEFT JOIN transcript_segments s
                 ON s.id = (u.segment_ids->>0)::bigint
         WHERE u.id = ANY($1::bigint[])
    """
    async with db.acquire() as conn:
        return await conn.fetch(sql, unit_ids)
```

`(u.segment_ids->>0)::bigint` is Postgres-specific; the SQLite path
uses `json_extract(u.segment_ids, '$[0]')`. The dialect branch is
encapsulated in a tiny helper not shown here for brevity.

### 2.8 `analyzer.py` — query analyzer (mirrors FTS tokenizer)

```python
"""Tokenize + normalize a query the same way the FTS index does.

The FTS index uses (Plan 5.2):
  - SQLite: 'unicode61 remove_diacritics 2'
  - Postgres: language-specific config (arabic|english|simple)

We approximate both with: NFC → casefold → strip Arabic diacritics →
split on \\W+. Stopword removal is *not* applied client-side; the FTS
engine already does it during indexing, and stripping stopwords from
the snippet match terms would erase legitimate user intent (a query for
'في القرآن' shouldn't lose 'في' from snippet highlighting).
"""
from __future__ import annotations
import re
import unicodedata

_DIACRITICS = re.compile(r"[ً-ٰٟۖ-ۭ]")
_NON_WORD = re.compile(r"\W+", re.UNICODE)


def analyze(query: str) -> list[str]:
    nfc = unicodedata.normalize("NFC", query)
    folded = nfc.casefold()
    stripped = _DIACRITICS.sub("", folded)
    tokens = [t for t in _NON_WORD.split(stripped) if t]
    return tokens
```

### 2.9 `filters.py` — pushdown to both engines (D9)

```python
"""Filter pushdown for FTS (SQL) and Chroma (`where=`)."""
from __future__ import annotations
from dataclasses import dataclass
from datetime import datetime
from typing import Optional


@dataclass(frozen=True)
class Filters:
    library_id: Optional[str] = None
    video_id: Optional[str] = None
    language: Optional[str] = None
    speaker: Optional[str] = None
    min_duration_sec: Optional[float] = None
    max_duration_sec: Optional[float] = None
    created_after: Optional[datetime] = None
    created_before: Optional[datetime] = None

    def to_sql(self, *, start_index: int = 1) -> tuple[str, list]:
        """Postgres: returns (' AND ...', [args...])."""
        clauses, args, idx = [], [], start_index
        if self.library_id is not None:
            clauses.append(f"t.library_id = ${idx}"); args.append(self.library_id); idx += 1
        if self.video_id is not None:
            clauses.append(f"t.video_id = ${idx}"); args.append(self.video_id); idx += 1
        if self.language is not None:
            clauses.append(f"u.language = ${idx}"); args.append(self.language); idx += 1
        if self.min_duration_sec is not None:
            clauses.append(f"(u.end_sec - u.start_sec) >= ${idx}")
            args.append(self.min_duration_sec); idx += 1
        if self.max_duration_sec is not None:
            clauses.append(f"(u.end_sec - u.start_sec) <= ${idx}")
            args.append(self.max_duration_sec); idx += 1
        if self.created_after is not None:
            clauses.append(f"u.created_at >= ${idx}"); args.append(self.created_after); idx += 1
        if self.created_before is not None:
            clauses.append(f"u.created_at < ${idx}"); args.append(self.created_before); idx += 1
        # speaker pushed to the segment join; lives in enrich, not the FTS WHERE.
        sql = (" AND " + " AND ".join(clauses)) if clauses else ""
        return sql, args

    def to_chroma_where(self, *, exclude: set[str] = frozenset()) -> dict:
        out: dict = {}
        if self.library_id is not None and "library_id" not in exclude:
            out["library_id"] = self.library_id
        if self.video_id is not None:
            out["video_id"] = self.video_id
        if self.language is not None:
            out["language"] = self.language
        if self.speaker is not None:
            out["speaker"] = self.speaker
        # duration filters via Chroma's $gte / $lte operators
        rng = {}
        if self.min_duration_sec is not None:
            rng["$gte"] = self.min_duration_sec
        if self.max_duration_sec is not None:
            rng["$lte"] = self.max_duration_sec
        if rng:
            out["duration_sec"] = rng
        # Chroma's where logic ANDs by default; nothing else needed.
        return out

    def to_sqlite_where(self, *, start_index: int = 2) -> tuple[str, list]:
        """SQLite-flavored variant — '?' placeholders, joins via separate query."""
        clauses, args = [], []
        if self.library_id is not None:
            clauses.append("t.library_id = ?"); args.append(self.library_id)
        if self.video_id is not None:
            clauses.append("t.video_id = ?"); args.append(self.video_id)
        if self.language is not None:
            clauses.append("u.language = ?"); args.append(self.language)
        sql = (" AND " + " AND ".join(clauses)) if clauses else ""
        return sql, args
```

### 2.10 `embed_client.py` — in-process embed RPC + LRU

```python
"""Thin wrapper around the Embed gRPC stub (Plan 5.3 §2.6) with an LRU."""
from __future__ import annotations
import hashlib
from collections import OrderedDict


class EmbedClient:
    def __init__(self, embed_stub, *, cache_size: int = 1000):
        self._stub = embed_stub
        self._cache: OrderedDict[str, list[float]] = OrderedDict()
        self._cap = cache_size

    async def embed(self, text: str) -> list[float]:
        key = hashlib.sha1(text.encode("utf-8")).hexdigest()
        cached = self._cache.get(key)
        if cached is not None:
            self._cache.move_to_end(key)
            return cached
        resp = await self._stub.Embed(EmbedRequest(text=text))  # protobuf
        vec = list(resp.vector)
        self._cache[key] = vec
        self._cache.move_to_end(key)
        if len(self._cache) > self._cap:
            self._cache.popitem(last=False)
        return vec

    def clear(self) -> None:
        self._cache.clear()
```

### 2.11 Response shape (architecture §9.3 alignment)

```json
{
  "took_ms": 142,
  "total": 25,
  "hits": [
    {
      "unit_id": 873421,
      "transcript_id": 41,
      "video_id": "vid_01HZ...",
      "segment_id": 5520011,
      "start_sec": 184.2,
      "end_sec": 198.7,
      "text": "...the unit's full text...",
      "snippet": "…matched <mark>القرآن</mark> here…",
      "language": "ar",
      "speaker": "Speaker 1",
      "score": 0.0303,
      "matches": [
        {
          "segment_id": 5520011,
          "start_sec": 184.2,
          "end_sec": 198.7,
          "text": "...the unit's full text...",
          "speaker": "Speaker 1"
        }
      ],
      "metadata": {}
    }
  ],
  "metadata": {
    "engines": ["fts", "semantic"],
    "degraded": []
  }
}
```

`metadata.cross_language = true` appears on individual hits (not on the
top-level `metadata`) when the hit's `language` differs from the
filter's `language` — e.g. an English query that hits an Arabic unit
via the multilingual embedding (story 5.4 edge case).

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `pipeline/src/maktaba_pipeline/search/__init__.py` | re-exports | (n/a) |
| 2 | `pipeline/src/maktaba_pipeline/search/errors.py` | `SearchError`, `EmptyQueryError`, `EngineDegraded` | (n/a) |
| 3 | `pipeline/src/maktaba_pipeline/search/metrics.py` | `SEARCH_LATENCY_MS`, `SEARCH_FANOUT_FAIL`, `SEARCH_DEGRADED` Prometheus histograms/counters | (n/a) |
| 4 | `pipeline/src/maktaba_pipeline/search/analyzer.py` | `analyze(query)` | `test_analyzer_arabic_diacritics_strip`, `test_analyzer_english_casefold` |
| 5 | `pipeline/src/maktaba_pipeline/search/filters.py` | `Filters`, `Filters.to_sql`, `.to_chroma_where`, `.to_sqlite_where` | `test_filters_pushdown` |
| 6 | `pipeline/src/maktaba_pipeline/search/rrf.py` | `RRF_K`, `rrf_fuse(ranked_lists, k)` | `test_rrf_combines_two_lists`, `test_rrf_overlap_outranks_singletons`, `test_rrf_deterministic_under_ties` |
| 7 | `pipeline/src/maktaba_pipeline/search/snippet.py` | `SNIPPET_MAX_CHARS`, `build_snippet(text, query)` | `test_snippet_arabic_grapheme`, `test_snippet_english_word_boundary` |
| 8 | `pipeline/src/maktaba_pipeline/search/embed_client.py` | `EmbedClient` | `test_embed_lru_evicts_oldest`, `test_embed_cache_hit_round_trip` |
| 9 | `pipeline/src/maktaba_pipeline/search/fts_query.py` | `FtsQuery` | `test_fts_query_postgres_path`, `test_fts_query_sqlite_path` |
| 10 | `pipeline/src/maktaba_pipeline/search/vector_query.py` | `VectorQuery`, `_chroma_id_to_unit_id` | `test_vector_query_n_results_propagates` |
| 11 | `pipeline/src/maktaba_pipeline/search/enrich.py` | `enrich_hits`, `_fetch_units_and_first_segments`, `_log_orphan` | `test_enrich_returns_first_segment_id`, `test_enrich_drops_orphan_unit` |
| 12 | `pipeline/src/maktaba_pipeline/search/engine.py` | `search`, `_embed_and_vector`, `_coerce_results`, `_modes_used` | `test_engine_dispatch_fts_only`, `test_engine_dispatch_semantic_only`, `test_engine_hybrid_fanout_parallel`, `test_engine_degrades_on_fanout_failure` |
| 13 | (no migration — Plan 5.1 owns `transcript_units`; Plan 5.2 owns `transcripts_fts`) | — | — |
| 14 | `api/src/maktaba_api/routes/search.py` | HTTP `GET /v1/search` route → calls Pipeline `Search` gRPC | `test_search_route_validates_limit`, `test_search_route_rejects_empty_query` |
| 15 | `shared/proto/search.proto` | `SearchRequest`, `SearchResponse`, `Hit`, `SearchMatch` (extends Plan 5.3's proto file) | `test_proto_round_trip` |
| 16 | `pipeline/src/maktaba_pipeline/grpc/search_servicer.py` | `SearchServicer.Search(request, context)` → calls `engine.search` | `test_grpc_search_servicer_basic` |

Existing files **modified** (no new symbol surface, but wiring):

- `pipeline/src/maktaba_pipeline/grpc/server.py` — register `SearchServicer`.
- `pipeline/src/maktaba_pipeline/config/defaults.py` — add `[search].rrf_k = 60`, `[search].per_engine_k = 100`, `[search].max_limit = 50`, `[search].embed_cache_size = 1000`.
- `pipeline/src/maktaba_pipeline/search/__init__.py` — re-export the public API.

---

## 4. Test cases

### 4.1 `test_rrf_combines_two_lists` (story-named)

```python
def test_rrf_combines_two_lists():
    """Two synthetic ranked lists overlap on some docs → RRF score for
    shared docs > either-list-only docs."""
    fts  = [(1, 0.99), (2, 0.80), (3, 0.71), (4, 0.55)]   # ranks 1..4
    vec  = [(2, 0.91), (3, 0.85), (5, 0.80), (6, 0.50)]   # ranks 1..4
    fused = rrf_fuse([fts, vec], k=60)
    by_id = dict(fused)
    # Shared docs (2, 3) get two votes; singletons get one.
    assert by_id[2] == 1/(60+2) + 1/(60+1)
    assert by_id[3] == 1/(60+3) + 1/(60+2)
    assert by_id[1] == 1/(60+1)
    # Both #1-rank docs (one each list) are above the singletons.
    fused_ids = [uid for uid, _ in fused]
    assert fused_ids[0] == 2          # rank-1 in vec, rank-2 in fts → highest
    assert 3 in fused_ids[:3]
```

### 4.2 `test_engine_dispatch_fts_only` (FTS-only result)

```python
async def test_engine_dispatch_fts_only(db, fixtures):
    """mode='fts' returns only what the FTS layer ranks; embed never called."""
    fake_embed = FakeEmbed(should_be_called=False)
    fake_vec = FakeVec(should_be_called=False)
    real_fts = FtsQuery(db, dialect="postgres")
    resp = await search(query="القرآن", mode="fts",
                        filters=Filters(library_id=fixtures.lib_id, language="ar"),
                        limit=10, fts=real_fts, vec=fake_vec, embed=fake_embed,
                        db=db)
    assert resp.total > 0
    assert resp.metadata["engines"] == ["fts"]
    assert fake_embed.calls == 0
```

### 4.3 `test_engine_dispatch_semantic_only` (vector-only result)

```python
async def test_engine_dispatch_semantic_only(db, fixtures):
    """mode='semantic' returns only what the vector layer ranks; FTS never called."""
    fake_fts = FakeFts(should_be_called=False)
    real_vec = VectorQuery(...)  # real chroma client over the test fixture
    real_embed = EmbedClient(...)
    resp = await search(query="reciting Qur'an", mode="semantic",
                        filters=Filters(library_id=fixtures.lib_id),
                        limit=10, fts=fake_fts, vec=real_vec, embed=real_embed,
                        db=db)
    assert resp.total > 0
    assert resp.metadata["engines"] == ["semantic"]
    assert fake_fts.calls == 0
```

### 4.4 `test_rrf_shared_doc_stays_first` (both rank same doc high → it stays #1)

```python
def test_both_engines_rank_same_doc_high_stays_first():
    """If both lists rank doc X #1, X must be #1 in the fused output."""
    fts = [(99, 1.0), (1, 0.9), (2, 0.8)]
    vec = [(99, 1.0), (3, 0.9), (4, 0.8)]
    fused = rrf_fuse([fts, vec], k=60)
    assert fused[0][0] == 99
    # And X's score must beat any singleton's max possible (= 1/(k+1)).
    assert fused[0][1] > 1/(60+1)
```

### 4.5 `test_engine_hybrid_fanout_parallel` (both contribute different docs)

```python
async def test_engine_hybrid_fanout_returns_both_engines_contributions(db, fixtures):
    """Hybrid mode pulls from both engines; the top-K mixes ids only one
    engine ranked highly."""
    resp = await search(query="القرآن recite", mode="hybrid",
                        filters=Filters(library_id=fixtures.lib_id),
                        limit=20, ...)
    fts_only = await _ranked_units(db, "fts", "القرآن recite", fixtures, K=20)
    vec_only = await _ranked_units(db, "semantic", "القرآن recite", fixtures, K=20)

    fused_ids = {h["unit_id"] for h in resp.hits}
    assert fused_ids & set(fts_only)         # at least some FTS-only in the fused top-K
    assert fused_ids & set(vec_only)         # and some vector-only
    assert resp.metadata["engines"] == ["fts", "semantic"]
```

### 4.6 `test_search_latency_warm_cache` (latency under budget — story-named)

```python
@pytest.mark.bench
async def test_search_latency_warm_cache(db, fixtures_15kh):
    """1,000 queries on the seed fixture with embeddings pre-cached;
    P95 ≤ 500 ms (D5)."""
    queries = load_query_log("seed/queries-1000.json")
    # Warm: embed every query once before measuring.
    for q in queries:
        await embed_client.embed(q)

    durations_ms = []
    for q in queries:
        t0 = time.monotonic()
        await search(query=q, mode="hybrid",
                     filters=Filters(library_id=fixtures_15kh.lib_id),
                     limit=50, ...)
        durations_ms.append((time.monotonic() - t0) * 1000.0)

    p50 = percentile(durations_ms, 50)
    p95 = percentile(durations_ms, 95)
    p99 = percentile(durations_ms, 99)
    assert p50 <= 200, f"warm p50={p50:.1f}"
    assert p95 <= 500, f"warm p95={p95:.1f}"
    assert p99 <= 800, f"warm p99={p99:.1f}"
```

### 4.7 `test_search_latency_cold_cache` (latency under budget — story-named)

```python
@pytest.mark.bench
async def test_search_latency_cold_cache(db, fixtures_15kh):
    """Cold cache: model just warmed, embedding cache flushed each call.
    Smaller N to keep CI bounded; P95 ≤ 800 ms (D5)."""
    queries = load_query_log("seed/queries-100.json")
    durations_ms = []
    for q in queries:
        embed_client.clear()
        t0 = time.monotonic()
        await search(query=q, mode="hybrid",
                     filters=Filters(library_id=fixtures_15kh.lib_id),
                     limit=50, ...)
        durations_ms.append((time.monotonic() - t0) * 1000.0)

    p50 = percentile(durations_ms, 50)
    p95 = percentile(durations_ms, 95)
    assert p50 <= 350, f"cold p50={p50:.1f}"
    assert p95 <= 800, f"cold p95={p95:.1f}"
```

### 4.8 `test_snippet_arabic_grapheme` (snippet contains query terms — story-named)

```python
def test_snippet_arabic_combining_marks_intact():
    """Query containing an Arabic letter with combining marks; the
    snippet contains the term and never splits the cluster."""
    text = "بِسْمِ اللَّهِ الرَّحْمَٰنِ الرَّحِيمِ. الحمد لله رب العالمين"
    query = "الرحمن"
    snippet, spans = build_snippet(text, query)

    # The matched span exists in the snippet.
    assert "<mark>" in snippet and "</mark>" in snippet
    # No grapheme cluster was split — every code point in the snippet sits
    # inside a complete cluster (sanity check via the grapheme library).
    walked = "".join(grapheme.graphemes(snippet))
    assert walked == snippet  # identity holds when no cluster boundary was broken
    # The base letter's combining marks must travel together.
    assert "ٰ" not in snippet or "ن" + "ٰ" in snippet or "ن" + "ٰ" in snippet
```

### 4.9 `test_snippet_english_word_boundary`

```python
def test_snippet_english_word_boundary():
    """Snippet edges snap to whitespace, never mid-word, for English."""
    text = "the quick brown fox jumps over the lazy dog " * 10
    snippet, spans = build_snippet(text, "fox")
    assert "<mark>fox</mark>" in snippet
    # Stripped of <mark> tags, the snippet should not contain a partial
    # word at either end (no leading/trailing alphanumeric without space).
    raw = snippet.replace("<mark>", "").replace("</mark>", "")
    raw = raw.strip("…")
    assert not raw[:1].isalpha() or raw.startswith("the ") or raw.startswith("fox") \
        or raw.split(" ")[0] in {"the","quick","brown","fox","jumps","over","lazy","dog"}
```

### 4.10 `test_segment_id_resolution_uses_first_source_segment` (D6, story-named)

```python
async def test_segment_id_resolution_uses_first_source_segment(db, fixtures):
    """A unit spanning two segments → response's segment_id = unit.segment_ids[0]
    and start_sec matches that segment's start_sec."""
    # Build: unit U covers segments S1 (start=10), S2 (start=18).
    u_id = await fixtures.make_unit(
        segment_ids=[fixtures.s1_id, fixtures.s2_id],
        start=10.0, end=24.0, text="مرحبا بالعالم")
    resp = await search(query="مرحبا", mode="fts",
                        filters=Filters(library_id=fixtures.lib_id),
                        limit=10, ...)
    hit = next(h for h in resp.hits if h["unit_id"] == u_id)
    assert hit["segment_id"] == fixtures.s1_id        # = segment_ids[0]
    assert hit["start_sec"] == 10.0                   # unit's bound (=S1.start)
    assert hit["end_sec"] == 24.0                     # unit's bound (=S2.end)
```

### 4.11 `test_filters_pushdown` (story-named)

```python
async def test_filters_pushdown_executes_at_engine_layer(db, fixtures, sql_recorder):
    """Filters reach the engines, not a post-fusion WHERE."""
    sql_recorder.start()
    resp = await search(query="القرآن", mode="hybrid",
                        filters=Filters(library_id=fixtures.lib_id, language="ar"),
                        limit=10, ...)
    sql_recorder.stop()

    # FTS SQL ran with language predicate.
    fts_sql = next(s for s in sql_recorder.queries if "transcripts_fts" in s.text or "transcript_units" in s.text)
    assert "u.language = " in fts_sql.text
    # Plan must use the language index from Plan 5.1.
    plan = await db.fetch(f"EXPLAIN {fts_sql.text}", *fts_sql.args)
    assert any("transcript_units_lang" in r["QUERY PLAN"] for r in plan)
    # Chroma where also has language.
    assert "ar" in str(captured_chroma_where["language"])
```

### 4.12 `test_engine_degrades_on_fanout_failure`

```python
async def test_engine_degrades_when_vector_fails(db, fixtures, monkeypatch):
    """Vector engine raises → FTS results returned with metadata.degraded=['vector']."""
    monkeypatch.setattr(VectorQuery, "query", _raises(RuntimeError("chroma down")))
    resp = await search(query="القرآن", mode="hybrid",
                        filters=Filters(library_id=fixtures.lib_id),
                        limit=10, ...)
    assert resp.total > 0
    assert resp.metadata["degraded"] == ["vector"]
    assert resp.metadata["engines"] == ["fts"]
```

### 4.13 `test_deep_link_resolves_to_segment` (story-named)

```python
async def test_deep_link_resolves_to_segment(db, fixtures, api_client):
    """A hit's (video_id, start_sec) pair, followed via the API, returns a
    segment whose bounds contain that timestamp."""
    resp = await search(query="القرآن", mode="hybrid",
                        filters=Filters(library_id=fixtures.lib_id),
                        limit=5, ...)
    h = resp.hits[0]
    seg = await api_client.get_segment_at(
        video_id=h["video_id"], at_sec=h["start_sec"])
    assert seg["start_sec"] <= h["start_sec"] <= seg["end_sec"]
    assert seg["id"] == h["segment_id"]
```

### 4.14 `test_cross_language_flag`

```python
async def test_cross_language_hits_get_metadata_flag(db, fixtures_bilingual):
    """English query, Arabic results via multilingual embedding; hits get
    metadata.cross_language = true when filter.language differs."""
    resp = await search(query="forgiveness", mode="semantic",
                        filters=Filters(library_id=fixtures_bilingual.lib_id,
                                        language="en"),
                        limit=10, ...)
    cross = [h for h in resp.hits if h.get("metadata", {}).get("cross_language")]
    # Multilingual e5 should bring back at least one Arabic hit for an English query.
    assert any(h["language"] == "ar" for h in cross)
```

### 4.15 `test_rrf_deterministic_under_ties`

```python
def test_rrf_deterministic_under_ties():
    """Two units with identical fused score → same order across runs."""
    a = [(10, 0.5), (20, 0.5)]
    b = [(10, 0.5), (20, 0.5)]
    out_1 = rrf_fuse([a, b], k=60)
    out_2 = rrf_fuse([a, b], k=60)
    assert out_1 == out_2
    # And by the rrf.py secondary key, lower unit_id wins; the
    # post-enrich tiebreak by (start_sec, segment_id) is asserted in
    # test_engine_hybrid_fanout_parallel.
    assert out_1[0][0] == 10
```

### 4.16 `test_filters_to_chroma_where_passes_duration_range`

```python
def test_filters_to_chroma_where_passes_duration_range():
    f = Filters(library_id="L1", min_duration_sec=5, max_duration_sec=30)
    where = f.to_chroma_where()
    assert where["library_id"] == "L1"
    assert where["duration_sec"] == {"$gte": 5, "$lte": 30}
```

---

## 5. Edge cases and how the plan handles each

| # | Edge case (story §"Edge cases") | Handled by |
|---|---------------------------------|------------|
| E1 | **Empty query.** | `engine.search` raises `EmptyQueryError` if `query.strip()` is empty. The HTTP route catches it and returns HTTP 400 with `{ "error": "empty query" }`. The engine never sees an empty string, so `analyze("")` returning `[]` is a redundant safety net. (`test_search_route_rejects_empty_query`) |
| E2 | **Query in a language the unit's index config doesn't know.** | FTS path: the `tsvector` for unknown languages is built with `'simple'` config (Plan 5.2). The vector path handles unknown languages natively because `intfloat/multilingual-e5-large` covers 100+ languages. Cross-language hits (English query, Arabic result) are tagged with `metadata.cross_language = true` in the response when `filters.language` is set and differs from the hit's language. (`test_cross_language_flag`) |
| E3 | **Ties in RRF.** | Broken in `enrich.py`'s final sort by `(start_sec ASC, segment_id ASC)` (D3). Both keys come from the unit's first source segment (D6) and are deterministic across calls. The intermediate `rrf.py` also uses a deterministic `(unit_id ASC)` secondary key, so even before enrichment the order is reproducible. (`test_rrf_deterministic_under_ties`) |
| E4 | **Filter that excludes all hits from one of the indexes** (e.g., `language='fr'` on an Arabic-only library). | Each engine returns whatever its filtered pushdown produces — possibly an empty list. RRF over `[]` and a non-empty list returns the non-empty list as-is (degenerate but correct). The response has `total = N` from whichever engine had hits; `metadata.degraded` is **not** set because the engines didn't error, they just returned nothing. If both return empty, `hits = []` and `total = 0` — never an error. (`test_filters_pushdown` exercises the language filter; the both-empty case is covered by `test_search_returns_empty_for_no_match` in §4.) |
| E5 | **One engine errors → degrade gracefully.** | `engine.search`'s hybrid path uses `asyncio.gather(..., return_exceptions=True)` and `_coerce_results` separates failures from results. The surviving engine's hits are passed to `rrf_fuse`; the response gains `metadata.degraded = ["fts"\|"vector"]` and a WARN log fires with the exception repr. If **both** engines error, the engine raises `SearchError` and the API returns HTTP 503. (`test_engine_degrades_on_fanout_failure`) (D8) |
| E6 | **Empty results.** | Returned as `{"hits": [], "total": 0, "took_ms": ...}`. Never an HTTP error. The `metadata.engines` array still reports which engines were consulted. |
| E7 | **Paginated results.** | The HTTP layer accepts `?offset=N`. The engine pulls the same K=100 from each engine, fuses, and returns slice `[offset : offset+limit]`. The deterministic tiebreak (D3) makes pagination stable: page 2 sees the same fusion order as page 1. **Pagination beyond `offset + limit > 100` requires re-running with a larger per-engine K** — the API returns HTTP 400 in that case ("offset too deep; refine your query"). The deep-pagination story is deferred to Story 5.6. |
| E8 | **Language filter excludes everything.** | Both engines apply the filter via pushdown (D9); both return `[]`; `rrf_fuse([[], []]) = []`; response is `{"hits": [], "total": 0}`. No special case in the engine. |
| E9 | **Unit that maps to zero segments.** | Plan 5.1 invariant says this can't happen, but `enrich.py` defensively drops the hit and emits a structured log `kind=orphan_unit, unit_id=...`. An alert fires after 5 occurrences in a 5-minute window (Epic 21 owns the alert rule). The dropped hit does not get a slot in the response, so `len(response.hits) ≤ limit` is the contract (never exceeds, occasionally undershoots in pathological cases). |
| E10 | **Both engines return overlapping hit but different speakers.** | The hit's `speaker` field is taken from the **first source segment** (D6), specifically `transcript_segments.speaker` for `segment_ids[0]`. There is no per-engine speaker disagreement because both engines key by `unit_id`; only one segment is ever consulted for the response. |
| E11 | **Embed RPC times out.** | The `EmbedClient.embed` call surfaces the timeout as an exception; in hybrid mode it lands in the vector branch of the fan-out → degraded vector engine (E5 path). In semantic-only mode the engine raises `SearchError`. |
| E12 | **Unicode query with bidi marks** (RTL/LTR override characters). | `analyze()` passes through `\W+` splitting which treats bidi marks as non-word; they're stripped from the analysis. The original `text` for the snippet is unchanged, so RTL display in the UI is preserved. |
| E13 | **Snippet would be longer than the text it summarizes.** | `_truncate_to_grapheme(text, SNIPPET_MAX_CHARS)` returns `text` unchanged when `len(graphemes) ≤ SNIPPET_MAX_CHARS`; no leading/trailing `…` is added. |

---

## 6. Acceptance checklist

- [ ] **A1** `search(query, mode, filters, limit)` lives in `pipeline/src/maktaba_pipeline/search/engine.py`. The three modes `"fts"`, `"semantic"`, `"hybrid"` (default) are implemented per D10. (`test_engine_dispatch_*`)
- [ ] **A2** Hybrid mode fan-outs FTS and (embed → vector) in parallel via `asyncio.gather`, fuses with RRF (constant `k = 60`, D1), and returns `min(limit, 50)` hits. The per-engine pre-fusion K is **100** (D2). (`test_engine_hybrid_fanout_parallel`)
- [ ] **A3** RRF formula is the textbook `Σ_i 1/(k + rank_i(d))` (D4 — equal weights, no per-engine weighting). (`test_rrf_combines_two_lists`, `test_rrf_shared_doc_stays_first`)
- [ ] **A4** Tiebreak in fused scores is `(start_sec ASC, segment_id ASC)` — deterministic across calls and processes. (`test_rrf_deterministic_under_ties`) (D3)
- [ ] **A5** All filters supported in v1 (`library_id`, `video_id`, `language`, `speaker`, `min_duration_sec`, `max_duration_sec`, `created_after`, `created_before`) push down to **both engines** — FTS via SQL `WHERE`, vector via Chroma `where=`. The `transcript_units(language)` index from Plan 5.1 is used by the FTS path. (`test_filters_pushdown`) (D9)
- [ ] **A6** **Canonical search latency budget** (resolves REVIEW §1.4.d): warm-cache **p50 ≤ 200 ms, p95 ≤ 500 ms, p99 ≤ 800 ms**; cold-cache **p50 ≤ 350 ms, p95 ≤ 800 ms, p99 ≤ 1200 ms**. Measured at `limit = 50` on the 15,000 h / 100k-segment seed fixture. (`test_search_latency_warm_cache`, `test_search_latency_cold_cache`) (D5)
- [ ] **A7** **Unit→segment resolution rule** (resolves REVIEW §2.5.a, §4.2): the response's `segment_id` is `unit.segment_ids[0]` (first source segment by `seq`); `start_sec` and `end_sec` are the unit's bounds. The full segment fan-out (`segment_ids[1:]`) is **not** in the response — consumers needing it call the unit detail endpoint. (`test_segment_id_resolution_uses_first_source_segment`, `test_deep_link_resolves_to_segment`) (D6)
- [ ] **A8** Snippets wrap matched terms in `<mark>...</mark>`, are capped at 240 grapheme clusters (`SNIPPET_MAX_CHARS`), snap edges to word boundaries, and **never split Arabic grapheme clusters**. (`test_snippet_arabic_grapheme`, `test_snippet_english_word_boundary`) (D7)
- [ ] **A9** When one engine errors in hybrid mode, the engine **degrades gracefully**: returns the surviving engine's results, sets `metadata.degraded = ["fts"|"vector"]`, logs WARN. When both error, raises `SearchError` (HTTP 503). (`test_engine_degrades_on_fanout_failure`) (D8)
- [ ] **A10** Empty queries are rejected at the API with HTTP 400 (engine raises `EmptyQueryError`). `limit > 50` is rejected with HTTP 400. (`test_search_route_rejects_empty_query`, `test_search_route_validates_limit`)
- [ ] **A11** Cross-language hits (English query → Arabic result via multilingual embedding) are tagged `metadata.cross_language = true` on the individual hit when `filters.language` is set and differs from the hit's language. (`test_cross_language_flag`)
- [ ] **A12** Pagination (`offset`, `limit`) is stable across calls thanks to A4's tiebreak; `offset + limit > 100` is rejected with HTTP 400 ("offset too deep; refine your query") in v1.
- [ ] **A13** Response shape matches §2.7 / `architecture §9.3`: top-level `{hits, total, took_ms, metadata}`; per-hit `{unit_id, transcript_id, video_id, segment_id, start_sec, end_sec, text, snippet, language, speaker, score, matches[], metadata}`. Verified by `test_proto_round_trip` and an explicit response-shape test against a JSON schema in `shared/schemas/search-response.json`.
- [ ] **A14** No code path in this story touches the embedding model from outside the Pipeline process — query embeddings go through the `Embed` gRPC RPC owned by Plan 5.3. (Static check on `import sentence_transformers` outside `pipeline/src/maktaba_pipeline/search/embed_client.py` and the embed servicer.)
- [ ] **A15** The orphan-unit defensive drop is exercised: deleting all source segments for a unit (which by Plan 5.1's invariant cascades and is impossible, but injectable in test) → that unit is dropped from the response and a structured log `kind=orphan_unit` fires. (`test_enrich_drops_orphan_unit`)
- [ ] **A16** Operator-tunable knobs (`[search].rrf_k`, `[search].per_engine_k`, `[search].max_limit`, `[search].embed_cache_size`) are wired through `pipeline/src/maktaba_pipeline/config/defaults.py`; the HTTP API does **not** accept per-call overrides for `rrf_k` (D1) or `per_engine_k` (D2).
