# Story 8.1 — Server skeleton, signed URL middleware

The bytes-only HTTP surface. Every Streaming endpoint runs through one
middleware that validates a signed JWT against the API's public JWKS,
extracts the session id, and rejects expired or wrong-audience tokens.

**AC-1 — Signed URL validation.**
- **Given** a request `GET /stream/{session_id}/{path}?sig={jwt}`,
- **When** the middleware runs,
- **Then** the JWT is verified RS256 against the public key cached from
  `GET <api_origin>/.well-known/jwks.json`, the `aud` claim must equal
  `streaming`, the `sub` claim must equal `session_id`, the `exp` must
  be in the future, and the `lib[]` claim must contain the library_id
  of the video being served (looked up in the probe cache, Story 8.15).
  Failure → `401 Unauthorized` problem+json with `type:
  signed-url-invalid` (the sub-types `missing`, `expired`, `wrong-aud`,
  `wrong-sub`, `wrong-lib`, `bad-signature` carried in `detail`).

**AC-2 — JWKS refresh.**
- **Given** the cached JWKS is older than `jwks_refresh_sec` (default
  300 s),
- **When** the next request arrives,
- **Then** the JWKS is refreshed asynchronously (the in-flight request
  uses the cached key); on refresh failure the cache is kept (don't
  invalidate working keys on transient API outage).

**AC-3 — Range-correct error envelopes.**
- **Given** an authenticated request to a missing segment,
- **When** processed,
- **Then** the response is `404 Not Found` problem+json (NOT 200 with
  empty body — players treat empty 200 as "stream ended").

**AC-4 — Direct-play JWT in query string.**
- **Given** `GET /stream/direct/{video_id}?sig=<jwt>`,
- **When** processed,
- **Then** the JWT is validated as in AC-1 except `aud=streaming-direct`
  and `sub=video_id`. The endpoint also accepts `Authorization: Bearer
  <jwt>` for native players that prefer headers. The `lib[]` claim
  check applies the same way.

**AC-5 — Static-asset JWT for posters/sprites/subtitles.**
- **Given** any request to `/stream/posters/*`, `/stream/sprites/*`,
  `/stream/thumbs/*`, or `/stream/{session_id}/subs/*`,
- **When** processed,
- **Then** the JWT is validated with `aud=streaming-static`, the `sub`
  must equal the SHA-256 of the artifact path (so a stolen URL only
  unlocks one artifact), and `lib[]` must contain the asset's library.
  Story 8.13 and Story 8.11 enforce this — the middleware is the same
  code path for all signed paths.

**Test cases:**
- Unit: an unsigned URL → 401 `type: signed-url-missing`.
- Unit: JWT signed with the wrong key → 401 `type: bad-signature`.
- Unit: JWT for a different session id → 401 `type: wrong-sub`.
- Unit: JWT whose `lib[]` claim omits the resource's library → 401
  `type: wrong-lib`.
- Integration: JWKS endpoint returning a new key id → next request
  succeeds without restart.
- Integration: 1000 parallel requests to the same session URL → the JWKS
  is fetched at most once.
- Security: an attacker who steals a manifest URL can only access that
  session's segments (sub claim) for the remaining TTL, and only for
  libraries the original session's user could read at mint time.

**Edge cases:**
- Clock skew between API and Streaming up to ±60 s — `exp` is checked
  with a `clock_skew_leeway_sec` (default 60). Test case: a JWT with
  `exp = now()-30s` is still accepted.
- API is down so JWKS can't refresh — keep using the cached key
  indefinitely; emit a warning metric `jwks_refresh_failed_total`.
  Test case: kill API, requests using existing session continue to work.
- The API rotates its signing key — the new key id is in the JWKS;
  Streaming picks it up on next refresh; old in-flight URLs (signed by
  the old key) keep working until they expire because the JWKS contains
  both keys during rotation. Documented in Epic 10 Story 10.6.
- A player that retries a failed segment with the same URL after JWT
  expiry — receives 401 once, then must call `POST /api/stream/sessions`
  to mint a fresh URL. The Streaming binary never extends a JWT.
- A user whose access to a library was revoked after session mint — the
  JWT's `lib[]` claim is still valid until expiry (default 1800 s for
  sessions). Documented as the "≤30 min revocation lag" in Epic 10
  Story 10.5; immediate revocation requires the API to call
  `streaming.CloseSession` on the affected sessions.
