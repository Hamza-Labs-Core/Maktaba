"""Verify the Python arabic_normalize matches the SQL one (slot 0019)."""

from __future__ import annotations

import pytest

from maktaba_pipeline.search.fts.normalize import arabic_normalize


@pytest.mark.unit
def test_lowercases_latin() -> None:
    assert arabic_normalize("Hello WORLD") == "hello world"


@pytest.mark.unit
def test_strips_tashkeel() -> None:
    # الحَمد → الحمد (fatha U+064E stripped).
    assert arabic_normalize("الحَمد") == "الحمد"


@pytest.mark.unit
def test_strips_multiple_diacritics() -> None:
    # بِسْمِ → بسم (kasra, sukun, kasra all stripped).
    assert arabic_normalize("بِسْمِ") == "بسم"


@pytest.mark.unit
def test_alef_variants_unified() -> None:
    # All variants collapse to plain alef ا.
    assert arabic_normalize("إنا") == "انا"
    assert arabic_normalize("أحمد") == "احمد"
    assert arabic_normalize("آية") == "ايه"  # آ → ا, ة → ه
    assert arabic_normalize("ٱلله") == "الله"


@pytest.mark.unit
def test_ya_variant_to_plain_ya() -> None:
    # ى (U+0649) → ي (U+064A)
    assert arabic_normalize("على") == "علي"


@pytest.mark.unit
def test_taa_marbuta_to_ha() -> None:
    assert arabic_normalize("مدينة") == "مدينه"


@pytest.mark.unit
def test_collapses_whitespace_at_end() -> None:
    assert arabic_normalize("a   b\t\nc") == "a b c"


@pytest.mark.unit
def test_idempotent() -> None:
    s = "الحَمدُ لله ربِّ العالمين"
    once = arabic_normalize(s)
    twice = arabic_normalize(once)
    assert once == twice


@pytest.mark.unit
def test_tatweel_stripped() -> None:
    # ـ U+0640 is the tatweel; the SQL regex strips it.
    assert arabic_normalize("الـحـمد") == "الحمد"
