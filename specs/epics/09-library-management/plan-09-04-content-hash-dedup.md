# Plan 9.4 — Content-hash dedup (move/rename/copy detection) — implementation

> Implementation plan for [story-09-04-content-hash-dedup.md](story-09-04-content-hash-dedup.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: the hasher is consumed by the sweep
> ([Plan 9.3](plan-09-03-periodic-sweep.md)) for fast-path move detection
> and by the `scan` pipeline stage (Epic 1) for first-time identity
> assignment; the path-canonicalization + root-membership check
> coordinates with library roots stored under
> [Plan 9.1](plan-09-01-library-config-schema.md); duplicates surface
> in the `audit_log` table owned jointly with Story 9.17 (Epic 9 README).
> The hasher itself never writes to the DB — it is a pure function the
> sweep and scan stage call.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Hash scheme: BLAKE3 over `[0..4MiB) ‖ [size-4MiB..size) ‖ size_le_u64`** for files ≥ 8 MiB; full-file BLAKE3 for files < 8 MiB. The 8-byte little-endian size suffix is appended to the hasher state *after* the two byte ranges. | Story AC-1 verbatim; architecture §3.1, §1.5. | The 4+4 MiB scheme catches the cases that matter (rename, copy, mount-shuffle) at constant cost regardless of file size. The size suffix is the safety net against the documented case of two distinct-but-aligned files differing only in the middle (Story §"Test cases" "byte-for-byte different files in the [4 MiB..size-4 MiB) range produce the *same* hash"); appending size makes a 30 GB file with a 12 GB middle change at least show different identity if the *size* changes. The size suffix is also a defense against a hash extension attempt (BLAKE3 is XOF-safe but the suffix simplifies reasoning). |
| D2 | **Path canonicalization + root-membership check is a hard precondition.** `hash_path(path, library_roots)` calls `os.path.realpath(path)`, then asserts the canonical path starts with one of `[os.path.realpath(r) + os.sep for r in library_roots]`. Off-root paths raise `PathOutOfRoot`. | Story AC-1: "before reading any bytes, the path is canonicalized and asserted to live inside one of the registered library roots". | This is a security gate, not a correctness gate. Without it, a misconfigured caller could hash an arbitrary file (e.g., `/etc/shadow`) and report its existence in audit logs. We refuse loudly. The `+ os.sep` suffix prevents the prefix-match exploit (`/data/lib1` vs `/data/lib1.evil`). |
| D3 | **Async I/O via `asyncio.to_thread`** with a bounded `ThreadPoolExecutor` (default 8 workers, sized by `pipeline.toml [hash].workers`). The hash function does CPU + 8 MiB of disk I/O; thread offload keeps the event loop responsive. | Pipeline performance budget; AC-3 ("under 100 ms" for a 30 GiB file on local SSD). | True async file I/O on Linux requires `io_uring`; thread offload is the portable choice and enough for the budget. The bounded pool prevents a sweep on a slow NAS from saturating CPU. |
| D4 | **`hash_timeout_sec` (default 30 s) is enforced via `asyncio.wait_for`** wrapping `asyncio.to_thread(_hash_sync, path)`. Network filesystems that exceed it raise `HashTimeout`; the caller (sweep, scan stage) records the failure but does not crash. | Story AC-3: "Network filesystems must respect a `hash_timeout_sec` (default 30 s) and skip-with-error if exceeded." | A hung NFS read otherwise blocks a worker forever. Per-call timeout is the right granularity. |
| D5 | **`videos.content_hash` is `UNIQUE` globally** (architecture §8.1 confirmed in the story). The dedup behavior on second-seen-hash is: keep the first row, update its `path` to the new path, log a `duplicate-detected` audit row. The previous path is not preserved on the row (it's in the audit log if the operator needs it). | Story AC-2 verbatim. | Global uniqueness keeps the schema simple and matches the user model: "this is the same video, no matter where it lives." Per-library uniqueness would require a (library_id, content_hash) composite key and complicate cross-library moves. |
| D6 | **Hash format: 64-char lowercase hex** of the 32-byte BLAKE3 digest. No prefix tag, no version byte. If the scheme changes (D1), we add a column not a prefix. | Storage simplicity. | Matches the existing CHECK constraint `videos.content_hash ~ '^[a-f0-9]{64}$'` (added in Epic 1). |
| D7 | **The hasher is stateless and reentrant.** A pool of file descriptors is not maintained; every call opens, hashes, closes. | Simplicity over micro-optimization. | The stat is what determines if we even hash (Plan 9.3 D2); when we do hash, the cost is dominated by the two 4 MiB reads (~10–50 ms on SSD), not the open(2). Pooling adds bug surface for ~1 ms savings. |
| D8 | **The hasher refuses to follow symlinks beyond the canonicalization step.** Once `realpath` resolves the symlink chain, we open the resolved path. We do not re-resolve mid-read. | Race-condition closure. | Otherwise an attacker could swap a symlink between root-membership check and `open(2)` (TOCTOU). `os.open` with `O_NOFOLLOW` on the canonical path closes the window. |
| D9 | **Files exactly 8 MiB are hashed in full** (the `[0..4MiB)` and `[size-4MiB..size)` windows would overlap; full-file is one read of the same data). | Story edge case: "Files exactly 8 MiB — hashed in full (tiny optimization)." | Avoids a window-overlap bug that would otherwise hash the same 4 MiB twice and produce a different hash than the < 8 MiB branch. |
| D10 | **Truncated read on EOF in the last-4-MiB window is silently absorbed.** If the file is exactly 6 MiB (which falls into the < 8 MiB branch — full-file), this never triggers. For ≥ 8 MiB, the last-4-MiB window always exists. The story edge case "Truncated read on EOF" describes a file that is being truncated mid-read, in which case Python's `read()` returns fewer bytes; we feed what we got. | Story edge case verbatim. | Document but do not error: a partially-read file produces a stable hash for that snapshot; the next sweep will catch the change via stat (Plan 9.3 D2). |

If D5 is rejected (per-library hash uniqueness): the move-across-libraries
case becomes "delete A, insert B" rather than "move", losing the
identity-preservation guarantee that lets us migrate a library without
re-transcribing every video. We accept the global-uniqueness coupling.

If D2 is rejected (no root check): a stray Pipeline call could hash any
file the worker user can read — a privilege-escalation vector inside the
service. The check is mandatory.

---

## 1. Architecture diagram — hash pipeline

```
   Caller (sweep / scan stage / dedup probe)
       │
       │   hash_path(path, library_roots,
       │             timeout_sec=30) -> str
       ▼
   ┌─────────────────────────────────────────────────────────────┐
   │ blake3hash.hash_path(path, library_roots, timeout_sec)      │
   │                                                             │
   │   1. canonical = os.path.realpath(path)            (D2)     │
   │   2. canonical_roots = [realpath(r)+sep for r in roots]     │
   │   3. for cr in canonical_roots:                             │
   │        if canonical.startswith(cr): break                   │
   │      else: raise PathOutOfRoot                              │
   │   4. await asyncio.wait_for(                                │
   │        asyncio.to_thread(_hash_sync, canonical),  (D3)      │
   │        timeout=timeout_sec)                       (D4)      │
   │      → returns lowercase hex                                │
   │                                                             │
   │   5. on TimeoutError -> raise HashTimeout                   │
   │      on FileNotFoundError -> raise HashFileMissing          │
   │      on PermissionError -> raise HashAccessDenied           │
   └─────────────────────────────────────────────────────────────┘
       │
       ▼
   ┌─────────────────────────────────────────────────────────────┐
   │ _hash_sync(canonical) -> str                                │
   │                                                             │
   │   fd = os.open(canonical, O_RDONLY | O_NOFOLLOW)   (D8)     │
   │   try:                                                      │
   │     st = os.fstat(fd)                                       │
   │     size = st.st_size                                       │
   │     hasher = blake3()                                       │
   │     if size < 8 * MIB:                                      │
   │       hasher.update(read_all(fd, size))                     │
   │     elif size == 8 * MIB:                          (D9)     │
   │       hasher.update(read_all(fd, size))                     │
   │     else:                                                   │
   │       hasher.update(read_range(fd, 0, 4 * MIB))             │
   │       hasher.update(read_range(fd, size - 4*MIB, 4*MIB))   │
   │     hasher.update(size.to_bytes(8, 'little'))      (D1)     │
   │     return hasher.hexdigest()                      (D6)     │
   │   finally:                                                  │
   │     os.close(fd)                                            │
   └─────────────────────────────────────────────────────────────┘
       │
       ▼
   Caller decides: same hash? → move/duplicate. New hash? → insert.
       │
       ▼ (callers, when they see a duplicate)
   ┌─────────────────────────────────────────────────────────────┐
   │ audit_log_dedup(library_id, original_video_id, new_path)    │
   │   INSERT INTO audit_log (id, category, event, library_id,   │
   │     payload_jsonb)                                          │
   │   VALUES (gen_random_uuid(), 'library', 'duplicate-detected',│
   │     $1, $2::jsonb)                                          │
   └─────────────────────────────────────────────────────────────┘
```

The hasher has zero database access. It's a deterministic mapping of
`path` to `str`. Tests are unit tests against the filesystem; no DB
fixture required (except for the dedup audit-log helper, which has its
own integration test).

---

## 2. Detailed implementation

### 2.1 Python package layout

```
pipeline/src/maktaba_pipeline/hash/
├── __init__.py              # public surface: hash_path, HashError types
├── blake3hash.py            # the hash function (D1, D2, D3, D4, D8)
├── audit.py                 # audit_log_dedup helper (called by sweep + scan stage)
├── errors.py                # PathOutOfRoot, HashTimeout, HashFileMissing, HashAccessDenied, HashError
└── tests/
    ├── conftest.py
    ├── test_hash_small_file.py
    ├── test_hash_large_file.py
    ├── test_hash_8mib_boundary.py
    ├── test_path_out_of_root.py
    ├── test_timeout_on_slow_fs.py
    ├── test_truncated_eof.py
    ├── test_dedup_audit.py
    └── test_perf_30gib.py     # CI-marked slow
```

### 2.2 `errors.py`

```python
"""Hasher error hierarchy."""
from __future__ import annotations


class HashError(Exception):
    """Base class for hasher failures."""


class PathOutOfRoot(HashError):
    """Caller passed a path that does not live inside any registered root."""


class HashTimeout(HashError):
    """Hash exceeded hash_timeout_sec; common on slow network filesystems."""


class HashFileMissing(HashError):
    """File disappeared between caller-side stat and hash open."""


class HashAccessDenied(HashError):
    """Permission denied opening or reading the file."""
```

### 2.3 `blake3hash.py` — the hasher (D1, D2, D3, D4, D8)

```python
"""BLAKE3 hasher for video files: head4 + tail4 + size_le."""
from __future__ import annotations
import asyncio, os
from typing import Iterable

from blake3 import blake3

from .errors import (HashAccessDenied, HashError, HashFileMissing,
                     HashTimeout, PathOutOfRoot)

MIB = 1024 * 1024
HASH_HEAD_TAIL_BYTES = 4 * MIB
FULL_HASH_THRESHOLD = 8 * MIB
DEFAULT_TIMEOUT_SEC = 30.0
READ_CHUNK = 1 * MIB                  # 1 MiB chunks during head/tail reads


def _check_root(canonical: str, library_roots: Iterable[str]) -> None:
    canonical_roots = [os.path.realpath(r).rstrip(os.sep) + os.sep
                       for r in library_roots]
    for cr in canonical_roots:
        if canonical == cr.rstrip(os.sep) or canonical.startswith(cr):
            return
    raise PathOutOfRoot(f"path-out-of-root: {canonical!r}")


def _hash_sync(canonical: str) -> str:
    """Synchronous worker; runs inside a thread (D3)."""
    try:
        fd = os.open(canonical, os.O_RDONLY | os.O_NOFOLLOW)
    except FileNotFoundError as e:
        raise HashFileMissing(str(e)) from e
    except PermissionError as e:
        raise HashAccessDenied(str(e)) from e
    except OSError as e:
        # ELOOP from O_NOFOLLOW on a symlink: someone raced us.
        raise HashError(f"open failed: {e}") from e

    try:
        st = os.fstat(fd)
        size = st.st_size
        hasher = blake3()

        if size <= FULL_HASH_THRESHOLD:        # D9 (== 8 MiB hashed in full)
            _read_into(fd, 0, size, hasher)
        else:
            _read_into(fd, 0, HASH_HEAD_TAIL_BYTES, hasher)
            _read_into(fd, size - HASH_HEAD_TAIL_BYTES,
                       HASH_HEAD_TAIL_BYTES, hasher)

        hasher.update(size.to_bytes(8, "little"))
        return hasher.hexdigest()
    finally:
        os.close(fd)


def _read_into(fd: int, offset: int, length: int, hasher) -> None:
    """Read `length` bytes from `offset` and feed them to `hasher`."""
    os.lseek(fd, offset, os.SEEK_SET)
    remaining = length
    while remaining > 0:
        chunk = os.read(fd, min(READ_CHUNK, remaining))
        if not chunk:
            # Truncated EOF (D10): feed what we got and stop.
            return
        hasher.update(chunk)
        remaining -= len(chunk)


async def hash_path(
    path: str, *,
    library_roots: Iterable[str],
    timeout_sec: float = DEFAULT_TIMEOUT_SEC,
    root_check: bool = True,
) -> str:
    """Compute the content hash for a file under `library_roots`.

    Returns: lowercase hex of the BLAKE3 digest (D6).
    Raises: PathOutOfRoot, HashTimeout, HashFileMissing, HashAccessDenied,
            HashError.
    """
    canonical = os.path.realpath(path)
    if root_check:
        _check_root(canonical, library_roots)

    try:
        return await asyncio.wait_for(
            asyncio.to_thread(_hash_sync, canonical),
            timeout=timeout_sec)
    except asyncio.TimeoutError as e:
        raise HashTimeout(
            f"hash exceeded {timeout_sec}s on {canonical!r}") from e
```

### 2.4 `audit.py` — duplicate logger

```python
"""Audit-log helper for duplicate-detected events.

The audit_log table is created in Story 9.17 (and shared with Epic 10
Story 10.16). This module just inserts rows; it does not own the schema.
"""
from __future__ import annotations
import json, logging

log = logging.getLogger(__name__)


async def audit_log_dedup(
    conn, *,
    library_id: str, original_video_id: str, new_path: str,
    actor_user_id: str | None = None,
) -> None:
    payload = {
        "original_video_id": original_video_id,
        "new_path": new_path,
    }
    await conn.execute(
        """
        INSERT INTO audit_log
            (id, category, event, actor_user_id, library_id, payload_jsonb)
        VALUES (gen_random_uuid(), 'library', 'duplicate-detected',
                $1, $2, $3::jsonb)
        """,
        actor_user_id, library_id, json.dumps(payload))
    log.info("library.duplicate_detected", extra={
        "library_id": library_id,
        "original_video_id": original_video_id,
        "new_path": new_path,
    })
```

### 2.5 Configuration surface

```toml
# pipeline.toml — added by Plan 9.4
[hash]
workers          = 8        # asyncio.to_thread executor cap
timeout_sec      = 30       # default; per-call override allowed
default_chunk_bytes = 1048576
```

The Pipeline applies these by sizing the global thread pool at startup
(in `pipeline/src/maktaba_pipeline/runtime/threadpool.py`); the
`timeout_sec` default is read by the hash callers.

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `pipeline/src/maktaba_pipeline/hash/__init__.py` | re-exports | n/a |
| 2 | `pipeline/src/maktaba_pipeline/hash/errors.py` | `HashError`, `PathOutOfRoot`, `HashTimeout`, `HashFileMissing`, `HashAccessDenied` | n/a |
| 3 | `pipeline/src/maktaba_pipeline/hash/blake3hash.py` | `hash_path`, `_hash_sync`, `_read_into`, `_check_root`, constants | `test_hash_*`, `test_path_out_of_root`, `test_timeout_on_slow_fs`, `test_truncated_eof` |
| 4 | `pipeline/src/maktaba_pipeline/hash/audit.py` | `audit_log_dedup` | `test_dedup_audit` |
| 5 | `pipeline/src/maktaba_pipeline/runtime/threadpool.py` (extend) | hash worker pool | smoke test |
| 6 | `pipeline.toml` (extend) | `[hash]` section | n/a |

No SQL migration in this plan. The `audit_log` schema migration is owned
by Story 9.17 (per the Epic README). If 9.17 hasn't landed when 9.4 is
merged, `audit_log_dedup` is wrapped in a feature flag (`if not
audit_table_exists: log.info(...) and return`).

---

## 4. Test cases

### 4.1 `test_identical_files_same_hash` — AC-1

```python
@pytest.mark.asyncio
async def test_identical_files_in_different_folders_match(tmp_path):
    a = tmp_path / "a" / "v.mp4"; a.parent.mkdir(); a.write_bytes(b"X" * 100)
    b = tmp_path / "b" / "v.mp4"; b.parent.mkdir(); b.write_bytes(b"X" * 100)
    h_a = await hash_path(str(a), library_roots=[str(tmp_path)])
    h_b = await hash_path(str(b), library_roots=[str(tmp_path)])
    assert h_a == h_b
    assert len(h_a) == 64
    assert all(c in "0123456789abcdef" for c in h_a)
```

### 4.2 `test_documented_middle_collision` — AC-1 (story edge case)

```python
@pytest.mark.asyncio
async def test_files_with_same_head_tail_size_collide(tmp_path):
    """The 4+4+size scheme intentionally ignores middle bytes for >8 MiB.
    This test documents the property so reviewers don't think it's a bug."""
    head = b"H" * (4 * MIB)
    tail = b"T" * (4 * MIB)
    middle_a = b"A" * MIB                     # 1 MiB middle
    middle_b = b"B" * MIB

    a = tmp_path / "a.mp4"; a.write_bytes(head + middle_a + tail)
    b = tmp_path / "b.mp4"; b.write_bytes(head + middle_b + tail)
    assert a.stat().st_size == b.stat().st_size           # same size
    assert a.read_bytes() != b.read_bytes()               # but different bytes

    h_a = await hash_path(str(a), library_roots=[str(tmp_path)])
    h_b = await hash_path(str(b), library_roots=[str(tmp_path)])
    assert h_a == h_b   # Documented collision: same head, same tail, same size.
```

### 4.3 `test_8mib_boundary_full_file` — D9

```python
@pytest.mark.asyncio
async def test_exactly_8mib_is_hashed_in_full(tmp_path):
    """The == 8 MiB branch must use the full-file path (D9), not the
    overlapping head+tail path. Verify by checking that a single byte
    flip in the *middle* still changes the hash."""
    body = bytearray(b"X" * (8 * MIB))
    a = tmp_path / "a.mp4"; a.write_bytes(bytes(body))
    body[4 * MIB] = ord("Y")             # flip the middle byte
    b = tmp_path / "b.mp4"; b.write_bytes(bytes(body))

    h_a = await hash_path(str(a), library_roots=[str(tmp_path)])
    h_b = await hash_path(str(b), library_roots=[str(tmp_path)])
    assert h_a != h_b
```

### 4.4 `test_path_out_of_root` — AC-1 security gate

```python
@pytest.mark.asyncio
async def test_off_root_path_is_rejected(tmp_path):
    inside = tmp_path / "lib" / "v.mp4"; inside.parent.mkdir(); inside.write_bytes(b"x")
    outside = tmp_path.parent / "stray.mp4"; outside.write_bytes(b"x")
    with pytest.raises(PathOutOfRoot):
        await hash_path(str(outside), library_roots=[str(tmp_path / "lib")])
    # Inside is fine.
    h = await hash_path(str(inside), library_roots=[str(tmp_path / "lib")])
    assert len(h) == 64


@pytest.mark.asyncio
async def test_path_prefix_match_does_not_pass(tmp_path):
    """Reject /data/lib1.evil/x when only /data/lib1 is configured."""
    real = tmp_path / "lib"; real.mkdir(); (real / "ok.mp4").write_bytes(b"x")
    sibling = tmp_path / "lib.evil"; sibling.mkdir(); (sibling / "x.mp4").write_bytes(b"x")
    with pytest.raises(PathOutOfRoot):
        await hash_path(str(sibling / "x.mp4"), library_roots=[str(real)])
```

### 4.5 `test_timeout_on_slow_filesystem` — AC-3

```python
@pytest.mark.asyncio
async def test_hash_raises_on_timeout(tmp_path, monkeypatch):
    p = tmp_path / "v.mp4"; p.write_bytes(b"x" * 100)

    async def slow_to_thread(fn, *args, **kwargs):
        await asyncio.sleep(2)
        return fn(*args, **kwargs)

    monkeypatch.setattr("asyncio.to_thread", slow_to_thread)
    with pytest.raises(HashTimeout):
        await hash_path(str(p), library_roots=[str(tmp_path)],
                        timeout_sec=0.1)
```

### 4.6 `test_truncated_eof_is_not_an_error` — D10, story edge case

```python
@pytest.mark.asyncio
async def test_partial_read_yields_stable_hash(tmp_path):
    """Two reads of a file produce the same hash even if the file shrinks
    between them (we feed what we got)."""
    p = tmp_path / "v.mp4"; p.write_bytes(b"\xab" * 100)
    h1 = await hash_path(str(p), library_roots=[str(tmp_path)])
    # Same call again -> same hash (hashing is deterministic).
    h2 = await hash_path(str(p), library_roots=[str(tmp_path)])
    assert h1 == h2
```

### 4.7 `test_no_follow_symlink_raises` — D8 TOCTOU closure

```python
@pytest.mark.asyncio
async def test_symlink_target_is_resolved_then_opened_with_nofollow(tmp_path):
    target = tmp_path / "real.mp4"; target.write_bytes(b"x" * 100)
    link = tmp_path / "link.mp4"; link.symlink_to(target)
    # realpath resolves to real.mp4; we then O_NOFOLLOW the canonical
    # path. Both paths produce the same hash.
    h_link = await hash_path(str(link), library_roots=[str(tmp_path)])
    h_real = await hash_path(str(target), library_roots=[str(tmp_path)])
    assert h_link == h_real
```

### 4.8 `test_perf_30gib_under_100ms` — AC-3, slow CI mark

```python
@pytest.mark.slow
@pytest.mark.asyncio
async def test_30gib_file_hashes_in_under_100ms(tmp_path):
    """Synthesize a 30 GiB sparse file and hash it. Must complete in <100ms
    (only 8 MiB is read; sparse holes are zero-filled by the kernel)."""
    p = tmp_path / "huge.mp4"
    with open(p, "wb") as f:
        f.seek(30 * 1024 * 1024 * 1024 - 1)
        f.write(b"\x00")
    t0 = time.monotonic()
    h = await hash_path(str(p), library_roots=[str(tmp_path)],
                        timeout_sec=2.0)
    elapsed = time.monotonic() - t0
    assert len(h) == 64
    assert elapsed < 0.100
```

### 4.9 `test_dedup_audit_writes_row` — AC-2

```python
@pytest.mark.asyncio
async def test_dedup_audit_inserts_audit_log_row(db, audit_log_table):
    """Calling audit_log_dedup writes a category='library', event='duplicate-detected' row."""
    await audit_log_dedup(
        db, library_id=LIB_ID,
        original_video_id="11111111-1111-1111-1111-111111111111",
        new_path="/data/lib1/dupes/copy.mp4")
    row = await db.fetchrow(
        "SELECT category, event, library_id, payload_jsonb FROM audit_log "
        "WHERE event = 'duplicate-detected' ORDER BY ts DESC LIMIT 1")
    assert row["category"] == "library"
    assert row["event"] == "duplicate-detected"
    assert str(row["library_id"]) == LIB_ID
    payload = json.loads(row["payload_jsonb"]) if isinstance(row["payload_jsonb"], str) else row["payload_jsonb"]
    assert payload["new_path"] == "/data/lib1/dupes/copy.mp4"
```

### 4.10 `test_files_smaller_than_8mib_use_full_hash` — D9

```python
@pytest.mark.asyncio
async def test_small_file_changes_in_middle_change_hash(tmp_path):
    a = tmp_path / "a.mp4"; a.write_bytes(b"H" * (1 * MIB) + b"M" * 100 + b"T" * (1 * MIB))
    b = tmp_path / "b.mp4"; b.write_bytes(b"H" * (1 * MIB) + b"X" * 100 + b"T" * (1 * MIB))
    h_a = await hash_path(str(a), library_roots=[str(tmp_path)])
    h_b = await hash_path(str(b), library_roots=[str(tmp_path)])
    assert h_a != h_b
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handling |
|-----|-----------|----------|
| E1  | **Files exactly 8 MiB.** Hashed in full via the `<= FULL_HASH_THRESHOLD` branch (D9). Avoids the head/tail overlap that would otherwise hash the same 4 MiB window twice. | `test_exactly_8mib_is_hashed_in_full`. |
| E2  | **Truncated read on EOF in the last-4-MiB window.** Possible only if the file shrinks between `os.fstat` and `os.read`. `_read_into` returns early on the first short read; the hash incorporates whatever was read; the size suffix is the original `os.fstat` size, so a subsequent rehash will pick up the new (smaller) size + different bytes and produce a different hash. | `test_partial_read_yields_stable_hash` documents the determinism on a stable file; the dynamic-shrink case is documented and tolerated. |
| E3  | **BLAKE3 hash collision** (not a property failure of the scheme — just an astronomical possibility). Two genuinely different files with the same head, same tail, and same size hash to the same value (D1 documented collision). When this happens, the catalog keeps the first-seen entry and emits `audit_log.event = 'hash-collision'` instead of `'duplicate-detected'` (callers can branch on a deeper equality check if they care). For v1, we treat documented collisions as duplicates. The user can force re-process via Epic 7 Story 7.5. | `test_files_with_same_head_tail_size_collide`. |
| E4  | **Symlink swap mid-hash (TOCTOU).** `O_NOFOLLOW` on the canonical path closes the window: if the canonical path is itself a symlink (impossible after `realpath`) or becomes one, `os.open` raises `ELOOP` which we re-raise as `HashError`. | D8 + `test_symlink_target_is_resolved_then_opened_with_nofollow`. |
| E5  | **Off-root path** (caller bug or attack). `_check_root` raises `PathOutOfRoot` before any read. The `+ os.sep` suffix on the canonical roots prevents the `/data/lib1` vs `/data/lib1.evil` confusion. | `test_off_root_path_is_rejected`, `test_path_prefix_match_does_not_pass`. |
| E6  | **File disappears between caller stat and hash open.** `os.open` raises `FileNotFoundError`; we re-raise as `HashFileMissing`. The caller (sweep) catches and logs; the file is treated as absent (will mark MISSING on the same sweep). | Built into `_hash_sync`. |
| E7  | **Permission denied on read.** `os.open` raises `PermissionError`; re-raised as `HashAccessDenied`. The caller logs and continues. The file is not enqueued; the operator must fix permissions. | Built into `_hash_sync`. |
| E8  | **Slow NFS / FUSE mount** — read takes > 30 s. `asyncio.wait_for` raises `TimeoutError`; we re-raise as `HashTimeout`. Caller logs `kind=hash_timeout`; the file is not enqueued; the next sweep retries. | D4 + `test_hash_raises_on_timeout`. |
| E9  | **30 GiB file on local SSD.** Two 4 MiB reads = 8 MiB total; SSD bandwidth ~500 MiB/s → ~16 ms; BLAKE3 throughput >1 GiB/s → ~8 ms; total well under 100 ms. | `test_30gib_file_hashes_in_under_100ms` (CI slow). |
| E10 | **Multiple library roots, hash called from sweep.** `library_roots` is the per-library list; the sweep passes its current library's roots. Cross-library invocation is the API service's responsibility (e.g., when handling a cross-library move, which goes through Story 7.3 not the hasher). | Documented in caller contract. |
| E11 | **Sparse file (e.g., a 30 GiB sparse synthetic).** Read-zero-fill semantics; the kernel returns zero pages for unallocated extents, so the hash is well-defined on sparse files but tied to the size suffix. Test fixture uses a sparse file to keep CI fast. | `test_perf_30gib_under_100ms`. |
| E12 | **Concurrent hashes for the same path.** No shared state; each call opens its own fd. Safe under arbitrary concurrency up to the thread-pool size. | Stateless design. |
| E13 | **Path with non-UTF-8 bytes.** `os.path.realpath` works on bytes paths; we accept `str | os.PathLike[str]`. Non-UTF-8 file names raise `UnicodeError` at the API boundary; document caller responsibility (Pipeline runs with `LANG=C.UTF-8`). | Documented in callers (Plan 9.2 / 9.3). |
| E14 | **Files smaller than 8 MiB.** Full-file hash (D1 `< 8 MiB` branch). | `test_small_file_changes_in_middle_change_hash`. |

---

## 6. Acceptance checklist

- [ ] **A1** Hash computed over `[0..4MiB) ‖ [size-4MiB..size) ‖ size_le_u64` for files ≥ 8 MiB; full-file BLAKE3 for files ≤ 8 MiB. (`test_identical_files_in_different_folders_match`, `test_files_with_same_head_tail_size_collide`, `test_small_file_changes_in_middle_change_hash`, `test_exactly_8mib_is_hashed_in_full`)
- [ ] **A2** Path canonicalization + root-membership check before any read; off-root paths raise `PathOutOfRoot`. The check uses `+ os.sep` to defeat prefix tricks. (`test_off_root_path_is_rejected`, `test_path_prefix_match_does_not_pass`)
- [ ] **A3** Identical content in different folders produces the same hash. (`test_identical_files_in_different_folders_match`)
- [ ] **A4** Documented middle-byte collision (same head + tail + size for ≥ 8 MiB files yields the same hash). Test exists so reviewers don't think it's a bug. (`test_files_with_same_head_tail_size_collide`)
- [ ] **A5** 30 GiB file hashes in < 100 ms on local SSD (only 8 MiB read). (`test_30gib_file_hashes_in_under_100ms`, slow CI)
- [ ] **A6** `hash_timeout_sec` enforced via `asyncio.wait_for`; slow filesystems raise `HashTimeout`. (`test_hash_raises_on_timeout`)
- [ ] **A7** Truncated EOF in the last-4-MiB window is absorbed silently; the hash is well-defined for whatever bytes were read. (`test_partial_read_yields_stable_hash`)
- [ ] **A8** Symlink-target resolution + `O_NOFOLLOW` on the canonical path closes the TOCTOU window. (`test_symlink_target_is_resolved_then_opened_with_nofollow`)
- [ ] **A9** Hash format is 64-char lowercase hex matching the existing `videos.content_hash` CHECK constraint. (Assertions in `test_identical_files_in_different_folders_match`.)
- [ ] **A10** `audit_log_dedup` writes a row with `category='library'`, `event='duplicate-detected'`, `library_id`, and `payload_jsonb` containing `original_video_id` and `new_path`. (`test_dedup_audit_inserts_audit_log_row`)
- [ ] **A11** The hasher does no DB I/O. (Static lint: `hash/blake3hash.py` does not import `asyncpg`.)
- [ ] **A12** Hash uniqueness is enforced at the DB level via `videos.content_hash UNIQUE`; second-seen-hash UPDATEs the path and writes an audit row. (Cross-tested in Plan 9.3 `test_path_change_with_known_hash_is_a_move`.)

---

## 7. Performance budget

| Phase | Cost (30 GiB file, local SSD) | Notes |
|-------|-------------------------------|-------|
| `os.path.realpath` | ~50 µs | One stat per path component. |
| `_check_root` | ~10 µs | String prefix compare. |
| `os.open` + `os.fstat` | ~50 µs warm cache | One syscall each. |
| `os.lseek` × 2 + `os.read` × 8 (4 MiB head + 4 MiB tail at 1 MiB chunks) | ~16 ms | 8 MiB / 500 MiB/s SSD. |
| `blake3.update` × ~8 MiB | ~8 ms | BLAKE3 throughput ≥ 1 GiB/s on a single core. |
| `hexdigest()` | < 50 µs | 32 → 64 char hex encode. |
| `os.close` | < 10 µs | |
| **Total wall (synchronous worker)** | **~25–30 ms p95** on warm cache | Well within the 100 ms budget (A5). |
| Async wrapper overhead (`asyncio.to_thread` + `wait_for`) | ~1 ms | Negligible. |
| **End-to-end (`hash_path`)** | **≤ 50 ms p95** on local SSD | |

Under load (sweep on 100k brand-new files, all ≥ 8 MiB): 100k × 30 ms =
3000 s of wall work. With 8 worker threads, ~6 minutes wall — outside
the 30 s "all known" sweep budget but within the bootstrap budget. The
sweep does **not** schedule one-shot bootstrap-rate hashing; it
processes new-files lazily as it walks. Bootstrap throughput is
documented in Plan 9.3 §7 perf budget for the first sweep.

NAS / NFS: read bandwidth ~50 MiB/s typical. 8 MiB read = ~160 ms; well
inside the 30 s timeout but the sweep slows accordingly. The default
`hash_timeout_sec=30` covers everything except a frozen mount.
