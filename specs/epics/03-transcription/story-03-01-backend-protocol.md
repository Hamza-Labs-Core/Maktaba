# Story 3.1 — STT backend protocol

## Description

A single `STTBackend` interface that every concrete backend implements,
verified by a backend-agnostic conformance test suite. Adding a new
backend later means writing a class and the suite passing — nothing else.

## Acceptance criteria

- The protocol matches `architecture.md §3.4`:

  ```python
  class STTBackend(Protocol):
      name: str
      supports_streaming: bool
      requires_file: bool
      cost_per_minute: float | None

      async def transcribe(
          self,
          audio: AudioSource,
          language: str | None,
          hints: TranscriptionHints,
      ) -> AsyncIterator[Segment]: ...

      async def detect_language(self, audio: AudioSource) -> str: ...
      async def health(self) -> BackendHealth: ...
  ```

- Every backend yields `Segment` objects (the canonical schema in
  `architecture §3.4`). A backend that does **not** stream still
  implements the same async iterator interface; it simply yields all
  segments at the end of `transcribe()`.
- `BackendHealth` reports `{ready: bool, model_loaded: bool, version,
  device, last_check_at}` — used by `GET /api/system/health` and by the
  pipeline's preflight check before claiming a job.
- A pytest fixture `stt_conformance_suite(backend)` runs the **same**
  shared suite of fixtures against any backend and is gated as required
  in CI for every backend listed in §3.4.

## Test cases (conformance suite, run per backend)

- `test_transcribe_short_arabic` — 30 s known-text Arabic clip → at least
  one segment whose `text`, after Unicode NFC + diacritics-stripped
  comparison, contains the expected reference phrase.
- `test_transcribe_short_english` — 30 s known-text English clip →
  similar match against reference.
- `test_segments_are_monotonic` — for any input, `seg[i].end <=
  seg[i+1].start + ε` (allow ε=0.05 s overlap that some backends emit).
- `test_segments_cover_audio` — sum of `seg.end - seg.start` ≥ 0.9 ×
  audio_duration (90% coverage; silence accounts for the rest).
- `test_word_timestamps_when_supported` — when `supports_word_timestamps
  = true` and `hints.word_timestamps = true`, every segment has
  non-empty `words` with `start <= end` and contained within the parent
  segment's bounds.
- `test_language_detection` — fixture in `ar` and a fixture in `en`
  → `detect_language()` returns the expected ISO 639-1.
- `test_pause_between_segments` — consume an iterator until segment N,
  cancel it, then create a new iterator with `start_offset =
  seg[N].end` → output continues from there with no overlap and no gap
  beyond ε.

## Edge cases

- **Backends that emit segments out of order** (rare; some streaming
  decoders) — the orchestrator buffers and reorders before commit,
  guaranteeing monotonic `seq` in the DB.
- **Backends that emit empty `text`** for silence — those segments are
  dropped before commit; they still count toward `processed_seconds`
  via the gap accounting.
- **Backend cold start.** A backend whose `health.model_loaded=false`
  loads on first call; the pipeline calls `await backend.warmup()`
  before flipping the job to `running` to avoid blowing the heartbeat
  window on a 30 s model load.
