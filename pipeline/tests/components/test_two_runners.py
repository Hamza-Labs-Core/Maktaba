"""Two pipeline component runners on one Postgres, the home's and a machine's (Epic 51,
story 51.12, T08): an override that turns transcription off on the machine stops the
machine claiming its stages within 2 s (LISTEN components_changed), while the home keeps
claiming them."""

from __future__ import annotations

import asyncio
import contextlib
import time
import uuid
from typing import Any

import pytest

from maktaba_pipeline.cluster import stages as cluster_stages
from maktaba_pipeline.cluster.stages import StageSelector
from maktaba_pipeline.components.runner import ComponentRunner
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.sql import is_pg
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.runtime import Database
from tests.components._helpers import set_want

STAGES = ("extract", "transcribe")


@pytest.fixture(autouse=True)
def _every_stage_can_run(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(cluster_stages, "_cannot_run", lambda _st, _h: None)


def _runner(db: Any, **kw: Any) -> tuple[ComponentRunner, StageSelector]:
    sel = StageSelector(graph=GRAPH, role=kw.get("role", "home"))
    sel.compute()
    # the 30 s poll is the fallback: only the notification can make it within 2 s
    r = ComponentRunner(db, graph=GRAPH, selector=sel, reconcile=True, poll_s=30.0, **kw)
    return r, sel


async def _until(cond: Any, timeout: float) -> float:
    start = time.monotonic()
    while not cond():
        if time.monotonic() - start > timeout:
            raise AssertionError(f"not within {timeout} s")
        await asyncio.sleep(0.02)
    return time.monotonic() - start


@pytest.mark.asyncio
async def test_t08_an_override_off_stops_the_machine_and_not_the_home(db: Database) -> None:
    if not is_pg(db):
        pytest.skip("NOTIFY is Postgres's; SQLite homes take no machines")
    await sync_graph(db, GRAPH, gates={})
    await set_want(db, "transcription", "enabled")
    node_id = str(uuid.uuid4())
    role = await db.fetchval("SELECT session_user")  # the guard lets the tests' login act for it
    await db.execute(
        "INSERT INTO cluster_nodes (id, name, state, node_pubkey, db_role)"
        " VALUES ($1, 'n', 'approved', 'pub', $2)",
        node_id,
        role,
    )
    home, home_sel = _runner(db, worker_id="w-home")
    node, node_sel = _runner(db, worker_id="w-node", machine=node_id, role="node", part="pipeline")
    stop = asyncio.Event()
    tasks = [asyncio.create_task(home.run(stop)), asyncio.create_task(node.run(stop))]
    try:
        await _until(
            lambda: home.revision >= 0 and node.revision >= 0 and node._listener is not None, 5.0
        )
        assert set(STAGES) <= set(node_sel.served) and set(STAGES) <= set(home_sel.served)
        # what PUT /api/components/transcription/machines/{node} writes
        await db.execute(
            "INSERT INTO component_overrides (node_id, id, want, updated_by)"
            " VALUES ($1, 'transcription', 'installed', 'maya')",
            node_id,
        )
        took = await _until(lambda: not set(STAGES) & set(node_sel.served), 2.0)
        assert took <= 2.0
        assert node_sel.stages_off["transcribe"] == "component_off:transcription"
        await asyncio.sleep(0.2)  # the home saw the same notification
        assert set(STAGES) <= set(home_sel.served)
        enabled = await db.fetchval("SELECT enabled FROM job_stages WHERE stage = 'transcribe'")
        assert enabled  # the home's stage rows are the home's: still claimed there
    finally:
        stop.set()
        for t in tasks:
            with contextlib.suppress(Exception):
                await asyncio.wait_for(t, 10)
