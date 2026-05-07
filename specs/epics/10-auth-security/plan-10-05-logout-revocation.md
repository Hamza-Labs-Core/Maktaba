# Implementation Plan — Story 10.5 Logout + session revocation

> Companion to [story-10-05-logout-revocation.md](story-10-05-logout-revocation.md).
> Sessions are owned by [Story 10.2](plan-10-02-web-login.md). Refresh
> families are owned by [Story 10.3](plan-10-03-native-login.md) /
> [Story 10.4](plan-10-04-token-refresh.md). Audit-row writes go through
> [Story 10.16](story-10-16-security-audit.md). Streaming-session close
> is a gRPC into the Streaming Service per architecture §9.9.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Web logout | `POST /api/auth/logout` — clears cookie + revokes web_session row. |
| Native logout | Same handler, branched by client header. Revokes the refresh row by id. |
| Logout-all | `POST /api/auth/logout-all` — revokes all web_sessions + refresh families for the caller's user_id. |
| Admin revocation | `DELETE /api/users/{id}/sessions/{session_id}` (web) and `DELETE /api/users/{id}/refresh-tokens/{family_id}` (native). |
| Streaming close-all | `POST /api/users/{id}/streaming/close-all` — iterates `streaming_sessions` and calls Streaming gRPC `CloseSession`. |
| Out of scope | Key rotation for instant-revoke (Story 10.6 owns `keys rotate --immediate`). The 15-min lag on access tokens is documented but not closed here. |

## 1. Architecture diagram

```
POST /api/auth/logout (cookie or refresh_token in body)
   ▼
┌─────────────────────────────────────────────────────────────┐
│ http/auth_logout.go                                         │
│   - if mkt_sess cookie: store.RevokeWebSession(sid)         │
│       → ClearSessionCookies(w)                              │
│       → audit: event='logout', payload={surface:'web'}      │
│   - if body.refresh_token: refresh.RevokeByPlaintext(...)   │
│       → audit: event='logout', payload={surface:'native'}   │
│   - 204 No Content                                          │
└─────────────────────────────────────────────────────────────┘

POST /api/auth/logout-all
   ▼
┌─────────────────────────────────────────────────────────────┐
│ http/auth_logout_all.go                                     │
│   tx:                                                       │
│     n1 := sessions.RevokeAllForUser(uid)                    │
│     n2 := refresh.RevokeAllForUser(uid)  // all families     │
│   audit: event='logout-all', payload={web_n: n1, refresh_n}│
│   return 204                                                │
└─────────────────────────────────────────────────────────────┘

DELETE /api/users/{id}/sessions/{session_id}
DELETE /api/users/{id}/refresh-tokens/{family_id}
   ▼
┌─────────────────────────────────────────────────────────────┐
│ http/users_admin_revoke.go                                  │
│   - requireAdmin middleware                                 │
│   - sessions.Revoke(sid) | refresh.RevokeFamily(fam)        │
│   - audit: event='logout', payload={by_admin:true,target_uid}│
└─────────────────────────────────────────────────────────────┘

POST /api/users/{id}/streaming/close-all
   ▼
┌─────────────────────────────────────────────────────────────┐
│ http/users_admin_streaming.go                               │
│   - requireAdmin                                            │
│   - SELECT id FROM streaming_sessions WHERE user_id=$1      │
│        AND closed_at IS NULL                                │
│   - for each: streaming.CloseSession(grpc, session_id,      │
│              reason='admin-revoke')                         │
│   - audit per session: event='streaming.close-admin'        │
└─────────────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/http/auth_logout.go` | `POST /api/auth/logout` (web + native dual). |
| `api/internal/http/auth_logout_all.go` | `POST /api/auth/logout-all`. |
| `api/internal/http/users_admin_revoke.go` | Admin revocation endpoints (sessions + refresh families). |
| `api/internal/http/users_admin_streaming.go` | `POST /api/users/{id}/streaming/close-all`. |
| `api/internal/auth/refresh_revoke.go` | `RevokeByPlaintext`, `RevokeAllForUser`. |
| `api/internal/auth/sessions_revoke.go` | `RevokeAllForUser` returns count. |
| `api/internal/http/auth_logout_test.go` | Integration tests. |
| `api/internal/http/users_admin_revoke_test.go` | Admin-revocation tests. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `shared/db/queries/refresh_tokens.sql` | Add `RevokeAllRefreshForUser`, `GetActiveRefreshFamiliesForUser`. |
| `shared/db/queries/web_sessions.sql` | Already has `RevokeAllWebSessionsForUser` (Story 10.2 plan §4). |
| `api/internal/http/router.go` | Mount logout/logout-all/admin routes. |
| `api/internal/grpcclient/streaming.go` | Already exposes `CloseSession` (Epic 7); reuse. |

### 2.3 Type definitions

```go
// api/internal/auth/refresh_revoke.go
func (s *store) RevokeByPlaintext(ctx context.Context, plaintext string) error
func (s *store) RevokeAllForUser(ctx context.Context, userID uuid.UUID) (int, error)
```

`RevokeByPlaintext` parses, looks up the row by id, and revokes it
**without** triggering family revocation (logout is not theft):

```go
func (s *store) RevokeByPlaintext(ctx context.Context, plaintext string) error {
    id, _, err := ParsePlaintext(plaintext)
    if err != nil { return ErrInvalidRefreshToken }
    return s.q.RevokeRefreshTokenByID(ctx, id)
}
```

(The `RevokeRefreshTokenByID` SQL already exists in 10.4's plan, but we
add it explicitly here in case the rotation patch hadn't yet.)

### 2.4 SQL additions

`shared/db/queries/refresh_tokens.sql`:

```sql
-- name: RevokeRefreshTokenByID :exec
UPDATE refresh_tokens SET revoked_at = now()
 WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeAllRefreshForUser :execrows
UPDATE refresh_tokens SET revoked_at = now()
 WHERE user_id = $1 AND revoked_at IS NULL;
```

## 3. Handlers

### 3.1 Web/native logout (single endpoint)

```go
// api/internal/http/auth_logout.go
func logoutHandler(
    sessions auth.SessionStore, refresh auth.RefreshStore,
    audit auth.AuditSink, cfg auth.Config,
) http.HandlerFunc {
    type req struct {
        RefreshToken string `json:"refresh_token,omitempty"`
    }
    return func(w http.ResponseWriter, r *http.Request) {
        var body req
        // Body is optional for the cookie path; tolerate empty bodies.
        if r.ContentLength > 0 {
            _ = json.NewDecoder(r.Body).Decode(&body)
        }

        cookieRevoked := false
        if c, err := r.Cookie("mkt_sess"); err == nil {
            if sid, err := uuid.Parse(c.Value); err == nil {
                _ = sessions.Revoke(r.Context(), sid)
                cookieRevoked = true
                audit.Record(r.Context(), auth.AuditLogout{
                    Surface: "web", SessionID: sid,
                    UserID: userIDFromCtx(r.Context()),
                })
            }
            // Clear cookies regardless (the cookie was malformed → still wipe).
            auth.ClearSessionCookies(w, cfg.Cookies)
        }

        if body.RefreshToken != "" {
            err := refresh.RevokeByPlaintext(r.Context(), body.RefreshToken)
            if err == nil {
                audit.Record(r.Context(), auth.AuditLogout{
                    Surface: "native",
                    UserID:  userIDFromCtx(r.Context()),
                })
            }
            // We deliberately do NOT 401 here: a stale refresh during logout
            // should still result in a clean 204 from the client's POV.
        }

        // 204 even if neither cookie nor refresh was present — logout is
        // idempotent. The intentional 15-min access-token lag is the
        // documented trade-off (story AC-2).
        w.WriteHeader(http.StatusNoContent)

        _ = cookieRevoked // reserved for telemetry counter increment
    }
}
```

### 3.2 Logout-all

```go
// api/internal/http/auth_logout_all.go
func logoutAllHandler(
    sessions auth.SessionStore, refresh auth.RefreshStore,
    audit auth.AuditSink, cfg auth.Config,
) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        user, ok := auth.UserFromContext(r.Context())
        if !ok {
            problem(w, http.StatusUnauthorized, "unauthorized", "")
            return
        }

        n1, err := sessions.RevokeAllForUser(r.Context(), user.ID)
        if err != nil { problem(w, 500, "internal", ""); return }
        n2, err := refresh.RevokeAllForUser(r.Context(), user.ID)
        if err != nil { problem(w, 500, "internal", ""); return }

        // If the caller is on the cookie surface, also clear their cookies.
        auth.ClearSessionCookies(w, cfg.Cookies)

        audit.Record(r.Context(), auth.AuditLogoutAll{
            UserID:           user.ID,
            WebRevokedCount:  n1,
            RefreshRevokedCount: n2,
        })

        w.WriteHeader(http.StatusNoContent)
    }
}
```

### 3.3 Admin revoke

```go
// api/internal/http/users_admin_revoke.go
func revokeUserSession(s auth.SessionStore, audit auth.AuditSink) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        uid := uuid.MustParse(chi.URLParam(r, "id"))
        sid := uuid.MustParse(chi.URLParam(r, "session_id"))
        // Scoping: only revoke if the session belongs to the path user.
        n, err := s.RevokeForUser(r.Context(), uid, sid)
        if err != nil { problem(w, 500, "internal", ""); return }
        if n == 0    { problem(w, 404, "session-not-found", ""); return }

        audit.Record(r.Context(), auth.AuditAdminRevoke{
            ByAdmin:   uuidFromCtx(r.Context()),
            TargetUID: uid, SessionID: sid, Kind: "web",
        })
        w.WriteHeader(http.StatusNoContent)
    }
}

func revokeUserRefreshFamily(s auth.RefreshStore, audit auth.AuditSink) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        uid := uuid.MustParse(chi.URLParam(r, "id"))
        fam := uuid.MustParse(chi.URLParam(r, "family_id"))
        // Scoped: ensure the family belongs to the path user (via SQL WHERE).
        n, err := s.RevokeFamilyForUser(r.Context(), uid, fam)
        if err != nil { problem(w, 500, "internal", ""); return }
        if n == 0    { problem(w, 404, "family-not-found", ""); return }

        audit.Record(r.Context(), auth.AuditAdminRevoke{
            ByAdmin: uuidFromCtx(r.Context()),
            TargetUID: uid, FamilyID: fam, Kind: "refresh-family",
        })
        w.WriteHeader(http.StatusNoContent)
    }
}
```

The "scoped to user" SQL prevents an admin from accidentally revoking
session/family rows that belong to a different user (CSRF + path-id
mismatch defense).

### 3.4 Streaming close-all

```go
// api/internal/http/users_admin_streaming.go
func closeAllStreamingForUser(
    db *db.Queries, streaming pb.StreamingClient, audit auth.AuditSink,
) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        uid := uuid.MustParse(chi.URLParam(r, "id"))
        sessions, err := db.ListActiveStreamingSessionsForUser(r.Context(), uid)
        if err != nil { problem(w, 500, "internal", ""); return }

        var closed int
        for _, s := range sessions {
            _, gerr := streaming.CloseSession(r.Context(), &pb.CloseSessionRequest{
                SessionId: s.ID.String(), Reason: "admin-revoke",
            })
            if gerr != nil {
                // Log + continue; partial close is acceptable. The DB row
                // gets flipped by the Streaming side on close.
                slog.Warn("close streaming failed", "sid", s.ID, "err", gerr)
                continue
            }
            closed++
            audit.Record(r.Context(), auth.AuditStreamingCloseAdmin{
                ByAdmin: uuidFromCtx(r.Context()), TargetUID: uid, SID: s.ID,
            })
        }
        writeJSON(w, http.StatusOK, map[string]any{
            "found": len(sessions), "closed": closed,
        })
    }
}
```

## 4. Test plan

### 4.1 Web logout (`auth_logout_test.go`)

| Test | What it pins |
|---|---|
| `TestWebLogoutRevokesSession` | Login → POST /logout with cookie → 204; row's `revoked_at != NULL`; response has `Set-Cookie: mkt_sess=; Max-Age=0` and `mkt_csrf=; Max-Age=0`. |
| `TestWebLogoutNextRequestIs401` | After logout, GET /api/me with the cleared cookie → 401 (the cookie is cleared client-side; even if cookie was kept, it's revoked). |
| `TestWebLogoutEmitsAudit` | One audit row `event='logout', payload.surface='web'`. |
| `TestWebLogoutNoCookieReturns204` | POST /logout with no cookie and no body → 204; no DB writes; no audit row. |
| `TestWebLogoutMalformedCookieReturns204AndClears` | `mkt_sess=garbage` → 204; the cookie is cleared in the response. |

### 4.2 Native logout

| Test | What it pins |
|---|---|
| `TestNativeLogoutRevokesRefresh` | POST /logout with body `{refresh_token: ...}` → 204; row's `revoked_at != NULL`. |
| `TestNativeLogoutAccessTokenStillWorksUntilExp` | After native logout, the access token continues to verify until its `exp`. Documented behavior. |
| `TestNativeLogoutMalformedRefreshReturns204` | `{refresh_token: "garbage"}` → 204; no row touched; no audit. |
| `TestNativeLogoutEmitsAudit` | `event='logout', payload.surface='native'`. |

### 4.3 Logout-all

| Test | What it pins |
|---|---|
| `TestLogoutAllRevokesEveryWebSession` | Three web sessions for user X; logout-all → all three rows have `revoked_at`; response 204. |
| `TestLogoutAllRevokesEveryRefreshFamily` | Two refresh families; logout-all → all rows in both families have `revoked_at`. |
| `TestLogoutAllOnDeviceBLogsOutDeviceA` | Login from A and B (native); logout-all from B → A's next refresh returns 401 `invalid-refresh` (its row is revoked). |
| `TestLogoutAllAuditWithCounts` | Audit row's payload contains `web_n=3, refresh_n=2`. |
| `TestLogoutAllRequiresAuth` | Unauthenticated → 401. |

### 4.4 Admin revoke

| Test | What it pins |
|---|---|
| `TestAdminRevokeWebSession` | Admin DELETE /api/users/X/sessions/SID → 204; row revoked; X's next request 401. |
| `TestAdminRevokeRefreshFamily` | Admin DELETE /api/users/X/refresh-tokens/F → 204; family fully revoked. |
| `TestAdminRevokeWrongUserReturns404` | DELETE /users/X/sessions/SID where SID belongs to user Y → 404 `session-not-found` (proves WHERE-user_id scope). |
| `TestAdminRevokeNonAdminReturns403` | A non-admin caller → 403. |
| `TestAdminRevokeAuditWritten` | One audit row with `by_admin` and `target_uid` populated. |

### 4.5 Streaming close-all

| Test | What it pins |
|---|---|
| `TestStreamingCloseAllInvokesGRPCPerSession` | 3 active streaming sessions for user X; admin POST /streaming/close-all → 3 gRPC `CloseSession` calls; response `{found: 3, closed: 3}`. |
| `TestStreamingCloseAllPartialFailureContinues` | Mock gRPC to fail on session 2; → `{found: 3, closed: 2}`; audit rows for sessions 1 and 3 only. |
| `TestStreamingCloseAllRequiresAdmin` | Non-admin → 403. |
| `TestStreamingCloseAllRequiresExistingUser` | `id` of nonexistent user → 200 with `{found: 0, closed: 0}` (idempotent). |

## 5. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Access token still valid for ~15 min after logout | Documented trade-off (story AC-2). For instant-kill, operator runs `keys rotate --immediate` (Story 10.6). | n/a |
| Logout while client has no network | Server cannot expire the token until the device returns and POSTs refresh; then the row is `revoked_at != NULL` and the refresh returns 401 `invalid-refresh`. | `TestNativeLogoutAccessTokenStillWorksUntilExp` (control case) |
| `logout-all` from a user whose token was already revoked | All UPDATEs match 0 rows; response 204; audit row records `web_n=0, refresh_n=0`. | `TestLogoutAllIdempotent` |
| Admin revokes a session that was already revoked | `RevokeForUser` returns 0 → 404 `session-not-found`. We do not 200-with-noop because the admin's path-param implies the row should still exist; reporting 404 surfaces the staleness. | `TestAdminRevokeAlreadyRevoked` |
| `streaming/close-all` while Streaming is unhealthy | gRPC errors → logged WARN; partial-close response. The operator can retry. | `TestStreamingCloseAllPartialFailureContinues` |
| User logs out while a `Refresh` is in flight | The in-flight refresh either (a) lands first → rotation succeeds → user is logged out anyway when the new refresh row is then revoked-by-logout-all, or (b) races with logout's revoke. The conditional-update pattern in 10.4 resolves both deterministically. | `TestLogoutDuringInFlightRefresh` |
| Cookie-only logout against a refresh-token user | The cookie path is a no-op (no cookie present); refresh body is missing → 204 with no DB write. The mobile app uses the body form. | `TestWebLogoutNoCookieReturns204` |
| `Authorization: Bearer …` access tokens used after `keys rotate --immediate` | Verify fails (`unknown-kid`); request is 401. The user re-logs-in or refreshes. | Story 10.6 plan |
| Sentinel admin (single-user mode) calls logout-all | The sentinel UUID has no real sessions → both counts 0; 204 returned; no observable effect. The admin token stays valid (it bypasses the user table). | `TestLogoutAllSingleUserModeNoOp` |

## 6. Dependencies

No new dependencies beyond the rotation/issuance stories.

## 7. Acceptance checklist

**Endpoints**
- [ ] `POST /api/auth/logout` accepts cookie or `{refresh_token}` body and returns 204.
- [ ] `POST /api/auth/logout-all` revokes every web session AND every refresh family for the caller; returns 204.
- [ ] `DELETE /api/users/{id}/sessions/{session_id}` is admin-only; scoped by user_id; 404 on wrong-user mismatch.
- [ ] `DELETE /api/users/{id}/refresh-tokens/{family_id}` is admin-only; scoped.
- [ ] `POST /api/users/{id}/streaming/close-all` is admin-only; iterates `streaming_sessions` and calls `streaming.CloseSession`.

**Behavior**
- [ ] AC-1: web logout sets `revoked_at`; clears both cookies.
- [ ] AC-2: native logout revokes refresh row; access token is NOT invalidated server-side and continues to work until `exp`.
- [ ] AC-3: logout-all revokes everything for the user; audit row carries counts.
- [ ] AC-4: admin revoke routes return 204; 404 when the row doesn't belong to the path user.

**Audit**
- [ ] `event='logout'` writes one row with `surface ∈ {web, native}`.
- [ ] `event='logout-all'` writes one row with the counts in payload.
- [ ] `event='streaming.close-admin'` written per closed session.

**Idempotency**
- [ ] Repeated logout of the same session is a no-op (204; no extra audit rows).

**Tests**
- [ ] All §4 tests pass on both dialects.

**Docs**
- [ ] README.md ticks story 10.5.
- [ ] The 15-min access-token lag is documented at the top of the auth README.
