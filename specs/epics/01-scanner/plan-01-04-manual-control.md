# Plan 1.4 — Manual Control Surface (implementation)

> Implementation plan for [story-01-04-manual-control.md](story-01-04-manual-control.md).
> Self-contained: a developer should be able to ship the story from this
> document alone.

## 0. Decisions and departures from `architecture.md`

The story's three acceptance criteria — start (idempotent), stop (clean
cancellation), and dry-run — sit on top of the scanner shipped by
[Plan 1.1](plan-01-01-file-discovery.md) and the per-library state
table introduced by [Plan 1.5](plan-01-05-schema-decisions.md). This plan only
adds the **control plane** (mutex, cancellation signal, progress
read-back, dry-run mode). It changes no scanning logic.

| # | Decision | Source | Rationale |
|---|----------|--------|-----------|
| D1 | The cross-process mutex is a Postgres **session-scoped** advisory lock keyed by `hashtext('scan:' || library_id::text)`, exactly as specified by the story's edge-case section. | [Story 1.4 Edge cases](story-01-04-manual-control.md). | One key namespace, one lock per library, automatic release if the holder crashes (the session dies and Postgres releases the lock). No need for a separate `scan_runs` row with crash-recovery TTLs. |
| D2 | The `maktaba-pipeline scan --dry-run` command in the story is shipped as **`maktaba-scan --dry-run`** (the Go binary established in [Plan 1.1 D1](plan-01-01-file-discovery.md)), not as a Python `maktaba-pipeline` subcommand. | Departs from the literal command name in [Story 1.4 AC #3](story-01-04-manual-control.md). Aligns with [Plan 1.1 D1](plan-01-01-file-discovery.md): the scanner is a Go binary, the Python pipeline owns ML stages only. | Dry-run is a property of the walker+hasher path; that path lives in Go. Re-implementing it in Python (or shelling out from Python to Go) just to keep the command name is gratuitous duplication. The flag, not the binary name, is what AC #3 actually tests. |
| D3 | Cancellation is **cooperative at file boundaries**. The scanner checks two signals after each per-file transaction commits: (a) `ctx.Done()` for in-process cancel, (b) `library_scan_state.cancel_requested = true` for cross-process cancel (CLI scan + API DELETE). | [Story 1.4 AC #2](story-01-04-manual-control.md): "stops within 5 s after the next file boundary". | Per-file is the natural cut point — Plan 1.1 already commits one transaction per file. We do not need finer granularity, and coarser granularity would miss the 5 s SLA on slow hashes. |
| D4 | The DELETE response is `202 Accepted`, not a synchronous "already cancelled". Cancellation is observed asynchronously over `/ws/library/{id}` (`scan.cancelled` frame) or via `GET /api/libraries/{id}/scan` polling. | New decision; story is silent. | The scanner can be in another process. We cannot wait synchronously without holding HTTP threads for up to 5 s. The story's 5 s SLA is on the worker, not on the API response. |
| D5 | The `library_scan_state` table from [Plan 1.5 §2.3](plan-01-05-schema-decisions.md) is the authoritative read source for scan progress and the cancel signal. This plan adds two columns: `cancel_requested BOOLEAN` and `progress_pct REAL`. | Extends [Plan 1.5](plan-01-05-schema-decisions.md) by one migration. | Plan 1.5 owns the watermark/counters table; this story needs two more fields. Adding them in a separate migration keeps the `library_scan_state` migration single-purpose and lets this story land independently of 1.5 if 1.5 slips. |

If D2 is rejected and the dry-run must live in `maktaba-pipeline`:
the CLI section, dry-run scaffolding, and one test in §8 are the only
parts that change; the API contract, lock SQL, migrations, and
acceptance checklist are language-agnostic.

---

## 1. Architecture diagram

```
                    ┌──────────────────────────────────────────────────┐
                    │                client (UI / CLI)                 │
                    └──┬───────────────────┬────────────────────────┬──┘
                       │ POST              │ DELETE                 │ exec
                       │ /api/libraries    │ /api/libraries         │ maktaba-scan
                       │   /{id}/scan      │   /{id}/scan           │   --library NAME
                       │                   │                        │   --dry-run
                       ▼                   ▼                        ▼
   ┌──────────────────────────────────────────────────────────┐  ┌──────────┐
   │                   API Service (Go)                       │  │   CLI    │
   │ ┌──────────────────────┐    ┌──────────────────────────┐ │  │ (Go)     │
   │ │ POST handler         │    │ DELETE handler           │ │  └────┬─────┘
   │ │  - try advisory lock │    │  - UPDATE cancel_req=t   │ │       │
   │ │  - if held → 200     │    │  - cancel() in-proc ctx  │ │       │
   │ │    {already_running, │    │  - 202 Accepted          │ │       │
   │ │     progress}        │    └──────────┬───────────────┘ │       │
   │ │  - else 202 + start  │               │                 │       │
   │ └──────────┬───────────┘               │                 │       │
   │            │                           │                 │       │
   │            ▼                           ▼                 │       │
   │ ┌──────────────────────────────────────────────────────┐ │       │
   │ │              activeScans (sync.Map)                  │ │       │
   │ │  library_id → { ctx, cancel(), result *Result }      │ │       │
   │ └─────────────────────┬────────────────────────────────┘ │       │
   └───────────────────────┼──────────────────────────────────┘       │
                           │                                          │
                           ▼                                          ▼
              ┌───────────────────────────────────────────────────────────┐
              │                 scan.Scanner (shared package)             │
              │  ┌────────────────────────────────────────────────────┐   │
              │  │ Run(ctx, libraryID, opts):                          │  │
              │  │   acquireAdvisoryLock(library_id)  ── holds session │  │
              │  │   if not acquired → ErrAlreadyRunning               │  │
              │  │                                                     │  │
              │  │   for each candidate from walker:                   │  │
              │  │     hash + Store.Save (one tx)                      │  │
              │  │     ── after each commit ──                         │  │
              │  │     if ctx.Done() OR pollCancel(every N files)      │  │
              │  │       → exit cleanly                                │  │
              │  │                                                     │  │
              │  │   release advisory lock; finalize state             │  │
              │  └────────────────────────────────────────────────────┘   │
              └────────────────────────────┬──────────────────────────────┘
                                           │ pgx
                                           ▼
              ┌──────────────────────────────────────────────────────────┐
              │                       PostgreSQL 16                      │
              │  pg_advisory_lock(hashtext('scan:' || library_id::text)) │
              │  library_scan_state.cancel_requested                     │
              │  library_scan_state.progress_pct                         │
              │  videos · processing_jobs (per-file tx — Plan 1.1)       │
              └──────────────────────────────────────────────────────────┘
```

**Two-layer cancellation** (D3):
- *In-process*: DELETE handler calls `entry.cancel()`; the Go ctx
  cancellation fires immediately, the scanner exits at the next
  per-file boundary check on `ctx.Done()`.
- *Cross-process*: DELETE handler writes
  `library_scan_state.cancel_requested = true`; the scanner running
  in `maktaba-scan` (another process) polls the column every N files
  and exits at the next boundary.

The advisory lock guarantees these two paths can never both be in
flight for the same library.

---

## 2. Implementation steps (ordered)

Each step is a discrete commit. Bracketed paths are relative to the
repo root. References to "Plan 1.1" assume the scanner package
landed (this is the prerequisite epic-1 story).

### Step 1 — Migration `0008_scan_control.sql`

This plan owns slot `0008` per the canonical
[migration manifest](../../../shared/db/migrations/MANIFEST.md). It adds
two columns to `library_scan_state` (introduced by
[plan-01-05](plan-01-05-schema-decisions.md) at slot 0006). Hard
dependencies: slots 0001, 0002, 0006.

See §4.1 below for the SQL.

### Step 2 — Advisory lock helper

`[scanner/internal/scanlock/scanlock.go]` — a small wrapper around
`pg_try_advisory_lock` and `pg_advisory_unlock`. Holds the pgx
connection for the lock's lifetime; returns it to the pool on
release. See §3.1.

### Step 3 — In-process active-scan registry

`[api/internal/scan/registry.go]` — a `sync.Map` keyed by
`uuid.UUID` (library id) holding `{ctx, cancel, scanID, startedAt}`.
Exposed methods: `Start`, `Cancel`, `Get`. See §3.2.

The registry lives in the API service so the HTTP handlers can
cancel without going through the DB. Cross-process cancellation is
the DB path.

### Step 4 — Scanner extensions (cancel polling, progress)

Modify `[scanner/internal/scan/scanner.go]` (from Plan 1.1):

- Add `Options` struct with `DryRun bool` and `CancelPollEvery int`
  (default 50 files).
- After each per-file tx commits, check:
  - `ctx.Err() != nil` → return `(*Result, ctx.Err())`
  - Every `CancelPollEvery` files: query
    `library_scan_state.cancel_requested`. If true → return
    `(*Result, ErrCancelled)`.
- Update `library_scan_state.progress_pct` every
  `CancelPollEvery` files (same poll round-trip — `RETURNING
  cancel_requested`).
- On entry, check `libraries.deleted_at IS NOT NULL`; if so, abort
  before walking.
- See §3.3.

### Step 5 — Dry-run store

`[scanner/internal/store/dryrun.go]` — a `DryRunStore` that
implements the same `Save` contract as `store.Store` but writes
nothing to the database. Instead it emits one JSONL line per
candidate to a configured `io.Writer` (stdout for the CLI,
`bytes.Buffer` for tests):

```json
{"action":"would_insert","library_id":"...","path":"...",
 "filename":"...","size_bytes":1234,"mtime":"...","content_hash":"..."}
```

`scan.Run` accepts a `Store` interface; the dry-run path swaps
the implementation. The walker and hasher are unchanged.

The dry-run path **does not acquire the advisory lock and does not
write to `library_scan_state`** — a dry-run is a read-only
preview that must not interfere with a concurrent real scan.

See §3.4.

### Step 6 — POST handler updates

`[api/internal/scan/handler.go]` — replace the Plan 1.1
fire-and-forget handler:

1. Try `pg_try_advisory_lock(hashtext('scan:' || $1::text))`.
2. If `false`: read `library_scan_state.progress_pct`,
   `in_progress_sweep_id`; respond 200 with
   `{status: "already_running", scan_id, progress}`.
3. If `true`: register in `activeScans`; spawn goroutine running
   `scan.Run` with the in-process ctx; release the advisory lock
   when the goroutine exits; respond 202 with
   `{status: "started", scan_id, library_id, started_at}`.

See §3.5.

### Step 7 — DELETE handler

`[api/internal/scan/handler.go]` — the cancel handler:

1. `UPDATE library_scan_state SET cancel_requested = true WHERE
   library_id = $1 AND in_progress_sweep_id IS NOT NULL`.
2. If the registry has an in-process entry for this library, call
   `entry.cancel()` (in-process fast path — no need to wait for
   the DB poll).
3. Respond 202 with `{status: "cancelling", library_id, scan_id?}`.

If no scan is in flight (no row found, no registry entry), respond
200 with `{status: "idle"}`. This is idempotent and matches the
broader convention in [architecture.md §9.5](../../architecture.md)
that pause/resume/cancel return 200 unchanged when the request
matches current state.

See §3.5.

### Step 8 — GET handler (progress read-back)

`[api/internal/scan/handler.go]` — `GET /api/libraries/{id}/scan`:

```
{status: "running" | "idle" | "cancelling",
 scan_id?, started_at?, progress_pct?, files_visited?, files_inserted?}
```

Reads `library_scan_state` and the registry. AC #1 needs the POST
handler to return `progress`, so this endpoint is the same projection
exposed for polling.

### Step 9 — CLI updates

`[scanner/cmd/maktaba-scan/main.go]` — three new flags:

```
--dry-run           print would-be inserts to stdout, write nothing
--cancel            DELETE the running scan for --library and exit
--watch             tail progress to stdout while a scan is running
```

For Story 1.4, only `--dry-run` and `--cancel` are required. `--watch`
is nice-to-have; if it slips, none of the ACs fail.

When `--dry-run` is set, the CLI never acquires the advisory lock,
never writes to `library_scan_state`, and the only side effect is
stdout. Exit code 0 if the dry-run completes; non-zero only on
walker/hasher errors (consistent with Plan 1.1 §6.3).

When `--cancel` is set, the CLI does the same UPDATE the DELETE
handler does, then exits.

See §3.6.

### Step 10 — Tests

Write the test suite from §7 below. The three story-mandated tests
(`test_scan_idempotent_concurrent_invocation`,
`test_scan_cancellation_cleans_up`, `test_dry_run_writes_nothing`)
all live in `test/integration/manual_control_test.go`.

Run `go test ./scanner/... ./api/internal/scan/... -race -count=1`
locally and in CI; integration tests stand up Postgres via
testcontainers (same harness as Plan 1.1 §7.2).

---

## 3. Go code scaffolding

### 3.1 `scanner/internal/scanlock/scanlock.go`

```go
package scanlock

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrAlreadyHeld is returned by TryAcquire when another session holds
// the per-library scan lock.
var ErrAlreadyHeld = errors.New("scanlock: lock already held by another session")

// Lock represents a session-scoped Postgres advisory lock pinned to one
// library_id. The connection is held for the duration of the lock and
// released back to the pool by Close.
type Lock struct {
	conn      *pgxpool.Conn
	libraryID uuid.UUID
	lockKey   int64
	released  bool
}

// TryAcquire attempts pg_try_advisory_lock(hashtext('scan:'||library_id))
// in a single round-trip. On success it returns a *Lock that pins one
// connection from the pool until Close is called. On failure
// (ErrAlreadyHeld) the connection is returned to the pool immediately.
//
// Uses pg_try_advisory_lock(bigint), promoting hashtext's int4 result
// to int8 so the call is unambiguous across pgx and psql clients.
func TryAcquire(ctx context.Context, pool *pgxpool.Pool, libraryID uuid.UUID) (*Lock, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("scanlock: acquire conn: %w", err)
	}

	var key int64
	if err := conn.QueryRow(ctx,
		`SELECT hashtext('scan:' || $1::text)::bigint`, libraryID,
	).Scan(&key); err != nil {
		conn.Release()
		return nil, fmt.Errorf("scanlock: derive key: %w", err)
	}

	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&ok); err != nil {
		conn.Release()
		return nil, fmt.Errorf("scanlock: try lock: %w", err)
	}
	if !ok {
		conn.Release()
		return nil, ErrAlreadyHeld
	}
	return &Lock{conn: conn, libraryID: libraryID, lockKey: key}, nil
}

// Close releases the advisory lock and returns the connection to the
// pool. Safe to call multiple times.
func (l *Lock) Close(ctx context.Context) error {
	if l == nil || l.released {
		return nil
	}
	l.released = true
	defer l.conn.Release()
	if _, err := l.conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, l.lockKey); err != nil {
		// Connection death already releases the lock server-side; log
		// but do not surface to caller. errors.Is is the canonical
		// project pattern; we never string-match.
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("scanlock: unlock: %w", err)
		}
	}
	return nil
}

// Conn exposes the held connection so the caller can reuse it for
// queries that should observe the lock-holding session (rarely useful;
// mostly available for tests).
func (l *Lock) Conn() *pgxpool.Conn { return l.conn }
```

### 3.2 `api/internal/scan/registry.go`

```go
package scan

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Entry is one in-flight scan. Fields are immutable after Start
// except for the cancel func, which is safe to call at any time.
type Entry struct {
	LibraryID uuid.UUID
	ScanID    uuid.UUID
	StartedAt time.Time
	cancel    context.CancelFunc
}

// Cancel triggers in-process cancellation. Idempotent.
func (e *Entry) Cancel() { e.cancel() }

// Registry tracks scans started by the API service (this process).
// Cross-process scans (CLI in another binary) are not in this map;
// cancellation for those flows through library_scan_state.cancel_requested.
type Registry struct {
	scans sync.Map // map[uuid.UUID]*Entry, keyed by library_id
}

func NewRegistry() *Registry { return &Registry{} }

// Start registers a new scan and returns the derived context. Returns
// (entry, false) if a scan for this library is already registered;
// the caller is responsible for surfacing "already running" via the
// advisory-lock layer (this is just an in-process cache).
func (r *Registry) Start(parent context.Context, libraryID uuid.UUID, scanID uuid.UUID) (*Entry, context.Context, bool) {
	ctx, cancel := context.WithCancel(parent)
	e := &Entry{
		LibraryID: libraryID,
		ScanID:    scanID,
		StartedAt: time.Now().UTC(),
		cancel:    cancel,
	}
	if _, loaded := r.scans.LoadOrStore(libraryID, e); loaded {
		cancel() // discard the unused ctx
		return nil, nil, false
	}
	return e, ctx, true
}

// Get returns the active entry for this library, or nil.
func (r *Registry) Get(libraryID uuid.UUID) *Entry {
	v, ok := r.scans.Load(libraryID)
	if !ok {
		return nil
	}
	return v.(*Entry)
}

// Cancel triggers in-process cancellation if the library has a
// registered scan; returns true if an entry existed.
func (r *Registry) Cancel(libraryID uuid.UUID) bool {
	if e := r.Get(libraryID); e != nil {
		e.Cancel()
		return true
	}
	return false
}

// Done is called by the worker goroutine when the scan exits, removing
// the entry from the map.
func (r *Registry) Done(libraryID uuid.UUID) {
	r.scans.Delete(libraryID)
}
```

### 3.3 `scanner/internal/scan/scanner.go` — additions

The Plan 1.1 `Scanner` type grows a small `Options` parameter and two
new behaviors on the per-file boundary check.

```go
// Options carries the runtime knobs that distinguish a normal scan
// from a dry-run, and tune the cancellation polling cadence.
type Options struct {
	DryRun          bool
	CancelPollEvery int    // poll cancel_requested every N files; default 50
	OnProgress      func(pct float32, visited int64) // optional progress callback
}

// Run drives one scan. Three new responsibilities versus Plan 1.1:
//   1. Refuse to start if libraries.deleted_at IS NOT NULL.
//   2. After every per-file commit, check ctx.Done() and (every
//      Options.CancelPollEvery files) library_scan_state.cancel_requested.
//   3. Update library_scan_state.progress_pct on the same poll round-trip.
//
// Dry-run mode (Options.DryRun) skips all writes and acquires no
// advisory lock; the caller must still pass a ScanID for log/output
// correlation.
func (s *Scanner) Run(ctx context.Context, libraryID uuid.UUID, opts Options) (*Result, error) {
	if opts.CancelPollEvery <= 0 {
		opts.CancelPollEvery = 50
	}

	lib, err := s.store.GetLibrary(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	if lib.DeletedAt != nil {
		s.log.Warn("scanner.library_deleted", "library_id", lib.ID)
		return nil, ErrLibraryDeleted
	}

	res := &Result{LibraryID: lib.ID, StartedAt: time.Now().UTC()}
	defer func() { res.FinishedAt = time.Now().UTC() }()

	// ... walker/hasher pool setup unchanged from Plan 1.1 §3.4 ...

	processedSinceCheck := 0
	for c := range candidates {
		atomic.AddInt64(&res.FilesWalked, 1)

		if err := s.processOne(ctx, lib, c, res, opts); err != nil {
			res.Errors = append(res.Errors, ScanError{Path: c.Path, Err: err})
		}

		// ── Boundary check ───────────────────────────────────────────
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		processedSinceCheck++
		if processedSinceCheck >= opts.CancelPollEvery {
			processedSinceCheck = 0
			if !opts.DryRun {
				cancelled, pct, err := s.store.PollControlAndProgress(
					ctx, lib.ID, res.FilesInserted, res.FilesWalked, lib.EstimatedFiles,
				)
				if err != nil {
					s.log.Warn("scanner.control_poll_failed", "err", err)
					continue
				}
				if cancelled {
					return res, ErrCancelled
				}
				if opts.OnProgress != nil {
					opts.OnProgress(pct, res.FilesWalked)
				}
			}

			// Library deletion check (story edge case).
			deleted, err := s.store.IsLibraryDeleted(ctx, lib.ID)
			if err == nil && deleted {
				return res, ErrLibraryDeleted
			}
		}
	}

	return res, nil
}

// Sentinel errors. ErrCancelled is returned when cancel_requested goes
// true mid-scan; ErrLibraryDeleted when libraries.deleted_at flips.
var (
	ErrCancelled      = errors.New("scan: cancelled by user")
	ErrLibraryDeleted = errors.New("scan: library deleted")
)
```

The corresponding `Store` additions (one round-trip per poll):

```go
// PollControlAndProgress writes the current progress in the same query
// that reads cancel_requested, saving a round-trip on a hot path.
//
// Returns (cancelRequested, progressPct, err).
func (s *Store) PollControlAndProgress(
	ctx context.Context,
	libraryID uuid.UUID,
	inserted, walked int64,
	estimatedFiles int64, // optional; 0 if unknown
) (bool, float32, error) {
	pct := float32(0)
	if estimatedFiles > 0 {
		pct = float32(walked) / float32(estimatedFiles) * 100
		if pct > 99.0 {
			pct = 99.0 // cap below 100; final 100 is set on completion
		}
	}
	const q = `
		UPDATE library_scan_state
		   SET files_visited = $2,
		       files_inserted = $3,
		       progress_pct  = $4,
		       updated_at    = now()
		 WHERE library_id    = $1
		RETURNING cancel_requested, progress_pct`
	var cancel bool
	var stored float32
	err := s.pool.QueryRow(ctx, q, libraryID, walked, inserted, pct).Scan(&cancel, &stored)
	return cancel, stored, err
}

// IsLibraryDeleted returns true if libraries.deleted_at IS NOT NULL.
// Cheap: PK lookup.
func (s *Store) IsLibraryDeleted(ctx context.Context, libraryID uuid.UUID) (bool, error) {
	const q = `SELECT deleted_at IS NOT NULL FROM libraries WHERE id = $1`
	var deleted bool
	err := s.pool.QueryRow(ctx, q, libraryID).Scan(&deleted)
	return deleted, err
}
```

### 3.4 `scanner/internal/store/dryrun.go`

```go
package store

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DryRunStore implements the Store interface but writes nothing to
// the database. Each SaveCandidate call emits one JSONL line to the
// configured Writer, so callers can pipe the output through `wc -l`,
// `jq`, or a fixture file.
//
// DryRunStore is safe for concurrent use by the hasher pool.
type DryRunStore struct {
	w  io.Writer
	mu sync.Mutex
}

func NewDryRunStore(w io.Writer) *DryRunStore { return &DryRunStore{w: w} }

func (d *DryRunStore) GetLibrary(ctx context.Context, id uuid.UUID) (Library, error) {
	// In the CLI dry-run path, the caller still loads the library
	// definition from the real store (to get roots, settings) — we
	// only short-circuit Save. So GetLibrary should never be called
	// on this implementation; if it is, fail loudly.
	return Library{}, errDryRunNotSupported
}

func (d *DryRunStore) SaveCandidate(ctx context.Context, p SaveCandidateParams, libraryDisabled bool) (SaveCandidateResult, error) {
	line := struct {
		Action      string    `json:"action"`
		LibraryID   uuid.UUID `json:"library_id"`
		Path        string    `json:"path"`
		Filename    string    `json:"filename"`
		SizeBytes   int64     `json:"size_bytes"`
		Mtime       time.Time `json:"mtime"`
		ContentHash string    `json:"content_hash"`
	}{
		Action:      "would_insert",
		LibraryID:   p.LibraryID,
		Path:        p.Path,
		Filename:    p.Filename,
		SizeBytes:   p.SizeBytes,
		Mtime:       p.Mtime,
		ContentHash: p.ContentHash,
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	enc := json.NewEncoder(d.w)
	if err := enc.Encode(line); err != nil {
		return SaveCandidateResult{}, err
	}
	// Synthetic id; no row is written. Caller treats every dry-run
	// file as "Inserted=true" so totals match a live run.
	return SaveCandidateResult{VideoID: uuid.New(), Inserted: true}, nil
}

func (d *DryRunStore) PollControlAndProgress(ctx context.Context, _ uuid.UUID, _, _, _ int64) (bool, float32, error) {
	return false, 0, nil // dry-run never cancellable via DB; only via ctx
}

func (d *DryRunStore) IsLibraryDeleted(ctx context.Context, _ uuid.UUID) (bool, error) {
	return false, nil
}

var errDryRunNotSupported = errorString("dryrun: GetLibrary is not implemented; load library from real store first")

type errorString string

func (e errorString) Error() string { return string(e) }
```

The `Store` interface (one new line in `store/store.go`):

```go
type Store interface {
	GetLibrary(ctx context.Context, id uuid.UUID) (Library, error)
	SaveCandidate(ctx context.Context, p SaveCandidateParams, libraryDisabled bool) (SaveCandidateResult, error)
	PollControlAndProgress(ctx context.Context, libraryID uuid.UUID, inserted, walked, estimatedFiles int64) (bool, float32, error)
	IsLibraryDeleted(ctx context.Context, libraryID uuid.UUID) (bool, error)
}
```

The `*store.Store` from Plan 1.1 trivially satisfies this; the
`*DryRunStore` here also satisfies it. The CLI hands the right one
to the scanner depending on `--dry-run`.

### 3.5 `api/internal/scan/handler.go`

```go
package scan

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/maktaba/scanner/internal/scan"
	"github.com/maktaba/scanner/internal/scanlock"
	"github.com/maktaba/scanner/internal/store"
)

type Handler struct {
	Pool     *pgxpool.Pool
	Store    *store.Store
	Scanner  *scan.Scanner
	Registry *Registry
}

// POST /api/libraries/{id}/scan
//
// Returns:
//   200 { status: "already_running", scan_id, progress }
//   202 { status: "started", scan_id, library_id, started_at }
//   404                                       — library does not exist
//   400                                       — invalid library id
func (h *Handler) PostScan(w http.ResponseWriter, r *http.Request) {
	libID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid library id", http.StatusBadRequest)
		return
	}

	lock, err := scanlock.TryAcquire(r.Context(), h.Pool, libID)
	if err != nil && !errors.Is(err, scanlock.ErrAlreadyHeld) {
		http.Error(w, "lock acquire failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if errors.Is(err, scanlock.ErrAlreadyHeld) {
		// Another session holds the lock — read progress and report.
		var (
			scanID   uuid.UUID
			progress float32
		)
		row := h.Pool.QueryRow(r.Context(), `
			SELECT in_progress_sweep_id, progress_pct
			  FROM library_scan_state
			 WHERE library_id = $1`, libID)
		if err := row.Scan(&scanID, &progress); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":   "already_running",
			"scan_id":  scanID,
			"progress": progress,
		})
		return
	}

	scanID := uuid.New()
	entry, runCtx, ok := h.Registry.Start(context.Background(), libID, scanID)
	if !ok {
		// Registry entry already exists despite the lock being free —
		// possible during a brief release/reacquire window. Treat as
		// already_running for safety.
		_ = lock.Close(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"status": "already_running"})
		return
	}

	go func() {
		defer h.Registry.Done(libID)
		defer lock.Close(context.Background())
		_, _ = h.Scanner.Run(runCtx, libID, scan.Options{})
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":     "started",
		"scan_id":    entry.ScanID,
		"library_id": libID,
		"started_at": entry.StartedAt,
	})
}

// DELETE /api/libraries/{id}/scan
//
// Returns:
//   202 { status: "cancelling", library_id, scan_id? }
//   200 { status: "idle",       library_id }
func (h *Handler) DeleteScan(w http.ResponseWriter, r *http.Request) {
	libID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid library id", http.StatusBadRequest)
		return
	}

	tag, err := h.Pool.Exec(r.Context(), `
		UPDATE library_scan_state
		   SET cancel_requested = true,
		       updated_at       = now()
		 WHERE library_id        = $1
		   AND in_progress_sweep_id IS NOT NULL`, libID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	hadInProcess := h.Registry.Cancel(libID)
	if tag.RowsAffected() == 0 && !hadInProcess {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "idle",
			"library_id": libID,
		})
		return
	}

	body := map[string]any{
		"status":     "cancelling",
		"library_id": libID,
	}
	if e := h.Registry.Get(libID); e != nil {
		body["scan_id"] = e.ScanID
	}
	writeJSON(w, http.StatusAccepted, body)
}

// GET /api/libraries/{id}/scan
func (h *Handler) GetScan(w http.ResponseWriter, r *http.Request) {
	libID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid library id", http.StatusBadRequest)
		return
	}
	var (
		sweep    *uuid.UUID
		progress float32
		visited  int64
		inserted int64
		started  *time.Time
		cancel   bool
	)
	err = h.Pool.QueryRow(r.Context(), `
		SELECT in_progress_sweep_id, progress_pct, files_visited,
		       files_inserted, in_progress_started, cancel_requested
		  FROM library_scan_state WHERE library_id = $1`, libID,
	).Scan(&sweep, &progress, &visited, &inserted, &started, &cancel)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	status := "idle"
	if sweep != nil {
		status = "running"
		if cancel {
			status = "cancelling"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         status,
		"scan_id":        sweep,
		"progress_pct":   progress,
		"files_visited":  visited,
		"files_inserted": inserted,
		"started_at":     started,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
```

### 3.6 `scanner/cmd/maktaba-scan/main.go` — `--dry-run` and `--cancel`

```go
// Excerpt — only the new flags. The existing flag set from Plan 1.1
// (--library, --config, --json, --purge-missing, --yes) is unchanged.

var (
	flagDryRun = flag.Bool("dry-run", false,
		"walk and hash, but write nothing to the database; emit JSONL to stdout")
	flagCancel = flag.Bool("cancel", false,
		"signal the running scan for --library to stop, then exit")
)

func run(ctx context.Context, cfg config.Config) (int, error) {
	pool, err := pgxpool.New(ctx, cfg.Database.URL)
	if err != nil {
		return 3, err
	}
	defer pool.Close()

	st := store.New(pool)
	lib, err := resolveLibrary(ctx, st, *flagLibrary)
	if err != nil {
		return 2, err
	}

	switch {
	case *flagCancel:
		if _, err := pool.Exec(ctx, `
			UPDATE library_scan_state
			   SET cancel_requested = true, updated_at = now()
			 WHERE library_id = $1 AND in_progress_sweep_id IS NOT NULL`, lib.ID); err != nil {
			return 3, err
		}
		fmt.Fprintf(os.Stderr, "scan cancellation requested for library %s\n", lib.Name)
		return 0, nil

	case *flagDryRun:
		// No advisory lock; no library_scan_state writes; stdout JSONL.
		dry := store.NewDryRunStore(os.Stdout)
		scanner := scan.New(dry, scanCfg(cfg), slog.Default())
		// Plant the library bypassing GetLibrary on the dry store.
		scanner.SetLibrary(lib)
		_, err := scanner.Run(ctx, lib.ID, scan.Options{DryRun: true})
		if err != nil {
			return 1, err
		}
		return 0, nil

	default:
		// Normal scan path — Plan 1.1 implementation.
		lock, err := scanlock.TryAcquire(ctx, pool, lib.ID)
		if err != nil {
			if errors.Is(err, scanlock.ErrAlreadyHeld) {
				fmt.Fprintln(os.Stderr, "another scan is already running for this library")
				return 4, nil
			}
			return 3, err
		}
		defer lock.Close(ctx)
		scanner := scan.New(st, scanCfg(cfg), slog.Default())
		_, err = scanner.Run(ctx, lib.ID, scan.Options{})
		if err != nil {
			return 1, err
		}
		return 0, nil
	}
}
```

`scanner.SetLibrary` is a one-line setter so the dry-run path can
inject the library projection it loaded itself, avoiding the
`DryRunStore.GetLibrary` stub.

---

## 4. Database migrations

### 4.1 `shared/db/migrations/0008_scan_control.sql`

Adds the cancel signal and progress field to `library_scan_state`,
and `deleted_at` to `libraries` (the latter is used by the
"Library deleted mid-scan" edge case — it lives in this migration
because it has no other dependencies in epic 1).

```sql
-- +goose Up
ALTER TABLE library_scan_state
    ADD COLUMN cancel_requested BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN progress_pct     REAL    NOT NULL DEFAULT 0;

-- Idempotent: libraries.deleted_at may already exist if epic 7 (Library
-- CRUD) landed first. The IF NOT EXISTS guard keeps this migration
-- order-independent.
ALTER TABLE libraries
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS libraries_alive_idx
    ON libraries (id) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS libraries_alive_idx;
ALTER TABLE libraries           DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE library_scan_state  DROP COLUMN cancel_requested;
ALTER TABLE library_scan_state  DROP COLUMN progress_pct;
```

The `cancel_requested` flag is **always reset** by `claimSweep` from
[Plan 1.5 §3](plan-01-05-schema-decisions.md) at the start of a new sweep. We
amend that query (single line) so that a stale `cancel_requested =
true` from a previous cancelled run does not abort the next attempt
on its first poll:

```sql
-- name: ClaimScanSweep :execrows  (Plan 1.5; this story amends)
UPDATE library_scan_state
   SET in_progress_sweep_id = $2,
       in_progress_started  = $3,
       cancel_requested     = false,        -- new in plan 1.4
       progress_pct         = 0,            -- new in plan 1.4
       files_visited        = 0,
       files_inserted       = 0,
       files_updated        = 0,
       files_skipped        = 0,
       files_marked_missing = 0,
       last_error           = NULL,
       updated_at           = now()
 WHERE library_id            = $1
   AND in_progress_sweep_id IS NULL;
```

This change is in-scope for this story because it is the safety net
for a sequence the story exercises directly:
`POST → DELETE → POST → expect a fresh scan to run, not exit
immediately`.

---

## 5. SQL queries (added in this story)

`[shared/db/queries/scanner/manual_control.sql]`:

```sql
-- name: TryAcquireScanLock :one
-- Session-scoped advisory lock keyed by hashtext('scan:'||library_id).
-- Returns true on success; the same session must run pg_advisory_unlock
-- (or close) to release.
SELECT pg_try_advisory_lock(hashtext('scan:' || $1::text)::bigint) AS acquired;

-- name: ReleaseScanLock :one
SELECT pg_advisory_unlock(hashtext('scan:' || $1::text)::bigint) AS released;

-- name: RequestCancelScan :execrows
UPDATE library_scan_state
   SET cancel_requested = true,
       updated_at       = now()
 WHERE library_id        = $1
   AND in_progress_sweep_id IS NOT NULL;

-- name: ReadScanProgress :one
SELECT in_progress_sweep_id,
       progress_pct,
       files_visited,
       files_inserted,
       cancel_requested,
       in_progress_started
  FROM library_scan_state
 WHERE library_id = $1;

-- name: PollScanControl :one
-- Single round-trip: write progress and read cancel flag. Used inside
-- the per-file boundary loop (every CancelPollEvery files).
UPDATE library_scan_state
   SET files_visited  = $2,
       files_inserted = $3,
       progress_pct   = $4,
       updated_at     = now()
 WHERE library_id     = $1
RETURNING cancel_requested, progress_pct;

-- name: IsLibraryDeleted :one
SELECT (deleted_at IS NOT NULL) AS deleted FROM libraries WHERE id = $1;
```

`make sqlc` regenerates the corresponding Go bindings.

---

## 6. API contract

### 6.1 REST

```
POST /api/libraries/{id}/scan
  Auth: Bearer JWT (admin or library-owner scope)
  Body: (none)

  202 Accepted   — scan started
  Content-Type: application/json
  {
    "status":     "started",
    "scan_id":    "<uuid>",
    "library_id": "<uuid>",
    "started_at": "<RFC3339 UTC>"
  }

  200 OK         — scan already in flight (AC #1)
  Content-Type: application/json
  {
    "status":   "already_running",
    "scan_id":  "<uuid>",
    "progress": 47.3                  // float32, percent, 0..99.0 in flight
  }

  404 Not Found  — library does not exist
  400 Bad Req    — library id not a UUID
```

```
DELETE /api/libraries/{id}/scan

  202 Accepted   — cancellation in progress (worker has not yet exited)
  {
    "status":     "cancelling",
    "library_id": "<uuid>",
    "scan_id":    "<uuid>"            // present iff the scan is in this process
  }

  200 OK         — no scan was running (idempotent no-op)
  {
    "status":     "idle",
    "library_id": "<uuid>"
  }

  404 Not Found  — library does not exist
```

```
GET /api/libraries/{id}/scan

  200 OK
  {
    "status":         "running" | "cancelling" | "idle",
    "scan_id":        "<uuid>" | null,
    "progress_pct":   72.5,
    "files_visited":  812,
    "files_inserted": 712,
    "started_at":     "<RFC3339 UTC>" | null
  }
```

### 6.2 WebSocket frames produced by this story

When the scanner observes its own cancellation (either via ctx or via
`cancel_requested`), it emits one final NOTIFY on `library.scan` so
the API translates it into a WebSocket frame for clients waiting on
`/ws/library/{id}`:

```json
{
  "type":        "scan.cancelled",
  "library_id":  "<uuid>",
  "scan_id":     "<uuid>",
  "files_visited":  812,
  "files_inserted": 712,
  "ts":          "2026-05-03T12:34:56.123Z"
}
```

The mirror frame `scan.completed` exists for normal terminations.
Both are emitted by the scanner via `pg_notify('library.scan', ...)`
inside the same connection that holds the advisory lock (so the
notification ordering is consistent with the lock release).

### 6.3 CLI

```
maktaba-scan --library <name|uuid>
             [--config /etc/maktaba/scanner.toml]
             [--dry-run]            # AC #3 — print would-be inserts, write nothing
             [--cancel]             # request cancellation and exit
             [--json]               # emit ScanResult as JSON instead of human

  Exit codes:
    0   scan completed; no per-file errors          (or --cancel succeeded)
    1   scan completed; one or more files errored
    2   library not found
    3   config or DB error before walking
    4   scan refused: another scan already in flight (--dry-run is exempt)
```

`--dry-run` and `--cancel` are mutually exclusive; passing both is a
config error (exit 3).

### 6.4 gRPC

No new gRPC surface. The Pipeline/Streaming gRPC schemas in
[architecture.md §9.9](../../architecture.md) are unchanged.

---

## 7. Test plan

### 7.1 Unit tests

| File | Target | Cases |
|------|--------|-------|
| `internal/scanlock/scanlock_test.go` | `scanlock.TryAcquire` | acquire success, second acquire on same library returns `ErrAlreadyHeld`, lock survives connection in the pool, `Close` releases, lock auto-released when conn dies (close pool). |
| `internal/store/dryrun_test.go` | `DryRunStore.SaveCandidate` | emits one JSONL line per call, JSON shape matches spec, concurrent calls produce well-formed JSONL (each line a complete object), no DB connection used. |
| `api/internal/scan/registry_test.go` | `Registry` | `Start` then `Get` round-trip, `LoadOrStore` second start fails, `Cancel` triggers ctx, `Done` removes entry. |

### 7.2 Integration tests (Postgres via testcontainers)

The three story-mandated tests live in
`test/integration/manual_control_test.go`. All three use the same
fixture helper (`genTree` + `setupDBWithLibrary` from Plan 1.1
§7.2), parametrized by file count.

**Test 1 — `TestScanIdempotentConcurrentInvocation`** (story
`test_scan_idempotent_concurrent_invocation`):

```
1. Build a 200-file tree (large enough for the scan to take >100ms).
2. Spawn goroutine A: POST /api/libraries/{id}/scan → expect 202.
3. Spawn goroutine B: POST same → expect 200 with status=already_running.
4. Wait for A to complete.
5. Assert: SELECT count(*) FROM videos WHERE library_id=$1 == 200
   (no duplicates).
6. Assert: SELECT count(*) FROM processing_jobs == 200 (idem).
```

**Test 2 — `TestScanCancellationCleansUp`** (story
`test_scan_cancellation_cleans_up`):

```
1. Build a 5,000-file tree (enough that cancellation lands mid-walk).
2. POST /api/libraries/{id}/scan → 202.
3. Wait until SELECT files_visited FROM library_scan_state >= 100.
4. DELETE /api/libraries/{id}/scan → 202.
5. Wait up to 5 s for SELECT in_progress_sweep_id IS NULL.
6. Assert: every processing_jobs row has a corresponding videos row
   (LEFT JOIN, count of orphans = 0).
7. Assert: no videos row exists without one of the documented states
   (`discovered`); none half-inserted (every row has content_hash,
   path, size_bytes, mtime).
8. Assert: library_scan_state.cancel_requested observed before reset
   (the next claim resets it; we capture the value just before).
```

**Test 3 — `TestDryRunWritesNothing`** (story
`test_dry_run_writes_nothing`):

```
1. Capture row counts of videos, processing_jobs, library_scan_state.
2. Build a 50-file tree.
3. Run scan.New(dryStore, ...).Run(ctx, libID, Options{DryRun: true})
   with dryStore = NewDryRunStore(&buf).
4. Assert: row counts unchanged from step 1.
5. Assert: buf has 50 JSONL lines.
6. Assert: each line has action="would_insert", a non-empty content_hash,
   filename, and size_bytes > 0.
```

### 7.3 Edge-case integration tests

| Test | Edge case from story | Assertion |
|------|----------------------|-----------|
| `TestCLIAndAPIScanContend` | "CLI invocation while the gRPC server is also running" | A `maktaba-scan` subprocess holds the advisory lock; a concurrent POST returns 200 `already_running`. The CLI completes; a follow-up POST starts a fresh scan. |
| `TestLibraryDeletedMidScan` | "Library deleted mid-scan" | Mid-scan, set `libraries.deleted_at = now()`. Scanner exits at the next 50-file boundary with `ErrLibraryDeleted`; no further `videos` rows are inserted; the lock is released. |
| `TestCancelResetsOnNextClaim` | (regression) | After a cancelled scan, `cancel_requested` is true. A new POST clears it via `ClaimScanSweep` and the new scan completes normally. |
| `TestDeleteIsIdempotentWhenIdle` | (regression) | DELETE with no scan in flight returns 200 `idle`; second DELETE same. |

### 7.4 Tests are tagged `// +build integration`

Run separately in CI:
`go test -tags=integration ./scanner/test/integration/manual_control_test.go`.

---

## 8. Test code scaffolding

### 8.1 `test/integration/manual_control_test.go` (skeleton)

```go
//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maktaba/api/internal/scan"
	"github.com/maktaba/scanner/internal/scan"      // aliased
	"github.com/maktaba/scanner/internal/store"
)

func TestScanIdempotentConcurrentInvocation(t *testing.T) {
	ctx := context.Background()
	pool, libID := setupDBWithLibrary(t, ctx, false)
	root := genTree(t, 200, ".mp4")
	bindRoot(ctx, t, pool, libID, root)

	srv := httptest.NewServer(newAPIHandler(t, pool))
	t.Cleanup(srv.Close)

	url := srv.URL + "/api/libraries/" + libID.String() + "/scan"

	var wg sync.WaitGroup
	results := make([]int, 2)
	bodies := make([]map[string]any, 2)
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(url, "application/json", nil)
			if err != nil {
				t.Errorf("post: %v", err)
				return
			}
			defer resp.Body.Close()
			results[i] = resp.StatusCode
			_ = json.NewDecoder(resp.Body).Decode(&bodies[i])
		}()
	}
	wg.Wait()

	// Exactly one 202, one 200.
	if (results[0] == 202 && results[1] == 200) ||
		(results[0] == 200 && results[1] == 202) {
		// pass
	} else {
		t.Fatalf("status codes = %v, want one 202 + one 200", results)
	}
	for _, b := range bodies {
		if b["status"] == "already_running" {
			if _, ok := b["progress"]; !ok {
				t.Errorf("already_running response missing progress; got %v", b)
			}
		}
	}

	// Wait for the scan to complete, then assert no duplicate rows.
	if err := waitForScanIdle(ctx, pool, libID, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM videos WHERE library_id=$1`, libID,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 200 {
		t.Fatalf("videos = %d, want 200 (no duplicates)", n)
	}
}

func TestScanCancellationCleansUp(t *testing.T) {
	ctx := context.Background()
	pool, libID := setupDBWithLibrary(t, ctx, false)
	root := genTree(t, 5000, ".mp4")
	bindRoot(ctx, t, pool, libID, root)

	srv := httptest.NewServer(newAPIHandler(t, pool))
	t.Cleanup(srv.Close)

	postResp, err := http.Post(
		srv.URL+"/api/libraries/"+libID.String()+"/scan",
		"application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	postResp.Body.Close()
	if postResp.StatusCode != 202 {
		t.Fatalf("post status = %d, want 202", postResp.StatusCode)
	}

	// Wait until at least 100 files are visited.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var visited int64
		_ = pool.QueryRow(ctx,
			`SELECT files_visited FROM library_scan_state WHERE library_id=$1`,
			libID).Scan(&visited)
		if visited >= 100 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Cancel.
	req, _ := http.NewRequest(http.MethodDelete,
		srv.URL+"/api/libraries/"+libID.String()+"/scan", nil)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != 202 {
		t.Fatalf("delete status = %d, want 202", delResp.StatusCode)
	}

	// Within 5 s the scan must be idle (story SLA).
	if err := waitForScanIdle(ctx, pool, libID, 5*time.Second); err != nil {
		t.Fatalf("scan did not exit within 5s: %v", err)
	}

	// No orphaned processing_jobs.
	var orphans int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM processing_jobs j
		LEFT JOIN videos v ON v.id = j.video_id
		WHERE v.id IS NULL`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Fatalf("orphans = %d, want 0", orphans)
	}

	// Every videos row is fully populated (no half-inserts).
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM videos
		WHERE library_id=$1
		  AND (content_hash IS NULL OR path IS NULL OR
		       size_bytes IS NULL OR mtime IS NULL)`,
		libID).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Fatalf("half-inserted = %d, want 0", orphans)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	pool, libID := setupDBWithLibrary(t, ctx, false)
	root := genTree(t, 50, ".mp4")
	bindRoot(ctx, t, pool, libID, root)

	before := tableCounts(t, ctx, pool, libID)

	var buf bytes.Buffer
	dry := store.NewDryRunStore(&buf)

	scanner := newScannerWithStore(t, pool, dry)
	scanner.SetLibrary(loadLibrary(t, ctx, pool, libID))
	if _, err := scanner.Run(ctx, libID, scan.Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}

	after := tableCounts(t, ctx, pool, libID)
	if before != after {
		t.Fatalf("table counts changed: before=%v after=%v", before, after)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 50 {
		t.Fatalf("JSONL lines = %d, want 50", len(lines))
	}
	for i, line := range lines {
		var got map[string]any
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d not valid JSON: %v", i, err)
		}
		if got["action"] != "would_insert" {
			t.Errorf("line %d action = %v, want would_insert", i, got["action"])
		}
		if h, _ := got["content_hash"].(string); h == "" {
			t.Errorf("line %d missing content_hash", i)
		}
	}
}

// --- helpers ---

func waitForScanIdle(ctx context.Context, pool *pgxpool.Pool, libID uuid.UUID, within time.Duration) error {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		var sweep *uuid.UUID
		_ = pool.QueryRow(ctx,
			`SELECT in_progress_sweep_id FROM library_scan_state WHERE library_id=$1`,
			libID).Scan(&sweep)
		if sweep == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("scan did not become idle in time")
}

type counts struct{ Videos, Jobs int64 }

func tableCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, libID uuid.UUID) counts {
	t.Helper()
	var c counts
	pool.QueryRow(ctx, `SELECT count(*) FROM videos WHERE library_id=$1`, libID).Scan(&c.Videos)
	pool.QueryRow(ctx, `
		SELECT count(*) FROM processing_jobs j JOIN videos v ON v.id=j.video_id
		 WHERE v.library_id=$1`, libID).Scan(&c.Jobs)
	return c
}
```

(`newAPIHandler`, `newScannerWithStore`, and `loadLibrary` are
straightforward helpers that wire the same pool through the registry,
handler, and scanner; they live in the same `_test.go` file and are
omitted here for brevity.)

---

## 9. Edge-case handling matrix

| Edge case | Scenario | Behavior |
|-----------|----------|----------|
| Concurrent POST | Two API replicas behind an LB receive POST simultaneously. | Both call `pg_try_advisory_lock`. Postgres serializes; one returns `acquired=true`, the other `false`. The losing replica returns `200 already_running` with the progress that the winning replica has already written to `library_scan_state`. |
| API crash during scan | The API process holding the lock dies. | Postgres releases session-scoped advisory locks automatically when the session ends; `library_scan_state.in_progress_sweep_id` is left non-null until reaped. Plan 1.5's stale-claim reaper (heartbeat-based) clears it; until then a new POST returns `already_running` with stale progress. **Acceptable for v1**; epic 06 adds a stale-claim reaper that this plan does not duplicate. |
| CLI + API contention | `maktaba-scan` (CLI) and POST run in different processes. | Same advisory-lock race as above. The CLI prints `another scan is already running for this library` to stderr and exits with code 4. |
| Library deleted mid-scan | User deletes the library while a scan walks. | Scanner checks `libraries.deleted_at IS NOT NULL` in the same poll as `cancel_requested`. On true, returns `ErrLibraryDeleted`; the goroutine releases the lock and finalizes `library_scan_state` with `last_error="library deleted"`. |
| DELETE before scan starts | DELETE arrives while the POST is still acquiring the lock. | Two cases. (a) The DELETE precedes the lock acquire: `library_scan_state.in_progress_sweep_id` is still null, the UPDATE updates 0 rows, DELETE returns `200 idle`. (b) The DELETE arrives after the registry entry is created but before the scanner's first poll: the in-process `cancel()` fires immediately; the scanner's first ctx-check returns `ctx.Err()` and the scan exits before walking. Both cases are AC-clean. |
| Scan completes before DELETE arrives | Scan finishes, releases lock, removes registry entry; DELETE arrives 100 ms later. | UPDATE matches 0 rows (in_progress_sweep_id is null); registry has no entry; DELETE returns `200 idle`. |
| Cancel-then-restart | DELETE → 202 → wait for idle → POST again. | `ClaimScanSweep` resets `cancel_requested = false` (see §4.1 amendment). New scan runs to completion. |
| Dry-run while real scan is running | `maktaba-scan --library X --dry-run` and a normal scan of X are concurrent. | Dry-run does not acquire the advisory lock and writes nothing to `library_scan_state`; output is one JSONL line per file the dry-run *would* insert. The two paths are independent. |

---

## 10. Acceptance checklist

Mapping from the story's acceptance criteria, test cases, and edge
cases to this plan.

### From Acceptance Criteria

| AC | How verified | Test |
|----|--------------|------|
| AC1 — Second concurrent POST returns 200 `{status: "already_running", progress}`; no duplicate rows | Advisory lock loses the race in the second POST handler; handler reads progress and replies 200. Per-library unique `(library_id, content_hash)` constraint guards rows. | `TestScanIdempotentConcurrentInvocation` (§8.1) |
| AC2 — DELETE stops the scanner within 5 s after next file boundary; no orphaned `processing_jobs`; library state consistent | DELETE writes `cancel_requested=true` AND in-process `cancel()`. Scanner polls every 50 files (≪5 s on a 30 TB library at 100 files/s). Per-file tx atomicity from Plan 1.1 means rollback of in-flight is automatic. | `TestScanCancellationCleansUp` (§8.1) |
| AC3 — `--dry-run` prints would-be inserts and writes nothing | `DryRunStore` (§3.4) emits JSONL to stdout; CLI bypasses advisory lock and `library_scan_state` writes. | `TestDryRunWritesNothing` (§8.1) |

### From Test cases

| Story test name | Maps to | Where |
|-----------------|---------|-------|
| `test_scan_idempotent_concurrent_invocation` | `TestScanIdempotentConcurrentInvocation` | §8.1 |
| `test_scan_cancellation_cleans_up` | `TestScanCancellationCleansUp` | §8.1 |
| `test_dry_run_writes_nothing` | `TestDryRunWritesNothing` | §8.1 |

### From Edge cases

| Edge case | How handled | Where |
|-----------|-------------|-------|
| CLI + gRPC server contention on advisory lock | `pg_try_advisory_lock(hashtext('scan:'||library_id::text)::bigint)` is the single mutex used by both POST handler and `maktaba-scan`. The losing path backs off (200 `already_running` for HTTP, exit 4 for CLI). | §3.1, §3.5, §3.6, `TestCLIAndAPIScanContend` (§7.3) |
| Library deleted mid-scan | Scanner polls `libraries.deleted_at IS NOT NULL` in the same loop as cancel_requested; returns `ErrLibraryDeleted` cleanly. | §3.3, `TestLibraryDeletedMidScan` (§7.3) |

### Done definition

- [ ] Migration `0008_scan_control.sql` applies cleanly via `goose up`
      against a fresh Postgres 16, **and** against a Postgres that
      already has `libraries.deleted_at` (idempotent `IF NOT EXISTS`).
- [ ] `go test ./scanner/internal/scanlock/... ./scanner/internal/store/... ./api/internal/scan/... -race -count=1` is green.
- [ ] `go test -tags=integration ./test/integration/manual_control_test.go` is green.
- [ ] `golangci-lint run ./scanner/... ./api/internal/scan/...` is clean.
- [ ] `maktaba-scan --library <name> --dry-run` against a real-world
      tree emits exactly one JSONL line per supported file and writes
      no rows.
- [ ] `maktaba-scan --library <name> --cancel` against a running scan
      stops it within 5 s.
- [ ] `POST /api/libraries/{id}/scan` returns 202 on first call; a
      simultaneous second call returns 200 `already_running`.
- [ ] `DELETE /api/libraries/{id}/scan` returns 202 while a scan
      runs, 200 `idle` otherwise.
- [ ] Plan committed beside the story file; story file unchanged.

---

## 11. Out of scope (deferred)

- **Pause/resume of an in-flight scan.** Scan jobs are not pause-resume
  in v1 (unlike transcribe jobs in [architecture.md §7.7](../../architecture.md));
  cancel + restart is the only control. A future story can add it by
  extending `library_scan_state` with `pause_requested` parallel to
  `cancel_requested`.
- **Stale-claim reaper for crashed scanners.** Plan 1.5 owns the
  per-library scan-state lifecycle; epic 06 (job queue) ships the
  generic stale-claim reaper used by all stages including scan.
  This plan does not duplicate it. Until both land, a crashed
  scanner leaves `in_progress_sweep_id` set; a follow-up POST returns
  `already_running` with stale progress, which is acceptable for v1.
- **WebSocket subscription multiplexing.** The `scan.cancelled` and
  `scan.completed` frames are produced (§6.2) but the
  `/ws/library/{id}` fan-out lives in the API epic (epic 07). If
  that epic has not landed when this story does, the frames are
  visible only via raw `LISTEN` clients and direct DB polling — both
  AC-equivalent.
- **Per-user authorization.** The handlers in §3.5 trust `r.Context()`
  for auth; the JWT middleware that puts the user there is the API
  epic's responsibility. Single-user mode (env-token) works
  end-to-end with this plan as written.
- **Metrics export.** OpenTelemetry counters for `scanner.cancels`,
  `scanner.already_running_responses`, `scanner.dry_runs` are
  deferred to Epic 21 (Observability).
