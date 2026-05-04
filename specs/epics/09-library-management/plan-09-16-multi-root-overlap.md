# Plan 9.16 — Multi-root and overlap detection — implementation

> Implementation plan for [story-09-16-multi-root-overlap.md](story-09-16-multi-root-overlap.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: extends the `libraries` table introduced
> by Story 9.1; the runtime sweep that calls back into canonicalization
> is owned by [Story 9.3](story-09-03-periodic-sweep.md); audit rows
> use the `audit_log` schema from [Plan 9.17](plan-09-17-library-audit.md).

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Roots are stored as `TEXT[]` on `libraries`, persisted in canonical form.** Canonicalization (`filepath.EvalSymlinks` + `filepath.Clean` + ensure trailing-slash absent) happens at create/update time **before** insert. The DB never sees a non-canonical path. | AC-3 states the overlap rule in canonical form; canonicalizing at write time means the overlap query is a simple GIN-backed `&&` join. | Canonicalizing on every read would make the `&&` query non-sargable (it would call a function on the column) and force a sequential scan. Pre-canonicalizing also makes the `(library_id, root)` invariant testable from psql alone. |
| D2 | **Overlap detection uses a Postgres GIN index on `roots` plus a custom `paths_overlap(text[], text[])` SQL function** that returns true when any path in either array is a prefix of any path in the other. The pre-create check is `SELECT id FROM libraries WHERE roots && $1 OR paths_overlap(roots, $1)` (the `&&` array-overlap operator catches *exact* duplicates in one call; `paths_overlap` catches *prefix* overlaps). | AC-2: 422 on overlap; AC-3: prefix-rule on canonical paths. | The `&&` operator on its own only detects exact equality, not prefix containment. The composite query short-circuits exact duplicates via the index and falls back to a function for the prefix case. The function is `IMMUTABLE` so Postgres can cache it. |
| D3 | **Within a single library, roots must also not nest** (`["/a", "/a/b"]` is rejected). The validation runs in Go *before* the DB call so a single-library nesting is caught without a roundtrip. | AC test case: "`["/a", "/a/b"]` in one library is rejected." | Self-nested roots double-walk the same files in the sweep, double-charge dedup, and confuse stats. Catching client-side keeps the failure mode loud. |
| D4 | **Pipeline's periodic sweep (Story 9.3) re-canonicalizes every root per sweep and emits `library-roots-runtime-overlap` whenever two now-canonical roots overlap.** The warning is logged at WARN, an audit row is written with `category='library', event='roots.runtime_overlap'`, and the sweep continues — no work is suppressed. | AC-4: "the sweep emits a `WARN` log ... writes an audit row; further sweep work continues." | Mount layouts change at runtime (NFS automount, encrypted-filesystem mount) and we cannot block scanning on operator action. The warning + audit row gives the operator the evidence to fix the mount; the sweep keeps draining the queue. |
| D5 | **Canonicalization tolerates missing roots.** If `EvalSymlinks` fails with `ENOENT`, the root is stored as `Clean(input)` and the overlap check uses the cleaned form. The library is still creatable (an unmounted root will fail-soft at scan time, not at create time). | Refines the story (which doesn't say what to do for missing paths). | Operators frequently create libraries before the disk is mounted (boot-sequence ordering). Failing creation here would force a runbook around mount ordering; instead we accept the path lexically and let the sweep raise the missing-root warning when it tries to walk. |
| D6 | **Two roots that resolve to the same physical inode (via different mount points) overlap.** `EvalSymlinks` does not resolve bind mounts; we additionally `os.Stat` each root and compare `Sys().(*syscall.Stat_t).Dev/Ino`. Identical (Dev, Ino) pairs are treated as overlap. | AC-3 "after path canonicalization (resolve symlinks, trailing slashes, `..`)." | A bind-mount of `/data` at `/mnt/external` resolves to two distinct paths via `EvalSymlinks` but the same inode. Without the inode check the user can declare both as separate roots and the sweep will scan files twice. The inode comparison closes the hole. |

If D2 is rejected (no `paths_overlap` function): pre-create checks would need an O(N×M) comparison loop in Go, which is fine at our scale (≤ ~100 libraries × ~10 roots) but loses the ability to use one query for "find every library that overlaps with this proposed root" — a useful diagnostic for ops.

If D6 is rejected (skip inode dedup): bind-mount duplicates slip through and the operator only finds out from the Story 9.4 dedup pass days later, with files already double-processed. The runtime warning in D4 is the safety net; without inode dedup the safety net catches a mountful of duplicates instead of zero.

---

## 1. Architecture diagram — overlap detection flow

```
   ┌────────── Create / Update library (API, Go) ────────────┐
   │                                                          │
   │   client POSTs roots                                     │
   │       │                                                  │
   │       ▼                                                  │
   │  canonicalize(roots):                                    │
   │    - Clean (resolve `..`, trailing /)                    │
   │    - EvalSymlinks (D1)                                   │
   │    - Stat → (Dev, Ino) (D6)                              │
   │    - reject duplicates within request                    │
   │       │                                                  │
   │       ▼                                                  │
   │  intra-library nest check (D3):                          │
   │    if any root_i is prefix of root_j → 422               │
   │       │                                                  │
   │       ▼                                                  │
   │  cross-library overlap check (D2):                       │
   │    SELECT id, name, roots FROM libraries                 │
   │     WHERE roots && $1 OR paths_overlap(roots, $1)        │
   │    if any → 422 library-roots-overlap (with details)     │
   │       │                                                  │
   │       ▼                                                  │
   │  INSERT/UPDATE libraries SET roots = $canonical          │
   └──────────────────────────────────────────────────────────┘

   ┌────────── Periodic sweep (Pipeline, Python) ─────────────┐
   │                                                          │
   │   per library:                                           │
   │     re-canonicalize each root (Story 9.3 entry)          │
   │     compare to OTHER libraries' canonical roots          │
   │     if any new overlap detected:                         │
   │       log WARN library-roots-runtime-overlap             │
   │       INSERT into audit_log                              │
   │           (category='library',                           │
   │            event='roots.runtime_overlap', payload=...)   │
   │     continue with normal sweep work                      │
   └──────────────────────────────────────────────────────────┘
```

---

## 2. Detailed implementation

### 2.1 Package layout

**API (Go):**
```
api/internal/library/
├── roots.go              # Canonicalize, OverlapDetector, intra-library nest check
├── roots_test.go
└── handler_create.go     # extended: invokes canonicalize + overlap check
```

**Pipeline (Python):**
```
pipeline/src/maktaba_pipeline/
├── sweep/
│   ├── runtime_overlap.py    # check_runtime_overlap(library)
│   └── tests/test_runtime_overlap.py
└── pipeline/stages/sweep.py  # extended: call check_runtime_overlap per library
```

**Shared SQL:**
```
shared/db/migrations/
└── 0021_library_roots_canonical.sql   # roots TEXT[] + GIN index + paths_overlap()
```

### 2.2 Schema migration — `0021_library_roots_canonical.sql`

```sql
BEGIN;

-- The libraries.roots column was created by Story 9.1's migration as TEXT[].
-- This migration adds the GIN index and the paths_overlap helper.

CREATE INDEX IF NOT EXISTS libraries_roots_gin
    ON libraries USING GIN (roots);

-- Returns true iff any element of `a` is equal to, or a path-prefix of,
-- any element of `b` (or vice-versa). Inputs MUST be already canonical
-- (no trailing slash, no symlinks, no '..').
CREATE OR REPLACE FUNCTION paths_overlap(a TEXT[], b TEXT[])
RETURNS BOOLEAN AS $$
DECLARE
    pa TEXT;
    pb TEXT;
BEGIN
    IF a IS NULL OR b IS NULL THEN
        RETURN FALSE;
    END IF;
    FOREACH pa IN ARRAY a LOOP
        FOREACH pb IN ARRAY b LOOP
            IF pa = pb THEN
                RETURN TRUE;
            END IF;
            -- Prefix containment: pa under pb, OR pb under pa.
            IF pa LIKE pb || '/%' THEN
                RETURN TRUE;
            END IF;
            IF pb LIKE pa || '/%' THEN
                RETURN TRUE;
            END IF;
        END LOOP;
    END LOOP;
    RETURN FALSE;
END;
$$ LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE;

-- Convenience: per-library invariant (no nested roots inside one library).
-- Enforced again in Go for friendly error messages, but a CHECK is the
-- last line of defense.
ALTER TABLE libraries
    ADD CONSTRAINT libraries_roots_no_self_nest
    CHECK (NOT paths_overlap(roots, roots) OR cardinality(roots) <= 1);

COMMIT;
```

The `CHECK` uses the same function: a single-element array trivially
"overlaps with itself" (every element is equal to itself), so the
constraint short-circuits when `cardinality(roots) <= 1`.

### 2.3 Go canonicalization — `api/internal/library/roots.go`

```go
package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CanonicalRoot is a path the DB will see — already cleaned + symlink-resolved.
type CanonicalRoot struct {
	Path string
	Dev  uint64
	Ino  uint64
}

var (
	ErrEmptyRoots         = errors.New("roots: must not be empty")
	ErrRelativeRoot       = errors.New("roots: must be absolute paths")
	ErrSelfNestedRoots    = errors.New("roots: one root is a prefix of another within the same library")
	ErrInodeDuplicate     = errors.New("roots: two roots resolve to the same physical inode")
	ErrCrossLibraryOverlap = errors.New("library-roots-overlap")
)

// Canonicalize cleans + symlink-resolves each input. ENOENT is tolerated (D5):
// the lexically-clean form is kept and Dev/Ino are zero.
func Canonicalize(inputs []string) ([]CanonicalRoot, error) {
	if len(inputs) == 0 {
		return nil, ErrEmptyRoots
	}
	out := make([]CanonicalRoot, 0, len(inputs))
	seen := make(map[string]bool)
	seenInode := make(map[[2]uint64]string)

	for _, raw := range inputs {
		raw = strings.TrimSpace(raw)
		if !filepath.IsAbs(raw) {
			return nil, fmt.Errorf("%w: %q", ErrRelativeRoot, raw)
		}
		clean := filepath.Clean(raw)
		resolved, err := filepath.EvalSymlinks(clean)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("evalsymlinks %q: %w", raw, err)
			}
			resolved = clean // D5: tolerate missing
		}
		// Strip trailing slash (Clean already does this except for "/").
		if resolved != "/" {
			resolved = strings.TrimRight(resolved, "/")
		}
		if seen[resolved] {
			continue // request-level dup is silently coalesced
		}
		seen[resolved] = true

		var dev, ino uint64
		if info, err := os.Stat(resolved); err == nil {
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				dev, ino = uint64(st.Dev), st.Ino
				if ino != 0 {
					if prev, exists := seenInode[[2]uint64{dev, ino}]; exists {
						return nil, fmt.Errorf("%w: %q and %q",
							ErrInodeDuplicate, prev, resolved)
					}
					seenInode[[2]uint64{dev, ino}] = resolved
				}
			}
		}
		out = append(out, CanonicalRoot{Path: resolved, Dev: dev, Ino: ino})
	}

	// Sort + intra-library nest check (D3).
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	for i := 0; i < len(out)-1; i++ {
		a, b := out[i].Path, out[i+1].Path
		if a == b || strings.HasPrefix(b, a+"/") {
			return nil, fmt.Errorf("%w: %q under %q", ErrSelfNestedRoots, b, a)
		}
	}
	return out, nil
}

// CrossLibraryOverlap is what the API returns when paths_overlap fires.
type CrossLibraryOverlap struct {
	LibraryID   uuid.UUID `json:"library_id"`
	LibraryName string    `json:"library_name"`
	Conflicts   []string  `json:"conflicting_roots"`
}

type OverlapDetector struct {
	pool *pgxpool.Pool
}

// FindOverlapping returns the libraries whose roots overlap (== or prefix)
// with `proposed`. `excludeID` is set on update calls so a library doesn't
// flag itself.
func (d *OverlapDetector) FindOverlapping(
	ctx context.Context, proposed []CanonicalRoot, excludeID *uuid.UUID,
) ([]CrossLibraryOverlap, error) {
	paths := make([]string, len(proposed))
	for i, r := range proposed {
		paths[i] = r.Path
	}
	rows, err := d.pool.Query(ctx, `
		SELECT id, name, roots
		FROM libraries
		WHERE (roots && $1 OR paths_overlap(roots, $1))
		  AND ($2::uuid IS NULL OR id <> $2)
	`, paths, excludeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []CrossLibraryOverlap
	for rows.Next() {
		var hit CrossLibraryOverlap
		var existingRoots []string
		if err := rows.Scan(&hit.LibraryID, &hit.LibraryName, &existingRoots); err != nil {
			return nil, err
		}
		// Compute the actually-conflicting subset for the error payload.
		for _, p := range paths {
			for _, e := range existingRoots {
				if p == e || strings.HasPrefix(p, e+"/") || strings.HasPrefix(e, p+"/") {
					hit.Conflicts = append(hit.Conflicts, e)
					break
				}
			}
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}
```

### 2.4 Handler integration — `handler_create.go` (excerpt)

```go
func (h *CreateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req CreateLibraryRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	canon, err := Canonicalize(req.Roots)
	if err != nil {
		switch {
		case errors.Is(err, ErrSelfNestedRoots),
			errors.Is(err, ErrInodeDuplicate),
			errors.Is(err, ErrRelativeRoot),
			errors.Is(err, ErrEmptyRoots):
			writeProblem(w, http.StatusUnprocessableEntity, "library-roots-invalid", err.Error())
		default:
			writeProblem(w, http.StatusInternalServerError, "library-roots-canonicalize-failed", err.Error())
		}
		return
	}

	hits, err := h.overlap.FindOverlapping(r.Context(), canon, nil)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "library-roots-check-failed", err.Error())
		return
	}
	if len(hits) > 0 {
		writeProblemDetail(w, http.StatusUnprocessableEntity, ProblemDetail{
			Type:  "library-roots-overlap",
			Title: "Roots overlap with existing libraries",
			Extra: map[string]any{"conflicts": hits},
		})
		return
	}

	roots := make([]string, len(canon))
	for i, c := range canon {
		roots[i] = c.Path
	}
	row := h.pool.QueryRow(r.Context(),
		`INSERT INTO libraries (id, name, roots) VALUES (uuidv7(), $1, $2) RETURNING id`,
		req.Name, roots)
	var id uuid.UUID
	if err := row.Scan(&id); err != nil {
		// pgconn.PgError check — unique_violation, check_violation, etc.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "libraries_roots_no_self_nest" {
			writeProblem(w, http.StatusUnprocessableEntity, "library-roots-self-nested", pgErr.Message)
			return
		}
		writeProblem(w, http.StatusInternalServerError, "library-create-failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, CreateLibraryResponse{ID: id, Roots: roots})
}
```

### 2.5 Pipeline runtime overlap detection — `runtime_overlap.py`

```python
"""Story 9.16 AC-4: runtime sweep re-canonicalizes each root and
emits library-roots-runtime-overlap when previously-disjoint roots
now resolve to overlapping paths."""
from __future__ import annotations

import logging
import os
import os.path as osp
from dataclasses import dataclass
from typing import Iterable

import asyncpg

log = logging.getLogger(__name__)


@dataclass(frozen=True)
class CanonicalRoot:
    raw: str
    canonical: str
    dev: int
    ino: int


def canonicalize(raw: str) -> CanonicalRoot:
    clean = osp.normpath(raw)
    try:
        resolved = osp.realpath(clean, strict=False)
    except OSError:
        resolved = clean
    resolved = resolved.rstrip("/") or "/"
    try:
        st = os.stat(resolved)
        dev, ino = int(st.st_dev), int(st.st_ino)
    except OSError:
        dev = ino = 0
    return CanonicalRoot(raw=raw, canonical=resolved, dev=dev, ino=ino)


def _overlap_path(a: str, b: str) -> bool:
    return a == b or a.startswith(b + "/") or b.startswith(a + "/")


def _overlap_inode(a: CanonicalRoot, b: CanonicalRoot) -> bool:
    return a.ino != 0 and a.dev == b.dev and a.ino == b.ino


async def check_runtime_overlap(
    conn: asyncpg.Connection,
    library_id: str,
    library_roots: list[str],
) -> list[dict]:
    """Compare this library's currently-canonical roots against every other
    library's currently-canonical roots. Returns the list of (library_id,
    library_name, conflicting_pair) records that fired this sweep, and
    writes one audit_log row per conflict.

    The DB never sees the runtime-resolved paths — they exist only for
    the duration of this check. The stored roots stay as written.
    """
    mine = [canonicalize(r) for r in library_roots]
    rows = await conn.fetch(
        "SELECT id, name, roots FROM libraries WHERE id <> $1", library_id)

    fired: list[dict] = []
    for r in rows:
        theirs = [canonicalize(p) for p in r["roots"]]
        for a in mine:
            for b in theirs:
                if _overlap_path(a.canonical, b.canonical) or _overlap_inode(a, b):
                    fired.append({
                        "other_library_id": str(r["id"]),
                        "other_library_name": r["name"],
                        "my_root_raw": a.raw,
                        "my_root_canonical": a.canonical,
                        "their_root_raw": b.raw,
                        "their_root_canonical": b.canonical,
                        "via_inode": a.ino == b.ino and a.ino != 0,
                    })

    if not fired:
        return []

    log.warning(
        "library-roots-runtime-overlap",
        extra={"library_id": library_id, "conflicts": fired},
    )
    # Best-effort audit row per conflict.
    for f in fired:
        try:
            await conn.execute("""
                INSERT INTO audit_log (
                    id, ts, category, event, library_id, payload_jsonb)
                VALUES (uuidv7(), now(), 'library',
                        'roots.runtime_overlap', $1, $2::jsonb)
            """, library_id, _payload_json(f))
        except Exception:  # pragma: no cover — best effort
            log.exception("audit_write_failed for runtime overlap")
    return fired


def _payload_json(f: dict) -> str:
    import json
    return json.dumps(f)
```

### 2.6 Sweep stage integration — `sweep.py` (excerpt)

```python
# pipeline/src/maktaba_pipeline/pipeline/stages/sweep.py
from maktaba_pipeline.sweep.runtime_overlap import check_runtime_overlap


async def run_sweep_for_library(ctx, library_row):
    async with ctx.db_pool.acquire() as conn:
        # Pre-sweep AC-4 hook.
        try:
            await check_runtime_overlap(
                conn, str(library_row["id"]), library_row["roots"],
            )
        except Exception:  # never block sweep on overlap detection
            ctx.log.exception("runtime overlap check failed")

    # ... existing sweep work (Story 9.3) ...
```

---

## 3. File scaffolding checklist

| Order | File | Symbols | Tests gating |
|-------|------|---------|--------------|
| 1 | `shared/db/migrations/0021_library_roots_canonical.sql` | `libraries_roots_gin`, `paths_overlap()`, `libraries_roots_no_self_nest` | `TestPathsOverlapFunction` |
| 2 | `api/internal/library/roots.go` | `CanonicalRoot`, `Canonicalize`, `OverlapDetector.FindOverlapping`, sentinel errors | `TestCanonicalize*`, `TestOverlapDetector*` |
| 3 | `api/internal/library/handler_create.go` (extend) | wired call to `Canonicalize` + `OverlapDetector` + 422 emission | `TestHandlerCreate_OverlapReturns422` |
| 4 | `pipeline/src/maktaba_pipeline/sweep/runtime_overlap.py` | `CanonicalRoot`, `canonicalize`, `check_runtime_overlap` | `test_runtime_overlap_*` |
| 5 | `pipeline/src/maktaba_pipeline/pipeline/stages/sweep.py` (extend) | `run_sweep_for_library` calls `check_runtime_overlap` | `test_sweep_logs_warning_on_overlap` |

---

## 4. Test cases keyed to ACs

### T1 — AC-1: two roots, both walked, results merged

```go
func TestCreate_TwoRoots_BothPersistedSorted(t *testing.T) {
	rootA := tempDir(t, "videos-a")
	rootB := tempDir(t, "videos-b")
	resp := postCreate(t, CreateLibraryRequest{
		Name: "Lib", Roots: []string{rootB, rootA},
	})
	assert.Equal(t, http.StatusCreated, resp.Code)
	stored := scanRoots(t, db, resp.Body.ID)
	assert.Equal(t, []string{rootA, rootB}, stored) // sorted
}
```

### T2 — AC-2: cross-library overlap returns 422 `library-roots-overlap`

```go
func TestCreate_OverlapsExistingLibrary_Returns422(t *testing.T) {
	seedLibrary(t, db, "First", []string{"/mnt/media"})

	body := postCreateRaw(t, CreateLibraryRequest{
		Name: "Second", Roots: []string{"/mnt/media/sub"},
	})
	assert.Equal(t, http.StatusUnprocessableEntity, body.Code)
	assert.Equal(t, "library-roots-overlap", body.Problem.Type)
	assert.Equal(t, "First", body.Problem.Extra["conflicts"].([]any)[0].(map[string]any)["library_name"])
}
```

### T3 — AC-3: canonicalization fixtures (symlink, `..`, trailing slash)

```go
func TestCanonicalize_SymlinkResolved(t *testing.T) {
	target := tempDir(t, "real")
	link := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(target, link))

	out, err := Canonicalize([]string{link})
	require.NoError(t, err)
	assert.Equal(t, target, out[0].Path)
}

func TestCanonicalize_TrailingSlashAndDotDotResolved(t *testing.T) {
	d := tempDir(t, "x")
	out, err := Canonicalize([]string{d + "/sub/../"})
	require.NoError(t, err)
	assert.Equal(t, d, out[0].Path)
}
```

### T4 — AC-3: intra-library nesting rejected

```go
func TestCanonicalize_SelfNestRejected(t *testing.T) {
	a := tempDir(t, "a")
	b := filepath.Join(a, "b")
	require.NoError(t, os.MkdirAll(b, 0o755))
	_, err := Canonicalize([]string{a, b})
	assert.ErrorIs(t, err, ErrSelfNestedRoots)
}
```

### T5 — D6: bind-mount inode-duplicate rejected

```go
func TestCanonicalize_InodeDuplicateRejected(t *testing.T) {
	if !canBindMount(t) {
		t.Skip("requires bind mount")
	}
	src := tempDir(t, "src")
	dst := tempDir(t, "dst")
	bindMount(t, src, dst)

	_, err := Canonicalize([]string{src, dst})
	assert.ErrorIs(t, err, ErrInodeDuplicate)
}
```

### T6 — AC-4: runtime symlink change → sweep emits warning + audit row

```python
async def test_runtime_overlap_emits_audit_row(db, sweep_logger):
    # Two libraries with originally-distinct roots.
    a_id = await seed_library(db, name="A", roots=["/mnt/a"])
    b_id = await seed_library(db, name="B", roots=["/mnt/b"])
    # Simulate runtime change: /mnt/b is now a symlink into /mnt/a.
    monkey_canonicalize.map = {"/mnt/a": "/mnt/a", "/mnt/b": "/mnt/a/sub"}

    fired = await check_runtime_overlap(db, a_id, ["/mnt/a"])
    assert len(fired) == 1
    assert fired[0]["other_library_id"] == b_id

    rows = await db.fetch(
        "SELECT event, payload_jsonb FROM audit_log "
        "WHERE category='library' AND event='roots.runtime_overlap'")
    assert len(rows) == 1

    sweep_logger.assert_warned("library-roots-runtime-overlap")
```

### T7 — AC-4: sweep continues after warning

```python
async def test_sweep_continues_after_runtime_overlap_warning(db, sweep_runner):
    library = await seed_library(db, roots=["/mnt/a"])
    monkey_canonicalize.map = {"/mnt/a": "/mnt/x"}  # collides with another lib
    await sweep_runner.run(library)
    assert sweep_runner.scanned_files > 0  # work still happened
```

### T8 — `paths_overlap()` SQL function exhaustive

```sql
-- test_paths_overlap.sql
DO $$
BEGIN
  ASSERT paths_overlap(ARRAY['/a'], ARRAY['/a/b']);
  ASSERT paths_overlap(ARRAY['/a/b'], ARRAY['/a']);
  ASSERT paths_overlap(ARRAY['/a'], ARRAY['/a']);
  ASSERT NOT paths_overlap(ARRAY['/a'], ARRAY['/b']);
  ASSERT NOT paths_overlap(ARRAY['/abc'], ARRAY['/ab']); -- prefix-of-path-segment, NOT prefix-of-string
END $$;
```

---

## 5. Edge cases

| #   | Edge case | Handled by |
|-----|-----------|------------|
| E1  | **Symlink that resolves to an already-declared root.** Caught by `EvalSymlinks`-then-overlap-check at create time (D1, D2) and by `check_runtime_overlap` at sweep time (D4). | `Canonicalize` + sweep AC-4 path. |
| E2  | **Bind mount creating two roots with same inode.** Caught by Dev/Ino comparison at create (D6). At runtime, the same comparison fires inside `check_runtime_overlap`. | `Canonicalize` (in-request) + `check_runtime_overlap._overlap_inode`. |
| E3  | **`/abc` vs `/ab`.** Not an overlap — `paths_overlap` matches on `'/' || rest` so `/ab` is not a prefix-segment of `/abc`. | T8 fixture. |
| E4  | **Root is `"/"` (rare but legal).** `Clean("/") == "/"`, every path is a sub-path. `paths_overlap(['/'], anything_else)` returns true. We reject `"/"` as a root with a friendlier 422 (`library-roots-system-root-forbidden`) before the cross-library check fires. | `Canonicalize` adds the rule. |
| E5  | **Root that doesn't exist yet (mount pending).** `EvalSymlinks` returns ENOENT; we fall back to `Clean(input)` (D5). The library is creatable; the missing-root warning surfaces at sweep time via Story 9.3. | `Canonicalize` ENOENT branch. |
| E6  | **Update that removes one root and adds another.** Same code path as create — re-canonicalize the full new array, run the overlap query with `excludeID = library.id` so we don't flag self. | `OverlapDetector.FindOverlapping` `excludeID` arg. |
| E7  | **Two libraries created in parallel both proposing `/mnt/x`.** The `paths_overlap` check is not in a transaction; both can pass it before either commits. The `libraries_roots_gin` plus a **post-insert overlap re-check** in a SERIALIZABLE retry would close this; for v1 we accept the rare race and flag the warning at the next sweep. Documented. | Operations doc. |
| E8  | **`paths_overlap` performance with N×M arrays.** Worst case N=M=10 roots per library → 100 comparisons per row, scanning all libraries. With ~100 libraries that's 10K comparisons per call — sub-millisecond. We rely on the GIN `&&` operator to short-circuit exact-match cases. | Index in migration. |
| E9  | **A root that's a relative path.** Rejected with `ErrRelativeRoot`. | `Canonicalize`. |
| E10 | **Audit_log unavailable during runtime overlap.** Best-effort: log + counter, sweep continues. Story 9.17 owns the metric. | `check_runtime_overlap` try/except. |

---

## 6. Acceptance checklist

- [ ] **A1** (AC-1) A library with N roots can be created; `GET /api/libraries/{id}` returns the full canonical roots array; the sweep walks every root. (T1)
- [ ] **A2** (AC-2) `POST /api/libraries` with roots overlapping any existing library returns 422 `library-roots-overlap` with a payload listing the conflicting library and the conflicting paths. (T2)
- [ ] **A3** (AC-3) Canonicalization resolves symlinks, `..`, and trailing slashes; the stored value is the resolved form. (T3)
- [ ] **A4** (AC-3) A request with `["/a", "/a/b"]` returns 422 `library-roots-self-nested`. (T4)
- [ ] **A5** (D6) Two roots that resolve to the same inode (bind mount) return 422 `library-roots-inode-duplicate`. (T5)
- [ ] **A6** (AC-4) When the periodic sweep detects two roots that now resolve to overlapping paths, it logs `library-roots-runtime-overlap` at WARN and writes one audit row per conflicting pair with `category='library', event='roots.runtime_overlap'`. (T6)
- [ ] **A7** (AC-4) Sweep work proceeds normally after the warning; no scan is suppressed. (T7)
- [ ] **A8** (D2) The `paths_overlap` SQL function is `IMMUTABLE PARALLEL SAFE`, returns true for path-segment prefix relations, false for path-string prefixes that don't end at a `/` segment. (T8)
- [ ] **A9** (E5) A non-existent path is accepted at create time and flagged at sweep time. (TestCreate_NonexistentRoot_StillCreatable + sweep test)
- [ ] **A10** Update flow re-uses canonicalize + overlap detection with `excludeID = self`. (TestUpdate_OwnRootsDoNotConflict)
