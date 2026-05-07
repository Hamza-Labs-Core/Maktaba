# Story 2.3 — Stream extraction (no intermediate WAV by default)

## Description

Extraction runs as part of the `extract` stage and feeds audio into the
transcriber via a pipe. When the chosen STT backend cannot consume a
stream, fall back to a temp WAV; otherwise stream PCM straight into
the transcriber's iterator.

## Acceptance criteria

- **Given** a `videos` row in state `PROBED` and a selected track,
  **when** the `extract` stage runs,
  **then** it spawns
  `ffmpeg -hide_banner -nostdin -threads 1 -i {file} -map 0:a:{idx}
   -ac 1 -ar 16000 -sample_fmt s16 -f s16le pipe:1` and yields the
  resulting byte stream as an async iterator of PCM chunks (default
  64 KiB per chunk).
- **Given** an STT backend that requires a file (some `openai-whisper`
  paths, OpenAI API), the extract stage instead writes
  `~/.maktaba/cache/audio/{hash}.wav` (16-bit PCM mono 16 kHz) and
  passes its path; the file is removed when the job reaches `done`,
  `failed`, or `cancelled`.
- **Given** a file FFmpeg cannot open (corrupt header, unsupported codec
  with no decoder),
  **when** extraction starts,
  **then** the job transitions to `failed` with a structured `error`
  containing `{kind: "ffmpeg_decode", returncode, stderr_tail}`; no
  partial PCM is delivered.
- **Given** the transcriber consumes the stream and the worker is paused
  (Epic 6),
  **when** the pause check fires,
  **then** the FFmpeg process is sent `SIGTERM`, drained for up to 5 s,
  then `SIGKILL`-ed if still alive; no zombie ffmpegs survive a paused
  job.

## Test cases

- `test_extract_streams_pcm` — fixture file → consumer receives expected
  byte count (`duration_sec * 16000 * 2`), within ±1 chunk tolerance.
- `test_extract_pipes_directly_into_stt` — mock STT backend captures
  stream; assert the byte stream matches reference WAV's data section.
- `test_extract_to_file_when_backend_requires` — STT backend declares
  `requires_file = True` → temp WAV written; path cleaned up after job
  reaches terminal state.
- `test_extract_fails_on_bad_input` — fixture is a renamed `.txt` →
  job state `failed`, `error.kind == "ffmpeg_decode"`.
- `test_extract_kill_on_pause` — start extraction; set `pause_requested
  = true` mid-stream; assert ffmpeg process exits within 5 s and no
  PCM is committed to the transcript table.
- `test_extract_resume_uses_seek` — resume from `last_segment_end_sec
  = 320.5` → ffmpeg is invoked with `-ss 320.5` (input seek for fast
  decoder warmup); the first byte yielded corresponds to ≥320.5 s of
  the source.

## Edge cases

- **Variable-frame-rate sources.** `-ss` placed **before** `-i` does a
  fast-but-imprecise input seek; for VBR/VFR audio we seek slightly
  earlier (`max(0, ss - 0.5)`) and discard the lead-in until the first
  PCM sample whose presentation timestamp is ≥ requested. This keeps
  resume offsets exact.
- **Concatenated TS streams** with mid-file PTS resets. The extractor
  applies `-fflags +genpts` to force monotonic PTS; otherwise the
  per-segment `start_sec` jumps backward and breaks resume.
- **Audio in a video container with broken duration metadata.** The
  extractor falls back to "stream until EOF" rather than trusting the
  reported duration; `processing_jobs.total_duration_seconds` is
  refreshed from the actually-decoded length on completion.
- **Decoder that emits fewer samples than frame headers claim.** The
  extractor records an EWMA of `decoded_samples / declared_samples`;
  if the ratio drops below 0.95, the job is failed with
  `error.kind == "audio_drift"` for human review rather than producing
  a misaligned transcript.
