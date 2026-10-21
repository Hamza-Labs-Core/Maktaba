# Plan 35.10 — Routing on a node — implementation

> Implementation plan for [story-35-10-node-routing](story-35-10-node-routing.md). Design: [design.md](design.md) §11.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Class declared at mount time as chi middleware, default `home`. | Visible next to each route; a missing class can only make a route safer. |
| D2 | Plain `httputil.ReverseProxy` to the home, not frames on the channel. | WebSockets, SSE and uploads work for free; the channel stays for control. |
| D3 | Writes are `home` in v1. | One writer path for audit, NOTIFY and cache invalidation. |
| D4 | TV always through the home's front door. | Epic 32 R3: one place routes channels; Plex and VLC only see the home. |
| D5 | Disk cache for images only. | Images are the bulk of proxied bytes; JSON stays uncached for correctness. |

## 1. Files

**Create**
- `api/internal/cluster/placement/placement.go` — `Local`, `Any`, `Home` middleware, registry, walk.
- `api/internal/cluster/homeproxy/proxy.go` — reverse proxy, signing (35.9), header rules.
- `api/internal/cluster/homeproxy/cache.go` — image disk cache, LRU.
- `web/src/pages/Paused.tsx` (+ test), i18n en/ar.
- Tests: `placement_test.go` (route walk), `proxy_test.go`, `cache_test.go`.

**Modify**
- `api/internal/router/router.go`, `p6.go`, `p9.go`, `p10.go`, `p11.go`, `p27.go`, `p28.go`,
  `p29.go`, `p31.go`, `p31_agent.go`, `p32_remote.go`, `p33.go`, `logs.go`, `processes.go` — classify
  every mount.
- `cmd/maktaba-server/cmd_serve.go` — node mode: `newStreamProxy` targets `MAKTABA_HOME_STREAM_URL`;
  public stream paths proxied to the home.
- `cmd/maktaba-server/stream_gate.go` — gate against the node API.
- `cmd/maktaba-server/internal/supervisor/env.go` — `MAKTABA_HOME_STREAM_URL` in node mode.
- `web/src/lib/api*` — on 503 `paused` (or `home_unreachable` with `readable: false`), route to the
  paused page; any other `home_unreachable` keeps the app under a notice (PR #145).

## 2. Steps

1. Placement middleware and the route walk test; classify every router file (TC1). Review the `any`
   list against Epic 31's revoked tables (devices and viewing data are `home`).
2. Home proxy with signing and header rules (TC2); home-side acceptance is story 35.9.
3. Node-mode wiring in `api/main.go`: `any` gated by the schema guard (TC3).
4. Web port in node mode: stream gate and front-door proxy (TC4).
5. Image cache (TC5).
6. Paused page and API client handling (TC6).
7. Playwright pages matrix on a two-machine CI setup (TC7).

## 3. Tests

Story TCs; a load test proxying 50 concurrent HLS clients through a node to the home front door
without added latency beyond 50 ms p95.

## 4. Rollout

Phase C, with 35.9. The classification lands first on the home (no behaviour change there), then the
node proxy is enabled with node mode.
