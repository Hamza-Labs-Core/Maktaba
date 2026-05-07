# Epic 03 — Transcription

**Phase.** Pipeline (M2 — Transcription). The long-running stage — hours
of wall-clock per video — and the primary consumer of the Job Queue
(Epic 6).
**Owner.** Pipeline Service · `pipeline/src/maktaba_pipeline/stt/`
(backends) and `pipeline/src/maktaba_pipeline/pipeline/stages/transcribe.py`
(stage orchestration).

> **Goal.** Convert extracted audio into a sequence of
> `transcript_segments` with second-accurate timestamps; support multiple
> swappable STT backends; durable per-segment commits; pause/resume to
> the exact second; bidi-correct text for Arabic.

Source: [README](../../../specs/epics/03-transcription/README.md) ·
Architecture §3.4 (Transcriber).

---

## Stories

| # | Title | Priority | Linear | Story | Plan |
|---|-------|----------|--------|-------|------|
| 3.1 | STT backend protocol | Gate | [HLB-15](../linear-map.md) | [story-03-01](../../../specs/epics/03-transcription/story-03-01-backend-protocol.md) | [plan-03-01](../../../specs/epics/03-transcription/plan-03-01-backend-protocol.md) |
| 3.2 | Whisper MLX backend (default on Apple Silicon) | Core | [HLB-16](../linear-map.md) | [story-03-02](../../../specs/epics/03-transcription/story-03-02-whisper-mlx-backend.md) | [plan-03-02](../../../specs/epics/03-transcription/plan-03-02-whisper-mlx-backend.md) |
| 3.3 | Faster-Whisper backend (CUDA / CPU) | Core | [HLB-17](../linear-map.md) | [story-03-03](../../../specs/epics/03-transcription/story-03-03-faster-whisper-backend.md) | [plan-03-03](../../../specs/epics/03-transcription/plan-03-03-faster-whisper-backend.md) |
| 3.4 | OpenAI API backend | Core | [HLB-18](../linear-map.md) | [story-03-04](../../../specs/epics/03-transcription/story-03-04-openai-api-backend.md) | [plan-03-04](../../../specs/epics/03-transcription/plan-03-04-openai-api-backend.md) |
| 3.5 | Backend registry, transcript history & per-library selection | Gate | [HLB-19](../linear-map.md) | [story-03-05](../../../specs/epics/03-transcription/story-03-05-backend-registry.md) | [plan-03-05](../../../specs/epics/03-transcription/plan-03-05-backend-registry.md) |
| 3.6 | Real-time per-segment durable commit | Gate | [HLB-20](../linear-map.md) | [story-03-06](../../../specs/epics/03-transcription/story-03-06-segment-commit.md) | [plan-03-06](../../../specs/epics/03-transcription/plan-03-06-segment-commit.md) |
| 3.7 | Pause and resume to the exact second | Core | [HLB-21](../linear-map.md) | [story-03-07](../../../specs/epics/03-transcription/story-03-07-pause-resume.md) | [plan-03-07](../../../specs/epics/03-transcription/plan-03-07-pause-resume.md) |
| 3.8 | Crash recovery & graceful shutdown | Core | [HLB-22](../linear-map.md) | [story-03-08](../../../specs/epics/03-transcription/story-03-08-crash-recovery.md) | [plan-03-08](../../../specs/epics/03-transcription/plan-03-08-crash-recovery.md) |
| 3.9 | Diarization (opt-in) | Polish | [HLB-23](../linear-map.md) | [story-03-09](../../../specs/epics/03-transcription/story-03-09-diarization.md) | [plan-03-09](../../../specs/epics/03-transcription/plan-03-09-diarization.md) |

> Linear IDs from [linear-map.md](../linear-map.md).

### Related mockups & diagrams

| Story | Mockup | Diagram |
|-------|--------|---------|
| 3.1, 3.5 | [admin/library-config.html](../../../web/mockups/admin/library-config.html) (STT profile per library) | [transcription-pipeline.drawio](../../../specs/diagrams/transcription-pipeline.drawio) |
| 3.2, 3.3, 3.4 | [admin/job-pipeline.html](../../../web/mockups/admin/job-pipeline.html) | [transcription-pipeline.drawio](../../../specs/diagrams/transcription-pipeline.drawio) |
| 3.6 | [mockup-11-05-processing-queue](../../../web/mockups/mockup-11-05-processing-queue.html) | [transcription-pipeline.drawio](../../../specs/diagrams/transcription-pipeline.drawio) · [data-flow.drawio](../../../specs/diagrams/data-flow.drawio) |
| 3.7, 3.8 | [admin/job-pipeline.html](../../../web/mockups/admin/job-pipeline.html) | [job-lifecycle.drawio](../../../specs/diagrams/job-lifecycle.drawio) |
| 3.9 | [admin/speaker-manager.html](../../../web/mockups/admin/speaker-manager.html) | [transcription-pipeline.drawio](../../../specs/diagrams/transcription-pipeline.drawio) |

---

## DB tables owned

| Table | Purpose |
|-------|---------|
| `transcripts` | One row per transcription pass on a video. `is_active` (partial UNIQUE) marks the live one; history rows are kept for reprocessing. Tracks backend, model, parameters, progress. |
| `transcript_segments` | Per-segment STT output: text, confidence, `start_sec`, `end_sec`, optional speaker, optional word-level data. Sequence enforced monotonically. |
| `transcript_words` | Optional word-level timestamps + confidences (when the backend supplies them). |
| `stt_usage` | OpenAI-API ledger: per-library monthly USD spend, enforces `max_usd_per_month` cap before claim. |

> Story 3.5 owns the `transcripts.is_active` partial-UNIQUE constraint —
> the resolution to REVIEW §1.1.b/§1.1.i (impossible full UNIQUE on
> `(video_id)`).

---

## API endpoints owned

| Method · Path | Purpose | Story |
|---|---|---|
| `POST /api/jobs/{id}/pause` | Cooperative pause; sets the request flag, the worker stops at the next segment boundary. | 3.7, also 6.4 |
| `POST /api/jobs/{id}/pause?force=true` | Force-pause; orphan cleanup runs after worker abort. | 3.7, 3.8 |
| `POST /api/jobs/{id}/resume` | Resume from `last_segment_end_sec`. | 3.7 |
| `GET /api/system/health` | Adds backend health: `ready`, `model_loaded`, `device`, `version`. | 3.5 |
| `GET /api/transcripts/{id}/backends` | Surface ready backends for the per-library selection UI. | 3.5 |

The HTTP surface for these endpoints is implemented in the API service
(Epic 7); this epic owns the pipeline-side semantics.

---

## gRPC services owned

| Service · RPC | Purpose |
|---|---|
| `Transcriber.Transcribe(TranscribeRequest, stream TranscribeProgress)` | Run a transcription job; per-segment commits go via the in-process `commit_segment()` PL/pgSQL function, not over the wire. |

---

## LISTEN/NOTIFY channels owned

| Channel | Producer | Consumer |
|---------|----------|----------|
| `segments.committed` | `AFTER INSERT` trigger on `transcript_segments` | Epic 5 indexer (Story 5.5) · API → WebSocket |

Payload: `{transcript_id, video_id, library_id, seq, last_segment_end_sec}`.

---

## Dependencies

**Depends on.**
- Epic 01 — `videos.state` machine; the `READY_NO_AUDIO` short-circuit.
- Epic 02 — extracted PCM stream + per-track `detected_language`.
- Epic 06 Stories 6.1–6.4 — claim, heartbeat, request flags.

**Depended on by.**
- Epic 04 — subtitle generation reads `transcript_segments_v` (the active
  view).
- Epic 05 — full-text + vector index listen on `segments.committed`.
- Epic 07 — WebSocket fans out live progress.
- Epic 09 — speaker matching against a known library (deferred to v1.1)
  rides the diarization output from Story 3.9.

---

## Key technical decisions

- **Backend is a Python `Protocol`.** All three backends conform to one
  `STTBackend` interface and run a shared conformance suite. Adding a
  fourth (e.g. Deepgram) is a new module, not a new orchestrator.
- **mlx-whisper on Apple Silicon (default).** ~0.3× real-time. Initial-
  prompt biasing for Arabic religious vocabulary; OOM auto-degradation
  (`large-v3 → medium → small`); hallucination-loop detection.
- **faster-whisper on CUDA / CPU.** ~0.1× RT on NVIDIA, ~3× RT on CPU
  fallback. Compute-type fallback (`float16 → float32`, `int8 →
  float32`) is recorded in transcript metadata.
- **OpenAI API backend.** Network-bound. Size-based chunking at 24 MB,
  silence pre-strip with reversal, exponential backoff on 429/5xx.
  Monthly USD cap enforced *before* claim via `stt_usage`.
- **Atomic per-segment commit.** `commit_segment()` PL/pgSQL function
  inserts the segment + bumps `processing_jobs.last_segment_end_sec` +
  recomputes EWMA real-time factor (α = 0.2) — all one transaction.
  An in-memory `ReorderBuffer` enforces monotonicity.
- **Resume to the exact second.** `start_offset_sec` is supported by all
  three backends; resume re-seeks to `last_segment_end_sec` and the
  `ReorderBuffer` discards anything ≤ that mark.
- **Crash recovery without rework.** Because each segment is committed
  before being yielded, a hard crash loses at most one in-flight
  segment. The reaper (Epic 6 Story 6.6) flips the job to `paused` and
  the next claim resumes cleanly.
- **`is_active` partial-UNIQUE on transcripts.** Allows multiple
  reprocessing rows per `(video_id)` while keeping a single live one.
- **Diarization is opt-in and runs *before* STT.** Sequential by default;
  parallel mode is gated on ≥ 24 GB GPU. `pyannote.audio` is a lazy
  import. Speaker matching against a known library is deferred to v1.1.

---

## Libraries / dependencies introduced

- `mlx-whisper` (Apple Silicon path)
- `faster-whisper >= 1.0` (CUDA / CPU path; depends on `ctranslate2`)
- `openai >= 1.0` (API path)
- `pyannote.audio >= 2.1` (diarization, lazy)
- `audioread`, `librosa` (silence-map for OpenAI chunker)
- `asyncpg`

---

## Test coverage summary

- **Conformance suite** (run against every backend): short Arabic + short
  English fixtures, segment monotonicity, ≥ 90 % audio coverage,
  word-timestamp parity, language detection, pause/resume continuity.
- **mlx-whisper:** initial-prompt biasing, OOM degradation, hallucination
  loop break (Apple-Silicon-only CI).
- **faster-whisper:** CPU always available; CUDA gated on device
  visibility; compute-type fallback recorded in metadata.
- **OpenAI:** budget cap is checked *before* claim; chunk size ≤ 24 MB;
  silence-strip reversal; 429/5xx exponential retry; network errors
  don't poison the job.
- **Commit / pause / crash:** PL/pgSQL atomicity; ReorderBuffer drops
  late segments; pause flag observed post-commit raises `StopWorker`;
  reaper-flipped jobs resume from the recorded `last_segment_end_sec`.
- **Diarization:** off by default (lazy import asserted); sequential by
  default; speaker IDs are stable across runs of the same fixture.
