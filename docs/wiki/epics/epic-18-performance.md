# Epic 18 — Performance

> **Status:** spec + plans complete. **Source:** `specs/epics/18-performance/`.
> **Anchors:** [`architecture.md` §9](../../../specs/architecture.md) (single-host capacity), §10 (endpoint budgets), §10.3 (cache layout).

## Goal

Maktaba feels snappy on a single Mac mini / NAS-class host with a 30 TB library. User-facing latency budgets are explicit, measured in CI on representative hardware, and regress-tested. Hot paths (search, manifest issue, range serve, WS event) hit cache; cold paths (cold transcode, cold embed) are bounded and observable. This epic is the **normative source for v1 latency budgets** — when [`architecture.md`](../../../specs/architecture.md) gives the same number, the two must agree.

## Stories & Plans

| # | Story | Plan | Summary |
|---|-------|------|---------|
| 18.1 | [Latency budgets](../../../specs/epics/18-performance/story-18-01-latency-budgets.md) | [plan-18-01](../../../specs/epics/18-performance/plan-18-01-latency-budgets.md) | Per-endpoint p50/p95/p99 budgets in `shared/perf_budgets.yaml`; CI assertions. |
| 18.2 | [Search end-to-end](../../../specs/epics/18-performance/story-18-02-search-performance.md) | [plan-18-02](../../../specs/epics/18-performance/plan-18-02-search-performance.md) | Hybrid (FTS+vector) search; 200 ms hard embed deadline → FTS-only fallback; embedding LRU ≥90 % hit rate. |
| 18.3 | [Streaming hot path](../../../specs/epics/18-performance/story-18-03-streaming-hot-path.md) | [plan-18-03](../../../specs/epics/18-performance/plan-18-03-streaming-hot-path.md) | Manifest from cached probe (no FFmpeg); range-aware segment serve; single-flight cold transcodes. |
| 18.4 | [Pipeline throughput](../../../specs/epics/18-performance/story-18-04-pipeline-throughput.md) | [plan-18-04](../../../specs/epics/18-performance/plan-18-04-pipeline-throughput.md) | Per-stage benchmarks: transcribe ≥4× realtime; index ≥50 seg/s; thumbnails ≤90 s/60 min. |
| 18.5 | [Memory & CPU envelopes](../../../specs/epics/18-performance/story-18-05-memory-cpu-envelopes.md) | [plan-18-05](../../../specs/epics/18-performance/plan-18-05-memory-cpu-envelopes.md) | Per-service RSS ceilings + 24 h soak (slope <1 MiB/h); goroutine / asyncio task tracking. |
| 18.6 | [Client-perceived performance](../../../specs/epics/18-performance/story-18-06-client-perceived-performance.md) | [plan-18-06](../../../specs/epics/18-performance/plan-18-06-client-perceived-performance.md) | Lighthouse + Playwright budgets for cold/warm load, player TTFF, search keystroke-to-paint. |
| 18.7 | [DB query performance & N+1](../../../specs/epics/18-performance/story-18-07-database-query-performance.md) | [plan-18-07](../../../specs/epics/18-performance/plan-18-07-database-query-performance.md) | EXPLAIN snapshots per named query; per-endpoint query-count caps; covering indexes. |
| 18.8 | [Cache layout & hit-rate floors](../../../specs/epics/18-performance/story-18-08-cache-layout-hit-rates.md) | [plan-18-08](../../../specs/epics/18-performance/plan-18-08-cache-layout-hit-rates.md) | Every cache exports hit/miss/size metrics; explicit eviction policy; admin flush endpoint. |

## Latency budget table (warm cache, 1 000-video / 10 k-segment fixture, `mac-m2-8gb`)

| Surface | p50 | p95 | p99 |
|---|---|---|---|
| `GET /api/libraries` | ≤30 ms | ≤80 ms | — |
| `GET /api/videos/{id}` | ≤25 ms | ≤60 ms | — |
| `POST /api/search` (warm) | ≤250 ms | ≤500 ms | ≤800 ms |
| `POST /api/search` (cold) | — | ≤1.5 s | ≤2 s |
| HLS manifest (warm) | ≤50 ms | ≤120 ms | — |
| HLS segment (warm cache hit) | ≤40 ms | ≤100 ms | — |
| HLS segment (cold transcode) | ≤2.5 s | ≤6 s | — |
| WS `/ws/job-progress` (NOTIFY → client) | — | ≤250 ms | — |
| Time-to-first-frame (web warm) | — | ≤1.5 s | — |

## Cache hit-rate floors (story 18.8)

| Cache | Type | Floor |
|---|---|---|
| HLS segment | disk LRU, 50 GiB | ≥70 % |
| Embedding | in-mem LRU, 10 k entries | ≥90 % |
| FFprobe | in-mem LRU, 4 k entries | ≥99 % |
| JWKS | TTL 5 min | ≥99 % |

## Key technical decisions

- **Latency-budget hierarchy.** End-to-end budgets (this epic) are the user-facing contract; internal sub-budgets (e.g., 200 ms DB query) are decomposed inside. No duplication; any drift between architecture.md and `perf_budgets.yaml` is a CI failure.
- **Cache-state tagging.** Every measurable endpoint carries a `warm` or `cold` tag in `perf_budgets.yaml`. Warm runs abort if hit-rate <50 % (detection of accidental cold state); cold runs skip with reason if instance is accidentally warm.
- **Single-flight on cold paths.** Streaming segment cold transcodes and search-embedding cache misses use `golang.org/x/sync/singleflight` (Go) / `asyncio.Lock` (Python) so N concurrent identical requests spawn 1 upstream operation; all N wait on the result.
- **Embedding deadline as hard deadline.** Pipeline `Embed` gRPC has a 200 ms server-side budget. Breach → graceful degradation: return FTS-only with `degraded: true`. No silent retry.
- **Content-hash identity in storage.** `content_hash = blake3(first_4MiB ‖ size_le8 ‖ last_4MiB)`. Renames/moves do not trigger re-processing; underpins cold-scan bounded memory and dedup.
- **Metrics-as-contracts.** Every story exports Prometheus metrics; the perf harness loads them; assertions read from them.
- **CI regression testing.** `make perf` runs nightly on self-hosted `mac-m2-8gb` and `linux-x86-16gb`. Breach → non-zero exit; `tests/perf/results.json` published.

## Files & code paths introduced

- `shared/perf_budgets.yaml` (canonical budget file)
- `tests/perf/{budgets,runner,profile}.go`
- `api/internal/search/{handler,orchestrator,embed_cache,fusion,degradation}.go`
- `streaming/internal/{session/manager,probe/cache,hls/manifest,segments/cache,segments/singleflight}.go`
- `pipeline/maktaba_pipeline/stages/{transcribe,index,thumbnail}.py`
- `tests/soak/main.go` + `tests/soak/{workload,meter}/`
- `web/lighthouse/{lighthouserc.cjs,budget.json}`, `web/tests/perf/*.spec.ts`
- `shared/cache/{policy,reporter,flusher}.go`
- DB indexes (story 18.7): `videos_library_id_created_at_idx`, `segments_video_id_ts_start_idx`, `processing_jobs_pending_priority_idx`, `processing_jobs_state_finished_at_idx`, `processing_jobs_pending_created_at_idx`

## Migrations claimed

This epic ships index migrations for hot tables. Index slots are claimed by individual plans; see the manifest. Story 18.7 introduces a perf-index migration (`00xx_indexes_perf.sql`) using `CREATE INDEX CONCURRENTLY`. No new tables.

## Dependencies

- **Epic 5** (Search Indexing): FTS5 schema, Chroma upsert, embedding model.
- **Epic 6** (Job Queue): `processing_jobs` schema, `SKIP LOCKED` claim.
- **Epic 3** (Transcription): Whisper MLX / faster-whisper backends.
- **Epic 1** (Scanner): cold-scan of 30 TB tree (depends on hash identity).
- **Story 21.2** (Metrics surface).
- Story **18.1** is foundational; all other 18.x depend on it.

## Out of scope

- Multi-host scaling — see [Epic 19](epic-19-scalability.md).
- Bundle-size budgets — Epic 11.
- CDN / edge caching (no CDN in v1).
- Real-time search analytics (lookahead, popularity weighting) — baseline is static FTS+vector fusion.
- Native-app perf budgets (Capacitor/Tauri spot-checked via Maestro flows, not blocking CI).
- Model selection / fine-tuning — owned by Epic 3.

## See also

- [Epic 19 — Scalability](epic-19-scalability.md) (capacity floor, scale axes).
- [Epic 21 — Observability](epic-21-observability.md) (metrics surface, traces).
- [Epic 20 — Testing](epic-20-testing.md) (perf-CI subset and quarantine).
- [Glossary](../glossary.md) — latency budget, hot path, warm/cold path, single-flight, graceful degradation, soak test, content_hash, cache hit rate.
