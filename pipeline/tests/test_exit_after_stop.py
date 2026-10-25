"""The worker's exit once its runtime stopped (``__main__``): the search index is
closed with no write running, and the process ends without the threads still running
(Python's exit waits for them, and the supervisor killed the index worker at the end of
its grace instead, in the middle of a write).

Not ``unit``-marked: asyncio's loop opens a socket pair (the unit tier's netguard)."""

from __future__ import annotations

import asyncio
import threading
from typing import Any

import pytest

from maktaba_pipeline import __main__ as cli
from maktaba_pipeline.search import gate, semantic


class _Log:
    def __init__(self) -> None:
        self.events: list[tuple[str, dict[str, Any]]] = []

    def info(self, event: str, **kw: Any) -> None:
        self.events.append((event, kw))

    warning = error = info


class _Exited(Exception):
    pass


class _QuietLog:
    def info(self, *_a: Any, **_k: Any) -> None:
        pass

    warning = info


@pytest.fixture(autouse=True)
def _quiet_gate(monkeypatch: pytest.MonkeyPatch) -> None:
    """The gate's own log line goes nowhere (an earlier test may have left the log's
    stream closed)."""
    monkeypatch.setattr(gate, "_log", _QuietLog())


@pytest.fixture
def exits(monkeypatch: pytest.MonkeyPatch) -> list[int]:
    codes: list[int] = []

    def fake_exit(code: int) -> None:
        codes.append(code)
        raise _Exited

    monkeypatch.setattr("maktaba_pipeline.__main__.os._exit", fake_exit)
    monkeypatch.setattr(cli, "_EXIT_WAIT_SEC", 0.2)
    monkeypatch.setattr(cli, "_INDEX_CLOSE_WAIT_SEC", 0.2)
    return codes


def test_the_exit_does_not_wait_for_a_stuck_thread(exits: list[int]) -> None:
    release = threading.Event()
    stuck = threading.Thread(target=release.wait, name="stuck-on-the-share")
    stuck.start()
    log = _Log()
    try:
        with pytest.raises(_Exited):
            asyncio.run(cli._exit_if_threads_linger(0, log))
    finally:
        release.set()
        stuck.join()
    assert exits == [0]
    (event, kw) = log.events[-1]
    assert event == "pipeline_exit_threads_left" and "stuck-on-the-share" in kw["threads"]


def test_a_clean_stop_exits_normally(exits: list[int]) -> None:
    async def main() -> None:
        await asyncio.to_thread(lambda: None)  # the executor's thread ends at its shutdown
        await cli._exit_if_threads_linger(0, _Log())

    asyncio.run(main())
    assert exits == []


def test_the_search_index_is_closed_after_its_write(monkeypatch: pytest.MonkeyPatch) -> None:
    closed: list[bool] = []

    def close_clients() -> int:
        closed.append(gate.wait_idle(0))
        return 1

    monkeypatch.setattr(semantic, "close_clients", close_clients)
    log = _Log()
    asyncio.run(cli._close_search_index(log))
    assert gate.is_closed() and closed == [True]
    assert ("search_index_closed", {"clients": 1}) in log.events


def test_the_client_is_never_closed_under_a_write(
    monkeypatch: pytest.MonkeyPatch, exits: list[int]
) -> None:
    monkeypatch.setattr(semantic, "close_clients", lambda: pytest.fail("closed under a write"))
    hold, started = threading.Event(), threading.Event()

    def writer() -> None:
        with gate._write_lock:
            started.set()
            hold.wait(5)

    t = threading.Thread(target=writer)
    t.start()
    started.wait(2)
    log = _Log()
    try:
        asyncio.run(cli._close_search_index(log))
    finally:
        hold.set()
        t.join()
    assert log.events[-1][0] == "search_index_write_still_running"
