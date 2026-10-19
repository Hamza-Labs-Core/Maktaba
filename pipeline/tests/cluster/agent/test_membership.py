"""A close code is a hint; the database is the authority (both DBs, a fake hub).

A hub (or anyone answering as one) closing with 4003 makes the agent check the machine's
membership through the database at once: approved, the worker keeps working; revoked or
removed, the heartbeat path stops it with exit code 77."""

from __future__ import annotations

import asyncio
import contextlib
import random

import pytest

from maktaba_pipeline.cluster import crypto, exitcodes
from maktaba_pipeline.cluster.agent.client import AgentIdentity, ChannelAgent, Commands
from maktaba_pipeline.cluster.agent.logship import LogShipper
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.llm.runtimes import LLMRuntimes
from maktaba_pipeline.llm.service import LocalLLM
from maktaba_pipeline.runtime import Database
from tests.cluster.agent.fakes import FakeHub, running_hub
from tests.cluster.test_node_runtime import _agent, _node

pytestmark = [pytest.mark.integration, pytest.mark.asyncio]


async def _until(cond: object, timeout: float = 5.0) -> None:
    async def wait() -> None:
        while not cond():  # type: ignore[operator]
            await asyncio.sleep(0.01)

    await asyncio.wait_for(wait(), timeout)


async def test_a_4003_asks_the_database(db: Database) -> None:
    await sync_graph(db, GRAPH)
    node_id = await _node(db)
    worker, stop = await _agent(db, node_id)
    checks: list[int] = []

    async def check() -> None:
        checks.append(1)
        await worker.beat()

    key = crypto.generate_keypair()
    hub = FakeHub(node_pub=key.public, refuse="not_member", refuse_code=4003)
    async with running_hub(hub):
        ident = AgentIdentity(
            kind="node",
            url=hub.url,
            worker_id="w1",
            host="h",
            role="node",
            node_id=node_id,
            node_private=key.private,
            home_static=hub.home_static.public,
        )
        agent = ChannelAgent(
            ident,
            shipper=LogShipper(),
            llm=LocalLLM(LLMRuntimes(env={"MAKTABA_OLLAMA_URL": ""})),
            commands=Commands(check_membership=check),
            backoff_min=0.02,
            backoff_max=0.1,
            rng=random.Random(1),
        )
        task = asyncio.create_task(agent.run(stop))
        try:
            # the database says approved: the worker keeps working
            await _until(lambda: len(checks) >= 2)
            assert not stop.is_set() and exitcodes.exit_code(0) == 0 and not task.done()
            # the database says revoked: the heartbeat path stops it, exit 77
            await db.execute("UPDATE cluster_nodes SET state = 'revoked' WHERE id = $1", node_id)
            await _until(stop.is_set)
            assert exitcodes.exit_code(0) == exitcodes.EX_NOPERM
        finally:
            agent.stop()
            with contextlib.suppress(Exception):
                await asyncio.wait_for(task, 3)
