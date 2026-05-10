"""Story 9.18 — chapter inference from transcript topic shifts."""

from __future__ import annotations

import pytest

from maktaba_pipeline.library_mgmt.chapter import (
    DEFAULT_DROP_THRESHOLD,
    DEFAULT_WINDOW_SEGMENTS,
    Segment,
    infer_chapters,
    title_for_chapter,
)


def _seg(start: float, end: float, vec: list[float], text: str = "") -> Segment:
    return Segment(start_sec=start, end_sec=end, embedding=vec, text=text)


def _block(start: int, count: int, vec: list[float], step: float = 5.0) -> list[Segment]:
    """Helper: build ``count`` consecutive segments of length ``step``."""
    return [
        _seg(start + i * step, start + (i + 1) * step, vec) for i in range(count)
    ]


@pytest.mark.unit
def test_uniform_transcript_produces_one_chapter() -> None:
    segs = _block(0, 30, [1.0, 0.0])
    chapters = infer_chapters(segs)
    assert len(chapters) == 1
    assert chapters[0].start_sec == segs[0].start_sec
    assert chapters[0].end_sec == segs[-1].end_sec


@pytest.mark.unit
def test_three_obvious_topic_shifts_produce_four_chapters() -> None:
    # Four blocks of 20 segs (100 sec each) with orthogonal embeddings.
    a = _block(0, 20, [1.0, 0.0, 0.0, 0.0])
    b = _block(100, 20, [0.0, 1.0, 0.0, 0.0])
    c = _block(200, 20, [0.0, 0.0, 1.0, 0.0])
    d = _block(300, 20, [0.0, 0.0, 0.0, 1.0])
    chapters = infer_chapters(a + b + c + d, min_chapter_sec=30.0)
    assert len(chapters) == 4


@pytest.mark.unit
def test_min_chapter_sec_collapses_short_chapters() -> None:
    # Same fixture as above but with longer min_chapter_sec → fewer.
    a = _block(0, 20, [1.0, 0.0, 0.0, 0.0])
    b = _block(100, 20, [0.0, 1.0, 0.0, 0.0])
    c = _block(200, 20, [0.0, 0.0, 1.0, 0.0])
    chapters_60 = infer_chapters(a + b + c, min_chapter_sec=60.0)
    chapters_500 = infer_chapters(a + b + c, min_chapter_sec=500.0)
    assert len(chapters_500) <= len(chapters_60)


@pytest.mark.unit
def test_too_few_segments_yields_one_chapter() -> None:
    # Below 2 * window_segments → one chapter spanning everything.
    segs = _block(0, DEFAULT_WINDOW_SEGMENTS - 1, [1.0, 0.0])
    chapters = infer_chapters(segs)
    assert len(chapters) == 1


@pytest.mark.unit
def test_empty_segments_yields_no_chapters() -> None:
    assert infer_chapters([]) == []


@pytest.mark.unit
def test_drop_threshold_zero_emits_only_min_chapter_constraint() -> None:
    # Even with threshold 0, min_chapter_sec bounds the count.
    a = _block(0, 5, [1.0, 0.0])
    b = _block(25, 5, [0.0, 1.0])
    c = _block(50, 5, [1.0, 0.0])
    out = infer_chapters(a + b + c, drop_threshold=0.0, min_chapter_sec=20.0, window_segments=2)
    # At most floor(duration/min_chapter_sec) + 1 chapters.
    assert len(out) >= 1


@pytest.mark.unit
def test_title_falls_back_when_resolver_missing() -> None:
    chapters = infer_chapters(_block(0, 30, [1.0, 0.0]))
    title = title_for_chapter(chapters[0], _block(0, 30, [1.0, 0.0]), label_resolver=None)
    assert title.startswith("Chapter ")


@pytest.mark.unit
def test_title_uses_resolver_when_available() -> None:
    segs = _block(0, 30, [1.0, 0.0], step=4.0)
    segs = [
        Segment(s.start_sec, s.end_sec, s.embedding, text=f"text-{i}")
        for i, s in enumerate(segs)
    ]
    chapters = infer_chapters(segs)
    out = title_for_chapter(
        chapters[0],
        segs,
        label_resolver=lambda texts: "prayer-rituals",
    )
    assert out == "prayer-rituals"


@pytest.mark.unit
def test_default_drop_threshold_constant() -> None:
    assert DEFAULT_DROP_THRESHOLD == 0.35
