# Implementation Plan — Story 19.8 Multi-Tenant Readiness

> Companion to [story-19-08-multi-tenant-readiness.md](story-19-08-multi-tenant-readiness.md).
> v1 ships single-user; schema and auth surfaces must allow flipping a flag
> to multi-user without a migration. Sentinel UUID + `library_acl`.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Sentinel | `00000000-0000-0000-0000-000000000001` constant in `shared/auth/sentinel.go` and `pipeline/auth/sentinel.py`. |
| Auth flag | `MAKTABA_MULTI_USER=1` (env) or `system_config.multi_user=true` (DB). Either flips the gate. |
| Admin token | `MAKTABA_ADMIN_TOKEN` resolves user_id → sentinel; documented and enforced. |
| ACL | `library_acl(library_id, user_id, role)` table. In single-user, the row is implicit. |
| Migration test | `tests/migrations/multi_user_flip_test.go` flips the flag and asserts data continuity. |

## 1. Project layout

```
shared/
├── auth/
│   ├── sentinel.go          # SentinelUserID const + helpers
│   ├── single_user.go       # auth shim
│   ├── single_user_test.go
│   └── admin_token.go
└── db/migrations/
    └── 00xx_multi_tenant_readiness.sql
api/internal/auth/
├── middleware.go
├── multi_user.go
└── tests/
api/internal/library_acl/
├── store.go
├── enforce.go
└── enforce_test.go
tests/migrations/
└── multi_user_flip_test.go
```

## 2. Sentinel constant

```go
// shared/auth/sentinel.go
package auth

import "github.com/google/uuid"

var SentinelUserID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

func IsSentinel(id uuid.UUID) bool { return id == SentinelUserID }
```

```python
# pipeline/auth/sentinel.py
SENTINEL_USER_ID = uuid.UUID("00000000-0000-0000-0000-000000000001")
```

## 3. Schema

```sql
-- 00xx_multi_tenant_readiness.sql

-- Sentinel user is materialized so FKs always resolve.
INSERT INTO users (id, email, role, created_at)
VALUES ('00000000-0000-0000-0000-000000000001', 'sentinel@maktaba.local', 'admin', now())
ON CONFLICT (id) DO NOTHING;

-- Forbid a real user accidentally claiming the sentinel UUID.
ALTER TABLE users ADD CONSTRAINT users_no_real_sentinel
  CHECK (id <> '00000000-0000-0000-0000-000000000001'
         OR email = 'sentinel@maktaba.local');

-- Backfill user_id on tables that didn't have one.
ALTER TABLE watch_state ADD COLUMN IF NOT EXISTS user_id UUID;
UPDATE watch_state SET user_id = '00000000-0000-0000-0000-000000000001' WHERE user_id IS NULL;
ALTER TABLE watch_state ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE watch_state ALTER COLUMN user_id SET DEFAULT '00000000-0000-0000-0000-000000000001';
ALTER TABLE watch_state ADD CONSTRAINT watch_state_user_fk
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

-- Same shape for collections_by_user, etc. (one ALTER block per table).

-- ACL table
CREATE TABLE library_acl (
    library_id  UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id)     ON DELETE CASCADE,
    role        TEXT NOT NULL CHECK (role IN ('admin','editor','viewer')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (library_id, user_id)
);
CREATE UNIQUE INDEX library_acl_library_user_uniq ON library_acl (library_id, user_id);

-- single-user mode: no rows yet; resolution is implicit. Multi-user backfill below.
```

## 4. Single-user auth shim

```go
// shared/auth/single_user.go
type Resolver struct{ multiUser bool; jwt JWTVerifier }

func (r *Resolver) UserID(ctx context.Context, req *http.Request) (uuid.UUID, error) {
    if !r.multiUser {
        if tok := req.Header.Get("X-Admin-Token"); tok != "" && tok == os.Getenv("MAKTABA_ADMIN_TOKEN") {
            return SentinelUserID, nil
        }
        return SentinelUserID, nil          // every request resolves to sentinel
    }
    claims, err := r.jwt.Verify(req)
    if err != nil { return uuid.Nil, err }
    return claims.UserID, nil
}
```

AC2 mapping: admin-token path returns the **same** sentinel. Test `TestAdminTokenSameSentinel` asserts a write via admin-token and a write via the auth shim land in the same `user_id` column value.

## 5. Library ACL enforcement

```go
// api/internal/library_acl/enforce.go
type Role string
const (RoleAdmin Role = "admin"; RoleEditor = "editor"; RoleViewer = "viewer")

func (s *Store) Allow(ctx context.Context, libID, userID uuid.UUID, need Role) (bool, error) {
    if !s.multiUser {
        // Implicit admin for sentinel; everyone else denied (but we only ever
        // see sentinel in single-user mode).
        return userID == auth.SentinelUserID, nil
    }
    var role Role
    err := s.db.QueryRowContext(ctx,
        `SELECT role FROM library_acl WHERE library_id=$1 AND user_id=$2`,
        libID, userID).Scan(&role)
    if errors.Is(err, sql.ErrNoRows) { return false, nil }
    if err != nil { return false, err }
    return rank(role) >= rank(need), nil
}
```

Middleware applied to `library_id`-bearing routes:

```go
r.With(libraryACL("viewer")).Get("/api/libraries/{id}/videos", h.ListVideos)
```

## 6. Multi-user flip backfill

```go
// api/internal/library_acl/backfill.go
func BackfillSentinelACL(ctx context.Context, db *sql.DB) error {
    _, err := db.ExecContext(ctx, `
        INSERT INTO library_acl (library_id, user_id, role)
        SELECT id, '00000000-0000-0000-0000-000000000001', 'admin' FROM libraries
        ON CONFLICT (library_id, user_id) DO NOTHING
    `)
    return err
}
```

Triggered by `POST /admin/multi_user/enable`:

```go
func EnableMultiUser(ctx context.Context, db *sql.DB) error {
    return inTx(ctx, db, func(tx *sql.Tx) error {
        if err := BackfillSentinelACL(ctx, db); err != nil { return err }
        _, err := tx.ExecContext(ctx,
            `UPDATE system_config SET multi_user=true WHERE id=1`)
        return err
    })
}
```

## 7. Test cases

### TC1 — Schema audit
`tests/migrations/schema_audit_test.go`:

```go
func TestEveryUserBearingTableHasNotNullUserID(t *testing.T) {
    expected := []string{"watch_state", "collections_by_user", "favorites", "saved_searches", "user_settings"}
    for _, table := range expected {
        var nullable bool
        err := db.QueryRow(`
            SELECT is_nullable='YES'
              FROM information_schema.columns
             WHERE table_name=$1 AND column_name='user_id'
        `, table).Scan(&nullable)
        require.NoError(t, err)
        require.False(t, nullable, "%s.user_id must be NOT NULL", table)
    }
}
```

### TC2 — Flag flip continuity
`tests/migrations/multi_user_flip_test.go`:

1. Seed single-user data (libraries, videos, watch_state, collections — all with sentinel `user_id`).
2. Call `EnableMultiUser`.
3. Authenticate as a real user with `user_id = sentinel`-mapped account (test JWT minted with `sub=sentinel`).
4. Assert: same set of libraries returned by `GET /api/libraries`.
5. Assert: `library_acl` has one row per library for sentinel user with role=admin.
6. Watch state and collections all readable.

### TC3 — Cross-user ACL
Mint two users A and B. A creates library L; backfill grants A admin. As B (no row in `library_acl`), `GET /api/libraries/L/videos` returns 404 (not 403, to avoid leaking existence).

### TC4 — Admin-token sentinel link
Set `MAKTABA_ADMIN_TOKEN=secret`. POST a watch_state via admin-token. Auth-shim resolves to sentinel. Read via the auth shim (no token); same row visible. Assert both writes have identical `user_id = sentinel`.

## 8. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 imported rows without user_id | story | Migration `UPDATE … SET user_id = sentinel WHERE user_id IS NULL`; logged count. |
| EC2 JWT subject mismatch on watch_state | story | Read OK in single-user (publicly readable); write rejected with 403. |
| EC3 sentinel collision | story | Check constraint `users_no_real_sentinel`. |
| Backfill on huge libraries table | impl | `INSERT … SELECT` with `ON CONFLICT DO NOTHING`; monitored under Story 19.5 migration safety. |
| Mid-flip request | impl | `EnableMultiUser` runs in single transaction; either all callers see the flip or none. |

## 9. Configuration

```yaml
auth:
  multi_user: false              # source of truth is system_config.multi_user
  admin_token_env: MAKTABA_ADMIN_TOKEN
  jwt:
    issuer: ${JWT_ISSUER}
    audience: ${JWT_AUDIENCE}
```

`system_config.multi_user` is the runtime source of truth; the env var is only used at boot to bootstrap the row.

## 10. Migration plan documentation

`docs/runbooks/single-to-multi-user.md`:

1. Create real users via `/api/admin/users` (multi-user disabled — admin-token path).
2. Map at least one real user to sentinel (link by adding row to `users` with the same email, then `UPDATE library_acl SET user_id=<real>` if you want to retire sentinel).
3. `POST /admin/multi_user/enable` → backfill runs, flag flips.
4. Disable `MAKTABA_ADMIN_TOKEN` (story 23.1 covers).

## 11. Dependencies

- Story 23.1 (admin token bypass).
- Epic 10 auth (JWT verifier).
- Story 19.5 (migration safety; this is a small migration).
- Epic 9 library management (libraries + collections schemas).
