# Epic 47: Keep libraries offline, in MP4, and without duplicates: three queue jobs

## Why

The home's libraries are mostly YouTube channels, one library per channel, under
`/Volumes/Originals/Backup/Youtube/`. Most were downloaded with Tartube or yt-dlp and arrive in mixed
containers: `.mkv`, `.webm`, `.mp4` and the odd `.avi`. The same lecture is often in two places,
for example after a re-download or because it is in two playlists. The owner asked for three things,
each run from the existing Queues page and with no new UI anywhere else:

1. **Convert to MP4** without losing a file. Keep the original until the new MP4 is proven to play.
2. **Remove duplicates** without losing a file or anything attached to it: watch history,
   playlists, favourites, transcripts and embeddings.
3. **Refresh a library from YouTube**, so it stays offline. Download only what is new, with
   metadata, subtitles and thumbnails, and keep to YouTube's rate limits.

The owner pointed at two of their own repos as the starting point: **FileUtilities** for encoding
and clean-up, and **Make-It-Offline** (MIO), an unfinished yt-dlp front end inspired by Tartube.
This epic says what each repo gives us and what Maktaba already has, then plans the three
pipelines.

## What exists today (checked 2026-10-28 against `origin/main` 1bde8839)

### In Maktaba

| Need | What is there | Gap |
|---|---|---|
| Queue, workers, retries, cancel, progress | `processing_jobs` (0002, 0058, 0088, 0143), the `GRAPH` in `pipeline/.../engine/graph.py`, claiming with `SKIP LOCKED` (`engine/claim.py`), backoff (`pipeline/backoff.py`), failure classes and parking (Epic 40), `ctx.progress()` / `ctx.check_cancel()` (`engine/context.py`), `POST /api/jobs/{id}/{pause,resume,cancel,retry}` | none: the new stages fit it as they are |
| Queues page | `/queue` (`QueueOverview`, `FlowLanes`/`QueueCard`, `LibrariesTable` with a per-library *Requeue*, `RunPanel`, `/queue/failures`). Stage tiles come from `job_stages`. All admin-only (`requireAdmin`, `queues.go`) | per-library actions for the new jobs, a YouTube sources dialog, and a review list for duplicates |
| Remux to MP4 | `library_mgmt/repair.py`: remux with `-c copy`, falling back to AAC audio, checked with `ffprobe`, copied beside the file, size checked, swapped in, original kept as `.broken`. The streaming `Remuxer` (`streaming/internal/ffmpeg/remux.go`) is never called and writes only to its cache | no stage converts a library's files; there is no full-decode check and no trash |
| Duplicates | `library_mgmt/dedup.py` (Story 9.4) is **never called**. The scanner **silently merges** a second identical file into the first file's row: `ON CONFLICT (library_id, content_hash) DO UPDATE SET path=…` (`scanner/store.py`), so only one of the two paths is tracked. `video_signals.thumb_dhash`/`frame_hashes` (0090) feed series detection only. `web/mockups/admin/duplicates.html` is a mockup only. The library merge (Epic 45) deletes rows whose hash is already in the target library | no detection or review of duplicates, and no merge that keeps user data |
| YouTube | `youtube_channels` (`library_id`, `url`, `status`), `youtube_videos`, `youtube_playlists`, `youtube_playlist_items` (0090). The `youtube_sync` stage (library subject, pool `network_yt`) **lists** channels and handles bot checks. `repair_broken` + `repair.py` already **download** one video by id (`download_args`, `format_for`, a local work folder, cookies from the browser). yt-dlp is the optional `[mediaintel]` extra | nothing downloads the new items a listing finds; there are no playlist subscriptions and no archive file |
| Trash | none. The only backups are `.broken` files from repair; Epic 45's *Empty trash* forgets `missing` rows | a file trash with a grace period |

Two facts shape every design below:

- **`videos.content_hash` is a partial hash.** It is BLAKE3 over the first 4 MiB, the last 4 MiB and
  the size (`identity/hasher.py`), and it is unique per library. A converted file gets a new hash,
  and two different files could in theory share one.
- **The thumbnail cache is keyed by content hash** (`~/.maktaba/cache/thumbnails/<hash>/`). A
  converted file's cached thumbnails must be moved to the new key or made again.

### FileUtilities (`~/Code/FileUtilities`, C#/.NET 8 + Avalonia, last commit 2026-01-01)

A desktop toolbox with **Encode** (batch ffmpeg), Split, Merge, Clean Up, Rename and Explore, plus an
EF Core audit history (`TrackedFile`, `FileAuditEntry`). It is complete for what it does, with xUnit
tests for the services.

What it offers:

- **Encode presets** (`MD.WinUtilities.UI/Models/AppSettings.cs:39-127`), for example
  `-c:v h264_videotoolbox -b:v 8M -c:a aac -b:a 192k -movflags +faststart`, HEVC VideoToolbox, and
  NVENC, QSV and AMF variants. We **port** the VideoToolbox H.264 and HEVC presets and
  `+faststart`.
- **The encode pipeline** (`EncodeService.cs`): scan, then probe any existing output, then an access
  check, then encode. It retries, deletes the partial output on failure or cancel, and **skips an
  input whose output already exists and verifies**, which makes reruns cheap. We **port the shape**:
  skip if verified, delete partials, retry.
- **Verification** (`VerifyOutputAsync`, `EncodeService.cs:909-939`): the output exists and is not
  empty, `ffprobe` reads its duration, and the duration matches the input within a tolerance. We
  **port it and make it stronger** (see below). A duration check alone passes a file whose middle is
  corrupt.
- **Unicode file-name handling for network shares** (`TryOpenFileWithNormalization`, `:987-1030`):
  if opening a path fails, try its NFC form, then its NFD form, then `ditto` into a temp file on
  macOS. This is relevant to the Arabic file names on the NAS. We **port it** into the convert
  stage's open step.
- **Clean Up** (`CleanUpService.cs`) deletes `X.mkv` whenever `X.mp4` exists, **with no check of the
  MP4**. This is the exact pattern the owner wants to avoid. **Not reused.**
- **No deduplication at all**: no hashing and no duplicate detection. The owner may have expected
  some, but the dedup plan below rests on Maktaba's own hashes and signals.
- **Not reused:** the Avalonia UI, the EF/SQLite history (Maktaba has `job_events` and adds
  `file_actions` below), and DeepL renaming.

We **port the ideas to Python** and don't call the .NET binaries. Shipping a .NET runtime in
Maktaba.app for about 200 lines of logic is not worth it, and the pipeline already owns ffmpeg.

### Make-It-Offline (`github.com/Hamza-Labs-Core/Make-It-Offline`, C#/.NET 9 + Avalonia, one squashed commit, 2026-02-05)

MIO is a desktop yt-dlp front end. It drives yt-dlp through the YoutubeDLSharp wrapper and keeps
channels, playlists and videos in SQLite. Its shape is borrowed from Tartube: a **check** step
lists a source and records new videos, then a **download** queue fetches them. It is about half
done:

- The Playlists and Channels views and the Tartube import trigger are TODO.
- Auto-download is `null` on the view-model code paths, and it bypasses the queue with `Task.Run`.
- Subtitle, rate-limit and proxy arguments are built and then never passed to yt-dlp
  (`DownloadService.cs:855-899`).
- It has no `--download-archive`. "Already downloaded" is `Url.Contains(id) && Status == Complete`,
  so a Queued or Failed video is inserted again on every check.
- Retries never see the error type (the prefix is written to a different object), so every failure
  is retried, bot checks and removed videos included.
- About 1,200 tests, mostly with mocks, and none against real yt-dlp.

What we take from it:

- **The check/download split.** In Maktaba, `youtube_sync` is the check and the new `youtube_fetch`
  is the download.
- **The error table** (`DownloadService.cs:19-39, 1139-1248`): BotDetection, RateLimited,
  AuthRequired (private, age-gated, members-only), GeoBlocked, VideoUnavailable, NetworkError,
  FormatUnavailable and ExtractorError. We **port** it as an ordered regex list into Epic 40's
  failure classes, and fix the plumbing so the class reaches the retry decision.
- **Per-source options** (`DownloadProfile`): maximum height, audio only, subtitle languages and
  cookies. `DownloadProfile.ToYtDlpOptions()` serves as the checklist of argument mappings.
- **Folder-name sanitising** (`DownloadService.cs:1294-1324`).
- **The Tartube import** (`tools/convert_tartube_db.py`). It loads `tartube.db`, a pickle, without
  Tartube installed by stubbing the missing classes. It is useful for seeding the archive from an
  existing Tartube database: every video with `dl_flag` set becomes a `youtube <id>` line. Two
  caveats:
  - Its `find_class` tries real imports first, so it is a full `pickle.load` and can run code. We
    port it with a stub-only unpickler and run it only on a file the owner names, on the home,
    never on an upload.
  - It drops `options_obj`, the per-channel options.

What we don't take: title-only output templates (two videos with the same title collide), the
`.part` path guessing, `Url.Contains` matching, the non-flat 200-video fetch on every check, and
everything that is UI, telemetry or translation.

What neither repo has and we add: `--download-archive`, `--write-info-json`, `--write-thumbnail`,
`--sleep-requests`/`--sleep-interval`, `--retries`/`--fragment-retries`, `--match-filter` to skip
live and upcoming streams, and progress parsing through `--progress-template`.

## Goals

- Three admin-only, queue-driven pipelines, each one or more stages in the existing `GRAPH`, run
  from the Queues page per library:
  - **A. Convert to MP4**
  - **B. Find and merge duplicates**
  - **C. Refresh from YouTube**
- **No file is ever lost.** Nothing is deleted before its replacement or keeper is verified. A
  removed file goes to a **trash folder on the same volume** and is purged only after a grace period
  (the owner decides the period; see Decisions). Every move is recorded in `file_actions` and can be
  undone until the purge.
- **No user data is lost.** A converted video keeps its `videos.id`, so every row that points at it
  keeps pointing at it. A merged duplicate's user data is moved to the keeper before its own row is
  retired.
- Crash-safe: a crash at any point leaves either the old file or the new file in place and tracked,
  and the next run finishes or rolls back the step.

## Non-goals

- Any UI outside `/queue`. Libraries, Home and the player stay as they are.
- Changing how streaming plays a file. Direct-playable MP4s mean fewer live transcodes, a free win.
- Downloading anything other than YouTube (yt-dlp's other sites can come later).
- Live streams and premieres (skipped by `--match-filter`).
- Re-encoding to save space. Converting means making a file MP4; shrinking it is a separate decision.

## Design

### Shared: the file-action ledger and the trash

**`file_actions`** (new) is one row per file operation. It is the audit, the crash-recovery journal
and the undo list:

```
file_actions(
  id uuid PK, library_id → libraries ON DELETE CASCADE,
  video_id → videos ON DELETE SET NULL, job_id bigint,
  kind text CHECK (kind IN ('convert','dedup','youtube_fetch','purge','restore')),
  state text CHECK (state IN ('planned','staged','verified','swapped','done','rolled_back','failed')),
  old_path text, new_path text, trash_path text,
  old_hash text, new_hash text, old_size bigint, new_size bigint,
  verify jsonb,          -- what the check measured: durations, streams, decode errors, frames
  purge_after timestamptz, purged_at timestamptz,
  created_at, updated_at
)
```

**The trash** is `<library root>/.maktaba/trash/<kind>/<yyyy-mm-dd>/<relative path>`. The scanner
and walker already skip `.maktaba` (`scanner/walker.py`). Because the trash is on the same volume
and share, moving a file in is a `rename`: atomic, with no copy and no space doubling.

A **`trash_purge`** stage (library subject, pool `nas_write`, daily on the leader) deletes the trash
entries whose `purge_after` has passed, records `purged_at`, and stops if a file's size no longer
matches the ledger. **Restore** is a failures-page-style action on a `file_actions` row: move the
trash file back, then reverse the DB change.

**The swap order** (common to A and C, and to B's loser):

1. Stage the new file beside the target as `.<name>.maktaba-new.mp4`. This is a dotfile, so the
   watcher and scanner ignore it. Check its size against the local copy, then **verify it in place
   on the share**. Ledger: `staged`, then `verified`.
2. In one DB transaction, update the existing `videos` row: `path`, `filename`, `content_hash`,
   `size_bytes`, `mtime`. The `id` is kept. Ledger: `swapped`. The row is updated **before** the
   rename, so when the watcher sees the new file, the scanner's upsert on `(library_id,
   content_hash)` finds this row and makes no new one.
3. `rename(original → trash)`, then `rename(staged → final name)`. Ledger: `done`.
4. Recovery on worker start and in the stage's own retry: a ledger row in `swapped` finishes steps
   3–4 when the staged file is there, or else puts the DB row back to `old_*`. A row in `staged` or
   `verified` deletes its staged file and starts again.

**The verification** (`library_mgmt/verify.py`, shared by A and C). The FileUtilities check is the
floor; this is what we add:

- `ffprobe -show_streams -show_format` reads the file. It has a video stream (unless the original
  had none), every audio stream the original had, and the same width and height. A remux keeps the
  same codec. Duration is within `max(0.5 s, 0.5 %)` of the original.
- **A full decode**: `ffmpeg -v error -xerror -i new -map 0:v:0 -map 0:a? -f null -` exits 0 with an
  empty stderr. Every frame of the full duration is read once. This is CPU work, about 20–60× real
  time with VideoToolbox decoding, and it runs at `nice` on the convert pool.
- **The frame count** of the first video stream matches the original's (`-count_packets`), within 1
  for a remux and within 0.5 % for a re-encode.
- **The moov atom at the front** (`+faststart`), so the browser can start playback without reading
  to the end.

The result is stored in `file_actions.verify`, so the failures page can say exactly why a file was
not swapped.

**Unicode paths.** Opening goes through the FileUtilities fallback: the path as given, then NFC,
then NFD, then on macOS a `ditto` copy into the work folder.

### A. Convert to MP4

**Stage `convert_mp4`.** Subject: video. A new pool, `convert`, size 1 by default, placed on the
`home`. Lane: media. It is queued for a library from the Queues page and by a rule on `scan` when
`settings.convert.auto` is on (off by default).

**Which files.** From `media_info` and `audio_tracks`:

- **Skip**: already `.mp4`/`.m4v` with H.264 or HEVC video, AAC, AC-3 or E-AC-3 audio, and the moov
  atom at the front. Outcome `skipped_unchanged`.
- **Remux** (`-c copy`, lossless): H.264 or HEVC video with AAC/AC-3/E-AC-3/MP3 audio, in another
  container (`.mkv`, `.mov`, `.ts`), or an MP4 whose moov is at the end.
- **Audio only** (`-c:v copy -c:a aac -b:a 192k`): MP4-ready video with Opus or Vorbis audio, the
  usual YouTube `.webm`/`.mkv` case.
- **Re-encode** (lossy): VP9, AV1 (where the owner's devices don't decode it), MPEG-4 Part 2,
  WMV and so on. Uses the FileUtilities VideoToolbox preset (H.264 or HEVC, see Decisions), with
  `libx264 -crf 20 -preset medium` as the fallback. Only when `settings.convert.allow_reencode` is
  on; otherwise the outcome is `skipped` with the reason `needs_reencode`.

**Subtitle and attachment streams.** MKV text subtitles become `mov_text` inside the MP4 **and** are
written as `<name>.<lang>.srt` beside it, so nothing is lost. Image subtitles (PGS, VobSub) can't go
into MP4: those files are skipped with the reason `image_subtitles` unless the owner chooses to drop
them. Chapters and global metadata are kept with `-map_metadata 0 -map_chapters 0`.

**Steps.**

1. Copy or open the input from the local work folder `~/.maktaba/work/convert/<job>/`. As
   `repair.py` learned, ffmpeg's mux step fails when writing to the NAS.
2. Run ffmpeg into the work folder, with progress from `-progress pipe:1` sent to `ctx.progress`,
   and `ctx.check_cancel()` between chunks. Cancelling kills ffmpeg and deletes the partial file.
3. Verify locally, copy to the staged name on the share, then verify again on the share. The share
   check is cheap: a size check plus `ffprobe`. The full decode runs once, locally.
4. Swap, then re-key the thumbnail cache directory from the old hash to the new one. Enqueue
   `thumb_small`/`thumb_hash` only if the re-key fails.
5. Outcome `changed`. The new `content_hash` must **not** cascade into transcribe, index or
   embeddings, since the audio and words are the same. `convert_mp4` emits `changed_subjects` only to
   the stages that read the container (`probe`/`access_check`). Transcripts, chapters, embeddings and
   user data stay as they are because the `videos.id` is unchanged.

**Priority.** 150, below normal pipeline work (100), so a library conversion never starves new
videos. A per-video *Convert now* from the failures page uses 50.

**Failures** (Epic 40 classes):

| Cause | Class | What happens |
|---|---|---|
| `ffmpeg_decode` on the **input** | fail at once | marked Needs you, with "the original is damaged; Repair (YouTube) or Ignore" |
| verification failed (`convert_verify`) | fail after 1 retry, the retry forcing a re-encode | original untouched, `verify` JSON shown |
| share unavailable | park on `dependency:share:<root>` | — |
| disk full in work folder | park on `dependency:disk:work` | — |

**Undo.** Restore puts the original back from the trash, restores `old_*` on the row, and moves the
MP4 to the trash.

### B. Deduplicate without loss

**Detection tiers:**

| Tier | Signal | Confidence | Applied |
|---|---|---|---|
| **Exact** | same `content_hash` **and** the same full-file BLAKE3 (`videos.full_hash`, new, computed on demand by `dedup_scan` for candidates only) | certain | automatically, if `settings.dedup.auto_exact` |
| **Same video, other file** | same `media_metadata_enrichment.external_id` (YouTube id), or the same title once normalised, with a duration within 1 s **and** a `video_signals.frame_hashes` dHash distance ≤ 6 on at least 80 % of sampled frames | probable | always reviewed |
| **Converted pair** | the ledger says so (A's own original), so it is never reported | — | — |

The scanner change (story 47.5): today a second byte-identical file overwrites the first file's
`path`. Instead it records the extra path in `video_copies(video_id → videos ON DELETE CASCADE, path,
size_bytes, mtime, seen_at)` and keeps the row's own path. `dedup_scan` then reports these copies as
exact duplicates. That makes the hidden ones visible: copies that are on disk today but tracked by
nobody.

**Scope.** Within a library by default, or across all libraries when the admin asks (the owner's 66
channel libraries share lectures). A cross-library merge keeps the keeper in its own library and
never moves files between libraries.

**Stages:**

- **`dedup_scan`.** Subject: library. Pool `nas_read`. Priority 150. It finds the candidate groups,
  computes `full_hash` for exact candidates (sequential reads, progress in bytes), and writes:
  - `duplicate_groups(id, library_id, tier, status: open|applied|dismissed|failed, keeper_video_id,
    reason, created_at, decided_by, decided_at)`
  - `duplicate_members(group_id, video_id, path, is_copy bool, score jsonb)`

  A dismissed group is remembered by the hash of its sorted member ids, so it doesn't reappear.
- **`dedup_apply`.** Subject: a duplicate group (a new `subject_kind 'dup_group'`). Pool `nas_write`.
  Exact groups run with `auto_exact` on; the rest go through the existing review flow
  (`write_mode = review`, outcome `awaiting_review`, then **Apply** on the Queues page).

**Which copy is kept.** The first rule that separates the copies decides:

1. A file that plays directly (MP4 with H.264/HEVC and AAC) beats one that doesn't.
2. The higher resolution, then the higher video bitrate.
3. More audio tracks or subtitle streams.
4. The copy with more user data: watch progress, playlist entries, favourites.
5. The oldest `videos.created_at`, the one people have known longest.
6. The shorter path, as a stable tie-break.

The admin can choose another keeper in the review list.

**The merge** (one DB transaction per group, then the files):

1. **User data moves to the keeper.** For each table, a row that would collide with one the keeper
   already has is merged, not dropped:

   | Table | Rule |
   |---|---|
   | `playback_state` | keep the most recent position; `completed` if either is |
   | `watch_sessions`, `streaming_sessions` | repointed; history is additive |
   | `user_playlist_items` | repointed, keeping the loser's position; skipped if the keeper is already in that playlist |
   | `user_favorites` | union |
   | `collection_items`, `video_tags`, `episode_tags`, `video_topics`, `series_episodes` | union, keeping the keeper's position on a clash |
   | `recommendation_dismissals`, `user_recs` | union / dropped (recomputed) |

2. **Derived data is moved only if the keeper lacks it**: transcripts, subtitle files, chapters,
   transcript units, `video_embeddings`, `segment_embeddings`, `media_metadata_enrichment`. For an
   exact duplicate it is the same content. Otherwise the keeper's own is kept and the loser's is
   left to cascade at purge.
3. **The loser's row** becomes `state = 'superseded'` (an existing state) with
   `videos.superseded_by = keeper` (new column). The row is kept until purge, so the merge can be
   undone and `device_downloads` / `purge_log` (no foreign keys) still resolve.
4. **The loser's file** is moved to the trash, after one last check that the keeper's file is
   readable (`ffprobe`) and, for exact groups, that its `full_hash` still matches. Ledger kind:
   `dedup`.
5. `trash_purge` later deletes the file, and only then the superseded row (cascading its derived
   data).

A table added later that points at `videos.id` would be silently left behind. So `dedup_apply` reads
the list of foreign keys to `videos` from the catalogue at start-up and **refuses to run** if any is
not covered by the rules above. A test makes the same check, so a new migration fails CI until the
merge rules cover it.

**Failures.** The keeper unreadable: group `failed`, nothing moved. A move to the trash fails:
the transaction has committed, so the loser stays superseded with its file in place and the ledger
row `failed`. Retry moves the file only.

### C. Refresh a library from YouTube (make it offline)

**Sources.** `youtube_channels` already ties a channel to a library. Playlists are new:

```
youtube_sources(
  id uuid PK, library_id → libraries ON DELETE CASCADE,
  kind text CHECK (kind IN ('channel','playlist')), url text, external_id text,
  folder text,                 -- relative to the library's first root; default: the source's sanitised title
  mode text CHECK (mode IN ('off','new_only','all')) DEFAULT 'new_only',
  date_after date,             -- 'new_only' sets it to the day the source is added
  options jsonb,               -- max_height, sub_langs, audio_only, cookies_from_browser
  status text, error text, last_checked_at, last_fetched_at,
  UNIQUE (library_id, kind, external_id)
)
```

A migration backfills one `channel` row for each existing `youtube_channels` row, with
`mode = 'off'`. Nothing starts downloading until the owner turns a source on.

```
youtube_downloads(
  library_id, youtube_id, PK (library_id, youtube_id),
  source_id → youtube_sources ON DELETE SET NULL,
  state text CHECK (state IN ('wanted','downloading','done','skipped','gone','failed')),
  reason text,                 -- members_only, private, age_gate, geo, live, removed, too_old …
  video_id → videos ON DELETE SET NULL,
  attempts int, failure_class text, error text, not_before timestamptz,
  bytes bigint, requested_at, done_at
)
```

**Stages:**

- **`youtube_sync`** (exists; this is the **check**). Extend it to:
  - list `youtube_sources` playlists as well as channels, using `--flat-playlist` plus
    `--break-on-existing` against the archive, so a refresh reads only the new head of each list;
  - after listing, mark `wanted` every listed id that is in an enabled source, passes `date_after`,
    and has no local video. "Local" means a `media_metadata_enrichment` row with `provider =
    'youtube'` for that id in this library, or a `youtube_downloads` row that is `done`;
  - enqueue `youtube_fetch` when anything is wanted.
- **`youtube_fetch`** (new; this is the **download**). Subject: library. Pool `network_yt` (size
  1). Priority 120. One job per library drains its `wanted` list in upload order. This keeps one
  stream of requests per home, which is what YouTube's limits want. Progress shows "12 of 40" plus
  the bytes of the current item.

  For each item, yt-dlp runs into `~/.maktaba/work/yt/<job>/`. It reuses `repair.py`'s
  `format_for()` and adds:

  ```
  --no-playlist --download-archive <root>/.maktaba/yt-dlp-archive.txt
  -f <format_for('mp4', max_height)> --merge-output-format mp4
  --write-info-json --write-thumbnail --convert-thumbnails jpg
  --write-subs --write-auto-subs --sub-langs <ar,en by default> --convert-subs srt
  --match-filter "!is_live & live_status!=is_upcoming"
  --retries 10 --fragment-retries 10 --sleep-requests 1 --sleep-interval 5 --max-sleep-interval 30
  [--limit-rate <rate>] [--cookies-from-browser <browser>]
  --newline --progress-template "download:%(progress)j"
  -o "<plain id name>.%(ext)s"
  ```

  Then it verifies the file (the shared verification), moves it with its sidecars into
  `<root>/<folder>/<upload_date> <title> [<id>].mp4`, writes the archive line, and calls the
  scanner's single-file upsert. The normal pipeline then picks the video up: `link` reads the
  `.info.json`, and probe, transcribe and the rest follow. Whether the archive is up to date doesn't
  matter: the DB is the truth, and the archive is rebuilt from `youtube_downloads` (`done`) at the
  start of each job.

  `ctx.check_cancel()` runs between items, and during a download it kills yt-dlp and leaves the
  `.part` file in the work folder. yt-dlp's own `--continue` resumes it on the next run, while the
  work folder is kept for 24 h.

**Rate limits and errors.** The MIO table is ported as an ordered regex list in
`integrations/youtube_errors.py`:

| yt-dlp says | Class | Item | Job |
|---|---|---|---|
| "Sign in to confirm you're not a bot" | `youtube_bot_check` (exists) | stays `wanted` | **parks** on `dependency:youtube`, with the source `status = 'bot_check'` |
| HTTP 429 / "Too Many Requests" | `youtube_rate_limited` | stays `wanted`, `not_before = now + backoff` | parks for at least 1 h |
| members-only, private, age-gated without cookies | `skipped` | `skipped` with the reason | continues |
| geo-blocked | `skipped` | `skipped` (`geo`) | continues |
| "Video unavailable", removed, terminated | `gone` | `gone` | continues |
| network, timeout, 5xx | transient | `attempts++`, retried later in the same job, up to 5 | continues |
| requested format not available | retry once with `-f b` | | continues |
| verification failed | `convert_verify` | `failed`; the work file is kept for a look | continues |

After 3 failed items in a row with the same class, the job stops early with that class. A broken
yt-dlp shows up as one failure, not 400.

**The schedule.** `settings.youtube.refresh_every_hours` per library (default 24, 0 = manual) is
read by the existing scheduler that queues `youtube_sync`. A *Refresh now* on the Queues page queues
`youtube_sync` with `force` for that library, or for all of them.

**Seeding** (story 47.11). Before a source's first refresh, the archive is seeded from the
library's existing videos that have a YouTube id. Optionally it is also seeded from a Tartube
database the owner points at (the MIO import, ported with a stub-only unpickler), so the first
refresh doesn't download the channel again.

### The Queues page (the only UI)

All of the following is **admin only**, server-side, with a role test per endpoint.

- **New tiles** appear on their own from `job_stages`: `convert_mp4`, `dedup_scan`, `dedup_apply`,
  `youtube_fetch` and `trash_purge`, with their health from Epic 40.
- **`LibrariesTable`**: each library row gets a menu with **Refresh from YouTube**, **Convert to
  MP4**, **Find duplicates** and **YouTube sources…**, plus a bulk version for the selected rows. New
  columns: *YouTube* (sources on, wanted / downloaded / failed, last refresh), *Not MP4* (count) and
  *Duplicates* (open groups).
- **YouTube sources dialog** (per library): list, add (paste a channel or playlist URL; the API
  resolves it with `yt-dlp --flat-playlist -J --playlist-end 1`), set the mode and options, turn a
  source off, and *Retry gone/skipped*. The dialog lives on the Queues page because the owner wants
  no UI elsewhere.
- **Duplicates review** (`/queue/duplicates`, a sub-page like `/queue/failures`): groups with each
  member's path, size, resolution, codec, watch data, and a frame strip from `chapter_thumbs`/the
  poster. Per group: choose the keeper, Apply, Dismiss. Bulk Apply for exact groups.
- **File actions** (`/queue/file-actions`): the ledger, with filters by kind and state, **Restore**
  per row, and the trash size per library with the next purge date.
- **Per job**: the progress, cancel and retry the page already has. A failed item in
  `youtube_downloads` or a failed verification appears in `/queue/failures` with its reason and the
  existing actions (retry, ignore), plus *Restore* where there is a ledger row.

### API (Go, `api/internal/handlers/queues/…`)

Every endpoint is `requireAdmin`, audited, and has a non-admin 403 test.

- `POST /api/libraries/{id}/convert`, `POST /api/libraries/{id}/dedup-scan` and
  `POST /api/libraries/{id}/youtube/refresh` enqueue through `processing_jobs_enqueue`, with
  `{all: bool}` for every library.
- `GET/POST/PATCH/DELETE /api/libraries/{id}/youtube/sources[/{sid}]`.
- `GET /api/queues/duplicates` (filters, keyset paging), `POST
  /api/queues/duplicates/{gid}/{apply,dismiss}` (`{keeper_video_id?}`).
- `GET /api/queues/file-actions`, `POST /api/queues/file-actions/{id}/restore`.
- `GET /api/libraries/{id}/offline-summary` (the new columns).

### Schema (slot `0166_offline_convert_dedup`, with `.sqlite.sql`)

- New tables: `file_actions`, `video_copies`, `duplicate_groups`, `duplicate_members`,
  `youtube_sources`, `youtube_downloads`.
- New columns: `videos.full_hash text`, `videos.superseded_by uuid → videos ON DELETE SET NULL`.
- `subject_kind` gains `dup_group`.
- New `worker_pools` row `convert` (size 1, placement `home`). New `job_stages` rows:
  `convert_mp4`, `dedup_scan`, `dedup_apply`, `youtube_fetch`, `trash_purge`.
- Backfill: one `youtube_sources` row per `youtube_channels` row, with `mode = 'off'`.
- Grants to `maktaba_worker`. A `MANIFEST.md` row. Bump `SCHEMA_REV` (pipeline) and `SchemaRev`
  (streaming).
- Purely additive: no existing data changes except the backfill, which writes new rows only.

## Testing (never the live DB or live media)

- **Database:** scratch Postgres via `MAKTABA_TEST_PG_URL` (`pgtest.ScratchDB`, the pipeline `db`
  fixture on SQLite and Postgres). The migration is tested up and down on a copy of the schema at
  0165.
- **Media:** generated per test with `ffmpeg -f lavfi` into `tmp_path`. No real library files are
  used:
  - a 10 s H.264+AAC MKV (remux)
  - a VP9+Opus WebM (audio-only path, and a re-encode)
  - an MPEG-4 AVI (re-encode)
  - an MKV with SRT and PGS subtitles
  - an MP4 with the moov at the end
  - a **truncated** MKV and an MP4 with a **corrupted middle**, both of which must fail verification
    while the original stays put
  - two byte-identical copies, a re-encoded copy, and a different video of the same length
- **Crash recovery:** inject a failure after each ledger step (`staged`, `verified`, `swapped`), then
  run recovery. Each case ends with exactly one tracked, playable file and the same `videos.id`.
- **Merge:** a fixture library with watch state, playlist items, favourites, transcripts and
  embeddings on both members; the keeper ends up with the union. The catalogue test fails when a new
  foreign key to `videos` isn't covered.
- **YouTube:**
  - yt-dlp is faked through `engine.media.set_runner`, with recorded stdout, stderr and
    `.info.json` per error class (bot check, 429, members-only, geo, removed, network);
  - the incremental case checks that a second refresh with no new uploads downloads nothing;
  - one **opt-in** real yt-dlp smoke test (`MAKTABA_TEST_YTDLP=1`) downloads a short Creative
    Commons test video into `tmp_path`.
- **Web:** vitest for the new menus, the sources dialog, the duplicates page and the file-actions
  page (states, admin-only rendering), plus a11y. A screenshot pass in the dark theme against a
  branch stack on a scratch DB.
- **Go:** handler tests per endpoint (admin 200, non-admin 403, audit row).
- **A dry run before the live run:** each library action takes `{dry_run: true}`, which writes the
  plan (what would be converted, merged or downloaded) to the job's `output_summary` and touches no
  file. The first live use per library is a dry run the owner reads.

## Stories

| # | Story | Scope |
|---|---|---|
| 47.1 | **Schema and stage registry** | migration 0166 (+ sqlite twin), MANIFEST, SCHEMA_REV bumps, `GRAPH` entries and the `convert` pool, grants. **AC:** up/down on scratch PG and SQLite; the stages appear on `/queue` disabled. |
| 47.2 | **Ledger, trash, swap and recovery** | `library_mgmt/ledger.py`, `trash.py`, the swap order, recovery on worker start, the `trash_purge` stage, Restore. **AC:** crash-injection tests at each step; purge respects `purge_after` and refuses a size mismatch; Restore round-trips. |
| 47.3 | **Verification** | `library_mgmt/verify.py`: ffprobe, stream parity, full decode, frame count, faststart; the Unicode open fallback ported from FileUtilities. **AC:** truncated and corrupt-middle fixtures fail; good remux and re-encode pass; NFD names open. |
| 47.4 | **`convert_mp4` stage** | plan (skip / remux / audio / re-encode), subtitles to `mov_text` + `.srt`, VideoToolbox presets with a libx264 fallback, progress and cancel, thumbnail re-key, no cascade into transcribe/index. **AC:** each fixture lands as a verified MP4 with the same `videos.id`, the original in the trash, a `done` ledger row; transcripts untouched; `allow_reencode` off skips VP9. |
| 47.5 | **Scanner keeps every copy** | `video_copies` instead of overwriting `path` on a hash conflict; Epic 45's merge uses it. **AC:** two identical files give one video and one copy row; removing either file keeps the video. |
| 47.6 | **`dedup_scan`** | candidate tiers, on-demand `full_hash`, groups and members, dismissed-group memory, cross-library option. **AC:** exact, same-video and different-video fixtures are grouped correctly; a dismissed group stays dismissed. |
| 47.7 | **`dedup_apply` and the merge** | keeper rule, user-data merge table, derived-data move, `superseded_by`, the loser to the trash, the foreign-key coverage guard, undo. **AC:** the merge fixture's union; the guard test fails on an uncovered FK; Restore revives the loser. |
| 47.8 | **YouTube sources and errors** | `youtube_sources` (backfilled `off`), `youtube_downloads`, the MIO error table as `integrations/youtube_errors.py` mapped to Epic 40 classes, `youtube_sync` extended (playlists, `--flat-playlist --break-on-existing`, marking `wanted`). **AC:** recorded listings mark only new, in-range items; each error class lands right. |
| 47.9 | **`youtube_fetch` stage** | per-library drain, the yt-dlp argv above, the archive rebuilt from the DB, verify, file-and-sidecar placement, the single-file scan, park on bot check and 429, early stop on a failure run. **AC:** faked runs for every class; a second refresh downloads nothing; the opt-in real smoke test passes. |
| 47.10 | **API** | the endpoints above with audit and admin-only role tests; `dry_run`; URL resolution for a new source. **AC:** handler tests, admin 200 / non-admin 403 each. |
| 47.11 | **Seeding and Tartube import** | seed the archive from linked videos; the stub-only Tartube unpickler (from MIO's `convert_tartube_db.py`) behind an admin action that takes a path on the home. **AC:** a fixture `tartube.db` seeds the archive with no import of Tartube code; a malicious pickle fixture runs nothing. |
| 47.12 | **Web: the Queues page** | LibrariesTable menu and columns, the YouTube sources dialog, `/queue/duplicates`, `/queue/file-actions`, failures-page reasons; en + ar strings. **AC:** vitest per state and action, a11y, dark-theme screenshots on a branch stack. |
| 47.13 | **Help and rollout** | Epic 41 help pages for the three jobs; a dry run on one library on the live home (read-only plan), then one real library, then the rest by the owner. **AC:** the owner has read the dry-run plans. |

## Order

47.1 → 47.2 → 47.3 → then three tracks that can run in parallel worktrees:

- **A:** 47.4
- **B:** 47.5 → 47.6 → 47.7
- **C:** 47.8 → 47.9 → 47.11

After the tracks: 47.10 → 47.12 → 47.13.

47.1–47.9 and 47.11 are pipeline and migration work. 47.10 is Go. 47.12 is web. The migration is
applied to the live database by the main session, after review.

## Decisions for the owner

1. **What happens to a converted original:**
   - trash for N days, then delete (**proposed: 30 days**);
   - trash forever, purged only by hand;
   - delete as soon as the MP4 verifies (not proposed).
2. **What happens to a duplicate's file:** the same three choices (**proposed: trash for 30 days**).
3. **Re-encoding.**
   - Allow lossy re-encodes for files whose codec MP4 can't hold (VP9, AV1, old MPEG-4), or remux
     and audio-only conversions only (**proposed: remux and audio only at first; re-encode
     opt-in per library**).
   - If re-encoding: **H.264 or HEVC** (HEVC is half the size; H.264 plays everywhere, including
     the browser on every OS).
4. **Image subtitles** (PGS/VobSub): skip those files (**proposed**), or convert and drop the
   subtitles.
5. **Exact duplicates:** merge automatically (**proposed**), or review every group.
6. **Duplicate scope:** within each library only (**proposed by default**), or across all
   libraries.
7. **YouTube defaults:**
   - maximum height (**proposed 1080**);
   - subtitle languages (**proposed `ar,en` plus auto-subs**);
   - a new source downloads **new uploads only** from the day it's added (**proposed**), or the
     whole back catalogue;
   - refresh every **24 h** (**proposed**);
   - cookies from which browser, if any.
8. **Tartube.** Is there a Tartube database on the home to seed the archive from, and where?

## Out of scope

- Space-saving re-encodes, and quality ladders for streaming.
- Sites other than YouTube.
- Live streams and premieres.
- Any page outside `/queue`.
- Moving files between libraries during a merge.
