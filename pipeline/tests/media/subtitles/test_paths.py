"""Path helpers — canonical, alias, ensure_sidecar_dirs."""

from __future__ import annotations

import os
import sys
from pathlib import Path

import pytest

from maktaba_pipeline.media.subtitles.paths import (
    alias_path_for,
    canonical_subtitle_path,
    ensure_sidecar_dirs,
)


@pytest.mark.unit
def test_canonical_path_layout(tmp_path: Path) -> None:
    p = canonical_subtitle_path(tmp_path, "abc123", "en", "vtt")
    assert p == tmp_path / ".maktaba" / "subs" / "abc123.en.vtt"


@pytest.mark.unit
def test_alias_path_uses_lang_fmt_infix(tmp_path: Path) -> None:
    video = tmp_path / "Lecture 01.mp4"
    p = alias_path_for(video, "ar", "srt")
    assert p == tmp_path / "Lecture 01.ar.srt"


@pytest.mark.unit
def test_alias_path_preserves_multi_dot_stem(tmp_path: Path) -> None:
    # Path.stem only strips the final ".mp4" suffix — what we want.
    video = tmp_path / "Talk.2024.S01E02.mp4"
    p = alias_path_for(video, "en", "vtt")
    assert p.name == "Talk.2024.S01E02.en.vtt"


@pytest.mark.unit
def test_ensure_sidecar_dirs_creates_layout(tmp_path: Path) -> None:
    maktaba = ensure_sidecar_dirs(tmp_path)
    assert maktaba == tmp_path / ".maktaba"
    assert (tmp_path / ".maktaba" / "subs").is_dir()
    assert (tmp_path / ".maktaba" / ".tmp").is_dir()


@pytest.mark.unit
def test_ensure_sidecar_dirs_idempotent(tmp_path: Path) -> None:
    ensure_sidecar_dirs(tmp_path)
    # Second call must not raise.
    ensure_sidecar_dirs(tmp_path)


@pytest.mark.unit
@pytest.mark.skipif(sys.platform == "win32", reason="POSIX-only perms")
def test_ensure_sidecar_dirs_raises_on_readonly_root(tmp_path: Path) -> None:
    if os.geteuid() == 0:
        pytest.skip("root can write anywhere")
    readonly = tmp_path / "ro"
    readonly.mkdir(mode=0o555)
    try:
        with pytest.raises(OSError) as excinfo:
            ensure_sidecar_dirs(readonly)
        assert getattr(excinfo.value, "kind", None) == "sidecar_dir"
    finally:
        readonly.chmod(0o755)
