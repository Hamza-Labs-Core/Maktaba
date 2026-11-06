---
schema: 1
kind: pr
pr: 611
title: The second listen is its own stage; an 8 GB card takes turns
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 44524a4ef. `transcribe` stores Whisper's transcript with its word timings, and `second_listen` corrects it from that transcript and the audio, so subtitles, the index and tags run once on the corrected words.
- On a GPU too small for both models (the 8 GB RTX 2080 SUPER on Windows: 6.95 GiB free, Cohere needs 6.06), Whisper and Cohere take turns, one batch each (max 30 min), with the idle model's weights parked in system memory (`stt/gpu_turns.py`). The choice comes from the driver's measured free memory, not the OS.
- Also fixed: re-running Whisper replaced the corrected transcript and never re-queued the second listen when the text matched.
- Measured on the M3 Ultra with a fixture: Whisper 43-48x, second listen 91-116x; the 2080 SUPER is about 27x combined (estimate, PC unreachable).
- Migration 0174 `second_listen_stage` seeds the stage (SchemaRev 174).

## What we learned
- Decide GPU fit from the driver's measured free memory, not the OS's total.
