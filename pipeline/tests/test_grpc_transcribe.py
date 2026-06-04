"""Transcribe + STTTest RPCs on the pipeline gRPC service.

These exercise the JSON-dict in / JSON-dict out contract the Go API's
realclient relies on. The STT backend is a tiny in-memory fake so the
suite never loads a model or spawns a subprocess; the default
registry-driven path is what's under test (PipelineService selects the
first ready backend and streams its segments).
"""

from __future__ import annotations

import asyncio
import json
from collections.abc import AsyncIterator, Coroutine
from datetime import UTC, datetime
from typing import Any

from maktaba_pipeline.grpc_server import PipelineService, _dispatch
from maktaba_pipeline.stt.protocol import BackendHealth, Segment
from maktaba_pipeline.stt.registry import BackendRegistry


class _FakeBackend:
    """Minimal STTBackend: yields canned segments, reports ready."""

    def __init__(
        self,
        name: str = "fake",
        *,
        ready: bool = True,
        segments: list[Segment] | None = None,
    ) -> None:
        self.name = name
        self.model = "tiny"
        self.supports_streaming = True
        self.requires_file = True
        self.cost_per_minute: float | None = 0.0
        self.supports_word_timestamps = False
        self._ready = ready
        self._segments = segments or [
            Segment(seq=0, start_sec=0.0, end_sec=1.0, text="Bismillah"),
            Segment(seq=1, start_sec=1.0, end_sec=2.5, text="ar-Rahman", speaker="Sheikh"),
        ]
        self.seen_audio: list[str] = []

    async def transcribe(
        self, audio: Any, language: str | None, hints: Any
    ) -> AsyncIterator[Segment]:
        self.seen_audio.append(audio)
        for seg in self._segments:
            yield seg

    async def detect_language(self, audio: Any) -> str:
        return "ar"

    async def health(self) -> BackendHealth:
        return BackendHealth(
            ready=self._ready,
            model_loaded=True,
            version="1.0.0",
            device="cpu",
            last_check_at=datetime.now(tz=UTC),
        )

    async def warmup(self) -> None:
        return


def _svc(backend: _FakeBackend | None = None) -> tuple[PipelineService, _FakeBackend]:
    backend = backend or _FakeBackend()
    reg = BackendRegistry()
    reg.register(backend)
    return PipelineService(stt_registry=reg), backend


def _run(coro: Coroutine[Any, Any, Any]) -> Any:
    return asyncio.run(coro)


def test_transcribe_returns_segments() -> None:
    svc, backend = _svc()
    resp = _run(svc.transcribe({"path": "/media/movie.mkv"}))
    segs = resp["segments"]
    assert len(segs) == 2
    assert segs[0]["seq"] == 0
    assert segs[0]["text"] == "Bismillah"
    assert segs[1]["speaker"] == "Sheikh"
    assert all(s["final"] is True for s in segs)
    # The audio path flowed through to the backend.
    assert backend.seen_audio == ["/media/movie.mkv"]


def test_transcribe_accepts_video_id_alias() -> None:
    svc, backend = _svc()
    resp = _run(svc.transcribe({"video_id": "/media/lecture.mp4"}))
    assert len(resp["segments"]) == 2
    assert backend.seen_audio == ["/media/lecture.mp4"]


def test_transcribe_missing_path_is_inband_error() -> None:
    svc, _ = _svc()
    # Driven through _dispatch so we assert the wire-level error contract.
    raw = _run(_dispatch(json.dumps({}).encode("utf-8"), svc.transcribe))
    out = json.loads(raw)
    assert "error" in out
    assert "path is required" in out["error"]


def test_transcribe_no_backend_is_inband_error() -> None:
    svc = PipelineService(stt_registry=BackendRegistry())  # empty registry
    raw = _run(_dispatch(json.dumps({"path": "/x.mkv"}).encode("utf-8"), svc.transcribe))
    out = json.loads(raw)
    assert "error" in out
    assert "no STT backend ready" in out["error"]


def test_transcriber_seam_overrides_default() -> None:
    async def fake_transcriber(path: str, language: str | None) -> list[dict[str, Any]]:
        return [
            {
                "seq": 0,
                "start_sec": 0.0,
                "end_sec": 1.0,
                "text": "injected",
                "speaker": "",
                "final": True,
            }
        ]

    svc = PipelineService(transcriber=fake_transcriber)
    resp = _run(svc.transcribe({"path": "/x.mkv"}))
    assert resp["segments"][0]["text"] == "injected"


def test_stt_test_runs_bundled_sample() -> None:
    svc, backend = _svc()
    resp = _run(svc.stt_test({"backend": "fake"}))
    assert resp["ok"] is True
    assert resp["backend"] == "fake"
    assert resp["segments"] == 2
    assert resp["latency_ms"] >= 0
    # The bundled sample (a real temp WAV) was the audio source.
    assert backend.seen_audio and backend.seen_audio[0].endswith(".wav")
    assert "Bismillah" in resp["sample_text"]


def test_stt_test_defaults_to_first_ready_backend() -> None:
    svc, _ = _svc(_FakeBackend(name="whisper"))
    resp = _run(svc.stt_test({}))
    assert resp["backend"] == "whisper"


def test_stt_test_unknown_backend_is_inband_error() -> None:
    svc, _ = _svc()
    raw = _run(_dispatch(json.dumps({"backend": "nope"}).encode("utf-8"), svc.stt_test))
    out = json.loads(raw)
    assert "error" in out
    assert "unknown backend" in out["error"]
