"""Stories 3.2 / 3.3 / 3.4 — backend smoke tests with injected fakes.

The real backends shell out to mlx_whisper / faster-whisper / OpenAI;
these tests only exercise the orchestration scaffolding around them
(language autodetect, streaming yield, retry, chunking, budget cap).
"""

from __future__ import annotations

import asyncio
from datetime import UTC, datetime
from pathlib import Path
from typing import Any
from uuid import uuid4

import pytest

from maktaba_pipeline.stt.faster_whisper import FasterWhisperBackend
from maktaba_pipeline.stt.mlx import WhisperMLXBackend
from maktaba_pipeline.stt.openai_api import (
    API_CHUNK_BYTES,
    DEFAULT_BACKOFFS_SEC,
    OpenAIWhisperBackend,
    compute_chunk_offsets,
    first_of_next_month,
    monthly_spend_usd,
    period_yyyymm,
    record_stt_usage,
    should_refuse_claim,
)
from maktaba_pipeline.stt.protocol import TranscriptionHints
from tests.audio._fake_audio_db import FakeAudioDB

# --- WhisperMLXBackend ----------------------------------------------------


def test_mlx_health_reports_unready_when_forced_off() -> None:
    backend = WhisperMLXBackend(force_ready=False)
    health = asyncio.run(backend.health())
    assert health.ready is False
    assert health.device == "unavailable"


def test_mlx_yields_normalised_segments_via_injected_fn() -> None:
    raw_segments = [
        {"start": 0.0, "end": 2.5, "text": "  hello  ", "confidence": 0.9},
        {"start": 2.5, "end": 5.0, "text": "world", "confidence": 0.95},
    ]

    def _fake_transcribe(_path: str, _kwargs: dict[str, Any]) -> list[dict[str, Any]]:
        return raw_segments

    backend = WhisperMLXBackend(force_ready=True, transcribe_fn=_fake_transcribe)

    async def _drive() -> list[Any]:
        out = []
        hints = TranscriptionHints()
        async for seg in backend.transcribe("/tmp/audio.wav", language="ar", hints=hints):
            out.append(seg)
        return out

    segments = asyncio.run(_drive())
    assert [s.seq for s in segments] == [0, 1]
    assert segments[0].text == "hello"  # NFC + strip


# --- FasterWhisperBackend -------------------------------------------------


def test_faster_whisper_compute_type_default_per_device() -> None:
    cpu = FasterWhisperBackend(device="cpu", force_ready=True)
    cuda = FasterWhisperBackend(device="cuda", force_ready=True)
    health_cpu = asyncio.run(cpu.health())
    health_cuda = asyncio.run(cuda.health())
    assert health_cpu.details["compute_type"] == "int8"
    assert health_cuda.details["compute_type"] == "float16"


def test_faster_whisper_yields_segments_with_injected_fn() -> None:
    def _fake(_path: str, _kwargs: dict[str, Any]) -> list[dict[str, Any]]:
        return [
            {"start": 0.0, "end": 1.0, "text": "ok", "confidence": 0.5},
            {"start": 1.0, "end": 2.0, "text": "more", "confidence": 0.6},
        ]

    backend = FasterWhisperBackend(device="cpu", force_ready=True, transcribe_fn=_fake)

    async def _drive() -> list[Any]:
        out = []
        hints = TranscriptionHints()
        async for seg in backend.transcribe("/tmp/x.wav", language="ar", hints=hints):
            out.append(seg)
        return out

    segs = asyncio.run(_drive())
    assert [s.text for s in segs] == ["ok", "more"]


# --- OpenAIWhisperBackend -------------------------------------------------


def test_openai_chunking_respects_24mb_default() -> None:
    assert API_CHUNK_BYTES == 24 * 1024 * 1024


def test_openai_default_backoffs_match_ac() -> None:
    assert DEFAULT_BACKOFFS_SEC == (0.5, 1.0, 2.0, 4.0, 8.0)


def test_openai_chunk_planner_returns_single_chunk_for_small_file(tmp_path: Path) -> None:
    p = tmp_path / "a.wav"
    p.write_bytes(b"\x00" * 1024)
    chunks = compute_chunk_offsets(str(p), chunk_bytes=API_CHUNK_BYTES)
    assert len(chunks) == 1
    assert chunks[0].offset_sec == 0.0


def test_openai_chunk_planner_splits_large_file(tmp_path: Path) -> None:
    p = tmp_path / "big.wav"
    p.write_bytes(b"\x00" * 200_000)
    chunks = compute_chunk_offsets(str(p), chunk_bytes=64_000)
    assert len(chunks) == 4  # ceil(200000/64000)
    # offsets are monotonic, in seconds; bytes_per_sec=32_000 default.
    assert chunks[0].offset_sec == 0.0
    assert chunks[1].offset_sec > 0


def test_openai_budget_cap_refuses_when_projection_exceeds() -> None:
    # 30 min × $0.006/min = $0.18; cap = $0.10 → refuse.
    refuse = should_refuse_claim(
        duration_sec=30 * 60,
        cost_per_minute=0.006,
        monthly_spent_usd=0.0,
        monthly_cap_usd=0.10,
    )
    assert refuse is True


def test_openai_budget_cap_accepts_under_projection() -> None:
    refuse = should_refuse_claim(
        duration_sec=10 * 60,
        cost_per_minute=0.006,
        monthly_spent_usd=0.0,
        monthly_cap_usd=0.10,
    )
    assert refuse is False


def test_openai_budget_cap_zero_or_none_means_unlimited() -> None:
    assert (
        should_refuse_claim(
            duration_sec=10_000,
            cost_per_minute=0.006,
            monthly_spent_usd=0.0,
            monthly_cap_usd=None,
        )
        is False
    )
    assert (
        should_refuse_claim(
            duration_sec=10_000,
            cost_per_minute=0.006,
            monthly_spent_usd=0.0,
            monthly_cap_usd=0,
        )
        is False
    )


def test_openai_health_reports_ready_when_transcribe_fn_is_set() -> None:
    backend = OpenAIWhisperBackend(transcribe_fn=lambda _p, _k: [])
    health = asyncio.run(backend.health())
    assert health.ready is True


# --- usage ledger (HLB-314: the real `stt_usage` producer + reader) ----


def test_period_yyyymm_buckets_by_calendar_month() -> None:
    assert period_yyyymm(datetime(2026, 5, 17, tzinfo=UTC)) == 202605
    assert period_yyyymm(datetime(2026, 12, 31, tzinfo=UTC)) == 202612


def test_first_of_next_month_rolls_year() -> None:
    assert first_of_next_month(datetime(2026, 5, 17, 9, 0, tzinfo=UTC)) == datetime(
        2026, 6, 1, tzinfo=UTC
    )
    # December → next January (year rolls).
    assert first_of_next_month(datetime(2026, 12, 9, tzinfo=UTC)) == datetime(
        2027, 1, 1, tzinfo=UTC
    )


def test_record_stt_usage_writes_then_accrues_ledger() -> None:
    db = FakeAudioDB(dialect="postgres")
    lib = uuid4()
    when = datetime(2026, 5, 17, tzinfo=UTC)

    async def _drive() -> tuple[float, float]:
        first = await record_stt_usage(
            db,
            library_id=lib,
            backend="openai-api",
            duration_sec=30 * 60,  # 30 min
            cost_per_minute=0.006,
            when=when,
        )
        # A second paid job the same month must ACCRUE, not overwrite.
        await record_stt_usage(
            db,
            library_id=lib,
            backend="openai-api",
            duration_sec=10 * 60,
            cost_per_minute=0.006,
            when=when,
        )
        spent = await monthly_spend_usd(db, library_id=lib, backend="openai-api", when=when)
        return first, spent

    first, spent = asyncio.run(_drive())
    assert first == pytest.approx(30 * 0.006)  # $0.18
    # 30 min + 10 min = 40 min × $0.006 = $0.24 accrued.
    assert spent == pytest.approx(40 * 0.006)


def test_record_stt_usage_is_noop_for_free_backend() -> None:
    db = FakeAudioDB(dialect="postgres")
    lib = uuid4()
    when = datetime(2026, 5, 17, tzinfo=UTC)

    async def _drive() -> float:
        recorded = await record_stt_usage(
            db,
            library_id=lib,
            backend="whisper-mlx",
            duration_sec=9999,
            cost_per_minute=0.0,  # local backend — free
            when=when,
        )
        return recorded

    assert asyncio.run(_drive()) == 0.0
    assert db.stt_usage == {}


def test_monthly_spend_usd_zero_when_no_rows() -> None:
    db = FakeAudioDB(dialect="postgres")

    async def _drive() -> float:
        return await monthly_spend_usd(
            db, library_id=uuid4(), backend="openai-api", when=datetime(2026, 5, 1, tzinfo=UTC)
        )

    assert asyncio.run(_drive()) == 0.0


def test_should_refuse_claim_consumes_real_recorded_spend() -> None:
    """The cap projection now has a real data source (was a facade)."""
    db = FakeAudioDB(dialect="postgres")
    lib = uuid4()
    when = datetime(2026, 5, 17, tzinfo=UTC)

    async def _drive() -> bool:
        await record_stt_usage(
            db,
            library_id=lib,
            backend="openai-api",
            duration_sec=20 * 60,  # $0.12 already spent this month
            cost_per_minute=0.006,
            when=when,
        )
        spent = await monthly_spend_usd(db, library_id=lib, backend="openai-api", when=when)
        # A further 20-min job projects $0.12 more → $0.24 > $0.20 cap.
        return should_refuse_claim(
            duration_sec=20 * 60,
            cost_per_minute=0.006,
            monthly_spent_usd=spent,
            monthly_cap_usd=0.20,
        )

    assert asyncio.run(_drive()) is True
