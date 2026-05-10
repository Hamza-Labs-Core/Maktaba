"""Atomic file writes (Epic 24 plan-24-01).

The canonical recipe for "publish artifact X" in the pipeline is:

1. Write to ``X.tmp.<pid>.<rand>``.
2. fsync the file.
3. Rename to ``X`` (atomic on POSIX within the same filesystem).
4. fsync the containing directory so the rename is durable.

These helpers wrap that recipe. Callers do **not** use ``open(...).write``
directly — every artifact that survives a crash goes through here.
"""

from __future__ import annotations

import os
import pathlib
import secrets
import sys


class AtomicWriteError(IOError):
    """Raised when an atomic write fails before the final rename."""


def _tmp_path(target: pathlib.Path) -> pathlib.Path:
    suffix = f".tmp.{os.getpid()}.{secrets.token_hex(4)}"
    return target.with_name(target.name + suffix)


def atomic_write_bytes(target: pathlib.Path | str, data: bytes, mode: int = 0o644) -> None:
    """Atomically write ``data`` to ``target``.

    On POSIX, this is crash-safe: if the process dies after step 1 but
    before step 3, the temp file is left behind for the next sweep but
    the existing ``target`` (if any) is untouched.
    """
    target = pathlib.Path(target)
    tmp = _tmp_path(target)
    try:
        # O_EXCL so we never overwrite a stray .tmp from another process.
        flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
        if hasattr(os, "O_CLOEXEC"):
            flags |= os.O_CLOEXEC
        fd = os.open(tmp, flags, mode)
        try:
            with os.fdopen(fd, "wb") as f:
                f.write(data)
                f.flush()
                os.fsync(f.fileno())
        except Exception:  # noqa: BLE001
            try:
                os.close(fd)
            except OSError:
                pass
            raise
        os.replace(tmp, target)
        _fsync_dir(target.parent)
    except OSError as e:
        try:
            tmp.unlink(missing_ok=True)
        except OSError:
            pass
        raise AtomicWriteError(str(e)) from e


def atomic_write_text(target: pathlib.Path | str, text: str, encoding: str = "utf-8") -> None:
    """Convenience wrapper for text writes."""
    atomic_write_bytes(target, text.encode(encoding))


def _fsync_dir(directory: pathlib.Path) -> None:
    """Best-effort directory fsync. Windows has no directory fsync; skip."""
    if sys.platform.startswith("win"):
        return
    try:
        fd = os.open(directory, os.O_RDONLY)
    except OSError:
        return
    try:
        os.fsync(fd)
    except OSError:
        # Some filesystems (e.g. tmpfs on macOS) don't support directory
        # fsync. Treat as best-effort: the data fsync above is what
        # actually matters.
        pass
    finally:
        os.close(fd)
