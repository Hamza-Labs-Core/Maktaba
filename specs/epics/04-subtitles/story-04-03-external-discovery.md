# Story 4.3 — External subtitle auto-discovery

## Description

Files like `Lecture 1.ar.srt` shipped alongside the video should appear
in the library without anyone running an explicit pipeline.

## Acceptance criteria

- During scanning ([Epic 1](../01-scanner/README.md)), for each video
  file the scanner also matches the regex
  `^<basename>(?:\.(?P<lang>[a-z]{2,3}))?\.(?P<ext>srt|vtt|ass|ssa)$`
  against siblings in the same directory.
- Each match creates a `subtitle_files` row with `is_external = true`,
  `is_embedded = false`, `language = <lang or 'und'>`, `format = <ext>`,
  `transcript_id = NULL`, and `path = <absolute>`.
- An external `.ass` or `.ssa` file is recorded but **not** converted at
  scan time; conversion to VTT is deferred to first request by the
  Streaming Service (architecture §4.5), which writes the converted
  artifact to `.maktaba/subs/`.
- Re-scanning does not duplicate `subtitle_files` rows; uniqueness is
  `(video_id, language, format, is_external, path)`.

## Test cases

- `test_external_srt_discovered` — fixture dir with
  `Lecture.mp4 + Lecture.ar.srt` → exactly one `subtitle_files` row,
  `language = 'ar'`, `is_external = true`.
- `test_external_no_lang_tag` — fixture with `Lecture.srt` (no
  `.ar.` infix) → `language = 'und'`.
- `test_external_ass_recorded_not_converted` — `.ass` file → row
  exists; no `.vtt` is generated.
- `test_rescan_idempotent` — run scan twice → row count unchanged.

## Edge cases

- **Filename collision** between an auto-generated subtitle and an
  external one for the same language. The external one wins for
  serving (its row's `is_external = true` and is preferred by the
  Streaming Service); the auto-generated row stays with
  `is_active = false`.
- **Multiple external subtitles for the same language.** All are kept;
  the user chooses in the UI. The first-discovered one is marked
  `is_default = true` and exposed at the manifest's
  `DEFAULT=YES` slot.
- **Subtitle file moved without the video.** On the next scan, the row
  is updated with the new path; on its disappearance entirely, the
  row is soft-deleted (`subtitle_files.deleted_at` populated).
