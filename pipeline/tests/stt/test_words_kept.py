"""No word lost, doubled or moved (issue #1756): a window's words placed on its sentences,
stored, read back and corrected by the second listen, with words placed as they are now and
as they were up to the fix (``_old_place``)."""

from __future__ import annotations

import random
import re
from typing import Any

import pytest
from maktaba_fp_transcription.stages.transcribe import segment_rows

from maktaba_pipeline.stt import refine as rf
from maktaba_pipeline.stt.align import (
    APPEND,
    PREPEND,
    AlignItem,
    WordTiming,
    merge_punctuations,
    place,
)
from maktaba_pipeline.stt.batched import to_sentences
from maktaba_pipeline.stt.settings import SAMPLE_RATE, Sentence, Transcript
from maktaba_pipeline.stt.word_data import covers, stored_words

pytestmark = pytest.mark.unit

#: one sentence of a window and its tokens (here one a letter)
Window = list[str]


def _tokens(text: str) -> list[str]:
    """The tokenizer's words: cut at spaces and at ASCII punctuation; anything else that
    begins without a space («؟», «…», a sentence) joins the word before."""
    return re.findall(r"""\s?[!-/:-@\[-`{-~]|\s?[^\s!-/:-@\[-`{-~]+""", text)


def _timings(window: Window, offset: float) -> list[WordTiming]:
    out, t = [], offset
    for w in _tokens("".join(window)):
        out.append(WordTiming(w, [0] * len(w), round(t, 2), round(t + 0.4, 2), 0.9))
        t += 0.4
    return out


def _old_place(segments: list[dict[str, Any]], per: list[list[int]], words: list[Any]) -> None:
    """``place`` up to the fix: each sentence's tokens counted from its first word."""
    merge_punctuations(words, PREPEND, APPEND)
    wi = 0
    for seg, tokens in zip(segments, per, strict=True):
        saved = 0
        got: list[dict[str, Any]] = []
        while wi < len(words) and saved < len(tokens):
            t = words[wi]
            if t.word:
                got.append(
                    {"word": t.word, "start": t.start, "end": t.end, "probability": t.probability}
                )
            saved += len(t.tokens)
            wi += 1
        if got:
            seg["start"], seg["end"] = got[0]["start"], got[-1]["end"]
        seg["words"] = got


def _stored(window: Window, old: bool) -> list[Sentence]:
    """The window's sentences as the second listen reads them."""
    per = [[0] * len(s) for s in window]
    segments: list[dict[str, Any]] = []
    t = 0.0
    for text in window:
        length = 0.4 * max(1, len(_tokens(text)))
        segments.append(
            {"seek": 0, "start": t, "end": t + length, "text": text,
             "avg_logprob": -0.1, "no_speech_prob": 0.0}
        )  # fmt: skip
        t += length
    words = _timings(window, 0.0)
    if old:
        _old_place(segments, per, words)
    else:
        place(AlignItem(segments, 3000, 0.0, 0), per, words, merge_punctuations)
    rows, _any = segment_rows(to_sentences(segments))
    out = []
    for _seq, start, end, text, _conf, _meta, data in rows:
        row = {"start_sec": start, "text": text, "word_data": data}
        out.append(Sentence(start, end, text, -0.1, 0.0, stored_words(row, {})))
    return out


def _words_of(text: str) -> list[str]:
    """The words said, each as Whisper heard it (the second listen writes w7 as v7)."""
    return re.findall(r"w\d+", text.replace("v", "w"))


def _listen(window: Window, sentences: list[Sentence]) -> list[Sentence]:
    """The second listen, which hears in a stretch exactly the words said in it, and every
    third one otherwise."""
    said = [
        ((t.start + t.end) / 2, w)
        for t in _timings(window, 0.0)
        for w in re.findall(r"w\d+", t.word)
    ]

    def hear(stretches: list[Any]) -> list[str]:
        out = []
        for audio in stretches:
            a, b = audio[0] / SAMPLE_RATE, (audio[-1] + 1) / SAMPLE_RATE
            words = [w for mid, w in said if a <= mid < b]
            out.append(" ".join(w.replace("w", "v") if int(w[1:]) % 3 == 0 else w for w in words))
        return out

    end = int((said[-1][0] + 2) * SAMPLE_RATE) if said else SAMPLE_RATE
    result = Transcript(sentences, end / SAMPLE_RATE, "ar", "turbo", 1.0)
    rf.refine(result, range(end), hear, None, "cohere")
    return result.sentences


CASES: dict[str, Window] = {
    "a stray full stop": [" w1 w2 w4", ".", " w5 w6 w7", " w8 w9 w10", " w11 w12 w13"],
    "a wordless last sentence": [" w1 w2 w4 w5", ".", " w7"],
    "an Arabic question mark alone": [" w1 w2 w3", "؟", " w4 w5 w6", " w7 w8"],
    "an ellipsis alone": [" w1 w2 w3", "…", " w4 w5 w6", " w7 w8"],
    "three dots alone": [" w1 w2 w3", "...", " w4 w5 w6", " w7 w8"],
    "a sentence without its space": [" w1 w2", "w3 w4 w5", " w6 w7"],
    "an opening quote at the end": [' w1 w2 "', " w3 w4 w5", " w6 w7 w8"],
}


@pytest.mark.parametrize("old", [False, True])
@pytest.mark.parametrize("case", CASES)
def test_no_word_is_lost_doubled_or_moved(case: str, old: bool) -> None:
    window = CASES[case]
    out = _listen(window, _stored(window, old))
    assert [_words_of(s.text) for s in out] == [_words_of(s) for s in window]


@pytest.mark.parametrize("case", CASES)
def test_every_sentence_that_says_something_has_its_words_stored(case: str) -> None:
    for s in _stored(CASES[case], old=False):
        assert covers(s.text, s.words) or not _words_of(s.text), s
        assert s.words  # punctuation alone keeps its own word too


def test_the_second_listen_still_corrects_around_a_damaged_sentence() -> None:
    window = [f" w{i} w{i + 1} w{i + 2}" for i in range(1, 25, 3)]
    sentences = _stored(window, old=False)
    sentences[3].words = None
    text = [s.text for s in _listen(window, sentences)]
    assert text[3] == window[3].strip() and "v" not in text[3]
    assert all("v" in t for i, t in enumerate(text) if i != 3)


@pytest.mark.parametrize("seed", range(8))  # a few windows each: the unit tier's time cap
@pytest.mark.parametrize("old", [False, True])
def test_no_word_is_lost_doubled_or_moved_in_random_windows(old: bool, seed: int) -> None:
    rng = random.Random(seed)
    damaged = 0
    for _ in range(40):
        n = 0
        window: Window = []
        for _s in range(rng.randint(2, 9)):
            kind = rng.random()
            if kind < 0.2 and window:
                window.append(rng.choice([".", "؟", "…", "...", " .", "!", ' "']))
                continue
            words = [f"w{n + i}" for i in range(1, rng.randint(1, 6) + 1)]
            n += len(words)
            lead = "" if kind > 0.9 and window and window[-1][-1].isalnum() else " "
            end = rng.choice(["", "", "", ".", "،", "؟", ' "'])
            window.append(lead + " ".join(words) + end)
        if not n:
            continue
        sentences = _stored(window, old)
        damaged += sum(not covers(s.text, s.words) and bool(_words_of(s.text)) for s in sentences)
        out = _listen(window, sentences)
        want = [_words_of(s) for s in window]
        # a sentence that says nothing can go with its words' time
        got = [g for s in out if (g := _words_of(s.text))]
        assert got == [w for w in want if w], window
    assert bool(damaged) is old


def test_adjacent_stretches_do_not_double_the_next_sentence_s_first_short_word() -> None:
    """Issue #1765 (item 3): across multiple back-to-back stretches (> CHUNK_SEC) with short
    words (0.15 s), no word at a stretch boundary is heard twice."""
    # ~400 words * 0.15 s = 60 s -> at least two back-to-back stretches
    window = [f" w{i} w{i + 1} w{i + 2} w{i + 3}" for i in range(1, 401, 4)]
    step = 0.15
    sentences: list[Sentence] = []
    said: list[tuple[float, str]] = []
    t = 0.0
    for text in window:
        ws: list[Any] = []
        for tok in text.split():
            from maktaba_pipeline.stt.settings import Word

            ws.append(Word(round(t, 3), round(t + step, 3), tok, 0.9))
            said.append((t + step / 2, tok))
            t += step
        sentences.append(Sentence(ws[0].start, ws[-1].end, text.strip(), -0.1, 0.0, ws))

    assert len(rf.chunks(sentences)) >= 2

    def hear(stretches: list[Any]) -> list[str]:
        out = []
        for audio in stretches:
            a, b = audio[0] / SAMPLE_RATE, (audio[-1] + 1) / SAMPLE_RATE
            words = [w for mid, w in said if a <= mid < b]
            out.append(" ".join(w.replace("w", "v") if int(w[1:]) % 3 == 0 else w for w in words))
        return out

    end = int((t + 2) * SAMPLE_RATE)
    result = Transcript(sentences, end / SAMPLE_RATE, "ar", "turbo", 1.0)
    rf.refine(result, range(end), hear, None, "cohere")
    assert [_words_of(s.text) for s in result.sentences] == [_words_of(s) for s in window]
