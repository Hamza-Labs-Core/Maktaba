"""The batched transcriber's seek logic and batching, with a fake decoder (no GPU)."""

from __future__ import annotations

import asyncio
from collections.abc import Sequence
from dataclasses import dataclass, field
from typing import Any

import numpy as np
import pytest

from maktaba_pipeline.engine.stages.subtitle_gen import srt, vtt
from maktaba_pipeline.stt.batched import (
    N_FRAMES,
    BatchedTranscriber,
    WhisperSettings,
    _Job,
    apply_window,
    to_sentences,
)

TS = 1000  # timestamp_begin of the fake tokenizer; <|t|> = TS + t / 0.02
EOT = 999


class Tok:
    timestamp_begin = TS
    eot = EOT

    def decode(self, tokens: list[int]) -> str:
        return " ".join(f"w{t}" for t in tokens)


def ts(sec: float) -> int:
    return TS + round(sec / 0.02)


@dataclass
class R:
    tokens: list[int]
    avg_logprob: float = -0.2
    compression_ratio: float = 1.2
    no_speech_prob: float = 0.0
    temperature: float = 0.0


def _job(seconds: float) -> _Job:
    # the seek logic never touches the future or the loop (no event loop: no sockets)
    frames = int(seconds * 100)
    return _Job(
        mel=None, content_frames=frames, audio_sec=seconds,
        future=None, loop=None, admitted=0,  # type: ignore[arg-type]
    )  # fmt: skip


S = WhisperSettings(word_timestamps=False)


@pytest.mark.unit
def test_complete_sentences_then_silence_moves_past_the_window() -> None:
    j = _job(60)
    apply_window(j, R([ts(0), 1, 2, ts(3), ts(3), 3, ts(7)]), N_FRAMES, Tok(), S)
    # ends with a lone timestamp after text: no speech after it, the next window is next
    assert j.seek == N_FRAMES
    assert [(s["start"], s["end"]) for s in j.segments] == [(0.0, 3.0), (3.0, 7.0)]


@pytest.mark.unit
def test_an_unfinished_sentence_is_decoded_again_from_its_start() -> None:
    j = _job(60)
    # two finished sentences, then text with no closing timestamp
    apply_window(j, R([ts(0), 1, ts(4), ts(4), 2, ts(9), ts(9), 3]), N_FRAMES, Tok(), S)
    assert j.seek == 900  # 9 s: the last complete timestamp
    assert len(j.segments) == 2


@pytest.mark.unit
def test_silence_skips_the_window_without_text() -> None:
    j = _job(60)
    apply_window(j, R([ts(0), 5, ts(2)], no_speech_prob=0.9, avg_logprob=-1.5), N_FRAMES, Tok(), S)
    assert j.seek == N_FRAMES and j.segments == []


@pytest.mark.unit
def test_a_window_with_one_timestamp_ends_where_it_says() -> None:
    j = _job(60)
    apply_window(j, R([ts(0), 1, 2]), N_FRAMES, Tok(), S)
    assert j.seek == N_FRAMES and len(j.segments) == 1


@pytest.mark.unit
def test_word_bounds_set_the_sentence_and_the_seek() -> None:
    j = _job(60)

    def align(segs: list[dict[str, Any]], mel: Any, frames: int, last: float) -> None:
        for s in segs:
            s["words"] = [{"start": s["start"] + 0.3, "end": s["end"] - 0.2, "word": "x"}]

    apply_window(
        j, R([ts(0), 1, ts(4), ts(4), 2, ts(9), ts(9), 3]), N_FRAMES, Tok(), S,
        mel_segment=object(), align=align,
    )  # fmt: skip
    assert j.seek == 880  # the last word ends at 8.8 s
    sents = to_sentences(j.segments)
    assert [(s.start, s.end) for s in sents] == [(0.3, 3.8), (4.3, 8.8)]


@dataclass
class FakeModel:
    """Decodes a fake mel (a list of per-window token lists) one window per call."""

    calls: list[tuple[int, float]] = field(default_factory=list)
    bad_once: set[int] = field(default_factory=set)

    def decode(self, mels: Sequence[Any], temps: Sequence[float]) -> list[R]:
        self.calls.append((len(mels), max(temps)))
        out = []
        for win, t in zip(mels, temps, strict=True):
            tokens, wid = win
            if wid in self.bad_once and t == 0.0:
                out.append(R(tokens, avg_logprob=-3.0))  # improbable: fall back
            else:
                out.append(R(tokens, temperature=t))
        return out


def _transcriber(model: FakeModel, max_batch: int = 8) -> BatchedTranscriber:
    def mel_of(audio: np.ndarray) -> Any:
        # audio carries its own script: one token list per 30 s window
        n = len(audio) // 480_000
        return _Mel([[ts(0), 100 + int(audio[0]), 101, ts(5)] for _ in range(n)], int(audio[0]))

    def window_of(mel: Any, seek: int, size: int) -> Any:
        return (mel.windows[seek // N_FRAMES], mel.file * 100 + seek // N_FRAMES)

    return BatchedTranscriber(
        WhisperSettings(word_timestamps=False, max_batch=max_batch),
        decode=model.decode,
        mel_of=mel_of,
        window_of=window_of,
        tokenizer=Tok(),
    )


class _Mel:
    def __init__(self, windows: list[list[int]], file: int) -> None:
        self.windows, self.file = windows, file
        self.shape = (len(windows) * N_FRAMES + N_FRAMES, 128)


def _audio(file: int, windows: int) -> np.ndarray:
    a = np.zeros(480_000 * windows, np.float32)
    a[0] = file
    return a


@pytest.mark.asyncio
async def test_many_files_share_each_pass_and_get_their_own_sentences() -> None:
    model = FakeModel()
    bt = _transcriber(model)
    results = await asyncio.gather(*(bt.transcribe(_audio(f, 3)) for f in range(1, 5)))
    for f, r in enumerate(results, 1):
        assert len(r.sentences) == 3  # one per window
        assert all(s.text == f"w{100 + f} w101" for s in r.sentences)
    assert max(n for n, _ in model.calls) == 4  # four files in one pass


@pytest.mark.asyncio
async def test_an_improbable_window_is_decoded_again_warmer() -> None:
    model = FakeModel(bad_once={101})  # file 1, window 1
    bt = _transcriber(model)
    r = await bt.transcribe(_audio(1, 3))
    assert len(r.sentences) == 3
    assert (1, 0.2) in model.calls  # the same window again at the next temperature


@pytest.mark.asyncio
async def test_a_file_that_fails_does_not_stop_the_others() -> None:
    model = FakeModel()
    bt = _transcriber(model)
    orig = bt._mel_of
    assert orig is not None

    def mel_of(audio: np.ndarray) -> Any:
        if audio[0] == 9:
            raise ValueError("broken audio")
        return orig(audio)

    bt._mel_of = mel_of
    ok, bad = await asyncio.gather(
        bt.transcribe(_audio(1, 2)), bt.transcribe(_audio(9, 2)), return_exceptions=True
    )
    assert not isinstance(ok, BaseException) and len(ok.sentences) == 2
    assert isinstance(bad, ValueError)


@pytest.mark.unit
def test_subtitle_formats() -> None:
    segs = [(0.0, 1.5, "السلام عليكم"), (3661.25, 3662.0, " الحمد لله ")]
    assert srt(segs) == (
        "1\n00:00:00,000 --> 00:00:01,500\nالسلام عليكم\n\n"
        "2\n01:01:01,250 --> 01:01:02,000\nالحمد لله\n"
    )
    v = vtt(segs)
    assert v.startswith("WEBVTT\n\nNOTE maktaba")
    assert "01:01:01.250 --> 01:01:02.000\nالحمد لله" in v


@pytest.mark.asyncio
async def test_alignment_reads_each_windows_own_encoding_and_a_fallback_is_not_reencoded() -> None:
    model = FakeModel(bad_once={101})  # file 1, window 1 falls back once
    encoded: list[Any] = []
    aligned: list[tuple[int, int]] = []

    def encode(windows: list[Any], cached: list[Any]) -> list[Any]:
        encoded.extend(w[1] for w, c in zip(windows, cached, strict=True) if c is None)
        return [c if c is not None else w for w, c in zip(windows, cached, strict=True)]

    def align_batch(items: list[Any], feats: list[Any]) -> None:
        for it in items:
            seg = it.segments[0]
            aligned.append((feats[it.row][1], seg["seek"]))
            for s in it.segments:
                s["words"] = [{"start": s["start"], "end": s["end"], "word": "x"}]

    bt = _transcriber(model)
    bt._encode, bt._align_batch = encode, align_batch
    r1, r2 = await asyncio.gather(bt.transcribe(_audio(1, 3)), bt.transcribe(_audio(2, 2)))
    assert len(r1.sentences) == 3 and len(r2.sentences) == 2
    # every window encoded once, the fallback's second decode reused its encoding
    assert sorted(encoded) == [100, 101, 102, 200, 201]
    assert (1, 0.2) in model.calls
    # each window aligned against its own row of the pass's encoder output
    assert sorted(w for w, _ in aligned) == [100, 101, 102, 200, 201]
    assert all(w % 100 == seek // N_FRAMES for w, seek in aligned)
    assert bt.audio_done == pytest.approx(5 * 30) and bt.audio_lost == 0


@pytest.mark.asyncio
async def test_a_failed_batch_counts_its_progress_as_lost() -> None:
    model = FakeModel()
    bt = _transcriber(model)
    orig = model.decode

    def decode(mels: Sequence[Any], temps: Sequence[float]) -> list[R]:
        if len(model.calls) == 1:
            raise RuntimeError("gpu fault")
        return orig(mels, temps)

    bt._decode = decode
    with pytest.raises(RuntimeError):
        await bt.transcribe(_audio(1, 3))
    assert bt.audio_lost == pytest.approx(30) == bt.audio_done  # one window in, then lost


@pytest.mark.asyncio
async def test_each_file_s_language_is_detected_once_from_its_first_window() -> None:
    model = FakeModel()
    asked: list[list[int]] = []

    def detect(features: list[Any], rows: list[int]) -> list[str]:
        asked.append([features[k][1] for k in rows])
        return ["en" if features[k][1] // 100 == 2 else "" for k in rows]

    bt = _transcriber(model)
    bt._encode = lambda windows, cached: [
        c if c is not None else w for w, c in zip(windows, cached, strict=True)
    ]
    bt._detect = detect
    r1, r2 = await asyncio.gather(bt.transcribe(_audio(1, 3)), bt.transcribe(_audio(2, 2)))
    assert asked == [[100, 200]]  # first windows only, in one call
    assert r1.detected_language is None  # unsure: not recorded
    assert r2.detected_language == "en" and r2.language == "ar"
