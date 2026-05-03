# Story 2.1 — Probe the audio tracks

## Description

Before extraction, the probe stage records every audio track ffprobe
finds, with language, codec, channel layout, and sample rate. This stage
also drives the first non-trivial state transition for a video
(`DISCOVERED → PROBED` or `DISCOVERED → READY_NO_AUDIO`; see
[Epic 1 Story 1.6](../01-scanner/story-01-06-video-state-machine.md)).

## Acceptance criteria

- **Given** a video in state `DISCOVERED`,
  **when** the `probe` stage runs,
  **then** `media_info` is populated with container, video codec,
  resolution, fps, bitrate, and `has_subtitles`; `audio_tracks` has one
  row per audio stream with its `index` (the ffmpeg `-map 0:a:N` index),
  `codec`, `channels`, `sample_rate`, `language` (ISO 639-3 from the
  stream's `tags.language` if present, else `und`), `title`, and
  `is_default` (true when the stream's `disposition.default == 1`).
- **Given** the same probe,
  **when** it completes,
  **then** the video state advances to `PROBED` exactly once, and a
  `processing_jobs(stage='extract')` row in state `pending` is enqueued.
- **Given** a video that has zero audio tracks,
  **when** probed,
  **then** the state advances to `PROBED` but **no** `extract` job is
  enqueued; instead the video transitions to `READY_NO_AUDIO` (a terminal
  but searchable state — title/description still indexable, no
  transcript). See [Epic 1 Story 1.6](../01-scanner/story-01-06-video-state-machine.md)
  for the FSM definition.

## Test cases

- `test_probe_writes_media_info` — fixture `lecture.mkv` (1080p, h264,
  ar audio) → exact expected row in `media_info`.
- `test_probe_writes_one_audio_row_per_track` — fixture `multiaudio.mkv`
  (3 audio tracks: ar, en, fr) → 3 `audio_tracks` rows; `is_default` set
  on the one with `disposition.default == 1`.
- `test_probe_handles_undefined_language` — fixture without `tags.language`
  → row has `language = 'und'`, not NULL.
- `test_probe_audioless_video` — fixture `silent.mp4` → `audio_tracks`
  empty, video state `READY_NO_AUDIO`, no `extract` job.
- `test_probe_idempotent_on_replay` — run probe twice → no duplicate
  rows; second run is a no-op (`UPSERT ON CONFLICT DO NOTHING`).

## Edge cases

- **Mislabeled tracks.** Some MKVs declare `tags.language=eng` for an
  Arabic track. The probe records what the file claims; the transcriber's
  language auto-detect is what actually drives behavior. We never silently
  override the file's metadata.
- **Mid-file codec change.** Rare. ffprobe reports the first packet's
  codec. If the file later switches codec, the extractor will fail at run
  time and the job retries with `transcoded_extract = true` (see Story 2.3).
- **CDN-style fragmented streams.** `ffprobe -show_format` reports the
  full duration only after a `-analyzeduration 100M -probesize 50M`
  bump for some MPEG-TS sources; the probe applies these flags
  unconditionally.
