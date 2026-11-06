"""A sentence's words, packed (Epic 52, story 52.1): ``stt/word_data.py``, against the golden
fixtures the Go twin (``api/internal/transcript/worddata``) reads too."""

from __future__ import annotations

import base64
import json
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.stt import word_data as wd
from maktaba_pipeline.stt.batched import to_sentences
from maktaba_pipeline.stt.settings import HEARD_DIFFERENTLY, INSERTED, NO_PROB, QURAN, Word, _plain

FIXTURES = Path(__file__).resolve().parents[3] / "shared" / "testdata" / "word_data"


def _cases() -> list[Path]:
    return sorted(FIXTURES.glob("*.json"))


def test_there_are_golden_fixtures_for_every_shape() -> None:
    names = {p.stem for p in _cases()}
    assert {"arabic_harakat", "mixed_ar_en", "emoji", "inserted", "quran_run", "empty"} <= names


@pytest.mark.parametrize("path", _cases(), ids=lambda p: p.stem)
def test_golden_fixture_packs_byte_for_byte(path: Path) -> None:
    case = json.loads(path.read_text(encoding="utf-8"))
    words = [Word(w["start"], w["end"], w["text"], w["prob"], w["flags"]) for w in case["words"]]
    packed = wd.pack(case["text"], words, case["start"])
    assert packed.misses == 0
    assert packed.data == path.with_suffix(".bin").read_bytes()
    back = wd.unpack(case["text"], packed.data, case["start"])
    assert [w.text for w in back] == [w.text.strip() for w in words]
    for got, want in zip(back, words, strict=True):
        if want.prob is None:
            assert got.prob is None
        else:
            assert got.prob is not None and abs(got.prob - want.prob) <= 1 / 255
        assert abs(got.start - want.start) <= 0.01 and abs(got.end - want.end) <= 0.011


def test_offsets_are_code_points_past_an_emoji() -> None:
    text = "hello 😀 world"
    packed = wd.pack(text, [Word(0, 1, "hello"), Word(1, 2, "😀"), Word(2, 3, "world")], 0.0)
    assert packed.data is not None
    slots = wd.decode(packed.data)
    assert [(s.char_start, s.char_len) for s in slots] == [(0, 5), (6, 1), (8, 5)]


def test_a_word_not_in_the_text_is_skipped_and_counted() -> None:
    packed = wd.pack("one two", [Word(0, 1, "one", 0.9), Word(1, 2, "zzz", 0.5),
                                 Word(2, 3, "two", 0.8)], 0.0)  # fmt: skip
    assert packed.misses == 1 and packed.data is not None
    assert [w.text for w in wd.unpack("one two", packed.data, 0.0)] == ["one", "two"]


def test_flags_and_a_missing_probability() -> None:
    words = [Word(0, 1, "a", None, INSERTED), Word(1, 2, "b", 0.5, HEARD_DIFFERENTLY | QURAN)]
    packed = wd.pack("a b", words, 0.0)
    assert packed.data is not None
    slots = wd.decode(packed.data)
    assert slots[0].prob == 255 and slots[0].flags == INSERTED | NO_PROB
    assert slots[1].prob == 128 and slots[1].flags == HEARD_DIFFERENTLY | QURAN
    back = wd.unpack("a b", packed.data, 0.0)
    assert back[0].prob is None and back[0].flags == INSERTED  # no_prob is the format's own


def test_limits_store_nothing_rather_than_wrap() -> None:
    assert wd.pack("x" * 70_000, [Word(0, 1, "x")], 0.0).data is None
    assert wd.pack("late", [Word(700.0, 701.0, "late")], 0.0).data is None  # past 655 s
    assert len(wd.pack("ab", [Word(0, 1, "ab")], 0.0).data or b"") == 3 + 9


def test_decode_refuses_what_it_does_not_know() -> None:
    for bad in (b"", b"\x01", b"\x02\x00\x00", b"\x01\x01\x00"):
        with pytest.raises(ValueError):
            wd.decode(bad)


@pytest.mark.parametrize("path", sorted((FIXTURES / "edits").glob("*.json")), ids=lambda p: p.stem)
def test_edit_golden_cases(path: Path) -> None:
    """The same cases Go's ``worddata.Edit`` runs: byte-identical results."""
    c: dict[str, Any] = json.loads(path.read_text(encoding="utf-8"))
    text, data = wd.edit(
        c["text"], base64.b64decode(c["word_data"]), c["char_start"], c["char_len"], c["after"]
    )
    assert text == c["want_text"]
    assert data == base64.b64decode(c["want_word_data"])


def test_shift_after_moves_only_later_words() -> None:
    data = wd.encode([wd.Slot(0, 3, 0, 1, 1, 0), wd.Slot(4, 2, 1, 1, 1, 0)])
    slots = wd.decode(wd.shift_after(data, 4, 3))
    assert [s.char_start for s in slots] == [0, 7]


def test_the_checkpoint_keeps_each_word_s_probability() -> None:
    """A resumed job gives the same evidence as an uninterrupted one (criterion 6)."""
    seg = {"start": 0.0, "end": 1.0, "text": " a b", "avg_logprob": -0.2, "no_speech_prob": 0.1,
           "words": [{"start": 0.0, "end": 0.5, "word": " a", "probability": 0.25},
                     {"start": 0.5, "end": 1.0, "word": " b", "probability": 0.75}]}  # fmt: skip
    resumed = to_sentences([json.loads(json.dumps(_plain(seg)))])
    direct = to_sentences([seg])
    assert resumed == direct
    assert direct[0].words is not None and [w.prob for w in direct[0].words] == [0.25, 0.75]
    a = wd.pack(direct[0].text, direct[0].words, direct[0].start).data
    b = wd.pack(resumed[0].text, resumed[0].words or [], resumed[0].start).data
    assert a == b
