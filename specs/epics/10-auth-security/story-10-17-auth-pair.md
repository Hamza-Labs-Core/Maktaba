# Story 10.17 — Device pairing endpoint

`POST /api/auth/pair` is the API-side implementation of the QR-pairing
flow consumed by Epic 15.5 and the TV/desktop apps. REVIEW.md §3.2
flagged this as a high-impact gap (no API story owned it).

**AC-1 — Device requests a pairing code.**
- **Given** an unauthenticated client (e.g., a TV app on first launch)
  sending `POST /api/auth/pair {device_kind, device_label,
  bundle_id?}`,
- **When** processed,
- **Then** the API inserts a row into `pairing_codes` with state
  `pending`, an 8-character base32 code (avoiding visually ambiguous
  characters `01ILO`), and `expires_at = now() + pairing_ttl_sec`
  (default 600 s). The response is `201` with
  `{code, expires_at, poll_url}`. The endpoint is rate-limited to
  6/min per IP.

**AC-2 — Authenticated user claims the code.**
- **Given** an authenticated user (web or native) who scanned the QR,
- **When** `POST /api/auth/pair/claim {code}` is sent,
- **Then** the row is updated `state='claimed', user_id=<sub>` in a
  single UPDATE with `WHERE state='pending' AND expires_at > now()`.
  Returns 204 on success, 410 `type: pair-code-expired` if expired,
  404 `type: pair-code-unknown` otherwise.

**AC-3 — Device polls and receives tokens.**
- **Given** a device polling `GET /api/auth/pair/{code}` (the
  `poll_url` from AC-1),
- **When** the row is `claimed`,
- **Then** the response is `{access_token, refresh_token, user, ...}`
  using the same shape as Story 10.3 AC-1, the row is deleted (or
  marked terminal), and an audit row
  `category='security', event='pair.code-claimed'` is written. While
  pending, the response is 202 with `state: 'pending'` and a
  recommended retry interval.

**AC-4 — Code uniqueness and constant-time match.**
- **Given** a code candidate,
- **When** matched against `pairing_codes`,
- **Then** the lookup uses a constant-time hash compare (the table
  stores SHA-256(code) keyed by an opaque id, not the plaintext code,
  so a DB read leak does not yield active codes). The plaintext is
  visible only in the `Location` header at issue time.

**AC-5 — Cleanup.**
- **Given** the pairing reaper runs every 60 s,
- **When** it finds rows with `expires_at < now()` AND `state='pending'`,
- **Then** they are marked `state='expired'` and reaped after another
  24 h; an audit row `event='pair.code-expired'` is written for the
  count.

**AC-6 — Rate limits.**
- **Given** AC-1's per-IP cap of 6/min,
- **When** an IP exceeds it,
- **Then** the response is 429. This sits inside the broader
  `auth_rate_per_min = 30` cap from Story 10.12 AC-1 (the `pair`
  endpoint is a member of `/api/auth/*`).

**Test cases:**
- Integration: full pair flow: device requests code → user claims →
  device polls → tokens delivered. Total time under 10 s.
- Integration: an expired code returns 410.
- Integration: an attacker brute-forcing 8-char codes (32^8 ≈ 1e12 keys)
  is throttled by AC-1's per-IP cap and Story 10.12.
- Security: the stored code is hashed (a DB dump cannot be replayed).
- Audit: claiming and expiring write the appropriate `audit_log` rows.

**Edge cases:**
- A device that polls forever (user never scans) — code expires after
  the TTL; subsequent polls return 410 once.
- Two users claim the same code in parallel — only one UPDATE wins
  (the unique state transition); the second sees 404 `type:
  pair-code-unknown`.
- A device claims, then the user logs out everywhere (Story 10.5
  AC-3) — the device's refresh token is revoked along with the rest;
  the device falls back to re-pairing.
- A device that already has a valid refresh token re-runs pair — the
  new pair issues fresh tokens; the old refresh stays valid until
  rotated or revoked.
