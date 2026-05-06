# Story 25.9 — HTTP relay proxy

> Epic 25 · Cloud relay · Phase 2 (relay)

## Description

Client traffic to `https://{username}.maktaba.app/...` is accepted at
the cloud edge, routed onto the right server's tunnel (25.8),
demultiplexed into REQ frames, awaited, and the response is streamed
back. To the client, this looks like a plain HTTP/1.1 or HTTP/2
request. To the server, it looks like an HTTP request from the
cloudlink. To the cloud, it's a managed bidirectional pipe.

Components:

- **Edge listener.** TLS-terminating HTTP/2 server on `:443`.
  Cert is the wildcard `*.maktaba.app` (25.23). The Hetzner LB
  routes to a `relay` role pod with consistent hashing on
  `Host:` so the same subdomain consistently lands on the same
  pod that holds its tunnel.
- **Subdomain → server lookup.** Strip the host suffix
  `.maktaba.app`; look up `cloud_subdomains` (citext PK) →
  `(server_id, user_id, suspended_at)`. If suspended,
  `503 server_suspended`. If subdomain reserved or unclaimed,
  `404 unknown_host`.
- **Tunnel lookup.** Get tunnel handle from registry. If
  missing, `503 server_offline` (formatted JSON or HTML based
  on `Accept`).
- **Request adaptation.** The relay rewrites:
  - `Host:` from `{user}.maktaba.app` to the server's local
    canonical (`maktaba.local` or `127.0.0.1:8080`).
  - Adds `X-Maktaba-Original-Host: {user}.maktaba.app`,
    `X-Maktaba-Client-Ip: <peer>`,
    `X-Maktaba-Cloud-User: <jwt-sub-or-anon>` (if auth header
    present and decodable; else absent — server's normal auth
    runs).
  - Strips `X-Forwarded-For`, `Forwarded`, `Cf-Connecting-Ip`
    (we don't trust them; only ours is authoritative).
- **Stream allocation.** Pick the next odd `stream_id`; emit
  `REQ_HEAD` with method, path, query, headers; pump request
  body into REQ_BODY frames respecting the 64 KiB window;
  emit REQ_END when the body closes.
- **Response stream.** Wait for RESP_HEAD (deadline 30s for
  headers; longer for body); emit headers to client; pump
  RESP_BODY frames; emit trailing 0-chunk on RESP_END.
- **Timeouts.** 30s wait for first response byte; 5min total
  for streaming responses (HLS chunks reset the timer);
  10min cap for video downloads (Pro tier; longer caps for
  Family).
- **Idempotent retries.** GETs that fail mid-headers
  (server crashed mid-response) are not retried by the relay
  — the client retries. POSTs / PATCHes / DELETEs are *never*
  retried by the relay.
- **WebSocket pass-through.** A `Upgrade: websocket` request
  is relayed by tunneling the raw upgrade onto a dedicated
  `0x30 WS_HEAD` frame type; subsequent frames carry binary
  payload. Used for `/ws/library/{id}` from Epic 07.

Bandwidth accounting (25.11) hooks in at the byte counters
on the request and response streams.

## Acceptance criteria

- **Given** `mahmoud.maktaba.app` is claimed and the server
  is online,
  **when** a client `GET`s `https://mahmoud.maktaba.app/api/libraries`,
  **then** the response status, headers, and body are
  byte-equivalent to a direct LAN call (modulo `Server`,
  `Date`, and `Via` headers).
- **Given** the subdomain is unclaimed,
  **when** a client requests it,
  **then** the response is `404 unknown_host` (HTML/JSON
  depending on `Accept`).
- **Given** the server is offline,
  **when** a client requests an endpoint,
  **then** the response is
  `503` with `Retry-After: 60` and a
  body that includes `last_seen_at`.
- **Given** a streaming HLS playlist request,
  **when** the relay forwards it,
  **then** the response uses chunked transfer-encoding and
  bytes flow as the server emits them (no buffering of the
  whole stream).
- **Given** a client uploads a 100 MB file,
  **when** the request reaches the relay,
  **then** the relay streams body bytes to the server
  through the tunnel without buffering more than 1 MB in
  memory.
- **Given** a `Upgrade: websocket` request,
  **when** the relay accepts it,
  **then** the bidirectional WS payloads are tunneled
  with negligible latency overhead (<5ms p50 added).
- **Given** a request with malformed `Host:` header
  (e.g., contains `..maktaba.app`),
  **when** the relay parses it,
  **then** the response is `400 bad_host`.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | integration | mock server returning 200 + JSON body | relay GET | byte-identical |
| T02 | integration | streaming server: 100 MB chunked body | GET | first byte ≤ 100 ms after server's first byte |
| T03 | integration | server returns `Connection: close` | observe | relay also closes its client connection cleanly |
| T04 | integration | upload of 1 GB | POST | relay memory bound, completes; bytes_in metric matches |
| T05 | regression  | request with smuggled `Transfer-Encoding: chunked, identity` | reject | 400 |
| T06 | regression  | request with embedded null byte in path | reject | 400 |
| T07 | integration | WS upgrade for `/ws/library/{id}` | client subscribes | events arrive |
| T08 | regression  | offline server then online mid-test | first request | 503; second request after reconnect | 200 |
| T09 | integration | suspended subdomain | GET | 503 with body referencing billing |
| T10 | unit        | wildcard certificate trust | verify chain | valid against `*.maktaba.app` |
| T11 | regression  | pseudo-rebinding: `Host: example.com` to wildcard cert | verify | 421 `misdirected_request` (we only handle `*.maktaba.app`) |

## Edge cases

- **HTTP request smuggling.** Strict header parsing; refuse
  duplicate `Content-Length`, conflicting `Transfer-Encoding`,
  `Host` headers > 255 chars, embedded CRLF in any header.
- **Hop-by-hop headers.** Strip `Connection`, `Keep-Alive`,
  `Proxy-*`, `TE`, `Trailer`, `Transfer-Encoding`, `Upgrade`
  (handled separately for WS).
- **Range requests for video.** Pass through; the server's
  streaming service (Epic 08) handles partial content. The
  relay accounts only the *bytes actually transferred* in
  bandwidth.
- **Long polls.** Header-wait timeout is 30s; for endpoints
  that long-poll (rare on Maktaba), set `X-Long-Poll:
  true` upstream and the relay extends to 5 min. Currently
  no Maktaba endpoints long-poll; documented for future.
- **Slow client.** Client downloads at 1 KB/s; server fills
  its outbound window; the relay applies WINDOW_UPDATE
  conservatively to prevent server-side buffer growth.
- **Connection reuse.** HTTP/2 multiplexes; HTTP/1.1
  keep-alive is honored; WS upgrades pin one stream for the
  duration.
- **TLS handshake errors.** Surface in metrics
  (`tls_errors_total{reason}`); never crash the listener.
- **DDoS.** Large attack volumes hit Cloudflare in front of
  the relay (when on `*.maktaba.app` proxied through CF) —
  but the *streaming* path bypasses CF for cost reasons. We
  keep an L7 rate limit at the relay (per IP, per
  subdomain) backed by Redis (25.24) and a circuit breaker
  per server when a server is being hammered (auto-suspend
  at 100×normal + alert; 25.25).
- **`HEAD` requests.** Forwarded normally; server may
  decline body, the relay just doesn't read body frames.
- **Trailers.** Forwarded; documented but rarely used.

## Files / packages

- `cloud/internal/relay/proxy.go` — adapter from HTTP server
  to tunnel frames.
- `cloud/internal/relay/host_router.go` — host → server lookup
  (LRU cache, 60s TTL, invalidated by `cloud_subdomains` notify).
- `cloud/internal/relay/ws_passthrough.go`.
- `cloud/internal/relay/header_sanitizer.go`.

## Open questions

- **HTTP/3 (QUIC).** Out for v1; defer until adoption is
  routine.
- **Per-server connection cap.** A server overloaded by 5k
  concurrent client connections might still have a single
  WS tunnel — does the cloud queue or 503? Current design
  forwards all; we'll add a per-server `streams_in_flight`
  cap at 200 (configurable per tier in 25.12).
