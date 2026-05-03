# Story 4.1 — Generate SRT and VTT from `transcript_segments`

## Description

When transcription completes (`state = TRANSCRIBED`), produce both subtitle
formats from the canonical segments — never from a previously written file.

This story owns the `subtitle_gen` stage that the FSM defines in
[Epic 1 Story 1.6](../01-scanner/story-01-06-video-state-machine.md).
The stage runs after `transcribe` reaches `done` for a transcript and
must complete (alongside `index`) before the video advances to
`INDEXED`.

## Acceptance criteria

- **Given** a video whose `state = TRANSCRIBED`,
  **when** the `subtitle_gen` stage runs,
  **then** two files are produced:
  - `<library_root>/.maktaba/subs/<hash>.<lang>.srt`
  - `<library_root>/.maktaba/subs/<hash>.<lang>.vtt`

  and a copy alias is written next to the source file:
  - `<source_dir>/<source_basename>.<lang>.srt`

  The alias uses the source file's basename so external players
  auto-discover it.
- **Given** the same job,
  **when** it succeeds,
  **then** rows are inserted into `subtitle_files` for both formats with
  `is_external = false`, `is_embedded = false` (see
  [Story 4.4](story-04-04-embedded-extraction.md) for the column
  ownership), and the orchestrator advances the video toward `INDEXED`
  once `index` is also `done`.
- **Cue text is sanitized.** All cue text is HTML-escaped before being
  written to either format: `<` → `&lt;`, `>` → `&gt;`, `&` → `&amp;`.
  This prevents `<script>` or other tag-like content (vanishingly rare
  from STT but possible from external sidecar SRT promoted into the
  pipeline) from landing verbatim in a VTT cue. The escape is applied
  after wrapping but before format-specific framing
  (resolves REVIEW §5.3).
- **Given** a write failure (disk full, permission denied),
  **when** `subtitle_gen` retries,
  **then** the partial files at the temp path are removed; on retry the
  same final paths are written atomically (write to
  `…/.maktaba/.tmp/<uuid>.{srt,vtt}` then `os.replace()` to the final
  path).
- **Given** the file's source directory is read-only (e.g., a CIFS mount
  with restrictive perms),
  **when** the alias copy fails,
  **then** the sidecar in `.maktaba/subs/` is still written, the
  `subtitle_files` row is still inserted, and a WARN is logged with
  `kind=alias_copy_failed`. The job is **not** failed — the canonical
  artifact exists.

## Test cases

- `test_srt_round_trips` — input segments → SRT → re-parse with
  `srt` library → same number of cues, same text, timestamps within
  1 ms.
- `test_vtt_round_trips` — same against `webvtt` library.
- `test_cue_text_html_escaped` — segment text contains `<script>alert(1)</script>`
  and `Tom & Jerry` → SRT and VTT output contain the literal escapes
  `&lt;script&gt;`, `&amp;`; no parser interprets them as markup.
- `test_alias_copy_uses_source_basename` — fixture
  `/lib/Lecture 1.mp4` → alias is `/lib/Lecture 1.ar.srt` (note the
  `.ar.` infix; ISO 639-1 code).
- `test_atomic_replace_on_retry` — kill the worker between writing temp
  and replace → no `.srt` at the final path; retry produces the file
  cleanly.
- `test_readonly_source_dir_does_not_fail_job` — source dir read-only
  → job state `done`, alias copy log warned, `.maktaba/subs/` file
  exists.

## Edge cases

- **`.maktaba/` directory not yet created.** Created with mode `0755`
  on first write; if creation fails (parent perms), the job fails with
  `error.kind = "sidecar_dir`.
- **Source basename collision** when two different videos in the same
  directory share a basename. `subtitle_files.path` is a function of
  the video, not the basename, so the row is correct; the alias copy,
  however, is **skipped** for the second video to avoid clobbering and
  logged as `kind=alias_collision`.
- **Filenames with right-to-left content** (Arabic). The OS-level path
  is preserved as-is; we don't reorder bytes. The renderer in the UI
  is responsible for bidi-correct display.
- **Cue text containing existing entities** (e.g., the literal
  ampersand-amp-semicolon string). Escape is one-pass (`&` → `&amp;`
  first, then `<`/`>`); a previously-encoded entity becomes
  `&amp;amp;` once. This is correct because cue text is treated as
  literal user text, not markup.
