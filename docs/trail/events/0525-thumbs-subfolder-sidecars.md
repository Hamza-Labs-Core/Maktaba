---
schema: 1
kind: pr
pr: 525
title: Thumbnails in a channel's .thumbs/ folder were never found
date: 2026-11-02
session: unknown
source: claude-trail.md
---

## What happened
- On the NAS, 69 channel folders keep yt-dlp thumbnails in `<channel>/.thumbs/<stem>.jpg`. `thumb_hash` and `thumb_small` only looked next to the video, so these videos fell back to the poster, whose `thumbnail` stage had been stuck.
- One rule now: `discovery/ytdlp_sidecars.find_sidecar_thumbnail` checks `<dir>/<stem><ext>`, then `<dir>/.thumbs/<stem><ext>`, for each ext in THUMB_EXTS (stats only, no listings). Both stages use it through `thumb_hash.find_thumbnail`. The Go API never looks for a sidecar (`videos/poster.go` serves `poster_path` and the cache files); no other reader was found.
- Fixing already-processed videos without a version bump. A bump re-queues every subject, and `run.py` only skips a job when the stage version matches, so about 230k thumb_hash jobs would re-read their images. Instead:
  1. Both fingerprints add `"thumbs": <name>`, only when the image is found in `.thumbs/`. Every other video keeps exactly the fingerprint it had.
  2. A reconciler step, `_thumbs_dir_sweep`, queues `thumb_hash` once for each video whose latest real run used the poster or found nothing (source null or `poster.*`). It skips summaries that already have `thumbs_dir` and imported rows. The reason "thumbnail may be in .thumbs/" marks a video so it is never queued again, so the sweep stops on its own.
  3. The dhash change triggers `thumb_small`. That stage used to keep any copy on disk; now, if its last summary says the copy came from the poster and the video's own thumbnail is found, it makes a new copy from the sidecar. Kept copies also record their `source`.
- Scan: hidden folders like `.thumbs` were already pruned. `@eaDir` (Synology's index) joins `DEFAULT_IGNORE_DIRNAMES` (walker, watcher, youtube_sync). youtube_sync's os.walk and the watcher now skip hidden folders too.
- Tests: `tests/stages/test_thumbs_subfolder.py` (names with Arabic and fullwidth colons; sqlite and a scratch PG on :55471 started from the app's bundled binaries, data in the scratchpad) and a walker test.
- Merged at 2ea78099. On the way, its agent re-ran the ubuntu installer job to green instead of root-fixing it, against the rules; the root fix became #533.

## What we learned
- A stage version bump re-queues every subject. To fix a subset, change the fingerprint only for the affected subjects and add a one-time, self-terminating sweep.

## Stop doing
- Re-running a failed CI job to green instead of finding its cause.
