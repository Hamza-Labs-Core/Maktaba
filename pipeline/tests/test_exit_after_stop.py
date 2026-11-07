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
    monkeypatch.setattr(cli, "_THREAD_END_SEC", 0.2)
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


def test_a_clean_stop_exits_normally(exits: list[int], monkeypatch: pytest.MonkeyPatch) -> None:
    before = set(cli._lingering_threads())  # other tests' threads are not this exit's
    real = cli._lingering_threads
    monkeypatch.setattr(cli, "_lingering_threads", lambda: [t for t in real() if t not in before])

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


def test_the_exit_never_cuts_a_write(monkeypatch: pytest.MonkeyPatch, exits: list[int]) -> None:
    """A write still running after both waits: no os._exit (the process ends once the
    write's thread did, or at the supervisor's kill), and no client is closed under it."""
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
        asyncio.run(cli._exit_if_threads_linger(0, log))
    finally:
        hold.set()
        t.join()
    assert exits == [] and log.events[-1][0] == "search_index_write_still_running"


def test_a_thread_ending_as_the_worker_stops_is_not_left_behind(
    exits: list[int], monkeypatch: pytest.MonkeyPatch
) -> None:
    # issue #862: a closed connection's thread ends a moment after its close returned; the
    # count gives the threads that are ending their grace, and the exit is the normal one
    before = set(cli._lingering_threads())
    real = cli._lingering_threads
    monkeypatch.setattr(cli, "_lingering_threads", lambda: [t for t in real() if t not in before])
    monkeypatch.setattr(cli, "_THREAD_END_SEC", 2.0)
    ending = threading.Thread(target=threading.Event().wait, args=(0.3,), name="ending")
    ending.start()
    log = _Log()
    asyncio.run(cli._exit_if_threads_linger(0, log))
    assert exits == []
    assert not ending.is_alive()
    assert all(e != "pipeline_exit_threads_left" for e, _ in log.events)


def test_a_clean_stop_through_serve_leaves_no_thread_and_never_exits(
    exits: list[int], monkeypatch: pytest.MonkeyPatch, tmp_path: Any
) -> None:
    # issue #862, end to end: the worker on SQLite stops cleanly with its database's
    # aiosqlite thread slow to end (a loaded runner); _serve closes the database, waits for
    # that thread, and returns without os._exit and with no thread of its own left running
    import time

    import aiosqlite.core

    from maktaba_pipeline.cluster import config as node_config

    real_worker = aiosqlite.core._connection_worker_thread

    def slow_to_end(tx: Any) -> None:
        real_worker(tx)
        time.sleep(0.3)

    monkeypatch.setattr(aiosqlite.core, "_connection_worker_thread", slow_to_end)
    monkeypatch.setenv(node_config.ENV_VAR, str(tmp_path / "node.json"))
    monkeypatch.setenv("MAKTABA_DATABASE_URL", f"sqlite:///{tmp_path / 'w.db'}")
    monkeypatch.delenv("MAKTABA_PIPELINE_WORKER_ID", raising=False)

    async def fake_run(_cfg: Any, *, db: Any, dispatch_overrides: Any) -> int:
        return 0

    monkeypatch.setattr(cli, "run", fake_run)
    before = {t for t in threading.enumerate() if not t.daemon}
    log = _Log()
    args = cli._build_parser().parse_args(["run"])
    code = asyncio.run(cli._serve(args, log))
    assert code == 0
    assert exits == []
    assert all(e != "pipeline_exit_threads_left" for e, _ in log.events)
    left = [
        t.name for t in threading.enumerate() if not t.daemon and t.is_alive() and t not in before
    ]
    assert left == []
