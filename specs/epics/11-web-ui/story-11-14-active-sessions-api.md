# Story 11.14 — Active session listing & per-session revoke

**Status:** **NEW** — added in response to
[REVIEW §3.4](../../REVIEW.md): Story 11.6 references "list active
sessions (with revoke)" but Epic 10 Story 10.5 only covers the
single-session and logout-all flows. This story owns the listing surface
and per-session revoke.

A user can see every device that holds a refresh token and revoke any of
them individually from Settings → Account → Sessions. Useful when a
device is lost or shared and the user wants to invalidate one credential
without logging out everywhere.

**Anchors:** [`architecture.md` §9.8](../../architecture.md), Epic 10
Stories 10.3 (refresh tokens), 10.5 (logout). Depends on the
`refresh_tokens` and `web_sessions` tables (Epic 10; their migrations are
the owner per [REVIEW §1.1.h](../../REVIEW.md)).

## AC

### Endpoints

- `GET /api/me/sessions` →
  ```
  200 {items: [{
    id,                  # opaque session id
    kind,                # "web" | "mobile" | "desktop" | "tv" | "pat" | "device"
    device_label,        # user-supplied or auto-derived (e.g., "iPhone 14, Safari 17")
    user_agent,
    ip_first_seen,       # CIDR-truncated for privacy (Story 21.8)
    created_at,
    last_used_at,
    expires_at,
    is_current           # true for the session that issued the request
  }]}
  ```
  - `kind = "web"` rows come from `web_sessions`; `kind = "mobile" |
    "desktop" | "tv"` rows come from `refresh_tokens` joined with
    `device_label`.
  - PATs (`kind = "pat"`) are listed here as well, deduplicated against
    Story 11.13's `GET /api/me/tokens` (same `id`, same row).
- `DELETE /api/me/sessions/{id}` → `204` revokes the session.
  - For web sessions: deletes the row, the cookie is invalid on next
    request.
  - For refresh tokens: sets `revoked_at = now()`; the access token
    survives until natural expiry (≤ 15 min, per
    [REVIEW §1.5.c](../../REVIEW.md)).
  - Admin variant `DELETE /api/users/{id}/sessions/{session_id}` covers
    the orphan endpoint flagged in [REVIEW §3.2](../../REVIEW.md); it
    requires admin scope.
- Both endpoints return `404` if the session does not exist or belongs to
  another user (no enumeration).

### UI / behavior

- Settings → Account → Sessions renders the list, sorted by
  `last_used_at DESC`, with the current session annotated and pinned at
  the top.
- Revoking the current session logs the user out (same effect as
  `POST /api/auth/logout`).
- Bulk action "Revoke all other sessions" maps to
  `POST /api/auth/logout-all` (Epic 10 Story 10.5).

## TC

- Log in from web + iPhone + iPad: `GET /api/me/sessions` returns 3 rows.
- Revoke the iPad row: subsequent request from the iPad with the access
  token works for ≤ 15 min then fails 401; refresh fails 401 `token-revoked`.
- Issue a PAT (Story 11.13): it appears in `GET /api/me/sessions` with
  `kind = "pat"`.
- Admin revokes another user's session via
  `DELETE /api/users/{id}/sessions/{sid}`: target user's next refresh
  fails.

## EC

- Session list at scale (>100 sessions): paginate at 50 per page; the UI
  shows "and N older — purge?".
- Two web sessions with identical user agents (private window): both
  rows present; `is_current` distinguishes which is the caller.
- Revoke a session that has already expired naturally: 204 (idempotent).
- Race: revoke and refresh arrive within ~10 ms; refresh either succeeds
  (and is then revoked on next call) or fails (transactional outcome
  acceptable either way; documented).
