"""Story 2.3 follow-on — :func:`audio.extract.commit_extract` DB writes.

``commit_extract`` is the EXTRACT-stage analogue of ``commit_probe``:
it persists the extracted-audio artifact into ``audio_cache``, stamps
``audio_tracks.last_extracted_at``, advances the video FSM
``PROBED -> AUDIO_EXTRACTED``, and enqueues the follow-on TRANSCRIBE
job. Same canonical :class:`FakeAudioDB`, same ``asyncio.run`` driver
the ``commit_probe`` tests use.
"""

from __future__ import annotations

import asyncio
from uuid import UUID, uuid4

from maktaba_pipeline.audio.extract import commit_extract

from ._fake_audio_db import FakeAudioDB, _AudioTrackRow


def _seed_track(db: FakeAudioDB, video_id: UUID, *, track_index: int = 0) -> int:
    tid = db._audio_next_id  # noqa: SLF001
    db._audio_next_id += 1  # noqa: SLF001
    db.audio_tracks[tid] = _AudioTrackRow(
        id=tid,
        video_id=video_id,
        track_index=track_index,
        codec="aac",
        channels=2,
        sample_rate=48000,
        language="ara",
        title=None,
        is_default=True,
        disposition='{"default": 1}',
    )
    return tid


def test_commit_extract_persists_artifact_advances_and_enqueues() -> None:
    db = FakeAudioDB()
    vid = db.add_video(state="probed")
    tid = _seed_track(db, vid)

    new_state = asyncio.run(
        commit_extract(
            db,
            video_id=vid,
            audio_track_id=tid,
            content_hash="a" * 64,
            cache_path="/cache/audio/aaaa.wav",
            bytes_written=4096,
        )
    )

    assert new_state == "audio_extracted"
    assert db.videos[vid].state == "audio_extracted"
    assert ("a" * 64) in db.audio_cache
    row = db.audio_cache["a" * 64]
    assert row.video_id == vid
    assert row.audio_track_id == tid
    assert row.path == "/cache/audio/aaaa.wav"
    assert row.bytes == 4096
    assert db.audio_tracks[tid].last_extracted_at is not None
    assert any(
        j.stage == "transcribe" and j.state == "pending"
        for j in db.processing_jobs.values()
    )


def test_commit_extract_idempotent_on_replay() -> None:
    db = FakeAudioDB()
    vid = db.add_video(state="probed")
    tid = _seed_track(db, vid)

    asyncio.run(
        commit_extract(
            db,
            video_id=vid,
            audio_track_id=tid,
            content_hash="b" * 64,
            cache_path="/cache/b.wav",
            bytes_written=1,
        )
    )
    # Re-run: the late_stage_finish guard makes the FSM advance a no-op
    # and the enqueue idempotency keeps a single TRANSCRIBE row.
    asyncio.run(
        commit_extract(
            db,
            video_id=vid,
            audio_track_id=tid,
            content_hash="b" * 64,
            cache_path="/cache/b.wav",
            bytes_written=1,
        )
    )

    assert db.videos[vid].state == "audio_extracted"
    transcribe = [j for j in db.processing_jobs.values() if j.stage == "transcribe"]
    assert len(transcribe) == 1


def test_commit_extract_missing_video_raises() -> None:
    db = FakeAudioDB()
    missing = uuid4()
    try:
        asyncio.run(
            commit_extract(
                db,
                video_id=missing,
                audio_track_id=1,
                content_hash="c" * 64,
                cache_path="/cache/c.wav",
                bytes_written=1,
            )
        )
    except LookupError:
        pass
    else:  # pragma: no cover
        raise AssertionError("expected LookupError for missing video row")
