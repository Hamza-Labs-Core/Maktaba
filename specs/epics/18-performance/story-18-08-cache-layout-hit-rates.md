# Story 18.8 — Cache layout and hit-rate floors

Every cache (HLS segments, embedding, probe, JWKS, FTS prepared
statements) must publish a hit-rate metric, have a configured size, and
be exercised by tests.

## Acceptance criteria

- AC1. Each cache exports `*_cache_hits_total`, `*_cache_misses_total`,
  `*_cache_size_bytes` (or entries).
- AC2. Documented hit-rate floors after warm-up:
  HLS segment ≥ 70 %, embedding ≥ 90 %, probe ≥ 99 %, JWKS ≥ 99 %.
- AC3. Each cache has an explicit eviction policy (LRU / TTL / size-bounded
  with single-flight) named in the code and tested.
- AC4. A `maktaba-streaming gc` and an equivalent admin endpoint can
  drop each cache, and the next request fills it correctly.

## Test cases

- TC1. Replay a real-shape session log and assert hit-rate floors.
- TC2. Forced eviction: fill HLS cache to `max_gib + 5 %`; LRU evicts
  exactly down to `max_gib × 0.95` and resumes serving.
- TC3. Single-flight: 50 simultaneous misses for the same key spawn 1
  upstream call; all 50 receive the same payload byte-for-byte.

## Edge cases

- EC1. JWKS rotation mid-flight: cached public keys are honored until TTL,
  but new keys are picked up within ≤ 5 minutes (configurable).
- EC2. Embedding cache key collision (two different texts hashing to the
  same key, vanishingly rare) — test verifies the cache stores full text,
  not hash, as the key.
- EC3. Probe cache invalidation when a file's `(size, mtime)` changes —
  the next manifest issue must re-probe and overwrite the entry atomically.
