import asyncio
import pathlib

import pytest

from maktaba_pipeline.perf import Concurrency, ThroughputProbe, load_budgets


REPO_ROOT = pathlib.Path(__file__).resolve().parents[3]
BUDGETS = REPO_ROOT / "shared" / "perf_budgets.yaml"


def test_load_real_budgets_file() -> None:
    b = load_budgets(BUDGETS)
    assert b.version >= 1
    assert "libraries_list" in b.endpoints
    assert b.endpoints["libraries_list"].p95_ms == 80


def test_load_rejects_bad_p99(tmp_path: pathlib.Path) -> None:
    bad = """
version: 1
endpoints:
  e:
    surface: rest
    path: /x
    profile: linux-x86-16gb
    cache: warm
    p95_ms: 100
    p99_ms: 50
"""
    f = tmp_path / "b.yaml"
    f.write_text(bad)
    with pytest.raises(ValueError):
        load_budgets(f)


def test_throughput_probe_per_second() -> None:
    p = ThroughputProbe(window_sec=60.0)
    p.record(10)
    p.record(20)
    p.record(30)
    assert p.total() == 60
    assert p.per_second() >= 0


def test_throughput_probe_reset() -> None:
    p = ThroughputProbe()
    p.record(7)
    p.reset()
    assert p.total() == 0


def test_throughput_probe_rejects_bad_window() -> None:
    with pytest.raises(ValueError):
        ThroughputProbe(window_sec=0)


def test_concurrency_acquires_and_releases() -> None:
    async def go() -> None:
        c = Concurrency("transcode", capacity=2)
        assert c.in_use == 0
        async with c.acquire():
            assert c.in_use == 1
            async with c.acquire():
                assert c.in_use == 2
        assert c.in_use == 0

    asyncio.run(go())


def test_concurrency_blocks_at_capacity() -> None:
    async def go() -> None:
        c = Concurrency("stt", capacity=1)

        async def long_task() -> None:
            async with c.acquire():
                await asyncio.sleep(0.05)

        t1 = asyncio.create_task(long_task())
        await asyncio.sleep(0.01)
        # Now try to acquire with a tight timeout — should fail because t1 holds.
        await asyncio.sleep(0)
        with pytest.raises(asyncio.TimeoutError):
            async with asyncio.timeout(0.02):
                async with c.acquire():
                    pass
        await t1

    asyncio.run(go())
