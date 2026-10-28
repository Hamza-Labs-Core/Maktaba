"""Worker start-up: graph sync, served stages, worker registry (notes N2/N3)."""

from __future__ import annotations

import asyncio
import contextlib
from dataclasses import replace
from typing import Any

import pytest

from maktaba_pipeline.db.jobs import DBConn, Job, Stage
from maktaba_pipeline.engine.dispatch import EngineRuntime
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.stages.registry import get_impl
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
    started = await _start_engine(db, cfg, "pc/1", cfg.host or "PC", stop, overrides)
    stop.set()
    for t in started.tasks:
        t.cancel()
    for t in started.tasks:
        with contextlib.suppress(asyncio.CancelledError, Exception):
            await t
    return started.runtime, started.served, started.stage_pools


@pytest.mark.asyncio
async def test_default_worker_serves_every_stage_it_has_a_handler_for(db: Database) -> None:
    rt, stages, pools = await _boot(db, host="PC")
    core_engine = {
        "thumb_hash", "link", "fingerprint", "style_sample", "youtube_sync", "detect",
        "fp_gate", "tag", "rate_style", "plan_channels", "write_channels",
    }  # fmt: skip
    served = set(stages)
    # transcribing needs a speech model this machine may not have (a CI runner has none)
    impl = get_impl("transcribe")
    cannot = {"transcribe"} if impl and impl.unavailable and impl.unavailable() else set()
    assert served >= LEGACY_HANDLERS - cannot
    assert not (served & cannot)
    # legacy stages with no handler anywhere are not claimed (they would fail non-retryably)
    assert "categorize" not in served
    # the transcript analysis stages are engine stages now (this worker has numpy and
    # the search extra, which chapter_infer and topic_recluster need)
    assert {"chapter_infer", "topic_assign", "topic_recluster"} <= served
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


def test_a_stage_this_worker_cannot_run_is_never_claimed(monkeypatch: pytest.MonkeyPatch) -> None:
    from maktaba_pipeline.engine.stages import index, transcribe

    monkeypatch.setattr(transcribe, "unavailable", lambda: "MLX-Whisper needs Apple Silicon")
    monkeypatch.setattr(
        transcribe, "IMPL", replace(transcribe.IMPL, unavailable=transcribe.unavailable)
    )
    monkeypatch.setattr(index, "IMPL", replace(index.IMPL, unavailable=lambda: "no search extra"))
    from maktaba_pipeline.engine.stages import registry

    registry.reset_registry()
    try:
        served = _served_stages(RuntimeConfig(database_url="x", stages=()), GRAPH, set(), {})
        asked = _served_stages(
            RuntimeConfig(database_url="x", stages=("transcribe",)), GRAPH, set(), {}
        )
    finally:
        registry.reset_registry()
    assert "transcribe" not in served and "index" not in served
    assert "subtitle_gen" in served  # needs neither
    assert asked == []  # even when asked for by name


def test_the_transcribe_stage_imports_without_numpy() -> None:
    """A worker without the stt extra imports it to enqueue transcribe jobs."""
    import subprocess
    import sys

    code = (
        "import sys; sys.modules['numpy'] = None; sys.modules['mlx'] = None\n"
        "from maktaba_pipeline.engine.stages import transcribe as t\n"
        "assert t.IMPL.input_fingerprint is not None\n"
        "assert t.settings_from_env().language == 'ar'\n"
    )
    subprocess.run([sys.executable, "-c", code], check=True)


async def _boot_tasks(db: Database, **kw: Any) -> list[str]:
    """The background tasks a worker starts with (their names)."""
    cfg = RuntimeConfig(database_url="x", stages=(), host="PC", **kw)
    stop = asyncio.Event()
    overrides: dict[Stage | str, StageHandler] = dict.fromkeys(LEGACY_HANDLERS, _noop_handler)
    started = await _start_engine(db, cfg, "pc/1", "PC", stop, overrides)
    stop.set()
    names = [t.get_name() for t in started.tasks]
    for t in started.tasks:
        t.cancel()
    for t in started.tasks:
        with contextlib.suppress(asyncio.CancelledError, Exception):
            await t
    return names


@pytest.mark.asyncio
async def test_a_worker_without_the_share_never_checks_it(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The index worker (share=False): no storage check, no path conversion, no share
    probes. A stalled share must not hold its threads, nor get it killed mid-write."""
    from maktaba_pipeline.cluster.node import WorkerAgent
    from maktaba_pipeline.engine import failure_policy

    monkeypatch.setattr(failure_policy, "_share_probes", True)  # restored after the test
    homes: list[bool] = []

    async def start_home(self: WorkerAgent) -> None:
        homes.append(True)

    monkeypatch.setattr(WorkerAgent, "start_home", start_home)
    assert "queue-health" in await _boot_tasks(db)
    assert homes == [True]
    homes.clear()
    names = await _boot_tasks(db, share=False)
    assert homes == [], "a worker without the share must not run the storage check"
    assert "queue-health" not in names
    assert failure_policy._share_probes is False
    assert {"worker-hb", "stage-selection", "reconciler"} <= set(names)
