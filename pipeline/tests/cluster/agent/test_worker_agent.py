"""The agent starts and stops with an engine worker (both DBs, a fake hub); the health
numbers ride its metrics frame instead of the database."""

from __future__ import annotations

import asyncio
import contextlib
import json
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline import llm as llm_door
from maktaba_pipeline.cluster import agent as cluster_agent
from maktaba_pipeline.cluster.agent import jobs as job_events
from maktaba_pipeline.db.jobs import DBConn, Job, Stage
from maktaba_pipeline.engine import health
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.sql import loads
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.runtime import (
    Database,
    EngineStart,
    RuntimeConfig,
    StageHandler,
    _start_engine,
)
from tests.cluster.agent.fakes import FakeHub, running_hub

LEGACY = {"scan", "probe", "extract", "transcribe", "subtitle_gen", "index", "thumbnail"}
HOME_KEY = bytes(range(32, 64))


async def _noop(db: DBConn, job: Job) -> None:
    """Never runs."""


async def _boot(db: Database) -> tuple[EngineStart, asyncio.Event]:
    stop = asyncio.Event()
    overrides: dict[Stage | str, StageHandler] = dict.fromkeys(LEGACY, _noop)
    cfg = RuntimeConfig(database_url="", stages=(), role="home")
    started = await _start_engine(db, cfg, "home-w1", "mac-mini", stop, overrides)
    return started, stop


async def _stop(started: EngineStart, stop: asyncio.Event, *, grace: float = 0.0) -> None:
    """As ``runtime.run`` stops: the shutdown event, then the background tasks cancelled."""
    stop.set()
    await asyncio.sleep(grace)
    for t in started.tasks:
        t.cancel()
    for t in started.tasks:
        with contextlib.suppress(asyncio.CancelledError, Exception):
            await asyncio.wait_for(t, 5)


@pytest.fixture
def agent_env(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    monkeypatch.setenv("HOME", str(tmp_path / "home"))  # status files, the local fallback
    monkeypatch.setenv("MAKTABA_CLUSTER_AGENT", "1")
    monkeypatch.setenv("MAKTABA_CLUSTER_DIR", str(tmp_path / "share"))
    monkeypatch.setenv("MAKTABA_OLLAMA_URL", "")
    key = tmp_path / "home-agent.key"
    key.write_bytes(HOME_KEY)
    monkeypatch.setenv("MAKTABA_HOME_AGENT_KEY", str(key))
    return tmp_path


@pytest.fixture
def registered() -> Any:
    health.register_metric("stt.audio_lost:t1", lambda: 1.0, final=lambda: 4.0)
    yield
    health._METRICS.pop("stt.audio_lost:t1", None)
    health._FINAL.pop("stt.audio_lost:t1", None)


def test_enabled_switch() -> None:
    assert cluster_agent.enabled({})
    assert not cluster_agent.enabled({"MAKTABA_CLUSTER_AGENT": "0"})
    assert not cluster_agent.enabled({"MAKTABA_CLUSTER_AGENT": "off"})


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_home_worker_runs_its_agent(
    db: Database, agent_env: Path, monkeypatch: pytest.MonkeyPatch, registered: Any
) -> None:
    await sync_graph(db, GRAPH)
    hub = FakeHub(home_key=HOME_KEY)
    async with running_hub(hub):
        monkeypatch.setenv("MAKTABA_API_URL", hub.url)
        started, stop = await _boot(db)
        assert "cluster-agent" in [t.get_name() for t in started.tasks]
        try:
            s = await hub.wait_session(1, timeout=10)
            assert s.hello["kind"] == "home" and s.hello["worker_id"] == "home-w1"
            m = await s.next("metrics", timeout=5)
            served = list(started.selector.served)
            assert served and m["pipeline"]["stages"] == served
            assert m["pipeline"]["role"] == "home"
            assert m["files"]["on_share"] is True
            # the health map: the health_samples names, but nas.reads_busy (the hub's)
            names = set(await health.measure(health.HealthCollector()))
            assert set(m["health"]) == names and "nas.reads_busy" not in names
            assert m["health"]["stt.audio_lost:t1"] == 1.0

            await s.send({"t": "cmd", "id": "d", "name": "drain", "args": {}})
            r = await s.next("cmd.result")
            assert r["ok"] and started.selector.served == ()
            assert set(started.selector.stages_off.values()) >= {"draining"}
            row = await db.fetchrow("SELECT stages FROM workers WHERE id = $1", "home-w1")
            assert list(loads(row["stages"]) or []) == []  # the registry follows at once
            await s.send({"t": "cmd", "id": "u", "name": "undrain", "args": {}})
            assert (await s.next("cmd.result"))["ok"]
            assert list(started.selector.served) == served
            await s.send({"t": "cmd", "id": "r", "name": "reload_stages", "args": {}})
            r = await s.next("cmd.result")
            assert r["ok"] and r["detail"].startswith(f"serving {len(served)} stage(s)")
            assert llm_door._TRANSPORT is not None
        finally:
            await _stop(started, stop, grace=0.3)
        final = await s.next("metrics", lambda f: bool(f.get("final")))
        assert final["health"]["stt.audio_lost:t1"] == 4.0  # the shutdown reader
    # nothing of the agent's went to the database
    assert await db.fetchval("SELECT count(*) FROM health_samples") == 0
    # stopped with the worker: the door is detached, the sinks are gone, files are there
    assert llm_door._TRANSPORT is None
    assert job_events.tracker()._sinks == []
    machine = next((agent_env / "share").iterdir())
    ident = json.loads((machine / "processes" / "home-w1.json").read_text())
    assert ident["kind"] == "home" and ident["stopped_at"]
    assert list((machine / "metrics").glob("*/home-w1.jsonl"))
    status = json.loads((agent_env / "home/.maktaba/cluster/status/home-w1.json").read_text())
    assert status["state"] == "stopped"


@pytest.mark.integration
@pytest.mark.asyncio
async def test_disabled_means_no_agent_and_no_health_numbers(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("MAKTABA_CLUSTER_AGENT", "0")
    await sync_graph(db, GRAPH)
    started, stop = await _boot(db)
    try:
        names = [t.get_name() for t in started.tasks]
        assert "cluster-agent" not in names and "health" not in names
        await asyncio.sleep(0.2)
        assert await db.fetchval("SELECT count(*) FROM health_samples") == 0
    finally:
        await _stop(started, stop)
