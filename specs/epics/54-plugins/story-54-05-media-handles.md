# Story 54.05 — Media handles and host services

> Epic 54 · Phase 1 · Depends on: 54.02, 54.04 · Needed by: 54.12

## Why

Most useful plugins need the media itself: find silences, detect intros, read embedded tags,
make a waveform, check loudness. A plugin has no filesystem (54.02), and handing it paths would
also hand it every other file. And a WebAssembly plugin cannot bring ffmpeg or NumPy. So
Maktaba hands out **handles**: a byte stream of one video, or decoded frames and audio made by
Maktaba's own ffmpeg, only for the video the job is about, only while the job runs.

## Description

### 1. Three kinds of access

| WIT function | Capability | What the plugin gets | Made by |
|---|---|---|---|
| `media.open(video) -> stream` then `stream.read(offset, len)`, `stream.size()` | `media.read` | raw bytes of the file, ≤ 8 MB per read | streaming service, range reads |
| `media.frame(video, t-ms, max-edge) -> image` | `media.frames` | one decoded frame as PNG or raw RGB, longest edge ≤ 1,280 px | ffmpeg on the home |
| `media.audio(video, t0-ms, t1-ms, rate) -> pcm` | `media.audio` | mono 16-bit PCM at 8, 16 or 22.05 kHz, ≤ 10 minutes per call | ffmpeg on the home |
| `media.info(video)` | `library.read` | duration, streams, codecs (from `probe`) | database |

### 2. How a handle is made

- The host forwards `media.open` to the plugin API with the job token. The API checks the
  capability and that `video` is the job's subject (or, for library and catalog jobs, in the
  granted libraries), then returns a **handle id** bound to the invocation, the video and the
  file's content hash at that moment.
- `stream.read` calls go host → plugin API → the streaming service's internal range endpoint
  (the same reader playback uses, so NAS throttling and the `nas_read` pool apply). The plugin
  API, not the host, talks to streaming; the host has no network.
- Frames and audio: the plugin API asks the pipeline's existing media tools through an internal
  call on the home (the code `thumbnail/` and `audio/` already use), with a per-plugin
  concurrency of 1 and the `cpu` pool, and returns the bytes.
- Handles end with the invocation. A handle whose file changed (content hash differs) answers
  `409 media_changed`, so a plugin never mixes two versions of a file.

### 3. Budgets

Per job: total bytes read ≤ 2× the file size, frames ≤ 2,000, audio ≤ 4× the duration. Over
budget answers `403 plugin_quota`. These stop a plugin from turning a job about one video into
a copy of the library, and keep NAS load predictable.

### 4. What is never exposed

Paths, share names, NAS addresses, other files in the folder (sidecar `.nfo`, other subtitles),
the database's file ids. A plugin that wants existing subtitles reads them through
`transcript`/`library` calls, not files.

## Acceptance criteria

- **Given** a job for video V with `media.read`,
  **when** the plugin opens V and reads the first 1 MB,
  **then** it gets the same bytes as the file, and the read goes through the `nas_read` pool.
- **Given** the same job,
  **when** it opens video W (not the subject),
  **then** `403 plugin_scope`.
- **Given** a plugin without `media.frames`,
  **when** it calls `media.frame`,
  **then** the host answers `error(capability)` without a call, and a forged API call gets 403.
- **Given** V's file is replaced during the job,
  **when** the plugin reads again,
  **then** `409 media_changed`.
- **Given** a plugin reading 3× the file size,
  **when** it passes 2×,
  **then** `403 plugin_quota`.
- **Given** a 2-hour video,
  **when** a plugin asks for audio 0–15 min,
  **then** it is refused (`plugin_too_large`, max 10 min per call); 0–10 min returns 16 kHz PCM
  of the right length.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | integration (PG, go + streaming) | fixture video | open, read ranges, size | bytes equal; pool used |
| T02 | integration | subject vs other video vs other library | open | 200 / 403 / 403 |
| T03 | integration | file replaced mid-job | read | 409 `media_changed` |
| T04 | integration | byte, frame and audio budgets | exceed | 403 `plugin_quota` |
| T05 | integration (py tools) | frame at 0, mid, past end | frame | PNG ≤ 1,280 px / PNG / 400 |
| T06 | integration | audio 10 min / 15 min / rate 44.1k | audio | ok / 413 / 400 |
| T07 | e2e (`plugins-e2e`) | the "silence chapters" sample plugin | run on fixture | chapters written; handle ended after job |
| T08 | unit (go) | handle used after invocation end | read | 401 |

## Edge cases

- **A video on an offline NAS**: `503 media_unavailable`; the job fails with the existing
  `nas_timeout` cause (class `transient`), so Epic 40 treats it like any NAS job.
- **Huge files (100 GB)**: reads are range-based, never buffered whole.
- **DRM-free only**: Maktaba has no DRM; nothing changes.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `api/internal/plugins/pluginapi/routes_media.go` (new) | Handles, budgets, scope. |
| `streaming/internal/...` internal range endpoint | Accept plugin API's internal calls (machine auth on loopback), reuse playback reader. |
| `pipeline/src/maktaba_pipeline/plugins/media_service.py` (new) | Frame and audio extraction on request, using existing `thumbnail/` and `audio/` helpers. |
| `plugind/src/host/media.rs` (new) | Forwarders and stream resource. |
| `shared/plugins/wit/maktaba-plugin.wit` | `media` interface. |
| Tests next to each | T01–T08. |

### Tests

- Integration T01–T06, T08 in the Go and pipeline Postgres jobs; T07 in `plugins-e2e`.

### Rollout

Dark until a plugin with a media capability is installed.

### Rollback

Release rollback; no data.

### Dependencies

54.02 (host forwarders), 54.04 (tokens, scope).
