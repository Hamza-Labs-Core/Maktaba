"""``extract`` on the engine does what the legacy extract handler did (Epic 54 story 54.36
§2), with a speech backend that reads a file and with one that streams.

One library is scanned and probed once; extract is queued for each video; then two copies
of that database part: on one ``extract`` is legacy (its handler queues ``transcribe`` by
hand and the engine records a hash of the time), on the other an engine stage (it asks for
``transcribe`` as a declared request). The decoder is a fake that answers by file name: a
video it decodes, one it fails on, one with no audio track, and one whose transcription is
already handled (a live transcribe job: a streaming extract queues none for it). Every job
but ``transcribe`` (it needs a speech model) then drains on both. The videos, the audio
cache, the tracks, the queue (``transcribe``'s payload and priority among it) and the job log
must be identical, with these allowances and no others (each is the conversion itself):

* the ``transcribe`` jobs extract queued: the reason they were queued (``enqueue`` by hand,
  or the request from ``extract``), in the job row and in its ``enqueued`` or ``coalesced``
  event (the outcome in the event is compared, and equal);
* a successful ``extract`` job's ``output_hash`` (the legacy bookkeeping hashed the time; the
  stage hashes the track and the WAV) and ``output_summary`` (the legacy wrote none), both
  checked below, and its ``requested`` event, which must name exactly the transcribes
  asked for (a live one coalesced). Its outcome, version and propagation are compared, and
  equal; so are a failed run's error kind, retry flag and attempts.

The rows are compared as sets, and the time inside each queue reason is masked.
"""

from __future__ import annotations

import importlib
import json
import os
import sys
import uuid
from collections.abc import Iterator
from dataclasses import replace
from pathlib import Path
from typing import Any

import pytest
from maktaba_fp_transcription.stages import extract as extract_stage

from maktaba_pipeline import handlers as handlers_mod
from maktaba_pipeline.audio.extract import ExtractError
from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.dispatch import EngineRuntime, dispatch_job
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import Edge, bound_graph
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.media import reset_runner
from maktaba_pipeline.engine.stages.legacy import _always_changed
from maktaba_pipeline.handlers import build_real_dispatch
from tests.stages.test_probe_engine_parity import _differences, _fork
from tests.stages.test_scan_engine_parity import _side
from tests.stages.world import identity_known, make_fake_media

pytestmark = [pytest.mark.integration]

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")


@pytest.fixture(autouse=True)
def _media(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> Iterator[None]:
    make_fake_media({})
    probe_mod = sys.modules["maktaba_pipeline.audio.probe"]

    async def fake_ffprobe(path: str, **_: Any) -> dict[str, Any]:
        streams: list[dict[str, Any]] = [
            {"index": 0, "codec_type": "video", "codec_name": "h264", "width": 1280,
             "height": 720, "avg_frame_rate": "25/1"},
        ]  # fmt: skip
        if not Path(path).name.startswith("silent"):
            streams.append(
                {"index": 1, "codec_type": "audio", "codec_name": "aac", "channels": 2,
                 "sample_rate": "48000", "tags": {"language": "ara"},
                 "disposition": {"default": 1}},
            )  # fmt: skip
        return {"streams": streams, "format": {"format_name": "mov,mp4", "duration": "900.5"}}

    async def fake_decode(src: str, track_index: int, *, content_hash: str, **_: Any) -> Path:
        if Path(src).name.startswith("broken"):
            raise ExtractError("ffmpeg_extract", returncode=1, stderr_tail="bad packet")
        wav = tmp_path / "cache" / f"{content_hash}.wav"
        wav.parent.mkdir(parents=True, exist_ok=True)
        wav.write_bytes(b"RIFF" + bytes(60))
        return wav

    monkeypatch.setattr(probe_mod, "run_ffprobe", fake_ffprobe)
    monkeypatch.setattr(handlers_mod, "_ffmpeg_extract", fake_decode)
    monkeypatch.setattr(extract_stage, "decode", fake_decode)
    yield
    reset_runner()


def _graphs() -> tuple[dict[str, Any], dict[str, Any]]:
    new = {s.name: s for s in bound_graph()}
    old = replace(
        new["extract"],
        legacy=True,
        run=None,
        output_fingerprint=_always_changed,
        may_request=(),
        edges=(Edge("transcribe", "same", legacy=True),),
    )
    return {**new, "extract": old}, new


async def _drain_except(db: Any, graph: dict[str, Any], handlers: Any, skip: set[str]) -> list[str]:
    rt = EngineRuntime(graph=graph)
    ran: list[str] = []
    for _ in range(300):
        job = None
        for pool in sorted({s.pool for s in graph.values()}):
            stages = [s.name for s in graph.values() if s.pool == pool and s.name not in skip]
            if not stages:
                continue
            job = await claim_for_pool(
                db, worker_id="parity", pool=pool, stages=stages, scope="per_worker"
            )
            if job is not None:
                break
        if job is None:
            waiting = await db.fetchval(
                "SELECT count(*) FROM processing_jobs WHERE state = 'pending'"
                " AND parked_on IS NULL AND NOT (stage = ANY($1::text[]))",
                sorted(skip),
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


ROWS = {
    "videos": "SELECT path, state FROM videos ORDER BY path",
    "audio_cache": ("SELECT v.path, c.path AS wav, c.bytes, t.track_index,"
                    " t.last_extracted_at IS NOT NULL AS stamped"
                    " FROM audio_cache c JOIN videos v ON v.id = c.video_id"
                    " JOIN audio_tracks t ON t.id = c.audio_track_id ORDER BY v.path"),
    "jobs": ("SELECT j.stage, j.subject_kind, v.path AS subject, j.state, j.outcome,"
             " j.priority, j.attempts, j.payload, j.output_hash, j.output_summary,"
             " j.input_hash, j.stage_version, j.propagated_at IS NOT NULL AS propagated,"
             " j.reasons, j.error::jsonb->>'kind' AS error_kind,"
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
    if table == "jobs" and r["stage"] == "transcribe" and r["state"] == "pending":
        r.pop("reasons")
    if table == "jobs" and r["stage"] == "extract" and r["state"] == "done":
        r.pop("output_hash")
        r.pop("output_summary")
    if table == "events" and r["stage"] == "transcribe" and r["kind"] in ("enqueued", "coalesced"):
        # the reason only: the outcome (inserted, or a live one coalesced) is compared
        r["data"] = json.loads(r["data"])["outcome"]
    return r


@pytest.mark.asyncio
@pytest.mark.parametrize("reads_file", [True, False], ids=["file", "stream"])
async def test_extract_on_the_engine_saves_and_queues_what_the_handler_did(
    pg_template: str, tmp_path: Path, monkeypatch: pytest.MonkeyPatch, reads_file: bool
) -> None:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    stt = importlib.import_module("maktaba_pipeline.stt.transcribe")
    monkeypatch.setattr(stt, "configured_backend_requires_file", lambda: reads_file)
    root = tmp_path / "lib"
    names = ("talk_a.mp4", "broken_b.mp4", "silent_c.mp4", "handled_d.mp4")
    for name in names:
        p = root / "الحلقات" / name
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(f"video {name}".encode() * 64)
    old_graph, new_graph = _graphs()
    handlers = build_real_dispatch(engine=True)
    tag = uuid.uuid4().hex[:8]
    # the scan and the probes run once, before the two sides part: both start from the same
    # videos and tracks and the same queued extracts
    base = f"maktaba_extract_base_{tag}"
    db = await _side(pg_template, base, new_graph, root)
    try:
        # the scan first, then every video's identity marked known (the parity is extract's:
        # the poster and transcript chain the first probe starts is not compared here)
        await _drain_except(db, new_graph, handlers, set(new_graph) - {"scan"})
        await identity_known(db)
        await _drain_except(db, new_graph, handlers, {"extract", "transcribe"})
        for row in await db.fetch("SELECT id, path FROM videos ORDER BY path"):
            await enqueue_job(db, subject_kind="video", subject_id=row["id"], stage="extract")
            if row["path"].endswith("handled_d.mp4"):
                await enqueue_job(
                    db, subject_kind="video", subject_id=row["id"], stage="transcribe"
                )
    finally:
        await db.close()
    out: dict[str, dict[str, list[dict[str, Any]]]] = {}
    for side, graph in (("old", old_graph), ("new", new_graph)):
        db = await _fork(base, f"maktaba_extract_{side}_{tag}", graph)
        try:
            ran = await _drain_except(db, graph, handlers, {"transcribe"})
            assert "extract" in ran, ran
            out[side] = {t: [dict(r) for r in await db.fetch(q)] for t, q in ROWS.items()}
        finally:
            await db.close()
    # the engine's record of what extract asked for: on the new side only, and exactly the
    # transcribes asked for
    triggered = [
        r
        for r in out["new"]["events"]
        if r["stage"] == "extract" and r["kind"] in ("triggered", "requested")
    ]
    asked = sorted(
        (r["subject"], t["stage"], t["outcome"])
        for r in triggered
        for t in json.loads(r["data"])["to"]
    )
    out["new"]["events"] = [r for r in out["new"]["events"] if r not in triggered]
    for table in ROWS:
        old = sorted((_allowed(table, r) for r in out["old"][table]), key=repr)
        new = sorted((_allowed(table, r) for r in out["new"][table]), key=repr)
        assert old == new, f"{table}: {_differences(old, new)}"
    # the world is what it says, and the allowances hold what they say
    jobs = {
        (r["stage"], r["subject"].rsplit("/", 1)[-1]): r
        for r in out["new"]["jobs"]
        if r["stage"] in ("extract", "transcribe")
    }
    assert jobs[("extract", "broken_b.mp4")]["error_kind"] == (
        "ffmpeg_extract" if reads_file else None
    )
    assert jobs[("extract", "silent_c.mp4")]["error_kind"] == "extract_no_audio_track"
    queued = {n for (s, n), r in jobs.items() if s == "transcribe"}
    want = {"talk_a.mp4", "handled_d.mp4"} | ({"broken_b.mp4"} if not reads_file else set())
    assert queued == want, queued
    # a WAV written always asks (a live transcribe coalesces it); a streaming finish asks only
    # while the transcription is not handled
    expect_asked = {"talk_a.mp4"} | ({"broken_b.mp4"} if not reads_file else set())
    want_asked = {(n, "transcribe", "inserted") for n in expect_asked}
    if reads_file:
        want_asked.add(("handled_d.mp4", "transcribe", "coalesced"))
    assert {(s.rsplit("/", 1)[-1], st, o) for s, st, o in asked} == want_asked
    for name in expect_asked:
        t = jobs[("transcribe", name)]
        payload = json.loads(t["payload"])
        assert set(payload) == {"audio_track_id", "content_hash"} and t["priority"] == 100
        e = jobs[("extract", name)]
        wav = next((c["wav"] for c in out["new"]["audio_cache"] if c["path"].endswith(name)), None)
        assert (wav is not None) == reads_file
        assert e["output_hash"] == canonical_hash({"track": payload["audio_track_id"], "wav": wav})
        summary = json.loads(e["output_summary"])
        assert summary["wav"] == reads_file and summary["track"] == payload["audio_track_id"]
