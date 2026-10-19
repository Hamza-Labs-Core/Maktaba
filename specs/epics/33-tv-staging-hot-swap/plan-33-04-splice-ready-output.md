# Plan 33.4 — Splice-ready output — implementation

> Implementation plan for [story-33-04](story-33-04-splice-ready-output.md).
> Design: [design.md](design.md) §5. No migration (`keyint_seconds` is a `rules.tv_output` key).

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Unix epoch as the channel timeline epoch. | Deterministic across slots and hosts, no coordination needed. |
| D2 | Grid IDRs by `-force_key_frames` with the block's wall-clock offset. | Works with libx264, NVENC and VideoToolbox; keeps tv-core's CBR settings. |
| D3 | Fixed PIDs in arguments. | A new binary or ffmpeg cannot silently renumber streams under a splice. |
| D4 | Keep `+initial_discontinuity` per block. | The muxer still sees block-level CC resets; the front door handles client continuity. |

## 1. Files

**Modify**
- `streaming/internal/channel/blockargs.go` — `TSOffsetSec` from wall clock; `videoEncoderArgs` keyframe expression `gte(t+off,n_forced*k)`; `-flags +cgop`; fixed PIDs in the block and muxer args
- `streaming/internal/channel/station.go` — `in.TSOffsetSec = float64(start.UnixNano())/1e9 mod wrap`; `ptsTracker` aware of wrap
- `streaming/internal/channel/settings.go` (tv-core) — `keyint_seconds` (default 2, 1–10), schema + UI hint
- `api/internal/handlers/rules/schema.json` — the key
- tests (`blockargs_test.go`, `tvcore_test.go`, new `align_test.go`)

## 2. Steps

1. Offset and keyframe expression; unit tests on arguments.
2. Muxer `-copyts` already keeps input timestamps; verify the HLS muxer's PDT comes from the wall clock
   (`program_date_time` uses the system clock at segment start — check that it equals the PTS-derived
   time; if not, compute PDT in the front door from PTS, 33.7).
3. Real-ffmpeg integration test (CI job with ffmpeg) comparing two stations.
4. Wrap test with a fake clock near 2³³.

## 3. Tests

As in the story. The real-ffmpeg job runs libx264; VideoToolbox and NVENC are checked manually on the
home and the PC (results in the PR).

## 4. Rollout

Phase A, on for everyone (no setting): it changes timestamps only. Watch the On air page for muxer
restarts after deploy.
