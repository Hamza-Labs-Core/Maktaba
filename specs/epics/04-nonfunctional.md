# Maktaba — Epics 04: Non-Functional Requirements

> Cross-cutting quality attributes for the platform. These epics describe the
> standards every functional surface (Epics 01–03) is held to: performance
> envelopes, scale targets, the test pyramid, observability, delivery, security
> posture, and durability of state.
>
> Source of truth for capacities, topology, and design contracts is
> [`specs/architecture.md`](../architecture.md). Where this document gives a
> number, it is normative for v1; where the architecture document gives the
> same number, the two must agree.

## Conventions used in this document

- **Epic** — a thematic grouping of related stories (one quality attribute).
- **Story** — a discrete unit of work small enough to land in 1–3 PRs.
- **Acceptance criteria** (AC) — checks that must hold before the story is
  marked done. Each AC is independently verifiable.
- **Test cases** (TC) — concrete, named scenarios the test suite will cover
  for the story. Format: *given / when / then* compressed to one or two
  sentences.
- **Edge cases** (EC) — known-tricky inputs or environmental conditions the
  implementation must explicitly handle.
- **Service identifiers** — `api` (Go), `streaming` (Go), `pipeline`
  (Python), `web` (TypeScript), `apps/*` (Capacitor / Tauri / Swift /
  Kotlin). Where a story belongs to "all backend services," it means
  api + streaming + pipeline.

---

## Epic 18 — Performance

**Goal.** Maktaba feels snappy on a single Mac mini / NAS-class host with a
30 TB library. User-facing latency budgets are explicit, measured in CI on
representative hardware, and regress-tested. Hot paths (search, manifest
issue, range serve, WS event) hit cache; cold paths (cold transcode, cold
embed) are bounded and observable.

This epic does **not** cover scale beyond a single household — that's
Epic 19. It covers what "fast enough" means at one box.

### Story 18.1 — Define and codify latency budgets

Establish per-endpoint p50 / p95 / p99 budgets and encode them as test
assertions so a regression fails CI.

**Acceptance criteria:**

- AC1. A `perf_budgets.yaml` file under `shared/` lists every user-facing
  endpoint (REST, GraphQL, WS, HLS manifest, HLS segment first byte, search
  first result) with `p50_ms`, `p95_ms`, `p99_ms`, and a hardware profile
  tag (`mac-m2-8gb`, `linux-x86-16gb`).
- AC2. Initial v1 budgets, all measured at a 1,000-video / 10 k-segment
  warm cache:
  - `GET /api/libraries` — p50 ≤ 30 ms, p95 ≤ 80 ms.
  - `GET /api/videos/{id}` — p50 ≤ 25 ms, p95 ≤ 60 ms.
  - `POST /api/search` (FTS+vector fusion, top 20) — p50 ≤ 250 ms, p95 ≤
    500 ms, p99 ≤ 800 ms.
  - HLS manifest first byte (warm) — p50 ≤ 50 ms, p95 ≤ 120 ms.
  - HLS segment first byte (warm cache hit) — p50 ≤ 40 ms, p95 ≤ 100 ms.
  - HLS segment first byte (cold transcode) — p50 ≤ 2.5 s, p95 ≤ 6 s.
  - WebSocket job-progress event end-to-end (Postgres NOTIFY → client) —
    p95 ≤ 250 ms.
  - Time-to-first-frame in the web player on a warm session — p95 ≤ 1.5 s.
- AC3. The perf-budget file is loaded by the perf test harness; assertions
  read budgets from it (no magic numbers in tests).
- AC4. The `make perf` target runs all budgets against a seeded dev
  instance and exits non-zero on any breach.

**Test cases:**

- TC1. Unit: budget loader rejects malformed YAML (missing key, negative
  ms, p99 < p95) with a clear error.
- TC2. Integration: against a fresh dev compose stack with the seed
  fixture, every endpoint listed reports < budget.
- TC3. Regression: artificially slow `pgx` to add 200 ms to every query;
  the harness must fail with the offending endpoint named.

**Edge cases:**

- EC1. Cold-cache run: budgets are tagged `warm` or `cold`; running the
  warm suite against a cold instance must explicitly skip with a clear
  reason, not silently pass.
- EC2. CI runner variance: each measurement is the median of 5 trials
  after 1 warm-up; outliers > 3σ are reported but don't fail the build,
  unless the median itself breaches.
- EC3. Hardware profile mismatch: running `make perf` on a host whose
  detected profile isn't in the budget file fails fast with an actionable
  message ("add a profile tag for `linux-arm64-32gb` and re-run").

### Story 18.2 — Search end-to-end performance

Search is Maktaba's signature feature; it must feel instant. Cover both
cache-cold and cache-warm paths and the FTS + vector fusion overhead.

**Acceptance criteria:**

- AC1. Hybrid search at 100 k segments returns the top-20 fused result set
  in p95 ≤ 500 ms on the reference Mac profile (warm).
- AC2. Query embedding is cached in-process (LRU, 10 k entries); cache
  hit rate ≥ 90 % after warm-up against the test query corpus.
- AC3. The fusion step (RRF or weighted) is allocation-bounded — no
  `O(N)` Go allocations per request beyond the result page.
- AC4. The Pipeline `Embed` gRPC call has a hard 200 ms server-side budget
  per query; on breach the API logs the slow query and continues with FTS
  results only (graceful degradation).

**Test cases:**

- TC1. Load: 200 concurrent search queries against a 100 k-segment
  fixture; throughput ≥ 50 qps with p95 ≤ 500 ms.
- TC2. Cache: replay the same 100 queries twice; second pass p95 must be
  ≥ 30 % faster than first pass and embedding cache must show ≥ 90 % hit
  rate.
- TC3. Degradation: kill the Pipeline service mid-test; subsequent
  searches return FTS-only results within budget and the response payload
  carries a `degraded: true` flag.

**Edge cases:**

- EC1. Empty query — must return 400 with no DB hit.
- EC2. Single-character Arabic query — must still tokenize correctly
  through the FTS5 unicode61 tokenizer and not blow out the result set.
- EC3. RTL + LTR mixed query (e.g., Arabic + a Latin proper noun) —
  highlighting must not re-order tokens; perf budget unchanged.
- EC4. Query of exactly the cache size (10 k unique strings) — LRU
  eviction must not cascade into a stop-the-world; p99 ≤ 1.5 s.

### Story 18.3 — Streaming hot-path performance

Manifest issue, segment serve, and session open are on every video
playback. They must be cheap.

**Acceptance criteria:**

- AC1. `OpenSession` gRPC call (API → Streaming) returns in p95 ≤ 80 ms
  for a previously-probed video.
- AC2. HLS master manifest is generated from cached probe data with zero
  FFmpeg invocations; p95 ≤ 30 ms server-side.
- AC3. Range request hit on a cached HLS segment serves with `Content-
  Length` set, no chunked encoding, and p95 first-byte ≤ 100 ms.
- AC4. The transcode worker pool exposes a `transcode_queue_depth`
  metric; under steady-state direct-play workloads it is 0.

**Test cases:**

- TC1. Open 50 concurrent sessions on the same video; all complete within
  budget and the second-onwards `OpenSession` is faster than the first
  (probe cache hit).
- TC2. Issue 500 segment range requests against a fully-warm cache; all
  succeed within budget with no FFmpeg subprocess spawned.
- TC3. Force a cold transcode by `EvictHashCache` and request a segment;
  first segment p95 ≤ 6 s, subsequent segments fall to warm budget.

**Edge cases:**

- EC1. Client requests a byte range that crosses a segment boundary —
  must serve from two cached segments without re-transcoding.
- EC2. Concurrent identical cold-segment requests — only one FFmpeg
  invocation runs; the others wait on the in-flight result (single-flight).
- EC3. Cache LRU at exact `max_gib`: an in-progress transcode must not
  evict its own output mid-write.

### Story 18.4 — Pipeline throughput targets

Establish the throughput floor for transcription, indexing, and
thumbnailing, and assert them per stage.

**Acceptance criteria:**

- AC1. On the Mac M2 reference profile with `whisper-mlx large-v3`:
  transcription throughput ≥ 4× realtime (i.e., 1 hour of audio in ≤ 15
  minutes) for clean Arabic speech.
- AC2. Indexing (FTS + Chroma upsert) at ≥ 50 segments/s sustained on the
  reference profile.
- AC3. Thumbnail + sprite generation for a 60-minute video completes in
  ≤ 90 s on the reference profile.
- AC4. The pipeline's `pipeline_stage_duration_seconds` histogram exposes
  per-stage p50/p95 and is queried by the perf test harness.

**Test cases:**

- TC1. Per-stage benchmark: feed each stage a sealed fixture (20 min of
  Arabic audio, a 90 min film, a 5-track mkv) and assert the throughput
  floor.
- TC2. End-to-end: a single 60-minute Arabic lecture goes
  DISCOVERED → READY in ≤ 20 minutes wall-clock with one worker pool.
- TC3. Backpressure: enqueue 10 hours of audio with `concurrency.transcribe
  = 1`; the queue drains in ≤ 2.5 hours and no worker exceeds its
  configured timeout.

**Edge cases:**

- EC1. Mixed-language audio (Arabic with English code-switching) —
  throughput target is allowed to drop by 20 %; below that fails.
- EC2. Very short clip (< 30 s) — fixed per-job overhead means realtime
  multiple is meaningless; assert wall-clock < 60 s instead.
- EC3. Failing GPU (MLX init error) — falls back to `faster-whisper` CPU
  with a warning and a relaxed budget; test asserts the fallback path
  completes, not the throughput.

### Story 18.5 — Memory and CPU envelopes

Per-service resident memory and CPU ceilings under steady-state and burst.

**Acceptance criteria:**

- AC1. API service idle RSS ≤ 80 MiB; under 200 qps sustained ≤ 250 MiB;
  no monotonic growth over 24 h soak (slope < 1 MiB/h).
- AC2. Streaming service idle RSS ≤ 100 MiB; with 8 concurrent transcodes
  the parent Go process ≤ 300 MiB (FFmpeg children excluded).
- AC3. Pipeline service idle RSS ≤ 600 MiB (Whisper model loaded); during
  a transcribe burst total worker RSS does not exceed
  `concurrency.transcribe × per-model RSS + 500 MiB` overhead.
- AC4. Goroutine count (Go) and asyncio-task count (Python) are emitted
  as metrics; tests assert no leak after a soak.

**Test cases:**

- TC1. Soak: run a representative workload for 24 h; collect RSS at 1 min
  intervals; linear regression slope < 1 MiB/h per service.
- TC2. Burst: hit each service with 10× steady-state load for 5 minutes;
  RSS returns to within 10 % of steady-state ≤ 60 s after burst ends.
- TC3. Goroutine leak: open and close 1,000 streaming sessions; final
  goroutine count is within 50 of the post-warm-up baseline.

**Edge cases:**

- EC1. CGO heap (Go) is invisible to `runtime.MemStats`; tests must use
  RSS from the OS, not Go runtime numbers.
- EC2. Python multiprocessing workers' RSS is double-counted by `ps`
  shared pages; tests use `smaps_rollup` PSS where available.
- EC3. macOS jetsam pressure: the soak test must not run with the laptop
  asleep (the harness disables App Nap and pins to performance cores).

### Story 18.6 — Client-perceived performance

Time-to-interactive and player-start budgets for the web client; native
apps inherit and are spot-checked.

**Acceptance criteria:**

- AC1. PWA cold load (no service worker cache) — Largest Contentful Paint
  ≤ 2.0 s on a simulated 4G profile, Total Blocking Time ≤ 200 ms.
- AC2. PWA warm load (SW cache hit) — LCP ≤ 600 ms.
- AC3. Time-to-first-frame in the player from "tap play" to first decoded
  frame — p95 ≤ 1.5 s warm, ≤ 3.5 s cold transcode.
- AC4. Search keystroke-to-results latency (with debounced input, 250 ms)
  end-to-end p95 ≤ 750 ms.

**Test cases:**

- TC1. Lighthouse CI runs on the production build; LCP/TBT thresholds
  fail the build on regression.
- TC2. Playwright records `play` → first `timeupdate` event for a warm
  and a cold session and asserts both budgets.
- TC3. Synthetic search: type "بسم الله" character by character with a
  250 ms debounce; assert the request fires once and the response paints
  within budget.

**Edge cases:**

- EC1. RTL paint regressions: Lighthouse runs once with `lang=ar` and
  once with `lang=en`; both must hit budget.
- EC2. Slow video element initialization on Safari (HLS native) vs.
  Vidstack on Chrome — budgets are tracked per browser.
- EC3. Capacitor WebView vs. mobile Safari: the mobile budget is the
  Capacitor measurement, not browser Safari.

### Story 18.7 — Database query performance and N+1 prevention

The DB is the universal bottleneck candidate. Index everything that's hot
and forbid N+1 patterns by test, not by review.

**Acceptance criteria:**

- AC1. Every query in `shared/db/queries/` is covered by an `EXPLAIN
  ANALYZE` snapshot test; any query that becomes a sequential scan on a
  table > 10 k rows fails the test.
- AC2. The hot-path queries (`videos by library`, `segments by video`,
  `jobs claim`) all complete in ≤ 5 ms on the reference fixture.
- AC3. A `db_query_count_total` metric is exported; the perf harness
  asserts a hard cap on per-request DB queries (e.g., `GET /api/videos`
  ≤ 3 queries regardless of result-set size).
- AC4. Postgres + SQLite both pass the same query suite (semantic parity
  test).

**Test cases:**

- TC1. Snapshot: `EXPLAIN ANALYZE` for each named query is captured under
  `tests/explain/`; any change in query plan kind (e.g., index → seq scan)
  fails CI.
- TC2. N+1 detector: a request that fetches 100 videos issues exactly 1
  videos query and 1 batched media-info query, not 100.
- TC3. Cross-engine: every sqlc-generated query has a Python equivalent
  in `pipeline/db/`; both produce the same rows on the test fixture.

**Edge cases:**

- EC1. SQLite lacks `EXPLAIN ANALYZE` — use `EXPLAIN QUERY PLAN` with a
  separate snapshot file.
- EC2. Empty tables: query plans differ on empty vs. populated; snapshots
  are taken against the seeded fixture only.
- EC3. Postgres planner instability: the snapshot stores `using
  index_X | using seq_scan`, not the full plan, to avoid noise.

### Story 18.8 — Cache layout and hit-rate floors

Every cache (HLS segments, embedding, probe, JWKS, FTS prepared
statements) must publish a hit-rate metric, have a configured size, and
be exercised by tests.

**Acceptance criteria:**

- AC1. Each cache exports `*_cache_hits_total`, `*_cache_misses_total`,
  `*_cache_size_bytes` (or entries).
- AC2. Documented hit-rate floors after warm-up:
  HLS segment ≥ 70 %, embedding ≥ 90 %, probe ≥ 99 %, JWKS ≥ 99 %.
- AC3. Each cache has an explicit eviction policy (LRU / TTL / size-bounded
  with single-flight) named in the code and tested.
- AC4. A `maktaba-streaming gc` and an equivalent admin endpoint can
  drop each cache, and the next request fills it correctly.

**Test cases:**

- TC1. Replay a real-shape session log and assert hit-rate floors.
- TC2. Forced eviction: fill HLS cache to `max_gib + 5 %`; LRU evicts
  exactly down to `max_gib × 0.95` and resumes serving.
- TC3. Single-flight: 50 simultaneous misses for the same key spawn 1
  upstream call; all 50 receive the same payload byte-for-byte.

**Edge cases:**

- EC1. JWKS rotation mid-flight: cached public keys are honored until TTL,
  but new keys are picked up within ≤ 5 minutes (configurable).
- EC2. Embedding cache key collision (two different texts hashing to the
  same key, vanishingly rare) — test verifies the cache stores full text,
  not hash, as the key.
- EC3. Probe cache invalidation when a file's `(size, mtime)` changes —
  the next manifest issue must re-probe and overwrite the entry atomically.

---

## Epic 19 — Scalability

**Goal.** Maktaba serves the 30 TB / single-household target on one box
without falling over, and the same code paths scale horizontally to
multi-host deployments without architectural rewrites. Each service has
an explicit scale axis; bottlenecks are detected by load test, not by
production incident.

This epic does not cover *speed* of any single request (that's Epic 18).
It covers *capacity*: how many videos, segments, sessions, and concurrent
users a deployment can hold and serve before the next tier kicks in.

### Story 19.1 — Single-host capacity floor

Establish what "one Mac mini" must handle and assert it.

**Acceptance criteria:**

- AC1. Reference deployment (Mac mini M2, 16 GB RAM, 30 TB external SSD,
  Postgres) sustains:
  - 50,000 videos in the catalog.
  - 1,000,000 transcript segments indexed.
  - 8 concurrent direct-play streaming sessions, or
  - 4 concurrent transcoded streaming sessions.
  - 1 concurrent transcribe + 4 concurrent index workers in the pipeline.
- AC2. The library landing page (first 50 videos, paginated) loads end-to-
  end in p95 ≤ 500 ms with the catalog loaded.
- AC3. The system survives a 30 TB initial scan without exhausting
  memory or running out of FDs (`ulimit -n` ≥ 4096 documented).
- AC4. The capacity floor is asserted by a `make capacity` target that
  loads the seeded fixture, runs the workload mix for 30 minutes, and
  fails on any budget breach (RSS, RPS, error rate ≤ 0.1 %).

**Test cases:**

- TC1. Catalog walk: paginate through all 50 k videos at 50 per page;
  total walk completes in ≤ 5 minutes with stable RSS.
- TC2. Concurrent playback: open 8 direct-play sessions on distinct
  videos, hold for 10 minutes; no buffer underrun event recorded.
- TC3. Mixed workload: 8 streams + 1 active transcribe + 100 search qps;
  p95 search budget from Epic 18 still holds.

**Edge cases:**

- EC1. Backing storage on slow USB (≤ 50 MB/s sequential) — direct play
  ladder is capped at 720p; documented and asserted.
- EC2. macOS file-watch limits: `watchdog` falls back to polling when
  inotify-equivalent FDs exhaust; the test forces the failure and
  verifies the polling fallback still discovers new files within 60 s.
- EC3. SQLite mode: capacity floor is documented at 1/4 of Postgres
  (~12 k videos, ~250 k segments) before write contention degrades.

### Story 19.2 — Horizontal scale-out for the API service

The API is stateless; running N replicas behind any L7 LB must work
without sticky sessions and without losing WebSocket events.

**Acceptance criteria:**

- AC1. Two API replicas behind an LB serve identical responses for the
  same request; cookies and JWTs validate on either replica.
- AC2. A WebSocket client connected to replica A receives events
  triggered on replica B within p95 ≤ 250 ms, fanned out via Postgres
  `LISTEN/NOTIFY`.
- AC3. The NOTIFY payload size is bounded ≤ 8 KiB; events larger than
  that store the payload in a `events` table and notify with the row id
  only.
- AC4. Replica restart (rolling) does not drop in-flight WebSocket
  subscriptions for clients connected to the other replica.

**Test cases:**

- TC1. Round-robin: alternate two replicas for 1,000 requests; all
  succeed; session continuity is preserved.
- TC2. WS fan-out: connect 100 clients across both replicas, fire 1,000
  job-progress events; every client receives every event in order.
- TC3. Rolling restart: bounce replica A while 50 clients are connected
  there; clients reconnect to replica B, missed events are replayed
  from the `events` table by `last_event_id`.

**Edge cases:**

- EC1. Postgres `LISTEN/NOTIFY` queue overflow under burst — the API
  switches to a poll-the-events-table fallback within 5 s of the first
  dropped notification.
- EC2. Clock skew across replicas: any `now()`-using logic uses
  Postgres `now()`, not Go `time.Now()`, for tie-breaking.
- EC3. JWKS rotation across replicas: a key rotated on replica A is
  visible to replica B within ≤ 5 minutes (TTL of the JWKS cache).

### Story 19.3 — Horizontal scale-out for the streaming service

Sessions are pinned to the box that owns the FFmpeg subprocess; LB must
route accordingly. Migration is by clean reopen, not by FFmpeg state
transfer.

**Acceptance criteria:**

- AC1. Two streaming replicas behind a sticky-session LB (consistent
  hash on `session_id`) serve manifests and segments without cross-
  replica cache misses for a single session.
- AC2. `OpenSession` selects the local replica's session store; the
  signed URL embeds the replica's cache origin.
- AC3. If a client's hashed replica is down, the LB reroutes to the next
  replica; the client receives a `session_invalidated` and reopens —
  watch position is preserved (server-side from Postgres).
- AC4. `EvictHashCache` propagates to all replicas via gRPC fan-out.

**Test cases:**

- TC1. Pin: open 100 sessions; verify each session's segment requests
  always hit the same replica.
- TC2. Failover: kill replica A mid-session; client reopens, resumes
  within 5 s, no duplicated segment download by FFmpeg on replica B.
- TC3. Eviction fan-out: trigger `EvictHashCache(content_hash=X)` via
  any replica; both replicas drop X from their LRU within 1 s.

**Edge cases:**

- EC1. Two clients sharing a `session_id` due to a buggy proxy — the
  session store rejects with `409` and the second client gets a fresh
  session.
- EC2. Replica disk fills: LRU stops admitting new segments, returns
  `503` for cold transcodes; the LB drains that replica.
- EC3. Time-skew between replicas affects HLS segment timestamps —
  segments are timestamped from FFmpeg's PTS, not wall-clock.

### Story 19.4 — Horizontal scale-out for the pipeline service

Multiple pipeline workers across hosts coordinate via the Postgres job
queue; GPU stages take per-device locks; CPU stages have per-host caps.

**Acceptance criteria:**

- AC1. N workers (N ≥ 2) on distinct hosts, all pointed at the same
  Postgres and the same shared media volume, drain a 100-job queue in
  ≤ T/N + ε wall-clock (where T is single-host time).
- AC2. `SELECT … FOR UPDATE SKIP LOCKED` ensures every job runs exactly
  once across all workers (verified by output uniqueness on the
  fixture).
- AC3. GPU-bound jobs claim a per-device advisory lock keyed by
  `(host_id, device_id)`; two GPU jobs on the same device serialize.
- AC4. Adding a worker host requires only a config file pointing at the
  shared DB and media volume; no code change.

**Test cases:**

- TC1. Two-host drain: enqueue 60 minutes of audio across 30 jobs; two
  workers (each with 1 GPU) finish in ≈ half the single-host time.
- TC2. Exactly-once: with 4 workers and 1,000 small jobs, the output
  table contains 1,000 unique (job_id, output_hash) rows.
- TC3. Lock contention: pin two GPU jobs to the same device; their
  effective wall-clock is sequential, not parallel.

**Edge cases:**

- EC1. A worker dies mid-job — the heartbeat reaper returns the job to
  `pending` after `heartbeat_sec × 3`; another worker re-claims it and
  resumes from `last_segment_end_sec`.
- EC2. Shared media volume is unreliable (NFS hiccup) — read errors are
  retried with exponential backoff; the job is requeued, not failed,
  after 3 attempts.
- EC3. Workers running mismatched code versions — each job records the
  worker's `version`, `backend`, `model_hash`; a mismatch with the
  library's expected `(backend, model)` fails the job into a
  human-readable retry pile.

### Story 19.5 — Database scaling and failover

Postgres is the single source of truth and the WS bus. Plan its growth
and recovery.

**Acceptance criteria:**

- AC1. The schema sustains 1 M segments + 50 k videos with all queries
  in the perf budget (Epic 18.7).
- AC2. A streaming-replica Postgres can be added with documented setup
  steps (`pg_basebackup` + `recovery.conf`); read-only replica handles
  search queries (eventual consistency tolerated).
- AC3. Daily logical backup (`pg_dump`) runs as a documented systemd /
  launchd job, retains N=14 days, and a one-line restore script is
  tested in CI.
- AC4. Migration safety: `goose up` against a 30 TB-class fixture
  completes in ≤ 60 s; long-running migrations are forbidden by a
  pre-merge lint.

**Test cases:**

- TC1. Restore drill: take a fresh dump, restore into a temp DB, run
  the catalog smoke test against it; all videos and segments round-trip.
- TC2. Read-replica search: configure the API to read from the replica
  with a 5 s lag tolerance; search results match primary within tolerance.
- TC3. Migration size: attempt a `CREATE INDEX` on a 1 M row table; the
  pre-merge lint flags it for `CREATE INDEX CONCURRENTLY`.

**Edge cases:**

- EC1. `pg_dump` on a busy primary slows transcribe writes — the cron
  pins the dump to a low-traffic window and uses `--jobs` for parallelism.
- EC2. Replica falls behind by > 60 s — the API stops routing search to
  it and pages an alert.
- EC3. SQLite path: there is no replica story; backup is a `VACUUM
  INTO` snapshot and the alert about replica lag is a no-op.

### Story 19.6 — Storage scaling and large library handling

Identity is `content_hash`; renames and moves do not re-process. The
scanner must handle a 30 TB tree in bounded memory.

**Acceptance criteria:**

- AC1. Cold scan of a 30 TB tree (≈ 50 k files) completes in ≤ 30
  minutes on the reference profile, bounded RSS ≤ 800 MiB peak.
- AC2. `content_hash` is BLAKE3 of the first + last 4 MiB plus file
  size; correctness verified against a known fixture and adversarial
  inputs (zero-byte file, exactly-8-MiB file, sparse file).
- AC3. Rename / move of 10 % of the library triggers no re-transcribe,
  no re-index, no thumbnail regen.
- AC4. The watcher debounces FS events at 2 s; a copy-then-rename
  sequence (atomic mv) emits one job, not two.

**Test cases:**

- TC1. Cold scan: synthesize 50 k empty-but-uniquely-hashable files;
  assert wall-clock and RSS budgets.
- TC2. Identity stability: rename every file to a new path; verify
  `videos.content_hash` rows are unchanged and no jobs are enqueued.
- TC3. Pathological content: two files with identical first + last 4
  MiB but different middle bytes — content_hash differs because
  size-or-mid sentinel is included; documented and tested.

**Edge cases:**

- EC1. A still-being-written file: the scanner skips files whose
  `mtime` is < 30 s in the past (configurable) to avoid hashing partial
  uploads.
- EC2. SMB mount latency spike: hash computation has a 60 s per-file
  timeout; the file is requeued.
- EC3. File deleted mid-scan: graceful skip with debug log, no error.

### Story 19.7 — Concurrency caps and quotas

Per-host CPU/GPU/concurrency caps protect the box from being overrun.
Quotas are observable and tunable at runtime.

**Acceptance criteria:**

- AC1. Default `transcode.max_concurrent = (num_cores / 4)` with a
  configurable override; new sessions above the cap fall back to direct
  play with a quality cap, or queue with a "starting soon" UI hint.
- AC2. Pipeline `concurrency.transcribe` defaults to 1 per GPU device,
  enforced by an advisory lock; CPU-bound stages use a host-wide
  semaphore.
- AC3. Per-library budget cap (`max_usd_per_month`) for paid STT
  backends is enforced at job-claim time; over-budget jobs return to
  `pending` with `not_before = next month` (per architecture §10.4).
- AC4. All caps are visible in `/api/system/health` and exported as
  metrics.

**Test cases:**

- TC1. Transcode cap: open `max_concurrent + 2` transcoded sessions; the
  last 2 either downgrade to direct play or receive `503` with `Retry-
  After`.
- TC2. GPU lock: enqueue 4 transcribe jobs with 1 GPU; queue depth
  observed to be 3, throughput unchanged.
- TC3. Budget cap: simulate USD usage at 95 % cap; the next API-backed
  transcribe job is held; an in-progress job is not preempted.

**Edge cases:**

- EC1. Cap reduced at runtime below current concurrency — running jobs
  finish; new claims respect the new cap immediately.
- EC2. Hot reload of the budget cap mid-month — the new cap is honored
  without restart.
- EC3. Free-tier STT (local Whisper) — no budget cap applies; tested
  that the cap path is bypassed for `backend.type = local`.

### Story 19.8 — Multi-tenant readiness (deferred capacity)

Single-user is v1; the schema and identity surfaces must not preclude
multi-user later.

**Acceptance criteria:**

- AC1. Every user-scoped row (watch progress, collections-by-user) has
  a `user_id` column; single-user mode uses a sentinel `user_id =
  '00000000-0000-0000-0000-000000000001'` so no schema migration is
  needed to flip on multi-user.
- AC2. The API's auth layer treats single-user mode as "all requests
  authorized as the sentinel user," with a feature flag to require
  real auth.
- AC3. Per-library ACL rows (`library_acl(library_id, user_id, role)`)
  exist; in single-user mode the row is implicit.
- AC4. A migration plan from single-user → multi-user is documented
  and tested by an integration test that flips the flag and asserts
  data continuity.

**Test cases:**

- TC1. Schema audit: every user-bearing table has `user_id NOT NULL`
  with the sentinel as default for v1.
- TC2. Flag flip: enable multi-user mode on a seeded single-user DB;
  log in as the sentinel-mapped account; all collections and watch
  state are visible.
- TC3. ACL: in multi-user mode, a non-owner user cannot list videos in
  another user's library.

**Edge cases:**

- EC1. Pre-existing rows without `user_id` after an external import —
  the migration backfills with the sentinel and logs the count.
- EC2. JWT subject mismatch with a watch-progress row — the read
  succeeds (publicly readable in single-user) but the write is
  rejected.
- EC3. The sentinel UUID conflicting with a real user — forbidden by
  a check constraint; documented.

---

## Epic 20 — Testing

**Goal.** Every layer of Maktaba has a test posture proportional to its
risk. The test pyramid is wide at the bottom (unit), substantial in the
middle (integration with real Postgres, real FFmpeg, fixture media),
focused at the top (a few end-to-end smoke flows). CI runs all three on
every PR; nothing merges red.

This epic defines what the test suite looks like, what it covers, what
fixtures it uses, and how flakes are managed. Specific test cases for
features live in their respective epics; this is the meta-epic for "how
we test."

### Story 20.1 — Test pyramid and runtime budgets

Codify the layers and what each layer is and is not allowed to do.

**Acceptance criteria:**

- AC1. Three layers, in `make test` execution order:
  1. **Unit** — pure, no I/O, no DB, no FFmpeg, no network. Per-package.
  2. **Integration** — real Postgres (testcontainers / `postgres`
     binary), real FFmpeg, real ChromaDB, fixture media. Per-service.
  3. **End-to-end** — full compose stack via Playwright + a headless
     browser; a handful of golden flows.
- AC2. Runtime budgets:
  - Unit: total ≤ 60 s across all services.
  - Integration: total ≤ 8 minutes.
  - E2E: total ≤ 12 minutes.
  - PR CI green-to-merge ≤ 20 minutes wall-clock.
- AC3. Each test is unambiguously tagged (`//go:build unit` /
  `pytest.mark.unit` / `test.unit.spec.ts`); CI runs each tier
  independently.
- AC4. A new test that exceeds its tier's per-test soft cap (unit 100
  ms, integration 5 s, e2e 30 s) emits a warning; > 3× the soft cap
  fails the build.

**Test cases:**

- TC1. Tier compliance: a unit test that opens a network socket fails
  with a clear "unit tests must not do I/O" assertion.
- TC2. Runtime breach: artificially `sleep(2 * unit_soft_cap_ms)` in a
  unit test; build flags it.
- TC3. CI parallelism: three matrix jobs (unit, integration, e2e) run
  in parallel and the slowest is integration.

**Edge cases:**

- EC1. `testcontainers` slow-start on a CI runner — the integration
  tier has a 60 s container-up timeout with a retry; further failure is
  surfaced as a flake category, not a test failure.
- EC2. macOS-only paths (MLX, AVPlayer) — tagged `darwin-only` and
  skipped on Linux CI with a recorded skip reason.
- EC3. Ephemeral `/tmp` cleanup — every integration test owns a
  per-test temp dir under `t.TempDir()` (Go) / `tmp_path` (pytest),
  asserted empty at process exit.

### Story 20.2 — Fixtures and seed data

Tests must run against shared, reproducible fixtures that are small,
royalty-free, and committed to the repo.

**Acceptance criteria:**

- AC1. `shared/fixtures/samples/` contains:
  - 1 short Arabic lecture (~ 60 s, royalty-free or self-recorded).
  - 1 short English clip (~ 60 s).
  - 1 mixed-language clip.
  - 1 multi-track mkv (2 audio, 2 subtitle tracks).
  - 1 4 K HDR sample (download-on-demand, not committed).
  - Each with a known `content_hash`, expected probe output, and (where
    applicable) expected transcript golden file.
- AC2. Total committed fixture size ≤ 50 MiB; larger samples (4 K HDR)
  are downloaded by `make fixtures` from a documented mirror with
  checksum verification.
- AC3. A `seeded_db` fixture (Postgres dump) populates 1 k videos and
  10 k segments for performance / capacity tests; load time ≤ 5 s.
- AC4. Fixtures carry a `LICENSE` file documenting source and rights
  for each sample.

**Test cases:**

- TC1. Determinism: the probe stage on each fixture produces an
  identical JSON byte-for-byte across 10 runs.
- TC2. Size guard: `make fixtures-check` fails CI if any committed file
  > 5 MiB (per file) or total > 50 MiB.
- TC3. Re-download: a corrupted 4 K sample (wrong checksum) is
  re-downloaded automatically; persistent failure aborts with a clear
  message.

**Edge cases:**

- EC1. Sample with no audio track — a transcribe job on it must skip
  cleanly with `state = SKIPPED_NO_AUDIO`.
- EC2. Fixture with a corrupted moov atom — probe must fail with a
  classified error, not a panic.
- EC3. RTL filename with directional characters — must round-trip
  through scan / probe / index / search.

### Story 20.3 — Unit test coverage and conventions

Per-language conventions and a numeric coverage floor for business
logic, not for generated code or boilerplate.

**Acceptance criteria:**

- AC1. Coverage floors (lines): `api/internal/domain` ≥ 85 %,
  `streaming/internal/transcode` ≥ 80 %, `streaming/internal/manifest`
  ≥ 90 %, `pipeline/src/maktaba_pipeline/domain` ≥ 85 %,
  `web/src/lib` ≥ 80 %.
- AC2. Generated code (sqlc, gqlgen, protobuf, GraphQL types) is
  excluded from coverage; the exclude list is checked into
  `.coveragerc` / `coverage.go.yml` / `vitest.config.ts`.
- AC3. Table-driven tests in Go for every public domain function;
  parametrized tests in pytest; each test asserts a single behavior.
- AC4. Mutation testing (`go-mutesting`, `mutmut`, `stryker`) runs
  weekly; surviving mutations on critical paths (auth, hash,
  signed-URL) are fixed within 1 sprint.

**Test cases:**

- TC1. Coverage gate: a PR that drops coverage below floor for a covered
  package fails the build.
- TC2. Negative space: every public function has at least one error-path
  test; CI lints for missing error tests on functions that return errors.
- TC3. Mutation: the weekly mutation report shows ≤ 5 surviving mutations
  on auth code and 0 on hash code.

**Edge cases:**

- EC1. New file with 100 % coverage but only happy-path — the error-
  path lint flags missing negative tests.
- EC2. Generated code accidentally counted — the build prints the file
  list being measured for inspection.
- EC3. Coverage flake from `init()` ordering — tests do not rely on
  `init()` side effects; lint forbids `init()` outside `cmd/`.

### Story 20.4 — Integration tests with real backends

Integration tests use a real Postgres, real FFmpeg, real ChromaDB. No
mocks at service boundaries owned by us.

**Acceptance criteria:**

- AC1. The Go integration suite spins up Postgres via `testcontainers`
  on first test, reuses the container across tests in the same run, and
  truncates between tests with a per-test transaction or savepoint.
- AC2. The Python integration suite spins up Postgres, ChromaDB, and a
  real FFmpeg subprocess against fixture media.
- AC3. gRPC contract tests — the API integration suite stands up a real
  Pipeline gRPC server (or a buffconn in-process variant) against the
  generated stubs from `shared/proto/`.
- AC4. No `gomock` / `unittest.mock` for our own services. Mocks are
  allowed only at external SaaS boundaries (OpenAI Whisper API, etc.)
  and must use `httptest`-style replay tapes recorded once.

**Test cases:**

- TC1. Spin-up: a fresh CI runner brings up Postgres + ChromaDB in ≤ 30
  s; total integration tier completes within budget.
- TC2. Cross-service: enqueue a transcribe job via API, verify the
  Pipeline worker claims it, and observe the WS event on the API side
  — all without manual coordination.
- TC3. Replay tapes: recorded OpenAI responses are deterministic;
  re-recording requires a flag and is gated by code review.

**Edge cases:**

- EC1. CI runner without Docker — fall back to a `pg-embed`-style
  local Postgres binary and a Python-backed ChromaDB process.
- EC2. Postgres version drift between dev and CI — tests pin
  `postgres:16` exactly; mismatch fails the spin-up.
- EC3. FFmpeg version skew — the integration suite probes
  `ffmpeg -version` and fails fast if below the minimum supported
  version (documented).

### Story 20.5 — End-to-end smoke flows

A handful of golden user journeys; broken e2e blocks the merge.

**Acceptance criteria:**

- AC1. Five flows exist as Playwright specs and pass green:
  1. First-run setup (admin token, library creation, media root pick).
  2. Drop a video into the library, see it appear in the grid as
     `processing`, then `ready`, within fixture-bound wall-clock.
  3. Search "بسم الله" returns the seeded segment; click jumps to the
     correct timestamp in the player.
  4. Pause / resume a transcribe job; resume continues from the same
     segment, no duplicate output.
  5. Switch language to Arabic; UI flips to RTL with no layout
     regression (visual diff < 0.5 % pixel delta on the home and
     player screens).
- AC2. E2E suite is dockerized and runnable locally with one command
  (`make e2e`).
- AC3. Each spec records an HTML report and a video trace on failure;
  artifacts uploaded by CI.
- AC4. E2E tests are not allowed to depend on external network beyond
  the compose stack.

**Test cases:**

- TC1. Cold run: each flow passes against a fresh `docker compose up`.
- TC2. Re-runnable: the suite passes when run twice in a row without
  stack restart (idempotency under shared state).
- TC3. RTL diff: the visual diff harness produces ≤ 0.5 % delta on the
  baseline screens; > 0.5 % fails with the diff image attached.

**Edge cases:**

- EC1. Headless Chrome HLS support: tests use `chrome --enable-features
  =NativeHls` or fall back to Vidstack JS playback; both paths exercised.
- EC2. Capacitor-wrapped mobile: e2e on mobile is via Appium spot-checks,
  not Playwright; documented as a separate target.
- EC3. tvOS / Android TV — out of e2e scope for v1; they have their
  own per-platform XCUITest / Espresso suites.

### Story 20.6 — Contract tests for service boundaries

GraphQL, REST, gRPC, and WebSocket schemas are versioned; client and
server are tested against the same contract.

**Acceptance criteria:**

- AC1. `shared/graphql/schema.graphql` and `shared/proto/*.proto` are
  the single source of truth; CI fails if generated code drifts from
  the schema.
- AC2. REST contract is captured in OpenAPI (auto-generated from chi
  routes via reflection or a manual checked-in file); a contract test
  exercises every operationId.
- AC3. WebSocket events have a typed schema (TypeScript discriminated
  unions, Go structs, Python pydantic); a payload that doesn't match
  the schema fails the parser tests.
- AC4. Backwards-compatibility lint: removing a field from the schema
  fails CI; renaming requires a deprecation window of one minor
  version.

**Test cases:**

- TC1. Drift: edit a `.proto` field, regenerate; CI passes only when
  generated code is committed.
- TC2. WS schema: an unknown event type from the server is logged and
  surfaced as a typed `unknown` discriminant on the client, not a
  parse error that crashes the page.
- TC3. Compat: a removal-only PR fails; the same PR with a deprecation
  comment + a migration date passes (the deprecation is enforced at
  the next minor).

**Edge cases:**

- EC1. Generated code re-hash collision: code-gen tools must produce
  byte-stable output; a non-deterministic generator is replaced or
  pinned.
- EC2. OpenAPI auto-gen omits a route — the contract test fails because
  a documented operation is missing.
- EC3. Pydantic strict-mode mismatches — the WS schema parser is
  strict on receive; the sender is lenient.

### Story 20.7 — Performance regression tests in CI

Epic 18 budgets need a CI lane; flakes are managed, not silenced.

**Acceptance criteria:**

- AC1. A `make perf-ci` target runs a reduced perf suite (≤ 5 minutes)
  on every PR against a docker-compose stack on a known runner profile.
- AC2. The full perf suite (`make perf`, 30 minutes) runs nightly on a
  dedicated runner; results are pushed to a time-series store
  (Prometheus pushgateway or simple file).
- AC3. PR perf changes are reported as a comment with deltas vs. main;
  a > 10 % regression on any p95 budget blocks merge.
- AC4. Flake handling: a perf budget that fails 3× in 5 days on main
  is marked unstable and quarantined with an issue auto-filed.

**Test cases:**

- TC1. PR delta: artificially slow a query by 50 ms; the comment shows
  the regression and the merge gate fires.
- TC2. Nightly publish: results are queryable for 30 days; charts are
  rendered in a static dashboard.
- TC3. Quarantine: a flapping budget is automatically tagged and a
  triage issue is filed with logs attached.

**Edge cases:**

- EC1. Runner contention (CI host shared) — perf-ci runs on a tagged
  runner only; if no tagged runner is available, the job queues
  rather than running on a busy one.
- EC2. Cold-start vs. warm-cache — the perf-ci suite is explicitly
  warm-only; a separate weekly cold suite exists.
- EC3. Cross-OS variance — perf-ci reports per-OS budgets and never
  averages across runners.

### Story 20.8 — Flaky test policy

Flakes are a debt category; the policy must enforce repair, not
retries-as-default.

**Acceptance criteria:**

- AC1. A test that fails on a `main` build but passes on retry is
  recorded in a flake registry with the failure log.
- AC2. A test with ≥ 3 recorded flakes in a 7-day window is auto-skipped
  (`t.Skip(reason="quarantined-flake-#issue")`) and a P2 issue is filed.
- AC3. Quarantined tests have a 2-week SLA to fix or delete; SLA breach
  pages the test owner.
- AC4. Retry-on-fail (`--rerun-failed=1`) is allowed only in CI and
  only at the e2e tier; unit and integration tiers fail on first
  failure.

**Test cases:**

- TC1. Synthetic: introduce a 5 % flake; the registry records 3
  failures in 7 days; the test is auto-skipped and an issue is filed.
- TC2. SLA breach: an open quarantine issue past 14 days fires a
  notification.
- TC3. Retry policy: a unit test failure is not retried; an e2e
  failure may retry once.

**Edge cases:**

- EC1. Genuine intermittent infra issue (DNS hiccup) — separate
  classification; not counted toward flake budget.
- EC2. Time-zone-dependent test — banned by lint; tests use injected
  clock.
- EC3. Order-dependent test — banned by running the suite in randomized
  order in CI; failures expose the dependency.

---

## Epic 21 — Observability

**Goal.** A self-hoster can answer "what's it doing?" and "why is it
slow?" without attaching a debugger. Every request, job, and stream is
traceable end-to-end. Health is reportable at a glance. Alerting is
optional but supported. Personal data and secrets never appear in logs
or traces.

This epic does not specify a monitoring vendor; it specifies the
*surfaces* (metrics, logs, traces, health) and the cardinality, format,
and retention rules that work with Prometheus, OpenTelemetry, or a
plain text-log fallback.

### Story 21.1 — Structured logging

One global logger per service; all logs are structured key-value, not
free-form strings. Logs are the lowest-friction observability surface
and the one a self-hoster always has.

**Acceptance criteria:**

- AC1. Go services use `slog` with JSON handler in production and a
  human handler in dev; Python uses `structlog` with the same JSON
  format in production. TypeScript browser logs use a thin `logger`
  with the same field names.
- AC2. Every log line includes: `ts` (RFC 3339 UTC), `level`, `service`,
  `msg`, and (where applicable) `request_id`, `session_id`, `job_id`,
  `video_id`, `user_id`. No log lines without `service`.
- AC3. Log levels: `debug` (off in prod), `info` (default), `warn`
  (recoverable issue), `error` (operation failed), `fatal` (process
  exits). Level is configurable at runtime via signal (Go: `SIGUSR1`
  cycles level) or admin endpoint.
- AC4. No string concatenation of user data into the `msg` field;
  user-controlled strings go in their own fields with an explicit name.

**Test cases:**

- TC1. Schema check: a CI lint parses every log call site; a call with
  a non-fielded user-controlled value (e.g., `slog.Info("user " + name)`)
  fails the build.
- TC2. Round-trip: every emitted JSON line round-trips through `jq` and
  contains the required fields.
- TC3. Hot-reload: `SIGUSR1` to the API process toggles between info
  and debug; observed in subsequent log lines.

**Edge cases:**

- EC1. Log line > 64 KiB — truncated to 60 KiB with a `truncated: true`
  field; large bodies (full HTTP requests) go to the trace, not the log.
- EC2. Logs from FFmpeg subprocess (stderr) — wrapped in
  `event=ffmpeg_stderr` lines, not passed through unstructured.
- EC3. Unicode bidi text in `msg` — the JSON escaper handles RTL
  characters without garbling.

### Story 21.2 — Metrics surface

Prometheus-compatible metrics per service, with strict cardinality and
clear semantics.

**Acceptance criteria:**

- AC1. Each service exposes `/metrics` (default port 9100, 9101, 9102)
  with the following baseline:
  - `*_request_duration_seconds` histogram with labels `method`,
    `route_template` (never raw path), `status_class`.
  - `*_in_flight_requests` gauge.
  - `db_query_duration_seconds` histogram with `query_name` label.
  - `cache_hits_total`, `cache_misses_total` per cache.
  - `pipeline_jobs_total` counter with `stage`, `result` labels.
  - `transcode_active_sessions`, `transcode_queue_depth` gauges
    (streaming).
  - `pipeline_stage_duration_seconds` histogram per stage.
- AC2. Label cardinality is bounded: never include `video_id`,
  `user_id`, or anything per-row in a label. A static lint enforces
  this against the metric registration code.
- AC3. Histograms use exponential native buckets (Prometheus 2.40+) or
  fall back to a documented fixed bucket layout `[1ms, 2.5ms, 5ms, 10,
  25, 50, 100, 250, 500, 1000, 2500, 5000, 10000]` ms.
- AC4. `/metrics` is unauthenticated by default but bound to localhost;
  an admin can opt into network exposure via config and then it requires
  the admin token.

**Test cases:**

- TC1. Cardinality: a lint that scans metric registrations rejects a
  label named `id`, `path`, or anything containing user-supplied data.
- TC2. Schema test: every documented metric is present in `/metrics`
  output on a freshly-started service.
- TC3. Network exposure: with the default config, `/metrics` is
  reachable from `127.0.0.1` only; with the opt-in flag, it requires
  bearer auth.

**Edge cases:**

- EC1. Long-running FFmpeg subprocess on a dropped client — its
  contribution to `transcode_active_sessions` decrements only when the
  reaper claims the session, not when the parent goroutine exits.
- EC2. Restart resets counters — Prometheus handles this correctly with
  `rate()`; documentation calls it out for naive consumers.
- EC3. Web client metrics — the browser doesn't push metrics; instead
  it sends an opt-in `/api/telemetry/web-vitals` POST capped at 1
  request per 5 minutes per session.

### Story 21.3 — Distributed tracing

OpenTelemetry traces span the full request path: client → API → gRPC →
Pipeline / Streaming → Postgres / FFmpeg / ChromaDB.

**Acceptance criteria:**

- AC1. Each service is wired with `otelhttp` (Go) /
  `opentelemetry-instrumentation` (Python) and propagates W3C
  `traceparent` headers across REST, GraphQL, gRPC, and Postgres.
- AC2. Tracing is sampled: 100 % for `error`-tagged spans, 100 % for
  any request that took > p95 budget, and 1 % otherwise. The sampler
  is a head sampler with tail-sampling-aware tags; no separate tail
  sampler required for v1.
- AC3. The web client emits a span per top-level page load and per
  search query, with the `traceparent` carried into the API call so
  the trace covers client + server.
- AC4. Tracing is opt-in: it is disabled by default; enabled via
  `[telemetry].otlp_endpoint` in the service config; never silently
  exfiltrates data.

**Test cases:**

- TC1. End-to-end: a search request from the web client produces a
  trace containing spans from `web → api → pipeline.Embed → postgres
  query → chroma query`.
- TC2. Sampling: 1,000 fast, successful requests produce ≈ 10 traces; a
  single slow request produces 1 trace tagged `slow=true`.
- TC3. Disabled-by-default: a fresh install produces no OTLP
  connections; `netstat`/`lsof` confirms no outbound calls.

**Edge cases:**

- EC1. OTLP endpoint unreachable — exporter buffers ≤ 10 MiB then
  drops with a `warn`-level log, never blocks the request path.
- EC2. Large request body — body content is never put in span
  attributes; only sizes and counts.
- EC3. PII in URLs (search query string with a personal name) — the
  query string is hashed in the span attribute, not stored verbatim.

### Story 21.4 — Health and readiness probes

Every service has `/healthz` (liveness) and `/readyz` (readiness) and a
unified `/api/system/health` aggregating across services for the UI.

**Acceptance criteria:**

- AC1. `/healthz` returns 200 when the process is alive; never blocks
  on dependencies. Used by the orchestrator (compose / launchd /
  systemd) to restart hung processes.
- AC2. `/readyz` returns 200 only when:
  - DB connection pool has ≥ 1 healthy conn.
  - Required gRPC peers (API → Pipeline; API → Streaming) are reachable.
  - Configured caches have been warmed (or are explicitly degraded).
  Returns 503 with a JSON body listing failing dependencies.
- AC3. `/api/system/health` aggregates all three services'
  `/readyz` plus disk free, queue depth, transcribe budget remaining,
  and renders a JSON the web admin panel can display.
- AC4. Probes are unauthenticated, bound to a separate admin port by
  default (or fronted by a reverse proxy with allowlist).

**Test cases:**

- TC1. Liveness: kill Postgres; `/healthz` still returns 200,
  `/readyz` returns 503.
- TC2. Aggregator: with Pipeline down, `/api/system/health` returns
  `degraded` with `pipeline.reason="grpc_unavailable"`.
- TC3. Cold start: during the first 30 s after start, `/readyz` may
  return 503 with `reason="warming"`; the probe never deadlocks.

**Edge cases:**

- EC1. DB primary failover — `/readyz` flips 503 → 200 within 30 s as
  the connection pool reconnects.
- EC2. Streaming with all replicas down — `/api/system/health` shows
  `streaming.unavailable` and the UI surfaces "playback offline" while
  search and library still work.
- EC3. SQLite mode: the Postgres-specific dependencies are absent and
  `/readyz` doesn't check them; documented per-mode probe matrix.

### Story 21.5 — Error reporting and alerting integration

A self-hoster opting into alerting must get accurate signals. We do not
auto-page; we expose hooks.

**Acceptance criteria:**

- AC1. Every `error`-level log emits a structured `error_id` (UUIDv7),
  the stack trace (Go: `errors.WithStack` or runtime stack; Python:
  `traceback`), and a `category` (auth, db, ffmpeg, network, ml,
  unknown).
- AC2. A built-in webhook posts a redacted error summary to a
  configurable URL (Slack, Discord, generic webhook); rate-limited to
  10/min with an exponential-backoff suppress window.
- AC3. Sentry / Honeycomb / GlitchTip integration is opt-in via config;
  DSN is read from env, never logged.
- AC4. Errors crossing service boundaries carry their `error_id` so the
  upstream and downstream logs can be correlated.

**Test cases:**

- TC1. Burst suppression: emit 1,000 errors in 5 s; the webhook
  receives at most 10 with a "910 suppressed" summary appended.
- TC2. Cross-service correlation: a Pipeline error during transcribe
  surfaces with the same `error_id` in the API job-status row and the
  client-visible error.
- TC3. Redaction: the webhook payload omits paths, file names, and any
  field tagged `sensitive=true`.

**Edge cases:**

- EC1. Webhook endpoint flapping (502 on every other call) — circuit
  breaker opens after 5 consecutive failures; opens close after 60 s.
- EC2. Sentry DSN typo — the SDK does not crash the app; it logs a
  one-time warning.
- EC3. Errors during shutdown — flushed to the webhook with a 5 s
  drain budget, then dropped to local file (`error_drop_log`).

### Story 21.6 — Audit log for sensitive actions

A non-rotated, append-only log of security-relevant events. Distinct
from the operational log.

**Acceptance criteria:**

- AC1. Audit events are written to a dedicated `audit_log` Postgres
  table (and a mirrored file `/var/maktaba/audit/audit.log` for
  out-of-DB recovery).
- AC2. Events recorded:
  - login success / failure (with `user_id`, `ip`, `ua`).
  - admin token use.
  - JWT key rotation.
  - settings change (key + old/new value hashes, never plaintext for
    secrets).
  - library root added/removed.
  - data export / bulk delete actions.
- AC3. Audit rows are append-only enforced by a `BEFORE UPDATE OR
  DELETE` trigger raising an exception.
- AC4. Audit retention default 1 year; configurable; explicit delete
  requires a documented admin CLI command that itself emits an audit
  row.

**Test cases:**

- TC1. Append-only: an attempted `UPDATE audit_log SET ...` fails with
  the trigger's exception.
- TC2. Login flow: failed login attempts are logged with the offered
  username and the source IP, not the offered password (never).
- TC3. Retention: rows older than the retention window are removed by
  a scheduled task; the deletion itself is audited.

**Edge cases:**

- EC1. DB unreachable during a sensitive action — the file mirror
  captures the event; on next DB connection, the mirror is replayed
  into the table.
- EC2. Clock skew on file mirror — events use `now()` from the DB when
  available, falling back to wall-clock with a `clock_source` field.
- EC3. Audit for the audit reader — every `SELECT` against `audit_log`
  through the API is itself audited (read-audit), bounded by the
  audit reader role.

### Story 21.7 — Job and pipeline visibility

Self-hosters need to see "what is the pipeline doing right now" without
SSH'ing in.

**Acceptance criteria:**

- AC1. A `GET /api/processing/status` returns counts by `stage × state
  × library`, queue depth, oldest pending job age, in-progress job
  count, average wall-clock per stage, last 50 errors.
- AC2. Per-job `GET /api/processing/jobs/{id}` returns the full state
  machine history, segment-by-segment progress for transcribe, and the
  last heartbeat timestamp.
- AC3. A live `WS /ws/processing/{id}` streams per-segment progress
  updates with ≤ 1 s end-to-end latency from segment commit to client
  paint.
- AC4. The admin panel page (web) renders the above as charts
  (queue-depth time series, throughput by stage) and a sortable job
  list with filter chips.

**Test cases:**

- TC1. Snapshot: with 100 jobs across all stages, the status endpoint
  returns counts that match a hand-counted SQL query.
- TC2. Live progress: drop a 60 s clip; the WS stream emits ≥ 6
  progress events (one per ~10 s segment) before READY.
- TC3. Errored job: a transcribe with an unreadable file appears in
  `last_errors` with `error_id`, `category=ffmpeg`, and a link to the
  full job page.

**Edge cases:**

- EC1. Long-stalled job (heartbeat > 3× interval) is shown as
  `state=stuck` in the UI even though the DB still says `running`.
- EC2. WS subscriber count > 100 on a single job — fan-out batches
  per-second to avoid amplification.
- EC3. Privileged data (full file path) — masked to a per-library
  relative path for non-admin users.

### Story 21.8 — Privacy of telemetry

Every observability surface protects user data; opt-in is real.

**Acceptance criteria:**

- AC1. No telemetry leaves the host by default. Tracing, error
  webhooks, and external integrations are all explicitly opt-in via
  config, with a top-level `[telemetry].outbound_enabled = false`
  master switch.
- AC2. A canonical "redaction list" enumerates field types that are
  never logged, traced, or webhooked: passwords, API keys, JWT
  bearer values, file contents, full filesystem paths, transcript
  body text. Enforced by a CI lint over log/trace call sites.
- AC3. Logs include a "telemetry-leak detector" test that scans 1,000
  representative log lines for known-sensitive substrings (a list of
  test secrets) and fails on any match.
- AC4. The web client's optional `/api/telemetry/web-vitals` is
  off-by-default and labeled clearly in the privacy section of the
  UI.

**Test cases:**

- TC1. Default-off: a fresh install with default config makes no
  outbound DNS queries beyond NTP and TLS for the configured public
  origin (verified by a packet-capture integration test).
- TC2. Leak scan: a deliberate `slog.Info("password=" + p)` is caught
  by the redaction lint and fails CI.
- TC3. Redaction at runtime: a misbehaving call site that escapes the
  lint is caught at runtime by a structured-log middleware that
  rewrites known-sensitive keys to `***`.

**Edge cases:**

- EC1. Config-stored secret echoed back through `/api/settings` —
  forbidden; setting endpoints return only metadata, never the value.
- EC2. Stack trace containing a user file path — paths under the
  media root are masked to `<media>/<library>/<relative>` before
  emission.
- EC3. Browser console in a developer-mode build — verbose logs are
  off in production builds; a flag is required to opt in.

---

## Epic 22 — DevOps and Delivery

**Goal.** A self-hoster gets running in one command and stays running
across upgrades. Maintainers ship releases predictably without manual
steps. CI proves every change before it lands; CD makes shipping an
already-validated build a one-liner.

This epic covers CI, build, packaging, release, install, upgrade, and
rollback. It does **not** cover security hardening (Epic 23) or
operational observability (Epic 21).

### Story 22.1 — Continuous integration pipeline

Every commit to a branch and every PR gets the same gated checks. CI
is the merge gate.

**Acceptance criteria:**

- AC1. The CI workflow has six gates, run in parallel where possible:
  1. `lint` — golangci-lint, ruff, eslint, tsc --noEmit, prettier
     check, gofmt check, mypy strict on pipeline.
  2. `unit` — Epic 20.1 unit tier across all services.
  3. `integration` — Epic 20.4 integration tier with Postgres + ChromaDB
     containers.
  4. `e2e` — Epic 20.5 against a docker-compose stack.
  5. `perf-ci` — Epic 20.7 reduced perf suite.
  6. `build-artifacts` — produces every binary, container image, and
     web bundle as artifacts.
- AC2. PR merge requires all six green; force-merge requires a recorded
  override with a documented reason in the PR body.
- AC3. CI runs on three OS / arch combos for the build gate: linux/
  amd64, linux/arm64, darwin/arm64. Test gates run on linux/amd64
  with darwin/arm64 spot-checks for darwin-only paths.
- AC4. Total wall-clock for a green PR ≤ 20 minutes (Epic 20.1).

**Test cases:**

- TC1. Gate independence: each gate fails for its own reason with no
  spillover; a `lint` failure does not also report `unit` as failed.
- TC2. Cross-platform build: a Go change that breaks linux/arm64 fails
  the `build-artifacts` gate visibly with the offending arch named.
- TC3. Override: a force-merge without the required PR body section is
  refused by a branch protection rule.

**Edge cases:**

- EC1. Flaky CI runner — the `flake` quarantine policy from Epic 20.8
  applies; retries are not a substitute for a fix.
- EC2. PR from a fork — secrets are unavailable; `e2e` and `perf-ci`
  skip with a clear "needs maintainer rerun" comment.
- EC3. PR touches only docs — non-doc gates skip with a labeled "docs-
  only" status.

### Story 22.2 — Reproducible builds and artifacts

Every build is byte-stable given the same inputs. Artifacts are signed.

**Acceptance criteria:**

- AC1. Go binaries built with `-trimpath -ldflags='-buildid='` and
  vendored deps; `sha256` of the resulting binaries is stable across
  two builds on the same OS/arch with the same Go version.
- AC2. Container images use a deterministic builder (`ko`, `kaniko`,
  or `docker buildx --provenance`) and pinned base images by digest.
- AC3. Python pipeline ships as a pinned `uv` lockfile; `uv lock`
  drift fails CI.
- AC4. Web bundle uses pinned `pnpm` lockfile; `vite build` produces
  byte-stable output (sorted file order, deterministic hash).
- AC5. All release artifacts (binaries, images, web tarball, mobile/
  desktop installers) are signed: cosign for images, minisign for
  binaries; signatures published alongside the artifacts.

**Test cases:**

- TC1. Reproducibility: build twice on the same runner; sha256 sums
  match for every Go binary and the web bundle.
- TC2. Signature verification: `cosign verify` and `minisign -V`
  succeed against the published signatures and the maintainer public
  key.
- TC3. Lockfile drift: a deliberate edit to `pyproject.toml` without
  re-running `uv lock` fails CI.

**Edge cases:**

- EC1. Python C-extension wheels — built via `cibuildwheel` and
  reproducible per-platform; documented per-platform stability.
- EC2. Native iOS/Android signing — release builds require maintainer-
  held signing material; CI uses dev signing only.
- EC3. Reproducibility under different timezones / locales — builds
  pin `SOURCE_DATE_EPOCH`, `TZ=UTC`, `LANG=C.UTF-8`.

### Story 22.3 — Container images and compose stack

The canonical self-host path is `docker compose up`. Compose must work
on Mac and Linux without local dependencies beyond Docker.

**Acceptance criteria:**

- AC1. Four images published per release: `maktaba/api`,
  `maktaba/streaming`, `maktaba/pipeline`, `maktaba/web` (built once,
  served by Caddy in prod).
- AC2. `deploy/compose/docker-compose.yml` boots the full stack
  (Postgres + Caddy + the four services) on a fresh host with one
  `docker compose up -d` command.
- AC3. `docker-compose.mac.yml` overlay bind-mounts host FFmpeg and
  exposes Apple Neural Engine to the Pipeline container. A "doctor"
  one-liner verifies the bind worked.
- AC4. Image sizes ≤ targets: api ≤ 60 MiB, streaming ≤ 80 MiB
  (FFmpeg static excluded), pipeline ≤ 1.2 GiB (Whisper + Chroma),
  web ≤ 30 MiB.

**Test cases:**

- TC1. Cold boot: `docker compose up -d` on a CI runner brings the
  stack to "all healthy" within ≤ 90 s.
- TC2. Mac overlay: on darwin/arm64, the compose-mac overlay produces
  a Pipeline container that successfully invokes MLX (verified by
  `maktaba-pipeline doctor`).
- TC3. Image size guard: a build that pushes any image past its size
  budget fails CI with the size delta.

**Edge cases:**

- EC1. Docker Desktop's file-system performance on Mac — bind mounts
  use `:cached` consistency for the media volume; documented.
- EC2. SELinux on Linux — bind mounts include `:Z` where required;
  documented and tested.
- EC3. Rootless Docker — compose works rootless; user-namespace
  remapping for the media volume is documented.

### Story 22.4 — Database migrations

Schema is owned by `goose` migrations under `shared/db/migrations/`.
Migrations are forward-only at the bytes level; rollback is a manual,
documented operation.

**Acceptance criteria:**

- AC1. Migrations are append-only: edits to a previously-merged
  migration are forbidden by a CI lint comparing against the main
  branch.
- AC2. `maktaba-api migrate up` runs at boot by default behind a
  feature flag; a separate `--migrate-only` flag runs migrations and
  exits, used in deployments where boot-time migration is undesirable.
- AC3. Every migration has an idempotency guard (`IF NOT EXISTS`,
  `IF EXISTS`) so re-running is a no-op.
- AC4. Long-running DDL is forbidden in v1: a CI lint flags
  unsupported patterns (`CREATE INDEX` without `CONCURRENTLY` on
  Postgres, table rewrites without batched migration plan).
- AC5. SQLite parity: each migration ships a `.sqlite.sql` variant
  reviewed and tested.

**Test cases:**

- TC1. Append-only: editing the SQL of a merged migration fails CI.
- TC2. Idempotent: run `migrate up` twice; second run is a no-op
  (no DDL emitted, no errors).
- TC3. Long-running guard: a deliberate `CREATE INDEX` (without
  CONCURRENTLY) on a > 10 k row table fails the lint with a fix-it
  hint.

**Edge cases:**

- EC1. Down migrations exist in the repo but are unsupported in
  production paths; documented as "for local dev only."
- EC2. Migration that requires a backfill — pattern: ship the DDL,
  run a separate idempotent backfill job tracked in `processing_jobs`,
  flip the read path. The pattern is documented and tested.
- EC3. SQLite missing a Postgres-only feature (e.g., partial indexes)
  — the SQLite variant uses a fallback (full index + filter); parity
  test asserts query results are identical.

### Story 22.5 — Release management and versioning

SemVer for the platform; releases are tagged, changelogged, and built
from a green main.

**Acceptance criteria:**

- AC1. Versions follow `MAJOR.MINOR.PATCH`; the platform's "version"
  spans api + streaming + pipeline + web in lockstep. App-store
  releases for mobile/desktop/TV are tagged separately but pinned to
  a platform version.
- AC2. A release is a Git tag `v{MAJOR}.{MINOR}.{PATCH}` on a green
  main commit; the release workflow rebuilds artifacts from that tag
  (no "release the artifacts CI happened to produce" path).
- AC3. CHANGELOG.md follows Keep-a-Changelog; CI fails a PR that adds
  user-visible behavior without a changelog entry.
- AC4. A `maktaba --version` and `GET /api/system/version` both return
  semver + git-sha + build-time; consistent across the four backend
  services.

**Test cases:**

- TC1. Version surface: a fresh build's `--version` matches the tag
  and the embedded git sha; mismatch fails the release workflow.
- TC2. Changelog gate: a feature PR without a CHANGELOG line fails
  CI; a docs-only PR is exempt.
- TC3. Tag → artifact lineage: the published image's OCI label
  `org.opencontainers.image.revision` matches the source tag commit.

**Edge cases:**

- EC1. Hotfix release on an old minor — branch from the tag, cherry-
  pick, tag a new patch; CI handles the release branch identically.
- EC2. Pre-release identifiers (`v1.2.0-rc.1`) — produced by the same
  workflow with a `-rc` channel tag; consumers explicitly opt in.
- EC3. Mobile app version vs. platform — `apps/mobile/capacitor.config.ts`
  embeds a `compatibleApiVersion` range; a mismatch refuses to
  connect with a clear UI message.

### Story 22.6 — Upgrade and rollback

Self-hosters must be able to upgrade and roll back without losing data.

**Acceptance criteria:**

- AC1. Upgrade path on the canonical compose deployment is `git pull;
  docker compose pull; docker compose up -d`; the platform supports
  rolling each service independently when its API surface is
  back-compatible.
- AC2. Rollback path within one minor version is documented and
  tested: pull the previous tag, `docker compose up -d`. Migrations
  are forward-compatible across one minor (the new minor reads old
  rows; the old minor reads new rows that don't use new columns).
- AC3. A pre-upgrade `maktaba-api migrate doctor` runs the planned
  migrations against a pg_dump in a temp DB and prints a duration
  estimate before touching production data.
- AC4. Long-running migrations (> 30 s) require an explicit operator
  ack via `--accept-long-migration`.

**Test cases:**

- TC1. Forward+back: upgrade a seeded fixture from v1.0 → v1.1 →
  rollback to v1.0; data is intact, the app boots, no
  `migrate-down` was needed.
- TC2. Doctor: with a synthetic 1 M row migration, doctor reports
  the duration; without ack, upgrade refuses.
- TC3. Rolling: bump streaming alone to a new patch version while
  api and pipeline stay; clients connected during the rolling
  restart drop fewer than 1 % of in-flight streams.

**Edge cases:**

- EC1. Two-minor jump (v1.0 → v1.2) — supported only via v1.0 → v1.1
  → v1.2; documented and tested. A direct jump fails fast with a
  clear error.
- EC2. Custom config path — upgrades preserve config; a
  configuration-schema bump is handled by a `config migrate` step in
  the doctor.
- EC3. Postgres major upgrade — out of scope for Maktaba's upgrade
  path; documented separately as a Postgres operator task.

### Story 22.7 — Multi-platform packaging

Beyond compose, native packages for the user's platform.

**Acceptance criteria:**

- AC1. **macOS (Homebrew tap):** `brew install maktaba/tap/maktaba`
  installs the three native binaries and a `uv`-managed Pipeline
  venv, drops three `launchd` plists, and starts them.
- AC2. **Linux (Debian / RPM):** packages ship for current Debian /
  Ubuntu / Fedora; they install a `systemd` unit per service and
  create the `maktaba` user.
- AC3. **Mobile (iOS / Android):** Capacitor-built apps published to
  the App Store and Play Store, signed and versioned per Story 22.5;
  builds gated on a minimum platform version.
- AC4. **Desktop (mac/Win/Linux):** Tauri-built installers (`.dmg`,
  `.msi`, `.AppImage`) signed and notarized where required; auto-
  update is opt-in.
- AC5. **TV apps:** XCode and Gradle builds for tvOS / Android TV
  produce signed packages; published manually for v1.

**Test cases:**

- TC1. Homebrew end-to-end: a CI job on a clean macOS runner installs
  via the tap, brings the three plists up, and passes the smoke
  test.
- TC2. Debian end-to-end: a CI job installs the .deb on a fresh
  Ubuntu LTS runner, starts via systemd, runs smoke.
- TC3. Mobile build: Capacitor sync produces an .ipa and .apk that
  open against a mock backend; smoke test exercises login + library.

**Edge cases:**

- EC1. Homebrew tap with the user's existing Postgres — the formula
  detects and uses it; a clean-room install creates a Postgres role
  and DB.
- EC2. Linux distro without `ffmpeg` ≥ minimum — package declares
  the dep; install fails with a clear message rather than silently
  shipping a broken Pipeline.
- EC3. Auto-update for desktop on Linux AppImage — uses the
  AppImage updater; the user can disable.

### Story 22.8 — Local developer workflow

Day-1 contributor must be able to make a change, see it live, and run
tests inside ≤ 30 minutes.

**Acceptance criteria:**

- AC1. `make dev` brings up the full stack with live-reload mounts
  for all four services; saving a `.go`, `.py`, or `.tsx` file shows
  the change in ≤ 5 s.
- AC2. `make test` (Epic 20.1 entry point) runs without external
  network and without sudo on a dev laptop.
- AC3. A `CONTRIBUTING.md` gives the canonical workflow; CI runs
  the same exact `make` targets a developer runs locally — no
  divergent CI-only scripts.
- AC4. `pre-commit` config is checked in; running it covers the lint
  gate's quick checks.

**Test cases:**

- TC1. Cold dev start: a fresh clone + `make dev` boots in ≤ 5
  minutes the first time, ≤ 90 s warm.
- TC2. Live-reload latency: edit a Go file in `api/`, save, refresh
  the browser; the change is visible within 5 s.
- TC3. Parity: `make lint` locally and `make lint` in CI produce the
  same set of pass/fail outcomes on a dirty fixture branch.

**Edge cases:**

- EC1. Apple Silicon vs. Intel Mac — both paths are tested; doc
  notes which features (MLX) require Apple Silicon.
- EC2. Slow corporate proxy — `make dev` resolves images from a
  configurable mirror; documented.
- EC3. Pre-commit hooks bypassed (`git commit --no-verify`) — CI
  catches the missed checks; merge gate enforces.

---

## Epic 23 — Security

**Goal.** Maktaba is safe to expose on a home LAN by default and safe to
expose to the internet with the documented production hardening. No
secret leaves the host that wasn't authorized to. Users authenticate
once and stay authenticated across the device fleet. The supply chain
is auditable.

This epic addresses authentication, authorization, transport, secrets,
content safety, and supply-chain integrity. It composes with Epic 21
(audit log) and Epic 22 (signed artifacts).

### Story 23.1 — Authentication

Two surfaces (web cookies, mobile/TV bearer tokens), one identity
table, modern password hashing, and rotation-friendly JWT signing.

**Acceptance criteria:**

- AC1. Passwords hashed with `argon2id`, parameters configurable but
  default to RFC 9106 second recommendation (`memory=65536KiB,
  iterations=3, parallelism=1`); rehash on login when params change.
- AC2. Web flow: login sets `httpOnly Secure SameSite=lax` session
  cookie; CSRF tokens required for any state-changing request and
  validated against the session.
- AC3. Native flow: login returns short-lived bearer JWT (RS256, 15
  min) + opaque refresh token (30 d). Refresh tokens are stored
  hashed in DB; rotation revokes the previous refresh.
- AC4. JWKS published at `/api/.well-known/jwks.json`; key rotation
  rolls every 90 days with a 30-day overlap; the streaming service
  caches JWKS for ≤ 5 min.
- AC5. Single-user mode: `MAKTABA_ADMIN_TOKEN` env-supplied bearer
  bypasses the user table entirely; the UI stores it after first boot.
  This path is feature-flagged off when `auth.multi_user = true`.

**Test cases:**

- TC1. Hashing: a known password produces a hash that verifies; an
  argon2id param bump on login transparently re-hashes and stores.
- TC2. Token rotation: refresh once, the previous refresh is invalid;
  reusing it returns 401 and the family is revoked (refresh-token
  reuse detection).
- TC3. JWKS rollover: rotate the signing key; existing access tokens
  continue to validate until expiry; new tokens are signed by the
  new key; streaming validates both during the overlap.

**Edge cases:**

- EC1. Clock skew between API and Streaming — JWT validation has a 30
  s `nbf` / `exp` skew tolerance; documented.
- EC2. Lost refresh token — user logs in fresh; the lost token's
  family is revoked on next attempted use.
- EC3. Admin token leaked — there is exactly one admin token; rotating
  it requires an env change and a service restart; documented in the
  ops guide.

### Story 23.2 — Authorization and ACLs

Library-level and action-level authorization at every API entry point.
"Single-user" is a special case of multi-user with one user.

**Acceptance criteria:**

- AC1. Every authenticated REST/GraphQL handler runs through a single
  `authorize(action, resource)` call; missing the call fails a CI
  lint that scans handler functions.
- AC2. ACL roles: `admin`, `editor` (can ingest, edit metadata),
  `viewer` (read + watch only). Per-library ACL row defaults to
  `admin` for the library creator.
- AC3. Streaming validates JWT signature and checks library
  membership against the JWT's `library_ids` claim before issuing a
  segment; expired JWTs are rejected and produce a clear `403`
  (not a 401, since the claim was valid at signing).
- AC4. Admin-only routes (`/api/system/*`, `/api/auth/users`) require
  `admin`; tested per route.

**Test cases:**

- TC1. Lint: a new handler missing `authorize()` fails CI.
- TC2. Cross-tenant: a `viewer` on library A cannot read or stream a
  video in library B; both REST and gRPC paths covered.
- TC3. Privilege escalation: a `viewer` cannot promote themselves;
  `editor` cannot promote anyone; `admin` can.

**Edge cases:**

- EC1. JWT contains a `library_ids` claim that the user has since
  lost — the next manifest refresh detects the change; in-flight
  segments continue until the JWT expires (≤ 15 min).
- EC2. Admin removes the only admin — refused with a constraint
  error; documented.
- EC3. Library deleted while a user has an active session — open
  sessions error gracefully with `library_gone`; the UI returns to
  the home screen.

### Story 23.3 — Transport security

TLS by default; localhost is the only documented exception.

**Acceptance criteria:**

- AC1. Caddy fronts the stack and terminates TLS; on Mac, Caddy's
  local-CA mode auto-issues a trusted cert to the keychain; on
  Linux, Let's Encrypt against the user's domain.
- AC2. HSTS enabled by default with `max-age=31536000;
  includeSubDomains`; opt-out documented.
- AC3. TLS configuration: TLS 1.2 minimum, modern cipher suites
  only, OCSP stapling on, ALPN h2.
- AC4. Internal gRPC between services uses mTLS when the services
  are not co-located; for `localhost` colocated processes,
  loopback-only gRPC is permitted with a documented threat model.

**Test cases:**

- TC1. Cipher floor: `nmap --script ssl-enum-ciphers` against the
  default Caddy config reports no `weak` or `broken` entries.
- TC2. HSTS: a fresh load returns the header; the `--no-hsts` flag
  opts it out and observable in the response.
- TC3. mTLS: with a cert mismatch, an inter-service gRPC call fails
  with a clear cert error; loopback path bypasses with a startup
  warning.

**Edge cases:**

- EC1. Self-signed cert on a fresh install — the web client shows a
  documented "trust this device" flow on mobile, none on desktop.
- EC2. Let's Encrypt rate-limit hit — Caddy retries with backoff;
  health probes still report alive, ready=false until cert acquired.
- EC3. Captive-portal proxies that downgrade — clients refuse to
  send credentials over a downgraded connection.

### Story 23.4 — Secrets management

Secrets live in env or config files only; never in DB rows users can
read; never in logs; never in metrics; never in error reports.

**Acceptance criteria:**

- AC1. Canonical secret list is enumerated in architecture §11.5:
  `MAKTABA_ADMIN_TOKEN`, `MAKTABA_DATABASE_URL`,
  `MAKTABA_JWT_PRIVATE_KEY_PEM`, `MAKTABA_JWT_PUBLIC_KEY_PEM`,
  `OPENAI_API_KEY`, etc. Each has a documented owner service.
- AC2. The Streaming service never sees the JWT private key or any
  STT backend keys (architecture §11.5); the binary is shipped with
  no code path that reads them.
- AC3. `/api/settings` never returns secret values, only metadata
  (key name, whether set, source: env/file). Secret values are
  write-only.
- AC4. A redaction middleware rewrites known secret-shaped values
  (high-entropy strings, keys named `*_KEY`, `*_TOKEN`, `*_PASSWORD`)
  in any log line that escapes the structured-field rule.

**Test cases:**

- TC1. Streaming binary: `strings` on the binary contains no
  reference to the JWT private key env name; static analysis CI
  asserts.
- TC2. Settings round-trip: `GET /api/settings` for a configured
  `OPENAI_API_KEY` returns `{configured: true, source: "env"}`,
  never the value.
- TC3. Redaction: a `slog.Info` with a secret-shaped value writes
  `***` to the log; the original value never appears in any sink.

**Edge cases:**

- EC1. Secret in a stack trace — middleware redacts the secret in the
  trace before emission.
- EC2. Multi-line PEM key in env — supported; parsing tolerates
  `\n`-escaped and literal-newline forms.
- EC3. Secret rotation while in flight — the service holds the loaded
  value for the lifetime of an in-flight request and reloads on next
  inbound after a SIGHUP or admin endpoint trigger.

### Story 23.5 — Input validation and content safety

Every external input is validated; SSRF, path traversal, command
injection, and untrusted file content are explicitly defended.

**Acceptance criteria:**

- AC1. All API inputs are validated against the OpenAPI / GraphQL
  schema; rejected with `400` and a structured `problem+json` error.
- AC2. Filesystem paths from clients are forbidden as values; only
  opaque IDs (UUID v7) are accepted. Paths in config / library
  roots are normalized and re-rooted; `..` traversal is rejected.
- AC3. FFmpeg invocations build argv as a slice, never a shell
  string; subprocess execution is `os/exec` with explicit args, no
  `sh -c`. The same rule applies to `pyannote`, `whisper-cli`, etc.
- AC4. SSRF defense: any code path fetching from a URL (e.g., poster
  fetch, OAuth callbacks if added) checks the resolved IP is not
  RFC 1918 / loopback / link-local and follows ≤ 3 redirects.
- AC5. Untrusted file content: probe outputs are size-bounded; a
  malformed media file produces an error, not a panic; subtitle
  files are sanitized for HTML/script injection before rendering.

**Test cases:**

- TC1. Path traversal: `POST /api/libraries` with `root="/etc/passwd/.."`
  is rejected; `root="/var/maktaba/../../etc"` after normalization
  is rejected.
- TC2. Command injection: a video filename containing `; rm -rf /`
  passes through every FFmpeg invocation untouched and produces
  no shell expansion.
- TC3. SSRF: `POST /api/libraries/poster?url=http://169.254.169.254/`
  refuses to fetch; `http://localhost:5432/` refuses to fetch.

**Edge cases:**

- EC1. Subtitle file with `<script>` tags — sanitizer escapes; a
  rendered VTT cue is plain text in the player.
- EC2. Filename with NUL byte — rejected; no path operation accepts
  a NUL byte.
- EC3. Symlinks under media root — followed only if the target is
  also under a configured root; otherwise rejected with a logged
  warning.

### Story 23.6 — Rate limiting and abuse protection

Self-host doesn't mean "no abuse." A misbehaving client (or a
compromised account on a shared LAN) shouldn't take the box down.

**Acceptance criteria:**

- AC1. Per-IP rate limits on auth endpoints (`/api/auth/login` 10/min
  per IP, /refresh 60/min); structured `429` with `Retry-After`.
- AC2. Per-user rate limits on expensive endpoints (search 60/min,
  bulk job submit 10/min).
- AC3. Failed login attempts are tracked per (user, ip); ≥ 5 failures
  in 5 minutes locks the user for 15 minutes; an audit row is
  written; an admin override clears the lock.
- AC4. Limits are configurable; in single-user mode, defaults are
  relaxed (since one user owns the box) but never disabled.

**Test cases:**

- TC1. Login burst: 11 failed login attempts from the same IP within
  60 s; the 11th is `429`; subsequent attempts continue to be `429`
  for the configured window.
- TC2. User lockout: 5 failed logins for one user; the 6th attempt
  even with the correct password returns `423 Locked`; admin
  unlock clears it.
- TC3. Search burst: 100 requests in 30 s from one user; the limiter
  responds with `429` after the threshold, never crashes.

**Edge cases:**

- EC1. Behind a reverse proxy that strips `X-Forwarded-For` — the
  limiter falls back to the connecting IP; an admin warning is
  emitted on startup if proxy headers are required but absent.
- EC2. Legitimate burst from a multi-device household — per-user
  limits dominate over per-IP; documented.
- EC3. Distributed admin operations (bulk re-process) — exempt from
  user limits with explicit `admin: true` flag; audited.

### Story 23.7 — Supply-chain security

Every dependency, base image, and binary that ships in a release is
auditable.

**Acceptance criteria:**

- AC1. SBOM (`cyclonedx-go`, `cyclonedx-py`, npm SBOM) generated for
  every release artifact; published alongside.
- AC2. CVE scanning gate in CI (`govulncheck`, `pip-audit`, `npm
  audit --audit-level=high`); a high-severity vuln blocks merge
  unless explicitly suppressed with a recorded reason and a date
  ceiling.
- AC3. Base images pinned by digest; `Dockerfile`s use
  `--platform=$BUILDPLATFORM` correctly; no `:latest`.
- AC4. Dependency upgrades are managed by `dependabot` /
  `renovate`; a weekly run opens PRs; security PRs are auto-approved
  if green.

**Test cases:**

- TC1. SBOM: every release tag publishes four SBOM files (api,
  streaming, pipeline, web); each contains all transitive deps
  with versions.
- TC2. CVE block: a deliberate `pin` of an old `golang.org/x/net`
  with a known high CVE fails the merge gate.
- TC3. Digest pin: a Dockerfile editing a base image to `:latest`
  fails CI's lint.

**Edge cases:**

- EC1. False-positive CVE — suppression requires a markdown file
  under `security/suppressions/<cve-id>.md` with rationale and
  expiry date; expired suppressions auto-fail.
- EC2. Vendored Go module with a CVE — `govulncheck` against the
  vendor tree finds it; build fails until rebuilt.
- EC3. Air-gapped builds — `make build-airgap` produces a tarball
  including all deps; CI smoke runs an air-gapped path.

### Story 23.8 — Coordinated disclosure and security response

A self-hosted product needs a way for security researchers to
report. We're tiny; our process is small but real.

**Acceptance criteria:**

- AC1. `SECURITY.md` documents the disclosure address (a dedicated
  email or GitHub Security Advisories), the response SLA (3 business
  days to ack, 90 days to fix or coordinated disclosure), and the
  scope (in-tree code, official artifacts).
- AC2. Reported vulnerabilities are tracked in a private repo or
  GHSA draft; once fixed, an advisory is published with CVE if
  applicable, mitigation, and affected versions.
- AC3. Critical fixes are released as patch versions on supported
  branches; the release notes link the GHSA.
- AC4. The web client surfaces a one-click "What version am I
  running?" with a link to known advisories for that version.

**Test cases:**

- TC1. Security workflow drill: a synthetic report is filed against
  the documented address; an acknowledgement is recorded within
  the SLA in a tabletop exercise.
- TC2. Advisory link: a versioned client renders advisories
  matching its `version` field; an intentionally outdated client
  renders the upgrade prompt.
- TC3. Patch release: a synthetic CVE produces a patch tag, an
  artifact rebuild, an SBOM update, and an advisory notification
  end-to-end.

**Edge cases:**

- EC1. Reporter requests anonymity — supported; published advisory
  thanks "an anonymous researcher" by default.
- EC2. Vulnerability in an upstream dep — Maktaba's advisory points
  to the upstream and ships the dep bump as the fix.
- EC3. Disclosure conflict (researcher wants to publish before fix)
  — `SECURITY.md` documents the 90-day default; deviations are
  coordinated case-by-case.

---

## Epic 24 — Data Integrity

**Goal.** A user's media library and the platform's derived state survive
crashes, power loss, partial writes, concurrent jobs, and operator
mistakes. The library is the durable truth; derived data (transcripts,
indexes, caches) is recoverable from it. Recovery is documented and
tested.

This epic covers atomicity, idempotency, consistency between authoritative
state and derived state, backup / restore, durability of in-flight work,
and integrity verification.

### Story 24.1 — Atomic writes for sidecar artifacts

Every generated artifact lives next to the source file (`.maktaba/`) and
must never appear on disk in a half-written state.

**Acceptance criteria:**

- AC1. Subtitle (`.srt`, `.vtt`), segment JSON, thumbnail, sprite, and
  poster outputs are written to a temp path under the same
  filesystem and atomically renamed into the final location only on
  successful completion.
- AC2. The atomic-rename invariant holds across crash points: a kill
  -9 mid-write leaves no partial output and a stale temp file the
  reaper sweeps within 24 h.
- AC3. Atomic-write helpers are centralized in a single utility
  (`media.atomic_write`) used by every generator; bypassing it fails
  a CI lint.
- AC4. On filesystems that don't support `rename(2)` atomicity (some
  network shares), the writer falls back to a `(write, fsync,
  rename, fsync_dir)` sequence with a documented warning.

**Test cases:**

- TC1. Crash mid-write: kill the worker mid-subtitle write; on
  restart, the final output is missing or complete, never partial.
- TC2. Rename atomicity: race a write against a concurrent reader;
  the reader sees old content or new content, never partial bytes.
- TC3. Sweep: a stale temp older than 24 h is removed by the reaper;
  no error if it was already cleaned up.

**Edge cases:**

- EC1. Out-of-space mid-write — fail the write, leave no partial
  output, error reported with `category=disk_full`.
- EC2. Network-share rename non-atomic on Windows SMB — documented
  fallback path and per-target tested.
- EC3. Source file deleted after temp write but before rename — the
  rename succeeds, leaving an orphan sidecar; the next scan
  reconciles by removing orphans.

### Story 24.2 — Idempotent and resumable jobs

Every pipeline stage can be re-run safely. A crash during transcription
leaves no partial subtitle file; a resume picks up exactly where it
left off.

**Acceptance criteria:**

- AC1. Each stage's job has a stable idempotency key:
  `(content_hash, stage, backend, model, config_hash)`. Re-claiming
  a job with the same key skips work that's already complete.
- AC2. Transcription commits each STT segment in its own DB
  transaction along with `processing_jobs.last_segment_end_sec` and
  a heartbeat (architecture §7.6). Resume reads
  `last_segment_end_sec` and feeds it to the STT engine as start
  offset.
- AC3. Sidecar outputs are regenerated from DB state, not from
  whatever was on disk; the on-disk subtitle is a *projection* and
  can be deleted and rebuilt.
- AC4. Bulk re-process commands accept a `--from-stage` to start at
  any point in the DAG; downstream stages re-run; upstream is
  unchanged.

**Test cases:**

- TC1. Resume from segment N: kill mid-transcribe at minute 30 of a
  60-min clip; resume from same job; total wall-clock is ~30
  minutes, output matches a clean-run reference within tolerance.
- TC2. Idempotent claim: enqueue the same `(content_hash, stage)`
  twice; only one job runs; the other returns the existing result.
- TC3. Rebuild sidecars: delete `.maktaba/` directory; trigger
  `maktaba-pipeline reprocess --from-stage subtitle_gen`;
  artifacts return.

**Edge cases:**

- EC1. STT engine non-determinism — segment boundaries may shift on
  resume; the test asserts segment text similarity ≥ 95 %, not
  byte-equality.
- EC2. Backend changed mid-job (config bumped) — the resume
  detects the `config_hash` mismatch and re-runs from start; no
  silent splicing across backends.
- EC3. Crash exactly at the segment-commit boundary — the next
  worker re-runs the segment; the output table's `(job_id,
  segment_idx)` unique constraint deduplicates.

### Story 24.3 — Database consistency and constraints

The schema enforces integrity constraints; no business logic relies on
"the application will be correct."

**Acceptance criteria:**

- AC1. Foreign keys on every relation; `ON DELETE` is explicit
  (`CASCADE` for child rows; `RESTRICT` for cross-aggregate refs).
- AC2. Unique constraints enforce business invariants:
  `videos.content_hash` unique, `(library_id, video_id)` unique,
  `(video_id, segment_idx)` unique, `users.username` unique.
- AC3. Check constraints validate enum-shaped fields (`videos.state
  IN (...)`, `processing_jobs.state IN (...)`); SQLite parity tested.
- AC4. Soft deletes use `deleted_at TIMESTAMPTZ NULL` with a partial
  unique index where applicable; hard deletes are restricted to
  admin-driven `gc` operations.

**Test cases:**

- TC1. FK enforcement: deleting a `library` cascades to its `videos`,
  which cascades to `segments`; counted before and after.
- TC2. Unique violation: two writers attempt to insert the same
  `(content_hash)`; one succeeds, one gets a clean unique-violation
  error with the conflicting field named.
- TC3. State enum: an attempt to set `videos.state = 'unknown'`
  fails the check constraint, not in app code.

**Edge cases:**

- EC1. SQLite missing `ON DELETE CASCADE` enforcement unless
  `PRAGMA foreign_keys = ON` — the connection bootstrap sets it; a
  test asserts.
- EC2. Concurrent state-machine transitions — the state column has
  a `CHECK` and the update has a `WHERE state = expected_prev`
  guard; one transition wins, the other returns "stale transition."
- EC3. Migration that adds a NOT NULL column — pattern is documented:
  ship as nullable, backfill, add NOT NULL, ship.

### Story 24.4 — Concurrency and locking

Concurrent writes against the same row, or claims for the same job, are
serialized correctly without livelocks.

**Acceptance criteria:**

- AC1. Job claim uses `SELECT … FOR UPDATE SKIP LOCKED` (architecture
  §7.3); exactly-once across N workers verified by Epic 19.4.TC2.
- AC2. Watch-progress writes are last-writer-wins per (user, video)
  with monotonic guarantee (server rejects updates with
  `position_sec` lower than current unless a `seek=true` flag is
  set).
- AC3. ChromaDB upserts use a documented single-writer rule (one
  Pipeline process at a time) per architecture §10.3; multi-writer
  is reserved for the Chroma server deployment.
- AC4. Postgres advisory locks gate per-resource serialization (per-
  GPU-device, per-cache-eviction); locks are released on connection
  close and on explicit unlock; no orphaned locks after a worker
  crash.

**Test cases:**

- TC1. Race on watch progress: 10 concurrent writes to the same
  (user, video); the final value is the latest non-seek progress;
  audit log records the sequence.
- TC2. Advisory lock release on crash: hold a lock; kill the holder;
  the next acquirer succeeds within the connection-timeout window.
- TC3. ChromaDB single-writer: two Pipeline processes pointed at
  the same Chroma path are detected at startup; the second logs a
  warning and refuses to write.

**Edge cases:**

- EC1. SKIP LOCKED with priority queues — the priority ordering is
  preserved across concurrent claimers because `ORDER BY priority,
  created_at` is part of the claim query.
- EC2. Long-held advisory lock — the holder must heartbeat; > 3×
  heartbeat without progress is force-released by the reaper.
- EC3. Watch-progress for a deleted video — the write is dropped
  with a `category=stale_resource` log, not an error.

### Story 24.5 — Backup and restore

Every state-bearing surface has a documented, tested backup and
restore procedure.

**Acceptance criteria:**

- AC1. **Postgres:** daily `pg_dump --format=custom` to a configured
  backup root; retention configurable (default 14 days); a
  `maktaba-api restore --from <file>` command runs `pg_restore` with
  conflict-safe options.
- AC2. **SQLite:** daily `VACUUM INTO` snapshot; restore is a file
  copy.
- AC3. **ChromaDB:** documented as recoverable from the source media
  + transcripts (rebuild via `reprocess --from-stage index`); not
  separately backed up unless explicitly opted in.
- AC4. **Caches:** never backed up (HLS, sprites, embedding cache);
  documented.
- AC5. **Media volume:** out of scope for Maktaba's backup; pointer
  to "use your existing media backup story (rsync, ZFS snapshots,
  Time Machine, etc.)" with linked recommendations.

**Test cases:**

- TC1. Restore drill: run the daily backup, simulate disaster
  (drop the DB), restore, assert the catalog smoke test passes.
- TC2. Cross-version restore: a v1.0 dump can be restored into a
  v1.1 server; migrations run forward; data intact.
- TC3. Chroma rebuild: delete the Chroma store; `reprocess --from-
  stage index` restores it; semantic search returns equivalent
  results within tolerance.

**Edge cases:**

- EC1. Backup-during-burst — `pg_dump` runs in a low-traffic window
  (configurable cron); a documented "snapshot now" command exists
  for one-off snapshots.
- EC2. Backup file corruption — the backup runner verifies the
  dump immediately by streaming through `pg_restore --list` after
  writing; corrupted backups are alerted on.
- EC3. Backup target full — the runner deletes the oldest backup
  beyond retention before writing the new one; if still full,
  fails the new backup with an alert (does not overwrite a good
  recent backup).

### Story 24.6 — Disaster recovery

Documented recovery scenarios with verified RTO/RPO targets.

**Acceptance criteria:**

- AC1. Recovery scenarios documented with steps and expected wall-
  clock:
  1. **DB lost, media intact** — restore from latest backup;
     reprocess any new media since the backup. RTO ≤ 30 minutes.
     RPO ≤ 24 h (last daily backup).
  2. **DB and derived caches lost, media intact** — same as #1
     plus full reindex. RTO ≤ proportional to library size,
     reported per-library.
  3. **Media partially corrupted** — content_hash on
     mismatch detects; the affected videos go to `state =
     CORRUPTED` and the user is notified.
  4. **Service binaries corrupted** — reinstall via the canonical
     install path (Epic 22.7); state intact.
- AC2. A `make dr-drill` target runs scenario #1 against a seeded
  fixture in CI nightly; failures alert.
- AC3. The user-visible "Restore" UI (admin panel) walks the user
  through each scenario with a single Run button per step.

**Test cases:**

- TC1. Scenario #1: `dr-drill` brings up a fresh stack, restores a
  previous-day dump, runs the catalog smoke test, all green within
  RTO budget.
- TC2. Scenario #3: corrupt a file's middle bytes; the next
  integrity check (Story 24.7) finds and marks it `CORRUPTED`;
  the UI shows it.
- TC3. Documented commands: every step in the DR doc has a
  copy-pasteable command that is exercised by the drill.

**Edge cases:**

- EC1. Partial DB restore (one schema lost) — the restore script
  refuses partial restores by default; an `--allow-partial` flag
  with documented risk is required.
- EC2. Restore onto a host with a higher schema version —
  migrations run forward automatically; the doctor reports the
  delta.
- EC3. Restore onto a host with a *lower* schema version — refused
  with a clear error; the operator is told to upgrade first.

### Story 24.7 — Integrity verification

Periodic and on-demand integrity checks across the canonical surfaces.

**Acceptance criteria:**

- AC1. A `maktaba-pipeline doctor --integrity` runs:
  - `content_hash` re-verification on a configurable sample (or
    full library, opt-in).
  - Sidecar presence check (every `READY` video has expected
    artifacts).
  - DB referential integrity (no dangling FKs, no soft-deleted
    children with live parents).
  - FTS / Chroma row-count parity with `segments` row count.
- AC2. Integrity reports are written to `audit_log` (Epic 21.6) and
  visible in the admin panel.
- AC3. Auto-remediation is opt-in: `--repair` re-enqueues missing
  sidecars and re-indexes missing segments. Without `--repair`,
  doctor only reports.
- AC4. The doctor runs as a scheduled task once a week by default;
  configurable cadence; off in single-user mode unless opted in.

**Test cases:**

- TC1. Detection: corrupt a transcript file; doctor reports
  mismatch; with `--repair`, sidecar is regenerated.
- TC2. FTS parity: delete one row directly from the FTS table;
  doctor reports drift; `--repair` reindexes.
- TC3. Sample mode: with a 50 k-video library, sampled integrity
  check completes in ≤ 5 minutes; full-mode is allowed but
  documented as overnight.

**Edge cases:**

- EC1. Hash recomputation reading 30 TB — the sample mode is the
  default; full mode requires explicit opt-in and a confirmation
  prompt.
- EC2. Drift caused by a known background job — the doctor
  cross-references in-flight jobs and excludes their outputs from
  the parity check.
- EC3. False positive from a clock skew (mtime regressions) —
  doctor uses content_hash, not mtime, as the source of truth.

### Story 24.8 — Identity stability across operations

`content_hash` is the system-wide identity. Every flow that touches a
file must preserve identity correctly.

**Acceptance criteria:**

- AC1. `content_hash = BLAKE3(first 4 MiB || last 4 MiB || u64_le(size))`
  with documented tie-breaker for files smaller than 8 MiB (the
  whole file is hashed once).
- AC2. Identity is computed once on first scan and stored on the
  `videos` row; subsequent scans reuse the stored hash if `(path,
  size, mtime)` is unchanged.
- AC3. Move / rename within a tracked root preserves the hash and
  updates the path; copy creates a new path → same hash → already-
  ready row served immediately (no re-process).
- AC4. Identity regression suite covers small files, sparse files,
  files exactly at the boundary, and files modified-in-place
  (mtime change but bytes equal).

**Test cases:**

- TC1. Move stability: rename 1,000 random files; assert no
  re-process is enqueued and all `content_hash` rows are unchanged.
- TC2. Copy stability: copy 100 files to a new location; the new
  rows reuse existing transcripts and indexes.
- TC3. Modify-in-place: edit a single byte in a video; the new
  `content_hash` differs; a re-process is enqueued; old-hash row
  is preserved (history) until GC.

**Edge cases:**

- EC1. File exactly 8 MiB — `first 4 MiB || last 4 MiB` overlap is
  the whole file; documented and tested as identical to "hash the
  whole file once."
- EC2. File smaller than 4 MiB — same handling; whole-file hash.
- EC3. Sparse file with hole at end — `last 4 MiB` includes the
  hole bytes; consistent with how the OS reports them; documented.

### Story 24.9 — Forward and backward compatibility

State produced by version N must be readable by N+1 (and reasonably
by N-1 across one minor version), so upgrades and rollbacks (Epic
22.6) don't corrupt data.

**Acceptance criteria:**

- AC1. Schema changes follow the documented playbook: add column
  nullable → backfill → set NOT NULL in a later release. A single
  release never introduces "breaking" reads of old rows.
- AC2. Generated artifact formats (segment JSON, sidecars) carry a
  `schema_version` field; readers tolerate higher minor versions
  by ignoring unknown fields.
- AC3. Cache key prefixes include the platform major version; a
  major bump invalidates caches automatically.
- AC4. A "forward-compat" test loads fixtures captured from
  previous versions and asserts they still work in the current
  version.

**Test cases:**

- TC1. Old dump load: a v1.0 `pg_dump` restores into a v1.2
  schema; migrations run; smoke test passes.
- TC2. Old sidecar parse: a `schema_version=1` segment JSON file is
  parsed by the current version with the documented field-default
  behavior.
- TC3. Cache invalidation on major bump: simulating v2.0, v1.x
  cache entries are ignored; new entries are written under the
  v2 prefix.

**Edge cases:**

- EC1. A v1.x client connecting to a v1.(x+1) server — supported
  per Epic 22.6; a v1.x client connecting to a v2.0 server is
  refused with a clear "incompatible major version" message.
- EC2. `schema_version` missing on an old artifact — reader treats
  it as `1`; documented.
- EC3. Lossy migration (rare; e.g., dropping a deprecated field) —
  documented in CHANGELOG and the migration script archives the
  data to a `removed_data_v{n}` JSON file for forensics.

---

## Cross-references and source-of-truth pointers

Where this document gives a number, table, or contract, the canonical
source is one of the following sections of
[`specs/architecture.md`](../architecture.md):

- Service split, language choice, single-binary topology — §1.3, §1.4.
- Tech stack defaults — §2.
- Pipeline stage list and idempotency — §3, §7.
- Streaming session model and cache layout — §4.
- Job state machine, claim loop, heartbeats — §7.2, §7.3, §7.6.
- Database schema (videos, segments, jobs, users) — §8.
- API surface — §9; gRPC — §9.9.
- Scalability targets and capacity — §10.
- Configuration layering and secrets — §11.
- Repo layout, build/release tooling — §12.

When this document and the architecture doc disagree on a number, the
architecture doc wins for system invariants (capacities, schema,
contracts) and this document wins for quality bars (latency budgets,
test pyramid, audit policy). Resolution PRs update both in lockstep.
