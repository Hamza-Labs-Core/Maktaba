# Implementation Plan — Story 15.6 API: device pairing endpoints

> Companion to [story-15-06-pairing-api.md](story-15-06-pairing-api.md).
> The story states *what* and *why*; this plan states *how*.
>
> **Endpoint ownership.**
> [plan-10-17](../10-auth-security/plan-10-17-auth-pair.md) is the canonical
> owner of `POST /api/auth/pair`, `POST /api/auth/pair/claim`, and
> `GET /api/auth/pair/{code}`, including the `pairing_codes` table
> (migration `0027_pairing_codes.sql`), the `code_hash` (sha256) at-rest
> defense, the `state IN ('pending','claimed','expired')` enum, and the
> unified Python reaper. plan-15-06 **extends** plan-10-17 with the
> QR-flow security additions Story 15.5 needs (a 32-byte nonce bound to
> the QR; the response composition that ships server identity / SPKI
> hash; and the device-fan-out registration call after successful
> claim). The duplicate `pairing_codes` schema, code-generator, and
> sweeper that this plan once owned are **removed**; only the nonce
> column ALTER, the QR-URL composition, and the device-registration
> step survive.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Core endpoints | Owned by [plan-10-17](../10-auth-security/plan-10-17-auth-pair.md). This plan does NOT redeclare `POST /api/auth/pair`, `POST /api/auth/pair/claim`, or `GET /api/auth/pair/{code}` — those routes, their handlers, the code generator, and the rate-limit composition all live there. |
| Sibling endpoints | `GET /api/auth/pair` (list-mine) and `DELETE /api/auth/pair/{code}` (revoke) are added by *this* plan; they do not exist in plan-10-17. |
| Migration | `shared/db/migrations/0053_pairing_codes_qr.sql` — an `ALTER TABLE pairing_codes` that adds `nonce BYTEA NOT NULL DEFAULT '\x00'::bytea` (Postgres) / `BLOB` (SQLite). Bare default exists only so the ALTER is non-blocking on existing rows; new rows must override with 32 random bytes (CHECK in §2). |
| sqlc queries | `shared/db/queries/pairing_codes_qr.sql` — `ListMinePairingCodes`, `RevokePairingCode`, and a `GetPairingByHashWithNonce` helper that wraps plan-10-17's `GetPairingByHash`. |
| HTTP handlers | `api/internal/http/auth/pairing_qr.go`. The `claim` handler in plan-10-17 is wrapped via a **claim-extension hook** that runs nonce verification and device registration *after* plan-10-17's conditional UPDATE succeeds. |
| Audit | Reuses [plan-09-17](../09-library-management/plan-09-17-library-audit.md) `audit_log` with `category = 'pair'` (now permitted by the expanded CHECK enum — see [PLAN_REVIEW_14_17 §1.4](../../PLAN_REVIEW_14_17.md)). |
| Out of scope | The core pair/claim/poll flow (plan-10-17); the QR rendering / scanning / pin store ([Story 15.5](story-15-05-qr-pairing.md)); device fan-out registration ([Story 12.10](../12-mobile/story-12-10-device-registration-api.md)) — we call into it. |

## 1. Architecture diagram

```
   POST /api/auth/pair (issuer auth required)
         │
         ▼
   ┌──────────────────────┐
   │ pairing.Service      │
   │  - genCode(32^6)     │  ← 32-symbol alphabet, no IL01
   │  - genNonce(32 bytes)│
   │  - persist           │
   └─────────┬────────────┘
             │
             ▼
   ┌──────────────────────┐         ┌──────────────────────┐
   │ pairing_codes table  │         │ audit_log            │
   └──────────────────────┘         └──────────────────────┘

   POST /api/auth/pair/claim {code, nonce, ...}
         │
         ▼
   ┌──────────────────────┐
   │ pairing.Service      │
   │  - validate code     │
   │  - validate nonce    │
   │  - mint tokens (10.3)│
   │  - register device   │  ← Story 12.10
   │  - audit             │
   └──────────────────────┘
```

## 2. Database migration

`shared/db/migrations/0053_pairing_codes_qr.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
-- plan-10-17 created `pairing_codes` with the `code_hash`/`state`
-- design. This migration extends it with the QR-flow nonce that
-- Story 15.5 binds to the QR. The CHECK is added as NOT VALID first
-- so existing pre-Story-15.6 rows (which carry the bare zero nonce
-- default) don't trip the constraint; new rows MUST be 32 random
-- bytes from the application layer.
ALTER TABLE pairing_codes
    ADD COLUMN nonce BYTEA NOT NULL DEFAULT '\x00'::bytea;

ALTER TABLE pairing_codes
    ADD CONSTRAINT pairing_codes_nonce_len_chk
    CHECK (octet_length(nonce) IN (1, 32))   -- 1 = legacy default, 32 = QR-flow row
    NOT VALID;

-- Optional: also store the user who created the code so this plan's
-- list-mine and revoke endpoints can scope by user without a JOIN
-- back to the issuing audit row.
ALTER TABLE pairing_codes
    ADD COLUMN created_by_user_id UUID REFERENCES users(id) ON DELETE CASCADE;

CREATE INDEX pairing_codes_user_idx
    ON pairing_codes (created_by_user_id, state);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS pairing_codes_user_idx;
ALTER TABLE pairing_codes DROP COLUMN IF EXISTS created_by_user_id;
ALTER TABLE pairing_codes DROP CONSTRAINT IF EXISTS pairing_codes_nonce_len_chk;
ALTER TABLE pairing_codes DROP COLUMN IF EXISTS nonce;
-- +goose StatementEnd
```

SQLite variant uses `BLOB` for nonce and drops the `octet_length`
predicate (Go-side validation enforces length). The `state` column,
`code_hash` PK, and `expires_at` checks all come from plan-10-17.

## 3. Code generation

The pairing-code generator (8-char Crockford base32, sha256 hashed,
collision-retried) lives in plan-10-17's `auth.GeneratePairingCode` and
`auth.HashPairingCode`. This plan only adds the nonce generator:

```go
// api/internal/auth/pairing/nonce.go
func GenerateNonce() ([]byte, error) {
    n := make([]byte, 32)
    _, err := rand.Read(n)
    return n, err
}
```

The 32-byte nonce is uniform random from `crypto/rand`; it is the QR
binding secret (see Story 15.5). It is stored in the new `nonce`
column added by §2.

## 4. sqlc queries

The Insert/Claim/Get/Sweep queries are owned by plan-10-17 (see
`shared/db/queries/pairing_codes.sql`). This plan adds the
sibling-endpoint queries plus a nonce-aware getter:

`shared/db/queries/pairing_codes_qr.sql`:

```sql
-- name: ListMinePairingCodes :many
SELECT id, code_hash, device_kind, device_label, state,
       created_at, expires_at, claimed_by_device_id
FROM pairing_codes
WHERE created_by_user_id = $1
  AND created_at > now() - interval '24 hours'
ORDER BY created_at DESC;

-- name: RevokePairingCode :execrows
-- Conditional UPDATE so the user can only revoke their own pending code.
UPDATE pairing_codes
   SET state = 'expired', expires_at = now()
 WHERE code_hash = $1
   AND created_by_user_id = $2
   AND state = 'pending';

-- name: GetNonceByHash :one
SELECT nonce FROM pairing_codes WHERE code_hash = $1;
```

Race resolution for the core claim flow lives in plan-10-17's single
conditional UPDATE on `(code_hash, state='pending', expires_at>$3)`; the
loser sees `n=0` and is mapped to 409 `pair-code-already-claimed`. The
nonce check (§6) runs *before* the conditional UPDATE, so a wrong-nonce
claim attempt does not consume the code.

## 5. HTTP handlers

The `POST /api/auth/pair`, `POST /api/auth/pair/claim`, and
`GET /api/auth/pair/{code}` routes live in plan-10-17's
`api/internal/http/auth_pair.go`. This plan adds two sibling routes
plus an *issue-extension* hook that decorates plan-10-17's 201 response
with the QR URL.

`api/internal/http/auth/pairing_qr.go`:

```go
func MountPairingQR(r chi.Router, q *pairing.QRService) {
    r.With(requireAuth).Get("/auth/pair", listMine(q))
    r.With(requireAuth).Delete("/auth/pair/{code}", revoke(q))
}
```

The issue path: plan-10-17's `issuePair` handler is wrapped by an
`AfterIssue` hook (registered at server start) that:

1. Generates the 32-byte nonce.
2. UPDATEs the freshly-issued row to set `nonce`, `created_by_user_id`,
   `device_kind` (whose enum has been since plan-10-17).
3. Mutates the response body to add the `qr_url`.

```go
type AfterIssue func(ctx context.Context, code string, hash string, userID uuid.UUID, body map[string]any) error

func qrAfterIssue(q *pairing.QRService) AfterIssue {
    return func(ctx context.Context, code, hash string, userID uuid.UUID, body map[string]any) error {
        nonce, err := pairing.GenerateNonce()
        if err != nil { return err }
        if err := q.AttachNonce(ctx, hash, userID, nonce); err != nil { return err }
        body["qr_url"] = q.RenderQRURL(code, nonce)
        return nil
    }
}
```

`out` shape (after the hook):

```json
{
  "code": "BX7K9M",
  "qr_url": "https://server.example.com/pair?code=BX7K9M&mid=...&spki=...&n=...",
  "expires_at": "2026-05-04T15:00:00Z",
  "poll_url": "/api/auth/pair/BX7K9M"
}
```

The `qr_url`'s host is the server's canonical address (LAN host on
private; relay edge for public). The `spki` is the **current** TLS cert
SPKI hash; the `mid` is `mdns_id`; `n` is the base64-url nonce.

## 6. Claim flow

The base claim flow (hash lookup, conditional UPDATE, token mint, row
delete) is owned by plan-10-17's `pollPair` handler. This plan adds a
**pre-claim hook** that runs the nonce check and a **post-claim hook**
that registers the device with Story 12.10 and decorates the response
with the server's `mdns_id` and current SPKI hash:

```go
// api/internal/http/auth/pairing_qr.go
type BeforeClaim func(ctx context.Context, code, hash string, in ClaimInput) error
type AfterClaim  func(ctx context.Context, hash string, userID uuid.UUID, in ClaimInput, body map[string]any) error

func qrBeforeClaim(q *pairing.QRService) BeforeClaim {
    return func(ctx context.Context, code, hash string, in ClaimInput) error {
        rowNonce, err := q.GetNonce(ctx, hash)
        if err != nil { return ErrInvalidCode }
        if subtle.ConstantTimeCompare(rowNonce, in.Nonce) != 1 {
            q.audit(ctx, "claim-nonce-mismatch", hash)
            return ErrNonceMismatch
        }
        return nil
    }
}

func qrAfterClaim(q *pairing.QRService, devices *devices.Service) AfterClaim {
    return func(ctx context.Context, hash string, userID uuid.UUID, in ClaimInput, body map[string]any) error {
        deviceID, err := devices.Register(ctx, registerParamsFromClaim(userID, in))
        if err != nil { return err }
        body["server"] = map[string]any{
            "mdns_id": q.identity.MDNSID,
            "spki":    q.tls.CurrentSPKI(),
        }
        body["device_id"] = deviceID
        q.audit(ctx, "claim-success", hash)
        return nil
    }
}
```

The compile-bug fix: `subtle.ConstantTimeCompare` returns `1` on equality; the
correct guard is `!= 1`, **not** `!compare(...) == 1` (which Go parses as
`(!compare(...)) == 1` and always evaluates to `false`). The earlier
draft of this plan inverted the check; the corrected form above is the
canonical one.

Re-issued tokens come from plan-10-17's `issueJWTPair` (Stories 10.3 +
10.6: refresh + RS256 keys).

## 7. Rate limiting

The 6/min/IP cap on `POST /api/auth/pair` is owned by plan-10-17's
`ratelimit_auth.go`. This plan does not add a separate per-user
limiter; the per-IP cap is sufficient for the QR flow because the
issuer (a TV / desktop) is the rate-limit subject, not the human.

## 8. Sweeper

The pairing-code sweeper is owned by plan-10-17's unified Python
reaper (`pipeline/src/maktaba_pipeline/tasks/auth_reaper.py`), which
runs every 60 s and handles the `pending → expired` flip plus the
24-hour hard delete. This plan does not ship a duplicate Go-side
sweeper.

## 9. Audit

Every `create`, `claim-success`, `claim-failure-{kind}`, and `revoke`
writes one row to `audit_log` with `category = 'pair'` (per the AC).
plan-10-17 emits the base `pair.code-issued` and `pair.code-claimed`
rows; this plan adds `pair.claim-nonce-mismatch` (from the
pre-claim hook) and `pair.code-revoked` (from the revoke endpoint).
The `'pair'` category was added to the
[plan-09-17](../09-library-management/plan-09-17-library-audit.md)
CHECK enum as part of the same
[PLAN_REVIEW_14_17 §1.4](../../PLAN_REVIEW_14_17.md) resolution.

The `claim-failure` rows are useful for detecting brute-force attempts;
if a single `created_by_user_id`'s codes accumulate ≥ 10 failed
attempts within 1 hour, the security audit
([Story 10.16](../10-auth-security/plan-10-16-security-audit.md))
raises a notification.

## 10. Test plan

### 10.1 Migration

| Test | What it pins |
|---|---|
| `TestPairingCodesMigrationApplies` | Postgres + SQLite. |
| `TestNonceLengthChecked` | Insert with 16-byte nonce → CHECK violation. |
| `TestExpiresAfterCreated` | Insert with `expires_at < created_at` → CHECK violation. |
| `TestUserCascadeDeletes` | Delete user → pairing_codes rows gone. |

### 10.2 Service & HTTP

| Test | What it pins |
|---|---|
| `TestCreateReturnsCodeQRAndExpiry` | 201 with the three fields. |
| `TestCreateRequiresAuth` | Anonymous → 401. |
| `TestCreateRateLimit` | 6th request in a minute → 429 with `Retry-After`. |
| `TestClaimSuccess` | Right code + right nonce → 200 with tokens; row's `claimed_at` set. |
| `TestClaimWrongNonce` | 400 `nonce-mismatch`; row remains unclaimed. |
| `TestClaimAlreadyClaimed` | Two claims; second 409 `pair-code-already-claimed` (matches plan-10-17 status). |
| `TestClaimExpired` | Wait past `expires_at`; 410 `pair-code-expired` (matches plan-10-17 status). |
| `TestClaimUnknownCode` | 404 `pair-code-unknown` (matches plan-10-17 status). |
| `TestClaimRevokedCode` | DELETE then claim → 410 `pair-code-expired` (revoke flips state to `expired`). |
| `TestClaimAfterUserDeleted` | User deleted → CASCADE gone → 404 `pair-code-unknown`. |
| `TestClaimIsConstantTimeOnNonce` | Two-sample t-test on nonce-mismatch latency vs. invalid-code latency: difference statistically insignificant. |
| `TestRevokeIdempotent` | DELETE twice → both 204. |
| `TestRevokeOnlyOwnCodes` | User A revokes user B's code → 404. |
| `TestListMineReturnsLast24h` | Older codes (created 25 h ago) excluded. |
| `TestSweeperMarksExpired` | After 30 s tick, expired-unclaimed rows have `claimed_at = expires_at`. |
| `TestSweeperHardDeletes7Days` | 8-day-old row gone after sweep. |

### 10.3 Race

| Test | What it pins |
|---|---|
| `TestConcurrentClaims` | 100 parallel claims of the same code → exactly one 200, 99 × 400 `code-already-claimed`. |

## 11. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| TLS cert rotated between issue and claim | The `qr_url`'s `spki` is the issue-time pin; the claim response includes the *current* SPKI; client surfaces re-confirmation. | `TestClaimAfterCertRotation` |
| User revokes mid-claim | `RevokePairingCode` flips `claimed_at = expires_at`; claim sees zero rows on update → 400 `code-revoked`. | `TestRevokeMidClaim` |
| Created-by user deleted | CASCADE → row gone → claim 400 `invalid-code`. | `TestClaimAfterUserDeleted` |
| Two phones race | DB serializes; only one wins. | `TestConcurrentClaims` |
| Rate-limiter saturated | 429 with `Retry-After`; legitimate users in the same household hit it rarely. | `TestCreateRateLimit` |
| Brute-force nonce | 32 bytes random; even at 1k attempts/sec, 2^256 → infeasible. Constant-time compare resists timing oracle. | `TestClaimIsConstantTimeOnNonce` |
| Server clock skew | `expires_at` is server-side; client clock irrelevant. | `TestClaimExpired` |
| Audit log table absent in test SQLite | The audit writes are best-effort: failure logs but does not fail the request. | `TestAuditFailureNonFatal` |

## 12. Acceptance checklist

**Schema**
- [ ] `pairing_codes` exists on Postgres + SQLite with full constraint set.

**Endpoints**
- [ ] `POST /api/auth/pair` (owned by plan-10-17) → 201 with code+qr_url+expires_at+poll_url after this plan's `AfterIssue` hook decorates the body.
- [ ] `POST /api/auth/pair/claim` (owned by plan-10-17) → 204 on success after this plan's `BeforeClaim` nonce check passes; status codes (404 / 410 / 409) match plan-10-17.
- [ ] `DELETE /api/auth/pair/{code}` idempotent (this plan).
- [ ] `GET /api/auth/pair` returns last 24 h (this plan).

**Behaviour**
- [ ] 6/min/IP rate limit (plan-10-17); 60 s sweeper + 24 h hard delete (plan-10-17).
- [ ] Audit row on every action (`pair.code-issued`, `pair.code-claimed` from plan-10-17; `pair.claim-nonce-mismatch`, `pair.code-revoked` from this plan).

**Tests**
- [ ] All §10 tests pass.

**Docs**
- [ ] `specs/epics/15-discovery/README.md` ticks story 15.6.
