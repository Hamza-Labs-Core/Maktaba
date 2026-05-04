# Implementation Plan — Story 15.6 API: device pairing endpoints

> Companion to [story-15-06-pairing-api.md](story-15-06-pairing-api.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration file | `shared/db/migrations/0053_pairing_codes.sql` (Postgres) and `0053_pairing_codes.sqlite.sql`. |
| sqlc queries | `shared/db/queries/pairing_codes.sql`. |
| HTTP handlers | `api/internal/http/auth/pairing.go` mounted under `/api/auth/pair`. |
| Pairing service | `api/internal/auth/pairing/service.go` — code generation, claim, sweep. |
| Sweeper | A goroutine in the API service running every 30 s. |
| Audit | Reuses Epic 21 Story 21.6 `audit_log` (`category = 'pair'`). |
| Out of scope | The QR rendering / scanning / pin store ([Story 15.5](story-15-05-qr-pairing.md)); device fan-out registration ([Story 12.10](../12-mobile/story-12-10-device-registration-api.md)) — we call into it. |

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

`shared/db/migrations/0053_pairing_codes.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE pairing_codes (
    code                  TEXT PRIMARY KEY,
    nonce                 BYTEA NOT NULL,
    created_by_user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_label          TEXT,
    device_kind           TEXT NOT NULL CHECK (device_kind IN ('mobile','desktop','tv')),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at            TIMESTAMPTZ NOT NULL,
    claimed_at            TIMESTAMPTZ,
    claimed_by_device_id  UUID REFERENCES devices(id) ON DELETE SET NULL,
    CHECK (octet_length(nonce) = 32),
    CHECK (length(code) = 6),
    CHECK (expires_at > created_at)
);

CREATE INDEX pairing_codes_expires_at_idx
    ON pairing_codes (expires_at)
    WHERE claimed_at IS NULL;

CREATE INDEX pairing_codes_user_idx
    ON pairing_codes (created_by_user_id, claimed_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS pairing_codes;
-- +goose StatementEnd
```

SQLite variant uses `BLOB` for nonce and removes Postgres-specific `octet_length`. Go-side validation checks length already.

## 3. Code generation

```go
// api/internal/auth/pairing/codegen.go
const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // 32 chars; no I, L, 0, 1

func generateCode() (string, error) {
    var b [6]byte
    _, err := rand.Read(b[:])
    if err != nil { return "", err }
    out := make([]byte, 6)
    for i, x := range b { out[i] = alphabet[int(x) % len(alphabet)] }
    return string(out), nil
}

func generateNonce() ([]byte, error) {
    n := make([]byte, 32)
    _, err := rand.Read(n)
    return n, err
}
```

The `% len(alphabet)` introduces a small bias; with 32 dividing 256 evenly, the bias is exactly 0. The choice of 32 letters is therefore principled rather than convenient.

## 4. sqlc queries

`shared/db/queries/pairing_codes.sql`:

```sql
-- name: InsertPairingCode :exec
INSERT INTO pairing_codes (code, nonce, created_by_user_id, device_label, device_kind, expires_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetPairingCode :one
SELECT * FROM pairing_codes WHERE code = $1;

-- name: ClaimPairingCode :one
UPDATE pairing_codes
SET claimed_at = now(), claimed_by_device_id = $2
WHERE code = $1
  AND claimed_at IS NULL
  AND expires_at > now()
RETURNING *;

-- name: RevokePairingCode :exec
UPDATE pairing_codes
SET claimed_at = expires_at        -- mark as terminal
WHERE code = $1
  AND created_by_user_id = $2
  AND claimed_at IS NULL;

-- name: ListPendingPairingCodes :many
SELECT * FROM pairing_codes
WHERE created_by_user_id = $1
  AND created_at > now() - interval '24 hours'
ORDER BY created_at DESC;

-- name: SweepExpiredPairingCodes :exec
UPDATE pairing_codes
SET claimed_at = expires_at
WHERE claimed_at IS NULL AND expires_at <= now();

-- name: DeleteOldPairingCodes :exec
DELETE FROM pairing_codes WHERE created_at < now() - interval '7 days';
```

The `ClaimPairingCode` is the **race-resolver**: the `WHERE claimed_at IS NULL` predicate plus the row lock (default `READ COMMITTED`) means two concurrent claims with the right code can only succeed once. The losing claim sees zero rows and returns `400 code-already-claimed`.

## 5. HTTP handlers

`api/internal/http/auth/pairing.go`:

```go
func MountPairing(r chi.Router, s *pairing.Service) {
    r.Route("/auth/pair", func(r chi.Router) {
        r.With(requireAuth).Post("/", create(s))
        r.With(requireAuth).Get("/", listMine(s))
        r.With(requireAuth).Delete("/{code}", revoke(s))
        r.Post("/claim", claim(s))                // unauthenticated; code is the credential
    })
}

func create(s *pairing.Service) http.HandlerFunc {
    type req struct {
        DeviceLabel string `json:"device_label,omitempty"`
        DeviceKind  string `json:"device_kind"`
    }
    return func(w http.ResponseWriter, r *http.Request) {
        var body req
        if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
            problem(w, 400, "invalid-json", ""); return
        }
        if !validKind(body.DeviceKind) { problem(w, 422, "invalid-device-kind", ""); return }
        u := auth.UserFromContext(r.Context())
        if !s.RateLimiter.Take(u.ID) {
            problem(w, 429, "too-many-requests", "5/min/user"); return
        }
        out, err := s.Create(r.Context(), pairing.CreateParams{
            UserID: u.ID, DeviceKind: body.DeviceKind, DeviceLabel: body.DeviceLabel,
        })
        if err != nil { problem(w, 500, "internal", ""); return }
        writeJSON(w, 201, out)
    }
}
```

`out` shape:

```json
{
  "code": "BX7K9M",
  "qr_url": "https://server.example.com/pair?code=BX7K9M&mid=...&spki=...&n=...",
  "expires_at": "2026-05-04T15:00:00Z"
}
```

The `qr_url`'s host is the server's canonical address (LAN host on private; relay edge for public). The `spki` is the **current** TLS cert SPKI hash; the `mid` is `mdns_id`; `n` is the base64-url nonce.

## 6. Claim flow

```go
func (s *Service) Claim(ctx context.Context, in ClaimParams) (ClaimResult, error) {
    row, err := s.db.GetPairingCode(ctx, in.Code)
    if err != nil { return ClaimResult{}, ErrInvalidCode }
    if !subtle.ConstantTimeCompare(row.Nonce, in.Nonce) == 1 {
        s.audit(ctx, "claim-nonce-mismatch", row)
        return ClaimResult{}, ErrNonceMismatch
    }
    if row.ClaimedAt.Valid { return ClaimResult{}, ErrAlreadyClaimed }
    if row.ExpiresAt.Before(time.Now()) { return ClaimResult{}, ErrCodeExpired }

    deviceID, err := s.devices.Register(ctx, devices.RegisterParams{
        UserID: row.CreatedByUserID,
        Kind:   row.DeviceKind,
        Label:  row.DeviceLabel,
        OS:     in.OS, AppVersion: in.AppVersion, Locale: in.Locale,
        PushToken: in.DeviceToken,
    })
    if err != nil { return ClaimResult{}, err }

    upd, err := s.db.ClaimPairingCode(ctx, db.ClaimPairingCodeParams{
        Code: in.Code, ClaimedByDeviceID: deviceID,
    })
    if err != nil { return ClaimResult{}, ErrAlreadyClaimed } // race lost

    access, refresh, err := s.tokens.Mint(ctx, upd.CreatedByUserID, deviceID)
    if err != nil { return ClaimResult{}, err }

    s.audit(ctx, "claim-success", upd)
    return ClaimResult{
        AccessToken: access, RefreshToken: refresh,
        User: s.userView(upd.CreatedByUserID),
        Server: ServerView{MDNSID: s.identity.MDNSID, SPKI: s.tls.CurrentSPKI()},
    }, nil
}
```

Constant-time nonce compare uses `subtle.ConstantTimeCompare`. Re-issued tokens come from Story 10.3 and Story 10.6 (refresh + RS256 keys).

## 7. Rate limiting

`s.RateLimiter` is a token bucket per `user_id`: 5 codes / minute. Implementation reuses the rate-limit middleware from [Story 10.12](../10-auth-security/plan-10-12-rate-limiting-auth.md) but scopes by user ID instead of IP.

Why per-user, not per-IP: a single TV in a household can legitimately request multiple codes; only abuse from the same authenticated user is suspicious.

## 8. Sweeper

```go
func (s *Service) RunSweeper(ctx context.Context) {
    t := time.NewTicker(30 * time.Second); defer t.Stop()
    for {
        select {
        case <-t.C:
            _ = s.db.SweepExpiredPairingCodes(ctx)
            _ = s.db.DeleteOldPairingCodes(ctx)         // > 7 days
        case <-ctx.Done():
            return
        }
    }
}
```

The 30 s cadence is the AC; we deliberately avoid Postgres-side `pg_cron` so SQLite is supported.

## 9. Audit

Every `create`, `claim-success`, `claim-failure-{kind}`, and `revoke` writes one row to `audit_log` with `category = 'pair'` (per the AC). The `claim-failure` rows are useful for detecting brute force attempts; if a single `created_by_user_id`'s codes accumulate ≥ 10 failed attempts within 1 hour, the security audit ([Story 10.16](../10-auth-security/plan-10-16-security-audit.md)) raises a notification.

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
| `TestClaimAlreadyClaimed` | Two claims; second 400 `code-already-claimed`. |
| `TestClaimExpired` | Wait past `expires_at`; 400 `code-expired`. |
| `TestClaimUnknownCode` | 400 `invalid-code`. |
| `TestClaimRevokedCode` | DELETE then claim → 400 `code-revoked`. |
| `TestClaimAfterUserDeleted` | User deleted → CASCADE gone → 400 `invalid-code`. |
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
- [ ] `POST /api/auth/pair` → 201 with code+qr_url+expires_at.
- [ ] `POST /api/auth/pair/claim` → 200 on success or 400 with the documented error kinds.
- [ ] `DELETE /api/auth/pair/{code}` idempotent.
- [ ] `GET /api/auth/pair` returns last 24 h.

**Behaviour**
- [ ] 5/min/user rate limit; 30 s sweeper; 7-day hard delete.
- [ ] Audit row on every action.

**Tests**
- [ ] All §10 tests pass.

**Docs**
- [ ] `specs/epics/15-discovery/README.md` ticks story 15.6.
