---
schema: 1
kind: pr
pr: 537
title: Convert rejects inputs ffprobe only guesses at
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge 387fc9fd. Bug: seeded junk read by ffprobe 8.1 as raw h263 (no duration, 1 frame) re-encoded to a 1-frame MP4 that passed verify (the duration check was skipped when the original has none; frame counts matched), and the original went to trash.
- `unreadable()` (library_mgmt/convert.py) rejects no-container demuxers (`RAW_GUESSES`: raw video, text subs like lrc, text-art), a video stream with no positive duration, or 0 frames. The job fails `ffmpeg_decode` "the original cannot be read" even with re-encoding off; the library plan skips such files as `unreadable`. `verify_media` fails when the original has video but no duration.
- Not gated on ffprobe's probe score (MPEG-PS scores 26, MPEG-TS 50). Fixtures are built (a real H.263 frame plus seeded junk), so they do not depend on the ffmpeg version.

## What we learned
- A `.bin` extension makes ffprobe read anything as `bintext`.
- The scratchpad path is too long for a Postgres Unix socket; start it with `-k ''` and use TCP.
- The bundled Postgres has no `createdb`.
