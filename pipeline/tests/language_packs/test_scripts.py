"""Script runs and the shared splitter (Epic 52, story 52.3)."""

from __future__ import annotations

from maktaba_pipeline.language_packs.scripts import runs, script_of
from maktaba_pipeline.language_packs.segment import split


def test_runs_of_arabic_with_english_inside() -> None:
    text = "قال النبي ﷺ in the hadith، والله أعلم"
    got = [(r.script, text[r.start : r.end]) for r in runs(text)]
    # the spaces before a script's first letter go with its run; punctuation stays behind
    assert got == [("Arab", "قال النبي ﷺ"), ("Latn", " in the hadith،"), ("Arab", " والله أعلم")]
    # the runs cover the text, in code points
    assert "".join(text[r.start : r.end] for r in runs(text)) == text


def test_harakat_and_digits_join_the_run_around_them() -> None:
    text = "بِسْمِ ٣ 10"
    assert [(r.script, r.start, r.end) for r in runs(text)] == [("Arab", 0, len(text))]
    assert script_of("َ") == "Zinh" and script_of("٣") == "Zyyy"


def test_split_keeps_offsets_and_kinds() -> None:
    text = "don't stop 10:30, well-known 3rd!"
    toks = split(text, "'-")
    assert [(t.text, t.kind) for t in toks] == [
        ("don't", "word"), ("stop", "word"), ("10:30", "number"), (",", "punct"),
        ("well-known", "word"), ("3rd", "number"), ("!", "punct"),
    ]  # fmt: skip
    assert all(text[t.start : t.end] == t.text for t in toks)


def test_offsets_are_code_points_after_an_emoji() -> None:
    text = "😀 مِنَ word"
    toks = split(text)
    assert [(t.start, t.end) for t in toks] == [(0, 1), (2, 6), (7, 11)]
    assert toks[1].text == "مِنَ" and toks[1].kind == "word"


def test_a_word_never_crosses_scripts() -> None:
    assert [t.text for t in split("これはペンです")] == ["これは", "ペン", "です"]
