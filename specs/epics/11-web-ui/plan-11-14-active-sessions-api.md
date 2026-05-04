# Implementation Plan — Story 11.14 Active Sessions API

> Companion to [story-11-14-active-sessions-api.md](story-11-14-active-sessions-api.md).
> Reads from `web_sessions` and `refresh_tokens` (Epic 10 owner per REVIEW §1.1.h);
> dedupes PAT rows against Story 11.13.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Endpoints | `GET /api/me/sessions`, `DELETE /api/me/sessions/{id}`, `DELETE /api/users/{id}/sessions/{sid}` (admin). |
| Placement | `api/internal/http/me_sessions.go`, `api/internal/auth/sessions/`. |
| Source tables | `web_sessions` (cookie sessions), `refresh_tokens` (mobile/desktop/tv), `personal_access_tokens` (Story 11.13 — PATs surfaced as "kind=pat"). |
| Privacy | `ip_first_seen` is CIDR-truncated /24 (IPv4) / /48 (IPv6) by Story 21.8. |
| Sort | `last_used_at DESC`; current session pinned. |
| Web UI | Embedded into Story 11.6 Account section (`<SessionsManager>`). |
| Out of scope | `POST /api/auth/logout-all` (Epic 10 Story 10.5); device registration (Story 12.10). |

## 1. Unified session view

```sql
-- materialized as a Go union; not a SQL view.
SELECT id, 'web'::text AS kind, device_label, user_agent, ip_first_seen,
       created_at, last_used_at, expires_at,
       (id = $current_web_session_id) AS is_current
FROM web_sessions
WHERE user_id = $1 AND revoked_at IS NULL
UNION ALL
SELECT id, kind, device_label, user_agent, ip_first_seen,
       created_at, last_used_at, expires_at,
       (id = $current_refresh_token_id) AS is_current
FROM refresh_tokens
WHERE user_id = $1 AND revoked_at IS NULL
UNION ALL
SELECT id, 'pat'::text, name AS device_label, NULL, NULL,
       created_at, last_used_at, expires_at, false
FROM personal_access_tokens
WHERE user_id = $1 AND revoked_at IS NULL;
```

The Go service runs three queries and concatenates; this avoids a giant view and lets each table's repo own its types.

## 2. File layout

| Path | Purpose |
|---|---|
| `api/internal/auth/sessions/service.go` | `List(ctx, userID, currentCtx) ([]Session, error)`, `Revoke(ctx, userID, id) error`. |
| `api/internal/auth/sessions/types.go` | `Session` struct shared by HTTP + repos. |
| `api/internal/auth/sessions/repo_web.go` | Reads `web_sessions`; identifies `is_current` by cookie. |
| `api/internal/auth/sessions/repo_refresh.go` | Reads `refresh_tokens`; identifies `is_current` if the request was authed by this token. |
| `api/internal/auth/sessions/repo_pat.go` | Reads `personal_access_tokens`; never `is_current`. |
| `api/internal/http/me_sessions.go` | Handlers. |
| `web/src/features/settings/components/SessionsManager.tsx` | UI consumer. |
| `web/src/features/settings/components/SessionRow.tsx` | One row + revoke. |

## 3. Session model

```go
type SessionKind string
const (
    KindWeb     SessionKind = "web"
    KindMobile  SessionKind = "mobile"
    KindDesktop SessionKind = "desktop"
    KindTV      SessionKind = "tv"
    KindPAT     SessionKind = "pat"
    KindDevice  SessionKind = "device"
)

type Session struct {
    ID           string
    Kind         SessionKind
    DeviceLabel  string
    UserAgent    string
    IPFirstSeen  string        // CIDR-truncated
    CreatedAt    time.Time
    LastUsedAt   time.Time
    ExpiresAt    *time.Time
    IsCurrent    bool
}
```

## 4. Endpoints

### `GET /api/me/sessions`

```go
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
    ident := auth.IdentityFrom(r)
    cur := currentSessionCtx(r)               // resolves cookie or refresh-token id
    items, err := h.svc.List(r.Context(), ident.UserID, cur)
    // sort by last_used_at DESC; pin current to top
    json.NewEncoder(w).Encode(map[string]any{ "items": items })
}
```

### `DELETE /api/me/sessions/{id}`

```go
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
    ident := auth.IdentityFrom(r)
    id := chi.URLParam(r, "id")
    if err := h.svc.Revoke(r.Context(), ident.UserID, id); err != nil {
        if errors.Is(err, ErrNotFound) { problem.Write(w, 404, "session-not-found"); return }
    }
    w.WriteHeader(http.StatusNoContent)
}
```

`Revoke` dispatches by kind:

- `web` → `DELETE FROM web_sessions WHERE id = $1 AND user_id = $2`.
- `mobile|desktop|tv|device` → `UPDATE refresh_tokens SET revoked_at = now() WHERE id = $1 AND user_id = $2`.
- `pat` → delegates to Story 11.13 (`UPDATE personal_access_tokens SET revoked_at = now()`); same row visible to either endpoint.

`session-not-found` returns 404 whether the row doesn't exist OR belongs to another user (no enumeration).

### Admin `DELETE /api/users/{id}/sessions/{sid}`

Same flow as above but:
- Requires `admin` scope.
- Audit log row written (`category='session', action='admin-revoke'`).

## 5. Current-session detection

```go
func currentSessionCtx(r *http.Request) currentCtx {
    if c, _ := r.Cookie("ms"); c != nil { return currentCtx{ webID: c.Value } }
    ident := auth.IdentityFrom(r)
    if ident.RefreshID != "" { return currentCtx{ refreshID: ident.RefreshID } }
    return currentCtx{}
}
```

The bearer middleware annotates `Identity.RefreshID` when the access token was minted from a refresh token whose id we know (Epic 10 plumbing).

## 6. UI integration (Story 11.6 → Account → Sessions)

```tsx
const sessions = useQuery(['me','sessions'], fetchSessions, { staleTime: 30_000 });
const revoke = useMutation((id: string) => api.delete(`/me/sessions/${id}`), { onSuccess: () => qc.invalidateQueries(['me','sessions']) });

return (<>
  <ul>
    {sorted(sessions.data).map(s =>
      <SessionRow key={s.id} session={s}
        onRevoke={() => {
          if (s.isCurrent) {
            confirm(t('settings.sessions.revokeCurrent.confirm')) && revoke.mutate(s.id).then(() => auth.logout());
          } else {
            revoke.mutate(s.id);
          }
        }}/>)}
  </ul>
  <Button onClick={() => api.post('/auth/logout-all').then(() => qc.invalidateQueries(['me','sessions']))}>
    {t('settings.sessions.revokeOthers')}
  </Button>
</>);
```

`<SessionRow>` shows kind icon, device label, UA, IP (CIDR), `last_used_at` ("3 hours ago"), expiry, and a "Current" pill if `isCurrent`.

## 7. Pagination & limits

- Default page size 50; cursor by `last_used_at DESC`. Endpoint: `GET /api/me/sessions?cursor=…`.
- UI shows "and N older — purge?" → batch revoke via `?older_than=<iso>` filter on a future `POST /api/me/sessions:purge`. Not in v1; v1 revokes one at a time.

## 8. Edge cases

| Case | Handling |
|---|---|
| 100+ sessions | Cursor pagination; UI surfaces "purge older". |
| Two private windows same UA | Two rows; `is_current` distinguishes. |
| Revoke an already-expired session | Idempotent 204. |
| Race: revoke + refresh ~10 ms apart | Either succeeds (then revoked next call) or fails (token-revoked). Acceptable. |
| PAT row dedup | `id` matches across `/api/me/pats` and `/api/me/sessions`; clients can correlate. |

## 9. Test cases

### 9.1 Unit (Go)

| Test | Asserts |
|---|---|
| `list union of three sources` | Web + refresh + PAT rows merged; sorted by `last_used_at DESC`. |
| `is_current set for the cookie session` | Mock req with cookie → matching row carries `IsCurrent`. |
| `is_current set for refresh-bound access token` | Mock req with `Identity.RefreshID` → matching row carries `IsCurrent`. |
| `revoke web deletes row` | Row removed from `web_sessions`. |
| `revoke refresh marks revoked_at` | Row updated; access token still works ≤ 15 min. |
| `revoke another user 404` | No leak. |
| `admin revoke writes audit row` | `category='session', action='admin-revoke'`. |

### 9.2 Integration

| Test | Asserts |
|---|---|
| `web + iPhone + iPad → 3 rows` | List length 3. |
| `revoke iPad → next refresh fails` | 401 `token-revoked`. |
| `PAT visible in /api/me/sessions` | One row with `kind='pat'`. |
| `revoke current = logout` | Subsequent request unauthenticated. |

## 10. Performance

- Each table query uses `(user_id, last_used_at DESC)` index; total query budget ≤ 5 ms for 50 rows.
- `is_current` resolved in Go after fetch; no extra DB roundtrip.

## 11. Dependencies

- Epic 10 Stories 10.3, 10.5 (refresh tokens, logout).
- Story 11.13 for PAT row schema.
- Story 21.8 for IP truncation.
- REVIEW §1.1.f for audit log table.
