# Implementation Plan — Story 9.16 Multi-root and Overlap Detection

> Companion to [story-09-16-multi-root-overlap.md](story-09-16-multi-root-overlap.md).
> The story states *what* and *why*; this plan states *how*.
> Builds on Story 9.3 (sweep — runs the AC-4 runtime overlap check)
> and Story 9.17 (audit rows on runtime overlap).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Roots schema | `library_roots (id UUID PRIMARY KEY, library_id UUID NOT NULL, path TEXT NOT NULL, canonical_path TEXT NOT NULL, added_at TIMESTAMPTZ)`. The `canonical_path` is the realpath at insert time; queries against it use prefix matching. |
| Canonicalization | Go: `filepath.EvalSymlinks` + `filepath.Clean` + trailing-slash strip. Python: `os.path.realpath` + `Path.resolve()` for cross-tool agreement. The fixture parity (`shared/db/test_fixtures/path_canonicalize/`) keeps them aligned. |
| Overlap rule | Two paths overlap iff `relpath(a, b)` doesn't traverse upward (no leading `..`), or `relpath(b, a)` similarly — i.e., one is a prefix of the other after canonicalization. The store enforces this on insert via a serializable check. |
| Same-library nesting | AC-3 says even within one library, nested roots are forbidden. The check covers both inter- and intra-library cases. |
| Runtime overlap | Sweep (Story 9.3) calls `CheckRuntimeOverlap()` once per run; on detection, emits a WARN log and writes an `audit_log` row with `event='roots-runtime-overlap'`. The sweep continues; ops decide. |
| Out of scope | The HTTP routes (Epic 7 Story 7.3); the recovery flow when overlap is detected (manual). |

## 1. Architecture diagram

```
   POST /api/libraries  body.roots = ["/mnt/a", "/mnt/b/sub"]
        ↓
   handlers/libraries/create.go
        ↓
   libraries.AddRoots(ctx, library_id, raw_paths) :
      BEGIN TX (SERIALIZABLE)
        canonicals = [path_safety.Canonicalize(p) for p in raw_paths]
        # 1. self-overlap within the supplied set
        if any pair (a,b) overlaps: 422 library-roots-overlap
        # 2. overlap with existing roots in any library
        for c in canonicals:
            existing = SELECT library_id, canonical_path FROM library_roots
                        WHERE c LIKE canonical_path || '/%'
                           OR canonical_path LIKE c || '/%'
                           OR canonical_path = c
            if existing: 422 library-roots-overlap
                          payload: { conflicts_with_library_id, path }
        # 3. insert
        INSERT INTO library_roots (id, library_id, path, canonical_path)
        SELECT gen_random_uuid(), $library_id, raw_paths[i], canonicals[i]
        FROM unnest(...)
      COMMIT

   sweep tick (Story 9.3):
        ↓ once per sweep, call CheckRuntimeOverlap()
   for r in library_roots:
       cur = realpath(r.path)
       if cur != r.canonical_path:
           # mount layout changed
           if cur overlaps any other library_roots.canonical_path:
               log warn library-roots-runtime-overlap r.id
               audit('library', 'roots-runtime-overlap', payload={
                     library_id, root_id, declared=r.path,
                     resolved_now=cur, conflicts_with=other_root_id})
       # whether or not overlap, do NOT auto-update canonical_path:
       # operator must fix the mount and trigger a re-canonicalize.
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/libraries/roots.go` | `AddRoots`, `RemoveRoot`, `CheckRuntimeOverlap`, `Canonicalize` (Go). |
| `api/internal/libraries/roots_test.go` | Unit tests per §6. |
| `pipeline/src/maktaba_pipeline/libraries/roots.py` | Python `canonicalize`, `check_runtime_overlap` for the sweep. |
| `shared/db/migrations/0044_library_roots.sql` | The table + indexes. |
| `shared/db/queries/library_roots.sql` | sqlc input. |
| `shared/db/test_fixtures/path_canonicalize/cases.json` | Cross-language parity fixtures. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/handlers/libraries/create.go` | Calls `AddRoots` inside the create tx. |
| `api/internal/handlers/libraries/update.go` | Calls `AddRoots`/`RemoveRoot` for `PATCH .roots = […]`. |
| `pipeline/src/maktaba_pipeline/sweep/sweep_runner.py` | After enqueue accept and before walk, calls `check_runtime_overlap(library_id)`. |
| `specs/epics/09-library-management/README.md` | Tick story 9.16. |

### 2.3 Type definitions

```go
// api/internal/libraries/roots.go
package libraries

type Root struct {
    ID            uuid.UUID
    LibraryID     uuid.UUID
    Path          string
    CanonicalPath string
    AddedAt       time.Time
}

type OverlapError struct {
    Code             string    // "library-roots-overlap"
    Path             string
    ConflictsWith    uuid.UUID // root_id
    ConflictsLibrary uuid.UUID
}

func (e *OverlapError) Error() string { return e.Code }
```

## 3. Database migration

`shared/db/migrations/0044_library_roots.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

CREATE TABLE library_roots (
    id              UUID PRIMARY KEY,
    library_id      UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    path            TEXT NOT NULL,         -- as supplied (display)
    canonical_path  TEXT NOT NULL,         -- realpath()'d
    added_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A library has at least one root, but uniqueness is on canonical_path
-- across the whole table — that's the durable overlap guard for the
-- exact-equal case. Prefix overlap is enforced in app code (see §4).
CREATE UNIQUE INDEX library_roots_canonical_unique
    ON library_roots (canonical_path);

-- Lookup by library_id (fast):
CREATE INDEX library_roots_by_library
    ON library_roots (library_id);

-- Prefix-search shape: btree LIKE works with text_pattern_ops on the
-- canonical_path. We use it for the overlap probe.
CREATE INDEX library_roots_canonical_prefix
    ON library_roots (canonical_path text_pattern_ops);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS library_roots;
-- +goose StatementEnd
```

`shared/db/queries/library_roots.sql`:

```sql
-- name: ListRootsForLibrary :many
SELECT id, library_id, path, canonical_path, added_at
  FROM library_roots WHERE library_id = $1
  ORDER BY added_at;

-- name: ListRootsAll :many
SELECT id, library_id, path, canonical_path, added_at
  FROM library_roots ORDER BY canonical_path;

-- name: FindOverlappingRoots :many
-- $1 = candidate canonical path
SELECT id, library_id, canonical_path
  FROM library_roots
 WHERE canonical_path = $1
    OR canonical_path LIKE $1 || '/%'
    OR $1 LIKE canonical_path || '/%';

-- name: InsertRoot :one
INSERT INTO library_roots (id, library_id, path, canonical_path)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: DeleteRoot :exec
DELETE FROM library_roots WHERE id = $1;
```

## 4. Code scaffolding

### 4.1 Canonicalization

```go
// api/internal/libraries/roots.go
import (
    "errors"
    "os"
    "path/filepath"
    "strings"
)

// Canonicalize: realpath + clean + strip trailing slash. Returns the
// absolute, symlink-free path. Errors if the path doesn't exist.
func Canonicalize(p string) (string, error) {
    if p == "" {
        return "", &ValidationError{Code: "root-empty"}
    }
    abs, err := filepath.Abs(p)
    if err != nil { return "", err }
    real, err := filepath.EvalSymlinks(abs)
    if err != nil {
        if os.IsNotExist(err) {
            return "", &ValidationError{Code: "root-missing", Message: p}
        }
        return "", err
    }
    cleaned := filepath.Clean(real)
    cleaned = strings.TrimSuffix(cleaned, string(os.PathSeparator))
    return cleaned, nil
}

// overlapsExact returns true iff a is a prefix of b OR b is a prefix of a,
// using filepath.Separator boundaries so /a/b does NOT overlap /a/bc.
func overlapsExact(a, b string) bool {
    if a == b { return true }
    sep := string(filepath.Separator)
    if strings.HasPrefix(b, a+sep) { return true }
    if strings.HasPrefix(a, b+sep) { return true }
    return false
}
```

### 4.2 `AddRoots`

```go
func AddRoots(ctx context.Context, db DBPool,
              libraryID uuid.UUID, rawPaths []string) ([]Root, error) {
    canonicals := make([]string, 0, len(rawPaths))
    for _, p := range rawPaths {
        c, err := Canonicalize(p)
        if err != nil { return nil, err }
        canonicals = append(canonicals, c)
    }

    // Self-overlap (same submission set): includes intra-library nesting.
    for i := 0; i < len(canonicals); i++ {
        for j := i + 1; j < len(canonicals); j++ {
            if overlapsExact(canonicals[i], canonicals[j]) {
                return nil, &OverlapError{Code: "library-roots-overlap",
                    Path: rawPaths[j]}
            }
        }
    }

    tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
    if err != nil { return nil, err }
    defer tx.Rollback(ctx)
    q := dbq.WithTx(tx)

    var inserted []Root
    for i, c := range canonicals {
        // Probe overlapping roots in any library. Index-backed:
        existing, err := q.FindOverlappingRoots(ctx, c)
        if err != nil { return nil, err }
        for _, e := range existing {
            // The exact-equal case is also caught by the unique index;
            // the prefix cases come back here.
            if e.LibraryID != libraryID || !overlapsExact(c, e.CanonicalPath) ||
                c == e.CanonicalPath {
                return nil, &OverlapError{
                    Code:             "library-roots-overlap",
                    Path:             rawPaths[i],
                    ConflictsWith:    e.ID,
                    ConflictsLibrary: e.LibraryID,
                }
            }
        }
        row, err := q.InsertRoot(ctx, dbq.InsertRootParams{
            ID: uuid.New(), LibraryID: libraryID,
            Path: rawPaths[i], CanonicalPath: c,
        })
        if err != nil {
            // Unique index violation falls here — convert to OverlapError.
            if isUniqueViolation(err) {
                return nil, &OverlapError{
                    Code: "library-roots-overlap",
                    Path: rawPaths[i],
                }
            }
            return nil, err
        }
        inserted = append(inserted, toRoot(row))
    }
    if err := tx.Commit(ctx); err != nil { return nil, err }
    return inserted, nil
}
```

### 4.3 `CheckRuntimeOverlap`

```go
// CheckRuntimeOverlap walks every root, re-resolves its symlinks, and
// emits a warning + audit row if the resolved-now overlaps any other
// root's canonical_path. Called from the sweep prologue.
func CheckRuntimeOverlap(ctx context.Context, db DBPool, audit AuditWriter,
                         libraryID uuid.UUID) error {
    roots, err := dbq.New(db).ListRootsForLibrary(ctx, libraryID)
    if err != nil { return err }
    others, err := dbq.New(db).ListRootsAll(ctx)
    if err != nil { return err }

    for _, r := range roots {
        cur, err := Canonicalize(r.Path)
        if err != nil { continue } // root may be unmounted; sweep deals
        if cur == r.CanonicalPath { continue }
        // Mount layout has shifted. Check overlap with siblings.
        for _, o := range others {
            if o.ID == r.ID { continue }
            if overlapsExact(cur, o.CanonicalPath) {
                audit.Write(ctx, audit.LibraryEvent{
                    Event: "roots-runtime-overlap",
                    LibraryID: libraryID,
                    Payload: map[string]any{
                        "root_id":         r.ID,
                        "declared":        r.Path,
                        "resolved_now":    cur,
                        "conflicts_with":  o.ID,
                        "other_library":  o.LibraryID,
                    },
                })
                log.Warn("library-roots-runtime-overlap library_id=", libraryID)
                runtimeOverlapTotal.Inc()
            }
        }
    }
    return nil
}
```

### 4.4 Python sweep prologue hook

```python
# pipeline/src/maktaba_pipeline/libraries/roots.py
async def check_runtime_overlap(db, library_id) -> None:
    rows = await db.fetch(
        "SELECT id, path, canonical_path FROM library_roots "
        "WHERE library_id=$1", library_id,
    )
    others = await db.fetch(
        "SELECT id, library_id, canonical_path FROM library_roots "
        "WHERE library_id <> $1", library_id,
    )
    for r in rows:
        try:
            cur = os.path.realpath(r["path"])
        except OSError:
            continue
        if cur == r["canonical_path"]:
            continue
        for o in others:
            if _overlaps_exact(cur, o["canonical_path"]):
                await db.execute(
                    "INSERT INTO audit_log (id, category, event, library_id, payload_jsonb) "
                    "VALUES (gen_random_uuid(), 'library', 'roots-runtime-overlap', $1, $2::jsonb)",
                    library_id, json.dumps({
                        "root_id": str(r["id"]),
                        "declared": r["path"],
                        "resolved_now": cur,
                        "conflicts_with": str(o["id"]),
                    }),
                )
                runtime_overlap_total.inc()
```

## 5. Test plan

### 5.1 Canonicalization parity (`cases.json`)

```json
[
  {"in": "/mnt/a", "expect": "/mnt/a"},
  {"in": "/mnt/a/", "expect": "/mnt/a"},
  {"in": "/mnt/a//b", "expect": "/mnt/a/b"},
  {"in": "/mnt/a/../a/./b", "expect": "/mnt/a/b"},
  {"in": "/etc/passwd", "expect": "/etc/passwd"},
  {"symlink": [{"src": "/tmp/_test/sym", "dst": "/tmp/_test/real"}],
   "in": "/tmp/_test/sym/x", "expect": "/tmp/_test/real/x"}
]
```

Tests in both Go and Python load `cases.json` and assert identical
output where applicable.

### 5.2 Go unit tests (`roots_test.go`)

| Test | What it pins |
|---|---|
| `TestAddRoots_HappyPath` | `["/mnt/a", "/mnt/b"]` → 2 rows. |
| `TestAddRoots_RejectsSelfOverlap` | `["/mnt/a", "/mnt/a/sub"]` → `library-roots-overlap` (intra-library nesting). AC-3. |
| `TestAddRoots_RejectsExactDuplicate` | Pre-existing `/mnt/a` in libraryX; new request `/mnt/a` for libraryY → `library-roots-overlap` with `conflicts_with` and `conflicts_library`. |
| `TestAddRoots_RejectsPrefixOverlap` | `/mnt/a` exists; new request `/mnt/a/sub` → rejected. AC-2. |
| `TestAddRoots_RejectsParentOverlap` | `/mnt/a/b` exists; new request `/mnt/a` → rejected. |
| `TestAddRoots_DoesNotConfuseSiblings` | `/mnt/a` exists; new request `/mnt/ab` → ACCEPTED (not a prefix at separator boundary). |
| `TestAddRoots_TrailingSlashIgnored` | `/mnt/a/` is treated equal to `/mnt/a`; second insert is rejected. |
| `TestAddRoots_RealpathFollowsSymlink` | Make `/tmp/sym → /tmp/real`; `/tmp/sym/x` and `/tmp/real/x` are treated as the same canonical. |
| `TestAddRoots_NonexistentRootRejected` | `/does/not/exist` → `root-missing`. |
| `TestAddRoots_TransactionalSerializable` | Two parallel `AddRoots` calls inserting overlapping paths; serializable isolation makes one fail with `library-roots-overlap` (or serialization failure, retriable). |

### 5.3 Runtime overlap tests

| Test | What it pins |
|---|---|
| `TestCheckRuntimeOverlap_NoOverlap_NoAudit` | No mount changes → no audit row, no warning. |
| `TestCheckRuntimeOverlap_DetectsAfterSymlinkChange` | Pre-state: rootA = `/mnt/a` (real), rootB = `/mnt/b` (real, separate); change rootA's symlink to point at `/mnt/b/sub` → `Canonicalize(/mnt/a)` returns `/mnt/b/sub`; overlap with rootB's canonical detected; audit row written with `event='roots-runtime-overlap'`. AC-4. |
| `TestCheckRuntimeOverlap_DoesNotAutoUpdateCanonical` | After detection, the original `canonical_path` in DB is **unchanged** — operator must fix the mount and trigger a manual recanonicalize. | `TestCheckRuntimeOverlap_DoesNotAutoUpdateCanonical` |
| `TestCheckRuntimeOverlap_UnmountedRootSkipsSilently` | A root whose realpath now fails (unmount) → skipped; no audit row; metric `roots_unmount_total` increments. |

### 5.4 Migration tests

| Test | What it pins |
|---|---|
| `test_unique_index_on_canonical_path` | Two INSERTs with same canonical_path → unique violation. |
| `test_prefix_index_used` | EXPLAIN of `FindOverlappingRoots` uses `library_roots_canonical_prefix` for the LIKE conditions. |

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Symlink that resolves to an existing root | Canonicalize follows symlinks before comparison; the overlap check catches it. | `TestAddRoots_RealpathFollowsSymlink` |
| `..` traversal in raw input | `filepath.Clean` resolves; the canonical path is the resolved form. | `cases.json` "/mnt/a/../a/./b" |
| Sibling-prefix false positive | Canonical paths use separator-boundary checks. `/mnt/a` does NOT overlap `/mnt/ab`. | `TestAddRoots_DoesNotConfuseSiblings` |
| Empty roots list on PATCH | API requires at least one root; 422 `library-needs-root`. (Out of scope here.) | API validator |
| Runtime symlink change creating overlap | AC-4: WARN + audit row; sweep continues; ops fixes. | `TestCheckRuntimeOverlap_DetectsAfterSymlinkChange` |
| Mount disappears between create and sweep | Sweep skips the missing root with a metric; no audit (it's a mount issue, not an overlap). | `TestCheckRuntimeOverlap_UnmountedRootSkipsSilently` |
| User adds `/` as a root | Allowed by canonicalize but operationally suicidal — every other library overlaps. The error message points to the conflict. Documented as "don't do this". | Documented |

## 7. Configuration

| Key | Default | Effect |
|---|---|---|
| (none) | — | This story has no library-level knobs. |

## 8. Dependencies

| Dep | Source | Why |
|---|---|---|
| `path/filepath` (stdlib Go), `os.path` (Python) | stdlib | Canonicalization. |
| `audit_log` | Story 9.17 | Runtime overlap audit. |
| Sweep runner | Story 9.3 | Calls `check_runtime_overlap` once per run. |

## 9. Acceptance checklist

**Migration**
- [ ] `library_roots` exists with the unique canonical-path index and the prefix-search index.

**Code**
- [ ] `AddRoots` is serializable; rejects self-overlap and cross-library overlap; returns structured `OverlapError`.
- [ ] `CheckRuntimeOverlap` runs in the sweep prologue.

**Behaviour (story acceptance criteria)**
- [ ] AC-1: a library with multiple roots merges results; no per-root subdivision in API.
- [ ] AC-2: overlapping roots across libraries → 422 `library-roots-overlap`.
- [ ] AC-3: overlap detection includes the same library (intra-library nesting).
- [ ] AC-4: runtime mount change creating overlap emits WARN log + audit row.

**Observability**
- [ ] Counter `library_roots_overlap_rejections_total{kind=create|update}`.
- [ ] Counter `roots_runtime_overlap_total`.
- [ ] Counter `roots_unmount_total`.

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.16.
- [ ] Operator handbook documents the runtime-overlap warning and the manual recanonicalize procedure.
