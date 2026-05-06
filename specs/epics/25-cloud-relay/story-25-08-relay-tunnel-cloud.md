# Story 25.8 — WebSocket relay tunnel: cloud side

> Epic 25 · Cloud relay · Phase 2 (relay)

## Description

The cloud accepts persistent WSS tunnels from servers (25.7) and
maintains an in-memory registry that maps `server_id → active
tunnel`. The HTTP relay (25.9) reads this registry to find a tunnel
and dispatch a request onto it. This story scopes the cloud-side
listener, registry, and lifecycle.

Process model:

- **Role `relay`.** The `maktaba-cloud --role=relay` process binds
  `:8081` for tunnel ingress (TLS-passthrough behind Hetzner LB,
  not Cloudflare; Cloudflare is on the `:443` HTTP path of 25.9).
- **Per-tunnel goroutine pair.** One reader goroutine drains
  incoming frames; one writer goroutine multiplexes outgoing
  frames from the registry. A bounded write buffer of 256 frames
  prevents one slow server from backpressuring the listener.
- **Registry.** In-memory `sync.Map` of
  `server_id → *tunnelHandle{conn, write_ch, sessions}`. On
  multi-pod deployment, registry is **node-local**; clients are
  routed to the right pod via consistent hash on
  `server_id` at the L4 LB. Tunnel handovers across pods are
  not supported in v1 (a server reconnect lands wherever
  consistent hashing sends it; usually the same pod).
- **Stream allocator.** The cloud picks odd `stream_id` values;
  rolls over at 2³¹. `stream_id` is opaque to the server.
- **Auth at handshake.** `Authorization: Bearer <server_token>`
  is bcrypt-checked against `cloud_server_tokens`. Misses get
  401; rate-limited per IP. On success, `cloud_servers.last_seen_at
  = now()` and a `cloud_audit` row records `tunnel.connect`.
- **Session limit.** One active tunnel per server. If a second
  tunnel arrives for the same `server_id`, the *older* tunnel
  is closed with `4003 superseded` (last-write-wins). This
  handles server restart faster than waiting for PONG-timeout.
- **Heartbeat tracking.** PINGs from the server are echoed
  with PONG. If no inbound frame arrives in 90s, force close
  with `4002 idle_timeout`.
- **Backpressure mirror.** When the cloud's per-stream write
  buffer fills, the cloud sends `WINDOW_UPDATE` lazily.
  Asymmetric: server sends data faster than the cloud can
  forward to a slow client → cloud signals window narrow.
- **Cleanup.** On disconnect: cancel all open streams (each
  client request gets 502 + `Connection: close`), update
  `cloud_servers.last_seen_at`, write audit `tunnel.disconnect`
  with reason code.

Metrics exposed at `/metrics` (relay role):

- `tunnels_open` (gauge) — current connections.
- `tunnel_handshakes_total{result=ok|invalid_token|malformed}`.
- `tunnel_messages_total{direction=ingress|egress, type=...}`.
- `tunnel_bytes_total{direction, server_id}` (high-cardinality;
  scoped to a separate scrape endpoint).
- `tunnel_reconnects_total{reason=normal|superseded|timeout|...}`.

## Acceptance criteria

- **Given** a server presents a valid `server_token`,
  **when** it upgrades `/tunnel/v1/connect`,
  **then** the response is `101 Switching Protocols` and the
  registry has `server_id → handle`.
- **Given** a server presents an invalid token,
  **when** it upgrades,
  **then** the response is `401`, no registry entry is
  created, and `tunnel_handshakes_total{result=invalid_token}`
  increments.
- **Given** a server already has an active tunnel,
  **when** a second tunnel for the same `server_id`
  successfully handshakes,
  **then** the older tunnel is closed with `4003 superseded`
  within 1s and the new one replaces it.
- **Given** a tunnel has been silent for 90s,
  **when** the idle check fires,
  **then** the tunnel is closed with `4002 idle_timeout`.
- **Given** the relay process restarts,
  **when** servers reconnect,
  **then** registry is rebuilt from new handshakes; no state
  is required from disk.
- **Given** an HTTP request arrives at the relay needing
  server S,
  **when** S's tunnel exists in the registry,
  **then** the relay can dispatch a `REQ_HEAD` onto S's
  write channel within 1ms (p99 in steady state).
- **Given** an HTTP request arrives for server S,
  **when** S has no active tunnel (offline),
  **then** the relay returns
  `503 server_offline` with body
  `{"server_id":"...","last_seen_at":"...","help":"..."}`.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | frame writer overflow | flood 1000 frames | drops with `tunnel_write_buffer_full` after 256 |
| T02 | integration | two tunnels for same server | both handshake | older closed `4003` |
| T03 | integration | 1k servers connect, hold | observe | RSS < 1 MiB / tunnel; CPU < 5% steady |
| T04 | integration | restart relay | reconnects | 99% reconnect within 30s |
| T05 | regression  | malformed frame from server | feed | tunnel closed `4001 protocol_error`, audit recorded |
| T06 | integration | invalid bearer | handshake | 401, IP rate-limit counter inc'd |
| T07 | unit        | stream id rollover at 2³¹ | force | wraps to 1 (odd) without collision |
| T08 | regression  | OOM safety: cloud receives 10 GiB body in single REQ_BODY frame | feed | rejected with `4001`; we limit per-frame to 1 MiB |
| T09 | integration | client disconnects mid-stream | observe | RST_STREAM equivalent → server cancels goroutine |
| T10 | unit        | bcrypt cost | benchmark | < 50ms / handshake at cost=10 |

## Edge cases

- **Stale registry on graceful shutdown.** SIGTERM: stop accepting
  new tunnels, send `4000 normal_closure` to all current ones,
  close after 5s grace. Servers backoff-reconnect to whichever
  pod the LB sends them to.
- **One pod, many tunnels.** With 50 ms idle CPU per tunnel, a
  4-vCPU pod handles ~20k tunnels comfortably. Sizing in 25.1
  documents pod ratios.
- **Slowloris-style abuse.** A tunnel that handshakes but never
  sends/recvs frames is closed at 90s by idle timer.
- **Pod failure.** If a pod crashes, all its tunnels disconnect;
  servers reconnect to a survivor pod. Failover is
  **server-driven**, not stateful — no tunnel migration.
- **Pod-affinity for the same `server_id`.** Hetzner L4 LB does
  consistent hashing on the `server_id` extracted from the
  bearer token; we put a sidecar that parses the bearer and
  rewrites a `Connection: <pod>` header upstream. v1
  simplification: random LB; pods replicate registry via
  ETag-based pull (deferred). For v1 we accept that two
  reconnects in a row may land on different pods but
  consistent hashing on bearer reduces it; this is fine because
  registry is rebuilt at handshake.
- **WSS upgrade rate.** Capped at 50/s per pod via token-bucket;
  excess gets 503 (clients backoff).
- **Memory pressure on burst.** Per-tunnel writer buffer is
  bounded; per-stream window is bounded; the relay should
  *never* hold more than `(N_streams_in_flight × 64 KiB) +
  (N_tunnels × 256 frames × 1 MiB)` and the worst case is
  bounded statically.
- **TLS at LB only.** The relay process speaks plain WS
  (`ws://`) on the loopback to the LB sidecar; TLS terminates
  at LB. Inside the cluster network, no TLS. v2: mTLS to
  catch attackers inside the cluster.

## Files / packages

- `cloud/internal/relay/listener.go` — accept loop.
- `cloud/internal/relay/handle.go` — per-tunnel goroutines.
- `cloud/internal/relay/registry.go` — `sync.Map` + observers.
- `cloud/internal/relay/frame.go` — wire format (shared with 25.7).
- `cloud/internal/relay/auth.go` — bearer verification.
- `cloud/internal/relay/metrics.go`.

## Open questions

- **Multi-pod registry sync.** Defer to scale issue (v2). Until
  then, sticky LB on bearer hash is sufficient for our
  expected fleet sizes (≤ 50k servers).
- **gRPC streaming alternative.** WebSockets are universally
  proxy-friendly; gRPC requires HTTP/2 support across all
  intermediaries (rarer on hotel wifi). WS wins for v1.
