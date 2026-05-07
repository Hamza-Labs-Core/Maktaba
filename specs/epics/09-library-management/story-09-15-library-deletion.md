# Story 9.15 — Library deletion

Epic 7 Story 7.3 AC-4 binds the endpoint with `?purge`; this story
defines the on-disk semantics and the audit trail.

**AC-1 — Catalog deletion (default).**
- **Given** `DELETE /api/libraries/{id}` (`?purge=false`),
- **When** processed,
- **Then** the library row is deleted in one transaction; FK cascades
  remove `videos`, `media_info`, `audio_tracks`, `transcripts`,
  `transcript_segments`, `chapters`, `subtitle_files`, `playback_state`,
  `collection_items`, `video_tags`, `library_topics`, `library_sweeps`,
  `media_features`, and `streaming_sessions` (closing each first via
  gRPC). The cascade reaches `streaming_sessions` through
  `videos.library_id`, not directly.

**AC-2 — File purge (`?purge=true`).**
- **Given** the purge flag with `?confirm=<library_name>` matching the
  library's name (Epic 7 Story 7.3 AC-4),
- **When** the catalog deletion succeeds,
- **Then** for each root, every file matching `supported_video_exts`
  (and not in `ignore_globs`) is unlinked. Sidecar `.maktaba/` dirs at
  each root are also unlinked. The audit log captures
  `category='library', event='purge', payload={by_user, root,
  file_count, freed_bytes}`.

**AC-3 — Atomicity.**
- **Given** the catalog delete succeeds but a file unlink fails,
- **When** processed,
- **Then** the response is `207 Multi-Status` with the list of
  unlink failures; the catalog is *not* rolled back. The user must
  manually clean the leftover files.

**Test cases:**
- Integration: delete with active streaming sessions → sessions are
  closed first (via gRPC to Streaming), then the catalog is deleted.
- Integration: purge with a read-only file → 207, the file remains, the
  catalog is gone.
- Integration: dry-run mode (`?dry_run=true`) returns the list of files
  that *would* be deleted without touching anything.

**Edge cases:**
- Library has 1M videos — the FK cascade is one DB transaction;
  Postgres handles it but a long lock is taken. Operations doc warns to
  use `pg_terminate_backend` if it stalls.
- Purge while a Pipeline worker is writing a sidecar — the unlink may
  succeed before the write completes; the worker's atomic-rename pattern
  fails harmlessly. No corruption.
