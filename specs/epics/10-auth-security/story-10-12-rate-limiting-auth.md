# Story 10.12 — Rate limiting on auth endpoints

Coordinated with Epic 7 Story 7.19's general rate-limit middleware.
Auth endpoints get tighter caps because each call is an attack vector.

> **Reconciled values.** REVIEW.md §1.4.a flagged a 30 vs 10 conflict
> between this epic and the NFR. The reconciliation: the broader
> `/api/auth/*` surface (refresh, logout, pair) shares a 30/min cap;
> the narrower `/api/auth/login` endpoint has its own stricter cap of
> 10/min. Both are per-IP. NFR Story 23.6's "10/min per IP for login"
> aligns with AC-3 below.

**AC-1 — Per-IP cap on `/api/auth/*` (broader surface).**
- **Given** any IP,
- **When** the IP exceeds `auth_rate_per_min` (default **30**) on the
  union of `/api/auth/*` *excluding* `/api/auth/login`,
- **Then** further requests return 429 with `Retry-After`, regardless
  of whether the credentials would have been valid.

**AC-2 — Per-token-family cap on `/refresh`.**
- **Given** a refresh token family,
- **When** more than `refresh_rate_per_min` (default 6) refreshes
  succeed in a minute (a healthy device refreshes every 10 min),
- **Then** further refreshes for that family return 429. Mitigates a
  buggy client spamming refresh.

**AC-3 — Per-IP cap on `/api/auth/login` (narrow, stricter).**
- **Given** any IP,
- **When** the IP exceeds `login_rate_per_min` (default **10**) on
  `/api/auth/login`,
- **Then** further requests return 429 with `Retry-After`. This is
  separate from and stricter than AC-1's cap.

**Test cases:**
- Integration: 11 logins from one IP in 60 s → at least one 429 fires
  from AC-3's narrower cap before AC-1 would have triggered.
- Integration: 31 calls to `/api/auth/refresh` from one IP in 60 s →
  at least one 429 from AC-1 (refresh path is in the broader surface).
- Integration: a misbehaving client refreshing every 5 s → ratelimited
  after 6 refreshes; access tokens still valid.

**Edge cases:**
- A NAT-shared IP (office) — login cap of 10/min may pinch a noisy
  office; the admin can raise the limits via Epic 7 Story 7.15
  settings.
- Admin can raise the limits via settings; the changes take effect on
  the next settings reload.
