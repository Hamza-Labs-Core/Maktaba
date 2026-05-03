# Story 10.8 — Signed-URL minter

The API mints signed URLs for Streaming sessions, direct play, and
sidecar artifacts (Epic 7 Stories 7.10, 7.7). Every minted URL carries
the `lib[]` claim required by Epic 8 Story 8.1 AC-1's offline
authorization check.

**AC-1 — Manifest URL.**
- **Given** `mintManifestURL(session_id, user_id, library_id, ttl)`,
- **When** called,
- **Then** the URL is `https://<streaming_origin>/stream/{session_id}/
  manifest.m3u8?sig=<jwt>` where `<jwt>` carries
  `aud=streaming, sub=session_id, usr=user_id, lib=[library_id],
  exp=now+ttl, jti, kid`. Default ttl is `session_url_ttl_sec` (1800 s).

**AC-2 — Direct URL.**
- **Given** `mintDirectURL(video_id, user_id, library_id, ttl)`,
- **When** called,
- **Then** the URL carries `aud=streaming-direct, sub=video_id,
  usr=user_id, lib=[library_id]`, signed with the API's key. The
  `usr` claim is required for audit (Story 10.16's
  `streaming.direct.access` event).

**AC-3 — Static-asset URL (poster, sprite, subtitle, chapters JSON).**
- **Given** `mintStaticURL(artifact_path, video_id, user_id, library_id, ttl)`,
- **When** called,
- **Then** the URL carries `aud=streaming-static, sub=<sha256(artifact_path)>,
  usr=user_id, lib=[library_id]`; ttl matches the asset's recommended
  cache lifetime (poster 1 h, subtitle 1 h, sprite 1 h, chapters JSON
  1 h). Epic 8 Story 8.13 enforces the validation server-side.

**AC-4 — TTL is bounded.**
- **Given** any caller,
- **When** ttl is requested above `max_signed_url_ttl_sec` (default
  86400),
- **Then** the value is capped silently and a metric incremented.

**AC-5 — `lib[]` resolution.**
- **Given** a video,
- **When** the minter prepares the JWT,
- **Then** the API resolves the user's accessible libraries at mint
  time (intersection of `library_acl` rows for the user and the
  resource's library); if the user does not have access, the minter
  returns `403 access-denied` *before* signing — no JWT is issued for a
  resource the user can't read. This guarantees that any well-formed
  signed URL Streaming sees grants the right library.

**Test cases:**
- Unit: each URL kind decodes to the expected claims, including a
  non-empty `lib` array with the correct library id.
- Integration: a minted URL is accepted by Streaming until expiry, then
  rejected.
- Integration: minting for a video the user can't access returns 403
  and never produces a JWT.

**Edge cases:**
- The API has no private key configured (misconfig) — the minter raises
  a `KeyUnavailable` error; callers translate to 503 `type:
  signing-unavailable`.
- A URL minted with `aud=streaming` then sent to `/stream/direct/...` →
  rejected with `type: wrong-aud` (Epic 8 Story 8.1 AC-1).
