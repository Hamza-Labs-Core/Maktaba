"""The schema guard of a joined machine (Epic 31 design section 5.1): a node whose code
needs migrations the home's database does not have pauses and resumes by itself."""

from __future__ import annotations

import asyncio
import contextlib
import os
import re
import uuid
from typing import Any

import pytest

from maktaba_pipeline import runtime as runtime_mod
from maktaba_pipeline.cluster.schema import (
    MIN_NODE_FUNCTION,
    SCHEMA_REV,
    HomeSchema,
    highest_slot,
    pause_reason,
    read_home_schema,
)
from maktaba_pipeline.cluster.stages import select_stages
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.sql import loads
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.pipeline.reaper import Reaper
from maktaba_pipeline.runtime import Database, RuntimeConfig, _while_unpaused
from tests.cluster.test_node_runtime import _agent, _boot, _node, _row, _stop, _worker_stages
from tests.engine.migrate import MIGRATIONS_DIR

BEHIND = "paused: home database at 0119, this machine needs 0124 — update the home"


# ------------------------------------------------------------------ the rule


def test_a_home_behind_this_code_pauses() -> None:
    assert pause_reason(HomeSchema(applied=119), 124) == BEHIND


def test_a_home_at_or_ahead_of_this_code_runs() -> None:
    assert pause_reason(HomeSchema(applied=124), 124) is None
    # newer migrations are additive: the per-stage version check is enough
    assert pause_reason(HomeSchema(applied=130), 124) is None
    assert pause_reason(HomeSchema(applied=130, min_node=124), 124) is None


def test_a_breaking_migration_on_the_home_pauses_older_code() -> None:
    got = pause_reason(HomeSchema(applied=130, min_node=127), 124)
    assert got == (
        "paused: home database at 0130 needs this machine at 0127 or newer (it has 0124)"
        " — update this machine"
    )


def test_an_unreadable_home_schema_is_not_judged() -> None:
    assert pause_reason(HomeSchema(applied=None), 124) is None


def test_schema_rev_is_the_highest_migration_this_code_ships() -> None:
    names = [p.name for p in MIGRATIONS_DIR.glob("*.sql")]
    assert highest_slot(names) == SCHEMA_REV, (
        "a migration was added: raise SCHEMA_REV in pipeline/src/maktaba_pipeline/cluster/"
        "schema.py and SchemaRev in streaming/internal/cluster/schema.go"
    )
    manifest = (MIGRATIONS_DIR / "MANIFEST.md").read_text(encoding="utf-8")
    slots = [int(m) for m in re.findall(r"^\| `(\d{4})` \|", manifest, re.MULTILINE)]
    assert max(slots) == SCHEMA_REV


def test_a_breaking_migration_is_marked_in_the_manifest() -> None:
    """A migration that (re)defines ``maktaba_min_node_schema()`` says so in its MANIFEST
    row (``Breaking for older nodes``), and a row that says so ships the function."""
    manifest = (MIGRATIONS_DIR / "MANIFEST.md").read_text(encoding="utf-8")
    marked = {
        int(m.group(1))
        for m in re.finditer(r"^\| `(\d{4})` \|.*Breaking for older nodes", manifest, re.M)
    }
    defining = {
        int(p.name[:4])
        for p in MIGRATIONS_DIR.glob("[0-9][0-9][0-9][0-9]_*.sql")
        if not p.name.endswith(".sqlite.sql")
        and MIN_NODE_FUNCTION in p.read_text(encoding="utf-8").split("-- +goose Down")[0]
    }
    assert marked == defining


def test_a_paused_selection_keeps_every_stage_off_with_the_reason() -> None:
    served, off = select_stages(
        GRAPH, requested={"probe", "tag"}, handlers={"probe"}, role="node", paused=BEHIND
    )
    assert served == []
    assert off["probe"] == BEHIND and off["tag"] == BEHIND
    # not asked for stays "not_selected"
    assert off["scan"] == "not_selected"


# ------------------------------------------------------------------ the agent


def _reader(state: dict[str, Any]) -> Any:
    async def read(_db: Any) -> HomeSchema:
        state["reads"] = state.get("reads", 0) + 1
        return HomeSchema(applied=state["applied"])

    return read


@pytest.mark.asyncio
async def test_a_paused_node_starts_without_probing_storage_and_resumes(db: Database) -> None:
    await sync_graph(db, GRAPH)
    node_id = await _node(db)
    agent, _ = await _agent(db, node_id)
    agent.required_schema = 124
    home = {"applied": 119}
    agent.schema_reader = _reader(home)

    await agent.start_node({"cpu_count": 2})
    assert agent.paused
    await agent.apply()
    served, off = await _worker_stages(db)
    assert served == [] and off["probe"] == BEHIND
    # nothing of the start that writes ran: no storage probe, no report
    row = await _row(db, node_id)
    assert not loads(row["storage"]) and not loads(row["capabilities"])

    # still behind: every refresh asks again, nothing changes
    await agent.refresh()
    assert agent.paused and home["reads"] == 2

    home["applied"] = 124
    await agent.refresh()
    assert not agent.paused
    served, off = await _worker_stages(db)
    assert "probe" in served and "probe" not in off
    row = await _row(db, node_id)
    assert loads(row["capabilities"]) == {"cpu_count": 2} and loads(row["storage"])["read"]


@pytest.mark.asyncio
async def test_a_running_node_pauses_when_the_home_goes_back(db: Database) -> None:
    await sync_graph(db, GRAPH)
    node_id = await _node(db)
    agent, _ = await _agent(db, node_id)
    agent.required_schema = 124
    home = {"applied": 124}
    agent.schema_reader = _reader(home)
    await agent.start_node({})
    await agent.apply()
    assert not agent.paused and "probe" in agent.selector.served
    # checked again with the storage check (every refresh once it is due)
    home["applied"] = 119
    agent.storage_check_sec = 0.0
    await agent.refresh()
    assert agent.paused and agent.selector.served == ()


@pytest.mark.asyncio
async def test_a_paused_node_boots_claiming_nothing_and_no_topup(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    await sync_graph(db, GRAPH)
    node_id = await _node(db)
    topups: list[int] = []

    async def fake_topup(_db: Any, stop: asyncio.Event, **_: Any) -> None:
        topups.append(1)
        await stop.wait()

    async def behind(_db: Any) -> HomeSchema:
        return HomeSchema(applied=SCHEMA_REV - 5)

    monkeypatch.setattr(runtime_mod, "run_topup_loop", fake_topup)
    monkeypatch.setattr("maktaba_pipeline.cluster.node.read_home_schema", behind)
    cfg = RuntimeConfig(
        database_url="x", stages=(), role="node", node_id=node_id, path_map="/lib=/nowhere"
    )
    started, stop = await _boot(db, cfg)
    try:
        assert started.served == ()
        w = await db.fetchrow("SELECT stages_off FROM workers WHERE id = 'node-6f1c2a3b-1'")
        reasons = set(loads(w["stages_off"]).values())
        assert reasons == {pause_reason(HomeSchema(applied=SCHEMA_REV - 5))}
        await asyncio.sleep(0.2)
        assert topups == []
    finally:
        await _stop(started, stop)


@pytest.mark.asyncio
async def test_a_home_worker_is_never_paused(db: Database, monkeypatch: pytest.MonkeyPatch) -> None:
    await sync_graph(db, GRAPH)

    async def behind(_db: Any) -> HomeSchema:
        return HomeSchema(applied=1)

    monkeypatch.setattr("maktaba_pipeline.cluster.node.read_home_schema", behind)
    started, stop = await _boot(db, RuntimeConfig(database_url="x", stages=()))
    try:
        assert not started.agent.paused and "probe" in started.served
    finally:
        await _stop(started, stop)


# ------------------------------------------------------------------ the writers


class _Pausable:
    def __init__(self) -> None:
        self.paused = False


@pytest.mark.asyncio
async def test_writers_stop_on_a_pause_and_start_again_on_resume() -> None:
    agent = _Pausable()
    shutdown = asyncio.Event()
    starts: list[int] = []
    running = asyncio.Event()

    async def loop(stop: asyncio.Event) -> None:
        starts.append(1)
        running.set()
        await stop.wait()
        running.clear()

    task = asyncio.create_task(
        _while_unpaused(agent, shutdown, loop, "test", poll_sec=0.01)  # type: ignore[arg-type]
    )
    try:
        await asyncio.wait_for(running.wait(), 1)
        agent.paused = True
        for _ in range(100):
            if not running.is_set():
                break
            await asyncio.sleep(0.01)
        assert not running.is_set() and len(starts) == 1
        await asyncio.sleep(0.05)
        assert len(starts) == 1  # nothing runs while paused
        agent.paused = False
        await asyncio.wait_for(running.wait(), 1)
        assert len(starts) == 2
    finally:
        shutdown.set()
        await asyncio.wait_for(task, 2)


@pytest.mark.asyncio
async def test_the_reaper_writes_nothing_while_paused() -> None:
    class NoDB:
        dialect = "postgres"

        async def fetchrow(self, *_: Any) -> Any:
            raise AssertionError("a paused reaper touched the database")

    reaper = Reaper(NoDB(), paused=lambda: True)
    assert await reaper.tick() == 0


# ------------------------------------------------------------------ the real role

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")

# what goose creates before it applies the first migration
GOOSE_TABLE = """
CREATE TABLE goose_db_version (
    id serial PRIMARY KEY,
    version_id bigint NOT NULL,
    is_applied boolean NOT NULL,
    tstamp timestamp DEFAULT now()
)"""


def _swap(url: str, *, db: str | None = None, user: str | None = None) -> str:
    base, _, name = url.rpartition("/")
    if user is not None:
        scheme, _, rest = base.partition("://")
        host = rest.rpartition("@")[2]
        base = f"{scheme}://{user}@{host}"
    return f"{base}/{db or name}"


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_machine_role_reads_the_home_schema() -> None:
    """A node's login (a member of ``maktaba_worker``) reads goose's table on a home
    migrated by goose (slot 0103 granted it), and the breaking declaration."""
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    import asyncpg  # type: ignore[import-untyped]

    from tests.engine.migrate import apply_postgres

    name = "maktaba_schema_" + uuid.uuid4().hex[:8]
    role = "maktaba_node_" + uuid.uuid4().hex[:8]
    admin = await asyncpg.connect(PG_URL)
    await admin.execute(f'CREATE DATABASE "{name}"')
    try:
        conn = await asyncpg.connect(_swap(PG_URL, db=name))
        try:
            await conn.execute(GOOSE_TABLE)
            await apply_postgres(conn, upto=119)
            await conn.executemany(
                "INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)",
                [(v,) for v in range(0, 120)],
            )
            # a down that was undone does not count
            await conn.execute(
                "INSERT INTO goose_db_version (version_id, is_applied) VALUES (200, false)"
            )
            await conn.execute(f"CREATE ROLE {role} LOGIN IN ROLE maktaba_worker")
        finally:
            await conn.close()

        node_db = await Database.connect(_swap(PG_URL, db=name, user=role))
        try:
            assert await node_db.fetchval("SELECT session_user") == role
            home = await read_home_schema(node_db)
            assert home == HomeSchema(applied=119, min_node=None)
            assert pause_reason(home, 124) == BEHIND
        finally:
            await node_db.close()

        conn = await asyncpg.connect(_swap(PG_URL, db=name))
        try:
            await conn.execute(
                f"CREATE FUNCTION {MIN_NODE_FUNCTION}() RETURNS integer"
                " LANGUAGE sql IMMUTABLE AS 'SELECT 127'"
            )
        finally:
            await conn.close()
        node_db = await Database.connect(_swap(PG_URL, db=name, user=role))
        try:
            home = await read_home_schema(node_db)
            assert home.min_node == 127
        finally:
            await node_db.close()
    finally:
        with contextlib.suppress(Exception):
            await admin.execute(f'DROP DATABASE IF EXISTS "{name}" WITH (FORCE)')
        with contextlib.suppress(Exception):
            await admin.execute(f"DROP ROLE IF EXISTS {role}")
        await admin.close()


@pytest.mark.asyncio
async def test_a_paused_node_still_builds_its_path_map(db: Database) -> None:
    """The cluster agent places its files through the active map while the node waits:
    without it the home's /Volumes/... was taken as a local path (a stray Volumes
    folder on a Windows drive)."""
    from maktaba_pipeline.engine.paths import PathMap, active_path_map, set_active_path_map
    from maktaba_pipeline.sharepath.resolver import Resolver

    await sync_graph(db, GRAPH)
    node_id = await _node(db)
    agent, _ = await _agent(db, node_id)
    agent.explicit_path_map = False
    agent.mounts = []
    agent.required_schema = 124
    agent.schema_reader = _reader({"applied": 119})
    try:
        await agent.start_node({})
        assert agent.paused
        assert isinstance(agent.path_map, Resolver) and active_path_map() is agent.path_map
        row = await _row(db, node_id)
        assert not loads(row["storage"])  # still nothing written while paused
    finally:
        set_active_path_map(PathMap())
