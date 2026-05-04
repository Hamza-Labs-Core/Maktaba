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
| Identity function | `BLAKE3(first 4 MiB ‖ last 4 MiB ‖ u64_le(size))` for files ≥ 8 MiB; whole-file hash otherwise. |
| Implementation | `pipeline/src/maktaba_pipeline/domain/identity.py`. Pure function; no I/O assumptions beyond the file path. |
| Reuse on rescan | `videos.path == p AND videos.size == st_size AND videos.mtime == st_mtime` short-circuits hashing; the stored hash is reused. |
| Move/rename | Path-changed-but-hash-matches → update path, no re-process. |
| Copy | Same hash → already-ready row served. |
| Modify-in-place | Hash differs → new row inserted; old row preserved with `superseded_by` link until GC. |
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
    when the (path, size, mtime) short-circuit fails.
    """
    st = path.stat()
    row = await db.fetch_one(
        "SELECT id, content_hash, size, mtime FROM videos WHERE path=$1",
        str(path))
    if row and row["size"] == st.st_size and row["mtime"] == st.st_mtime_ns:
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
            await self.db.execute(
                "UPDATE videos SET path=$2, mtime=$3 WHERE id=$1",
                video_id, str(path), path.stat().st_mtime_ns)
        case Supersede(old_id):
            new_id = await self._insert_new_video(path, content_hash)
            await self.db.execute(
                "UPDATE videos SET superseded_by=$1, state='SUPERSEDED' WHERE id=$2",
                new_id, old_id)
            await self._enqueue_pipeline(new_id)
        case InsertNew():
            new_id = await self._insert_new_video(path, content_hash)
            await self._enqueue_pipeline(new_id)
```

The `superseded_by` field plus the `SUPERSEDED` state (already in the
architecture §3 enum) makes the relationship queryable for history.

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
| `TestEditByteCreatesNewRow` | Edit one byte of a fixture file; rescan inserts a new `videos` row with a different hash; old row gets `superseded_by` pointing at it; old row's state flips to `SUPERSEDED`. |
| `TestSupersededRowPreservedUntilGc` | History: `videos WHERE superseded_by IS NOT NULL` returns the modified file's old row. |

### 3.4 Boundaries (AC4)

| Test | What it pins |
|---|---|
| `TestExactly8MiB` (EC1) | A file of exactly 8 MiB hashes the same whether interpreted as "head ‖ tail ‖ size" or "whole file" (the head and tail abut perfectly; the bytes covered are identical). |
| `TestSmallFileWholeHash` (EC2) | A 1 MiB file is hashed end-to-end; the size suffix is *not* appended (only sampled paths use it). |
| `TestSparseFileHoleAtEnd` (EC3) | A sparse file with a 4 MiB hole at the end produces a hash matching one constructed by reading 4 MiB of zeros for the tail; consistent with what the OS returns. |
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
- [ ] `videos.superseded_by` column present.
- [ ] `SUPERSEDED` state used for old rows.

**Tests**
- [ ] All §3 boundary tests pass.
