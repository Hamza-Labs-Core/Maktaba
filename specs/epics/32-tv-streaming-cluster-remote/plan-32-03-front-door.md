# Plan 32.3 — Front door — implementation

> Implementation plan for [story-32-03](story-32-03-front-door.md).
> Design: [design.md](design.md) §4. No migration.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | `/auto/v{n}` and `live.ts`: always proxied through a splicing copier on the home. | HDHomeRun clients bind one device and one BaseURL; redirects are not in the protocol; tuner leases must be counted where Plex connects; the splice gives failover without a dropped tuner (32.4). |
| D2 | HLS playlists: always proxied. Segments: proxied by default; `307` to a signed node URL only with `hls_mode = redirect`. | Single origin, the session gate, CORS and failover stay simple; redirect is an opt-in bandwidth saver. |
| D3 | The front door is a layer in the home's streaming service, not in the web port. | The web port already proxies `/stream/` to streaming; HDHR lives in streaming; one implementation serves both. |
| D4 | HMAC-SHA256 request signing with the cluster stream key; 60 s expiry; header for proxied requests, query for redirected segments. | Cheap, no TLS setup on the LAN, no per-request DB call on nodes. |

## 1. Files

**Create**
- `streaming/internal/frontdoor/frontdoor.go` — `Handler{Placement, Hosts, Engine, Leases, Key}`: mounts over `/stream/channel/{id}/…`; local → existing `channel.HTTPHandler`; remote → proxy
- `streaming/internal/frontdoor/proxy.go` — per-host `httputil.ReverseProxy` pool (`FlushInterval: -1`, `MaxIdleConnsPerHost: 32`), director signs and sets `X-Maktaba-Placement`
- `streaming/internal/frontdoor/splice.go` — TS copier with up to 3 splices, write deadline 15 s (as `pumpTS`)
- `streaming/internal/frontdoor/redirect.go` — signed segment URLs
- `streaming/internal/frontdoor/*_test.go`

**Modify**
- `streaming/internal/hdhr/handler.go` — `Streamer` is satisfied by the front door (`StreamChannelTS` resolves then local or splice); lease handling stays in `Auto`
- `streaming/main.go` — on the home, mount the front door instead of mounting `channel.HTTPHandler` directly; on a node, mount `channel.HTTPHandler` behind `cluster.VerifyMiddleware` (32.1)
- `streaming/internal/cluster/sign.go` (from 32.1) — `Sign(method, path, exp)`, `Verify(r)` accepting header or query
- `cmd/maktaba-server/stream_gate.go` — no change in behaviour; a test that a proxied node response streams through the gate unbuffered

## 2. Request flow

```
GET /stream/channel/{id}/index.m3u8        (web port gate → home streaming)
  p := placement.Resolve(id, Need{})
  p.Host == self → channel.HTTPHandler.servePlaylist (today)
  else           → proxy(p.Host).ServeHTTP  (+ retry once after Resolve on failure)
GET /stream/channel/{id}/seg-N.ts
  self → serveSegment; else hls_mode == proxy → proxy; redirect → 307 signed URL
GET /auto/v{n}  (hdhr.Auto: lease acquired on the home)
  → frontdoor.StreamChannelTS(ctx, id, w): self → Engine.StreamChannelTS
                                          else → splice copier from {host}/stream/channel/{id}/live.ts
```

Host failure accounting: each proxy error increments `hosts.Fail(host)`; three in a row → down
(32.4 consumes it); a 2xx resets.

## 3. Tests

- httptest node serving TS and HLS; the front door in front: bytes identical; first byte latency.
- Signing: good, expired, tampered path, wrong key, query form only for segments.
- Lease counted on the home: tuner_count 2 → third `/auto` "all tuners in use".
- Redirect mode: 307 `Location` host = node; the node accepts the query signature.
- Gate pass-through: `cmd/maktaba-server` test with a fake streaming that proxies a slow TS.

## 4. Rollout

No visible change with one host. With a node: Plex and the web keep the home's addresses. Keep
`hls_mode = proxy` unless the home's NIC is busy (metric `maktaba_tv_frontdoor_bytes_total`).
