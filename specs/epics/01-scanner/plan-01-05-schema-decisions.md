# Story 1.5 Implementation Plan — Schema, Migrations, Incremental Scan

> **Companion to** [story-01-05-schema-decisions.md](story-01-05-schema-decisions.md).
> Story 1.5 is a *gate* story — its single user-visible deliverable is a
> migration that relaxes `videos.content_hash` from globally unique to
> per-library unique, plus the `--purge-missing` CLI flag. But that
> migration is the **first** schema change shared by every Scanner-epic
> story, so this plan documents the entire schema, migration system, and
> incremental-scan machinery it lands into. Stories 1.1–1.4 and 1.6 lean
> on the contracts laid out here.

> **Language note.** [architecture.md §1.3](../../architecture.md) puts
> the Pipeline service (which owns the scanner) in **Python**. The
> task brief asked for **Go** scaffolding for the Scanner struct + scan
> loop. Both are documented below: §3 gives the Go scaffolding as
> requested (suitable for a future port, or for the Go-side library/scan
> handlers in `api/internal/jobs/`), and §3.b gives the Python
> equivalent that lands today in `pipeline/src/maktaba_pipeline/library/`.
> The migration runner in §6 is Go-only because `goose` is the canonical
> migration tool per [architecture.md §2.1](../../architecture.md), and
> migrations are run at API boot, not by the Python pipeline.

---

## 1. Architecture diagram — incremental scan flow

```
                ┌──────────────────────────────────────────┐
                │  scan trigger                            │
                │  - POST /api/libraries/{id}/scan         │
                │  - watchdog event (Story 1.3)            │
                │  - periodic full sweep (every 6 h)       │
                └────────────────────┬─────────────────────┘
                                     │
                            ┌────────▼────────┐
                            │ enumerate roots │  os.walk, ignore globs,
                            │ (extension OK?) │  skip .maktaba/, *.part
                            └────────┬────────┘
                                     │  for each candidate path
                                     ▼
                ┌──────────────────────────────────────────┐
                │ FAST CHECK — no hash, no insert          │
                │  SELECT id, content_hash, size_bytes,    │
                │         mtime, state                     │
                │  FROM videos                             │
                │  WHERE library_id = $1 AND path = $2     │
                └────────────────────┬─────────────────────┘
                                     │
              ┌──────────────────────┼─────────────────────┐
              │                      │                     │
       row not found          row found AND          row found AND
              │            (size, mtime) match       (size, mtime)
              │                  on disk                differ
              │                      │                     │
              ▼                      ▼                     ▼
       ┌──────────────┐       ┌──────────────┐      ┌──────────────┐
       │ UNKNOWN PATH │       │   SKIP — no  │      │   STALE —    │
       │ - hash file  │       │   change.    │      │ rehash; the  │
       │ - look up by │       │ Touch        │      │ identity may │
       │ content_hash │       │ scan_state.  │      │ have changed │
       └──────┬───────┘       └──────────────┘      └──────┬───────┘
              │                                            │
              │ ┌──────────────── rehash result ───────────┘
              │ │
              ▼ ▼
        ┌──────────────────────────────┐
        │ UPSERT BY content_hash       │
        │  ON CONFLICT (library_id,    │
        │               content_hash)  │
        │  DO UPDATE SET path = $path  │  ← rename / move handled here
        │              ,mtime = $mtime │
        │              ,size_bytes=$sz │
        │              ,state = CASE   │
        │   WHEN state='missing'       │  ← rediscovery (Story 1.6)
        │     THEN 'discovered'        │
        │   ELSE state END             │
        └──────────────┬───────────────┘
                       │  on insert (not update)
                       ▼
        ┌──────────────────────────────┐
        │ enqueue probe job            │
        │  INSERT INTO processing_jobs │
        │  (video_id, stage='probe',   │
        │   state='pending')           │
        │  + pg_notify('videos.new')   │
        └──────────────────────────────┘

After every visited path, we update library_scan_state.last_seen_at = now().
At end of sweep, any videos with last_seen_at < sweep_started_at AND
state != 'missing' are transitioned to MISSING (soft delete; Story 1.3).
```

The two fast paths below are the heart of the incremental property:

- **`(library_id, path)` lookup** is O(1) via index. If `(size, mtime)`
  match the row, we **never open the file** — this is what makes a
  full re-sweep over 30 TB tractable.
- **`(library_id, content_hash)` upsert** lets a rename or move land as
  a `path` update, not a re-process — Story 1.2's contract.

---

## 2. Complete database schema for the Scanner epic

These are the tables Stories 1.1–1.6 either write to (Pipeline scanner) or
read from (API server, Streaming server). Migration 0001 (owned by this
plan) lays the `libraries` and `videos` tables down as a single
transaction; slot 0003 (owned by [plan-01-02](plan-01-02-content-identity.md))
relaxes the uniqueness rule from global to per-library.

The dialect is PostgreSQL; the SQLite variants substitute `JSONB`→`JSON`,
`TIMESTAMPTZ`→`DATETIME`, `UUID`→`TEXT`, `BIGSERIAL`→`INTEGER PRIMARY KEY
AUTOINCREMENT`, and partial indexes are emulated with normal indexes.

### 2.1 `libraries` — top-level configuration

```sql
CREATE TABLE libraries (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT         NOT NULL,
    roots           TEXT[]       NOT NULL,                  -- absolute paths
    settings        JSONB        NOT NULL DEFAULT '{}'::jsonb,
    -- settings keys consumed by Story 1.5 and friends:
    --   disabled                  BOOL    skip in claim loop
    --   watch                     BOOL    enable watchdog (Story 1.3)
    --   follow_symlinks           BOOL    Story 1.1 edge case
    --   debounce_sec              INT     watcher debounce
    --   periodic_sweep_sec        INT     fallback sweep period (default 21600)
    --   ignore_globs              TEXT[]  glob patterns to skip
    --   supported_extensions      TEXT[]  defaults applied if NULL
    --   stt_profile               TEXT    pipeline routing
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT libraries_name_unique UNIQUE (name)
);
```

### 2.2 `videos` — one row per identified file *per library*

```sql
CREATE TABLE videos (
    id                  UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id          UUID         NOT NULL
                                     REFERENCES libraries(id) ON DELETE CASCADE,
    content_hash        TEXT         NOT NULL,              -- BLAKE3 head+tail+size, lower-hex
    path                TEXT         NOT NULL,              -- current absolute path
    filename            TEXT         NOT NULL,
    size_bytes          BIGINT       NOT NULL CHECK (size_bytes >= 0),
    mtime               TIMESTAMPTZ  NOT NULL,
    state               TEXT         NOT NULL DEFAULT 'discovered',
    detected_language   TEXT,                                -- ISO 639-1
    title               TEXT,
    description         TEXT,
    poster_path         TEXT,
    sprite_path         TEXT,
    duration_sec        REAL,
    metadata            JSONB        NOT NULL DEFAULT '{}'::jsonb,
    -- metadata keys consumed by Scanner:
    --   additional_paths          TEXT[]  Story 1.2: same hash, different paths
    --   missing_since             TIMESTAMPTZ  Story 1.3 → soft delete
    --   purged_at                 TIMESTAMPTZ  Story 1.5 --purge-missing audit
    last_seen_at        TIMESTAMPTZ  NOT NULL DEFAULT now(), -- updated each visit
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT videos_state_valid CHECK (state IN (
        'discovered','probed','audio_extracted','transcribed','indexed',
        'thumbnailed','ready','ready_no_audio','missing','superseded',
        'corrupted','failed'
    )),

    -- THE story-1.5 constraint: per-library uniqueness, NOT global.
    CONSTRAINT videos_library_content_hash_key UNIQUE (library_id, content_hash)
);

CREATE INDEX videos_library_state_idx     ON videos (library_id, state);
CREATE INDEX videos_library_path_idx      ON videos (library_id, path);
CREATE INDEX videos_detected_language_idx ON videos (detected_language)
    WHERE detected_language IS NOT NULL;
CREATE INDEX videos_missing_idx           ON videos (state, last_seen_at)
    WHERE state = 'missing';
```

Key points:
- The unique constraint is `(library_id, content_hash)`, **not**
  `content_hash` — see [story-01-02](story-01-02-content-identity.md)
  rationale and §6.3 below for the migration that lands this.
- `(library_id, path)` is an index, not a unique constraint:
  during a rename window we may transiently have an old `path` row in
  state `missing` and a new `path` row in state `discovered` for the
  same content_hash. The unique constraint that matters is on
  `(library_id, content_hash)`, which folds them into a single row.
- `last_seen_at` is the **incremental scan engine's heartbeat** —
  every successful path lookup advances it. End-of-sweep, anything
  not advanced is a candidate for `missing`.

### 2.3 `library_scan_state` — per-library scan progress and watermarks

This is the one table this story introduces beyond what
[architecture.md §8](../../architecture.md) already names. It is
strictly cache/observability — the truth lives in `videos`. We carry
it because (a) the API needs "last scan finished N minutes ago" for
the UI without a heavy aggregate over `videos`, and (b) we need a
sweep-id watermark so concurrent sweeps don't race on the
"transition stragglers to MISSING" step.

```sql
CREATE TABLE library_scan_state (
    library_id           UUID         PRIMARY KEY
                                       REFERENCES libraries(id) ON DELETE CASCADE,
    last_full_sweep_id   UUID,                          -- the most recent finished sweep
    last_full_sweep_at   TIMESTAMPTZ,
    in_progress_sweep_id UUID,                          -- non-null while a sweep runs
    in_progress_started  TIMESTAMPTZ,
    files_visited        BIGINT       NOT NULL DEFAULT 0,
    files_inserted       BIGINT       NOT NULL DEFAULT 0,
    files_updated        BIGINT       NOT NULL DEFAULT 0,
    files_skipped        BIGINT       NOT NULL DEFAULT 0,  -- fast-path hits
    files_marked_missing BIGINT       NOT NULL DEFAULT 0,
    last_error           TEXT,
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT now()
);
```

A sweep:
1. `UPDATE library_scan_state SET in_progress_sweep_id = $uuid,
   in_progress_started = now() WHERE library_id = $lib AND
   in_progress_sweep_id IS NULL` — single-writer guard. If `0 rows`
   are updated, another sweep is already running and we exit.
2. Walk; tick counters; advance `last_seen_at` on visited rows.
3. End: in one transaction, mark stragglers `missing` and write
   the final counters; `in_progress_sweep_id = NULL`,
   `last_full_sweep_id = $uuid`, `last_full_sweep_at = now()`.

### 2.4 `processing_jobs` — already defined in [architecture.md §7.1](../../architecture.md)

The Scanner enqueues `(video_id, stage='probe', state='pending')` per
new `videos` row. No schema changes from this story; we depend on it.

### 2.5 Why **no** `library_videos` junction table

The task brief asked about a junction. The architecture deliberately
rejected one — see [story-01-02 §uniqueness scope](story-01-02-content-identity.md):

| Option | Pros | Cons |
|--------|------|------|
| `videos.library_id` + `(library_id, content_hash)` UNIQUE (current) | Simple deletes, no joins on read path, transcripts naturally scope to one library. | Same bytes in two libraries → two rows → duplicate transcribe work. |
| `videos` global + `library_videos(library_id, video_id)` junction | One transcript shared across libraries. | "Delete a library" needs a join + decisions about orphaned transcripts; cross-library auth on the search path. |

We accept duplicate work for cross-library duplicates because (a) the
scenario is rare in practice (a user "categorizes" a single tree, they
don't ingest the same file twice), and (b) the search path stays
trivial. The migration in §6.3 codifies this choice.

---

## 3. Go scaffolding — Scanner struct, scan loop, skip-if-known

Lives in `api/internal/scan/scanner.go` (or `pipeline/cmd/scan/` if we
ever port to a Go-only scanner binary). The Go side here is the
**library + scan-job HTTP/gRPC plane** that the API exposes; the actual
walk in production is the Python code in §3.b.

```go
// api/internal/scan/scanner.go
package scan

import (
    "context"
    "errors"
    "io/fs"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "time"

    "github.com/google/uuid"
    "github.com/maktaba/api/internal/db"     // sqlc-generated
    "github.com/maktaba/api/internal/hash"   // BLAKE3 wrapper, Story 1.2
    "log/slog"
)

// Scanner walks a library's roots and reconciles videos with the DB.
// One Scanner per library per sweep; not safe for concurrent reuse.
type Scanner struct {
    DB        *db.Queries
    Hasher    hash.Hasher       // hash.New() returns BLAKE3-head+tail+size
    Logger    *slog.Logger
    Notify    Notifier          // pg_notify wrapper

    Library   db.Library
    SweepID   uuid.UUID
    Started   time.Time

    Workers   int               // parallel hash workers; default GOMAXPROCS
    Extensions map[string]struct{} // ".mp4", ".mkv", ...

    counters Counters           // atomic counters; flushed to library_scan_state
}

type Counters struct {
    mu       sync.Mutex
    Visited  int64
    Inserted int64
    Updated  int64
    Skipped  int64
    Missing  int64
}

// Run drives one full sweep. It is idempotent: a re-run produces the
// same end-state as long as the filesystem is stable.
func (s *Scanner) Run(ctx context.Context) error {
    if err := s.claimSweep(ctx); err != nil {
        return err
    }
    defer s.releaseSweep(ctx) // always release, even on panic

    candidates := make(chan candidate, 1024)
    var wg sync.WaitGroup
    for i := 0; i < s.Workers; i++ {
        wg.Add(1)
        go s.worker(ctx, candidates, &wg)
    }

    for _, root := range s.Library.Roots {
        if err := s.walkRoot(ctx, root, candidates); err != nil {
            s.Logger.Error("walk failed", "root", root, "err", err)
            // continue with other roots; per-root failures don't abort
        }
    }
    close(candidates)
    wg.Wait()

    return s.markStragglersMissing(ctx)
}

// candidate is the cheap envelope produced by the walk.
// It carries only stat info; we hash lazily.
type candidate struct {
    Path  string
    Size  int64
    MTime time.Time
}

func (s *Scanner) walkRoot(ctx context.Context, root string, out chan<- candidate) error {
    return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
        if err != nil {
            s.Logger.Warn("walk error, skipping", "path", p, "err", err)
            return nil // never abort the whole sweep on one bad dir
        }
        if ctx.Err() != nil {
            return ctx.Err()
        }
        if d.IsDir() {
            // skip the .maktaba sidecar tree
            if d.Name() == ".maktaba" {
                return fs.SkipDir
            }
            return nil
        }
        // hidden / partial
        if strings.HasPrefix(d.Name(), ".") {
            return nil
        }
        ext := strings.ToLower(filepath.Ext(d.Name()))
        if _, ok := s.Extensions[ext]; !ok {
            return nil
        }
        if isPartialDownload(d.Name()) { // ".part", ".crdownload", ".tmp"
            return nil
        }
        info, err := d.Info()
        if err != nil {
            s.Logger.Warn("stat failed", "path", p, "err", err)
            return nil
        }
        if info.Size() == 0 {
            return nil // Story 1.1 edge: zero-byte = skipped
        }
        select {
        case out <- candidate{Path: p, Size: info.Size(), MTime: info.ModTime()}:
        case <-ctx.Done():
            return ctx.Err()
        }
        return nil
    })
}

// worker is the hot loop. It implements the skip-if-known fast path.
func (s *Scanner) worker(ctx context.Context, in <-chan candidate, wg *sync.WaitGroup) {
    defer wg.Done()
    for c := range in {
        if err := s.processCandidate(ctx, c); err != nil {
            if errors.Is(err, context.Canceled) {
                return
            }
            s.Logger.Error("process failed", "path", c.Path, "err", err)
        }
    }
}

// processCandidate is the per-file decision. The order matters: we
// touch the DB BEFORE the disk hash so unchanged files cost zero IO.
func (s *Scanner) processCandidate(ctx context.Context, c candidate) error {
    s.counters.add(visited, 1)

    // ── FAST CHECK ─────────────────────────────────────────────────
    row, err := s.DB.GetVideoByLibraryAndPath(ctx, db.GetVideoByLibraryAndPathParams{
        LibraryID: s.Library.ID,
        Path:      c.Path,
    })
    if err == nil && row.SizeBytes == c.Size && row.Mtime.Equal(c.MTime) {
        // Identity by (size, mtime) — no hash, no insert.
        // Just bump last_seen_at so the straggler sweep doesn't kill it.
        if _, err := s.DB.TouchVideoSeen(ctx, row.ID); err != nil {
            return err
        }
        s.counters.add(skipped, 1)
        return nil
    }

    // ── STALE OR UNKNOWN ─────────────────────────────────────────────
    h, err := s.Hasher.Hash(ctx, c.Path)
    if err != nil {
        return err
    }

    upsert, err := s.DB.UpsertVideoByContentHash(ctx,
        db.UpsertVideoByContentHashParams{
            LibraryID:   s.Library.ID,
            ContentHash: h,
            Path:        c.Path,
            Filename:    filepath.Base(c.Path),
            SizeBytes:   c.Size,
            Mtime:       c.MTime,
        })
    if err != nil {
        return err
    }

    if upsert.WasInserted {
        // Brand-new video — enqueue probe and notify.
        if _, err := s.DB.EnqueueProbeJob(ctx, upsert.VideoID); err != nil {
            return err
        }
        if err := s.Notify.New(ctx, upsert.VideoID); err != nil {
            // notification is best-effort; do not fail the sweep
            s.Logger.Warn("notify failed", "video_id", upsert.VideoID, "err", err)
        }
        s.counters.add(inserted, 1)
    } else {
        // Existing row — rename, move, or stat-drift.
        s.counters.add(updated, 1)
    }
    return nil
}

// markStragglersMissing flips rows whose last_seen_at predates this
// sweep's start to state='missing'. Story 1.3 owns the watcher path
// to MISSING; this is the periodic-sweep counterpart.
func (s *Scanner) markStragglersMissing(ctx context.Context) error {
    n, err := s.DB.MarkStragglersMissing(ctx, db.MarkStragglersMissingParams{
        LibraryID: s.Library.ID,
        Cutoff:    s.Started,
    })
    if err != nil {
        return err
    }
    s.counters.set(missingC, n)
    return nil
}

func (s *Scanner) claimSweep(ctx context.Context) error {
    rows, err := s.DB.ClaimScanSweep(ctx, db.ClaimScanSweepParams{
        LibraryID: s.Library.ID,
        SweepID:   s.SweepID,
        StartedAt: s.Started,
    })
    if err != nil {
        return err
    }
    if rows == 0 {
        return ErrSweepInProgress
    }
    return nil
}

func (s *Scanner) releaseSweep(ctx context.Context) {
    _, _ = s.DB.ReleaseScanSweep(ctx, db.ReleaseScanSweepParams{
        LibraryID:  s.Library.ID,
        SweepID:    s.SweepID,
        Counters:   s.counters.snapshot(),
        FinishedAt: time.Now(),
    })
}

var ErrSweepInProgress = errors.New("scan: another sweep is already in progress")

func isPartialDownload(name string) bool {
    n := strings.ToLower(name)
    return strings.HasSuffix(n, ".part") ||
        strings.HasSuffix(n, ".crdownload") ||
        strings.HasSuffix(n, ".tmp")
}
```

The matching `sqlc` queries (excerpt):

```sql
-- name: GetVideoByLibraryAndPath :one
SELECT id, content_hash, size_bytes, mtime, state
FROM videos
WHERE library_id = $1 AND path = $2;

-- name: TouchVideoSeen :exec
UPDATE videos SET last_seen_at = now() WHERE id = $1;

-- name: UpsertVideoByContentHash :one
INSERT INTO videos (library_id, content_hash, path, filename, size_bytes, mtime)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (library_id, content_hash) DO UPDATE
   SET path         = EXCLUDED.path,
       filename     = EXCLUDED.filename,
       size_bytes   = EXCLUDED.size_bytes,
       mtime        = EXCLUDED.mtime,
       last_seen_at = now(),
       state        = CASE WHEN videos.state = 'missing'
                            THEN 'discovered'
                            ELSE videos.state END,
       updated_at   = now()
RETURNING id AS video_id, (xmax = 0) AS was_inserted;

-- name: EnqueueProbeJob :one
INSERT INTO processing_jobs (video_id, stage, state, priority)
VALUES ($1, 'probe', 'pending', 100)
ON CONFLICT DO NOTHING
RETURNING id;

-- name: MarkStragglersMissing :execrows
UPDATE videos
SET state = 'missing',
    metadata = jsonb_set(metadata, '{missing_since}', to_jsonb(now()::text)),
    updated_at = now()
WHERE library_id = $1
  AND last_seen_at < $2
  AND state NOT IN ('missing','superseded','corrupted');

-- name: ClaimScanSweep :execrows
UPDATE library_scan_state
SET in_progress_sweep_id = $2,
    in_progress_started  = $3
WHERE library_id = $1 AND in_progress_sweep_id IS NULL;

-- name: ReleaseScanSweep :exec
UPDATE library_scan_state
SET in_progress_sweep_id = NULL,
    last_full_sweep_id   = $2,
    last_full_sweep_at   = $4,
    files_visited        = files_visited        + (@counters::jsonb->>'visited')::bigint,
    files_inserted       = files_inserted       + (@counters::jsonb->>'inserted')::bigint,
    files_updated        = files_updated        + (@counters::jsonb->>'updated')::bigint,
    files_skipped        = files_skipped        + (@counters::jsonb->>'skipped')::bigint,
    files_marked_missing = files_marked_missing + (@counters::jsonb->>'missing')::bigint,
    updated_at           = now()
WHERE library_id = $1;
```

### 3.b Python equivalent (production today)

The same logic in `pipeline/src/maktaba_pipeline/library/scanner.py`:

```python
class Scanner:
    def __init__(self, db, hasher, library, settings):
        self.db = db
        self.hasher = hasher
        self.library = library
        self.settings = settings
        self.sweep_id = uuid.uuid4()
        self.started = datetime.now(timezone.utc)

    async def run(self) -> Counters:
        if not await self.db.claim_sweep(self.library.id, self.sweep_id, self.started):
            raise SweepInProgress
        try:
            sem = asyncio.Semaphore(self.settings.workers)
            async with asyncio.TaskGroup() as tg:
                async for cand in self._walk():
                    await sem.acquire()
                    tg.create_task(self._process(cand, sem))
            await self._mark_stragglers_missing()
        finally:
            await self.db.release_sweep(self.library.id, self.sweep_id, self.counters)
        return self.counters

    async def _process(self, c: Candidate, sem: asyncio.Semaphore) -> None:
        try:
            row = await self.db.get_video_by_library_and_path(self.library.id, c.path)
            if row and row.size_bytes == c.size and row.mtime == c.mtime:
                await self.db.touch_video_seen(row.id)
                self.counters.skipped += 1
                return
            h = await self.hasher.hash(c.path)
            up = await self.db.upsert_video_by_content_hash(
                self.library.id, h, c.path, c.size, c.mtime,
            )
            if up.was_inserted:
                await self.db.enqueue_probe_job(up.video_id)
                await self.db.notify_new(up.video_id)
                self.counters.inserted += 1
            else:
                self.counters.updated += 1
        finally:
            sem.release()
```

---

## 4. Incremental strategy — detect already-processed files cheaply

The scanner-side incremental contract has three layers, in order of
cost:

| Layer | Trigger | Cost per file | What it detects |
|-------|---------|---------------|-----------------|
| **L1: stat-only** | Always | 1 stat + 1 indexed SELECT | Unchanged file at known path → skip entirely. |
| **L2: BLAKE3 head+tail+size** | (size, mtime) drift, or unknown path | 2 seeks + 8 MiB read + 1 upsert | Rename / move / copy / replace. |
| **L3: stage-level idempotency** | Pipeline-side, not Scanner | Stage-specific | A re-emitted `DISCOVERED` row whose content_hash matches an existing transcript reuses it (Story 1.2). |

L1 is what makes 30 TB sweeps cheap: a quiet library with 50,000 files
needs 50,000 stats + 50,000 indexed lookups per sweep, which on
Postgres-on-NVMe is well under 30 s end-to-end.

L2 is what makes renames/moves free: by the time we hash, we already
know the path lookup missed. The hash buys us a second-chance
identity match on `(library_id, content_hash)`. Story 1.2 guarantees
the hash itself reads at most 8 MiB.

L3 is the **stage** invariant — the Scanner doesn't decide whether to
re-transcribe; it just produces (or doesn't produce) `DISCOVERED` rows.
Re-processing logic lives in [architecture.md §10.2](../../architecture.md):
each stage records `(backend, model, config_hash)`, and only the user
explicitly bumping that triple causes re-runs.

**Why not mtime alone?** mtime + size is cheap and catches 99% of
stale rows, but a coordinated edit-in-place (hex-edit a single byte
without changing length, then `touch -d` to restore mtime) would slip
past it. We accept this — the integrity sweep in Epic 24 owns the
"hash everything once a quarter" check; the scanner does not.

**Why include size in the BLAKE3 input** ([story-01-02](story-01-02-content-identity.md))?
Two truncations of a long file would share the same head+tail prefix.
Mixing size into the hash (as a little-endian u64) turns size drift
into hash drift, eliminating that collision class.

---

## 5. Multi-library support

The scanner is **single-library by invocation**: one `Scanner` instance
per library per sweep. The DB enforces the rest. Concretely:

- **One row per (library, content_hash)**, never global. Two libraries
  with the same bytes get two rows; deletes scope cleanly. See §2.5.
- **Per-library scan state** in `library_scan_state` (§2.3). The API
  surface `GET /api/libraries/{id}/stats` reads from here.
- **Per-library settings** in `libraries.settings` (§2.1) drive
  `disabled`, `watch`, `debounce_sec`, etc. The orchestrator reads
  these before invoking the scanner.
- **Concurrency.** Multiple libraries can sweep concurrently; the
  `claim_sweep` row-level guard prevents two sweeps **of the same
  library** from racing. Within a sweep, parallelism is bounded by
  `workers` (default = `min(GOMAXPROCS, num_disks * 2)`).
- **Cross-library moves are not auto-detected.** Moving a file from
  `lectures/` to `films/` produces (a) a `MISSING` row in `lectures`
  and (b) a `DISCOVERED` row in `films`. We do not try to merge them
  because we cannot tell intent (the user might want both).

---

## 6. Migration system

### 6.1 Tooling

- **Tool:** [`pressly/goose`](https://github.com/pressly/goose) v3.
  Already named in [architecture.md §2.1](../../architecture.md). Embedded
  via `goose.Up(db, "migrations")` at API boot; also exposed as
  `maktaba migrate {up,down,status}` in the API binary.
- **Layout:** all migrations live in `shared/db/migrations/` with
  filenames `NNNN_short_description.sql`. The first three digits are
  zero-padded sequential; collisions are caught by CI (`make
  migrate-check` lints filename uniqueness).
- **Format:** plain SQL with `-- +goose Up` / `-- +goose Down`
  separators. Each migration runs in a single transaction
  (`-- +goose NO TRANSACTION` opt-out for `CREATE INDEX
  CONCURRENTLY`).
- **Tracking table:** `goose_db_version` is auto-managed; we never
  touch it directly.
- **Why not `golang-migrate`?** Both are fine. `goose` ships better
  embeddable Go API and supports `embed.FS` directly, which matters
  for the single-binary deploy goal.
- **Python side:** the Pipeline reads the same SQL files for
  fixtures-against-real-schema tests, but does **not** run migrations
  itself — boot order is `migrate (API binary)` → `api` →
  `pipeline`. This keeps the schema in one place.

### 6.2 Migration manifest (Scanner epic — slots claimed in canonical manifest)

The canonical numbering and ownership for every migration in the
project lives in
[`shared/db/migrations/MANIFEST.md`](../../../shared/db/migrations/MANIFEST.md).
Story 1.5 owns the slots below; the others are listed for context (this
plan declares dependencies on them but does not own the SQL).

| Slot | File | Purpose | Owner |
|------|------|---------|-------|
| `0001` | `0001_init_libraries_and_videos.sql` | `libraries`, `videos` (with `UNIQUE(content_hash)` initially; relaxed at slot 0003), `videos.metadata JSONB`, basic indexes. | **plan-01-05** (this plan) |
| `0002` | `0002_processing_jobs.sql` | Job table from architecture §7.1. | plan-06-01 |
| `0003` | `0003_videos_content_hash.sql` | Drops global `UNIQUE(content_hash)`, adds `UNIQUE(library_id, content_hash)`, adds the hash-format CHECK. | plan-01-02 |
| `0004` | `0004_video_states_and_stages.sql` | `CHECK` on `videos.state` (12-state enum) and `processing_jobs.stage`. | plan-01-06 |
| `0005` | `0005_videos_new_notify.sql` | `videos.new` NOTIFY trigger. | plan-01-01 |
| `0006` | `0006_library_scan_state.sql` | Per-library scan watermark/counters table. | **plan-01-05** (this plan) |
| `0007` | `0007_videos_last_seen_at.sql` | Adds `last_seen_at` + `videos_missing_idx`. | **plan-01-05** (this plan) |
| `0008` | `0008_scan_control.sql` | Adds `cancel_requested`, `progress_pct` to `library_scan_state`. | plan-01-04 |

The flip from global to per-library uniqueness — historically the
"flagship migration" of story 1.5 — now ships under
plan-01-02 at slot 0003 because that plan already owns the
`content_hash` work. This plan keeps the *decision* (D2 in §1) but no
longer ships the SQL.

### 6.3 Slot 0001 — `0001_init_libraries_and_videos.sql`

```sql
-- +goose Up
-- ============================================================================
-- 0001 — Initial schema: libraries + videos.
--
-- Establishes the architecture-§8.1 base shape. The initial UNIQUE on
-- content_hash is GLOBAL; slot 0003 (plan-01-02) relaxes it to per-library
-- once the content-hash format CHECK is in place.
-- ============================================================================

BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE libraries (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT         NOT NULL UNIQUE,
    roots       TEXT[]       NOT NULL,
    settings    JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE videos (
    id                 UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id         UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    content_hash       TEXT         NOT NULL UNIQUE,
    path               TEXT         NOT NULL,
    filename           TEXT         NOT NULL,
    size_bytes         BIGINT       NOT NULL,
    mtime              TIMESTAMPTZ  NOT NULL,
    state              TEXT         NOT NULL DEFAULT 'discovered',
    detected_language  TEXT,
    title              TEXT,
    description        TEXT,
    poster_path        TEXT,
    sprite_path        TEXT,
    duration_sec       REAL,
    -- metadata is the JSONB extension column shared by plans
    -- 01-02 (additional_paths), 01-03 (missing_since/rediscovered_at),
    -- 01-04 (deleted_at), and 02-02 (track_override). Documented in
    -- shared/db/migrations/MANIFEST.md §"Plan-introduced schema
    -- extensions".
    metadata           JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX videos_library_state_idx ON videos (library_id, state);
CREATE INDEX videos_state_idx         ON videos (state);
CREATE INDEX videos_detected_language_idx ON videos (detected_language);

COMMIT;

-- +goose Down
BEGIN;
DROP TABLE videos;
DROP TABLE libraries;
COMMIT;
```

### 6.4 Slot 0006 — `0006_library_scan_state.sql`

```sql
-- +goose Up
BEGIN;

CREATE TABLE library_scan_state (
    library_id        UUID         PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
    last_scan_at      TIMESTAMPTZ,
    last_scan_id      UUID,
    in_progress       BOOLEAN      NOT NULL DEFAULT false,
    files_seen        INT          NOT NULL DEFAULT 0,
    files_inserted    INT          NOT NULL DEFAULT 0,
    files_updated     INT          NOT NULL DEFAULT 0,
    files_missing     INT          NOT NULL DEFAULT 0,
    -- offline_since lives inside metadata when a mount disappears (see
    -- plan-01-03 §10).
    metadata          JSONB        NOT NULL DEFAULT '{}'::jsonb,
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now()
);

COMMIT;

-- +goose Down
BEGIN;
DROP TABLE library_scan_state;
COMMIT;
```

### 6.5 Slot 0007 — `0007_videos_last_seen_at.sql`

```sql
-- +goose Up
BEGIN;

ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS deleted_at   TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS videos_missing_idx
    ON videos (library_id, last_seen_at)
    WHERE state = 'missing';

COMMIT;

-- +goose Down
BEGIN;
DROP INDEX IF EXISTS videos_missing_idx;
ALTER TABLE videos
    DROP COLUMN IF EXISTS deleted_at,
    DROP COLUMN IF EXISTS last_seen_at;
COMMIT;
```

### 6.4 Idempotency

`goose` is idempotent at the migration level (it tracks
`goose_db_version`). At the SQL level, every `ALTER TABLE` uses
`IF EXISTS` / `IF NOT EXISTS` where the dialect supports it so a
half-applied migration recovers cleanly. The
`test_migration_idempotent` test in §7 nails this down.

### 6.5 `--purge-missing` CLI flag

Lives in `maktaba migrate` is the wrong home — purging is not a
schema change. It belongs in `maktaba-pipeline scan`:

```
maktaba-pipeline scan --library lectures --purge-missing [--yes] [--age-days 7]
```

Behavior:
- Defaults: `--age-days 7`, prompt unless `--yes`.
- Hard-deletes any `videos` row where `state='missing'` and
  `metadata->>'missing_since'::timestamptz < now() - interval '7 days'`.
- ON DELETE CASCADE on `processing_jobs`, `media_info`,
  `audio_tracks`, `transcripts`, `transcript_segments`,
  `subtitle_files`, `chapters` cleans up derived data.
- Writes an audit row to a `purge_log` table (added in this story or
  rolled into 0004) for observability:
  ```sql
  CREATE TABLE purge_log (
      id           BIGSERIAL PRIMARY KEY,
      library_id   UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
      video_id     UUID NOT NULL,           -- intentionally not a FK; videos is gone
      content_hash TEXT NOT NULL,
      path         TEXT NOT NULL,
      missing_since TIMESTAMPTZ NOT NULL,
      purged_at    TIMESTAMPTZ NOT NULL DEFAULT now()
  );
  ```

---

## 7. Test plan

| ID | Type | Story | Coverage |
|----|------|-------|----------|
| **Schema** | | | |
| `test_migration_drops_global_unique` | DB-fixture | 1.5 | After slot 0003, `videos_content_hash_key` is absent and `videos_library_content_hash_key` is present. |
| `test_migration_idempotent` | DB-fixture | 1.5 | Running `goose up` twice leaves the schema consistent. |
| `test_migration_down_then_up` | DB-fixture | 1.5 | `goose down 0003 && goose up` restores per-library uniqueness. |
| `test_migration_down_blocks_on_dup` | DB-fixture | 1.5 | After ingesting cross-library duplicates, `goose down 0003` errors with a constraint violation. |
| `test_videos_state_check_constraint` | DB-fixture | 1.6 | `INSERT … state='nonsense'` fails. |
| `test_cascade_deletes` | DB-fixture | 1.5 | Deleting a library cascades into its videos and their processing_jobs. |
| **Incremental scan** | | | |
| `test_scan_skips_unchanged_files` | Integration | 1.1/1.5 | Two consecutive sweeps over the same fixture: pass 1 inserts N, pass 2 inserts 0 and skipped == N. |
| `test_scan_detects_mtime_drift` | Integration | 1.5 | `os.utime` a fixture → next sweep re-hashes that one file, no others. |
| `test_scan_detects_size_drift` | Integration | 1.5 | Append a byte → re-hash, content_hash changes, row updated in place. |
| `test_scan_handles_rename_within_library` | Integration | 1.2/1.5 | Rename → same `videos.id`, only `path` changes, **no** new probe job. |
| `test_scan_handles_atomic_replace` | Integration | 1.5 | `mv tmpfile target` (atomic replace) → hash drifts, row updated, **new** probe job because content changed. |
| `test_scan_marks_stragglers_missing` | Integration | 1.5/1.6 | Pre-seed video; remove from disk; sweep → state='missing', last_seen_at < started_at. |
| `test_scan_revives_missing_on_rediscovery` | Integration | 1.6 | Stage='missing'; same hash reappears (any path) → state flips back to 'discovered'. |
| `test_scan_concurrent_sweeps_blocked` | Integration | 1.5 | Two `Scanner.Run` invocations → second errors with `ErrSweepInProgress`. |
| **Multi-library** | | | |
| `test_two_libraries_same_bytes_two_rows` | Integration | 1.2/1.5 | Same fixture under both libraries → two `videos` rows, same content_hash, distinct library_id. |
| `test_delete_library_does_not_orphan_other` | Integration | 1.5 | Delete library A → A's videos cascade-delete; B's identical-hash row survives. |
| `test_per_library_scan_state` | Integration | 1.5 | After parallel sweeps of A and B, `library_scan_state` rows show correct independent counters. |
| **Purge** | | | |
| `test_purge_missing_requires_age` | CLI | 1.5 | 3-day-missing video survives; 8-day-missing video deleted. |
| `test_purge_missing_prompts_without_yes` | CLI | 1.5 | TTY prompt; stdin EOF → abort. |
| `test_purge_missing_writes_audit_row` | CLI | 1.5 | Each purged video gets a `purge_log` row. |
| **Performance** | | | |
| `bench_scan_50k_files_warm` | Benchmark | 1.5 | Second sweep over 50k unchanged fixtures finishes in ≤ 30 s wall-clock on the reference box. |
| `bench_hash_30gb_io_budget` | Benchmark | 1.2 | Hashing a 30 GB fixture reads ≤ 8 MiB. |

Tests are split between Go (`api/internal/scan/scanner_test.go`) and
Python (`pipeline/tests/library/test_scanner.py`). Schema and migration
tests are Go-only — the migrations are Go-runnable, the tests share a
testcontainers Postgres.

---

## 8. Test code scaffolding

### 8.1 Go — `api/internal/scan/scanner_test.go`

```go
package scan_test

import (
    "context"
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/maktaba/api/internal/db"
    "github.com/maktaba/api/internal/scan"
    "github.com/maktaba/api/internal/testdb"
    "github.com/stretchr/testify/require"
)

func newTestScanner(t *testing.T, root string) (*scan.Scanner, *db.Queries) {
    t.Helper()
    queries := testdb.MustOpen(t)
    lib := testdb.MustCreateLibrary(t, queries, "test", []string{root})
    s := &scan.Scanner{
        DB:       queries,
        Hasher:   scan.NewBlake3HeadTailHasher(8 * 1024 * 1024),
        Logger:   testdb.NewLogger(t),
        Notify:   scan.NoopNotifier{},
        Library:  lib,
        SweepID:  testdb.UUID(),
        Started:  time.Now(),
        Workers:  4,
        Extensions: scan.DefaultExtensions(),
    }
    return s, queries
}

func TestScan_SkipsUnchangedFiles(t *testing.T) {
    root := t.TempDir()
    testdb.WriteFixture(t, root, "lecture-01.mkv", 12*1024*1024)
    testdb.WriteFixture(t, root, "lecture-02.mp4", 8*1024*1024)

    s, q := newTestScanner(t, root)
    require.NoError(t, s.Run(context.Background()))
    require.Equal(t, int64(2), s.Counters().Inserted)

    // Second sweep: zero inserts, two skips.
    s2, _ := newTestScanner(t, root)
    s2.Library = s.Library // same library
    require.NoError(t, s2.Run(context.Background()))
    require.Equal(t, int64(0), s2.Counters().Inserted)
    require.Equal(t, int64(2), s2.Counters().Skipped)
    require.Equal(t, int64(2), testdb.CountVideos(t, q, s.Library.ID))
}

func TestScan_HandlesRenameWithinLibrary(t *testing.T) {
    root := t.TempDir()
    src := testdb.WriteFixture(t, root, "old-name.mkv", 12*1024*1024)

    s, q := newTestScanner(t, root)
    require.NoError(t, s.Run(context.Background()))
    before := testdb.GetVideoByPath(t, q, s.Library.ID, src)

    dst := filepath.Join(root, "new-name.mkv")
    require.NoError(t, os.Rename(src, dst))

    s2, _ := newTestScanner(t, root)
    s2.Library = s.Library
    require.NoError(t, s2.Run(context.Background()))

    after := testdb.GetVideoByPath(t, q, s.Library.ID, dst)
    require.Equal(t, before.ID, after.ID, "rename must reuse the existing row")
    require.Equal(t, before.ContentHash, after.ContentHash)
    require.Equal(t, int64(1), testdb.CountProbeJobs(t, q, after.ID),
        "probe must NOT be re-enqueued on a rename")
}

func TestScan_MarksStragglersMissing(t *testing.T) {
    root := t.TempDir()
    p := testdb.WriteFixture(t, root, "doomed.mkv", 12*1024*1024)

    s, q := newTestScanner(t, root)
    require.NoError(t, s.Run(context.Background()))
    require.Equal(t, "discovered", testdb.GetVideoByPath(t, q, s.Library.ID, p).State)

    require.NoError(t, os.Remove(p))

    s2, _ := newTestScanner(t, root)
    s2.Library = s.Library
    require.NoError(t, s2.Run(context.Background()))
    require.Equal(t, "missing",
        testdb.GetVideoByContentHash(t, q, s.Library.ID, testdb.HashOf(t, p)).State)
}

func TestScan_TwoLibrariesSameBytesTwoRows(t *testing.T) {
    rootA := t.TempDir()
    rootB := t.TempDir()
    p := testdb.WriteFixture(t, rootA, "shared.mkv", 12*1024*1024)
    require.NoError(t, testdb.Copy(p, filepath.Join(rootB, "shared.mkv")))

    queries := testdb.MustOpen(t)
    libA := testdb.MustCreateLibrary(t, queries, "libA", []string{rootA})
    libB := testdb.MustCreateLibrary(t, queries, "libB", []string{rootB})

    runScan(t, queries, libA)
    runScan(t, queries, libB)

    require.Equal(t, int64(1), testdb.CountVideos(t, queries, libA.ID))
    require.Equal(t, int64(1), testdb.CountVideos(t, queries, libB.ID))

    h := testdb.HashOf(t, p)
    require.NotEqual(t,
        testdb.GetVideoByContentHash(t, queries, libA.ID, h).ID,
        testdb.GetVideoByContentHash(t, queries, libB.ID, h).ID)
}

func TestScan_ConcurrentSweepsBlocked(t *testing.T) {
    root := t.TempDir()
    testdb.WriteFixture(t, root, "x.mkv", 12*1024*1024)

    s1, _ := newTestScanner(t, root)
    s2, _ := newTestScanner(t, root)
    s2.Library = s1.Library

    started := make(chan struct{})
    done := make(chan error, 1)
    go func() {
        close(started)
        done <- s1.Run(context.Background())
    }()
    <-started
    err := s2.Run(context.Background())
    require.ErrorIs(t, err, scan.ErrSweepInProgress)
    require.NoError(t, <-done)
}
```

### 8.2 Migration tests — `api/internal/db/migrations_test.go`

```go
func TestMigration0003_DropsGlobalUnique(t *testing.T) {
    pool := testdb.OpenAtVersion(t, "0002")
    require.True(t, testdb.HasConstraint(t, pool, "videos", "videos_content_hash_key"))

    require.NoError(t, testdb.MigrateUpTo(pool, "0003"))

    require.False(t, testdb.HasConstraint(t, pool, "videos", "videos_content_hash_key"))
    require.True(t, testdb.HasConstraint(t, pool, "videos", "videos_library_content_hash_key"))
}

func TestMigration0003_Idempotent(t *testing.T) {
    pool := testdb.OpenAtVersion(t, "0003")
    // Apply again — goose should report no-op, not error.
    require.NoError(t, testdb.MigrateUpTo(pool, "0003"))
    require.True(t, testdb.HasConstraint(t, pool, "videos", "videos_library_content_hash_key"))
}

func TestMigration0003_DownThenUp(t *testing.T) {
    pool := testdb.OpenAtVersion(t, "0003")
    require.NoError(t, testdb.MigrateDownTo(pool, "0002"))
    require.True(t,  testdb.HasConstraint(t, pool, "videos", "videos_content_hash_key"))
    require.False(t, testdb.HasConstraint(t, pool, "videos", "videos_library_content_hash_key"))

    require.NoError(t, testdb.MigrateUpTo(pool, "0003"))
    require.False(t, testdb.HasConstraint(t, pool, "videos", "videos_content_hash_key"))
    require.True(t,  testdb.HasConstraint(t, pool, "videos", "videos_library_content_hash_key"))
}

func TestMigration0003_DownBlocksOnCrossLibraryDup(t *testing.T) {
    pool := testdb.OpenAtVersion(t, "0003")
    libA := testdb.MustCreateLibrary(t, pool, "A", nil)
    libB := testdb.MustCreateLibrary(t, pool, "B", nil)
    h := "0123456789abcdef"
    testdb.MustInsertVideo(t, pool, libA.ID, h, "/a/x.mkv")
    testdb.MustInsertVideo(t, pool, libB.ID, h, "/b/x.mkv")

    err := testdb.MigrateDownTo(pool, "0002")
    require.Error(t, err, "down must fail when cross-library dups exist")
}
```

### 8.3 Python — `pipeline/tests/library/test_scanner.py`

```python
@pytest.mark.asyncio
async def test_scanner_skip_path_when_unchanged(tmp_path, db, library):
    f = write_fixture(tmp_path, "x.mkv", size_mib=12)
    s = Scanner(db=db, hasher=Blake3HeadTailHasher(8 * MiB), library=library)
    await s.run()
    assert s.counters.inserted == 1

    s2 = Scanner(db=db, hasher=Blake3HeadTailHasher(8 * MiB), library=library)
    await s2.run()
    assert s2.counters.inserted == 0
    assert s2.counters.skipped == 1


@pytest.mark.asyncio
async def test_scanner_purge_missing_requires_age(tmp_path, db, library, freezer):
    f = write_fixture(tmp_path, "doomed.mkv", size_mib=12)
    await Scanner(db, Blake3HeadTailHasher(8 * MiB), library).run()
    f.unlink()
    await Scanner(db, Blake3HeadTailHasher(8 * MiB), library).run()  # → MISSING

    freezer.tick(timedelta(days=3))
    purged = await purge_missing(db, library.id, age_days=7, yes=True)
    assert purged == 0  # too young

    freezer.tick(timedelta(days=5))  # now 8 days
    purged = await purge_missing(db, library.id, age_days=7, yes=True)
    assert purged == 1
```

---

## 9. Performance — 30 TB scan targets and parallelism

### 9.1 Targets

| Scenario | Target | Notes |
|----------|--------|-------|
| Cold sweep, 50,000 files, 30 TB | ≤ 30 min wall clock | Dominated by `stat()` + 8 MiB hash IO per file. ~36 ms/file → ~30 min single-threaded; parallel walk gets us under that. |
| Warm sweep, 50,000 files, 0 changes | ≤ 30 s wall clock | All paths hit the L1 fast path: 1 stat + 1 indexed SELECT each. |
| Watcher-driven incremental (Story 1.3) | < 5 s end-to-end (event → row) | Bounded by `2 × debounce_sec + 1 s`. |
| Single rename | hash-cost-equivalent (≤ 200 ms) | Single 8 MiB read, single upsert. |
| `--purge-missing` over 50,000 rows | ≤ 5 s | One indexed `DELETE ... WHERE` + audit insert. |

### 9.2 Parallel scan strategy

```
                   ┌─────────────────────────────┐
                   │  walk goroutine (1)         │
                   │  filepath.WalkDir, stat,    │
                   │  ext + glob filter.         │
                   └──────────────┬──────────────┘
                                  │ candidate (path, size, mtime)
                                  ▼
                   ┌─────────────────────────────┐
                   │  bounded channel (cap 1024) │
                   └──────────────┬──────────────┘
              ┌───────────────────┼────────────────────┐
              ▼                   ▼                    ▼
       ┌──────────────┐    ┌──────────────┐     ┌──────────────┐
       │ worker 1     │    │ worker 2     │ ... │ worker N     │
       │ DB lookup    │    │ DB lookup    │     │ DB lookup    │
       │ hash if dirty│    │ hash if dirty│     │ hash if dirty│
       │ upsert       │    │ upsert       │     │ upsert       │
       └──────────────┘    └──────────────┘     └──────────────┘
```

Tuning:
- **`Workers` default** = `min(GOMAXPROCS, num_disks * 2)`. The
  bottleneck on a JBOD/RAID6 is **seeks**, not CPU — over-paralleling
  causes head thrashing on spinning rust. On NVMe, CPU-bound ceiling
  applies and `GOMAXPROCS` wins.
- **Channel buffer 1024.** Smoothes out the burst between a fast walk
  and slower hashing without unbounded memory. Bounded queue is what
  gives us the "no OOM under event storm" property the watcher tests
  also need.
- **No batched DB writes.** We `UPSERT` per file. The cost of a
  per-row roundtrip is dwarfed by the hash IO; batching adds
  complexity around partial failures with no measurable wall-clock
  gain at our scale.
- **Read-ahead the hash.** The hash is `head + tail + size`, which is
  two seeks. We issue them sequentially because attempting parallel
  IO across two regions of one file on spinning rust regresses on
  the dominant case.
- **Yield to watcher.** When `library.settings.watch=true` and the
  watcher channel is non-empty, the periodic sweep yields a slot.
  Watcher-driven changes get priority over the background sweep.

### 9.3 Why the warm-sweep target is the load-bearing one

Production reality: 99% of sweeps run on a quiet library. The 30 TB
cold sweep happens **once** when a user first ingests their archive.
Optimizing for warm is what makes the periodic 6-hour sweep
unobtrusive. The L1 fast path (§4) is the single biggest contributor;
everything else is in service of it.

---

## 10. Acceptance checklist

Story 1.5 ships when **all** of the following are true. Each item maps
to a test in §7.

### Schema
- [ ] Slot 0001 (`0001_init_libraries_and_videos.sql`) exists in
  `shared/db/migrations/` (Postgres + SQLite variants).
- [ ] After slot 0003 lands, `videos_content_hash_key` is gone.
- [ ] After slot 0003 lands, `videos_library_content_hash_key`
  (`UNIQUE (library_id, content_hash)`) is present and enforced.
- [ ] `library_scan_state` table exists (slot 0006).
- [ ] `videos.last_seen_at` exists with `videos_missing_idx` partial
  index (slot 0007).
- [ ] `purge_log` table exists.
- [ ] State `CHECK` constraint on `videos.state` covers all 12 states
  in [story-1.6](story-01-06-video-state-machine.md) (slot 0004).

### Migration system
- [ ] `goose up` / `goose down` work on both Postgres and SQLite.
- [ ] Running migrations twice is a no-op (`test_migration_idempotent`).
- [ ] Boot order is enforced: API binary runs `goose up` before
  serving; Pipeline refuses to start if `goose status` shows pending
  migrations.
- [ ] `make migrate` works from the top-level Makefile.
- [ ] Migration filename uniqueness is CI-enforced.

### Incremental scan
- [ ] L1 fast path skips files whose `(path, size, mtime)` match the DB
  row — verified by `test_scan_skips_unchanged_files`.
- [ ] L2 hash path triggers on `(size, mtime)` drift OR unknown path.
- [ ] Renames update `path` only; do not re-enqueue probe.
- [ ] Stragglers (`last_seen_at < started_at`) flip to `missing`.
- [ ] `MISSING` rows revive to `DISCOVERED` on rediscovery via
  `(library_id, content_hash)` upsert.

### Multi-library
- [ ] Same bytes in two libraries → two rows, one per
  `(library_id, content_hash)`.
- [ ] Concurrent sweeps of distinct libraries run in parallel; sweeps
  of the same library are serialized via `claim_sweep`.
- [ ] Dropping a library cascades into its videos and their derived
  rows; other libraries are untouched.

### `--purge-missing`
- [ ] Default off; flag opts in.
- [ ] Refuses to purge rows younger than `--age-days` (default 7).
- [ ] Prompts on TTY; aborts on stdin EOF unless `--yes`.
- [ ] Writes a `purge_log` row per deletion.
- [ ] CASCADE cleans up `processing_jobs`, `media_info`,
  `transcripts`, etc.

### Performance
- [ ] Warm sweep over 50k files completes in ≤ 30 s on the reference
  rig (`bench_scan_50k_files_warm`).
- [ ] Hashing a 30 GB file reads ≤ 8 MiB
  (`bench_hash_30gb_io_budget`).
- [ ] Parallel sweep of two libraries shows ≥ 1.7× wall-clock speedup
  vs sequential on a 4-core box.

### Documentation
- [ ] [story-01-05-schema-decisions.md](story-01-05-schema-decisions.md) cross-links to this plan.
- [ ] Migration at slot 0003 (plan-01-02) docstring records the
  precondition (no prior cross-library dups can exist) and the lock
  window note.
- [ ] [architecture.md §8.1](../../architecture.md) is updated to show the per-library
  unique constraint as the canonical version (not "TBD by Story 1.5").

---

## Open questions / deferred decisions

1. **Should the scanner be Go or Python?** Architecture says Python;
   this plan provides a Go scaffold per the task brief, plus the
   Python equivalent that ships today. A future ADR could revisit if
   the warm-sweep target slips on Python's GIL — `asyncio` over thread
   pool *should* be enough since the work is IO-bound.
2. **Should `purge_log` join `audit_log`?** Out of scope; if Epic 22
   introduces a unified audit_log we fold this in then.
3. **Cross-library de-duplication of transcripts.** Explicitly rejected
   for v1 (story 1.2). Could be revisited in v2 via a
   `transcript_aliases` table if the duplicate-transcribe cost ever
   becomes load-bearing.
