"""Sentence-boundary detection covers Latin + Arabic punctuation."""

from __future__ import annotations

import pytest

from maktaba_pipeline.search.models import SegmentRow
from maktaba_pipeline.search.segmenter import split_into_sentences


def _seg(seg_id: int, text: str, start: float = 0.0, end: float = 1.0) -> SegmentRow:
    return SegmentRow(id=seg_id, seq=seg_id, start_sec=start, end_sec=end, text=text)


@pytest.mark.unit
def test_english_period_breaks_sentence() -> None:
    out = split_into_sentences([_seg(1, "Hello world. This is fine.")])
    assert len(out) == 2
    assert out[0].text == "Hello world."
    assert out[1].text == "This is fine."


@pytest.mark.unit
def test_arabic_question_mark_breaks_sentence() -> None:
    # U+061F (Arabic ?) is the second-clause terminator.
    out = split_into_sentences([_seg(1, "كيف حالك؟ بخير الحمد لله.")])
    assert len(out) == 2
    assert out[0].text == "كيف حالك؟"
    assert out[1].text == "بخير الحمد لله."


@pytest.mark.unit
def test_sentence_spans_multiple_segments() -> None:
    s1 = _seg(1, "Hello", start=0.0, end=1.0)
    s2 = _seg(2, "world.", start=1.0, end=2.0)
    out = split_into_sentences([s1, s2])
    assert len(out) == 1
    assert out[0].segment_ids == (1, 2)
    assert out[0].start_sec == pytest.approx(0.0)
    assert out[0].end_sec == pytest.approx(2.0)


@pytest.mark.unit
def test_empty_input_yields_empty_list() -> None:
    assert split_into_sentences([]) == []


@pytest.mark.unit
def test_no_terminator_yields_one_sentence() -> None:
    out = split_into_sentences([_seg(1, "no terminator here")])
    assert len(out) == 1
    assert out[0].text == "no terminator here"


@pytest.mark.unit
def test_newline_breaks_sentence() -> None:
    out = split_into_sentences([_seg(1, "first line\nsecond line")])
    assert len(out) == 2
    assert out[0].text == "first line"
    assert out[1].text == "second line"
