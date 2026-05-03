# Story 10.7 — Streaming-side offline JWT verification

Streaming validates JWTs without calling the API (§9.8). Epic 8 Story
8.1 implements the wire format; this story owns the *behavior* of trust:
how Streaming bootstraps and refreshes JWKS, how it handles rotation,
and how it interprets the `lib[]` claim.

**AC-1 — JWKS bootstrap.**
- **Given** Streaming starts,
- **When** the first JWKS fetch runs,
- **Then** it succeeds within `jwks_initial_timeout_sec` (default 10 s);
  on failure the binary still starts but rejects all signed-URL
  requests with 503 `type: jwks-unavailable` until the first fetch
  succeeds.

**AC-2 — Rotation handling.**
- **Given** the API rotates and adds a new `kid` to the JWKS,
- **When** Streaming refreshes (next 5 min poll, or sooner via
  `LISTEN jwks_changed`),
- **Then** tokens with the new `kid` verify correctly. Tokens with the
  old `kid` continue to verify until the API removes that key from the
  JWKS.

**AC-3 — Audience, issuer, library checks.**
- **Given** any token,
- **When** verified,
- **Then** the middleware enforces:
  - `iss="maktaba"`,
  - `aud ∈ {streaming, streaming-direct, streaming-static}` (per
    endpoint class — Epic 8 Story 8.1),
  - `exp` not past, `nbf` not future,
  - `kid` in JWKS,
  - the `lib[]` claim contains the library_id of the resource being
    served (the resource's library is read from the in-memory probe
    cache so this check is offline).
  Mismatch → 401 with the appropriate `type` (Epic 8 Story 8.1 AC-1's
  `wrong-aud`, `wrong-sub`, `wrong-lib`, `bad-signature`, `expired`,
  `missing`).

**Test cases:**
- Integration: end-to-end signed URL from API to Streaming verifies; an
  attacker-signed token with the same shape fails.
- Integration: rotation event → Streaming accepts new tokens within 5
  s if `LISTEN jwks_changed` is wired up, else within 5 min.
- Integration: a JWT whose `lib[]` excludes the resource's library →
  401 `type: wrong-lib`.

**Edge cases:**
- Two API replicas with different active signing keys momentarily during
  a rotation — both keys are in the JWKS; both verify fine. Documented
  as the reason JWKS holds N>1 keys.
- A clock-skew at the Streaming box that puts `now()` 30 s behind the
  API — `exp` leeway absorbs it.
- The probe cache doesn't yet know about a video (cold) — Streaming
  performs a single DB fallback (Epic 8 Story 8.15) before returning
  401, so a freshly-probed video isn't immediately rejected.
