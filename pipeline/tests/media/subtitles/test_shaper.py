"""Shaper behaviour — pass-through and wrapping."""

from __future__ import annotations

import pytest

from maktaba_pipeline.media.subtitles.cue import Segment
from maktaba_pipeline.media.subtitles.shaper import (
    PassThroughShaper,
    WrappingShaper,
    default_shaper,
)


@pytest.mark.unit
def test_passthrough_one_cue_per_segment() -> None:
    segs = [
        Segment(seq=1, start_sec=0.0, end_sec=1.0, text="hello"),
        Segment(seq=2, start_sec=1.0, end_sec=2.0, text="world", speaker="Alice"),
    ]
    cues = list(PassThroughShaper().shape(segs, language="en"))
    assert len(cues) == 2
    assert cues[0].lines == ("hello",)
    assert cues[0].cue_id == "seq-1"
    assert cues[1].speaker == "Alice"
    assert cues[1].cue_id == "seq-2"


@pytest.mark.unit
def test_wrapping_respects_max_line_chars() -> None:
    text = "the quick brown fox jumps over the lazy dog and runs away fast"
    seg = Segment(seq=1, start_sec=0.0, end_sec=1.0, text=text)
    shaper = WrappingShaper(max_line_chars=20, max_lines=4)
    cue = next(shaper.shape([seg], language="en"))
    for line in cue.lines:
        assert len(line) <= 20


@pytest.mark.unit
def test_wrapping_default_42_char_lines() -> None:
    text = "Once upon a time in a faraway land there lived a wise old owl"
    seg = Segment(seq=1, start_sec=0.0, end_sec=1.0, text=text)
    shaper = WrappingShaper()  # defaults 42/2
    cue = next(shaper.shape([seg], language="en"))
    assert len(cue.lines) <= 2
    for line in cue.lines:
        assert len(line) <= 42


@pytest.mark.unit
def test_wrapping_oversize_token_on_own_line() -> None:
    long_word = "a" * 60
    seg = Segment(
        seq=1,
        start_sec=0.0,
        end_sec=1.0,
        text=f"short {long_word} end",
    )
    shaper = WrappingShaper(max_line_chars=20, max_lines=4)
    cue = next(shaper.shape([seg], language="en"))
    # The oversize token is on its own line — not mid-broken.
    assert long_word in cue.lines


@pytest.mark.unit
def test_wrapping_empty_text_yields_empty_line() -> None:
    seg = Segment(seq=1, start_sec=0.0, end_sec=1.0, text="")
    shaper = WrappingShaper()
    cue = next(shaper.shape([seg], language="en"))
    assert cue.lines == ("",)


@pytest.mark.unit
def test_default_shaper_is_wrapping_shaper() -> None:
    assert isinstance(default_shaper(), WrappingShaper)


@pytest.mark.unit
def test_wrapping_invalid_params_rejected() -> None:
    with pytest.raises(ValueError):
        WrappingShaper(max_line_chars=0)
    with pytest.raises(ValueError):
        WrappingShaper(max_lines=0)
