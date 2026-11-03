"""The built-in extensions are components (Epic 51, story 51.07): the stage reasons'
order (T05), dizqueTV waiting on livetv and catching up when it is back (T06, T07), an
enabled extension without its settings (T08), and an entry-point extension left as it is
(T10)."""

from __future__ import annotations

import dataclasses
from typing import Any

import pytest

from maktaba_pipeline.cluster import stages as cluster_stages
from maktaba_pipeline.cluster.stages import StageSelector
from maktaba_pipeline.components import store
from maktaba_pipeline.components.catalog import default
from maktaba_pipeline.components.runner import ComponentRunner
from maktaba_pipeline.components.stages import (
    extension_name,
    gates,
    machine_reasons,
    stage_components,
)
from maktaba_pipeline.components.store import Desired
from maktaba_pipeline.engine.loader import build_full_graph
from maktaba_pipeline.engine.sql import dumps, is_pg, loads
from maktaba_pipeline.engine.sync import stage_enabled_state, sync_graph
from maktaba_pipeline.extensions import discover
from maktaba_pipeline.extensions.store import ExtensionState, ensure_rows, load_state
from maktaba_pipeline.runtime import Database
from tests.components._helpers import set_want, stage_row

EXTENSIONS = discover(include_entry_points=False)
GRAPH = build_full_graph(EXTENSIONS)
BY_NAME = {s.name: s for s in GRAPH}
DIZQUETV_STAGES = ("dizquetv_show", "dizquetv_channels", "dizquetv_audit")


@pytest.fixture(autouse=True)
def _no_env_settings(monkeypatch: pytest.MonkeyPatch) -> None:
    for var in ("MAKTABA_DIZQUETV_URL", "MAKTABA_PLEX_URL", "MAKTABA_PLEX_TOKEN"):
        monkeypatch.delenv(var, raising=False)
    monkeypatch.setattr(cluster_stages, "_cannot_run", lambda _st, _h: None)


def _state(enabled: bool, configured: bool) -> ExtensionState:
    return ExtensionState("dizquetv", enabled, {}, configured)


# -- T05 ---------------------------------------------------------------------------------


@pytest.mark.parametrize(
    ("row", "gate", "want"),
    [
        (_state(False, True), "component_held", (False, "extension_disabled")),
        (_state(True, False), "component_off", (False, "not_configured")),
        (_state(True, True), "component_held", (False, "component_held")),
        (_state(True, True), "component_off", (False, "component_off")),
        (_state(True, True), None, (True, None)),
    ],
)
def test_t05_the_extensions_own_reasons_come_first(
    row: ExtensionState, gate: str | None, want: tuple[bool, str | None]
) -> None:
    st = BY_NAME["dizquetv_show"]
    assert stage_enabled_state(st, {"dizquetv": row}, gate) == want


def test_the_built_in_extensions_are_stage_groups() -> None:
    ids = {c.id for c in stage_components()}
    assert {"ext.plex", "ext.dizquetv"} <= ids
    c = default().get("ext.plex")
    assert c is not None and extension_name(c) == "plex"
    off = gates({"livetv": Desired(id="livetv", want="installed", generation=1)})
    assert off["ext.dizquetv"].reason == "dependency_off"
    assert off["ext.dizquetv"].params == {"component": "livetv"}
    assert machine_reasons(off)["dizquetv_show"] == "component_off:ext.dizquetv"


# -- T10 ---------------------------------------------------------------------------------


def test_t10_an_entry_point_extension_is_not_a_component() -> None:
    other = dataclasses.replace(BY_NAME["dizquetv_show"], name="acme_sync", extension="acme")
    assert default().by_stage("acme_sync") is None
    everything_off = {
        c.id: Desired(id=c.id, want="installed", generation=1) for c in default().components()
    }
    assert "acme_sync" not in machine_reasons(gates(everything_off))
    # its extensions row stays its only switch
    acme = {"acme": ExtensionState("acme", False, {}, True)}
    assert stage_enabled_state(other, acme, None) == (False, "extension_disabled")
    acme = {"acme": ExtensionState("acme", True, {}, True)}
    assert stage_enabled_state(other, acme, None) == (True, None)


# -- T06-T08: the runner ----------------------------------------------------------------------


async def _extension(db: Database, name: str, *, enabled: bool, settings: dict[str, str]) -> None:
    await db.execute(
        "UPDATE extensions SET enabled = $2, settings = $3 WHERE name = $1",
        name,
        enabled if is_pg(db) else int(enabled),
        dumps(settings),
    )


async def _states(db: Database) -> dict[str, Any]:
    return {e.manifest.name: await load_state(db, e) for e in EXTENSIONS}


async def _runner(db: Database) -> ComponentRunner:
    await ensure_rows(db, EXTENSIONS)
    sel = StageSelector(graph=GRAPH)
    sel.compute()

    async def states() -> dict[str, Any]:
        return await _states(db)

    r = ComponentRunner(
        db,
        graph=GRAPH,
        selector=sel,
        worker_id="w-test",
        reconcile=True,
        extension_states=states,
        drain_poll_s=0.02,
    )
    return r


async def _pass(r: ComponentRunner) -> None:
    desired, rev = await store.desired(r.db)
    await r.apply(desired, rev)
    await r.wait_drains()
    await r._report()


async def _status(db: Database, cid: str) -> store.Status:
    rows = [s for s in await store.statuses(db, cid) if s.part == "pipeline"]
    assert len(rows) == 1, rows
    return rows[0]


@pytest.mark.asyncio
async def test_t06_t07_dizquetv_waits_on_livetv_and_catches_up(db: Database) -> None:
    r = await _runner(db)
    await _extension(db, "dizquetv", enabled=True, settings={"url": "http://dizquetv:8000"})
    await sync_graph(db, GRAPH, await _states(db), gates={})
    await set_want(db, "ext.dizquetv", "enabled")
    await set_want(db, "livetv", "installed")
    await _pass(r)
    st = await _status(db, "ext.dizquetv")
    assert st.phase == "waiting"
    assert st.reason == "dependency_off"
    assert st.conditions[0].params == {"component": "livetv"}
    for stage in DIZQUETV_STAGES:
        assert await stage_row(db, stage) == (False, "component_off")
    # its desired state is the admin's, untouched
    desired, _ = await store.desired(db)
    assert desired["ext.dizquetv"].want == "enabled"

    await set_want(db, "livetv", "enabled")
    await _pass(r)
    for stage in DIZQUETV_STAGES:
        assert await stage_row(db, stage) == (True, None)
    assert (await _status(db, "ext.dizquetv")).phase == "running"
    jobs = await db.fetch("SELECT payload FROM processing_jobs WHERE stage = 'component_catchup'")
    assert len(jobs) == 1
    entries = {e["component"] for e in loads(jobs[0]["payload"])["components"]}
    assert "ext.dizquetv" in entries
    await r.close()


@pytest.mark.asyncio
async def test_t08_an_enabled_extension_without_its_settings_waits(db: Database) -> None:
    r = await _runner(db)
    await _extension(db, "plex", enabled=True, settings={})
    await sync_graph(db, GRAPH, await _states(db), gates={})
    await set_want(db, "ext.plex", "enabled")
    await _pass(r)
    st = await _status(db, "ext.plex")
    assert (st.phase, st.reason) == ("waiting", "not_configured")
    # the stage rows are as they were before components
    assert await stage_row(db, "plex_index") == (False, "not_configured")
    await r.close()


@pytest.mark.asyncio
async def test_an_extension_switched_off_reads_off_with_todays_rows(db: Database) -> None:
    r = await _runner(db)
    await _extension(db, "plex", enabled=False, settings={})
    await sync_graph(db, GRAPH, await _states(db), gates={})
    await set_want(db, "ext.plex", "installed")
    await _pass(r)
    assert (await _status(db, "ext.plex")).phase == "off"
    assert await stage_row(db, "plex_index") == (False, "extension_disabled")
    await r.close()
