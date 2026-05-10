"""External subtitle discovery scanning."""

from __future__ import annotations

from pathlib import Path

import pytest

from maktaba_pipeline.media.subtitles.discovery import (
    discover_subtitles_for_video_sync,
)


@pytest.mark.unit
def test_discovers_sibling_srt(tmp_path: Path) -> None:
    video = tmp_path / "Lecture.mp4"
    video.write_bytes(b"")
    sidecar = tmp_path / "Lecture.ar.srt"
    sidecar.write_bytes(b"")

    rows = discover_subtitles_for_video_sync(video, video_id="vid-1")
    assert len(rows) == 1
    assert rows[0].format == "srt"
    assert rows[0].language == "ar"
    assert rows[0].is_external is True
    assert rows[0].is_embedded is False


@pytest.mark.unit
def test_discovers_subs_subdirectory(tmp_path: Path) -> None:
    video = tmp_path / "Lecture.mp4"
    video.write_bytes(b"")
    subs_dir = tmp_path / "Subs"
    subs_dir.mkdir()
    (subs_dir / "Lecture.en.vtt").write_bytes(b"")

    rows = discover_subtitles_for_video_sync(video, video_id="vid-1")
    paths = sorted(r.path for r in rows)
    assert any(p.endswith("Subs/Lecture.en.vtt") for p in paths)


@pytest.mark.unit
def test_unknown_lang_is_undetermined_with_metadata(tmp_path: Path) -> None:
    video = tmp_path / "Lecture.mp4"
    video.write_bytes(b"")
    (tmp_path / "Lecture.zzz.srt").write_bytes(b"")

    rows = discover_subtitles_for_video_sync(video, video_id="vid-1")
    assert len(rows) == 1
    assert rows[0].language == "und"
    assert rows[0].metadata == {"raw_lang_tag": "zzz"}


@pytest.mark.unit
def test_flags_decoded(tmp_path: Path) -> None:
    video = tmp_path / "Lecture.mp4"
    video.write_bytes(b"")
    (tmp_path / "Lecture.en.sdh.vtt").write_bytes(b"")

    rows = discover_subtitles_for_video_sync(video, video_id="vid-1")
    assert len(rows) == 1
    assert rows[0].flags == {
        "forced": False,
        "sdh": True,
        "cc": False,
        "hi": False,
    }


@pytest.mark.unit
def test_unrelated_files_ignored(tmp_path: Path) -> None:
    video = tmp_path / "Lecture.mp4"
    video.write_bytes(b"")
    (tmp_path / "Random.notes.txt").write_bytes(b"")
    (tmp_path / "Other.ar.srt").write_bytes(b"")

    rows = discover_subtitles_for_video_sync(video, video_id="vid-1")
    assert rows == []


@pytest.mark.unit
def test_missing_video_directory_returns_empty(tmp_path: Path) -> None:
    video = tmp_path / "absent" / "Lecture.mp4"
    rows = discover_subtitles_for_video_sync(video, video_id="vid-1")
    assert rows == []
