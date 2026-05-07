# Implementation Plan — Story 10.13 Permission model

> Companion to [story-10-13-permission-model.md](story-10-13-permission-model.md).
> The `library_acl` table is owned here (no other story claims it).
> The `lib[]` snapshot is consumed by [Story 10.7](plan-10-07-streaming-jwt-verify.md)
> and minted by [Story 10.3](plan-10-03-native-login.md) /
> [Story 10.8](plan-10-08-signed-url-minter.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration file | `shared/db/migrations/0025_library_acl.sql` and `.sqlite.sql`. |
| Authz interface | `api/internal/auth/authz.go` — `Authz.Can(ctx, action, resourceID) (bool, error)`. |
| v1 policy | Compiled-in switch table in `authz.go`; v2 will read role configs from DB. |
| Per-user filters | `api/internal/auth/scope.go` — `OwnerScope(ctx)` returns the user_id for filtering `playback_state` and `saved_searches` queries. |
| 403 envelope | Generic `type: forbidden` (do not leak resource existence). |
| Out of scope | RBAC beyond admin/viewer (v2). SSO/OIDC (v2). 2FA (v2). |

## 1. Architecture diagram

```
                      ┌────────────────────────────────────┐
                      │ library_acl                         │
                      │  user_id   library_id               │
                      │  ────────  ──────────               │
                      │  (FK→users) (FK→libraries)          │
                      │  PRIMARY KEY (user_id, library_id)  │
                      └────────────────────────────────────┘
                                  ▲
                                  │ used by
                                  │
       ┌──────────────────────────┴──────────────────────────┐
       │                                                       │
┌───────────────────┐                              ┌───────────────────────┐
│ LibACL.Libraries  │                              │ Authz.Can(ctx, action, │
│ ForUser(uid)      │                              │   resourceID)          │
│   used by:        │                              │   - admin? → allow      │
│   - JWT minter    │                              │   - "*.write" → admin   │
│   - signed-URL    │                              │   - "*.read" → check    │
│   - middleware    │                              │     library_acl         │
└───────────────────┘                              │   - per-user resources  │
                                                   │     check OwnerScope    │
                                                   └───────────────────────┘
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

type Authz interface {
    Can(ctx context.Context, action Action, resourceID uuid.UUID) (bool, error)
}

var ErrForbidden = errors.New("authz: forbidden")
```

```go
// api/internal/auth/lib_acl.go
type LibACL interface {
    LibrariesForUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
    AllLibraryIDs(ctx context.Context) ([]uuid.UUID, error)
    Grant(ctx context.Context, userID, libraryID uuid.UUID) error
    Revoke(ctx context.Context, userID, libraryID uuid.UUID) error
}
```

## 3. Database migration — Postgres

`shared/db/migrations/0025_library_acl.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE library_acl (
    user_id     UUID NOT NULL REFERENCES users(id)     ON DELETE CASCADE,
    library_id  UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, library_id)
);

-- "list libraries for user X" — primary read shape, but also used by
-- the JWT minter on EVERY mint, so this is hot.
CREATE INDEX library_acl_by_user ON library_acl (user_id);

-- "list users with access to library L" — used by admin UIs and audit.
CREATE INDEX library_acl_by_library ON library_acl (library_id);
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
    user_id     TEXT NOT NULL REFERENCES users(id)     ON DELETE CASCADE,
    library_id  TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    granted_at  TEXT NOT NULL DEFAULT (CURRENT_TIMESTAMP),
    PRIMARY KEY (user_id, library_id)
);

CREATE INDEX library_acl_by_user    ON library_acl (user_id);
CREATE INDEX library_acl_by_library ON library_acl (library_id);
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
SELECT library_id FROM library_acl WHERE user_id = $1 ORDER BY library_id;

-- name: ListAllLibraryIDs :many
SELECT id FROM libraries ORDER BY id;

-- name: GrantLibraryACL :exec
INSERT INTO library_acl (user_id, library_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: RevokeLibraryACL :execrows
DELETE FROM library_acl WHERE user_id = $1 AND library_id = $2;

-- name: ListUsersWithLibraryAccess :many
SELECT u.id, u.username, u.is_admin
  FROM library_acl la
  JOIN users u ON u.id = la.user_id
 WHERE la.library_id = $1
 ORDER BY u.username;
```

## 5. Authz implementation

```go
// api/internal/auth/authz.go
type authz struct {
    libACL LibACL
    cfg    Config
}

func (a *authz) Can(ctx context.Context, action Action, resourceID uuid.UUID) (bool, error) {
    user, ok := UserFromContext(ctx)
    if !ok { return false, nil }   // anonymous → deny
    if user.IsAdmin { return true, nil }

    switch action {
    case ActionLibraryRead:
        libs, err := a.libACL.LibrariesForUser(ctx, user.ID)
        if err != nil { return false, err }
        for _, l := range libs { if l == resourceID { return true, nil } }
        return false, nil

    case ActionVideoRead:
        // resourceID is the video id. Resolve its library, then ACL-check.
        libID, err := a.lookupVideoLibrary(ctx, resourceID)
        if err != nil { return false, err }
        return a.Can(ctx, ActionLibraryRead, libID)

    case ActionVideoWrite:
        return false, nil   // admin-only — already returned true above

    case ActionLibraryWrite, ActionSettingsWrite:
        return false, nil   // admin-only

    case ActionSearchRead, ActionSettingsRead:
        return true, nil    // any authenticated user

    default:
        return false, nil
    }
}

func (a *authz) lookupVideoLibrary(ctx context.Context, videoID uuid.UUID) (uuid.UUID, error) {
    // Cache resolution via the in-process video metadata cache when present;
    // fall back to a SELECT library_id FROM videos WHERE id=$1.
    // (Same in-memory shape as Streaming's probe cache, populated lazily.)
    return a.cfg.VideoLibraryLookup(ctx, videoID)
}
```

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
func getVideo(authz auth.Authz, q *db.Queries) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        vid := uuid.MustParse(chi.URLParam(r, "id"))
        ok, err := authz.Can(r.Context(), auth.ActionVideoRead, vid)
        if err != nil || !ok {
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
| `TestGrantThenList` | After Grant(uid, lib1), ListLibraryACLForUser(uid) returns [lib1]. |
| `TestGrantIdempotent` | Grant twice → ON CONFLICT → no duplicate row. |
| `TestRevokeRemovesRow` | Revoke returns 1; second call returns 0. |
| `TestCascadeOnUserDelete` | Delete user → all their library_acl rows gone. |
| `TestCascadeOnLibraryDelete` | Delete library → all rows for that lib gone. |
| `TestAllLibraryIDsReturnsEvery` | Insert 3 libraries → AllLibraryIDs returns all 3. |

### 9.2 Authz (`authz_test.go`)

| Test | What it pins |
|---|---|
| `TestAdminAllowedAll` | is_admin=true → `Can(_, video.write, _) == true`. |
| `TestAnonDeniedAll` | No user in ctx → `Can(_, video.read, _) == false`. |
| `TestVideoReadAllowedWhenLibraryGranted` | Grant lib L; video V in L → `Can(_, video.read, V) == true`. |
| `TestVideoReadDeniedWhenLibraryNotGranted` | No grant; video V in L → false. |
| `TestLibraryWriteRequiresAdmin` | Non-admin → false. |
| `TestSettingsReadAllowedForAuth` | Any authenticated user → true. |

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
- [ ] `0025_library_acl.sql` applies; both indexes present; CASCADE from users + libraries.

**Authz**
- [ ] `Authz.Can` returns true for admin on every action.
- [ ] AC-1 v1 policy table: `*.read` → ACL; `*.write` → admin.
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
- [ ] v2 plan note: replace per-user lib snapshot with role-based authz; add `lib_all` for admins.
