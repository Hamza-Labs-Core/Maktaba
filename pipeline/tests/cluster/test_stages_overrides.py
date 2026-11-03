"""A machine's component override written through its stage switches (Epic 51, story
51.12, T07): the API writes ``cluster_node_stages`` rows (``enabled = false``) for every
stage of a component turned off or held on one machine, so a machine with no component
runner (a release before Epic 51) stops claiming those stages through ``switched_off``."""

from __future__ import annotations

import pytest

from maktaba_pipeline.cluster import stages as cluster_stages
from maktaba_pipeline.cluster.stages import REASON_SWITCHED_OFF
from maktaba_pipeline.components.catalog import default
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.runtime import Database
from tests.cluster.test_node_runtime import _agent, _node, _worker_stages


@pytest.fixture(autouse=True)
def _every_stage_can_run(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(cluster_stages, "_cannot_run", lambda _st, _h: None)


@pytest.mark.asyncio
async def test_t07_an_override_reaches_a_machine_without_a_component_runner(db: Database) -> None:
    await sync_graph(db, GRAPH)
    node_id = await _node(db)
    agent, _ = await _agent(db, node_id)
    c = default().get("transcription")
    assert c is not None and set(c.stages) == {"extract", "transcribe"}
    assert {"extract", "transcribe"} <= set(agent.selector.current())
    # the rows PUT /api/components/transcription/machines/{node} {"want":"installed"} writes
    for stage in c.stages:
        await db.execute(
            "INSERT INTO cluster_node_stages (node_id, stage, enabled) VALUES ($1, $2, $3)",
            node_id,
            stage,
            False,
        )
    # no component runner here: no component reason, only the stage switches
    assert agent.selector.components == {}
    assert await agent.refresh() is True
    served = set(agent.selector.current())
    assert not served & {"extract", "transcribe"}
    _, off = await _worker_stages(db)
    assert off["extract"] == REASON_SWITCHED_OFF and off["transcribe"] == REASON_SWITCHED_OFF
    # follow the home again: the rows go and the stages come back
    await db.execute("DELETE FROM cluster_node_stages WHERE node_id = $1", node_id)
    await agent.refresh()
    assert {"extract", "transcribe"} <= set(agent.selector.current())
