---
schema: 1
kind: pr
pr: 527
title: Extract writes no WAV on a streaming STT backend
date: 2026-11-02
session: unknown
source: claude-trail.md
---

## What happened
- Live: 1,323 extract jobs waiting (847 pending, 476 paused), stage paused by hand. Each would write `~/.maktaba/cache/audio/<hash>.wav` (about 115 MB/h) that nothing reads: the engine's transcribe decodes off the share, and the configured backend (`whisper-mlx`) streams.
- Decided: code only, no migration. `extract_handler` asks `stt.transcribe.configured_backend_requires_file()` (backend name resolved as `default_select_backend` does; true only for a class with `requires_file = True`, today `openai-api`). On a streaming backend: `commit_extract_skipped` (no ffmpeg, no file, no `audio_cache` row, no `last_extracted_at`), FSM `PROBED -> AUDIO_EXTRACTED` and the TRANSCRIBE enqueue as after a real extract, except the enqueue is left out when transcription is already handled (active transcript or live transcribe job, the #267 rule); logs `extract_skipped_streaming_backend`. So 0172 resuming the stage is harmless: the backlog finishes in ms.
- Merged at bb4a742b; the tag hold was lifted after it.

## Open
- Follow-up (done in #543): drop the probe->extract edge and probe's eager enqueue, have transcribe request extract via `may_request` when a backend needs a file, delete orphaned WAVs. Note: `--no-engine` mode chains probe->extract->legacy transcribe_handler, which reads `audio_cache`; on a streaming backend that handler now finds no artifact.
