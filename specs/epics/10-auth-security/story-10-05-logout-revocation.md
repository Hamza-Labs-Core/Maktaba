# Story 10.5 — Logout + session revocation

Both surfaces support explicit logout. Admins can revoke any session.

**AC-1 — Web logout.**
- **Given** a logged-in web client,
- **When** `POST /api/auth/logout` is sent with the cookie,
- **Then** the `web_sessions` row is updated `revoked_at=now()` (kept
  for audit), the response clears `mkt_sess` and `mkt_csrf`, status
  `204`.

**AC-2 — Native logout.**
- **Given** a refresh token,
- **When** `POST /api/auth/logout {refresh_token}` is sent,
- **Then** the matching refresh row is revoked. The access token is
  *not* invalidated server-side and remains usable until its `exp`
  (default 15 min).
- **The 15-minute revocation lag is intentional** (§9.8): stateless JWT
  verification trades instantaneous revocation for offline streaming
  (Epic 8 Story 8.1). For instant-revocation needs, set
  `auth.access_ttl_sec` to 60 s and accept the 4× refresh churn. This
  trade-off is also surfaced by the permission model in Story 10.13.

**AC-3 — Logout-all-devices.**
- **Given** a user,
- **When** `POST /api/auth/logout-all` is sent,
- **Then** every web session and every refresh family for that user is
  revoked; an audit row is written with `category='security',
  event='logout-all'`. In-flight access tokens still work for up to 15
  min unless the operator additionally issues
  `maktaba-api keys rotate --immediate` (which invalidates every JWT
  globally, see Story 10.6).

**AC-4 — Admin revocation.**
- **Given** an admin,
- **When** `DELETE /api/users/{id}/sessions/{session_id}` is sent
  (Story 10.1 AC-3),
- **Then** the session is revoked. Same for `refresh_tokens` rows via
  `DELETE /api/users/{id}/refresh-tokens/{family_id}`.
- For an immediate kill of all of a user's in-flight streaming, the
  admin can additionally call `POST /api/users/{id}/streaming/close-all`
  which iterates `streaming_sessions` for that user and calls
  `streaming.CloseSession` per row.

**Test cases:**
- Integration: logout → next request with the cookie is 401.
- Integration: logout-all from device A → device B's next refresh fails.
- Integration: admin revokes user X's session; user X is a normal user,
  not admin → user X is logged out.
- Integration: admin streaming close-all flips every active streaming
  session for the user to `closed_reason='admin-revoke'`.

**Edge cases:**
- Access token still in flight for ~15 min after logout — accepted as
  the price of stateless verification per AC-2.
- Logout while the client has no network — the server has no way to
  expire the token until the client comes back online and POSTs the
  refresh to be told `revoked`.
