# Plan 10.13 — Permission model — implementation

> Implementation plan for [story-10-13-permission-model.md](story-10-13-permission-model.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: depends on `users.is_admin` from Story 10.1;
> the `lib[]` JWT claim and signed-URL `lib[]` snapshot are minted by
> [Plan 10.8](plan-10-08-signed-url-minter.md) using the resolver in this
> plan; per-user filtering on `playback_state` is the contract Epic 7
> handlers rely on; Streaming-side offline checks (Epic 8 Story 8.1)
> consume the `lib[]` claim with no API roundtrip.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Single Go interface `Authz` with one method `Can(ctx, action, resourceID)`** in `internal/auth/authz`. v1 ships `RoleBasedAuthz` (admin + per-library ACL); v2 fine-grained roles slot in by registering a new implementation. Handlers depend only on the interface. | Story description: "implemented via a single `Authz` interface so v2 can add fine-grained roles without rewriting handlers." | A second method (`Filter` or `List`) would let v2 evade the interface; one method forces all decisions through the same code path. The action vocabulary is a string for forward compatibility. |
| D2 | **Action vocabulary as Go `const` strings**, namespace-scoped: `video.read`, `video.write`, `library.read`, `library.write`, `library.acl.write`, `user.write`, `playback.read`, `playback.write`, `saved_search.read`, `saved_search.write`, `audit.read`. | Story AC-1 defaults table; extended to cover Epic 7 surfaces. | A typed enum closes the set; we want it open so Epic 7 can add `chapter.read` without re-vendoring. Constants in one file (`actions.go`) keep them discoverable. |
| D3 | **`library_acl` is a per-(user, library) row with `can_write BOOL`**, no role enum. Read access is granted by row presence; write access requires `can_write=true` OR `is_admin=true`. | Story AC-1: "any authenticated user with the relevant `library_id` in their `library_acl` rows". | A role enum (`viewer`, `contributor`, `owner`) bakes policy into the schema; a single `can_write` flag is the minimum that satisfies v1 and lets v2 layer roles on top via a sibling table. |
| D4 | **403 problem+json `type: forbidden` with a generic message**; never differentiate "resource not found" from "no access". The middleware emits `{"type":"forbidden","title":"Forbidden","status":403}` and logs the actual reason internally. | Story AC-4: "don't leak whether the resource exists." | Enumerating "exists but forbidden" vs "not found" is an information leak (CWE-204). Loud-fail internal logs preserve debuggability. |
| D5 | **`lib[]` JWT claim = snapshot of `library_acl` row library_ids at issue time**. The resolver `LibraryIDsForUser(ctx, userID)` returns `[]uuid.UUID` (admins → all library IDs). Story 10.8 (signed URLs) and Story 10.3 (access tokens) both call this resolver at mint time. Streaming verifies offline against the snapshot — staleness window = access-token TTL (15 min). | Story AC-5; architecture §9.4 / §9.8. | Online checks would force Streaming to call the API on every range request, defeating the offline guarantee. The 15-minute staleness is documented as a trade-off; `logout-all` + key rotation kills in-flight tokens immediately if needed (Story 10.6 AC-5). |
| D6 | **Per-user filtering on `playback_state` and `saved_searches` happens in the SQL handler, not in `Authz`.** `Authz.Can(ctx, "playback.read", videoID)` only verifies the user can read the video; the SQL `WHERE user_id = $1` is the row-level guard. Reasoning: filtering 1000 rows through `Authz` would mean 1000 calls; SQL filters in O(1). | Story AC-2/AC-3. | Mixing row-level filtering into `Authz` couples it to SQL. Keeping the boundary at the resource (video/saved-search-id) lets `Authz` stay stateless. |

If D5 is rejected (online checks instead of `lib[]` snapshot): every Streaming range request adds an API roundtrip (~5 ms p50), inflating playback start time and creating an availability dependency from Streaming → API. The 15-minute staleness window is the documented trade.

---

## 1. Architecture diagram

```
   ┌────────────────────────────────────────────────────────────────┐
   │  HTTP request → chi mux                                        │
   │     │                                                          │
   │     ▼                                                          │
   │  internal/auth/middleware.RequireUser  (Story 10.2/10.3)       │
   │     attaches *authn.Principal{UserID, IsAdmin, LibraryIDs}     │
   │     to ctx via WithPrincipal()                                 │
   │     │                                                          │
   │     ▼                                                          │
   │  Handler (Epic 7) calls authz.Can(ctx, action, resID)          │
   │     │                                                          │
   │     ▼                                                          │
   │  ┌───────────────────────────────────────────────────────┐    │
   │  │ RoleBasedAuthz                                        │    │
   │  │   1. extract principal from ctx                       │    │
   │  │   2. switch on action namespace:                      │    │
   │  │       library.*  → admin-only                         │    │
   │  │       *.write    → admin OR owner-of-resource         │    │
   │  │       *.read     → admin OR resource.library_id ∈ ACL │    │
   │  │   3. ACL membership: principal.LibraryIDs (preloaded) │    │
   │  │   4. miss → return ErrForbidden                       │    │
   │  └───────────────────────────────────────────────────────┘    │
   │     │                                                          │
   │     │ ok                          │ ErrForbidden               │
   │     ▼                              ▼                           │
   │  handler proceeds         middleware.WriteForbidden            │
   │                           writes problem+json type=forbidden   │
   └────────────────────────────────────────────────────────────────┘

   Token / signed URL minting (Stories 10.3, 10.8):
   ┌────────────────────────────────────────────────────────────────┐
   │ authz.LibraryIDsForUser(ctx, userID) →                         │
   │     SELECT library_id FROM library_acl WHERE user_id = $1      │
   │     UNION                                                      │
   │     SELECT id FROM libraries (when is_admin)                   │
   │ result baked into JWT 'lib' claim → Streaming reads offline    │
   └────────────────────────────────────────────────────────────────┘
```

---

## 2. Detailed implementation

### 2.1 Package layout

```
api/
├── internal/
│   └── auth/
│       └── authz/
│           ├── authz.go            // Authz interface + ErrForbidden + Action consts
│           ├── role_based.go       // RoleBasedAuthz implementation
│           ├── resolver.go         // LibraryIDsForUser, video→library_id lookup
│           ├── principal.go        // ctx helpers
│           └── authz_test.go
└── shared/db/migrations/
    └── 0042_library_acl.sql
```

### 2.2 SQL migration

```sql
-- shared/db/migrations/0042_library_acl.sql
BEGIN;

CREATE TABLE library_acl (
    user_id     UUID NOT NULL REFERENCES users(id)     ON DELETE CASCADE,
    library_id  UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    can_write   BOOLEAN NOT NULL DEFAULT false,
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    granted_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    PRIMARY KEY (user_id, library_id)
);

-- Lookup at token-mint time: "what libs does user X see?"
CREATE INDEX library_acl_user ON library_acl (user_id);
-- Reverse lookup for admin UI: "who has access to this library?"
CREATE INDEX library_acl_library ON library_acl (library_id);

COMMIT;
```

### 2.3 `authz.go` — interface + constants

```go
// Package authz implements Maktaba's permission model.
// v1 = admin + per-library ACL. The Authz interface keeps handlers
// indifferent to which version is wired in.
package authz

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// Action is a dotted namespace.verb string. Keep the set documented;
// new actions go in this file so reviewers see the policy surface grow.
type Action string

const (
	ActionVideoRead        Action = "video.read"
	ActionVideoWrite       Action = "video.write"
	ActionLibraryRead      Action = "library.read"
	ActionLibraryWrite     Action = "library.write"
	ActionLibraryACLWrite  Action = "library.acl.write"
	ActionUserWrite        Action = "user.write"
	ActionPlaybackRead     Action = "playback.read"
	ActionPlaybackWrite    Action = "playback.write"
	ActionSavedSearchRead  Action = "saved_search.read"
	ActionSavedSearchWrite Action = "saved_search.write"
	ActionAuditRead        Action = "audit.read"
)

// ErrForbidden is the only error Authz returns. Handlers map it to a
// 403 problem+json with type=forbidden (D4: never leak existence).
var ErrForbidden = errors.New("forbidden")

// Authz is the v1 + v2-ready permission interface. Implementations MUST
// be safe for concurrent use and MUST NOT block on I/O in the hot path
// (the principal carries a snapshot of the user's library IDs).
type Authz interface {
	Can(ctx context.Context, action Action, resourceID uuid.UUID) error
}
```

### 2.4 `principal.go` — context plumbing

```go
package authz

import (
	"context"

	"github.com/google/uuid"
)

// Principal is the per-request snapshot the auth middleware attaches.
// It is populated from the JWT (native) or the web_session row (cookie).
type Principal struct {
	UserID     uuid.UUID
	IsAdmin    bool
	LibraryIDs []uuid.UUID // snapshot at token-issue time (D5)
}

type ctxKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

func PrincipalFromCtx(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(*Principal)
	return p, ok && p != nil
}

func (p *Principal) HasLibrary(id uuid.UUID) bool {
	for _, l := range p.LibraryIDs {
		if l == id {
			return true
		}
	}
	return false
}
```

### 2.5 `role_based.go` — v1 policy

```go
package authz

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RoleBasedAuthz implements Authz with admin + per-library ACL.
// Construction takes a pool because *.write actions on user-scoped
// resources (playback, saved_search) require an owner lookup.
type RoleBasedAuthz struct {
	pool *pgxpool.Pool
	res  *Resolver
}

func NewRoleBasedAuthz(pool *pgxpool.Pool) *RoleBasedAuthz {
	return &RoleBasedAuthz{pool: pool, res: NewResolver(pool)}
}

func (a *RoleBasedAuthz) Can(ctx context.Context, action Action, resourceID uuid.UUID) error {
	p, ok := PrincipalFromCtx(ctx)
	if !ok {
		return ErrForbidden
	}
	if p.IsAdmin {
		return nil // admin shortcut covers every action
	}

	ns, verb := splitAction(action)
	switch ns {
	case "library":
		// All library.* actions are admin-only in v1 (D3 keeps roles flat).
		return ErrForbidden

	case "user":
		// user.write is admin-only (covered by IsAdmin shortcut above).
		return ErrForbidden

	case "audit":
		return ErrForbidden // 10.16 admin-only audit feed

	case "video", "library_video":
		libID, err := a.res.VideoLibrary(ctx, resourceID)
		if err != nil {
			return ErrForbidden // collapses "not found" + "denied" (D4)
		}
		if !p.HasLibrary(libID) {
			return ErrForbidden
		}
		if verb == "write" {
			// v1: writes on videos are admin-only.
			return ErrForbidden
		}
		return nil

	case "playback":
		// Per-user resource: ownership is enforced in SQL (D6); Authz
		// just verifies the user can see the underlying video.
		libID, err := a.res.VideoLibrary(ctx, resourceID)
		if err != nil || !p.HasLibrary(libID) {
			return ErrForbidden
		}
		return nil

	case "saved_search":
		// Per-user resource: ownership enforced in SQL (D6).
		owner, err := a.res.SavedSearchOwner(ctx, resourceID)
		if err != nil {
			return ErrForbidden
		}
		if owner != p.UserID {
			return ErrForbidden
		}
		return nil

	default:
		return ErrForbidden // closed by default
	}
}

func splitAction(a Action) (ns, verb string) {
	s := string(a)
	i := strings.IndexByte(s, '.')
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+1:]
}
```

### 2.6 `resolver.go` — DB lookups + JWT claim resolver

```go
package authz

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Resolver struct{ pool *pgxpool.Pool }

func NewResolver(p *pgxpool.Pool) *Resolver { return &Resolver{pool: p} }

// LibraryIDsForUser returns the snapshot for the JWT 'lib' claim and the
// signed-URL 'lib' field. Admins → all libraries.
func (r *Resolver) LibraryIDsForUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	const q = `
		SELECT library_id FROM library_acl WHERE user_id = $1
		UNION
		SELECT id FROM libraries
		 WHERE EXISTS (SELECT 1 FROM users WHERE id = $1 AND is_admin)
		ORDER BY 1
	`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *Resolver) VideoLibrary(ctx context.Context, videoID uuid.UUID) (uuid.UUID, error) {
	var libID uuid.UUID
	err := r.pool.QueryRow(ctx,
		`SELECT library_id FROM videos WHERE id = $1`, videoID).Scan(&libID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrForbidden
	}
	return libID, err
}

func (r *Resolver) SavedSearchOwner(ctx context.Context, ssID uuid.UUID) (uuid.UUID, error) {
	var u uuid.UUID
	err := r.pool.QueryRow(ctx,
		`SELECT user_id FROM saved_searches WHERE id = $1`, ssID).Scan(&u)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrForbidden
	}
	return u, err
}
```

### 2.7 Forbidden response helper

```go
// internal/httpx/forbidden.go
package httpx

import (
	"encoding/json"
	"net/http"
)

func WriteForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "forbidden",
		"title":  "Forbidden",
		"status": http.StatusForbidden,
	})
}
```

### 2.8 Handler usage example

```go
// Epic 7 video detail handler (illustrative)
func (h *VideoHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, _ := uuid.Parse(chi.URLParam(r, "id"))
	if err := h.authz.Can(r.Context(), authz.ActionVideoRead, id); err != nil {
		httpx.WriteForbidden(w)
		return
	}
	// Per-user filter on playback_state happens in SQL (D6):
	// WHERE video_id = $1 AND user_id = $2
	//   $2 = principal.UserID
	...
}
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/0042_library_acl.sql` | `library_acl` table + indexes | `test_migration` |
| 2 | `api/internal/auth/authz/authz.go` | `Action` consts, `Authz` iface, `ErrForbidden` | (n/a) |
| 3 | `api/internal/auth/authz/principal.go` | `Principal`, `WithPrincipal`, `PrincipalFromCtx` | unit |
| 4 | `api/internal/auth/authz/resolver.go` | `Resolver.LibraryIDsForUser`, `.VideoLibrary`, `.SavedSearchOwner` | integration |
| 5 | `api/internal/auth/authz/role_based.go` | `RoleBasedAuthz`, `Can` | integration (covers all ACs) |
| 6 | `api/internal/httpx/forbidden.go` | `WriteForbidden` | unit |
| 7 | `api/internal/auth/authz/authz_test.go` | full test set | — |

---

## 4. Test cases (keyed to ACs)

### AC-1 — Resource scope checks
- `test_video_read_admin_allowed` — admin bypass.
- `test_video_read_user_with_lib_in_acl_allowed` — non-admin with row in `library_acl` for the video's library → ok.
- `test_video_read_user_without_lib_denied` — same user, missing ACL row → ErrForbidden.
- `test_video_write_non_admin_denied` — non-admin, even with `can_write=true`, fails until v1.1 (v1 = admin-only writes).
- `test_library_create_non_admin_denied` — `library.write` returns ErrForbidden for non-admin.
- `test_unknown_action_denied` — `authz.Can(ctx, "fake.thing", id)` → ErrForbidden (closed-by-default).

### AC-2 — Per-user playback_state
- Integration: user A's GET `/api/videos/{id}` returns only A's playback row (SQL filter; not Authz). Verifies B's row is absent.
- `test_playback_read_user_with_lib_allowed` — Authz layer.
- `test_playback_read_user_without_lib_denied`.

### AC-3 — Saved searches per-user
- `test_saved_search_read_owner_allowed`.
- `test_saved_search_read_other_user_denied`.
- `test_saved_search_read_admin_allowed` — admin bypass even for someone else's saved search (matches Story 10.13's admin-bypass rule).

### AC-4 — Generic 403
- HTTP integration: GET `/api/videos/<nonexistent-uuid>` for a non-admin → 403 problem+json `type=forbidden`. Body matches the same shape as a real-but-denied access; no `404` leak.
- HTTP integration: GET `/api/videos/<real-but-denied>` → byte-identical 403 body to the previous case.

### AC-5 — `lib[]` snapshot in JWT
- `test_resolver_admin_returns_all_libraries`.
- `test_resolver_non_admin_returns_acl_subset`.
- Integration with Plan 10.8: minted signed URL contains `lib=[<acl-row-libs>]` exactly; Streaming-side rejects when video.library_id ∉ lib (covered in Plan 10.7's tests).
- Integration: a user with no ACL rows minting a signed URL gets `lib=[]`; Streaming rejects every URL.

---

## 5. Edge cases

| #   | Case | Handled by |
|-----|------|------------|
| E1  | User downgraded admin → viewer mid-session. JWT still carries `is_admin: true` for ≤ 15 min. | Documented in story; `logout-all` + key rotation kills early (Story 10.5 AC-3, 10.6 AC-5). |
| E2  | User added to a new library mid-session. Existing JWT's `lib[]` is stale until next refresh. | Same: 15-min window is the documented trade. |
| E3  | Race on resolver: ACL row deleted between token mint and request. The signed URL is still valid until expiry; the API call goes through Authz which re-reads the principal but the principal was loaded from the JWT, so the deleted ACL doesn't affect this request. | Documented; key rotation is the kill switch. |
| E4  | Single-user mode (Story 10.9). The synthetic admin principal has `IsAdmin: true` and `LibraryIDs = []` (no enumeration); the `IsAdmin` shortcut in `Can()` covers all actions. | Falls out of the design. |
| E5  | A handler forgets to call `Can()`. Defence-in-depth: every Epic 7 handler is required to call Authz; a CI lint (golang-ci-lint custom rule + grep on `chi.HandleFunc`) flags handlers that don't import `authz`. | Test in CI: `test_handlers_call_authz_static`. |
| E6  | Video's `library_id` is NULL (legacy data). `Resolver.VideoLibrary` returns `ErrForbidden` (the row's `library_id` is non-null in the schema, but if a migration ever loosens this we close-by-default). | DB constraint + Resolver fallback. |
| E7  | Saved-search lookup race: the row is deleted between `Can()` and the SQL fetch. `Can()` returns ok (row existed, owner matched); the subsequent fetch returns `pgx.ErrNoRows` → handler returns 404. No info leak (the user owned it). | Handler-level. |
| E8  | A non-admin requests `/api/security/audit` (Story 10.16). `Can(ctx, "audit.read", uuid.Nil)` → ErrForbidden → 403. | Covered by `audit` namespace in `Can()`. |

---

## 6. Acceptance checklist

- [ ] **A1** Migration `0042_library_acl.sql` creates the table with PK `(user_id, library_id)`, FK cascades to `users.id` and `libraries.id`, and indexes on `(user_id)` and `(library_id)`.
- [ ] **A2** `authz.Authz` interface defined with one method `Can(ctx, Action, uuid.UUID) error`.
- [ ] **A3** `RoleBasedAuthz` implements the AC-1 default policy: admins always pass; `library.*` and `user.*` are admin-only; `*.read` checks `library_acl` membership; `*.write` is admin-only; per-user resources check ownership.
- [ ] **A4** `Resolver.LibraryIDsForUser` returns the user's ACL libraries OR all libraries when admin; consumed at JWT-mint time and at signed-URL mint time (Story 10.8).
- [ ] **A5** `httpx.WriteForbidden` returns problem+json `type=forbidden, title=Forbidden, status=403` with no body details. `404` and `403` are byte-identical to the client.
- [ ] **A6** `Principal` flows through context; handlers retrieve via `PrincipalFromCtx`; per-user SQL filters use `principal.UserID` for `playback_state` and `saved_searches`.
- [ ] **A7** Integration: a `lib`-less JWT cannot stream any video (Plan 10.7 enforces this; Plan 10.13 ships the resolver that produces the empty `lib[]`).
- [ ] **A8** All Epic 7 handlers that touch a video, library, playback, or saved-search call `authz.Can(...)`; CI lint flags missing calls.
- [ ] **A9** Closed-by-default: an unknown action string returns `ErrForbidden` rather than passing through.

---

## 7. Forward compatibility notes

- v2 fine-grained roles: add a `roles` table and a `role_actions` table; ship a `MultiRoleAuthz` that consults both `library_acl` and the role tables; switch wiring in `cmd/api/main.go` from `NewRoleBasedAuthz` to `NewMultiRoleAuthz`. Handlers do not change.
- v2 multi-tenancy: the `Action` namespace allows tenant-scoped actions (`tenant.video.read`); `Principal` gains a `TenantID`; Resolver gains a tenant filter on every query.
- The `lib[]` snapshot semantics carry over unchanged.
