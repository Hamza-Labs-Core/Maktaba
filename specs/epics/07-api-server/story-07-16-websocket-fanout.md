# Story 7.16 — WebSocket fan-out

`/ws/jobs`, `/ws/library/{id}`, `/ws/playback/{video_id}` per §6.2 +
§7.10. SSE fallback for blocked-WebSocket networks (§6.2).

**AC-1 — WebSocket auth at handshake.**
- **Given** a connect request,
- **When** the upgrade is processed,
- **Then** the connection is accepted only if the request carries a valid
  JWT (Authorization header for native clients; cookie for web clients).
  Failure → close with code 4401.

**AC-2 — Subscription scoping.**
- **Given** a connected client,
- **When** events flow through Postgres LISTEN,
- **Then** the client only receives events they are authorized to see
  (per-user `playback`; per-library `library`; jobs are admin-only by
  default, configurable via per-user permission).

**AC-3 — Event shape.**
- **Given** any event,
- **When** sent over the wire,
- **Then** the JSON envelope is `{type: "<channel.event>", at: "<RFC3339>",
  ...payload}` and the type names are stable (semver — additions allowed,
  renames forbidden).

**AC-4 — Backpressure with persistent replay.**
- **Given** a slow client whose receive buffer fills up,
- **When** the server's per-connection send queue exceeds 1000 frames or
  1 MiB,
- **Then** the connection is closed with code 1011 `slow-consumer` and
  the listener row is freed. The client is expected to reconnect with a
  cursor (`?since=<at>`) and replay from the persistent `events` table
  (Epic 19 owns the schema; default retention 24 h). Per-connection
  replay is bounded to the last 1000 events to prevent abuse.

**AC-5 — Heartbeat / idle close.**
- **Given** an idle WebSocket,
- **When** no frame is sent or received for 30 s,
- **Then** the server sends a ping; if no pong arrives within 10 s the
  connection is closed.

**AC-6 — SSE fallback.**
- **Given** a client that requests `/ws/jobs` with `Accept:
  text/event-stream` instead of WebSocket upgrade,
- **When** processed,
- **Then** the same event stream is delivered as SSE frames with the same
  envelope.

**AC-7 — NOTIFY channel naming.**
- All Postgres NOTIFY channels follow the `<resource>.<event>` convention.
  Job-queue channels are: `jobs.new`, `jobs.flag_set`, `jobs.progress`,
  `jobs.heartbeat`, `jobs.reaped`, `jobs.force_pause`. (The pre-review
  alternative name `job.pending` is renamed to `jobs.new`; Pipeline
  Story 6.2's enqueue path writes to `jobs.new`.)

**Test cases:**
- Integration: connect without auth → 401 close; connect with valid auth
  → ping/pong cycle proven over 60 s.
- Integration: a job state change in DB → subscribed client receives a
  `jobs.state_changed` event within 200 ms.
- Integration: 1000 simultaneous connections in a load test → memory
  stays under 200 MB (per architecture choice of `coder/websocket`).
- Integration: slow consumer test — drop receive on the client → server
  closes with 1011 inside 5 s.
- Integration: SSE fallback delivers the same first 10 events as the WS
  variant for the same fixture.

**Edge cases:**
- Postgres connection drops while the listener is active — the listener
  reconnects with exponential backoff; events lost during the gap are
  replayed from `events` (AC-4), and a `gap` event with `from`/`to`
  timestamps is emitted to the client for visibility.
- Two API replicas both LISTEN on the same channel — both receive the
  NOTIFY and both deliver to their own connected clients (fanout is
  per-replica). Document in operations.
- A client subscribes to `/ws/library/{id}` for a library they no longer
  have access to (revoked mid-session) — the next event is intercepted
  and the connection is closed with 4403 `forbidden`.
- WebSocket upgrade behind a buggy proxy that strips `Connection: Upgrade`
  — the SSE fallback is the documented escape hatch.
