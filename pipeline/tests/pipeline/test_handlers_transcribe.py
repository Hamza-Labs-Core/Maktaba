"""Track R3 — real TRANSCRIBE stage adapter.

The adapter is a thin glue layer mirroring :func:`probe_handler` /
:func:`extract_handler`:

1. resolve the EXTRACT-produced ``audio_cache`` row for the job's
   ``payload.content_hash`` (the cached WAV path + ``audio_track_id``),
2. select an STT backend via the registry seam (default = the
   configured/registry default; tests inject a fake backend yielding
   canned segments so no model is ever loaded),
3. create + activate the transcript row
   (:func:`stt.registry.flip_active_transcript`),
4. persist every backend segment via the existing
   :func:`stt.segment_commit.commit_segment` (heavy logic stays there),
5. advance the FSM ``AUDIO_EXTRACTED -> TRANSCRIBED`` and enqueue the
   follow-on ``SUBTITLE_GEN`` + ``INDEX`` jobs via the same idempotent
   per-video :func:`db.jobs.enqueue` ``commit_extract`` uses,
6. flip the job ``done``.

These tests assert the adapter wires those pieces together against the
*same* DB contract the libraries already expect (the shared
:class:`StageDB` fake seeded with EXTRACT-produced rows + a fake STT
backend). Async work is driven via :func:`asyncio.run` — the same
pattern ``tests/pipeline/test_handlers_extract.py`` uses, and for the
same netguard reason; these tests are intentionally NOT ``unit``-marked.
"""

from __future__ import annotations

import asyncio
import json
from collections.abc import AsyncIterator
from typing import Any
from uuid import UUID

from maktaba_pipeline.db.jobs import Stage
from maktaba_pipeline.handlers import transcribe_handler
from maktaba_pipeline.stt.protocol import Segment
from tests.audio._fake_audio_db import _AudioCacheRow, _AudioTrackRow, _ProcessingJobRow

from .conftest import StageDB, make_job

_SEGMENTS = (
    Segment(seq=0, start_sec=0.0, end_sec=2.0, text="bismillah", audio_sec=2.0, wall_sec=0.5),
    Segment(seq=1, start_sec=2.0, end_sec=4.5, text="al-hamdu", audio_sec=2.5, wall_sec=0.5),
    Segment(seq=2, start_sec=4.5, end_sec=6.0, text="lillah", audio_sec=1.5, wall_sec=0.5),
)


def _seed_claimed_job(
    stage_db: StageDB,
    *,
    job_id: int,
    video_id: UUID,
    content_hash: str,
    audio_track_id: int,
) -> None:
    """Mirror a TRANSCRIBE row the ClaimLoop flipped to ``running``.

    EXTRACT enqueued it with ``payload={audio_track_id, content_hash}``.
    """
    stage_db.processing_jobs[job_id] = _ProcessingJobRow(
        id=job_id,
        video_id=video_id,
        stage=Stage.TRANSCRIBE.value,
        state="running",
        payload=json.dumps(
            {"audio_track_id": audio_track_id, "content_hash": content_hash}
        ),
    )
    stage_db._job_next_id = max(stage_db._job_next_id, job_id + 1)  # noqa: SLF001


def _seed_extract_output(
    stage_db: StageDB,
    *,
    video_id: UUID,
    content_hash: str,
) -> int:
    """Seed exactly what EXTRACT's ``commit_extract`` would have left.

    An ``audio_tracks`` row + the ``audio_cache`` artifact keyed by
    ``content_hash`` pointing at the decoded WAV.
    """
    track_id = stage_db._audio_next_id  # noqa: SLF001
    stage_db._audio_next_id += 1  # noqa: SLF001
    stage_db.audio_tracks[track_id] = _AudioTrackRow(
        id=track_id,
        video_id=video_id,
        track_index=0,
        codec="aac",
        channels=2,
        sample_rate=48000,
        language="ara",
        title=None,
        is_default=True,
        disposition=json.dumps({"default": 1}),
        last_extracted_at=stage_db._now(),
    )
    stage_db.audio_cache[content_hash] = _AudioCacheRow(
        content_hash=content_hash,
        video_id=video_id,
        audio_track_id=track_id,
        path=f"/cache/audio/{content_hash}.wav",
        bytes=123456,
    )
    return track_id


def _fake_backend(
    name: str = "fake-stt", *, segments: tuple[Segment, ...] = _SEGMENTS
) -> Any:
    """A canonical fake STT backend yielding deterministic segments."""

    class _FakeBackend:
        def __init__(self) -> None:
            self.name = name
            self.supports_streaming = True
            self.requires_file = True
            self.cost_per_minute: float | None = 0.0
            self.supports_word_timestamps = False
            self.seen: list[tuple[object, str | None]] = []

        def transcribe(
            self, audio: Any, language: str | None, hints: Any
        ) -> AsyncIterator[Segment]:
            self.seen.append((audio, language))

            async def _gen() -> AsyncIterator[Segment]:
                for seg in segments:
                    yield seg

            return _gen()

        async def detect_language(self, audio: Any) -> str:
            return "ar"

    return _FakeBackend()


def test_transcribe_handler_selects_backend_persists_segments_advances() -> None:
    stage_db = StageDB(dialect="postgres")
    chash = "a" * 64
    video_id = stage_db.add_video(
        state="audio_extracted", path="/lib/movie.mkv", content_hash=chash
    )
    track_id = _seed_extract_output(stage_db, video_id=video_id, content_hash=chash)
    job = make_job(
        job_id=20,
        video_id=video_id,
        stage=Stage.TRANSCRIBE,
        payload={"audio_track_id": track_id, "content_hash": chash},
    )
    _seed_claimed_job(
        stage_db, job_id=20, video_id=video_id, content_hash=chash,
        audio_track_id=track_id,
    )

    backend = _fake_backend()
    picks: list[str] = []

    async def fake_pick(*, video_id: UUID) -> tuple[Any, str, str | None]:  # noqa: ARG001
        picks.append("picked")
        return backend, "fake-stt", "9.9.9"

    asyncio.run(transcribe_handler(stage_db, job, select_backend=fake_pick))

    # backend was selected via the injected registry seam.
    assert picks == ["picked"]
    # the cached WAV path was handed to the backend (requires_file).
    assert backend.seen[0][0] == f"/cache/audio/{chash}.wav"
    # a transcript row was created + activated for this video/track.
    actives = [
        t
        for t in stage_db.transcripts.values()
        if t.video_id == video_id and t.is_active
    ]
    assert len(actives) == 1
    tr = actives[0]
    assert tr.audio_track_id == track_id
    assert tr.backend == "fake-stt"
    # every backend segment was persisted via commit_segment.
    segs = sorted(
        (s for s in stage_db.transcript_segments.values() if s.transcript_id == tr.id),
        key=lambda r: r.seq,
    )
    assert [s.seq for s in segs] == [0, 1, 2]
    assert [s.text for s in segs] == ["bismillah", "al-hamdu", "lillah"]
    # FSM advanced AUDIO_EXTRACTED -> TRANSCRIBED.
    assert stage_db.videos[video_id].state == "transcribed"
    # BOTH downstream stages were enqueued.
    stages = {
        pj.stage
        for pj in stage_db.processing_jobs.values()
        if pj.video_id == video_id
    }
    assert Stage.SUBTITLE_GEN.value in stages
    assert Stage.INDEX.value in stages
    # the TRANSCRIBE job itself is done.
    assert stage_db.processing_jobs[20].state == "done"


def test_transcribe_handler_downstream_payload_contract() -> None:
    """SUBTITLE_GEN + INDEX carry the transcript id (+ track) so the
    downstream stages can locate the active transcript without a
    re-query race."""
    stage_db = StageDB(dialect="postgres")
    chash = "b" * 64
    video_id = stage_db.add_video(
        state="audio_extracted", path="/lib/m.mkv", content_hash=chash
    )
    track_id = _seed_extract_output(stage_db, video_id=video_id, content_hash=chash)
    job = make_job(
        job_id=21,
        video_id=video_id,
        stage=Stage.TRANSCRIBE,
        payload={"audio_track_id": track_id, "content_hash": chash},
    )
    _seed_claimed_job(
        stage_db, job_id=21, video_id=video_id, content_hash=chash,
        audio_track_id=track_id,
    )

    backend = _fake_backend()

    async def fake_pick(*, video_id: UUID) -> tuple[Any, str, str | None]:  # noqa: ARG001
        return backend, "fake-stt", None

    asyncio.run(transcribe_handler(stage_db, job, select_backend=fake_pick))

    tr = next(t for t in stage_db.transcripts.values() if t.is_active)
    for stage in (Stage.SUBTITLE_GEN, Stage.INDEX):
        row = next(
            pj
            for pj in stage_db.processing_jobs.values()
            if pj.stage == stage.value and pj.video_id == video_id
        )
        payload = json.loads(row.payload or "{}")
        assert payload["transcript_id"] == str(tr.id)
        assert payload["audio_track_id"] == track_id


def test_transcribe_handler_missing_audio_cache_is_non_retryable() -> None:
    """No ``audio_cache`` row for the payload hash → a data
    inconsistency (EXTRACT must have persisted it); non-retryable."""
    stage_db = StageDB(dialect="postgres")
    chash = "c" * 64
    video_id = stage_db.add_video(
        state="audio_extracted", path="/lib/m.mkv", content_hash=chash
    )
    # NOTE: no _seed_extract_output → audio_cache empty.
    job = make_job(
        job_id=22,
        video_id=video_id,
        stage=Stage.TRANSCRIBE,
        payload={"audio_track_id": 999, "content_hash": chash},
    )
    _seed_claimed_job(
        stage_db, job_id=22, video_id=video_id, content_hash=chash,
        audio_track_id=999,
    )

    async def fake_pick(*, video_id: UUID) -> tuple[Any, str, str | None]:  # noqa: ARG001  # pragma: no cover
        raise AssertionError("backend must not be picked with no audio_cache")

    asyncio.run(transcribe_handler(stage_db, job, select_backend=fake_pick))

    assert stage_db.processing_jobs[22].state == "failed"
    err = json.loads(stage_db.processing_jobs[22].error or "{}")
    assert err["retryable"] is False
    assert err["kind"] == "transcribe_missing_audio_cache"


def test_transcribe_handler_missing_payload_is_non_retryable() -> None:
    """A TRANSCRIBE job with no payload (or no content_hash) cannot
    locate its audio — a data inconsistency, non-retryable."""
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="audio_extracted")
    job = make_job(job_id=23, video_id=video_id, stage=Stage.TRANSCRIBE)
    stage_db.processing_jobs[23] = _ProcessingJobRow(
        id=23,
        video_id=video_id,
        stage=Stage.TRANSCRIBE.value,
        state="running",
        payload=None,
    )

    async def fake_pick(*, video_id: UUID) -> tuple[Any, str, str | None]:  # noqa: ARG001  # pragma: no cover
        raise AssertionError("backend must not be picked with no payload")

    asyncio.run(transcribe_handler(stage_db, job, select_backend=fake_pick))

    assert stage_db.processing_jobs[23].state == "failed"
    err = json.loads(stage_db.processing_jobs[23].error or "{}")
    assert err["retryable"] is False
    assert err["kind"] == "transcribe_missing_payload"


def test_transcribe_handler_backend_error_is_retryable() -> None:
    """A transient backend / IO failure mid-stream is retryable."""
    stage_db = StageDB(dialect="postgres")
    chash = "d" * 64
    video_id = stage_db.add_video(
        state="audio_extracted", path="/lib/m.mkv", content_hash=chash
    )
    track_id = _seed_extract_output(stage_db, video_id=video_id, content_hash=chash)
    job = make_job(
        job_id=24,
        video_id=video_id,
        stage=Stage.TRANSCRIBE,
        payload={"audio_track_id": track_id, "content_hash": chash},
    )
    _seed_claimed_job(
        stage_db, job_id=24, video_id=video_id, content_hash=chash,
        audio_track_id=track_id,
    )

    class _BoomBackend:
        name = "boom"
        supports_streaming = True
        requires_file = True
        cost_per_minute = 0.0
        supports_word_timestamps = False

        def transcribe(
            self, audio: Any, language: str | None, hints: Any
        ) -> AsyncIterator[Segment]:
            async def _gen() -> AsyncIterator[Segment]:
                yield _SEGMENTS[0]
                raise OSError("backend pipe broke")

            return _gen()

        async def detect_language(self, audio: Any) -> str:
            return "ar"

    async def fake_pick(*, video_id: UUID) -> tuple[Any, str, str | None]:  # noqa: ARG001
        return _BoomBackend(), "boom", None

    asyncio.run(transcribe_handler(stage_db, job, select_backend=fake_pick))

    # attempts(1) < max_attempts(3) and retryable → pending.
    assert stage_db.processing_jobs[24].state == "pending"
    # FSM stayed put — no premature TRANSCRIBED.
    assert stage_db.videos[video_id].state == "audio_extracted"


def test_transcribe_handler_idempotent_on_rerun() -> None:
    """Re-running TRANSCRIBE (retried job) is replay-safe: the FSM
    advance is a no-op past AUDIO_EXTRACTED and the downstream enqueue
    dedupes — no duplicate SUBTITLE_GEN/INDEX rows."""
    stage_db = StageDB(dialect="postgres")
    chash = "e" * 64
    video_id = stage_db.add_video(
        state="audio_extracted", path="/lib/m.mkv", content_hash=chash
    )
    track_id = _seed_extract_output(stage_db, video_id=video_id, content_hash=chash)

    async def fake_pick(*, video_id: UUID) -> tuple[Any, str, str | None]:  # noqa: ARG001
        return _fake_backend(), "fake-stt", None

    job1 = make_job(
        job_id=25,
        video_id=video_id,
        stage=Stage.TRANSCRIBE,
        payload={"audio_track_id": track_id, "content_hash": chash},
    )
    _seed_claimed_job(
        stage_db, job_id=25, video_id=video_id, content_hash=chash,
        audio_track_id=track_id,
    )
    asyncio.run(transcribe_handler(stage_db, job1, select_backend=fake_pick))

    job2 = make_job(
        job_id=26,
        video_id=video_id,
        stage=Stage.TRANSCRIBE,
        payload={"audio_track_id": track_id, "content_hash": chash},
    )
    _seed_claimed_job(
        stage_db, job_id=26, video_id=video_id, content_hash=chash,
        audio_track_id=track_id,
    )
    asyncio.run(transcribe_handler(stage_db, job2, select_backend=fake_pick))

    assert stage_db.processing_jobs[25].state == "done"
    assert stage_db.processing_jobs[26].state == "done"
    assert stage_db.videos[video_id].state == "transcribed"
    sg = [
        pj
        for pj in stage_db.processing_jobs.values()
        if pj.stage == Stage.SUBTITLE_GEN.value
    ]
    idx = [
        pj
        for pj in stage_db.processing_jobs.values()
        if pj.stage == Stage.INDEX.value
    ]
    assert len(sg) == 1
    assert len(idx) == 1
