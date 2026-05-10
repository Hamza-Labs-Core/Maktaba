"""Packer respects target_chars target and cap_chars hard cap."""

from __future__ import annotations

import pytest

from maktaba_pipeline.search.models import Sentence
from maktaba_pipeline.search.packer import Packer


def _sent(text: str, sid: int = 1, start: float = 0.0, end: float = 1.0) -> Sentence:
    return Sentence(text=text, start_sec=start, end_sec=end, segment_ids=(sid,))


@pytest.mark.unit
def test_pack_empty_returns_empty() -> None:
    assert Packer().pack([]) == []


@pytest.mark.unit
def test_pack_reaches_target_chars() -> None:
    # Each sentence ~30 chars; we expect them to combine to reach the
    # ~200-char target before emitting.
    sentences = [_sent("Lorem ipsum dolor sit amet ok." + str(i), sid=i + 1) for i in range(20)]
    units = Packer().pack(sentences)
    assert units
    # At least one unit should be in the [target, cap] band.
    in_band = [u for u in units if 100 <= len(u.text) <= 400]
    assert in_band


@pytest.mark.unit
def test_pack_seq_is_one_based_and_contiguous() -> None:
    sentences = [_sent("x" * 220, sid=1)]  # forces immediate emit
    units = Packer().pack(sentences)
    assert [u.seq for u in units] == list(range(1, len(units) + 1))


@pytest.mark.unit
def test_pack_word_split_when_sentence_over_cap() -> None:
    # A single sentence well past the 400-char cap.
    long_text = " ".join(["word"] * 200)  # ~999 chars (200 * "word " - 1)
    units = Packer(target_chars=200, cap_chars=400).pack([_sent(long_text)])
    assert len(units) >= 3
    for u in units:
        assert len(u.text) <= 400
        assert u.metadata.get("split_method") == "word"


@pytest.mark.unit
def test_pack_segment_ids_collected_in_order() -> None:
    sentences = [
        _sent("first sentence here." + " x" * 50, sid=1),
        _sent("second sentence here." + " y" * 50, sid=2),
        _sent("third one." + " z" * 50, sid=3),
    ]
    units = Packer(target_chars=50, cap_chars=400).pack(sentences)
    seen = [s for u in units for s in u.segment_ids]
    # Order preserved across all units.
    assert seen == sorted(seen)


@pytest.mark.unit
def test_pack_no_punctuation_marker_set_when_all_word_split() -> None:
    long_text = "x" * 1500  # one giant word > cap, gets hard-split
    units = Packer(target_chars=200, cap_chars=400).pack([_sent(long_text)])
    # The packer hard-splits at cap; every emitted unit was word-split.
    assert units[-1].metadata.get("no_punctuation") is True
