# Story 25.5 — Account profile, deletion & data export

> Epic 25 · Cloud relay · Phase 1 (identity)

## Description

Authenticated users can view, edit, delete, and export their cloud
account. This story closes the GDPR / CCPA gap and gives users the
basic profile editing they expect.

Endpoints:

- `GET /api/me` — returns
  `{id, email, display_name, avatar_url, locale, timezone,
   plan, created_at, identities:[{provider, email_at_provider,
   linked_at}], servers:[{id, subdomain, last_seen_at}]}`.
- `PATCH /api/me` — accepts any subset of
  `{display_name, locale, timezone, avatar_url}`. Email
  change is a separate flow (re-verify required).
- `POST /api/me/email-change` — sends a verification email to
  the *new* address; clicking the link confirms; old address
  is also notified ("your email was changed").
- `POST /api/me/password-change` — requires current password
  for confirmation; revokes all other sessions on success.
- `DELETE /api/me` — soft-deletes (`deleted_at = now()`),
  schedules hard-purge in 30 days. The user is signed out
  immediately; servers receive a `revoked` push and stop
  serving relay traffic.
- `POST /api/me/restore` — within 30 days of soft-delete, an
  authorized request from the original email can restore.
- `GET /api/me/export` — kicks off an asynchronous export;
  responds 202 with a job id; when done, a signed URL to a
  ZIP arrives by email and via push. ZIP contains a
  `profile.json`, `identities.json`, `sessions.json`,
  `servers.json`, `bandwidth.csv`, `subscriptions.json`,
  `audit.json` (filtered to the user's own actions).

Avatar handling:

- `POST /api/me/avatar` accepts a JPEG/PNG ≤ 2 MB; we strip
  EXIF, resize to 256×256, store in object storage at
  `avatars/{user_id}.jpg`, and serve via Cloudflare-cached
  signed URL.
- `DELETE /api/me/avatar` — removes; profile falls back to
  initials.

## Acceptance criteria

- **Given** an authenticated user,
  **when** they `GET /api/me`,
  **then** the response includes their profile, identities,
  and servers (no PII of other users).
- **Given** a user PATCHes `display_name` to a 200-char string,
  **when** the request is validated,
  **then** the response is `400 display_name_too_long`
  (cap 80 graphemes).
- **Given** a user requests an email change to a new address,
  **when** they click the verification link,
  **then** the email is updated and both old and new addresses
  receive a notification email.
- **Given** a user `DELETE /api/me`,
  **when** the request succeeds,
  **then** all sessions are revoked, all linked servers
  receive a tunnel `revoke` frame, the response is `204`,
  and a `cloud_audit` row records `actor=user_id, action=account.delete`.
- **Given** a soft-deleted account 31 days old,
  **when** the nightly purge job runs,
  **then** PII is hard-deleted from `cloud_users`,
  `cloud_identities`, `cloud_devices`, `cloud_sessions`;
  `cloud_audit` rows are PII-redacted (`actor` becomes a
  pseudonymous hash, `payload_jsonb` is stripped of email/name).
- **Given** a user requests a data export,
  **when** the worker completes,
  **then** an email arrives with a signed URL valid for 7 days,
  pointing at a ZIP whose top-level `manifest.json` lists every
  file's SHA-256.
- **Given** an unauthenticated request to `GET /api/me`,
  **when** the request hits the API,
  **then** the response is `401`.
- **Given** the avatar upload is a 5 MB file,
  **when** the API receives it,
  **then** the response is `413 avatar_too_large`.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | display_name with combining marks 81 graphemes | validate | rejected |
| T02 | unit        | timezone `Europe/Berlin` | validate | accepted |
| T03 | unit        | timezone `Mars/Standard` | validate | rejected |
| T04 | integration | soft-delete + 31 days clock advance | run purge | `cloud_users` row gone, audit redacted |
| T05 | integration | export: 1 server, 30 days bandwidth | request → wait worker | ZIP exists, manifest verifies |
| T06 | integration | upload 1 MB JPEG with EXIF GPS tags | upload | EXIF stripped, GPS not present |
| T07 | integration | concurrent PATCH + DELETE | both arrive | DELETE wins (state snapshot under lock) |
| T08 | regression  | restore within 30 days | POST `/restore` | account active again, no data lost |
| T09 | regression  | restore at day 31 | POST | 410 `account_purged` |
| T10 | unit        | export ZIP integrity | unzip + verify SHA | every file matches |

## Edge cases

- **Email change to a colliding address.** If the new email
  already belongs to another user, reject with
  `email_taken`. We do *not* attempt to merge; the user must
  delete the other account first.
- **Email change rate limit.** Once per 30 days; surface a
  cooldown timer in `GET /api/me`.
- **Pending billing.** Cannot delete if there's an unpaid
  invoice; surface "settle the invoice or contact support".
  Stripe `customer` reference is anonymized but kept for
  accounting.
- **Active subscription on delete.** Cancel the subscription
  immediately (no proration); user keeps cloud features
  through `current_period_end` then is signed out.
- **Avatar abuse.** Strip EXIF; transcode through libvips so
  pathological PNGs (decompression bomb) get rejected at
  decode time.
- **Export DOS.** One export per user per 24h; subsequent
  requests get the previous link instead of recomputing.
- **`/api/me` cardinality on `servers`.** A v2 federation user
  could have many servers; we cap inline at 50, paginate
  beyond that with a `more` flag.
- **Race: user signs in just as soft-delete fires.** The
  check is at the access-token layer (`sub` lookup hits
  `WHERE deleted_at IS NULL`). Sessions in flight see 401
  on next refresh.
- **Import is not implemented.** Out of scope; users
  re-create accounts manually.

## Files / packages

- `cloud/internal/account/{me,delete,export}.go`
- `cloud/internal/account/avatar.go` — libvips bindings.
- `cloud/internal/jobs/purge_deleted.go` — nightly cron.
- `cloud/internal/jobs/export_user_data.go` — async worker.
- `cloud/migrations/00090009_user_export_jobs.sql`.

## Open questions

- **Object storage.** Backblaze B2 (cheap, S3-compatible) vs.
  Cloudflare R2 (zero egress, slightly more $). R2 wins for
  exports because they're CDN-served; B2 wins for backup.
  Decision: R2 for v1.
