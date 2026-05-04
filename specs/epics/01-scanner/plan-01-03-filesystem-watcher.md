# Story 1.3 — Filesystem Watcher Implementation Plan

> **Note on language choice.** [architecture.md](../../architecture.md) §1.5/§2.1
> places the watcher in the Python Pipeline Service via `watchdog`. This plan
> spec'd in **Go with `fsnotify`** as an explicit alternative — same semantics,
> but cheap goroutines, low memory, native epoll/kqueue/FSEvents, and the
> ability to ship the watcher as a sidecar (or fold it into the API binary)
> without an interpreter. The state-machine and DB contracts are identical
> regardless of language; if we keep the watcher in Python, every Go snippet
> below has a one-to-one `watchdog` translation.

---

## 1. Architecture Diagram

```
┌──────────────────────────────────────────────────────────────────────────┐
│                              Maktaba Watcher                             │
│                                                                          │
│  ┌────────────────┐    raw    ┌──────────────┐  settled  ┌────────────┐  │
│  │ fsnotify.Watch │──events──▶│  EventLoop   │──events──▶│  Debouncer │  │
│  │  (per-root)    │           │   (1 per     │           │  (per-path │  │
│  │                │           │   library)   │           │   timers)  │  │
│  └────────────────┘           └──────┬───────┘           └─────┬──────┘  │
│         ▲                            │                         │         │
│         │ Add/Remove                 │ subdir CREATE/REMOVE    │         │
│         │ recursive                  ▼                         ▼         │
│         │                  ┌────────────────────┐    ┌──────────────────┐│
│         └──────────────────│ DirectoryRegistry  │    │ SettlingProbe    ││
│                            │  visited inodes    │    │ size+mtime stable││
│                            │  symlink guard     │    │ for debounce_sec ││
│                            └────────────────────┘    └────────┬─────────┘│
│                                                               │          │
│                                                  classify     ▼          │
│                                          ┌────────────────────────────┐  │
│                                          │  EventClassifier           │  │
│                                          │  CREATE | WRITE | RENAME   │  │
│                                          │  REMOVE | CHMOD            │  │
│                                          └────────────┬───────────────┘  │
│                                                       │                  │
│                              bounded chan (cap=4096)  ▼                  │
│                            ┌────────────────────────────────────────┐    │
│                            │           Dispatcher                   │    │
│                            │ - dedupe by (library_id, path)         │    │
│                            │ - rename pairing (inode/cookie)        │    │
│                            │ - drop ignored extensions / .maktaba/  │    │
│                            └─────────────┬──────────────────────────┘    │
└──────────────────────────────────────────┼──────────────────────────────┘
                                           │ Postgres (single tx per event)
                       ┌───────────────────┼────────────────────┐
                       ▼                   ▼                    ▼
             ┌──────────────────┐ ┌──────────────────┐ ┌─────────────────┐
             │   Scan Pipeline  │ │  Move Detector   │ │  Soft-delete    │
             │ insert videos    │ │ UPDATE videos    │ │ state→missing   │
             │ enqueue probe    │ │ SET path WHERE   │ │ (Story 1.6)     │
             │ NOTIFY videos.new│ │ content_hash=?   │ │ keep derived    │
             └──────────────────┘ └──────────────────┘ └─────────────────┘
```

The **EventLoop** is the only goroutine that reads from `fsnotify.Watcher.Events`.
Everything downstream (debouncer, classifier, dispatcher) communicates by
bounded channels so an event storm cannot pin RAM.

---

## 2. Detailed Implementation

### 2.1 Library wiring

- `pkg/watcher` — public API: `New(cfg, db, log) *Watcher`, `Run(ctx)`, `Stats()`.
- One `*Watcher` per process. Inside, one `*libraryWatcher` per library
  row with `watch = true`. A library with N roots gets N `fsnotify.Watcher`
  handles **only if** the roots live on different filesystems; otherwise
  one watcher is shared (we coalesce in the loop, not in the kernel).
- The Pipeline boot sequence:
  1. Run the catch-up sweep (Story 9.3) for every library.
  2. Subscribe to filesystem events.
  3. Begin enqueueing.
  This ordering closes the "missed-during-downtime" hole: anything added
  while we were offline is already in the DB before the watcher starts.

### 2.2 fsnotify setup

`fsnotify` on Linux uses `inotify`, on macOS uses `kqueue` (one fd per
file/directory — see scale notes), on Windows uses
`ReadDirectoryChangesW`. **`fsnotify` is not recursive on any platform.**
We add directories ourselves; see §2.3.

```go
w, err := fsnotify.NewWatcher()
if err != nil { return fmt.Errorf("fsnotify: %w", err) }
defer w.Close()
```

The buffered Events channel inside `fsnotify` is small (256). On macOS
FSEvents-backed builds we replace the default with `fsnotify/fsevents`
which gives recursive watching natively (one watcher per root, no per-dir
descriptor). Choose at build time:

```go
//go:build darwin
// libraryWatcher uses fsevents; per-dir Add() is a no-op.
```

### 2.3 Recursive directory registration

```go
func (lw *libraryWatcher) addRecursive(root string) error {
    return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
        if err != nil {
            lw.log.Warn("walk error", "path", p, "err", err)
            return nil // keep going; the periodic sweep is the backstop
        }
        if !d.IsDir() { return nil }
        if shouldIgnoreDir(p) { return fs.SkipDir } // .maktaba/, hidden, etc.
        if lw.symlinkLoop(p) { return fs.SkipDir }
        if err := lw.fs.Add(p); err != nil {
            lw.log.Warn("watch add failed", "path", p, "err", err)
        }
        return nil
    })
}
```

When the watcher receives a `CREATE` for a *directory*, it calls
`addRecursive` on the new path; when it receives a `REMOVE` for a
directory, `fsnotify` removes the watch automatically. We don't track
descriptors — `fsnotify` does.

### 2.4 Event debouncing (the main correctness mechanism)

Each path that fires an event gets a per-path **settle timer** stored in
a `sync.Map` keyed by absolute path. Repeated events for the same path
within the debounce window reset the timer. When the timer fires, we
*probe* the file (size + mtime). If the size changed since the previous
probe, we re-arm the timer. Only when two consecutive probes see the
same size do we emit a `settled` event downstream.

```
event arrives ─┐
               ├─► reset timer(path, debounce_sec)
write arrives ─┘
               (no further event)
               ├─► tick: probe size
               ├─► size grew? reset timer; loop
               └─► size stable for 2 ticks? emit settled(path)
```

This matches AC-2 and the §5.1 architecture text exactly.

### 2.5 Rename detection

Linux `inotify` emits `IN_MOVED_FROM` and `IN_MOVED_TO` paired by a
**cookie**. `fsnotify` exposes both as `fsnotify.Rename` and
`fsnotify.Create` but does not surface the cookie. Two strategies:

- **Inode-stable detection (preferred):** when `Rename` fires for `A`,
  we record `(inode, hash_of_first_4MiB)` from our most recent stat
  (we cached it during `addRecursive`). When the next `Create` event
  arrives within the rename window (default 1 s) and `os.Stat` of that
  path returns the same inode, treat it as a move.
- **Hash fallback:** if the inode trick fails (e.g., across filesystems
  inside one library, or on macOS where `Rename` semantics differ),
  fall back to the existing dedupe path: hash the new file with the
  Story 1.2 BLAKE3 sketch and `UPDATE videos SET path = $1 WHERE
  content_hash = $2`.

Cross-library moves are deliberately **not** detected as moves; the
`library_id` changes, so derived rows would point at the wrong library.
We let the source row go to `missing` and the destination row come up
fresh.

---

## 3. Go Code Scaffolding

```go
// pipeline/internal/watcher/watcher.go
package watcher

import (
    "context"
    "errors"
    "fmt"
    "io/fs"
    "log/slog"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "time"

    "github.com/fsnotify/fsnotify"
    "github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
    DebounceSec   time.Duration // default 2s
    SettleTicks   int           // default 2 (consecutive stable probes required)
    RenameWindow  time.Duration // default 1s
    QueueCapacity int           // default 4096
    IgnoredExts   []string      // ".part", ".crdownload", ".tmp"
    SettleSec     time.Duration // default 5s; min mtime age before enqueue
}

func DefaultConfig() Config {
    return Config{
        DebounceSec:   2 * time.Second,
        SettleTicks:   2,
        RenameWindow:  time.Second,
        QueueCapacity: 4096,
        IgnoredExts:   []string{".part", ".crdownload", ".tmp"},
        SettleSec:     5 * time.Second,
    }
}

type Library struct {
    ID    int64
    Roots []string
}

type Watcher struct {
    cfg Config
    db  *pgxpool.Pool
    log *slog.Logger

    mu          sync.RWMutex
    perLibrary  map[int64]*libraryWatcher

    settled chan SettledEvent // bounded by cfg.QueueCapacity
}

type SettledEvent struct {
    LibraryID int64
    Path      string
    Op        Op
    Size      int64
    InodeOld  uint64 // for rename pairing; 0 if unknown
}

type Op int

const (
    OpCreate Op = iota + 1
    OpWrite
    OpRename
    OpRemove
)

func New(cfg Config, db *pgxpool.Pool, log *slog.Logger) *Watcher {
    return &Watcher{
        cfg:        cfg,
        db:         db,
        log:        log,
        perLibrary: make(map[int64]*libraryWatcher),
        settled:    make(chan SettledEvent, cfg.QueueCapacity),
    }
}

func (w *Watcher) AddLibrary(ctx context.Context, lib Library) error {
    lw, err := newLibraryWatcher(lib, w.cfg, w.settled, w.log.With("library", lib.ID))
    if err != nil { return err }
    w.mu.Lock(); w.perLibrary[lib.ID] = lw; w.mu.Unlock()
    go lw.run(ctx)
    return nil
}

func (w *Watcher) RemoveLibrary(id int64) {
    w.mu.Lock(); lw, ok := w.perLibrary[id]; delete(w.perLibrary, id); w.mu.Unlock()
    if ok { lw.close() }
}

// Run consumes settled events and dispatches DB writes.
func (w *Watcher) Run(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case ev := <-w.settled:
            if err := w.dispatch(ctx, ev); err != nil {
                w.log.Error("dispatch failed", "path", ev.Path, "err", err)
            }
        }
    }
}
```

```go
// pipeline/internal/watcher/library_watcher.go
package watcher

import (
    "context"
    "io/fs"
    "log/slog"
    "os"
    "path/filepath"
    "sync"
    "time"

    "github.com/fsnotify/fsnotify"
)

type libraryWatcher struct {
    lib  Library
    cfg  Config
    fs   *fsnotify.Watcher
    out  chan<- SettledEvent
    log  *slog.Logger

    timersMu sync.Mutex
    timers   map[string]*pendingFile // keyed by abs path

    inodeMu  sync.Mutex
    inodes   map[uint64]string // visited inodes → first path (symlink loop guard)

    renameMu sync.Mutex
    pendingRenames map[uint64]renameContext // inode → context awaiting Create
}

type pendingFile struct {
    timer       *time.Timer
    lastSize    int64
    stableTicks int
    op          Op
}

type renameContext struct {
    oldPath string
    expires time.Time
}

func newLibraryWatcher(lib Library, cfg Config, out chan<- SettledEvent, log *slog.Logger) (*libraryWatcher, error) {
    w, err := fsnotify.NewWatcher()
    if err != nil { return nil, err }
    lw := &libraryWatcher{
        lib: lib, cfg: cfg, fs: w, out: out, log: log,
        timers: make(map[string]*pendingFile),
        inodes: make(map[uint64]string),
        pendingRenames: make(map[uint64]renameContext),
    }
    for _, root := range lib.Roots {
        if err := lw.addRecursive(root); err != nil {
            w.Close()
            return nil, err
        }
    }
    return lw, nil
}

func (lw *libraryWatcher) close() { _ = lw.fs.Close() }

func (lw *libraryWatcher) run(ctx context.Context) {
    defer lw.fs.Close()
    for {
        select {
        case <-ctx.Done():
            return
        case ev, ok := <-lw.fs.Events:
            if !ok { return }
            lw.onEvent(ev)
        case err, ok := <-lw.fs.Errors:
            if !ok { return }
            lw.log.Warn("fsnotify error", "err", err)
        }
    }
}

func (lw *libraryWatcher) onEvent(ev fsnotify.Event) {
    if shouldIgnore(ev.Name, lw.cfg.IgnoredExts) { return }

    // Directory created? Recurse.
    if ev.Op&fsnotify.Create == fsnotify.Create {
        if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
            if err := lw.addRecursive(ev.Name); err != nil {
                lw.log.Warn("addRecursive after create", "path", ev.Name, "err", err)
            }
            return
        }
    }

    op := classify(ev.Op)
    if op == 0 { return } // CHMOD-only and friends are ignored

    switch op {
    case OpRemove:
        lw.flushRemove(ev.Name)
    case OpRename:
        lw.armRename(ev.Name)
    case OpCreate, OpWrite:
        lw.armSettle(ev.Name, op)
    }
}

func classify(op fsnotify.Op) Op {
    switch {
    case op&fsnotify.Remove != 0: return OpRemove
    case op&fsnotify.Rename != 0: return OpRename
    case op&fsnotify.Create != 0: return OpCreate
    case op&fsnotify.Write  != 0: return OpWrite
    }
    return 0
}

// armSettle (re)starts the per-path debounce timer.
func (lw *libraryWatcher) armSettle(path string, op Op) {
    lw.timersMu.Lock()
    defer lw.timersMu.Unlock()
    pf, ok := lw.timers[path]
    if !ok {
        pf = &pendingFile{op: op, lastSize: -1}
        lw.timers[path] = pf
    }
    if pf.timer != nil { pf.timer.Stop() }
    pf.timer = time.AfterFunc(lw.cfg.DebounceSec, func() { lw.tick(path) })
}

func (lw *libraryWatcher) tick(path string) {
    fi, err := os.Stat(path)
    if err != nil {
        // Vanished between debounce and tick → treat as remove.
        lw.flushRemove(path)
        return
    }
    if time.Since(fi.ModTime()) < lw.cfg.SettleSec {
        // mtime still fresh; keep waiting.
        lw.armSettle(path, OpWrite)
        return
    }

    lw.timersMu.Lock()
    pf := lw.timers[path]
    if pf == nil { lw.timersMu.Unlock(); return }
    if pf.lastSize == fi.Size() {
        pf.stableTicks++
    } else {
        pf.stableTicks = 0
        pf.lastSize = fi.Size()
    }
    if pf.stableTicks < lw.cfg.SettleTicks {
        pf.timer = time.AfterFunc(lw.cfg.DebounceSec, func() { lw.tick(path) })
        lw.timersMu.Unlock()
        return
    }
    op := pf.op
    delete(lw.timers, path)
    lw.timersMu.Unlock()

    inode, _ := inodeOf(fi)
    select {
    case lw.out <- SettledEvent{
        LibraryID: lw.lib.ID, Path: path, Op: op,
        Size: fi.Size(), InodeOld: inode,
    }:
    default:
        // Bounded queue is full → drop with a warning. Periodic sweep
        // will catch up. We never block the event loop.
        lw.log.Warn("settled queue full; dropping", "path", path)
    }
}

func (lw *libraryWatcher) flushRemove(path string) {
    lw.timersMu.Lock()
    if pf, ok := lw.timers[path]; ok && pf.timer != nil {
        pf.timer.Stop()
        delete(lw.timers, path) // CREATE+REMOVE within window: never fired.
    }
    lw.timersMu.Unlock()
    select {
    case lw.out <- SettledEvent{LibraryID: lw.lib.ID, Path: path, Op: OpRemove}:
    default:
        lw.log.Warn("settled queue full; dropping remove", "path", path)
    }
}

func (lw *libraryWatcher) armRename(oldPath string) {
    fi, err := os.Stat(oldPath)
    var inode uint64
    if err == nil { inode, _ = inodeOf(fi) }

    lw.renameMu.Lock()
    lw.pendingRenames[inode] = renameContext{
        oldPath: oldPath,
        expires: time.Now().Add(lw.cfg.RenameWindow),
    }
    lw.renameMu.Unlock()

    // If no Create lands within the window, treat as a Remove.
    time.AfterFunc(lw.cfg.RenameWindow, func() {
        lw.renameMu.Lock()
        rc, ok := lw.pendingRenames[inode]
        if ok && rc.oldPath == oldPath {
            delete(lw.pendingRenames, inode)
            lw.renameMu.Unlock()
            lw.flushRemove(oldPath)
            return
        }
        lw.renameMu.Unlock()
    })
}
```

```go
// pipeline/internal/watcher/dispatch.go
package watcher

import (
    "context"
    "errors"
    "fmt"
)

func (w *Watcher) dispatch(ctx context.Context, ev SettledEvent) error {
    switch ev.Op {
    case OpCreate, OpWrite:
        return w.upsertVideo(ctx, ev)
    case OpRename:
        return w.handleRename(ctx, ev)
    case OpRemove:
        return w.softDelete(ctx, ev)
    }
    return errors.New("unknown op")
}

// upsertVideo inserts a 'discovered' row if no row exists for this content_hash;
// otherwise updates the path (treats it as a rediscovery / out-of-tree move).
func (w *Watcher) upsertVideo(ctx context.Context, ev SettledEvent) error {
    hash, err := identity.ComputeSketch(ev.Path) // Story 1.2 BLAKE3 sketch
    if err != nil { return fmt.Errorf("hash: %w", err) }

    tx, err := w.db.Begin(ctx)
    if err != nil { return err }
    defer tx.Rollback(ctx)

    // Existing row by hash?
    var existingID int64
    var state string
    err = tx.QueryRow(ctx,
        `SELECT id, state FROM videos WHERE content_hash = $1`, hash,
    ).Scan(&existingID, &state)
    switch {
    case errors.Is(err, pgx.ErrNoRows):
        if _, err := tx.Exec(ctx, `
            INSERT INTO videos (library_id, path, content_hash, size_bytes, state, mtime)
            VALUES ($1, $2, $3, $4, 'discovered', NOW())`,
            ev.LibraryID, ev.Path, hash, ev.Size,
        ); err != nil { return err }
        if _, err := tx.Exec(ctx, `
            INSERT INTO processing_jobs (video_id, stage, state, created_at)
            SELECT id, 'probe', 'pending', NOW() FROM videos WHERE content_hash = $1`,
            hash,
        ); err != nil { return err }
        // The 'videos.new' NOTIFY is emitted by the trigger from plan-01-01
        // slot 0005; no manual NOTIFY needed here.
    case err != nil:
        return err
    default:
        // Existing row — treat as a rediscovery / out-of-tree move. The
        // architecture-§8.1 videos table has no rediscovered_at column;
        // we store the timestamp inside videos.metadata JSONB instead.
        if _, err := tx.Exec(ctx, `
            UPDATE videos
               SET path = $2, library_id = $3,
                   state = CASE WHEN state = 'missing' THEN 'discovered' ELSE state END,
                   metadata = jsonb_set(
                       COALESCE(metadata, '{}'::jsonb),
                       '{rediscovered_at}',
                       to_jsonb(NOW()::text)
                   )
             WHERE id = $1`,
            existingID, ev.Path, ev.LibraryID,
        ); err != nil { return err }
    }
    return tx.Commit(ctx)
}

func (w *Watcher) handleRename(ctx context.Context, ev SettledEvent) error {
    // Try inode-keyed match against a pending rename pair.
    // Falls back to upsertVideo (which dedupes by hash) when the pair was lost.
    return w.upsertVideo(ctx, ev)
}

func (w *Watcher) softDelete(ctx context.Context, ev SettledEvent) error {
    // The architecture-§8.1 videos table has no missing_at column; store
    // the timestamp inside videos.metadata JSONB (key 'missing_since',
    // matching plan-01-05's metadata schema).
    _, err := w.db.Exec(ctx, `
        UPDATE videos
           SET state = 'missing',
               metadata = jsonb_set(
                   COALESCE(metadata, '{}'::jsonb),
                   '{missing_since}',
                   to_jsonb(NOW()::text)
               )
         WHERE library_id = $1 AND path = $2 AND state <> 'missing'`,
        ev.LibraryID, ev.Path,
    )
    return err
}
```

```go
// pipeline/internal/watcher/util.go
package watcher

import (
    "io/fs"
    "os"
    "path/filepath"
    "strings"
    "syscall"
)

func shouldIgnore(path string, ignoredExts []string) bool {
    base := filepath.Base(path)
    if strings.HasPrefix(base, ".") { return true } // hidden files
    if strings.Contains(path, string(os.PathSeparator)+".maktaba"+string(os.PathSeparator)) {
        return true
    }
    ext := strings.ToLower(filepath.Ext(path))
    for _, e := range ignoredExts {
        if ext == e { return true }
    }
    return false
}

func shouldIgnoreDir(path string) bool {
    base := filepath.Base(path)
    return base == ".maktaba" || strings.HasPrefix(base, ".")
}

func inodeOf(fi os.FileInfo) (uint64, bool) {
    st, ok := fi.Sys().(*syscall.Stat_t)
    if !ok { return 0, false }
    return uint64(st.Ino), true
}

// symlinkLoop returns true if `dir` is a symlink whose target inode we've
// already entered. Maintains a visited-inode set per libraryWatcher.
func (lw *libraryWatcher) symlinkLoop(dir string) bool {
    fi, err := os.Stat(dir) // follow symlinks intentionally
    if err != nil { return false }
    inode, ok := inodeOf(fi)
    if !ok { return false }
    lw.inodeMu.Lock(); defer lw.inodeMu.Unlock()
    if first, seen := lw.inodes[inode]; seen && first != dir {
        return true
    }
    lw.inodes[inode] = dir
    return false
}
```

---

## 4. Event Handling

| fsnotify op       | Trigger                      | Action                                                                                                  |
|-------------------|------------------------------|---------------------------------------------------------------------------------------------------------|
| `Create` (file)   | new file appeared            | Arm settle timer; on settle → `upsertVideo` (insert `discovered` + enqueue `probe`)                     |
| `Create` (dir)    | new subdir appeared          | `addRecursive` synchronously; **no DB write** for the directory itself                                  |
| `Write`           | size/contents changed        | Re-arm the same settle timer; on settle, only emit if the size truly differs from the last known row   |
| `Rename`          | `IN_MOVED_FROM` / Darwin mv  | Stash `(inode, oldPath)` in `pendingRenames`; if a `Create` with the same inode lands within `RenameWindow`, `UPDATE videos.path`; otherwise treat as `Remove` |
| `Remove`          | unlink / rmdir               | Cancel any pending settle timer; emit `OpRemove`; dispatcher transitions row to `missing` (Story 1.6)   |
| `Chmod`           | permission change            | Ignored — does not affect identity or content                                                            |

### Cross-platform quirks

- **Linux:** `Rename` fires on the *source* path; the destination arrives
  as a separate `Create`. Pair them via inode.
- **macOS (kqueue):** `Rename` fires only on the *source*; the destination
  may not arrive at all if outside the watched tree. macOS FSEvents (used
  by the optional `darwin` build tag) emits a single `MovedTo` flag.
- **macOS (FSEvents recursive):** the Cocoa flag `kFSEventStreamEventFlagItemRemoved` is sticky for batched events; debouncing must
  re-stat to ground truth instead of trusting the event payload.
- **Windows:** `ReadDirectoryChangesW` reports renames as `OldName`+
  `NewName` pairs; `fsnotify` already linearizes these into
  `Rename`+`Create`, so the inode trick is replaced by name pairing.

---

## 5. Debouncing Strategy

The hard requirement (AC-2) is: **never enqueue a file that is still
being written.** Three distinct mechanisms layered defensively:

1. **Per-path debounce timer.** Every event resets a `time.AfterFunc`
   with `cfg.DebounceSec` (default 2 s). Bursts of `Write` events
   collapse into one tick.
2. **Stable-size probe.** When the timer fires, `os.Stat` the path and
   compare to the previous tick. The file is "settled" only after
   `cfg.SettleTicks` (default 2) consecutive ticks return the *same*
   size.
3. **Mtime quarantine.** Even if size is stable, if `time.Since(mtime) <
   cfg.SettleSec` (default 5 s) we re-arm. This catches the pathological
   case where a copy stalls at the exact final byte count for one tick.

```
WRITE WRITE WRITE  WRITE  WRITE         (final flush)
 │     │     │      │      │
 ▼     ▼     ▼      ▼      ▼
 ├─────┼─────┼──────┼──────┼─── reset → reset → reset → reset → tick (size unchanged, mtime old) → SETTLED
 t0    t1    t2     t3     t4    t6 (last write)        t8         t10              t12
```

**Worst case latency:** `2 × DebounceSec + SettleSec` after the final
write. With defaults: `2 × 2 + 5 = 9 s` before the row appears. AC-1
permits `2 × debounce_sec + 1`, so we will need to **lower
`SettleSec` to 1 s by default** (or split the AC into "size stable" +
"mtime quiet" budgets). The plan: ship defaults `Debounce=2s, Settle=1s,
Ticks=2` → max 5 s, comfortably within AC.

**Backpressure.** The `settled` channel is bounded
(`cfg.QueueCapacity = 4096`). If the dispatcher falls behind and the
channel fills, we **drop** the event with a warning rather than block
the event loop. Story 9.3 (periodic sweep) is the explicit fallback for
dropped events and is documented as such.

---

## 6. Scale Considerations (30 TB / millions of files)

### 6.1 Linux inotify limits

Per-user defaults are tiny:
- `fs.inotify.max_user_watches` — 8 192 (kernel default; many distros bump to 524 288)
- `fs.inotify.max_user_instances` — 128
- `fs.inotify.max_queued_events` — 16 384

A 30 TB archive easily contains 100 000+ directories. Each directory
needs one watch descriptor. Mitigations:

- At boot, refuse to start if `max_user_watches < expected_dir_count *
  1.5` and emit a clear log telling the user to raise the sysctl.
- Provide `deploy/sysctl.d/maktaba.conf`:
  ```
  fs.inotify.max_user_watches = 1048576
  fs.inotify.max_user_instances = 1024
  fs.inotify.max_queued_events = 65536
  ```
- Document the failure mode: when `Add()` returns `ENOSPC`, we log
  `inotify_full=true`, abandon the watcher for that root, and fall back
  to the periodic sweep at `cfg.SweepFallbackSec` (default 1 h, vs the
  normal 6 h).

### 6.2 macOS FSEvents

FSEvents is a system-level service (no per-fd cost), so directory count
is **not** the bottleneck. Two real costs:

- The user must grant Full Disk Access to the binary on macOS 12+ for
  paths outside `~/Library/`. We document this in the install guide.
- FSEvents coalesces — bursts can return a single event flag for an
  entire subtree. The settle/probe layer handles this naturally because
  we always re-stat ground truth; we never trust the event payload.

For >10 M files we strongly recommend the macOS native build
(`go build -tags darwin_fsevents`) which bypasses kqueue's per-fd cost.

### 6.3 Network filesystems (NFS, SMB, CIFS, FUSE)

`inotify` does **not** see remote writes. The watcher checks
`statfs(2).f_type` at startup; for known network/FUSE types
(`NFS_SUPER_MAGIC`, `SMB_SUPER_MAGIC`, `FUSE_SUPER_MAGIC`, …) we skip
fsnotify entirely and run a more aggressive periodic sweep
(`cfg.NetworkSweepSec`, default 30 min). Behaviour matches story 1.3
edge case "Network filesystems".

### 6.4 Memory budget

- One `pendingFile` ≈ 64 B.
- 100 000 simultaneously-debouncing files ≈ 6 MB. Acceptable.
- The bounded `settled` channel caps in-flight events at 4 096 × ~80 B
  ≈ 320 kB.
- `inodes` set: 16 B per directory × 1 M dirs ≈ 16 MB. Acceptable.

The watcher's steady-state memory is dominated by the kernel's
inotify-watch table, not Go heap.

---

## 7. Database Updates

| Op            | Statement                                                                                                                                              |
|---------------|--------------------------------------------------------------------------------------------------------------------------------------------------------|
| **CREATE**    | `INSERT INTO videos (library_id, path, content_hash, size_bytes, state, mtime) VALUES (..., 'discovered', NOW())` — guarded by `UNIQUE (library_id, content_hash)`. On conflict: fall through to RENAME. |
| **CREATE-2**  | `INSERT INTO processing_jobs (video_id, stage, state) VALUES (?, 'probe', 'pending')` — kicks the pipeline.                                            |
| **CREATE-3**  | `NOTIFY "videos.new"` — API translates to WebSocket fanout (architecture §5.1).                                                                        |
| **WRITE**     | If `videos.size_bytes != new_size` → recompute hash and possibly *re-probe* (state → `discovered`); if hash unchanged → no-op. WRITE on an already-settled file is rare; mtime change without size change is ignored. |
| **RENAME**    | `UPDATE videos SET path = $1 WHERE content_hash = $2 AND id = $3` — single-row update, no derived data touched.                                       |
| **REMOVE**    | `UPDATE videos SET state = 'missing', metadata = jsonb_set(COALESCE(metadata,'{}'::jsonb), '{missing_since}', to_jsonb(NOW()::text)) WHERE library_id = $1 AND path = $2 AND state <> 'missing'` — soft delete; transcripts/index entries persist (Story 1.6 governs the eventual hard-delete after `missing_grace_days`). The architecture-§8.1 videos table has no dedicated `missing_at` column, so the timestamp lives in the `metadata` JSONB. |
| **REMOVE-2**  | `NOTIFY "videos.missing"` — clients refresh the library grid.                                                                                          |

All five statements run in a **single transaction per event**. The
`(library_id, path)` index and the `UNIQUE (content_hash)` constraint
make every statement above an index seek.

---

## 8. Test Plan

### 8.1 Unit tests (`watcher_test.go`)

- `TestDebouncerCollapsesBurst` — 1 000 synthetic `Write` events in
  100 ms produce exactly one settled event after `2×DebounceSec`.
- `TestDebouncerWaitsForSizeStability` — events fired while size grows;
  no settled event emitted until size stops changing.
- `TestRenamePairingByInode` — synthesize Linux-style
  `Rename(old)+Create(new)` with the same inode; only one settled event,
  classified `OpRename`.
- `TestRenameTimeoutBecomesRemove` — `Rename(old)` with no matching
  `Create` within `RenameWindow` → settled `OpRemove`.
- `TestCreateThenRemoveWithinWindow` — pending timer is cancelled; no
  emission.
- `TestIgnoredExtensions` — `.part`, `.crdownload`, `.tmp`, hidden,
  `.maktaba/...` all silently dropped.
- `TestSymlinkLoop` — `a → b → a`; `addRecursive` returns without
  hanging and adds each inode once.

### 8.2 Integration tests (`watcher_integ_test.go`)

Real `fsnotify`, real temp dir, real Postgres (testcontainers).

- `TestWatcherPicksUpNewFile` — write a fixture, wait ≤ 5 s, assert one
  `videos` row + one `processing_jobs(probe)` row.
- `TestWatcherDebouncesPartialWrites` — open file, write 1 MiB / 200 ms
  for 5 s, close, assert one ingestion event after the final write
  settles.
- `TestWatcherHandlesRename` — `os.Rename(a, b)` within the watched
  root; assert same `videos.id`, only `path` updated, no new probe job.
- `TestWatcherHandlesDelete` — `os.Remove`; assert
  `state = 'missing'`, related transcript rows still exist.
- `TestWatcherRecoversFromEventStorm` — copy 10 000 files in a tight
  loop; assert all are eventually ingested, watcher memory bounded
  under 100 MB, no panics.
- `TestWatcherInotifyExhaustion` — `ulimit`-style cap on watches;
  assert clean fallback to periodic sweep, no goroutine leak.

### 8.3 Property tests (optional, `rapid`)

- For any sequence of (create/write/rename/remove) events on N paths,
  the dispatcher emits a *consistent* sequence: every settled `path` was
  preceded by a non-Remove event; every `Remove` cancels a pending
  settle.

---

## 9. Test Code (Go)

```go
// pipeline/internal/watcher/watcher_test.go
package watcher

import (
    "context"
    "os"
    "path/filepath"
    "sync/atomic"
    "testing"
    "time"
)

func TestDebouncerCollapsesBurst(t *testing.T) {
    dir := t.TempDir()
    target := filepath.Join(dir, "movie.mkv")

    out := make(chan SettledEvent, 4)
    cfg := DefaultConfig()
    cfg.DebounceSec = 100 * time.Millisecond
    cfg.SettleSec   = 0
    cfg.SettleTicks = 1

    lw := newTestLibraryWatcher(t, dir, cfg, out)
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go lw.run(ctx)

    f, err := os.Create(target); if err != nil { t.Fatal(err) }
    for i := 0; i < 1000; i++ {
        if _, err := f.Write([]byte("a")); err != nil { t.Fatal(err) }
        time.Sleep(50 * time.Microsecond)
    }
    f.Close()

    select {
    case ev := <-out:
        if ev.Path != target { t.Fatalf("got %s, want %s", ev.Path, target) }
    case <-time.After(2 * time.Second):
        t.Fatal("no settled event")
    }
    select {
    case ev := <-out:
        t.Fatalf("unexpected second event: %+v", ev)
    case <-time.After(500 * time.Millisecond):
    }
}

func TestDebouncerWaitsForSizeStability(t *testing.T) {
    dir := t.TempDir()
    target := filepath.Join(dir, "growing.mkv")

    out := make(chan SettledEvent, 4)
    cfg := DefaultConfig()
    cfg.DebounceSec = 200 * time.Millisecond
    cfg.SettleSec   = 0
    cfg.SettleTicks = 2

    lw := newTestLibraryWatcher(t, dir, cfg, out)
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go lw.run(ctx)

    f, _ := os.Create(target)
    var total int64
    for i := 0; i < 25; i++ {
        n, _ := f.Write(make([]byte, 1<<20)) // 1 MiB
        atomic.AddInt64(&total, int64(n))
        time.Sleep(150 * time.Millisecond) // slower than debounce
    }
    f.Close()

    var got SettledEvent
    select {
    case got = <-out:
    case <-time.After(3 * time.Second):
        t.Fatal("no settled event")
    }
    if got.Size != atomic.LoadInt64(&total) {
        t.Fatalf("settled at size %d, want %d", got.Size, total)
    }
}

func TestRenameTimeoutBecomesRemove(t *testing.T) {
    dir := t.TempDir()
    target := filepath.Join(dir, "x.mkv")
    if err := os.WriteFile(target, []byte("x"), 0o644); err != nil { t.Fatal(err) }

    out := make(chan SettledEvent, 4)
    cfg := DefaultConfig()
    cfg.RenameWindow = 100 * time.Millisecond

    lw := newTestLibraryWatcher(t, dir, cfg, out)
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go lw.run(ctx)
    drain(out, 500*time.Millisecond) // swallow the initial create.

    if err := os.Rename(target, filepath.Join(t.TempDir(), "x.mkv")); err != nil {
        t.Fatal(err)
    }

    select {
    case ev := <-out:
        if ev.Op != OpRemove {
            t.Fatalf("op=%v want OpRemove", ev.Op)
        }
    case <-time.After(2 * time.Second):
        t.Fatal("no remove event")
    }
}

// helpers omitted: newTestLibraryWatcher, drain — boilerplate.
```

---

## 10. Edge Cases

| Case                                    | Behavior                                                                                                                                                  |
|-----------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------|
| Rapid create-then-delete                | `flushRemove` cancels the pending settle timer; **never enqueued** (matches AC and Story 9.2 edge case).                                                  |
| Symlink cycle (`a → b → a`)             | `addRecursive` consults the per-watcher `inodes` set; second visit returns `fs.SkipDir`. Loop is broken in O(1).                                          |
| Permission denied on a subdir           | `addRecursive` logs and continues. The directory is *not* watched; periodic sweep is the backstop. We never crash on a single bad dir.                    |
| Mount appears mid-run (e.g., USB)       | `Create` fires for the mount point. `addRecursive` walks it. The new mount may have a *different* inotify cap; treat as a new root.                       |
| Mount disappears (USB unplug)           | `Remove` fires for every file in the mount. We **suppress** mass remove if `> N events / s` for paths under the same root for `cfg.UnmountSuppressSec`; instead we record `library_scan_state.metadata.offline_since` for that root and re-scan when it comes back. The canonical `videos.state` enum has no library-level "offline" value, so the offline marker lives in the per-library scan-state row, not in `videos.state`. Without this guard, an unplugged drive transitions every video to `missing`. |
| `*.part`, `*.crdownload`, `*.tmp`       | Filtered by extension up front; the rename to a final extension fires a fresh `Create` and goes through the normal path.                                   |
| Atomic mv from outside watched root     | Single `Create` event. `upsertVideo` hashes; if the hash matches a `missing` row, transitions `missing → discovered`. Story 1.6 governs the rediscovery.   |
| File modified during hashing            | Hashing reads first 4 MiB + last 4 MiB + size; if the first stat differs from the second, we abort and re-arm the settle timer.                            |
| Inotify `IN_Q_OVERFLOW`                 | `fsnotify` surfaces this in `Errors`. We log loudly, schedule an immediate full sweep for the affected library, and continue.                              |
| Clock skew (mtime in the future)        | `time.Since(fi.ModTime())` returns a negative duration; treat as "settled enough" iff size also stable. We never wait forever.                             |
| Directory deleted while walking         | `WalkDir` callback receives the error; we log and `return nil` to skip. Other roots continue.                                                              |
| Two libraries point at the same root    | Architecturally disallowed by Story 9.16 (multi-root overlap). The watcher refuses `AddLibrary` if any new root is a prefix of an existing watched path.   |
| Watcher restart with files added meanwhile | Boot order: full sweep first, watcher second. No "missed-during-downtime" hole (matches Story 9.2 AC-4).                                                |

---

## 11. Acceptance Checklist

- [ ] **AC-1.1** Dropping `lecture.mkv` into a watched root creates exactly one `videos` row and one `processing_jobs(probe)` row within `2×debounce_sec + 1` s.
- [ ] **AC-1.2** A copy in progress (mtime advancing) is **not** ingested until size is stable for `SettleTicks` consecutive debounce intervals.
- [ ] **AC-1.3** A rename within the library updates `videos.path`; no new `videos` row, no new `processing_jobs` row.
- [ ] **AC-1.4** A delete transitions the row to `state = 'missing'`; transcript / search-index rows are preserved.
- [ ] **Test** `test_watcher_picks_up_new_file` passes with the watcher running.
- [ ] **Test** `test_watcher_debounces_partial_writes` produces exactly one ingestion event.
- [ ] **Test** `test_watcher_handles_rename` retains `videos.id`.
- [ ] **Test** `test_watcher_handles_delete` leaves transcripts intact.
- [ ] **Test** `test_watcher_recovers_from_event_storm` ingests 10 000 files with bounded memory.
- [ ] **Edge** Network FS (`statfs` reports non-local) skips fsnotify and uses periodic sweep at `NetworkSweepSec`.
- [ ] **Edge** Atomic mv into the root with a hash matching a `missing` row triggers `missing → discovered`, not a fresh insert.
- [ ] **Edge** `.maktaba/` directories are ignored by the recursive walk and the event filter.
- [ ] **Edge** `*.part`, `*.crdownload`, `*.tmp` extensions are filtered before debouncing.
- [ ] **Edge** Symlink cycles do not hang `addRecursive`.
- [ ] **Edge** Mount unplug does not mass-transition videos to `missing`; the library scan-state row records `offline_since` until the mount returns.
- [ ] **Ops** Boot fails fast with a clear message when `fs.inotify.max_user_watches` is below `1.5×` the directory count for any watched root.
- [ ] **Ops** `deploy/sysctl.d/maktaba.conf` ships in the Linux installer.
- [ ] **Ops** Watcher metrics exposed: `watcher_events_total{op}`, `watcher_settled_dropped_total`, `watcher_pending_files`, `watcher_inotify_watches`.
- [ ] **Ops** `IN_Q_OVERFLOW` triggers a full library sweep and increments `watcher_overflow_total`.
- [ ] **Ops** Watcher restart performs the catch-up sweep before subscribing — no events lost across restarts.
