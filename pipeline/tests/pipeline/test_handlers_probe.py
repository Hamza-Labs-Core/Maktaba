"""Story R1.2 — real PROBE stage adapter.

The adapter is a thin glue layer:

1. resolve ``videos.path`` for the job's ``video_id``,
2. run ffprobe (DI seam: tests inject a fake that returns a curated
   :class:`ProbeResult` so no subprocess is spawned),
3. hand the result to the already-tested :func:`audio.probe.commit_probe`
   (which persists media_info / audio_tracks, advances the FSM, and
   enqueues the follow-on EXTRACT job),
4. flip the job ``done``.

These tests assert the adapter wires those pieces together against the
*same* DB contract ``commit_probe`` already expects (the shared
:class:`StageDB` fake). Async work is driven via :func:`asyncio.run`
rather than ``@pytest.mark.asyncio`` — the same pattern
``tests/audio/test_probe.py`` uses. These tests are intentionally NOT
``unit``-marked: ``asyncio.run`` opens the event loop's self-pipe
socketpair which the unit-tier netguard (Story 20.1 AC1) forbids, the
same caveat documented in ``tests/pipeline/test_runner.py`` and shared
by every async DB test in the suite.
"""

from __future__ import annotations

import asyncio
from uuid import UUID, uuid4

from maktaba_pipeline.audio.probe import AudioTrack, MediaInfo, ProbeResult
from maktaba_pipeline.db.jobs import Stage
from maktaba_pipeline.handlers import probe_handler
from tests.audio._fake_audio_db import _ProcessingJobRow

from .conftest import StageDB, make_job


def _seed_claimed_job(stage_db: StageDB, *, job_id: int, video_id: UUID) -> None:
    """Mirror a row the ClaimLoop would have flipped to ``running``.

    The adapter's terminal ``mark_done`` / ``mark_failed_or_retry``
    UPDATEs gate on ``state IN ('claimed','running','resuming')``, so
    the fake needs the row present in that state — exactly what the
    real queue guarantees by the time dispatch runs.
    """
    stage_db.processing_jobs[job_id] = _ProcessingJobRow(
        id=job_id, video_id=video_id, stage=Stage.PROBE.value, state="running"
    )
    # Keep the fake's autoincrement ahead of the seeded row so the
    # follow-on EXTRACT enqueue() gets a fresh id rather than colliding.
    stage_db._job_next_id = max(stage_db._job_next_id, job_id + 1)  # noqa: SLF001


def _result(*, with_audio: bool) -> ProbeResult:
    media = MediaInfo(
        container="matroska,webm",
        video_codec="h264",
        width=1920,
        height=1080,
        fps=24.0,
        bitrate_kbps=4000,
        has_subtitles=False,
        raw_ffprobe={"format": {}, "streams": []},
    )
    audio = (
        [
            AudioTrack(
                index=0,
                codec="aac",
                channels=2,
                sample_rate=48000,
                language="ara",
                title=None,
                is_default=True,
                disposition={"default": 1},
            )
        ]
        if with_audio
        else []
    )
    return ProbeResult(media=media, audio=audio)


def test_probe_handler_persists_and_enqueues_extract() -> None:
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="discovered", path="/lib/movie.mkv")
    job = make_job(job_id=1, video_id=video_id, stage=Stage.PROBE)
    _seed_claimed_job(stage_db, job_id=1, video_id=video_id)

    seen_paths: list[str] = []

    async def fake_probe(path: str) -> ProbeResult:
        seen_paths.append(path)
        return _result(with_audio=True)

    asyncio.run(probe_handler(stage_db, job, run_probe=fake_probe))

    # ffprobe was driven against the resolved source path.
    assert seen_paths == ["/lib/movie.mkv"]
    # commit_probe wrote media_info for the video.
    assert video_id in stage_db.media_info
    assert stage_db.media_info[video_id].container == "matroska,webm"
    # ...and the audio track.
    assert any(t.video_id == video_id for t in stage_db.audio_tracks.values())
    # commit_probe enqueued the follow-on EXTRACT job.
    assert any(
        pj.video_id == video_id and pj.stage == Stage.EXTRACT.value
        for pj in stage_db.processing_jobs.values()
    )
    # the job itself is done.
    assert stage_db.processing_jobs[1].state == "done"


def test_probe_handler_no_audio_skips_extract() -> None:
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="discovered", path="/lib/silent.mkv")
    job = make_job(job_id=1, video_id=video_id, stage=Stage.PROBE)
    _seed_claimed_job(stage_db, job_id=1, video_id=video_id)

    async def fake_probe(_path: str) -> ProbeResult:
        return _result(with_audio=False)

    asyncio.run(probe_handler(stage_db, job, run_probe=fake_probe))

    assert not any(
        pj.stage == Stage.EXTRACT.value for pj in stage_db.processing_jobs.values()
    )
    assert stage_db.processing_jobs[1].state == "done"


def test_probe_handler_missing_video_path_fails_job() -> None:
    stage_db = StageDB(dialect="postgres")
    # Job references a video with no row → unresolvable source path.
    video_id = uuid4()
    job = make_job(job_id=1, video_id=video_id, stage=Stage.PROBE)
    _seed_claimed_job(stage_db, job_id=1, video_id=video_id)

    async def fake_probe(_path: str) -> ProbeResult:  # pragma: no cover - must not run
        raise AssertionError("probe must not run when the source path is missing")

    asyncio.run(probe_handler(stage_db, job, run_probe=fake_probe))

    assert stage_db.processing_jobs[1].state == "failed"
