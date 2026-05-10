"""SRT writer round-trip — parse the output manually so the test
doesn't depend on an external SRT library."""

from __future__ import annotations

import re

import pytest

from maktaba_pipeline.media.subtitles.cue import Cue
from maktaba_pipeline.media.subtitles.srt_writer import (
    format_srt_timestamp,
    write_srt,
)


def _parse_srt(text: str) -> list[tuple[int, str, str, list[str]]]:
    """Tiny SRT parser used only by these tests."""
    blocks = re.split(r"\r\n\r\n", text.strip("\r\n"))
    parsed: list[tuple[int, str, str, list[str]]] = []
    for block in blocks:
        lines = block.split("\r\n")
        idx = int(lines[0])
        start, _, end = lines[1].partition(" --> ")
        body = lines[2:]
        parsed.append((idx, start, end, body))
    return parsed


@pytest.mark.unit
def test_timestamp_format_zero() -> None:
    assert format_srt_timestamp(0.0) == "00:00:00,000"


@pytest.mark.unit
def test_timestamp_format_complex() -> None:
    # 1h 2m 3s 456ms
    seconds = 3600 + 120 + 3 + 0.456
    assert format_srt_timestamp(seconds) == "01:02:03,456"


@pytest.mark.unit
def test_negative_timestamp_clamped_to_zero() -> None:
    assert format_srt_timestamp(-1.5) == "00:00:00,000"


@pytest.mark.unit
def test_write_srt_single_cue() -> None:
    cue = Cue(
        start_sec=1.0,
        end_sec=2.5,
        lines=("Hello, world",),
    )
    out = write_srt([cue]).decode("utf-8")
    parsed = _parse_srt(out)
    assert parsed == [(1, "00:00:01,000", "00:00:02,500", ["Hello, world"])]


@pytest.mark.unit
def test_write_srt_cue_numbers_are_one_indexed_and_monotonic() -> None:
    cues = [
        Cue(start_sec=0.0, end_sec=1.0, lines=("a",)),
        Cue(start_sec=1.0, end_sec=2.0, lines=("b",)),
        Cue(start_sec=2.0, end_sec=3.0, lines=("c",)),
    ]
    out = write_srt(cues).decode("utf-8")
    parsed = _parse_srt(out)
    assert [row[0] for row in parsed] == [1, 2, 3]


@pytest.mark.unit
def test_write_srt_escapes_html_chars() -> None:
    cue = Cue(start_sec=0.0, end_sec=1.0, lines=("Tom & <Jerry>",))
    out = write_srt([cue]).decode("utf-8")
    assert "Tom &amp; &lt;Jerry&gt;" in out


@pytest.mark.unit
def test_write_srt_uses_crlf_line_endings() -> None:
    cue = Cue(start_sec=0.0, end_sec=1.0, lines=("line1", "line2"))
    out = write_srt([cue]).decode("utf-8")
    # Every internal separator and the trailing blank line use CRLF.
    assert out.endswith("\r\n\r\n")
    assert "line1\r\nline2" in out


@pytest.mark.unit
def test_write_srt_returns_bytes() -> None:
    out = write_srt([Cue(start_sec=0.0, end_sec=1.0, lines=("x",))])
    assert isinstance(out, bytes)


@pytest.mark.unit
def test_write_srt_drops_speaker_tags() -> None:
    cue = Cue(start_sec=0.0, end_sec=1.0, lines=("hi",), speaker="Alice")
    out = write_srt([cue]).decode("utf-8")
    assert "Alice" not in out
    assert "<v" not in out
