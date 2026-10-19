# Story 32.9 — LAN or remote in the apps

## Description

*As a viewer, I want the TV player to just work: from the home's stream when I am at home, from
Cloudflare when I am away, without choosing anything.*

Web first (it is the app used today); then the native mobile and TV apps.

Design: [design.md](design.md) §8.

## Acceptance criteria

### Server
- **AC1** `GET /api/tv/info` adds `remote: {available, reason, via_relay, max_streams,
  token_ttl_minutes}`. `via_relay` is true for requests carrying `X-Maktaba-Via: relay` from the
  loopback relay agent, or (with `remote_detection = relay_or_public_ip`) from a non-private client
  address. The API strips `X-Maktaba-Via` from requests not coming from loopback.

### Web
- **AC2** The live player (`web/src/pages/LiveTV/Player.tsx`) uses hls.js, loaded lazily, for every
  browser without native HLS (fixes the audit's P1-3), and native HLS where it exists (Safari).
- **AC3** `via_relay` → the remote path (tune, heartbeat, end); otherwise the LAN path, unchanged.
- **AC4** Remote tokens are swapped without reloading the media: a custom hls.js loader rewrites the
  token path segment of playlist and segment URLs to the newest token. On Safari (native HLS) the
  player re-tunes at `refresh_after` and swaps the source at a segment boundary.
- **AC5** Heartbeat every 60 s while playing; end the session on stop, channel change and page
  unload (`sendBeacon`).
- **AC6** States and messages (en + ar): "Starting remote stream…" (202), "Remote viewing paused:
  the monthly budget is reached" (402), "Too many remote streams" / "All remote channels are in use"
  (429), "Remote viewing is not set up" (503 with reason).
- **AC7** Channel surfing remotely ends the old session at once (frees the slot) and tunes the new
  channel; the 400 ms surf debounce stays.
- **AC8** A LAN playback that fails three times (401/502) while `remote.available` offers "Watch
  remotely".

### Native apps (phase C)
- **AC9** iOS/Android/tvOS/Android TV use the Epic 25.10 resolver: LAN endpoint → LAN HLS; relay →
  the remote API and Cloudflare HLS; stream affinity: a session stays on its path until it ends.
- **AC10** Native players re-tune at `refresh_after` and swap the item at the next segment boundary
  (AVQueuePlayer / ExoPlayer playlist).
- **AC11** The guide and channel list come from the existing JSON APIs through the relay.

## Test cases

- **TC1** API: `TestInfoViaRelayHeader`, `TestInfoStripsSpoofedHeader`, `TestPublicIPDetection`.
- **TC2** Web unit: path choice from `info`; loader rewrites `/<old>/` → `/<new>/` only in the token
  segment; heartbeat timer; `sendBeacon` on unload.
- **TC3** Web e2e (Playwright, fake API + fake HLS origin): remote tune → plays; token swap mid-play
  → no `emptied` event; 402 message shown.

## Edge cases

- **EC1** A phone moving from Wi-Fi to mobile data mid-play: the LAN session fails; the player
  shows "Reconnecting…" and, through the relay, tunes remotely (affinity ends with the failed session).
- **EC2** Corporate networks blocking `*.cloudflarestream.com`: the error says the remote stream
  host is blocked.
- **EC3** Browser autoplay rules: the first remote play needs a tap like LAN play.

## Dependencies

32.8; Epic 25.10 (native resolver); Epic 25 relay agent wired in `maktaba-server`.
