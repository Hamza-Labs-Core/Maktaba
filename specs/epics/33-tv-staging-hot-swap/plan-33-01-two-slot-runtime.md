# Plan 33.1 — Slot TV runtime (live, hot standby, staging) — implementation

> Implementation plan for [story-33-01](story-33-01-two-slot-runtime.md).
> Design: [design.md](design.md) §1, §2. Migration: slot 0129 (`tv_slots`, placement `track`/`slot`).

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Slots are OS processes, not two engines in one process. | A crash or a new binary must not touch the live slot. |
| D2 | The front door keeps every client socket and the on-demand sessions. | Client connections survive a slot swap and a slot crash. |
| D3 | Same binary, new subcommand `slot`. | One build; the release picks the version (33.2). |
| D4 | Loopback on the home; signed prefix on nodes. | No new exposed port; reuses Epic 32's HMAC. |
| D5 | Hot standby on by default for the home (owner). | Instant failover on a live crash; costs a second encode of the warm channels. |
| D6 | Three instances with rotating roles. | The standby stays while a change is staged; staging is the third. |
| D7 | Failover reuses the swap's splice, stitch and relay switch. | One continuity path for swaps, rollbacks and crashes. |

## 1. Files

**Create**
- `streaming/cmd_slot.go` — `slot` subcommand: flags `--slot`, `--listen`, `--admin`; builds the Engine with a release-bound repo (33.2), mounts `/stream/channel/{id}/…` and `/internal/tv/slot/*`
- `streaming/internal/slot/status.go` — `GET /internal/tv/slot/status`, `POST …/warm`, `POST …/drain`
- `streaming/internal/slot/beat.go` — `tv_slot_beat` loop
- `streaming/internal/frontdoor/route.go` — `Route(channel) → upstream{host, slot, base, epoch}` from `tv_routing` + placements, cache 2 s, LISTEN `tv_routing`/`tv_placement`
- `streaming/internal/frontdoor/standby.go` — standby warm set (budget order live → standby), failover on live-slot loss (epoch + 1, all channels switch at once)
- `shared/db/migrations/0129_tv_slots.sql` (+ `.sqlite.sql`)
- tests

**Modify**
- `cmd/maktaba-server/internal/supervisor/supervisor.go` — role `tv-slot` with three instances started on demand; `ProbeURL` per instance; ordering (slots before streaming on start, after on stop); report a slot exit to the front door at once (faster than the probe)
- `cmd/maktaba-server/cmd_serve.go` — `--role tv-slot --slot blue` for single-role runs
- `streaming/main.go` — engine construction moves to `cmd_slot.go`; serve mode wires front door handlers to `frontdoor.Route`
- `streaming/internal/hdhr/handler.go` — `Streamer` implemented by the front door's splicing copier
- `streaming/internal/channel/engine.go` — fence with `track`/`slot`; output dir root per slot
- `streaming/internal/channel/registry.go` — synthetic holds for warm
- Epic 32 `streaming/internal/placement` — `track`, `slot` columns

## 2. Steps

1. Migration 0129: `tv_slots`, `tv_slot_beat`, `tv_channel_switch`, placement columns, PK change
   (if Epic 32's placement migration (0121) is not merged yet, fold the columns into it and keep 0129 for the rest).
2. `slot` subcommand with the existing Engine; output root `<cache>/tv/<slot>`.
3. Front door router: a single-host install has placements implicitly `home/blue/live`
   (no placement rows needed when Epic 32 is not on; `Route` answers the live slot).
4. HLS: the front door proxies the slot's playlist and segments (stitching comes in 33.7; until then
   pass-through with `/s/{slot}/` URIs rewritten).
5. TS: hdhr `Auto` and `live.ts` through the splicing copier (one upstream).
6. Supervisor: instances, probes, start order; `hot_standby` read from settings at boot and on change
   (start/stop the standby); the third instance on request of the change controller.
8. Hot standby: warm set, budget order, failover path (uses the splicer/stitcher; until 33.7 lands the
   failover is the Epic 32 restart-style splice).
7. Bootstrap (EC2): routing epoch 1, slot blue live with release 1.

## 3. Tests

Supervisor unit tests with fake binaries; front-door routing with two `httptest` slots; fence tests in
`channel`; integration: front door + slot with fake ffmpeg, kill/restart front door and check the
first segment latency; the whole existing streaming suite through the front door.

## 4. Rollout

Phase A: live slot + hot standby. Measure the added hop (loopback proxy) on the On air page; budget < 5 ms per
segment request.
