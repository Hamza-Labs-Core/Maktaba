# Implementation Plan — Story 9.4 Content-Hash Dedup

> Companion to [story-09-04-content-hash-dedup.md](story-09-04-content-hash-dedup.md).
> The story states *what* and *why*; this plan states *how*.
> Builds on Stories 9.1–9.3 and on the `videos.content_hash UNIQUE`
> decision in [architecture.md §8.1](../../architecture.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Hash primitive | BLAKE3 over `[0..4 MiB) + [size-4 MiB..size) + size_le_8B`. The mixed tail blocks rules out same-prefix-but-truncated collisions; the size suffix locks identity to the file length. Single source: `pipeline/src/maktaba_pipeline/hash/blake3_4mib.py`. |
| Path canonicalization | `pipeline/src/maktaba_pipeline/path_safety.py::canonicalize_within_roots(path, roots)`. Resolves `..`, symlinks, and trailing slashes; raises `PathOutOfRootError` if the canonical path is not a subpath of any registered root. Called *before* any read of bytes (AC-1 security). |
| Uniqueness enforcement | DB-level `UNIQUE` on `videos.content_hash` (architecture §8.1: `content_hash TEXT NOT NULL UNIQUE`, hex-encoded BLAKE3 — 64 chars). The unique index is the durable guard; application code uses `INSERT … ON CONFLICT (content_hash) DO UPDATE SET path=EXCLUDED.path RETURNING (xmax = 0) AS inserted` from the same statement. |
| Duplicate audit | Architecture §8.1 keeps the *first-seen* row and audits the second with `category='library', event='duplicate-detected'`. Implemented via the same `INSERT ON CONFLICT` clause + a side-effect call to the audit logger (Story 9.17). |
| Network filesystem timeout | `hash_timeout_sec` (default 30 s) wraps `aiofiles.open(...).read()` in `asyncio.wait_for`. On timeout, the file is skipped with `error.code='hash-timeout'` and a metric. |
| Out of scope | The `videos.content_hash UNIQUE` migration itself (already in architecture); the audit table (Story 9.17); the per-library uniqueness future-revision discussion (out of scope per AC-2). |

## 1. Architecture diagram

```
   ┌────────────────────────────────────────────────────────────────┐
   │  Caller: scan worker / sweep runner / watcher                  │
   │     (path, expected_size, library_roots) →                     │
   └────────────────────┬───────────────────────────────────────────┘
                        │
                        ▼
   ┌────────────────────────────────────────────────────────────────┐
   │  path_safety.canonicalize_within_roots(path, roots)            │
   │    1. resolve symlinks, "..", trailing slash                    │
   │    2. for r in roots: if str(p).startswith(str(r) + sep)        │
   │         return canonical                                        │
   │    3. raise PathOutOfRootError                                  │
   └────────────────────┬───────────────────────────────────────────┘
                        │
                        ▼
   ┌────────────────────────────────────────────────────────────────┐
   │  hash.blake3_4mib(path, size, *, timeout_sec=30)               │
   │    if size <= 8 MiB:                                            │
   │        digest = BLAKE3(open.read())  # whole file                │
   │    else:                                                        │
   │        h = BLAKE3()                                              │
   │        with open(path, 'rb') as f:                              │
   │            h.update(f.read(4 MiB))                              │
   │            f.seek(size - 4 MiB)                                 │
   │            h.update(f.read(4 MiB))                              │
   │            h.update(size.to_bytes(8, 'little'))                 │
   │    return h.hexdigest()   # 64 hex chars (TEXT in DB §8.1)      │
   └────────────────────┬───────────────────────────────────────────┘
                        │
                        ▼
   ┌────────────────────────────────────────────────────────────────┐
   │  videos.upsert_with_hash(library_id, path, size_bytes, hash)   │
   │    INSERT INTO videos (id, library_id, path, size_bytes,        │
   │                        content_hash)                            │
   │    VALUES ($1,$2,$3,$4,$5)                                      │
   │    ON CONFLICT (content_hash) DO UPDATE                         │
   │       SET path = EXCLUDED.path,                                 │
   │           updated_at = now()                                    │
   │       WHERE videos.path <> EXCLUDED.path                        │
   │    RETURNING id, (xmax = 0) AS inserted, path AS final_path     │
   │                                                                  │
   │    if not inserted and original_path != incoming_path:          │
   │        audit(category='library', event='duplicate-detected',    │
   │              payload={path, original_video_id, original_path})  │
   └────────────────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/hash/__init__.py` | Re-export `blake3_4mib`, `HashTimeoutError`. |
| `pipeline/src/maktaba_pipeline/hash/blake3_4mib.py` | The hashing function and its async wrapper. |
| `pipeline/src/maktaba_pipeline/path_safety.py` | `canonicalize_within_roots`, `PathOutOfRootError`. |
| `pipeline/src/maktaba_pipeline/db/videos.py` | `upsert_with_hash` and the `audit_duplicate` helper. |
| `pipeline/tests/hash/test_blake3_4mib.py` | Unit tests per §6.1. |
| `pipeline/tests/path/test_path_safety.py` | Unit tests per §6.2. |
| `pipeline/tests/db/test_videos_upsert.py` | Integration tests per §6.3. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/pyproject.toml` | Add `blake3>=0.4` (Rust-backed Python binding). |
| `pipeline/src/maktaba_pipeline/sweep/sweep_runner.py` | Replace the placeholder `blake3_4mib` import with the real one. |
| `pipeline/src/maktaba_pipeline/scan/scan_worker.py` | Use `videos.upsert_with_hash` instead of plain `INSERT`. |
| `shared/db/migrations/0033_videos_content_hash.sql` | Idempotent — adds the `content_hash TEXT NOT NULL UNIQUE` column *if* not already present (architecture §8.1 added it; the migration is defensive). |
| `shared/db/queries/videos.sql` | Add `UpsertVideoByHash` (Go-side analogue used by API write paths). |

### 2.3 Type definitions

```python
# pipeline/src/maktaba_pipeline/hash/blake3_4mib.py
from __future__ import annotations
from pathlib import Path

EIGHT_MIB = 8 * 1024 * 1024
FOUR_MIB  = 4 * 1024 * 1024
HASH_SIZE_BYTES = 32       # BLAKE3 default
HASH_HEX_LEN    = 64       # hex-encoded BLAKE3 stored as TEXT (architecture §8.1)


class HashTimeoutError(Exception):
    """Raised when reading the hash bytes exceeds hash_timeout_sec."""
```

```python
# pipeline/src/maktaba_pipeline/path_safety.py
class PathOutOfRootError(ValueError):
    code = "path-out-of-root"


def canonicalize_within_roots(path: str | Path,
                              roots: list[Path]) -> Path: ...
```

```python
# pipeline/src/maktaba_pipeline/db/videos.py
from dataclasses import dataclass
from uuid import UUID

@dataclass(slots=True, frozen=True)
class UpsertResult:
    id: UUID
    inserted: bool
    final_path: str
```

### 2.4 Function signatures

```python
# pipeline/src/maktaba_pipeline/hash/blake3_4mib.py
def blake3_4mib(path: Path, size: int) -> str:
    """Synchronous; intended for asyncio.to_thread(...). Returns the
    64-char lowercase hex digest (architecture §8.1 stores TEXT)."""

async def blake3_4mib_async(
    path: Path, size: int, *, timeout_sec: float = 30.0
) -> str:
    """Wraps the sync version in to_thread + wait_for(timeout_sec).
    Returns hex digest. Raises HashTimeoutError on timeout."""
```

```python
# pipeline/src/maktaba_pipeline/db/videos.py
async def upsert_with_hash(
    db, *,
    library_id: UUID,
    path: str,
    size_bytes: int,
    content_hash: str,                 # 64-char hex BLAKE3 (architecture §8.1)
    audit: AuditWriter | None = None,
) -> UpsertResult:
    """INSERT … ON CONFLICT (content_hash) DO UPDATE.

    Returns `inserted` from the `RETURNING (xmax = 0) AS inserted` clause
    of the same statement (no separate probe). On conflict where the
    original path differs from `path`, writes a
    `category='library', event='duplicate-detected'` audit row.
    """
```

## 3. Database

The architecture-level `videos.content_hash` column already exists as
`TEXT NOT NULL UNIQUE` (architecture §8.1, line 1307). This story's
defensive migration:

`shared/db/migrations/0033_videos_content_hash.sql`

```sql
-- +goose Up
-- +goose StatementBegin

-- Defensive: add the column + unique index if a deployment was set up
-- before architecture §8.1 was finalized. New deployments are no-ops.
-- content_hash is the 64-char lowercase hex of a BLAKE3 digest.
ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS content_hash TEXT;

-- 64 hex chars (32-byte BLAKE3 digest).
ALTER TABLE videos
    ADD CONSTRAINT videos_content_hash_hex_chk
    CHECK (content_hash IS NULL
           OR (char_length(content_hash) = 64
               AND content_hash ~ '^[0-9a-f]{64}$'));

-- Architecture §8.1 has the column NOT NULL; this defensive migration
-- leaves NULLs allowed so legacy rows can backfill before NOT NULL is
-- promoted. The unique index does not need a partial WHERE clause once
-- the NOT NULL invariant holds; until then we keep it partial.
CREATE UNIQUE INDEX IF NOT EXISTS videos_content_hash_unique
    ON videos (content_hash)
    WHERE content_hash IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Intentional no-op; rolling back the unique index is destructive.
-- +goose StatementEnd
```

## 4. Code scaffolding

### 4.1 `blake3_4mib`

```python
# pipeline/src/maktaba_pipeline/hash/blake3_4mib.py
import asyncio
from pathlib import Path

from blake3 import blake3 as _blake3


def blake3_4mib(path: Path, size: int) -> str:
    h = _blake3()
    with open(path, "rb") as f:
        if size <= EIGHT_MIB:
            h.update(f.read())
        else:
            h.update(f.read(FOUR_MIB))
            f.seek(size - FOUR_MIB)
            tail = f.read(FOUR_MIB)
            h.update(tail)               # may be < FOUR_MIB on short read
            h.update(size.to_bytes(8, "little"))
    # architecture §8.1: content_hash TEXT — store hex.
    return h.hexdigest()


async def blake3_4mib_async(
    path: Path, size: int, *, timeout_sec: float = 30.0
) -> str:
    try:
        return await asyncio.wait_for(
            asyncio.to_thread(blake3_4mib, path, size),
            timeout=timeout_sec,
        )
    except asyncio.TimeoutError as e:
        raise HashTimeoutError(
            f"hash for {path} (size {size}) timed out after {timeout_sec}s"
        ) from e
```

The Python `blake3` package is Rust-backed and runs at ~3 GB/s
single-threaded on Apple Silicon, well above the 8 MiB read budget for
AC-3.

### 4.2 `canonicalize_within_roots`

```python
# pipeline/src/maktaba_pipeline/path_safety.py
import os
from pathlib import Path


class PathOutOfRootError(ValueError):
    code = "path-out-of-root"


def canonicalize_within_roots(path, roots):
    p = Path(os.path.realpath(str(path)))
    for r in roots:
        rcanon = Path(os.path.realpath(str(r)))
        # strict subpath check; equality also counts.
        try:
            p.relative_to(rcanon)
            return p
        except ValueError:
            continue
    raise PathOutOfRootError(f"{path!s} is outside all registered roots")
```

### 4.3 `upsert_with_hash`

```python
# pipeline/src/maktaba_pipeline/db/videos.py
import logging
from uuid import uuid4

log = logging.getLogger(__name__)

# content_hash is TEXT NOT NULL UNIQUE (architecture §8.1) — no partial
# WHERE clause needed on the ON CONFLICT target.
_UPSERT_PG = """
INSERT INTO videos (id, library_id, path, size_bytes, content_hash, state,
                    created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, 'discovered', now(), now())
ON CONFLICT (content_hash)
DO UPDATE SET path = EXCLUDED.path, updated_at = now()
   WHERE videos.path <> EXCLUDED.path
RETURNING id, (xmax = 0) AS inserted, path AS final_path
"""


async def upsert_with_hash(db, *, library_id, path, size_bytes,
                           content_hash, audit=None) -> UpsertResult:
    new_id = uuid4()
    row = await db.fetchrow(
        _UPSERT_PG, new_id, library_id, path, size_bytes, content_hash,
    )
    # `inserted` comes back from RETURNING (xmax = 0) on the same
    # statement — single round-trip, no separate probe.
    res = UpsertResult(id=row["id"],
                       inserted=row["inserted"],
                       final_path=row["final_path"])
    if not res.inserted and audit is not None and res.final_path != path:
        # Path changed → DO UPDATE fired → audit. Non-blocking writer
        # (Story 9.17 contract: Write never blocks, never propagates).
        audit.Write(
            category="library",
            event="duplicate-detected",
            library_id=library_id,
            video_id=res.id,
            payload={
                "path": path,
                "original_path": res.final_path,
                "original_video_id": str(res.id),
            },
        )
        log.warning("duplicate_detected video_id=%s path=%s original=%s",
                    res.id, path, res.final_path)
    return res
```

### 4.4 Go side — read-only mirror

```go
// shared/db/queries/videos.sql
-- name: UpsertVideoByHash :one
INSERT INTO videos (id, library_id, path, size_bytes, content_hash, state)
VALUES ($1, $2, $3, $4, $5, 'discovered')
ON CONFLICT (content_hash)
DO UPDATE SET path = EXCLUDED.path, updated_at = now()
   WHERE videos.path <> EXCLUDED.path
RETURNING id, (xmax = 0) AS inserted, path AS final_path;

-- name: GetVideoByHash :one
SELECT * FROM videos WHERE content_hash = $1;
```

The API never originates a hash — only the Pipeline does. The Go-side
query exists so the `/api/videos?content_hash=...` lookup path (Epic 7)
can find dupes by hash without joining through path.

## 5. Test plan

### 5.1 Hash unit tests (`test_blake3_4mib.py`)

| Test | What it pins |
|---|---|
| `test_identical_files_same_hash` | Two byte-identical 100 MiB files → same 64-char hex digest. |
| `test_size_below_8mib_hashes_full_file` | 1 MiB file → result equals `blake3(file_bytes + size_le)`. |
| `test_size_exactly_8mib_hashes_full_file` | 8 MiB file → also full-file path (the `<= EIGHT_MIB` branch). |
| `test_size_above_8mib_uses_endpoints` | 100 MiB file → equals `blake3(first_4MiB + last_4MiB + size_le)`; verified with a hand-rolled reference implementation. |
| `test_files_differing_only_in_middle_have_same_hash` | Two 100 MiB files that differ only in bytes `[10 MiB..90 MiB)` → **same** digest. The story documents this as a known property; the test proves it. |
| `test_truncated_tail_short_read` | A file whose last 4 MiB straddles EOF (caller gives wrong size) → hashes the bytes available; deterministic across two calls; doesn't crash. |
| `test_size_lock_changes_hash` | Same first/last 4 MiB, different stated sizes → different digests. |
| `test_async_timeout_raises` | `blake3_4mib_async` against a /dev/zero-style fixture (slow read simulated by stubbed `to_thread`) raises `HashTimeoutError` after `timeout_sec`. |
| `test_async_returns_under_budget` | A 30 GiB sparse file on tmpfs hashes in ≤ 100 ms (AC-3). Skipped on CI without tmpfs; gated by `pytest.mark.perf`. |

### 5.2 Path-safety unit tests (`test_path_safety.py`)

| Test | What it pins |
|---|---|
| `test_within_root_accepted` | `/mnt/lib/a/b.mp4` with root `/mnt/lib` → returns absolute canonical. |
| `test_outside_root_rejected` | `/etc/passwd` with root `/mnt/lib` → `PathOutOfRootError`. |
| `test_dotdot_resolved_then_checked` | `/mnt/lib/../etc/passwd` → resolves to `/etc/passwd` → rejected. |
| `test_symlink_inside_root_followed` | `/mnt/lib/inner -> /mnt/lib/real`; calling with the symlink path returns the real path; both inside the root → accepted. |
| `test_symlink_escapes_root_rejected` | `/mnt/lib/escape -> /etc`; `/mnt/lib/escape/passwd` resolves to `/etc/passwd` → rejected. |
| `test_trailing_slash_canonicalized` | `/mnt/lib/a.mp4/` → returns `/mnt/lib/a.mp4` (no trailing slash). |
| `test_root_itself_accepted` | `/mnt/lib` with root `/mnt/lib` → returns it. |
| `test_root_with_trailing_slash_treated_equal` | `/mnt/lib/` ≡ `/mnt/lib`. |

### 5.3 `upsert_with_hash` integration tests (`test_videos_upsert.py`)

| Test | What it pins |
|---|---|
| `test_first_insert_returns_inserted_true` | Empty table; one upsert → `inserted=True`, `final_path=incoming_path`. |
| `test_second_insert_same_hash_different_path_updates_path` | First insert at `/a`; second at `/b` with same hash → `inserted=False`, `final_path='/b'`; only one row in table. AC-2. |
| `test_second_insert_same_hash_same_path_is_noop` | Both calls with the same `path` → second sets `inserted=False`; `path` unchanged; no audit. |
| `test_audit_row_written_on_path_change` | Verify exactly one `audit_log` row with `category='library', event='duplicate-detected'`, payload contains both paths. |
| `test_no_audit_when_audit_writer_none` | Pass `audit=None` → no audit row, no error. |
| `test_concurrent_upserts_serialize_via_unique_index` | 8 concurrent upserts of the same hash → exactly one row; only one returns `inserted=True`. |
| `test_path_out_of_root_rejected_before_hash` | Caller path is outside roots → `PathOutOfRootError` raised; no DB write; no file read (proven by absence of file-stat side effects). AC-1 security. |
| `test_hash_collision_log_metric` | If we synthesize two genuinely different files that produce the same 4MiB+4MiB+size hash (we use a known degenerate fixture from `test_files_differing_only_in_middle_have_same_hash`), the second upsert lands on the same row; the metric `maktaba_hash_collision_total` increments. Documents that the user can force-rehash to disambiguate. |

### 5.4 Performance gate

| Test | Target |
|---|---|
| `test_hash_30gib_under_100ms_local_ssd` | Sparse fixture on tmpfs; AC-3 budget. |
| `test_hash_timeout_against_slow_fs` | Simulate a 60 s read; assert `HashTimeoutError` after default 30 s. |

### 5.5 Cross-dialect parity

`test_videos_upsert_pg` and `test_videos_upsert_sqlite` parametrized on
the same fixtures. The `(xmax = 0)` Postgres trick has no SQLite
analogue — for SQLite we use `RETURNING last_insert_rowid() = id`
indirection: every successful insert sets `last_insert_rowid`, conflict
resolution does not. The SQLite query reads:

```sql
INSERT INTO videos (id, library_id, path, size_bytes, content_hash, state)
VALUES (?, ?, ?, ?, ?, 'discovered')
ON CONFLICT (content_hash)
DO UPDATE SET path = EXCLUDED.path, updated_at = CURRENT_TIMESTAMP
   WHERE videos.path <> EXCLUDED.path
RETURNING id, path AS final_path;
```

The `inserted` flag is computed at the application layer by checking
whether the returned `id` equals the freshly-generated UUID.

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| File exactly 8 MiB | Hashed in full (`size <= EIGHT_MIB` branch). | `test_size_exactly_8mib_hashes_full_file` |
| File between 4 and 8 MiB | Same as above — full-file hash. | covered by `test_size_below_8mib_hashes_full_file` (parameterized 1, 4, 5, 8 MiB). |
| Truncated read on EOF in last 4 MiB | The function reads what's available; result is deterministic across calls; size mismatch with reality is the *caller's* problem (sweep validates st.st_size before passing in). | `test_truncated_tail_short_read` |
| BLAKE3 collision | Astronomically unlikely; the unique index keeps the first-seen row; the metric `maktaba_hash_collision_total` increments; user can `?rehash=true`. | `test_hash_collision_log_metric` |
| Path outside any root | `PathOutOfRootError` raised before any I/O. | `test_outside_root_rejected` + `test_path_out_of_root_rejected_before_hash` |
| Symlink inside the root | Followed and accepted (the `realpath` checks containment). | `test_symlink_inside_root_followed` |
| Symlink escaping root | Rejected. | `test_symlink_escapes_root_rejected` |
| NFS read takes 60 s | `blake3_4mib_async` with default `timeout_sec=30` raises `HashTimeoutError`; caller (sweep) records the error in `library_sweeps.errors_jsonb` and skips the file. | `test_async_timeout_raises` |
| Same content under two paths simultaneously on disk | The second upsert updates `path` to the most-recently-seen one; the audit captures both. The first path now points to a video row whose `path` is the second one — but the file still exists at the first path. The next sweep sees the first path as new (it doesn't match any catalog `path`), hashes it, conflicts on the unique index again, and the audit fires again. The audit table records the back-and-forth. To break the loop, the user must delete one. | `test_two_files_same_hash_audit_records_both` |
| Per-library uniqueness future revision | Out of scope. The story explicitly notes the ACs assume the architecture-level global-unique decision and would need a revision otherwise. | Documented in §8 of this plan. |
| Empty file | Size 0 → hashed as empty BLAKE3 + 8 zero bytes for size; deterministic. Tiny but valid. | `test_zero_byte_file` |

## 7. Configuration knobs

| Key | Default | Effect |
|---|---|---|
| `hash_timeout_sec` | 30.0 | Wall-clock timeout for `blake3_4mib_async`. |
| `hash_chunk_size_4mib_default` | 4 (MiB) | Forward-compat: revisable to 8 MiB if collision rates require. Schema-validated at boot but expected to remain 4. |

## 8. Future work

- Per-library uniqueness (instead of global) would require relaxing the
  `UNIQUE` index to `(library_id, content_hash)` — and a careful migration
  that handles cross-library duplicates. Out of scope per AC-2.
- Rolling the chunk size to 8 MiB would invalidate every stored hash;
  any change is a schema migration with a recompute job. Defer.

## 9. Dependencies

| Dep | Version | Why |
|---|---|---|
| `blake3` | ≥ 0.4 | Rust-backed; 3 GB/s; 32-byte digest. |
| `aiofiles` | already pinned | Not used inside `blake3_4mib` (which is sync, run in `to_thread`); reserved for the timeout wrapper if we move to async file I/O. |

## 10. Acceptance checklist

**Code**
- [ ] `pipeline/src/maktaba_pipeline/hash/blake3_4mib.py` exposes `blake3_4mib`, `blake3_4mib_async`, `HashTimeoutError`.
- [ ] `pipeline/src/maktaba_pipeline/path_safety.py` exposes `canonicalize_within_roots`, `PathOutOfRootError`.
- [ ] `pipeline/src/maktaba_pipeline/db/videos.py` exposes `upsert_with_hash`, `UpsertResult`.
- [ ] `shared/db/queries/videos.sql` regenerates `UpsertVideoByHash` (Go-side mirror).

**Migration**
- [ ] `0033_videos_content_hash.sql` is a no-op on deployments that already have the column + unique index; on legacy ones it adds them.
- [ ] CHECK on `char_length(content_hash) = 64 AND content_hash ~ '^[0-9a-f]{64}$'` rejects malformed values.

**Behaviour (story acceptance criteria)**
- [ ] AC-1: a 100 MiB file hashes deterministically with the documented schema; off-root paths raise `PathOutOfRootError`.
- [ ] AC-2: a second file with the same hash updates `videos.path` to the new path and writes an audit row; only one `videos` row remains.
- [ ] AC-3: 30 GiB local-SSD file hashes in ≤ 100 ms; NFS read past `hash_timeout_sec` raises `HashTimeoutError`.

**Observability**
- [ ] Counter `maktaba_hash_files_total{outcome=ok|timeout|out_of_root}`.
- [ ] Counter `maktaba_hash_collision_total`.
- [ ] Histogram `maktaba_hash_duration_seconds{size_bucket}`.

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.4.
- [ ] `docs/operations/hashing.md` documents the chunk schema, the perf budget, and the `?rehash=true` escape hatch.
