"""Behaviour tests for :mod:`maktaba_pipeline.scanner.walker` (Story 1.1).

Each test builds a temporary tree under ``tmp_path``, runs :func:`walk`,
and asserts the set of emitted relative paths. The walker is purely
filesystem — no DB, no asyncio — so these tests run as the ``unit``
tier (no sockets, no containers).
"""

from __future__ import annotations

import os
import stat
import sys
from collections.abc import Iterable
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.scanner.walker import (
    DEFAULT_VIDEO_EXTENSIONS,
    Candidate,
    WalkConfig,
    walk,
)


@dataclass
class _RecordingLogger:
    """Captures structlog-style calls so tests can pin log shape."""

    warnings: list[tuple[str, dict[str, Any]]] = field(default_factory=list)
    debugs: list[tuple[str, dict[str, Any]]] = field(default_factory=list)
    infos: list[tuple[str, dict[str, Any]]] = field(default_factory=list)
    errors: list[tuple[str, dict[str, Any]]] = field(default_factory=list)

    def warning(self, event: str, **kwargs: Any) -> None:
        self.warnings.append((event, kwargs))

    def debug(self, event: str, **kwargs: Any) -> None:
        self.debugs.append((event, kwargs))

    def info(self, event: str, **kwargs: Any) -> None:
        self.infos.append((event, kwargs))

    def error(self, event: str, **kwargs: Any) -> None:
        self.errors.append((event, kwargs))


def _touch(root: Path, rel: str, *, content: bytes = b"x") -> Path:
    """Create a regular file ``rel`` (relative to ``root``) and parents."""
    p = root / rel
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_bytes(content)
    return p


def _rels(root: Path, candidates: Iterable[Candidate]) -> list[str]:
    """Return sorted POSIX-style relative paths for assertion comparisons.

    Does not resolve symlinks — the walker emits the path as observed
    (so a symlinked file is reported under the link's name, not the
    target's). Tests pin that behaviour, so :meth:`Path.resolve` would
    silently lie here.
    """
    out: list[str] = []
    for c in candidates:
        rel = os.path.relpath(c.path, start=str(root))
        out.append(Path(rel).as_posix())
    out.sort()
    return out


@pytest.mark.unit
def test_walk_yields_only_supported_extensions(tmp_path: Path) -> None:
    _touch(tmp_path, "a.mp4")
    _touch(tmp_path, "b.MKV")  # case-insensitive
    _touch(tmp_path, "c.txt")
    _touch(tmp_path, "d.jpg")
    _touch(tmp_path, "subdir/e.webm")
    log = _RecordingLogger()

    got = _rels(tmp_path, walk(tmp_path, WalkConfig(), log))

    assert got == ["a.mp4", "b.MKV", "subdir/e.webm"]


@pytest.mark.unit
def test_walk_emits_size_and_mtime(tmp_path: Path) -> None:
    p = _touch(tmp_path, "a.mp4", content=b"hello")
    log = _RecordingLogger()

    candidates = list(walk(tmp_path, WalkConfig(), log))

    assert len(candidates) == 1
    c = candidates[0]
    st = p.stat()
    assert c.size_bytes == 5
    assert c.size_bytes == st.st_size
    assert c.mtime_ns == st.st_mtime_ns


@pytest.mark.unit
def test_walk_skips_hidden_files_and_directories(tmp_path: Path) -> None:
    _touch(tmp_path, ".hidden.mp4")
    _touch(tmp_path, ".cache/inner.mp4")
    _touch(tmp_path, "visible.mp4")
    log = _RecordingLogger()

    got = _rels(tmp_path, walk(tmp_path, WalkConfig(), log))

    assert got == ["visible.mp4"]


@pytest.mark.unit
def test_walk_skips_partial_download_globs(tmp_path: Path) -> None:
    _touch(tmp_path, "movie.mp4.part")
    _touch(tmp_path, "show.crdownload")
    _touch(tmp_path, "draft.partial")
    _touch(tmp_path, "scratch.tmp")
    _touch(tmp_path, "good.mp4")
    log = _RecordingLogger()

    got = _rels(tmp_path, walk(tmp_path, WalkConfig(), log))

    assert got == ["good.mp4"]


@pytest.mark.unit
def test_walk_prunes_maktaba_sidecar_directory(tmp_path: Path) -> None:
    _touch(tmp_path, ".maktaba/sidecar.mp4")
    _touch(tmp_path, "show.mp4")
    log = _RecordingLogger()

    got = _rels(tmp_path, walk(tmp_path, WalkConfig(), log))

    assert got == ["show.mp4"]


@pytest.mark.unit
def test_walk_prunes_custom_ignored_dirnames(tmp_path: Path) -> None:
    _touch(tmp_path, "Trash/lost.mp4")
    _touch(tmp_path, "kept/found.mp4")
    log = _RecordingLogger()

    cfg = WalkConfig(ignore_dirnames=frozenset({".maktaba", "Trash"}))
    got = _rels(tmp_path, walk(tmp_path, cfg, log))

    assert got == ["kept/found.mp4"]


@pytest.mark.unit
def test_walk_skips_special_files(tmp_path: Path) -> None:
    """FIFOs / sockets are filesystem entries the walker must reject."""
    if sys.platform == "win32":
        pytest.skip("FIFOs not available on Windows")
    fifo = tmp_path / "named.pipe"
    os.mkfifo(fifo)
    _touch(tmp_path, "real.mp4")
    log = _RecordingLogger()

    got = _rels(tmp_path, walk(tmp_path, WalkConfig(), log))

    assert got == ["real.mp4"]


@pytest.mark.unit
def test_walk_skips_symlinks_by_default(tmp_path: Path) -> None:
    """``follow_symlinks=False`` means a symlinked file is not yielded."""
    target = _touch(tmp_path, "outside/real.mp4")
    link = tmp_path / "linked.mp4"
    link.symlink_to(target)
    log = _RecordingLogger()

    got = _rels(tmp_path, walk(tmp_path, WalkConfig(), log))

    # Both ``outside/real.mp4`` (the regular file) and the symlink would
    # ordinarily be candidates, but the symlink is filtered because we
    # ``lstat`` and reject non-regular entries.
    assert got == ["outside/real.mp4"]


@pytest.mark.unit
def test_walk_follows_symlinks_when_opted_in(tmp_path: Path) -> None:
    target = _touch(tmp_path, "real.mp4")
    link = tmp_path / "linked.mp4"
    link.symlink_to(target)
    log = _RecordingLogger()

    cfg = WalkConfig(follow_symlinks=True)
    got = _rels(tmp_path, walk(tmp_path, cfg, log))

    assert got == ["linked.mp4", "real.mp4"]


@pytest.mark.unit
def test_walk_breaks_symlink_loops_when_following(tmp_path: Path) -> None:
    """``(st_dev, st_ino)`` visited set must catch a loop without OOM/stack."""
    a = tmp_path / "a"
    b = tmp_path / "b"
    a.mkdir()
    b.mkdir()
    (a / "to_b").symlink_to(b, target_is_directory=True)
    (b / "to_a").symlink_to(a, target_is_directory=True)
    _touch(a, "movie.mp4")
    log = _RecordingLogger()

    cfg = WalkConfig(follow_symlinks=True)
    got = _rels(tmp_path, walk(tmp_path, cfg, log))

    # The same movie.mp4 may appear under multiple paths thanks to the
    # symlinked directories — what matters is that the walk terminated
    # at all (no infinite recursion). At minimum the canonical path is
    # present.
    assert "a/movie.mp4" in got
    # ``scanner.symlink_loop_skipped`` should fire at least once.
    loop_events = [event for event, _ in log.debugs if event == "scanner.symlink_loop_skipped"]
    assert loop_events, "symlink loop guard never triggered"


@pytest.mark.unit
def test_walk_swallows_permission_denied_dir_and_logs_once(tmp_path: Path) -> None:
    if sys.platform == "win32":
        pytest.skip("POSIX permission semantics required")
    if os.geteuid() == 0:
        pytest.skip("root bypasses permission bits")

    locked = tmp_path / "locked"
    locked.mkdir()
    inner = _touch(locked, "inner.mp4")
    _ = inner  # the file exists but is unreachable
    open_root = _touch(tmp_path, "open.mp4")
    _ = open_root
    locked2 = tmp_path / "locked2"
    locked2.mkdir()
    _touch(locked2, "also.mp4")

    locked.chmod(0)
    locked2.chmod(0)
    try:
        log = _RecordingLogger()
        got = _rels(tmp_path, walk(tmp_path, WalkConfig(), log))
        # Two locked directories, one warning total — the rest stay at
        # DEBUG to keep the log readable.
        assert got == ["open.mp4"]
        assert sum(1 for event, _ in log.warnings if event == "scanner.permission_denied") == 1
    finally:
        # Restore so pytest's tmp_path cleanup can succeed.
        locked.chmod(stat.S_IRWXU)
        locked2.chmod(stat.S_IRWXU)


@pytest.mark.unit
def test_walk_does_not_log_above_debug_for_extension_filter(tmp_path: Path) -> None:
    """Story 1.1 AC3 — non-supported extensions produce no log noise above DEBUG."""
    for name in ("a.txt", "b.jpg", "c.tar", "d.bin", "e.mov"):
        _touch(tmp_path, name)
    log = _RecordingLogger()

    list(walk(tmp_path, WalkConfig(), log))

    assert log.warnings == []
    assert log.errors == []
    assert log.infos == []


@pytest.mark.unit
def test_walk_handles_missing_root(tmp_path: Path) -> None:
    """A missing root produces a single FileNotFoundError-style debug, not a crash."""
    log = _RecordingLogger()
    missing = tmp_path / "does_not_exist"

    got = list(walk(missing, WalkConfig(), log))

    assert got == []
    # The walker logs at debug level for vanished directories — no
    # warning above debug for a missing root subdir.
    assert log.warnings == []


@pytest.mark.unit
def test_walk_default_extensions_match_story_spec() -> None:
    """Story 1.1 AC3 supported list."""
    expected = frozenset({".mp4", ".mkv", ".mov", ".webm", ".avi", ".ts", ".m4v"})
    assert expected == DEFAULT_VIDEO_EXTENSIONS
