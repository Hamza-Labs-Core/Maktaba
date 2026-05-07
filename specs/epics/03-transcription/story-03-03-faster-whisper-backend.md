# Story 3.3 — Faster-Whisper (CUDA / CPU) backend

## Description

Linux + NVIDIA path and CPU fallback.

## Acceptance criteria

- `FasterWhisperBackend(name="whisper-cuda" | "whisper-cpu")` wraps
  `faster_whisper.WhisperModel` with `device="cuda"` or `device="cpu"`
  selected at construction. Both share a base class to avoid duplication.
- Streaming: `transcribe(audio, ...)` yields each `Segment` as
  `faster-whisper` emits it (it does so naturally through its
  generator interface).
- Conformance suite (Story 3.1) passes for both variants on the CI matrix
  (CPU run is mandatory; CUDA run is optional and skipped when no GPU).

## Test cases

- Conformance suite per device.
- `test_faster_whisper_word_timestamps_match_segment` — when word
  timestamps are enabled, sum of word durations is within ε of the
  parent segment's duration.

## Edge cases

- **Compute-type mismatch.** `compute_type` defaults to `float16` on
  CUDA, `int8` on CPU; if the constructor raises on the requested type,
  the backend falls back to `float32` once and records the choice.
