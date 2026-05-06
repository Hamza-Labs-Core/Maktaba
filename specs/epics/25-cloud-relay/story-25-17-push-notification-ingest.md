# Story 25.17 — Push notification ingest

> Epic 25 · Cloud relay · Phase 4 (push)

## Description

Servers POST push events to the cloud; the cloud authenticates the
server, finds the user's registered devices, and dispatches via APNs
(25.18) or FCM (25.19). Servers never see device tokens.

Events servers send:

- `library.video_ready` — a transcribed video is ready to play.
- `library.scan_complete` — bulk scan finished.
- `download.complete` — offline download is ready (mobile).
- `system.alert` — non-critical condition (disk 90% full).
- `system.error` — high-severity (disk failed, transcription
  errors > N).
- `family.invite` — a family member added (cross-account).

We deliberately **do not** ship arbitrary string-passthrough; servers
pick from a fixed envelope schema and the cloud localizes copy.

Endpoint:

- `POST /api/push/dispatch` (server-only, `X-Server-Token` auth).
  Body:
  ```json
  {
    "user_id": "<uuid>",
    "kind": "library.video_ready",
    "ref_id": "<video_id>",
    "data": {
      "title": "Episode 12",
      "library_name": "Lectures"
    },
    "ts": "2026-05-06T12:34:56Z",
    "dedupe_key": "video-ready-<video_id>",
    "ttl_seconds": 3600
  }
  ```
- The server token must be linked to the same `user_id` (a server
  may only push to its own owner; family fanout is server-side
  via a different cross-server protocol — out of v1).

Behavior:

- Authenticate `X-Server-Token` (same bearer as 25.7 tunnel).
- Look up `user_id`'s registered devices in `cloud_devices`
  (`token_sealed`, `platform`, `revoked_at IS NULL`).
- For each device, write a row to `cloud_push_outbox` with
  `dispatched_at = NULL`. A worker drains the outbox and
  calls 25.18 (APNs) or 25.19 (FCM).
- Localize copy server-side using `cloud_users.locale` and a
  templates table:
  ```
  library.video_ready / en  → "{library_name}: {title} is ready to watch"
  library.video_ready / ar  → "{library_name}: ‎{title} جاهز للمشاهدة"
  ```
- Dedupe: if `cloud_push_outbox` has a row with the same
  `(user_id, dedupe_key)` within `ttl_seconds`, skip.
- TTL: dispatch within `ttl_seconds`; otherwise drop.
- Retry: APNs/FCM failures retried 3 times with backoff;
  permanent failures (`BadDeviceToken`) revoke the device.

Devices register with the cloud (client-side):

- `POST /api/push/devices` body `{platform, token, locale}`. The
  token is encrypted at rest with the cloud's data key.
  Response: `{device_id}`.
- `DELETE /api/push/devices/{id}` — user-initiated unregistration.
- A device record auto-revokes if APNs/FCM returns a permanent
  invalid-token error.

## Acceptance criteria

- **Given** a server posts a `library.video_ready` for its own
  user with 2 registered iOS devices,
  **when** the cloud processes it,
  **then** 2 rows land in `cloud_push_outbox` and within 5s
  both are dispatched to APNs.
- **Given** a server posts to dispatch for a user it doesn't
  own,
  **when** the cloud authenticates,
  **then** the response is `403 not_your_user` and a
  `cloud_abuse_events kind=cross_user_push` is recorded.
- **Given** the same `dedupe_key` arrives twice within
  `ttl_seconds`,
  **when** the second arrives,
  **then** no second outbox row is created.
- **Given** an event with `kind=system.error` and the user's
  locale is `ar`,
  **when** the message is dispatched,
  **then** the body is the Arabic template substituted with
  `data` fields.
- **Given** a device's APNs token is `BadDeviceToken`,
  **when** the dispatcher reports it back,
  **then** the device row is `revoked_at = now()` and no
  further pushes are attempted.
- **Given** the cloud has 0 devices for a user,
  **when** a push event arrives,
  **then** the response is `200 {"sent":0}` and no outbox
  rows are created.
- **Given** an unauthenticated request,
  **when** it hits `/api/push/dispatch`,
  **then** the response is `401`.
- **Given** an oversized payload (>4 KB serialized for APNs),
  **when** dispatched,
  **then** the cloud truncates to 4 KB by trimming the
  longest `data` field; Logs the truncation.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | integration | 2 devices, valid event | dispatch | 2 outbox rows |
| T02 | integration | dedupe within window | dispatch twice | 1 row |
| T03 | integration | dedupe outside window | dispatch twice over 2h | 2 rows |
| T04 | regression  | cross-user attempt | dispatch | 403, abuse |
| T05 | unit        | `kind` not in template table | reject | 400 `unknown_kind` |
| T06 | unit        | locale fallback (`fr` not in table) | render | falls back to `en` |
| T07 | integration | RTL Arabic message | render | bidi marks present, proper word order |
| T08 | regression  | 4 KB payload | dispatch | truncated |
| T09 | unit        | TTL expired | drain | row marked `dispatched_at`, no API call |
| T10 | regression  | revoked device | dispatch | skipped |

## Edge cases

- **Token re-registration.** A device that re-registers
  with a new token should mark the old one revoked. We
  enforce this with a unique partial index on
  `(user_id, platform, token_sealed)` filtered to
  non-revoked rows; a re-registration upserts.
- **Encrypted-at-rest device tokens.** The dispatcher
  decrypts in-process only at send time; tokens never
  appear in logs.
- **High-volume sender.** A server in error state could
  spam pushes; we rate-limit per server: 1000 events / hour
  default, 10000 for "trusted servers" (25.25 reputation).
- **Backpressure into FCM/APNs.** If APNs returns 429
  (rare), we backoff and retry; if FCM, same. Outbox
  durability shields the failure.
- **Stale TTL events.** A power-cycled cloud may have a
  backlog at restart; TTL prevents spamming users with
  hours-old "now ready" events.
- **Cross-account family pushes.** Out for v1. A server
  sending to a non-owner user 403s, deliberately.
- **Translations as data.** Localization tables live in
  `cloud_push_templates(kind, locale, title_template,
  body_template)`; updates are deployed via a migration
  (we don't fetch from a CMS at request time).
- **Variable substitution safety.** Templates accept only
  named placeholders we declare per `kind`; unknown
  placeholders are dropped. Defends against template
  injection from server-supplied `data`.

## Files / packages

- `cloud/internal/push/ingest.go` — `/api/push/dispatch`.
- `cloud/internal/push/devices.go` — `/api/push/devices`.
- `cloud/internal/push/outbox.go` — drain worker.
- `cloud/internal/push/templates.go` — i18n rendering.
- `cloud/internal/crypto/seal.go` — AES-GCM data-key wrapper.
- `cloud/migrations/00050005_push.sql` — `cloud_devices`,
  `cloud_push_outbox`, `cloud_push_templates`.

## Open questions

- **Web push.** Out for v1; FCM (25.19) covers Chrome
  via the same dispatcher, but Safari web push needs
  Apple-issued VAPID keys. Punt.
- **Email fallback.** If a user has no devices, the cloud
  could email instead. v1 silently drops (most events
  aren't email-worthy).
