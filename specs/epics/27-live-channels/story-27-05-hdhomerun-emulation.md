# Story 27.5 — HDHomeRun emulation

## Description

Make Maktaba's channels discoverable by **Plex DVR, Jellyfin Live TV, and
Emby** with **zero configuration**, by emulating a
[SiliconDust HDHomeRun](https://www.silicondust.com/) network tuner. To
those media servers, a network tuner is a box on the LAN that announces
itself over SSDP, answers a small JSON discovery protocol, and serves a
continuous **MPEG-TS** stream per channel. We speak exactly that
protocol; the "tuner" is virtual and its channels are Maktaba's.

This lives in the **Streaming Service** (Go): it is fundamentally about
serving video (an MPEG-TS mux per tuner connection, joined at the
wall-clock offset like the HLS engine in
[27.3](story-27-03-live-stream-engine.md)), and the streaming binary is
already reachable on the LAN, which SSDP discovery requires.

The companion **XMLTV guide** that Plex/Jellyfin pair with this tuner is
served by [27.4](story-27-04-epg-generation.md); together they give a
full Live TV + guide experience inside those apps.

## Protocol surface

| Endpoint | Purpose |
|----------|---------|
| SSDP responder (udp/1900) | Answers UPnP `M-SEARCH` so the device is auto-discovered |
| `GET /device.xml` | UPnP device description (referenced by SSDP `LOCATION`) |
| `GET /discover.json` | Device info: `FriendlyName`, `DeviceID`, `TunerCount`, `BaseURL`, `LineupURL`, `DeviceAuth` |
| `GET /lineup.json` | Channel lineup: `[{GuideNumber, GuideName, URL}]` |
| `GET /lineup_status.json` | Tuner/scan status: `{ScanInProgress, ScanPossible, Source, SourceList}` |
| `POST /lineup.post?scan=start` | Channel-scan ack (no-op; returns immediately) |
| `GET /auto/v{channel}` | Continuous **MPEG-TS** stream for one tuner connection |

## Acceptance criteria

- **AC1** The streaming binary answers UPnP **SSDP** `M-SEARCH` on
  udp/1900 with a response pointing at `/device.xml`, so Plex/Jellyfin's
  "scan for tuners" discovers Maktaba **without** the user typing an IP.
- **AC2** `GET /discover.json` returns valid HDHomeRun device JSON: a
  stable `DeviceID` (derived once, persisted in `hdhr_device`), a
  configurable `FriendlyName`, the configured `TunerCount`, and correct
  `BaseURL`/`LineupURL` for the host the request arrived on (so it works
  over LAN IP and via the relay host alike).
- **AC3** `GET /lineup.json` lists every **enabled** channel as
  `{GuideNumber: <number>, GuideName: <name>, URL: <…/auto/v{number}>}`,
  scoped to the channels the **access token** permits (see AC8).
- **AC4** `GET /lineup_status.json` reports a sane idle status
  (`ScanInProgress: 0`, `ScanPossible: 1`); `POST /lineup.post?scan=start`
  returns success immediately (we have nothing to scan, but Plex expects
  the call to succeed).
- **AC5** `GET /auto/v{channel}` returns a **continuous MPEG-TS** stream
  for that channel, **joined at the current wall-clock offset** (same
  scheduling truth as the HLS engine), produced by an FFmpeg `-f mpegts`
  mux. The stream is open-ended and survives program boundaries
  seamlessly (reusing the concat look-ahead from
  [27.3](story-27-03-live-stream-engine.md)).
- **AC6** Each `/auto/v{channel}` connection consumes one **tuner
  lease**; concurrent connections beyond `TunerCount` are refused with
  the HDHomeRun "all tuners in use" response, so Plex shows the expected
  "no available tuners" rather than a broken stream.
- **AC7** A tuner connection is **lazily activated** and torn down when
  the consumer disconnects (the lease is released, the MPEG-TS FFmpeg
  child stopped) — reusing the lazy-activation/reaper model from
  [27.3](story-27-03-live-stream-engine.md); an idle tuner costs nothing.
- **AC8** HDHomeRun endpoints are **auth-scoped**: discovery is harmless
  metadata, but `/auto/v{ch}` requires the stream auth model (a
  per-device access token embedded in the `BaseURL`/lineup URLs), so an
  arbitrary LAN client cannot pull channels the operator didn't expose.
  The token scopes which channels appear in `/lineup.json` and which
  `/auto/v{ch}` will serve.
- **AC9** The feature is **toggleable** (`hdhr_device.enabled`): off by
  default for a fresh install (the operator opts in), and when off the
  SSDP responder is silent and the endpoints 404.
- **AC10** The same channel can be consumed **simultaneously** via HLS
  (Maktaba's own player) and via HDHomeRun MPEG-TS (Plex) — they share
  the schedule and the warm encoder where possible but are independent
  outputs; one disconnecting doesn't drop the other.

## Test cases

- **TC1** `test_ssdp_responds_to_msearch` — a simulated `M-SEARCH` →
  response with `ST: ...HDHomeRun...` and a `LOCATION` to `/device.xml`.
- **TC2** `test_discover_json_shape` — `/discover.json` has all required
  keys; `DeviceID` stable across restarts (persisted); `BaseURL` matches
  request host.
- **TC3** `test_lineup_lists_enabled_channels` — only enabled,
  token-permitted channels; `URL` points at `/auto/v{number}`.
- **TC4** `test_lineup_status_idle` — reports not-scanning, scan-possible;
  `lineup.post?scan=start` → 200.
- **TC5** `test_auto_stream_is_mpegts` — `/auto/v5` → `Content-Type`
  video/mpeg-ts, an open-ended TS packet stream; first packets arrive
  promptly.
- **TC6** `test_auto_join_walclock` — with a fake clock, the TS stream
  starts at the channel's current program offset (probe first GOP
  timestamp ≈ expected).
- **TC7** `test_tuner_lease_cap` — `TunerCount=2`; a 3rd concurrent
  `/auto` → "all tuners in use"; leases tracked in `hdhr_tuner_leases`.
- **TC8** `test_lease_released_on_disconnect` — drop a connection →
  lease freed, FFmpeg child reaped, tuner available again.
- **TC9** `test_auth_scopes_lineup_and_stream` — a token scoped to
  channels 1–3 → lineup omits 4+, `/auto/v4` rejected.
- **TC10** `test_disabled_feature_silent` — `enabled=false` → SSDP
  silent, endpoints 404.
- **TC11** `test_hls_and_ts_concurrent` — same channel pulled via HLS and
  via `/auto` at once; both play; closing one leaves the other running.

## Edge cases

- **EC1 Plex caches DeviceID.** `DeviceID` must be **stable** — Plex
  binds its DVR to it; a changing id makes Plex think the tuner vanished.
  Persist it in `hdhr_device` on first boot.
- **EC2 BaseURL behind the relay.** When reached via the Epic 25 relay
  host (not the LAN IP), `discover.json`/`lineup.json` must advertise the
  relay-reachable `BaseURL`, or Plex will try an unreachable LAN IP.
  Derive `BaseURL` from the request, not a static config.
- **EC3 Plex probes with a short read then reconnects.** Plex frequently
  opens `/auto`, reads a few seconds to detect format, closes, and
  reopens. The lazy-activation grace window must keep the encoder warm
  briefly so the reconnect is instant and doesn't burn a fresh transcode
  each probe.
- **EC4 TunerCount vs. host capacity.** `TunerCount` advertised to Plex
  must not exceed what the per-host concurrent-channel cap
  ([27.3](story-27-03-live-stream-engine.md) AC7) can actually transcode;
  the config validates `TunerCount ≤ cap`.
- **EC5 MPEG-TS PID/PCR continuity across program boundaries.** The
  mux must maintain continuity counters / PCR monotonicity across concat
  boundaries so Plex doesn't log corruption; normalise via the transcode
  ladder rather than stream-copying heterogeneous sources.
- **EC6 SSDP on multi-homed / containerised hosts.** The responder binds
  the correct interface(s); in Docker, document the host-network / SSDP
  reflector requirement (multicast doesn't cross bridge networks).
- **EC7 Jellyfin vs. Emby vs. Plex quirks.** Each consumes the protocol
  slightly differently (Jellyfin wants `tvg-id` matching the XMLTV;
  Emby's tuner setup differs). Lineup/discover fields chosen to satisfy
  all three; documented in the plan.
- **EC8 Channel renumber while Plex is bound.** Plex maps its guide by
  `GuideNumber`; renumbering a channel changes the lineup — document that
  the user re-runs Plex's channel mapping after a renumber (same as a real
  tuner rescan).
