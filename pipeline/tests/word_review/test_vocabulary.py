"""Vocabulary (Epic 52, story 52.10): what fixes teach, and the derived prompt."""

from __future__ import annotations

from maktaba_pipeline.word_review.vocabulary import (
    PROMPT_TOKENS,
    Vocabulary,
    damerau1,
    derived_prompt,
)


def test_one_edit_away() -> None:
    assert damerau1("تيمية", "تيميه") and damerau1("abcd", "abdc") and damerau1("abc", "abcd")
    assert (
        not damerau1("abcd", "abcd") and not damerau1("abcd", "badc") and not damerau1("ab", "abcd")
    )


def test_near_needs_four_letters_and_one_edit() -> None:
    v = Vocabulary(terms={"ar": {"عثيمين": "عثيمين", "ابن": "ابن"}})
    assert v.near("ar", "عتيمين") == "عثيمين"
    assert v.near("ar", "عتيمنن") is None  # two edits
    assert v.near("ar", "ابت") is None  # too short to mean anything
    assert v.knows("ar", "عثيمين") and not v.knows("en", "عثيمين")


def test_the_owner_s_prompt_comes_first_and_is_never_cut() -> None:
    owner = "محاضرات " * 200
    got = derived_prompt(owner, ["ابن تيمية", "ابن القيم"])
    assert got is not None and got.startswith(owner.strip()) and "ابن تيمية" not in got


def test_terms_by_use_within_the_limit() -> None:
    terms = [f"اسم{i}" for i in range(400)]
    got = derived_prompt("دروس السيرة", terms)
    assert got is not None and got.startswith("دروس السيرة, اسم0, اسم1")
    assert (len(got) + 1) // 2 <= PROMPT_TOKENS
    exact = derived_prompt(None, terms, count_tokens=lambda s: len(s.split(",")))
    assert exact is not None and len(exact.split(",")) == PROMPT_TOKENS
    assert derived_prompt(None, []) is None
