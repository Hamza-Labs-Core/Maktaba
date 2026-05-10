"""Hard-link (or copy-fallback) of the canonical sidecar to an alias path.

We prefer ``os.link`` because it consumes zero extra disk space and
keeps the two paths backed by the same inode — so a renamed canonical
file doesn't leave a stale alias behind. On filesystems that don't
support hard links across the relevant boundary (FAT32 USB sticks,
some network shares), we fall back to ``shutil.copy2``.

Read-only parent directories are *not* fatal — the function returns
``False`` and logs ``alias_copy_failed`` so the canonical artefact
still lands and the pipeline keeps making progress.
"""

from __future__ import annotations

import os
import shutil
from pathlib import Path
from typing import Any, Protocol

__all__ = ["alias_copy"]


class _Logger(Protocol):
    def warning(self, event: str, **kwargs: Any) -> Any: ...

    def info(self, event: str, **kwargs: Any) -> Any: ...


def _same_inode(a: Path, b: Path) -> bool:
    """Return True iff both paths reference the same inode.

    Used to detect a stale alias that points elsewhere on a re-run.
    On Windows ``st_ino`` may be ``0`` for non-NTFS volumes; we
    treat that conservatively as "different" so we don't overwrite.
    """
    try:
        sa = a.stat()
        sb = b.stat()
    except OSError:
        return False
    if sa.st_ino == 0 or sb.st_ino == 0:
        return False
    return sa.st_ino == sb.st_ino and sa.st_dev == sb.st_dev


def alias_copy(source: Path, alias: Path, *, log: _Logger) -> bool:
    """Link or copy ``source`` to ``alias``. Return True on success.

    The function is graceful: it never raises for environmental
    failures (read-only filesystem, missing parent directory,
    cross-volume hard-link rejection). It logs and returns ``False``
    instead, so the calling stage can mark itself ``ok`` even when
    the alias couldn't be created. Returns ``False`` without logging
    when the alias already points at the same inode (idempotent
    re-run).
    """
    if alias.exists():
        if _same_inode(source, alias):
            return True
        log.warning(
            "alias_collision",
            kind="alias_collision",
            source=str(source),
            alias=str(alias),
        )
        return False

    if not alias.parent.exists():
        log.warning(
            "alias_copy_failed",
            kind="alias_copy_failed",
            reason="parent_missing",
            source=str(source),
            alias=str(alias),
        )
        return False

    # Try hard link first.
    try:
        os.link(source, alias)
        return True
    except OSError:
        # Fall through to copy. Could be EXDEV (different FS),
        # EPERM (FAT32), or ENOTSUP — all benign here.
        pass

    try:
        shutil.copy2(source, alias)
        return True
    except (OSError, shutil.SameFileError) as exc:
        log.warning(
            "alias_copy_failed",
            kind="alias_copy_failed",
            reason=type(exc).__name__,
            source=str(source),
            alias=str(alias),
        )
        return False
