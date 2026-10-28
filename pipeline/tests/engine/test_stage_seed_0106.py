"""Slot 0106 seeds ``job_stages`` for the stages it adds, as the graph defines them, so a fresh
database matches the graph before the first worker syncs it."""

from __future__ import annotations

from typing import Any

import pytest

from maktaba_pipeline.engine.graph import GRAPH, POOLS
from maktaba_pipeline.engine.sql import loads
from maktaba_pipeline.runtime import Database


def as_list(v: Any) -> list[str]:
    return sorted(loads(v) if isinstance(v, str) else (v or []))


@pytest.mark.asyncio
@pytest.mark.parametrize("stage", ["exact_dates", "repair_broken", "youtube_sync"])
async def test_the_migrated_row_is_the_graphs_stage(db: Database, stage: str) -> None:
    s = next(s for s in GRAPH if s.name == stage)
    r = await db.fetchrow("SELECT * FROM job_stages WHERE stage = $1", stage)
    assert r is not None
    assert (r["label"], r["pool"], r["lane"], r["lane_order"], r["version"]) == (
        s.label,
        s.pool,
        s.lane,
        s.lane_order,
        s.version,
    )
    # what SQL reads before a worker's graph sync; edges, may_request and entry_for are the
    # sync's (only the pipeline reads them), so the graph may change them without a slot
    assert as_list(r["subject_kinds"]) == sorted(s.subject_kinds)
    assert bool(r["external_write"]) == s.external_write
    assert (r["debounce_sec"], r["debounce_max_sec"], r["max_attempts"]) == (
        s.debounce_sec,
        s.debounce_max_sec,
        s.max_attempts,
    )


@pytest.mark.asyncio
async def test_the_pools_exist_before_any_worker_runs(db: Database) -> None:
    have = {r["pool"] for r in await db.fetch("SELECT pool FROM worker_pools")}
    assert {"audits", "repairs"} <= have
    assert {"audits", "repairs"} <= {p.pool for p in POOLS}
