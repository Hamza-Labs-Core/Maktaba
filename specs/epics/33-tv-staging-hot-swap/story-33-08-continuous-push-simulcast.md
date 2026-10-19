# Story 33.8 — Continuous remote push and simulcast across swaps

## Description

*As a remote viewer watching through Cloudflare, and as the owner simulcasting a channel to YouTube or
Facebook, I want a swap, a rollback or a slot crash to go unnoticed: no stall, no re-tune, no new
broadcast, and no disconnect on YouTube/Facebook.*

Epic 32 pushes a channel from the encoding host's Hub to a Cloudflare live input (32.7), and story
32.13 attaches simulcast outputs (YouTube, Facebook, custom RTMP) to that input. If the push were owned
by a slot, every swap would end the RTMPS/SRT session: Cloudflare would start a new video after
`timeoutSeconds` (30 s), tokens whose subject is the video id would stop working, and every simulcast
output would drop. This story moves the push into the front door's **push relay**, which keeps one
session per channel and switches its *source* between slots (and hosts) at a grid IDR with continuous
timestamps. **The simulcast outputs are protected by that single continuous session**: they hang off
the live input, and the live input never sees its publisher go away.

Design: [design.md](design.md) §9.

## Acceptance criteria

- **AC1** The push for a channel runs in the front door (`frontdoor/relay`): one long-lived ffmpeg
  (video copy, audio copy or AAC) per pushed channel, fed through the splicer from the **routed** slot.
  No slot and no node runs a pusher; `tv_push_credentials` answers only the front door's role.
- **AC2** Across a swap, a warm rollback and a live-slot failover to a ready slot, the relay keeps the
  same RTMPS/SRT connection (same TCP/SRT socket, no ffmpeg restart): Cloudflare's live input status
  stays `connected`, the lifecycle `videoUID` is unchanged, no new recording starts, remote session
  tokens stay valid, and remote players do not stall more than one segment.
- **AC3** Timestamps fed to ffmpeg are monotonic and continuous across the source switch (gap ≤ one
  frame with aligned output; re-timestamped otherwise); the FLV/SRT output timestamps are monotonic.
- **AC4** Simulcast outputs attached to the input (32.13) see **no disconnect** across a swap, rollback
  or slot failover: Cloudflare output status stays connected; the YouTube/Facebook stream health shows
  no interruption (manual check in the PR with a private YouTube stream).
- **AC5** Staging warm-up and verification never push and never connect to a live input; there is never
  a second publisher to the same input (asserted by the fake ingest: exactly one publisher per stream key
  at any time).
- **AC6** Multi-host: when the channel's encoding host changes at the swap, the relay on the home pulls the
  new host's TS (signed) and switches source; nothing reconnects.
- **AC7** Parameter changes (`splice.compat = param_change`) on a pushed channel: if the 33.8 spike shows
  Cloudflare accepts an in-session sequence-header change, the switch stays in-session; otherwise the
  relay reconnects within 2 s (inside `timeoutSeconds`: same video id) and the change required explicit
  approval (`ack_param_change`) or was deferred for that channel (`param_change_push = defer`: the channel
  switches when its push ends). Never auto-promoted.
- **AC8** Relay ffmpeg crash: restarted within 1 s on the same input; front door restart: relays reconnect
  within the restart time (< 30 s): same video id in both cases (event `tv_push_reconnect`).
- **AC9** Audio transcode (AC-3 channels): the AAC stream is continuous across the switch (no audio gap
  > one AAC frame).

## Test cases

- **TC1** `TestRelayHoldsSessionAcrossSwitch` — fake RTMP server counts connections: exactly one.
- **TC2** `TestRelayTimestampsMonotonic` — parse FLV tags at the fake server across two switches (swap,
  rollback): monotonic, max gap ≤ 1 GOP.
- **TC3** `TestRelayHostChange` — upstream A (home slot) → B (node slot over signed HTTP).
- **TC4** `TestNoSecondPublisher` — staging warm-up with remote enabled: fake ingest sees one publisher.
- **TC5** **E2E with a fake ingest and an attached output**: fake RTMP ingest (Go test server) that
  re-publishes everything it receives to a second fake server (the "simulcast output"); run swap →
  rollback → live-slot kill with failover; assert one ingest session, one output session, no disconnect
  on either, monotonic timestamps on both, no gap > 1 GOP. Same test with SRT (`ffmpeg -listen` or a
  libsrt test server) when `libsrt` is available.
- **TC6** `TestRelayParamChangeReconnectWithinWindow` — forced reconnect < 2 s.
- **TC7** Spike (manual, documented in the PR): real Cloudflare input, resolution change mid-session over
  RTMPS and SRT; record whether the same video continues and whether a YouTube simulcast output drops.

## Edge cases

- **EC1** A push that starts during a swap window: it starts on the current routed slot and is switched
  with the others.
- **EC2** The front door is down longer than `timeoutSeconds`: Cloudflare starts a new video; Epic 32's
  path applies (sessions get new tokens on their next heartbeat); simulcast outputs reconnect — logged as
  `tv_push_broken (front_door_down)`.
- **EC3** Budget/limits (32.10) unchanged: the relay is the same push, counted once.

## Dependencies

Epic 32.7 (push), 32.8 (tokens), 32.13 (simulcast); 33.4, 33.7. Amends Epic 32 §7.4 (pusher location).
