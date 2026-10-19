"""File access under the cluster directory that never follows a link.

The share is writable by every machine, so a symlink (or, on Windows, a junction or other
reparse point) planted under ``<cluster dir>`` could send a write, a read or a deletion
somewhere else. Everything below the cluster directory is therefore checked component by
component with ``lstat`` (a link is refused, never followed), files are opened with
``O_NOFOLLOW`` where the OS has it (and checked to be regular files after opening), and
retention deletes single regular files only.

The cluster directory itself is configuration (it may be a link on purpose); only what
is *under* it is checked.
"""

from __future__ import annotations

import contextlib
import errno
import os
import stat
from collections.abc import Iterator, Sequence
from pathlib import Path
from typing import IO

__all__ = [
    "UnsafePath",
    "is_link",
    "list_real_dirs",
    "list_real_files",
    "open_append",
    "open_read",
    "real_dir",
    "real_file",
    "remove_real_file",
    "safe_dir",
    "write_replace",
]

_REPARSE = getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0x400)
_NOFOLLOW = getattr(os, "O_NOFOLLOW", 0)
_CLOEXEC = getattr(os, "O_CLOEXEC", 0)
_BINARY = getattr(os, "O_BINARY", 0)


class UnsafePath(OSError):
    """A link, or something other than what was expected, under the cluster directory."""


def is_link(st: os.stat_result) -> bool:
    if stat.S_ISLNK(st.st_mode):
        return True
    attrs = getattr(st, "st_file_attributes", 0)
    return bool(attrs & _REPARSE)


def _check_part(part: str) -> None:
    if part in ("", ".", "..") or "/" in part or "\\" in part or "\x00" in part:
        raise UnsafePath(f"bad path component {part!r}")


def real_dir(path: Path) -> bool:
    """``path`` exists and is a directory, not a link."""
    try:
        st = os.lstat(path)
    except OSError:
        return False
    return not is_link(st) and stat.S_ISDIR(st.st_mode)


def real_file(path: Path) -> bool:
    """``path`` exists and is a regular file, not a link."""
    try:
        st = os.lstat(path)
    except OSError:
        return False
    return not is_link(st) and stat.S_ISREG(st.st_mode)


def safe_dir(base: Path, parts: Sequence[str], *, create: bool = True) -> Path:
    """``base/parts...``, each part a real directory (created when missing)."""
    cur = base
    for part in parts:
        _check_part(part)
        cur = cur / part
        try:
            st = os.lstat(cur)
        except FileNotFoundError:
            if not create:
                raise
            with contextlib.suppress(FileExistsError):
                os.mkdir(cur, 0o755)
            st = os.lstat(cur)
        if is_link(st) or not stat.S_ISDIR(st.st_mode):
            raise UnsafePath(f"{cur} is not a real directory")
    return cur


def _check_existing_file(path: Path) -> None:
    try:
        st = os.lstat(path)
    except FileNotFoundError:
        return
    if is_link(st) or not stat.S_ISREG(st.st_mode):
        raise UnsafePath(f"{path} is not a regular file")


def _check_opened(fd: int, path: Path) -> None:
    st = os.fstat(fd)
    if not stat.S_ISREG(st.st_mode):
        os.close(fd)
        raise UnsafePath(f"{path} is not a regular file")


def open_append(path: Path) -> IO[str]:
    """Append to ``path`` (created when missing) without following a link."""
    _check_part(path.name)
    _check_existing_file(path)
    flags = os.O_WRONLY | os.O_APPEND | os.O_CREAT | _NOFOLLOW | _CLOEXEC | _BINARY
    try:
        fd = os.open(path, flags, 0o644)
    except OSError as exc:
        if _NOFOLLOW and exc.errno in (errno.ELOOP, errno.EMLINK):  # O_NOFOLLOW hit a link
            raise UnsafePath(f"{path} is a link") from exc
        raise
    _check_opened(fd, path)
    return os.fdopen(fd, "a", encoding="utf-8")


def open_read(path: Path) -> IO[str]:
    _check_existing_file(path)
    fd = os.open(path, os.O_RDONLY | _NOFOLLOW | _CLOEXEC | _BINARY)
    _check_opened(fd, path)
    return os.fdopen(fd, "r", encoding="utf-8", errors="replace")


def write_replace(directory: Path, name: str, text: str) -> None:
    """Write ``directory/name`` whole (a fresh temp file renamed over it: a rename
    replaces a planted link itself, never what it points at)."""
    _check_part(name)
    tmp = directory / f".{name}.{os.getpid()}.tmp"
    with contextlib.suppress(FileNotFoundError):
        _check_existing_file(tmp)
        os.unlink(tmp)
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | _NOFOLLOW | _CLOEXEC | _BINARY, 0o644)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as fh:
            fh.write(text)
        os.replace(tmp, directory / name)
    except BaseException:
        with contextlib.suppress(OSError):
            os.unlink(tmp)
        raise


def remove_real_file(path: Path) -> bool:
    """Delete ``path`` only when it is a regular file (a link is left alone)."""
    if not real_file(path):
        return False
    os.unlink(path)
    return True


def list_real_dirs(path: Path) -> Iterator[Path]:
    """The real (non-link) directories in ``path`` (nothing when it is a link)."""
    if not real_dir(path):
        return
    try:
        entries = sorted(path.iterdir())
    except OSError:
        return
    for p in entries:
        if real_dir(p):
            yield p


def list_real_files(path: Path, suffix: str = ".jsonl") -> Iterator[Path]:
    if not real_dir(path):
        return
    try:
        entries = sorted(path.iterdir())
    except OSError:
        return
    for p in entries:
        if p.name.endswith(suffix) and real_file(p):
            yield p
