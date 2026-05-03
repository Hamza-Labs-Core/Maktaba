# Story 3.4 — OpenAI API backend

## Description

For users without local hardware.

## Acceptance criteria

- `OpenAIWhisperBackend(name="openai-api")` calls the official Whisper
  endpoint; `cost_per_minute` populated from the live price list at
  package build time.
- `supports_streaming = false`; `requires_file = true` (the API takes a
  file upload, not a stream). The orchestrator therefore writes a
  temp WAV ([Story 2.3](../02-audio-extraction/story-02-03-stream-extraction.md))
  before calling.
- The backend chunks audio into 24 MB pieces (API limit); each chunk's
  segments are re-timestamped against the original timeline. The
  re-stitching is verified by an integration test against a 90-min
  fixture.
- Per-library budget cap (`stt.backends.openai.max_usd_per_month`)
  enforced **before** claim: a worker computes the projected cost from
  `videos.duration_sec × cost_per_minute`, sums the running total for
  the calendar month, and refuses the claim with `not_before = first of
  next month` if the projection would exceed the cap.

## Test cases

- `test_openai_chunking_preserves_timestamps` — 90-min fixture; assert
  segments tile the timeline contiguously and stitched timestamps match
  the single-call equivalent within ε.
- `test_openai_budget_cap` — set cap = $0.10; try to enqueue a 30 min
  transcribe → claim refused, job pushed to next month with reason
  `budget_cap`.
- `test_openai_retry_on_429` — API returns 429 → backend retries with
  exponential backoff (0.5/1/2/4/8 s, jitter ±25%) up to 5 attempts
  before failing the segment chunk.

## Edge cases

- **API timeout mid-upload.** Treated as a transient failure; backend
  retries the chunk. `processed_seconds` only advances on a successful
  segment commit.
- **API returns segments without confidence.** `Segment.confidence` is
  set to `None`; downstream code never assumes confidence is present.
- **Audio that includes silence longer than the API's 30 s
  internal-window limit.** Pre-strip silences > 5 s using
  `ffmpeg -af silenceremove` before upload, but record a "silence map"
  so segment timestamps remain in the **original** timeline. Verified
  against a fixture with a known 60 s silence in the middle.
