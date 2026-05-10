"""Pure chunker plumbing: segmenter → packer → language tag."""

from __future__ import annotations

import pytest

from maktaba_pipeline.search.chunker import chunk_segments_into_units
from maktaba_pipeline.search.models import SegmentRow


def _seg(sid: int, text: str, start: float, end: float) -> SegmentRow:
    return SegmentRow(id=sid, seq=sid, start_sec=start, end_sec=end, text=text)


@pytest.mark.unit
def test_chunk_returns_nonempty_for_typical_input() -> None:
    segments = [
        _seg(1, "Hello world.", 0.0, 1.0),
        _seg(2, "This is the second sentence.", 1.0, 2.0),
        _seg(3, "And one more here.", 2.0, 3.0),
    ]
    units = chunk_segments_into_units(segments, language="en")
    assert units
    assert all(u.language == "en" for u in units)


@pytest.mark.unit
def test_first_segment_id_maps_back_to_start_sec() -> None:
    segments = [
        _seg(101, "Hello world.", 5.0, 6.0),
        _seg(102, "Goodbye.", 6.0, 7.0),
    ]
    units = chunk_segments_into_units(segments, language="en")
    assert units
    first_unit = units[0]
    assert first_unit.segment_ids[0] == 101
    # And the unit's start_sec matches the first segment's start.
    assert first_unit.start_sec == pytest.approx(5.0)


@pytest.mark.unit
def test_empty_segments_yields_no_units() -> None:
    assert chunk_segments_into_units([], language="en") == []


@pytest.mark.unit
def test_arabic_language_is_tagged() -> None:
    segments = [
        _seg(1, "السلام عليكم. كيف حالك؟", 0.0, 5.0),
    ]
    units = chunk_segments_into_units(segments, language="ar")
    assert units
    for u in units:
        assert u.language == "ar"
