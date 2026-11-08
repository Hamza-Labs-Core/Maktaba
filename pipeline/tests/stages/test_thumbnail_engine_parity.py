"""``thumbnail`` on the engine does what the legacy thumbnail handler did (Epic 54 story
54.36 §2).

One library is scanned and everything but ``thumbnail`` drained once; then two copies of
that database part: on one ``thumbnail`` is legacy (its handler marks its job done and the
engine records a hash of the time), on the other an engine stage (its output the poster's
and the sprite's paths). The frame maker is a fake that answers by file name: a video it
makes frames for, one whose state is ``indexed`` (the stage moves it on), and one ffmpeg
fails on. Every job then drains on both. The videos (their poster, sprite and state), the
queue and the job log must be identical, with these allowances and no others (each is the
conversion itself):

* a successful ``thumbnail`` job's ``output_hash`` (the legacy bookkeeping hashed the time;
  the stage hashes the two paths, checked below) and ``output_summary`` (the legacy wrote
  none; the stage writes its chapter count and the video's state, checked below). Its
  outcome, version and propagation are compared, and equal; so are a failed run's error kind,
  retry flag and attempts.

The rows are compared as sets (two runs of one library stage have no order of their own),
and the time inside each queue reason (``reasons[].at``) is masked.
"""

from __future__ import annotations

import json
import os
import sys
import uuid
from collections.abc import Iterator
from dataclasses import replace
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.dispatch import EngineRuntime, dispatch_job
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import bound_graph
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.media import reset_runner
from maktaba_pipeline.engine.stages import thumbnail as thumbnail_stage
from maktaba_pipeline.engine.stages.legacy import _always_changed
from maktaba_pipeline.handlers import build_real_dispatch
from maktaba_pipeline.thumbnail import handler as thumbnail_handler
from maktaba_pipeline.thumbnail.generator import ThumbnailError, ThumbnailSet
from tests.stages.test_probe_engine_parity import _differences, _fork
from tests.stages.test_scan_engine_parity import _drain, _side
from tests.stages.world import make_fake_media

pytestmark = [pytest.mark.integration]

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")


@pytest.fixture(autouse=True)
def _media(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> Iterator[None]:
    make_fake_media({})
    probe_mod = sys.modules["maktaba_pipeline.audio.probe"]

    async def fake_ffprobe(path: str, **_: Any) -> dict[str, Any]:
        return {
            "streams": [{"index": 0, "codec_type": "video", "codec_name": "h264",
                         "width": 1280, "height": 720, "avg_frame_rate": "25/1"}],
            "format": {"format_name": "mov,mp4", "duration": "1500.25"},
        }  # fmt: skip

    async def fake_frames(
        src: str, *, duration_sec: float, content_hash: str, chapters: Any = (), **_: Any
    ) -> ThumbnailSet:
        if Path(src).name.startswith("broken"):
            raise ThumbnailError("ffmpeg_thumbnail", returncode=1, stderr_tail="bad frame")
        d = tmp_path / "thumbs" / content_hash
        d.mkdir(parents=True, exist_ok=True)
        poster, sprite = d / "poster.jpg", d / "sprite.jpg"
        poster.write_bytes(b"poster")
        sprite.write_bytes(b"sprite")
        return ThumbnailSet(poster=poster, sprite=sprite)

    monkeypatch.setattr(probe_mod, "run_ffprobe", fake_ffprobe)
    monkeypatch.setattr(thumbnail_handler, "generate_thumbnails", fake_frames)
    monkeypatch.setattr(thumbnail_stage, "generate", fake_frames)
    yield
    reset_runner()


def _graphs() -> tuple[dict[str, Any], dict[str, Any]]:
    new = {s.name: s for s in bound_graph()}
    old = replace(new["thumbnail"], legacy=True, run=None, output_fingerprint=_always_changed)
    return {**new, "thumbnail": old}, new


async def _drain_but_thumbnails(db: Any, graph: dict[str, Any], handlers: Any) -> None:
    rt = EngineRuntime(graph=graph)
    for _ in range(300):
        job = None
        for pool in sorted({s.pool for s in graph.values()}):
            stages = [s.name for s in graph.values() if s.pool == pool and s.name != "thumbnail"]
            job = await claim_for_pool(
                db, worker_id="parity", pool=pool, stages=stages, scope="per_worker"
            )
            if job is not None:
                break
        if job is None:
            waiting = await db.fetchval(
                "SELECT count(*) FROM processing_jobs WHERE state = 'pending'"
                " AND parked_on IS NULL AND stage <> 'thumbnail'"
            )
            if not waiting:
                return
            await db.execute(
                "UPDATE processing_jobs SET not_before = NULL WHERE state = 'pending'"
                " AND parked_on IS NULL"
            )
            continue
        await dispatch_job(db, job, runtime=rt, legacy_handlers=handlers)
    raise AssertionError("the base did not quiesce")


ROWS = {
    "videos": "SELECT path, state, poster_path, sprite_path FROM videos ORDER BY path",
    "jobs": ("SELECT j.stage, j.subject_kind, v.path AS subject, j.state, j.outcome,"
             " j.priority, j.attempts, j.output_hash, j.output_summary, j.input_hash,"
             " j.stage_version, j.propagated_at IS NOT NULL AS propagated, j.reasons,"
             " j.error::jsonb->>'kind' AS error_kind,"
             " j.error::jsonb->>'retryable' AS retryable"
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
    r = dict(row)
    if r.get("reasons"):
        r["reasons"] = [{k: v for k, v in x.items() if k != "at"} for x in json.loads(r["reasons"])]
    if table == "jobs" and r["stage"] == "thumbnail" and r["state"] == "done":
        r.pop("output_hash")
        r.pop("output_summary")
    return r


@pytest.mark.asyncio
async def test_thumbnail_on_the_engine_saves_and_queues_what_the_handler_did(
    pg_template: str, tmp_path: Path
) -> None:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    root = tmp_path / "lib"
    for name in ("talk_a.mp4", "indexed_b.mp4", "broken_c.mkv"):
        p = root / "الحلقات" / name
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(f"video {name}".encode() * 64)
    old_graph, new_graph = _graphs()
    handlers = build_real_dispatch(engine=True)
    tag = uuid.uuid4().hex[:8]
    # everything before the thumbnails runs once, before the two sides part: both start from
    # the same videos (their ids are in the library stages' hashes) and the same queued thumbnails
    base = f"maktaba_thumb_base_{tag}"
    db = await _side(pg_template, base, new_graph, root)
    try:
        await _drain_but_thumbnails(db, new_graph, handlers)
        for (vid,) in await db.fetch("SELECT id FROM videos ORDER BY path"):
            await enqueue_job(db, subject_kind="video", subject_id=vid, stage="thumbnail")
        await db.execute("UPDATE videos SET state = 'indexed' WHERE path LIKE '%indexed_b.mp4'")
    finally:
        await db.close()
    out: dict[str, dict[str, list[dict[str, Any]]]] = {}
    for side, graph in (("old", old_graph), ("new", new_graph)):
        db = await _fork(base, f"maktaba_thumb_{side}_{tag}", graph)
        try:
            ran = await _drain(db, EngineRuntime(graph=graph), handlers)
            assert "thumbnail" in ran, ran
            out[side] = {t: [dict(r) for r in await db.fetch(q)] for t, q in ROWS.items()}
        finally:
            await db.close()
    for table in ROWS:
        old = sorted((_allowed(table, r) for r in out["old"][table]), key=repr)
        new = sorted((_allowed(table, r) for r in out["new"][table]), key=repr)
        assert old == new, f"{table}: {_differences(old, new)}"
    # the world is what it says, and the allowances hold what they say
    jobs = {
        r["subject"].rsplit("/", 1)[-1]: r for r in out["new"]["jobs"] if r["stage"] == "thumbnail"
    }
    videos = {r["path"].rsplit("/", 1)[-1]: r for r in out["new"]["videos"]}
    assert jobs["broken_c.mkv"]["state"] == "failed"
    assert jobs["broken_c.mkv"]["error_kind"] == "ffmpeg_thumbnail"
    assert videos["indexed_b.mp4"]["state"] == "thumbnailed"
    for name in ("talk_a.mp4", "indexed_b.mp4"):
        v, j = videos[name], jobs[name]
        assert j["state"] == "done" and j["outcome"] == "changed"
        assert j["output_hash"] == canonical_hash(
            {"poster": v["poster_path"], "sprite": v["sprite_path"]}
        )
        assert json.loads(j["output_summary"]) == {"chapters": 0, "state": v["state"]}
        legacy_job = next(
            r
            for r in out["old"]["jobs"]
            if r["stage"] == "thumbnail" and r["subject"] == j["subject"]
        )
        assert legacy_job["output_summary"] is None
        assert legacy_job["output_hash"] != j["output_hash"]
