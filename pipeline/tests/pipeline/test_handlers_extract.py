"""Track R2 — real EXTRACT stage adapter.

The adapter is a thin glue layer mirroring :func:`probe_handler`:

1. resolve ``videos.path`` + ``videos.content_hash`` for the job's
   ``video_id`` (the source media + the audio-cache key — the latter
   produced by the scanner, the former still on the row),
2. read back the ``audio_tracks`` rows PROBE persisted and pick a
   track via :func:`audio.track_selection.select_tracks`,
3. run the extractor (DI seam: tests inject a fake that returns a
   cache path so no ffmpeg subprocess is spawned),
4. hand the result to :func:`audio.extract.commit_extract` (which
   UPSERTs the ``audio_cache`` artifact row, stamps
   ``audio_tracks.last_extracted_at``, advances the FSM
   ``PROBED -> AUDIO_EXTRACTED``, and enqueues the follow-on
   TRANSCRIBE job — exactly mirroring ``commit_probe``),
5. flip the job ``done``.

These tests assert the adapter wires those pieces together against
the *same* DB contract the libraries already expect (the shared
:class:`StageDB` fake seeded with PROBE-produced rows). Async work is
driven via :func:`asyncio.run` rather than ``@pytest.mark.asyncio`` —
the same pattern ``tests/pipeline/test_handlers_probe.py`` uses, and
for the same netguard reason; these tests are intentionally NOT
``unit``-marked.
"""

from __future__ import annotations

import asyncio
import json
from pathlib import Path
from uuid import UUID, uuid4

from maktaba_pipeline.db.jobs import Stage
from maktaba_pipeline.handlers import extract_handler
from tests.audio._fake_audio_db import _AudioTrackRow, _ProcessingJobRow

from .conftest import StageDB, make_job


def _seed_claimed_job(
    stage_db: StageDB, *, job_id: int, video_id: UUID, stage: Stage = Stage.EXTRACT
) -> None:
    """Mirror a row the ClaimLoop would have flipped to ``running``."""
    stage_db.processing_jobs[job_id] = _ProcessingJobRow(
        id=job_id, video_id=video_id, stage=stage.value, state="running"
    )
    stage_db._job_next_id = max(stage_db._job_next_id, job_id + 1)  # noqa: SLF001


def _seed_audio_track(
    stage_db: StageDB,
    *,
    video_id: UUID,
    track_index: int = 0,
    language: str = "ara",
    is_default: bool = True,
    channels: int = 2,
    disposition: dict[str, int] | None = None,
) -> int:
    """Insert an ``audio_tracks`` row exactly as ``commit_probe`` would."""
    tid = stage_db._audio_next_id  # noqa: SLF001
    stage_db._audio_next_id += 1  # noqa: SLF001
    stage_db.audio_tracks[tid] = _AudioTrackRow(
        id=tid,
        video_id=video_id,
        track_index=track_index,
        codec="aac",
        channels=channels,
        sample_rate=48000,
        language=language,
        title=None,
        is_default=is_default,
        disposition=json.dumps(disposition or {"default": 1 if is_default else 0}),
    )
    return tid


def test_extract_handler_extracts_persists_and_enqueues_transcribe() -> None:
    stage_db = StageDB(dialect="postgres")
    chash = "b" * 64
    video_id = stage_db.add_video(
        state="probed", path="/lib/movie.mkv", content_hash=chash
    )
    track_id = _seed_audio_track(stage_db, video_id=video_id)
    job = make_job(job_id=7, video_id=video_id, stage=Stage.EXTRACT)
    _seed_claimed_job(stage_db, job_id=7, video_id=video_id)

    calls: list[tuple[str, int, str]] = []

    async def fake_extract(path: str, track_index: int, *, content_hash: str) -> Path:
        calls.append((path, track_index, content_hash))
        return Path(f"/cache/audio/{content_hash}.wav")

    asyncio.run(extract_handler(stage_db, job, run_extract=fake_extract))

    # extractor was driven with the resolved source path, the selected
    # audio-track index, and the content-hash cache key.
    assert calls == [("/lib/movie.mkv", 0, chash)]
    # the artifact reference was persisted into audio_cache.
    assert chash in stage_db.audio_cache
    art = stage_db.audio_cache[chash]
    assert art.video_id == video_id
    assert art.audio_track_id == track_id
    assert art.path == f"/cache/audio/{chash}.wav"
    # audio_tracks.last_extracted_at was stamped.
    assert stage_db.audio_tracks[track_id].last_extracted_at is not None
    # FSM advanced PROBED -> AUDIO_EXTRACTED.
    assert stage_db.videos[video_id].state == "audio_extracted"
    # the follow-on TRANSCRIBE job was enqueued.
    assert any(
        pj.video_id == video_id and pj.stage == Stage.TRANSCRIBE.value
        for pj in stage_db.processing_jobs.values()
    )
    # the EXTRACT job itself is done.
    assert stage_db.processing_jobs[7].state == "done"


def test_extract_handler_selects_track_via_policy() -> None:
    """Two tracks: the language/default policy picks the Arabic one."""
    stage_db = StageDB(dialect="postgres")
    chash = "c" * 64
    video_id = stage_db.add_video(
        state="probed", path="/lib/multi.mkv", content_hash=chash
    )
    # index 0 is English (default-flagged), index 1 is Arabic. The
    # selection rule prefers Arabic over the container default.
    _seed_audio_track(
        stage_db,
        video_id=video_id,
        track_index=0,
        language="eng",
        is_default=True,
        disposition={"default": 1},
    )
    ara_id = _seed_audio_track(
        stage_db,
        video_id=video_id,
        track_index=1,
        language="ara",
        is_default=False,
        disposition={"default": 0},
    )
    job = make_job(job_id=8, video_id=video_id, stage=Stage.EXTRACT)
    _seed_claimed_job(stage_db, job_id=8, video_id=video_id)

    seen: list[int] = []

    async def fake_extract(path: str, track_index: int, *, content_hash: str) -> Path:
        seen.append(track_index)
        return Path(f"/cache/{content_hash}.wav")

    asyncio.run(extract_handler(stage_db, job, run_extract=fake_extract))

    # Arabic track (audio-rank index 1) is selected, not the default eng.
    assert seen == [1]
    assert stage_db.audio_cache[chash].audio_track_id == ara_id


def test_extract_handler_missing_source_path_fails_non_retryable() -> None:
    stage_db = StageDB(dialect="postgres")
    video_id = uuid4()  # no videos row → unresolvable source / hash
    _seed_audio_track(stage_db, video_id=video_id)
    job = make_job(job_id=9, video_id=video_id, stage=Stage.EXTRACT)
    _seed_claimed_job(stage_db, job_id=9, video_id=video_id)

    async def fake_extract(*_a: object, **_k: object) -> Path:  # pragma: no cover
        raise AssertionError("extractor must not run with no source path")

    asyncio.run(extract_handler(stage_db, job, run_extract=fake_extract))

    assert stage_db.processing_jobs[9].state == "failed"
    err = json.loads(stage_db.processing_jobs[9].error or "{}")
    assert err["retryable"] is False
    assert err["kind"] == "extract_missing_source"


def test_extract_handler_no_audio_tracks_fails_non_retryable() -> None:
    """PROBE should have skipped EXTRACT for silent media; if an EXTRACT
    job exists with zero audio_tracks rows that is an unrecoverable
    inconsistency, not a transient error."""
    stage_db = StageDB(dialect="postgres")
    chash = "d" * 64
    video_id = stage_db.add_video(
        state="probed", path="/lib/silent.mkv", content_hash=chash
    )
    job = make_job(job_id=10, video_id=video_id, stage=Stage.EXTRACT)
    _seed_claimed_job(stage_db, job_id=10, video_id=video_id)

    async def fake_extract(*_a: object, **_k: object) -> Path:  # pragma: no cover
        raise AssertionError("extractor must not run with no audio tracks")

    asyncio.run(extract_handler(stage_db, job, run_extract=fake_extract))

    assert stage_db.processing_jobs[10].state == "failed"
    err = json.loads(stage_db.processing_jobs[10].error or "{}")
    assert err["retryable"] is False
    assert err["kind"] == "extract_no_audio_track"


def test_extract_handler_ffmpeg_failure_is_retryable() -> None:
    stage_db = StageDB(dialect="postgres")
    chash = "e" * 64
    video_id = stage_db.add_video(
        state="probed", path="/lib/movie.mkv", content_hash=chash
    )
    _seed_audio_track(stage_db, video_id=video_id)
    job = make_job(job_id=11, video_id=video_id, stage=Stage.EXTRACT)
    _seed_claimed_job(stage_db, job_id=11, video_id=video_id)

    from maktaba_pipeline.audio.extract import ExtractError

    async def fake_extract(*_a: object, **_k: object) -> Path:
        raise ExtractError("ffmpeg_decode", returncode=183, stderr_tail="boom")

    asyncio.run(extract_handler(stage_db, job, run_extract=fake_extract))

    # attempts(1) < max_attempts(3) and the error is retryable → pending.
    assert stage_db.processing_jobs[11].state == "pending"
    assert chash not in stage_db.audio_cache


def test_extract_handler_video_vanished_midflight_is_terminal() -> None:
    """TOCTOU: the video row is present at the source SELECT but gone by
    the time ``commit_extract`` reads ``videos.state`` (it raises
    ``LookupError``). That is unrecoverable — classify it non-retryable
    so no attempt is wasted before the retry would hit the
    missing-source guard anyway."""
    import pytest as _pytest

    from maktaba_pipeline.audio import extract as _extract_mod

    stage_db = StageDB(dialect="postgres")
    chash = "9" * 64
    video_id = stage_db.add_video(
        state="probed", path="/lib/movie.mkv", content_hash=chash
    )
    _seed_audio_track(stage_db, video_id=video_id)
    job = make_job(job_id=14, video_id=video_id, stage=Stage.EXTRACT)
    _seed_claimed_job(stage_db, job_id=14, video_id=video_id)

    async def fake_extract(path: str, track_index: int, *, content_hash: str) -> Path:
        return Path(f"/cache/{content_hash}.wav")

    async def vanished_commit(*_a: object, **_k: object) -> str:
        raise LookupError(f"video {video_id} not found")

    with _pytest.MonkeyPatch.context() as mp:
        mp.setattr(_extract_mod, "commit_extract", vanished_commit)
        asyncio.run(extract_handler(stage_db, job, run_extract=fake_extract))

    # terminal failure, non-retryable, with the dedicated kind — and
    # crucially still on attempt 1 (no wasted retry).
    assert stage_db.processing_jobs[14].state == "failed"
    err = json.loads(stage_db.processing_jobs[14].error or "{}")
    assert err["retryable"] is False
    assert err["kind"] == "extract_video_vanished"


def test_extract_handler_idempotent_on_rerun() -> None:
    """Re-running EXTRACT (e.g. a retried job) is a clean no-op-ish:
    the artifact UPSERT and the FSM advance both tolerate a repeat."""
    stage_db = StageDB(dialect="postgres")
    chash = "f" * 64
    video_id = stage_db.add_video(
        state="probed", path="/lib/movie.mkv", content_hash=chash
    )
    _seed_audio_track(stage_db, video_id=video_id)

    async def fake_extract(path: str, track_index: int, *, content_hash: str) -> Path:
        return Path(f"/cache/{content_hash}.wav")

    job1 = make_job(job_id=12, video_id=video_id, stage=Stage.EXTRACT)
    _seed_claimed_job(stage_db, job_id=12, video_id=video_id)
    asyncio.run(extract_handler(stage_db, job1, run_extract=fake_extract))

    job2 = make_job(job_id=13, video_id=video_id, stage=Stage.EXTRACT)
    _seed_claimed_job(stage_db, job_id=13, video_id=video_id)
    asyncio.run(extract_handler(stage_db, job2, run_extract=fake_extract))

    assert stage_db.processing_jobs[12].state == "done"
    assert stage_db.processing_jobs[13].state == "done"
    assert stage_db.videos[video_id].state == "audio_extracted"
    # Exactly one TRANSCRIBE row — the enqueue idempotency held.
    transcribe_rows = [
        pj
        for pj in stage_db.processing_jobs.values()
        if pj.stage == Stage.TRANSCRIBE.value
    ]
    assert len(transcribe_rows) == 1
