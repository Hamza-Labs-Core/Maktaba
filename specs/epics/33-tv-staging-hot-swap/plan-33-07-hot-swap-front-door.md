# Plan 33.7 — Atomic hot swap in the front door — implementation

> Implementation plan for [story-33-07](story-33-07-hot-swap-front-door.md).
> Design: [design.md](design.md) §8. No new migration (`tv_routing` 0122, `tv_channel_switch` 0124).

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | One commit transaction; per-channel switches after it. | The guide/lineup change atomically; streams switch where no programme is cut. |
| D2 | Make-before-break per channel. | The staging upstream is open and aligned before live is let go. |
| D3 | The front door owns the HLS playlist (stitcher). | Upstream epoch-based sequence numbers and two slots cannot be exposed as one playlist otherwise. |
| D4 | TS splice at a grid IDR, re-timestamp only as fallback. | With 33.4 the splice is exact; the fallback keeps correctness for param changes. |
| D5 | Switch at `B_c` by default. | A programme change already shows a cut; the swap hides inside it. |
| D6 | Epoch fencing for every routing decision. | A delayed or duplicated swap message cannot route to the wrong slot. |

## 1. Files

**Create**
- `streaming/internal/frontdoor/hls/stitcher.go` — per-channel playlist state, upstream polling, switch, render
- `streaming/internal/frontdoor/ts/splice.go` — packet parser (PAT/PMT/PES/adaptation), IDR detection (RAI + NAL type 5), CC rewrite, PTS/DTS/PCR offset, PAT/PMT versioning (extends Epic 32's splicing copier)
- `streaming/internal/frontdoor/swap.go` — `/internal/tv/routing/swap`: load plan, schedule switches, per-channel state machine `planned → switching → switched|skipped`, completion
- `api/internal/tvchange/swap.go` — plan request, re-anchor (calls pipeline `tvrelease.reanchor` via a job or SQL function), commit transaction, enqueue `plex_livetv`
- tests and TS fixtures

**Modify**
- `streaming/internal/frontdoor/route.go` — epoch-aware; per-channel override during a switch
- `streaming/internal/hdhr/lineup.go` — reads `lifecycle = live` (33.3) — already; ensure renumber path
- `pipeline/src/maktaba_pipeline/extensions/plex/livetv.py` — accept `reason: tv_swap` (force channel map + guide reload, no throttle)

## 2. Steps

1. TS parser/splicer with fixtures (aligned, offset, PMT change, audio-before-IDR).
2. Stitcher replacing pass-through (from 33.1), feature-flagged until the swap path is used.
3. Plan + re-anchor + commit in the API; the front door receives `{change_id, epoch}` and reads the plan
   from `tv_channel_switch`.
4. Per-channel switch: open staging upstream (TS for TS clients and relay; playlist for HLS) at
   `switch_at − 10 s`, align, switch at the IDR ≥ `switch_at`.
5. Removed channels: end streams at `B_c`.
6. Rolling swap ordering.
7. Previous-slot timer → drain → stop (slot role transitions).

## 3. Tests

Unit (stitcher, splicer, plan); integration with fake slots; E2E with the fake RTMP ingest (33.8); a
manual matrix on the home in the PR (Plex on Apple TV, Plex Web, Safari, Chrome, VLC).

## 4. Rollout

Phase B: admin-triggered swaps on the home with a test Plex first; then the real Plex.
