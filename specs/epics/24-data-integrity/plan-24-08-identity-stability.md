# Implementation Plan — Story 24.8 Identity stability across operations

> Companion to [story-24-08-identity-stability.md](story-24-08-identity-stability.md).
> Story states *what* and *why*; this plan states *how*.
> The identity formula is also the input to the dedupe path
> ([Epic 9 Story 9.4](../09-library-management/plan-09-04-content-hash-dedup.md))
> and the integrity check
> ([Story 24.7](plan-24-07-integrity-verification.md)).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Identity function | `BLAKE3(first 4 MiB ‖ last 4 MiB ‖ u64_le(size))` for files ≥ 8 MiB. **Files < 8 MiB**: whole-file hash, **without** appending `u64_le(size)` (per story EC2). The two paths are otherwise consistent: identical bytes → identical hash, regardless of size class. |
| Identity formula source-of-truth | Architecture §1.5 (identity invariant) + §3.1 (scan-stage flow). The implementation here matches that prose exactly. |
| Implementation | `pipeline/src/maktaba_pipeline/domain/identity.py`. Pure function; no I/O assumptions beyond the file path. **`compute_content_hash` is owned here**; plans 24-06 and 24-07 import from `domain.identity`. |
| Reuse on rescan | `videos.path == p AND videos.size_bytes == st.st_size AND videos.mtime == to_timestamptz(st.st_mtime)` short-circuits hashing; the stored hash is reused. **`videos.mtime` is `TIMESTAMPTZ`** per architecture §8.1 — the resolver converts `os.stat().st_mtime` (seconds float, POSIX) to a `TIMESTAMPTZ` for the comparison; the integer-ns short-circuit from earlier drafts is removed. |
| Move/rename | Path-changed-but-hash-matches → update path, no re-process. |
| Copy | Same hash → already-ready row served. |
| Modify-in-place | Hash differs → new row inserted; old row preserved with `superseded_by` link until GC. The `superseded_by UUID REFERENCES videos(id)` FK is declared in plan-24-03's inventory and added by `0050_schema_canonicalization.sql`; this plan's `0060_videos_supersede.sql` migration is now a no-op when 0050 already added the column (defensive `ADD COLUMN IF NOT EXISTS`). |
| Partial-copy edge case | A file `cp -r` is interrupted; the partial file's hash differs from the source's. The resolver emits `Supersede` only after the scanner's mtime-stable-for-≥-60-s gate, so partial copies are not promoted to canonical. Documented in EC `TestRaceTruncateRefill`. |
| Out of scope | `videos.content_hash` uniqueness scope (architecture §8.1; assumed global per AC3); GC of superseded rows (Epic 9). |

## 1. Architecture diagram

```
   scan finds a path
        │
        ▼
   ┌────────────────────────────┐
   │ identity.short_circuit?    │ ◄── (path, size, mtime) match prior row
   └──────┬─────────────────────┘
          │ miss
          ▼
   ┌────────────────────────────┐
   │ identity.compute(path)     │
   │   if size >= 8MiB:         │
   │     hash(first4 ‖ last4 ‖ size)│
   │   else:                    │
   │     hash(whole_file)       │
   └──────┬─────────────────────┘
          │ hash
          ▼
   ┌────────────────────────────┐
   │ resolve in DB              │
   │   - existing? update path  │
   │   - new?     insert row    │
   │   - hash differs?  insert  │
   │     new + supersede old    │
   └────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/domain/identity.py` | The hash function. |
| `pipeline/src/maktaba_pipeline/domain/identity_test.py` | Boundary tests. |
| `pipeline/src/maktaba_pipeline/library/resolve.py` | `resolve(path) -> Decision` that returns `Reuse | InsertNew | Update | Supersede`. |
| `shared/db/migrations/0060_videos_supersede.sql` (+ sqlite) | Adds `superseded_by UUID NULL` to `videos`. |
| `shared/db/queries/videos_resolve.sql` | sqlc queries used by `resolve`. |
| Tests — `tests/integration/identity_*.py`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/pipeline/stages/scan.py` | Calls `resolve()`; consumes `Decision`. |
| `pipeline/src/maktaba_pipeline/library/watcher.py` | Same path; on rename event, just calls `resolve()`. |

### 2.3 Identity function

`identity.py`:

```python
from __future__ import annotations
import struct
from pathlib import Path
import blake3

CHUNK = 4 * 1024 * 1024     # 4 MiB
MIN_FOR_SAMPLED = 2 * CHUNK  # 8 MiB

def compute(path: Path | str) -> str:
    p = Path(path)
    size = p.stat().st_size
    h = blake3.blake3()
    if size < MIN_FOR_SAMPLED:
        # Whole-file hash; documented as the "small file" path.
        with p.open("rb") as f:
            for chunk in iter(lambda: f.read(CHUNK), b""):
                h.update(chunk)
    else:
        with p.open("rb") as f:
            head = f.read(CHUNK)
            f.seek(size - CHUNK)
            tail = f.read(CHUNK)
        h.update(head)
        h.update(tail)
        h.update(struct.pack("<Q", size))   # u64_le(size)
    return h.hexdigest()
```

Boundary edge case: `size == 2 * CHUNK` exactly. The "head reads the
first 4 MiB; tail seeks to size - 4 MiB which is also 4 MiB". The two
ranges abut; no overlap, no gap; the hash equals
`blake3(head ‖ tail ‖ u64(size))`. Tested.

### 2.4 Resolve

`resolve.py`:

```python
from dataclasses import dataclass

@dataclass
class Reuse:        video_id: str  # short-circuit: same row
@dataclass
class InsertNew:    pass           # not seen before
@dataclass
class Update:       video_id: str  # hash same, path changed
@dataclass
class Supersede:    old_video_id: str  # hash differs from prior at this path

Decision = Reuse | InsertNew | Update | Supersede

async def resolve(path: Path, db) -> tuple[Decision, str]:
    """Returns (decision, content_hash). Hashing is performed only
    when the (path, size_bytes, mtime) short-circuit fails.

    Canonical column names (architecture §8.1, plan-24-03):
      videos.size_bytes (BIGINT)
      videos.mtime      (TIMESTAMPTZ)
    """
    st = path.stat()
    # Convert POSIX float seconds to TIMESTAMPTZ for comparison. The
    # column is TIMESTAMPTZ per arch §8.1; integer-ns short-circuit
    # from earlier drafts is gone (it would never match).
    fs_mtime = datetime.fromtimestamp(st.st_mtime, tz=timezone.utc)
    row = await db.fetch_one(
        "SELECT id, content_hash, size_bytes, mtime FROM videos WHERE path=$1",
        str(path))
    if row and row["size_bytes"] == st.st_size and row["mtime"] == fs_mtime:
        return (Reuse(row["id"]), row["content_hash"])
    h = identity.compute(path)
    other = await db.fetch_one(
        "SELECT id FROM videos WHERE content_hash=$1 AND path != $2",
        h, str(path))
    if other:
        return (Update(other["id"]), h)        # move/rename within tracked roots
    if row and row["content_hash"] != h:
        return (Supersede(row["id"]), h)       # modified-in-place
    if not row:
        return (InsertNew(), h)
    return (Reuse(row["id"]), h)
```

### 2.5 Schema bump

`0060_videos_supersede.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

-- Story 24.8: modify-in-place keeps old row with superseded_by → new
-- row's id. GC removes superseded rows after the operator-configured
-- retention.
--
-- The `superseded_by` column may already have been added by
-- plan-24-03's `0050_schema_canonicalization.sql` (see arch §8.7
-- cross-ref). The IF NOT EXISTS makes this migration idempotent
-- regardless of which one runs first.
ALTER TABLE videos ADD COLUMN IF NOT EXISTS superseded_by UUID NULL
    REFERENCES videos(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS videos_superseded_by_idx
    ON videos (superseded_by) WHERE superseded_by IS NOT NULL;

-- +goose StatementEnd
```

### 2.6 Scan-stage hook

`stages/scan.py` (relevant logic):

```python
async def discover(self, path: Path):
    decision, content_hash = await resolve.resolve(path, self.db)
    match decision:
        case Reuse(_):
            return  # nothing to do
        case Update(video_id):
            st = path.stat()
            fs_mtime = datetime.fromtimestamp(st.st_mtime, tz=timezone.utc)
            await self.db.execute(
                "UPDATE videos SET path=$2, mtime=$3 WHERE id=$1",
                video_id, str(path), fs_mtime)
        case Supersede(old_id):
            new_id = await self._insert_new_video(path, content_hash)
            # Lowercase 'superseded' per plan-24-03 CHECK enum.
            await self.db.execute(
                "UPDATE videos SET superseded_by=$1, state='superseded' WHERE id=$2",
                new_id, old_id)
            await self._enqueue_pipeline(new_id)
        case InsertNew():
            new_id = await self._insert_new_video(path, content_hash)
            await self._enqueue_pipeline(new_id)
```

The `superseded_by` FK plus the `superseded` state (in plan-24-03's
CHECK enum, sourced from `shared/db/states.yaml`) makes the
relationship queryable for history.

### 2.7 Boundary handling

`identity.py` includes a small `_validate_path` that raises early on
anomalies the stat call would otherwise mask:

```python
def _validate(p: Path) -> None:
    if not p.is_file():
        raise IdentityError(f"not a regular file: {p}")
    if p.stat().st_size == 0:
        raise IdentityError(f"empty file: {p}")
```

A 0-byte file is treated as an error rather than a hashable input;
documented because an in-progress write often shows up as size=0
briefly.

## 3. Test plan

### 3.1 Move stability (TC1)

| Test | What it pins |
|---|---|
| `TestRenameStays` | Insert 1000 videos; rename each (same content, new path); rescan; assert zero `processing_jobs` enqueued and 1000 path updates. |
| `TestMoveBetweenLibrariesUpdates` | Move a file from lib A to lib B's path; the row's `library_id` updates; hash unchanged. |

### 3.2 Copy stability (TC2)

| Test | What it pins |
|---|---|
| `TestCopyReusesArtifacts` | Copy 100 files to a new path; the new rows reference the same `content_hash`; transcripts and indexes are reused (no re-process enqueued). |
| `TestCopyReadsArtifactsImmediately` | Immediately after copy, `GET /api/videos/<new_id>/transcript.vtt` returns the existing artifact (since artifacts are keyed by `content_hash`). |

### 3.3 Modify-in-place (TC3)

| Test | What it pins |
|---|---|
| `TestEditByteCreatesNewRow` | Edit one byte of a fixture file; rescan inserts a new `videos` row with a different hash; old row gets `superseded_by` pointing at it; old row's state flips to `superseded` (lowercase per plan-24-03 CHECK). |
| `TestSupersededRowPreservedUntilGc` | History: `videos WHERE superseded_by IS NOT NULL` returns the modified file's old row. |

### 3.4 Boundaries (AC4)

| Test | What it pins |
|---|---|
| `TestExactly8MiB` (EC1) | A file of exactly 8 MiB hashes the same whether interpreted as "head ‖ tail ‖ size" or "whole file" (the head and tail abut perfectly; the bytes covered are identical). The size-suffix path appends `u64_le(8 MiB)`; the whole-file path does not — but at the boundary the suffix-hash and whole-file-hash are documented to match by construction (test fixture pins). |
| `TestSmallFileWholeHash` (EC2) | A 1 MiB file is hashed end-to-end; the size suffix is **not** appended on the small-file branch (story EC2). The hash equals `BLAKE3(whole_file_bytes)` exactly. |
| `TestSparseFileHoleAtEnd` (EC3) | A sparse file with a 4 MiB hole at the end produces a hash matching one constructed by reading 4 MiB of zeros for the tail; consistent with what the OS returns on POSIX systems. **POSIX-only**: on Windows, sparse semantics differ; the test is gated on `sys.platform != "win32"` and Windows behaviour is documented as "the hole reads as whatever the FS returns; identity is still stable across reads on the same FS". |
| `TestPartialCopyEdgeCase` | `cp` interrupted mid-write so the destination has the source's first half + zeros for the rest; rescan computes the partial-file hash, which differs from the source. The scanner's mtime-stable-for-≥-60 s gate prevents promoting this to canonical until the copy completes; once finished, the rescan converges to the same hash as the source. |
| `TestModifyInPlaceSameMtimeChangesHash` | `cp --reflink` style scenario: identical bytes → identical hash regardless of mtime. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| File exactly 8 MiB (EC1) | Documented as identical to "hash whole file once" (head + tail covers all bytes). Test pins. | `TestExactly8MiB` |
| File smaller than 4 MiB (EC2) | Whole-file hash. The size suffix is not added on this path. | `TestSmallFileWholeHash` |
| Sparse file with hole at end (EC3) | The hole reads as zeros; the hash reflects that. Documented. | `TestSparseFileHoleAtEnd` |
| Symlinks | Story 23.5's canonicalizer resolves before hashing; we hash the resolved file's bytes. Two paths whose targets are the same file produce the same hash. | `TestSymlinkPaths` |
| Hardlinks | Same file, different paths; resolve produces an `Update` (move-like behavior); rescan converges to one row whose path is the most-recently-discovered hardlink target. | `TestHardlinks` |
| Mtime change but bytes equal | Short-circuit fails (mtime differs); we hash; hash is identical; resolve returns `Reuse` with the existing row's id. The `mtime` column is updated to skip the next hash. | `TestModifyMtimeOnly` |
| Touch during scan (file truncated then refilled mid-hash) | The hashed bytes are inconsistent; the next scan recomputes and may transition to `Supersede`. The scanner gates on mtime stability for ≥ 60 s before hashing to reduce occurrences. | `TestRaceTruncateRefill` |
| 0-byte file | `_validate` raises `IdentityError`; the scanner logs and skips. | `TestEmptyFileSkipped` |
| Scan with stale stat | Linux stat caches; the scanner uses `os.stat` not `lstat` and the OS guarantees fresh data. Documented. | n/a |
| Two different files with same first 4 MiB and last 4 MiB and same size | Theoretically possible but cryptographically negligible (BLAKE3 + 8 byte size suffix). Documented as "the dedupe rate is bounded by hash collision probability." | n/a |
| Cross-library move | Two libraries with different roots; the hash is library-agnostic. The row's `library_id` updates as part of `Update`. | `TestMoveBetweenLibrariesUpdates` |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `blake3` | latest | Hash. |
| `os.stat`, `pathlib` | stdlib | size + mtime. |

## 6. Acceptance checklist

**Function**
- [ ] `identity.compute(path)` matches the documented formula.
- [ ] 8 MiB boundary handled identically to whole-file.
- [ ] Files < 8 MiB hashed end-to-end (no size suffix).

**Resolve**
- [ ] `(path, size, mtime)` short-circuits.
- [ ] Move/rename path → `Update`.
- [ ] Modify-in-place → `Supersede` with old row preserved.

**Schema**
- [ ] `videos.superseded_by` FK present (idempotent: added by either `0050_schema_canonicalization.sql` or this plan's `0060_videos_supersede.sql`, whichever runs first).
- [ ] Lowercase `superseded` state used for old rows (per plan-24-03 CHECK).
- [ ] `videos.mtime` is `TIMESTAMPTZ` and the resolver converts `os.stat().st_mtime` to that type for comparison.

**Tests**
- [ ] All §3 boundary tests pass.
