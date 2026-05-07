# Story 18.2 — Search end-to-end performance

Search is Maktaba's signature feature; it must feel instant. Cover both
cache-cold and cache-warm paths and the FTS + vector fusion overhead.

The numbers here are the canonical normative values for v1. Where the
pipeline epic ([Story 5.4](../01-pipeline/)) gives a stricter
*internal* budget (200 ms p95 for the database-side query path on a
15 k-hour library), that number is a sub-budget that must be met inside
the end-to-end values below.

## Acceptance criteria

- AC1. Hybrid search at 100 k segments returns the top-20 fused result set
  within these end-to-end p95 budgets on the reference Mac profile:
  - **Warm** (embedding cache hit) — p95 ≤ 500 ms, p99 ≤ 800 ms.
  - **Cold** (embedding cache miss requiring a fresh `Pipeline.Embed` call)
    — p95 ≤ 1.5 s, p99 ≤ 2.0 s.
  Warm and cold are reported as separate metrics; CI asserts both
  independently. The `degraded` response path described in AC4 is
  excluded from these budgets.
- AC2. Query embedding is cached in-process (LRU, 10 k entries); cache
  hit rate ≥ 90 % after warm-up against the test query corpus.
- AC3. The fusion step (RRF or weighted) is allocation-bounded — no
  `O(N)` Go allocations per request beyond the result page.
- AC4. The Pipeline `Embed` gRPC call has a hard 200 ms server-side budget
  per query; on breach the API logs the slow query and continues with FTS
  results only (graceful degradation), and the response payload carries a
  `degraded: true` flag.

## Test cases

- TC1. Load: 200 concurrent search queries against a 100 k-segment
  fixture with a warm embedding cache; throughput ≥ 50 qps with p95
  ≤ 500 ms.
- TC2. Cache: replay the same 100 queries twice; second pass p95 must be
  ≥ 30 % faster than first pass and embedding cache must show ≥ 90 % hit
  rate.
- TC3. Degradation: kill the Pipeline service mid-test; subsequent
  searches return FTS-only results within the FTS-only budget (warm
  p95 ≤ 250 ms) and the response payload carries a `degraded: true` flag.
- TC4. Cold search budget: run 100 unique queries against an empty
  embedding cache; assert p95 ≤ 1.5 s and verify the per-query path
  shows an `Embed` round-trip on each request.

## Edge cases

- EC1. Empty query — must return 400 with no DB hit.
- EC2. Single-character Arabic query — must still tokenize correctly
  through the FTS5 unicode61 tokenizer and not blow out the result set.
- EC3. RTL + LTR mixed query (e.g., Arabic + a Latin proper noun) —
  highlighting must not re-order tokens; perf budget unchanged.
- EC4. Query of exactly the cache size (10 k unique strings) — LRU
  eviction must not cascade into a stop-the-world; p99 ≤ 1.5 s under
  the warm budget; cold-budget unchanged.
