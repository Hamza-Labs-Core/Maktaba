"""Atomic SRT/VTT pair writer."""

from __future__ import annotations

from pathlib import Path

import pytest

from maktaba_pipeline.media.subtitles.atomic import write_atomic_pair


@pytest.mark.unit
def test_write_atomic_pair_lands_both_files(tmp_path: Path) -> None:
    subs = tmp_path / "subs"
    subs.mkdir()
    tmp_dir = tmp_path / ".tmp"
    tmp_dir.mkdir()

    srt = subs / "out.srt"
    vtt = subs / "out.vtt"
    write_atomic_pair(
        srt, b"srt-bytes",
        vtt, b"vtt-bytes",
        tmp_dir=tmp_dir,
    )
    assert srt.read_bytes() == b"srt-bytes"
    assert vtt.read_bytes() == b"vtt-bytes"


@pytest.mark.unit
def test_write_atomic_pair_overwrites_existing(tmp_path: Path) -> None:
    subs = tmp_path / "subs"
    subs.mkdir()
    tmp_dir = tmp_path / ".tmp"
    tmp_dir.mkdir()
    srt = subs / "out.srt"
    vtt = subs / "out.vtt"
    srt.write_bytes(b"old-srt")
    vtt.write_bytes(b"old-vtt")

    write_atomic_pair(
        srt, b"new-srt",
        vtt, b"new-vtt",
        tmp_dir=tmp_dir,
    )
    assert srt.read_bytes() == b"new-srt"
    assert vtt.read_bytes() == b"new-vtt"


@pytest.mark.unit
def test_write_atomic_pair_cleans_tmp_dir(tmp_path: Path) -> None:
    subs = tmp_path / "subs"
    subs.mkdir()
    tmp_dir = tmp_path / ".tmp"
    tmp_dir.mkdir()

    write_atomic_pair(
        subs / "out.srt", b"a",
        subs / "out.vtt", b"b",
        tmp_dir=tmp_dir,
    )
    # No tempfiles should remain after a successful run.
    assert list(tmp_dir.iterdir()) == []
