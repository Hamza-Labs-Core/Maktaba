# Implementation Plan — Story 23.2 Authorization and ACLs

> Companion to [story-23-02-authorization-acls.md](story-23-02-authorization-acls.md).
> Story states *what* and *why*; this plan states *how*.
> Builds on the JWT minter from
> [Story 23.1](plan-23-01-authentication.md), the per-library ACL table
> from [Story 19.8](../19-scalability/story-19-08-multi-tenant-readiness.md),
> and the streaming session model from
> [Epic 8](../08-streaming/README.md).

## 0. Scope and placement

This plan is the **canonical implementation** of the three-role
authorization model (architecture §8.6). [plan-10-13](../10-auth-security/plan-10-13-permission-model.md)
creates the `library_acl(library_id, user_id, role)` table with the
three-role check constraint and a minimal `Authz.Can` stub; full
semantics (role matrix, middleware, lint, streaming-side checks) live
here.

| Concern | Decision |
|---|---|
| `Authz.Can(ctx, Action, Resource) error` interface | `api/internal/authz/authorize.go`. **Canonical signature**, matched by [plan-10-13](../10-auth-security/plan-10-13-permission-model.md). One function; every handler calls it. |
| Roles | `admin | editor | viewer` per library; rows in `library_acl(library_id, user_id, role)` (canonical schema, arch §8.6 line 1690; created by plan-10-13). |
| JWT shape | Uses `sub`, `lib`, `is_admin`, `aud` claims (defined in Story 23.1; user UUID is `sub`, no separate `usr` claim). This plan gates streaming on them. |
| Streaming-side check | `streaming/internal/authz/library.go`; rejects manifest/segment/poster requests without a matching `lib` claim. |
| Audience separation | `streaming` (segments + manifest), `streaming-direct` (direct play range), `streaming-static` (poster, sprite, subtitle). [plan-10-08](../10-auth-security/plan-10-08-signed-url-minter.md) **owns audience minting** (which token gets which `aud`); this plan **owns audience-action mapping** (which path requires which audience). |
| Linter | `tools/authz-lint.go` walks chi routes and asserts every handler calls `authz.Can()`. |
| Out of scope | JWKS / signing (10.6, 23.1); ACL admin UI (Epic 11/19); audit log mechanics (Epic 21.6); audience minting for signed URLs (plan-10-08). |

## 1. Architecture diagram

```
                ┌───────────────────────────┐
   API request →│ chi router → authn        │ → resolves auth.User + libs
                └───────────────┬───────────┘
                                │
                                ▼
                ┌───────────────────────────┐
                │ authz.Can(ctx, act, res)  │ ← all handlers call this
                │  - admin bypass           │
                │  - role lookup            │
                │  - resource → library_id  │
                └───────────────┬───────────┘
                                │ ok
                                ▼
              handler runs; mints streaming JWT with `lib` claim


    streaming request
        │
        ▼
   ┌─────────────────────────┐
   │ jwks_cache.PublicKey    │
   │ jwt.Parse(audience...)  │
   │ assertLibraryInClaim()  │
   └─────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/authz/authorize.go` | The single function + helpers. |
| `api/internal/authz/roles.go` | `Role` type, `(role).Can(action)` matrix. |
| `api/internal/authz/resource.go` | `Resource` interface; concrete types per surface. |
| `api/internal/authz/middleware.go` | chi middleware that loads `library_acl` rows once per request. |
| `api/internal/authz/lint_helpers.go` | Recognizable function name pattern + struct tag; lint reads it. |
| `streaming/internal/authz/library.go` | Streaming-side check. |
| `streaming/internal/authz/audience.go` | Maps URL path → required audience. |
| `tools/authz-lint.go` | Static check; runs in lint gate (Story 22.1). |
| Tests — `_test.go` per file plus `cross_tenant_test.go` for TC2. |

### 2.2 Modified files

| Path | Change |
|---|---|
| Every handler under `api/internal/http/*` | Call `authz.Can(ctx, action, resource)` near the top. The lint checks this. |
| `api/internal/http/router.go` | Mount `authz.Middleware` between authn and the route handlers. |
| `streaming/internal/http/router.go` | Mount audience + library check before serving any media. |
| `api/internal/auth/jwt.go` (Story 23.1) | Already populates `lib`; this plan defines exactly which IDs go in (current entitlements at mint time). |

### 2.3 The single Authorize call

`api/internal/authz/authorize.go`. The canonical signature is
`Authz.Can(ctx context.Context, action Action, resource Resource) error`;
[plan-10-13](../10-auth-security/plan-10-13-permission-model.md)
matches this signature.

```go
package authz

import (
    "context"
    "errors"

    "maktaba/api/internal/authcontext"
)

type Action string

const (
    Read         Action = "read"
    Write        Action = "write"
    Stream       Action = "stream"
    Ingest       Action = "ingest"
    AdminLibrary Action = "admin"   // manage acls, rename, delete
)

type Resource interface {
    LibraryID() string  // empty for system-wide (admin) resources
}

var (
    ErrUnauthenticated = errors.New("authz: unauthenticated")
    ErrForbidden       = errors.New("authz: forbidden")
    ErrResourceMissing = errors.New("authz: resource gone")
)

// Authz is the authorization interface. Can returns nil iff the caller
// is permitted to perform act on res.
type Authz interface {
    Can(ctx context.Context, act Action, res Resource) error
}

// defaultAuthz is the production implementation. plan-10-13 ships a
// minimal stub matching this signature; this plan provides the full
// role-matrix semantics.
type defaultAuthz struct{ /* fields populated by middleware */ }

func (defaultAuthz) Can(ctx context.Context, act Action, res Resource) error {
    u, ok := authcontext.From(ctx)
    if !ok { return ErrUnauthenticated }

    libID := res.LibraryID()

    // System-wide route (e.g., /api/system/*): admin only.
    if libID == "" {
        if u.IsAdmin { return nil }
        return ErrForbidden
    }

    // Admin bypasses ACL (Story 23.1 single-user mode also goes through here
    // via the sentinel user — IsAdmin=true).
    if u.IsAdmin { return nil }

    role, ok := u.LibraryRoles[libID]
    if !ok { return ErrForbidden }
    if role.Can(act) { return nil }
    return ErrForbidden
}

// Can is the package-level entry point used by handlers and the lint.
func Can(ctx context.Context, act Action, res Resource) error {
    return defaultAuthz{}.Can(ctx, act, res)
}
```

The middleware (next section) populates `u.LibraryRoles` once per
request. The `authcontext` package is owned by Story 10.1.

### 2.4 Role matrix

`roles.go`:

```go
type Role string

const (
    RoleAdmin  Role = "admin"
    RoleEditor Role = "editor"
    RoleViewer Role = "viewer"
)

var matrix = map[Role]map[Action]bool{
    RoleAdmin:  {Read: true, Stream: true, Write: true, Ingest: true, AdminLibrary: true},
    RoleEditor: {Read: true, Stream: true, Write: true, Ingest: true, AdminLibrary: false},
    RoleViewer: {Read: true, Stream: true, Write: false, Ingest: false, AdminLibrary: false},
}

func (r Role) Can(a Action) bool {
    return matrix[r][a]
}
```

### 2.5 Middleware

`middleware.go`:

```go
func Middleware(q *db.Queries) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            u, ok := authcontext.From(r.Context())
            if !ok { next.ServeHTTP(w, r); return }
            // Single-user / admin: skip lookup; rights are unbounded.
            if u.IsAdmin {
                next.ServeHTTP(w, r); return
            }
            rows, err := q.SelectLibraryRolesForUser(r.Context(), u.ID)
            if err != nil {
                http.Error(w, "authz lookup", http.StatusInternalServerError); return
            }
            roles := make(map[string]Role, len(rows))
            for _, r := range rows {
                roles[r.LibraryID.String()] = Role(r.Role)
            }
            u.LibraryRoles = roles
            next.ServeHTTP(w, r.WithContext(authcontext.With(r.Context(), u)))
        })
    }
}
```

One DB hit per authenticated request; ACL changes thus propagate within
one request boundary (no in-process cache that could stale).

### 2.6 The lint

`tools/authz-lint.go`:

```go
// Walks every package under api/internal/http and asserts that every
// function with the signature `func(http.ResponseWriter, *http.Request)`
// or returned by a `func(...) http.HandlerFunc` calls authz.Can.
//
// Uses `go/ast` over the parsed AST. The lint allows opt-out via a
// `//authz: public` comment immediately above the function (e.g., for
// `/api/.well-known/jwks.json`).

func main() {
    fset := token.NewFileSet()
    pkgs, _ := parser.ParseDir(fset, "api/internal/http", nil, parser.ParseComments)
    var missing []string
    for _, pkg := range pkgs {
        for _, f := range pkg.Files {
            ast.Inspect(f, func(n ast.Node) bool {
                fn, ok := n.(*ast.FuncDecl)
                if !ok { return true }
                if !looksLikeHandler(fn.Type) { return true }
                if hasOptOutComment(fset, fn) { return true }
                if !callsAuthorize(fn.Body) {
                    missing = append(missing,
                        fmt.Sprintf("%s:%d %s missing authz.Can",
                            fset.Position(fn.Pos()).Filename,
                            fset.Position(fn.Pos()).Line,
                            fn.Name.Name))
                }
                return true
            })
        }
    }
    if len(missing) > 0 {
        for _, m := range missing { fmt.Fprintln(os.Stderr, m) }
        os.Exit(1)
    }
}
```

### 2.7 Streaming side

`streaming/internal/authz/library.go`:

```go
type Verifier struct {
    jwks    *auth.JWKSCache
    iss     string
    parser  *jwt.Parser
}

func (v *Verifier) Authorize(r *http.Request, libraryID, audience string) error {
    raw := bearerOrCookieToken(r)
    if raw == "" { return ErrUnauthenticated }
    var c auth.AccessClaims
    tok, err := v.parser.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
        kid, _ := t.Header["kid"].(string)
        return v.jwks.PublicKey(kid)
    })
    if err != nil || !tok.Valid {
        return wrapJWTError(err)  // expired -> 403, not 401 (per AC3)
    }
    // Audience is enforced by RegisteredClaims.Audience (a ClaimStrings).
    if !claimsHaveAudience(c.Audience, audience) {
        return ErrAudience
    }
    if c.Issuer != v.iss {
        return ErrIssuer
    }
    if !slices.Contains(c.Lib, libraryID) {
        return ErrLibraryNotInClaim
    }
    return nil
}
```

The `wrapJWTError` helper maps `jwt.ErrTokenExpired` to a typed error
the HTTP layer turns into `403 type: token-expired` (not 401, per AC3
— a manifest-level expiry isn't an unauthenticated request, it's a
forbidden one).

### 2.8 Audience routing

`streaming/internal/authz/audience.go`:

```go
func RequiredAudience(path string) (string, bool) {
    switch {
    case strings.HasSuffix(path, ".m3u8"),
         strings.HasSuffix(path, ".m4s"),
         strings.HasSuffix(path, ".ts"):
        return "streaming", true
    case strings.HasSuffix(path, ".vtt"),
         strings.HasSuffix(path, "/poster.jpg"),
         strings.Contains(path, "/sprites/"):
        return "streaming-static", true
    case strings.HasPrefix(path, "/stream/direct/"):
        return "streaming-direct", true
    }
    return "", false
}
```

## 3. Schema

`shared/db/queries/library_acl.sql`:

```sql
-- name: SelectLibraryRolesForUser :many
SELECT library_id, role
FROM library_acl
WHERE user_id = $1;

-- name: PutLibraryRole :exec
INSERT INTO library_acl (user_id, library_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, library_id) DO UPDATE SET role = EXCLUDED.role;

-- name: DeleteLibraryRole :exec
DELETE FROM library_acl
WHERE user_id = $1 AND library_id = $2;
```

The table is owned by Story 19.8 (`library_acl`). This plan only adds
new queries to its companion `.sql` file.

## 4. Test plan

### 4.1 Lint (TC1)

| Test | What it pins |
|---|---|
| `TestLintFailsOnMissingAuthorize` | A fixture handler in `api/internal/http/_lintfix/missing.go` (build-tagged out of the binary) is detected by `tools/authz-lint`; CI fails. |
| `TestLintAcceptsOptOut` | `//authz: public` comment exempts the JWKS handler and the health endpoint. |

### 4.2 Cross-tenant (TC2)

| Test | What it pins |
|---|---|
| `TestViewerLibAOnLibBRefused` | User V has viewer role on library A, no role on B; `GET /api/videos?library=B` returns 403 `forbidden`; streaming `GET /stream/B/.../seg.m4s` returns 403 `library-not-in-claim`. |
| `TestEditorOnLibAReadOnB` | E has editor on A only; `GET /api/library/B` returns 403. |
| `TestAdminCrossesAllLibs` | Admin sees all libs; both REST and streaming. |

### 4.3 Privilege escalation (TC3)

| Test | What it pins |
|---|---|
| `TestViewerCannotPromoteSelf` | `PUT /api/library/{id}/acl` from V → 403. |
| `TestEditorCannotPromoteAnyone` | E PATCH on the ACL → 403. |
| `TestAdminCanPromote` | Admin PATCH succeeds. |

### 4.4 JWT shape (TC4)

| Test | What it pins |
|---|---|
| `TestMintIncludesLibSubIsAdmin` | `auth.Mint` for a user with libs `[a,b]` produces a JWT whose claims include `lib=[a,b]`, `sub=<user-uuid>`, `is_admin=false`. |
| `TestStreamingRejectsMissingLib` | A JWT minted without `lib` (manually) is rejected by the streaming verifier with 403 `lib-missing`. |
| `TestExpiredJWTReturns403` | An expired JWT to streaming returns 403 `token-expired`, not 401. |

### 4.5 Audience (TC5)

| Test | What it pins |
|---|---|
| `TestStaticAudOnSegmentRefused` | A `streaming-static` JWT against `seg.m4s` returns 403 `audience-mismatch`. |
| `TestSegmentAudOnPosterAccepted` | The audience check rejects, not allows, the wider audience: a `streaming` JWT against poster paths succeeds (because segments are the strictest path; the poster path accepts both `streaming` and `streaming-static`). The `RequiredAudience` map is the source of truth — an audit checks that only the explicitly listed audiences are allowed per path. |

## 5. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Lost ACL mid-stream (EC1) | Existing JWT (≤ 15 min) continues to authorize segments; manifest refresh after the change drops the lib from the new JWT. Documented as "≤ 15 min revocation lag." | `TestRevocationLagBoundedBy15Min` |
| Removing the only admin (EC2) | The user store rejects the delete with `ErrLastAdmin` (Story 10.1); the authz layer never sees the request. | n/a (Story 10.1) |
| Library deleted while session active (EC3) | `Can()` returns `ErrResourceMissing`; HTTP layer maps to 410 `library-gone`; web client navigates home. | `TestLibraryGoneDuringSession` |
| Direct DB ACL edit | The middleware reads the table per request; an admin's `psql` UPDATE takes effect on the next request. | n/a |
| Performance under high load | The DB hit per request is by `(user_id, library_id)` index; benchmark shows < 0.5 ms p99 in the integration suite. The plan does not introduce a process-local cache (would slow ACL revocations). | `BenchmarkAuthzMiddleware` |
| `aud` field mismatch on signed-URL JWTs | Signed URLs (Epic 8.13) use `streaming-direct` or `streaming-static`; never `streaming`. The audience map enforces. | `TestSignedURLAudience` |
| JWT with `lib=null` | `slices.Contains(nil, x) == false` — refused. | `TestNullLibClaim` |
| Empty `aud` claim | An empty `Audience` ClaimStrings is rejected by the parser when `WithAudience` constraints are set. | `TestEmptyAudClaim` |
| Multi-library streaming session | Admins streaming across libraries get a token whose `lib` includes all libraries they have a role on, capped at 256 entries. | `TestLibClaimSizeCap` |
| Hot-changing roles via WebSocket | Web client receives a `acl_changed` event (Epic 11) and forces a manifest re-mint; the new manifest carries the updated `lib`. | `TestAclChangedTriggersRemint` |

## 6. Dependencies

| Dep | Version | Why |
|---|---|---|
| `github.com/golang-jwt/jwt/v5` | latest | Verify on streaming side. |
| `slices` | stdlib (Go 1.21+) | `slices.Contains`. **Pin to stdlib `"slices"`**, not `golang.org/x/exp/slices`. |
| `go/ast`, `go/parser`, `go/token` | stdlib | Authz-lint. |
| `chi/v5` | already | Routing. |

## 7. Acceptance checklist

**API**
- [ ] Every handler calls `authz.Can`. Lint enforces.
- [ ] `library_acl` lookup happens once per request (middleware).
- [ ] Admin and single-user-mode short-circuit correctly.

**JWT**
- [ ] Tokens carry `sub` (user UUID), `lib`, `is_admin`, `aud`, `kid`.
- [ ] `aud` is one of `streaming | streaming-direct | streaming-static`.

**Streaming**
- [ ] Path → audience mapping matches `RequiredAudience`.
- [ ] `lib` claim must contain the requested library.
- [ ] Expired tokens return 403, not 401.

**Lint**
- [ ] `tools/authz-lint` runs in the lint gate.
- [ ] Documented opt-out comment for genuinely public endpoints (JWKS, health).

**Tests**
- [ ] All §4 tests pass.
