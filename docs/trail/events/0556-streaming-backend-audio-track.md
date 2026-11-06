---
schema: 1
kind: pr
pr: 556
title: A streaming STT backend decodes the chosen audio track
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge 9fb8b4290, a gap left by #543. With `--no-engine` and no `audio_cache` row, whisper-mlx decoded the source's default audio stream while the transcript was labelled with extract's chosen track (wrong language on Arabic + English files).
- `AudioArtifact.stream_index` / `TranscriptionHints.audio_stream` carry the chosen `-map 0:a:N` when a file has several streams; whisper-mlx and faster-whisper decode just that stream via ffmpeg into memory (no WAV).
- The engine transcribe stage had the same gap (always `0:a:0` as track 0): it now picks among the probed streams (`media_info.raw_ffprobe`, not `audio_tracks` rows, which can outlive a replaced file).
- Tests: an ffmpeg-made two-tone two-track file (440 Hz en default, 1000 Hz ar) checks the decoded frequency on the legacy handler, both backends and the engine.

## What we learned
- `audio_tracks` rows can outlive a replaced file; read the probed streams instead.

## Open
- pytest stopped at about 82 % with no summary when `tests/cluster` ran with the other folders in one call (fixed in #565).
