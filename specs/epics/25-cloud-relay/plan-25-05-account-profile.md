# Implementation Plan — Story 25.5 Account profile, deletion & data export

> Companion to [story-25-05-account-profile.md](story-25-05-account-profile.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Read API | `GET /api/me` — single query joining `users` + `oauth_links` (provider info, no PII of others) + `servers` (subdomain, last_seen_at, capped at 50). |
| Mutations | `PATCH /api/me` (display_name, locale, timezone, avatar_url); `POST /api/me/email-change`; `POST /api/me/password-change`. |
| Soft-delete | `DELETE /api/me` → set `deleted_at`; schedule hard-purge after 30d via `account_deletions`. |
| Restore | `POST /api/me/restore` → clears `deleted_at`, only if `<30d` old. |
| Export | `GET /api/me/export` → async job; ZIP with manifest.json + 7 JSONs/CSV; signed URL valid 7 days; one export per 24h. |
| Avatar | `POST /api/me/avatar` (≤2MB JPEG/PNG), `DELETE /api/me/avatar`. EXIF stripped via libvips; resized 256×256; stored in Cloudflare R2 at `avatars/<user_id>.jpg`. |
| Object store | Cloudflare R2 (S3-compatible). Bucket `maktaba-cloud-avatars` and `maktaba-cloud-exports`. Pre-signed URLs only. |
| Out of scope | Account import (deferred). 2FA (v2). |

## 1. Migration `00030001_account.sql` (slot 0003 per README)

Slot 0003 is the account-profile/deletion slot in the canonical
allocation (see [cloud/migrations/README.md](../../../cloud/migrations/README.md)).
The `avatar_url` column itself ships in `00020001_identity.sql`
(plan-25-02) so OAuth-only users have it from day one; this migration
only adds the email-change, account-deletion, and data-export
machinery.

```sql
-- +goose Up
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS email_change_requested_at TIMESTAMPTZ;

CREATE TABLE email_change_requests (
    token_hash    BYTEA PRIMARY KEY,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    new_email     CITEXT NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    used_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE account_deletions (
    user_id        UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    requested_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    purge_after    TIMESTAMPTZ NOT NULL,
    cancelled_at   TIMESTAMPTZ
);

CREATE TABLE export_jobs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requested_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at    TIMESTAMPTZ,
    completed_at  TIMESTAMPTZ,
    failed_at     TIMESTAMPTZ,
    failure_reason TEXT,
    object_key    TEXT,                    -- R2 key (only after completion)
    signed_url_expires_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX export_jobs_user_inflight_uq
    ON export_jobs(user_id)
    WHERE completed_at IS NULL AND failed_at IS NULL;

CREATE INDEX users_deleted_at_idx ON users(deleted_at) WHERE deleted_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS users_deleted_at_idx;
DROP TABLE IF EXISTS export_jobs, account_deletions, email_change_requests;
ALTER TABLE users DROP COLUMN IF EXISTS email_change_requested_at;
```

Audit-row indexes for GDPR-export queries land in the audit sub-
migration (`00020002_audit.sql`, plan-25-20) since the canonical
audit table lives in slot 0002.

## 2. Endpoints

```
GET    /api/me
PATCH  /api/me
POST   /api/me/email-change            body: {new_email}
POST   /api/me/email-change/confirm    body: {token}
POST   /api/me/password-change         body: {current, new}
DELETE /api/me                         soft-delete
POST   /api/me/restore
GET    /api/me/export                  202 async; returns job id
GET    /api/me/export/{job_id}         job status; if done, signed URL
POST   /api/me/avatar                  multipart/form-data, max 2MB
DELETE /api/me/avatar
```

All require valid access token. Soft-deleted users get 401 on all endpoints (token's `sub` lookup filters `deleted_at IS NULL`).

## 3. `GET /api/me` response

```json
{
  "id": "<uuid>",
  "email": "user@example.com",
  "display_name": "Mahmoud",
  "avatar_url": "https://cdn.maktaba.app/avatars/<id>.jpg",
  "locale": "ar",
  "timezone": "Asia/Riyadh",
  "plan": "pro",
  "created_at": "2026-01-01T00:00:00Z",
  "identities": [{"provider":"google","email_at_provider":"user@gmail.com","linked_at":"..."}],
  "servers": [{"id":"...","subdomain":"mahmoud","last_seen_at":"..."}],
  "more_servers": false,
  "email_change_pending": false,
  "next_email_change_at": "2026-04-15T..."
}
```

## 4. Display-name validation

```go
func ValidateDisplayName(s string) error {
    s = norm.NFKC.String(s)
    if strings.ContainsAny(s, "\x00\r\n\t") { return ErrBadName }
    n := uniseg.GraphemeClusterCount(s)
    if n == 0 || n > 80 { return ErrNameLength }
    return nil
}
```

## 5. Email change flow

`POST /api/me/email-change` with `{new_email}`:

1. Normalize new email. If equals current → 400 `same_email`.
2. Check 30-day cooldown via `email_change_requested_at`.
3. If `users.email = new_email` for any non-deleted user → 409 `email_taken`.
4. Insert `email_change_requests` (24h TTL, single-use).
5. Send confirmation email to `new_email` with the link.
6. Send notification to *current* email "your email change was requested".

`/email-change/confirm` validates token (HMAC + DB row), updates `users.email`, sets `email_change_requested_at = now()`, marks token used. Sends "your email has changed" to both addresses.

## 6. Soft-delete + purge

`DELETE /api/me`:

1. Inside a txn:
   - `UPDATE users SET deleted_at=now() WHERE id=$1 AND deleted_at IS NULL`.
   - Revoke all sessions.
   - For each linked server: emit `0x20 REVOKE` frame on its tunnel (best-effort; tunnel may be down — purge job retries on reconnect).
   - Cancel active Stripe subscription (no proration; user keeps access until `current_period_end`).
   - Insert `account_deletions(user_id, scheduled_at = now()+30d)`.
2. Audit `account.delete_requested`.

Purge worker (nightly):

```go
func PurgeStep(ctx context.Context, db *pgxpool.Pool) error {
    rows, _ := db.Query(ctx, `
        SELECT user_id FROM account_deletions
        WHERE purged_at IS NULL AND scheduled_at <= now()
        LIMIT 100
    `)
    for rows.Next() {
        var uid uuid.UUID
        rows.Scan(&uid)
        purgeUser(ctx, db, uid)  // delete identities, devices, sessions, password_resets, etc.;
                                 // null-out audit PII; keep accounting refs anonymized.
    }
    return nil
}
```

Per-table purge map:

| Table | Action |
|---|---|
| `users` | DELETE (CASCADE handles dependents that have ON DELETE CASCADE; we still explicitly target rows below for ones without cascade). |
| `oauth_links` | DELETE (cascade). |
| `sessions` | DELETE (cascade). |
| `push_devices` | DELETE (cascade) — token bytes already encrypted. |
| `email_verifications` | DELETE (cascade). |
| `email_change_requests` | DELETE (cascade). |
| `servers` | DELETE → cascade tunnels closed + bearer tokens (handled in 25.6 migration with `ON DELETE CASCADE`). |
| `audit_events` | UPDATE: `actor_user_id = NULL`; `payload = jsonb_strip_path(payload, ['email','name'])`; keep `actor_pseudonym = sha256(user_id || salt)`. Retain 90d post-delete. |
| `bandwidth_samples` | KEEP for billing reconciliation; set `user_id = NULL` 90d after purge (already cascaded from `server_id`-only rows; bandwidth carries `server_id` not user). |
| `invoices` | KEEP with anonymized customer ref (Stripe still has it). |
| `subscriptions` | KEEP `current_period_end`, status; anonymize user. |

Mark `account_deletions.purged_at`.

## 7. Restore

```go
func restore(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // Must auth as the soft-deleted user via emailed restore link
        // (the link is included in the "your account was deleted" email).
        // Token: HMAC{user_id, exp=30d}, used once.
        ...
        u, err := s.repo.RestoreUser(r.Context(), userID)
        if errors.Is(err, ErrAlreadyPurged) { problem(w, 410, "account_purged", ""); return }
        // Drop from purge queue.
        s.repo.DropFromPurgeQueue(r.Context(), userID)
        s.audit(r.Context(), "account.restore", userID.String())
        writeJSON(w, 200, u)
    }
}
```

## 8. Export job

`GET /api/me/export`:

1. Check rate limit: one job per 24h. If a recent job exists (`completed_at IS NOT NULL AND signed_url_expires_at > now()`), return its signed URL (job-id reused) instead of recomputing.
2. Otherwise insert a fresh row; enqueue `export_user_data` worker.
3. Respond 202 `{job_id, status:"pending"}`.

Worker:

```go
func RunExport(ctx context.Context, db *pgxpool.Pool, r2 *R2, userID, jobID uuid.UUID) error {
    // 1. Collect:
    //   - profile.json    (users row minus password_hash)
    //   - identities.json (oauth_links)
    //   - sessions.json   (history)
    //   - servers.json    (servers + subdomain history)
    //   - bandwidth.csv   (bandwidth_samples, last 24 months)
    //   - subscriptions.json + invoices.json (joined with Stripe references)
    //   - audit.json      (audit_events where actor_user_id = ?, last 24 months)
    //   - devices.json    (platform + last_used; token_sealed NOT included)
    // 2. Build ZIP in-memory or to a temp file (≤ 100 MB practical cap).
    // 3. Compute manifest.json with SHA-256 of each entry.
    // 4. Upload to R2 at exports/<user_id>/<job_id>.zip; KMS-encrypted at rest.
    // 5. Update job row: object_key, signed_url_expires_at = now()+7d, completed_at.
    // 6. Push notification + email with the signed URL.
}
```

Signed URL: R2's `s3.PresignedGetObject` with 7-day expiry (the longest Cloudflare supports).

## 9. Avatar pipeline

```go
// POST /api/me/avatar  multipart, max 2MB
func avatarUpload(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
        if err := r.ParseMultipartForm(2<<20); err != nil { problem(w, 413, "avatar_too_large", ""); return }
        file, _, err := r.FormFile("avatar")
        if err != nil { problem(w, 400, "bad_request", ""); return }
        defer file.Close()
        raw, err := io.ReadAll(file)
        if err != nil { problem(w, 400, "bad_request", ""); return }
        // libvips: detect format, strip EXIF, resize 256x256 JPEG q=85.
        out, err := vips.Process(raw, vips.Options{
            MaxBytes: 2<<20,
            Resize: 256,
            Format: "jpeg",
            Quality: 85,
            StripMetadata: true,
        })
        if err != nil { problem(w, 415, "unsupported_image", ""); return }
        key := fmt.Sprintf("avatars/%s.jpg", currentUserID(r))
        url := s.r2.PutPublicCacheable(r.Context(), key, out, "image/jpeg")
        s.repo.SetAvatarURL(r.Context(), currentUserID(r), url)
        writeJSON(w, 200, map[string]string{"avatar_url": url})
    }
}
```

libvips is bound via `davidbyttow/govips/v2` (cgo); CI builds with libvips installed. Decompression-bomb defense is built into libvips.

## 10. Test plan

### 10.1 Unit

| Test | Pins |
|---|---|
| `TestDisplayNameGraphemes` | 81 graphemes → reject. |
| `TestTimezoneValidate` | `Europe/Berlin` accepted; `Mars/Standard` rejected. |
| `TestEXIFStripped` | JPEG with GPS tag → output has none. |
| `TestExportSHA256Manifest` | Re-hash every file; matches manifest. |

### 10.2 Integration

| Test | Pins |
|---|---|
| `TestSoftDeleteThenPurgeIn31d` | `clock.Advance(31d)`; run purge; rows gone, audit redacted. |
| `TestRestoreWithin30d` | After soft-delete, restore endpoint reactivates. |
| `TestRestoreAt31d` | 410 `account_purged`. |
| `TestExportJobOneIn24h` | Second request returns previous link. |
| `TestExportPushAndEmail` | Worker emits both notifications. |
| `TestEmailChangeCooldown30d` | Second change within 30d → 429 with `next_change_at`. |
| `TestAvatarOversize` | 5MB upload → 413. |
| `TestConcurrentPATCHandDELETE` | DELETE wins; subsequent reads 401. |

## 11. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Email change to colliding address | 409 `email_taken`. | `TestEmailChangeCollision`. |
| Unpaid invoice on delete | 409 `unpaid_invoice`. | `TestDeleteWithUnpaidInvoice`. |
| Active subscription on delete | Cancel immediately; user keeps access through period. | `TestDeleteCancelsSubscription`. |
| Avatar decompression bomb | libvips refuses; 415. | `TestAvatarDecompressionBomb`. |
| Soft-delete race with login | Login fails 401 (`WHERE deleted_at IS NULL`). | `TestSoftDeleteRaceLogin`. |
| Export ZIP integrity | Per-file SHA-256 in manifest verified. | `TestExportSHA256Manifest`. |
| Active server when deleting | Tunnel revoke sent; bearer revoked; server reports "cloud link removed". | Cross-tested with 25.7. |
| GDPR audit retention | Audit retained 90d post-purge, PII redacted. | `TestAuditRedactedAfterPurge`. |
| R2 upload failure during export | Job marked `failed`; user gets retry option. | `TestExportR2Failure`. |
| Avatar moderation | Out of v1 (no content scan); documented. | Spec. |

## 12. Dependencies

- 25.1 (foundation).
- 25.2 (`users`, sessions, audit, JWKS).
- 25.3/25.4 (`oauth_links` for join).
- 25.6 (servers — `servers` exists; emit revoke).
- 25.7 (tunnel revoke frame).
- 25.13/25.14 (Stripe customer for subscription cancel).
- 25.17 (push outbox for export-ready notification).
- 25.18 (R2 client / object store; same data-key seal).

## 13. Acceptance checklist

- [ ] Migration 00030001 applies; account-deletion + export-job tables.
- [ ] All 12 endpoints implemented.
- [ ] Soft-delete → 30-day purge schedule.
- [ ] Restore works within 30 days; 410 at day 31.
- [ ] Export ZIP includes 7 datasets + manifest.json + matching SHA-256s.
- [ ] Avatar pipeline strips EXIF, resizes via libvips.
- [ ] Email change has 30-day cooldown and dual-notify.
- [ ] Tests in §10 pass.
