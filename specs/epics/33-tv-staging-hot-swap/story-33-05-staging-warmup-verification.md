# Story 33.5 — Staging warm-up and automated verification

## Description

*As the owner, I want a staged change to be running and checked on the channels people actually watch
before anyone sees it, without starving the live channels of encoders.*

When a change is staged, the staging slot starts on its release; the front door warms the channels
with demand, the probe channel and each changed channel in turn, within the encoder budget; then it
runs the checks of design §6.2 for a soak period and writes the report.

Design: [design.md](design.md) §6.

## Acceptance criteria

- **AC1** On `staged`, the controller starts the third slot instance (the one that is neither live nor
  standby) on the release; the hot standby keeps running; on `healthy` the change becomes `verifying`.
- **AC2** Warm set = demand ∪ probe ∪ rolling window of changed channels (`verify_probe_seconds` each);
  each gets a `track = staging` placement and a warm hold; the set follows demand (a channel tuned
  during verification is warmed within 5 s).
- **AC3** Extra encodes never exceed the budget (`staging_max_extra_encodes`, auto = what is left after
  live and the standby's warm set); neither live nor the standby ever loses a session or falls back to
  software because of staging (NVENC).
- **AC4** All checks of design §6.2 run and are recorded per channel with the measured value;
  verification passes after `verify_soak_seconds` with no fail.
- **AC5** `cloudflare.ingest` checks H.264, AAC (or AC-3 with relay transcode), closed GOP within 2–10 s,
  combined bitrate 2–14 Mb/s, no B-frames when `tv_remote.low_latency`; it fails when remote viewing
  is enabled and warns otherwise.
- **AC6** `splice.compat` compares each warmed channel's stream parameters with live's and reports
  `seamless` or `param_change` (which parameter); `push.continuity` fails for a pushed channel with
  `param_change` until acknowledged (`ack_param_change`) or deferred (`param_change_push = defer`).
- **AC7** For unchanged channels, staging's block and PTS equal live's (± 1 frame); for changed
  channels, staging's block equals N+1 at now.
- **AC8** Verification never pushes to Cloudflare, never opens a live input, never receives push
  credentials (test asserts no call to `tv_push_credentials` from a slot).
- **AC9** While `ready`, a subset re-runs every 60 s; a fail moves the change back to `verifying`.
- **AC10** The report is available at `GET /api/tv/changes/{id}` and summarised (pass/warn/fail counts).

## Test cases

- **TC1** `TestWarmSetDemandProbeRolling`.
- **TC2** `TestWarmBudgetNVENC` — cap 6, live uses 4: staging gets 2; rolling probes the rest.
- **TC3** `TestChecksTSConformity` with crafted TS (missing PAT, B-frames, open GOP, CC errors).
- **TC4** `TestCloudflareIngestRules` — table of profiles.
- **TC5** `TestSpliceCompat` — equal and differing parameters.
- **TC6** `TestVerifyNeverPushes`.
- **TC7** Integration: fake-ffmpeg slots, a change with one renumbered and one regenerated channel,
  report passes; a broken release (wrong fps) fails `ts.conformity`.

## Edge cases

- **EC1** No channel watched and staging on a quiet night: only the probe channel and the rolling
  probes run.
- **EC2** The staging slot crashes during verification: restarted by the watchdog; 3 crashes →
  `failed (slot_crash_loop)`.
- **EC3** A change touching only the guide (titles) of far-future programmes: `sched_changed` but no
  difference before 24 h; warm set = probe only; guide checks carry the verification.

## Dependencies

33.1–33.4; Epic 32.2 placements (for multi-host budget; single host works without).
