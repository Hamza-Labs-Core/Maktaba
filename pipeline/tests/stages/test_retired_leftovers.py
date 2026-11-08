"""The rows a release removed from the code stay, and nothing trips over them.

Slot 0215 drops no data (a release's migrations lose none): the Plex sync stages' and the
dizqueTV extension's ``job_stages``, ``processing_jobs``, ``worker_pools``, ``extensions``
and component rows remain until a later cleanup migration. Here a home updated with them
present: a ``plex_groups`` stage row and its pending job, the ``network_tv`` pool, the
``dizquetv`` extension row and the ``ext.dizquetv`` component rows. The graph sync, the
gates, the claims, the extensions and the components all go on; the old job is never
claimed, run or failed, and no "stage without a worker" names it.
"""

from __future__ import annotations

from dataclasses import replace
from typing import Any

import pytest

from maktaba_pipeline import extensions
from maktaba_pipeline.components import catalog as comp_catalog
from maktaba_pipeline.components import stages as comp_stages
from maktaba_pipeline.components import store as comp_store
from maktaba_pipeline.engine.claim import load_pools
from maktaba_pipeline.engine.dispatch import EngineRuntime
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import PoolDef, StageDef
from maktaba_pipeline.engine.loader import build_full_graph
from maktaba_pipeline.engine.retired import (
    RETIRED_COMPONENTS,
    RETIRED_EXTENSIONS,
    RETIRED_POOLS,
    RETIRED_STAGES,
)
from maktaba_pipeline.engine.sql import dumps, now, to_ts
from maktaba_pipeline.engine.sync import _ensure_pools, apply_gates, stages_ahead, sync_graph
from maktaba_pipeline.engine.workers import stages_without_worker
from maktaba_pipeline.extensions import store as ext_store
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.sharepath import auto
from tests.engine.factory import make_library
from tests.stages.world import drain_core, models_off

OLD = "plex_groups"


def _full() -> tuple[StageDef, ...]:
    """The core graph with the built-in extensions' stages (plex_livetv among them)."""
    return tuple(build_full_graph(extensions.discover()))


async def _leftovers(db: Database) -> str:
    """What the release before left: returns the old job's library id."""
    ts = to_ts(db, now())
    livetv = next(s for s in _full() if s.name == "plex_livetv")
    # the old release's stage row, as its graph sync wrote it (Plex was set up: enabled)
    old = replace(livetv, name=OLD, label="Plex groups", subject_kinds=("library",), extension=None)
    await sync_graph(db, (old,))
    await _ensure_pools(db, (PoolDef("network_tv", "dizqueTV", "global", 1, 1),))
    lib = await make_library(db, "قناة")
    res = await enqueue_job(db, subject_kind="library", subject_id=lib, stage=OLD)
    assert res.job_id is not None
    await db.execute(
        "INSERT INTO extensions (name, enabled, settings, updated_at) VALUES ($1, $2, $3, $4)",
        "dizquetv",
        True,
        dumps({"url": "http://dizque:8000"}),
        ts,
    )
    await db.execute(
        "INSERT INTO components (id, want, generation, updated_by, updated_at)"
        " VALUES ($1, $2, $3, $4, $5)",
        "ext.dizquetv",
        "enabled",
        1,
        "seed",
        ts,
    )
    await db.execute(
        "INSERT INTO component_status (machine, id, part, phase, since, updated_at)"
        " VALUES ($1, $2, $3, $4, $5, $5)",
        "home",
        "ext.dizquetv",
        "worker",
        "failed",
        ts,
    )
    return str(lib)


async def _old_job(db: Database) -> Any:
    return await db.fetchrow(
        "SELECT state, attempts, claimed_by FROM processing_jobs WHERE stage = $1", OLD
    )


def test_the_retired_names_are_no_longer_in_the_code() -> None:
    assert not RETIRED_STAGES & {s.name for s in _full()}
    assert not RETIRED_EXTENSIONS & {e.manifest.name for e in extensions.discover()}
    assert not RETIRED_COMPONENTS & set(comp_catalog.default().order())
    used = {s.pool for s in _full()}
    assert not RETIRED_POOLS & used


@pytest.mark.asyncio
async def test_the_update_goes_on_with_the_old_rows_present(db: Database) -> None:
    await _leftovers(db)

    # the new release's graph sync and gates: the old row is left as it was
    full = _full()
    await sync_graph(db, full)
    await models_off(db)
    await apply_gates(db, full, {})
    assert OLD not in await stages_ahead(db, full)
    row = await db.fetchrow("SELECT enabled, pool FROM job_stages WHERE stage = $1", OLD)
    assert row is not None and row["pool"] == "network_plex"
    assert "network_tv" in await load_pools(db)

    # a worker drains every queue: the old job is never claimed, run or failed
    ran = await drain_core(db, EngineRuntime(graph={s.name: s for s in full}))
    assert OLD not in ran
    job = await _old_job(db)
    assert (job["state"], job["attempts"], job["claimed_by"]) == ("pending", 0, None)

    # nothing reports it as work waiting for a worker
    assert OLD not in await stages_without_worker(db)

    # pausing the storage pools for a share move leaves it alone (and resumes only its own)
    held = await auto.pause_path_stages(db, {"network_plex"})
    assert OLD not in held
    assert "plex_livetv" in held
    await auto.release_stages(db)


@pytest.mark.asyncio
async def test_extensions_and_components_ignore_the_removed_ones(db: Database) -> None:
    await _leftovers(db)
    found = extensions.discover()
    await ext_store.ensure_rows(db, found)
    states = {e.manifest.name: await ext_store.load_state(db, e) for e in found}
    assert "dizquetv" not in states and "plex" in states
    # the old row stays as it was
    row = await db.fetchrow("SELECT enabled FROM extensions WHERE name = 'dizquetv'")
    assert row is not None and bool(row["enabled"])

    desired, _ = await comp_store.desired(db)
    assert "ext.dizquetv" in desired  # the row is still there ...
    gates = comp_stages.gates(desired)
    assert "ext.dizquetv" not in gates  # ... and no gate comes of it
    statuses = await comp_store.statuses(db)
    assert any(s.id == "ext.dizquetv" for s in statuses)


def test_the_go_side_retires_the_same_names() -> None:
    """``shared/components/retired.go`` (the API's readers) names what this side does."""
    import re
    from pathlib import Path

    go = (Path(__file__).parents[3] / "shared" / "components" / "retired.go").read_text()

    def names(var: str) -> frozenset[str]:
        m = re.search(var + r" = \[\]string\{(.*?)\}", go, re.S)
        assert m is not None, var
        return frozenset(re.findall(r'"([^"]+)"', m.group(1)))

    assert names("RetiredStages") == RETIRED_STAGES
    assert names("RetiredPools") == RETIRED_POOLS
    assert names("RetiredExtensions") == RETIRED_EXTENSIONS
    assert names("RetiredComponents") == RETIRED_COMPONENTS
