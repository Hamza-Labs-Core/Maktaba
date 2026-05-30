# Epic 18 — Performance: spec-vs-implementation gap analysis

**Verdict (one line):** Epic 18 is a façade — the budget YAML, a generic
in-memory cache type, and a pool-config helper exist as isolated library code
with unit tests, but **nothing measures real endpoint latency, no perf gate
runs in CI (`make perf` does not exist; `make perf-ci` is a literal stub), and
the caches/pools/stage-timers are not wired to any hot path.** ~6 of 8 stories
are effectively unimplemented.

Method: every AC below was checked against code, not against
`specs/FULL_IMPLEMENTATION_AUDIT.md` or spec self-claims. Citations are
`file:line`.

## Status legend

- **complete** — exists, reachable, behaviorally satisfies the AC.
- **partial** — exists but does not fully satisfy the AC.
- **missing** — no implementing code found.
- **unwired** — code exists but is never called on the path the AC requires.
- **stub** — placeholder that returns/echoes without doing the work.

---

## Story 18.1 — Define and codify latency budgets

| AC | Status | Evidence / gap |
|----|--------|----------------|
| AC1 — `shared/perf_budgets.yaml` lists every user-facing endpoint with p50/p95/p99, profile tag, cache tag | **partial** | `shared/perf_budgets.yaml:1-145` exists with endpoints, profiles, cache tags. Gaps: only `linux-x86-16gb` profile is actually used for every endpoint (the `mac-m2-8gb` profile is declared `:26` but referenced by zero endpoints, contradicting story AC2 which requires the Mac profile as primary); GraphQL surface absent; "search first result" not a distinct entry. |
| AC2 — Initial v1 budgets at the 1k-video/10k-segment fixture | **missing** | Numbers are transcribed into YAML (`:29-116`) but there is **no fixture** (`tests/fixtures/perf-1k/` absent) and **no measurement** binding them to the fixture. The values are unverified literals. |
| AC3 — Perf harness loads budgets; assertions read from file (no magic numbers) | **partial** | Loader `api/internal/perf/budgets.go:63 Load`, validator `:80 Validate`, and `pipeline/src/maktaba_pipeline/perf/budgets.py` exist and are unit-tested (`api/internal/perf/budgets_test.go`, `pipeline/tests/perf/test_perf.py`). But **no harness consumes them to drive endpoints** — `Gauge.Observe`/`Report` (`budgets.go:168-197`) is never called from any endpoint test. |
| AC4 — `make perf` runs all budgets against seeded dev instance, exits non-zero on breach, names offending endpoint | **stub** | There is **no `make perf` target** in the `Makefile`. `make perf-ci` (`Makefile:310-317`) runs `perf-ci-inner` which is `@echo "perf-ci stub: Epic 20.7 will replace this..."`. CI workflow `.github/workflows/_perf-ci.yml:43` calls `make perf-ci` → the stub. No breach is ever detected in CI. |

TC1 (loader rejects malformed) — **complete** (`budgets_test.go:64-86`, `test_perf.py:19-34`).
TC2 (integration green against seeded stack) — **missing** (no fixture, no integration harness).
TC3 (artificially slow one endpoint, harness fails naming it) — **missing** (no per-route injection middleware, no harness to fail).
EC1/EC2/EC3 (cold-on-warm skip, 5-trial median + 3σ, hardware-profile-mismatch fail-fast) — **missing**: the 5-trial/warm-up/median/3σ runner described in plan-18-01 §4 and the `DetectProfile` of §5 do not exist in code.

---

## Story 18.2 — Search end-to-end performance

Note: plan-18-02 specifies an `api/internal/search/` package; the real search
code is `api/internal/handlers/search/search.go`. No `api/internal/search/`
directory exists.

| AC | Status | Evidence / gap |
|----|--------|----------------|
| AC1 — hybrid search at 100k segments, warm p95 ≤ 500 ms / cold p95 ≤ 1.5 s, asserted independently in CI | **missing** | No 100k-segment fixture, no load test, no warm/cold metric, no CI assertion. `search.go` records `took.FTS/Semantic/Fusion` (`:181,190,209`) in the response body only — never compared to a budget. |
| AC2 — query embedding cached in-process (LRU 10k), hit-rate ≥ 90 % | **missing** | No embedding cache anywhere. `search.go` calls `h.Semantic.Search(...)` (`:189`) on every request with no caching layer; the `SemanticClient` interface (`:38-42`) embeds + queries Chroma per call. The generic `perf.Cache` (`api/internal/perf/cache.go`) is never instantiated for embeddings. |
| AC3 — fusion allocation-bounded, no O(N) allocs beyond result page | **partial** | `RRFuse` (`search.go:470-498`) builds `scores`/`rec` maps sized to the full union and `out` sized `len(rec)` (`:486`), i.e. O(N) in total hits, not bounded to the result page as the AC requires. No `testing.AllocsPerRun` assertion exists. |
| AC4 — Embed gRPC hard 200 ms budget; on breach log + continue FTS-only + `degraded: true` in payload | **missing** | No per-call 200 ms deadline on the semantic call (`search.go:189` passes `r.Context()` straight through). The `Response` struct (`:85-91`) has **no `degraded` field**. Semantic errors are silently swallowed (`semHits, _ = ...` at `:189`) with no log, no metric, no degradation flag. |

TC1 (load, 50 qps, p95≤500) — **missing**. TC2 (replay-twice cache hit-rate) — **missing** (no cache). TC3 (kill pipeline → `degraded:true`) — **missing** (no flag). TC4 (cold budget, Embed round-trip per request) — **missing**.
EC1 (empty query → 400, no DB hit) — **complete** (`search.go:124-132`, returns before any query). EC2/EC3/EC4 — **not verifiable / unimplemented** (no tokenizer-perf, no bidi handling in highlight, no LRU since no cache).

---

## Story 18.3 — Streaming hot-path performance

| AC | Status | Evidence / gap |
|----|--------|----------------|
| AC1 — `OpenSession` gRPC p95 ≤ 80 ms for a probed video | **partial / unmeasured** | `streaming/internal/grpcsrv/server.go` + `session/session.go` implement session open; `probe.Cache` (`streaming/internal/probe/probe.go:52`) is an LRU fronting the DB. But there is **no latency metric** (`streaming_open_session_duration_seconds` absent) and **no budget assertion**. The 80 ms p95 is unverified. |
| AC2 — master manifest from cached probe data, zero FFmpeg, p95 ≤ 30 ms | **partial** | Manifest is built from probe data (no FFmpeg on that path). No `streaming_manifest_duration_seconds` metric, no 30 ms assertion. |
| AC3 — range hit on cached segment: `Content-Length` set, no chunked encoding, p95 first-byte ≤ 100 ms | **partial** | Range-serving handlers exist under `streaming/internal/handlers/`. No first-byte metric, no budget gate. |
| AC4 — transcode worker pool exposes `transcode_queue_depth`; 0 under direct-play | **missing** | `grep transcode_queue_depth` across `streaming/` returns nothing. The metric is not defined or exported. |

EC2 — concurrent identical cold-segment requests → single FFmpeg (single-flight): **missing**. `grep singleflight` in `streaming/internal/handlers/` and `streaming/internal/cache/` returns nothing; the segment GC (`streaming/internal/cache/layout.go`) is an LRU sweeper with **no singleflight group**. The probe cache keys by `videoID` only (`probe.go:86 Lookup(ctx, videoID)`), **not** `(path,size,mtime)`, so EC3-style invalidation in plan-18-03 §3 is not implemented.
TC1/TC2/TC3 — **missing** (no perf test files under streaming).

---

## Story 18.4 — Pipeline throughput targets

| AC | Status | Evidence / gap |
|----|--------|----------------|
| AC1 — transcription ≥ 4× realtime (Mac, whisper-mlx large-v3) | **missing** | No benchmark, no fixture (`pipeline/tests/fixtures/arabic-20min.wav` etc. absent), no assertion. |
| AC2 — indexing ≥ 50 seg/s sustained | **missing** | No benchmark/assertion. `ThroughputProbe` (`pipeline/src/maktaba_pipeline/perf/throughput.py`) is a generic rolling counter, unit-tested only (`test_perf.py:37-50`); never wired to the index stage. |
| AC3 — thumbnail+sprite for 60 min ≤ 90 s | **missing** | No benchmark/assertion/fixture. |
| AC4 — `pipeline_stage_duration_seconds` histogram exposes per-stage p50/p95, queried by harness | **partial/unwired** | A `StageTimer` and `Histogram` exist in `pipeline/src/maktaba_pipeline/observability.py:507,197`, but the emitted metric is `maktaba_job_attempts_total{stage,outcome}` (a **counter**, `:306-307,337`), **not** a `pipeline_stage_duration_seconds` histogram. Worse, `grep StageTimer` across `pipeline/src/` (excluding the definition) shows **zero callers** — no stage records durations, and no harness queries it. |

EC3 (MLX→faster-whisper fallback with `pipeline_fallback_total`) — **not found** as wired; backend selection per plan-18-04 §8 not located in stage code.

---

## Story 18.5 — Memory and CPU envelopes

| AC | Status | Evidence / gap |
|----|--------|----------------|
| AC1 — API idle RSS ≤ 80 MiB / 250 MiB @200qps / slope < 1 MiB/h over 24 h | **missing** | No soak harness. `tests/soak/` absent; `grep smaps_rollup\|SlopeMiBPerHour` returns nothing. `shared/perf_budgets.yaml:132-145` lists `envelopes:` with *different* numbers (api_resident_mb p95 350) than the story (80/250 MiB) — and nothing reads them. |
| AC2 — Streaming idle ≤ 100 MiB / ≤ 300 MiB @8 transcodes | **missing** | No measurement. |
| AC3 — Pipeline idle ≤ 600 MiB / bounded burst | **missing** | No measurement; `envelopes.yaml` (plan-18-05 §7) absent. |
| AC4 — goroutine + asyncio-task count emitted as metrics; no-leak soak | **partial** | `runtime.NumGoroutine` may be exposed via expvar somewhere, but the soak/leak assertion (plan-18-05 §9 TC3 `tests/streaming/leak_test.go`) does not exist. |

Entire story is **missing** at the verification level (no soak/burst/leak harness, no `meter/` package, no CI job).

---

## Story 18.6 — Client-perceived performance

| AC | Status | Evidence / gap |
|----|--------|----------------|
| AC1 — PWA cold LCP ≤ 2.0 s / TBT ≤ 200 ms on 4G | **missing** | No `web/lighthouse/lighthouserc.cjs`, no `budget.json`. `find web` for lighthouse/playwright perf specs returns nothing. |
| AC2 — PWA warm LCP ≤ 600 ms | **missing** | No LHCI config. |
| AC3 — TTFF p95 ≤ 1.5 s warm / ≤ 3.5 s cold | **missing** | No `web/tests/perf/time_to_first_frame.spec.ts`. |
| AC4 — search keystroke-to-results p95 ≤ 750 ms (debounced) | **missing** | No `web/tests/perf/search_keystroke.spec.ts`; no `.github/workflows/web-perf.yml`. |

Entire story **missing**. No Lighthouse, no Playwright perf, no Maestro flow.

---

## Story 18.7 — Database query performance & N+1 prevention

| AC | Status | Evidence / gap |
|----|--------|----------------|
| AC1 — every query in `shared/db/queries/` has an EXPLAIN ANALYZE snapshot; seq-scan on >10k-row table fails | **missing** | `shared/db/queries/` **does not exist**; no sqlc (`sqlc.yaml` absent); `tests/explain/` absent; no `EXPLAIN` snapshot test anywhere (`grep -i explain` over `*.go`/`*.py` returns nothing). Queries are inline in handlers (e.g. `search.go:246`). |
| AC2 — hot-path queries ≤ 5 ms backed by named covering indexes | **missing** | No covering indexes. `grep` of `shared/db/migrations/` for `library_id…created_at` / `INCLUDE` / `processing_jobs…finished_at` returns nothing; only `0014_transcript_segments_speaker_index.sql` exists. The plan-18-07 §3 migration `00xx_indexes_perf.sql` was never written. No ≤5 ms assertion. |
| AC3 — `db_query_count_total` metric; hard per-request query cap (e.g. GET /api/videos ≤ 3) | **missing** | No querycount middleware (`grep db_query_count\|querycount\|IncQuery` over `api/` returns nothing). `api/internal/middleware/querycount.go` (plan-18-07 §6) absent. |
| AC4 — Postgres + SQLite pass same query suite (parity test) | **missing** | No `tests/db/parity_test.go`; `pipeline/db/queries.py` mirror absent. `search.go:258` has an ad-hoc SQLite LIKE fallback but no parity test. |

Entire story **missing** (the perf audit's claim that "query EXPLAINs exist" is **false** — none were found).

---

## Story 18.8 — Cache layout and hit-rate floors

| AC | Status | Evidence / gap |
|----|--------|----------------|
| AC1 — each cache exports `*_cache_hits_total`/`_misses_total`/`_size` | **missing/partial** | The generic `perf.Cache` tracks `hits/misses/evicts` and a `Stats()` (`api/internal/perf/cache.go:91-111`) but is **never instantiated for any real cache** (no `perf.NewCache(...)` callers outside `cache_test.go`). The streaming `probe.Cache` has **no hit/miss counters**; JWKS cache (`streaming/internal/auth/jwks.go`) exports no `*_cache_hits_total`. No Prometheus cache metrics are wired. The plan-18-08 `shared/cache/reporter.go` does not exist. |
| AC2 — documented hit-rate floors (HLS ≥70 %, embed ≥90 %, probe ≥99 %, JWKS ≥99 %) enforced after warm-up | **missing** | No replay test (`tests/cache/replay_session_test.go` absent), no floor assertion. Embedding cache does not exist at all. |
| AC3 — explicit named eviction policy per cache, tested | **partial** | `perf.Cache` does LRU+TTL eviction (`cache.go:71-82,114-124`) and is tested in isolation; streaming `cache/layout.go` GC is LRU; but no `Policy{Kind:...}` descriptor (plan-18-08 §2) and the policies are not surfaced/asserted per the AC. |
| AC4 — `maktaba-streaming gc` + equivalent admin endpoint can drop each cache; next request refills | **unwired** | Admin endpoint `POST /admin/cache/{name}/flush` is implemented (`api/internal/handlers/perf/admin.go:35`) and **is now mounted** via `router.MountP10` (`api/internal/router/p10.go:105`, called from `api/main.go:266`) — this corrects the prior audit's "UNMOUNTED" claim. **However it is dead**: `MountP10` is called with `router.P10Deps{DB: appDB}` only (`main.go:266`), so `PerfRegistry` is nil → `p10.go:101-103` substitutes an **empty** `perf.NewRegistry()`. Nothing ever calls `Registry.Register(...)`, so every `/admin/cache/{name}/flush` returns **404 "cache not found"** (`admin.go:43-45`) and `/api/admin/perf/budgets` returns empty (`PerfBudgets` nil, `admin.go:70-72`). There is **no `maktaba-streaming gc` CLI** (`grep "gc"\|cobra` in `streaming/` returns nothing; no `cmd/maktaba-streaming`). |

EC1 (JWKS rotation ≤5 min) / EC2 (embed full-text key) / EC3 (probe (size,mtime) invalidation) — **missing**: embed cache absent; probe cache keys by videoID not (path,size,mtime).

---

## Cross-cutting wiring failures (verified, not trusted)

1. **`perf.ApplyPool` is never called.** `api/internal/perf/pool.go:35 ApplyPool` + `DefaultPoolConfig` (MaxOpen 50, etc.) are dead code. Real pool sizing is `api/main.go:220 appDB.SetMaxOpenConns(envIntDefault("MAKTABA_DB_MAX_OPEN", 32))` and `main.go:431 SetMaxOpenConns(1)` — bypassing the perf package and its tuned numbers entirely. The audit's "pools exist" is true only as orphan code.
2. **`perf.Cache` is never used on a hot path.** No `perf.NewCache` caller exists outside `cache_test.go`. Manifest/segment/library hot paths do not consult it. The audit's "caches exist" is true only as orphan code.
3. **`ResponseTimer` middleware is documented but does not exist.** `budgets.go:8-9` promises a `ResponseTimer` middleware emitting `http_endpoint_p95_breach_total`; no such symbol exists anywhere (`grep ResponseTimer` finds only the doc comment). The per-request budget-breach metric (the only mechanism that could make budgets self-enforcing at runtime) is absent.
4. **No perf gate in CI.** `make perf` does not exist; `make perf-ci` is an echo stub; nightly full suite referenced by plan-18-01 §12 does not exist. A 200 ms regression on any endpoint would not fail any pipeline.

---

## Top gaps by impact

1. **No latency measurement or CI enforcement exists at all (Stories 18.1 AC3/AC4, 18.2, 18.3, 18.6).** The entire epic's value proposition — "regress-tested budgets fail CI" — is unrealized. `make perf` is absent and `make perf-ci` echoes a stub string. Every p50/p95/p99 number in `shared/perf_budgets.yaml` is an unverified literal. **Highest impact: a 10× latency regression ships green.**

2. **Search performance story is structurally absent (Story 18.2 AC2/AC4).** No embedding LRU cache, no 200 ms Embed deadline, no `degraded` flag, no graceful degradation. Semantic errors are silently dropped (`search.go:189 semHits, _ = ...`). On any pipeline slowness, search latency is unbounded and users get no degraded-mode signal — the opposite of the AC.

3. **Database performance story is entirely missing (Story 18.7, all ACs).** No `shared/db/queries/`, no sqlc, no EXPLAIN snapshots, no covering-index migration, no query-count cap, no N+1 detector. The prior structural audit's claim that "query EXPLAINs exist" is **false**. N+1 regressions and accidental seq-scans on the segments/jobs tables are entirely undetected.

4. **Cache/pool/timer infrastructure is orphaned (Stories 18.5, 18.8; cross-cutting).** `perf.Cache`, `perf.ApplyPool`, `StageTimer`, and the admin flush endpoint are implemented and unit-tested but wired to nothing real: the admin flush endpoint 404s on every call because its registry is always empty; the pool helper is bypassed by raw `SetMaxOpenConns`; `StageTimer` has zero callers; the documented `ResponseTimer` middleware does not exist.

### Single worst gap

There is **no latency measurement harness and no perf gate in CI** for the
entire epic. `make perf` does not exist; `make perf-ci` is literally
`@echo "perf-ci stub..."` (`Makefile:317`), and `.github/workflows/_perf-ci.yml`
invokes that stub. The budget loader, `Gauge.Report`, and `perf_budgets.yaml`
exist but are never driven against any endpoint or fixture. Consequently every
budget is an unenforced literal and any regression — 2× or 100× — ships green.
