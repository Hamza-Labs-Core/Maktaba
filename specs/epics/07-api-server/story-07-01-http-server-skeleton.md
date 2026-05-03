# Story 7.1 — HTTP server skeleton

Establish the chi-based HTTP server, the `application/problem+json` error
shape (RFC 9457), per-request IDs, structured logging, and graceful
shutdown. Every later story assumes this scaffold.

**AC-1 — RFC 9457 error envelope.**
- **Given** any handler that calls `httperror.Write(w, err)`,
- **When** the error is rendered,
- **Then** the response has `Content-Type: application/problem+json`, an
  HTTP status matching the error class, and a body `{type, title, status,
  detail, instance, requestId}` where `instance` is the request path and
  `requestId` is the per-request UUID v7 echoed in `X-Request-Id`.

**AC-2 — Request ID propagation.**
- **Given** an incoming request without `X-Request-Id`,
- **When** the request enters the middleware stack,
- **Then** a UUID v7 is generated, attached to the request context, echoed
  in the response `X-Request-Id` header, and included in every `slog` log
  line emitted while the request is in flight.
- **Given** an incoming request **with** `X-Request-Id` set to a syntactically
  valid UUID, **When** processed, **Then** that ID is used verbatim
  (idempotent retries from clients keep their ID).

**AC-3 — Graceful shutdown.**
- **Given** the server has in-flight requests,
- **When** `SIGTERM` is received,
- **Then** the listener stops accepting new connections, in-flight requests
  drain up to `shutdown_grace_sec` (default 30 s), and after the grace
  window any still-open connections are forcibly closed and the process
  exits 0.

**AC-4 — Idempotency-Key header convention.**
- **Given** a state-changing request (POST/PUT/PATCH/DELETE) carrying
  `Idempotency-Key: <uuid v7>`,
- **When** processed,
- **Then** the API consults a short-TTL key store (Postgres,
  `idempotency_keys (key, user_id, response_hash, status, body, created_at)`,
  TTL 24 h) and either replays the prior response (if the body hash
  matches) or processes the request and stores the result. Mismatched
  bodies for the same key return 409 `type: idempotency-key-conflict`.

**Test cases:**
- Unit: `httperror.Write` serializes a `NotFoundError` to a body with
  `status: 404` and `type: "https://maktaba.dev/problems/not-found"`.
- Unit: missing `X-Request-Id` header → middleware sets a v7 UUID; a
  malformed `X-Request-Id` → middleware overwrites with a fresh v7.
- Integration: a request panicking in a handler returns 500 with a
  problem+json body and the panic stack only goes to the log, not the body.
- Integration: `SIGTERM` while a slow request is mid-flight → request
  completes, then the server exits within `grace + 1 s`.
- Integration: same `Idempotency-Key` replayed → identical response, no
  duplicate side effect (e.g. duplicate session creation).

**Edge cases:**
- A handler calling `http.Error` directly (bypassing `problem+json`) is
  caught by a vet/lint rule (custom `analysispass`) and fails CI. Test
  case: the linter rule's golden test detects a synthesized `http.Error`
  call site.
- Two errors written by the same handler (double-write bug) — the second
  write is dropped and a warning is logged with the request ID. Test
  case: handler that calls `httperror.Write` twice → response body matches
  the first call exactly.
- A request that exceeds `shutdown_grace_sec` mid-shutdown → connection is
  closed; the request body is half-read; client sees a TCP RST. Test case:
  spawn a 10 s sleep handler, send SIGTERM with `grace = 1 s`, assert the
  client receives EOF inside 2 s.
