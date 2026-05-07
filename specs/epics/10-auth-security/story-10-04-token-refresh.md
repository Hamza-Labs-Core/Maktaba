# Story 10.4 — Token refresh + rotation

Refresh tokens use rotation: each refresh issues a new refresh token and
invalidates the old one. A reused old token signals theft and revokes
the entire family (§9.8 implied).

**AC-1 — Refresh flow.**
- **Given** a valid refresh token,
- **When** `POST /api/auth/refresh {refresh_token}` is sent,
- **Then** the response is `{access_token, refresh_token, ...}`; the
  used refresh row is marked `revoked_at=now(), replaced_by=<new id>`,
  the new row is inserted with `family_id` inherited. The new
  `access_token` re-snapshots the user's current `lib` set (Story
  10.3 AC-2), so refresh is the natural cadence at which library
  ACL revocations propagate.

**AC-2 — Reuse detection.**
- **Given** an already-revoked refresh token (replayed by an attacker),
- **When** presented,
- **Then** every active row in the same `family_id` is revoked; the
  user's other devices are silently logged out; an audit row is written
  to `audit_log` with `category='security', event='refresh.replay-detected',
  payload={user_id, family_id, ip}`. The response is `401 type:
  refresh-replayed`.

**AC-3 — Refresh expiry.**
- **Given** a refresh token whose `expires_at < now()`,
- **When** presented,
- **Then** 401 `type: refresh-expired`; the user must log in again. No
  family revocation (this is a normal expiry, not theft).

**Test cases:**
- Integration: refresh → old token now invalid; new token works.
- Security: replay an old refresh after a successful refresh → family
  revoked; previously valid sibling tokens no longer work.
- Integration: expired refresh → 401 expired; the user's other sessions
  are unaffected.
- Integration: a user whose library access was revoked sees an updated
  (smaller) `lib` claim on their next refresh.

**Edge cases:**
- Network race: client retries refresh before the server's response
  arrives — both requests carry the same old token; the second one
  triggers reuse detection. Mitigation: clients must serialize refresh
  per-device (documented in client SDKs).
- Refresh against a revoked user account — 401, no token issued.
- A device wiped without logout — the refresh token rots until expiry;
  fine.
