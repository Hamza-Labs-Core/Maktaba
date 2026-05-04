# Implementation Plan — Story 10.13 Permission model

> Companion to [story-10-13-permission-model.md](story-10-13-permission-model.md).
> The `library_acl` table is owned here (no other story claims it).
> The `lib[]` snapshot is consumed by [Story 10.7](plan-10-07-streaming-jwt-verify.md)
> and minted by [Story 10.3](plan-10-03-native-login.md) /
> [Story 10.8](plan-10-08-signed-url-minter.md).

## 0. Canonical schema and authz interface (cross-epic)

This plan creates the **canonical `library_acl(library_id, user_id, role)`
table** with the **three-role model `role IN ('admin', 'editor', 'viewer')`**
(architecture §8.6 line 1690 canonical).
**[plan-23-02 — Authorization and ACLs (Epic 23)](../23-security/plan-23-02-authorization-acls.md)
is the canonical implementation** of the full authorization semantics
(role matrix, middleware, lint, streaming-side checks). This plan ships:

- The schema with the three-role check constraint.
- A minimal `Authz.Can(ctx, Action, Resource) error` stub matching the
  canonical signature defined by plan-23-02.
- The default `'admin'` role on the `role` column so existing
  single-user installs continue to work without manual back-fills.

Detailed authorization semantics (full role matrix, audience-action
mapping, request-scoped role caching) **are deferred to plan-23-02**.

Cross-link: [plan-23-02 — Authorization and ACLs (Epic 23)](../23-security/plan-23-02-authorization-acls.md).

## 0.1 Scope and placement

| Concern | Decision |
|---|---|
| Migration file | `shared/db/migrations/0025_library_acl.sql` and `.sqlite.sql` — three-role schema. |
| Authz interface | `api/internal/auth/authz.go` — **canonical signature** `Authz.Can(ctx context.Context, action Action, resource Resource) error` matched by [plan-23-02](../23-security/plan-23-02-authorization-acls.md). |
| v1 minimal stub | This plan ships a minimal `Can` that reads the `role` column and returns `nil` for any `(admin|editor|viewer)` row matching the resource's `LibraryID()` (admin everywhere, editor and viewer for `Read`/`Stream`). Full role matrix and middleware live in plan-23-02. |
| Per-user filters | `api/internal/auth/scope.go` — `OwnerScope(ctx)` returns the user_id for filtering `playback_state` and `saved_searches` queries. |
| 403 envelope | Generic `type: forbidden` (do not leak resource existence). |
| Out of scope | Full role-matrix enforcement (deferred to [plan-23-02](../23-security/plan-23-02-authorization-acls.md)); SSO/OIDC (v2); 2FA (v2). |

## 1. Architecture diagram

```
                      ┌─────────────────────────────────────────────┐
                      │ library_acl                                  │
                      │  library_id  user_id  role                   │
                      │  ──────────  ───────  ─────────────          │
                      │  (FK→libs)  (FK→user) admin|editor|viewer    │
                      │  PRIMARY KEY (library_id, user_id)           │
                      └─────────────────────────────────────────────┘
                                  ▲
                                  │ used by
                                  │
       ┌──────────────────────────┴──────────────────────────┐
       │                                                       │
┌───────────────────┐                              ┌────────────────────────────┐
│ LibACL.Libraries  │                              │ Authz.Can(ctx, action, res) │
│ ForUser(uid)      │                              │   (canonical sig: matches   │
│   used by:        │                              │    plan-23-02)              │
│   - JWT minter    │                              │   - admin? → nil            │
│   - signed-URL    │                              │   - role lookup on          │
│   - middleware    │                              │     library_acl             │
└───────────────────┘                              │   - role+action check       │
                                                   │     (full matrix in 23-02)  │
                                                   └────────────────────────────┘
                                                              ▲
                                                              │
                                              ┌──────────────────────┐
                                              │ all handlers          │
                                              │  call authz.Can       │
                                              │  before any work      │
                                              └──────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `shared/db/migrations/0025_library_acl.sql` | Postgres table. |
| `shared/db/migrations/0025_library_acl.sqlite.sql` | SQLite variant. |
| `shared/db/queries/library_acl.sql` | sqlc input. |
| `api/internal/auth/authz.go` | `Authz` interface + v1 impl. |
| `api/internal/auth/lib_acl.go` | `LibACL.LibrariesForUser`, `AllLibraryIDs`, `Grant`, `Revoke`. |
| `api/internal/auth/scope.go` | Per-user filters. |
| `api/internal/http/middleware/requireadmin.go` | `RequireAdmin` middleware (used by user-mgmt + library-mgmt routes). |
| `api/internal/auth/authz_test.go` | Unit tests. |
| `api/internal/http/authz_integration_test.go` | End-to-end. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/http/router.go` | Wrap library-write and user-mgmt routes with `RequireAdmin`. |
| `api/internal/http/videos.go` | `GET /api/videos/{id}` filters `playback_state` to ctx user. |
| `api/internal/http/saved_searches.go` | List/Read filtered by ctx user. |
| `api/internal/auth/jwt.go` | `Lib` claim cap at 1000 entries with WARN log. |

### 2.3 Type definitions

```go
// api/internal/auth/authz.go
package auth

import (
    "context"
    "errors"

    "github.com/google/uuid"
)

type Action string

const (
    ActionVideoRead   Action = "video.read"
    ActionVideoWrite  Action = "video.write"
    ActionLibraryRead Action = "library.read"
    ActionLibraryWrite Action = "library.write"
    ActionSearchRead  Action = "search.read"
    ActionSettingsRead  Action = "settings.read"
    ActionSettingsWrite Action = "settings.write"
)

// Resource is the canonical resource interface shared with plan-23-02.
// LibraryID returns the empty string for system-wide (admin-only)
// resources.
type Resource interface {
    LibraryID() string
}

// Authz.Can returns nil iff the caller is permitted to perform action
// on resource. The signature matches plan-23-02 (canonical) so the
// two implementations are interchangeable. plan-23-02 ships the full
// role-matrix implementation; this plan ships a minimal stub.
type Authz interface {
    Can(ctx context.Context, action Action, resource Resource) error
}

var (
    ErrForbidden       = errors.New("authz: forbidden")
    ErrUnauthenticated = errors.New("authz: unauthenticated")
)
```

```go
// api/internal/auth/lib_acl.go
type LibACL interface {
    // LibrariesForUser returns the libraries the user has any role on.
    LibrariesForUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
    // RoleForUser returns the user's role on the library, or "" if none.
    RoleForUser(ctx context.Context, userID, libraryID uuid.UUID) (string, error)
    AllLibraryIDs(ctx context.Context) ([]uuid.UUID, error)
    // Grant inserts or updates the (user, library) → role row.
    Grant(ctx context.Context, libraryID, userID uuid.UUID, role string) error
    Revoke(ctx context.Context, libraryID, userID uuid.UUID) error
}
```

## 3. Database migration — Postgres

`shared/db/migrations/0025_library_acl.sql`. Schema is the canonical
three-role shape per architecture §8.6 (column order: `library_id,
user_id, role`). The `role` default of `'admin'` makes existing
single-user installs continue to work without a back-fill (the sentinel
admin user gets every row as `'admin'`).

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE library_acl (
    library_id  UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id)     ON DELETE CASCADE,
    role        TEXT NOT NULL DEFAULT 'admin'
                  CHECK (role IN ('admin', 'editor', 'viewer')),
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (library_id, user_id)
);

-- "list libraries for user X" — primary read shape, but also used by
-- the JWT minter on EVERY mint, so this is hot.
CREATE INDEX library_acl_by_user ON library_acl (user_id);

-- "list users with access to library L" — used by admin UIs and audit.
-- Already covered by the PK on (library_id, user_id) for "given lib"
-- queries; we keep an explicit role index for "list editors of L".
CREATE INDEX library_acl_by_library_role ON library_acl (library_id, role);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS library_acl;
-- +goose StatementEnd
```

### 3.1 SQLite variant

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE library_acl (
    library_id  TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL REFERENCES users(id)     ON DELETE CASCADE,
    role        TEXT NOT NULL DEFAULT 'admin'
                  CHECK (role IN ('admin', 'editor', 'viewer')),
    granted_at  TEXT NOT NULL DEFAULT (CURRENT_TIMESTAMP),
    PRIMARY KEY (library_id, user_id)
);

CREATE INDEX library_acl_by_user           ON library_acl (user_id);
CREATE INDEX library_acl_by_library_role   ON library_acl (library_id, role);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS library_acl;
-- +goose StatementEnd
```

## 4. sqlc queries

`shared/db/queries/library_acl.sql`:

```sql
-- name: ListLibraryACLForUser :many
SELECT library_id, role FROM library_acl WHERE user_id = $1 ORDER BY library_id;

-- name: ListAllLibraryIDs :many
SELECT id FROM libraries ORDER BY id;

-- name: GrantLibraryACL :exec
INSERT INTO library_acl (library_id, user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (library_id, user_id) DO UPDATE SET role = EXCLUDED.role;

-- name: RevokeLibraryACL :execrows
DELETE FROM library_acl WHERE user_id = $1 AND library_id = $2;

-- name: ListUsersWithLibraryAccess :many
SELECT u.id, u.username, u.is_admin, la.role
  FROM library_acl la
  JOIN users u ON u.id = la.user_id
 WHERE la.library_id = $1
 ORDER BY u.username;
```

## 5. Authz implementation (minimal stub; full semantics in plan-23-02)

```go
// api/internal/auth/authz.go
type authz struct {
    libACL LibACL
    cfg    Config
}

// Can returns nil iff the user is permitted to perform action on resource.
// This is the minimal stub: admin users pass everything; non-admins pass
// any 'read'/'stream' action when they have ANY role on the library, and
// pass 'write'/'ingest' only with role='admin' or 'editor'. Full role-
// matrix semantics are provided by plan-23-02.
func (a *authz) Can(ctx context.Context, action Action, res Resource) error {
    user, ok := UserFromContext(ctx)
    if !ok {
        return ErrUnauthenticated
    }
    if user.IsAdmin {
        return nil
    }
    libID := res.LibraryID()
    if libID == "" {
        return ErrForbidden  // system-wide actions are admin-only
    }
    role, err := a.libACL.RoleForUser(ctx, user.ID, uuid.MustParse(libID))
    if err != nil {
        return ErrForbidden
    }
    switch action {
    case ActionLibraryRead, ActionVideoRead, ActionSearchRead, ActionSettingsRead:
        // any role suffices
        if role != "" {
            return nil
        }
    case ActionLibraryWrite, ActionVideoWrite, ActionSettingsWrite:
        if role == "admin" || role == "editor" {
            return nil
        }
    }
    return ErrForbidden
}
```

The full role matrix (admin can do everything, editor can read+write,
viewer can read only, plus the streaming-side audience checks) lives
in [plan-23-02](../23-security/plan-23-02-authorization-acls.md). This
stub is enough to satisfy the tests in §9 without contradicting the
canonical implementation.

The `OwnerScope(ctx)` helper wraps queries that should be filtered:

```go
// api/internal/auth/scope.go
func OwnerScope(ctx context.Context) (uuid.UUID, bool) {
    u, ok := UserFromContext(ctx)
    if !ok { return uuid.Nil, false }
    return u.ID, true
}
```

## 6. RequireAdmin middleware

```go
// api/internal/http/middleware/requireadmin.go
func RequireAdmin(audit auth.AuditSink) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            u, ok := auth.UserFromContext(r.Context())
            if !ok {
                problem(w, http.StatusUnauthorized, "unauthorized", "")
                return
            }
            if !u.IsAdmin {
                // Story 10.16 audit emission: log a `permission.denied`
                // row on every 403 from this gate. Sampled at 1/min per
                // (user_id, action) at the emitter (per Story 10.16 §6)
                // — the AuditSink itself does not sample.
                audit.Record(r.Context(), auth.AuditPermissionDenied{
                    UserID:   u.ID,
                    Action:   "admin-required",
                    Resource: r.URL.Path,
                })
                problem(w, http.StatusForbidden, "forbidden", "")
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

The same emit happens in any other `Authz.Can(...) → false` 403 path
(e.g., per-resource `library.read` denials in handlers). The
`permission.denied` event is in the Story 10.16 vocabulary and uses the
sampled-at-emitter rule to bound write volume on busy denial loops.

## 7. Per-user filtering

Two queries change shape (additions to existing sqlc files):

```sql
-- name: GetVideoWithUserPlaybackState :one
SELECT v.*,
       ps.position_sec, ps.completed, ps.updated_at AS ps_updated_at
  FROM videos v
  LEFT JOIN playback_state ps ON ps.video_id = v.id AND ps.user_id = $2
 WHERE v.id = $1;

-- name: ListSavedSearchesForUser :many
SELECT * FROM saved_searches WHERE user_id = $1 ORDER BY created_at DESC;
```

The video handler:

```go
// api/internal/http/videos.go (additions)
type videoResource struct{ libID string }
func (v videoResource) LibraryID() string { return v.libID }

func getVideo(authz auth.Authz, q *db.Queries) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        vid := uuid.MustParse(chi.URLParam(r, "id"))
        libID, err := lookupVideoLibrary(r.Context(), vid)
        if err != nil {
            problem(w, http.StatusForbidden, "forbidden", "")
            return
        }
        if err := authz.Can(r.Context(), auth.ActionVideoRead, videoResource{libID: libID.String()}); err != nil {
            problem(w, http.StatusForbidden, "forbidden", "")
            return
        }
        uid, _ := auth.OwnerScope(r.Context())
        row, err := q.GetVideoWithUserPlaybackState(r.Context(), db.GetVideoWithUserPlaybackStateParams{
            ID: vid, UserID: uid,
        })
        // ...
    }
}
```

## 8. JWT `lib[]` cap

```go
// api/internal/auth/jwt.go (additions)
const MaxLibClaimEntries = 1000

func capLibClaim(libs []string) []string {
    if len(libs) <= MaxLibClaimEntries { return libs }
    slog.Warn("lib claim truncated",
        "have", len(libs), "max", MaxLibClaimEntries)
    return libs[:MaxLibClaimEntries]
}
```

Used by both the access-token mint (10.3) and the signed-URL mint (10.8).

## 9. Test plan

### 9.1 LibACL (`lib_acl_test.go`)

| Test | What it pins |
|---|---|
| `TestGrantWithRoleThenList` | After Grant(lib1, uid, "viewer"), ListLibraryACLForUser(uid) returns [(lib1, "viewer")]. |
| `TestGrantUpdatesRoleOnConflict` | Grant(lib1, uid, "viewer") then Grant(lib1, uid, "editor") → row's role is "editor"; no duplicate. |
| `TestRoleCheckConstraint` | Inserting role='owner' (not in admin/editor/viewer) fails the CHECK constraint. |
| `TestDefaultRoleIsAdmin` | INSERT without role specified yields role='admin'. |
| `TestRevokeRemovesRow` | Revoke returns 1; second call returns 0. |
| `TestCascadeOnUserDelete` | Delete user → all their library_acl rows gone. |
| `TestCascadeOnLibraryDelete` | Delete library → all rows for that lib gone. |
| `TestAllLibraryIDsReturnsEvery` | Insert 3 libraries → AllLibraryIDs returns all 3. |

### 9.2 Authz (`authz_test.go`)

| Test | What it pins |
|---|---|
| `TestAdminAllowedAll` | is_admin=true → `Can(_, video.write, _) == nil`. |
| `TestAnonDeniedAll` | No user in ctx → `Can(_, video.read, _) == ErrUnauthenticated`. |
| `TestVideoReadAllowedForViewer` | Grant lib L role=viewer; video V in L → `Can(_, video.read, videoResource{L}) == nil`. |
| `TestVideoWriteDeniedForViewer` | Grant lib L role=viewer → `Can(_, video.write, videoResource{L}) == ErrForbidden`. |
| `TestVideoWriteAllowedForEditor` | Grant lib L role=editor → `Can(_, video.write, videoResource{L}) == nil`. |
| `TestVideoReadDeniedWhenNoRole` | No grant → ErrForbidden. |
| `TestLibraryWriteSystemWideRequiresAdmin` | system-wide resource (LibraryID=="") + non-admin → ErrForbidden. |

### 9.3 HTTP integration

| Test | What it pins |
|---|---|
| `TestNonAdminPostLibrariesReturns403` | Non-admin POST `/api/libraries` → 403 `forbidden`. |
| `TestUserAReadingUserBPlaybackStateFiltered` | User A GETs `/api/videos/V`; the response's `playback_state` matches A's row, never B's. |
| `TestSavedSearchesPerUser` | User A's saved searches do not appear in user B's list. |
| `TestForbiddenDoesNotLeakExistence` | A non-admin GETs `/api/videos/<existing-id-they-dont-have-access-to>` → 403; same response shape as if the id didn't exist. |
| `TestStreamingRejectsLibLessJWT` | Mint a token with `lib=[]`; attempt to stream any video → Story 10.7's middleware returns 401 `wrong-lib` (cross-story integration test). |

### 9.4 JWT lib cap

| Test | What it pins |
|---|---|
| `TestLibClaimCappedAt1000` | A user with 1500 ACL rows → minted token's `lib` length == 1000; WARN log emitted. |
| `TestLibClaimUnderCap` | 50 ACL rows → length 50; no WARN. |

## 10. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Non-admin opens stream session for a video they have access to | Allowed; their `playback_state` records under their own user_id. Cross-user resume is intra-user only (story EC). | `TestStreamingPerUserPlayback` |
| User downgraded mid-session | In-flight access tokens still carry `is_admin: true` until `exp`; for instant revocation operator runs `keys rotate --immediate` (Story 10.6). | n/a (covered in 10.6 + 10.5 EC) |
| Library granted to a user mid-session | Their *next refresh* picks up the new lib in the JWT (Story 10.4 AC-1 re-snapshots); already-issued URLs continue to honor the *old* snapshot. | `TestLibClaimUpdatesOnRefresh` (Story 10.4) |
| User has admin and library_acl rows | Admin path short-circuits; the ACL rows are irrelevant. | `TestAdminAllowedAll` |
| 403 vs 404 trade-off | Always 403 for "you don't have access" even when the resource doesn't exist; this is the privacy-over-debuggability decision. The handler probes existence only after authz allows. | `TestForbiddenDoesNotLeakExistence` |
| Library deleted mid-request | CASCADE deletes the ACL row; the `Authz.Can` call may race and return true with a stale view. The downstream resource lookup then returns 404. The race is benign (no privilege escalation). | n/a |
| User in 50 libraries, each with 100 videos | Authz call resolves video → library (one DB read or cache hit) → check membership in the 50-element slice → O(50) — fine. | n/a |
| Sentinel admin (single-user) | `is_admin=true` short-circuits everything. | `TestAdminAllowedAll` |
| `lib[]` >> 1000 (admin with thousands of libs) | Capped at 1000 with WARN. v2 plan: emit `lib_all=true` sentinel claim and have Streaming honor it for admins. Documented in this story's plan. | `TestLibClaimCappedAt1000` |
| `Authz` interface called with an unknown Action | Returns `false, nil` (deny); doesn't error. Documented as a defense-in-depth choice. | `TestUnknownActionDenied` |

## 11. Dependencies

No new dependencies.

## 12. Acceptance checklist

**Schema**
- [ ] `0025_library_acl.sql` applies; PK is `(library_id, user_id)`; `role` column has CHECK constraint admin|editor|viewer; default is `'admin'`; CASCADE from users + libraries.

**Authz**
- [ ] `Authz.Can(ctx, Action, Resource) error` matches the canonical signature in plan-23-02.
- [ ] `Can` returns nil for admin on every action.
- [ ] Minimal stub: viewer/editor/admin role rows pass `Read`; editor/admin pass `Write`. Full role matrix deferred to plan-23-02.
- [ ] AC-2: video detail handler filters `playback_state` to ctx user.
- [ ] AC-3: saved-searches list filtered to ctx user.
- [ ] AC-4: forbidden response is `403 type: forbidden` with no resource leak.
- [ ] AC-5: JWT minter (10.3) and signed-URL minter (10.8) snapshot `lib[]` from ACLs.

**lib cap**
- [ ] `lib[]` capped at 1000 entries; WARN log emitted on overflow.

**Integration**
- [ ] Non-admin POST `/api/libraries` → 403.
- [ ] Streaming rejects a `lib`-less JWT for any video (verified via cross-story integration test).

**Tests**
- [ ] All §9 tests pass on both dialects.

**Docs**
- [ ] README.md ticks story 10.13.
- [ ] Cross-link to plan-23-02 documented in §0; full role matrix deferred there.
