"""ChapterInferer: end-to-end with a toy three-cluster fixture."""

from __future__ import annotations

from typing import Any

import pytest

from maktaba_pipeline.chapter.inferer import ChapterInferer, ChapterRow

VIDEO_ID = "vid-1"


def _make_units(starts: list[float], lang: str = "ar") -> list[dict[str, Any]]:
    return [
        {
            "seq": i,
            "start_sec": start,
            "end_sec": start + 10.0,
            "language": lang,
        }
        for i, start in enumerate(starts)
    ]


@pytest.mark.unit
def test_infer_emits_chapter_zero_at_start() -> None:
    units = _make_units([0.0, 30.0, 60.0])
    embeddings = [[1.0, 0.0, 0.0], [1.0, 0.0, 0.0], [1.0, 0.0, 0.0]]
    inferer = ChapterInferer()
    rows = inferer.infer(
        video_id=VIDEO_ID,
        transcript_id=42,
        units=units,
        embeddings=embeddings,
        video_duration_sec=120.0,
    )
    assert rows[0].seq == 0
    assert rows[0].start_sec == 0.0
    assert rows[0].confidence == 1.0
    # Single chapter spanning the whole video.
    assert rows[-1].end_sec == pytest.approx(120.0)


@pytest.mark.unit
def test_infer_last_chapter_ends_at_video_duration() -> None:
    # Three-cluster fixture, well-spaced in time so the merger keeps
    # the boundaries.
    starts = [0.0, 20.0, 40.0, 200.0, 220.0, 240.0, 400.0, 420.0, 440.0]
    units = _make_units(starts)
    embeddings = [
        [1.0, 0.0, 0.0], [0.95, 0.05, 0.0], [0.9, 0.1, 0.0],
        [0.0, 1.0, 0.0], [0.05, 0.95, 0.0], [0.1, 0.9, 0.0],
        [0.0, 0.0, 1.0], [0.05, 0.0, 0.95], [0.1, 0.0, 0.9],
    ]
    inferer = ChapterInferer(threshold=0.3, smoothing_window=1, min_chapter_sec=100.0)
    rows = inferer.infer(
        video_id=VIDEO_ID,
        transcript_id=42,
        units=units,
        embeddings=embeddings,
        video_duration_sec=600.0,
    )
    # First chapter starts at zero, last chapter ends at duration.
    assert rows[0].start_sec == 0.0
    assert rows[-1].end_sec == pytest.approx(600.0)
    # We should have at least three chapters (0, A→B, B→C).
    assert len(rows) >= 3


@pytest.mark.unit
def test_infer_assigns_sequential_seq() -> None:
    starts = [0.0, 200.0, 400.0, 600.0]
    units = _make_units(starts)
    embeddings = [
        [1.0, 0.0],
        [-1.0, 0.0],
        [0.0, 1.0],
        [0.0, -1.0],
    ]
    inferer = ChapterInferer(threshold=0.2, smoothing_window=1, min_chapter_sec=100.0)
    rows = inferer.infer(
        video_id=VIDEO_ID,
        transcript_id=1,
        units=units,
        embeddings=embeddings,
        video_duration_sec=800.0,
    )
    seqs = [r.seq for r in rows]
    assert seqs == list(range(len(rows)))


@pytest.mark.unit
def test_infer_picks_lang_from_head_units() -> None:
    units = [
        {"seq": 0, "start_sec": 0.0, "end_sec": 10.0, "language": "ar"},
        {"seq": 1, "start_sec": 200.0, "end_sec": 210.0, "language": "ar"},
        {"seq": 2, "start_sec": 400.0, "end_sec": 410.0, "language": "ar"},
    ]
    embeddings = [[1.0, 0.0], [0.0, 1.0], [1.0, 0.0]]
    inferer = ChapterInferer(threshold=0.2, smoothing_window=1, min_chapter_sec=100.0)
    rows = inferer.infer(
        video_id=VIDEO_ID,
        transcript_id=1,
        units=units,
        embeddings=embeddings,
        video_duration_sec=600.0,
    )
    assert all(r.lang == "ar" for r in rows)


@pytest.mark.unit
def test_infer_marks_mixed_lang_when_head_mixed() -> None:
    units = [
        {"seq": 0, "start_sec": 0.0, "end_sec": 10.0, "language": "ar"},
        {"seq": 1, "start_sec": 30.0, "end_sec": 40.0, "language": "en"},
        {"seq": 2, "start_sec": 60.0, "end_sec": 70.0, "language": "ar"},
    ]
    embeddings = [[1.0, 0.0], [1.0, 0.0], [1.0, 0.0]]
    inferer = ChapterInferer()
    rows = inferer.infer(
        video_id=VIDEO_ID,
        transcript_id=1,
        units=units,
        embeddings=embeddings,
        video_duration_sec=120.0,
    )
    assert rows[0].lang == "mixed"


@pytest.mark.unit
def test_infer_empty_units_returns_single_chapter() -> None:
    inferer = ChapterInferer()
    rows = inferer.infer(
        video_id=VIDEO_ID,
        transcript_id=99,
        units=[],
        embeddings=[],
        video_duration_sec=300.0,
    )
    assert len(rows) == 1
    assert isinstance(rows[0], ChapterRow)
    assert rows[0].seq == 0
    assert rows[0].start_sec == 0.0
    assert rows[0].end_sec == 300.0
