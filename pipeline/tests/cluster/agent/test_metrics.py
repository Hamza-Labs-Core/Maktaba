"""The metrics frame's shape (design-agent.md section 3)."""

from __future__ import annotations

import sys
import types
from typing import Any

import pytest

from maktaba_pipeline.cluster.agent.jobs import JobTracker
from maktaba_pipeline.cluster.agent.metrics import MetricsCollector, PipelineView

pytestmark = pytest.mark.unit


class FakeMX:
    def get_active_memory(self) -> int:
        return 3_000_000_000

    def get_cache_memory(self) -> int:
        return 1_000_000_000

    def get_peak_memory(self) -> int:
        return 5_000_000_000


class FakeTranscriber:
    def __init__(self) -> None:
        self._mx = FakeMX()
        self.audio_done = 0.0
        self.in_flight = 3


class Clock:
    t = 10.0

    def __call__(self) -> float:
        return self.t


def test_frame_shape(monkeypatch: pytest.MonkeyPatch) -> None:
    t = FakeTranscriber()
    fake = types.ModuleType("maktaba_pipeline.stt.batched")
    fake._SHARED = {"settings": t}  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "maktaba_pipeline.stt.batched", fake)
    tracker = JobTracker()
    job: Any = types.SimpleNamespace(id=9, stage="transcribe", subject_kind="video",
                                     subject_id="v", attempts=1)  # fmt: skip
    tracker.claimed(job)
    clock = Clock()
    c = MetricsCollector(
        PipelineView(role="node", stages=lambda: ("probe",), stages_off=lambda: {"x": "home_only"}),
        llm_stats=lambda: {"in_flight": 1, "tokens_per_s": 12.5},
        files_status=lambda: {"dir": "/s/1.2.3.4", "on_share": True, "reason": None},
        tracker=tracker,
        clock=clock,
    )
    c.collect()
    clock.t += 2.0
    t.audio_done = 60.0
    f = c.collect()
    assert f["t"] == "metrics" and f["ts"].endswith("Z")
    s = f["system"]
    for k in ("cpu_pct", "load1", "mem_total", "mem_available", "swap_used", "disks", "net"):
        assert k in s
    assert set(s["net"]) == {"rx_bps", "tx_bps"}
    assert all(set(d) == {"path", "total", "free"} for d in s["disks"])
    assert f["gpu"]["apple"] == {"active_gb": 3.0, "cache_gb": 1.0, "peak_gb": 5.0}
    assert isinstance(f["gpu"]["nvidia"], list)
    assert {"rss", "cpu_pct", "threads", "uptime_s"} <= set(f["process"])
    p = f["pipeline"]
    assert p["role"] == "node" and p["stages"] == ["probe"]
    assert p["stages_off"] == {"x": "home_only"}
    assert [j["job_id"] for j in p["jobs"]] == [9]
    assert p["claims_per_min"] == 1
    assert p["stt"] == {"audio_sec_per_s": 30.0, "files_in_flight": 3}
    assert f["llm"] == {"in_flight": 1, "tokens_per_s": 12.5}
    assert f["files"]["on_share"] is True


def test_no_transcriber_means_no_mlx() -> None:
    c = MetricsCollector()
    f = c.collect()
    assert f["llm"] is None and "files" not in f
    assert f["pipeline"]["stt"]["files_in_flight"] >= 0
