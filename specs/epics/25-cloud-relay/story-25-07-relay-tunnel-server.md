# Story 25.7 — WebSocket relay tunnel: server side

> Epic 25 · Cloud relay · Phase 2 (relay)

## Description

A linked Maktaba Server holds an outbound WSS tunnel to the cloud
relay. The tunnel is the only path through which inbound client
traffic reaches the server when the user is off-LAN. This story is
the **server-side** half: the local API (or a sibling service named
`maktaba-cloudlink`) opens, multiplexes, demultiplexes, and
gracefully reconnects the tunnel.

Wire protocol (in-band on a single WSS connection):

- **Frames are length-prefixed.** Each WS message is a binary blob
  with a 4-byte big-endian length, a 1-byte type, and a payload.
  Types: `0x01` REQ_HEAD, `0x02` REQ_BODY, `0x03` REQ_END,
  `0x04` RESP_HEAD, `0x05` RESP_BODY, `0x06` RESP_END,
  `0x10` PING, `0x11` PONG, `0x20` REVOKE, `0x21` ENT_REFRESH.
- **Streams are identified by a `stream_id` (uint32).** A REQ_HEAD
  starts a stream; REQ_END / RESP_END close it. The cloud chooses
  odd ids (its own); the server uses even ids only for its own
  initiated streams (currently none — reserved for v2).
- **Backpressure.** The server announces a per-stream window of
  64 KiB; when consumed, it stops reading bytes from the local
  upstream until a `WINDOW_UPDATE` (`0x12`) arrives. v1 uses a
  fixed window; HTTP/2-style dynamic windows are deferred.

Process model:

- **One persistent goroutine** owns the WSS connection.
  Inbound frames are dispatched onto a per-stream channel; each
  REQ_HEAD spawns a goroutine that proxies into a local
  `http.Client` (loopback) and pipes the response back as
  RESP_HEAD/RESP_BODY/RESP_END.
- **Connection lifecycle.** `connect → handshake → loop`. On
  network drop, exponential backoff (1s, 2s, 4s, 8s, max 60s)
  with full-jitter; reconnects keep the same `server_token`.
  The cloud assigns a new `tunnel_session_id` each connect;
  in-flight streams from the old session are dropped (the cloud
  responds 502 to the original client request for those).
- **Heartbeats.** PING every 25s; if no PONG within 10s, force a
  reconnect. Carrier-grade NAT (mobile hotspots, some ISPs) is
  why this is aggressive.
- **Auth.** WSS handshake includes `Authorization: Bearer
  <server_token>` (the bearer issued by 25.6). The cloud verifies
  bcrypt, then accepts. After handshake, no per-request auth.
- **Local TLS.** The local API's loopback HTTP listener may be
  HTTPS (Caddy with self-signed cert). The cloudlink trusts it
  via its bundled CA; rejects any other certs.
- **Health.** Tunnel state surfaces in `GET /admin/cloud-link`:
  `connected`, `last_handshake_at`, `last_pong_at`, `streams_in_flight`,
  `bytes_in_24h`, `bytes_out_24h`, `last_error`.

## Acceptance criteria

- **Given** a freshly claimed server with a valid `server_token`,
  **when** `maktaba-cloudlink` starts,
  **then** within 5s a WSS connection is established to
  `wss://relay.maktaba.app/tunnel/v1/connect` and
  `cloud_servers.last_seen_at` is updated.
- **Given** the cloud sends a `REQ_HEAD/REQ_BODY/REQ_END`
  for a `GET /api/libraries`,
  **when** the local API responds,
  **then** the cloudlink relays
  `RESP_HEAD/RESP_BODY/RESP_END` over the same `stream_id`
  and the bytes are byte-identical to a direct LAN call.
- **Given** the WS connection drops,
  **when** the cloudlink retries,
  **then** the first retry is at 1s ±0.5s jitter, doubling
  to a max of 60s, and any in-flight streams are aborted
  with RST_STREAM equivalent.
- **Given** PING is sent and no PONG arrives within 10s,
  **when** the timer fires,
  **then** the connection is closed with code `4002
  pong_timeout` and a reconnect is scheduled.
- **Given** the cloud sends frame type `0x20 REVOKE`,
  **when** the cloudlink receives it,
  **then** it disconnects, deletes its
  `server_token` from local storage, and shows an admin
  notification "Cloud connection revoked — re-claim to
  reconnect."
- **Given** a single tunnel,
  **when** the cloud sends 50 concurrent REQ_HEAD frames,
  **then** the server processes them concurrently and
  none observe HOL blocking (each runs in its own
  goroutine).
- **Given** a 1 GB stream relayed to the cloud,
  **when** memory is sampled during the transfer,
  **then** the cloudlink's RSS does not exceed 64 MiB
  (windowed I/O, no buffering of full stream).

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | frame encoder / decoder | round-trip random frames | byte-identical |
| T02 | unit        | reconnect backoff | drop 5 times | sequence within ±10% jitter of 1s/2s/4s/8s/16s |
| T03 | integration | mock cloud + mock local API | proxy 100 GETs | all 100 succeed, 0 errors |
| T04 | integration | mock local API returns 500 | proxy | 500 propagated cleanly |
| T05 | integration | huge response (1 GiB random bytes) | proxy | RSS bound, total time ≤ direct + 5% |
| T06 | integration | drop WS mid-stream | observe | client sees connection reset; server cleans goroutines |
| T07 | regression  | local API closed (port not listening) | proxy | 502 frame; tunnel stays up |
| T08 | unit        | clock skew between PING/PONG | feed timestamps | timeout enforced on monotonic clock only |
| T09 | regression  | restart cloud relay | observe server | reconnects within 60s, bytes resume |
| T10 | integration | server token revoked | reconnect attempt | 401 from cloud; cloudlink stops trying after 3 attempts and surfaces error |
| T11 | unit        | malformed frame from cloud | feed | tunnel closed with code `4001 protocol_error` |

## Edge cases

- **Captive-portal NAT (hotel wifi).** WSS port 443 is rarely
  blocked; we never use a non-443 port. If 443 is captive-portal
  intercepted, the TLS handshake to `*.maktaba.app` fails and we
  surface "captive portal detected".
- **Corporate MITM proxies.** Some corp networks intercept TLS;
  detection is "cert chain not pinned to known roots". We
  allow this if the user's local trust store is honored — but
  print a banner.
- **Frame fragmentation.** WS messages can split; we read until
  a complete length-prefixed frame is received. Single frame
  per WS message is the canonical encoding.
- **Goroutine leak on rapid reconnect.** Stream goroutines key
  on `tunnel_session_id`; on disconnect, we cancel the parent
  context which fans out cancel to all stream goroutines.
- **Local API unauthenticated loopback.** The cloudlink calls
  the local API as a privileged client (Epic 10 service
  account); requests carry a service token, not the cloud
  user's token. Per-request user identity comes from a
  trusted header `X-Maktaba-Cloud-User` set by the cloudlink
  after cloud-side auth.
- **Header sanitization.** The cloudlink strips
  `X-Forwarded-For`, `Cf-Connecting-Ip`, `X-Real-Ip` from the
  REQ_HEAD frame and re-adds based on the cloud-supplied
  `client_ip` in the head metadata. This makes the loopback
  request look like a normal LAN call from the cloud's IP
  (the actual client IP is preserved in
  `X-Maktaba-Original-Ip` for audit-only).
- **WebSocket ping vs. our PING.** WS-level PING/PONG are
  off (we use control frames within our own protocol so we
  control timing). RFC 6455 PING is acceptable as a
  passthrough but not relied on.
- **Single tunnel per server.** If the server crashes and
  restarts, two tunnels can briefly coexist; the cloud
  resolves by closing the older session (last-write-wins).
- **TLS termination at server.** The local API's loopback
  must not be 0.0.0.0; the cloudlink only trusts
  `127.0.0.1` connections so a misconfigured network exposure
  doesn't accidentally relay to the wrong process.
- **Outbound proxy (HTTP_PROXY env).** Honored for the WS
  upgrade only (CONNECT); after upgrade, raw bytes flow.

## Files / packages

- `internal/cloudlink/conn.go` — connect/handshake/reconnect.
- `internal/cloudlink/frame.go` — frame codec.
- `internal/cloudlink/multiplex.go` — stream demuxer.
- `internal/cloudlink/proxy.go` — cloud-to-local-API bridge.
- `internal/cloudlink/health.go` — admin endpoint exporter.
- New process: `cmd/maktaba-cloudlink/main.go` (separate
  systemd unit / compose service so a panic doesn't take
  down the local API).

## Open questions

- **Single binary or split process?** Splitting into
  `maktaba-cloudlink` keeps the API process unaware of cloud
  concerns; downside is one more service to start. Decision:
  **split**; the local API only knows about a Unix socket
  the cloudlink offers for "post-push-event" calls.
- **Multiple tunnels for redundancy.** Out for v1; one
  tunnel per server, the cloud handles failover at the
  edge (25.8).
