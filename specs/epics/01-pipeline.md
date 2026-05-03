# Pipeline — Epics & Stories

> Implementation plan for the Maktaba **core pipeline**: the chain of stages
> that turns a video file on disk into a streamable, searchable, indexed
> entry in the library. This document is the engineering contract between
> [`specs/architecture.md`](../architecture.md) (the design) and the code
> in `pipeline/`.

Each epic below is a self-contained vertical slice of the pipeline. Stories
are numbered `<epic>.<story>` and must be deliverable in order: every story
finishes with the system running, tests passing, and the next story's
preconditions established.

**Conventions:**

- Acceptance criteria are written as **Given / When / Then**.
- Test cases are concrete, runnable specifications (unit, integration, or
  end-to-end) — not narratives.
- Edge cases are listed separately so they cannot be silently dropped from
  the implementation.
- "Job" and "stage" follow the architecture's vocabulary: a stage is a
  pipeline step (`scan`, `probe`, …); a job is a row in `processing_jobs`
  representing one pending or in-flight execution of a stage on a video.
- Time is always seconds (`REAL`) of audio offset unless otherwise noted.
  Wall-clock times are UTC `TIMESTAMPTZ`.
- Code paths reference the layout in
  [`specs/architecture.md §12.1`](../architecture.md).

**Epics in this document:**

1. [Scanner](#epic-1--scanner) — discover and identify files on disk.
2. [Audio Extraction](#epic-2--audio-extraction) — pick the right track and
   stream it out as 16 kHz mono PCM.
3. [Transcription](#epic-3--transcription) — pluggable STT with durable,
   resumable per-segment commits.
4. [Subtitles](#epic-4--subtitles) — generate SRT/VTT sidecars; handle
   embedded and external subtitles.
5. [Search & Indexing](#epic-5--search--indexing) — FTS5/`tsvector` and
   ChromaDB with hybrid retrieval.
6. [Job Queue](#epic-6--job-queue) — claim/heartbeat/pause/resume on
   Postgres with `SELECT … FOR UPDATE SKIP LOCKED`.

---

## Epic 1 — Scanner

**Goal.** Detect every video file under a library's roots, assign it a
stable identity (`content_hash`), and create a `videos` row in state
`DISCOVERED`. Cope with renames, moves, copies, partial downloads, network
filesystems that lie about events, and the user dragging in a 200 GB folder
in one go.

**Owner.** Pipeline Service, `pipeline/src/maktaba_pipeline/library/`.

**Out of scope for this epic.** Probing (Epic 2 prerequisite handled by the
`probe` stage in §3.2 of the architecture), subtitles auto-discovery
(Epic 4), thumbnails.

### Story 1.1 — Bootstrap a library and walk its roots

A user creates a library and points it at one or more root directories.
The scanner walks each root once and inserts a `videos` row for every
candidate file it finds.

**Acceptance criteria.**

- **Given** a library with roots `[/mnt/media/lectures]` and a tree
  containing 1,000 `.mp4`/`.mkv` files,
  **when** the user invokes `POST /api/libraries/{id}/scan` (or
  `maktaba-pipeline scan --library lectures`),
  **then** within one wall-clock pass `videos` contains 1,000 rows linked
  to that library, each with `state = 'discovered'`, a populated
  `content_hash`, `path`, `filename`, `size_bytes`, and `mtime`, and a
  `processing_jobs` row of `(stage='probe', state='pending')` per video.
- **Given** the same scan,
  **when** it runs to completion,
  **then** the API receives `videos.new` `LISTEN/NOTIFY` events such that
  the count of WebSocket fanout messages on `/ws/library/{id}` equals the
  number of inserted rows.
- **Given** the supported-extension list `[.mp4, .mkv, .mov, .webm, .avi,
  .ts, .m4v]`,
  **when** the walker encounters files outside that list,
  **then** they are ignored (no `videos` row, no log noise above DEBUG).

**Test cases.**

- `test_scan_inserts_row_per_video` — fixture tree with N supported files
  → expect `len(rows) == N`, all in `discovered`.
- `test_scan_ignores_non_video_extensions` — fixture mixes `.txt`, `.jpg`,
  `.mp4` → only the `.mp4` row exists.
- `test_scan_emits_notify_per_insert` — listen on `videos.new`; assert
  one notification per insert.
- `test_scan_enqueues_probe_job` — after scan, every `videos` row has a
  matching `processing_jobs` row of `(video_id, stage='probe',
  state='pending')`.
- `test_scan_creates_no_jobs_when_library_disabled` — library with
  `settings.disabled = true` is walked but produces no jobs.

**Edge cases.**

- **Symlink loops.** Use `os.walk(followlinks=False)` by default; libraries
  may opt-in to `follow_symlinks = true`, in which case a `set` of
  `(st_dev, st_ino)` rejects revisits.
- **Permission-denied directories.** Logged once at WARN with the path,
  then skipped; the scan does not abort.
- **Zero-byte files.** Skipped (no hash possible) and logged at DEBUG.
- **Files smaller than 8 MiB** (less than 4 MiB head + 4 MiB tail used by
  the hash). The hash falls back to "entire file"; correctness preserved.
- **Library with zero roots.** The scan completes immediately with a
  WARN log; no rows touched.

### Story 1.2 — Content-addressable identity (BLAKE3)

Every file gets a stable identity that is independent of name and path so
that renaming or moving a file does not retrigger the entire pipeline.

**Acceptance criteria.**

- **Given** a file `F`,
  **when** the scanner hashes it,
  **then** the produced `content_hash` is the lowercase hex of
  `BLAKE3(first_4_MiB || last_4_MiB || size_le_u64)`, where the size is
  appended as a little-endian unsigned 64-bit integer.
- **Given** two files with identical bytes (and therefore identical
  hashes),
  **when** both are scanned,
  **then** only the first creates a row; the second logs at INFO
  (`duplicate_content_hash`) and is associated with the existing row via
  an `additional_paths` JSON list on `videos.metadata`.
- **Given** a 30 GB file,
  **when** it is hashed,
  **then** at most 8 MiB of its bytes are read off disk for hashing, and
  the wall-clock cost is bounded by two seeks plus 8 MiB of sequential
  read.

**Test cases.**

- `test_hash_is_deterministic` — hash a fixture twice → identical.
- `test_hash_handles_small_file` — file < 8 MiB → hash is full-content
  BLAKE3, not head+tail.
- `test_hash_changes_on_size_change` — append a byte to a fixture →
  hash differs (size is part of the input).
- `test_hash_invariant_under_path_change` — move the fixture; rerun
  scanner → same hash; the existing row is reused, no new insert.
- `test_hash_io_budget` — patch the file with 30 GB sparse layout; assert
  `read()` invocations consume ≤ 8 MiB total.
- `test_hash_collision_logs_and_links` — two distinct paths with byte-for-
  byte identical content → one row, second path appears in
  `metadata.additional_paths`.

**Edge cases.**

- **Identical file copied to two libraries.** Each library gets its own
  `videos` row (rows are scoped by `library_id`); the `content_hash` is
  the same but `(library_id, content_hash)` is the effective uniqueness
  key. The schema's `UNIQUE (content_hash)` is therefore changed to
  `UNIQUE (library_id, content_hash)` if libraries are not de-duplicated
  globally; this is captured in §1.5 below as a schema decision.
- **Sparse files / holes.** BLAKE3 reads through holes as zeros — accepted;
  the size suffix prevents two sparse files of different sizes from
  colliding.
- **Network filesystem reports wrong size.** Hash is recomputed on the
  next scan if `size_bytes != stat.st_size`; the row is updated, not
  duplicated.

### Story 1.3 — Watch for live filesystem changes

After the initial scan, the user dropping a file into the library should
become a `videos` row within seconds, without a full rewalk.

**Acceptance criteria.**

- **Given** a library with `watch = true` (default),
  **when** the user copies `lecture.mkv` into a watched root,
  **then** within `2 × debounce_sec + 1` seconds (default debounce 2 s)
  there is exactly one new `videos` row and one `processing_jobs(probe)`
  row.
- **Given** an in-progress copy that has not finished writing (`mtime`
  still advancing),
  **when** the watcher receives an event,
  **then** the file is **not** ingested until its size has been stable
  for one debounce interval.
- **Given** a file rename within the library,
  **when** the watcher receives the move event,
  **then** the matching `videos` row's `path` is updated; no new row is
  created and no pipeline stage re-runs.
- **Given** a file deleted from disk,
  **when** the watcher receives the delete event,
  **then** the `videos` row is **soft-deleted** (state set to
  `missing`, original row preserved), so derived data (transcripts,
  index entries) is not destroyed by transient unmounts.

**Test cases.**

- `test_watcher_picks_up_new_file` — write a fixture into the watched
  root → row appears within 5 s.
- `test_watcher_debounces_partial_writes` — open a file, write 1 MiB
  every 200 ms for 5 s, then close → exactly one ingestion event,
  triggered after the final write settles.
- `test_watcher_handles_rename` — rename a file on disk → the same
  `videos.id` is retained, only `path` updates.
- `test_watcher_handles_delete` — delete the file → row state becomes
  `missing`; transcript rows are not deleted.
- `test_watcher_recovers_from_event_storm` — copy 10,000 files in a
  burst → all are eventually ingested with no exceptions; backpressure
  prevents OOM (the queue between watcher and scanner is bounded).

**Edge cases.**

- **Network filesystems (NFS, SMB) without inotify fidelity.** The watcher
  falls back to a periodic re-walk (default every 6 h, configurable per
  library); this is on by default for any root whose `statvfs` reports
  a non-local fstype.
- **Atomic mv from outside the watched root.** Generates a single
  `created` event; treated like a fresh file unless its hash matches an
  existing row, in which case it's a rename of a previously-`missing`
  entry (state restored from `missing` → `discovered`-equivalent).
- **`.maktaba/` sidecar directories under the root.** Always ignored by
  both the initial walk and the watcher.
- **`*.part`, `*.crdownload`, `*.tmp` files.** Ignored by extension; the
  watcher waits for the rename to a final extension.
- **Time-of-check to time-of-use.** The hash is computed only after the
  file size has been stable for the debounce interval, eliminating the
  race where we hash a partially-written file.

### Story 1.4 — Manual control surface

Operators need to scan on demand, check progress, and stop a runaway scan.

**Acceptance criteria.**

- **Given** a running scan,
  **when** the user calls `POST /api/libraries/{id}/scan` again,
  **then** the request returns 200 with `{status: "already_running",
  progress: <pct>}`; a second scan is not started.
- **Given** a long-running scan,
  **when** the user calls `DELETE /api/libraries/{id}/scan`,
  **then** the scanner stops within 5 s after the next file boundary,
  rolls back any uncommitted batch, and the library state is consistent
  (no orphaned `processing_jobs`).
- **Given** the CLI invocation
  `maktaba-pipeline scan --library NAME --dry-run`,
  **when** it runs,
  **then** it prints the would-be inserts to stdout and writes nothing
  to the DB.

**Test cases.**

- `test_scan_idempotent_concurrent_invocation` — invoke scan twice in
  parallel; the second call returns `already_running` and no duplicate
  rows are produced.
- `test_scan_cancellation_cleans_up` — cancel mid-scan → no
  `processing_jobs` rows reference videos that don't exist, no half-
  inserted videos.
- `test_dry_run_writes_nothing` — fixture tree, `--dry-run` → DB row
  counts are unchanged.

**Edge cases.**

- **CLI invocation while the gRPC server is also running.** The CLI
  acquires the same per-library scan advisory lock the gRPC trigger uses
  (Postgres `pg_try_advisory_lock(hashtext('scan:' || library_id))`);
  one of the two backs off.
- **Library deleted mid-scan.** The scan exits cleanly the next time it
  checks `library.deleted_at IS NOT NULL`.

### Story 1.5 — Schema & ownership decisions

This story is a one-time decision needed before the scanner ships, captured
here so it does not get hidden in code:

- **Uniqueness key.** `videos.content_hash` is `UNIQUE` per library, not
  globally. This permits the same source file to be ingested into multiple
  libraries with different settings (e.g., a tutorial in both `Lectures`
  and `Films`), at the cost of duplicate transcription work if the user
  does so. We accept that trade-off because cross-library de-duplication
  would require a join on the search side and complicates the "delete a
  library" semantics.
- **Soft delete.** Files removed from disk become `state = 'missing'` and
  retain all derived data. A `--purge-missing` flag on the scanner CLI
  hard-deletes any video that has been `missing` for ≥ 7 days.
- **`.maktaba/` sidecar directory.** Created lazily on first generated
  artifact; `chmod 755`; not synced to derived data caches.

**Acceptance criteria.**

- The migration file `shared/db/migrations/000X_videos_unique_per_library.sql`
  changes the constraint accordingly and is applied as part of the scanner
  ship.
- The state machine in `pipeline/src/maktaba_pipeline/domain/states.py`
  includes `MISSING` as a non-terminal sink with one allowed transition
  back to `DISCOVERED` on rediscovery.
- A `--purge-missing` CLI flag exists, defaults to off, and prompts before
  deleting unless `--yes` is passed.

---

## Epic 2 — Audio Extraction

**Goal.** From a probed video, pick the right audio track, extract it as
mono 16 kHz signed-16-bit PCM (Whisper's required input shape), and feed it
into the transcriber **without** writing an intermediate WAV file when the
backend can consume a stream. Record what we extracted in `audio_tracks`
so the transcript can be tied back to a specific track even if the file
later changes.

**Owner.** Pipeline Service, `pipeline/src/maktaba_pipeline/media/`
(`ffmpeg.py`, `audio.py`) and the `extract` stage in
`pipeline/src/maktaba_pipeline/pipeline/stages/extract.py`.

**Out of scope.** STT itself (Epic 3); subtitle extraction (Epic 4);
audio-format conversion of original files (we never modify source media).

### Story 2.1 — Probe the audio tracks

Before extraction, the probe stage records every audio track ffprobe
finds, with language, codec, channel layout, and sample rate.

**Acceptance criteria.**

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
  transcript).

**Test cases.**

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

**Edge cases.**

- **Mislabeled tracks.** Some MKVs declare `tags.language=eng` for an
  Arabic track. The probe records what the file claims; the transcriber's
  language auto-detect is what actually drives behavior. We never silently
  override the file's metadata.
- **Mid-file codec change.** Rare. ffprobe reports the first packet's
  codec. If the file later switches codec, the extractor will fail at run
  time and the job retries with `transcoded_extract = true` (see 2.3).
- **CDN-style fragmented streams.** `ffprobe -show_format` reports the
  full duration only after a `-analyzeduration 100M -probesize 50M`
  bump for some MPEG-TS sources; the probe applies these flags
  unconditionally.

### Story 2.2 — Track selection

When a file has multiple audio tracks, pick the one most likely to be the
intended speech for transcription.

**Acceptance criteria.**

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

**Test cases.**

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

**Edge cases.**

- **Identical-language duplicate tracks** (`eng` stereo and `eng` 5.1).
  The 5.1 wins (more channels), then ties broken by `is_default`, then
  by index.
- **Audio described / SDH descriptive tracks.** Detected by
  `disposition.descriptions = 1` or by title regex
  `(?i)\b(audio description|described|sdh|cc)\b`; excluded by default.
- **Selection determinism under re-probe.** Re-probing produces the same
  rows in the same order; selection is therefore stable across re-runs.

### Story 2.3 — Stream extraction (no intermediate WAV by default)

Extraction runs as part of the `extract` stage and feeds audio into the
transcriber via a pipe.

**Acceptance criteria.**

- **Given** a `videos` row in state `PROBED` and a selected track,
  **when** the `extract` stage runs,
  **then** it spawns
  `ffmpeg -hide_banner -nostdin -threads 1 -i {file} -map 0:a:{idx}
   -ac 1 -ar 16000 -sample_fmt s16 -f s16le pipe:1` and yields the
  resulting byte stream as an async iterator of PCM chunks (default
  64 KiB per chunk).
- **Given** an STT backend that requires a file (some `openai-whisper`
  paths, OpenAI API), the extract stage instead writes
  `~/.maktaba/cache/audio/{hash}.wav` (16-bit PCM mono 16 kHz) and
  passes its path; the file is removed when the job reaches `done`,
  `failed`, or `cancelled`.
- **Given** a file FFmpeg cannot open (corrupt header, unsupported codec
  with no decoder),
  **when** extraction starts,
  **then** the job transitions to `failed` with a structured `error`
  containing `{kind: "ffmpeg_decode", returncode, stderr_tail}`; no
  partial PCM is delivered.
- **Given** the transcriber consumes the stream and the worker is paused
  (Epic 6),
  **when** the pause check fires,
  **then** the FFmpeg process is sent `SIGTERM`, drained for up to 5 s,
  then `SIGKILL`-ed if still alive; no zombie ffmpegs survive a paused
  job.

**Test cases.**

- `test_extract_streams_pcm` — fixture file → consumer receives expected
  byte count (`duration_sec * 16000 * 2`), within ±1 chunk tolerance.
- `test_extract_pipes_directly_into_stt` — mock STT backend captures
  stream; assert the byte stream matches reference WAV's data section.
- `test_extract_to_file_when_backend_requires` — STT backend declares
  `requires_file = True` → temp WAV written; path cleaned up after job
  reaches terminal state.
- `test_extract_fails_on_bad_input` — fixture is a renamed `.txt` →
  job state `failed`, `error.kind == "ffmpeg_decode"`.
- `test_extract_kill_on_pause` — start extraction; set `pause_requested
  = true` mid-stream; assert ffmpeg process exits within 5 s and no
  PCM is committed to the transcript table.
- `test_extract_resume_uses_seek` — resume from `last_segment_end_sec
  = 320.5` → ffmpeg is invoked with `-ss 320.5` (input seek for fast
  decoder warmup); the first byte yielded corresponds to ≥320.5 s of
  the source.

**Edge cases.**

- **Variable-frame-rate sources.** `-ss` placed **before** `-i` does a
  fast-but-imprecise input seek; for VBR/VFR audio we seek slightly
  earlier (`max(0, ss - 0.5)`) and discard the lead-in until the first
  PCM sample whose presentation timestamp is ≥ requested. This keeps
  resume offsets exact.
- **Concatenated TS streams** with mid-file PTS resets. The extractor
  applies `-fflags +genpts` to force monotonic PTS; otherwise the
  per-segment `start_sec` jumps backward and breaks resume.
- **Audio in a video container with broken duration metadata.** The
  extractor falls back to "stream until EOF" rather than trusting the
  reported duration; `processing_jobs.total_duration_seconds` is
  refreshed from the actually-decoded length on completion.
- **Decoder that emits fewer samples than frame headers claim.** The
  extractor records an EWMA of `decoded_samples / declared_samples`;
  if the ratio drops below 0.95, the job is failed with
  `error.kind == "audio_drift"` for human review rather than producing
  a misaligned transcript.

### Story 2.4 — Audio resource accounting

Extraction is disk-bound and competes with streaming. The pipeline must
not flood the box with parallel ffmpegs.

**Acceptance criteria.**

- **Given** the default config `concurrency.extract = 2`,
  **when** more than two `extract` jobs are eligible,
  **then** at most two run simultaneously per worker process; the rest
  remain in `pending`.
- **Given** a high-priority user-initiated extract (priority 50) and
  saturated extract slots,
  **when** the user-initiated job becomes pending,
  **then** the next slot to free runs the priority-50 job first
  (priority is the primary order in the claim loop, §6.3).
- **Given** the streaming service is currently transcoding,
  **when** the worker checks resource pressure (optional, off by default
  in v1),
  **then** if `cpu.load_avg_5m > N × cores`, the next claim is delayed
  by `not_before = now() + 30s`. (Toggled by
  `pipeline.cpu_throttle_enabled`.)

**Test cases.**

- `test_concurrency_cap_enforced` — enqueue 5 extract jobs with cap 2
  → at most 2 running at any instant; total wall time ≈ 3 batches.
- `test_priority_overrides_fifo` — enqueue 3 jobs at priority 100, then
  one at 50 → the priority-50 runs as soon as a slot frees.
- `test_cpu_throttle_delays_claim` — simulated load avg above
  threshold, throttling on → next claim's `not_before` is bumped.

**Edge cases.**

- **A worker dies holding an extract slot.** The reaper (§6.6) flips the
  job to `paused`; the freed slot is automatically reusable.
- **A library spans multiple disks.** The cap is per-process, not
  per-disk; users with this topology can scale by running multiple
  worker processes (architecture §10.3 horizontal scale-out).

---

## Epic 3 — Transcription

**Goal.** Convert extracted audio into a sequence of `transcript_segments`
with second-accurate timestamps, supporting multiple swappable STT
backends, durable per-segment commits, exact pause/resume to the audio
second, and bidi-correct text for Arabic. This is the long-running stage
of the pipeline (hours per video) and the primary consumer of
[Epic 6](#epic-6--job-queue)'s machinery.

**Owner.** Pipeline Service, `pipeline/src/maktaba_pipeline/stt/` (backends)
and `pipeline/src/maktaba_pipeline/pipeline/stages/transcribe.py` (stage
orchestration).

**Out of scope.** Subtitle file generation (Epic 4), search indexing
(Epic 5), diarization scoring against a known speaker library (deferred to
v1.1 — `pyannote.audio` is opt-in here).

### Story 3.1 — STT backend protocol

A single `STTBackend` interface that every concrete backend implements,
verified by a backend-agnostic conformance test suite. Adding a new
backend later means writing a class and the suite passing — nothing else.

**Acceptance criteria.**

- The protocol matches
  [`specs/architecture.md §3.4`](../architecture.md):

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

**Test cases (conformance suite, run per backend).**

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

**Edge cases.**

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

### Story 3.2 — Whisper MLX backend (default on Apple Silicon)

The flagship backend; this is what 80% of users will run.

**Acceptance criteria.**

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

**Test cases.**

- `test_mlx_runs_on_apple_silicon_only` — on `arch != arm64-darwin`,
  `WhisperMLXBackend.health().ready == false` and the registry skips it.
- `test_mlx_initial_prompt_used` — `hints.initial_prompt = "بسم الله
  الرحمن الرحيم"` → the first segment of an Arabic recitation
  reproduces the prompt's vocabulary at higher confidence than without.
- `test_mlx_language_autodetect` — Arabic file with `language = None`
  → `transcript.language == "ar"` and segment text is in Arabic.

**Edge cases.**

- **Out-of-VRAM.** `mlx_whisper` raises a `RuntimeError`; the worker
  releases the GPU lock, fails the job with backoff, and the next
  attempt is allowed (model size auto-degraded if `degrade_on_oom = true`
  in library settings — `large-v3` → `medium` → `small`, recorded in
  `transcripts.metadata`).
- **Repeated identical segments** ("hallucination loop"). The backend
  detects ≥3 consecutive segments with `text` Levenshtein-distance ≤2
  and `len(text) > 10`, and forces a new decode window; this is reported
  in `transcripts.metadata.hallucination_breaks`.

### Story 3.3 — Faster-Whisper (CUDA / CPU) backend

Linux + NVIDIA path and CPU fallback.

**Acceptance criteria.**

- `FasterWhisperBackend(name="whisper-cuda" | "whisper-cpu")` wraps
  `faster_whisper.WhisperModel` with `device="cuda"` or `device="cpu"`
  selected at construction. Both share a base class to avoid duplication.
- Streaming: `transcribe(audio, ...)` yields each `Segment` as
  `faster-whisper` emits it (it does so naturally through its
  generator interface).
- Conformance suite (3.1) passes for both variants on the CI matrix
  (CPU run is mandatory; CUDA run is optional and skipped when no GPU).

**Test cases.**

- Conformance suite per device.
- `test_faster_whisper_word_timestamps_match_segment` — when word
  timestamps are enabled, sum of word durations is within ε of the
  parent segment's duration.

**Edge cases.**

- **Compute-type mismatch.** `compute_type` defaults to `float16` on
  CUDA, `int8` on CPU; if the constructor raises on the requested type,
  the backend falls back to `float32` once and records the choice.

### Story 3.4 — OpenAI API backend

For users without local hardware.

**Acceptance criteria.**

- `OpenAIWhisperBackend(name="openai-api")` calls the official Whisper
  endpoint; `cost_per_minute` populated from the live price list at
  package build time.
- `supports_streaming = false`; `requires_file = true` (the API takes a
  file upload, not a stream). The orchestrator therefore writes a
  temp WAV (Story 2.3) before calling.
- The backend chunks audio into 24 MB pieces (API limit); each chunk's
  segments are re-timestamped against the original timeline. The
  re-stitching is verified by an integration test against a 90-min
  fixture.
- Per-library budget cap (`stt.backends.openai.max_usd_per_month`)
  enforced **before** claim: a worker computes the projected cost from
  `videos.duration_sec × cost_per_minute`, sums the running total for
  the calendar month, and refuses the claim with `not_before = first of
  next month` if the projection would exceed the cap.

**Test cases.**

- `test_openai_chunking_preserves_timestamps` — 90-min fixture; assert
  segments tile the timeline contiguously and stitched timestamps match
  the single-call equivalent within ε.
- `test_openai_budget_cap` — set cap = $0.10; try to enqueue a 30 min
  transcribe → claim refused, job pushed to next month with reason
  `budget_cap`.
- `test_openai_retry_on_429` — API returns 429 → backend retries with
  exponential backoff (0.5/1/2/4/8 s, jitter ±25%) up to 5 attempts
  before failing the segment chunk.

**Edge cases.**

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

### Story 3.5 — Backend registry & per-library selection

The transcribe stage at run time picks the backend declared by the
library's settings, with a fallback chain if the chosen backend is
unhealthy.

**Acceptance criteria.**

- `pipeline.stt.registry.list()` returns every backend whose
  `health.ready == true` at the moment of the call.
- Per-library config has `stt.backend = "<name>"` (architecture §11.4).
  At job time, the stage looks up the backend, runs its preflight, and
  if `ready=false`, walks `stt.fallback = ["whisper-cuda",
  "whisper-cpu"]` until one is ready or no fallback remains; if none,
  the job fails with `error.kind = "no_backend_ready"`.
- The chosen `(backend, model)` is persisted on the `transcripts` row
  (`backend`, `model`, `backend_version`); re-running a transcribe with
  a different backend creates a **new** transcript row; the old one is
  preserved for diff/comparison and tagged with
  `transcripts.is_active = false`.

**Test cases.**

- `test_registry_filters_unhealthy` — patch one backend's `health.ready`
  to false → `list()` excludes it.
- `test_fallback_walks_chain` — primary backend reports unhealthy →
  the next ready one is used; recorded in `metrics.fallback_from`.
- `test_reprocess_creates_new_transcript_row` — re-running with a
  different `model` → new row with `is_active = true`; old row's
  `is_active` flips to false in the same transaction.

**Edge cases.**

- **All backends unhealthy at claim time.** The job is requeued with
  `not_before = now() + 60s` (rather than failed) up to
  `max_attempts`, then failed.
- **A backend listed in `fallback` is missing from the build.** Treated
  as `health.ready=false`; logged once at startup.

### Story 3.6 — Real-time per-segment durable commit

The hot path: every segment is committed atomically with a job-progress
update before the worker advances. This is the core of pause/resume.

**Acceptance criteria.**

- For each segment `s` produced by the backend, the worker executes a
  single DB transaction that:
  1. Inserts a `transcript_segments` row with monotonic `seq`,
     `start_sec`, `end_sec`, `text`, optional `speaker`, `confidence`.
  2. Updates `processing_jobs` setting
     `last_segment_end_sec = s.end`,
     `processed_seconds = processed_seconds + (s.end - prev_end)` (where
     `prev_end` is the previous `last_segment_end_sec`),
     `segments_completed = segments_completed + 1`,
     `realtime_factor = ewma(prev, audio_sec_in_segment / wall_sec)`,
     `estimated_remaining_sec = (total - processed) /
     max(realtime_factor, ε)`,
     `progress_updated_at = now()`,
     `last_heartbeat_at = now()`.
  3. (Optional) inserts `transcript_words` rows when word timestamps
     are enabled.
- Both writes are **committed together**; if either fails, the
  transaction rolls back and the worker retries the same segment.
- After every committed segment, before pulling the next one off the
  backend, the worker checks `pause_requested` and `cancel_requested`
  in the same connection and exits cleanly if either is set
  (`architecture §7.6`).
- The post-commit invariant
  `last(transcript_segments.end_sec) == processing_jobs.last_segment_end_sec`
  holds at every consistent read.

**Test cases.**

- `test_segment_commit_atomic` — inject a failure on the
  `processing_jobs` UPDATE → `transcript_segments` row is also rolled
  back; on retry, the row appears exactly once.
- `test_progress_advances_with_audio_time_not_wall_time` — synthetic
  backend yields a 60 s segment instantly → `processed_seconds`
  increments by 60, not by the (sub-second) wall time.
- `test_realtime_factor_ewma` — feed alternating fast/slow segments;
  assert `realtime_factor` is smoothed (α = 0.2) and the visible
  series is monotonically tracking the input mean.
- `test_eta_uses_smoothed_factor` — eta is consistent with
  `(total - processed) / realtime_factor` to two decimal places.
- `test_pause_request_observed_after_commit` — set `pause_requested =
  true` mid-decode → exactly one more segment commits, then the worker
  exits to `paused` with `paused_at_sec == that segment's end_sec`.

**Edge cases.**

- **Segment shorter than the prior `last_segment_end_sec`.** The backend
  produced an out-of-order segment; the orchestrator's reorder buffer
  (Story 3.1) suppresses the commit until earlier segments arrive. If
  buffering exceeds `reorder_window_sec` (default 30 s), the offending
  segment is dropped with a WARN.
- **Backend emits a "final" segment past the audio's true end.** The
  orchestrator clamps `end_sec` to `min(end_sec, audio_duration)` to
  keep `processed_seconds <= total_duration_seconds`.
- **DB write contention with the API's read traffic.** The progress
  UPDATE uses the `(id)` PK only and is therefore O(1); no risk of
  contention beyond a single row lock.

### Story 3.7 — Pause and resume to the exact second

The user's most-demanded feature: pause a 4-hour transcribe, walk away,
resume exactly where it stopped — across process restarts and host
reboots.

**Acceptance criteria.**

- **Given** a running transcribe at `last_segment_end_sec = 1234.5`,
  **when** the user calls `POST /api/jobs/{id}/pause`,
  **then** the API sets `pause_requested = true`; within one segment
  boundary the worker commits the current segment and flips state to
  `paused`, recording `paused_at_sec = last_segment_end_sec`,
  `paused_reason = 'user'`, and releasing the GPU lock.
- **Given** the same paused job,
  **when** the user calls `POST /api/jobs/{id}/resume`,
  **then** the job becomes claimable; the next worker that picks it up
  flips state to `resuming`, opens the audio decoder seeked to
  `last_segment_end_sec` (Story 2.3), rebuilds the Whisper prompt from
  the last K segments' text (default K=3), and flips to `running`. The
  next emitted segment's `start_sec >= last_segment_end_sec`.
- **Given** a paused job whose original backend is no longer available,
  **when** the user resumes,
  **then** resume succeeds with the fallback backend; the
  `transcripts` row records `metrics.resumed_with_different_backend =
  {from, to, at_sec}`.
- **Given** force-pause via `POST /api/jobs/{id}/pause?force=true`,
  **when** the worker is stuck on a single long segment,
  **then** the job flips to `paused` immediately with `paused_at_sec =
  last_segment_end_sec` (the in-flight segment is discarded; no commit
  was attempted yet) and the worker is signalled to abort its
  subprocess.

**Test cases.**

- `test_resume_starts_from_last_segment_end_sec` — pause at 600.0; resume
  → first new segment's start is ≥ 600.0 and within 0.5 s of it.
- `test_resume_across_process_restart` — pause; kill the worker process;
  start a new worker; the job is reclaimed and resumes with no rework.
- `test_resume_after_backend_change` — pause on `whisper-mlx`; change
  library setting to `whisper-cuda`; resume → succeeds, transcript
  metadata captures the change.
- `test_force_pause_drops_inflight` — start a synthetic backend that
  hangs in a single segment; force-pause → state becomes `paused`
  within 1 s, no segment was committed for the in-flight chunk.
- `test_double_resume_is_idempotent` — resume on a paused job twice
  rapidly → exactly one worker claim succeeds; the second returns 200
  with the unchanged state.

**Edge cases.**

- **Audio file moved between pause and resume.** The video's
  `content_hash` resolves the new path before extraction reopens the
  file; if the file is gone, the resume claim fails the job back to
  `pending` with `error.kind = "audio_missing"` and `not_before = +5m`.
- **Whisper prompt seam glitch.** Some Arabic recitations re-detect a
  different language at the resume boundary because the first 30 s
  after the seek are in mid-sentence. The orchestrator reuses the
  pre-pause `transcripts.language` and disables auto-detect on resume.
- **Crash mid-segment commit.** No segment row is partially committed
  (transaction atomicity); on resume, the worker rebuilds from the
  same `last_segment_end_sec` it saw before. Verified by the chaos
  test in 3.8.

### Story 3.8 — Crash recovery & graceful shutdown

Make the worker survive `kill -9`, OOM-killer, host reboot, and `SIGTERM`
on a 4-hour job without losing more than the in-flight ≤30 s segment.

**Acceptance criteria.**

- On `SIGTERM` / `SIGINT`, the worker treats it as `pause_requested`
  for every job it holds, with `paused_reason = 'shutdown'`. Each
  affected job commits the current segment (if any), flips to `paused`,
  and the process exits within `shutdown_grace_sec` (default 120 s).
- A second `SIGTERM` / second Ctrl-C aborts immediately with the same
  correctness guarantee (the in-flight segment was uncommitted, so the
  DB is consistent).
- On crash (`SIGKILL`, panic, host reboot), the reaper (§6.6) finds
  jobs whose `last_heartbeat_at < now() - stale_claim_sec` (default
  90 s) and flips them to `paused` with `paused_reason = 'crash'`,
  `paused_at_sec = last_segment_end_sec`. They are then claimable as
  resumes by any worker.
- A "chaos" pytest fixture randomly `SIGKILL`s the worker mid-job;
  after restart, the resulting transcript matches the
  no-crash baseline byte-for-byte (or within ε for non-deterministic
  backends).

**Test cases.**

- `test_sigterm_pauses_all_jobs` — start two transcribe jobs; SIGTERM
  → both rows in `paused` with reason `shutdown` within
  `shutdown_grace_sec`.
- `test_double_sigterm_aborts_fast` — second SIGTERM forces exit < 5 s.
- `test_reaper_pauses_stale_claim` — claim a job; freeze its
  heartbeats; advance simulated clock past `stale_claim_sec` → reaper
  flips it to `paused` with reason `crash`.
- `test_chaos_kill_yields_consistent_resume` — kill -9 the worker N
  times during a fixture transcribe; final segment count matches the
  no-kill run; no duplicate `seq`s.

**Edge cases.**

- **Reaper races a recovering worker.** Both attempt to mutate the same
  job. The reaper's UPDATE includes `WHERE last_heartbeat_at < now() -
  stale_claim_sec`; a heartbeating worker invalidates the predicate, so
  the UPDATE matches zero rows and the worker keeps the claim.
- **Wall-clock skew.** All times are server-side `now()`; workers never
  send wall-clock timestamps for the heartbeat. A workstation whose
  clock jumped backward cannot fool the reaper into thinking heartbeats
  are still fresh.

### Story 3.9 — Diarization (opt-in, off by default)

Tag each segment with a speaker label when the library opts in.

**Acceptance criteria.**

- Library setting `diarize = true` enables `pyannote.audio`'s pretrained
  diarization pipeline. Default is `false`.
- When enabled, the diarizer runs **before** STT on the same audio
  stream (or in parallel if memory permits) and produces a list of
  `(start, end, speaker_id)` intervals. The transcribe stage assigns
  each segment's `speaker` to whichever interval covers its midpoint.
- Speaker IDs are local to the video (`Speaker 1`, `Speaker 2`, …) at
  this stage; matching to known speakers in `speakers` table is a
  follow-up story (deferred to v1.1).
- Diarization is gated by a process-global `diarization_lock` semaphore
  (default 1) because pyannote is GPU-greedy.

**Test cases.**

- `test_diarize_off_by_default` — fixture without `diarize` setting →
  segments have `speaker = None`.
- `test_diarize_assigns_speakers` — fixture with two speakers →
  segments alternate between `Speaker 1` and `Speaker 2` matching the
  fixture's known boundaries.
- `test_diarize_disabled_skips_pipeline` — `diarize = false` → pyannote
  is never imported (verify import is lazy).

**Edge cases.**

- **Diarization disagrees mid-segment.** When a single STT segment
  spans two speakers, the segment is **split** at the diarization
  boundary into two `transcript_segments` rows with the same `seq`
  prefix and a `.a/.b` suffix in `metadata.split_from`. Word-level
  text re-assignment happens only when word timestamps are present.
- **Diarization fails entirely.** Segments are committed without
  speaker labels; the failure is recorded on the transcript row but
  does not fail the job.

---

## Epic 4 — Subtitles

**Goal.** Convert finalized transcripts into well-formed `.srt` and `.vtt`
sidecars; auto-discover external subtitle files shipped with the video;
extract embedded subtitle tracks from MKVs on demand. The Streaming
Service can render live VTT from `transcript_segments` directly (architecture
§4.5), so the **on-disk** subtitle artifacts are for portability (a Plex
or VLC user opening the same folder) and for clients that prefer file
URLs over manifest-embedded subtitles.

**Owner.** Pipeline Service, `pipeline/src/maktaba_pipeline/media/subtitles.py`
and the `subtitle_gen` stage. Embedded extraction lives in the same module
but runs lazily on first request, not as a pipeline stage.

**Out of scope.** Burning subtitles into video (the player renders them);
translation between languages (deferred per architecture Appendix B).

### Story 4.1 — Generate SRT and VTT from `transcript_segments`

When transcription completes (`state = TRANSCRIBED`), produce both subtitle
formats from the canonical segments — never from a previously written file.

**Acceptance criteria.**

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
  `is_external = false`, and the video's state advances toward `INDEXED`
  (the subsequent stage).
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

**Test cases.**

- `test_srt_round_trips` — input segments → SRT → re-parse with
  `srt` library → same number of cues, same text, timestamps within
  1 ms.
- `test_vtt_round_trips` — same against `webvtt` library.
- `test_alias_copy_uses_source_basename` — fixture
  `/lib/Lecture 1.mp4` → alias is `/lib/Lecture 1.ar.srt` (note the
  `.ar.` infix; ISO 639-1 code).
- `test_atomic_replace_on_retry` — kill the worker between writing temp
  and replace → no `.srt` at the final path; retry produces the file
  cleanly.
- `test_readonly_source_dir_does_not_fail_job` — source dir read-only
  → job state `done`, alias copy log warned, `.maktaba/subs/` file
  exists.

**Edge cases.**

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

### Story 4.2 — SRT/VTT formatting & line wrapping

Subtitles must be readable on a phone, on a 4K TV, and through external
players. Line length and break rules matter as much as the text content.

**Acceptance criteria.**

- Each cue is at most `max_line_chars = 42` characters wide and at most
  `max_lines = 2` lines (configurable per library).
- Line breaks favor:
  1. Sentence-end punctuation (`.`, `?`, `!`, `؟`).
  2. Clause-end punctuation (`,`, `;`, `،`, `؛`, `:`).
  3. Word boundaries (never break mid-word).
- Cues never overlap; if two adjacent segments are within
  `merge_gap_sec = 0.05`, they merge; if a single segment is longer than
  `max_cue_sec = 6.0`, it is split at sentence/clause boundaries with the
  text proportionally divided by character count along word-timestamp
  positions where available.
- For Arabic source language: prefer Arabic punctuation glyphs over
  Latin equivalents in the rendered cue text (the input transcript
  already contains them where the STT model produced them; the wrapper
  must not "normalize" them away).
- VTT cues include speaker tags (`<v Speaker 1>...`) only when
  diarization ran and `speaker IS NOT NULL`.

**Test cases.**

- `test_wrap_respects_max_line_chars` — segment text 200 chars → no
  output line > 42 chars.
- `test_wrap_breaks_at_sentence` — fixture sentence with mid-segment
  period → line break exactly there, not at the next word boundary.
- `test_no_overlap_after_merge_or_split` — generated cues' time spans
  do not overlap; `cue[i].end <= cue[i+1].start`.
- `test_long_segment_split_proportionally` — 12 s single segment with
  word timestamps → split into 2–3 cues each ≤ `max_cue_sec`, each
  cue's time range matches its text's word-timestamp range.
- `test_arabic_punctuation_preserved` — input contains `؟` → output
  contains `؟`, not `?`.
- `test_speaker_tag_only_when_diarized` — `speaker = NULL` → VTT cue
  has no `<v>` tag.

**Edge cases.**

- **Word timestamps absent** but segment too long for one cue. We split
  by character count along the segment duration linearly — imperfect
  but defensible; record `metadata.split_method = "linear"`.
- **Tokens that themselves exceed `max_line_chars`** (URLs, hashtags).
  Such tokens are placed on their own line; the line is allowed to
  exceed the limit (one violation logged per file at DEBUG).
- **Bidi text mixing Arabic and English.** Wrap by grapheme cluster, not
  byte; verified against a fixture containing surrogate pairs and
  combining marks.

### Story 4.3 — External subtitle auto-discovery

Files like `Lecture 1.ar.srt` shipped alongside the video should appear
in the library without anyone running an explicit pipeline.

**Acceptance criteria.**

- During scanning (Epic 1), for each video file the scanner also matches
  the regex
  `^<basename>(?:\.(?P<lang>[a-z]{2,3}))?\.(?P<ext>srt|vtt|ass|ssa)$`
  against siblings in the same directory.
- Each match creates a `subtitle_files` row with `is_external = true`,
  `language = <lang or 'und'>`, `format = <ext>`, `transcript_id =
  NULL`, and `path = <absolute>`.
- An external `.ass` or `.ssa` file is recorded but **not** converted at
  scan time; conversion to VTT is deferred to first request by the
  Streaming Service (architecture §4.5), which writes the converted
  artifact to `.maktaba/subs/`.
- Re-scanning does not duplicate `subtitle_files` rows; uniqueness is
  `(video_id, language, format, is_external, path)`.

**Test cases.**

- `test_external_srt_discovered` — fixture dir with
  `Lecture.mp4 + Lecture.ar.srt` → exactly one `subtitle_files` row,
  `language = 'ar'`, `is_external = true`.
- `test_external_no_lang_tag` — fixture with `Lecture.srt` (no
  `.ar.` infix) → `language = 'und'`.
- `test_external_ass_recorded_not_converted` — `.ass` file → row
  exists; no `.vtt` is generated.
- `test_rescan_idempotent` — run scan twice → row count unchanged.

**Edge cases.**

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

### Story 4.4 — Embedded subtitle extraction

Some MKVs ship subtitles embedded as `S_TEXT/UTF8` or PGS streams. The
Streaming Service requests these from the Pipeline on demand.

**Acceptance criteria.**

- Probe (Story 2.1) records `media_info.has_subtitles = true` whenever
  ffprobe reports any subtitle stream, plus a list of `(index, codec,
  language)` in `media_info.raw_ffprobe`.
- An RPC `Pipeline.ExtractEmbeddedSubtitle(video_id, stream_index)`
  extracts the requested stream as VTT and writes it to
  `.maktaba/subs/<hash>.<lang>.embedded.vtt`, returning the path. The
  call is idempotent: a second call returns the cached file.
- Text-codec subs (`subrip`, `webvtt`, `ass`, `ssa`) are converted via
  `ffmpeg -map 0:s:N -c:s webvtt`. Bitmap-codec subs (`hdmv_pgs_subtitle`,
  `dvdsub`) are **not** converted in v1; the API returns
  `unsupported_subtitle_codec` and the UI hides them. (Recording the
  decision here so it is not silently re-attempted.)
- The extracted file appears in `subtitle_files` with `is_external =
  false`, `is_embedded = true` (new column).

**Test cases.**

- `test_embedded_text_extraction` — fixture `subs.mkv` with a `subrip`
  English track at index 2 → `Pipeline.ExtractEmbeddedSubtitle(id, 2)`
  produces a parseable WebVTT file with the expected cue count.
- `test_embedded_idempotent` — call twice → file path returned both
  times; ffmpeg is invoked exactly once (verify by mocking
  subprocess).
- `test_embedded_pgs_returns_unsupported` — fixture with PGS subs →
  RPC returns `unsupported_subtitle_codec`; no file is written.

**Edge cases.**

- **Stream language tag missing.** Defaults to `und`; user can rename
  via `PATCH /api/videos/{id}/subtitles/{id}` once a follow-up endpoint
  exists (deferred to v1.1).
- **Multiple subtitle tracks at the same language.** Each becomes its
  own row; the user picks per-session via the manifest.

### Story 4.5 — Live VTT serving (read-side, contract only)

The Streaming Service renders live VTT directly from `transcript_segments`
(architecture §4.5). This story owns the **contract** the Pipeline must
honor, not the Streaming code itself (which lives in
[`02-streaming.md`](./02-streaming.md), TBD).

**Acceptance criteria.**

- A read-only SQL view `transcript_segments_v` is created with columns
  `(video_id, transcript_id, seq, start_sec, end_sec, text, speaker,
  is_active)` and an index on `(video_id, start_sec)`.
- Only segments whose parent transcript has `is_active = true` are
  visible through the view.
- Pipeline-side write paths never lock the view's rows for more than the
  duration of a single-segment transaction (Story 3.6). This is
  guaranteed by row-level locks in Postgres and by SQLite's WAL mode.

**Test cases.**

- `test_view_excludes_superseded_transcripts` — two transcripts for
  one video, only one `is_active` → view returns only the active one's
  segments.
- `test_view_index_supports_window_query` — `EXPLAIN` of a
  `(video_id, start_sec BETWEEN x AND y)` query uses the index.

**Edge cases.**

- **Live read during the per-segment commit.** Because the segment
  insert and progress UPDATE share one transaction (Story 3.6),
  readers see all-or-nothing.

---

## Epic 5 — Search & Indexing

**Goal.** Make every transcribed second searchable in two complementary
ways — exact-phrase / proximity (Postgres `tsvector` or SQLite FTS5) and
semantic (ChromaDB) — and fuse them into one ranked result set with
language filters, deep-linkable timestamps, and snippet highlighting that
gets Arabic right.

**Owner.** Pipeline Service writes to both indexes
(`pipeline/src/maktaba_pipeline/search/`); the API Service reads (and
proxies semantic queries to Pipeline via gRPC `Embed`).

**Out of scope.** Saved searches and search analytics (API surface only,
covered in `03-api.md` TBD); cross-language *translation* of queries
(deferred per architecture Appendix B; cross-language *retrieval* is in
scope through the multilingual embedding).

### Story 5.1 — Search-unit chunking

Whisper segments are typically 5–30 s and may be a fragment of a sentence;
embeddings work better on small coherent units (~200 chars). The indexer
re-chunks segments into "search units" before writing.

**Acceptance criteria.**

- **Given** a transcript's segments,
  **when** the indexer runs,
  **then** it produces "search units" each containing 1–3 consecutive
  sentences with target ~200 characters and hard cap 400. A unit's
  `start_sec` is the first segment's `start`, its `end_sec` is the last
  segment's `end`.
- **Given** a segment that itself contains multiple sentences,
  **when** chunked,
  **then** each sentence becomes its own unit; segment boundaries are
  not load-bearing — the indexer reads `text` and re-segments by
  punctuation, then maps each new unit back to the segment(s) it
  derived from.
- **Given** an Arabic transcript,
  **when** chunking,
  **then** sentence boundaries are detected on `[.!?؟।]` plus
  newline; trailing whitespace stripped; combining marks preserved.
- The mapping `unit_id → list[segment_id]` is stored in
  `transcript_units(unit_id, transcript_id, seq, start_sec, end_sec,
  text, segment_ids JSONB)` so a search hit always resolves back to a
  precise segment timestamp.

**Test cases.**

- `test_chunking_target_length` — long fixture → distribution of unit
  lengths has median in `[150, 250]` chars and 99p ≤ 400.
- `test_chunking_arabic_punctuation` — fixture using `؟` →
  sentence break occurs there.
- `test_unit_to_segment_mapping_recovers_timestamps` — pick any unit;
  resolve its `segment_ids[0]` → that segment's `start_sec` equals the
  unit's `start_sec`.
- `test_chunking_does_not_drop_text` — concatenation of all unit texts
  (after de-NFC, preserving sentence joins) equals the concatenation of
  segment texts byte-for-byte.

**Edge cases.**

- **A single "sentence" longer than the cap.** Split at the nearest
  word boundary ≤ cap; record `metadata.split_method = "word"`.
- **No punctuation at all** (rare; bad STT output). The whole transcript
  is chunked by character count along word boundaries with target 200;
  `metadata.no_punctuation = true` for triage.

### Story 5.2 — FTS5 / `tsvector` exact-phrase index

The deterministic, cheap layer of search.

**Acceptance criteria.**

- For SQLite, the schema in `architecture §8.3` is created. For Postgres,
  an equivalent layer is created using a `tsvector` column on
  `transcript_units` with a GIN index, plus `pg_trgm` for prefix queries:

  ```sql
  ALTER TABLE transcript_units
    ADD COLUMN tsv tsvector
    GENERATED ALWAYS AS (
      to_tsvector(
        coalesce(language_to_regconfig(language), 'simple'),
        text
      )
    ) STORED;
  CREATE INDEX ON transcript_units USING GIN (tsv);
  CREATE INDEX ON transcript_units USING GIN (text gin_trgm_ops);
  ```

  where `language_to_regconfig` maps `ar → arabic`, `en → english`,
  `und → simple`.
- Diacritic-insensitive matching is enabled — for SQLite via
  `tokenize='unicode61 remove_diacritics 2'`, for Postgres via the
  `arabic` text-search config we ship (Pipeline owns the dictionary
  files in `shared/db/tsearch/`).
- Inserts into `transcript_units` automatically populate the FTS layer
  (SQLite uses triggers; Postgres uses the `STORED` generated column).
- Backfilling FTS on a 15,000 h library (architecture §10.1) finishes in
  under 30 minutes on the reference hardware.

**Test cases.**

- `test_fts_match_exact` — index a unit with text "الحمد لله رب
  العالمين" → query "الحمد لله" returns it; query "العالمين" returns
  it.
- `test_fts_match_diacritics_stripped` — unit with diacritics-bearing
  Arabic text → query without diacritics matches.
- `test_fts_proximity_query` — query `"الحمد" NEAR/3 "العالمين"` →
  matches a unit where the words are within 3 tokens.
- `test_fts_indexed_on_insert` — insert a unit; immediately query →
  result returned without any explicit reindex.
- `test_fts_language_specific_stopwords` — Arabic unit with stopword
  `في`; query just `في` → recall is reduced (matches stopword removal).

**Edge cases.**

- **Mixed-language unit** (Arabic with English code-switching). The
  `tsvector` is built with `'simple'` config when `language = 'und'`;
  for typed mixed content the dominant language tag is used and the
  English tokens are simply stemmed under Arabic rules — accepted, since
  semantic recall covers this case.
- **Backfill on existing data.** A migration that adds the GIN index
  uses `CREATE INDEX CONCURRENTLY` (Postgres) so the live API does not
  stall.
- **FTS query with no results.** Returns `total = 0`, `hits = []`;
  never errors. Empty queries are rejected at the API layer.

### Story 5.3 — ChromaDB vector index

The semantic layer.

**Acceptance criteria.**

- One Chroma collection per library, named `library-<library_id>`,
  configured with `{"hnsw:space": "cosine"}` (architecture §8.4).
- Embedding model `intfloat/multilingual-e5-large` by default,
  configurable via `pipeline.toml [search].embedding_model`. The model
  is loaded at process start and cached; `e5-base` is selected
  automatically on hosts without a GPU and `embedding_device = 'auto'`
  (recorded as `metrics.embedding_model_actual` in `transcripts`).
- For each search unit, the indexer adds a Chroma row with
  `id = "{transcript_id}:{seq}"`, `documents = unit.text`,
  `metadatas = {video_id, library_id, start, end, language, speaker}`.
  The id format is stable across re-runs so re-indexing upserts in place.
- An `Embed(text)` gRPC RPC encodes one query and returns the
  vector; the API uses this for query-time embedding (architecture
  §1.4) so the model stays in the Pipeline process only.
- Indexing throughput goal: at least 200 units/second on Apple Silicon
  with `e5-large`, sufficient to keep up with transcription.

**Test cases.**

- `test_chroma_add_and_query` — add 10 units; query the first unit's
  text → top-1 hit is itself.
- `test_chroma_idempotent_upsert` — add same id twice with different
  text → only the latest is stored.
- `test_embedding_dim_matches_model` — assert vector length equals the
  configured model's dim (1024 for `e5-large`, 768 for `e5-base`).
- `test_embed_grpc_returns_same_vector` — call `Embed("foo")` twice →
  identical vector; difference < 1e-6.
- `test_indexer_throughput` — fixture of 10,000 units → wall time
  ≤ 50 s on the reference machine (parameterized in CI).

**Edge cases.**

- **Library deleted while index exists.** A `DELETE FROM libraries`
  cascades to videos and transcripts, but Chroma is external; a
  cleanup hook removes the Chroma collection in the same transaction
  (best-effort; orphaned collections are removed by a nightly task).
- **Embedding model swap mid-library.** Vectors are not transferable;
  switching the model triggers a full library re-index. The settings
  endpoint shows a "this will reindex N hours of content" warning
  before applying.
- **GPU OOM during embedding.** The indexer falls back to CPU for the
  current batch (recorded), then resumes on GPU.

### Story 5.4 — Hybrid retrieval with Reciprocal Rank Fusion

Two indexes need to merge into one ranking. RRF is chosen because it is
score-scale-agnostic and implementation-trivial.

**Acceptance criteria.**

- A `search(query, mode, filters, limit)` API in
  `pipeline.search.engine`:
  - `mode = "fts"` → BM25-only, top-K from the FTS layer.
  - `mode = "semantic"` → cosine-only, top-K from Chroma.
  - `mode = "hybrid"` (default) → both, fused via RRF
    `score(d) = Σ_i 1 / (k + rank_i(d))` with `k = 60` (per Cormack
    et al.).
- Filters supported in v1: `library_id`, `video_id`, `language`,
  `speaker`, `min_duration_sec`, `max_duration_sec`, `created_after`,
  `created_before`. Filters are pushed down to both indexes — Chroma
  metadata filter, FTS via SQL `WHERE`.
- Result shape matches `architecture §9.3`'s search response with
  per-hit `matches` array of `{segment_id, start_sec, end_sec, text,
  speaker}`. Snippet highlighting wraps the matched span(s) in
  `<mark>` tags. For Arabic, highlighting is grapheme-aware (no
  splitting combining marks).
- A request P95 latency target of 200 ms for `limit ≤ 50` on a
  15,000 h library (architecture §10.1).

**Test cases.**

- `test_rrf_combines_two_lists` — two synthetic ranked lists overlap on
  some docs → RRF score for shared docs > either-list-only docs.
- `test_filters_pushdown` — query with `language = 'ar'` returns only
  hits from `transcript_units` whose `language = 'ar'`; verified by
  inspecting the executed SQL and Chroma `where=`.
- `test_snippet_highlight_arabic_grapheme_safe` — query containing a
  letter with combining marks; snippet does not split the cluster.
- `test_search_latency_target` — 1,000-query benchmark on the seed
  fixture; P95 ≤ 200 ms.
- `test_deep_link_resolves_to_segment` — a hit's `(video_id,
  start_sec)` pair, when followed via the API, returns a segment
  whose bounds contain that timestamp.

**Edge cases.**

- **Empty query.** Rejected at the API with HTTP 400; the engine never
  sees an empty string.
- **Query in a language the unit's index config doesn't know.** The FTS
  layer falls back to `simple` config; the semantic layer handles it
  natively. Cross-language hits are valid (English query, Arabic
  result) and tagged with `metadata.cross_language = true` in the
  response.
- **Ties in RRF.** Broken by `(start_sec ASC, segment_id ASC)`, making
  the result deterministic across calls.
- **Filter that excludes all hits from one of the indexes** (e.g.,
  `language = 'fr'` on a library that has only Arabic). The other
  index's results are returned as-is, no error.

### Story 5.5 — Incremental indexing

The indexer must keep up with live transcription, not run as a giant
batch at the end.

**Acceptance criteria.**

- The `index` stage runs on a video as soon as `transcribe` reaches
  `done`. It indexes only the units whose `transcript_id` matches the
  newly-completed transcript and whose `unit.indexed_at IS NULL`.
- Indexing also runs **incrementally during** transcription: a
  background "live indexer" task in the same worker subscribes to
  `LISTEN segments.committed` (Postgres) or polls every 5 s (SQLite),
  re-chunks any new segments into units, and writes them to FTS only
  (Chroma is deferred to the post-transcribe stage to amortize
  embedding cost). This makes search return live partial results
  while a long video is still transcribing.
- Re-processing a video (model upgrade, settings change) creates a new
  active transcript and indexes it in place; the old transcript's units
  are deleted from FTS and Chroma in the same transaction that flips
  `is_active = false` on the old transcript.

**Test cases.**

- `test_live_indexer_updates_fts_during_transcribe` — start a long
  fixture; query for a phrase that appears 1 minute in → after the
  segment containing it commits, the FTS layer returns it within 10 s.
- `test_chroma_added_only_at_index_stage` — during live transcribe,
  Chroma collection size does not grow; after `index` stage, it
  contains all units.
- `test_reindex_replaces_old_transcript` — re-process; old units are
  removed from both indexes; new units present.

**Edge cases.**

- **Transcribe paused mid-video.** Live FTS indexing pauses naturally
  (no new segments arriving). Resumed transcribe picks up; live
  indexer continues. No special handling needed.
- **Crash during live indexing.** `unit.indexed_at IS NULL` is the
  resume key; the indexer is idempotent.
- **A unit straddling a paused→resumed seam.** The live indexer waits
  until `processing_jobs.last_segment_end_sec` advances by at least
  one full unit's worth before chunking, to avoid re-chunking a partial
  unit that grew across a resume.

### Story 5.6 — Search query suggestions

Autocomplete dropdown for the search box.

**Acceptance criteria.**

- `GET /api/search/suggest?q=al` returns a ranked list of up to 8
  suggestions drawn from:
  1. The user's recent saved searches (architecture §8.5).
  2. Speaker names in the active library.
  3. High-frequency n-grams (2–4 tokens) from `transcript_units` that
     start with the prefix, computed offline via a nightly task.
- Latency target: P95 ≤ 50 ms.
- Arabic prefix matches use `pg_trgm` GIN on Postgres or FTS5 prefix
  tokens on SQLite (`MATCH 'al*'`).

**Test cases.**

- `test_suggest_includes_saved_search` — saved search "الحمد" → typing
  "ال" includes it.
- `test_suggest_speakers` — speakers `["Sheikh A", "Sheikh B"]` →
  typing "Sh" suggests both.
- `test_suggest_latency` — 1,000 calls; P95 ≤ 50 ms.

**Edge cases.**

- **Empty corpus.** Returns the user's saved searches only; no error.
- **Mixed-script prefix.** "al" returns Latin matches and the
  trigram-equivalent Arabic matches if any (typically none; that's
  fine).

---

## Epic 6 — Job Queue

**Goal.** Implement the durable, atomic, pause-aware job queue that every
other pipeline stage rides on. The queue is a single Postgres table
(`processing_jobs`, schema in `architecture §7.1`), not a broker, not a
Celery, not a Redis. All claim, heartbeat, retry, pause, resume, cancel,
and reaper logic is here, and is the single concern of this epic — the
stages above just call into it.

**Owner.** Pipeline Service,
`pipeline/src/maktaba_pipeline/pipeline/runner.py` (claim loop, worker)
and `pipeline/src/maktaba_pipeline/db/jobs.py` (SQL).

**Out of scope.** Per-stage implementations (Epics 1–5); UI rendering of
job state (`web/src/pages/queue.tsx`, separate doc); the API endpoints
themselves (`/api/jobs/*`, owned by `03-api.md`).

### Story 6.1 — Schema, migration, indexes

The schema in architecture §7.1 lands as one migration with all the
indexes needed for the claim, reaper, and progress queries.

**Acceptance criteria.**

- Migration `shared/db/migrations/000X_jobs.sql` creates
  `processing_jobs` exactly as specified in architecture §7.1, including
  all four indexes:
  - `(state, priority, not_before)` — the claim index.
  - `(video_id, stage)` — for "what's pending for this video".
  - `(state, last_heartbeat_at) WHERE state IN ('claimed', 'running',
    'resuming')` — the reaper's partial index.
  - `(pause_requested) WHERE pause_requested = true` — the pause poller.
- The same migration runs on Postgres and on SQLite (with the noted
  type swaps in architecture §8.0 preamble).
- An `enqueue(video_id, stage, priority, payload?)` Python helper writes
  one row, sets `state = 'pending'`, `attempts = 0`,
  `not_before = NULL`, returns the new id. Idempotency: if a row with
  the same `(video_id, stage)` already exists in a non-terminal state,
  return its id without inserting (or, if the stage is meant to be
  re-runnable like `index`, skip when state is `done` and the source's
  `updated_at <= last run finished_at`).

**Test cases.**

- `test_migration_creates_indexes` — query `pg_indexes` (or
  `sqlite_master`) → all four indexes present.
- `test_enqueue_idempotent` — call `enqueue(v, 'probe')` twice → only
  one row.
- `test_enqueue_skips_when_done_and_source_unchanged` — a `done` row
  with finished_at > video.updated_at → enqueue is a no-op.
- `test_enqueue_creates_new_when_source_changed` — bump
  `videos.updated_at` past `finished_at` → enqueue inserts a fresh
  pending row.

**Edge cases.**

- **Concurrent enqueue.** Two callers race on the same `(video_id,
  stage)`. The unique partial index `UNIQUE (video_id, stage) WHERE
  state IN ('pending','claimed','running','resuming','paused')`
  guarantees at most one non-terminal row per pair; the loser's INSERT
  raises and is converted to "row already pending".
- **SQLite single-writer.** The enqueue path uses a brief `BEGIN
  IMMEDIATE` to serialize writes; readers continue under WAL.

### Story 6.2 — Claim loop

The atomic primitive every worker is built on.

**Acceptance criteria.**

- The Postgres claim is exactly the SQL in architecture §7.3 (single
  UPDATE with `SELECT … FOR UPDATE SKIP LOCKED`).
- The SQLite claim uses an asyncio lock + `BEGIN IMMEDIATE` to emulate
  `SKIP LOCKED`, with the same semantics: at most one worker holds any
  given job at any time.
- The claim returns either a fully-populated `Job` or `None` (no work).
- The claim accepts both `pending` and `paused` rows whose
  `pause_requested = false` — i.e., a "resume" is just a claim against a
  paused row. The worker disambiguates from `state` and walks into
  `resuming` accordingly.
- The claim respects `cancel_requested = false`, `not_before <= now()`,
  and `stage = ANY($supported_stages)`.
- A worker that just claimed sees `state='claimed'`, `claimed_by =
  worker_id`, `claimed_at = now()`, `attempts = attempts + 1`.
- The claim never blocks indefinitely; the worker's claim cadence is
  driven by either `LISTEN job.pending` (Postgres) or polling at
  `claim_poll_sec` (default 1 s; SQLite).

**Test cases.**

- `test_claim_atomic_under_contention` — start N=10 in-process workers
  against the same DB; enqueue 100 jobs → each job is claimed by
  exactly one worker; sum of claims = 100.
- `test_claim_respects_priority` — enqueue jobs at priority 100 then
  one at priority 50 → the 50 is claimed first.
- `test_claim_skips_not_before` — job with `not_before = now() + 60s`
  → not claimable until time has advanced.
- `test_claim_picks_up_paused_resume` — a paused row with
  `pause_requested = false` is returned by `claim()` and the worker
  transitions it to `resuming`.
- `test_claim_returns_none_when_empty` — no eligible rows → returns
  `None` without raising.

**Edge cases.**

- **A worker dies between `SELECT` and `UPDATE`.** Cannot happen: the
  claim is one atomic UPDATE; the SELECT is in its sub-query.
- **A row whose `cancel_requested = true` arrives at the front.** It
  is skipped by the WHERE; cancellation is enacted by the cancel
  responder (Story 6.4), not by the claim loop.
- **Stage filter mismatch.** A worker that supports only `transcribe`
  never claims `index` jobs even if priority would otherwise win.

### Story 6.3 — Heartbeat & progress

The worker proves it's alive by writing to the same row it claimed.

**Acceptance criteria.**

- While running, the worker calls `tick(job_id, processed_seconds_delta,
  segments_completed_delta, last_segment_end_sec, realtime_factor,
  estimated_remaining_sec)` after every committed segment (Story 3.6).
  The function executes a single UPDATE that bumps progress and sets
  `last_heartbeat_at = now()`, `progress_updated_at = now()`.
- For non-transcribe stages (probe, index, thumbnail) that don't have
  natural segment cadence, the worker emits a pure heartbeat tick every
  `heartbeat_sec` (default 5 s) that updates only `last_heartbeat_at`.
- A `LISTEN job.progress` channel fires on each progress UPDATE; the
  payload is `{id, video_id, stage, state, last_segment_end_sec,
  processed_seconds, total_duration_seconds, realtime_factor,
  estimated_remaining_sec}` exactly per architecture §7.10.
- Pure heartbeat ticks do **not** fire `job.progress` (that's reserved
  for actual progress); they fire `job.heartbeat` instead, consumed
  only by the reaper, not by the UI.

**Test cases.**

- `test_progress_updates_visible` — call `tick`; subscribe to
  `LISTEN job.progress` → exactly one notification with matching
  payload.
- `test_heartbeat_only_does_not_emit_progress` — call the heartbeat-
  only path → no `job.progress` notification, but
  `last_heartbeat_at` advanced.
- `test_progress_payload_shape` — payload JSON matches the schema
  in §7.10 byte-for-byte.

**Edge cases.**

- **Stage that completes faster than the heartbeat interval.** No
  problem; the next tick is the completion UPDATE that flips state to
  `done`.
- **A long-running pure-CPU step inside one stage** (e.g., a 60 s
  ffmpeg decode for a single segment). The pure heartbeat path
  guarantees liveness even when the per-segment commit cadence
  exceeds `stale_claim_sec`.

### Story 6.4 — Pause, resume, cancel via request flags

Control plane: API only sets request flags, never mutates live state.

**Acceptance criteria.**

- `POST /api/jobs/{id}/pause` (handled by API but the contract is here)
  sets `pause_requested = true` and returns 200 with the job's current
  state. The endpoint is idempotent.
- `POST /api/jobs/{id}/pause?force=true` additionally executes
  `UPDATE … SET state = 'paused', paused_at = now(), paused_at_sec =
  last_segment_end_sec, pause_requested = false, claimed_by = NULL
  WHERE state IN ('claimed','running','resuming')` and signals the
  worker (via Postgres `NOTIFY job.force_pause` with the job id) to
  abort its subprocess.
- `POST /api/jobs/{id}/resume` sets `pause_requested = false` and, if
  the row was in `paused`, leaves it claimable. (No state change here;
  the next claim does the transition.)
- `POST /api/jobs/{id}/cancel` sets `cancel_requested = true`. The
  worker observes this on the next per-segment check (Story 3.6) and
  flips state to `cancelled`.
- The worker's per-segment check uses one cheap query: `SELECT
  pause_requested, cancel_requested FROM processing_jobs WHERE id =
  $1` (uses PK index, < 1 ms).

**Test cases.**

- `test_pause_request_is_idempotent` — call pause twice → same
  response, single state transition.
- `test_force_pause_drops_inflight` — covered in Story 3.7.
- `test_resume_does_not_mutate_state_directly` — resume on a paused
  job → `state` remains `paused` until a worker claims it.
- `test_cancel_after_pause_is_consistent` — cancel a paused job → state
  becomes `cancelled`, no orphaned worker references.
- `test_pause_observed_within_one_segment_window` — synthetic 1 s
  segments; set pause; assert ≤ 2 segments commit before the worker
  exits (timing tolerance for race after the check).

**Edge cases.**

- **Pause requested before claim.** A `pending` row with
  `pause_requested = true` is excluded from the claim WHERE; effectively
  it's "shelved" and only resume clears the flag.
- **Cancel requested mid-resume context-rebuild.** The `resuming`
  state's setup phase periodically polls cancel and aborts cleanly,
  flipping to `cancelled` from `resuming`.

### Story 6.5 — Backoff and retry

Transient failures should retry; permanent ones should stop wasting CPU.

**Acceptance criteria.**

- A failed job whose `attempts < max_attempts` is **not** failed
  permanently. The worker sets `state = 'pending'`, `not_before =
  now() + backoff(attempts)`, and writes the failure to `error` as
  structured JSON `{kind, message, traceback?, retryable: true}`.
- Backoff is `min(60 × 2^(attempts-1), 3600) ± 25%` jitter — i.e.,
  60 s, 120 s, 240 s, …, capped at 1 h.
- A failed job whose `attempts >= max_attempts` becomes `failed`
  (terminal). The error is preserved.
- A non-retryable error (signal: `error.retryable = false` from the
  stage) goes straight to `failed`, irrespective of attempts.
- `POST /api/jobs/{id}/retry` resets a `failed` job: `state = 'pending'`,
  `attempts = 0`, `not_before = NULL`, `error = NULL`.

**Test cases.**

- `test_retry_with_backoff` — first attempt fails → state `pending`,
  `not_before` ≈ `now() + 60s`.
- `test_max_attempts_terminal_fail` — three consecutive failures →
  fourth state is `failed`, no further retries.
- `test_non_retryable_skips_retries` — stage raises with
  `retryable=False` on first attempt → job state `failed` immediately.
- `test_retry_endpoint_resets_state` — failed job; call retry → row's
  attempts back to 0, state pending.

**Edge cases.**

- **`max_attempts = 1`** (no retries).  Behaves identically to a
  non-retryable first failure.
- **A retry's `not_before` lands during a configured maintenance
  window.** No special handling; the row remains pending and is
  claimed when the window passes.

### Story 6.6 — Reaper for crashed claims

A worker that died holding a `claimed`/`running`/`resuming` row must
release it.

**Acceptance criteria.**

- A reaper task runs every `reaper_interval_sec` (default 30 s) and
  executes:

  ```sql
  UPDATE processing_jobs
     SET state = 'paused',
         paused_at = now(),
         paused_at_sec = last_segment_end_sec,
         paused_reason = 'crash',
         claimed_by = NULL,
         pause_requested = false
   WHERE state IN ('claimed', 'running', 'resuming')
     AND last_heartbeat_at < now() - $stale_claim_sec
   RETURNING id;
  ```

  Default `stale_claim_sec = 90` (3× the 30 s heartbeat).
- For each reaped id, the reaper emits a `LISTEN job.reaped` notify
  with `{id, prev_state, paused_at_sec}`. The API surfaces this in the
  job's history.
- The reaper is **per-instance** but uses a `pg_advisory_lock` to
  prevent multiple Pipeline workers from running it simultaneously
  (only one runs at a time per DB).
- The reaper never reaps `done`, `failed`, `paused`, `cancelled`, or
  `pending` rows — only the live-claim states.

**Test cases.**

- `test_reaper_pauses_stale_claim` — covered in 3.8 from the worker
  side; here, assert the SQL pattern with synthetic clock.
- `test_reaper_skips_fresh_heartbeats` — claim with `last_heartbeat_at
  = now()` → reaper's UPDATE matches zero rows.
- `test_reaper_advisory_lock` — start two reaper tasks; only one
  acquires the lock; the other returns immediately.
- `test_reaper_emits_notify` — listener catches `job.reaped` for each
  reaped row.

**Edge cases.**

- **Clock skew between client and server.** Reaper compares server-
  side `now()` against server-side `last_heartbeat_at`; client clocks
  are irrelevant.
- **A worker that revives just as the reaper runs.** The UPDATE's
  WHERE filters by `last_heartbeat_at < now() - stale_claim_sec`; a
  recent heartbeat invalidates the predicate and the row is left alone.
  If both happened (heartbeat and reap) in the same millisecond, only
  one wins per row-level lock; the worker, seeing its row mutated to
  `paused`, exits cleanly.

### Story 6.7 — Concurrency model & per-host caps

Workers must declare what they can run, and the host must not be
oversubscribed.

**Acceptance criteria.**

- A worker process loads its concurrency map from
  `pipeline.toml [workers].concurrency` (architecture §11.4); defaults
  match §7.4 (`scan=4`, `probe=4`, `extract=2`, `transcribe=1`,
  `index=4`, `thumbnail=2`).
- Each stage has an in-process `asyncio.Semaphore` whose size is the
  declared concurrency; a worker's claim loop attempts a semaphore
  `acquire(timeout=0)` before claiming a job for that stage and
  releases after the job reaches a terminal-or-paused state.
- For GPU-bound stages, an additional process-global lock keyed by
  `device_id` (default `"cuda:0"` or `"mlx:0"`) serializes work on the
  same physical device, even across stages (e.g., `transcribe` and
  diarization both lock the GPU).
- A worker's `--stages` flag scopes which stages it claims; running
  multiple workers with disjoint stage sets is the recommended way to
  scale beyond the per-process caps.

**Test cases.**

- `test_concurrency_cap_respected` — 5 extract jobs queued, cap 2 →
  exactly 2 in `running` at any sample.
- `test_gpu_lock_serializes_transcribe` — two `transcribe` jobs queued,
  one GPU → second waits in `pending` (or its semaphore is acquired
  but its claim is delayed by the device lock); never two
  simultaneously running on the same device.
- `test_disjoint_stage_workers_scale` — two workers with `--stages
  transcribe` and `--stages index` respectively → both can run
  concurrently with no contention.

**Edge cases.**

- **Multi-GPU host.** Devices are enumerated at worker startup; the
  GPU lock is per-device; transcribe concurrency = number of devices.
- **A worker process loses access to its GPU mid-job** (driver crash).
  The job fails with a retryable error; the device is marked
  unhealthy for `device_recheck_sec` (default 5 min).

### Story 6.8 — Graceful shutdown semantics

The whole queue layer must shut down cleanly on `SIGTERM` so that no
running job is forgotten in `claimed`/`running` state.

**Acceptance criteria.**

- On signal, the worker:
  1. Sets a global `shutdown_requested` event.
  2. Stops the claim loop (no new claims).
  3. Sets `pause_requested = true` for every job it currently holds, in
     a single UPDATE keyed on `claimed_by = $worker_id`.
  4. Waits up to `shutdown_grace_sec` (default 120 s) for those jobs to
     reach `paused` (the per-segment check in §3.6 sees the flag and
     transitions cleanly).
  5. If any job is still not paused after the grace period, force-pauses
     it (the same effect as `?force=true`) and exits.
- The reaper's existence guarantees safety even if force-pause fails:
  the next reaper interval sweeps any orphaned claims to `paused` with
  `paused_reason = 'crash'`.
- Tests use a real `SIGTERM` (subprocess) plus a synthetic backend so
  that the assertion is end-to-end.

**Test cases.**

- `test_shutdown_pauses_all_claims` — start workers with two running
  jobs; SIGTERM → both become `paused` with reason `shutdown` within
  grace.
- `test_shutdown_force_pauses_after_grace` — synthetic backend that
  ignores pause; SIGTERM with grace 5 s → after 5 s, jobs forced to
  `paused`.
- `test_no_orphan_after_kill_minus_nine` — `kill -9` the worker; assert
  reaper sweeps within `stale_claim_sec`.

**Edge cases.**

- **Two `SIGTERM`s in quick succession.** The second forces an
  immediate exit (architecture §7.8); reaper is responsible for
  cleanup. Verified.
- **Container orchestrator's TERM-then-KILL window shorter than
  `shutdown_grace_sec`.** Document that operators should set the
  Compose `stop_grace_period` to ≥ `shutdown_grace_sec + 30s` or
  accept the reaper-driven path.

### Story 6.9 — Observability hooks

Operators need to see what the queue is doing without grepping logs.

**Acceptance criteria.**

- `GET /api/queue/stats` (contract owned here, implementation in API)
  returns:
  ```json
  {
    "by_stage": {
      "transcribe": {"pending": 12, "running": 1, "paused": 3,
                     "failed": 0, "done": 184},
      ...
    },
    "by_state": {"pending": 22, "running": 3, ...},
    "eta_total_sec": 31738.4,
    "realtime_factor_p50": 0.31
  }
  ```
- A Prometheus-compatible `/metrics` endpoint emits, at minimum:
  - `maktaba_jobs_total{stage,state}` gauge.
  - `maktaba_job_attempts_total{stage,outcome}` counter.
  - `maktaba_job_duration_seconds{stage,outcome}` histogram.
  - `maktaba_job_realtime_factor{stage}` summary.
- Structured logs (`structlog` JSON, architecture §11/§12.5) include
  `{job_id, video_id, stage, state, attempts}` on every state-changing
  event.

**Test cases.**

- `test_stats_aggregates_correctly` — fixture jobs across stages and
  states → stats match.
- `test_metrics_include_all_required_keys` — scrape `/metrics`; assert
  each metric and label is present.
- `test_log_event_for_state_transition` — capture logs during a full
  job lifecycle; assert `transition_to_running`, `paused_for_user`,
  `transition_to_done` are present with required fields.

**Edge cases.**

- **Empty queue.** Stats return zeros; metrics still emitted (so
  alerting on "no jobs running for X minutes" works).
- **A long-failing job creating noisy retry logs.** Logged at WARN
  with backoff window; debounce via the same `not_before` to avoid
  log spam.

### Story 6.10 — Single source of truth for resume

Capture the central correctness invariant — `last_segment_end_sec` is
the resume offset, never wall clock, never file mtime, never a
JSON sidecar.

**Acceptance criteria.**

- The DB constraint
  `last_segment_end_sec >= 0 AND last_segment_end_sec <=
  COALESCE(total_duration_seconds, last_segment_end_sec)` is enforced
  by a CHECK constraint on `processing_jobs`.
- A migration test asserts no other table has a column named
  `*_resume_offset` or similar — an architectural smoke test that the
  invariant isn't accidentally duplicated.
- A property test runs across crash/resume cycles for synthetic
  workloads and asserts: at every consistent read,
  `last(transcript_segments WHERE transcript_id = T) .end_sec
  == processing_jobs.last_segment_end_sec`.
- The runner refuses to write to a sidecar file as a checkpoint;
  attempts to do so fail a unit test that lints `pipeline/`.

**Test cases.**

- `test_invariant_after_crash_resume` — chaos-kill loop (Story 3.8);
  invariant holds at each restart.
- `test_no_sidecar_checkpoint_files` — grep `pipeline/` for
  `partial.json`, `checkpoint`, `_resume` patterns → none.

**Edge cases.**

- **A backend that emits an end past `total_duration_seconds`.** The
  per-segment commit clamps as described in 3.6's edge cases; the
  invariant still holds.
- **A migration that adds new columns.** The CHECK constraint stays
  pinned to `last_segment_end_sec`; new resume-related state must be
  proven derivable from it or rejected in code review.

---

## Cross-cutting concerns

These apply to every epic and are listed once here rather than scattered
across stories.

### Logging & telemetry

- Use `structlog` with JSON output (architecture §2.1, §12.5). One log
  event per state transition, plus per-segment DEBUG events that are
  off by default.
- OpenTelemetry traces span the full pipeline life cycle for a video:
  `pipeline.scan → pipeline.probe → pipeline.extract → pipeline.transcribe
  → pipeline.subtitle_gen → pipeline.index → pipeline.thumbnail`. Trace
  IDs propagate across the API ↔ Pipeline gRPC boundary.

### Configuration discipline

- All numeric defaults named in this document (debounce, heartbeats,
  caps, backoff windows) are exposed in `pipeline.toml` under names
  matching the variables used here. No "magic numbers" in code without
  a corresponding config knob.

### Testing infrastructure

- Every epic above ships with a `pytest -m <epic>` marker. CI runs all
  markers in parallel.
- A shared `tests/fixtures/media/` directory holds short royalty-free
  clips per language (architecture §12.1's `shared/fixtures/samples/`).
  Long-form fixtures (>10 min) are generated on demand by concatenating
  the short clips so the repo stays small.
- Integration tests run against the same Postgres image used in the
  Compose deployment (`postgres:16`); SQLite tests run against a
  per-test in-memory DB.

### Definition of done

A story is "done" when:

1. Acceptance criteria are met by code in `pipeline/`.
2. All listed test cases pass in CI on both Postgres and SQLite.
3. Edge cases are covered by tests or explicitly documented as
   "won't fix in v1" in this file with a follow-up issue link.
4. The architecture doc is updated only if the implementation diverged
   from the design — this epic file is the working contract; the
   architecture doc is the design intent.
5. `make test` passes locally on the reference machine (Apple Silicon
   M-series) end-to-end including the chaos test in Story 3.8.

---

## Story dependency graph (for planning)

```
1.1 ─┬─ 1.2 ─ 1.3 ─ 1.4 ─ 1.5
     │
     └─ 6.1 ─ 6.2 ─ 6.3 ─┬─ 6.4 ─ 6.5 ─ 6.6 ─ 6.7 ─ 6.8 ─ 6.9 ─ 6.10
                          │
                          └─ 2.1 ─ 2.2 ─ 2.3 ─ 2.4
                                                 │
                                                 ├─ 3.1 ─┬─ 3.2
                                                 │       ├─ 3.3
                                                 │       └─ 3.4
                                                 │           │
                                                 │           └─ 3.5 ─ 3.6 ─ 3.7 ─ 3.8 ─ 3.9
                                                 │                                     │
                                                 │                                     ├─ 4.1 ─ 4.2 ─ 4.3 ─ 4.4 ─ 4.5
                                                 │                                     │
                                                 │                                     └─ 5.1 ─ 5.2 ─ 5.3 ─ 5.4 ─ 5.5 ─ 5.6
```

Stories 6.1–6.3 unblock the full pipeline; 3.6 and 3.7 are the
correctness keystones; 5.5 closes the live-search loop. Ship in that
order and every intermediate state of the codebase remains a working,
deployable system.
