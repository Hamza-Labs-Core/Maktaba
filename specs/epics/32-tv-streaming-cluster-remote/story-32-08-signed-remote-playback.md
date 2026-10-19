# Story 32.8 — Signed playback for remote viewers

## Description

*As a family member away from home, I want to sign in to Maktaba, pick a channel and watch it,
without anyone who finds the link being able to watch it too.*

The tune API runs on the home and is reached through the Epic 25 relay. It checks the user, the
limits and the budget, makes sure the channel is pushed, and returns a Cloudflare HLS URL with a
token signed on the home.

Design: [design.md](design.md) §7.5, §7.6.

## Acceptance criteria

- **AC1** `POST /api/tv/remote/tune {channel_id, device?}` requires a signed-in user allowed by
  `tv_remote.viewers` who can read the channel (library ACL); returns
  `200 {session_id, hls_url, expires_at, refresh_after, channel}` when the push is live.
- **AC2** Gates in this order, each with its own error: integration not ready / remote disabled →
  503 `remote_unavailable` (with reason); budget → 402 `budget_exhausted`; user sessions ≥
  `max_streams_per_user` → 429 `user_streams_full`; a new push needed and pushes ≥ `max_channels` →
  429 `remote_channels_full`; storage guard → 503 `remote_storage_full`.
- **AC3** While the push starts, the call waits up to 20 s, then answers `202 {state:"starting",
  retry_after:3, session_id}`; retrying with the same user, channel and device is idempotent.
- **AC4** The token is RS256 with the integration's signing key: `sub` = the push's `video_uid`,
  `kid`, `exp = now + token_ttl_minutes` (default 60, max 1440), `nbf = now − 30 s`, and access
  rules allowing only `allowed_countries` when set. `hls_url` =
  `https://customer-<code>.cloudflarestream.com/<token>/manifest/video.m3u8`.
- **AC5** `POST /api/tv/remote/sessions/{id}/heartbeat` (every 60 s, owner only) refreshes presence;
  when the token is past half its life, or the push's `video_uid` changed, it returns a new
  `token`, `hls_url` and `expires_at`.
- **AC6** `DELETE /api/tv/remote/sessions/{id}` ends the session; a session with no heartbeat for
  150 s is ended by the controller.
- **AC7** `POST /api/tv/remote/signing-key/rotate` (admin): a new key signs from then on, the old
  key is deleted at Cloudflare (all old tokens stop working), sessions recover at their next
  heartbeat.
- **AC8** The relay must not count `tune`/`heartbeat` as streams (JSON responses; not
  `/api/streams/*`, no `.m3u8`), so free-tier relay users can watch remotely — the video bytes come
  from the user's Cloudflare account, not the relay.
- **AC9** Every tune and end is recorded in `tv_remote_sessions` (user, channel, device, times) and
  the audit log.

## Test cases

- **TC1** `TestTuneGatesOrder` — one test per error.
- **TC2** `TestTuneReusesPush` — two users on one channel → one push, two sessions.
- **TC3** `TestTokenClaims` — decode with the public key: `sub`, `kid`, `exp`, `nbf`, access rules.
- **TC4** `TestHeartbeatRefreshesToken` — fake clock at 31 min with a 60 min TTL → new token.
- **TC5** `TestHeartbeatAfterVideoChange` — new `video_uid` → new token and URL.
- **TC6** `TestSessionExpiresWithoutHeartbeat`.
- **TC7** `TestRotateSigningKey`.
- **TC8** `TestTuneIdempotent202`.

## Edge cases

- **EC1** The user loses access to the library mid-session: the next heartbeat answers 403 and the
  session ends (the token stays valid until `exp`; a short TTL bounds this).
- **EC2** Clock skew between the home and Cloudflare: `nbf` 30 s in the past; NTP is required on the
  home (runbook).
- **EC3** A user opens the same channel on two devices: two sessions, both count against the limit.

## Dependencies

32.6, 32.7; Epic 25 relay (the API reachable remotely, `X-Maktaba-Via`).
