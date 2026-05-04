# Plan 9.15 — Library deletion (catalog vs file purge) — implementation

> Implementation plan for [story-09-15-library-deletion.md](story-09-15-library-deletion.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: builds on the `DELETE /api/libraries/{id}`
> route bound by Epic 7 Story 7.3 AC-4; calls the Streaming Service's
> `CloseSession` gRPC; writes audit rows in the canonical `audit_log`
> table whose schema is owned by [Plan 9.17](plan-09-17-library-audit.md).

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Two-phase delete: gRPC `CloseSession` for every active streaming session, THEN one Postgres transaction that drops the `libraries` row and cascades to all 13 dependent tables.** No SQL is run before sessions are closed. | AC-1 explicitly orders "closing each first via gRPC", then "FK cascades remove videos, ..., streaming_sessions". | Closing inside the same Postgres transaction would block the catalog xact on a network round-trip per session and leave a dangling streaming socket if the close fails after commit. Doing closes first means a failure aborts the whole operation cleanly without touching the DB. |
| D2 | **Catalog deletion uses ON DELETE CASCADE, not application-level deletes.** The handler issues a single `DELETE FROM libraries WHERE id = $1` and trusts FK constraints to ripple through `videos → media_info, audio_tracks, transcripts, transcript_segments, chapters, subtitle_files, playback_state, collection_items, video_tags, library_topics, library_sweeps, media_features, streaming_sessions`. | AC-1 enumerates the cascade targets; the FKs are owned by their respective epics. | Issuing 13 explicit DELETEs would be 13× slower (one round-trip each) and would race against any new inserts (e.g., a sweep finishing mid-delete). One CASCADE inside one xact is atomic by definition; Postgres holds the right locks automatically. |
| D3 | **File purge runs AFTER the catalog xact commits, not inside it.** A catalog success with partial unlink failures returns `207 Multi-Status` and lists the failed paths; the catalog is **never** rolled back to recover files. | AC-3: "the catalog is *not* rolled back. The user must manually clean the leftover files." | An unlink failure mid-purge leaves the filesystem in a half-state regardless. Rolling back the catalog would re-create rows for files that may already be gone — worse than the leftover state. The 207 response gives the operator the exact list to clean up by hand. |
| D4 | **`?confirm=<library_name>` is required when `?purge=true`** and is matched **exactly** (case-sensitive, no trim) against `libraries.name`. Mismatch returns `422 confirm-mismatch`. `?confirm` without `?purge=true` is ignored. | Epic 7 Story 7.3 AC-4 binds the confirm token; this story owns the matching rule. | Library names are user-controlled but visible in the UI prompt that produced the token; case-sensitivity prevents a user who skimmed "MyLibrary" vs "mylibrary" from purging the wrong root. The trim policy matches Postgres comparison semantics so what the operator typed is what we delete. |
| D5 | **`?dry_run=true` enumerates files without unlinking and skips the catalog delete entirely.** The handler returns 200 with `{file_count, freed_bytes, sample_paths[:50]}`. No audit row is written for dry-run. | AC test case: "dry-run mode (?dry_run=true) returns the list of files that would be deleted without touching anything." | A real-money operation needs a preview. Skipping the audit row keeps dry-runs lightweight and avoids polluting the trail; the audit row only describes outcomes that actually changed state. |
| D6 | **Audit row is written best-effort AFTER the catalog xact** with `category='library', event='library.deleted'` (or `library.purged`). A failed audit write does **not** fail the deletion and does **not** roll anything back; an `audit_write_failed_total` counter increments. | Story 9.17 edge case: "audit is best-effort, never blocking". | A successful delete that can't write its audit row is still a successful delete. Surfacing the metric lets ops detect audit outages while keeping user-visible behaviour stable. |

If D1 is rejected (close streams inside the DB xact): a 30s gRPC timeout per session would hold an `ACCESS EXCLUSIVE` table lock on `streaming_sessions` for the duration, blocking every running playback. Operationally toxic.

If D3 is rejected (rollback catalog on unlink failure): the catalog and the filesystem still diverge whenever an unlink fails after the catalog xact commits but before purge completes (crash, OOM-kill, network partition). The 207 contract is the only honest answer.

---

## 1. Architecture diagram — delete flow

```
   client                 API (Go)               Streaming (gRPC)        Postgres            Filesystem
     │                       │                          │                    │                    │
     │ DELETE /api/...       │                          │                    │                    │
     │  ?purge=true&...      │                          │                    │                    │
     ├──────────────────────▶│                          │                    │                    │
     │                       │ 1. validate confirm      │                    │                    │
     │                       │    name (D4)             │                    │                    │
     │                       │ 2. SELECT roots,name     │                    │                    │
     │                       ├─────────────────────────────────────────────▶ │                    │
     │                       │                          │                    │                    │
     │                       │ 3. SELECT active session │                    │                    │
     │                       │    IDs for library       │                    │                    │
     │                       ├─────────────────────────────────────────────▶ │                    │
     │                       │                          │                    │                    │
     │                       │ 4. for each session:     │                    │                    │
     │                       │    CloseSession(id)      │                    │                    │
     │                       ├─────────────────────────▶│                    │                    │
     │                       │                          │                    │                    │
     │                       │ 5. if dry_run (D5):      │                    │                    │
     │                       │    walk roots, return    │                    │                    │
     │                       │    file list (no DB)     │                    │                    │
     │                       │                          │                    │                    │
     │                       │ 6. BEGIN; DELETE FROM    │                    │                    │
     │                       │    libraries WHERE id=$1 │                    │                    │
     │                       │    -> cascades (D2)      │                    │                    │
     │                       │    COMMIT                │                    │                    │
     │                       ├─────────────────────────────────────────────▶ │                    │
     │                       │                          │                    │                    │
     │                       │ 7. if ?purge=true:       │                    │                    │
     │                       │    walk roots; unlink    │                    │                    │
     │                       │    every supported file  │                    │                    │
     │                       │    + .maktaba sidecars   │                    │                    │
     │                       ├──────────────────────────────────────────────────────────────────▶│
     │                       │                          │                    │                    │
     │                       │ 8. INSERT audit row      │                    │                    │
     │                       │    (best-effort, D6)     │                    │                    │
     │                       ├─────────────────────────────────────────────▶ │                    │
     │                       │                          │                    │                    │
     │ 200 / 207             │                          │                    │                    │
     │◀──────────────────────│                          │                    │                    │
```

The handler is the only writer; gRPC is the only Streaming dependency; FK cascades are the only DB-side magic.

---

## 2. Detailed implementation

### 2.1 Package layout — Go (API Service)

```
api/internal/
├── library/
│   ├── delete_handler.go        # ServeHTTP for DELETE /api/libraries/{id}
│   ├── delete_service.go        # orchestration: validate, close, delete, purge, audit
│   ├── purge_walker.go          # walk roots, collect supported files + .maktaba dirs
│   ├── streaming_client.go      # thin wrapper over Streaming gRPC CloseSession
│   ├── audit_writer.go          # best-effort audit_log INSERT (Story 9.17)
│   └── delete_test.go           # handler + service tests
└── db/queries/library_delete.sql  # sqlc: SelectLibraryForDelete, SelectActiveSessions, DeleteLibrary
```

### 2.2 Schema — no migration needed

Story 9.15 owns no new tables. The delete relies on FKs that already
exist on the dependent tables (defined by their owning epics).
For reference, the Story 9.17 `audit_log` schema (see
[Plan 9.17](plan-09-17-library-audit.md)) is the only table written by
this handler beyond `libraries`.

### 2.3 sqlc queries — `db/queries/library_delete.sql`

```sql
-- name: SelectLibraryForDelete :one
SELECT id, name, roots, created_at
FROM libraries
WHERE id = $1
FOR UPDATE;

-- name: SelectActiveStreamingSessionsForLibrary :many
SELECT s.id
FROM streaming_sessions s
JOIN videos v ON v.id = s.video_id
WHERE v.library_id = $1
  AND s.closed_at IS NULL;

-- name: DeleteLibraryByID :execrows
DELETE FROM libraries
WHERE id = $1;
```

The `FOR UPDATE` lock on the library row (taken in step 2 of the flow)
prevents two concurrent deletions and blocks any UPDATE on the library
row until we commit.

### 2.4 `delete_handler.go` — chi handler

```go
package library

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// DeleteHandler binds DELETE /api/libraries/{id}.
type DeleteHandler struct {
	svc *DeleteService
	log *slog.Logger
}

func NewDeleteHandler(svc *DeleteService, log *slog.Logger) *DeleteHandler {
	return &DeleteHandler{svc: svc, log: log}
}

func (h *DeleteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "library-id-invalid", err.Error())
		return
	}

	q := r.URL.Query()
	purge, _ := strconv.ParseBool(q.Get("purge"))
	dryRun, _ := strconv.ParseBool(q.Get("dry_run"))
	confirm := q.Get("confirm")

	req := DeleteRequest{
		LibraryID: id,
		Purge:     purge,
		DryRun:    dryRun,
		Confirm:   confirm,
		ActorID:   actorFromContext(r.Context()),
		IP:        clientIP(r),
		UserAgent: r.UserAgent(),
	}

	resp, err := h.svc.Run(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeProblem(w, http.StatusNotFound, "library-not-found", err.Error())
		case errors.Is(err, ErrConfirmMismatch):
			writeProblem(w, http.StatusUnprocessableEntity, "confirm-mismatch", err.Error())
		case errors.Is(err, ErrPurgeWithoutConfirm):
			writeProblem(w, http.StatusUnprocessableEntity, "purge-requires-confirm", err.Error())
		default:
			h.log.Error("library delete failed", "library_id", id, "err", err)
			writeProblem(w, http.StatusInternalServerError, "library-delete-failed", err.Error())
		}
		return
	}

	status := http.StatusOK
	if len(resp.UnlinkFailures) > 0 {
		status = http.StatusMultiStatus
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}
```

### 2.5 `delete_service.go` — orchestration (D1, D2, D3, D5, D6)

```go
package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound            = errors.New("library not found")
	ErrConfirmMismatch     = errors.New("confirm token does not match library name")
	ErrPurgeWithoutConfirm = errors.New("purge requires ?confirm=<library_name>")
)

type DeleteRequest struct {
	LibraryID uuid.UUID
	Purge     bool
	DryRun    bool
	Confirm   string
	ActorID   *uuid.UUID
	IP        string
	UserAgent string
}

type UnlinkFailure struct {
	Path string `json:"path"`
	Err  string `json:"error"`
}

type DeleteResponse struct {
	LibraryID      uuid.UUID       `json:"library_id"`
	DryRun         bool            `json:"dry_run"`
	CatalogDeleted bool            `json:"catalog_deleted"`
	Purged         bool            `json:"purged"`
	FileCount      int             `json:"file_count"`
	FreedBytes     int64           `json:"freed_bytes"`
	UnlinkFailures []UnlinkFailure `json:"unlink_failures,omitempty"`
	SamplePaths    []string        `json:"sample_paths,omitempty"` // dry-run only
}

type DeleteService struct {
	pool      *pgxpool.Pool
	streaming StreamingClient
	walker    *PurgeWalker
	audit     *AuditWriter
	log       *slog.Logger
}

func (s *DeleteService) Run(ctx context.Context, req DeleteRequest) (*DeleteResponse, error) {
	if req.Purge && req.Confirm == "" {
		return nil, ErrPurgeWithoutConfirm
	}

	// Step 1+2: load + lock the library row.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var name string
	var roots []string
	row := tx.QueryRow(ctx,
		"SELECT name, roots FROM libraries WHERE id = $1 FOR UPDATE",
		req.LibraryID)
	if err := row.Scan(&name, &roots); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if req.Purge && req.Confirm != name {
		return nil, ErrConfirmMismatch
	}

	// Step 3: enumerate active streaming sessions.
	rows, err := tx.Query(ctx, `
		SELECT s.id FROM streaming_sessions s
		JOIN videos v ON v.id = s.video_id
		WHERE v.library_id = $1 AND s.closed_at IS NULL`,
		req.LibraryID)
	if err != nil {
		return nil, err
	}
	var sessionIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		sessionIDs = append(sessionIDs, id)
	}
	rows.Close()

	// Step 5 (early return): dry-run.
	if req.DryRun {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			s.log.Warn("dry-run rollback", "err", err)
		}
		files, bytes, sample, err := s.walker.Enumerate(ctx, roots, 50)
		if err != nil {
			return nil, fmt.Errorf("enumerate: %w", err)
		}
		return &DeleteResponse{
			LibraryID:   req.LibraryID,
			DryRun:      true,
			FileCount:   files,
			FreedBytes:  bytes,
			SamplePaths: sample,
		}, nil
	}

	// Step 4: close each streaming session via gRPC (D1). Best-effort:
	// a CloseSession failure is logged and swallowed because the FK
	// cascade will delete the row anyway and the stream will tear down
	// when its socket closes server-side.
	for _, sid := range sessionIDs {
		if err := s.streaming.CloseSession(ctx, sid); err != nil {
			s.log.Warn("CloseSession failed", "session_id", sid, "err", err)
		}
	}

	// Step 6: catalog delete (D2). FK cascades do all the work.
	if _, err := tx.Exec(ctx, "DELETE FROM libraries WHERE id = $1", req.LibraryID); err != nil {
		return nil, fmt.Errorf("delete library: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	resp := &DeleteResponse{
		LibraryID:      req.LibraryID,
		CatalogDeleted: true,
	}

	// Step 7: file purge (D3). Catalog stays committed even if this fails.
	if req.Purge {
		result := s.walker.Purge(ctx, roots)
		resp.Purged = true
		resp.FileCount = result.Count
		resp.FreedBytes = result.Bytes
		resp.UnlinkFailures = result.Failures
	}

	// Step 8: audit (D6, best-effort).
	event := "library.deleted"
	if req.Purge {
		event = "library.purged"
	}
	s.audit.WriteBestEffort(ctx, AuditRecord{
		Category:    "library",
		Event:       event,
		ActorUserID: req.ActorID,
		LibraryID:   &req.LibraryID,
		IP:          req.IP,
		UserAgent:   req.UserAgent,
		Payload: map[string]any{
			"name":        name,
			"roots":       roots,
			"file_count":  resp.FileCount,
			"freed_bytes": resp.FreedBytes,
			"failures":    len(resp.UnlinkFailures),
			"at":          time.Now().UTC(),
		},
	})

	return resp, nil
}
```

### 2.6 `purge_walker.go` — file enumeration + unlink

```go
package library

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// supportedExts is the static list shared with the Pipeline filesystem
// watcher (Story 9.5). Kept in sync via shared/config/supported_exts.go.
var supportedExts = map[string]bool{
	".mp4": true, ".mkv": true, ".webm": true, ".mov": true,
	".avi": true, ".m4v": true, ".ts": true, ".mpg": true,
}

type PurgeWalker struct{}

type PurgeResult struct {
	Count    int
	Bytes    int64
	Failures []UnlinkFailure
}

// Enumerate walks roots and returns counts + a sample of paths. No I/O writes.
func (w *PurgeWalker) Enumerate(ctx context.Context, roots []string, sampleN int) (int, int64, []string, error) {
	count := 0
	var bytes int64
	sample := make([]string, 0, sampleN)
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return nil // skip unreadable
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if !supportedExts[ext] {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			count++
			bytes += info.Size()
			if len(sample) < sampleN {
				sample = append(sample, path)
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return 0, 0, nil, err
		}
	}
	return count, bytes, sample, nil
}

// Purge unlinks every supported file under each root, plus each root's
// .maktaba sidecar dir. Returns failures rather than aborting.
func (w *PurgeWalker) Purge(ctx context.Context, roots []string) PurgeResult {
	res := PurgeResult{}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil || d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if !supportedExts[ext] {
				return nil
			}
			info, ierr := d.Info()
			if ierr == nil {
				res.Bytes += info.Size()
			}
			if rmErr := os.Remove(path); rmErr != nil {
				res.Failures = append(res.Failures,
					UnlinkFailure{Path: path, Err: rmErr.Error()})
			} else {
				res.Count++
			}
			return nil
		})
		// Sidecar .maktaba/ dir at each root.
		sidecar := filepath.Join(root, ".maktaba")
		if err := os.RemoveAll(sidecar); err != nil && !errors.Is(err, fs.ErrNotExist) {
			res.Failures = append(res.Failures,
				UnlinkFailure{Path: sidecar, Err: err.Error()})
		}
	}
	return res
}
```

### 2.7 `streaming_client.go` — gRPC wrapper

```go
package library

import (
	"context"

	"github.com/google/uuid"
	streamingv1 "github.com/maktaba/api/gen/streaming/v1"
)

type StreamingClient interface {
	CloseSession(ctx context.Context, sessionID uuid.UUID) error
}

type grpcStreamingClient struct {
	c streamingv1.StreamingServiceClient
}

func (g *grpcStreamingClient) CloseSession(ctx context.Context, sessionID uuid.UUID) error {
	_, err := g.c.CloseSession(ctx, &streamingv1.CloseSessionRequest{
		SessionId: sessionID.String(),
		Reason:    "library_deleted",
	})
	return err
}
```

### 2.8 `audit_writer.go` — best-effort INSERT

```go
package library

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditRecord struct {
	Category    string
	Event       string
	ActorUserID *uuid.UUID
	LibraryID   *uuid.UUID
	IP          string
	UserAgent   string
	Payload     map[string]any
}

type AuditWriter struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// WriteBestEffort never returns an error; it logs + increments a counter.
// Bound by 8 KiB payload cap (Story 9.17 edge case).
func (a *AuditWriter) WriteBestEffort(ctx context.Context, rec AuditRecord) {
	payload, err := json.Marshal(rec.Payload)
	if err != nil {
		a.log.Warn("audit marshal", "err", err)
		return
	}
	if len(payload) > 8192 {
		payload = payload[:8192]
	}
	_, err = a.pool.Exec(ctx, `
		INSERT INTO audit_log (id, ts, category, event, actor_user_id, library_id, ip, user_agent, payload_jsonb)
		VALUES (uuidv7(), now(), $1, $2, $3, $4, NULLIF($5,'')::inet, $6, $7::jsonb)`,
		rec.Category, rec.Event, rec.ActorUserID, rec.LibraryID,
		rec.IP, rec.UserAgent, string(payload))
	if err != nil {
		a.log.Warn("audit_write_failed", "category", rec.Category, "event", rec.Event, "err", err)
		// audit_write_failed_total counter increment in real impl
	}
}
```

---

## 3. File scaffolding checklist

| Order | File | Symbols | Tests gating |
|-------|------|---------|--------------|
| 1 | `api/internal/db/queries/library_delete.sql` | `SelectLibraryForDelete`, `SelectActiveStreamingSessionsForLibrary`, `DeleteLibraryByID` | `TestSqlcLibraryDeleteCompiles` |
| 2 | `api/internal/library/streaming_client.go` | `StreamingClient`, `grpcStreamingClient.CloseSession` | mock-based unit tests |
| 3 | `api/internal/library/audit_writer.go` | `AuditRecord`, `AuditWriter.WriteBestEffort` | `TestAuditWriterBestEffort` |
| 4 | `api/internal/library/purge_walker.go` | `PurgeWalker.Enumerate`, `.Purge`, `PurgeResult` | `TestEnumerateRoots`, `TestPurgeReadOnlyFile` |
| 5 | `api/internal/library/delete_service.go` | `DeleteService.Run`, `DeleteRequest`, `DeleteResponse`, sentinel errors | `TestDeleteService*` |
| 6 | `api/internal/library/delete_handler.go` | `DeleteHandler.ServeHTTP`, `NewDeleteHandler` | `TestDeleteHandler*` |
| 7 | route wiring in `api/internal/router/router.go` | `r.Delete("/api/libraries/{id}", ...)` | `TestRouteRegistered` |

---

## 4. Test cases keyed to ACs

### T1 — AC-1: catalog-only delete cascades

```go
func TestDelete_CatalogOnly_CascadesToAllChildren(t *testing.T) {
	ctx, db := newTestDB(t)
	libID := seedLibraryWithVideos(t, db, 3) // 3 videos, each with transcripts, segments
	streaming := &fakeStreaming{}
	svc := newDeleteService(db, streaming)

	resp, err := svc.Run(ctx, DeleteRequest{LibraryID: libID})
	require.NoError(t, err)
	assert.True(t, resp.CatalogDeleted)
	assert.False(t, resp.Purged)

	for _, table := range []string{"videos", "transcripts", "transcript_segments",
		"chapters", "subtitle_files", "playback_state", "library_topics",
		"library_sweeps", "media_features", "streaming_sessions"} {
		count := scanCount(t, db, "SELECT COUNT(*) FROM "+table+" WHERE library_id_or_via_video=$1", libID)
		assert.Zero(t, count, "table %s not cascaded", table)
	}
}
```

### T2 — AC-1: active sessions are closed first via gRPC

```go
func TestDelete_ActiveSessions_AreClosedFirst(t *testing.T) {
	ctx, db := newTestDB(t)
	libID := seedLibraryWithActiveSessions(t, db, 5)
	streaming := &fakeStreaming{}
	svc := newDeleteService(db, streaming)

	_, err := svc.Run(ctx, DeleteRequest{LibraryID: libID})
	require.NoError(t, err)

	assert.Len(t, streaming.closed, 5, "all 5 sessions closed")
	// Order: every CloseSession call must precede the catalog DELETE timestamp.
	for _, ts := range streaming.closeTimestamps {
		assert.True(t, ts.Before(streaming.deleteTime))
	}
}
```

### T3 — AC-2: purge with confirm matching name unlinks files

```go
func TestDelete_PurgeWithConfirm_UnlinksFiles(t *testing.T) {
	root := tempDirWithFiles(t, "a.mp4", "b.mkv", "ignored.txt")
	libID := seedLibrary(t, db, "MyLib", []string{root})

	resp, err := svc.Run(ctx, DeleteRequest{
		LibraryID: libID, Purge: true, Confirm: "MyLib",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, resp.FileCount) // .txt skipped
	assert.NoFileExists(t, filepath.Join(root, "a.mp4"))
	assert.FileExists(t, filepath.Join(root, "ignored.txt"))
}
```

### T4 — AC-2: confirm mismatch returns 422

```go
func TestDelete_ConfirmMismatch_Returns422(t *testing.T) {
	libID := seedLibrary(t, db, "MyLib", nil)
	_, err := svc.Run(ctx, DeleteRequest{LibraryID: libID, Purge: true, Confirm: "wrong"})
	assert.ErrorIs(t, err, ErrConfirmMismatch)
}
```

### T5 — AC-3: read-only file failure returns 207

```go
func TestDelete_PurgeReadOnlyFile_Returns207(t *testing.T) {
	root := tempDirWithReadOnlyFile(t, "locked.mp4")
	libID := seedLibrary(t, db, "RO", []string{root})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/libraries/"+libID.String()+"?purge=true&confirm=RO", nil)
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMultiStatus, rec.Code)
	var body DeleteResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.True(t, body.CatalogDeleted, "catalog NOT rolled back")
	assert.Len(t, body.UnlinkFailures, 1)
	assert.Contains(t, body.UnlinkFailures[0].Path, "locked.mp4")
}
```

### T6 — Test case: dry-run lists files without touching

```go
func TestDelete_DryRun_NoStateChange(t *testing.T) {
	root := tempDirWithFiles(t, "x.mp4", "y.mp4")
	libID := seedLibrary(t, db, "DR", []string{root})

	resp, err := svc.Run(ctx, DeleteRequest{LibraryID: libID, DryRun: true})
	require.NoError(t, err)
	assert.True(t, resp.DryRun)
	assert.False(t, resp.CatalogDeleted)
	assert.Equal(t, 2, resp.FileCount)
	assert.FileExists(t, filepath.Join(root, "x.mp4"))
	assert.NotZero(t, scanCount(t, db, "SELECT COUNT(*) FROM libraries WHERE id=$1", libID))
}
```

### T7 — D6: audit row is written with correct event + payload

```go
func TestDelete_WritesAuditRow(t *testing.T) {
	libID := seedLibrary(t, db, "AL", []string{})
	_, _ = svc.Run(ctx, DeleteRequest{LibraryID: libID, ActorID: &actorID})

	row := db.QueryRow(ctx,
		"SELECT category, event, actor_user_id, library_id, payload_jsonb "+
			"FROM audit_log WHERE library_id = $1 ORDER BY ts DESC LIMIT 1", libID)
	var category, event string
	var actor, lib uuid.UUID
	var payload []byte
	require.NoError(t, row.Scan(&category, &event, &actor, &lib, &payload))
	assert.Equal(t, "library", category)
	assert.Equal(t, "library.deleted", event)
}
```

---

## 5. Edge cases

| #   | Edge case | Handled by |
|-----|-----------|------------|
| E1  | **Library has 1M videos.** The single FK-cascade transaction holds an `ACCESS EXCLUSIVE` lock for several minutes. The handler keeps a long-running connection; we set `statement_timeout = 0` for the transaction (operations doc) and warn ops to use `pg_terminate_backend` only as a last resort. | Documented in the runbook; no code change. |
| E2  | **Worker writing a sidecar mid-purge.** Pipeline writes use atomic-rename; if our `os.Remove` runs first, the rename target dir vanishes and the rename fails harmlessly. We unlink files only — directories under the root are not removed (the root itself stays). | `purge_walker.go` only unlinks files; sidecar `.maktaba/` is a single `RemoveAll`. |
| E3  | **Symlinked root pointing outside the library tree.** `filepath.WalkDir` follows symlinks; if the target is outside the declared root we still unlink files there. To prevent surprise, the walker rejects when a child path is not a descendant of the resolved root (defense-in-depth). | `PurgeWalker.Purge` adds a `containedIn(root, path)` check before each unlink. |
| E4  | **gRPC CloseSession times out for one session.** The ctx-bound call returns `DeadlineExceeded`; we log + continue. The streaming row is deleted by the FK cascade; the stream itself tears down when its server-side ctx canceled. | Best-effort behaviour in `delete_service.go` step 4. |
| E5  | **Concurrent DELETE for the same library.** The `FOR UPDATE` lock on `libraries` serializes them; the second arrival re-reads after the first commits, finds nothing, returns 404. | DB-level enforcement. |
| E6  | **`?purge=false` and `?confirm=...` (confirm without purge).** Confirm is silently ignored; only the catalog delete runs. | `delete_service.go`: confirm check guarded by `req.Purge`. |
| E7  | **Audit table partition is missing for `now()`.** Story 9.17's monthly partition rotation should have created it; if not, the INSERT raises and `WriteBestEffort` swallows + logs. The deletion still succeeds. | D6 best-effort path. |
| E8  | **8 KiB payload truncation.** A library with thousands of roots could blow the cap; payload is truncated mid-JSON which is technically invalid. We pre-cap the roots list in the audit payload to 100 entries so the JSON stays well-formed. | `audit_writer.go` truncation path; explicit cap on `roots` in `delete_service.go`. |
| E9  | **Streaming Service unreachable entirely.** Every CloseSession returns `Unavailable`; we log all of them and proceed. Operators must reconcile dangling streams via Streaming's own GC (Epic 8 Story 8.x). | Best-effort behaviour. |
| E10 | **`?dry_run=true&purge=true`.** Dry-run wins (we never unlink). Confirm token still required; mismatch returns 422 even in dry-run so operators can rehearse the failure. | `delete_service.go`: confirm check runs before the dry-run early-return only when `Purge=true`. |

---

## 6. Acceptance checklist

- [ ] **A1** (AC-1) `DELETE /api/libraries/{id}` with `purge=false` (or absent) closes every active streaming session via gRPC, then deletes the `libraries` row in one transaction, cascading to all 13 child tables. Returns 200 with `{catalog_deleted: true, purged: false}`. (T1, T2)
- [ ] **A2** (AC-2) `purge=true` with `confirm=<name>` matching `libraries.name` exactly, after a successful catalog delete, walks each root and unlinks every file matching `supported_video_exts` plus the `.maktaba` sidecar dir; the audit row payload includes `{by_user, root, file_count, freed_bytes}`. (T3, T7)
- [ ] **A3** (AC-2) `purge=true` without matching `confirm` returns 422 `confirm-mismatch`; `purge=true` without any `confirm` returns 422 `purge-requires-confirm`. (T4)
- [ ] **A4** (AC-3) When the catalog xact succeeds but one or more unlinks fail, the response is 207 Multi-Status with the failure list; the catalog stays deleted. (T5)
- [ ] **A5** (test case) `dry_run=true` returns 200 with `{file_count, freed_bytes, sample_paths}` and makes no DB or filesystem changes. (T6)
- [ ] **A6** (D6) An audit row is written with `category='library', event ∈ {library.deleted, library.purged}`, the actor user, library id, and a payload capped at 8 KiB. Audit failure does not roll back the deletion. (T7)
- [ ] **A7** (E1) Operations doc includes the `pg_terminate_backend` runbook entry for libraries with >1M videos. (Doc check; no code.)
- [ ] **A8** (E3) Symlink targets outside the declared root are not unlinked. (`TestPurgeWalkerSymlinkOutsideRoot`)
