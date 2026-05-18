"""Track R4 — real SUBTITLE_GEN stage adapter.

The adapter is a thin glue layer mirroring :func:`probe_handler` /
:func:`extract_handler` / :func:`transcribe_handler`:

1. resolve the TRANSCRIBE-produced transcript for the job's
   ``payload.transcript_id`` (the exact transcript + its language),
2. load every ordered ``transcript_segments`` row and project it into
   the renderers' cue shape via the existing ``segments_to_cues``,
3. render SRT + VTT via the existing pure ``generate_srt`` /
   ``generate_vtt`` (rendering logic is NOT reimplemented),
4. persist both artifacts via the pre-existing ``write_atomic`` +
   ``register_subtitle`` (the ``subtitle_files`` registry — the
   EXTRACT-``audio_cache`` analogue),
5. advance the FSM ``TRANSCRIBED -> INDEXED`` (replay-guarded; the
   shared edge with the parallel INDEX stage means the second of the
   two no-ops),
6. flip the job ``done``.

These tests assert the adapter wires those pieces together against the
*same* DB contract the libraries already expect (the shared
:class:`StageDB` fake seeded with a transcript as TRANSCRIBE would
leave it). Async work is driven via :func:`asyncio.run` — the same
pattern ``tests/pipeline/test_handlers_transcribe.py`` uses, and for
the same netguard reason; these tests are intentionally NOT
``unit``-marked.
"""

from __future__ import annotations

import asyncio
import json
from uuid import UUID, uuid4

from maktaba_pipeline.db.jobs import Stage
from maktaba_pipeline.handlers import subtitle_handler
from maktaba_pipeline.subtitle.formats import SubtitleFormat
from maktaba_pipeline.subtitle.manager import SubtitleSource, cache_path_for
from tests.audio._fake_audio_db import _ProcessingJobRow

from .conftest import StageDB, make_job


def _seed_claimed_job(
    stage_db: StageDB,
    *,
    job_id: int,
    video_id: UUID,
    transcript_id: UUID | str,
    audio_track_id: int = 1,
) -> None:
    """Mirror a SUBTITLE_GEN row the ClaimLoop flipped to ``running``.

    TRANSCRIBE enqueued it with
    ``payload={transcript_id, audio_track_id}``.
    """
    stage_db.processing_jobs[job_id] = _ProcessingJobRow(
        id=job_id,
        video_id=video_id,
        stage=Stage.SUBTITLE_GEN.value,
        state="running",
        payload=json.dumps({"transcript_id": str(transcript_id), "audio_track_id": audio_track_id}),
    )
    stage_db._job_next_id = max(stage_db._job_next_id, job_id + 1)  # noqa: SLF001


def test_subtitle_handler_renders_persists_and_advances(tmp_path: object) -> None:
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="transcribed")
    tid = stage_db.seed_transcript(
        video_id=video_id,
        language="ara",
        segments=[
            (0, 0.0, 2.0, "bismillah"),
            (1, 2.0, 4.5, "al-hamdu lillah"),
            (2, 4.5, 6.0, "rabbi al-alamin"),
        ],
    )
    job = make_job(
        job_id=40,
        video_id=video_id,
        stage=Stage.SUBTITLE_GEN,
        payload={"transcript_id": str(tid), "audio_track_id": 1},
    )
    _seed_claimed_job(stage_db, job_id=40, video_id=video_id, transcript_id=tid)

    asyncio.run(subtitle_handler(stage_db, job))

    # Both formats were registered in subtitle_files for this video,
    # source=generated, pointing back at the transcript.
    rows = [r for r in stage_db.subtitle_files.values() if r.video_id == video_id]
    fmts = {r.format for r in rows}
    assert fmts == {"srt", "vtt"}
    for r in rows:
        assert r.source == "generated"
        assert r.language == "ara"
        assert r.transcript_id == tid
        assert r.is_embedded is False
        assert r.is_external is False
        # The deterministic content-addressed sidecar was written.
        expected = cache_path_for(
            video_id,
            "ara",
            SubtitleFormat(r.format),
            SubtitleSource.GENERATED,
        )
        assert r.path == str(expected)
        content = expected.read_text(encoding="utf-8")
        # Spot-check real rendered content + timestamps.
        assert "bismillah" in content
        assert "rabbi al-alamin" in content
        if r.format == "srt":
            # SRT: 1-based index, comma-decimal timestamps.
            assert "00:00:00,000 --> 00:00:02,000" in content
            assert content.startswith("1\n")
        else:
            assert content.startswith("WEBVTT")
            assert "00:00:00.000 --> 00:00:02.000" in content
        # cleanup the real file the deterministic path wrote.
        expected.unlink()

    # FSM advanced TRANSCRIBED -> INDEXED.
    assert stage_db.videos[video_id].state == "indexed"
    # the SUBTITLE_GEN job itself is done.
    assert stage_db.processing_jobs[40].state == "done"


def test_subtitle_handler_segments_loaded_in_order() -> None:
    """Out-of-insertion-order seed must still render seq-ordered cues."""
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="transcribed")
    tid = stage_db.seed_transcript(
        video_id=video_id,
        language="eng",
        # Deliberately seeded out of seq order.
        segments=[
            (2, 4.0, 6.0, "third"),
            (0, 0.0, 2.0, "first"),
            (1, 2.0, 4.0, "second"),
        ],
    )
    job = make_job(
        job_id=41,
        video_id=video_id,
        stage=Stage.SUBTITLE_GEN,
        payload={"transcript_id": str(tid), "audio_track_id": 1},
    )
    _seed_claimed_job(stage_db, job_id=41, video_id=video_id, transcript_id=tid)

    asyncio.run(subtitle_handler(stage_db, job))

    srt_row = next(
        r for r in stage_db.subtitle_files.values() if r.video_id == video_id and r.format == "srt"
    )
    from pathlib import Path

    content = Path(srt_row.path).read_text(encoding="utf-8")
    Path(srt_row.path).unlink()
    # The cues must be in seq order, not insertion order.
    assert content.index("first") < content.index("second") < content.index("third")
    assert "1\n00:00:00,000 --> 00:00:02,000\nfirst" in content


def test_subtitle_handler_idempotent_on_rerun() -> None:
    """Re-running SUBTITLE_GEN (retried job) is replay-safe: the FSM
    advance is a no-op past TRANSCRIBED and the subtitle_files UPSERT
    dedupes — no duplicate artifact rows."""
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="transcribed")
    tid = stage_db.seed_transcript(video_id=video_id, language="ara")

    job1 = make_job(
        job_id=42,
        video_id=video_id,
        stage=Stage.SUBTITLE_GEN,
        payload={"transcript_id": str(tid), "audio_track_id": 1},
    )
    _seed_claimed_job(stage_db, job_id=42, video_id=video_id, transcript_id=tid)
    asyncio.run(subtitle_handler(stage_db, job1))

    job2 = make_job(
        job_id=43,
        video_id=video_id,
        stage=Stage.SUBTITLE_GEN,
        payload={"transcript_id": str(tid), "audio_track_id": 1},
    )
    _seed_claimed_job(stage_db, job_id=43, video_id=video_id, transcript_id=tid)
    asyncio.run(subtitle_handler(stage_db, job2))

    assert stage_db.processing_jobs[42].state == "done"
    assert stage_db.processing_jobs[43].state == "done"
    assert stage_db.videos[video_id].state == "indexed"
    rows = [r for r in stage_db.subtitle_files.values() if r.video_id == video_id]
    # Still exactly two rows (srt + vtt) — the UPSERT deduped.
    assert len(rows) == 2
    assert {r.format for r in rows} == {"srt", "vtt"}
    # cleanup
    from pathlib import Path

    for r in rows:
        Path(r.path).unlink(missing_ok=True)


def test_subtitle_handler_replay_guarded_when_already_indexed() -> None:
    """When the parallel INDEX stage already advanced the video to
    INDEXED, SUBTITLE_GEN must NOT raise IllegalStateTransition — it
    no-ops the advance and still persists its artifacts + marks done."""
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="indexed")  # INDEX already won.
    tid = stage_db.seed_transcript(video_id=video_id, language="ara")
    job = make_job(
        job_id=44,
        video_id=video_id,
        stage=Stage.SUBTITLE_GEN,
        payload={"transcript_id": str(tid), "audio_track_id": 1},
    )
    _seed_claimed_job(stage_db, job_id=44, video_id=video_id, transcript_id=tid)

    asyncio.run(subtitle_handler(stage_db, job))

    assert stage_db.processing_jobs[44].state == "done"
    assert stage_db.videos[video_id].state == "indexed"
    rows = [r for r in stage_db.subtitle_files.values() if r.video_id == video_id]
    assert {r.format for r in rows} == {"srt", "vtt"}
    from pathlib import Path

    for r in rows:
        Path(r.path).unlink(missing_ok=True)


def test_subtitle_handler_missing_payload_is_non_retryable() -> None:
    """A SUBTITLE_GEN job with no payload (or no transcript_id) cannot
    locate its transcript — a data inconsistency, non-retryable."""
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="transcribed")
    job = make_job(job_id=45, video_id=video_id, stage=Stage.SUBTITLE_GEN)
    stage_db.processing_jobs[45] = _ProcessingJobRow(
        id=45,
        video_id=video_id,
        stage=Stage.SUBTITLE_GEN.value,
        state="running",
        payload=None,
    )

    asyncio.run(subtitle_handler(stage_db, job))

    assert stage_db.processing_jobs[45].state == "failed"
    err = json.loads(stage_db.processing_jobs[45].error or "{}")
    assert err["retryable"] is False
    assert err["kind"] == "subtitle_missing_payload"


def test_subtitle_handler_missing_transcript_is_non_retryable() -> None:
    """No transcript row for the payload id → a data inconsistency
    (TRANSCRIBE must have activated it); non-retryable."""
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="transcribed")
    ghost = uuid4()
    job = make_job(
        job_id=46,
        video_id=video_id,
        stage=Stage.SUBTITLE_GEN,
        payload={"transcript_id": str(ghost), "audio_track_id": 1},
    )
    _seed_claimed_job(stage_db, job_id=46, video_id=video_id, transcript_id=ghost)

    asyncio.run(subtitle_handler(stage_db, job))

    assert stage_db.processing_jobs[46].state == "failed"
    err = json.loads(stage_db.processing_jobs[46].error or "{}")
    assert err["retryable"] is False
    assert err["kind"] == "subtitle_missing_transcript"


def test_subtitle_handler_no_segments_is_non_retryable() -> None:
    """A transcript with zero renderable segments → non-retryable
    (TRANSCRIBE only enqueues after a complete transcript)."""
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="transcribed")
    tid = stage_db.seed_transcript(video_id=video_id, language="ara", segments=[])
    job = make_job(
        job_id=47,
        video_id=video_id,
        stage=Stage.SUBTITLE_GEN,
        payload={"transcript_id": str(tid), "audio_track_id": 1},
    )
    _seed_claimed_job(stage_db, job_id=47, video_id=video_id, transcript_id=tid)

    asyncio.run(subtitle_handler(stage_db, job))

    assert stage_db.processing_jobs[47].state == "failed"
    err = json.loads(stage_db.processing_jobs[47].error or "{}")
    assert err["retryable"] is False
    assert err["kind"] == "subtitle_missing_transcript"


def test_subtitle_handler_bad_transcript_id_is_non_retryable() -> None:
    """A payload.transcript_id that is not a UUID → non-retryable."""
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="transcribed")
    job = make_job(
        job_id=48,
        video_id=video_id,
        stage=Stage.SUBTITLE_GEN,
        payload={"transcript_id": "not-a-uuid", "audio_track_id": 1},
    )
    stage_db.processing_jobs[48] = _ProcessingJobRow(
        id=48,
        video_id=video_id,
        stage=Stage.SUBTITLE_GEN.value,
        state="running",
        payload=json.dumps({"transcript_id": "not-a-uuid", "audio_track_id": 1}),
    )

    asyncio.run(subtitle_handler(stage_db, job))

    assert stage_db.processing_jobs[48].state == "failed"
    err = json.loads(stage_db.processing_jobs[48].error or "{}")
    assert err["retryable"] is False
    assert err["kind"] == "subtitle_bad_payload"


def test_subtitle_handler_write_failure_is_retryable(monkeypatch: object) -> None:
    """A transient write/IO failure mid-persist is retryable and the
    FSM does not advance."""
    stage_db = StageDB(dialect="postgres")
    video_id = stage_db.add_video(state="transcribed")
    tid = stage_db.seed_transcript(video_id=video_id, language="ara")
    job = make_job(
        job_id=49,
        video_id=video_id,
        stage=Stage.SUBTITLE_GEN,
        payload={"transcript_id": str(tid), "audio_track_id": 1},
    )
    _seed_claimed_job(stage_db, job_id=49, video_id=video_id, transcript_id=tid)

    import maktaba_pipeline.subtitle.subtitle_gen as sg

    def _boom(*_a: object, **_kw: object) -> tuple[int, str]:
        raise OSError("disk full")

    monkeypatch.setattr(  # type: ignore[attr-defined]
        "maktaba_pipeline.subtitle.manager.write_atomic", _boom
    )
    # the commit imports write_atomic lazily from manager, so patching
    # the manager attribute is sufficient.
    assert sg is not None

    asyncio.run(subtitle_handler(stage_db, job))

    # attempts(1) < max_attempts(3) and retryable → pending.
    assert stage_db.processing_jobs[49].state == "pending"
    err = json.loads(stage_db.processing_jobs[49].error or "{}")
    assert err["retryable"] is True
    assert err["kind"] == "subtitle_failed"
    # FSM stayed put — no premature INDEXED.
    assert stage_db.videos[video_id].state == "transcribed"
    # No artifact rows persisted.
    assert not [r for r in stage_db.subtitle_files.values() if r.video_id == video_id]
