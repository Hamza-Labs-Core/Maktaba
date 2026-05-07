# Story 7.22 — Device registration for push

`POST /api/devices/register`, `DELETE /api/devices/{id}` from the clients
epic (Epic 12.4 push notifications). Owns the API-side implementation
that REVIEW.md §3.2 flagged as a high-impact gap.

**AC-1 — Register a device.**
- **Given** an authenticated request `POST /api/devices/register` with
  body `{platform, push_token, bundle_id, app_version?, locale?}`,
- **When** processed,
- **Then** a row is upserted into `devices` keyed on
  `(user_id, platform, push_token)`; if the row exists, `last_seen_at`
  and metadata fields are updated; the response is 201 (new) or 200
  (replaced) with the device id.

**AC-2 — `devices` schema.**
- The `devices` table is owned by this story:
  ```
  CREATE TABLE devices (
    id            UUID PRIMARY KEY,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform      TEXT NOT NULL CHECK (platform IN ('ios','android','web')),
    push_token    TEXT NOT NULL,
    bundle_id     TEXT NOT NULL,
    app_version   TEXT,
    locale        TEXT,
    registered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at    TIMESTAMPTZ,
    UNIQUE (user_id, platform, push_token)
  );
  CREATE INDEX devices_user_active ON devices (user_id) WHERE revoked_at IS NULL;
  ```

**AC-3 — Unregister.**
- **Given** an authenticated request `DELETE /api/devices/{id}`,
- **When** processed,
- **Then** the device row is updated `revoked_at = now()` (soft delete
  so the apns/fcm bridge can purge stale tokens lazily). Response 204.

**AC-4 — Push delivery hook.**
- **Given** an internal call `Notify(user_id, payload)` from any
  service (e.g., a job-completion handler),
- **When** dispatched,
- **Then** the push delivery service iterates over the user's active
  devices and invokes the appropriate vendor bridge (APNs for `ios`,
  FCM for `android`, Web Push for `web`). Failed deliveries with
  `BadDeviceToken`/`Unregistered` errors mark the row revoked.

**AC-5 — Token rotation.**
- **Given** a device whose vendor token rotated,
- **When** the client re-registers with the new token,
- **Then** the previous row for that `(user_id, platform, bundle_id)`
  combination is `revoked_at`-stamped on insert of the new token.

**Test cases:**
- Integration: register-then-list returns the device.
- Integration: registering the same `(user_id, platform, push_token)`
  twice updates `last_seen_at` and does not create a duplicate row.
- Integration: unregister marks `revoked_at`; subsequent push attempts
  for that user skip the device.
- Integration: a `BadDeviceToken` response from APNs flips the row to
  revoked automatically.

**Edge cases:**
- A user logs into their account on a new device that already has a
  push token from a previous user — the row is created under the new
  `user_id`; the previous user's row remains active until their app
  next launches and re-registers. Test case: assert two rows exist for
  the same `push_token` under different `user_id` values.
- A token longer than 1 KiB (vendor anomaly) — capped at 4 KiB by the
  validator; over-cap returns 422.
- A device that registers without a `bundle_id` — required for APNs
  routing; rejected with 422.
