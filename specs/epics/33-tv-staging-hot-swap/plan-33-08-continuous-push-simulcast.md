# Plan 33.8 — Continuous remote push and simulcast — implementation

> Implementation plan for [story-33-08](story-33-08-continuous-push-simulcast.md).
> Design: [design.md](design.md) §9. No migration (Epic 32's `tv_remote_pushes.host_id` becomes the
> front door's host; `generation` follows the routed placement).

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | The push is a front-door client, not a slot subscriber. | The session outlives slots and hosts; simulcast outputs need the input's publisher never to change. |
| D2 | Splice before ffmpeg (Go), ffmpeg sees one continuous TS. | ffmpeg's flv/srt output stays one session; no FLV timestamp jumps. |
| D3 | Reconnect only for parameter changes Cloudflare rejects, within 2 s. | Inside `timeoutSeconds` the broadcast (video id) continues. |
| D4 | Parameter changes on pushed channels need explicit approval or defer. | Simulcast platforms may still drop on a reconnect; the owner decides. |
| D5 | Fake ingest + fake output in CI. | Continuity is the requirement; it must be tested end to end. |

## 1. Files

**Create**
- `streaming/internal/frontdoor/relay/relay.go` — per pushed channel: source (splicer client), ffmpeg child, supervision (1 s restart), credentials via `tv_push_credentials` (front door role)
- `streaming/internal/frontdoor/relay/args.go` — RTMPS/SRT args (Epic 32 §7.4 commands, moved)
- `streaming/internal/frontdoor/relay/relay_e2e_test.go` — fake RTMP ingest that re-publishes to a fake output; FLV tag parser for timestamp checks
- `streaming/internal/testutil/fakertmp/` — minimal RTMP server (handshake, connect, publish, FLV tags) and re-publisher

**Modify**
- Epic 32 `streaming/internal/channel/pusher.go` — removed (or never created if 33.8 lands first); `api/internal/tvremote` assigns pushes to the front door host
- `streaming/internal/frontdoor/ts/splice.go` — relay client mode (no tuner lease)
- `streaming/internal/frontdoor/verify/` — `push.continuity` check consumes `splice.compat`
- `api/internal/tvchange/` — `ack_param_change`, `param_change_push = defer` per channel

## 2. Steps

1. Spike (TC7): Cloudflare behaviour on a mid-session SPS/resolution change (RTMPS and SRT), and simulcast
   output behaviour on a < 2 s reconnect. Record results in the PR and set the default policy.
2. Fake RTMP server + re-publisher in `testutil`.
3. Relay with source switching through the splicer; restart loop.
4. Move push ownership (API side: the push's host is the front door; nodes no longer receive credentials).
5. Parameter-change handling (in-session or reconnect) and the approval/defer rules.
6. E2E tests (RTMP always; SRT when libsrt is present).

## 3. Tests

As in the story; the E2E test runs in the Go CI job with ffmpeg; TC7 manual.

## 4. Rollout

With Phase B. Remote viewing (Epic 32 Phase B) on the home only after this story when staging is enabled.
