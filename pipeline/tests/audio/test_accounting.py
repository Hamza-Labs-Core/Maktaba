"""Story 2.4 — extract concurrency cap + CPU throttle."""

from __future__ import annotations

import asyncio
from datetime import datetime, timedelta

import pytest

from maktaba_pipeline.audio.accounting import (
    DEFAULT_CPU_THROTTLE_DELAY_SEC,
    ExtractAccountant,
    cpu_throttle_not_before,
)


def test_concurrency_cap_enforces_inflight_count() -> None:
    accountant = ExtractAccountant(capacity=2)

    async def _run() -> None:
        observed: list[int] = []

        async def _job() -> None:
            async with accountant.slot():
                observed.append(accountant.in_flight)
                await asyncio.sleep(0.02)

        await asyncio.gather(*[_job() for _ in range(5)])
        assert max(observed) <= 2

    asyncio.run(_run())


def test_capacity_below_one_rejected() -> None:
    with pytest.raises(ValueError):
        ExtractAccountant(capacity=0)


def test_cpu_throttle_returns_none_under_threshold() -> None:
    nb = cpu_throttle_not_before(load_avg_5m=0.5, cores=4)
    assert nb is None


def test_cpu_throttle_bumps_not_before_when_loaded() -> None:
    now = datetime(2026, 1, 1, 12, 0, 0)
    nb = cpu_throttle_not_before(load_avg_5m=8.0, cores=4, now=now)
    assert nb is not None
    assert nb - now == timedelta(seconds=DEFAULT_CPU_THROTTLE_DELAY_SEC)


def test_cpu_throttle_with_zero_cores_reports_no_throttle() -> None:
    assert cpu_throttle_not_before(load_avg_5m=10.0, cores=0) is None
