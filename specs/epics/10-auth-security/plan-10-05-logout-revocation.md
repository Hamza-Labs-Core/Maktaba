# Plan 10.5 — Logout + session revocation — implementation

> Implementation plan for [story-10-05-logout-revocation.md](story-10-05-logout-revocation.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: depends on web sessions from
> [Plan 10.2](plan-10-02-web-login.md) and refresh tokens / families
> from [Plan 10.3](plan-10-03-native-login.md) and
> [Plan 10.4](plan-10-04-token-refresh.md). Streaming admin close-all
> uses the gRPC `CloseSession` from
> [Epic 8 Plan 8.2](../08-streaming/plan-08-02-streaming-session.md).
> The 15-minute revocation lag for native access tokens is bound by
> Story 10.3 AC-2 — the immediate-kill escape hatch is the JWKS
> rotation in [Plan 10.6](plan-10-06-rs256-keys-jwks.md).

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Soft revocation, never DELETE.** Web sessions and refresh tokens get `revoked_at = now()` rather than being hard-deleted. The reaper (Story 10.2/10.3) sweeps rows past `expires_at + 30d`. | Story AC-1: "kept for audit." | Audit trail (Story 10.16) requires the row to stay around so we can answer "when was this session revoked, and from which IP?". Hard-delete loses that. |
| D2 | **Native `logout` accepts the refresh in the JSON body, not the access token's `jti`.** The access token isn't revocable (stateless RS256 + 15-min TTL), so revoking by access-token claims is a no-op anyway; revoking by refresh hash is the only thing that matters. | Story AC-2 narrative + §9.8. | Treats the two surfaces uniformly: cookie surface revokes the cookie's row, body surface revokes the row matched by the refresh hash. The access-token TTL window is documented, not engineered around. |
| D3 | **`logout-all` revokes web sessions AND every refresh family in one transaction**, with a single audit row whose payload lists the affected counts. Optional `streaming.close-all=true` body field also fans out to Streaming. | Story AC-3 + §9.8. | Single transaction guarantees we don't half-revoke (web cleared but refresh families alive); operators can correlate the audit row to a single user-initiated action. |
| D4 | **Streaming close-all is a gRPC fan-out from API, not a Streaming-side iteration.** The API selects active rows from `streaming_sessions` for the user and calls `streaming.CloseSession` once per row with `reason='admin-revoke'`. | Story AC-4 + Epic 8 boundary. | Streaming should not need to know about API tables (users, sessions). Keeping the iteration on the API side preserves "production owns the data" — the API is the system of record for which sessions belong to whom. |
| D5 | **Idempotent UPDATE: `WHERE revoked_at IS NULL`.** Re-issuing logout is a 204; we don't 404 on already-revoked rows. | Story AC-1, AC-2 implicit. | Mobile clients retry on flaky networks. A second logout for the same refresh shouldn't error. |
| D6 | **Admin endpoints under `/api/users/{id}/...` require `is_admin=true` in the actor's JWT/session and CSRF for the cookie surface.** Non-admin actors get 403. | Story AC-4 + Epic 10.13 admin gate. | Protects against a compromised viewer account from revoking the admin's session. The admin gate is the same middleware Story 10.13 ships. |
| D7 | **The 15-min access-token lag is a documented, surfaceable fact**, not a bug. The handler's response body for `logout-all` includes `access_token_lag_sec: 900` and the operator-only escape `key.rotate.immediate` reference. | Story AC-3 narrative. | Surfacing the lag in the API response makes it visible to clients (so they can warn users "still revoking other devices for up to 15 minutes") and operators (so they know the immediate-kill knob exists). |

---

## 1. Architecture diagram — logout fan-out

```
   Web client                           Native client                Admin
       │                                       │                        │
   POST /api/auth/logout                  POST /api/auth/logout    DELETE /api/users/{id}/sessions/{sid}
   (cookie + CSRF)                        {refresh_token}          (admin token + CSRF)
       │                                       │                        │
       ▼                                       ▼                        ▼
   ┌────────────────────────────────────────────────────────────────────────────┐
   │ api/internal/auth/revoke (Go, chi router, pgx/v5)                          │
   │                                                                            │
   │  RevokeWebSession(ctx, sid)        RevokeRefreshByHash(ctx, hash)          │
   │     UPDATE web_sessions               argon2id(token) → hash               │
   │       SET revoked_at = now()          UPDATE refresh_tokens                │
   │       WHERE id = $1                     SET revoked_at = now()             │
   │       AND revoked_at IS NULL            WHERE hash = $1                    │
   │                                         AND revoked_at IS NULL             │
   │                                                                            │
   │  RevokeAllForUser(ctx, uid)                                                │
   │     BEGIN                                                                  │
   │       UPDATE web_sessions SET revoked_at=now() WHERE user_id=$1 AND ...    │
   │       UPDATE refresh_tokens SET revoked_at=now() WHERE user_id=$1 AND ...  │
   │       INSERT INTO audit_log (... event='logout-all' ...)                   │
   │     COMMIT                                                                 │
   └─────────────────────────────────────┬──────────────────────────────────────┘
                                         │
                                         │ if streaming.close-all=true OR admin endpoint
                                         ▼
                                ┌──────────────────────────────────┐
                                │ StreamingClient.CloseAllForUser  │
                                │   SELECT id FROM streaming_sess… │
                                │     WHERE user_id=$1 AND closed… │
                                │   for each: gRPC CloseSession    │
                                │     reason='admin-revoke'        │
                                └──────────────────────────────────┘
```

Stateless access tokens remain valid until `exp` (15 min default). The
response body documents the lag; the operator may run
`maktaba-api keys rotate --immediate` (Plan 10.6) for instant kill.

---

## 2. Detailed implementation

### 2.1 Package layout — Go API

```
api/
├── internal/
│   ├── auth/
│   │   ├── revoke/
│   │   │   ├── revoke.go            # Service: RevokeWebSession, RevokeRefreshByHash, RevokeAllForUser
│   │   │   ├── streaming_client.go  # gRPC client wrapper to Streaming
│   │   │   └── revoke_test.go
│   │   └── handlers/
│   │       ├── logout.go            # POST /api/auth/logout (web + native)
│   │       ├── logout_all.go        # POST /api/auth/logout-all
│   │       ├── admin_revoke.go      # admin endpoints
│   │       └── handlers_test.go
│   └── audit/
│       └── audit.go                 # WriteSecurityEvent helper (existing — Story 10.16)
└── cmd/
    └── maktaba-api/
        └── routes.go                # mount points
```

### 2.2 SQL — no migration needed (revocation is UPDATE on existing tables)

The columns `web_sessions.revoked_at` and `refresh_tokens.revoked_at`
already exist (Stories 10.2, 10.3). All the work here is service code
plus sqlc queries.

```sql
-- shared/db/queries/revoke.sql

-- name: RevokeWebSession :execrows
UPDATE web_sessions
   SET revoked_at = now()
 WHERE id = $1
   AND revoked_at IS NULL;

-- name: RevokeRefreshByHash :execrows
UPDATE refresh_tokens
   SET revoked_at = now()
 WHERE hash = $1
   AND revoked_at IS NULL;

-- name: RevokeRefreshFamily :execrows
UPDATE refresh_tokens
   SET revoked_at = now()
 WHERE family_id = $1
   AND revoked_at IS NULL;

-- name: RevokeAllWebSessionsForUser :execrows
UPDATE web_sessions
   SET revoked_at = now()
 WHERE user_id = $1
   AND revoked_at IS NULL;

-- name: RevokeAllRefreshForUser :execrows
UPDATE refresh_tokens
   SET revoked_at = now()
 WHERE user_id = $1
   AND revoked_at IS NULL;

-- name: ActiveStreamingSessionsForUser :many
SELECT id
  FROM streaming_sessions
 WHERE user_id = $1
   AND closed_at IS NULL;
```

### 2.3 `revoke.go` — service

```go
// api/internal/auth/revoke/revoke.go
package revoke

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"maktaba/api/internal/audit"
	"maktaba/api/internal/auth/refresh" // exposes HashRefreshToken
	q "maktaba/api/internal/db/sqlc"
)

var (
	ErrNotFound       = errors.New("revoke: row not found or already revoked")
	ErrStreamingFanOut = errors.New("revoke: streaming close-all partial failure")
)

type Service struct {
	pool      *pgxpool.Pool
	queries   *q.Queries
	audit     *audit.Logger
	streaming StreamingCloser // interface, see streaming_client.go
}

type StreamingCloser interface {
	CloseAllForUser(ctx context.Context, userID uuid.UUID, reason string) (closed int, err error)
}

func NewService(pool *pgxpool.Pool, audit *audit.Logger, sc StreamingCloser) *Service {
	return &Service{pool: pool, queries: q.New(pool), audit: audit, streaming: sc}
}

// RevokeWebSession is idempotent: a second call returns ErrNotFound but
// the handler maps that to 204 (D5).
func (s *Service) RevokeWebSession(ctx context.Context, sid uuid.UUID, actorID uuid.UUID) error {
	rows, err := s.queries.RevokeWebSession(ctx, sid)
	if err != nil {
		return fmt.Errorf("revoke web session: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	s.audit.Security(ctx, audit.Event{
		Event:   "logout.web",
		ActorID: actorID,
		Payload: map[string]any{"session_id": sid},
	})
	return nil
}

// RevokeRefreshByPlaintext hashes and revokes. Returns ErrNotFound if
// no live row matches.
func (s *Service) RevokeRefreshByPlaintext(ctx context.Context, plaintext string, actorID uuid.UUID) error {
	hash, err := refresh.HashRefreshToken(plaintext)
	if err != nil {
		return fmt.Errorf("hash refresh: %w", err)
	}
	rows, err := s.queries.RevokeRefreshByHash(ctx, hash)
	if err != nil {
		return fmt.Errorf("revoke refresh: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	s.audit.Security(ctx, audit.Event{
		Event:   "logout.native",
		ActorID: actorID,
		Payload: map[string]any{"refresh_hash_prefix": hash[:8]},
	})
	return nil
}

type LogoutAllResult struct {
	WebSessionsRevoked   int64
	RefreshTokensRevoked int64
	StreamingClosed      int
	AccessTokenLagSec    int
}

// RevokeAllForUser is the body of POST /api/auth/logout-all and the
// admin "logout this user everywhere" path. Atomic over web+refresh;
// streaming fan-out happens after commit (it's idempotent and
// best-effort by design).
func (s *Service) RevokeAllForUser(
	ctx context.Context, userID uuid.UUID, alsoStreaming bool, actorID uuid.UUID,
) (LogoutAllResult, error) {
	res := LogoutAllResult{AccessTokenLagSec: 900}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return res, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	qx := s.queries.WithTx(tx)

	res.WebSessionsRevoked, err = qx.RevokeAllWebSessionsForUser(ctx, userID)
	if err != nil {
		return res, fmt.Errorf("revoke web for user: %w", err)
	}
	res.RefreshTokensRevoked, err = qx.RevokeAllRefreshForUser(ctx, userID)
	if err != nil {
		return res, fmt.Errorf("revoke refresh for user: %w", err)
	}
	// Audit row inside the same xact.
	if err := s.audit.SecurityTx(ctx, tx, audit.Event{
		Event:   "logout-all",
		ActorID: actorID,
		Payload: map[string]any{
			"target_user_id":         userID,
			"web_sessions_revoked":   res.WebSessionsRevoked,
			"refresh_tokens_revoked": res.RefreshTokensRevoked,
			"also_streaming":         alsoStreaming,
		},
	}); err != nil {
		return res, fmt.Errorf("audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return res, fmt.Errorf("commit: %w", err)
	}
	if alsoStreaming && s.streaming != nil {
		closed, err := s.streaming.CloseAllForUser(ctx, userID, "admin-revoke")
		res.StreamingClosed = closed
		if err != nil {
			// Don't fail the whole request — DB state is committed.
			return res, fmt.Errorf("%w: %v", ErrStreamingFanOut, err)
		}
	}
	return res, nil
}
```

### 2.4 `streaming_client.go` — gRPC fan-out (D4)

```go
// api/internal/auth/revoke/streaming_client.go
package revoke

import (
	"context"

	"github.com/google/uuid"

	q "maktaba/api/internal/db/sqlc"
	pb "maktaba/shared/proto/streaming/v1"
)

type StreamingGRPCClient struct {
	queries *q.Queries
	rpc     pb.StreamingServiceClient
}

func NewStreamingGRPCClient(queries *q.Queries, rpc pb.StreamingServiceClient) *StreamingGRPCClient {
	return &StreamingGRPCClient{queries: queries, rpc: rpc}
}

func (c *StreamingGRPCClient) CloseAllForUser(ctx context.Context, userID uuid.UUID, reason string) (int, error) {
	rows, err := c.queries.ActiveStreamingSessionsForUser(ctx, userID)
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, sid := range rows {
		_, err := c.rpc.CloseSession(ctx, &pb.CloseSessionRequest{
			SessionId: sid.String(),
			Reason:    reason,
		})
		if err != nil {
			// Log and continue — partial fan-out is acceptable; the
			// session reaper (Plan 8.2) catches stragglers.
			continue
		}
		closed++
	}
	return closed, nil
}
```

### 2.5 HTTP handlers — `handlers/logout.go` and `logout_all.go`

```go
// api/internal/auth/handlers/logout.go
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"maktaba/api/internal/auth/revoke"
	"maktaba/api/internal/httpx"
)

type LogoutHandler struct {
	svc *revoke.Service
}

type logoutBody struct {
	RefreshToken string `json:"refresh_token,omitempty"`
}

// POST /api/auth/logout
//   - cookie surface: revokes the session whose id is in mkt_sess.
//   - native surface: revokes the row matching argon2id(refresh_token).
func (h *LogoutHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if sid := httpx.WebSessionIDFromCtx(r.Context()); sid != uuid.Nil {
		err := h.svc.RevokeWebSession(r.Context(), sid, sid)
		if err != nil && !errors.Is(err, revoke.ErrNotFound) {
			httpx.WriteError(w, http.StatusInternalServerError, "internal", err)
			return
		}
		httpx.ClearCookie(w, "mkt_sess")
		httpx.ClearCookie(w, "mkt_csrf")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Native path.
	var body logoutBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RefreshToken == "" {
		httpx.WriteError(w, http.StatusBadRequest, "bad-request", errors.New("missing refresh_token"))
		return
	}
	actor := httpx.UserIDFromCtx(r.Context())
	err := h.svc.RevokeRefreshByPlaintext(r.Context(), body.RefreshToken, actor)
	if err != nil && !errors.Is(err, revoke.ErrNotFound) {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/auth/logout-all
type LogoutAllHandler struct{ svc *revoke.Service }

type logoutAllBody struct {
	AlsoStreaming bool `json:"also_streaming,omitempty"`
}

func (h *LogoutAllHandler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	uid := httpx.UserIDFromCtx(r.Context())
	if uid == uuid.Nil {
		httpx.WriteError(w, http.StatusUnauthorized, "missing", nil)
		return
	}
	var body logoutAllBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	res, err := h.svc.RevokeAllForUser(r.Context(), uid, body.AlsoStreaming, uid)
	if err != nil && !errors.Is(err, revoke.ErrStreamingFanOut) {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", err)
		return
	}
	httpx.ClearCookie(w, "mkt_sess")
	httpx.ClearCookie(w, "mkt_csrf")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(res)
}

// admin_revoke.go
type AdminRevokeHandler struct{ svc *revoke.Service }

// DELETE /api/users/{id}/sessions/{sid}
func (h *AdminRevokeHandler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	sid, err := uuid.Parse(chi.URLParam(r, "sid"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad-id", err)
		return
	}
	actor := httpx.UserIDFromCtx(r.Context())
	if err := h.svc.RevokeWebSession(r.Context(), sid, actor); err != nil &&
		!errors.Is(err, revoke.ErrNotFound) {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/users/{id}/refresh-tokens/{family_id}
func (h *AdminRevokeHandler) RevokeFamily(w http.ResponseWriter, r *http.Request) {
	fid, err := uuid.Parse(chi.URLParam(r, "family_id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad-id", err)
		return
	}
	if _, err := h.svc.RevokeFamily(r.Context(), fid, httpx.UserIDFromCtx(r.Context())); err != nil &&
		!errors.Is(err, revoke.ErrNotFound) {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/users/{id}/streaming/close-all
func (h *AdminRevokeHandler) StreamingCloseAll(w http.ResponseWriter, r *http.Request) {
	uid, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "bad-id", err)
		return
	}
	closed, err := h.svc.StreamingCloseAll(r.Context(), uid, httpx.UserIDFromCtx(r.Context()))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal", err)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]int{"closed": closed})
}
```

### 2.6 Route mounting

```go
// api/cmd/maktaba-api/routes.go (excerpt)
r.Route("/api/auth", func(r chi.Router) {
	r.With(mwSessionOrJWT).Post("/logout", logout.Logout)
	r.With(mwSessionOrJWT).Post("/logout-all", logoutAll.LogoutAll)
})
r.Route("/api/users/{id}", func(r chi.Router) {
	r.Use(mwAdminOnly) // Story 10.13
	r.Delete("/sessions/{sid}", adminRevoke.RevokeSession)
	r.Delete("/refresh-tokens/{family_id}", adminRevoke.RevokeFamily)
	r.Post("/streaming/close-all", adminRevoke.StreamingCloseAll)
})
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/queries/revoke.sql` | RevokeWebSession, RevokeRefreshByHash, RevokeRefreshFamily, RevokeAllWebSessionsForUser, RevokeAllRefreshForUser, ActiveStreamingSessionsForUser | sqlc generation passes |
| 2 | `api/internal/auth/revoke/revoke.go` | `Service`, `LogoutAllResult`, `ErrNotFound`, `ErrStreamingFanOut` | `revoke_test.go` |
| 3 | `api/internal/auth/revoke/streaming_client.go` | `StreamingGRPCClient`, `StreamingCloser` interface | mock-based unit |
| 4 | `api/internal/auth/handlers/logout.go` | `LogoutHandler.Logout` | `handlers_test.go` |
| 5 | `api/internal/auth/handlers/logout_all.go` | `LogoutAllHandler.LogoutAll` | `handlers_test.go` |
| 6 | `api/internal/auth/handlers/admin_revoke.go` | `AdminRevokeHandler.{RevokeSession,RevokeFamily,StreamingCloseAll}` | `handlers_test.go` |
| 7 | `api/cmd/maktaba-api/routes.go` (extend) | route mounts, admin middleware wiring | integration test boots router |

---

## 4. Test cases keyed to ACs

### 4.1 `TestWebLogoutClearsCookieAndRevokesRow` (AC-1)

```go
func TestWebLogoutClearsCookieAndRevokesRow(t *testing.T) {
	srv, _ := newTestServer(t)
	cookie := loginWeb(t, srv, "alice", "pw")
	// Hit logout
	resp := srv.Do(t, "POST", "/api/auth/logout", nil, cookie)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	// mkt_sess cleared
	require.Contains(t, resp.Header.Get("Set-Cookie"), "mkt_sess=;")
	// Next request with the same cookie is 401
	resp2 := srv.Do(t, "GET", "/api/me", nil, cookie)
	require.Equal(t, http.StatusUnauthorized, resp2.StatusCode)
	// DB row has revoked_at non-null
	row := dbRow(t, "SELECT revoked_at FROM web_sessions WHERE id=$1", sidFrom(cookie))
	require.NotNil(t, row.RevokedAt)
}
```

### 4.2 `TestNativeLogoutRevokesRefreshButAccessSurvivesUntilExp` (AC-2)

```go
func TestNativeLogoutRevokesRefresh(t *testing.T) {
	srv, _ := newTestServer(t)
	pair := loginNative(t, srv, "alice", "pw")
	// Logout via body
	resp := srv.Do(t, "POST", "/api/auth/logout",
		map[string]string{"refresh_token": pair.Refresh}, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	// Subsequent refresh fails
	resp2 := postRefresh(t, srv, pair.Refresh)
	require.Equal(t, http.StatusUnauthorized, resp2.StatusCode)
	// But the access token is still valid until exp (documented)
	resp3 := srv.Do(t, "GET", "/api/me", nil,
		bearer(pair.Access))
	require.Equal(t, http.StatusOK, resp3.StatusCode)
}
```

### 4.3 `TestLogoutAllRevokesEverywhere` (AC-3)

```go
func TestLogoutAllFromDeviceADisablesDeviceB(t *testing.T) {
	srv, _ := newTestServer(t)
	a := loginNative(t, srv, "alice", "pw")
	b := loginNative(t, srv, "alice", "pw") // separate device, separate family
	resp := srv.Do(t, "POST", "/api/auth/logout-all", nil, bearer(a.Access))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	// Body advertises the lag.
	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.EqualValues(t, 900, body["AccessTokenLagSec"])
	// B's next refresh fails.
	require.Equal(t, http.StatusUnauthorized, postRefresh(t, srv, b.Refresh).StatusCode)
	// audit_log row written.
	require.Equal(t, "logout-all",
		dbRow(t, "SELECT event FROM audit_log WHERE category='security' ORDER BY at DESC LIMIT 1").Event)
}
```

### 4.4 `TestAdminRevokeKicksTheUser` (AC-4)

```go
func TestAdminRevokeUserSessionLogsThemOut(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := loginNative(t, srv, "admin", "pw")
	cookie := loginWeb(t, srv, "viewer", "pw")
	sid := sidFrom(cookie)
	resp := srv.Do(t, "DELETE",
		fmt.Sprintf("/api/users/%s/sessions/%s", viewerID, sid), nil, bearer(admin.Access))
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	// viewer is logged out
	require.Equal(t, http.StatusUnauthorized,
		srv.Do(t, "GET", "/api/me", nil, cookie).StatusCode)
}
```

### 4.5 `TestAdminStreamingCloseAllSetsAdminRevoke` (AC-4 fan-out)

```go
func TestAdminStreamingCloseAll(t *testing.T) {
	srv, fakeStreaming := newTestServer(t)
	// Seed two active streaming_sessions for victim
	seedStreamingSession(t, victimID)
	seedStreamingSession(t, victimID)
	resp := srv.Do(t, "POST",
		fmt.Sprintf("/api/users/%s/streaming/close-all", victimID), nil, bearer(adminAccess))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 2, fakeStreaming.CloseSessionCalls("admin-revoke"))
}
```

### 4.6 `TestLogoutIsIdempotent` (D5)

```go
func TestDoubleLogoutIs204Both(t *testing.T) {
	srv, _ := newTestServer(t)
	cookie := loginWeb(t, srv, "alice", "pw")
	r1 := srv.Do(t, "POST", "/api/auth/logout", nil, cookie)
	r2 := srv.Do(t, "POST", "/api/auth/logout", nil, cookie)
	require.Equal(t, http.StatusNoContent, r1.StatusCode)
	require.Equal(t, http.StatusNoContent, r2.StatusCode)
}
```

---

## 5. Edge cases

| #  | Edge case | Handled by |
|----|-----------|------------|
| E1 | **Logout with no network** — server cannot proactively expire anything until the client returns and POSTs. Story narrative; no code change. The 15-min access-token lag is the documented worst case. | Documented in handler godoc + response body lag field. |
| E2 | **Refresh-token in body doesn't match any row** — possible after rotation reuse-detection nuked the family, or the user pasted garbage. Returns 204 anyway (D5) — leaking nonexistence is itself a leak. | Handler: `errors.Is(err, ErrNotFound)` → 204. |
| E3 | **Streaming gRPC partially fails** — e.g., one of three sessions is on a Streaming replica that's down. DB is committed; we report partial close count and log. The session reaper (Plan 8.2) cleans the rest. | `RevokeAllForUser` returns `ErrStreamingFanOut`; handler still 200s and reports `streaming_closed`. |
| E4 | **logout-all called by a non-admin against another user** — middleware (Story 10.13) returns 403 before the handler runs. | Route mount: `r.Use(mwAdminOnly)`. |
| E5 | **Race: logout-all and a refresh rotation happen at the same instant** — the rotation uses `SELECT ... FOR UPDATE` (Plan 10.4); whichever transaction acquires the row first wins. The other sees `revoked_at IS NOT NULL` and 401s. | Existing rotation locking. |
| E6 | **CSRF mismatch on web logout** — middleware rejects with 403 before the handler runs; the cookie remains valid. Documented; user retries with the right `X-CSRF-Token`. | CSRF middleware (Story 10.10). |
| E7 | **Same JWT used for `logout` after the refresh family was nuked** — the access token still works until `exp`; calling `logout` is a no-op (D5). | Behavioural: 204 idempotent. |
| E8 | **Admin attempts to revoke their own only-admin session via the user-facing logout-all** — they are temporarily locked out for up to 15 min (access TTL). Documented; they can re-login. | None — this is the intended consequence. |
| E9 | **Streaming.CloseSession returns "session already closed"** — gRPC handler is idempotent (Plan 8.2); we count it as success. | gRPC server contract. |
| E10 | **`ActiveStreamingSessionsForUser` returns >1000 rows** — fan-out loop is sequential; bound by `auth.streaming_close_all_max=1000` and we set `closed_reason='admin-revoke'` directly via SQL fallback when over the limit. | If `len(rows) > 1000`, switch to a single `UPDATE streaming_sessions SET closed_at=now(), closed_reason='admin-revoke' WHERE user_id=$1 AND closed_at IS NULL`. |

---

## 6. Acceptance checklist

- [ ] **A1** `POST /api/auth/logout` with cookie returns 204, sets `Set-Cookie: mkt_sess=;` and `mkt_csrf=;`, and updates `web_sessions.revoked_at`. (`TestWebLogoutClearsCookieAndRevokesRow`)
- [ ] **A2** `POST /api/auth/logout` with `{refresh_token}` body revokes the matching `refresh_tokens` row by hash; the access token continues to verify until `exp`. (`TestNativeLogoutRevokesRefresh`)
- [ ] **A3** `POST /api/auth/logout-all` revokes every active web session and every refresh family for the authenticated user inside one transaction; writes one `audit_log` row with `event='logout-all'`; response advertises `AccessTokenLagSec=900`. (`TestLogoutAllFromDeviceADisablesDeviceB`)
- [ ] **A4** `DELETE /api/users/{id}/sessions/{sid}` and `DELETE /api/users/{id}/refresh-tokens/{family_id}` are admin-only and revoke the targeted row(s). (`TestAdminRevokeUserSessionLogsThemOut`)
- [ ] **A5** `POST /api/users/{id}/streaming/close-all` enumerates `streaming_sessions` and calls `streaming.CloseSession(reason='admin-revoke')` for each. (`TestAdminStreamingCloseAll`)
- [ ] **A6** Logout endpoints are idempotent: a second call returns 204, not 404. (`TestDoubleLogoutIs204Both`)
- [ ] **A7** Streaming fan-out failure does not roll back the DB revocations; the response indicates partial fan-out. (Edge E3 fixture)
- [ ] **A8** Documentation: handler godoc and the `LogoutAllResult.AccessTokenLagSec` field record the 15-min stateless-token lag and reference Plan 10.6's `--immediate` rotation as the escape hatch.
