# Implementation Plan — Story 9.15 Library Deletion

> Companion to [story-09-15-library-deletion.md](story-09-15-library-deletion.md).
> The story states *what* and *why*; this plan states *how*.
> Builds on Stories 9.1 (settings/cascades), 9.5 (ignore globs for purge),
> 9.17 (audit), and the streaming-session contract from Epic 8.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| HTTP route | `DELETE /api/libraries/{id}` (Epic 7 Story 7.3 binds the URL; this plan implements the body). Query parameters: `?purge` (bool), `?confirm=<library_name>` (required when purge), `?dry_run` (bool). |
| Catalog deletion | One transaction; FK cascades from `libraries.id`. The cascade reach is enumerated in §3.2 below. |
| Streaming-session close | Before tx, gRPC call to Streaming Service: `CloseSessionsForLibrary(library_id)` (Epic 8 Story 8.7's contract). The cascade only reaches `streaming_sessions` *through* `videos.library_id`; closing first prevents in-flight 5xx. |
| Purge | After a successful catalog tx, walk each root and unlink files matching `supported_video_exts` AND not in `ignore_globs`. Sidecar `.maktaba/` dirs at each root are also unlinked. Purge runs *outside* the catalog tx — files are best-effort. |
| Atomicity | Catalog deletion is atomic; purge is not. On unlink error, the response is `207 Multi-Status` with the failed paths; the catalog stays deleted. |
| Dry run | `?dry_run=true` returns the list of would-be-deleted files + the cascade row counts; *nothing* is deleted. |
| Audit | `audit_log` row with `category='library', event='delete'` (always), plus `event='purge'` (when purge runs), and individual `event='purge-failed'` rows for unlinks that errored. All writes go through `audit.Writer.Write` — non-blocking, never propagates (Story 9.17 contract). |
| Out of scope | The `/api/libraries/{id}` route auth (Epic 10 Story 10.x); the Streaming gRPC stub (Epic 8 owns); the soft-delete vs. hard-delete decision (this story does hard-delete per AC). |

## 1. Architecture diagram

```
   DELETE /api/libraries/{id}?purge=true&confirm=Movies
        ↓
   handlers/libraries/delete.go
      1. parse + validate query params
      2. row = SELECT name FROM libraries WHERE id=$1 FOR UPDATE
      3. if purge AND confirm != row.name: 422 confirm-mismatch
      4. if not dry_run:
           ResolveStreamingSessions(id) → close via gRPC (best-effort retry)
      5. roots = SELECT path FROM library_roots WHERE library_id=$1
      6. dry-run path:
           gather catalog row counts; gather to-be-purged files
           return 200 DryRunResponse
      7. real path:
           BEGIN tx
             collect counts (logged in audit)
             DELETE FROM libraries WHERE id=$1
           COMMIT  (cascade reaches every dependent row)
           audit('library', 'delete', payload={by_user, name, counts})
           if purge:
               for root in roots:
                   walk root with ignore matcher
                   if path matches supported_video_exts AND not ignored:
                       unlink path; on error → record + audit
                   for sidecar in [root/.maktaba]:
                       rmtree sidecar; record errors
               audit('library', 'purge', payload={by_user, root,
                                                   files_deleted, freed_bytes})
           if any unlink errors:
               return 207 PurgeStatusResponse
           else:
               return 200 DeleteResponse
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/handlers/libraries/delete.go` | The handler. |
| `api/internal/handlers/libraries/delete_test.go` | Handler tests per §6. |
| `api/internal/libraries/purge.go` | `Purge(roots, matcher) → (filesDeleted, freedBytes, []PurgeError)`. |
| `api/internal/libraries/streaming_close.go` | Thin wrapper around the Streaming gRPC client. |
| `shared/db/migrations/0043_libraries_cascade_fixups.sql` | Adds any missing `ON DELETE CASCADE` clauses. |
| `shared/db/queries/libraries_delete.sql` | sqlc input — `GetLibraryForDelete`, `CountDependents`, `DeleteLibrary`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/router.go` | Wire the route. |
| `api/internal/handlers/libraries/scan.go` | Reject scan when library is mid-delete (race). |
| `pipeline/src/maktaba_pipeline/db/pubsub.py` | The canonical channel-name registry (09-01 §2.5) already declares `LIBRARY_DELETED`. This plan only consumes it. |
| `pipeline/src/maktaba_pipeline/watcher/supervisor.py` | Subscribe to `LIBRARY_DELETED`; stop the per-library watcher. |
| `specs/epics/09-library-management/README.md` | Tick story 9.15. |

### 2.3 Type definitions

```go
// api/internal/handlers/libraries/delete.go
package libraries

type DeleteRequest struct {
    LibraryID uuid.UUID
    Purge     bool
    Confirm   string
    DryRun    bool
    Actor     uuid.UUID
}

type CascadeCounts struct {
    Videos             int64 `json:"videos"`
    MediaInfo          int64 `json:"media_info"`
    AudioTracks        int64 `json:"audio_tracks"`
    Transcripts        int64 `json:"transcripts"`
    TranscriptSegments int64 `json:"transcript_segments"`
    Chapters           int64 `json:"chapters"`
    SubtitleFiles      int64 `json:"subtitle_files"`
    PlaybackState      int64 `json:"playback_state"`
    CollectionItems    int64 `json:"collection_items"`
    VideoTags          int64 `json:"video_tags"`
    LibraryTopics      int64 `json:"library_topics"`
    LibrarySweeps      int64 `json:"library_sweeps"`
    MediaFeatures      int64 `json:"media_features"`
    StreamingSessions  int64 `json:"streaming_sessions"`
    Speakers           int64 `json:"speakers"`
}

type DryRunResponse struct {
    Cascade   CascadeCounts `json:"cascade"`
    Files     []string      `json:"files,omitempty"`        // would purge
    Sidecars  []string      `json:"sidecars,omitempty"`     // would purge
    Bytes     int64         `json:"bytes"`                  // sum of file sizes
}

type PurgeError struct {
    Path  string `json:"path"`
    Error string `json:"error"`
}

type DeleteResponse struct {
    Cascade        CascadeCounts `json:"cascade"`
    PurgeRan       bool          `json:"purge_ran"`
    FilesDeleted   int           `json:"files_deleted,omitempty"`
    FreedBytes     int64         `json:"freed_bytes,omitempty"`
    UnlinkErrors   []PurgeError  `json:"unlink_errors,omitempty"`
}
```

## 3. Database migration

### 3.1 `shared/db/migrations/0043_libraries_cascade_fixups.sql`

```sql
-- +goose Up
-- +goose StatementBegin

-- The architecture set ON DELETE CASCADE on the obvious tables; this
-- migration sweeps the long tail to ensure DELETE FROM libraries is
-- truly atomic.

-- streaming_sessions reaches libraries via videos; ensure
-- streaming_sessions.video_id has ON DELETE CASCADE (idempotent).
DO $$
BEGIN
    PERFORM 1 FROM information_schema.referential_constraints
     WHERE constraint_name = 'streaming_sessions_video_id_fkey'
       AND delete_rule = 'CASCADE';
    IF NOT FOUND THEN
        ALTER TABLE streaming_sessions
            DROP CONSTRAINT streaming_sessions_video_id_fkey;
        ALTER TABLE streaming_sessions
            ADD CONSTRAINT streaming_sessions_video_id_fkey
                FOREIGN KEY (video_id) REFERENCES videos(id) ON DELETE CASCADE;
    END IF;
END$$;

-- Same defensive fixups for any other "indirect" tables.
-- Each is wrapped in a DO block to keep the migration idempotent.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Cascade fixups are not destructive on Down; intentional no-op.
-- +goose StatementEnd
```

### 3.2 Cascade reach

The full cascade chain (all confirmed by `information_schema.referential_constraints`):

```
libraries
  └─ videos (ON DELETE CASCADE)
     ├─ media_info, audio_tracks, transcripts, transcript_segments,
     │   chapters, subtitle_files, playback_state, video_tags,
     │   collection_items, video_topics, video_path_history,
     │   streaming_sessions, segment_speakers, media_features
     └─ processing_jobs (ON DELETE CASCADE)
  ├─ library_roots
  ├─ library_topics ─ video_topics (FK to compound (library_id, topic_id))
  ├─ library_sweeps
  ├─ library_stats_cache
  ├─ collections ─ collection_items
  └─ speakers ─ segment_speakers
```

`tags` is *not* cascaded — tags are global per the architecture; but
`video_tags` is removed via `videos`. After deletion, an orphaned tag
with zero `video_tags` remains; a separate cron prunes (Epic 22).

### 3.3 sqlc queries

```sql
-- name: GetLibraryForDelete :one
SELECT id, name, deleted_at
  FROM libraries WHERE id = $1
  FOR UPDATE;

-- name: CountDependents :one
SELECT
  (SELECT COUNT(*) FROM videos WHERE library_id = $1)              AS videos,
  (SELECT COUNT(*) FROM library_roots WHERE library_id = $1)       AS roots,
  (SELECT COUNT(*) FROM library_topics WHERE library_id = $1)      AS topics,
  (SELECT COUNT(*) FROM library_sweeps WHERE library_id = $1)      AS sweeps,
  (SELECT COUNT(*) FROM speakers WHERE library_id = $1)            AS speakers,
  (SELECT COUNT(*) FROM collections WHERE library_id = $1)         AS collections,
  (SELECT COUNT(*) FROM streaming_sessions s
     JOIN videos v ON v.id = s.video_id
    WHERE v.library_id = $1)                                       AS streaming_sessions,
  (SELECT COUNT(*) FROM transcripts t
     JOIN videos v ON v.id = t.video_id
    WHERE v.library_id = $1)                                       AS transcripts,
  -- ... and so on for every counted bucket in CascadeCounts
;

-- name: DeleteLibrary :exec
DELETE FROM libraries WHERE id = $1;
```

## 4. Code scaffolding

### 4.1 Handler

```go
// api/internal/handlers/libraries/delete.go
func DeleteHandler(d *handlers.Deps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()
        id, err := uuid.Parse(chi.URLParam(r, "id"))
        if err != nil { handlers.WriteError(w, 400, "bad-id", ""); return }

        purge := r.URL.Query().Get("purge") == "true"
        dryRun := r.URL.Query().Get("dry_run") == "true"
        confirm := r.URL.Query().Get("confirm")
        actor := handlers.RequireUser(ctx)

        tx, _ := d.Pool.Begin(ctx)
        defer tx.Rollback(ctx)
        q := d.Queries.WithTx(tx)
        lib, err := q.GetLibraryForDelete(ctx, id)
        if errors.Is(err, pgx.ErrNoRows) {
            handlers.WriteError(w, 404, "library-not-found", ""); return
        }
        if purge && confirm != lib.Name {
            handlers.WriteError(w, 422, "confirm-mismatch",
                "confirm must equal the library name"); return
        }

        counts, err := q.CountDependents(ctx, id)
        if err != nil { handlers.WriteError(w, 500, "count-failed", err.Error()); return }
        cc := buildCascadeCounts(counts)

        // Resolve roots before deleting them (we need them for purge).
        roots, _ := q.ListLibraryRootsForLibrary(ctx, id)

        if dryRun {
            files, sidecars, bytes, _ := purgePlan(roots, libraryIgnoreMatcher(d, lib))
            handlers.WriteJSON(w, 200, DryRunResponse{
                Cascade: cc, Files: files, Sidecars: sidecars, Bytes: bytes,
            })
            return
        }

        // Close streaming sessions before tx commit. Best-effort.
        if err := closeStreamingSessions(ctx, d, id); err != nil {
            log.WithError(err).Warn("streaming_close_failed library_id=", id)
        }

        if err := q.DeleteLibrary(ctx, id); err != nil {
            handlers.WriteError(w, 500, "delete-failed", err.Error()); return
        }
        if err := tx.Commit(ctx); err != nil {
            handlers.WriteError(w, 500, "commit-failed", err.Error()); return
        }

        d.Audit.Write(ctx, audit.LibraryEvent{
            Event: "delete", LibraryID: id, ActorUserID: actor.ID,
            Payload: map[string]any{"name": lib.Name, "cascade": cc},
        })

        // Notify Pipeline so the watcher can stop. Use the canonical
        // channel constant from pipeline/db/pubsub.py (LIBRARY_DELETED =
        // "library.deleted"); reference it via the Bus' typed helper
        // rather than the raw string literal.
        d.Bus.Notify(pubsub.LIBRARY_DELETED, map[string]any{"library_id": id})

        resp := DeleteResponse{Cascade: cc}
        if purge {
            n, bytes, errs := libraries.Purge(ctx, roots, libraryIgnoreMatcher(d, lib))
            resp.PurgeRan = true
            resp.FilesDeleted = n
            resp.FreedBytes = bytes
            resp.UnlinkErrors = errs

            d.Audit.Write(ctx, audit.LibraryEvent{
                Event: "purge", LibraryID: id, ActorUserID: actor.ID,
                Payload: map[string]any{
                    "files_deleted": n, "freed_bytes": bytes,
                    "errors": len(errs),
                },
            })
            for _, e := range errs {
                d.Audit.Write(ctx, audit.LibraryEvent{
                    Event: "purge-failed", LibraryID: id, ActorUserID: actor.ID,
                    Payload: map[string]any{"path": e.Path, "error": e.Error},
                })
            }
            if len(errs) > 0 {
                handlers.WriteJSON(w, 207, resp); return
            }
        }
        handlers.WriteJSON(w, 200, resp)
    }
}
```

### 4.2 `Purge`

```go
// api/internal/libraries/purge.go
func Purge(ctx context.Context, roots []db.LibraryRoot,
           matcher *ignore.Matcher) (int, int64, []PurgeError) {
    var (
        n      int
        bytes  int64
        errs   []PurgeError
    )
    for _, root := range roots {
        _ = filepath.WalkDir(root.Path, func(p string, d fs.DirEntry, err error) error {
            if err != nil {
                errs = append(errs, PurgeError{Path: p, Error: err.Error()})
                return nil
            }
            if d.IsDir() {
                if filepath.Base(p) == ".maktaba" {
                    if err := os.RemoveAll(p); err != nil {
                        errs = append(errs, PurgeError{Path: p, Error: err.Error()})
                    }
                    return filepath.SkipDir
                }
                return nil
            }
            if matcher.Matches(p) || !matcher.IsSupportedExtension(p) {
                return nil
            }
            info, _ := d.Info()
            if err := os.Remove(p); err != nil {
                errs = append(errs, PurgeError{Path: p, Error: err.Error()})
                return nil
            }
            n++
            if info != nil { bytes += info.Size() }
            return nil
        })
    }
    return n, bytes, errs
}

// purgePlan: same walk but no unlinks. Returns the list and total bytes.
func purgePlan(roots []db.LibraryRoot, matcher *ignore.Matcher) (
    files, sidecars []string, bytes int64, err error) {
    // ... same iteration, accumulating into slices.
}
```

### 4.3 Streaming session close

```go
// api/internal/libraries/streaming_close.go
func closeStreamingSessions(ctx context.Context, d *handlers.Deps,
                             libraryID uuid.UUID) error {
    return d.StreamingClient.CloseSessionsForLibrary(ctx,
        &streamingv1.CloseSessionsForLibraryRequest{LibraryId: libraryID.String()})
}
```

The Streaming-side handler is owned by Epic 8 Story 8.7; this story
just calls it.

## 5. Test plan

### 5.1 Handler tests (`delete_test.go`)

| Test | What it pins |
|---|---|
| `TestDelete_PurgeFalse_RemovesCatalogOnly` | Library with 100 videos, on-disk files left intact → all `videos`, `transcripts`, etc. cascade-deleted; files still on disk; response 200 with `purge_ran=false`. AC-1. |
| `TestDelete_PurgeTrue_RemovesFilesAndCatalog` | Purge with confirm matching → catalog gone, files gone for `.mp4`/`.mkv` etc., sidecar dirs gone; response 200 with `files_deleted=N`, `freed_bytes=B`. AC-2. |
| `TestDelete_PurgeRequiresConfirm` | `?purge=true` without `?confirm=name` → 422 `confirm-mismatch`. |
| `TestDelete_PurgeRespectsIgnoreGlobs` | Library has `ignore_globs: ["**/keep/**"]`; purge skips files under `keep/`. The catalog still cascades. |
| `TestDelete_StreamingSessionsClosed` | Three active streaming sessions for videos in the library → gRPC `CloseSessionsForLibrary` invoked once before catalog tx; sessions are gone after. |
| `TestDelete_207OnUnlinkFailures` | Mock filesystem with one read-only file (`os.Remove` fails) → response 207, `unlink_errors` includes the path; the catalog is *not* rolled back. AC-3. |
| `TestDelete_DryRunReturnsPlan` | `?dry_run=true` → 200 with cascade counts and the to-be-deleted file list; **no** DB or filesystem changes. |
| `TestDelete_NotFoundReturns404` | Unknown UUID → 404. |
| `TestDelete_AuditRowsWritten` | Verify `audit_log` rows: one `event='delete'`; one `event='purge'` (when purge); per-failure `event='purge-failed'`. |
| `TestDelete_NotifiesPipelineWatcher` | After commit, `library.deleted` NOTIFY fires; the test subscribes to the channel and asserts the payload. |
| `TestDelete_BlocksConcurrentScan` | While the delete tx holds `FOR UPDATE`, a concurrent `POST /scan` for the same library returns 409 `library-deleted`. |

### 5.2 `Purge` unit tests

| Test | What it pins |
|---|---|
| `TestPurge_SkipsIgnoredFiles` | Files matching `**/.maktaba/**` are not unlinked individually (the dir is rmtree'd as a sidecar). Files matching user globs are skipped. |
| `TestPurge_SkipsUnsupportedExtensions` | `.txt` files preserved. |
| `TestPurge_FollowsRootsOrder` | Multi-root: walks each in sequence; failures in root A do not stop root B. |
| `TestPurge_AccumulatesBytes` | Three files of 1 MiB each → `freed_bytes == 3 * 1024 * 1024`. |
| `TestPurge_RemoveAllSidecar` | `.maktaba/` dir with nested files → fully removed via `RemoveAll`. |
| `TestPurge_ReadOnlyFileRecordsError` | Chmod 0444 a file → unlink fails on POSIX (chmod the parent to 0555); error captured; walk continues. |

### 5.3 Cascade integration

`pipeline/tests/db/test_library_delete_cascade.py`:

| Test | What it pins |
|---|---|
| `test_cascade_reaches_every_dependent_table` | Stand up a library with one row in every dependent table; DELETE the library; assert every table has zero rows for that library. (Builds the matrix from `information_schema.referential_constraints`.) |
| `test_cascade_does_not_orphan_tags` | `tags` is global and not cascaded; verify orphan tags remain post-delete; the cleanup cron (Epic 22) handles them later. |
| `test_audit_log_rows_preserved` | `audit_log` rows for the deleted library are *not* removed by cascade (the FK is `ON DELETE SET NULL`). The library's history survives. |

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| 1M-video library | Catalog cascade runs in one tx; long lock; ops doc warns. | Documented |
| Purge while a Pipeline worker writes a sidecar | Worker uses atomic-rename; unlink may succeed before write completes; the worker's open file descriptor still works (POSIX); the rename ultimately fails harmlessly because the destination dir is gone. No corruption. | Documented |
| User cancels DELETE mid-tx | Client disconnect; the request context cancels; the tx rolls back; the audit row is never written; the library is intact. | Implicit (ctx cancellation) |
| Streaming gRPC down at delete time | We log a WARN and proceed; videos go away; in-flight sessions get 5xx. The choice is to favor catalog correctness over UX. | `TestDelete_StreamingSessionsClosed` (variant: `_StreamingDownStillSucceeds`) |
| Cross-library video reference (none in v1) | Not applicable; videos are owned by exactly one library via FK. | Architecture invariant |
| Tag pruning | Out of scope; cron runs later. | Documented |
| `audit_log` retention vs. delete | Audit rows survive; FK is `SET NULL`. The retention partitioning (Story 9.17) handles long-term storage. | `test_audit_log_rows_preserved` |

## 7. Configuration

| Key | Default | Effect |
|---|---|---|
| `delete_purge_confirm` | required when `purge=true` | Body must include `confirm` matching the library name. |
| `delete_dry_run` | optional | `?dry_run=true` returns plan only. |

## 8. Dependencies

| Dep | Source | Why |
|---|---|---|
| `streaming.CloseSessionsForLibrary` (gRPC) | Epic 8 Story 8.7 | Best-effort session close. |
| `audit_log` | Story 9.17 | Audit rows. |
| `ignore.Matcher` (Go side) | Story 9.5 | Need a Go-side mirror — implementation note: a tiny Go port of the same patterns and built-ins (or, simpler, call out to the same library via a shared service); for v1 we ship a Go re-implementation reading the same `BUILTIN_IGNORE_PATTERNS` from a JSON file. |

## 9. Acceptance checklist

**Code**
- [ ] `api/internal/handlers/libraries/delete.go` ships and is wired in `router.go`.
- [ ] `api/internal/libraries/purge.go` walks roots and unlinks per the matcher; collects errors.
- [ ] Streaming gRPC `CloseSessionsForLibrary` is invoked before catalog tx.

**Migration**
- [ ] `0043_libraries_cascade_fixups.sql` ensures `ON DELETE CASCADE` on every dependent FK.

**Behaviour (story acceptance criteria)**
- [ ] AC-1: `?purge=false` removes the catalog and leaves files.
- [ ] AC-2: `?purge=true` with valid confirm removes catalog and files; sidecars too; `audit_log` rows written.
- [ ] AC-3: unlink errors → 207 with the failed paths; catalog *not* rolled back.

**Observability**
- [ ] Counter `library_delete_total{purge=true|false, outcome=ok|partial}`.
- [ ] Histogram `library_delete_duration_seconds`.
- [ ] Counter `library_purge_unlink_failures_total`.

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.15.
- [ ] Operations doc explains the long-cascade lock warning, the streaming-close best-effort behaviour, and the dry-run usage.
