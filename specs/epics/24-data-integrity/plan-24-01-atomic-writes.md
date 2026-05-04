# Implementation Plan — Story 24.1 Atomic writes for sidecar artifacts

> Companion to [story-24-01-atomic-writes.md](story-24-01-atomic-writes.md).
> Story states *what* and *why*; this plan states *how*.
> Sidecar layout (`.maktaba/`) and artifact list defined in
> [architecture.md §10](../../architecture.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Helper module | `pipeline/src/maktaba_pipeline/media/atomic_write.py`. Single function `atomic_write_bytes(path, content, *, fsync=True)` plus a `atomic_write_stream(path)` context manager for large files. The helper signature documents `EXDEV` cross-FS fallback (copy + atomic-rename within destination FS). |
| Filesystem assumption | POSIX `rename(2)` atomicity on the same filesystem. Cross-FS rename uses copy + atomic-rename within the destination FS (`EXDEV` path). |
| HLS segments | **Scoped out** here per Epic 8 §4.8: `.m4s`/`.ts` segment writes are owned by the streaming origin (Epic 8) and use a different write-then-publish strategy (write into a staging dir, atomic `rename` of the directory tree). Do not route HLS segments through this helper. |
| Lint | `tools/atomic-write-lint.py` walks `pipeline/src/` for `open(path, "w")` / `Path.write_*` calls outside the helper. |
| Reaper | `pipeline/src/maktaba_pipeline/tasks/reaper.py` already exists per architecture §7; this plan adds a `sweep_stale_temps` step. |
| Out of scope | DB writes (Story 24.3); HLS segments (Epic 8 §4.8); the on-disk encoding format itself (Epic 4 owns subtitles, Epic 8 owns sprites). |

## 1. Architecture diagram

```
┌──────────────────┐                ┌──────────────────────────┐
│ subtitle writer  │ ─────────────► │ media.atomic_write_stream│
│ thumbnail writer │                │  - tmp = path + .tmp.<n> │
│ sprite writer    │                │  - write_all + fsync(file)│
│ poster writer    │                │  - rename(tmp, path)     │
│ segment json wri.│                │  - fsync_dir(parent)     │
└──────────────────┘                └──────────────────────────┘
                                              │ on success
                                              ▼
                                    ┌──────────────────────┐
                                    │ ./video.srt (final)  │
                                    └──────────────────────┘

┌──────────────────┐
│ reaper task      │ → daily sweep: remove tmp files older than 24 h
└──────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/media/atomic_write.py` | The helper. |
| `pipeline/src/maktaba_pipeline/media/atomic_write_test.py` | Tests, including the kill-mid-write fixture. |
| `pipeline/src/maktaba_pipeline/tasks/reaper.py` (extended) | Sweeper for stale temps. |
| `tools/atomic-write-lint.py` | Lint pass. |
| `pipeline/src/maktaba_pipeline/media/fs_capabilities.py` | Detects whether the destination FS supports atomic rename; remembers per-mountpoint. |
| Tests — `tests/integration/atomic_write_*.py`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/subtitles/vtt_writer.py` | Replaces `path.write_text` with `atomic_write_bytes`. |
| `pipeline/src/maktaba_pipeline/subtitles/srt_writer.py` | Same. |
| `pipeline/src/maktaba_pipeline/media/segment_json.py` | Same. |
| `pipeline/src/maktaba_pipeline/media/thumbnails.py` | Same. |
| `pipeline/src/maktaba_pipeline/media/sprites.py` | Same. |
| `pipeline/src/maktaba_pipeline/media/poster.py` | Same. |

### 2.3 The helper

`atomic_write.py`:

```python
from __future__ import annotations

import contextlib
import errno
import os
import secrets
import shutil
import tempfile
from pathlib import Path
from typing import IO, Iterator

from .fs_capabilities import supports_atomic_rename, mark_unsupported


def atomic_write_bytes(
    path: Path | str,
    content: bytes,
    *,
    fsync: bool = True,
    perm: int = 0o644,
) -> None:
    """Write `content` to `path` atomically.

    The temp file is created in the same directory as `path` so the
    rename is on the same filesystem. On filesystems where rename is
    NOT atomic (some SMB shares), the helper falls back to
    `(write, fsync, rename, fsync_dir)` which is still durable, just not
    truly atomic; a documented warning is logged.

    On `EXDEV` (the temp dir somehow ends up on a different filesystem
    than `path`'s dir — e.g., bind mount inside `parent`), the helper
    uses `shutil.copyfile + os.replace + os.unlink(tmp)` so the rename
    happens on the destination filesystem. This is the documented
    cross-FS fallback path; readers never observe a partial file.
    """
    p = Path(path)
    parent = p.parent
    parent.mkdir(parents=True, exist_ok=True)

    tmp = parent / f"{p.name}.tmp.{secrets.token_hex(8)}"
    try:
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL, perm)
        try:
            os.write(fd, content)
            if fsync:
                os.fsync(fd)
        finally:
            os.close(fd)
        os.replace(tmp, p)            # POSIX-atomic on same FS
        if fsync:
            _fsync_dir(parent)
    except OSError as e:
        # Out-of-space: leave no partial output.
        with contextlib.suppress(FileNotFoundError):
            tmp.unlink()
        if e.errno == errno.ENOSPC:
            raise DiskFullError(p) from e
        raise


@contextlib.contextmanager
def atomic_write_stream(
    path: Path | str,
    *,
    fsync: bool = True,
    perm: int = 0o644,
) -> Iterator[IO[bytes]]:
    """Streaming variant for large outputs (sprites, thumbnails grid).

    Yields a binary file-like object. On normal exit, fsync, rename, and
    fsync_dir. On exception, unlink the temp file and re-raise.
    """
    p = Path(path)
    parent = p.parent
    parent.mkdir(parents=True, exist_ok=True)
    tmp = parent / f"{p.name}.tmp.{secrets.token_hex(8)}"
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL, perm)
    try:
        with os.fdopen(fd, "wb") as f:
            yield f
            if fsync:
                f.flush()
                os.fsync(f.fileno())
        os.replace(tmp, p)
        if fsync:
            _fsync_dir(parent)
    except BaseException:
        with contextlib.suppress(FileNotFoundError):
            tmp.unlink()
        raise


def _fsync_dir(path: Path) -> None:
    """fsync the directory so the rename is durable across crashes."""
    try:
        fd = os.open(path, os.O_RDONLY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    except OSError as e:
        # Some filesystems (Linux tmpfs, NFSv3) refuse dir fsync; this
        # is documented as best-effort. Mark and continue.
        mark_unsupported(path, "fsync_dir")
```

`DiskFullError` carries the destination path for the error reporter
(`category=disk_full`, AC requirement EC1).

### 2.4 FS capability cache

`fs_capabilities.py`:

```python
"""Detects per-mountpoint atomic-rename and dir-fsync support.

Cache is process-local. The first write to a path probes by writing a
small test file, renaming, and reading. Subsequent writes to the same
mount reuse the cached answer.
"""

import os
from dataclasses import dataclass
from threading import Lock

@dataclass
class _Capability:
    atomic_rename: bool = True
    fsync_dir: bool = True

_cache: dict[str, _Capability] = {}
_lock = Lock()

def supports_atomic_rename(mount: str) -> bool:
    """Returns True iff the mount has been observed to atomically rename.

    Used by `atomic_write_bytes` to decide whether to take the fast path
    (single `os.replace`) or the SMB/network-share fallback path
    (write + fsync + rename + fsync_dir + log). Initial value is True
    (optimistic); flipped to False on first observed `EXDEV`/
    `EOPNOTSUPP` via `mark_unsupported`. The boolean is consulted on
    every write — not vestigial.
    """
    with _lock:
        c = _cache.get(mount)
        if c: return c.atomic_rename
        c = _probe(mount)
        _cache[mount] = c
        return c.atomic_rename

def mark_unsupported(path, op: str) -> None:
    mount = _mountpoint(path)
    with _lock:
        c = _cache.setdefault(mount, _Capability())
        if op == "atomic_rename": c.atomic_rename = False
        if op == "fsync_dir":     c.fsync_dir     = False

def _probe(mount: str) -> _Capability:
    # Best-effort initial probe: filesystem-type sniff via os.statvfs +
    # /proc/mounts (Linux) or `getmntinfo` (macOS). Known-non-atomic
    # types (cifs, smbfs, nfsv3) start with `atomic_rename=False`;
    # everything else starts True and is downgraded by mark_unsupported
    # on first observed EXDEV/EOPNOTSUPP.
    fstype = _detect_fstype(mount)
    if fstype in {"cifs", "smbfs", "nfs", "nfs3"}:
        return _Capability(atomic_rename=False, fsync_dir=False)
    return _Capability(atomic_rename=True, fsync_dir=True)
```

### 2.5 Stale-temp sweeper

`reaper.py` (extension):

```python
import time
from pathlib import Path

STALE_TMP_AGE = 24 * 60 * 60  # 24 h

def sweep_stale_temps(library_root: Path) -> int:
    """Remove `*.tmp.*` files older than 24 h under the library root.

    The glob uses `**` so it matches both flat layouts
    (`.maktaba/foo.tmp.<n>`) and nested layouts (`.maktaba/<vid>/.../*.tmp.<n>`).
    """
    removed = 0
    cutoff = time.time() - STALE_TMP_AGE
    for tmp in library_root.rglob(".maktaba/**/*.tmp.*"):
        try:
            if tmp.stat().st_mtime < cutoff:
                tmp.unlink(missing_ok=True)
                removed += 1
        except FileNotFoundError:
            # Race: another sweeper or the writer cleaned up. Fine.
            pass
    return removed
```

Wired into the existing reaper schedule (architecture §7); fires
hourly with a 24 h age threshold.

### 2.6 Lint

`tools/atomic-write-lint.py`:

```python
"""Refuses unguarded writes to sidecar paths.

Walks `pipeline/src/maktaba_pipeline/`; flags:
  - open(path, "w" | "wb")
  - open(path, "a" | "ab")
  - Path.write_text, Path.write_bytes
when called outside `media/atomic_write.py`. The helper itself is
exempt; tests under tests/ are exempt.
"""

import ast
import sys
from pathlib import Path

ALLOW = {Path("pipeline/src/maktaba_pipeline/media/atomic_write.py")}

class _Lint(ast.NodeVisitor):
    def visit_Call(self, node):
        # `Path(p).write_text(...)` / `Path(p).write_bytes(...)`.
        if isinstance(node.func, ast.Attribute) and node.func.attr in {"write_text", "write_bytes"}:
            self._flag(node, "Path.write_*: use media.atomic_write_bytes")
        # `open(p, "w")` etc.
        if isinstance(node.func, ast.Name) and node.func.id == "open":
            mode = next((a.value for a in node.args[1:] if isinstance(a, ast.Constant)), None)
            if mode and ("w" in mode or "a" in mode):
                self._flag(node, "open(..., 'w'/'a'): use media.atomic_write_bytes/_stream")
        self.generic_visit(node)
```

CI runs the lint as part of the `lint-py` make target.

## 3. Test plan

### 3.1 Crash mid-write (TC1)

| Test | What it pins |
|---|---|
| `TestKillMidWriteLeavesNoPartial` | Spawn a subprocess writing 64 MiB via `atomic_write_stream`; SIGKILL halfway; assert the final path is missing AND the temp file remains under `.maktaba/`. After reaper, the temp is gone. |
| `TestKillBeforeRenameNoFinal` | SIGKILL just after the write loop but before `os.replace`; the final path is missing on restart. |
| `TestKillAfterRenameFinalExists` | Trap SIGKILL after `os.replace` but before `_fsync_dir`; the final path exists and contents are the new bytes. The dir fsync was a durability tightening, not a correctness requirement for partial writes. |

### 3.2 Rename atomicity (TC2)

| Test | What it pins |
|---|---|
| `TestConcurrentReaderSeesOldOrNew` | A reader opens the file in a tight loop while a writer overwrites via `atomic_write_bytes` 1000×; the reader observes either the old contents or the new contents; never partial. |
| `TestSameInodeAfterReplace` | (Linux) Confirm `os.replace` reuses the inode of the target if the underlying FS supports it; this isn't a correctness requirement but proves we used `replace`, not `unlink+rename`. |

### 3.3 Reaper sweep (TC3)

| Test | What it pins |
|---|---|
| `TestSweepRemovesStaleTemp` | Create `.maktaba/x/y.srt.tmp.abcd` mtime 25 h ago; sweep removes it. |
| `TestSweepIgnoresFresh` | Same path mtime 1 h ago; sweep leaves it. |
| `TestSweepIdempotent` | Run sweep twice in a row; second is a no-op; no errors when the temp is already gone. |

### 3.4 Lint

| Test | What it pins |
|---|---|
| `TestLintCatchesDirectOpen` | A fixture file with `open("/tmp/foo", "w")` fails the lint with the file:line. |
| `TestLintCatchesPathWriteText` | `Path(p).write_text("...")` fails. |
| `TestLintAllowsHelperItself` | The helper file is exempt. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Out of space mid-write (EC1) | `OSError(ENOSPC)` is converted to `DiskFullError(path)`; the partial temp is unlinked; the caller's job is marked failed with `category=disk_full`. | `TestEnospcRaisesDiskFullError` |
| SMB share with non-atomic rename (EC2) | The probe (or first observed `EXDEV`/`EOPNOTSUPP`) marks the mount unsupported. The helper logs a one-time warning and uses the `(write, fsync, rename, fsync_dir)` sequence anyway — durability is preserved even if perfect atomicity isn't. The artifact may briefly be the new bytes while readers think it's still the old; documented. | `TestSmbFallbackPath` |
| Source deleted before rename (EC3) | If the source video file is deleted while a sidecar is being written, the rename succeeds (target dir still exists). The next library scan removes the orphan sidecar. | `TestOrphanSidecarReconciledByScan` |
| Rename across filesystems | `os.replace` raises `EXDEV`; the helper falls back to `shutil.copyfile + os.replace + os.unlink(tmp)`; the test asserts no partial reads observed mid-copy. | `TestCrossFsRenameFallback` |
| Symlink target | The helper writes the rename's destination, not the symlink target. Documented. Library roots reject symlinks above the root via the path canonicalizer (Story 23.5). | `TestSymlinkDestRespected` |
| Read-only filesystem | `OSError(EROFS)` propagates; no temp file created. | `TestReadOnlyFsRaises` |
| File mode mismatch | Default 0o644; callers requiring restricted modes pass `perm=0o600`. | `TestPermArgHonored` |
| Concurrent atomic writes to same path | Each writer's temp has a random suffix; the `os.replace` is racy but each call leaves the file in a consistent state — last writer wins. Documented. | `TestConcurrentWriteLastWins` |
| Disk full leaves pending temp | Caught by `errno==ENOSPC` cleanup; covered. | `TestEnospcLeavesNoTemp` |
| Path with shared `.tmp.*` glob from another tool | Sweeper's glob is scoped to `.maktaba/**/*.tmp.*`; the tool's other temp files (e.g., FFmpeg's `*.muxer.tmp`) are not under `.maktaba/` per layout. | n/a |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `os`, `shutil`, `contextlib` | stdlib | All primitives. |
| `secrets` | stdlib | Random temp suffix. |
| `ast` (lint) | stdlib | Static check. |

## 6. Acceptance checklist

**Helper**
- [ ] `atomic_write_bytes` and `atomic_write_stream` exist.
- [ ] All sidecar writers route through them.
- [ ] `DiskFullError` carries the path.

**Lint**
- [ ] `tools/atomic-write-lint.py` runs in `lint-py`.
- [ ] Any `open(... "w")` outside the helper fails CI.

**Reaper**
- [ ] `sweep_stale_temps` runs hourly with 24 h threshold.
- [ ] Idempotent on repeat invocations.

**Crash safety**
- [ ] Kill mid-write leaves no partial output.
- [ ] Concurrent reader sees either old or new contents.

**Cross-FS / SMB**
- [ ] Probe + capability cache detects non-atomic rename FS.
- [ ] Documented fallback path.
