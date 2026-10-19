# Story 33.1 — Two-slot TV runtime

## Description

*As the owner, I want the processes that encode my channels to be separate from the process that my
viewers connect to, and to have two of them, so that one can be replaced while the other keeps
airing.*

The streaming role is split: the **front door** (SSDP, HDHomeRun, XMLTV/M3U, HLS and TS to clients,
on-demand video sessions) stays in `maktaba-streaming serve`; the channel engine runs in **slot**
processes `maktaba-streaming slot --slot blue|green`, supervised as the new `tv-slot` role. With
staging off there is one slot (blue, live) and nothing visible changes.

Design: [design.md](design.md) §1, §2.

## Acceptance criteria

- **AC1** `maktaba-server serve` starts `tv-slot@blue` always and `tv-slot@green` when
  `tv_staging.enabled`; each slot is probed by the process watchdog (`/healthz` on 9111/9112) and
  restarted with backoff on crash or hang.
- **AC2** Slots listen on loopback only on the home (8791/8792); a request to a slot from another
  address is refused. On a node, slots are reachable only with a front-door signature (Epic 32 §4.4)
  under `/slot/<name>/`.
- **AC3** The front door routes every channel request to `(host, slot)` from `tv_routing` and the
  live placement; HLS playlists, segments, `live.ts` and `/auto/v{n}` work exactly as before for
  viewers (same URLs, same latency within 100 ms).
- **AC4** Each slot writes `tv_slots` every 5 s (`tv_slot_beat`), with its role, state, release,
  version, warm set and encoders used; a slot missing 3 beats is `unhealthy`.
- **AC5** A slot starts a station only for a placement naming its host, slot and track; a slot whose
  release does not match `tv_slots.release_id` runs no station (`release_mismatch`).
- **AC6** Each slot writes HLS output under `<cache>/tv/<slot>/<channel>/`; the two slots never share
  a directory.
- **AC7** Killing the front door leaves slot stations running; after the watchdog restarts it,
  playback resumes without a new encode warm-up (first segment served < 1 s after restart).
- **AC8** With `tv_staging.enabled = false`, the lineup, guide, Plex and the web player behave as on
  `main` (regression suite green).

## Test cases

- **TC1** `TestSupervisorStartsSlots` — roles and instances by setting.
- **TC2** `TestSlotLoopbackOnly`.
- **TC3** `TestFrontDoorRoutesToLiveSlot` — fake slots on two ports; routing row decides.
- **TC4** `TestSlotFenceTrackAndRelease`.
- **TC5** `TestFrontDoorRestartKeepsStations` (integration, fake ffmpeg).
- **TC6** Existing `channel` and `hdhr` suites pass against front door + one slot.

## Edge cases

- **EC1** Port in use: the slot fails `/readyz`, the watchdog backs off, an event names the port.
- **EC2** Upgrading from a single-process install: on first start the supervisor creates `tv_slots`
  rows, release 1 (story 33.2) and routing epoch 1; stations previously on air are restarted once
  (one stall, at install time only).
- **EC3** Windows: slot processes are in the supervisor's job object so they die with it (watchdog
  behaviour).

## Dependencies

`feat/process-watchdog`, `feat/tv-core`; Epic 32.3 front door package (or created here).
