# Story 2.2 — Track selection

## Description

When a file has multiple audio tracks, pick the one most likely to be the
intended speech for transcription.

## Acceptance criteria

- The selection function returns exactly one `audio_tracks` row given
  `(video, library_settings)`, with this priority order, first match wins:
  1. `library.settings.preferred_audio_language` matches an
     `audio_tracks.language` (ISO 639-3 normalized).
  2. The track tagged `ara` (Arabic) — Maktaba's first-class language.
  3. The track marked `is_default = true` by the container.
  4. The first track by `index`.
- **Given** the library setting `multi_audio = true`,
  **when** track selection runs,
  **then** the function returns **all** non-commentary tracks, and the
  pipeline enqueues one `transcribe` job per selected track.
- **Given** an audio track whose language is `und` and codec is `pcm`,
  **when** selection runs against an Arabic-preferring library,
  **then** the `und` track is still selected over no track at all (we
  don't refuse to transcribe just because we don't know the language —
  the STT auto-detect resolves it).

## Test cases

- `test_select_prefers_user_language` — settings prefer `en`; tracks
  are `[ara, eng]` → selects `eng`.
- `test_select_falls_back_to_arabic` — no preference set; tracks are
  `[eng, ara]` → selects `ara`.
- `test_select_uses_default_disposition` — no preference, no Arabic;
  tracks are `[eng-non-default, fre-default]` → selects `fre`.
- `test_select_falls_back_to_first` — no preference, no Arabic, none
  default → selects index 0.
- `test_select_multi_audio_returns_all` — `multi_audio = true`, three
  tracks → returns three.
- `test_select_excludes_commentary` — track with
  `disposition.commentary = 1` is never selected unless explicitly
  requested.

## Edge cases

- **Identical-language duplicate tracks** (`eng` stereo and `eng` 5.1).
  The 5.1 wins (more channels), then ties broken by `is_default`, then
  by index.
- **Audio described / SDH descriptive tracks.** Detected by
  `disposition.descriptions = 1` or by title regex
  `(?i)\b(audio description|described|sdh|cc)\b`; excluded by default.
- **Selection determinism under re-probe.** Re-probing produces the same
  rows in the same order; selection is therefore stable across re-runs.
