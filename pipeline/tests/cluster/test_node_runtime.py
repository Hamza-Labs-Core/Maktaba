"""A node's runtime on both databases: its cluster row, node mode, live stage selection."""

from __future__ import annotations

import asyncio
import contextlib
import json
import uuid
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.cluster import exitcodes, nodes_db
from maktaba_pipeline.cluster.node import WorkerAgent
from maktaba_pipeline.cluster.stages import StageSelector
from maktaba_pipeline.db.jobs import DBConn, Job, Stage
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.sql import dumps, is_pg, loads
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.engine.workers import register_worker
from maktaba_pipeline.runtime import (
    Database,
    EngineStart,
    RuntimeConfig,
    StageHandler,
    _start_engine,
)
from tests.engine.factory import make_library, make_video

LEGACY_HANDLERS = {"scan", "probe", "extract", "transcribe", "subtitle_gen", "index", "thumbnail"}


async def _noop(db: DBConn, job: Job) -> None:
    """Never runs: the engine is only booted."""


async def _node(db: Database, *, state: str = "approved", **cols: Any) -> str:
    node_id = str(uuid.uuid4())
    # on Postgres the functions check db_role = session_user (the tests' login)
    role = await db.fetchval("SELECT session_user") if is_pg(db) else "maktaba_node_x"
    await db.execute(
        "INSERT INTO cluster_nodes (id, name, state, node_pubkey, db_role) VALUES ($1,$2,$3,$4,$5)",
        node_id,
        "linux-box",
        state,
        "pub",
        role,
    )
    for col, val in cols.items():
        await db.execute(f"UPDATE cluster_nodes SET {col} = $2 WHERE id = $1", node_id, val)
    return node_id


async def _row(db: Database, node_id: str) -> Any:
    return await db.fetchrow("SELECT * FROM cluster_nodes WHERE id = $1", node_id)


# ------------------------------------------------------------------ nodes_db


@pytest.mark.asyncio
async def test_beat_reports_state_drain_and_map(db: Database) -> None:
    node_id = await _node(db)
    beat = await nodes_db.beat(db, node_id)
    assert beat is not None
    assert (beat.state, beat.drain, beat.path_map, beat.path_map_source) == (
        "approved",
        False,
        None,
        None,
    )
    assert (await _row(db, node_id))["last_seen_at"] is not None
    assert await nodes_db.beat(db, str(uuid.uuid4())) is None


@pytest.mark.asyncio
async def test_report_never_replaces_an_admin_map(db: Database) -> None:
    node_id = await _node(db)
    caps = {"os": "linux", "cpu_count": 8}
    storage = {"read": "ok", "write": "skipped", "checked_at": "t", "detail": None}
    await nodes_db.report(
        db, node_id, capabilities=caps, storage=storage, inferred_path_map={"Y:/": "/mnt/y/"}
    )
    row = await _row(db, node_id)
    assert loads(row["capabilities"]) == caps and loads(row["storage"]) == storage
    assert loads(row["path_map"]) == {"Y:/": "/mnt/y/"} and row["path_map_source"] == "inferred"
    # a storage-only report leaves the rest
    await nodes_db.report(db, node_id, storage={"read": "error"})
    row = await _row(db, node_id)
    assert loads(row["capabilities"]) == caps and loads(row["path_map"]) == {"Y:/": "/mnt/y/"}

    await db.execute(
        "UPDATE cluster_nodes SET path_map = $2, path_map_source = 'admin' WHERE id = $1",
        node_id,
        dumps({"Y:/": "/admin/"}),
    )
    await nodes_db.report(db, node_id, inferred_path_map={"Y:/": "/mnt/other/"})
    row = await _row(db, node_id)
    assert loads(row["path_map"]) == {"Y:/": "/admin/"} and row["path_map_source"] == "admin"
    beat = await nodes_db.beat(db, node_id)
    assert beat is not None and beat.path_map == {"Y:/": "/admin/"}


@pytest.mark.asyncio
async def test_stage_switches(db: Database) -> None:
    await sync_graph(db, GRAPH)
    node_id = await _node(db)
    assert await nodes_db.stage_switches(db, node_id) == {}
    for stage, on in (("tag", False), ("probe", True)):
        await db.execute(
            "INSERT INTO cluster_node_stages (node_id, stage, enabled) VALUES ($1,$2,$3)",
            node_id,
            stage,
            on,
        )
    assert await nodes_db.stage_switches(db, node_id) == {"tag": False, "probe": True}


# ------------------------------------------------------------------ node mode


async def _boot(db: Database, cfg: RuntimeConfig) -> tuple[EngineStart, asyncio.Event]:
    stop = asyncio.Event()
    overrides: dict[Stage | str, StageHandler] = dict.fromkeys(LEGACY_HANDLERS, _noop)
    started = await _start_engine(db, cfg, "node-6f1c2a3b-1", "linux-box", stop, overrides)
    return started, stop


async def _stop(started: EngineStart, stop: asyncio.Event) -> None:
    stop.set()
    for t in started.tasks:
        t.cancel()
    for t in started.tasks:
        with contextlib.suppress(asyncio.CancelledError, Exception):
            await t


@pytest.mark.asyncio
async def test_a_node_never_writes_the_registry_and_claims_no_home_stage(
    db: Database, tmp_path: Path
) -> None:
    await sync_graph(db, GRAPH)
    # the home's registry differs from this node's code: a node must leave it alone
    await db.execute("UPDATE job_stages SET label = 'home label' WHERE stage = 'tag'")
    await db.execute("DELETE FROM extensions")
    share = tmp_path / "share"
    share.mkdir()
    lib = await make_library(db)
    for i in range(2):
        (share / f"v{i}.mp4").write_bytes(b"x" * (100 + i))
        await make_video(db, lib, path=f"/lib/v{i}.mp4", size=100 + i)
    node_id = await _node(db)
    cfg = RuntimeConfig(
        database_url="x", stages=(), role="node", node_id=node_id, path_map=f"/lib={share}"
    )
    started, stop = await _boot(db, cfg)
    try:
        assert await db.fetchval("SELECT label FROM job_stages WHERE stage = 'tag'") == "home label"
        assert await db.fetchval("SELECT count(*) FROM extensions") == 0
        served = set(started.served)
        assert not served & {"index", "thumb_small", "review_pages"}
        assert started.selector.stages_off.get("thumb_small") in {"home_only", "unavailable"}
        assert started.selector.stages_off["review_pages"] == "home_only"
        assert "probe" in served
        w = await db.fetchrow("SELECT * FROM workers WHERE id = 'node-6f1c2a3b-1'")
        assert w["role"] == "node" and str(w["node_id"]) == node_id
        assert loads(w["capabilities"])["cpu_count"] >= 1
        assert loads(w["stages_off"])["review_pages"] == "home_only"
        row = await _row(db, node_id)
        storage = loads(row["storage"])
        assert storage["read"] == "ok" and storage["write"] == "ok"
        assert loads(row["capabilities"])["pipeline_version"]
        # --path-map given: nothing inferred, nothing stored
        assert row["path_map"] is None
    finally:
        await _stop(started, stop)


@pytest.mark.asyncio
async def test_a_home_worker_still_syncs_and_serves_home_stages(db: Database) -> None:
    await sync_graph(db, GRAPH)
    await db.execute("UPDATE job_stages SET label = 'stale' WHERE stage = 'tag'")
    started, stop = await _boot(db, RuntimeConfig(database_url="x", stages=()))
    try:
        assert await db.fetchval("SELECT label FROM job_stages WHERE stage = 'tag'") != "stale"
        assert "review_pages" in started.served
        w = await db.fetchrow("SELECT role, node_id FROM workers WHERE id = 'node-6f1c2a3b-1'")
        assert w["role"] == "home" and w["node_id"] is None
    finally:
        await _stop(started, stop)


@pytest.mark.asyncio
async def test_the_node_infers_and_stores_its_path_map(db: Database, tmp_path: Path) -> None:
    await sync_graph(db, GRAPH)
    lib = await make_library(db)
    mount = tmp_path / "mnt" / "nas"
    for i in range(3):
        f = mount / "Channel" / f"e{i}.mp4"
        f.parent.mkdir(parents=True, exist_ok=True)
        f.write_bytes(b"y" * (10 + i))
        await make_video(db, lib, path=f"Y:/Channel/e{i}.mp4", size=10 + i)
    node_id = await _node(db)
    env_hint = str(mount)
    mp = pytest.MonkeyPatch()
    mp.setenv("MAKTABA_STORAGE_HINTS", env_hint)
    try:
        started, stop = await _boot(
            db, RuntimeConfig(database_url="x", stages=(), role="node", node_id=node_id)
        )
    finally:
        mp.undo()
    try:
        local = str(mount).replace("\\", "/") + "/"
        row = await _row(db, node_id)
        assert loads(row["path_map"]) == {"Y:/": local}
        assert row["path_map_source"] == "inferred"
        assert started.runtime.path_map.as_json() == {"Y:/": local}
        assert loads(row["storage"])["read"] == "ok"
    finally:
        await _stop(started, stop)


# ------------------------------------------------------------------ while running


async def _agent(db: Database, node_id: str) -> tuple[WorkerAgent, asyncio.Event]:
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
    return agent, stop


async def _worker_stages(db: Database) -> tuple[list[str], dict[str, str]]:
    row = await db.fetchrow("SELECT stages, stages_off FROM workers WHERE id = 'w1'")
    stages = row["stages"]
    return (loads(stages) if isinstance(stages, str) else list(stages)), loads(row["stages_off"])


@pytest.mark.asyncio
async def test_a_switch_turned_off_leaves_the_set_at_the_next_refresh(db: Database) -> None:
    await sync_graph(db, GRAPH)
    node_id = await _node(db)
    agent, _ = await _agent(db, node_id)
    assert "probe" in agent.selector.current()
    await db.execute(
        "INSERT INTO cluster_node_stages (node_id, stage, enabled) VALUES ($1,'probe',$2)",
        node_id,
        False,
    )
    assert await agent.refresh() is True
    assert "probe" not in agent.selector.current()
    stages, off = await _worker_stages(db)
    assert "probe" not in stages and off["probe"] == "switched_off"
    # switched back on (row removed: automatic)
    await db.execute("DELETE FROM cluster_node_stages WHERE node_id = $1", node_id)
    await agent.refresh()
    assert "probe" in agent.selector.current()


@pytest.mark.asyncio
async def test_drain_at_the_heartbeat_claims_nothing_new(db: Database) -> None:
    await sync_graph(db, GRAPH)
    node_id = await _node(db)
    agent, stop = await _agent(db, node_id)
    await db.execute("UPDATE cluster_nodes SET drain = $2 WHERE id = $1", node_id, True)
    await agent.beat()
    assert agent.selector.current() == () and not stop.is_set()
    stages, off = await _worker_stages(db)
    assert stages == [] and off["probe"] == "draining"
    await db.execute("UPDATE cluster_nodes SET drain = $2 WHERE id = $1", node_id, False)
    await agent.beat()
    assert "probe" in agent.selector.current()


@pytest.mark.asyncio
async def test_revoked_at_the_heartbeat_stops_the_worker(db: Database) -> None:
    node_id = await _node(db)
    agent, stop = await _agent(db, node_id)
    await agent.beat()
    assert not stop.is_set()
    assert exitcodes.exit_code(0) == 0
    await db.execute("UPDATE cluster_nodes SET state = 'revoked' WHERE id = $1", node_id)
    await agent.beat()
    assert stop.is_set()
    assert exitcodes.exit_code(0) == 77  # refused for good: the service stops restarting


@pytest.mark.asyncio
async def test_a_removed_node_or_refused_login_stops_too(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    node_id = await _node(db)
    agent, stop = await _agent(db, node_id)
    await db.execute("DELETE FROM cluster_nodes WHERE id = $1", node_id)
    await agent.beat()
    assert stop.is_set()

    class InvalidPasswordError(Exception):
        pass

    async def refused(db: Any, node_id: str) -> None:
        raise InvalidPasswordError("password authentication failed")

    agent2, stop2 = await _agent(db, await _node(db))
    monkeypatch.setattr(nodes_db, "beat", refused)
    await agent2.beat()
    assert stop2.is_set()


@pytest.mark.asyncio
async def test_an_admin_map_set_while_running_is_followed(db: Database) -> None:
    node_id = await _node(db)
    agent, _ = await _agent(db, node_id)
    agent.explicit_path_map = False
    pm = json.dumps({"Y:/": "/new/"})
    await db.execute(
        "UPDATE cluster_nodes SET path_map = $2, path_map_source = 'admin' WHERE id = $1",
        node_id,
        pm,
    )
    await agent.beat()
    assert agent.path_map.as_json() == {"Y:/": "/new/"}
