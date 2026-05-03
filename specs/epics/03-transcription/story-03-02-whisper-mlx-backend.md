# Story 3.2 — Whisper MLX backend (default on Apple Silicon)

## Description

The flagship backend; this is what 80% of users will run.

## Acceptance criteria

- `WhisperMLXBackend(name="whisper-mlx")` wraps `mlx-whisper`'s `transcribe`
  function and yields segments as `mlx_whisper` produces them (it emits
  segments at known boundary points; we surface those without buffering
  beyond a single segment).
- `cost_per_minute = 0.0`; `supports_streaming = true`;
  `requires_file = false`.
- The backend respects `hints.initial_prompt` (used to bias the decoder
  toward Arabic religious vocabulary by default — `architecture §3.4`).
- The backend respects `hints.language`; when `language is None`, it runs
  language detection on the first 30 s and uses that for the rest of the
  decode.
- Output `Segment.text` is normalized to NFC; trailing whitespace
  trimmed; bidi marks (`U+200E`, `U+200F`) inserted only if necessary
  for mixed-direction display (we leave that to the renderer).

## Test cases

- `test_mlx_runs_on_apple_silicon_only` — on `arch != arm64-darwin`,
  `WhisperMLXBackend.health().ready == false` and the registry skips it.
- `test_mlx_initial_prompt_used` — `hints.initial_prompt = "بسم الله
  الرحمن الرحيم"` → the first segment of an Arabic recitation
  reproduces the prompt's vocabulary at higher confidence than without.
- `test_mlx_language_autodetect` — Arabic file with `language = None`
  → `transcript.language == "ar"` and segment text is in Arabic.

## Edge cases

- **Out-of-VRAM.** `mlx_whisper` raises a `RuntimeError`; the worker
  releases the GPU lock, fails the job with backoff, and the next
  attempt is allowed (model size auto-degraded if `degrade_on_oom = true`
  in library settings — `large-v3` → `medium` → `small`, recorded in
  `transcripts.metadata`).
- **Repeated identical segments** ("hallucination loop"). The backend
  detects ≥3 consecutive segments with `text` Levenshtein-distance ≤2
  and `len(text) > 10`, and forces a new decode window; this is reported
  in `transcripts.metadata.hallucination_breaks`.
