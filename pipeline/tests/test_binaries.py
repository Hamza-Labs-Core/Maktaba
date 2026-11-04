"""Which ffmpeg / ffprobe the worker runs: the setting, the tool.ffmpeg pack, the PATH."""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

from maktaba_pipeline.binaries import ffmpeg_binary, ffprobe_binary


def _program(folder: Path, name: str) -> Path:
    folder.mkdir(parents=True, exist_ok=True)
    p = folder / (name + ".exe" if sys.platform == "win32" else name)
    p.write_text("#!/bin/sh\n")
    p.chmod(0o755)
    return p


def test_the_installed_ffmpeg_pack_is_found_before_the_path(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Epic 51: with tool.ffmpeg installed (its current pointer's ffmpeg/), the worker runs
    its ffmpeg and ffprobe; MAKTABA_FFMPEG still wins, and without the pack (or with a
    pointer naming no folder) the names on PATH stay."""
    monkeypatch.delenv("MAKTABA_FFMPEG", raising=False)
    monkeypatch.delenv("MAKTABA_FFPROBE", raising=False)
    monkeypatch.setenv("MAKTABA_COMPONENTS_DIR", str(tmp_path))
    assert (ffmpeg_binary(), ffprobe_binary()) == ("ffmpeg", "ffprobe")

    pack = tmp_path / "tool.ffmpeg"
    ff = _program(pack / "1.2.0_abc" / "ffmpeg", "ffmpeg")
    fp = _program(pack / "1.2.0_abc" / "ffmpeg", "ffprobe")
    assert ffmpeg_binary() == "ffmpeg"  # no current pointer yet
    (pack / "current").write_text("1.2.0_abc\n")
    assert (ffmpeg_binary(), ffprobe_binary()) == (str(ff), str(fp))

    monkeypatch.setenv("MAKTABA_FFMPEG", "/own/ffmpeg")
    assert ffmpeg_binary() == "/own/ffmpeg"
    assert ffmpeg_binary("/given/ffmpeg") == "/given/ffmpeg"
    monkeypatch.delenv("MAKTABA_FFMPEG")

    (pack / "current").write_text("0.9.0_gone\n")
    assert ffmpeg_binary() == "ffmpeg"
    monkeypatch.delenv("MAKTABA_COMPONENTS_DIR")
    (pack / "current").write_text("1.2.0_abc\n")
    assert ffmpeg_binary() == "ffmpeg"
