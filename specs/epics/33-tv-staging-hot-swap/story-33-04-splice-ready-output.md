# Story 33.4 — Splice-ready output

## Description

*As a viewer, I want the switch from one TV process to the other to be invisible, which needs both
processes to produce the same timestamps and keyframes for the same instant.*

Stations stamp PTS/DTS/PCR from wall-clock time with a fixed channel epoch, force IDRs on a fixed
wall-clock grid and use fixed PIDs. Two slots (or two hosts) airing the same channel then produce
frame-aligned streams, and the splice at a swap, a rollback or a failover is exact.

Design: [design.md](design.md) §5.

## Acceptance criteria

- **AC1** `in.TSOffsetSec` is derived from wall-clock (Unix epoch), not the station's start; two stations
  of the same channel started 37 s apart produce the same PTS (± 1 tick) for the same frame instant.
- **AC2** IDRs fall on wall-clock multiples of `tv_output.keyint_seconds` (default 2, range 1–10) in
  every block, including after a block boundary and a muxer restart; GOPs are closed; no B-frames.
- **AC3** PMT PID, video/audio PIDs and service id are fixed in the arguments (`-mpegts_pmt_start_pid`,
  `-mpegts_start_pid`, `-mpegts_service_id`) and reported by the slot status.
- **AC4** HLS segments start on grid IDRs; `EXT-X-PROGRAM-DATE-TIME` of corresponding segments in two
  slots are equal to the millisecond.
- **AC5** PTS wrap (2³³ ticks ≈ 26.5 h) is handled: the muxer, the HLS output and the front door's
  splicer accept the wrap (test across a wrap with a fake clock).
- **AC6** No change for viewers: Plex, VLC, Safari and hls.js play as before (manual matrix in the PR).

## Test cases

- **TC1** `TestTSOffsetWallClock` — args for two start times give consistent offsets.
- **TC2** `TestForceKeyFramesGrid` — expression and offsets per block.
- **TC3** `TestFixedPIDs` — `ffprobe` of fake/real output (real ffmpeg job).
- **TC4** Integration (real ffmpeg, short synthetic source): two stations, compare PTS of IDRs and PDT
  of segments.
- **TC5** `TestPTSWrap`.

## Edge cases

- **EC1** Live blocks (yt-dlp input) re-encode too, so the grid holds; a source with variable frame
  rate is normalised by the existing `fps` filter.
- **EC2** VideoToolbox ignoring forced keyframes on some macOS versions: verification (33.5) checks the
  grid; fallback `-g` with `-force_key_frames` both set; documented if a version misbehaves.
- **EC3** A release that changes `keyint_seconds` or fps cannot align with live: the splicer
  re-timestamps (33.7) and verification reports `splice.compat = param_change`.

## Dependencies

`feat/tv-core` encoder arguments (`blockargs.go`).
