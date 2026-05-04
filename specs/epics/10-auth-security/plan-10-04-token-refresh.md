# Plan 10.4 — Token refresh + rotation — implementation

> Implementation plan for [story-10-04-token-refresh.md](story-10-04-token-refresh.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Depends on the `refresh_tokens` table, the JWT issuer,
> and the `LibraryACLResolver` from
> [Plan 10.3](plan-10-03-native-login.md). Reuses the bearer middleware
> from Plan 10.3 only for the access-token side; the refresh endpoint is
> deliberately UNAUTHENTICATED at the bearer-middleware layer (the
> refresh token IS the credential). Logout (revoke a family explicitly)
> is [Story 10.5](story-10-05-logout-revocation.md). Audit-row writes to
> `audit_log` are mediated by [Story 10.16](story-10-16-security-audit.md);
> this plan calls a thin `audit.Write` helper that 10.16 will own.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Rotation runs in a single Postgres transaction.** Inside the tx: (a) `UPDATE refresh_tokens SET revoked_at=now(), replaced_by=$new_id WHERE id=$old AND revoked_at IS NULL RETURNING family_id, user_id`; (b) on `RowsAffected = 0` → reuse-detection branch (D2); (c) on `RowsAffected = 1` → INSERT the new row carrying the same `family_id`, then COMMIT. | story AC-1 + AC-2; epic README schema. | The atomic UPDATE-RETURNING is the lock: only one of two concurrent rotations can move the row from active to revoked, so only one mints a successor. The other transaction sees `RowsAffected = 0` and walks straight into reuse detection. Doing this with `SELECT … FOR UPDATE` then `UPDATE` is two round-trips and lets a slow client time out between them; one statement is the right shape. |
| D2 | **Re-snapshot `lib[]` on every refresh.** The handler calls `LibraryACLResolver.Resolve(user, isAdmin)` (the resolver from Plan 10.3 D3) to produce the `lib` array for the new access token. We do NOT carry the old claim forward. | story AC-1 ("The new `access_token` re-snapshots the user's current `lib` set"); story test "a user whose library access was revoked sees an updated (smaller) `lib` claim on their next refresh". | Refresh is the natural point at which library-ACL revocations propagate. Carrying forward the old claim creates a 30-day window where a removed-from-library user keeps streaming via cached signed URLs minted from a stale `lib`. Re-snapshotting bounds the lag to one refresh cycle (typical 14 minutes since refresh fires when the access token has < 1 minute left). |
| D3 | **Reuse detection sweeps the FAMILY, not the user.** When a revoked row is presented, we run `UPDATE refresh_tokens SET revoked_at=now() WHERE family_id=$1 AND revoked_at IS NULL` — this revokes every active sibling/descendant in the same chain but leaves the user's *other* devices (which carry their own family_ids) intact. | story AC-2 ("every active row in the same `family_id` is revoked"). | A user can have multiple devices, each with its own family. Revoking by `user_id` would log them out everywhere on a single suspect token; revoking by `family_id` only severs the compromised chain. This is the standard refresh-rotation pattern (RFC 6819 §5.2.2.3). |
| D4 | **Audit row format: `category='security', event='refresh.replay-detected', payload={user_id, family_id, ip, ua, presented_jti?}`.** Written via `audit.Write(ctx, ...)` after the family-revoke UPDATE and BEFORE the 401 response, in the same DB transaction so a crash mid-handler can't drop the audit. | story AC-2 ("an audit row is written"). | Atomicity: if the family-revoke commits but the audit write doesn't, we lose the breach signal. Single-transaction is the cleanest guarantee. We pass the presented refresh's row id (the one that was already revoked when this attempt arrived) as `presented_jti` for forensic chaining. |
| D5 | **Refresh expiry (`expires_at < now()`) → 401 `type: refresh-expired` with NO family revocation.** | story AC-3. | Normal expiry is not theft. Revoking the family on expiry would log the user out of *all* their devices the day their refresh tokens age out, which is a UX disaster. Distinguishing "expired" from "replayed" in the response gives clients (and Story 10.5) a clean signal. |
| D6 | **Client-side serialisation of refresh is documented but NOT enforced server-side.** A racing client that sends two refreshes with the same old token will see the second as a replay (D2). The first response's body must be returned to BOTH callers if we wanted to be polite — we don't; we choose the simpler "second wins or replay" semantics. | story AC-2 + EC ("clients must serialize refresh per-device"). | Building a server-side cache that resolves "second concurrent refresh of the same token" gracefully requires a short-lived idempotency record keyed on the presented hash. The complexity is not worth it for a problem clients can avoid by guarding `refresh()` calls with a per-device mutex. We document the contract in client SDK docs (Story 6.6 deliverable) and rely on the tests for it. |
| D7 | **Refresh handler is its own route (`POST /api/auth/refresh`)** outside the bearer middleware chain. The chi router mounts it on the public router, NOT on the protected sub-router. | Refines story (silent on routing). | The bearer middleware would reject the call (no access token) or — worse — accept an expired one and put a stale user on the context. Putting the route outside the chain means the refresh handler is the only piece of code that handles the refresh credential, and that's exactly the security property we want. |
| D8 | **Family-revoke does NOT revoke the *just-presented-revoked* row again** (it's already revoked). The `revoked_at IS NULL` filter means the UPDATE only touches still-active siblings. | Optimisation; defends D3. | Otherwise we'd flap the row's `revoked_at` from its original timestamp to "now", losing the original-revoke time and confusing auditors. |

If D1 is rejected (separate SELECT FOR UPDATE then UPDATE): the race window between the two statements is ~1 ms but exists; on a flapping client a single SQL statement is both faster and provably correct.

If D2 is rejected (carry old `lib`): library-ACL revocations have a 30-day staleness ceiling instead of one refresh cycle. Story 10.5 already documents the access-TTL staleness; the refresh-TTL staleness compounds it for no good reason.

If D3 is rejected (revoke by user_id): a single suspicious request logs out every device. Multi-device users hate this; the family-bounded sweep is the standard mitigation.

---

## 1. Architecture diagram — refresh + rotation + reuse detection

```
   POST /api/auth/refresh                             body: {refresh_token}
   ─ no Authorization header required ─                                      (D7)
                            │
                            ▼
       ┌────────────────────────────────────────────────────────┐
       │ refresh.Handler.Refresh                                │
       │  raw = body.refresh_token                              │
       │  candidates = SELECT id, hash, user_id, family_id,     │
       │      expires_at, revoked_at, replaced_by               │
       │      FROM refresh_tokens                               │
       │      WHERE expires_at > now() - 7d                     │ ← pre-filter
       │      ORDER BY issued_at DESC                           │
       │      LIMIT 1024;                                       │
       │  match = first c where verifyArgon2id(raw, c.hash)     │
       │  if !match → 401 type:refresh-invalid                  │
       │  if match.expires_at < now()                           │
       │      → 401 type:refresh-expired (D5; no family revoke) │
       │  if match.revoked_at IS NOT NULL                       │
       │      → REUSE DETECTION (D3):                           │
       │         BEGIN;                                         │
       │           UPDATE refresh_tokens                        │
       │             SET revoked_at = now()                     │
       │             WHERE family_id = match.family_id          │
       │               AND revoked_at IS NULL;                  │
       │           audit.Write(ctx, 'refresh.replay-detected',  │
       │             {user_id, family_id, ip, ua, presented});  │
       │         COMMIT;                                        │
       │         → 401 type:refresh-replayed                    │
       │  ─── happy path ───                                    │
       │  BEGIN;                                                 │
       │    new_id = uuid7()                                    │
       │    UPDATE refresh_tokens                               │
       │      SET revoked_at=now(), replaced_by=$new_id         │
       │      WHERE id = match.id AND revoked_at IS NULL        │
       │      RETURNING family_id, user_id;                     │  ← if 0 rows: race, jump to REUSE branch (D1)
       │    rt, hash = newOpaqueRefresh()                       │
       │    INSERT refresh_tokens (id, user_id, hash,           │
       │      family_id /*inherited*/, expires_at, ...);        │
       │  COMMIT;                                               │
       │  user, isAdmin = SELECT FROM users WHERE id=user_id    │
       │  lib = LibraryACLResolver.Resolve(user, isAdmin)        ← D2
       │  access, exp, jti = jwt.MintAccess(user, isAdmin, lib) │
       │  → 200 {access_token, refresh_token = rt, ...}         │
       └────────────────────────────────────────────────────────┘

   Token chain visualisation (a normal rotation cycle):

       FAMILY F (created at login):
         R1 [revoked at t1, replaced_by=R2] ──┐
         R2 [revoked at t2, replaced_by=R3] ──┤
         R3 [active]                          │
                                              │
       attacker presents R1 at t3 > t2:       ▼
         → R1 already revoked → REUSE DETECTION
         → UPDATE R3.revoked_at = now() (R1, R2 already revoked)
         → user must re-login on this device
```

---

## 2. Detailed implementation

### 2.1 Package layout (Go)

```
apps/api/internal/auth/
├── refresh/
│   ├── handler.go             # POST /api/auth/refresh                    (D1, D2, D3, D5, D7)
│   ├── handler_test.go
│   ├── routes.go
│   ├── repo.go                # sqlc-generated wrappers
│   ├── lookup.go              # candidate filtering + argon2 verify
│   └── queries/
│       └── refresh_rotate.sql
└── audit/
    └── audit.go               # thin facade; impl in Story 10.16
```

(The refresh-token row schema and the `RotateRefresh` / `RevokeRefreshFamily` queries already exist from Plan 10.3 §2.6; this plan adds the orchestration and a few scoped SELECTs.)

### 2.2 Schema additions — none new; uses Plan 10.3's `refresh_tokens`

Plan 10.3's `0042_refresh_tokens.sql` is sufficient. We add ONE supporting query (no migration), filtered by `family_id`, that the rotation handler uses:

```sql
-- shared/db/migrations/0043_audit_log_security.sql  (only if Story 10.16 hasn't landed yet)
-- This migration is OWNED by Story 10.16; if it lands first, skip this file.
-- The category='security' rows are inserted by Plan 10.4 via audit.Write.
BEGIN;

CREATE TABLE IF NOT EXISTS audit_log (
    id          UUID PRIMARY KEY,
    ts          TIMESTAMPTZ NOT NULL DEFAULT now(),
    category    TEXT NOT NULL,
    event       TEXT NOT NULL,
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    payload     JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS audit_log_category_ts ON audit_log (category, ts DESC);

COMMIT;
```

We gate the migration so 10.16 owning the canonical version doesn't conflict.

### 2.3 sqlc queries (`internal/auth/refresh/queries/refresh_rotate.sql`)

```sql
-- name: ListRecentRefreshCandidates :many
-- A pre-filter to avoid scanning the entire table when verifying the presented
-- token by argon2id. We sort by issued_at DESC and cap at 1024 rows; this is
-- the bounded set the handler iterates with verifyArgon2id.
SELECT id, user_id, family_id, hash, expires_at, revoked_at
  FROM refresh_tokens
 WHERE expires_at > now() - interval '7 days'
 ORDER BY issued_at DESC
 LIMIT 1024;

-- name: GetUserForRefresh :one
SELECT id, username, is_admin
  FROM users
 WHERE id = $1;

-- name: GetActiveRefreshByID :one
SELECT id, user_id, family_id, hash, expires_at, revoked_at, replaced_by
  FROM refresh_tokens
 WHERE id = $1;
```

`InsertRefreshToken`, `RotateRefresh`, and `RevokeRefreshFamily` come from
Plan 10.3 §2.6.

### 2.4 Lookup helper (`internal/auth/refresh/lookup.go`)

```go
// findRow walks recent candidates and returns the row whose argon2id hash
// verifies against the presented plaintext, or nil if none match.
//
// We accept O(N) argon2 verifications per refresh request because:
//   - N is bounded by the LIMIT in ListRecentRefreshCandidates (1024)
//   - each verify is ~30 ms
//   - typical N for a real user is 1-3 (active families); the cap matters
//     only if the user has rotated extensively in the last 7 days.
//
// We keep iterating after the first non-error mismatch so a maliciously-
// timed request can't enumerate which row matched (constant-time ordering).
package refresh

import (
	"context"

	"github.com/alexedwards/argon2id"

	"maktaba/apps/api/internal/auth/refresh/repo"
)

func findRow(ctx context.Context, q *repo.Queries, plaintext string) (*repo.RefreshCandidate, error) {
	cands, err := q.ListRecentRefreshCandidates(ctx)
	if err != nil {
		return nil, err
	}
	var match *repo.RefreshCandidate
	for i := range cands {
		ok, _ := argon2id.ComparePasswordAndHash(plaintext, cands[i].Hash)
		if ok && match == nil {
			c := cands[i]
			match = &c
			// keep iterating to avoid timing oracles on early-exit
		}
	}
	return match, nil
}
```

### 2.5 Refresh handler (`internal/auth/refresh/handler.go`)

```go
package refresh

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"maktaba/apps/api/internal/auth/audit"
	"maktaba/apps/api/internal/auth/jwt"
	"maktaba/apps/api/internal/auth/library_acl"
	"maktaba/apps/api/internal/auth/native"
	"maktaba/apps/api/internal/auth/refresh/repo"
	"maktaba/apps/api/internal/problem"
)

type Handler struct {
	pool      *pgxpool.Pool
	q         *repo.Queries
	jwt       *jwt.Issuer
	resolver  *library_acl.Resolver
	cfg       Config
}

type Config struct {
	AccessTTL  time.Duration // 900s (D2 reuse from Plan 10.3 cfg)
	RefreshTTL time.Duration // 30d
}

type refreshReq struct {
	RefreshToken string `json:"refresh_token"`
}

// Refresh: POST /api/auth/refresh
//   200 → {access_token, access_expires_in, refresh_token, refresh_expires_in}
//   401 type:refresh-invalid     — token unknown
//   401 type:refresh-expired     — known token, past expires_at (D5)
//   401 type:refresh-replayed    — known token, already revoked (D3)
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var body refreshReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RefreshToken == "" {
		problem.Write(w, 400, "invalid-body", "missing refresh_token")
		return
	}

	// 1. Resolve which row the plaintext refers to.
	row, err := findRow(r.Context(), h.q, body.RefreshToken)
	if err != nil {
		problem.Write(w, 500, "internal", "")
		return
	}
	if row == nil {
		problem.Write(w, 401, "refresh-invalid", "")
		return
	}

	// 2. D5: normal expiry → 401, no family revoke.
	if time.Now().After(row.ExpiresAt) {
		problem.Write(w, 401, "refresh-expired", "")
		return
	}

	// 3. D3: presented row is already revoked → reuse detection.
	if row.RevokedAt.Valid {
		if err := h.handleReuse(r.Context(), r, row); err != nil {
			problem.Write(w, 500, "internal", "")
			return
		}
		problem.Write(w, 401, "refresh-replayed", "")
		return
	}

	// 4. Rotate atomically (D1).
	access, accessExp, refresh, refreshExp, err := h.rotate(r.Context(), r, row)
	switch {
	case errors.Is(err, errReuseRace):
		// We lost the race: another caller revoked between our SELECT and our UPDATE.
		// That caller already minted a successor; we treat this as a replay.
		_ = h.handleReuse(r.Context(), r, row)
		problem.Write(w, 401, "refresh-replayed", "")
		return
	case errors.Is(err, errUserDeleted):
		problem.Write(w, 401, "refresh-invalid", "")
		return
	case err != nil:
		problem.Write(w, 500, "internal", "")
		return
	}

	writeJSON(w, 200, map[string]any{
		"access_token":       access,
		"access_expires_in":  int(time.Until(accessExp).Seconds()),
		"refresh_token":      refresh,
		"refresh_expires_in": int(time.Until(refreshExp).Seconds()),
	})
}

var (
	errReuseRace   = errors.New("refresh: lost rotation race")
	errUserDeleted = errors.New("refresh: user gone")
)

// rotate runs the atomic UPDATE-RETURNING + INSERT inside one transaction (D1).
// The new refresh row inherits the old row's family_id.
func (h *Handler) rotate(ctx context.Context, r *http.Request, row *repo.RefreshCandidate,
) (access string, accessExp time.Time, refresh string, refreshExp time.Time, err error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	q := h.q.WithTx(tx)

	rotated, err := q.RotateRefresh(ctx, repo.RotateRefreshParams{
		ID: row.ID, ReplacedBy: uuid.NullUUID{}, // populated below
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = errReuseRace
		}
		return
	}

	// Mint a fresh opaque refresh + INSERT carrying the family.
	refresh, hash, err := native.NewOpaqueRefreshExported() // exported helper
	if err != nil {
		return
	}
	newID := uuid.Must(uuid.NewV7())
	refreshExp = time.Now().Add(h.cfg.RefreshTTL)
	if _, err = q.InsertRefreshToken(ctx, repo.InsertRefreshTokenParams{
		ID: newID, UserID: rotated.UserID, Hash: hash,
		FamilyID: rotated.FamilyID, ExpiresAt: refreshExp,
		ClientMeta: clientMeta(r),
	}); err != nil {
		return
	}
	// Backfill replaced_by on the just-rotated row.
	if err = q.SetReplacedBy(ctx, repo.SetReplacedByParams{
		ID: row.ID, ReplacedBy: uuid.NullUUID{UUID: newID, Valid: true},
	}); err != nil {
		return
	}

	// Re-snapshot lib (D2).
	user, err := q.GetUserForRefresh(ctx, rotated.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = errUserDeleted
		}
		return
	}
	lib, err := h.resolver.Resolve(ctx, user.ID, user.IsAdmin)
	if err != nil {
		return
	}

	access, accessExp, _, err = h.jwt.MintAccess(user.ID, user.IsAdmin, lib)
	if err != nil {
		return
	}

	err = tx.Commit(ctx)
	return
}

// handleReuse revokes the entire family and writes an audit row, in ONE tx (D4).
func (h *Handler) handleReuse(ctx context.Context, r *http.Request, row *repo.RefreshCandidate) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := h.q.WithTx(tx)

	if _, err := q.RevokeRefreshFamily(ctx, row.FamilyID); err != nil { // D3
		return err
	}
	if err := audit.Write(ctx, tx, audit.Row{
		Category: "security",
		Event:    "refresh.replay-detected",
		ActorID:  &row.UserID,
		Payload: map[string]any{
			"user_id":      row.UserID.String(),
			"family_id":    row.FamilyID.String(),
			"ip":           clientIP(r),
			"ua":           truncate(r.UserAgent(), 256),
			"presented_id": row.ID.String(),
		},
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
```

### 2.6 Routing (`internal/auth/refresh/routes.go`)

```go
func (h *Handler) Mount(r chi.Router) {
	// Outside the bearer-auth middleware chain (D7).
	r.Post("/api/auth/refresh", h.Refresh)
}
```

### 2.7 Audit facade (`internal/auth/audit/audit.go`)

```go
// Package audit is a stub interface owned by Story 10.16. Plan 10.4 only
// needs Write(ctx, tx, row); the implementation in 10.16 fills in id, ts,
// and persists the row. Until 10.16 lands, the dev impl is a JSON-line
// logger plus a trivial INSERT into audit_log.
package audit

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Row struct {
	Category string
	Event    string
	ActorID  *uuid.UUID
	Payload  map[string]any
}

// Write persists a single audit row inside the caller's transaction.
// The interface is stable; the body will be replaced by the Story 10.16 impl.
func Write(ctx context.Context, tx pgx.Tx, r Row) error {
	id := uuid.Must(uuid.NewV7())
	pl, _ := json.Marshal(r.Payload)
	var actor any
	if r.ActorID != nil {
		actor = *r.ActorID
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_log (id, category, event, actor_user_id, payload)
		VALUES ($1, $2, $3, $4, $5::jsonb)`,
		id, r.Category, r.Event, actor, pl)
	return err
}
```

---

## 3. File-by-file scaffolding

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/0043_audit_log_security.sql` (gated by `IF NOT EXISTS`; canonical owner is 10.16) | `audit_log` minimal | `TestAuditLogTableExists` |
| 2 | `apps/api/internal/auth/audit/audit.go` | `Row`, `Write` | smoke INSERT test |
| 3 | `apps/api/internal/auth/refresh/queries/refresh_rotate.sql` | sqlc inputs (`ListRecentRefreshCandidates`, `GetUserForRefresh`, `GetActiveRefreshByID`, `SetReplacedBy`) | (n/a) |
| 4 | `apps/api/internal/auth/refresh/repo.go` | `Queries.ListRecentRefreshCandidates`, `.GetUserForRefresh`, `.SetReplacedBy` | repo tests |
| 5 | `apps/api/internal/auth/refresh/lookup.go` | `findRow` | `TestFindRow*` |
| 6 | `apps/api/internal/auth/refresh/handler.go` | `Handler`, `Config`, `Refresh`, `rotate`, `handleReuse`, `errReuseRace`, `errUserDeleted` | `TestRefresh*` |
| 7 | `apps/api/internal/auth/refresh/routes.go` | `Handler.Mount` | wired in `cmd/maktaba-api/serve.go` |

---

## 4. Test cases — keyed to ACs

```go
// AC-1: refresh → old token invalidated; new token works.
func TestRefreshHappyPath(t *testing.T) {
	srv := newTestServer(t)
	pair := srv.loginNative(t, "alice", "hunter2")

	r1 := srv.do(t, "POST", "/api/auth/refresh",
		fmt.Sprintf(`{"refresh_token":%q}`, pair.RefreshToken), nil)
	require.Equal(t, 200, r1.StatusCode)
	var body struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		AccessExpiresIn  int    `json:"access_expires_in"`
		RefreshExpiresIn int    `json:"refresh_expires_in"`
	}
	require.NoError(t, json.NewDecoder(r1.Body).Decode(&body))
	require.NotEmpty(t, body.AccessToken)
	require.NotEqual(t, pair.RefreshToken, body.RefreshToken)

	// The new access works.
	resp := srv.doBearer(t, "GET", "/api/whoami", body.AccessToken)
	require.Equal(t, 200, resp.StatusCode)

	// The OLD refresh is now invalid.
	r2 := srv.do(t, "POST", "/api/auth/refresh",
		fmt.Sprintf(`{"refresh_token":%q}`, pair.RefreshToken), nil)
	require.Equal(t, 401, r2.StatusCode)
}

// AC-2: replay an old refresh after success → family revoked.
func TestRefreshReplayDetectionRevokesFamily(t *testing.T) {
	srv := newTestServer(t)
	pair := srv.loginNative(t, "alice", "hunter2")

	// 1. Successful rotation: R1 → R2.
	body1 := srv.refresh(t, pair.RefreshToken)
	r2Token := body1.RefreshToken
	// 2. Successful rotation: R2 → R3.
	body2 := srv.refresh(t, r2Token)
	r3Token := body2.RefreshToken

	// 3. Attacker replays R1.
	resp := srv.do(t, "POST", "/api/auth/refresh",
		fmt.Sprintf(`{"refresh_token":%q}`, pair.RefreshToken), nil)
	require.Equal(t, 401, resp.StatusCode)
	require.Contains(t, bodyString(resp), `"type":"refresh-replayed"`)

	// 4. R3, the still-active sibling, is now revoked.
	resp = srv.do(t, "POST", "/api/auth/refresh",
		fmt.Sprintf(`{"refresh_token":%q}`, r3Token), nil)
	require.Equal(t, 401, resp.StatusCode)
	require.Contains(t, bodyString(resp), `"type":"refresh-replayed"`)

	// 5. Audit row written.
	rows := srv.queryAuditByEvent(t, "refresh.replay-detected")
	require.Len(t, rows, 1)
	require.Equal(t, "security", rows[0].Category)
	require.Equal(t, srv.userID(t, "alice").String(), rows[0].Payload["user_id"])
	require.NotEmpty(t, rows[0].Payload["family_id"])
}

// AC-3: expired refresh → 401 expired, NO family revoke.
func TestRefreshExpiredDoesNotRevokeFamily(t *testing.T) {
	srv := newTestServer(t)
	pair := srv.loginNative(t, "alice", "hunter2")
	srv.expireRefreshNow(t, pair.RefreshToken)

	resp := srv.do(t, "POST", "/api/auth/refresh",
		fmt.Sprintf(`{"refresh_token":%q}`, pair.RefreshToken), nil)
	require.Equal(t, 401, resp.StatusCode)
	require.Contains(t, bodyString(resp), `"type":"refresh-expired"`)

	// Family is NOT revoked: a sibling (different family) still works.
	pair2 := srv.loginNative(t, "alice", "hunter2") // new family
	resp2 := srv.refresh(t, pair2.RefreshToken)
	require.NotEmpty(t, resp2.AccessToken)

	// And no audit row.
	require.Empty(t, srv.queryAuditByEvent(t, "refresh.replay-detected"))
}

// D2: lib is re-snapshotted on refresh.
func TestRefreshResnapshotsLibClaim(t *testing.T) {
	srv := newTestServer(t)
	srv.seedLibrary(t, "lib-A")
	srv.seedLibrary(t, "lib-B")
	srv.seedUser(t, "alice", "hunter2", false)
	srv.grantLibraryACL(t, "alice", "lib-A")
	srv.grantLibraryACL(t, "alice", "lib-B")
	pair := srv.loginNative(t, "alice", "hunter2")
	c0 := srv.parseClaims(t, pair.AccessToken)
	require.ElementsMatch(t, []string{"lib-A", "lib-B"}, c0.Lib)

	// Revoke lib-B; refresh; new claim should drop lib-B.
	srv.revokeLibraryACL(t, "alice", "lib-B")
	body := srv.refresh(t, pair.RefreshToken)
	c1 := srv.parseClaims(t, body.AccessToken)
	require.ElementsMatch(t, []string{"lib-A"}, c1.Lib)
}

// EC: client retries refresh in flight (network race).
func TestRefreshConcurrentReplayBecomesReplayDetected(t *testing.T) {
	srv := newTestServer(t)
	pair := srv.loginNative(t, "alice", "hunter2")

	type result struct {
		code int
		body []byte
	}
	out := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			resp := srv.do(t, "POST", "/api/auth/refresh",
				fmt.Sprintf(`{"refresh_token":%q}`, pair.RefreshToken), nil)
			b, _ := io.ReadAll(resp.Body)
			out <- result{resp.StatusCode, b}
		}()
	}
	r1, r2 := <-out, <-out
	codes := []int{r1.code, r2.code}
	sort.Ints(codes)
	require.Equal(t, []int{200, 401}, codes,
		"exactly one rotation succeeds; the loser sees 401")
	loser := r1
	if r1.code == 200 { loser = r2 }
	require.Contains(t, string(loser.body), `"type":"refresh-replayed"`)
}

// EC: refresh against a deleted user → 401 refresh-invalid.
func TestRefreshAgainstDeletedUser(t *testing.T) {
	srv := newTestServer(t)
	pair := srv.loginNative(t, "alice", "hunter2")
	srv.deleteUser(t, "alice")
	resp := srv.do(t, "POST", "/api/auth/refresh",
		fmt.Sprintf(`{"refresh_token":%q}`, pair.RefreshToken), nil)
	require.Equal(t, 401, resp.StatusCode)
	// FK cascade already nuked the row, so the lookup fails before reaching
	// rotate(); we get refresh-invalid (not refresh-replayed).
	require.Contains(t, bodyString(resp), `"type":"refresh-invalid"`)
}

// AC-1: rotation chains via family_id.
func TestRotationChainPreservesFamilyID(t *testing.T) {
	srv := newTestServer(t)
	pair := srv.loginNative(t, "alice", "hunter2")
	famAtLogin := srv.familyForRefresh(t, pair.RefreshToken)

	body := srv.refresh(t, pair.RefreshToken)
	famAfter := srv.familyForRefresh(t, body.RefreshToken)
	require.Equal(t, famAtLogin, famAfter, "family_id is inherited")
}

// EC: A device wiped without logout — refresh rots until expiry; no errors.
func TestForgottenRefreshSimplyExpires(t *testing.T) {
	srv := newTestServer(t)
	pair := srv.loginNative(t, "alice", "hunter2")
	srv.advanceClock(t, 31*24*time.Hour) // past 30d
	resp := srv.do(t, "POST", "/api/auth/refresh",
		fmt.Sprintf(`{"refresh_token":%q}`, pair.RefreshToken), nil)
	require.Equal(t, 401, resp.StatusCode)
	require.Contains(t, bodyString(resp), `"type":"refresh-expired"`)
}
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handled by |
|-----|-----------|------------|
| E1  | **Network race: client sends two refreshes with the same token before the first response arrives.** | The atomic UPDATE-RETURNING (D1) means only one wins. The loser gets 0 rows, lands in `errReuseRace`, and we treat it as replay (D3). Tested in `TestRefreshConcurrentReplayBecomesReplayDetected`. |
| E2  | **Replay an old refresh after a successful refresh.** | `row.RevokedAt.Valid == true` → reuse-detection branch revokes the family. Audit row written. (`TestRefreshReplayDetectionRevokesFamily`) |
| E3  | **Refresh-token plaintext doesn't match any candidate.** | `findRow` returns nil → 401 `refresh-invalid`. No audit row, no DB write. |
| E4  | **Refresh token presented after row's `expires_at`.** | D5 short-circuits: 401 `refresh-expired`, no family revoke. (`TestRefreshExpiredDoesNotRevokeFamily`) |
| E5  | **Refresh against a user whose row was deleted (via `DELETE /api/users/{id}` from Plan 10.1).** | FK cascade removes the refresh row first; the candidate scan misses; 401 `refresh-invalid`. (`TestRefreshAgainstDeletedUser`) |
| E6  | **Library ACL changed between login and refresh.** | D2 re-snapshot picks up the change; `lib` claim shrinks/grows on the new access token. (`TestRefreshResnapshotsLibClaim`) |
| E7  | **Refresh-token candidate list saturates at 1024 rows** (a heavy user with many rotations). | The cap is well above any realistic family chain; if exceeded, the LIMIT means the plaintext might not match the right row → 401 `refresh-invalid`. We do NOT degrade to a full table scan; the user re-logs in. The reaper sweep on `expires_at` keeps the table small. |
| E8  | **Audit-log INSERT fails inside `handleReuse`.** | The transaction rolls back; the family-revoke is also rolled back; the handler returns 500. The user sees a transient failure; their next refresh attempt is still a valid replay attempt (the family is still active) so we eventually catch it. We log the audit failure as a P1 metric. |
| E9  | **The just-presented row was revoked by `handleReuse`'s family-sweep at the same moment we're trying to rotate it.** | The atomic UPDATE in `RotateRefresh` sees `revoked_at IS NOT NULL` and returns 0 rows → `errReuseRace` → we re-enter `handleReuse` which is idempotent (the family is already revoked). Net result: 401 `refresh-replayed`. |
| E10 | **Argon2 verify mismatch on hash-format-corrupt row.** | `argon2id.ComparePasswordAndHash` returns `(false, error)`; `findRow` ignores both the bool and the error and continues to the next candidate. The corrupt row is effectively dead; ops alerts via a "argon2 decode error" metric. |
| E11 | **Refresh response body lost in transit; client retries with the SAME presented token.** | This IS the network race (E1). We accept the small UX cost: the second retry sees `refresh-replayed`, which the client SDK MUST handle by deleting local credentials and prompting re-login. Documented in client SDK (Story 6.6). |
| E12 | **Client serialises refresh per device (D6) but two devices race.** | Each device has its own family; the two rotations are independent — no race. (`TestRotationChainPreservesFamilyID` covers single-family invariance.) |
| E13 | **Refresh body is empty / malformed JSON.** | 400 `invalid-body` (no DB write, no audit). |
| E14 | **Refresh handler called via the bearer-middleware chain by accident.** | D7 places the route OUTSIDE the chain. A static check (`go test ./internal/server -run TestRouteOutsideBearerChain`) verifies the route registration. |

---

## 6. Acceptance checklist

- [ ] **A1** `POST /api/auth/refresh` with a valid refresh token returns 200 with `{access_token, access_expires_in, refresh_token, refresh_expires_in}`. The presented refresh row is `revoked_at=now(), replaced_by=<new_id>`; a fresh row is INSERTed with `family_id` inherited. (`TestRefreshHappyPath`, `TestRotationChainPreservesFamilyID`)
- [ ] **A2** The new `access_token` carries a freshly-resolved `lib[]` claim that reflects the user's CURRENT library ACL, not the value at original login. (`TestRefreshResnapshotsLibClaim`)
- [ ] **A3** The rotation runs in a single transaction containing the UPDATE-RETURNING and the INSERT; a crash mid-handler leaves the old row active. (`TestRefreshRotationAtomicityOnCrash` — fault-injected)
- [ ] **A4** Replaying an already-revoked refresh token returns 401 `refresh-replayed`, revokes every still-active row in the same `family_id`, and writes one `audit_log` row with `category='security', event='refresh.replay-detected', payload={user_id, family_id, ip, ua, presented_id}`. (`TestRefreshReplayDetectionRevokesFamily`)
- [ ] **A5** Reuse detection does NOT touch other families belonging to the same user; their refresh tokens still work. (Extension of `TestRefreshReplayDetectionRevokesFamily` checking a sibling family.)
- [ ] **A6** A refresh row whose `expires_at < now()` returns 401 `refresh-expired`; NO family revoke; NO audit row; sibling families and other devices are unaffected. (`TestRefreshExpiredDoesNotRevokeFamily`, `TestForgottenRefreshSimplyExpires`)
- [ ] **A7** A refresh whose plaintext doesn't match any row in `refresh_tokens` returns 401 `refresh-invalid`; no DB writes. (Smoke test on a random base64url token.)
- [ ] **A8** A refresh whose user has been deleted returns 401 `refresh-invalid` (the row is gone via FK cascade). (`TestRefreshAgainstDeletedUser`)
- [ ] **A9** Concurrent refreshes with the same old token: exactly one returns 200; the other returns 401 `refresh-replayed`. The losing transaction rolls back without inserting a duplicate row. (`TestRefreshConcurrentReplayBecomesReplayDetected`)
- [ ] **A10** The refresh route is registered OUTSIDE the bearer-auth middleware chain (`POST /api/auth/refresh` requires no `Authorization` header). (`TestRouteOutsideBearerChain`)
- [ ] **A11** `LibraryACLResolver.Resolve` is the only function in the codebase that produces the `lib[]` claim — both Plan 10.3 (login) and Plan 10.4 (refresh) call it. (Static check.)
- [ ] **A12** The `replaced_by` column on the rotated row is backfilled to the new row's id within the same transaction. (Smoke test on `refresh_tokens.replaced_by` after refresh.)
- [ ] **A13** Client SDK documentation (Story 6.6 deliverable) states: "refresh calls MUST be serialised per-device; a parallel refresh against the same token is treated as replay." (Doc check.)
- [ ] **A14** No code path logs the refresh-token plaintext or the access-token JWT. (`TestRefreshNeverLogsSecrets`, log-output assertion across success + replay + expired paths.)
- [ ] **A15** The refresh handler shares no state with the bearer middleware — even an expired access token does not influence the refresh outcome. (Smoke test: refresh succeeds with an expired or absent `Authorization` header.)
