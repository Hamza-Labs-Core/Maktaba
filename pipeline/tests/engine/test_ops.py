# ruff: noqa: E501  (long SQL strings and test data read better unwrapped)
"""Reconciler, retention, health, workers, path maps (plan 06-11 sections 6.4-6.6, notes N2)."""

from __future__ import annotations

import dataclasses
from datetime import timedelta
from pathlib import Path

import pytest

from maktaba_pipeline.db.jobs_state import mark_done
from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.health import HealthCollector, NasProbe, sample_once
from maktaba_pipeline.engine.paths import PathMap
from maktaba_pipeline.engine.prune import prune_once
from maktaba_pipeline.engine.reconcile import reconcile_once
from maktaba_pipeline.engine.sql import now, parse_ts, to_ts
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.engine.workers import (
    deregister_worker,
    heartbeat_worker,
    live_workers,
    register_worker,
    stages_without_worker,
)
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video
from tests.engine.toy import Recorder, drain, rows, setup, toy_graph

# ------------------------------------------------------------------ reconciler


@pytest.mark.asyncio
async def test_reconciler_repairs_a_crash_between_legacy_done_and_propagation(db: Database) -> None:
    await sync_graph(db, GRAPH)
    graph = {s.name: s for s in GRAPH}
    lib = await make_library(db)
    v = await make_video(db, lib, duration=600.0)
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="probe")
    job = await claim_for_pool(db, worker_id="w", pool="nas_read", stages=["probe"])
    assert job is not None
    await mark_done(db, job_id=job.id)  # the handler finished; the process died right here
    await db.execute(
        "UPDATE processing_jobs SET finished_at = $2 WHERE id = $1",
        job.id,
        to_ts(db, now() - timedelta(minutes=5)),
    )
    assert (await rows(db, "thumb_hash")) == []
    report = await reconcile_once(db, graph)
    assert report.leader and report.propagated == 1
    assert len(await rows(db, "thumb_hash")) == 1
    assert len(await rows(db, "link")) == 1
    detect = await rows(db, "detect")
    assert len(detect) == 1 and str(detect[0]["subject_id"]) == str(lib)
    probe = (await rows(db, "probe"))[0]
    assert probe["propagated_at"] is not None and probe["outcome"] == "changed"
    # idempotent
    again = await reconcile_once(db, graph)
    assert again.propagated == 0
    assert len(await rows(db, "thumb_hash")) == 1


@pytest.mark.asyncio
async def test_reconciler_version_sweep_queues_each_stale_subject_once(db: Database) -> None:
    rec = Recorder(inputs={"t_a": "in"})
    rt = await setup(db, rec)
    lib = await make_library(db)
    vs = [await make_video(db, lib) for _ in range(3)]
    for v in vs:
        await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    graph2 = tuple(dataclasses.replace(s, version=2) if s.name == "t_a" else s for s in toy_graph())
    rt2 = await setup(db, rec, graph2)
    report = await reconcile_once(db, rt2.graph)
    assert report.version_sweep == 3
    pending = [r for r in await rows(db, "t_a") if r["state"] == "pending"]
    assert len(pending) == 3 and {r["priority"] for r in pending} == {500}
    assert (await reconcile_once(db, rt2.graph)).version_sweep == 0  # already queued
    await drain(db, rt2)
    assert rec.count("t_a") == 6  # inputs unchanged but the version moved: ran once more each
    assert (await reconcile_once(db, rt2.graph)).version_sweep == 0


@pytest.mark.asyncio
async def test_reconciler_schedules_youtube_refresh(db: Database) -> None:
    await sync_graph(db, GRAPH)
    graph = {s.name: s for s in GRAPH}
    lib = await make_library(db)
    other = await make_library(db)  # no channels: never refreshed
    await db.execute(
        "INSERT INTO youtube_channels (channel_id, library_id, url, status)"
        " VALUES ('UC1', $1, 'https://youtube.com/@x', 'ok')",
        str(lib),
    )
    r1 = await reconcile_once(db, graph, yt_refresh_hours=168)
    assert r1.yt_refresh == 1
    q = await rows(db, "youtube_sync")
    assert len(q) == 1 and str(q[0]["subject_id"]) == str(lib)
    assert (await reconcile_once(db, graph)).yt_refresh == 0  # one is already live
    await db.execute(
        "UPDATE processing_jobs SET state = 'done', finished_at = $1", to_ts(db, now())
    )
    assert (await reconcile_once(db, graph)).yt_refresh == 0  # fresh enough
    await db.execute(
        "UPDATE processing_jobs SET finished_at = $1", to_ts(db, now() - timedelta(days=30))
    )
    assert (await reconcile_once(db, graph)).yt_refresh == 1
    assert other


# ------------------------------------------------------------------ retention


@pytest.mark.asyncio
async def test_prune_applies_retention_and_keeps_latest_done(db: Database) -> None:
    await sync_graph(db, GRAPH)
    lib = await make_library(db)
    v = await make_video(db, lib)
    old = to_ts(db, now() - timedelta(days=120))
    # three old done rows for one subject/stage + one old failed + one recent done
    for _ in range(3):
        await db.execute(
            "INSERT INTO processing_jobs (video_id, stage, subject_kind, subject_id, state,"
            " finished_at, outcome) VALUES ($1,'link','video',$1,'done',$2,'changed')",
            str(v),
            old,
        )
    await db.execute(
        "INSERT INTO processing_jobs (video_id, stage, subject_kind, subject_id, state,"
        " finished_at) VALUES ($1,'probe','video',$1,'failed',$2)",
        str(v),
        old,
    )
    other = await make_video(db, lib)
    await db.execute(
        "INSERT INTO processing_jobs (video_id, stage, subject_kind, subject_id, state,"
        " finished_at, outcome) VALUES ($1,'link','video',$1,'done',$2,'changed')",
        str(other),
        to_ts(db, now()),
    )
    await db.execute(
        "INSERT INTO processing_jobs (video_id, stage, subject_kind, subject_id, state,"
        " finished_at, outcome) VALUES ($1,'probe','video',$1,'done',$2,'changed')",
        str(other),
        old,
    )
    # logs at each level, old and new
    for level, age in (
        ("debug", 8),
        ("debug", 1),
        ("info", 31),
        ("info", 5),
        ("error", 100),
        ("error", 60),
    ):
        await db.execute(
            "INSERT INTO job_events (job_id, subject_kind, subject_id, stage, at, level, kind, message)"
            " VALUES (1,'video',$1,'probe',$2,$3,'step','m')",
            str(v),
            to_ts(db, now() - timedelta(days=age)),
            level,
        )
    # health samples: old ones fold into a rollup
    await db.execute(
        "INSERT INTO health_samples (at, host, metric, value) VALUES ($1,'h','cpu.pct',10)",
        to_ts(db, now() - timedelta(hours=60)),
    )
    await db.execute(
        "INSERT INTO health_samples (at, host, metric, value) VALUES ($1,'h','cpu.pct',20)",
        to_ts(db, now() - timedelta(hours=60) + timedelta(seconds=30)),
    )
    await db.execute(
        "INSERT INTO health_samples (at, host, metric, value) VALUES ($1,'h','cpu.pct',99)",
        to_ts(db, now()),
    )
    # plans: keep 20
    for i in range(25):
        await db.execute(
            "INSERT INTO channel_plans (planner_version, input_hash, plan) VALUES (1,$1,'{}')",
            f"h{i}",
        )
    # stale worker
    await db.execute(
        "INSERT INTO workers (id, host, last_seen_at) VALUES ('old','h',$1)",
        to_ts(db, now() - timedelta(days=3)),
    )

    report = await prune_once(db)

    jobs = await db.fetch("SELECT stage, state, finished_at FROM processing_jobs ORDER BY id")
    summary = sorted((r["stage"], r["state"]) for r in jobs)
    # kept: the newest old 'link' done row (latest per subject/stage), the recent link done row, and
    # the only old 'probe' done row of `other`; the older link rows and the old failed row are gone
    assert summary == [("link", "done"), ("link", "done"), ("probe", "done")]
    assert report.jobs == 3
    ev = sorted((r["level"],) for r in await db.fetch("SELECT level FROM job_events"))
    assert ev == [("debug",), ("error",), ("info",)]
    hs = await db.fetch("SELECT host, metric, value FROM health_samples ORDER BY value")
    assert [(r["host"], round(float(r["value"]))) for r in hs] == [("*rollup*", 15), ("h", 99)]
    assert await db.fetchval("SELECT count(*) FROM channel_plans") == 20
    assert await db.fetchval("SELECT count(*) FROM workers") == 0
    assert report.channel_plans == 5 and report.workers == 1


# ------------------------------------------------------------------ health


@pytest.mark.unit
def test_health_collector_percentiles_and_window() -> None:
    clock = [0.0]
    c = HealthCollector(clock=lambda: clock[0])
    assert c.open_percentiles() is None
    for ms in (10, 20, 30, 40, 100):
        c.record_open(ms)
    c.record_timeout()
    p50, p95 = c.open_percentiles()  # type: ignore[misc]
    assert p50 == 30 and p95 == 100
    assert c.timeouts() == 1
    clock[0] = 11.0  # the 10 s window rolls over
    assert c.open_percentiles() is None and c.timeouts() == 0


@pytest.mark.asyncio
async def test_nas_probe_times_a_real_file_and_counts_stderr(tmp_path: Path) -> None:
    f = tmp_path / "a.bin"
    f.write_bytes(b"x" * 100_000)
    nas = NasProbe()
    async with nas.timed(str(f)):
        pass
    assert nas.collector.open_percentiles() is not None
    assert nas.record_stderr("Error: [WinError 121] The semaphore timeout period has expired")
    assert not nas.record_stderr("everything is fine")
    assert nas.collector.timeouts() == 1
    async with nas.timed(str(tmp_path / "missing.bin")):  # a probe failure never raises
        pass


@pytest.mark.asyncio
async def test_sample_once_writes_health_samples(db: Database) -> None:
    c = HealthCollector()
    c.record_open(12.0)
    c.record_timeout()

    async def plex() -> float:
        return 42.0

    c.probes["net.plex_ms"] = plex
    written = await sample_once(db, c, host="pc", probe_network=True)
    assert written["nas.open_ms_p50"] == 12.0 and written["nas.timeouts"] == 1.0
    assert written["net.plex_ms"] == 42.0 and written["nas.reads_busy"] == 0.0
    stored = {r["metric"] for r in await db.fetch("SELECT metric FROM health_samples")}
    assert {"nas.open_ms_p50", "nas.open_ms_p95", "nas.timeouts", "net.plex_ms"} <= stored


# ------------------------------------------------------------------ workers


@pytest.mark.asyncio
async def test_a_new_worker_replaces_the_dead_ones_of_its_host(db: Database) -> None:
    for wid, host in (("mac-1-old", "MAC"), ("mac-2-old", "MAC"), ("pc-old", "PC")):
        await register_worker(db, worker_id=wid, host=host, stages=["probe"], pools=["cpu"])
    await register_worker(db, worker_id="mac-2-live", host="MAC", stages=["probe"], pools=["cpu"])
    stale = to_ts(db, now() - timedelta(minutes=10))
    await db.execute("UPDATE workers SET last_seen_at = $1 WHERE id LIKE '%-old'", stale)
    # killed before they could deregister; a new worker starts on the Mac
    await register_worker(db, worker_id="mac-1-new", host="MAC", stages=["probe"], pools=["cpu"])
    ids = sorted(r["id"] for r in await db.fetch("SELECT id FROM workers"))
    # the Mac's dead ones are gone, its live sibling stays, the PC's dead one still shows
    assert ids == ["mac-1-new", "mac-2-live", "pc-old"]


@pytest.mark.asyncio
async def test_worker_registry_and_stages_without_a_worker(db: Database) -> None:
    await sync_graph(db, GRAPH)
    await register_worker(
        db, worker_id="pc/1", host="PC", stages=["probe", "detect"], pools=["nas_read", "cpu"]
    )
    await register_worker(
        db,
        worker_id="mac/1",
        host="MAC",
        stages=["transcribe", "extract"],
        pools=["gpu"],
        path_map={"Y:/": "/Volumes/Y/"},
        version="1.0",
    )
    live = await live_workers(db)
    assert [w["host"] for w in live] == ["MAC", "PC"]
    assert live[0]["stages"] == ["transcribe", "extract"]
    missing = await stages_without_worker(db)
    assert "transcribe" not in missing and "probe" not in missing
    assert "thumb_hash" in missing and "subtitle_gen" in missing
    # the Mac goes away: its stages have no live worker
    await db.execute(
        "UPDATE workers SET last_seen_at = $1 WHERE id = 'mac/1'",
        to_ts(db, now() - timedelta(minutes=5)),
    )
    assert "transcribe" in await stages_without_worker(db)
    await heartbeat_worker(db, "mac/1")
    assert "transcribe" not in await stages_without_worker(db)
    await deregister_worker(db, "mac/1")
    assert [w["host"] for w in await live_workers(db)] == ["PC"]
    seen = parse_ts((await live_workers(db))[0]["last_seen_at"])
    assert seen is not None


# ------------------------------------------------------------------ path maps


@pytest.mark.unit
def test_path_map_parse_and_round_trip() -> None:
    pm = PathMap.parse("Y:/=/Volumes/Y; O:/=/Volumes/O/")
    assert pm.to_local("Y:/Channel/a.mp4") == "/Volumes/Y/Channel/a.mp4"
    assert pm.to_local("o:/x/b.mkv") == "/Volumes/O/x/b.mkv"  # drive letters are case-insensitive
    assert pm.to_local("Z:/other.mp4") == "Z:/other.mp4"  # unmapped paths pass through
    assert pm.to_db("/Volumes/Y/Channel/a.mp4") == "Y:/Channel/a.mp4"
    assert PathMap.parse('{"Y:/": "/mnt/y/"}').to_local("Y:/a") == "/mnt/y/a"
    assert PathMap.parse("").to_local("Y:/a") == "Y:/a"
    assert pm.as_json() == {"O:/": "/Volumes/O/", "Y:/": "/Volumes/Y/"}


@pytest.mark.unit
def test_path_map_longest_prefix_wins() -> None:
    pm = PathMap.parse("Y:/=/a/;Y:/special/=/b/")
    assert pm.to_local("Y:/special/x.mp4") == "/b/x.mp4"
    assert pm.to_local("Y:/plain/x.mp4") == "/a/plain/x.mp4"
