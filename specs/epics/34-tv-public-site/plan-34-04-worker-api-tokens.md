# Plan 34.4 — Worker API, tokens and SiteControl — implementation

> Implementation plan for [story-34-04](story-34-04-worker-api-tokens.md). Design: [design.md](design.md) §6.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | One Worker, two DO classes (`SiteControl` singleton, `LiveHub` sharded). | Single writer for sessions/limits/budget; cheap hibernating fan-out. |
| D2 | Home connects out to `SiteControl` over WebSocket (HMAC). | Outbound only; instant commands both ways; long-poll fallback. |
| D3 | Separate Stream signing key for the site, in Worker secrets. | Rotating it never affects Epic 32 viewers; no key in R2. |
| D4 | `sub` = active input id, TTL 10 min, optional IP rule. | Stable across broadcasts; short exposure of a leaked token. |
| D5 | Heartbeat-gated refresh. | No heartbeat → no new token → scrapers expire within one TTL. |

## 1. Files

**Create** (TypeScript, `web/tv-site/worker/src/`)
- `index.ts` — router (assets with `run_worker_first` for HTML routes, `/api/v1`, `/_home`, sitemap/robots)
- `sitecontrol.ts` — DO: state (§6.2) in SQLite storage, alarms (session expiry, daily salt), play/beat/end,
  commit, home WebSocket handlers, fan-out to LiveHub
- `livehub.ts` — DO: page WebSockets (hibernation, auto ping/pong), broadcast
- `tokens.ts` — RS256 with WebCrypto, claims, access rules
- `hmac.ts` — home request verification, nonce cache in SiteControl
- `html.ts` — HTMLRewriter meta/JSON-LD/boot data; `headers.ts` — security headers
- `ratelimit.ts` — binding wrappers; `iphash.ts`
- tests with `@cloudflare/vitest-pool-workers`

**Create** (Go)
- `api/internal/tvpublic/control.go` — WebSocket client (reconnect, seq/ack, messages of §6.6), long-poll fallback
- `api/internal/tvpublic/sitekey.go` — create/rotate the site Stream key, set Worker secrets (API)

**Modify**
- `web/tv-site/wrangler.jsonc` — DO bindings + migrations (`new_sqlite_classes: [SiteControl, LiveHub]`),
  R2 binding, ratelimits, analytics dataset, vars
- `api/internal/tvremote/cf` (Epic 32) — Workers secrets API, Stream keys (shared client)

## 2. Steps

1. Worker skeleton + assets + HTML meta; security headers.
2. SiteControl state, play/beat/end with the gate order; tokens; neighbours.
3. Home WebSocket + HMAC + messages; Go control client; offline detection (60 s).
4. LiveHub sharding and broadcast; load test with 1,000 sockets in Miniflare.
5. Panic off-air path and site key rotation (create → secret → wait version → delete old).
6. Integration test: Go control client ↔ `wrangler dev`.

## 3. Tests

Story TCs; plus a fuzz test of `/_home/*` bodies against the schema.

## 4. Rollout

Phase A deploys the Worker with play disabled (`play` returns 503 `video_not_enabled`) until 34.3 is live.
