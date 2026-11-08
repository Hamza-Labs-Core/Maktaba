"""``scan`` on the engine does what the legacy scan handler did (Epic 54 story 54.36 §2).

One library of real files is scanned on two copies of one database: once with the old
graph (``scan`` legacy, its handler queuing each new video's ``probe`` by hand), once with
the new (``scan`` an engine stage whose new videos are its changed subjects, the declared
edge queuing their probes). Every job then drains on both. The videos, the queue and the job
log must be identical, with these allowances and no others (each is the conversion itself):

* the ``probe`` jobs the scan queued: the reason they were queued (``enqueue`` by hand, or
  the edge from ``scan``), in the job row and in its ``enqueued`` event. Their priority is
  compared, and equal (100 either way);
* the ``scan`` job's ``output_hash`` and ``output_summary`` (the legacy bookkeeping hashed
  nothing of what it found; the stage hashes its new videos and counts its walk), and its
  ``triggered`` event, which must name exactly the probes queued. Its outcome, version and
  propagation are compared, and equal.

The time inside each queue reason (``reasons[].at``) is masked: it is when each side ran.
"""

from __future__ import annotations

import os
import uuid
from collections.abc import Iterator
from dataclasses import replace
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.dispatch import EngineRuntime, dispatch_job
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import Edge, bound_graph
from maktaba_pipeline.engine.media import reset_runner
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.handlers import build_real_dispatch
from maktaba_pipeline.runtime import Database
from tests.stages.world import make_fake_media, models_off

pytestmark = [pytest.mark.integration]

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")


@pytest.fixture(autouse=True)
def _media() -> Iterator[None]:
    make_fake_media({})
    yield
    reset_runner()


def _graphs() -> tuple[dict[str, Any], dict[str, Any]]:
    new = {s.name: s for s in bound_graph()}
    scan = new["scan"]
    old_scan = replace(
        scan, legacy=True, run=None, edges=(Edge("probe", "videos_of", legacy=True),)
    )
    return {**new, "scan": old_scan}, new


async def _drain(db: Any, rt: EngineRuntime, handlers: Any) -> list[str]:
    ran: list[str] = []
    pools = sorted({s.pool for s in rt.graph.values()})
    for _ in range(300):
        job = None
        for pool in pools:
            stages = [s.name for s in rt.graph.values() if s.pool == pool]
            job = await claim_for_pool(
                db, worker_id="parity", pool=pool, stages=stages, scope="per_worker"
            )
            if job is not None:
                break
        if job is None:
            waiting = await db.fetchval(
                "SELECT count(*) FROM processing_jobs WHERE state = 'pending' AND parked_on IS NULL"
            )
            if not waiting:
                return ran
            await db.execute(
                "UPDATE processing_jobs SET not_before = NULL WHERE state = 'pending'"
                " AND parked_on IS NULL"
            )
            continue
        await dispatch_job(db, job, runtime=rt, legacy_handlers=handlers)
        ran.append(job.stage)
    raise AssertionError(f"did not quiesce: {ran[-20:]}")


async def _side(template: str, name: str, graph: dict[str, Any], root: Path) -> Database:
    import asyncpg  # type: ignore[import-untyped]

    from tests.plugins.firstparty.harness import _swap_db

    admin = await asyncpg.connect(PG_URL)
    await admin.execute(f'CREATE DATABASE "{name}" TEMPLATE "{template}"')
    await admin.close()
    db = await Database.connect(_swap_db(PG_URL, name))
    await sync_graph(db, list(graph.values()))
    await models_off(db)
    lib = uuid.UUID(int=5448)
    await db.execute(
        "INSERT INTO libraries (id, name, roots) VALUES ($1, 'مكتبة المسح', $2)",
        str(lib), [str(root)],
    )  # fmt: skip
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="scan")
    return db


ROWS = {
    "videos": ("SELECT path, filename, content_hash, size_bytes, state, library_id"
               " FROM videos ORDER BY path"),
    "jobs": ("SELECT j.stage, j.subject_kind, v.path AS subject, j.state, j.outcome,"
             " j.priority, j.output_hash, j.output_summary, j.stage_version,"
             " j.propagated_at IS NOT NULL AS propagated, j.reasons"
             " FROM processing_jobs j LEFT JOIN videos v ON v.id::text = j.subject_id::text"
             " ORDER BY j.stage, v.path"),
    "events": ("SELECT j.stage, v.path AS subject, e.kind, e.level, e.data"
               " FROM job_events e JOIN processing_jobs j ON j.id = e.job_id"
               " LEFT JOIN videos v ON v.id::text = j.subject_id::text"
               " WHERE e.kind <> 'claimed' ORDER BY j.stage, v.path, e.kind, e.id"),
}  # fmt: skip


def _allowed(table: str, row: dict[str, Any]) -> dict[str, Any]:
    """The conversion's own differences, taken out of one row (see the module's doc), and
    the time each reason was given (when the side ran, not what it did)."""
    import json

    r = dict(row)
    if r.get("reasons"):
        r["reasons"] = [{k: v for k, v in x.items() if k != "at"} for x in json.loads(r["reasons"])]
    if table == "jobs" and r["stage"] == "probe":
        r.pop("reasons")
    if table == "jobs" and r["stage"] == "scan":
        r.pop("output_hash")
        r.pop("output_summary")
    if table == "events" and r["stage"] == "probe" and r["kind"] == "enqueued":
        r.pop("data")
    return r


@pytest.mark.asyncio
async def test_scan_on_the_engine_queues_and_saves_what_the_handler_did(
    pg_template: str, tmp_path: Path
) -> None:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    root = tmp_path / "lib"
    for i, name in enumerate(("intro_a.mp4", "intro_b.mp4", "talk_c.mkv", ".hidden.mp4")):
        p = root / ("الحلقات" if i < 2 else "") / name
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(f"video {name}".encode() * 64)
    old_graph, new_graph = _graphs()
    handlers = build_real_dispatch(engine=True)
    tag = uuid.uuid4().hex[:8]
    out: dict[str, dict[str, list[dict[str, Any]]]] = {}
    for side, graph in (("old", old_graph), ("new", new_graph)):
        db = await _side(pg_template, f"maktaba_scan_{side}_{tag}", graph, root)
        try:
            ran = await _drain(db, EngineRuntime(graph=graph), handlers)
            assert "scan" in ran and "probe" in ran, ran
            out[side] = {t: [dict(r) for r in await db.fetch(q)] for t, q in ROWS.items()}
        finally:
            await db.close()
    # the engine's record of what the scan's edge queued: on the new side only, and exactly
    # the three probes (the hand enqueue logged none)
    import json

    triggered = [
        r for r in out["new"]["events"] if r["stage"] == "scan" and r["kind"] == "triggered"
    ]
    assert len(triggered) == 1, triggered
    to = json.loads(triggered[0]["data"])["to"]
    assert (
        sorted((t["stage"], t["outcome"], t["subject_kind"]) for t in to)
        == [("probe", "inserted", "video")] * 3
    ), to
    out["new"]["events"] = [r for r in out["new"]["events"] if r not in triggered]
    for table in ROWS:
        old = [_allowed(table, r) for r in out["old"][table]]
        new = [_allowed(table, r) for r in out["new"][table]]
        assert old == new, f"{table}:\nold {old}\nnew {new}"
    # and the allowances are what they say: the probes the scan queued, nothing else
    probes = [r for r in out["new"]["jobs"] if r["stage"] == "probe"]
    assert len(probes) == 3 and all(r["subject"] for r in probes)


@pytest.mark.asyncio
async def test_a_scan_that_finds_nothing_new_queues_no_probe(
    pg_template: str, tmp_path: Path
) -> None:
    """The edge's map is ``videos_changed``: only the videos the scan names. A second scan of
    an unchanged library names none and queues nothing (``videos_of`` would fall back to every
    unlinked video)."""
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    root = tmp_path / "lib"
    root.mkdir()
    (root / "a.mp4").write_bytes(b"video a" * 64)
    _, graph = _graphs()
    db = await _side(pg_template, f"maktaba_scan_twice_{uuid.uuid4().hex[:8]}", graph, root)
    try:
        rt = EngineRuntime(graph=graph)
        handlers = build_real_dispatch(engine=True)
        await _drain(db, rt, handlers)
        assert await db.fetchval("SELECT count(*) FROM processing_jobs WHERE stage = 'probe'") == 1
        lib = await db.fetchval("SELECT id FROM libraries")
        await enqueue_job(db, subject_kind="library", subject_id=lib, stage="scan", force=True)
        ran = await _drain(db, rt, handlers)
        assert ran == ["scan"], ran
        assert await db.fetchval("SELECT count(*) FROM processing_jobs WHERE stage = 'probe'") == 1
    finally:
        await db.close()
