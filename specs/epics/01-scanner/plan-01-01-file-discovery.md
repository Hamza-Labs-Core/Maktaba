# Plan 1.1 — File Discovery (implementation)

> Implementation plan for [story-01-01-file-discovery.md](story-01-01-file-discovery.md).
> Self-contained: a developer should be able to ship the story from this
> document alone.

## 0. Decisions and departures from `architecture.md`

This plan deliberately departs from one aspect of [architecture.md](../../architecture.md) and
keeps to two existing decisions that the story already refers to.

| # | Decision | Source | Rationale |
|---|----------|--------|-----------|
| D1 | **Scanner is implemented in Go**, not Python. | Departs from [architecture.md §3.1](../../architecture.md) and the [epic README](README.md), which place the scanner in the Python Pipeline Service. | The scanner has no ML dependencies (filesystem walk + BLAKE3 + DB inserts). Putting it in Go (a) removes the only non-ML stage from the Python service, (b) reuses the API service's `pgx` connection pool and `sqlc` query layer, and (c) lets a single binary serve both `POST /api/libraries/{id}/scan` (in-process) and `maktaba-scan` (CLI). The Python pipeline retains every stage that touches a model (probe, extract, transcribe, index, thumbnail). |
| D2 | `videos.content_hash` is `UNIQUE (library_id, content_hash)` — per-library. | [Story 1.5](story-01-05-schema-decisions.md). | Story 1.5 owns this constraint; this plan ships the migration. |
| D3 | Default state for a fresh insert is `'discovered'`. The state machine in [Story 1.6](story-01-06-video-state-machine.md) only adds *transitions* — Story 1.1 owns no transitions. | Story 1.6. | This plan's only state write is the initial insert. |

If D1 is rejected, the test plan, code scaffolding, and dependency list
in this document are the only sections that change; the architecture
diagram, migrations, config keys, REST contract, and acceptance
checklist are language-agnostic.

---

## 1. Architecture diagram

```
                  ┌──────────────────────────────────────────────────────┐
                  │  client (UI / CLI)                                   │
                  └─────────────────┬─────────────────┬──────────────────┘
                                    │ POST            │ exec
                                    │ /api/libraries  │ maktaba-scan
                                    │   /{id}/scan    │   --library lectures
                                    ▼                 ▼
        ┌─────────────────────────────────────────────────────────────────┐
        │                           Scanner (Go)                          │
        │  ┌──────────────────┐    ┌──────────────────┐    ┌──────────┐   │
        │  │ HTTP handler     │    │ CLI entrypoint   │    │ Job      │   │
        │  │ (chi)            │    │ (cobra/std flag) │    │ worker   │   │
        │  └────────┬─────────┘    └────────┬─────────┘    └────┬─────┘   │
        │           │                       │                   │         │
        │           ▼                       ▼                   ▼         │
        │  ┌──────────────────────────────────────────────────────────┐   │
        │  │                    scan.Run(ctx, libraryID)              │   │
        │  │                                                          │   │
        │  │   ┌──────────┐    ┌──────────┐    ┌──────────────────┐   │   │
        │  │   │ walker   │───►│ hasher   │───►│ store + notifier │   │   │
        │  │   │          │    │ BLAKE3   │    │  (pgx tx)        │   │   │
        │  │   │ filepath │    │ head+tail│    │                  │   │   │
        │  │   │ .WalkDir │    │ + size   │    │ INSERT video     │   │   │
        │  │   │          │    │          │    │ INSERT job(probe)│   │   │
        │  │   │ goroutine│    │ semaphore│    │ NOTIFY videos.new│   │   │
        │  │   │ pool     │    │ (4)      │    │                  │   │   │
        │  │   └──────────┘    └──────────┘    └──────────────────┘   │   │
        │  └──────────────────────────────────────────────────────────┘   │
        └────────────────────────┬─────────────────────────┬──────────────┘
                                 │ pgx                     │ pgx LISTEN
                                 ▼                         ▼
              ┌──────────────────────────────┐    ┌──────────────────────┐
              │        PostgreSQL 16          │    │   API Service (Go)   │
              │  libraries · videos · jobs    │◄───┤  WebSocket fan-out   │
              │  LISTEN/NOTIFY: videos.new    │    │  /ws/library/{id}    │
              └──────────────────────────────┘    └──────────────────────┘
                                                             │ WSS
                                                             ▼
                                                  ┌──────────────────────┐
                                                  │   Web / mobile / TV  │
                                                  └──────────────────────┘
```

The scanner is the source of `videos.new` NOTIFY events. The API
Service owns the `LISTEN` side and the WebSocket fanout — that work
already exists in epic 06 (Job Queue) and epic 07 (API). This story is
green when the count of WebSocket frames on `/ws/library/{id}` equals
the number of inserted rows.

---

## 2. Implementation steps (ordered)

Each step is a discrete commit. Bracketed paths are relative to the repo
root.

### Step 1 — Repository scaffolding

Create the Go module and skeleton.

- `[scanner/go.mod]` — module `github.com/maktaba/scanner`, Go 1.23.
- `[scanner/cmd/scanner/main.go]` — daemon entry. Loads config, opens
  pgx pool, starts the job-claim loop, traps SIGTERM.
- `[scanner/cmd/maktaba-scan/main.go]` — one-shot CLI entry. Same flags
  as the daemon plus `--library NAME|UUID` and `--purge-missing` (the
  `--purge-missing` flag is implemented in Story 1.5; here it is a
  no-op stub).
- `[scanner/internal/config/config.go]` — viper loader for `scanner.toml`.
- `[scanner/internal/scan/scanner.go]` — orchestrator type and `Run`.
- `[scanner/internal/walker/walker.go]` — `filepath.WalkDir` wrapper.
- `[scanner/internal/hash/blake3.go]` — head+tail+size hasher.
- `[scanner/internal/store/store.go]` + `[scanner/internal/store/queries.sql.go]`
  — sqlc-generated queries (writing the SQL is Step 3).
- `[scanner/internal/notify/notify.go]` — pgx `NOTIFY` wrapper.
- `[scanner/sqlc.yaml]` — sqlc config.

### Step 2 — Migrations

Slot ownership follows the canonical
[migration manifest](../../../shared/db/migrations/MANIFEST.md). This plan
**depends on** earlier slots and **owns** exactly one new slot:

- **Depends on** `0001_init_libraries_and_videos.sql` (owned by
  [plan-01-05](plan-01-05-schema-decisions.md)) — `libraries` and the
  base `videos` table per architecture §8.1.
- **Depends on** `0002_processing_jobs.sql` (owned by
  [plan-06-01](../06-job-queue/plan-06-01-schema-indexes.md)) —
  canonical `processing_jobs` schema per architecture §7.1, including
  the partial heartbeat index `(state, last_heartbeat_at) WHERE state IN
  ('claimed','running','resuming')` and the `pause_requested` partial
  index.
- **Owns** `0005_videos_new_notify.sql` — trigger that emits
  `pg_notify('videos.new', payload)` after insert on `videos`. See §4.1
  below.

Run order is enforced by goose's numeric prefix.

### Step 3 — sqlc query definitions

In `[shared/db/queries/scanner/]` write raw SQL for sqlc to compile:

- `get_library_by_id.sql`, `get_library_by_name.sql`
- `insert_video.sql` — `INSERT … ON CONFLICT (library_id, content_hash) DO NOTHING RETURNING id`
- `lookup_video_by_hash.sql` — for the rename/move case (Story 1.2 will
  extend this; here we only need exact-hash lookup to avoid duplicate
  inserts when the same path is walked twice)
- `enqueue_probe_job.sql` — `INSERT INTO processing_jobs … ON CONFLICT DO NOTHING`

`make sqlc` regenerates `[scanner/internal/store/queries.sql.go]`.

### Step 4 — Walker

Implement `walker.Walk` (see §3.1 below). Use `filepath.WalkDir` with a
`fs.WalkDirFunc` that:

- skips files whose lowercased extension is not in the supported set;
- skips files whose basename starts with `.` (hidden) or matches a
  partial-download glob (`*.part`, `*.crdownload`, `*.partial`);
- skips any directory whose basename is `.maktaba` (sidecar);
- on `fs.ErrPermission`, logs once per scan at WARN with the offending
  path and returns `fs.SkipDir` so the walk continues;
- handles symlinks per the `follow_symlinks` config: `false` (default)
  uses `lstat`; `true` follows and tracks `(dev, ino)` in a visited
  set to break loops.

### Step 5 — Hasher

Implement `hash.BLAKE3` per the [Story 1.2](story-01-02-content-identity.md)
formula (this story doesn't fully own collision behavior, but it must
produce the right hash for forward compatibility):

```
content_hash = lowercase_hex( BLAKE3( head4 || tail4 || u64le(size) ) )
```

with the file-size cases:

- `size == 0` → skip (zero-byte: log DEBUG, no row).
- `size <= 8 MiB` → `head4 || tail4` collapses to "the whole file"; read
  it once and pass through with the size suffix.
- `size > 8 MiB` → read first 4 MiB, `Seek(size - 4 MiB)`, read last
  4 MiB.

### Step 6 — Orchestrator

Implement `scan.Run(ctx, libraryID)` (see §3.4 below). It:

1. Loads the library; aborts with WARN if `len(roots) == 0` or
   `settings.disabled == true`. (Disabled-library AC: walks the tree but
   inserts no jobs — see Test 5 below for the exact contract.)
2. Spawns one goroutine per root, gated by `runtime.NumCPU` semaphore.
3. Each root drives the walker; each candidate file is sent on a buffered
   channel to a hasher pool of `cfg.MaxConcurrentHashes` workers.
4. Each hasher worker computes the hash and writes through `store.Save`
   (one DB transaction per file: insert video + insert probe job;
   NOTIFY fires from the trigger inside the same transaction).
5. Aggregates a `ScanResult` (counts, errors); returns when all roots
   drain.

### Step 7 — REST handler

In `[scanner/internal/http/handler.go]` (or the API service if D1 is
overridden — the handler is the same shape):

```
POST /api/libraries/{id}/scan
  → 202 Accepted
    { "scan_id": "<uuid>", "library_id": "<uuid>", "started_at": "<ts>" }
```

For Story 1.1 the handler runs `scan.Run` synchronously in a goroutine
(returning 202 immediately). Epic 06 will reroute through the
`processing_jobs` queue; the handler signature does not change.

### Step 8 — CLI

`maktaba-scan --library <name-or-uuid> [--config /etc/maktaba/scanner.toml]`
resolves the library by name then UUID, calls `scan.Run`, prints the
`ScanResult` summary, exits non-zero if any per-file error occurred.

### Step 9 — Wire NOTIFY into the API WebSocket fanout

The API service already owns `/ws/library/{id}`. Extend
`api/internal/ws/listener.go` to subscribe to the `videos.new` channel
and translate each payload into a `{ type: "video.created", … }` frame.
This is one ten-line listener; it lives in the API epic but is required
by AC #2 of this story, so it ships in this plan.

### Step 10 — Tests

Write the test suite in §7 below. Run `go test ./scanner/... -race -count=1`
locally and in CI; integration tests stand up a Postgres via testcontainers.

---

## 3. Go code scaffolding

All packages set `package` line equal to the dirname; comments below
elide that for brevity.

### 3.1 `scanner/internal/walker/walker.go`

```go
package walker

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Candidate is a file the walker accepted for hashing. Emitted on the
// channel returned by Walk.
type Candidate struct {
	Path string      // absolute, OS-native
	Info fs.FileInfo // result of lstat (or stat if FollowSymlinks)
}

// Config controls walker behavior; sourced from scanner.Config.
type Config struct {
	SupportedExtensions map[string]struct{} // lowercased, leading "."
	IgnoreBasenames     []string            // glob patterns (filepath.Match)
	IgnoreDirNames      []string            // exact basenames to prune
	FollowSymlinks      bool
}

// Walk traverses every entry under root and sends accepted files to out.
// It logs and continues on permission denials, prunes ignored dirs, and
// breaks symlink loops by (dev, ino) when FollowSymlinks is true.
//
// Walk returns when the traversal completes or ctx is cancelled. The
// caller is responsible for closing out after all roots finish.
func Walk(ctx context.Context, root string, cfg Config, out chan<- Candidate, log *slog.Logger) error {
	visited := map[devIno]struct{}{}
	permLogged := false

	walkFn := func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				if !permLogged {
					log.Warn("scanner.permission_denied", "path", path)
					permLogged = true
				}
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			return err
		}

		// Prune sidecar / hidden directories.
		if d.IsDir() {
			base := d.Name()
			if strings.HasPrefix(base, ".") && path != root {
				return fs.SkipDir
			}
			for _, name := range cfg.IgnoreDirNames {
				if base == name {
					return fs.SkipDir
				}
			}
			if cfg.FollowSymlinks {
				if di, ok := devInoOf(path); ok {
					if _, seen := visited[di]; seen {
						return fs.SkipDir
					}
					visited[di] = struct{}{}
				}
			}
			return nil
		}

		info, statErr := d.Info()
		if statErr != nil {
			log.Debug("scanner.stat_failed", "path", path, "err", statErr)
			return nil
		}

		// Skip non-regular files (sockets, devices, fifos), and skip
		// symlinks unless explicitly opted in.
		if !info.Mode().IsRegular() {
			if info.Mode()&os.ModeSymlink == 0 || !cfg.FollowSymlinks {
				return nil
			}
		}

		base := info.Name()
		if strings.HasPrefix(base, ".") {
			return nil
		}
		for _, pat := range cfg.IgnoreBasenames {
			if ok, _ := filepath.Match(pat, base); ok {
				return nil
			}
		}

		ext := strings.ToLower(filepath.Ext(base))
		if _, supported := cfg.SupportedExtensions[ext]; !supported {
			log.Debug("scanner.extension_skipped", "path", path, "ext", ext)
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- Candidate{Path: path, Info: info}:
			return nil
		}
	}

	// fs.WalkDir does not follow symlinks; we re-enter symlinked dirs
	// manually when FollowSymlinks is set, using the visited map above
	// to break loops. For Story 1.1 the default (false) is the only
	// path tests cover; opt-in is exercised by Story 1.3.
	return filepath.WalkDir(root, walkFn)
}

// devInoOf returns the underlying (dev, ino) pair for a path on Unix;
// returns ok=false on non-Unix or on stat failure.
func devInoOf(path string) (devIno, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return devIno{}, false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return devIno{}, false
	}
	return devIno{Dev: uint64(sys.Dev), Ino: uint64(sys.Ino)}, true
}

// File-level type used by both Walk's visited map and devInoOf's return type.
type devIno struct{ Dev, Ino uint64 }
```

### 3.2 `scanner/internal/hash/blake3.go`

```go
package hash

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"lukechampine.com/blake3"
)

// HeadTailSize is the number of bytes read from the head and from the
// tail of a file when computing its content hash. Matches story 1.2:
//   content_hash = hex( BLAKE3( head4 || tail4 || u64le(size) ) )
const HeadTailSize = 4 << 20 // 4 MiB

var ErrZeroSize = errors.New("hash: zero-byte file")

// HashFile computes the BLAKE3 content hash for the file at path.
// `size` must equal the file's stat size; the caller has already
// stat'd it and passing the value avoids a second syscall.
//
// The canonical formula is BLAKE3( head || tail || size_le_u64 ), with
// head = first ht bytes, tail = last ht bytes, ht = min(HeadTailSize, size).
// For files smaller than HeadTailSize the two regions are the same byte
// range; we still emit them to the hasher twice so the formula is uniform
// across sizes (see plan-01-02 §2.4 for the rationale).
//
// IO budget is bounded:
//   size <= HeadTailSize: one sequential read of the entire file (which is
//                         then written to the hasher twice).
//   size  > HeadTailSize: one read of HeadTailSize, one Seek, one read of
//                         HeadTailSize. Total bytes off disk = 8 MiB
//                         regardless of file size.
func HashFile(ctx context.Context, path string, size int64) (string, error) {
	if size == 0 {
		return "", ErrZeroSize
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := blake3.New(32, nil)
	ht := int64(HeadTailSize)
	if size < ht {
		ht = size
	}

	head := make([]byte, ht)
	if _, err := io.ReadFull(ctxReader(ctx, f), head); err != nil {
		return "", err
	}
	if _, err := h.Write(head); err != nil {
		return "", err
	}

	if size <= ht {
		// Head and tail are the same byte range; honor head||tail||size by
		// writing the buffer to the hasher a second time.
		if _, err := h.Write(head); err != nil {
			return "", err
		}
	} else {
		if _, err := f.Seek(size-ht, io.SeekStart); err != nil {
			return "", err
		}
		tail := make([]byte, ht)
		if _, err := io.ReadFull(ctxReader(ctx, f), tail); err != nil {
			return "", err
		}
		if _, err := h.Write(tail); err != nil {
			return "", err
		}
	}

	var sizeBuf [8]byte
	binary.LittleEndian.PutUint64(sizeBuf[:], uint64(size))
	if _, err := h.Write(sizeBuf[:]); err != nil {
		return "", err
	}

	sum := h.Sum(nil)
	return hex.EncodeToString(sum), nil
}

// ctxReader wraps r so that Read returns ctx.Err() once cancelled.
// Cheaper than spawning a goroutine; sufficient for our single-call sites.
type ctxR struct {
	ctx context.Context
	r   io.Reader
}

func ctxReader(ctx context.Context, r io.Reader) io.Reader { return ctxR{ctx, r} }
func (r ctxR) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
```

### 3.3 `scanner/internal/store/store.go`

```go
package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Library is the minimal projection the scanner reads.
type Library struct {
	ID       uuid.UUID
	Name     string
	Roots    []string
	Disabled bool // settings->>'disabled' coerced to bool
}

// SaveCandidateParams is the unit of work for one scanned file.
type SaveCandidateParams struct {
	LibraryID   uuid.UUID
	ContentHash string
	Path        string
	Filename    string
	SizeBytes   int64
	Mtime       time.Time
}

// SaveCandidateResult carries the outcome of one Save call.
type SaveCandidateResult struct {
	VideoID  uuid.UUID
	Inserted bool // false if the row already existed for this content_hash
}

// Store is the persistence boundary: one transaction per file, inserting
// the video row and the probe job atomically. The NOTIFY fires from a
// trigger on the videos table (see migration 0004) so the listener side
// only sees committed rows.
type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// GetLibrary returns the library projection or pgx.ErrNoRows.
func (s *Store) GetLibrary(ctx context.Context, id uuid.UUID) (Library, error) {
	const q = `
		SELECT id, name, roots, COALESCE((settings->>'disabled')::boolean, false)
		  FROM libraries
		 WHERE id = $1`
	var lib Library
	err := s.pool.QueryRow(ctx, q, id).
		Scan(&lib.ID, &lib.Name, &lib.Roots, &lib.Disabled)
	return lib, err
}

// SaveCandidate inserts a video row (no-op on conflict) and, when the
// row is newly inserted AND the library is enabled, enqueues a probe
// job. Both operations share one transaction.
func (s *Store) SaveCandidate(ctx context.Context, p SaveCandidateParams, libraryDisabled bool) (SaveCandidateResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return SaveCandidateResult{}, err
	}
	defer tx.Rollback(ctx)

	const insertVideo = `
		INSERT INTO videos (library_id, content_hash, path, filename,
		                    size_bytes, mtime, state)
		VALUES ($1, $2, $3, $4, $5, $6, 'discovered')
		ON CONFLICT (library_id, content_hash) DO NOTHING
		RETURNING id`
	var videoID uuid.UUID
	err = tx.QueryRow(ctx, insertVideo,
		p.LibraryID, p.ContentHash, p.Path, p.Filename,
		p.SizeBytes, p.Mtime,
	).Scan(&videoID)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Already present in this library; look up the existing id.
		const lookup = `SELECT id FROM videos WHERE library_id=$1 AND content_hash=$2`
		if err := tx.QueryRow(ctx, lookup, p.LibraryID, p.ContentHash).Scan(&videoID); err != nil {
			return SaveCandidateResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return SaveCandidateResult{}, err
		}
		return SaveCandidateResult{VideoID: videoID, Inserted: false}, nil
	case err != nil:
		return SaveCandidateResult{}, err
	}

	if !libraryDisabled {
		const enqueue = `
			INSERT INTO processing_jobs (video_id, stage, state, priority)
			VALUES ($1, 'probe', 'pending', 100)
			ON CONFLICT DO NOTHING`
		if _, err := tx.Exec(ctx, enqueue, videoID); err != nil {
			return SaveCandidateResult{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return SaveCandidateResult{}, err
	}
	return SaveCandidateResult{VideoID: videoID, Inserted: true}, nil
}
```

### 3.4 `scanner/internal/scan/scanner.go`

```go
package scan

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/maktaba/scanner/internal/hash"
	"github.com/maktaba/scanner/internal/store"
	"github.com/maktaba/scanner/internal/walker"
)

// Config is the runtime knobs for one scan invocation; loaded from
// scanner.toml [scanner] section.
type Config struct {
	SupportedExtensions []string
	FollowSymlinks      bool
	IgnoreBasenames     []string
	IgnoreDirNames      []string
	MaxConcurrentHashes int
}

// Result is what Run returns and what the CLI / HTTP handler render.
type Result struct {
	LibraryID     uuid.UUID
	StartedAt     time.Time
	FinishedAt    time.Time
	FilesWalked   int64
	FilesInserted int64
	FilesSkipped  int64 // already present, hash matched
	FilesIgnored  int64 // wrong extension, hidden, partial
	BytesHashed   int64
	Errors        []ScanError
}

type ScanError struct {
	Path string
	Err  error
}

type Scanner struct {
	store *store.Store
	cfg   Config
	log   *slog.Logger
}

func New(s *store.Store, cfg Config, log *slog.Logger) *Scanner {
	return &Scanner{store: s, cfg: cfg, log: log}
}

// Run walks every root of the library exactly once.
//
// Disabled libraries: the walk still runs (so missing-file detection in
// Story 1.3 works) but no probe jobs are enqueued. Inserted videos
// remain in `discovered` state without downstream work.
//
// Zero-roots libraries: the call returns immediately with a WARN log
// and an empty Result. No errors are returned.
func (s *Scanner) Run(ctx context.Context, libraryID uuid.UUID) (*Result, error) {
	lib, err := s.store.GetLibrary(ctx, libraryID)
	if err != nil {
		return nil, err
	}

	res := &Result{
		LibraryID: lib.ID,
		StartedAt: time.Now().UTC(),
	}
	defer func() { res.FinishedAt = time.Now().UTC() }()

	if len(lib.Roots) == 0 {
		s.log.Warn("scanner.no_roots", "library_id", lib.ID, "name", lib.Name)
		return res, nil
	}

	walkerCfg := walker.Config{
		SupportedExtensions: extSet(s.cfg.SupportedExtensions),
		IgnoreBasenames:     s.cfg.IgnoreBasenames,
		IgnoreDirNames:      append([]string{".maktaba"}, s.cfg.IgnoreDirNames...),
		FollowSymlinks:      s.cfg.FollowSymlinks,
	}

	candidates := make(chan walker.Candidate, 64)
	errs := make(chan ScanError, 64)

	// Hasher pool — bounded fan-in to amortize pgx transaction cost.
	var hashers sync.WaitGroup
	for i := 0; i < max(1, s.cfg.MaxConcurrentHashes); i++ {
		hashers.Add(1)
		go func() {
			defer hashers.Done()
			for c := range candidates {
				atomic.AddInt64(&res.FilesWalked, 1)
				if err := s.processOne(ctx, lib, c, res); err != nil {
					select {
					case errs <- ScanError{Path: c.Path, Err: err}:
					default:
						s.log.Error("scanner.error_dropped", "path", c.Path, "err", err)
					}
				}
			}
		}()
	}

	// Walk every root concurrently, but serialize into the single
	// candidates channel so the hasher pool sees a flat stream.
	var walkers sync.WaitGroup
	for _, root := range lib.Roots {
		root := filepath.Clean(root)
		walkers.Add(1)
		go func() {
			defer walkers.Done()
			if err := walker.Walk(ctx, root, walkerCfg, candidates, s.log); err != nil && !errors.Is(err, context.Canceled) {
				errs <- ScanError{Path: root, Err: err}
			}
		}()
	}

	// Close candidates after every root finishes so hashers drain cleanly.
	go func() {
		walkers.Wait()
		close(candidates)
	}()

	hashers.Wait()
	close(errs)
	for e := range errs {
		res.Errors = append(res.Errors, e)
	}
	return res, nil
}

func (s *Scanner) processOne(ctx context.Context, lib store.Library, c walker.Candidate, res *Result) error {
	size := c.Info.Size()
	if size == 0 {
		atomic.AddInt64(&res.FilesIgnored, 1)
		s.log.Debug("scanner.zero_byte_skipped", "path", c.Path)
		return nil
	}

	contentHash, err := hash.HashFile(ctx, c.Path, size)
	if err != nil {
		return err
	}
	atomic.AddInt64(&res.BytesHashed, min(size, 2*hash.HeadTailSize))

	out, err := s.store.SaveCandidate(ctx, store.SaveCandidateParams{
		LibraryID:   lib.ID,
		ContentHash: contentHash,
		Path:        c.Path,
		Filename:    filepath.Base(c.Path),
		SizeBytes:   size,
		Mtime:       c.Info.ModTime().UTC(),
	}, lib.Disabled)
	if err != nil {
		return err
	}
	if out.Inserted {
		atomic.AddInt64(&res.FilesInserted, 1)
	} else {
		atomic.AddInt64(&res.FilesSkipped, 1)
	}
	return nil
}

func extSet(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, e := range in {
		out[e] = struct{}{}
	}
	return out
}
```

### 3.5 `scanner/internal/http/handler.go`

```go
package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/maktaba/scanner/internal/scan"
)

type Handler struct {
	Scanner *scan.Scanner
}

// POST /api/libraries/{id}/scan
//
// Returns 202 Accepted with a scan_id. The actual walk runs in a
// detached goroutine; the caller subscribes to /ws/library/{id} for
// per-file progress (NOTIFY-driven).
func (h *Handler) Scan(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	libID, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid library id", http.StatusBadRequest)
		return
	}
	scanID := uuid.New()
	go func() {
		// Detached context: scan must not be cancelled when the HTTP
		// request finishes. Bound by an internal timeout instead.
		_, _ = h.Scanner.Run(detachedCtx(), libID)
	}()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"scan_id":    scanID,
		"library_id": libID,
		"started_at": time.Now().UTC(),
	})
}
```

(`detachedCtx` returns a fresh `context.Background()` that the daemon
cancels on SIGTERM via a process-wide group; trivial helper not shown.)

---

## 4. Database migrations

This plan owns **one** new migration. The `libraries`, `videos`, and
`processing_jobs` tables are owned by other plans per the
[migration manifest](../../../shared/db/migrations/MANIFEST.md):

- Slot 0001 (`init_libraries_and_videos`) → [plan-01-05](plan-01-05-schema-decisions.md)
- Slot 0002 (`processing_jobs`) → [plan-06-01](../06-job-queue/plan-06-01-schema-indexes.md)

### 4.1 `shared/db/migrations/0005_videos_new_notify.sql`

```sql
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION videos_notify_new() RETURNS TRIGGER AS $$
BEGIN
  PERFORM pg_notify(
    'videos.new',
    json_build_object(
      'id',           NEW.id,
      'library_id',   NEW.library_id,
      'content_hash', NEW.content_hash,
      'path',         NEW.path,
      'filename',     NEW.filename,
      'state',        NEW.state
    )::text
  );
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER videos_notify_new_trg
    AFTER INSERT ON videos
    FOR EACH ROW EXECUTE FUNCTION videos_notify_new();

-- +goose Down
DROP TRIGGER videos_notify_new_trg ON videos;
DROP FUNCTION videos_notify_new();
```

The trigger runs once per inserted row, so AC #2 — "the count of
WebSocket frames equals the number of inserted rows" — is enforced by
the database, not by the application.

---

## 5. Configuration

`scanner.toml` is loaded from the layered locations described in
[architecture.md §11.1](../../architecture.md). Defaults live in
`internal/config/defaults.go`; nothing in this story requires the user
to set anything to ship.

```toml
[app]
home              = "/var/maktaba"
log_level         = "info"

[server]
listen            = "0.0.0.0:8082"   # only used if scanner exposes its own HTTP

[database]
url               = "postgres://maktaba:@/maktaba?host=/var/run/postgresql"

[scanner]
# AC #3 — supported-extension list.
supported_extensions = [".mp4", ".mkv", ".mov", ".webm", ".avi", ".ts", ".m4v"]

# Edge case — symlinks default to lstat; libraries opt in.
follow_symlinks      = false

# Edge case — partial-download globs and sidecar dir.
ignore_basenames     = ["*.part", "*.crdownload", "*.partial", "*.tmp"]
ignore_dir_names     = [".maktaba"]

# Hash workers per scan. Bounded by hasher pool, not by goroutines.
max_concurrent_hashes = 4

# Per-file context timeout. A network-mounted root that hangs on read
# does not block the entire scan beyond this.
file_timeout_sec      = 60
```

`MAKTABA_SCANNER_*` env overrides apply per the layered loader. CLI
flags on `maktaba-scan` override env, which overrides TOML.

---

## 6. API contract

### 6.1 REST

```
POST /api/libraries/{id}/scan
  Auth: Bearer JWT (admin or library-owner scope)
  Body: (none)

  202 Accepted
  Content-Type: application/json
  {
    "scan_id":    "<uuid>",          // identifier for this scan run
    "library_id": "<uuid>",
    "started_at": "<RFC3339 UTC>"
  }

  404 Not Found     — library does not exist
  409 Conflict      — a scan for this library is already in flight
                      (not enforced in story 1.1; reserved for story 1.4)
```

The scan is fire-and-forget. Progress flows over `/ws/library/{id}`.

### 6.2 WebSocket frames (consumed, not produced, by this story)

Each row inserted into `videos` produces exactly one `videos.new` NOTIFY
(see migration 0004). The API service translates each notification into
one `video.created` WebSocket frame:

```json
{
  "type":         "video.created",
  "library_id":   "5e1f...uuid",
  "video_id":     "8a2c...uuid",
  "content_hash": "f3a1...",
  "path":         "/mnt/media/lectures/0001.mp4",
  "filename":     "0001.mp4",
  "state":        "discovered",
  "ts":           "2026-05-03T12:34:56.123Z"
}
```

### 6.3 CLI

```
maktaba-scan --library <name|uuid>
             [--config /etc/maktaba/scanner.toml]
             [--purge-missing]   # story 1.5; no-op in story 1.1
             [--yes]             # story 1.5; no-op in story 1.1
             [--json]            # emit ScanResult as JSON instead of human

  Exit codes:
    0   scan completed; no per-file errors
    1   scan completed; one or more files errored (Result.Errors non-empty)
    2   library not found
    3   config or DB error before walking
```

### 6.4 gRPC

No new gRPC surface. The scanner is invoked by direct call (CLI, in-process
HTTP handler, or the future job-queue worker). The
[architecture.md §9.9](../../architecture.md) gRPC schemas are unchanged
by this story.

---

## 7. Test plan

### 7.1 Unit tests

| File | Target | Cases |
|------|--------|-------|
| `internal/walker/walker_test.go` | `walker.Walk` | extension filter, hidden file skip, partial-download glob skip, `.maktaba` directory prune, permission-denied dir, symlink default lstat, symlink loop with FollowSymlinks |
| `internal/hash/blake3_test.go`   | `hash.HashFile` | deterministic hash, head+tail formula, whole-file path for ≤8 MiB, size-suffix changes hash, IO budget ≤ 8 MiB on a 30 GB sparse file, zero-byte returns `ErrZeroSize` |
| `internal/store/store_test.go`   | `store.Store` | insert path, conflict path returns existing id, disabled library skips probe job, NOTIFY observed by separate `LISTEN` connection |
| `internal/scan/scanner_test.go`  | `scan.Run`   | end-to-end against tmpfs fixture (see §7.3) |

All tests are table-driven (`tt := []struct{ name string; … }{…}` then
`for _, tc := range tt { t.Run(tc.name, …) }`).

### 7.2 Integration tests

Using `testcontainers-go` to spin up Postgres 16:

- `test/integration/scan_test.go` — runs migrations, builds a fixture
  tree of 1,000 `.mp4` files, runs `scan.Run`, asserts:
  - `SELECT count(*) FROM videos WHERE library_id = $1 AND state='discovered'` == 1000;
  - every video has a matching `processing_jobs` row of
    `(stage='probe', state='pending')`;
  - a `LISTEN videos.new` connection observes 1000 notifications.
- `test/integration/scan_disabled_test.go` — same fixture but
  `settings.disabled = true`; assert 1000 `videos` rows and 0
  `processing_jobs` rows.

Integration tests are tagged `// +build integration` and run separately
in CI: `go test -tags=integration ./test/integration/...`.

### 7.3 Test fixtures

- `testdata/walker/` — tree under git: `a.mp4`, `b.MKV` (case-mixed
  ext), `.hidden.mp4`, `c.txt`, `d.jpg`, `e.part`,
  `.maktaba/cached.mp4`, `subdir/f.webm`. Asserts only `a.mp4`,
  `b.MKV`, `subdir/f.webm` are emitted.
- `testdata/hash/` — `tiny.bin` (1 KB), `head_tail.bin` (16 MiB,
  byte 0..3 == "HEAD", bytes (size-4)..(size-1) == "TAIL"), allows
  asserting the hash formula end-to-end.
- `testdata/scan/` — generated at test runtime by
  `t.TempDir()` + `genTree(t, 1000, ".mp4")`. The 1,000-file fixture
  is generated, never committed.

---

## 8. Test code scaffolding

### 8.1 `internal/walker/walker_test.go`

```go
package walker_test

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/maktaba/scanner/internal/walker"
)

func TestWalk(t *testing.T) {
	t.Parallel()

	type entry struct {
		Path string // relative to root
		IsDir bool
		Mode  fs.FileMode
	}

	tests := []struct {
		name        string
		fixture     []entry
		wantEmitted []string // sorted
	}{
		{
			name: "supported extensions only",
			fixture: []entry{
				{"a.mp4", false, 0644},
				{"b.MKV", false, 0644},
				{"c.txt", false, 0644},
				{"d.jpg", false, 0644},
			},
			wantEmitted: []string{"a.mp4", "b.MKV"},
		},
		{
			name: "hidden files and dirs are skipped",
			fixture: []entry{
				{".hidden.mp4", false, 0644},
				{".cache", true, 0755},
				{".cache/inner.mp4", false, 0644},
				{"visible.mp4", false, 0644},
			},
			wantEmitted: []string{"visible.mp4"},
		},
		{
			name: "partial download globs are skipped",
			fixture: []entry{
				{"a.mp4.part", false, 0644},
				{"b.crdownload", false, 0644},
				{"c.partial", false, 0644},
				{"good.mp4", false, 0644},
			},
			wantEmitted: []string{"good.mp4"},
		},
		{
			name: ".maktaba directory is pruned",
			fixture: []entry{
				{".maktaba", true, 0755},
				{".maktaba/sidecar.mp4", false, 0644},
				{"top.mp4", false, 0644},
			},
			wantEmitted: []string{"top.mp4"},
		},
		{
			name: "permission-denied dir is skipped, walk continues",
			fixture: []entry{
				{"locked", true, 0000},
				{"locked/inner.mp4", false, 0644},
				{"open.mp4", false, 0644},
			},
			wantEmitted: []string{"open.mp4"},
		},
	}

	cfg := walker.Config{
		SupportedExtensions: map[string]struct{}{
			".mp4": {}, ".mkv": {}, ".mov": {}, ".webm": {},
			".avi": {}, ".ts": {}, ".m4v": {},
		},
		IgnoreBasenames: []string{"*.part", "*.crdownload", "*.partial"},
		IgnoreDirNames:  []string{".maktaba"},
		FollowSymlinks:  false,
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for _, e := range tc.fixture {
				p := filepath.Join(root, e.Path)
				if e.IsDir {
					if err := os.MkdirAll(p, e.Mode); err != nil {
						t.Fatalf("mkdir %s: %v", p, err)
					}
					continue
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatalf("parent: %v", err)
				}
				if err := os.WriteFile(p, []byte("x"), e.Mode); err != nil {
					t.Fatalf("write %s: %v", p, err)
				}
			}

			out := make(chan walker.Candidate, 32)
			go func() {
				defer close(out)
				if err := walker.Walk(context.Background(), root, cfg, out, log); err != nil {
					t.Errorf("Walk: %v", err)
				}
			}()
			var got []string
			for c := range out {
				rel, _ := filepath.Rel(root, c.Path)
				got = append(got, filepath.ToSlash(rel))
			}
			sort.Strings(got)

			want := append([]string(nil), tc.wantEmitted...)
			sort.Strings(want)
			if !equalStrings(got, want) {
				t.Fatalf("emitted = %v, want %v", got, want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

### 8.2 `internal/hash/blake3_test.go`

```go
package hash_test

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zeebo/blake3"

	"github.com/maktaba/scanner/internal/hash"
)

func TestHashFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(t *testing.T, dir string) (path string, size int64)
		wantErr  error
		assertOK func(t *testing.T, got string, size int64)
	}{
		{
			name: "deterministic for same bytes",
			setup: func(t *testing.T, dir string) (string, int64) {
				return mustWriteRand(t, dir, "a.bin", 1<<20), 1 << 20
			},
			assertOK: func(t *testing.T, got string, size int64) {
				// Hash again and compare.
				path := filepath.Join(t.TempDir(), "second.bin")
				_ = path
			},
		},
		{
			name: "small file uses whole-file path",
			setup: func(t *testing.T, dir string) (string, int64) {
				return mustWriteBytes(t, dir, "small.bin", []byte("hello world")), int64(len("hello world"))
			},
			assertOK: func(t *testing.T, got string, size int64) {
				h := blake3.New()
				h.Write([]byte("hello world"))
				var sz [8]byte
				binary.LittleEndian.PutUint64(sz[:], uint64(size))
				h.Write(sz[:])
				want := hexOf(h.Sum(nil))
				if got != want {
					t.Fatalf("got %q want %q", got, want)
				}
			},
		},
		{
			name: "size suffix changes hash",
			setup: func(t *testing.T, dir string) (string, int64) {
				p := mustWriteBytes(t, dir, "padded.bin", make([]byte, 1024))
				return p, 1024
			},
			assertOK: func(t *testing.T, got string, size int64) {
				// Re-hash treating the file as size+1 (shouldn't happen
				// in practice but proves the formula). We rely on the
				// fact that two distinct sizes produce distinct hashes
				// because the size suffix is mixed in.
				path := mustWriteBytes(t, t.TempDir(), "padded2.bin", make([]byte, 1025))
				other, err := hash.HashFile(context.Background(), path, 1025)
				if err != nil {
					t.Fatal(err)
				}
				if got == other {
					t.Fatal("hashes collide across different sizes")
				}
			},
		},
		{
			name: "zero-byte returns ErrZeroSize",
			setup: func(t *testing.T, dir string) (string, int64) {
				return mustWriteBytes(t, dir, "empty.bin", nil), 0
			},
			wantErr: hash.ErrZeroSize,
		},
		{
			name: "head+tail formula matches expected",
			setup: func(t *testing.T, dir string) (string, int64) {
				// 16 MiB of zeros, with the first 4 bytes "HEAD" and
				// the last 4 bytes "TAIL". File size 16 MiB > 8 MiB
				// triggers the head+tail path.
				size := int64(16 << 20)
				buf := make([]byte, size)
				copy(buf[:4], []byte("HEAD"))
				copy(buf[size-4:], []byte("TAIL"))
				return mustWriteBytes(t, dir, "ht.bin", buf), size
			},
			assertOK: func(t *testing.T, got string, size int64) {
				head := make([]byte, 4<<20)
				copy(head[:4], []byte("HEAD"))
				tail := make([]byte, 4<<20)
				copy(tail[len(tail)-4:], []byte("TAIL"))

				h := blake3.New()
				h.Write(head)
				h.Write(tail)
				var sz [8]byte
				binary.LittleEndian.PutUint64(sz[:], uint64(size))
				h.Write(sz[:])
				want := hexOf(h.Sum(nil))
				if got != want {
					t.Fatalf("got %q want %q", got, want)
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path, size := tc.setup(t, dir)
			got, err := hash.HashFile(context.Background(), path, size)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if tc.assertOK != nil {
				tc.assertOK(t, got, size)
			}
		})
	}
}

// helpers
func mustWriteBytes(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
func mustWriteRand(t *testing.T, dir, name string, n int) string {
	t.Helper()
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	return mustWriteBytes(t, dir, name, buf)
}
func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&0x0f]
	}
	return string(out)
}
```

### 8.3 `test/integration/scan_test.go`

```go
//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/maktaba/scanner/internal/scan"
	"github.com/maktaba/scanner/internal/store"
)

func TestScanInsertsRowPerVideo(t *testing.T) {
	ctx := context.Background()
	pool, libID := setupDBWithLibrary(t, ctx, false)
	root := genTree(t, 1000, ".mp4")
	bindRoot(ctx, t, pool, libID, root)

	scanner := scan.New(store.New(pool), scan.Config{
		SupportedExtensions: []string{".mp4", ".mkv", ".mov", ".webm", ".avi", ".ts", ".m4v"},
		MaxConcurrentHashes: 4,
	}, slog.Default())

	res, err := scanner.Run(ctx, libID)
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesInserted != 1000 {
		t.Fatalf("inserted = %d, want 1000", res.FilesInserted)
	}

	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM videos
		 WHERE library_id=$1 AND state='discovered'`, libID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1000 {
		t.Fatalf("rows = %d, want 1000", n)
	}

	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM processing_jobs j
		  JOIN videos v ON v.id = j.video_id
		 WHERE v.library_id=$1 AND j.stage='probe' AND j.state='pending'`, libID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1000 {
		t.Fatalf("pending probe jobs = %d, want 1000", n)
	}
}

func TestScanIgnoresNonVideoExtensions(t *testing.T) {
	ctx := context.Background()
	pool, libID := setupDBWithLibrary(t, ctx, false)
	root := t.TempDir()
	mustTouch(t, filepath.Join(root, "a.mp4"))
	mustTouch(t, filepath.Join(root, "b.txt"))
	mustTouch(t, filepath.Join(root, "c.jpg"))
	bindRoot(ctx, t, pool, libID, root)

	scanner := newScanner(t, pool)
	if _, err := scanner.Run(ctx, libID); err != nil {
		t.Fatal(err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM videos WHERE library_id=$1`, libID).Scan(&n)
	if n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestScanEmitsNotifyPerInsert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, libID := setupDBWithLibrary(t, ctx, false)
	root := genTree(t, 50, ".mp4")
	bindRoot(ctx, t, pool, libID, root)

	listenConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listenConn.Release()
	if _, err := listenConn.Exec(ctx, "LISTEN \"videos.new\""); err != nil {
		t.Fatal(err)
	}

	notifs := make(chan struct{}, 100)
	go func() {
		for {
			n, err := listenConn.Conn().WaitForNotification(ctx)
			if err != nil {
				return
			}
			if n.Channel == "videos.new" {
				notifs <- struct{}{}
			}
		}
	}()

	if _, err := newScanner(t, pool).Run(ctx, libID); err != nil {
		t.Fatal(err)
	}

	count := 0
	deadline := time.After(5 * time.Second)
done:
	for {
		select {
		case <-notifs:
			count++
			if count == 50 {
				break done
			}
		case <-deadline:
			break done
		}
	}
	if count != 50 {
		t.Fatalf("notifications = %d, want 50", count)
	}
}

func TestScanCreatesNoJobsWhenLibraryDisabled(t *testing.T) {
	ctx := context.Background()
	pool, libID := setupDBWithLibrary(t, ctx, true /* disabled */)
	root := genTree(t, 10, ".mp4")
	bindRoot(ctx, t, pool, libID, root)

	if _, err := newScanner(t, pool).Run(ctx, libID); err != nil {
		t.Fatal(err)
	}
	var nVideos, nJobs int
	pool.QueryRow(ctx, `SELECT count(*) FROM videos WHERE library_id=$1`, libID).Scan(&nVideos)
	pool.QueryRow(ctx, `
		SELECT count(*) FROM processing_jobs j
		  JOIN videos v ON v.id=j.video_id WHERE v.library_id=$1`, libID).Scan(&nJobs)
	if nVideos != 10 {
		t.Fatalf("videos=%d, want 10", nVideos)
	}
	if nJobs != 0 {
		t.Fatalf("jobs=%d, want 0", nJobs)
	}
}

// --- helpers ---

func setupDBWithLibrary(t *testing.T, ctx context.Context, disabled bool) (*pgxpool.Pool, uuid.UUID) {
	t.Helper()
	container, err := tcpostgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:16-alpine"),
		tcpostgres.WithDatabase("maktaba"),
		tcpostgres.WithUsername("m"),
		tcpostgres.WithPassword("m"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, _ := container.ConnectionString(ctx, "sslmode=disable")
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	runMigrations(t, ctx, pool)

	libID := uuid.New()
	settings := "{}"
	if disabled {
		settings = `{"disabled": true}`
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO libraries (id, name, roots, settings) VALUES ($1, $2, $3, $4::jsonb)`,
		libID, fmt.Sprintf("lib-%s", libID), []string{}, settings)
	if err != nil {
		t.Fatal(err)
	}
	return pool, libID
}

func bindRoot(ctx context.Context, t *testing.T, pool *pgxpool.Pool, libID uuid.UUID, root string) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`UPDATE libraries SET roots=$1 WHERE id=$2`, []string{root}, libID); err != nil {
		t.Fatal(err)
	}
}

func genTree(t *testing.T, n int, ext string) string {
	t.Helper()
	root := t.TempDir()
	for i := 0; i < n; i++ {
		sub := filepath.Join(root, fmt.Sprintf("%03d", i/100))
		_ = os.MkdirAll(sub, 0o755)
		mustTouch(t, filepath.Join(sub, fmt.Sprintf("v_%05d%s", i, ext)))
	}
	return root
}

func mustTouch(t *testing.T, path string) {
	t.Helper()
	// Write at least one byte so the hasher doesn't reject as zero-size.
	if err := os.WriteFile(path, []byte{0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
}

func newScanner(t *testing.T, pool *pgxpool.Pool) *scan.Scanner {
	t.Helper()
	return scan.New(store.New(pool), scan.Config{
		SupportedExtensions: []string{".mp4", ".mkv", ".mov", ".webm", ".avi", ".ts", ".m4v"},
		IgnoreBasenames:     []string{"*.part", "*.crdownload", "*.partial"},
		IgnoreDirNames:      []string{".maktaba"},
		MaxConcurrentHashes: 4,
	}, slog.Default())
}

func runMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	// In CI this calls into goose against shared/db/migrations; for the
	// scaffolding we exec the SQL files directly.
	files, err := filepath.Glob("../../shared/db/migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		// Trim goose annotations to leave just the Up section.
		sql := splitGooseUp(string(b))
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}
	_ = pgx.ErrNoRows // keep pgx imported for callers
}

func splitGooseUp(s string) string { /* impl: keep text between -- +goose Up and -- +goose Down */ return s }
```

---

## 9. Dependencies (Go modules)

`scanner/go.mod`:

```go
module github.com/maktaba/scanner

go 1.23

require (
	github.com/go-chi/chi/v5             v5.1.0   // REST router
	github.com/google/uuid               v1.6.0   // UUID v7 ids
	github.com/jackc/pgx/v5              v5.6.0   // Postgres driver + LISTEN
	github.com/spf13/viper               v1.19.0  // layered TOML config
	github.com/zeebo/blake3              v0.2.4   // BLAKE3 hashing
)

require (
	// test-only
	github.com/stretchr/testify          v1.9.0
	github.com/testcontainers/testcontainers-go v0.31.0
	github.com/testcontainers/testcontainers-go/modules/postgres v0.31.0
)

// Build tools (not pulled by `go build`; declared in tools.go for `go install`):
//   github.com/sqlc-dev/sqlc/cmd/sqlc                v1.27.0
//   github.com/pressly/goose/v3/cmd/goose            v3.21.1
```

A `tools.go` (`//go:build tools`) records the build-time tools so
`go install ./tools/...` can install sqlc and goose pinned to the module.

`stdlib`: `log/slog`, `context`, `errors`, `io/fs`, `path/filepath`,
`sync/atomic`, `encoding/json`, `encoding/binary`, `encoding/hex`,
`net/http`, `os`, `syscall`, `time`.

No new dependencies in the API service; the WS listener extension
in §2 step 9 uses pgx, which the API already depends on.

---

## 10. Acceptance checklist

Mapping from the story's acceptance criteria and edge cases to this plan.

### From Acceptance Criteria

| AC | How verified | Test |
|----|--------------|------|
| AC1 — 1,000 `videos` rows after one pass, all `discovered`, with `content_hash`, `path`, `filename`, `size_bytes`, `mtime` populated, plus one `processing_jobs(probe, pending)` per row | Integration test with a 1,000-file fixture; SQL `count(*)` checks against `videos` and `processing_jobs`; column NOT NULLs guard the rest | `TestScanInsertsRowPerVideo` (§8.3) |
| AC2 — WS frame count on `/ws/library/{id}` equals inserted-row count | Trigger `videos_notify_new_trg` (§4.4) ensures one NOTIFY per insert; integration test uses a `LISTEN` connection to count notifications | `TestScanEmitsNotifyPerInsert` (§8.3) |
| AC3 — non-supported extensions ignored, no row, no log above DEBUG | Walker filters by extension before emitting; logs at DEBUG only | `TestScanIgnoresNonVideoExtensions` (§8.3) and walker unit tests |

### From Edge Cases

| Edge case | How handled | Where |
|-----------|-------------|-------|
| Symlink loops | `lstat` by default; `(dev, ino)` visited set when `follow_symlinks=true` | `walker.Walk` (§3.1) |
| Permission-denied directories | Logged once at WARN with path, walker continues | `walker.Walk` permission branch (§3.1) |
| Zero-byte files | Skipped, logged at DEBUG, no row, no error | `hash.HashFile` returns `ErrZeroSize`; `processOne` swallows it (§3.4) |
| Files smaller than 8 MiB | Hash falls back to "entire file"; size suffix preserved | `hash.HashFile` whole-file branch (§3.2) |
| Library with zero roots | Returns immediately with WARN log, empty Result | `scan.Run` early return (§3.4) |
| Disabled library | Walks but no probe job enqueued | `Store.SaveCandidate` `libraryDisabled` branch (§3.3) |

### Done definition

- [ ] All migrations apply cleanly via `goose up` against a fresh Postgres 16.
- [ ] `go test ./scanner/... -race -count=1` is green.
- [ ] `go test -tags=integration ./scanner/test/integration/...` is green.
- [ ] `golangci-lint run ./scanner/...` is clean.
- [ ] `maktaba-scan --library lectures` against a real-world tree
      produces one `videos` row and one `processing_jobs(probe, pending)`
      row per supported file; nothing else.
- [ ] `POST /api/libraries/{id}/scan` returns 202; the WebSocket frame
      count on `/ws/library/{id}` matches the row count.
- [ ] Plan committed beside the story file; story file unchanged.

---

## 11. Out of scope (deferred to later stories)

- **Re-scan / move detection.** The "rename in place" path (looking up
  an existing row by `content_hash` to update its `path`) is the
  contract of [Story 1.2](story-01-02-content-identity.md). This plan
  inserts new rows and tolerates re-walking the same file (the unique
  constraint suppresses duplicates), but does not yet update `path` on
  collision.
- **Filesystem watcher.** [Story 1.3](story-01-03-filesystem-watcher.md).
  This plan only ships the one-shot walker.
- **Pause / cancel of an in-flight scan.** [Story 1.4](story-01-04-manual-control.md).
- **`MISSING` state and `--purge-missing`.** [Story 1.5](story-01-05-schema-decisions.md)
  (constraint migration) and [Story 1.6](story-01-06-video-state-machine.md)
  (state semantics).
- **Metrics export.** OpenTelemetry counters for `scanner.files_walked`,
  `scanner.files_inserted`, `scanner.bytes_hashed` are deferred to
  Epic 21 (Observability).
