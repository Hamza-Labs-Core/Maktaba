# Story 25.19 — FCM dispatcher (Android + Web)

> Epic 25 · Cloud relay · Phase 4 (push)

## Description

Same as 25.18 but for Firebase Cloud Messaging — Android phones,
Android TV, and web push (Chrome / Edge / Firefox / Opera). Safari web
push is *not* covered by FCM and is out of v1.

Tech:

- HTTP v1 API:
  `POST https://fcm.googleapis.com/v1/projects/{project}/messages:send`.
- OAuth 2.0 service-account; cached access token (1h).
- Topic: not used; per-device send only (`token` field in
  payload).
- Concurrency: HTTP/2; up to 100 in-flight.

Payload:

- For Android:
  ```json
  {
    "message": {
      "token": "<device-token>",
      "notification": {"title":"...", "body":"..."},
      "android": {
        "priority": "HIGH",
        "ttl": "3600s",
        "notification": {"channel_id":"library_updates"}
      },
      "data": {
        "maktaba_kind":"library.video_ready",
        "maktaba_ref_id":"<uuid>",
        "maktaba_server_id":"<uuid>"
      }
    }
  }
  ```
- For web push: include `webpush.notification.title|body` and
  `webpush.headers.TTL`. Notifications open
  `https://app.maktaba.app/r/<ref_id>` on click.

Failure handling:

- `404 NOT_FOUND` (token not registered) → revoke device.
- `400 INVALID_ARGUMENT` → log + fail row, no retry.
- `403 SENDER_ID_MISMATCH` → operator alert (config issue).
- `429 RESOURCE_EXHAUSTED` → backoff.
- `500 INTERNAL` / `503 UNAVAILABLE` → backoff, retry 3x.

## Acceptance criteria

- **Given** a queued Android device,
  **when** the dispatcher runs,
  **then** within 2s an FCM POST succeeds and the row is
  marked sent.
- **Given** FCM returns 404 NOT_FOUND,
  **when** parsed,
  **then** the device row is revoked.
- **Given** the OAuth token is 55 min old,
  **when** dispatching,
  **then** a fresh token is fetched and cached.
- **Given** a web-push device,
  **when** dispatched,
  **then** `webpush.notification.click_action` is the
  deep-link URL.
- **Given** an Android TV device with channel_id
  `system_alerts`,
  **when** an `system.error` event dispatches,
  **then** the channel id flows through; the TV app routes
  to a leanback notification UI.
- **Given** a re-registration with the same FCM token,
  **when** the user opens the app on a previously-revoked
  device,
  **then** the device row reactivates (`revoked_at = NULL`)
  on registration.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | integration | mock FCM 200 | dispatch | sent |
| T02 | integration | mock 404 | dispatch | revoke |
| T03 | integration | OAuth refresh | dispatch | new token used |
| T04 | unit        | data payload size 5KB | reject | `500_above` per FCM caps |
| T05 | regression  | concurrent 100 rows | dispatch | all complete |
| T06 | integration | web-push device click | render | URL correct |
| T07 | regression  | sender-id mismatch | dispatch | metric + alert |
| T08 | integration | TTL expired | dispatch | skipped |

## Edge cases

- **Service-account JSON rotation.** Loaded from
  `/var/run/secrets/fcm/service-account.json`. Rotation
  swaps the file; the worker reloads on a SIGUSR1 or
  next 1h cycle.
- **Web push VAPID.** FCM handles VAPID for Chromium
  browsers. Safari web push is its own ecosystem (out).
- **Android notification channels.** The Android app
  registers channels at install; we send the `channel_id`
  the channel was registered under. Mismatch → silent
  delivery (Android default behavior). Document.
- **Topic-based broadcasts.** Out for v1; we always send
  to specific tokens.
- **Data-only messages.** Used for silent sync; respects
  Doze and battery limits on Android.
- **Token format collision.** APNs and FCM tokens are
  visually distinguishable; we still gate by
  `cloud_devices.platform`.

## Files / packages

- `cloud/internal/push/fcm.go`.
- `cloud/internal/push/fcm_oauth.go` — service-account JWT
  + token cache.
- `cloud/configs/cloud.example.toml` —
  `[fcm] project_id=..., service_account_path=...`.

## Open questions

- **Safari web push.** Apple's separate VAPID flow; defer.
- **Notification grouping on Android.** `tag` field
  collapses; we set tag to `dedupe_key`.
