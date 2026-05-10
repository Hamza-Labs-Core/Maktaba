"""Story 6.7 — per-stage caps + per-device GPU locks."""

from __future__ import annotations

import asyncio

import pytest

from maktaba_pipeline.db.jobs import Stage
from maktaba_pipeline.pipeline.concurrency import (
    DEFAULT_CONCURRENCY,
    GPU_STAGES,
    ConcurrencyManager,
    Reservation,
)
from maktaba_pipeline.pipeline.devices import DeviceID


def test_default_caps_match_arch_7_4() -> None:
    """Architecture §7.4: scan=4, probe=4, extract=2, transcribe=1,
    subtitle_gen=2, index=4, thumbnail=2."""
    assert DEFAULT_CONCURRENCY[Stage.SCAN] == 4
    assert DEFAULT_CONCURRENCY[Stage.PROBE] == 4
    assert DEFAULT_CONCURRENCY[Stage.EXTRACT] == 2
    assert DEFAULT_CONCURRENCY[Stage.TRANSCRIBE] == 1
    assert DEFAULT_CONCURRENCY[Stage.SUBTITLE_GEN] == 2
    assert DEFAULT_CONCURRENCY[Stage.INDEX] == 4
    assert DEFAULT_CONCURRENCY[Stage.THUMBNAIL] == 2


def test_default_dict_covers_canonical_stage_set() -> None:
    """Adding a stage to the enum without updating defaults breaks the build."""
    assert set(DEFAULT_CONCURRENCY.keys()) == set(Stage)


def test_subtitle_gen_default_concurrency() -> None:
    """Story 6.7 AC — subtitle_gen cap = 2."""
    mgr = ConcurrencyManager(devices=[])
    assert mgr.cap(Stage.SUBTITLE_GEN) == 2


def test_supports_returns_false_when_zero_cap() -> None:
    mgr = ConcurrencyManager(per_stage={Stage.SCAN: 0}, devices=[])
    assert mgr.supports(Stage.SCAN) is False
    assert mgr.supports(Stage.PROBE) is True


def test_partial_override_preserves_defaults() -> None:
    mgr = ConcurrencyManager(per_stage={Stage.EXTRACT: 8}, devices=[])
    assert mgr.cap(Stage.EXTRACT) == 8
    assert mgr.cap(Stage.PROBE) == 4  # default preserved


def test_transcribe_cap_auto_bumps_to_device_count() -> None:
    """With 2 GPUs, transcribe cap is bumped from default 1 → 2."""
    mgr = ConcurrencyManager(devices=[DeviceID("cuda:0"), DeviceID("cuda:1")])
    assert mgr.cap(Stage.TRANSCRIBE) == 2


def test_transcribe_cap_respects_higher_explicit_override() -> None:
    """If the operator set transcribe=4, single GPU does not lower it."""
    mgr = ConcurrencyManager(
        per_stage={Stage.TRANSCRIBE: 4},
        devices=[DeviceID("cuda:0")],
    )
    assert mgr.cap(Stage.TRANSCRIBE) == 4


def test_gpu_stages_includes_transcribe() -> None:
    assert Stage.TRANSCRIBE in GPU_STAGES


@pytest.mark.asyncio
async def test_acquire_release_cpu_stage_no_device() -> None:
    mgr = ConcurrencyManager(devices=[])
    r = await mgr.acquire(Stage.PROBE)
    assert isinstance(r, Reservation)
    assert r.device is None
    await mgr.release(r)


@pytest.mark.asyncio
async def test_acquire_unconfigured_stage_raises() -> None:
    mgr = ConcurrencyManager(per_stage={Stage.PROBE: 0}, devices=[])
    with pytest.raises(ValueError, match="not configured"):
        await mgr.acquire(Stage.PROBE)


@pytest.mark.asyncio
async def test_concurrency_cap_respected() -> None:
    """5 acquires against cap=2 → at most 2 holders at any sample."""
    mgr = ConcurrencyManager(per_stage={Stage.EXTRACT: 2}, devices=[])
    holders: list[int] = []
    peak = 0

    async def worker() -> None:
        nonlocal peak
        r = await mgr.acquire(Stage.EXTRACT)
        try:
            holders.append(1)
            peak = max(peak, len(holders))
            await asyncio.sleep(0.05)
        finally:
            holders.pop()
            await mgr.release(r)

    await asyncio.gather(*(worker() for _ in range(5)))
    assert peak == 2


@pytest.mark.asyncio
async def test_acquire_with_timeout_zero_raises_when_full() -> None:
    """Saturate cap → next acquire(timeout=0) raises TimeoutError."""
    mgr = ConcurrencyManager(per_stage={Stage.PROBE: 1}, devices=[])
    held = await mgr.acquire(Stage.PROBE)
    try:
        with pytest.raises((asyncio.TimeoutError, TimeoutError)):
            await mgr.acquire(Stage.PROBE, timeout=0.01)
    finally:
        await mgr.release(held)


@pytest.mark.asyncio
async def test_gpu_lock_serializes_transcribe_single_device() -> None:
    """Two transcribe acquires on a single-GPU host run serially."""
    mgr = ConcurrencyManager(
        per_stage={Stage.TRANSCRIBE: 2},  # bumped explicitly
        devices=[DeviceID("cuda:0")],
    )
    holders: list[str] = []
    peak = 0

    async def worker(name: str) -> None:
        nonlocal peak
        r = await mgr.acquire(Stage.TRANSCRIBE)
        try:
            holders.append(name)
            peak = max(peak, len(holders))
            await asyncio.sleep(0.05)
        finally:
            holders.pop()
            await mgr.release(r)

    await asyncio.gather(worker("a"), worker("b"))
    assert peak == 1


@pytest.mark.asyncio
async def test_multi_gpu_runs_in_parallel() -> None:
    """Two GPUs → two transcribe coroutines run on distinct devices."""
    mgr = ConcurrencyManager(
        devices=[DeviceID("cuda:0"), DeviceID("cuda:1")],
    )
    seen_devices: list[DeviceID | None] = []

    async def worker() -> None:
        r = await mgr.acquire(Stage.TRANSCRIBE)
        try:
            seen_devices.append(r.device)
            await asyncio.sleep(0.02)
        finally:
            await mgr.release(r)

    await asyncio.gather(worker(), worker())
    assert set(seen_devices) == {DeviceID("cuda:0"), DeviceID("cuda:1")}


@pytest.mark.asyncio
async def test_mark_device_unhealthy_skips_in_pick() -> None:
    """Unhealthy device is bypassed when a healthy one exists."""
    mgr = ConcurrencyManager(
        per_stage={Stage.TRANSCRIBE: 2},
        devices=[DeviceID("cuda:0"), DeviceID("cuda:1")],
    )
    mgr.mark_device_unhealthy(DeviceID("cuda:0"), recheck_sec=300.0)
    r = await mgr.acquire(Stage.TRANSCRIBE)
    try:
        assert r.device == DeviceID("cuda:1")
    finally:
        await mgr.release(r)


@pytest.mark.asyncio
async def test_mark_device_unhealthy_unknown_device_warns_and_returns() -> None:
    mgr = ConcurrencyManager(devices=[DeviceID("cuda:0")])
    mgr.mark_device_unhealthy(DeviceID("cuda:99"))
    # No exception raised — quiet warn, dict unchanged.
    r = await mgr.acquire(Stage.TRANSCRIBE)
    try:
        assert r.device == DeviceID("cuda:0")
    finally:
        await mgr.release(r)


@pytest.mark.asyncio
async def test_unhealthy_all_falls_back_to_any_device() -> None:
    """Every device unhealthy → still acquire (graceful degradation)."""
    mgr = ConcurrencyManager(devices=[DeviceID("cuda:0")])
    mgr.mark_device_unhealthy(DeviceID("cuda:0"), recheck_sec=300.0)
    r = await mgr.acquire(Stage.TRANSCRIBE)
    try:
        assert r.device == DeviceID("cuda:0")
    finally:
        await mgr.release(r)


@pytest.mark.asyncio
async def test_release_on_acquire_failure_returns_permit() -> None:
    """If pick_device raises, the stage semaphore is released."""

    mgr = ConcurrencyManager(
        per_stage={Stage.TRANSCRIBE: 1},
        devices=[DeviceID("cuda:0")],
    )

    async def boom() -> DeviceID:
        raise RuntimeError("driver init failed")

    mgr._pick_device = boom  # type: ignore[method-assign]
    with pytest.raises(RuntimeError, match="driver"):
        await mgr.acquire(Stage.TRANSCRIBE)
    # Permit was released — a fresh acquire (with the patched method
    # replaced) succeeds.
    mgr._pick_device = ConcurrencyManager._pick_device.__get__(mgr)  # type: ignore[method-assign]
    r = await mgr.acquire(Stage.TRANSCRIBE)
    await mgr.release(r)


def test_devices_property_is_immutable_view() -> None:
    mgr = ConcurrencyManager(devices=[DeviceID("cuda:0")])
    devices = mgr.devices
    assert isinstance(devices, tuple)
    assert devices == (DeviceID("cuda:0"),)
