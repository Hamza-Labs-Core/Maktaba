# Story 25.18 — APNs dispatcher

> Epic 25 · Cloud relay · Phase 4 (push)

## Description

The cloud holds Apple's APNs key; servers cannot. This worker reads
`cloud_push_outbox` rows for iOS / iPadOS / tvOS devices and
dispatches them to APNs over HTTP/2.

Tech:

- HTTP/2 to `https://api.push.apple.com:443` (production) and
  `https://api.sandbox.push.apple.com:443` (development /
  TestFlight).
- Authentication via JWT signed with the team's `.p8` private key
  (ES256), `aud=apns`, `iss=team_id`, `kid=key_id`. The JWT is
  rotated proactively before the 1h cap.
- Topic = bundle id (`app.maktaba.cloud` for the public app,
  `app.maktaba.cloud.tv` for the tvOS app) — one cloud
  configuration loads multiple topics, route by device-row
  `app_bundle_id`.
- One persistent HTTP/2 connection per process; connection
  health managed by Go's `http2.Transport`.
- Concurrency: up to 100 outstanding requests per connection.
  Outbox drainer runs in a single worker; spawns a goroutine
  per pending row up to a 200 in-flight cap.

Payload:

- Standard `aps.alert.title|body`, `aps.badge`, `aps.sound`
  (default), `aps.thread-id` (group by `kind`),
  `aps.category` (custom action category for v2 deep-links).
- Custom keys: `maktaba_kind`, `maktaba_ref_id`,
  `maktaba_server_id`. Used by the iOS app to deep-link.
- High-priority (10) for user-visible alerts; low (5) for
  background fetch events (system status).

Failure handling:

- `400 BadDeviceToken`, `410 Unregistered` → revoke device row.
- `400 BadCertificate`, `403 BadCollapseId`, `400 PayloadTooLarge`
  → log + mark outbox row failed; do not retry.
- `429 TooManyRequests` → exponential backoff per token.
- `500/503` → backoff, retry up to 3 times.
- Connection-level errors → recreate connection, retry.

Ops:

- Metrics: `apns_sent_total{result}`, `apns_request_duration_seconds`,
  `apns_jwt_minted_total`.
- Dashboards: success rate, p99 latency, devices revoked / hour.
- Alerts: success rate < 95% over 5 min, p99 > 10s.

## Acceptance criteria

- **Given** a queued row for an iOS device,
  **when** the dispatcher runs,
  **then** within 2s an HTTP/2 POST hits APNs and the row's
  `dispatched_at` is set.
- **Given** APNs returns `410 Unregistered`,
  **when** the response is parsed,
  **then** the device row is `revoked_at = now()` and no
  further outbox rows for it are sent.
- **Given** APNs returns `429`,
  **when** retry occurs,
  **then** the dispatcher backs off (jittered exponential)
  and eventually succeeds; metrics record `result=throttled`.
- **Given** the JWT is 50 minutes old,
  **when** the dispatcher composes a request,
  **then** the JWT is reminted and cached.
- **Given** the worker restarts,
  **when** it picks up undelivered rows,
  **then** rows whose original `enqueued_at` is older than
  `ttl_seconds` are skipped (marked failed with reason
  `expired`).
- **Given** a tvOS device on a different bundle id,
  **when** the dispatcher composes,
  **then** `apns-topic` header is the tvOS bundle, not the
  iOS one.
- **Given** APNs disconnects mid-flight,
  **when** the connection is recreated,
  **then** in-flight requests fail and are retried; no
  poisoned-pill loop.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | sign JWT with test `.p8` | verify with public | valid |
| T02 | integration | mock APNs server returning 200 | dispatch | row marked sent |
| T03 | integration | mock returning 410 | dispatch | device revoked |
| T04 | integration | mock returning 429 with `Retry-After` | dispatch | backoff honored |
| T05 | regression  | huge payload (5 KB) | dispatch | rejected before send (truncate at 25.17 layer) |
| T06 | unit        | bundle-id routing | enqueue tvOS row | header `apns-topic=app.maktaba.cloud.tv` |
| T07 | integration | concurrent 200 rows | dispatch | all complete within budget; in-flight cap respected |
| T08 | regression  | cert pinning fails | dispatch | error metric, retry, fail open after 3 tries |
| T09 | unit        | TTL expired in queue | dispatch | skipped, row failed `expired` |
| T10 | regression  | sandbox vs prod token mismatch | dispatch | 400 `BadDeviceToken`; revoked |

## Edge cases

- **Sandbox vs production tokens.** Determined by the
  device-registration endpoint (TestFlight builds register
  through a sandbox path). We tag `cloud_devices.environment
  = sandbox|production` and route accordingly.
- **`apns-collapse-id`.** Set to `dedupe_key` (max 64 bytes
  ASCII). Newer pushes with same id replace older
  undelivered ones.
- **Background pushes.** `aps.content-available=1` is
  optional per kind; system-level events use it to wake
  the app for sync.
- **Sound.** Defaults to "default"; user can mute via
  iOS settings (we never override).
- **Large fanout.** A single user with 5 iOS devices
  dispatches 5 APNs requests; we don't have a multicast
  topic for personal devices.
- **APNs outage.** Outbox grows; alert at 10k pending.
  Eventually flushes when APNs returns.
- **Critical alerts.** Apple-special category requires
  Critical Alerts entitlement; we don't ship as critical
  by default.
- **Time-sensitive notifications.** iOS 15+; we set
  `apns-priority=10` and `interruption-level=time-sensitive`
  for `system.error` only; defaults `passive` for status.
- **Region.** APNs has no regional endpoints — we always
  hit the global one.

## Files / packages

- `cloud/internal/push/apns.go` — HTTP/2 client.
- `cloud/internal/push/apns_jwt.go` — token minter.
- `cloud/internal/push/dispatcher.go` — outbox worker.
- `cloud/configs/cloud.example.toml` —
  `[apns] team_id=..., key_id=..., key_path=...,
  ios_bundle=app.maktaba.cloud, tvos_bundle=app.maktaba.cloud.tv`.

## Open questions

- **Live activities / dynamic island.** Out for v1.
- **Per-device locale via APNs.** We localize at ingest
  time (25.17); APNs doesn't help here.
