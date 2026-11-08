"""``probe`` on the engine does what the legacy probe handler did (Epic 54 story 54.36 §2).

One library is scanned on two copies of one database: once with the old graph (``probe``
legacy: its handler marks its job done and the engine hashes the video's length and frame
size afterwards), once with the new (``probe`` an engine stage returning that same hash).
ffprobe is a fake that answers by file name: a video with audio, one with none, and one it
cannot read. Every job then drains on both. The videos, their media facts and audio tracks,
the queue and the job log must be identical, with one allowance (the conversion itself):

* a successful ``probe`` job's ``output_summary``: the legacy bookkeeping wrote none; the
  stage writes its audio track count and the video's state, checked below against the
  tracks and the state saved. Its output hash, outcome and propagation are compared, and
  equal; so are a failed probe's error kind, retry flag and attempts.

The scan runs once before the two sides part, so both probe the same videos (their ids are
in the library stages' hashes); the rows are compared as sets (two runs of one library
stage have no order of their own).
The time inside each queue reason (``reasons[].at``) is masked: it is when each side ran.
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
from maktaba_pipeline.engine.graph import bound_graph
from maktaba_pipeline.engine.media import reset_runner
from maktaba_pipeline.engine.stages.legacy import _probe_output
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.handlers import build_real_dispatch
from maktaba_pipeline.runtime import Database
from tests.stages.test_scan_engine_parity import _drain, _side
from tests.stages.world import make_fake_media

pytestmark = [pytest.mark.integration]

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")


@pytest.fixture(autouse=True)
def _media(monkeypatch: pytest.MonkeyPatch) -> Iterator[None]:
    make_fake_media({})
    # the module (``maktaba_pipeline.audio`` exports a function of the same name)
    probe_mod = sys.modules["maktaba_pipeline.audio.probe"]

    async def fake_ffprobe(path: str, **_: Any) -> dict[str, Any]:
        name = Path(path).name
        if name.startswith("broken"):
            raise probe_mod.ProbeError(f"ffprobe could not read {name}")
        streams: list[dict[str, Any]] = [
            {"index": 0, "codec_type": "video", "codec_name": "h264", "width": 1280,
             "height": 720, "avg_frame_rate": "25/1"},
        ]  # fmt: skip
        if not name.startswith("silent"):
            streams += [
                {"index": 1, "codec_type": "audio", "codec_name": "aac", "channels": 2,
                 "sample_rate": "48000", "tags": {"language": "ara"},
                 "disposition": {"default": 1}},
                {"index": 2, "codec_type": "audio", "codec_name": "ac3", "channels": 6,
                 "sample_rate": "48000", "tags": {"language": "eng", "title": "5.1"},
                 "disposition": {"default": 0}},
            ]  # fmt: skip
        return {
            "streams": streams,
            "format": {"format_name": "mov,mp4", "duration": "1500.25", "bit_rate": "1200000"},
        }

    monkeypatch.setattr(probe_mod, "run_ffprobe", fake_ffprobe)
    yield
    reset_runner()


def _graphs() -> tuple[dict[str, Any], dict[str, Any]]:
    new = {s.name: s for s in bound_graph()}
    old_probe = replace(new["probe"], legacy=True, run=None, output_fingerprint=_probe_output)
    return {**new, "probe": old_probe}, new


async def _fork(base: str, name: str, graph: dict[str, Any]) -> Database:
    import asyncpg  # type: ignore[import-untyped]

    from tests.plugins.firstparty.harness import _swap_db

    admin = await asyncpg.connect(PG_URL)
    await admin.execute(f'CREATE DATABASE "{name}" TEMPLATE "{base}"')
    await admin.close()
    db = await Database.connect(_swap_db(PG_URL, name))
    await sync_graph(db, list(graph.values()))
    return db


ROWS = {
    "videos": ("SELECT path, state, duration_sec, metadata FROM videos ORDER BY path"),
    "media_info": ("SELECT v.path, m.container, m.video_codec, m.width, m.height, m.fps,"
                   " m.bitrate_kbps, m.has_subtitles, m.raw_ffprobe"
                   " FROM media_info m JOIN videos v ON v.id = m.video_id ORDER BY v.path"),
    "audio_tracks": ("SELECT v.path, a.track_index, a.codec, a.channels, a.sample_rate,"
                     " a.language, a.title, a.is_default, a.disposition"
                     " FROM audio_tracks a JOIN videos v ON v.id = a.video_id"
                     " ORDER BY v.path, a.track_index"),
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
    """The conversion's own difference, taken out of one row (see the module's doc), and the
    time each reason was given (when the side ran, not what it did)."""
    r = dict(row)
    if r.get("reasons"):
        r["reasons"] = [{k: v for k, v in x.items() if k != "at"} for x in json.loads(r["reasons"])]
    if table == "jobs" and r["stage"] == "probe" and r["state"] == "done":
        r.pop("output_summary")
    return r


def _differences(old: list[dict[str, Any]], new: list[dict[str, Any]]) -> str:
    """The rows that differ, and in each the fields that do."""
    if len(old) != len(new):
        return f"{len(old)} rows against {len(new)}:\nold {old}\nnew {new}"
    return "\n".join(
        f"{o.get('stage')} {o.get('subject') or o.get('path')}: "
        + ", ".join(f"{k} {o.get(k)!r} -> {n.get(k)!r}" for k in o if o.get(k) != n.get(k))
        for o, n in zip(old, new, strict=True)
        if o != n
    )


@pytest.mark.asyncio
async def test_probe_on_the_engine_saves_and_queues_what_the_handler_did(
    pg_template: str, tmp_path: Path
) -> None:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    root = tmp_path / "lib"
    for name in ("talk_a.mp4", "silent_b.mp4", "broken_c.mkv"):
        p = root / "الحلقات" / name
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(f"video {name}".encode() * 64)
    old_graph, new_graph = _graphs()
    handlers = build_real_dispatch(engine=True)
    tag = uuid.uuid4().hex[:8]
    # the scan runs once, before the two sides part: both start from the same videos (their
    # ids are in the library stages' hashes) and the same queued probes
    base = f"maktaba_probe_base_{tag}"
    db = await _side(pg_template, base, new_graph, root)
    try:
        job = await claim_for_pool(
            db, worker_id="parity", pool=new_graph["scan"].pool, stages=["scan"], scope="per_worker"
        )
        assert job is not None
        await dispatch_job(
            db, job, runtime=EngineRuntime(graph=new_graph), legacy_handlers=handlers
        )
        assert await db.fetchval("SELECT count(*) FROM processing_jobs WHERE stage = 'probe'") == 3
    finally:
        await db.close()
    out: dict[str, dict[str, list[dict[str, Any]]]] = {}
    for side, graph in (("old", old_graph), ("new", new_graph)):
        db = await _fork(base, f"maktaba_probe_{side}_{tag}", graph)
        try:
            ran = await _drain(db, EngineRuntime(graph=graph), handlers)
            assert "probe" in ran, ran
            out[side] = {t: [dict(r) for r in await db.fetch(q)] for t, q in ROWS.items()}
        finally:
            await db.close()
    for table in ROWS:
        # as sets of rows: two runs of one library stage have no order of their own
        old = sorted((_allowed(table, r) for r in out["old"][table]), key=repr)
        new = sorted((_allowed(table, r) for r in out["new"][table]), key=repr)
        assert old == new, f"{table}: {_differences(old, new)}"
    # the world is what it says: one video probed with its two tracks, one with none (its
    # own state), one that failed with the handler's kind; the probes' edges fired
    probes = {
        r["subject"].rsplit("/", 1)[-1]: r for r in out["new"]["jobs"] if r["stage"] == "probe"
    }
    assert probes["talk_a.mp4"]["state"] == probes["silent_b.mp4"]["state"] == "done"
    assert probes["broken_c.mkv"]["state"] == "failed"
    assert probes["broken_c.mkv"]["error_kind"] == "probe_failed"
    assert len(out["new"]["audio_tracks"]) == 2
    states = {r["path"].rsplit("/", 1)[-1]: r["state"] for r in out["new"]["videos"]}
    for name, tracks in (("talk_a.mp4", 2), ("silent_b.mp4", 0)):
        assert json.loads(probes[name]["output_summary"]) == {
            "audio_tracks": tracks,
            "state": states[name],
        }
        assert probes[name]["output_summary"] is not None
        assert (
            next(
                r
                for r in out["old"]["jobs"]
                if r["stage"] == "probe" and r["subject"] == probes[name]["subject"]
            )["output_summary"]
            is None
        )
    assert {r["stage"] for r in out["new"]["jobs"]} >= {"thumb_hash", "link"}
