"""Hard-link alias + read-only fallback behaviour."""

from __future__ import annotations

import os
import sys
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.media.subtitles.alias import alias_copy


class _CapturingLogger:
    """Minimal structlog stand-in that records calls for assertions."""

    def __init__(self) -> None:
        self.events: list[tuple[str, dict[str, Any]]] = []

    def warning(self, event: str, **kwargs: Any) -> None:
        self.events.append((event, kwargs))

    def info(self, event: str, **kwargs: Any) -> None:  # pragma: no cover
        self.events.append((event, kwargs))


@pytest.mark.unit
def test_alias_copy_hard_links_same_inode(tmp_path: Path) -> None:
    source = tmp_path / "canonical.srt"
    source.write_bytes(b"hello")
    alias = tmp_path / "alias.srt"

    log = _CapturingLogger()
    ok = alias_copy(source, alias, log=log)
    assert ok is True
    assert alias.read_bytes() == b"hello"
    # Hard link → same inode.
    assert source.stat().st_ino == alias.stat().st_ino


@pytest.mark.unit
def test_alias_copy_idempotent_when_alias_already_linked(tmp_path: Path) -> None:
    source = tmp_path / "canonical.srt"
    source.write_bytes(b"hello")
    alias = tmp_path / "alias.srt"
    os.link(source, alias)

    log = _CapturingLogger()
    assert alias_copy(source, alias, log=log) is True
    assert log.events == []  # no warning logged


@pytest.mark.unit
def test_alias_copy_collision_with_different_file(tmp_path: Path) -> None:
    source = tmp_path / "canonical.srt"
    source.write_bytes(b"hello")
    alias = tmp_path / "alias.srt"
    alias.write_bytes(b"other content")  # different inode

    log = _CapturingLogger()
    ok = alias_copy(source, alias, log=log)
    assert ok is False
    assert any(e[1].get("kind") == "alias_collision" for e in log.events)
    # We did NOT overwrite the existing file.
    assert alias.read_bytes() == b"other content"


@pytest.mark.unit
def test_alias_copy_missing_parent_logs_and_returns_false(tmp_path: Path) -> None:
    source = tmp_path / "canonical.srt"
    source.write_bytes(b"hello")
    alias = tmp_path / "does_not_exist" / "alias.srt"

    log = _CapturingLogger()
    assert alias_copy(source, alias, log=log) is False
    assert any(e[1].get("kind") == "alias_copy_failed" for e in log.events)


@pytest.mark.unit
@pytest.mark.skipif(sys.platform == "win32", reason="POSIX-only perms")
def test_alias_copy_readonly_parent_logs_and_returns_false(tmp_path: Path) -> None:
    if os.geteuid() == 0:
        pytest.skip("root can write anywhere")
    source = tmp_path / "canonical.srt"
    source.write_bytes(b"hello")
    target_dir = tmp_path / "ro"
    target_dir.mkdir(mode=0o555)
    alias = target_dir / "alias.srt"

    log = _CapturingLogger()
    try:
        ok = alias_copy(source, alias, log=log)
    finally:
        target_dir.chmod(0o755)
    assert ok is False
    assert any(e[1].get("kind") == "alias_copy_failed" for e in log.events)
