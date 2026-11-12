"""MLX's memory in a transcription process (stt/mlx_memory.py): the limits, the cache trim
and the idle time, from Whisper's settings or this machine's knobs. Whisper's and the second
listen's idle unload are tested beside them (test_batched.py, test_cohere.py)."""

from __future__ import annotations

from typing import Any

import pytest

from maktaba_pipeline.stt import mlx_memory
from maktaba_pipeline.stt.settings import WhisperSettings


class FakeMx:
    def __init__(self, cache: int = 0) -> None:
        self.calls: list[tuple[str, int]] = []
        self.cache = cache
        self.cleared = 0

    def set_memory_limit(self, n: int) -> None:
        self.calls.append(("memory", n))

    def set_cache_limit(self, n: int) -> None:
        self.calls.append(("cache", n))

    def get_cache_memory(self) -> int:
        return self.cache

    def get_active_memory(self) -> int:
        return 0

    def clear_cache(self) -> None:
        self.cleared += 1
        self.cache = 0


def test_limits_come_from_whisper_s_settings_else_the_machine_s_knobs(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    mx = FakeMx()
    mlx_memory.apply_limits(mx, WhisperSettings(gpu_limit_gb=12, gpu_cache_gb=0.5))
    assert mx.calls == [("memory", 12_000_000_000), ("cache", 500_000_000)]
    monkeypatch.setenv("MAKTABA_STT_GPU_GB", "20")
    monkeypatch.setenv("MAKTABA_STT_GPU_CACHE_GB", "2")
    mx = FakeMx()
    mlx_memory.apply_limits(mx, None)
    assert mx.calls == [("memory", 20_000_000_000), ("cache", 2_000_000_000)]


def test_the_cache_is_emptied_only_past_its_cap() -> None:
    s = WhisperSettings(gpu_cache_gb=1)
    under = FakeMx(cache=900_000_000)
    mlx_memory.trim(under, s)
    assert under.cleared == 0
    over = FakeMx(cache=1_100_000_000)
    mlx_memory.trim(over, s)
    assert over.cleared == 1
    mlx_memory.trim(None, s)  # no MLX: nothing to do


def test_the_idle_time_from_settings_or_the_knob(monkeypatch: pytest.MonkeyPatch) -> None:
    assert mlx_memory.unload_idle_s(WhisperSettings(unload_idle_s=30)) == 30
    assert mlx_memory.unload_idle_s(WhisperSettings(unload_idle_s=-5)) == 0
    assert mlx_memory.unload_idle_s(None) == 600
    monkeypatch.setenv("MAKTABA_STT_UNLOAD_IDLE_S", "0")
    assert mlx_memory.unload_idle_s(None) == 0
    monkeypatch.setenv("MAKTABA_STT_UNLOAD_IDLE_S", "soon")
    assert mlx_memory.unload_idle_s(None) == 600


def test_releasing_gives_mlx_s_buffers_back(monkeypatch: pytest.MonkeyPatch) -> None:
    mx: Any = FakeMx(cache=3)
    mlx_memory.release(mx, "m", 601)
    assert mx.cleared == 1
    mlx_memory.release(None, "m", 601)  # without MLX it only collects
