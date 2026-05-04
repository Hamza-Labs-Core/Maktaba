# Implementation Plan — Story 19.8 Multi-Tenant Readiness

> Companion to [story-19-08-multi-tenant-readiness.md](story-19-08-multi-tenant-readiness.md).
> v1 ships single-user; schema and auth surfaces must allow flipping a flag
> to multi-user without a migration. Sentinel UUID + `library_acl`.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Sentinel | `00000000-0000-0000-0000-000000000001` constant in `shared/auth/sentinel.go` and `pipeline/auth/sentinel.py`. (Arch §10.9 / plan-10-09.) |
| Auth flag (env → config) | `MAKTABA_AUTH_MULTI_USER=1` (env) maps to `auth.multi_user=true` (config key) and is persisted as `system_config.auth.multi_user` at boot. The `MAKTABA_AUTH_*` env prefix is the convention for everything under the `auth.*` config tree. |
| Admin token | `MAKTABA_ADMIN_TOKEN` resolves user_id → sentinel **only** in single-user mode. In multi-user mode, the admin-token gate from Story 23.1 AC5 takes over. |
| ACL | `library_acl(library_id, user_id, role)` table — canonical three-role model `admin|editor|viewer` (arch §8.6, owned by Epic 23.2 / plan-10-13). In single-user, the row is implicit; in multi-user we backfill on flip and on every library-create. |
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

> **Canonical `users` shape (arch §8.5):**
> `users(id, username, pw_hash, is_admin, created_at)` — there is NO `email`
> column and NO `role` column. The earlier draft of this migration referenced
> both and would have failed at first execution. The sentinel single-user
> row is `username='maktaba'`, `is_admin=true`, `pw_hash=NULL` (the
> single-user path never authenticates against pw_hash).

```sql
-- 00xx_multi_tenant_readiness.sql

-- Sentinel user is materialized so FKs always resolve.
-- Canonical column set: (id, username, pw_hash, is_admin, created_at).
INSERT INTO users (id, username, is_admin, created_at)
VALUES ('00000000-0000-0000-0000-000000000001', 'maktaba', true, now())
ON CONFLICT (id) DO NOTHING;

-- Forbid a real user accidentally claiming the sentinel UUID. The check
-- references `username` (the canonical identifying column), NOT `email`.
ALTER TABLE users ADD CONSTRAINT users_no_real_sentinel
  CHECK (id <> '00000000-0000-0000-0000-000000000001'
         OR username = 'maktaba');

-- Backfill user_id on existing user-scoped tables. Per architecture §8 the
-- only user-scoped tables in v1 are `playback_state` and `saved_searches`.
-- The earlier draft also touched `collections_by_user`, `favorites`, and
-- `user_settings` — none of those tables exist in arch §8 today; they are
-- explicitly deferred (see "AC1 user-scoped tables" note below).
ALTER TABLE playback_state ADD COLUMN IF NOT EXISTS user_id UUID;
UPDATE playback_state SET user_id = '00000000-0000-0000-0000-000000000001' WHERE user_id IS NULL;
ALTER TABLE playback_state ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE playback_state ALTER COLUMN user_id SET DEFAULT '00000000-0000-0000-0000-000000000001';
ALTER TABLE playback_state ADD CONSTRAINT playback_state_user_fk
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE saved_searches ADD COLUMN IF NOT EXISTS user_id UUID;
UPDATE saved_searches SET user_id = '00000000-0000-0000-0000-000000000001' WHERE user_id IS NULL;
ALTER TABLE saved_searches ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE saved_searches ALTER COLUMN user_id SET DEFAULT '00000000-0000-0000-0000-000000000001';
ALTER TABLE saved_searches ADD CONSTRAINT saved_searches_user_fk
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

-- ACL table — canonical three-role model (arch §8.6).
-- The PRIMARY KEY (library_id, user_id) already gives us uniqueness; no
-- additional UNIQUE INDEX is needed.
CREATE TABLE library_acl (
    library_id  UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id)     ON DELETE CASCADE,
    role        TEXT NOT NULL CHECK (role IN ('admin','editor','viewer')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (library_id, user_id)
);

-- single-user mode: no rows yet; resolution is implicit. Multi-user backfill below.
```

> **AC1 user-scoped tables — scope decision.** This migration touches only
> tables that exist in architecture §8 today: `playback_state` and
> `saved_searches`. AC1 also lists "collections-by-user", "favorites", and
> "user_settings" as user-scoped — those tables do **not** exist in arch §8
> and are out of scope for Story 19.8. They are tracked as future work
> (Epic 9 follow-up) and the story acceptance criteria should be amended
> accordingly.

## 4. Single-user auth shim

The unauthenticated branch returns `SentinelUserID` **only when
`auth.multi_user=false`**. In multi-user mode an unauthenticated request
must NOT silently land on the sentinel — it requires an admin-token gate
per Story 23.1 AC5. This change closes the prior auth bypass where the
shim would return sentinel even after the flag flip.

```go
// shared/auth/single_user.go
type Resolver struct{
    multiUser bool          // bound from auth.multi_user config key
    jwt       JWTVerifier
}

func (r *Resolver) UserID(ctx context.Context, req *http.Request) (uuid.UUID, error) {
    if !r.multiUser {
        // Single-user mode (`auth.multi_user=false`):
        //   - Admin-token path (Story 23.1 AC5) explicitly resolves to sentinel.
        //   - Any other request also resolves to sentinel because in single-
        //     user mode the sentinel IS the only user.
        if tok := req.Header.Get("X-Admin-Token"); tok != "" && tok == os.Getenv("MAKTABA_ADMIN_TOKEN") {
            return SentinelUserID, nil
        }
        return SentinelUserID, nil
    }

    // Multi-user mode (`auth.multi_user=true`):
    //   - Admin-token path is gated by Story 23.1 AC5 (admin user-id resolves
    //     via the admin token registry, NOT the sentinel).
    //   - All other requests must present a valid JWT; no implicit sentinel.
    if tok := req.Header.Get("X-Admin-Token"); tok != "" {
        adminID, err := r.adminTokenRegistry.Resolve(ctx, tok) // Story 23.1 AC5
        if err != nil { return uuid.Nil, err }
        return adminID, nil
    }
    claims, err := r.jwt.Verify(req)
    if err != nil { return uuid.Nil, err }
    return claims.UserID, nil
}
```

AC2 mapping: in single-user mode, admin-token writes and unauthenticated
writes land on the same `user_id`. After multi-user flip, admin-token is
resolved via the Story 23.1 AC5 registry; unauthenticated requests fail
auth instead of silently writing as sentinel.

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

### 6.1 Post-flip library-create trigger

The backfill above only fixes libraries that exist at flip time. Libraries
created **after** the flip would otherwise have no `library_acl` row for
the sentinel admin and become unreachable (the ACL middleware §5 would
deny). We add an `AFTER INSERT` trigger that grants the sentinel admin
role on every new library; the application library-create handler also
inserts the row (defence in depth, idempotent via PK).

```sql
-- 00xx_multi_tenant_readiness.sql (continued)
CREATE OR REPLACE FUNCTION grant_sentinel_admin_on_library_create()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO library_acl (library_id, user_id, role)
    VALUES (NEW.id, '00000000-0000-0000-0000-000000000001', 'admin')
    ON CONFLICT (library_id, user_id) DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER libraries_grant_sentinel_admin
    AFTER INSERT ON libraries
    FOR EACH ROW
    EXECUTE FUNCTION grant_sentinel_admin_on_library_create();
```

Application-side belt-and-braces in the library-create handler:

```go
func (h *Libraries) Create(ctx context.Context, in CreateLibraryRequest) (Library, error) {
    lib, err := h.store.Insert(ctx, in)
    if err != nil { return Library{}, err }
    // Idempotent on the trigger above: ensures even if the trigger is dropped
    // by an operator the sentinel-admin row is still present.
    _, _ = h.acl.Grant(ctx, lib.ID, auth.SentinelUserID, library_acl.RoleAdmin)
    return lib, nil
}
```

## 7. Test cases

### TC1 — Schema audit
`tests/migrations/schema_audit_test.go`:

```go
func TestEveryUserBearingTableHasNotNullUserID(t *testing.T) {
    // Scope per arch §8: only `playback_state` and `saved_searches` are
    // user-scoped in v1. `collections_by_user`, `favorites`, `user_settings`
    // are deferred (see §3 scope decision).
    expected := []string{"playback_state", "saved_searches"}
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

1. Seed single-user data (libraries, videos, playback_state, saved_searches — all with sentinel `user_id`).
2. Call `EnableMultiUser`.
3. Authenticate as a real user with `user_id = sentinel`-mapped account (test JWT minted with `sub=sentinel`).
4. Assert: same set of libraries returned by `GET /api/libraries`.
5. Assert: `library_acl` has one row per library for sentinel user with role=admin.
6. Playback state and saved searches all readable.

### TC3 — Cross-user ACL
Mint two users A and B. A creates library L; backfill grants A admin. As B (no row in `library_acl`), `GET /api/libraries/L/videos` returns 404 (not 403, to avoid leaking existence).

### TC4 — Admin-token sentinel link
Set `MAKTABA_ADMIN_TOKEN=secret`, `auth.multi_user=false`. POST a `playback_state` row via admin-token. Auth-shim resolves to sentinel. Read via the auth shim (no token); same row visible. Assert both writes have identical `user_id = sentinel`.

### TC5 — Post-flip library reachability
1. `auth.multi_user=true` (already flipped).
2. Create a library L via the library-create handler.
3. Assert: `library_acl` has a row `(L, sentinel, 'admin')` from the trigger.
4. Drop the trigger; create another library L2 via the handler.
5. Assert: `library_acl` still has `(L2, sentinel, 'admin')` from the
   handler-side belt-and-braces grant.

## 8. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 imported rows without user_id | story | Migration `UPDATE … SET user_id = sentinel WHERE user_id IS NULL`; logged count. |
| EC2 JWT subject mismatch on `playback_state` | story | Read OK in single-user (publicly readable); write rejected with 403. |
| EC3 sentinel collision | story | Check constraint `users_no_real_sentinel` (on `username`). |
| Backfill on huge libraries table | impl | `INSERT … SELECT` with `ON CONFLICT DO NOTHING`; monitored under Story 19.5 migration safety. |
| Mid-flip request | impl | `EnableMultiUser` runs in single transaction; either all callers see the flip or none. |

## 9. Configuration

```yaml
# Env-prefix convention: MAKTABA_<TREE>_<KEY> maps to <tree>.<key> in config.
# So MAKTABA_AUTH_MULTI_USER → auth.multi_user. The legacy
# MAKTABA_MULTI_USER name is removed; this plan standardises on the
# prefixed form to match the rest of the auth tree (e.g.
# MAKTABA_AUTH_JWT_ISSUER → auth.jwt.issuer).
auth:
  multi_user: false              # MAKTABA_AUTH_MULTI_USER; source of truth is system_config.auth.multi_user
  admin_token_env: MAKTABA_ADMIN_TOKEN
  jwt:
    issuer: ${MAKTABA_AUTH_JWT_ISSUER}
    audience: ${MAKTABA_AUTH_JWT_AUDIENCE}
```

`system_config.auth.multi_user` is the runtime source of truth; the env var
`MAKTABA_AUTH_MULTI_USER` is only used at boot to bootstrap the row.

## 10. Migration plan documentation

`docs/runbooks/single-to-multi-user.md`:

1. Create real users via `/api/admin/users` (multi-user disabled — admin-token path).
2. Map at least one real user to sentinel (link by adding row to `users` with the chosen `username`, then `UPDATE library_acl SET user_id=<real>` if you want to retire sentinel).
3. `POST /admin/multi_user/enable` → backfill runs, flag flips.
4. Disable `MAKTABA_ADMIN_TOKEN` (story 23.1 covers).

## 11. Dependencies

- Story 23.1 (admin token bypass).
- Epic 10 auth (JWT verifier).
- Story 19.5 (migration safety; this is a small migration).
- Epic 9 library management (libraries + collections schemas).
