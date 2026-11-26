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


@pytest.mark.parametrize("dur", [0.0, 0.01])
def test_a_short_word_across_sentences_is_split_so_neither_loses_its_words(dur: float) -> None:
    """Issue #1765 (item 2): a word shorter than 0.02 s crossing a sentence boundary must
    still split proportionally so neither sentence fails covers()."""
    from maktaba_pipeline.stt.settings import Word
    from maktaba_pipeline.stt.word_data import covers

    timings = [
        WordTiming(" a", [1], 0.0, 1.0, 0.9),
        WordTiming(" bc", [2, 3], 1.0, 1.0 + dur, 0.9),
        WordTiming(" d", [4], 1.0 + dur, 2.0, 0.9),
    ]
    segments: list[dict[str, Any]] = [
        {"seek": 0, "start": 0.0, "end": 1.0, "text": " a b"},
        {"seek": 0, "start": 1.0, "end": 2.0, "text": "c d"},
    ]
    place(AlignItem(segments, 3000, 0.0, 0), [[1, 2], [3, 4]], timings, merge_punctuations)
    assert [[w["word"] for w in s["words"]] for s in segments] == [[" a", " b"], ["c", " d"]]
    for seg in segments:
        ws = [Word(w["start"], w["end"], w["word"], w["probability"]) for w in seg["words"]]
        assert covers(seg["text"], ws)
        assert all(w.start <= w.end for w in ws)
    assert segments[0]["words"][-1]["end"] <= segments[1]["words"][0]["start"]


def test_a_zero_duration_word_across_three_sentences_splits_into_all_three() -> None:
    timings = [WordTiming(" a.b", [1, 2, 3], 1.0, 1.0, 0.9)]
    segments: list[dict[str, Any]] = [
        {"seek": 0, "start": 1.0, "end": 1.0, "text": " a"},
        {"seek": 0, "start": 1.0, "end": 1.0, "text": "."},
        {"seek": 0, "start": 1.0, "end": 1.0, "text": "b"},
    ]
    place(AlignItem(segments, 3000, 0.0, 0), [[1], [2], [3]], timings, merge_punctuations)
    assert [[w["word"] for w in s["words"]] for s in segments] == [[" a"], ["."], ["b"]]
