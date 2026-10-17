"""Worker start-up: graph sync, served stages, worker registry (notes N2/N3)."""

from __future__ import annotations

import asyncio
import contextlib
from typing import Any

import pytest

from maktaba_pipeline.db.jobs import DBConn, Job, Stage
from maktaba_pipeline.engine.dispatch import EngineRuntime
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.workers import live_workers, stages_without_worker
from maktaba_pipeline.runtime import (
    Database,
    RuntimeConfig,
    StageHandler,
    _served_stages,
    _start_engine,
)

LEGACY_HANDLERS = {"scan", "probe", "extract", "transcribe", "subtitle_gen", "index", "thumbnail"}


async def _noop_handler(db: DBConn, job: Job) -> None:
    """Stand-in legacy handler; the booted engine never runs a job here."""


async def _boot(db: Database, **kw: Any) -> tuple[EngineRuntime, tuple[str, ...], dict[str, str]]:
    cfg = RuntimeConfig(database_url="x", stages=(), **kw)
    stop = asyncio.Event()
    overrides: dict[Stage | str, StageHandler] = dict.fromkeys(LEGACY_HANDLERS, _noop_handler)
    rt, stages, pools, tasks = await _start_engine(
        db, cfg, "pc/1", cfg.host or "PC", stop, overrides
    )
    stop.set()
    for t in tasks:
        t.cancel()
    for t in tasks:
        with contextlib.suppress(asyncio.CancelledError, Exception):
            await t
    return rt, stages, pools


@pytest.mark.asyncio
async def test_default_worker_serves_every_stage_it_has_a_handler_for(db: Database) -> None:
    rt, stages, pools = await _boot(db, host="PC")
    core_engine = {
        "thumb_hash", "link", "fingerprint", "style_sample", "youtube_sync", "detect",
        "fp_gate", "tag", "rate_style", "plan_channels", "write_channels",
    }  # fmt: skip
    served = set(stages)
    assert served >= LEGACY_HANDLERS
    # legacy stages with no handler anywhere are not claimed (they would fail non-retryably)
    assert not {"topic_recluster", "topic_assign", "categorize", "chapter_infer"} & served
    # engine stages are served once their implementation exists
    assert (served & core_engine) <= core_engine
    assert pools["probe"] == "nas_read" and pools["transcribe"] == "gpu"
    row = (await live_workers(db))[0]
    assert row["host"] == "PC" and set(row["stages"]) == served
    assert set(rt.graph) >= {s.name for s in GRAPH}


@pytest.mark.asyncio
async def test_the_audio_chain_can_be_left_to_another_host(db: Database) -> None:
    _, stages, _ = await _boot(
        db, host="PC", exclude_stages=("extract", "transcribe", "subtitle_gen", "index")
    )
    assert "probe" in stages and "scan" in stages
    assert not {"extract", "transcribe", "subtitle_gen", "index"} & set(stages)
    # nobody serves transcribe: the registry says so
    assert "transcribe" in await stages_without_worker(db)
    assert "probe" not in await stages_without_worker(db)


def test_explicit_stage_list_and_version_ahead() -> None:
    cfg = RuntimeConfig(database_url="x", stages=("probe", "scan", "detect"))
    served = _served_stages(cfg, GRAPH, {"probe", "scan"}, {"scan": 9})
    assert "probe" in served and "scan" not in served  # db version is ahead of this worker
    assert "extract" not in served  # not requested
