---
schema: 1
kind: pr
pr: 477
title: Streams keep their audio when transcribe wrote the track row first
date: 2026-11-01
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Transcribe's bare `audio_tracks` row (from 68e9e903e, plan 03-10) plus the probe's `ON CONFLICT DO NOTHING` left 32,381 of 148,589 videos with a codec-less default track, which streaming read as no audio.
Now the probe does `DO UPDATE`, transcribe copies probe facts, streaming's `has_audio` also reads `raw_ffprobe`, and migration 0171 (`0171_audio_tracks_backfill_codec`) backfills.

## What we learned
- Rejected: a per-play `raw_ffprobe` fallback, because it hides bad rows.
- `jsonb_array_elements(...) s` let a bare `s` resolve to an outer column. Use `AS au(stream)`.
- A third SchemaRev lives in `schemaguard/guard.go`.
