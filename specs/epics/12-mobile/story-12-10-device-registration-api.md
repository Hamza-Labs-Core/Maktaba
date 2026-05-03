# Story 12.10 — API: device registration & push fan-out

**Status:** **NEW** — added in response to
[REVIEW §3.2](../../REVIEW.md): the push-notification story
([Story 12.4](story-12-04-push-notifications.md)) referenced
`POST /api/devices/register` and an APNs/FCM bridge with no API story
owner. This story owns both: the device registry, the fan-out worker,
and the secrets management.

## AC

### Schema (`devices` table — also fills [REVIEW §1.1.h](../../REVIEW.md))

- New table `devices`:
  - `id UUID PRIMARY KEY`
  - `user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE`
  - `platform TEXT NOT NULL CHECK (platform IN ('ios','android','web'))`
  - `token TEXT NOT NULL` (the APNs / FCM device token; opaque blob)
  - `token_hash BYTEA GENERATED ALWAYS AS (sha256(token::bytea)) STORED`
  - `app_version TEXT`
  - `os_version TEXT`
  - `locale TEXT NOT NULL DEFAULT 'en'`
  - `categories JSONB NOT NULL DEFAULT '{"processing":true,"new_content":true,"job_failed":true,"subscription":true}'`
  - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `revoked_at TIMESTAMPTZ`
  - `UNIQUE (user_id, token_hash)`
- Indexes:
  - `(user_id, revoked_at)` for listing
  - `(token_hash)` for cross-user dedup checks
- Migration owner: this story.

### Endpoints

- `POST /api/devices/register {platform, token, app_version?, os_version?,
  locale?, categories?}` →
  - `200 {id}` if existing row updated (token already known for user)
  - `201 {id}` if new row inserted
  - `400 invalid-token` if token format does not match platform
  - `409 token-claimed-by-other-user` if the same `token_hash` is owned
    by another user (the previous owner is revoked silently after a
    24-h grace; documented in security notes below)
- `PATCH /api/devices/{id} {categories?, locale?}` → 200; user can only
  patch their own.
- `DELETE /api/devices/{id}` → 204 (sets `revoked_at`); user can only
  revoke their own.
- `GET /api/me/devices` → list of current user's devices (no plaintext
  token returned; only `id, platform, app_version, os_version, locale,
  categories, created_at, last_seen_at, revoked_at`).

### Push fan-out worker

- A new internal worker `push-fanout` consumes domain events:
  - `job.completed` → "Processing complete" notification
  - `job.failed` → "Job failed" notification (admin-targeted only)
  - `library.video.added` → "New content added" (per-user opt-in)
  - `subscription.expiring` → "Subscription expiring" (Epic 16)
- Each event is mapped to a per-user notification payload using the
  device's `locale` and `categories` filter; suppressed if the
  category is off.
- Delivery: APNs over HTTP/2 (`/3/device/{token}`) for iOS; FCM HTTP v1
  (`/v1/projects/{id}/messages:send`) for Android.
- Retry policy: 3 retries with exponential backoff (1s, 4s, 16s); on
  permanent failure (`Unregistered`, `BadDeviceToken`), set
  `revoked_at = now()`.
- Batching: if more than 5 `job.completed` events fire for the same user
  within a 60-second window, coalesce into a single
  "5 jobs completed" notification (resolves
  [Epic 12 Story 12.4 EC about rate limits](story-12-04-push-notifications.md)).

### Configuration & secrets

- `[push.apns] team_id, key_id, p8_path, environment ∈ {sandbox, production}`.
- `[push.fcm] service_account_json_path` (Google service account).
- Secrets read at boot, never logged, masked by Story 21.1's redaction
  filter.
- If neither APNs nor FCM is configured, the worker disables itself and
  logs a warning at startup; the registration endpoint still accepts
  tokens (so devices register without errors).

### Security & privacy

- Plaintext tokens are never returned by GET endpoints (only `token_hash`
  semantically, but even that is not exposed; only metadata).
- Audit log entries for token registration and revocation
  (`category = 'device'` in the canonical `audit_log` table from
  [REVIEW §1.1.f](../../REVIEW.md)).
- Cross-user token claim (`409`): the previous owner's row is moved to
  `revoked_at = now() + 24h` rather than deleted immediately, giving the
  legitimate owner a window to reclaim if needed.

## TC

- iOS app posts `{platform: "ios", token: "<64-hex>"}`: `201`; row in
  `devices`; subsequent same-token POST updates `last_seen_at`.
- Toggle `categories.new_content = false` via `PATCH /api/devices/{id}`:
  the next `library.video.added` event does not produce a notification
  to that device.
- Trigger 6 `job.completed` events within 30 s for the same user: 1
  consolidated push is delivered, not 6.
- Receive an `Unregistered` response from APNs for a token: the row is
  marked `revoked_at`; subsequent events skip it.
- Misconfigured APNs cert: worker logs the failure once at boot; HTTP
  registration endpoint still 201s.

## EC

- `POST /api/devices/register` with a token that's already revoked: the
  endpoint un-revokes (`revoked_at = NULL`) and updates `last_seen_at`.
- A user with 100+ devices over time: `GET /api/me/devices` paginates;
  the worker only sends to non-revoked rows.
- APNs/FCM downtime > 16 s (after retries): events are persisted to a
  `push_outbox` table for replay by a separate sweep job (not in v1; v1
  drops with a logged warning).
- Token rotation in the middle of a fan-out: the registration POST
  arrives before fan-out enqueues the old token → no duplicate.
- A device whose user is deleted: cascade removes the row; in-flight
  pushes targeting that device may still send once before the cascade
  takes effect (acceptable race).
