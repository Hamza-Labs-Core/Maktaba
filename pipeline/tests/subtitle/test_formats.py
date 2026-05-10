"""Epic 4 — :mod:`maktaba_pipeline.subtitle.formats` tests."""

from __future__ import annotations

import math

import pytest

from maktaba_pipeline.subtitle.formats import (
    MAX_TIMESTAMP_SEC,
    escape_srt_text,
    escape_vtt_text,
    format_srt_timestamp,
    format_vtt_timestamp,
    wrap_cue,
)

# --- timestamp formatting ---------------------------------------------


def test_srt_timestamp_zero() -> None:
    assert format_srt_timestamp(0) == "00:00:00,000"


def test_srt_timestamp_uses_comma_decimal() -> None:
    assert format_srt_timestamp(3661.005) == "01:01:01,005"


def test_vtt_timestamp_uses_dot_decimal() -> None:
    assert format_vtt_timestamp(3661.005) == "01:01:01.005"


def test_negative_clamped_to_zero() -> None:
    assert format_srt_timestamp(-1.0) == "00:00:00,000"
    assert format_vtt_timestamp(-100.0) == "00:00:00.000"


def test_nan_clamped_to_zero() -> None:
    assert format_srt_timestamp(math.nan) == "00:00:00,000"


def test_overflow_clamped_to_cap() -> None:
    # Past the 99:59:59,999 cap — value clamps but stays parseable.
    assert format_srt_timestamp(MAX_TIMESTAMP_SEC + 1000) == "99:59:59,999"


def test_milliseconds_round_half_up() -> None:
    # 0.0005 → 1 ms rather than 0 ms (no banker's rounding).
    assert format_srt_timestamp(0.0005) == "00:00:00,001"


# --- escaping ---------------------------------------------------------


def test_srt_escape_breaks_arrow_delimiter() -> None:
    escaped = escape_srt_text("nope -->")
    # The cue-delimiter sequence must not appear verbatim in cue text.
    assert "-->" not in escaped


def test_srt_escape_preserves_tag_like_text() -> None:
    # SRT consumers tolerate raw <b> tags; we don't escape them.
    assert escape_srt_text("<b>hello</b>") == "<b>hello</b>"


def test_vtt_escape_html_entities() -> None:
    out = escape_vtt_text("<b>hi</b> a-->b & c")
    assert "<" not in out
    assert ">" not in out
    assert "&amp;" in out
    assert "&lt;" in out
    # The arrow can't be a valid delimiter anymore — the `>` is &gt;.
    assert "-->" not in out


def test_vtt_escape_amp_first() -> None:
    # ``escape_vtt_text`` must not double-escape entities.
    out = escape_vtt_text("&<>")
    assert out == "&amp;&lt;&gt;"


# --- cue wrapping (Story 4.2) -----------------------------------------


def test_wrap_cue_short_text_returns_single_cue_single_line() -> None:
    assert wrap_cue("hello world") == ["hello world"]


def test_wrap_cue_empty_returns_empty_list() -> None:
    assert wrap_cue("") == []
    assert wrap_cue("   ") == []


def test_wrap_cue_wraps_to_two_lines_when_over_line_chars() -> None:
    text = " ".join(["word"] * 20)  # 99 chars including separators
    cues = wrap_cue(text, line_chars=40, max_lines=2)
    assert len(cues) >= 1
    # First cue has at most two lines.
    assert cues[0].count("\n") <= 1
    # No individual line exceeds the limit (since "word" fits).
    for cue in cues:
        for line in cue.split("\n"):
            assert len(line) <= 40


def test_wrap_cue_splits_into_multiple_cues_when_over_max_lines() -> None:
    text = " ".join(["abcd"] * 30)  # forces overflow past 2-line limit
    cues = wrap_cue(text, line_chars=10, max_lines=2)
    assert len(cues) >= 2
    for cue in cues:
        assert cue.count("\n") <= 1


def test_wrap_cue_long_token_overflows_rather_than_breaks_word() -> None:
    # Mid-word breaks are silent and worse than overflow; the token
    # stays intact and is allowed to exceed the line cap.
    long_token = "a" * 120
    cues = wrap_cue(long_token, line_chars=40, max_lines=2)
    assert cues == [long_token]


def test_wrap_cue_rejects_non_positive_limits() -> None:
    with pytest.raises(ValueError):
        wrap_cue("hi", line_chars=0)
    with pytest.raises(ValueError):
        wrap_cue("hi", max_lines=0)
