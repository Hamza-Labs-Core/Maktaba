"""Recursive filesystem walker for the scanner.

Yields :class:`Candidate` records for every regular file under a root
whose lowercased extension is in :data:`WalkConfig.extensions`. Hidden
files and directories (basename starts with ``.``), partial-download
basenames matching :data:`WalkConfig.ignore_basenames`, and sidecar
directories named in :data:`WalkConfig.ignore_dirnames` are pruned
before stat. Permission denials log once at ``WARN`` per scan and the
walk continues; symlink loops are broken by an ``(st_dev, st_ino)``
visited set when ``follow_symlinks=True`` (default is ``False`` — the
walker uses ``lstat`` semantics via ``os.scandir``).

This module has no DB, no hashing, and no async — the orchestrator in
:mod:`maktaba_pipeline.scanner.service` composes the walker with the
identity hasher and the store. Keeping the walker pure makes it
trivially testable against ``tmp_path`` fixtures and reusable by the
filesystem watcher in Story 1.3.
"""

from __future__ import annotations

import errno
import fnmatch
import os
from collections.abc import Iterator
from dataclasses import dataclass, field
from typing import Any, Protocol

__all__ = [
    "DEFAULT_IGNORE_BASENAMES",
    "DEFAULT_IGNORE_DIRNAMES",
    "DEFAULT_VIDEO_EXTENSIONS",
    "Candidate",
    "WalkConfig",
    "walk",
]


#: Story 1.1 AC3 — exactly the seven extensions the walker emits.
#: Lowercase comparison: file extensions are normalised before lookup
#: so ``Foo.MKV`` and ``bar.mkv`` are both accepted.
DEFAULT_VIDEO_EXTENSIONS: frozenset[str] = frozenset(
    {".mp4", ".mkv", ".mov", ".webm", ".avi", ".ts", ".m4v"}
)

#: Browser/downloader partial-file globs. Matched against the file
#: basename via :func:`fnmatch.fnmatch` so users can extend with their
#: own patterns through :class:`WalkConfig`.
DEFAULT_IGNORE_BASENAMES: tuple[str, ...] = (
    "*.part",
    "*.crdownload",
    "*.partial",  # resume-invariant-ok: walker filters partial downloads, not a checkpoint
    "*.tmp",
)

#: Sidecar directories the scanner owns and must not recurse into.
#: ``.maktaba/`` houses cached intermediate artefacts that live next to
#: the source media; revisiting them would loop.
DEFAULT_IGNORE_DIRNAMES: frozenset[str] = frozenset({".maktaba"})


class _Logger(Protocol):
    """Minimal structlog-shaped logger the walker uses."""

    def warning(self, event: str, **kwargs: Any) -> Any: ...
    def debug(self, event: str, **kwargs: Any) -> Any: ...


@dataclass(slots=True, frozen=True)
class Candidate:
    """One accepted file emitted by :func:`walk`.

    ``size_bytes`` and ``mtime_ns`` come from the same ``stat`` call
    that classified the entry as a regular file with a supported
    extension, so the orchestrator can build a
    :class:`maktaba_pipeline.identity.FileSignature` without a second
    syscall.
    """

    path: str
    size_bytes: int
    mtime_ns: int


@dataclass(slots=True, frozen=True)
class WalkConfig:
    """Knobs for :func:`walk`. All defaults match Story 1.1's spec."""

    extensions: frozenset[str] = DEFAULT_VIDEO_EXTENSIONS
    ignore_basenames: tuple[str, ...] = DEFAULT_IGNORE_BASENAMES
    ignore_dirnames: frozenset[str] = DEFAULT_IGNORE_DIRNAMES
    follow_symlinks: bool = False


@dataclass(slots=True)
class _WalkState:
    """Per-scan mutable state passed down the recursion.

    ``permission_logged`` ensures we emit one ``scanner.permission_denied``
    line per scan — Story 1.1 edge case: a locked share root would
    otherwise spam the log on every entry attempt.
    """

    permission_logged: bool = False
    visited: set[tuple[int, int]] = field(default_factory=set)


def walk(
    root: str | os.PathLike[str],
    config: WalkConfig,
    log: _Logger,
) -> Iterator[Candidate]:
    """Yield :class:`Candidate` records for every accepted file under ``root``.

    Order is not guaranteed (depends on the filesystem's directory
    iteration order), so callers must not rely on a specific traversal
    sequence. The walker is synchronous and lazy: candidates are yielded
    as they're discovered, letting the orchestrator pipeline hashing
    against further walking.

    A ``root`` that does not exist or that the caller cannot read raises
    immediately — the orchestrator decides whether to log and skip or
    abort. Permission denials *inside* the tree are swallowed (logged at
    ``WARN`` once per scan) so a single locked subdirectory never aborts
    the rest of the walk.
    """
    state = _WalkState()
    root_str = os.fspath(root)
    yield from _walk_dir(root_str, config, log, state, depth=0)


def _walk_dir(
    path: str,
    config: WalkConfig,
    log: _Logger,
    state: _WalkState,
    *,
    depth: int,
) -> Iterator[Candidate]:
    """Recursive helper. ``depth`` is purely for diagnostics.

    Uses :func:`os.scandir` rather than :func:`os.walk` so we get a
    cached :class:`os.DirEntry.stat` per entry — saves a syscall for the
    hot path where most entries are regular files we accept or reject by
    name alone. ``follow_symlinks=False`` on the stat call gives us
    ``lstat`` semantics so a symlinked directory is not auto-followed
    unless the config explicitly opts in.
    """
    try:
        scanner = os.scandir(path)
    except PermissionError as err:
        _log_permission_once(log, path, state, err)
        return
    except FileNotFoundError:
        # Race: directory disappeared between discovery and entry.
        # Story 1.3's watcher will see the delete; the scan keeps going.
        log.debug("scanner.dir_disappeared", path=path)
        return
    except OSError as err:
        # Generic IO failure — log and continue. Common on flaky network
        # mounts.
        log.warning("scanner.scandir_failed", path=path, err=str(err))
        return

    with scanner as entries:
        for entry in entries:
            yield from _process_entry(entry, config, log, state, depth=depth)


def _process_entry(
    entry: os.DirEntry[str],
    config: WalkConfig,
    log: _Logger,
    state: _WalkState,
    *,
    depth: int,
) -> Iterator[Candidate]:
    """Decide whether ``entry`` is a directory to recurse into, a file to
    yield, or noise to skip."""
    name = entry.name

    # Hidden entries are always skipped — both files and directories.
    # The ``.maktaba`` sidecar matches this rule too, but we keep it in
    # the explicit ignore list so future renames stay intentional.
    if name.startswith("."):
        return

    try:
        is_dir = entry.is_dir(follow_symlinks=config.follow_symlinks)
    except PermissionError as err:
        _log_permission_once(log, entry.path, state, err)
        return
    except OSError as err:
        log.debug("scanner.is_dir_failed", path=entry.path, err=str(err))
        return

    if is_dir:
        if name in config.ignore_dirnames:
            log.debug("scanner.dir_pruned", path=entry.path, reason="ignored_name")
            return
        if config.follow_symlinks:
            key = _dev_ino(entry.path)
            if key is not None:
                if key in state.visited:
                    log.debug("scanner.symlink_loop_skipped", path=entry.path)
                    return
                state.visited.add(key)
        yield from _walk_dir(entry.path, config, log, state, depth=depth + 1)
        return

    # Below this point we treat ``entry`` as something file-shaped: a
    # regular file, a symlink to one (when follow_symlinks is on), or a
    # special file we should reject.
    if not _is_acceptable_basename(name, config):
        return

    try:
        is_file = entry.is_file(follow_symlinks=config.follow_symlinks)
    except OSError as err:
        log.debug("scanner.is_file_failed", path=entry.path, err=str(err))
        return
    if not is_file:
        # FIFOs, sockets, devices, dangling symlinks. Story 1.1 treats
        # these as noise — no row, no error.
        return

    try:
        st = entry.stat(follow_symlinks=config.follow_symlinks)
    except (PermissionError, FileNotFoundError, OSError) as err:
        log.debug("scanner.stat_failed", path=entry.path, err=str(err))
        return

    yield Candidate(
        path=entry.path,
        size_bytes=int(st.st_size),
        mtime_ns=int(st.st_mtime_ns),
    )


def _is_acceptable_basename(name: str, config: WalkConfig) -> bool:
    """Apply the extension allowlist and the ignore-basename globs."""
    for pattern in config.ignore_basenames:
        if fnmatch.fnmatch(name, pattern):
            return False
    # ``os.path.splitext`` returns ('a.tar', '.gz') for 'a.tar.gz' which
    # is the behaviour we want — only the final dotted segment counts.
    _, ext = os.path.splitext(name)
    return ext.lower() in config.extensions


def _dev_ino(path: str) -> tuple[int, int] | None:
    """Return ``(st_dev, st_ino)`` for ``path`` or ``None`` on failure.

    Used by the symlink-loop guard. Failures are silent because the
    caller will already have logged the directory-level error; we don't
    want to double-log just to bookkeep the visited set.
    """
    try:
        st = os.stat(path)
    except OSError:
        return None
    return (int(st.st_dev), int(st.st_ino))


def _log_permission_once(
    log: _Logger,
    path: str,
    state: _WalkState,
    err: OSError,
) -> None:
    """Emit at most one ``scanner.permission_denied`` line per scan."""
    if state.permission_logged:
        log.debug("scanner.permission_denied", path=path, errno=err.errno)
        return
    state.permission_logged = True
    log.warning(
        "scanner.permission_denied",
        path=path,
        errno=err.errno,
        errno_name=errno.errorcode.get(err.errno or 0, "UNKNOWN"),
    )
