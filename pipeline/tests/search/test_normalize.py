"""Cover the generic text normalize helpers."""

from __future__ import annotations

import pytest

from maktaba_pipeline.search.normalize import collapse_whitespace, nfc


@pytest.mark.unit
def test_nfc_composes_combining_marks() -> None:
    # e + combining acute → é (single codepoint)
    decomposed = "é"
    composed = nfc(decomposed)
    assert composed == "é"
    assert len(composed) == 1


@pytest.mark.unit
def test_nfc_idempotent() -> None:
    assert nfc(nfc("café")) == nfc("café")


@pytest.mark.unit
def test_collapse_whitespace_runs_become_single_space() -> None:
    assert collapse_whitespace("  hello   world\t\nfoo  ") == "hello world foo"


@pytest.mark.unit
def test_collapse_whitespace_unicode_nbsp() -> None:
    assert collapse_whitespace("hello  world") == "hello world"


@pytest.mark.unit
def test_collapse_whitespace_empty_string() -> None:
    assert collapse_whitespace("") == ""
    assert collapse_whitespace("   \t\n  ") == ""
