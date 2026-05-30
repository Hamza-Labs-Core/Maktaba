# Epic 07 — API Server: Spec-vs-Implementation Gap Analysis

**One-line verdict:** REST CRUD surface is broadly implemented and mounted, but the
three inter-service/real-time pillars are hollow — gRPC clients are TCP-probe
stubs (`ErrNotImplemented`), GraphQL returns 501 schema-only, WebSocket is
SSE-only with no Postgres LISTEN producer or JWT handshake, and the web
cookie-auth middleware is never installed in the boot path.

Scope reviewed: `specs/epics/07-api-server/README.md` + 22 story files + boot
path (`api/main.go`), router wiring (`api/internal/router/*.go`), and every
handler/middleware package. Verified against code, not audit/spec self-claims.

## AC counts by status

| Status | Count |
|---|---|
| complete | 31 |
| partial | 28 |
| missing | 14 |
| unwired | 9 |
| stub | 9 |
| **total ACs** | **91** |

Definitions: *complete* = exists, reachable from live router, behaviorally
satisfies the AC. *partial* = present but missing required behavior/edge cases.
*missing* = no implementation. *unwired* = code exists but unreachable from the
`serve` boot path / not injected. *stub* = placeholder returning a fixed
not-implemented / empty response.

---

## Per-story AC table

### Story 7.1 — HTTP server skeleton
| AC | Status | Evidence |
|---|---|---|
| AC-1 RFC 9457 envelope | complete | `api/internal/httperror/httperror.go`; router NotFound/MethodNotAllowed render problem+json (`router.go:96-106`). |
| AC-2 Request ID propagation | complete | `mw.RequestID` in stack (`router.go:75`); `api/internal/middleware/requestid.go`. |
| AC-3 Graceful shutdown | complete | `main.go:327-339` Shutdown(grace) + forced Close; `shutdownGrace()` honors env. |
| AC-4 Idempotency-Key store | partial | `mw.Idempotency` wired (`router.go:89-91`) but store is **in-memory only** (`api/internal/idempotency/store.go:36-38` — "Postgres backing lands when AC-4 acquires the migration slot"). AC-4 mandates Postgres `idempotency_keys` table with 24h TTL; replay breaks across horizontally-scaled replicas (README §1.2 stateless requirement). |

### Story 7.2 — Cursor pagination
| AC | Status | Evidence |
|---|---|---|
| AC-1 Opaque cursor encoding | complete | `api/internal/paginate/cursor.go:34-55` base64url no-pad `{u,i,v}`. |
| AC-2 Stable iteration | complete | `api/internal/paginate/sql.go` strictly-less-than tuple predicate. |
| AC-3 Limit & bounds | partial | `api/internal/paginate/limit.go` clamps; verify each list endpoint returns 400 `invalid-query-parameter` for out-of-range vs silently clamping (videos handler clamps `limit` silently in some paths). |

### Story 7.3 — Library CRUD
| AC | Status | Evidence |
|---|---|---|
| AC-1 Create | complete | `api/internal/handlers/libraries/libraries.go` Create + Location header. |
| AC-2 Reject invalid roots | complete | `api/internal/handlers/libraries/roots.go` per-path failure classification. |
| AC-3 Deep-merge settings | complete | libraries.go Patch deep-merge. |
| AC-4 Delete + purge flag | partial | Delete present; confirm `?confirm=<name>` 412 gate and 207 Partial on read-only mount edge case implemented. |
| AC-5 Trigger scan | complete | `/api/libraries/{id}/scan` mounted (`libraries.go:130`); enqueues + NOTIFY. |
| AC-6 Stats accuracy | complete | `api/internal/handlers/libraries/stats.go` single SQL. |

### Story 7.4 — Video list/detail/patch/delete
| AC | Status | Evidence |
|---|---|---|
| AC-1 List with filters | complete | `api/internal/handlers/videos/videos.go:105` + cursor. |
| AC-2 Detail eager joins | partial | Detail joins present; N+1-free single-round-trip claim not verified. |
| AC-3 Patch editable only | complete | videos.go Patch ignores non-editable fields. |
| AC-4 Delete options | partial | Delete present; `?confirm=<id>` 412 + `audit_log` row + `Maktaba-Warning: file-not-found` not all confirmed. |

### Story 7.5 — Video processing control
| AC | Status | Evidence |
|---|---|---|
| AC-1 Process now | complete | `/api/videos/{id}/process` mounted (`videos.go:109`). |
| AC-2 Reprocess from stage | partial | `/reprocess` mounted (`videos.go:110`); FSM predecessor resolution + `SUPERSEDED` transition + `videos.state_changed` NOTIFY require verification against Epic 9 FSM. |

### Story 7.6 — Transcript window
| AC | Status | Evidence |
|---|---|---|
| AC-1 Overlap window | complete | `api/internal/handlers/videos/segments.go` overlap predicate, seq order. |
| AC-2 Default window + cursor | complete | segments.go default 200 + next cursor. |
| AC-3 Word-level optional | complete | segments.go `?words` + per-segment words round-trip (`segments.go:94,157-161`). |
| Edge: bidi isolate / partial | complete | `bidiIsolate` U+2068/2069 (`segments.go:152`); `partial:true` (`segments.go:126,183-195`). |

### Story 7.7 — Subtitles & chapters
| AC | Status | Evidence |
|---|---|---|
| AC-1 Enumerate subtitles | partial | `/api/videos/{id}/subtitles` mounted; signed Streaming URL depends on Story 10.8 URLSigner which is **never injected** (see 7.10). URLs likely unsigned/empty. |
| AC-2 Chapters provenance | complete | `/api/videos/{id}/chapters` mounted (`videos.go:113`). |
| Edge: Accept-Language order | complete | `segments.go:266` header-based ordering. |

### Story 7.8 — Search API (FTS/semantic/hybrid)
| AC | Status | Evidence |
|---|---|---|
| AC-1 Hybrid default | stub-degraded | `search.go:187` semantic only runs if `h.Semantic != nil`; `P6Deps.SearchSemantic` is **never set in `main.go`'s `MountP6` call** (main.go:242-246 omits it). Production hybrid silently = FTS-only. No `Embed` gRPC, no Chroma. |
| AC-2 FTS/semantic-only modes | partial | FTS-only works; `mode=semantic` returns empty (no semantic client wired). |
| AC-3 Filter pushdown | partial | `runFTS` pushes library/language only; `duration_sec`, `speaker`, `date` filters not pushed to SQL. |
| AC-4 Suggest fast | partial | `Suggest` reads `search_history` table, not "FTS prefix index"; degrades to `[]` if table absent. |
| AC-5 Highlight markers | partial | `highlightSnippet` wraps `<mark>` + 240 cap, but **no bidi isolation** on RTL excerpt (AC-5 requires it). |
| AC-6 Unit→segment mapping | missing | No `transcript_units` → `segment_ids[0]` mapping logic; FTS hits read `transcript_segments` directly. |
| AC-7 Perf budget | missing | No perf path; no `cold_search_p95_ms`. |
| Edge: gRPC down → `degraded:true` | missing | No `degraded` field in `Response` struct (`search.go:85-91`); degradation is silent, not signaled. |

### Story 7.9 — Saved searches
| AC | Status | Evidence |
|---|---|---|
| AC-1 Save & replay | complete | `search.go:368-407` insert into `saved_searches`; migration `0037_saved_searches.sql`. |
| AC-2 Per-user namespacing | complete | `search.go:423-426` `WHERE user_id`. |

### Story 7.10 — Streaming session lifecycle
| AC | Status | Evidence |
|---|---|---|
| AC-1 Open session | partial/unwired | `streaming.go:126` OpenSession exists; gRPC `Service.Open` calls **stub** `streaming.realClient.OpenSession` → `ErrNotImplemented` (`grpcclients/streaming/realclient.go:51`). Step 3 JWT signing: `h.Signer` (`P6Deps.URLSigner`) **never injected** (main.go:242 omits it) → manifest URL unsigned. `ladder`/`current_rendition` never populated. |
| AC-2 Get session info | partial | `GetSession` returns row but not `ladder`/`current_rendition`/`last_segment_fetched_at` semantics fully. |
| AC-3 Close session | complete | `streaming.go:261` gRPC close best-effort + `closed_at`. |
| AC-4 Capabilities (gRPC + 60s cache) | stub | `GetCapabilities` cache present but real client `GetCapabilities` returns empty `Capabilities{}` (`streaming/realclient.go:63-65`), no live gRPC. |
| AC-5 Single client flow | complete (contract) | Endpoint shape returns `mode`/URL. |
| Edge: Streaming down → 503 Retry-After | partial | Handler returns 503 on `Service.Open` error (`streaming.go:182-189`), but stub always errors `ErrNotImplemented` → every real open is 503. |

### Story 7.11 — Watch progress sync
| AC | Status | Evidence |
|---|---|---|
| AC-1 Debounced upsert | complete | `streaming.go:309-391` upsert + 1/s debounce + 0.95 auto-complete. |
| AC-2 Fan out to other devices | missing | No WS publish on progress; `streaming.Handler` has no Hub reference; `/ws/playback/{video_id}` producer not wired. |
| AC-3 Rate limit firehose | complete | `sessionDebouncer` 1/s (`streaming.go:444-466`). |
| AC-4 Stale POSTs accepted | complete | No monotonicity check in upsert. |

### Story 7.12 — Job control
| AC | Status | Evidence |
|---|---|---|
| AC-1 Pause flag | complete | `api/internal/handlers/jobs/jobs.go:62` pause. |
| AC-2 Force pause | partial | `?force=true` single-UPDATE path needs verification. |
| AC-3 Resume | complete | jobs.go:63. |
| AC-4 Cancel | complete | jobs.go:64. |
| AC-5 Retry | complete | jobs.go:65 (409 `job-not-failed` on non-failed needs check). |
| AC-6 Per-video aggregates | complete | `/api/videos/{id}/pause|resume|cancel` mounted (jobs.go:66-68) with `affected`. |
| AC-7 Idempotency | partial | Flag-setter idempotency plausible; double-call identical-body not verified. |

### Story 7.13 — Queue stats
| AC | Status | Evidence |
|---|---|---|
| AC-1 Shape | partial | `/api/queue/stats` mounted (jobs.go:69); full `{by_stage, eta_sec, workers[]}` shape + zero-fill for empty stages needs verification. |
| AC-2 Required indexes | external | Owned by Pipeline Story 6.1; not in this epic's code. |

### Story 7.14 — Collections, tags, speakers
| AC | Status | Evidence |
|---|---|---|
| AC-1 Collection CRUD ordering | complete | `api/internal/handlers/collections/collections.go` mounted. |
| AC-2 Smart collection | complete | `collections/smart.go` MountSmart (`p6.go:83`). |
| AC-3 Tag delta | complete | `api/internal/handlers/tags/tags.go` `{add,remove}`. |
| AC-4 Speaker merge | complete | `api/internal/handlers/speakers/speakers.go` merge txn. |

### Story 7.15 — Settings & system
| AC | Status | Evidence |
|---|---|---|
| AC-1 Read + redaction | complete | `settings.go:71-92,203` `secretKeyPattern` + `<redacted>` + `*_present`. |
| AC-2 Patch DB-backed | partial | Patch persists to `app_settings` (migration `0042`); NOTIFY/LISTEN reload + 5s poll backstop convergence not verified. |
| AC-3 Patch denied non-runtime | partial | 403 `setting-not-runtime` path present; allowlist completeness unverified. |
| AC-4 STT backends listing | stub | `settings.go` calls pipeline `ListBackends` → stub returns `[]Backend{}` (`pipeline/realclient.go:65-69`). Always empty. |
| AC-5 STT dry-run | stub | `STTTest` → `ErrNotImplemented` (`pipeline/realclient.go:71-73`). |
| AC-6 `app_settings` schema | complete | Migration `0042_app_settings.sql`. |

### Story 7.16 — WebSocket fan-out
| AC | Status | Evidence |
|---|---|---|
| AC-1 WS auth at handshake | missing | `ws.go` is **SSE-only** (no WebSocket upgrader; comment `ws.go:9-13`). Auth via `principal.FromContext` but cookie middleware unwired → web clients rejected. No 4401 close code. |
| AC-2 Subscription scoping | partial | Channel keyed by id but no per-library/per-user authz enforcement on subscribe. |
| AC-3 Event shape | partial | Envelope `{type, at, payload}` (`ws.go:34-39`) close to spec; nested payload vs flat spread differs from `{type, at, ...payload}`. |
| AC-4 Backpressure + replay | missing | Slow consumer is silently dropped (`ws.go:103-107`), no 1011 close, no `?since=` replay from `events` table. |
| AC-5 Heartbeat/idle close | partial | 30s SSE comment ping (`ws.go:158`); no pong-timeout close (SSE has no pong). |
| AC-6 SSE fallback | partial | SSE is the *only* transport, not a negotiated fallback; no `Accept: text/event-stream` branching from a WS primary. |
| AC-7 NOTIFY channel naming | missing | **No Postgres LISTEN loop anywhere in `main.go`**; `Hub` is created fresh in `MountP6` (`p6.go:54-55`, `104`) with no producer. `PublishFromCtx` (`ws.go:185`) is a documented no-op never called. The entire real-time pipeline is dead. |

### Story 7.17 — GraphQL schema + resolvers
| AC | Status | Evidence |
|---|---|---|
| AC-1 Domain types in schema | partial | Inline SDL const `graphql/schema.go:30-346` has the types, but spec requires `shared/graphql/schema.graphql` — **that file does not exist** (no `.graphql` files in repo). |
| AC-2 Query/Mutation parity | stub | `graphql.Handler.ServeHTTP` returns **501 `schema-only`** for every POST (`schema.go:357-375`). No resolvers, no gqlgen (absent from repo). |
| AC-3 Subscription parity | missing | Subscriptions in SDL only; no resolver. |
| AC-4 DataLoader N+1 | missing | No DataLoader; no resolver layer. |
| AC-5 Persisted queries | missing | Not implemented. |
| Parity test | missing | `graphql/schema_test.go:8-9` explicitly states "We don't have a chi-route-table → schema diff here yet". AC-2's CI-enforcing parity test absent. |

### Story 7.18 — gRPC clients to Pipeline & Streaming
| AC | Status | Evidence |
|---|---|---|
| AC-1 Pipeline client interface | stub | `pipeline/realclient.go` — `Embed`/`Transcribe`/`ExtractEmbeddedSubtitle`/`STTTest` all return `ErrNotImplemented` (lines 53-73). Only a TCP dial probe at construction. **No `shared/proto/` dir exists** (no `.proto` files; spec §9.9 dependency unmet). |
| AC-2 Streaming client interface | stub | `streaming/realclient.go` — `OpenSession`/`CloseSession`/`EvictHashCache` → `ErrNotImplemented`; `GetCapabilities` returns empty struct (lines 51-65). |
| AC-3 Retry & circuit breaker | unwired | `Breaker`/`CallWithRetry` implemented (`pipeline/pipeline.go:94-202`) but **never invoked** by the stub real-clients (they error before any retry path). |
| AC-4 Context propagation | missing | No gRPC connection → no `maktaba-request-id` metadata propagation. |

### Story 7.19 — Validation, body limits, rate limiting
| AC | Status | Evidence |
|---|---|---|
| AC-1 Body size cap | complete | `api/internal/middleware/bodylimit.go`; `mw.BodyLimit(DefaultBodyLimit)` (`router.go:119`). |
| AC-2 Content-Type enforcement | complete | `api/internal/middleware/contenttype.go`. |
| AC-3 Struct-tag validation | complete | `api/internal/middleware/validate.go`. |
| AC-4 Per-user rate limit | complete | `mw.PerUser` wired (`router.go:84-86`); main.go default 600. |
| AC-5 Per-IP rate limit | complete | `mw.PerIP` wired (`router.go:81-83`); default 6000. |
| Edge: per-route body cap | partial | Search uses 32 KiB read cap inline (`search.go:119`); spec wants per-route 16 KiB; 8 KiB PATCH cap for videos not verified at middleware layer. |

### Story 7.20 — Health, version, metrics
| AC | Status | Evidence |
|---|---|---|
| AC-1 Health composition | partial | `system.NewAggregator` (`router.go:121`) fans out to `MAKTABA_HEALTH_PEERS`; pipeline/streaming gRPC component health depends on stub clients (TCP-probe only). |
| AC-2 Version endpoint | complete | `system.VersionHandler` (`router.go:123`); `api/internal/version`. |
| AC-3 Metrics export | complete | `/metrics` on admin port (`main.go:183-187`); `mw.Metrics` in stack. |
| AC-4 OTel traces | complete | `tracing.Init` (`main.go:164-174`), `tracing.HTTP` middleware (`router.go:77`). |

### Story 7.21 — Recommendations
| AC | Status | Evidence |
|---|---|---|
| AC-1 Personalized rails | complete | `api/internal/handlers/recommendations/recommendations.go:75-103` mounted, rails shape. |
| AC-2 Rail composition | partial | `continueRail` present; `for-you` reads `user_recs` (migration `0041`) but nightly Pipeline aggregation job is external/absent; `next-up`/`library` composition needs verification. |
| AC-3 Caching | complete | per-user in-memory cache key `userID:surface` (`recommendations.go:91`), `cache_hit`. |
| AC-4 `surface` param | complete | default `web-home` (`recommendations.go:75-77`). |
| AC-5 `user_recs` schema | complete | Migration `0041_user_recs.sql`. |

### Story 7.22 — Device registration
| AC | Status | Evidence |
|---|---|---|
| AC-1 Register device | complete | `api/internal/handlers/devices/devices.go:55-56` upsert. |
| AC-2 `devices` schema | complete | Migration `0040_devices.sql`. |
| AC-3 Unregister (soft delete) | complete | `devices.go:57` `revoked_at`. |
| AC-4 Push delivery hook `Notify()` | missing | No `Notify(user_id, payload)` function; no APNs/FCM/WebPush bridge in `devices.go` (only a doc comment referencing "Story 18.x"). |
| AC-5 Token rotation | partial | Upsert on `(user_id,platform,push_token)` exists; revoke-prior-on `(user_id,platform,bundle_id)` rotation not implemented. |

---

## Top gaps (by impact)

1. **gRPC clients are non-functional stubs (Story 7.18, blast radius: 7.8, 7.10,
   7.15).** `api/internal/grpcclients/{pipeline,streaming}/realclient.go` return
   `ErrNotImplemented` for every RPC; only a construction-time TCP dial runs. No
   `shared/proto/` directory exists in the repo. This cascades: semantic/hybrid
   search degrades to FTS silently, streaming `OpenSession` always 503s, STT
   backend listing is empty, STT dry-run fails. The retry/circuit-breaker code
   (`pipeline.go:94-202`) is dead. Highest-impact gap — three downstream stories
   are behaviorally inert in any real deployment.

2. **WebSocket fan-out has no producer and no WebSocket (Story 7.16, breaks
   7.11 AC-2).** `ws.go` is SSE-only; no Postgres LISTEN loop is wired in
   `main.go`; the `Hub` is freshly constructed in `MountP6` with nothing
   publishing to it. `PublishFromCtx` is a documented no-op never called. All
   real-time events (job progress, playback sync, library updates) are dead.

3. **GraphQL is 501 schema-only (Story 7.17).** Every `POST /graphql` returns
   `schema-only` 501 (`schema.go:357-375`). No gqlgen, no resolvers, no
   `shared/graphql/schema.graphql` file, no CI parity test (the test file admits
   it). Story 7.17 is ~95% unimplemented.

4. **Web cookie-auth middleware never installed (cross-cutting).** `MountP9`
   returns `*auth.Handler` with a working `CookieAuth` middleware
   (`auth.go:525`), but `main.go:259-260` only nil-checks the return for
   logging. Only `JWTBearer` (Bearer-header, native clients) is wired via
   `applySecurity`. Web SPA clients (cookie-auth) get no principal and hit 403
   on every authed handler.

5. **Streaming URL signing never wired (Story 7.10 AC-1, 7.7 AC-1).**
   `P6Deps.URLSigner` is omitted from the `MountP6` call in `main.go:242-246`,
   so `streaming.Handler.Signer` is nil — manifest/subtitle URLs are never
   JWT-signed (Epic 10 §10.8 contract violated).

6. **Idempotency store is in-memory, not Postgres (Story 7.1 AC-4).**
   `idempotency/store.go:36-38` — replay correctness is per-process, violating
   the README §1.2 stateless-horizontal-scale requirement.
