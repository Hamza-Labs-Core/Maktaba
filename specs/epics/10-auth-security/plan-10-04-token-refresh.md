# Implementation Plan — Story 10.4 Token refresh + rotation

> Companion to [story-10-04-token-refresh.md](story-10-04-token-refresh.md).
> Refresh-token storage and the issue path are owned by
> [Story 10.3](plan-10-03-native-login.md). Audit-row writes go through
> [Story 10.16](story-10-16-security-audit.md). This story owns the
> *rotation* and *reuse-detection* state machine.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Handler | `api/internal/http/auth_refresh.go` — `POST /api/auth/refresh`. |
| Rotation logic | `api/internal/auth/refresh_rotate.go` — `RotateOnce(ctx, oldID, params) (IssueResult, error)` — single transactional swap. |
| Reuse detection | A revoked token re-presented → caller-side branch in the handler that calls `RevokeFamily(ctx, fam)` and writes the audit row. |
| Replay-detection invariant | "First reuse of a revoked token revokes the entire family." Implemented via the `replaced_by` chain plus `revoked_at`. |
| Out of scope | Logout endpoints (10.5), JWT issuance details (10.3 owns claims building; this story re-uses `issueAccessClaims(...)`). |

## 1. Architecture diagram

```
POST /api/auth/refresh {refresh_token: "mkt_rt_v1.<id>.<secret>"}
   ▼
┌────────────────────────────────────────────────────────────────┐
│ http/auth_refresh.go                                            │
│   1. id, secret := refresh.ParsePlaintext(token)                 │
│   2. row, hash := store.GetByID(ctx, id)                         │
│        not found → 401 invalid-refresh                           │
│   3. ok := argon2.Verify(secret, hash)                           │
│        !ok → 401 invalid-refresh (no family revoke; could be     │
│              attacker probing)                                   │
│   4. branches by row state:                                      │
│        a) revoked_at != NIL              → REUSE DETECTED         │
│              RevokeFamily(family_id)                              │
│              audit: refresh.replay-detected                       │
│              401 type=refresh-replayed                            │
│        b) expires_at < now()             → 401 type=refresh-expired│
│              (no family revoke; this is normal expiry)            │
│        c) otherwise (live)              → ROTATE                  │
│              RotateOnce(oldID=row.ID, IssueParams{                │
│                UserID, FamilyID=row.FamilyID, TTL,                │
│                ClientMeta: refresh of row.ClientMeta              │
│              })                                                   │
│              re-mint access token with current lib[] snapshot     │
│              return {access_token, refresh_token, ...}            │
└────────────────────────────────────────────────────────────────┘
```

`RotateOnce` is a single transaction:

```
BEGIN
  UPDATE refresh_tokens
     SET revoked_at = now(), replaced_by = $newID
   WHERE id = $oldID
     AND revoked_at IS NULL
     AND expires_at > now()
   RETURNING id;            -- 0 rows → race lost; abort + retry as reuse
  INSERT INTO refresh_tokens (id, user_id, hash, family_id, ...)
       VALUES (...);
COMMIT
```

The 0-row return from the UPDATE is the canonical "someone got here
first" signal. We classify that as reuse: under contention the loser
has either (a) been beaten by a legitimate parallel refresh from the
same client (unusual but possible) or (b) been beaten by an attacker
who got the same token. Either way, the safe action is to revoke the
family. We accept the small false-positive rate against the protection
benefit; clients are documented to serialize refresh per-device
(story EC).

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/auth/refresh_rotate.go` | `RotateOnce`, `RevokeFamily`. |
| `api/internal/http/auth_refresh.go` | HTTP handler. |
| `api/internal/auth/refresh_rotate_test.go` | Unit tests. |
| `api/internal/http/auth_refresh_test.go` | Integration tests. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `shared/db/queries/refresh_tokens.sql` | Add `RotateRefreshTokenSwap`, `RevokeRefreshFamily`, `GetRefreshTokenLatest`. |
| `api/internal/http/router.go` | `r.Post("/api/auth/refresh", refreshHandler(...))`. |
| `api/internal/auth/refresh.go` | Expose `issueAccessClaims(user, libs, cfg)` so this handler can re-mint without re-implementing the shape. |
| `api/internal/config/config.go` | Add `Auth.RefreshSerializeWindowMs` (default 50) — purely for telemetry; no enforcement. |

### 2.3 Function signatures

```go
// api/internal/auth/refresh_rotate.go
func (s *store) RotateOnce(ctx context.Context, oldID uuid.UUID, p IssueParams) (IssueResult, error)
func (s *store) RevokeFamily(ctx context.Context, familyID uuid.UUID) (revokedCount int, err error)

// Sentinel returned when the conditional UPDATE matches 0 rows —
// the caller (handler) treats this as reuse.
var ErrRefreshAlreadyRotated = errors.New("auth: refresh token already rotated")
```

## 3. SQL — sqlc additions

`shared/db/queries/refresh_tokens.sql`:

```sql
-- name: RotateRefreshTokenSwap :one
-- Atomically marks the old row revoked + replaced and returns its
-- family_id and user_id so the caller can build IssueParams without
-- a second SELECT. Returns 0 rows on:
--   (a) row already revoked (reuse), or
--   (b) row expired between SELECT and this UPDATE (race).
UPDATE refresh_tokens
   SET revoked_at  = now(),
       replaced_by = $2
 WHERE id           = $1
   AND revoked_at  IS NULL
   AND expires_at  > now()
RETURNING user_id, family_id;

-- name: RevokeRefreshFamily :execrows
UPDATE refresh_tokens
   SET revoked_at = now()
 WHERE family_id = $1
   AND revoked_at IS NULL;

-- name: GetRefreshTokenByID :one
SELECT id, user_id, hash, family_id, issued_at, expires_at,
       revoked_at, replaced_by, client_meta
  FROM refresh_tokens
 WHERE id = $1;
```

The `RotateRefreshTokenSwap` does the swap *in the same transaction* as
the new INSERT; the handler runs both as one tx (`db.WithTx(...)`).

## 4. Rotation logic

```go
// api/internal/auth/refresh_rotate.go
package auth

import (
    "context"
    "errors"
    "time"

    "github.com/google/uuid"
)

func (s *store) RotateOnce(ctx context.Context, oldID uuid.UUID, p IssueParams) (IssueResult, error) {
    secret := make([]byte, 32)
    if _, err := rand.Read(secret); err != nil {
        return IssueResult{}, err
    }
    newID := uuid.Must(uuid.NewV7())
    hash, err := Hash(string(secret), DefaultArgon2)
    if err != nil {
        return IssueResult{}, err
    }

    var out IssueResult
    err = s.db.WithTx(ctx, func(q *db.Queries) error {
        row, err := q.RotateRefreshTokenSwap(ctx, db.RotateRefreshTokenSwapParams{
            ID: oldID, ReplacedBy: newID,
        })
        if errors.Is(err, pgx.ErrNoRows) {
            return ErrRefreshAlreadyRotated
        }
        if err != nil { return err }

        inserted, err := q.InsertRefreshToken(ctx, db.InsertRefreshTokenParams{
            ID:         newID,
            UserID:     row.UserID,
            Hash:       hash,
            FamilyID:   row.FamilyID,
            ExpiresAt:  time.Now().Add(p.TTL),
            ClientMeta: encodeMeta(p.ClientMeta),
        })
        if err != nil { return err }

        plain := RefreshPrefix +
            base64.RawURLEncoding.EncodeToString(newID[:]) + "." +
            base64.RawURLEncoding.EncodeToString(secret)
        out = IssueResult{Row: rowFromInsert(inserted), Plaintext: plain}
        return nil
    })
    return out, err
}

func (s *store) RevokeFamily(ctx context.Context, fam uuid.UUID) (int, error) {
    return s.q.RevokeRefreshFamily(ctx, fam)
}
```

The `WithTx` wrapper uses `BEGIN` on PG and `BEGIN IMMEDIATE` on SQLite
to serialize concurrent rotations cleanly under WAL.

## 5. HTTP handler

```go
// api/internal/http/auth_refresh.go
package http

import (
    "encoding/json"
    "errors"
    "net/http"
    "time"

    "maktaba/api/internal/auth"
)

func refreshHandler(
    refresh auth.RefreshStore, signer auth.Signer,
    users auth.Store, libACL auth.LibACL,
    audit auth.AuditSink, cfg auth.Config,
) http.HandlerFunc {
    type req struct {
        RefreshToken string `json:"refresh_token"`
    }
    return func(w http.ResponseWriter, r *http.Request) {
        var body req
        if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
            problem(w, http.StatusBadRequest, "invalid-json", "")
            return
        }

        id, secret, err := auth.ParsePlaintext(body.RefreshToken)
        if err != nil {
            problem(w, http.StatusUnauthorized, "invalid-refresh", "")
            return
        }
        row, hash, err := refresh.GetByID(r.Context(), id)
        if err != nil {
            problem(w, http.StatusUnauthorized, "invalid-refresh", "")
            return
        }
        ok, _, _ := auth.Verify(string(secret), hash)
        if !ok {
            // Wrong secret for a known id — could be a probe; do NOT revoke
            // family on a hash mismatch (the row's secret hasn't been used).
            problem(w, http.StatusUnauthorized, "invalid-refresh", "")
            return
        }

        // Reuse detection: row is already revoked.
        if row.RevokedAt != nil {
            n, _ := refresh.RevokeFamily(r.Context(), row.FamilyID)
            audit.Record(r.Context(), auth.AuditRefreshReplay{
                UserID: row.UserID, FamilyID: row.FamilyID,
                IP: clientIP(r), RevokedCount: n,
            })
            problem(w, http.StatusUnauthorized, "refresh-replayed", "")
            return
        }
        if !row.ExpiresAt.After(time.Now()) {
            problem(w, http.StatusUnauthorized, "refresh-expired", "")
            return
        }

        // Live row — rotate.
        issued, err := refresh.RotateOnce(r.Context(), id, auth.IssueParams{
            UserID:     row.UserID,
            FamilyID:   row.FamilyID,
            TTL:        time.Duration(cfg.RefreshTTLSec) * time.Second,
            ClientMeta: row.ClientMeta, // carry forward
        })
        if errors.Is(err, auth.ErrRefreshAlreadyRotated) {
            n, _ := refresh.RevokeFamily(r.Context(), row.FamilyID)
            audit.Record(r.Context(), auth.AuditRefreshReplay{
                UserID: row.UserID, FamilyID: row.FamilyID,
                IP: clientIP(r), RevokedCount: n, Reason: "race-lost",
            })
            problem(w, http.StatusUnauthorized, "refresh-replayed", "")
            return
        }
        if err != nil {
            problem(w, http.StatusInternalServerError, "internal", "")
            return
        }

        // Re-snapshot lib[] on every refresh so ACL revocations propagate.
        user, err := users.GetByID(r.Context(), row.UserID)
        if err != nil {
            problem(w, http.StatusUnauthorized, "unknown-sub", "")
            return
        }
        libs, _ := libACL.LibrariesForUser(r.Context(), user.ID)
        libStrs := make([]string, len(libs))
        for i, l := range libs { libStrs[i] = l.String() }

        now := time.Now().Unix()
        access, err := auth.Mint(auth.Claims{
            Iss: "maktaba", Aud: "api", Sub: user.ID.String(),
            Iat: now, Exp: now + int64(cfg.AccessTTLSec),
            IsAdmin: user.IsAdmin, Lib: libStrs,
        }, signer)
        if err != nil {
            problem(w, http.StatusInternalServerError, "signing-unavailable", "")
            return
        }

        writeJSON(w, http.StatusOK, map[string]any{
            "access_token":       access,
            "access_expires_in":  cfg.AccessTTLSec,
            "refresh_token":      issued.Plaintext,
            "refresh_expires_in": cfg.RefreshTTLSec,
            "user": map[string]any{
                "id": user.ID, "username": user.Username, "is_admin": user.IsAdmin,
            },
        })
    }
}
```

## 6. Reuse-detection state machine

```
                  ┌───────────────────┐
   issue   ──────►│  live (revoked=∅) │──┐
                  └───────────────────┘  │ rotate (legit)
                          │              ▼
                          │       ┌──────────────────┐
                          │       │ revoked, replaced │──┐
                          │       └──────────────────┘  │ presented again
                          │              ▲              │ (REUSE)
                          │              │              ▼
                          │              │   ┌──────────────────────┐
                          │              │   │ family fully revoked │
                          │              │   └──────────────────────┘
                          ▼              │              ▲
                  ┌───────────────────┐  │              │
                  │ expired (TTL hit) │  │   sibling presented
                  └───────────────────┘  │   while one revoked
                                         │              │
                                         └── normal rotation chain
```

A *family* is the set of refresh rows sharing `family_id`. Rotation
walks `replaced_by` forward; revocation flattens by setting
`revoked_at` on every row in the family in one UPDATE (no walk). The
handler does not need to walk the chain because `RevokeFamily` is a
single SQL statement keyed by `family_id`.

## 7. Test plan

### 7.1 Rotation (`refresh_rotate_test.go`)

| Test | What it pins |
|---|---|
| `TestRotateOnceMarksOldRevokedAndInsertsNew` | After RotateOnce: old row has `revoked_at != NULL` AND `replaced_by = newID`; new row has `revoked_at = NULL`, same `family_id`, fresh `id`. |
| `TestRotateOncePreservesFamilyID` | A 5-step chain (rotate, rotate, ...) → all 5 rows share one `family_id`. |
| `TestRotateOnceTTLApplied` | New row's `expires_at` ≈ now + cfg.RefreshTTLSec (within 1s). |
| `TestRotateOnceClientMetaCarried` | Client meta from old row is carried into new row's `client_meta`. |
| `TestRotateOnceConcurrentReturnsErrAlreadyRotated` | Two goroutines call RotateOnce on same oldID; one succeeds, the other returns `ErrRefreshAlreadyRotated`. |
| `TestRotateOnceOnExpiredRowReturnsErr` | Row with `expires_at = now()-1s` → `ErrRefreshAlreadyRotated` (the conditional UPDATE matches 0). |
| `TestRotateOnceOnRevokedRowReturnsErr` | Row already revoked → 0 rows updated → `ErrRefreshAlreadyRotated`. |
| `TestRevokeFamilyRevokesAll` | A 5-step chain, all live; RevokeFamily → all 5 rows now have `revoked_at`. Returns count=5. |
| `TestRevokeFamilyIdempotent` | Second call returns count=0; no-op. |

### 7.2 Handler (`auth_refresh_test.go`)

| Test | What it pins |
|---|---|
| `TestRefreshSuccessRotates` | Issue token A; refresh with A → response contains access_token + refresh_token B; A's row revoked, B's row live, both share family_id. |
| `TestRefreshUsedRefreshNoLongerValid` | After successful refresh, second request with A → 401 `refresh-replayed`; family_id from A's row is now fully revoked. |
| `TestRefreshExpiredReturns401Expired` | Force `expires_at = now()-1s`; refresh → 401 `refresh-expired`; family NOT revoked; siblings still work (sibling check separately). |
| `TestRefreshUnknownIDReturns401InvalidRefresh` | A plaintext with a random id (not in DB) → 401 `invalid-refresh`. |
| `TestRefreshBadSecretReturns401InvalidRefresh` | Take a valid id but corrupt the secret → 401 `invalid-refresh`; row is NOT revoked (probe protection). |
| `TestRefreshReuseRevokesEntireFamily` | Issue A → rotate to B → present A again → 401 `refresh-replayed`; B is now revoked too; AuditRefreshReplay row written with `revoked_count=2`. |
| `TestRefreshUpdatesLibClaim` | Pre-rotate: user has lib L1 only → access token `lib=[L1]`. Add ACL row for L2; rotate; new access token has `lib=[L1, L2]`. |
| `TestRefreshConcurrentTriggersReuseDetection` | Two parallel refreshes on the same A: one returns 200 with B; the other returns 401 `refresh-replayed`; family is then fully revoked. |
| `TestRefreshOnRevokedAccountReturns401` | Delete the user (or revoke their account); refresh → 401 (`unknown-sub` once user is gone via CASCADE; `invalid-refresh` if FK CASCADE swept the row). |

### 7.3 Audit integration

| Test | What it pins |
|---|---|
| `TestRefreshReplayWritesAuditRow` | Reuse-detect path writes one `audit_log` row with `category='security', event='refresh.replay-detected', payload.user_id, payload.family_id, payload.ip`. |
| `TestRefreshExpiredDoesNotWriteAudit` | Normal-expiry path writes no security audit (only the metric increments). |

### 7.4 Cross-dialect parity

`auth_refresh_dialect_test.go` runs both happy and reuse paths against
PG and SQLite. SQLite's `BEGIN IMMEDIATE` ensures two concurrent
RotateOnce calls serialize correctly.

## 8. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Network race: client retries refresh before response arrives | Both requests carry the same A. The first successfully rotates → 200 + new tokens. The second sees A is revoked → 401 `refresh-replayed` + entire family revoked. The client SDK is documented to serialize refreshes (story EC). | `TestRefreshConcurrentTriggersReuseDetection` |
| Wrong secret on a valid id | 401 `invalid-refresh`; row NOT revoked. Distinguished from "presented an old token": the row's secret has not actually been used. | `TestRefreshBadSecretReturns401InvalidRefresh` |
| User deleted between issue and refresh | CASCADE deleted the row → `GetByID` returns not-found → 401 `invalid-refresh`. (No `unknown-sub` because the row is gone before we look up the user.) | `TestRefreshOnDeletedUserReturns401` |
| Refresh against a token whose `kid` was rotated out | Refresh issues a *new* access token signed with the *current* signing key, so the old `kid` is irrelevant; only the refresh's own row state matters. | n/a |
| Token presented twice within the same millisecond | The conditional UPDATE serializes; one returns 1 row, the other returns 0 → reuse. | `TestRefreshConcurrentTriggersReuseDetection` |
| Family has 1000+ rows due to a long-lived device | RevokeFamily updates 1000 rows in one statement; bounded by `family_id` index. The reaper sweeps revoked rows after 90 days (Story 10.3 §3.0 reaper index). | n/a (load-test) |
| Lib snapshot grew very large | Same cap as Story 10.3: `lib` capped at 1000 entries with a WARN. Documented in Story 10.13. | Story 10.13 plan |
| `client_meta` contains stale UA / IP | Story EC: we carry forward the *original* meta on rotation. The audit log captures the *current* IP via `clientIP(r)` independently. | `TestRotateOnceClientMetaCarried` |
| Refresh token still valid after admin's `keys rotate --immediate` | Refresh works; the *new* access token is signed with the new key, so it verifies. Only the old in-flight access tokens are invalidated. Documented in Story 10.6 plan. | Story 10.6 plan |
| `audit.Record` fails mid-flow | Best-effort; the handler proceeds with the 401. Audit failures are logged at WARN; the security guarantee is still upheld (family is revoked). | `TestRefreshReplayProceedsOnAuditFailure` |

## 9. Dependencies

No new dependencies beyond Story 10.3.

## 10. Acceptance checklist

**SQL**
- [ ] `RotateRefreshTokenSwap` is a single conditional UPDATE that returns 0 rows on (revoked OR expired) — the canonical reuse signal.
- [ ] `RevokeRefreshFamily` is a single UPDATE keyed by `family_id`.

**Rotation**
- [ ] AC-1: refresh response carries new access + new refresh; the old row is `revoked_at != NULL` AND `replaced_by = newID`.
- [ ] AC-1 (re-snapshot lib): the new access token's `lib[]` reflects the current ACL state, not the old token's snapshot.

**Reuse detection**
- [ ] AC-2: presenting an already-revoked refresh revokes the *entire* family in one UPDATE; an audit row `event='refresh.replay-detected'` is written; response is 401 `refresh-replayed`.

**Expiry**
- [ ] AC-3: an expired refresh returns 401 `refresh-expired`; family NOT revoked; siblings remain valid.

**Race / contention**
- [ ] Two concurrent rotations on the same id: one succeeds; the other triggers the reuse path. Documented in client SDKs that refresh must be serialized per-device.

**Tests**
- [ ] All §7 tests pass on both dialects.

**Docs**
- [ ] README.md ticks story 10.4.
