---
schema: 1
kind: pr
pr: 468
title: "48.4: subtitles as HLS renditions"
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
A video session's master playlist lists its sidecar subtitles as HLS `SUBTITLES` renditions (a one-segment WebVTT playlist at `subs-{lang}/index.m3u8` over the sidecar route), so AVPlayer, AirPlay and Cast can show them.
No `stream_gate.go` change was needed: both paths fit its patterns (a test copies the regexes).
The API never sent `session.subtitles`, so the web's `<track>` list was always empty; hls.js now gives the web subtitles through the renditions.

## What we learned
- Failed first: without `X-TIMESTAMP-MAP`, AVPlayer and hls.js showed every cue 1.4 s early. ffmpeg's MPEG-TS starts 1.4 s in, and `-output_ts_offset` keeps video time.
- Fix: the sidecar route with `?hls=1` adds `MPEGTS:126000`. Checked with AVPlayer on a real transcode from 30 s (cues on time) and a real-ffmpeg test in `tools/media-e2e.sh`.
