# Story 32.7 — Live input pool and push from the home's push relay

## Description

*As a remote viewer, I want the channel I tune to be sent to Cloudflare once, from the machine that
already encodes it, so I can watch it from anywhere; as the owner, I want that to stop on its own
when nobody is watching remotely, and leave nothing stored in my Cloudflare account.*

Design: [design.md](design.md) §7.2–§7.4, §7.7, §7.8.

## Acceptance criteria

- **AC1** Slot 0121 creates `tv_remote_inputs`, `tv_remote_pushes`, `tv_remote_sessions`,
  `tv_remote_usage_daily`, `tv_push_secrets` and the functions `tv_push_credentials(push_id)` and
  `tv_push_report(push_id, state, detail)`; node roles get SELECT on non-secret columns and the
  functions only.
- **AC2** Inputs are created on demand up to `tv_remote.max_channels`, named
  `maktaba-<instance8>-<n>`, with recording `automatic`, `requireSignedURLs: true`,
  `timeoutSeconds: 30`, `enabled: false`; `preferLowLatency` follows `tv_remote.low_latency`.
  Stream keys / SRT passphrases are stored in `home_secrets`; while a push runs, its input's key is
  also in `tv_push_secrets` (revoked from workers), deleted when the push ends.
- **AC3** A push request (from 32.8) assigns an idle input, enables it, ensures the channel's
  placement, and moves the push to `requested`; the home's front door push relay starts within
  2 s (NOTIFY `tv_push`, 2 s poll fallback).
  *Amended by Epic 33 story 33.8:* the push runs in the home's front door, not on the encoding
  host ([story-33-08](../33-tv-staging-hot-swap/story-33-08-continuous-push-simulcast.md)).
- **AC4** The push relay pulls the channel's TS from the encoding host (the home's own Hub, or a
  node's signed `live.ts` through the splicing copier — a node-encoded channel crosses the LAN
  once more, ≈ 6.5 Mb/s, fine on the home's 1 Gb/s) and runs one ffmpeg with video stream-copied to
  RTMPS (default) or SRT; AC-3 audio is transcoded to AAC in the push only. No second video encode
  happens anywhere (verified by the process list in the E2E test).
- **AC5** The push is `live` when Cloudflare's lifecycle reports `live: true`; its `video_uid` is
  stored. Timeout 30 s → one retry on the same input, then `failed` with the reason.
- **AC6** At most one push per channel exists in `requested|connecting|live|stopping`.
- **AC7** A pushed channel is never reaped while the push runs (the push holds a viewer slot).
- **AC8** With no remote session for `idle_timeout_minutes` (default 3), the push stops: the relay's
  ffmpeg ends, the input is disabled, the recording is deleted once it leaves `live-inprogress` (≤ 120 s),
  the input returns to idle. A startup sweep deletes leftover recordings of Maktaba inputs.
- **AC9** A push older than `max_push_hours` (default 8) with viewers is rotated: the next push on
  the same input starts after this one ends; sessions get the new video's token at their next
  heartbeat.
- **AC10** On failover (32.4), a move or an Epic 33 swap the relay switches its source to the
  channel's new host/slot and keeps the same RTMPS/SRT session (33.8): same `video_uid`, sessions
  keep their tokens. Only a front-door outage longer than `timeoutSeconds` makes a new video; then
  the next heartbeat returns new tokens.
- **AC11** Cloudflare `ERR_STORAGE_QUOTA_EXHAUSTED` or `ERR_MISSING_SUBSCRIPTION` turns remote
  unavailable with that reason; codec/GOP/`ERR_UNHEALTHY` errors move the input to cleanup, retry
  once on a fresh input, then mark the channel "remote failed" for 10 min.
- **AC12** Credentials reach only the home's front door (`tv_push_credentials` answers only the
  home's database user; nodes never see an ingest key), are held in memory, and are masked in logs.
- **AC13** Optional webhook `POST /api/tv/remote/webhook` (secret required) triggers an immediate
  reconcile; polling alone is enough for correctness.

## Test cases

- **TC1** `TestPoolCreatesLazilyUpToMax` and `TestPoolShrinkDeletesIdle` (fake Cloudflare).
- **TC2** `TestPushStateMachine` — requested → connecting → live → stopping → cleanup → done with a
  fake clock and a fake Cloudflare.
- **TC3** `TestRelayArgs` — RTMPS and SRT command lines; AC-3 → `-c:a aac`; key masked in the log line.
- **TC4** `TestOnePushPerChannel` — concurrent tunes → one push.
- **TC5** `TestRecordingDeletedAfterStop` and `TestStartupSweep`.
- **TC6** `TestRotateAtMaxPushHours`.
- **TC7** `TestPushCredentialsOnlyForPlacedHost` (Postgres).
- **TC8** `TestErrorsMapToActions` — each `ERR_*` code.

## Edge cases

- **EC1** The home's uplink drops: the relay reconnects with backoff; Cloudflare continues the
  broadcast within `timeoutSeconds`; after that a new video starts and sessions are re-tokened.
- **EC2** The relay falls behind (uplink slower than the bitrate): restart;
  after 3 drops in 5 min the push fails with "uplink too slow for <kbps>".
- **EC3** The channel's look/encoder settings change mid-push: the station keeps its settings
  until the next block (as today); the push follows.
- **EC4** The channel is deleted while pushed: placement cascade → station stops → push ends.

## Dependencies

32.2 (placement with `can_push`), 32.6 (credentials, signing key), `feat/tv-core` (encoder args:
2 s GOP, no B-frames, CBR).
