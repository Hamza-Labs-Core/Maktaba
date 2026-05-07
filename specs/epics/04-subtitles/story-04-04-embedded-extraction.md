# Story 4.4 — Embedded subtitle extraction (with `is_embedded` schema)

## Description

Some MKVs ship subtitles embedded as `S_TEXT/UTF8` or PGS streams. The
Streaming Service requests these from the Pipeline on demand.

> **Resolves REVIEW §1.1.c.** The architecture schema's `subtitle_files`
> definition (`(id, video_id, transcript_id, format, language, path,
> is_external, created_at)`) does not contain `is_embedded`, but
> `is_embedded` is the only way to distinguish a Pipeline-extracted
> embedded track from a Pipeline-generated transcript artifact (both have
> `is_external = false`). This story is the **single owner** of the
> column and its migration.
>
> **Resolves REVIEW §1.2.c and §2.1.a.** This story also documents the
> `Pipeline.ExtractEmbeddedSubtitle` gRPC method that
> `architecture.md §9.9` is missing. Adding it to the proto is part of
> the acceptance criteria below.

## Acceptance criteria

- Probe ([Story 2.1](../02-audio-extraction/story-02-01-audio-probe.md))
  records `media_info.has_subtitles = true` whenever ffprobe reports any
  subtitle stream, plus a list of `(index, codec, language)` in
  `media_info.raw_ffprobe`.
- A migration `shared/db/migrations/000X_subtitle_files_is_embedded.sql`
  adds the column:
  ```sql
  ALTER TABLE subtitle_files
    ADD COLUMN is_embedded BOOLEAN NOT NULL DEFAULT FALSE;
  CREATE INDEX subtitle_files_video_kind
    ON subtitle_files (video_id, is_external, is_embedded);
  ```
  Existing rows are backfilled to `false` (none are embedded by
  construction; embedded rows only exist after this story ships).
- The gRPC service definition in `shared/proto/pipeline.proto` adds:
  ```proto
  rpc ExtractEmbeddedSubtitle(ExtractEmbeddedSubtitleRequest)
    returns (ExtractEmbeddedSubtitleResponse);

  message ExtractEmbeddedSubtitleRequest {
    string video_id = 1;
    int32 stream_index = 2;
  }
  message ExtractEmbeddedSubtitleResponse {
    string path = 1;
    string codec = 2;
    string language = 3;
    bool cached = 4;
  }
  ```
  `architecture.md §9.9` is updated to reflect the new RPC.
- The RPC `Pipeline.ExtractEmbeddedSubtitle(video_id, stream_index)`
  validates that `stream_index` corresponds to an existing subtitle
  stream in `media_info.raw_ffprobe`. An out-of-range or non-subtitle
  index returns the gRPC error `INVALID_ARGUMENT` with detail
  `unknown_subtitle_stream`; the call is **not** allowed to extract
  arbitrary streams (resolves REVIEW §5.2 input-validation gap).
- Valid calls extract the requested stream as VTT and write it to
  `.maktaba/subs/<hash>.<lang>.embedded.vtt`, returning the path. The
  call is idempotent: a second call returns the cached file with
  `cached = true`.
- Text-codec subs (`subrip`, `webvtt`, `ass`, `ssa`) are converted via
  `ffmpeg -map 0:s:N -c:s webvtt`. Bitmap-codec subs (`hdmv_pgs_subtitle`,
  `dvdsub`) are **not** converted in v1; the API returns the gRPC error
  `UNIMPLEMENTED` with detail `unsupported_subtitle_codec` and the UI
  hides them.
- The extracted file appears in `subtitle_files` with `is_external =
  false`, `is_embedded = true`.
- Cue text in extracted VTT is sanitized identically to
  [Story 4.1](story-04-01-generate-from-segments.md) before writing,
  even though the source is ffmpeg's output, because external
  contributors can craft hostile S_TEXT/UTF8 streams.

## Test cases

- `test_migration_adds_is_embedded` — apply migration; column exists
  with `NOT NULL DEFAULT FALSE`; index `subtitle_files_video_kind`
  present.
- `test_embedded_text_extraction` — fixture `subs.mkv` with a `subrip`
  English track at index 2 → `Pipeline.ExtractEmbeddedSubtitle(id, 2)`
  produces a parseable WebVTT file with the expected cue count;
  `subtitle_files` row has `is_embedded = true`.
- `test_embedded_idempotent` — call twice → file path returned both
  times; second response has `cached = true`; ffmpeg is invoked exactly
  once (verify by mocking subprocess).
- `test_embedded_invalid_index_rejected` — call with `stream_index = 99`
  on a fixture with 3 subtitle streams → returns `INVALID_ARGUMENT`,
  no file written.
- `test_embedded_audio_index_rejected` — call with a `stream_index` that
  points to an audio stream → returns `INVALID_ARGUMENT` (the validator
  cross-checks against `media_info.raw_ffprobe[stream_index].codec_type
  == 'subtitle'`).
- `test_embedded_pgs_returns_unsupported` — fixture with PGS subs →
  RPC returns `UNIMPLEMENTED` with `unsupported_subtitle_codec`; no
  file is written.

## Edge cases

- **Stream language tag missing.** Defaults to `und`; user-side rename
  is deferred (no API endpoint in v1; tracked separately for v1.1).
- **Multiple subtitle tracks at the same language.** Each becomes its
  own row; the user picks per-session via the manifest.
- **Concurrent ExtractEmbeddedSubtitle for the same `(video, index)`.**
  The implementation uses a per-pair file-lock around ffmpeg invocation;
  the second caller blocks until the first writes the artifact, then
  returns `cached = true` without re-running ffmpeg.
