# Epic 03 — Transcription

**Goal.** Convert extracted audio into a sequence of `transcript_segments`
with second-accurate timestamps, supporting multiple swappable STT
backends, durable per-segment commits, exact pause/resume to the audio
second, and bidi-correct text for Arabic. This is the long-running stage
of the pipeline (hours per video) and the primary consumer of
[Epic 6](../06-job-queue/README.md)'s machinery.

**Owner.** Pipeline Service, `pipeline/src/maktaba_pipeline/stt/` (backends)
and `pipeline/src/maktaba_pipeline/pipeline/stages/transcribe.py` (stage
orchestration).

**Out of scope.** Subtitle file generation (Epic 4), search indexing
(Epic 5), matching diarized speakers to a known speaker library (deferred
to v1.1).

## Stories

| # | Title | File |
|---|-------|------|
| 3.1 | STT backend protocol | [story-03-01-backend-protocol.md](story-03-01-backend-protocol.md) |
| 3.2 | Whisper MLX backend (default on Apple Silicon) | [story-03-02-whisper-mlx-backend.md](story-03-02-whisper-mlx-backend.md) |
| 3.3 | Faster-Whisper (CUDA / CPU) backend | [story-03-03-faster-whisper-backend.md](story-03-03-faster-whisper-backend.md) |
| 3.4 | OpenAI API backend | [story-03-04-openai-api-backend.md](story-03-04-openai-api-backend.md) |
| 3.5 | Backend registry, transcript history & per-library selection | [story-03-05-backend-registry.md](story-03-05-backend-registry.md) |
| 3.6 | Real-time per-segment durable commit | [story-03-06-segment-commit.md](story-03-06-segment-commit.md) |
| 3.7 | Pause and resume to the exact second | [story-03-07-pause-resume.md](story-03-07-pause-resume.md) |
| 3.8 | Crash recovery & graceful shutdown | [story-03-08-crash-recovery.md](story-03-08-crash-recovery.md) |
| 3.9 | Diarization (opt-in, off by default) | [story-03-09-diarization.md](story-03-09-diarization.md) |

## Dependency notes

- 3.1 (the protocol) unblocks 3.2, 3.3, 3.4, all of which feed 3.5.
- 3.5 owns the `transcripts.is_active` schema (resolves REVIEW §1.1.b
  and §1.1.i) and is the single owner of the partial-UNIQUE constraint
  that allows reprocessing history.
- 3.6, 3.7, 3.8 are the correctness keystones — every other epic that
  reads transcripts depends on the per-segment-commit invariant they
  uphold.
- Depends on Epic 2 Story 2.3 for the audio stream and on
  [Epic 6](../06-job-queue/README.md) Stories 6.1–6.4 for queue mechanics.
