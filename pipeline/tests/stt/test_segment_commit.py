"""Story 3.6 — atomic per-segment commit + reorder buffer."""

from __future__ import annotations

import asyncio
from uuid import uuid4

from maktaba_pipeline.stt.protocol import Segment, Word
from maktaba_pipeline.stt.segment_commit import ReorderBuffer, commit_segment

from ..audio._fake_audio_db import FakeAudioDB, _ProcessingJobRow


def _seed_job(db: FakeAudioDB, *, total_duration: float = 600.0) -> int:
    job_id = db._job_next_id
    db._job_next_id += 1
    db.processing_jobs[job_id] = _ProcessingJobRow(
        id=job_id,
        video_id=uuid4(),
        stage="transcribe",
    )
    return job_id


def test_pg_commit_advances_progress_with_audio_time() -> None:
    db = FakeAudioDB(dialect="postgres")
    transcript_id = uuid4()
    job_id = _seed_job(db)

    seg = Segment(seq=0, start_sec=0.0, end_sec=60.0, text="hello", audio_sec=60.0, wall_sec=1.0)
    result = asyncio.run(
        commit_segment(
            db, transcript_id=transcript_id, job_id=job_id, segment=seg, total_duration_sec=600.0
        )
    )
    assert result.accepted is True
    job = db.processing_jobs[job_id]
    assert job.processed_seconds == 60.0
    assert job.last_segment_end_sec == 60.0
    assert job.segments_completed == 1
    # 60 s audio in 1 s wall = factor 60; first sample of EWMA is alpha * x.
    assert job.realtime_factor is not None and job.realtime_factor > 0


def test_pg_commit_idempotent_on_replayed_seq() -> None:
    db = FakeAudioDB(dialect="postgres")
    transcript_id = uuid4()
    job_id = _seed_job(db)
    seg = Segment(seq=0, start_sec=0.0, end_sec=10.0, text="x", audio_sec=10.0, wall_sec=1.0)

    async def _commit() -> object:
        return await commit_segment(
            db,
            transcript_id=transcript_id,
            job_id=job_id,
            segment=seg,
            total_duration_sec=100.0,
        )

    asyncio.run(_commit())
    second = asyncio.run(_commit())
    assert second.accepted is False  # type: ignore[attr-defined]
    assert db.processing_jobs[job_id].segments_completed == 1


def test_sqlite_commit_emits_segments_committed_notify() -> None:
    import json as _json

    from maktaba_pipeline.db.pubsub import get_bus, reset_bus

    reset_bus()
    bus = get_bus()
    db = FakeAudioDB(dialect="sqlite")
    transcript_id = uuid4()
    job_id = _seed_job(db)
    seg = Segment(seq=0, start_sec=0.0, end_sec=2.0, text="x", audio_sec=2.0, wall_sec=0.1)

    async def _drive() -> dict[str, object]:
        queue = await bus.subscribe("segments.committed")
        await commit_segment(
            db,
            transcript_id=transcript_id,
            job_id=job_id,
            segment=seg,
            total_duration_sec=10.0,
        )
        # The bus delivers JSON strings; the callers parse them.
        text = await asyncio.wait_for(queue.get(), timeout=1.0)
        result: dict[str, object] = _json.loads(text)
        return result

    payload = asyncio.run(_drive())
    assert payload["seq"] == 0
    assert payload["transcript_id"] == str(transcript_id)
    # Story 3.6-4 — the live-indexer / VTT-renderer contract keys off
    # ``last_segment_end_sec`` (the advanced job watermark), NOT the raw
    # ``end_sec``. The old payload emitted ``end_sec`` which the Epic-5.5
    # consumer cannot consume.
    assert payload["last_segment_end_sec"] == 2.0
    assert "end_sec" not in payload


# --- transcript_words (Story 3.6-1.3) ------------------------------------

_WORDS = (
    Word(text="bismillah", start_sec=0.0, end_sec=0.8, confidence=0.97),
    Word(text="al-rahman", start_sec=0.8, end_sec=1.6, confidence=0.91),
    Word(text="al-rahim", start_sec=1.6, end_sec=2.0, confidence=None),
)


def test_pg_commit_persists_word_rows() -> None:
    db = FakeAudioDB(dialect="postgres")
    transcript_id = uuid4()
    job_id = _seed_job(db)
    seg = Segment(
        seq=0,
        start_sec=0.0,
        end_sec=2.0,
        text="bismillah al-rahman al-rahim",
        words=_WORDS,
        audio_sec=2.0,
        wall_sec=1.0,
    )
    result = asyncio.run(
        commit_segment(
            db, transcript_id=transcript_id, job_id=job_id, segment=seg, total_duration_sec=10.0
        )
    )
    assert result.accepted is True
    assert result.words_committed == 3
    rows = sorted(db.transcript_words.values(), key=lambda w: w.seq)
    assert [w.text for w in rows] == ["bismillah", "al-rahman", "al-rahim"]
    assert [w.seq for w in rows] == [0, 1, 2]
    assert all(w.segment_id == result.segment_id for w in rows)
    assert rows[0].confidence == 0.97
    assert rows[2].confidence is None


def test_sqlite_commit_persists_word_rows() -> None:
    db = FakeAudioDB(dialect="sqlite")
    transcript_id = uuid4()
    job_id = _seed_job(db)
    seg = Segment(
        seq=0,
        start_sec=0.0,
        end_sec=2.0,
        text="bismillah al-rahman al-rahim",
        words=_WORDS,
        audio_sec=2.0,
        wall_sec=0.5,
    )
    result = asyncio.run(
        commit_segment(
            db, transcript_id=transcript_id, job_id=job_id, segment=seg, total_duration_sec=10.0
        )
    )
    assert result.words_committed == 3
    rows = sorted(db.transcript_words.values(), key=lambda w: w.seq)
    assert [w.text for w in rows] == ["bismillah", "al-rahman", "al-rahim"]
    assert all(w.segment_id == result.segment_id for w in rows)


def test_word_rows_idempotent_on_replayed_seq() -> None:
    """A replayed segment commit must not duplicate (or orphan) words."""
    db = FakeAudioDB(dialect="postgres")
    transcript_id = uuid4()
    job_id = _seed_job(db)
    seg = Segment(
        seq=0, start_sec=0.0, end_sec=2.0, text="x", words=_WORDS, audio_sec=2.0, wall_sec=1.0
    )

    async def _commit() -> object:
        return await commit_segment(
            db, transcript_id=transcript_id, job_id=job_id, segment=seg, total_duration_sec=10.0
        )

    asyncio.run(_commit())
    second = asyncio.run(_commit())
    # ON CONFLICT swallowed the replayed segment → no second word write.
    assert second.accepted is False  # type: ignore[attr-defined]
    assert second.words_committed == 0  # type: ignore[attr-defined]
    assert len(db.transcript_words) == 3


def test_no_words_when_segment_has_none() -> None:
    db = FakeAudioDB(dialect="postgres")
    transcript_id = uuid4()
    job_id = _seed_job(db)
    seg = Segment(seq=0, start_sec=0.0, end_sec=2.0, text="x", audio_sec=2.0, wall_sec=1.0)
    result = asyncio.run(
        commit_segment(
            db, transcript_id=transcript_id, job_id=job_id, segment=seg, total_duration_sec=10.0
        )
    )
    assert result.words_committed == 0
    assert db.transcript_words == {}


# --- ReorderBuffer --------------------------------------------------------


def test_reorder_buffer_yields_in_seq_order() -> None:
    buf = ReorderBuffer()
    buf.admit(Segment(seq=1, start_sec=1.0, end_sec=2.0, text="b"))
    buf.admit(Segment(seq=0, start_sec=0.0, end_sec=1.0, text="a"))
    out = list(buf.drain_ready())
    assert [s.seq for s in out] == [0, 1]


def test_reorder_buffer_holds_gap_until_filled() -> None:
    buf = ReorderBuffer()
    buf.admit(Segment(seq=2, start_sec=2.0, end_sec=3.0, text="c"))
    out = list(buf.drain_ready())
    assert out == []
    assert buf.pending() == 1


def test_reorder_buffer_force_flushes_after_window() -> None:
    buf = ReorderBuffer(window_sec=10.0)
    # Admit at t=0 (clock=0) — too far ahead for seq 0.
    buf.admit(Segment(seq=2, start_sec=2.0, end_sec=3.0, text="c"), clock=0.0)
    # Drain at t=20 — past the window — the gap is filled by skipping to seq 2.
    out = list(buf.drain_ready(clock=20.0))
    assert [s.seq for s in out] == [2]
    assert buf.next_seq == 3
