"""Edge cases for the HTML-style escape used by both writers."""

from __future__ import annotations

import pytest

from maktaba_pipeline.media.subtitles.escape import (
    escape_cue_text,
    escape_speaker_label,
)


@pytest.mark.unit
def test_ampersand_escaped_first() -> None:
    assert escape_cue_text("Tom & Jerry") == "Tom &amp; Jerry"


@pytest.mark.unit
def test_lt_gt_escaped() -> None:
    assert (
        escape_cue_text("<script>alert(1)</script>")
        == "&lt;script&gt;alert(1)&lt;/script&gt;"
    )


@pytest.mark.unit
def test_no_double_escape_of_ampersand_in_entity() -> None:
    # If a literal "&amp;" appears in input it must become "&amp;amp;",
    # not stay as "&amp;" — proves we escape ampersands first.
    assert escape_cue_text("&amp;") == "&amp;amp;"


@pytest.mark.unit
def test_idempotent_on_safe_input() -> None:
    assert escape_cue_text("plain text") == "plain text"


@pytest.mark.unit
def test_speaker_label_uses_same_rules() -> None:
    assert escape_speaker_label("Q & A") == "Q &amp; A"
    assert escape_speaker_label("<host>") == "&lt;host&gt;"
