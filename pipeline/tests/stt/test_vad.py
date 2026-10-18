"""Speech-only decoding: Silero's stretches of speech, packed, and times mapped back."""

from __future__ import annotations

import numpy as np
import pytest

from maktaba_pipeline.stt.vad import (
    SAMPLE_RATE,
    WINDOW,
    TimeMap,
    VadSettings,
    chunks_from_probs,
    pack,
    vad_available,
)

STEP = WINDOW / SAMPLE_RATE  # 32 ms


def _probs(*spans: tuple[float, float, float]) -> list[float]:
    """(from s, to s, probability) spans, as 32 ms steps."""
    out: list[float] = []
    for a, b, p in spans:
        out += [p] * round((b - a) / STEP)
    return out


def test_a_long_pause_splits_and_a_short_one_does_not() -> None:
    probs = _probs((0, 3, 0.9), (3, 4, 0.05), (4, 6, 0.9), (6, 10, 0.05), (10, 12, 0.9))
    n = len(probs) * WINDOW
    chunks = chunks_from_probs(probs, n, VadSettings())
    got = [(a / SAMPLE_RATE, b / SAMPLE_RATE) for a, b in chunks]
    # 1 s pause: kept inside the speech; 4 s pause: cut, with 0.4 s kept each side
    assert len(got) == 2
    assert got[0][0] == 0 and got[0][1] == pytest.approx(6.4, abs=0.04)
    assert got[1][0] == pytest.approx(9.6, abs=0.04) and got[1][1] == pytest.approx(n / SAMPLE_RATE)


def test_no_speech_no_chunks() -> None:
    probs = _probs((0, 30, 0.02))
    assert chunks_from_probs(probs, len(probs) * WINDOW, VadSettings()) == []


def test_packed_times_map_back_to_the_file() -> None:
    sr = SAMPLE_RATE
    audio = np.arange(20 * sr, dtype=np.float32)
    packed, tmap = pack(audio, [(2 * sr, 5 * sr), (10 * sr, 12 * sr)])
    assert len(packed) == 5 * sr and packed[0] == 2 * sr
    assert tmap.packed_sec == 5
    assert tmap(0.0) == 2.0 and tmap(2.5) == 4.5  # inside the first stretch
    assert tmap(3.0) == 10.0 and tmap(4.25) == 11.25  # the second starts at 10 s
    assert TimeMap([])(7.0) == 7.0
    # an end exactly at the cut belongs to the stretch before the removed pause
    assert tmap(3.0, end=True) == 5.0 and tmap(3.0) == 10.0


@pytest.mark.skipif(not vad_available(), reason="silero-vad (the stt extra) is not installed")
def test_silero_hears_no_speech_in_a_tone() -> None:
    from maktaba_pipeline.stt.vad import speech_chunks

    t = np.arange(10 * SAMPLE_RATE) / SAMPLE_RATE
    tone = (0.3 * np.sin(2 * np.pi * 440 * t)).astype(np.float32)
    assert speech_chunks(tone) == []
