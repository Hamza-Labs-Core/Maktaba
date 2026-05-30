# Epic 21 — Observability: Spec-vs-Implementation Gap Analysis

**Verdict:** Logging is solid; metrics/health are real but incomplete (streaming has no `/metrics`; baseline metric set partial); tracing is a confirmed no-op stub; error-reporting (21.5) and telemetry-privacy enforcement (21.8) are essentially unimplemented; the audit log (21.6) exists as a flat non-partitioned, non-immutable table whose "views" and append-only guarantee are missing. **~9 complete, ~10 partial, ~13 missing, ~3 stub of ~35 ACs.**

Method: every AC traced to code; verified reachability and behaviour against `shared/log/go`, `shared/metrics/go`, `shared/tracing/go`, `shared/health/go`, `pipeline/src/maktaba_pipeline/observability.py`, `api/main.go`, `streaming/main.go`, router wiring, and migrations. Spec/audit self-claims were not trusted.

---

## Story 21.1 — Structured logging

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC1 Go `slog` JSON prod / text dev; Python `structlog` same JSON; TS thin `logger` | **partial** | Go: `shared/log/go/logger.go:104-119` JSON/text by env — complete. Python: `pipeline/src/maktaba_pipeline/log/__init__.py:119-123` — complete, field-parity with Go. TS browser logger: **missing** — no `logger.ts` anywhere under `web/src` or `shared` (grep for `VITE_LOG_LEVEL` / `export function log` returns nothing). Plan §5 specifies `shared/log/ts/logger.ts`; not created. |
| AC2 Base fields `ts,level,service,msg` + contextual ids; no line without `service` | **complete** | `logger.go:121-125` injects `service/version/env` on the base logger; `ctx.go:51-75` injects `request_id/session_id/job_id/video_id/user_id`. `makeReplaceAttr` (`logger.go:148-169`) renames time/level/msg to the contract. |
| AC3 Levels incl. `fatal`; runtime toggle via SIGUSR1 / admin endpoint | **partial** | SIGUSR1 cycle works (`sigusr1_unix.go:16-30`). `SetLevel` exists (`logger.go:136`). **Gaps:** (1) no `fatal` level — `slog` has no fatal; no helper that logs-then-exits (grep `fatal/Fatal/LevelFatal` in log pkg → none). (2) Admin endpoint `POST /admin/log/level` (plan §10) **not implemented** — no route registered; `SetLevel` has no HTTP caller. |
| AC4 No user-string concat into `msg`; explicit fields | **partial (unenforced)** | Convention followed in observed call sites (`slog_logger.go:48-54` uses fielded kv). **Gap:** the AC's enforcement mechanism — the AST concat lint (`plan §7`, `shared/log/go/lint/concat_lint.go`) — **does not exist**. No `lint/` dir under `shared/log/go`; no `make lint:log`. TC1 cannot pass. |
| EC1 >64 KiB msg → truncate 60 KiB + `truncated:true` | **partial** | `logger.go:156-161` truncates to 60 KiB with a ` ...[truncated]` suffix. **Gap vs AC:** the spec requires a sibling `truncated: true` field; the code explicitly cannot emit it (comment at `logger.go:157-159`) — only an inline marker. |
| EC2 FFmpeg stderr → `event=ffmpeg_stderr` | **complete (unwired)** | `shared/log/go/ffmpeg.go:18-37` implements the wrap correctly. **Caveat:** no production caller found wiring `WrapFFmpegStderr` to the streaming FFmpeg subprocess (streaming `internal/ffmpeg` not shown to call it) — surface exists but reachability on the real path is unverified/likely unwired. |
| EC3 RTL/bidi in msg not garbled | **complete** | Standard `slog` JSON handler UTF-8 encodes; no custom escaper that would break it. |

## Story 21.2 — Metrics surface

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC1 `/metrics` per service + baseline set | **partial** | API exposes `/metrics` on admin port (`api/main.go:183-187`). Baseline present in `shared/metrics/go/baseline.go`: `http_request_duration_seconds`, `http_in_flight_requests`, `db_query_duration_seconds`, `cache_hits/misses_total`, `pipeline_jobs_total`. **Gaps:** (a) **streaming exposes no `/metrics`** — `streaming/main.go:101-118` mounts only `health.AdminMux` (healthz/readyz); no `metrics.NewHandler` import/call. (b) `transcode_active_sessions`, `transcode_queue_depth`, `streaming_*` histograms (plan §6) **not registered anywhere**. (c) `pipeline_stage_duration_seconds` **not present** — pipeline uses a separate hand-rolled registry with different names (`maktaba_jobs_total`, `maktaba_job_duration_seconds`, `observability.py:301-320`), not the spec's metric. (d) `db_query_count` per-route histogram (plan §3) absent. |
| AC2 Bounded cardinality; static lint enforces | **partial (unenforced)** | Code convention is correct (`statusClass` buckets status; `route_template` from chi pattern, `middleware/metrics.go:44-53`). **Gap:** the cardinality lint (`plan §5`, `shared/metrics/go/lint/cardinality_lint.go`) **does not exist** — no `lint/` dir; `make lint:metrics` / TC1 cannot pass. |
| AC3 Native exponential buckets + documented fixed fallback | **complete** | `registry.go:55-66` sets `NativeHistogramBucketFactor:1.1` and `Buckets: msToS(FixedMSBuckets)` with the exact `[1ms..10000ms]` layout from the AC. |
| AC4 `/metrics` unauth localhost-default; opt-in network needs bearer | **partial** | `http.go:24-36` enforces "Public=true requires bearer" and constant-time `bearerWrap`. **Gap:** in `api/main.go:183` the handler is mounted with `metrics.Config{Bind: adminAddr}` and `Public:false` — but the binding is whatever `MAKTABA_ADMIN_ADDR` is (default `:9100`, i.e. all interfaces), **not** localhost-only. The localhost default the AC requires is not implemented; there is no public-metrics opt-in path wired (no `MAKTABA_METRICS_PUBLIC_ADDR` reading despite the comment in `main.go:131-132`). |
| EC1 FFmpeg-dropped session decrement via reaper | **missing** | No `transcode_active_sessions` gauge exists, so reaper-driven decrement is moot. |
| EC2 Counter reset doc note | **missing** | No `shared/metrics/README.md` (only `dashboards.go`); rate() guidance not documented. |
| EC3 Web vitals `POST /api/telemetry/web-vitals` capped 1/5min | **missing** | No `web_vitals` handler anywhere (`grep web-vitals/WebVitals/api/telemetry` → nothing in `api/internal`). Plan §7 unimplemented. |

## Story 21.3 — Distributed tracing

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC1 `otelhttp`/OTel + W3C `traceparent` across REST/GraphQL/gRPC/Postgres | **stub** | `shared/tracing/go/tracer.go:1-11` self-documents as a stub. `Init` (`:61-68`) returns a no-op shutdown for **both** empty and non-empty endpoints — the non-empty branch is a fake. HTTP middleware (`http.go:44-56`) hand-rolls a `traceparent` header but there is **no OTel SDK, no exporter, no span creation**. gRPC interceptors / pgx tracer (plan §5–6) **do not exist**. No Python tracer (`pipeline/.../tracing/init.py` absent). |
| AC2 Composite head sampler (100% error/slow, 1% else) | **missing** | No sampler; no `sampler.go`. |
| AC3 Web client span per page-load / search w/ traceparent | **missing** | No `web/src/lib/tracing.ts`. |
| AC4 Opt-in via `[telemetry].otlp_endpoint`; never silent exfiltration | **complete (vacuously)** | Empty endpoint = noop is honoured (`tracer.go:62-64`), and `api/main.go:164-174` reads `MAKTABA_OTLP_ENDPOINT`. Default-off holds — but only because tracing does nothing at all. |
| EC1/EC2/EC3 buffer cap, no body in span, query-hash | **partial** | `QueryHash` helper exists (`http.go:60-66`) and is PII-safe; but no spans exist to attach it to, and no buffer/drop-counter. |

## Story 21.4 — Health and readiness probes

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC1 `/healthz` 200 if alive, never blocks | **complete** | `shared/health/go/healthz.go:43-52` — constant 200, no deps. Wired in API (`api/main.go:178,279`) and streaming (`streaming/main.go:102`). |
| AC2 `/readyz` 200 only when DB≥1 conn, required gRPC peers reachable, caches warmed; 503 + JSON of failures | **partial** | `readyz.go:84-139` runs checks in parallel under budget, returns 503+JSON. DB check `checks.go:50-55`. **Gaps:** (a) gRPC peer check is a bare `TCPDial` (`checks.go:62-85`) not a gRPC `connectivity.Ready` probe (plan §3 `GRPCPing` not used); a listening socket that isn't serving gRPC passes. (b) **Cache-warm check (`CacheWarm`, plan §3) not implemented** — no cache readiness contributor; "caches warmed/degraded" clause unmet. (c) In `api/main.go:436-449` peers come from `MAKTABA_GRPC_PEERS` and are optional — a default deploy has no peer check at all. |
| AC3 `/api/system/health` aggregates 3 services + disk free + queue depth + transcribe budget | **partial** | `api/internal/system/aggregator.go` fans out to `/readyz` and rolls up ok/degraded/down — solid. **Gap:** `DiskFreeBytes`, `QueueDepth`, `BudgetUSDLeft` are declared `omitempty` placeholders never populated (`aggregator.go:51-57`; comment at `:46-50` admits this). AC3's disk/queue/budget fields are absent in practice. |
| AC4 Probes unauth on separate admin port | **complete** | Admin mux on `:9100`/`:9101` distinct from public `:8080`/`:8081` (`api/main.go:288-292`, `streaming/main.go:108-118`). |
| EC1 DB failover flips 503→200 ≤30s | **partial** | Relies on `database/sql` pool reconnection; plausible but the readiness DB pool is opened with `SetMaxOpenConns(1)` (`api/main.go:431`) — a single dead conn can wedge the probe; behaviour unverified. |
| EC2 Streaming all down → aggregator surfaces | **complete** | `deriveStatus` (`aggregator.go:171-195`) yields down/degraded correctly. |
| EC3 SQLite-mode probe matrix | **missing** | No SQLite-specific check branch (plan §5 `SQLitePing`); `buildChecks` only does `DBPing` for postgres-style `DATABASE_URL`. No `docs/runbooks/probe-matrix.md`. |

## Story 21.5 — Error reporting and alerting integration

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC1 Every error-log emits `error_id` (UUIDv7) + stack + `category` | **missing** | No `shared/errrpt` package (dir absent). No error wrapper with `category`/UUIDv7. `audit.go:91` references an `error_id` column but nothing generates/propagates one. Error logs are plain `slog.Error` with no id/stack/category contract. |
| AC2 Built-in webhook (Slack/Discord/generic), rate-limited 10/min + backoff suppress | **missing** | No webhook code anywhere (`grep error_drop_log/webhook/Suppressor` → nothing). |
| AC3 Sentry/Honeycomb/GlitchTip opt-in via env DSN, never logged | **missing** | No Sentry SDK, no `InitSentry`, no DSN handling (`grep -i sentry` → none). |
| AC4 `error_id` crosses service boundaries via gRPC metadata | **missing** | No `propagator.go`, no `x-error-id` metadata key. |
| EC1/EC2/EC3 circuit breaker / DSN-typo tolerance / shutdown drain | **missing** | None implemented. (Note: a *gRPC client* circuit breaker exists in `grpcclients/pipeline` but is unrelated to error-webhook EC1.) |

## Story 21.6 — Audit log for sensitive actions

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC1 Dedicated `audit_log` Postgres table **partitioned by month** + file mirror; specified schema | **partial / wrong shape** | Table exists (`migrations/0036_audit_log.sql:12-20`, extended `0054_audit_log.sql`). **Gaps:** (a) **NOT partitioned** — `0036` is a plain `CREATE TABLE ... PRIMARY KEY (id)`; no `PARTITION BY RANGE (occurred_at)`, no monthly partitions, no `audit_log_default` (grep `PARTITION BY` in migrations → none). (b) **No file mirror** `/var/maktaba/audit/audit.log` and no replayer (`shared/audit` dir absent; only `shared/log/go/audit.go` which is DB-only). (c) Schema differs from AC (e.g. `actor_user_id` not `actor_user`; no `clock_source` default semantics tied to mirror). |
| AC2 Events recorded across auth/keys/config/library/data/admin | **partial** | `library` events written via `libraries/audit.go:145-182`; `security`/auth events via `securityaudit.go:96-118`. **Gap:** auth/login events are stored with `category='security'` (`securityaudit.go:51`), but the AC's canonical taxonomy puts login under `category='auth'`; `keys`/`config` rotation+settings-hash events not demonstrably emitted. |
| AC3 Append-only enforced by BEFORE UPDATE OR DELETE trigger | **missing** | No trigger in any migration (`grep audit_log_block_mutate / BEFORE UPDATE OR DELETE` → none). The table is freely mutable. TC1 fails. |
| AC4 Retention via partition detach/drop; admin CLI emits its own audit row | **missing** | No partitions ⇒ no detach/drop retention; no `cmd/maktaba-admin/audit_drop_partition`. |
| TC4 View parity: `audit_log_security` / `audit_log_library` views; `/api/security/audit` filters `category IN ('auth','admin','keys')` | **missing/incorrect** | **Views `audit_log_security` / `audit_log_library` do not exist** (grep → none). Endpoints query the base table directly: library handler filters `category='library'` (`libraries/audit.go:96`) — OK; but `securityaudit.ListRecent` filters `category=$1` with `$1='security'` (`securityaudit.go:150-153`), **not** `category IN ('auth','admin','keys')` as the AC requires — view-parity contract unmet. |
| EC3 Read-audit (every audit SELECT emits a `data` row) | **missing** | Neither `libraries/audit.go` Audit nor `auth.SecurityAudit` emits a `data.*_read` row after serving. Plan §7 read-audit unimplemented. |

## Story 21.7 — Job and pipeline visibility

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC1 `GET /api/queue/stats` returns counts by (stage,state,library), depth, oldest-pending age, in-progress, rolling 1h avg/stage, last 50 errors | **partial** | `jobs.Stats` (`handlers/jobs/jobs.go:361-457`) returns by-stage counts, total in-flight, oldest-pending age, a worker snapshot. **Gaps:** (a) no `(…, library_id)` dimension. (b) **no rolling 1-hour average per stage**. (c) **no `last_errors[]`** with `error_id/category/occurred_at` (the spec's join to `audit_log` on `event='job_error'` is not implemented; `last_error_id` not surfaced). The response shape (`StatsResponse`) differs from plan §2. |
| AC2 `GET /api/jobs/{id}` full state-machine history + per-segment progress + last heartbeat | **partial** | `jobs.Get` returns a flat `Job` with `last_heartbeat_at`. **Gaps:** **no `StateHistory`** (no `job_state_log` read), **no `SegmentProgress`** (no `job_segment_progress` table/read), no synthetic `stuck` state, no path masking. |
| AC3 `WS /ws/jobs?job_id=` filtered, ≤1 s latency, single endpoint | **partial** | `ws.go:120` registers `/ws/jobs` but as **SSE only** (`serveSSE("jobs")`), with **no `?job_id=` filter** (whole "jobs" channel), no per-second batching, no server-side authz filtering of events. WebSocket itself is explicitly not implemented (`ws.go:116-118`). |
| AC4 Admin panel charts + sortable job list | **missing** | No `web/src/admin/QueueDashboard.tsx` / `JobList.tsx` / `JobDetail.tsx` (plan §9 components absent). |
| TC4 No `/api/processing/*`; CI route-list lint | **partial** | No `/api/processing/*` route is registered (good), but the **route-namespace lint** (`plan §8`, `route_lint.go`, `make lint:routes`) **does not exist**, so TC4's enforcement clause fails. |
| EC1 stuck classifier / EC2 fan-out batching / EC3 path mask | **missing** | `jobs/stuck.go`, `jobs/path_mask.go` (plan §6–7) not present. |

## Story 21.8 — Privacy of telemetry

| AC | Status | Evidence / Gap |
|----|--------|----------------|
| AC1 No telemetry leaves host by default; master `[telemetry].outbound_enabled=false` | **partial** | De-facto default-off because tracing is a stub, no webhook, no Sentry. **Gap:** the explicit master switch `outbound_enabled` and the `shared/redact/go/outbound.go` gate (plan §6) **do not exist**; "off" is incidental, not enforced through a single switch. |
| AC2 Canonical redaction list enforced by CI lint over log/trace sites | **partial** | A redaction *set* exists (`shared/log/go/redact.go` `DefaultRedactedFields`; Python mirror) and is applied at handler level (`logger.go:164-166`). **Gaps:** no canonical `shared/redact/list.yaml`, no `forbidden_in_attrs`, no path-masking module, and **no CI lint** (`shared/redact/go/lint/log_lint.go`, `make lint:redact`) — TC2 cannot pass. |
| AC3 Leak-detector test scanning 1,000 lines for test secrets | **missing** | No `leak_detector_test.go` / `leak_detector_test.py` (grep → none). |
| AC4 Web `POST /api/telemetry/web-vitals` off-by-default + labelled in privacy UI | **missing** | Endpoint missing (see 21.2 EC3); no `web/src/lib/privacy_settings.ts` toggle. |
| EC1 Settings echo forbidden — return metadata only | **partial/divergent** | `settings.redactSecrets` (`handlers/settings/settings.go:203-216`) returns `"<redacted>"` plus a `*_present` boolean rather than the AC's `*_set` boolean and "metadata only" — the value is masked (not leaked) but the response still includes the key with a placeholder; behaviour close but not to spec, and there's no test asserting no configured-secret substring appears. |
| EC2 Stack paths under media root masked | **missing** | No `path_masker.go` / `Mask()`; stacks not emitted to a webhook anyway (21.5 missing). |
| EC3 Browser verbose off in prod builds | **missing** | No TS logger / build-time `VITE_LOG_LEVEL` (see 21.1 AC1). |

---

## Top gaps by impact

1. **Distributed tracing (21.3) is a total no-op stub.** `shared/tracing/go/tracer.go:61-68` returns a fake shutdown even when an OTLP endpoint is configured; there is no OTel SDK, exporter, sampler, gRPC/pgx instrumentation, Python tracer, or browser tracer. The epic's headline promise ("trace every request end-to-end, answer *why is it slow*") is unachievable. **AC1/2/3 missing; all TCs fail.**

2. **Error reporting & alerting (21.5) is entirely absent.** No `shared/errrpt`, no `error_id`/category/stack contract, no webhook, no Sentry, no cross-service correlation. A self-hoster opting into alerting gets nothing; "errors crossing service boundaries carry their `error_id`" (cited by 21.7 AC1 `last_errors`) is impossible.

3. **Audit log (21.6) lacks its core security guarantees.** The `audit_log` table is a flat, freely-mutable table: **no monthly partitioning, no `BEFORE UPDATE OR DELETE` append-only trigger, no `audit_log_security`/`audit_log_library` views, no file mirror/replayer, no read-audit, no retention.** An "append-only security log" that can be silently `UPDATE`/`DELETE`d is the single most dangerous gap — TC1 (append-only) fails outright and the endpoints diverge from the canonical taxonomy (`securityaudit` writes/reads `category='security'`, not the AC's `auth/admin/keys`).

4. **No enforcement tooling.** Every "a CI lint enforces this" clause across 21.1 (concat lint), 21.2 (cardinality lint), 21.7 (route lint), 21.8 (redaction lint + leak-detector) is unimplemented. Conventions are followed by hand today but nothing prevents regression.

5. **Streaming has no `/metrics`; baseline metric set is partial.** `streaming/main.go` mounts only health probes; `transcode_active_sessions`, `transcode_queue_depth`, `streaming_*`, and `pipeline_stage_duration_seconds` are unregistered. Pipeline ships a parallel hand-rolled registry with non-spec names. The metrics surface is not the cross-service-consistent surface 21.2 AC1 requires.
