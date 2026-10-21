"""A node whose home's database stops answering pauses (PR #145): it claims nothing, its
jobs in flight finish unless the outage nears the claim lease, and it resumes by itself
at the first beat the home answers, releasing what its ended jobs left claimed."""

from __future__ import annotations

import asyncio
import contextlib
import json
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.cluster import exitcodes, nodes_db
from maktaba_pipeline.cluster.agent.client import AgentIdentity, ChannelAgent
from maktaba_pipeline.cluster.agent.logship import LogShipper
from maktaba_pipeline.cluster.node import WorkerAgent, home_unreachable
from maktaba_pipeline.cluster.stages import StageSelector
from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.engine.workers import register_worker
from maktaba_pipeline.pipeline.runner import ClaimLoop, WorkerConfig
from maktaba_pipeline.pipeline.wakeup import PollOnlyWakeup
from maktaba_pipeline.runtime import Database
from tests.cluster.test_node_runtime import LEGACY_HANDLERS, _node, _worker_stages
from tests.engine.factory import make_library, make_video


class PostgresConnectionError(Exception):
    """asyncpg's class 08 base, by name."""


class ConnectionDoesNotExistError(PostgresConnectionError):
    pass


class InterfaceError(Exception):
    pass


class UndefinedTableError(Exception):
    pass


def test_a_lost_database_is_told_from_one_that_answers_with_an_error() -> None:
    lost: tuple[BaseException, ...] = (
        ConnectionRefusedError("refused"),
        TimeoutError(),
        ConnectionDoesNotExistError("connection was closed in the middle of operation"),
        InterfaceError("cannot perform operation: connection is closed"),
    )
    answered: tuple[BaseException, ...] = (
        UndefinedTableError("relation does not exist"),
        ValueError("x"),
        InterfaceError("busy"),
    )
    for exc in lost:
        assert home_unreachable(exc), exc
    for exc in answered:
        assert not home_unreachable(exc), exc


class _Claims:
    """The claim loop's in-flight jobs, as the beat sees them."""

    def __init__(self) -> None:
        self.in_flight: set[asyncio.Task[None]] = set()
        self.stops = 0

    def run(self) -> None:
        self.in_flight.add(asyncio.ensure_future(asyncio.sleep(3600)))

    def stop_in_flight(self) -> int:
        self.stops += 1
        n = 0
        for t in self.in_flight:
            if not t.done():
                t.cancel()
                n += 1
        return n


async def _agent(db: Database, node_id: str) -> tuple[WorkerAgent, asyncio.Event, _Claims]:
    await register_worker(db, worker_id="w1", host="h", stages=[], pools=[])
    stop = asyncio.Event()
    selector = StageSelector(
        graph=GRAPH, handlers=frozenset(LEGACY_HANDLERS), role="node", storage=None
    )
    agent = WorkerAgent(
        db=db,
        worker_id="w1",
        selector=selector,
        graph=GRAPH,
        shutdown=stop,
        node_id=node_id,
        explicit_path_map=True,
    )
    await agent.apply()
    claims = _Claims()
    agent.claims = claims
    return agent, stop, claims


def _home_goes(monkeypatch: pytest.MonkeyPatch) -> dict[str, bool]:
    """``nodes_db.beat`` refused while ``state["down"]`` (the home's database gone)."""
    state = {"down": False}
    real = nodes_db.beat

    async def beat(db: Any, node_id: str) -> Any:
        if state["down"]:
            raise ConnectionRefusedError(
                "[WinError 1225] The remote computer refused the network connection"
            )
        return await real(db, node_id)

    monkeypatch.setattr(nodes_db, "beat", beat)
    return state


async def _claimed_by_w1(db: Database) -> int:
    lib = await make_library(db)
    v = await make_video(db, lib)
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="probe")
    job = await claim_for_pool(db, worker_id="w1", pool="nas_read", stages=["probe"])
    assert job is not None
    return int(job.id)


async def _job(db: Database, job_id: int) -> tuple[str, str | None]:
    row = await db.fetchrow("SELECT state, claimed_by FROM processing_jobs WHERE id = $1", job_id)
    return str(row["state"]), row["claimed_by"]


@pytest.mark.asyncio
async def test_a_node_pauses_while_the_homes_database_is_gone_and_resumes(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    await sync_graph(db, GRAPH)
    agent, stop, claims = await _agent(db, await _node(db))
    leftover = await _claimed_by_w1(db)  # a job of this node's that ends in the outage
    assert "probe" in agent.selector.current()
    home = _home_goes(monkeypatch)

    home["down"] = True
    await agent.beat()
    assert not agent.paused, "one unanswered beat is a blip"
    claims.run()
    await agent.beat()
    assert agent.paused and agent.selector.current() == ()
    assert agent.selector.unreachable is not None
    assert agent.selector.unreachable.startswith("paused: the home's database does not answer")
    assert agent.selector.stages_off["probe"].startswith("paused:")
    assert not stop.is_set() and exitcodes.exit_code(0) == 0  # never a reason to stop
    assert claims.stops == 0, "jobs in flight finish while the claim lease holds"
    assert await agent.refresh() is False  # nothing is read meanwhile
    stages, _ = await _worker_stages(db)
    assert "probe" in stages, "nothing is written to the home while it is gone"

    # the outage nears the claim lease: the jobs in flight are stopped, once
    agent.stop_jobs_after_sec = 0
    await agent.beat()
    await agent.beat()
    assert claims.stops == 1
    await asyncio.sleep(0)
    assert all(t.done() for t in claims.in_flight)

    home["down"] = False
    await agent.beat()
    assert not agent.paused and "probe" in agent.selector.current()
    # nothing in flight here: what its ended jobs left claimed is released at once
    assert await _job(db, leftover) == ("paused", None)
    stages, off = await _worker_stages(db)
    assert "probe" in stages and "probe" not in off


@pytest.mark.asyncio
async def test_after_a_short_outage_a_job_still_running_keeps_its_claim(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    await sync_graph(db, GRAPH)
    agent, _, claims = await _agent(db, await _node(db))
    running = await _claimed_by_w1(db)
    claims.run()
    home = _home_goes(monkeypatch)
    home["down"] = True
    await agent.beat()
    await agent.beat()
    assert agent.paused and claims.stops == 0
    home["down"] = False
    await agent.beat()
    assert not agent.paused
    state, by = await _job(db, running)
    assert by == "w1" and state == "claimed"
    for t in claims.in_flight:
        t.cancel()


@pytest.mark.asyncio
async def test_a_hanging_beat_counts_as_unanswered(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    agent, _, _ = await _agent(db, await _node(db))
    agent.beat_timeout_sec = 0.01

    async def hangs(db: Any, node_id: str) -> None:
        await asyncio.sleep(3600)

    monkeypatch.setattr(nodes_db, "beat", hangs)
    await agent.beat()
    await agent.beat()
    assert agent.paused


@pytest.mark.asyncio
async def test_an_error_the_database_answers_is_not_an_outage(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    agent, _, _ = await _agent(db, await _node(db))

    async def broken(db: Any, node_id: str) -> None:
        raise UndefinedTableError("function cluster_node_beat(uuid) does not exist")

    monkeypatch.setattr(nodes_db, "beat", broken)
    for _ in range(3):
        with pytest.raises(UndefinedTableError):
            await agent.beat()
    assert not agent.paused


class _LostDB:
    """A database that went away: every call is refused."""

    dialect = "postgres"

    def __init__(self) -> None:
        self.calls = 0

    async def _refused(self, *_a: Any, **_k: Any) -> Any:
        self.calls += 1
        raise ConnectionRefusedError("refused")

    fetch = fetchrow = fetchval = execute = _refused


@pytest.mark.asyncio
async def test_the_claim_loop_survives_a_lost_database() -> None:
    db = _LostDB()
    stop = asyncio.Event()
    cfg = WorkerConfig(
        supported_stages=("probe",),
        claim_poll_sec=0.01,
        claim_poll_max_sec=0.02,
        worker_id="w1",
        pool_mode=True,
        stage_pools={"probe": "nas_read"},
        dynamic_stages=lambda: ("probe",),
    )

    async def dispatch(job: Any) -> None:
        raise AssertionError("nothing is claimed")

    loop = ClaimLoop(db, cfg, dispatch, shutdown_event=stop, wakeup=PollOnlyWakeup(poll_sec=0.01))
    task = asyncio.ensure_future(loop.run())
    for _ in range(200):
        if db.calls >= 3:
            break
        await asyncio.sleep(0.01)
    assert db.calls >= 3 and not task.done(), "each tick tries again; the loop lives on"
    stop.set()
    await asyncio.wait_for(task, timeout=5)


@pytest.mark.asyncio
async def test_stop_in_flight_cancels_the_loops_jobs() -> None:
    cfg = WorkerConfig(supported_stages=("probe",), worker_id="w1")

    async def dispatch(job: Any) -> None:
        return None

    loop = ClaimLoop(_LostDB(), cfg, dispatch, wakeup=PollOnlyWakeup(poll_sec=1))
    job = asyncio.ensure_future(asyncio.sleep(3600))
    loop.in_flight.add(job)
    assert loop.stop_in_flight() == 1
    with contextlib.suppress(asyncio.CancelledError):
        await job
    assert job.cancelled() and loop.stop_in_flight() == 0


def test_the_agents_status_file_says_the_worker_is_paused(tmp_path: Path) -> None:
    reason = "paused: the home's database does not answer (since 2026-10-20 04:00 UTC)"
    held: dict[str, str | None] = {"why": reason}
    ident = AgentIdentity(
        kind="node",
        url="ws://127.0.0.1:1",
        worker_id="node-ab12cd34-1",
        host="win-box",
        role="node",
        node_id="ab12cd34-0000-0000-0000-000000000000",
        node_private=None,
        home_static=b"",
        home_key_path=None,
        ip="127.0.0.1",
        os="windows",
        version="0.0.1",
    )
    path = tmp_path / "w1.json"
    agent = ChannelAgent(
        ident, shipper=LogShipper("info"), status_path=path, paused=lambda: held["why"]
    )
    agent._set_state("backoff")  # the channel retrying while the home is gone
    assert json.loads(path.read_text(encoding="utf-8"))["paused"] == reason
    held["why"] = None
    agent._set_state("connecting")
    assert json.loads(path.read_text(encoding="utf-8"))["paused"] is None
