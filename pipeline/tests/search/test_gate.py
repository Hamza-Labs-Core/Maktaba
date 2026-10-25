"""The search index's writes (search/gate.py): one at a time, never cut short, none once
the worker stops.

Not ``unit``-marked: asyncio's loop opens a socket pair (the unit tier's netguard)."""

from __future__ import annotations

import asyncio
import threading
from collections.abc import Iterator
from typing import Any

import pytest

from maktaba_pipeline.search import gate


class _QuietLog:
    def info(self, *_a: Any, **_k: Any) -> None:
        pass

    warning = info


@pytest.fixture(autouse=True)
def _quiet_gate(monkeypatch: pytest.MonkeyPatch) -> None:
    """The gate's own log line goes nowhere (an earlier test may have left the log's
    stream closed)."""
    monkeypatch.setattr(gate, "_log", _QuietLog())


@pytest.fixture(autouse=True)
def _open_gate() -> Iterator[None]:
    gate.reopen()
    yield
    gate.reopen()


def _slow(log: list[str], name: str, sec: float = 0.2) -> None:
    log.append(f"{name}+")
    threading.Event().wait(sec)
    log.append(f"{name}-")


@pytest.mark.asyncio
async def test_writes_run_one_at_a_time() -> None:
    log: list[str] = []
    await asyncio.gather(*(gate.write(_slow, log, n, 0.05) for n in "abc"))
    assert len(log) == 6
    for i in range(0, 6, 2):  # every write ends before the next starts
        assert log[i][0] == log[i + 1][0] and log[i].endswith("+") and log[i + 1].endswith("-")


@pytest.mark.asyncio
async def test_a_cancelled_write_finishes_before_the_job_ends() -> None:
    log: list[str] = []
    job = asyncio.create_task(gate.write(_slow, log, "a", 0.3))
    await asyncio.sleep(0.05)
    job.cancel()
    await asyncio.sleep(0)
    job.cancel()  # twice: still waits
    with pytest.raises(asyncio.CancelledError):
        await job
    assert log == ["a+", "a-"]


@pytest.mark.asyncio
async def test_no_write_starts_once_closed() -> None:
    log: list[str] = []
    running = asyncio.create_task(gate.write(_slow, log, "a", 0.2))
    await asyncio.sleep(0.05)
    waiting = asyncio.create_task(gate.write(_slow, log, "b"))
    await asyncio.sleep(0.01)
    gate.close()
    await running  # the write in progress finishes
    with pytest.raises(gate.IndexClosed):
        await waiting  # the queued one never starts
    with pytest.raises(gate.IndexClosed):
        await gate.write(_slow, log, "c")
    with pytest.raises(gate.IndexClosed):
        await gate.read(len, "x")
    assert log == ["a+", "a-"]
    # a cancellation: the job ends as interrupted (claimed again), not as failed
    assert issubclass(gate.IndexClosed, asyncio.CancelledError)


@pytest.mark.asyncio
async def test_a_write_raises_its_own_error() -> None:
    def boom() -> None:
        raise ValueError("chroma said no")

    with pytest.raises(ValueError, match="chroma said no"):
        await gate.write(boom)
    assert await gate.write(lambda: 7) == 7


def test_wait_idle() -> None:
    assert gate.wait_idle(0)
    hold = threading.Event()
    started = threading.Event()

    def writer() -> None:
        with gate._write_lock:
            started.set()
            hold.wait(2)

    t = threading.Thread(target=writer)
    t.start()
    started.wait(2)
    assert not gate.wait_idle(0.05)
    hold.set()
    t.join()
    assert gate.wait_idle(0.5)


def test_close_clients_closes_each_and_forgets_them(monkeypatch: pytest.MonkeyPatch) -> None:
    from maktaba_pipeline.search import semantic

    class WithClose:
        closed = 0

        def close(self) -> None:
            WithClose.closed += 1

    class System:
        stopped = 0

        def stop(self) -> None:
            System.stopped += 1

    class Older:  # a chromadb without Client.close: its system is stopped
        _system = System()

    class Broken:
        def close(self) -> None:
            raise RuntimeError("already closed")

    monkeypatch.setattr(semantic, "_CLIENTS", {"/a": WithClose(), "/b": Older(), "/c": Broken()})
    monkeypatch.setattr(semantic, "_COLLECTIONS", {("/a", "c"): object()})
    assert semantic.close_clients() == 3
    assert WithClose.closed == 1 and System.stopped == 1
    assert semantic._CLIENTS == {} and semantic._COLLECTIONS == {}
    assert semantic.close_clients() == 0
