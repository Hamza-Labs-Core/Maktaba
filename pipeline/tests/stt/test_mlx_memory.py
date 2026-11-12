"""A transcription process gives its memory back (stt/mlx_memory.py): an idle Whisper and an
idle second listen let their models go and load them again for the next file, MLX's limits
are set by whichever model loads first, and a finished file's spectrogram is not kept."""

from __future__ import annotations

import asyncio
import gc
import importlib.util
import time
import weakref
from typing import Any

import numpy as np
import pytest

from maktaba_pipeline.stt import cohere, engine
from maktaba_pipeline.stt.batched import N_FRAMES, BatchedTranscriber
from maktaba_pipeline.stt.settings import WhisperSettings

from .test_batched import FakeModel, Tok, _audio, _Mel, ts

MODEL = "CohereLabs/cohere-transcribe-arabic-07-2026"


class _Loading(BatchedTranscriber):
    """Loads its fake model the way MLX-Whisper's is loaded (in ``_load``, on the GPU
    thread), so it may be let go when idle; remembers each file's spectrogram weakly."""

    def __init__(self, settings: WhisperSettings) -> None:
        super().__init__(settings)
        self.model = FakeModel()
        self.loads = 0
        self.mels: list[weakref.ref[Any]] = []

    def _load(self) -> None:
        if self._decode is not None:
            return
        self.loads += 1

        def mel_of(audio: np.ndarray) -> Any:
            n = len(audio) // 480_000
            mel = _Mel([[ts(0), 100 + int(audio[0]), 101, ts(5)] for _ in range(n)], int(audio[0]))
            self.mels.append(weakref.ref(mel))
            return mel

        def window_of(mel: Any, seek: int, size: int) -> Any:
            return (mel.windows[seek // N_FRAMES], mel.file * 100 + seek // N_FRAMES)

        self._decode = self.model.decode
        self._mel_of = mel_of
        self._window_of = window_of
        self._tokenizer = Tok()
        self._mlx_loaded = True


def _settings(idle: float) -> WhisperSettings:
    return WhisperSettings(word_timestamps=False, max_batch=4, unload_idle_s=idle)


async def _until(cond: Any, timeout: float = 10.0) -> None:
    end = time.monotonic() + timeout
    while not cond():
        assert time.monotonic() < end, "timed out"
        await asyncio.sleep(0.05)


@pytest.mark.asyncio
async def test_an_idle_whisper_lets_its_model_go_and_loads_it_for_the_next_file() -> None:
    bt = _Loading(_settings(idle=0.2))
    first = await bt.transcribe(_audio(1, 2))
    assert len(first.sentences) == 2
    # no file for the idle time: the model is let go and the GPU thread ends
    await _until(lambda: bt.unloads == 1 and bt._thread is None)
    assert bt._decode is None and bt._mel_of is None and not bt._mlx_loaded
    # the next file loads it again and is transcribed as the first was
    again = await bt.transcribe(_audio(1, 2))
    assert bt.loads == 2
    assert [s.text for s in again.sentences] == [s.text for s in first.sentences]


@pytest.mark.asyncio
async def test_whisper_keeps_its_model_when_unloading_is_off() -> None:
    bt = _Loading(_settings(idle=0))
    await bt.transcribe(_audio(1, 1))
    await asyncio.sleep(1.5)  # past the GPU thread's 1 s wait for work
    assert bt.unloads == 0 and bt._decode is not None
    assert bt._thread is not None and bt._thread.is_alive()


@pytest.mark.asyncio
async def test_a_finished_file_s_spectrogram_is_not_kept_while_the_thread_waits() -> None:
    # the loop's variable kept the last finished file (its whole spectrogram, on the GPU
    # with MLX) referenced until the next file finished
    bt = _Loading(_settings(idle=0))
    await bt.transcribe(_audio(1, 3))
    await asyncio.sleep(0.1)
    gc.collect()
    assert bt.mels and all(ref() is None for ref in bt.mels)


@pytest.fixture
def fake_cohere(monkeypatch: pytest.MonkeyPatch) -> dict[str, Any]:
    """The second listen loads a stand-in model, and MLX is a recorder."""
    state: dict[str, Any] = {"loads": 0, "limits": [], "cleared": 0}
    real_find = importlib.util.find_spec
    monkeypatch.setattr(
        importlib.util,
        "find_spec",
        lambda name, *a: object() if name == "mlx_audio" else real_find(name, *a),
    )
    monkeypatch.setattr(engine, "current", lambda: engine.MLX)

    class FakeMx:
        def set_memory_limit(self, n: int) -> None:
            state["limits"].append(("memory", n))

        def set_cache_limit(self, n: int) -> None:
            state["limits"].append(("cache", n))

        def get_cache_memory(self) -> int:
            return 0

        def get_active_memory(self) -> int:
            return 0

        def clear_cache(self) -> None:
            state["cleared"] += 1

    monkeypatch.setattr(cohere, "_mlx", lambda: FakeMx())

    def load(self: cohere.CohereRecogniser) -> Any:
        state["loads"] += 1
        return object()

    monkeypatch.setattr(cohere.CohereRecogniser, "_load", load)
    monkeypatch.setattr(cohere.CohereRecogniser, "_hear", lambda self, st: ["x" for _ in st])
    monkeypatch.setattr(cohere, "_REAPER", [])
    return state


def test_the_second_listen_sets_mlx_s_limits_when_it_loads_first(
    fake_cohere: dict[str, Any],
) -> None:
    # an ml runner whose first job is a second listen: MLX's cache was unbounded until
    # Whisper loaded
    s = WhisperSettings(gpu_limit_gb=20, gpu_cache_gb=1.5)
    cohere.recogniser(MODEL, None, s)
    assert fake_cohere["limits"] == [("memory", 20_000_000_000), ("cache", 1_500_000_000)]


def test_an_idle_second_listen_lets_its_model_go_and_loads_it_again(
    fake_cohere: dict[str, Any],
) -> None:
    s = WhisperSettings(unload_idle_s=600)
    rec = cohere.recogniser(MODEL, None, s)
    assert rec(["a", "b"]) == ["x", "x"]
    # not yet idle long enough
    assert cohere.reap_idle(rec.last_used + 599, s) == []
    assert cohere.reap_idle(rec.last_used + 601, s) == [MODEL]
    assert rec._model is None and rec.unloads == 1
    assert fake_cohere["cleared"] == 1  # MLX's freed buffers given back
    # a caller still holding it hears on: the model is loaded again for the call
    assert rec(["a"]) == ["x"]
    assert fake_cohere["loads"] == 2 and rec._model is not None
    assert cohere.recogniser(MODEL, None, s) is rec


def test_a_second_listen_in_use_or_kept_is_not_let_go(fake_cohere: dict[str, Any]) -> None:
    s = WhisperSettings(unload_idle_s=600)
    rec = cohere.recogniser(MODEL, None, s)
    rec._users = 1  # a call hearing now
    assert cohere.reap_idle(rec.last_used + 10_000, s) == []
    rec._users = 0
    # handed out again: idle from now
    before = rec.last_used
    time.sleep(0.01)
    assert cohere.recogniser(MODEL, None, s) is rec and rec.last_used > before
    # unloading off: kept however long
    off = WhisperSettings(unload_idle_s=0)
    assert cohere.reap_idle(rec.last_used + 10_000, off) == []
    assert rec._model is not None and fake_cohere["loads"] == 1
