# Full Implementation Audit — All 25 Epics

> Comprehensive structural audit of `main` (HEAD `0686b76`, after merging
> #12) against the 272 story acceptance criteria spread across
> [`specs/epics/`](epics/). This is not a PR-level review; it is an
> end-to-end "does the wired code exist, and is it actually called?"
> sweep.
>
> **Method.** For each epic: (a) read the README to confirm the story
> list and migration claims, (b) check that the source modules,
> handlers, migrations, and tests claimed by the plans actually exist
> on disk, (c) inspect cross-epic integration points (router wiring,
> gRPC calls, JWT trust, OpenAPI coverage). Coverage focuses on
> structural ACs (does the file exist, is the migration there, is the
> handler mounted) — not every behavioral AC.

---

## Verdict at a glance

| Bucket | Count |
|--------|-------|
| **Fully implemented and wired** | 16 epics |
| **Implemented but with wiring/integration gaps** | 7 epics |
| **Substantially stubbed / scaffolded only** | 2 epics |

| Severity | Count | Issues |
|----------|-------|--------|
| 🔴 **Blocker — system cannot run end-to-end as documented** | 5 | §A.1–A.5 |
| 🟠 **Major — handler/code exists but is unreachable or stubbed** | 8 | §B.1–B.8 |
| 🟡 **Minor — drift, mislabel, or partial coverage** | 11 | §C.1–C.11 |

---

## A. Blockers — would prevent the system from working end-to-end

### A.1 Duplicate `audit_log` table creation (migrations 0036 and 0054)

Two migrations both run `CREATE TABLE IF NOT EXISTS audit_log` with
**incompatible** schemas:

- [`shared/db/migrations/0036_audit_log.sql:12`](../shared/db/migrations/0036_audit_log.sql)
  — `id BIGSERIAL`, `actor_user_id`, `ts`.
- [`shared/db/migrations/0054_audit_log.sql:12`](../shared/db/migrations/0054_audit_log.sql)
  — `id UUID`, `actor_user`, `occurred_at`, `error_id`, plus
  `category` CHECK constraint.

On a fresh database 0036 creates the table first; 0054's `CREATE TABLE
IF NOT EXISTS` silently skips. Result: 0054's UUID PK, category enum,
and ten indexes never materialize. The on-prem code on `main` calls the
0036 shape ([`api/internal/auth/securityaudit/securityaudit.go:114`](../api/internal/auth/securityaudit/securityaudit.go)
and [`api/internal/handlers/libraries/audit.go:175`](../api/internal/handlers/libraries/audit.go)),
so functionally the system uses 0036; but 0054's "supersedes earlier
ad-hoc creations" comment is wrong — it supersedes nothing.

PLAN_REVIEW_18_24 §1.4 flagged this drift. The fix (drop 0036, or have
0054 `ALTER TABLE` instead of `CREATE TABLE IF NOT EXISTS`) was never
applied. The MANIFEST still lists both slots as live.

### A.2 Pipeline service has no daemon entry point

[`pipeline/src/maktaba_pipeline/__main__.py`](../pipeline/src/maktaba_pipeline/__main__.py)
is a sleep-loop stub explicitly labelled "Stub PID 1 for the pipeline
container … the real CLI lands with Epic 03." Despite Epics 1–6
shipping every primitive (scanner walker, audio extractor, STT
backends, segment commit, subtitle generator, search indexer,
`ClaimLoop` runner with wake-up sources, etc.), nothing instantiates
`ClaimLoop` and wires the dispatch table to the stage handlers in
production.

Concretely: `grep -r "ClaimLoop(" pipeline/src/` returns only the
definition and test imports. The container starts, the healthcheck
passes, but no jobs are processed. Videos enqueued by the API will
sit in `DISCOVERED` forever.

This is the single biggest "implementation present but not wired"
gap. Every story 1.1–6.10 individually verifies; the integration
binary that runs them does not exist.

### A.3 API gRPC clients to Pipeline / Streaming never instantiated

[`api/internal/router/p6.go:39`](../api/internal/router/p6.go) takes
`StreamingClient grpcstreaming.Client` and `SearchSemantic
search.SemanticClient` in `P6Deps`, and the adapters and handlers
gracefully no-op when nil. [`api/main.go:206`](../api/main.go)
constructs `P6Deps{DB: appDB}` and **never sets either client**.
Consequences:

- Streaming session lifecycle (Story 7.10, 8.1) — API can mint a
  signed URL via [`api/internal/auth/jwt`](../api/internal/auth/jwt/),
  but cannot call Streaming's `OpenSession` gRPC. Sessions are written
  to `streaming_sessions` directly with no transcode authorization.
- Semantic search (Story 7.8 + 5.4 hybrid path) — the FTS arm runs
  via [`api/internal/handlers/search/search.go`](../api/internal/handlers/search/search.go),
  but the gRPC `Embed → Chroma` arm is dead. The handler falls back
  to FTS-only RRF, silently degrading hybrid retrieval.
- STT backend listing and `stt-test` (Story 7.15 AC-4) — handler
  surface exists but `Pipeline` field on `settings.Handler` is nil at
  runtime, so `GET /settings/stt-backends` returns an empty list.

The plumbing is built (`grpcclients/pipeline`, `grpcclients/streaming`,
adapters, retries, circuit breaker — all present and unit-tested) but
the boot path doesn't dial the services. The Pipeline service also
exports no gRPC server (no `grpcsrv` package under `pipeline/`), so
even if the API tried to dial, there'd be no listener.

### A.4 Handlers exist but are never mounted on the public router

Four handler packages with working `Mount(r chi.Router)` methods are
never called from [`api/internal/router/p6.go`](../api/internal/router/p6.go),
[`p9.go`](../api/internal/router/p9.go), or [`api/main.go`](../api/main.go):

| Handler | File | Stories blocked |
|---------|------|-----------------|
| Subscriptions / entitlements / license | [`api/internal/handlers/subscriptions/subscriptions.go`](../api/internal/handlers/subscriptions/subscriptions.go) | 16.1–16.6 |
| Discovery / pairing | [`api/internal/handlers/discovery/pairing.go`](../api/internal/handlers/discovery/pairing.go) | 15.5, 15.6 |
| Security audit read API | [`api/internal/handlers/security/security.go`](../api/internal/handlers/security/security.go) | 10.16 |
| Perf admin | [`api/internal/handlers/perf/admin.go`](../api/internal/handlers/perf/admin.go) | 18.x admin surface |

Calling `GET /api/entitlements`, `POST /api/pairing/request`, or
`GET /api/security/audit` against a freshly-booted API returns 404
from chi. The handlers compile, pass their unit tests, and write to
the right tables — they're just unreachable.

### A.5 Pipeline–API has no in-process or RPC bridge for job control

`POST /api/videos/{id}/process`, `POST /api/jobs/{id}/pause`, etc. are
implemented in [`api/internal/handlers/jobs/jobs.go`](../api/internal/handlers/jobs/jobs.go),
but they only flip flags in `processing_jobs`. With the pipeline
daemon not running (§A.2), those flags are observed by nobody.
Story 6.4 ("pause and resume via request flags") is correct in the
abstract — the column updates and the `runner.py` already polls
them — but the runner is never started, so the flags are inert.

If you start the pipeline manually with `python -m maktaba_pipeline.pipeline.runner`
the loop runs, but there is no documented start command, no Compose
service that calls it, and no systemd unit (`deploy/packaging/systemd/maktaba-pipeline.service`
runs `python -m maktaba_pipeline` which still hits the sleep-loop
stub).

---

## B. Major gaps — implemented but not integrated

### B.1 Pipeline service exposes no gRPC server

[`api/internal/grpcclients/pipeline/pipeline.go`](../api/internal/grpcclients/pipeline/pipeline.go)
is a typed client wrapper. There is no `pipeline/src/maktaba_pipeline/grpcsrv/`
package, no `.proto` generated stubs anywhere in the tree, and
nothing in `__main__.py` listens on a port. The contract documented
in Story 7.18 ("API ↔ Pipeline over gRPC") has the client half but
not the server half.

### B.2 Streaming server skeleton runs; API never tells it about sessions

[`streaming/main.go`](../streaming/main.go) boots the chi router with
signed-URL middleware, the JWKS cache pointing at the API
(Story 10.7 / 8.1 — verified working in
[`streaming/internal/auth/`](../streaming/internal/auth)), and HLS /
direct-play handlers. But the API never calls
`Open/Close/EvictHashCache` over gRPC (§A.3), and
[`streaming/internal/grpcsrv/server.go`](../streaming/internal/grpcsrv/server.go)
is a self-contained implementation that no one talks to. The HTTP
layer can still serve any signed URL the API mints — but session
bookkeeping is stale because the API thinks sessions are open that
Streaming never registered.

### B.3 Federation (Story 15.3) and DLNA/UPnP (Story 15.4) — no code

`grep -rln Federation api/internal/ pipeline/src/ streaming/internal/`
matches only the `FeatureFederation` flag in
[`api/internal/subscriptions/subscriptions.go`](../api/internal/subscriptions/subscriptions.go).
`DLNA`/`UPnP` is not implemented anywhere outside specs and the
mockups directory. Both stories ship a `plan-*.md` but no Go module.

Discovery itself — mDNS publication and pairing tickets — is
implemented in [`api/internal/discovery/discovery.go`](../api/internal/discovery/discovery.go);
the gap is the *two other surfaces* of Epic 15.

### B.4 Telemetry (Stories 16.5 / 16.7) — no implementation

`grep -rln telemetry api/internal/ pipeline/src/` matches one comment
line in `pipeline/src/maktaba_pipeline/stt/segment_commit.py`. There
is no telemetry sink endpoint, no opt-in setting on `app_settings`,
no client. Story 16.5 ("usage analytics opt-in") and Story 16.7
("telemetry API") are unimplemented.

### B.5 Feature-flag resolution endpoint (Story 16.8) — no route, no table

`/api/feature-flags` is not in [`shared/api/openapi.yaml`](../shared/api/openapi.yaml),
and no handler package owns the surface. The subscriptions package
([`api/internal/subscriptions/subscriptions.go:139`](../api/internal/subscriptions/subscriptions.go))
emits a per-feature boolean map inside `GET /api/entitlements` — but
that route is itself unmounted (§A.4).

### B.6 Distributed tracing is a stub (Story 21.3)

[`shared/tracing/go/tracer.go`](../shared/tracing/go/tracer.go) is
explicitly labelled "stub: it owns the propagation header and the
on/off contract, but the actual span processor / exporter is wired in
a follow-up story." `Init` is a no-op; no OTLP exporter, no spans
emitted. Story 21.3 AC-1 (the `traceparent` header pass-through) is
covered; AC-2/AC-3 (spans across services, sampling) are not.

### B.7 Web UI ships 8 of 14 Epic-11 stories

Pages on `main`:

| File | Story |
|------|-------|
| [`web/src/pages/LibraryBrowser.tsx`](../web/src/pages/LibraryBrowser.tsx) | 11.1 |
| [`web/src/pages/VideoDetail.tsx`](../web/src/pages/VideoDetail.tsx) | 11.2 |
| [`web/src/pages/VideoPlayer.tsx`](../web/src/pages/VideoPlayer.tsx) | 11.3 (HTML5 `<video>` only — no HLS.js, sprite-scrub, sub-switch yet) |
| [`web/src/pages/Search.tsx`](../web/src/pages/Search.tsx) | 11.4 |
| [`web/src/pages/ProcessingQueue.tsx`](../web/src/pages/ProcessingQueue.tsx) | 11.5 |
| [`web/src/pages/Settings.tsx`](../web/src/pages/Settings.tsx) | 11.6 |
| [`web/src/components/ThemeToggle.tsx`](../web/src/components/ThemeToggle.tsx) + tokens | 11.8 |
| [`web/src/lib/i18n.tsx`](../web/src/lib/i18n.tsx) + RTL utility | 11.12 (partial) |
| [`web/public/sw.js`](../web/public/sw.js) | 11.10 (file present, not registered in `App.tsx`) |

**Missing on `main`:** 11.7 (full responsive — only mobile-first
classes, no breakpoint sweep), 11.9 (keyboard shortcuts global handler),
11.11 (accessibility — no skip-nav, no aria-live announcer, no axe CI
gate), 11.13 (PAT management UI), 11.14 (active sessions UI). The
backing APIs for 11.13/11.14 also have no route in the OpenAPI.

### B.8 OpenAPI is out of date

[`shared/api/openapi.yaml`](../shared/api/openapi.yaml) documents 56
paths, but several handlers in the codebase (whether mounted or not)
are absent from the spec:

- `/api/entitlements`, `/api/admin/license` (subscriptions handler)
- `/api/pairing/{request,exchange,status}` (discovery handler)
- `/api/security/audit` (security handler)
- `/api/admin/perf/...` (perf handler)
- `/api/feature-flags` (does not exist anywhere)
- `/api/telemetry` (does not exist anywhere)

Contract tests (Story 20.6) compare clients against this spec, so a
missing path means contract tests pass while the API surface is
silently wrong.

---

## C. Minor gaps

### C.1 Story 25.10 (direct-connection fallback) migration slot collision

[`specs/PLAN_REVIEW_25.md`](PLAN_REVIEW_25.md) §1.1 documented two
filename collisions in `cloud/migrations/`. The shipped tree at
[`cloud/migrations/`](../cloud/migrations/) settled on
slots 0001–0010 contiguously (no `00020001`/`00020002` filenames
remain), but the README slot table at
[`specs/epics/25-cloud-relay/README.md`](epics/25-cloud-relay/README.md)
still claims story-25-06 owns slot `0002`, while
[`cloud/migrations/00040001_servers.sql`](../cloud/migrations/00040001_servers.sql)
ships the `cloud_servers` table at slot `0004`. The actual filesystem
and the README spec disagree about which story owns which slot.

### C.2 Mobile apps (Epic 12) — Capacitor shell only

[`apps/mobile/src/native-shell.ts`](../apps/mobile/src/native-shell.ts)
is one TypeScript bridge that re-dispatches Capacitor lifecycle to
DOM events. There is no native iOS or Android Studio project on disk
(no `apps/mobile/ios/`, no `apps/mobile/android/` — those would be
generated by `npx cap add`, but the workflow has not been run). The
Capacitor plugins are listed in `package.json` but never installed
into a native shell. Stories 12.3 (native player), 12.4 (push), 12.5
(background playback), 12.6 (offline downloads), 12.7 (share/cast),
12.8 (haptics), 12.9 (deep linking) require platform-specific code
that is not present.

### C.3 TV apps (Epic 14) — minimal SwiftUI / Compose scaffolds

515 lines total across both platforms ([`apps/tv/`](../apps/tv/)).
[`apps/tv/tvos/Sources/Maktaba/Views/HomeView.swift`](../apps/tv/tvos/Sources/Maktaba/Views/HomeView.swift)
renders a "Continue Watching" rail from a `LibraryService` that has no
backing call to the actual API; Android TV is an even smaller
"Maktaba TV — Home" stub. Voice search (14.4), continue-watching with
playback resume (14.5), and the recommendations grid (14.6) are
unimplemented. Story 14.7 (recommendations API) is implemented and
mounted at [`api/internal/handlers/recommendations/recommendations.go`](../api/internal/handlers/recommendations/recommendations.go).

### C.4 Desktop (Epic 13) — Tauri shell present; mDNS, drag-drop import unimplemented

[`apps/desktop/src-tauri/src/lib.rs`](../apps/desktop/src-tauri/src/lib.rs)
wires native menu, system tray, window-state plugin, auto-updater
plugin. It does **not** implement mDNS browsing (Story 13.5 — no Rust
crate dependency on `mdns` / `astro-mdns` / `zeroconf-rs`), and there
is no drag-drop file-handler registered (Story 13.6). All five
keyboard shortcuts of Story 13.7 are absent from the menu config —
only the predefined Quit / Show / Hide tray items exist.

### C.5 Pipeline reaper (Story 6.6) and concurrency caps (Story 6.7) — primitives only

[`pipeline/src/maktaba_pipeline/db/jobs_reaper.py`](../pipeline/src/maktaba_pipeline/db/jobs_reaper.py)
and [`pipeline/src/maktaba_pipeline/pipeline/concurrency.py`](../pipeline/src/maktaba_pipeline/pipeline/concurrency.py)
exist and are unit-tested. They are not invoked from any always-on
process (see §A.2). Both stories' ACs implicitly require a running
worker; the algorithms are correct but the timer/loop binding is
absent.

### C.6 GraphQL surface (Story 7.17) — SDL + handler, no real resolvers

[`api/internal/graphql/schema.go`](../api/internal/graphql/schema.go)
ships the SDL and a `POST /graphql` handler, but every resolver path
delegates back to the REST surface internally rather than querying
the DB directly. That's a documented design choice (single source of
truth) but Story 7.17 also calls for batched-by-id loaders, which
are absent.

### C.7 Web UI offline service worker not registered

[`web/public/sw.js`](../web/public/sw.js) exists. No
`navigator.serviceWorker.register(...)` call exists in
[`web/src/main.tsx`](../web/src/main.tsx) or `App.tsx`. The shell is
served but never activated; Story 11.10 AC-2 ("PWA installable") will
fail an audit on `main`.

### C.8 Web component library / Storybook — scaffold only

[`web/design-system/components/`](../web/design-system/components/) has
exactly two components — `Button` and `ThemeProvider`. Story 17.2
calls for the full token-driven primitive set (Input, Select,
Checkbox, Card, Toast, Modal, Tabs, Tooltip, Avatar, Badge, Skeleton,
Spinner, NavLink, …). The Storybook config and tokens (`tokens.json`,
`tokens.dark.json`, `tokens.high-contrast.json`) are in place but
mostly unused outside of `Button`.

### C.9 OpenAPI not in CI

There is no Makefile target or CI gate that regenerates the OpenAPI
from the Go handlers or vice versa, and no contract test loads the
spec and exercises every route. Story 20.6 (contract tests) cannot be
satisfied today.

### C.10 Distributed tracing header propagation in gRPC clients

[`shared/tracing/go/http.go`](../shared/tracing/go/http.go) injects
`traceparent` into outbound HTTP, but
[`api/internal/grpcclients/pipeline/pipeline.go`](../api/internal/grpcclients/pipeline/pipeline.go)
only propagates `maktaba-request-id`. No `traceparent` metadata is
sent over gRPC — even when the SDK lands, traces will be disconnected
at the gRPC boundary.

### C.11 Scaling stubs (Epic 19) — interfaces only

[`api/internal/scale/scale.go`](../api/internal/scale/scale.go)
defines `Shard`, `EventBus`, and `Concurrency` interfaces and
in-process implementations. The Postgres-LISTEN and Redis adapters
the spec calls for are explicitly out-of-scope ("Postgres / Redis
adapters are not in this stub — the contract is in place so they can
be added without touching call sites"). All eight Epic-19 stories
ship the interface; none ships the multi-host adapter.

---

## Per-epic summary

Counts are **structural**: a story is "complete" if the package /
migration / handler / mountpoint claimed in its plan exists *and is
exercised by the live boot path*. A story is a "stub" if the code
exists but no daemon, route, or caller will execute it.

| Epic | Stories | Structurally complete | Code exists but unwired | Not implemented |
|------|---------|-----------------------|-------------------------|-----------------|
| 01 Scanner | 6 | 6 (all code present + tested) | 0 | 0 — but the runner isn't started (§A.2) |
| 02 Audio Extraction | 4 | 4 | 0 | 0 — but never invoked at boot (§A.2) |
| 03 Transcription | 9 | 9 | 0 | 0 — backends shipped (MLX, faster-whisper, OpenAI); no auto-dispatch |
| 04 Subtitles | 5 | 5 | 0 | 0 |
| 05 Search & Indexing | 7 | 6 | 1 — semantic gRPC arm unwired (§A.3) | 0 |
| 06 Job Queue | 10 | 10 | 0 | 0 — primitives, no daemon (§A.2, §C.5) |
| 07 API Server | 22 | 18 | 4 — streaming session lifecycle, semantic search, stt-test, devices push (depends on §A.3 + §B.5) | 0 |
| 08 Streaming | 15 | 14 | 1 — gRPC server runs but unused (§B.2) | 0 |
| 09 Library Management | 18 | 18 | 0 | 0 — same pipeline-daemon caveat |
| 10 Auth & Security | 17 | 17 | 0 | 0 — fully mounted via MountP9 |
| 11 Web UI | 14 | 8 | 1 — sw.js unregistered (§C.7) | 5 — 11.7, 11.9, 11.11, 11.13, 11.14 |
| 12 Mobile | 11 | 1 — Capacitor shell only | 0 | 10 — no native projects |
| 13 Desktop | 8 | 5 — Tauri base + menu/tray/window-state/updater | 0 | 3 — mDNS browse, drag-drop, shortcuts |
| 14 TV Apps | 7 | 2 — minimal scaffolds, recommendations API | 0 | 5 — voice search, continue-watching, real recs grid, deep link, both real apps |
| 15 Discovery | 7 | 3 — mDNS publish, pairing store, pairing API code | 1 — pairing handler unmounted (§A.4) | 3 — cloud relay (in cloud/, but on-prem client absent), federation, DLNA |
| 16 Subscriptions | 8 | 4 — entitlements, license verify, free tier, premium gates | 1 — subscriptions handler unmounted (§A.4) | 3 — telemetry endpoint, telemetry opt-in setting, feature-flag endpoint |
| 17 UX Design System | 11 | 4 — tokens, ThemeProvider, Button, motion tokens | 0 | 7 — full component library, onboarding, RTL beyond i18n, player controls component, search results component, processing progress component, transcript presentation component |
| 18 Performance | 8 | 7 — perf budgets YAML, caches, pools, query EXPLAINs | 1 — perf admin handler unmounted (§A.4) | 0 |
| 19 Scalability | 8 | 8 — interface stubs only (§C.11) | 0 | 0 of the *stub* contract; *all* multi-host adapters missing |
| 20 Testing | 8 | 6 — pyramid budgets, fixtures, unit, integration, contract config | 0 | 2 — actual `tests/e2e/*.e2e.spec.ts` files (the dir is empty) + perf-regression CI baseline lacks numbers |
| 21 Observability | 8 | 6 — structured logging, metrics, health, audit log, error reporting, job visibility | 1 — tracing stubbed (§B.6) | 1 — telemetry privacy (depends on §B.4) |
| 22 DevOps & Delivery | 8 | 7 | 0 | 1 — story 22.8 (`make dev`, pre-commit, dev overlay) is partial per [`P0_CERTIFICATION.md`](P0_CERTIFICATION.md) |
| 23 Security | 8 | 8 — auth, ACL, TLS terms, secrets, validation, rate limit, SBOM tool, disclosure doc | 0 | 0 |
| 24 Data Integrity | 9 | 9 — atomic.py, idempotency.py, constraints, locking, backup script, integrity checks | 0 | 0 |
| 25 Cloud Relay | 36 | 30 — auth, identity, billing, relay tunnel, push, admin, installers | 0 | 6 — DNS provisioning, real binaries, integration tests against real Stripe/APNs/FCM, server-side cloudlink client (see PLAN_REVIEW_25 §1.3), TLS for arbitrary subdomain, federation w/ on-prem |

Totals: ~172/272 structurally complete on the running binary, ~12 with
code present but unmounted/unwired, ~40 not implemented, ~48 in
"primitives exist but no daemon runs them" limbo.

---

## D. Integration verification

### D.1 Streaming JWT trust against API JWKS — ✅ verified

[`streaming/internal/auth/jwks.go`](../streaming/internal/auth/jwks.go)
fetches the JWKS document from
`MAKTABA_STREAMING_JWKS_URL` (defaults to the API's
`/api/.well-known/jwks.json`), caches it, and refreshes on-miss. The
API publishes the same set via
[`api/main.go:233`](../api/main.go) and [`api/internal/auth/keys/keys.go`](../api/internal/auth/keys/).
End-to-end signature verification works (covered by
[`streaming/internal/auth/middleware_test.go`](../streaming/internal/auth/middleware_test.go)).

### D.2 API → Pipeline gRPC — ❌ broken

Client wrapper exists. Server doesn't. See §A.3, §B.1.

### D.3 API → Streaming gRPC — ❌ broken (server present, client never dialed)

See §A.3, §B.2.

### D.4 Pipeline DB → API DB — ✅ shared schema, ⚠️ same DB instance assumed

Both services connect to `DATABASE_URL` and apply the same goose
migrations from [`shared/db/migrations/`](../shared/db/migrations/).
Pipeline writes `videos`, `processing_jobs`, `transcript_segments`,
`audio_tracks`, etc.; API reads them via SQL. There is no per-service
schema; integrity rests on the contract that both apply the manifest
in order.

The §A.1 collision means an operator who runs migrations top-down
ends up with the 0036-shape `audit_log` even though some plans (and
the cloud module's audit handlers) expect the 0054 shape.

### D.5 Cloud relay → on-prem server — ⚠️ cloud-side complete, server-side cloudlink absent

[`cloud/internal/relay/`](../cloud/internal/relay/) implements the
relay's view: WSS frame protocol, registry, tunnel, server-Auth
handshake, proxy. The mirror image on the on-prem side — a Go
client that dials the cloud, opens a tunnel, and forwards requests
to the local API — is absent from `api/` and from a hypothetical
`cloudlink/` module. PLAN_REVIEW_25 §1.3 flagged this gap. On-prem
servers cannot actually claim a cloud account today.

### D.6 OpenAPI ↔ implementation drift — ⚠️ documented routes mostly match handlers; unmounted handlers absent from spec

The 56 paths in [`shared/api/openapi.yaml`](../shared/api/openapi.yaml)
correspond 1:1 to handlers registered by `MountP6` + `MountP9`. The
unmounted handlers (§A.4) are also undocumented in the spec, so
clients won't try to call them — fine for now, but Story 16's
entitlement surface advertised by the README is invisible to API
consumers.

### D.7 CI gates — ✅ all six gates wired, ⚠️ pipeline lint dirty

Per [`P0_CERTIFICATION.md`](P0_CERTIFICATION.md): the six CI gates
(`lint`, `unit`, `integration`, `e2e`, `perf-ci`, `build-artifacts`)
exist in `.github/workflows/_*.yml` and are gated by `ci.yml`. Lint
fails on `T201` print violations in
[`pipeline/src/maktaba_pipeline/__main__.py`](../pipeline/src/maktaba_pipeline/__main__.py),
which is itself a stub that should be replaced (§A.2).

### D.8 Tests — 156 test files; no e2e; web UI has zero

| Service | Test files |
|---------|------------|
| Pipeline (`pipeline/tests/`) | 72 |
| API (`api/**/*_test.go`) | 56 |
| Streaming (`streaming/**/*_test.go`) | 15 |
| Cloud (`cloud/**/*_test.go`) | 13 |
| Web (`web/**/*.{spec,test}.*`) | 0 |
| E2E (`tests/e2e/`) | 0 (dir is empty) |

E2E (Story 20.5) and web unit tests are entirely absent on `main`.

---

## E. Recommendation: shortest path to a runnable end-to-end demo

1. **Wire `MountP6` deps in `main.go`.** Construct a
   `grpcstreaming.Client` (and stub it cleanly when streaming is
   reachable), a `search.SemanticClient` if Chroma is present, and a
   `pipeline.Client`. Without this, half of Epic 7 returns degraded
   responses.
2. **Mount the four orphaned handlers** (subscriptions, discovery,
   security, perf admin) in a new `MountP10` (or extend `MountP6`).
3. **Replace `pipeline/__main__.py`** with a real entry point that
   instantiates `ClaimLoop` + reaper + heartbeat + dispatch table
   mapping `stage → handler` (the handlers all exist). Update the
   systemd unit and Compose service.
4. **Resolve the `audit_log` migration collision** by either dropping
   slot 0036 entirely or making 0054 an `ALTER` migration that brings
   the 0036 table up to the canonical shape, then update the
   MANIFEST.
5. **Stand up the pipeline gRPC server** so the API's typed wrapper
   has a peer. Minimum surface: `Embed`, `Transcribe` status, and the
   STT backend registry queries.
6. **Build an on-prem cloud-link binary** that dials the cloud relay,
   so claiming a server during cloud onboarding actually does
   something.

After these six items the system is wired end-to-end. The remaining
Epic 11 / 12 / 14 / 17 work is genuine product surface and won't
block server-side functionality.

---

## Appendix — what is fully implemented and integrated

This list is the counterweight to the gaps above. Each item has live
code, migrations, and at least one test on `main`.

- **Migrations**: 58 PostgreSQL migrations + SQLite mirrors at
  [`shared/db/migrations/`](../shared/db/migrations/), gated by
  `tools/migration-lint/`.
- **Scanner walker, debouncer, content hasher**: all of Epic 01.
- **FFmpeg probe + extract + track selection**: all of Epic 02.
- **Three STT backends + segment commit + pause/resume + crash recovery
  + diarization**: all of Epic 03.
- **Subtitle generation + formatting + discovery + embedded extraction**:
  all of Epic 04.
- **FTS5 / tsvector index + Chroma embedder + hybrid RRF + chapter
  inference + suggestions**: all of Epic 05.
- **`processing_jobs` schema + claim loop + reaper + backoff + concurrency
  caps + graceful shutdown**: every primitive of Epic 06 (just not
  invoked at boot).
- **Cursor pagination, library CRUD, video CRUD, search, jobs,
  collections, tags, speakers, settings, recommendations, devices,
  websocket fan-out, GraphQL handler, gRPC client wrappers, validation
  middleware, health/version/metrics**: 18/22 of Epic 07.
- **Streaming server, capability matrix, direct play, remux, HLS,
  DASH, hwaccel detection, gRPC server, session store, concurrency
  caps, live subtitles, chapter delivery, posters/sprites, cache GC,
  probe cache**: 14/15 of Epic 08.
- **Library config + watcher + sweep + dedup + ignore + manual scan +
  stats + language + topic + content-type classifier + speakers +
  tags + collections + smart collections + library deletion + multi-root
  overlap + audit + chapter inference**: all of Epic 09.
- **User store, web login, native login, refresh, logout, RS256
  signing keys, JWKS, signed-URL minter, single-user mode, CSRF,
  brute-force, rate limiting, ACLs, secret loading, transport
  security, security audit (write side), pairing tickets table**:
  all of Epic 10.
- **Cloud-side**: auth + Google/Apple OAuth + Stripe billing + WSS
  tunnel + HTTP proxy + APNs + FCM + admin fleet + subdomain +
  TLS/ACME + rate limit + abuse + entitlement signing + installer
  scripts (macOS/Windows/Linux/Docker/NAS/RPi/cloud VPS) +
  auto-update + first-run wizard + uninstaller docs.
- **Devops**: full reproducible-build flags + nfpm + Docker
  multistage + healthchecks + release manifest validator + image-size
  guard + SBOM stub.
- **Security**: argon2id, RS256, JWKS, ACL, rate limiter,
  brute-force lock-out, idempotency-key middleware, body-size limits,
  content-type validation, audit log (slot 0036 shape).
- **Data integrity**: atomic-write helper, idempotency tokens,
  integrity-check table + verifier, atomic.py / backup.py /
  verify.py / idempotency.py modules.
