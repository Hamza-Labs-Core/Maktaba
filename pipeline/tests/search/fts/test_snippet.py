"""Snippet builder wraps matches in <mark> tags."""

from __future__ import annotations

import pytest

from maktaba_pipeline.search.fts.snippet import build_snippet


@pytest.mark.unit
def test_basic_match_wrapped() -> None:
    out = build_snippet("the quick brown fox", ["quick"])
    assert "<mark>quick</mark>" in out


@pytest.mark.unit
def test_multiple_matches_all_wrapped() -> None:
    out = build_snippet("foo bar foo baz", ["foo"])
    assert out.count("<mark>") == 2
    assert out.count("</mark>") == 2


@pytest.mark.unit
def test_case_insensitive_match() -> None:
    out = build_snippet("Hello WORLD", ["world"])
    # Snippet preserves the original casing within the marks.
    assert "<mark>WORLD</mark>" in out


@pytest.mark.unit
def test_arabic_diacritics_match() -> None:
    # Query without diacritics still matches indexed text with them.
    out = build_snippet("الحَمد لله", ["الحمد"])
    assert "<mark>" in out
    assert "</mark>" in out


@pytest.mark.unit
def test_no_match_returns_head_no_marks() -> None:
    out = build_snippet("hello world", ["nope"])
    assert "<mark>" not in out
    assert out.startswith("hello world")


@pytest.mark.unit
def test_empty_terms_returns_head() -> None:
    out = build_snippet("hello world", [])
    assert "<mark>" not in out


@pytest.mark.unit
def test_window_truncation() -> None:
    long_text = "lorem ipsum " * 50 + "needle " + "dolor sit amet " * 50
    out = build_snippet(long_text, ["needle"], max_chars=60)
    # The needle is in the snippet.
    assert "<mark>needle</mark>" in out
    # The original text is far longer than 60 chars; we got a window.
    assert len(out) < len(long_text)


@pytest.mark.unit
def test_custom_mark_tags() -> None:
    out = build_snippet("the quick fox", ["quick"], mark_open="**", mark_close="**")
    assert "**quick**" in out
