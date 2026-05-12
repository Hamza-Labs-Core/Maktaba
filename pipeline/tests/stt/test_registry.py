"""Story 3.5 — registry health filter, fallback walk, transcript flip."""

from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator
from datetime import UTC, datetime
from uuid import UUID

from maktaba_pipeline.stt.protocol import BackendHealth
from maktaba_pipeline.stt.registry import (
    BackendRegistry,
    NoBackendReady,
    flip_active_transcript,
    pick_backend,
)

from ..audio._fake_audio_db import FakeAudioDB


class _FakeBackend:
    def __init__(self, name: str, ready: bool) -> None:
        self.name = name
        self.supports_streaming = True
        self.requires_file = False
        self.cost_per_minute: float | None = 0.0
        self.supports_word_timestamps = False
        self._ready = ready

    async def transcribe(self, audio, language, hints):  # type: ignore[no-untyped-def]
        async def _g() -> AsyncIterator[object]:
            return
            yield  # pragma: no cover

        return _g()

    async def detect_language(self, audio):  # type: ignore[no-untyped-def]
        return "ar"

    async def health(self) -> BackendHealth:
        return BackendHealth(
            ready=self._ready,
            model_loaded=True,
            version="0.0.0",
            device="cpu",
            last_check_at=datetime.now(tz=UTC),
        )

    async def warmup(self) -> None:
        return


def test_list_ready_filters_unhealthy_backends() -> None:
    registry = BackendRegistry()
    registry.register(_FakeBackend("up", ready=True))
    registry.register(_FakeBackend("down", ready=False))
    ready = asyncio.run(registry.list_ready())
    assert {b.name for b in ready} == {"up"}


def test_pick_backend_walks_chain_until_first_ready() -> None:
    registry = BackendRegistry()
    registry.register(_FakeBackend("primary", ready=False))
    registry.register(_FakeBackend("alt", ready=True))

    backend, trace = asyncio.run(pick_backend(registry, primary="primary", fallback=["alt"]))
    assert backend.name == "alt"
    assert trace.attempted == ("primary", "alt")
    assert trace.chosen == "alt"


def test_pick_backend_raises_when_no_ready() -> None:
    registry = BackendRegistry()
    registry.register(_FakeBackend("a", ready=False))
    registry.register(_FakeBackend("b", ready=False))

    try:
        asyncio.run(pick_backend(registry, primary="a", fallback=["b"]))
    except NoBackendReady as exc:
        assert exc.attempted == ["a", "b"]
    else:
        raise AssertionError("expected NoBackendReady")


def test_flip_active_transcript_retires_previous_and_inserts_new() -> None:
    db = FakeAudioDB()
    vid = db.add_video()
    # Audio track row needed by the FK.
    audio_id = db._audio_next_id
    db._dispatch(
        "INSERT INTO audio_tracks "
        "(video_id, track_index, codec, channels, sample_rate, language, title, "
        "is_default, disposition) "
        "VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) "
        "ON CONFLICT (video_id, track_index) DO NOTHING",
        (vid, 0, "aac", 2, 48000, "ara", None, True, "{}"),
        many=False,
    )

    # First flip: no previous active.
    new_id_1 = asyncio.run(
        flip_active_transcript(
            db,
            video_id=vid,
            audio_track_id=audio_id,
            language="ar",
            detected_language="ar",
            language_confidence=0.95,
            backend="whisper-mlx",
            model="large-v3",
            backend_version="0.0.0",
            word_level=False,
            diarized=False,
        )
    )
    assert isinstance(new_id_1, UUID)
    assert db.transcripts[new_id_1].is_active is True

    # Second flip: previous becomes inactive.
    new_id_2 = asyncio.run(
        flip_active_transcript(
            db,
            video_id=vid,
            audio_track_id=audio_id,
            language="ar",
            detected_language="ar",
            language_confidence=0.95,
            backend="whisper-cpu",
            model="large-v3",
            backend_version="0.0.0",
            word_level=False,
            diarized=False,
        )
    )
    assert db.transcripts[new_id_1].is_active is False
    assert db.transcripts[new_id_2].is_active is True
    actives = [t for t in db.transcripts.values() if t.is_active]
    assert len(actives) == 1
