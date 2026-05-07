# Epic 02 — Audio Extraction

**Phase.** Pipeline (M1 — Audio).
**Owner.** Pipeline Service · `pipeline/src/maktaba_pipeline/media/`
(`ffmpeg.py`, `audio.py`) and the `extract` stage at
`pipeline/src/maktaba_pipeline/pipeline/stages/extract.py`. The
`MediaService.Probe` RPC binding is in `internal/ffmpeg/probe` (Go-side,
shared with Streaming).

> **Goal.** From a probed video, pick the right audio track, extract it as
> mono 16 kHz signed-16-bit PCM, and feed it into the transcriber
> **without** an intermediate WAV when the backend can consume a stream.
> Record what we extracted in `audio_tracks` so a transcript can be tied
> back to the exact track even if the file later changes.

Source: [README](../../../specs/epics/02-audio-extraction/README.md) ·
Architecture §3.2 (Probe), §3.3 (Audio Extractor), §8.1 (`audio_tracks`,
`media_info`).

---

## Stories

| # | Title | Priority | Linear | Story | Plan |
|---|-------|----------|--------|-------|------|
| 2.1 | Probe the audio tracks | Core | [HLB-11](../linear-map.md) | [story-02-01](../../../specs/epics/02-audio-extraction/story-02-01-audio-probe.md) | [plan-02-01](../../../specs/epics/02-audio-extraction/plan-02-01-audio-probe.md) · [plan-02-01-ffprobe-binding](../../../specs/epics/02-audio-extraction/plan-02-01-ffprobe-binding.md) |
| 2.2 | Track selection | Core | [HLB-12](../linear-map.md) | [story-02-02](../../../specs/epics/02-audio-extraction/story-02-02-track-selection.md) | [plan-02-02](../../../specs/epics/02-audio-extraction/plan-02-02-track-selection.md) |
| 2.3 | Stream extraction (no intermediate WAV) | Core | [HLB-13](../linear-map.md) | [story-02-03](../../../specs/epics/02-audio-extraction/story-02-03-stream-extraction.md) | [plan-02-03](../../../specs/epics/02-audio-extraction/plan-02-03-stream-extraction.md) |
| 2.4 | Audio resource accounting | Polish | [HLB-14](../linear-map.md) | [story-02-04](../../../specs/epics/02-audio-extraction/story-02-04-resource-accounting.md) | [plan-02-04](../../../specs/epics/02-audio-extraction/plan-02-04-resource-accounting.md) |

> Story 2.1 has two plan files because the Go FFprobe binding and the
> Python `probe` stage were planned as a pair. Linear IDs from
> [linear-map.md](../linear-map.md).

### Related mockups & diagrams

| Story | Mockup | Diagram |
|-------|--------|---------|
| 2.1, 2.2 | [admin/job-pipeline.html](../../../web/mockups/admin/job-pipeline.html) | [pipeline-stories.drawio](../../../specs/diagrams/pipeline-stories.drawio) |
| 2.3 | [admin/job-pipeline.html](../../../web/mockups/admin/job-pipeline.html) | [data-flow.drawio](../../../specs/diagrams/data-flow.drawio) · [transcription-pipeline.drawio](../../../specs/diagrams/transcription-pipeline.drawio) |
| 2.4 | — | — |

---

## DB tables owned

| Table | Purpose |
|-------|---------|
| `media_info` | Per-video container, video codec, resolution, fps, bitrate, has-subtitles flag, raw `ffprobe` JSONB. Probe cache. |
| `audio_tracks` | One row per audio stream — index, codec, channels, sample rate, language (ISO 639-3), title, `is_default`, disposition, `detected_language`. |
| `track_selection_decisions` | Audit trail of which track(s) were chosen and why; rejected tracks with rationale. |

---

## API endpoints owned

| Method · Path | Purpose | Story |
|---|---|---|
| `GET /api/videos/{id}/tracks` | Preview the auto-selected track + alternatives, for an admin/library override UI. | 2.2 |
| `PUT /api/videos/{id}/tracks/override` | User-pinned track selection; supersedes auto-selection on the next extract. | 2.2 |
| `POST /api/diagnostics/extract-args` | Operator endpoint: returns the exact FFmpeg argv that *would* run for a video. | 2.3 |

---

## gRPC services owned

| Service · RPC | Purpose |
|---|---|
| `MediaService.Probe(ProbeRequest) → ProbeResponse` | Exposes the Go FFprobe parser to the Python pipeline (and Streaming, for direct-play decisions); returns container, duration, video stream, audio tracks, subtitles, chapters. |

---

## LISTEN/NOTIFY channels

This epic does not own a notify channel. State transitions emit
`videos.state_changed` (owned by Epic 1 Story 1.6).

---

## Dependencies

**Depends on.**
- Epic 01 Stories 1.1, 1.2 — a video in `DISCOVERED` is the precondition
  for `probe`.
- Epic 06 Stories 6.1–6.3 — claim loop, heartbeat, semaphore.

**Depended on by.**
- Epic 03 (Transcription) — consumes the streamed PCM and the per-track
  `detected_language`.
- Epic 04 (Subtitles) — Story 4.4 reads `media_info.raw_ffprobe` to find
  embedded subtitle stream indexes.
- Epic 07 (API) — track-preview UI and operator diagnostics.

---

## Key technical decisions

- **Mono 16-bit 16 kHz PCM by default.** The shape Whisper requires.
  No intermediate WAV is written unless an STT backend declares
  `requires_file = true`.
- **Streaming via FFmpeg subprocess + asyncio.** 64 KiB chunks piped
  straight into the transcriber's iterator — zero-copy through the worker
  process.
- **Probe is metadata-only.** No disk writes, no decoder warm-up. The
  state transition (`DISCOVERED → PROBED` or `READY_NO_AUDIO`) and the
  `extract` job enqueue happen atomically inside one Postgres
  transaction.
- **Track selection is a deterministic priority list.** User override →
  preferred language → Arabic → `default` disposition → first by index.
  The decision is recorded so a later "why this track?" query is cheap.
- **Two-stage concurrency caps.** Probe is unlimited (≤ 500 ms, cheap).
  Extract defaults to 2-per-process (disk-bound, competes with
  Streaming). CPU-pressure throttling exists but ships disabled in v1.
- **No shell, ever.** FFmpeg invocations go through `exec.CommandContext`
  with positional argv and a `--` sentinel.

---

## Libraries / dependencies introduced

- **Go:** `internal/ffmpeg/probe` (parser shared with Streaming),
  `internal/ffmpeg/extract` (command builder).
- **Python:** `langcodes` (ISO 639 normalisation), `whisper-cpp-python`
  (lightweight language detection for `und` tracks), `asyncpg`.
- **Shared:** existing `ffmpeg` and `ffprobe` binaries; JSON-schema
  fixtures for the parser.

---

## Test coverage summary

- **Parser (unit):** `und` language fallback, multi-track, chapters,
  disposition flags, idempotent replay over the same JSONB.
- **Extract (integration):** real ffprobe + FFmpeg on four fixture clips
  (lecture, multi-audio, silent, corrupt) on Linux and macOS CI with
  ffprobe 6.x and 7.x.
- **Hardening:** shell-injection attempts on filenames are inert;
  signal handling on pause/cancel cleans up the FFmpeg child.
- **Invariants:** one `audio_tracks` row per stream; state transitions
  are guarded by conditional `UPDATE`; no extract job is enqueued for
  audio-less video; the probe row + state change + job enqueue happen in
  a single transaction.
