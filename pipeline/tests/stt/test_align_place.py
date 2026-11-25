"""Each word of a window on its own sentence (stt/align.py ``place``, issue #1756)."""

from __future__ import annotations

from typing import Any

import pytest

from maktaba_pipeline.stt.align import AlignItem, WordTiming, merge_punctuations, place

pytestmark = pytest.mark.unit


def _placed(words: list[tuple[str, list[int]]], sentences: list[list[int]]) -> list[list[str]]:
    timings = [WordTiming(w, list(t), i * 1.0, i + 1.0, 0.9) for i, (w, t) in enumerate(words)]
    segments: list[dict[str, Any]] = [{"seek": 0, "start": 0.0, "end": 9.0} for _ in sentences]
    place(AlignItem(segments, 3000, 0.0, 0), sentences, timings, merge_punctuations)
    return [[w["word"] for w in s["words"]] for s in segments]


def test_a_sentence_of_punctuation_moves_no_other_sentence_s_words() -> None:
    words = [(" a", [1]), (" b", [2]), (".", [3]), (" c", [4]), (" d", [5])]
    # before: [" a", " b."], [" c"], [" d"], and the last sentence of such a window got none
    assert _placed(words, [[1, 2], [3], [4, 5]]) == [[" a", " b"], ["."], [" c", " d"]]
    assert _placed(words[:3], [[1, 2], [3]]) == [[" a", " b"], ["."]]


def test_punctuation_joins_the_words_of_its_sentence() -> None:
    words = [(' "', [1]), ("a", [2]), (".", [3]), (' "', [4]), (" b", [5]), ("!", [6])]
    assert _placed(words, [[1, 2, 3], [4], [5, 6]]) == [[' "a.'], [' "'], [" b!"]]


def test_a_word_across_two_sentences_is_the_first_one_s() -> None:
    words = [(" a", [1]), (" bc", [2, 3]), (" d", [4])]
    assert _placed(words, [[1, 2], [3, 4]]) == [[" a", " bc"], [" d"]]
