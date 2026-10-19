# Plan 33.5 — Staging warm-up and verification — implementation

> Implementation plan for [story-33-05](story-33-05-staging-warmup-verification.md).
> Design: [design.md](design.md) §6. No migration (`tv_changes.verify_report`, slot 0123; placements
> `track`, slot 0125).

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | The front door probes; the API aggregates. | The front door already reads slot streams and knows demand. |
| D2 | Demand first, then probe, then rolling changed channels. | Watched channels are what the swap must get right. |
| D3 | Budget from free hardware sessions. | Doubling encodes must never degrade live. |
| D4 | TS checks parse packets in Go, no ffprobe in the hot path. | Cheap, testable with crafted TS; ffprobe only for codec details once per channel. |
| D5 | Cloudflare rules checked even when remote is off (warn). | Turning remote on later must not meet surprises. |

## 1. Files

**Create**
- `streaming/internal/frontdoor/warm.go` — warm set, holds, staging placements
- `streaming/internal/frontdoor/verify/` — `ts.go` (PAT/PMT, PIDs, IDR grid, GOP, B-frames, CC, PCR), `hls.go`, `wallclock.go`, `params.go` (codec params via one ffprobe per channel), `cloudflare.go`, `report.go`
- `streaming/internal/frontdoor/internal_verify.go` — `GET /internal/tv/verify/{channel}`
- `api/internal/tvchange/verify.go` — orchestrates: start slot, poll front door, guide checks (XMLTV render of N+1), lineup sanity, soak, write report, transitions
- tests with crafted TS fixtures under `testdata/`

**Modify**
- `streaming/internal/slot/status.go` — report block id, position, PTS of last IDR, params, encoder per station
- `streaming/internal/placement` — `choose` for `track = staging` honours the budget

## 2. Steps

1. Slot status fields.
2. Warm-set loop (5 s) and holds.
3. Probes: TS 10 s sample per channel per minute; HLS playlist poll; wallclock and block checks against
   the DB (`sched_gen` of the release) and against live's status for unchanged channels.
4. Params + Cloudflare rules + splice compatibility + push continuity.
5. Guide coverage/vs-stream and lineup sanity in the API.
6. Soak and report; `ready` subset loop.

## 3. Tests

Unit per check with fixtures; budget and warm-set tests with fake hosts; integration with fake-ffmpeg
slots (extend `fakeffmpeg_test.go` to emit grid-aligned TS with configurable defects).

## 4. Rollout

Phase B, admin-triggered changes only.
