"""Track R4 — :func:`subtitle.subtitle_gen` library functions.

``commit_subtitles`` is the SUBTITLE_GEN-stage analogue of
``commit_extract`` / ``commit_transcribe``: it loads the transcript's
ordered segments, renders SRT + VTT via the existing pure generator,
persists both via the pre-existing ``subtitle_files`` registry +
content-addressed sidecar, and advances the FSM
``TRANSCRIBED -> INDEXED`` (replay-guarded). Same canonical
:class:`FakeAudioDB`, same ``asyncio.run`` driver the
``commit_extract`` tests use.
"""

from __future__ import annotations

import asyncio
from pathlib import Path
from uuid import uuid4

import pytest

from maktaba_pipeline.subtitle.subtitle_gen import (
    commit_subtitles,
    load_transcript_cues,
)
from tests.audio._fake_audio_db import FakeAudioDB


def test_load_transcript_cues_orders_by_seq_and_projects(tmp_path: Path) -> None:
    async def run() -> None:
        db = FakeAudioDB(dialect="postgres")
        vid = db.add_video(state="transcribed")
        tid = db.seed_transcript(
            video_id=vid,
            language="ara",
            segments=[
                (1, 2.0, 4.0, "second"),
                (0, 0.0, 2.0, "first"),
                (2, 4.0, 6.0, ""),  # empty → dropped by segments_to_cues
            ],
        )
        loaded = await load_transcript_cues(db, transcript_id=tid)
        assert loaded is not None
        assert loaded.language == "ara"
        assert loaded.video_id == vid
        # seq-ordered, empty-text row dropped.
        assert [c.text for c in loaded.cues] == ["first", "second"]
        assert loaded.cues[0].start_sec == 0.0

    asyncio.run(run())


def test_load_transcript_cues_missing_returns_none() -> None:
    async def run() -> None:
        db = FakeAudioDB(dialect="postgres")
        assert await load_transcript_cues(db, transcript_id=uuid4()) is None

    asyncio.run(run())


def test_load_transcript_cues_no_renderable_segments_returns_none() -> None:
    async def run() -> None:
        db = FakeAudioDB(dialect="postgres")
        vid = db.add_video(state="transcribed")
        tid = db.seed_transcript(video_id=vid, segments=[(0, 0.0, 1.0, "  ")])
        assert await load_transcript_cues(db, transcript_id=tid) is None

    asyncio.run(run())


def test_commit_subtitles_renders_persists_and_advances(tmp_path: Path) -> None:
    async def run() -> None:
        db = FakeAudioDB(dialect="postgres")
        vid = db.add_video(state="transcribed")
        tid = db.seed_transcript(
            video_id=vid,
            language="ara",
            segments=[(0, 0.0, 2.5, "salam"), (1, 2.5, 5.0, "alaykum")],
        )
        loaded = await load_transcript_cues(db, transcript_id=tid)
        assert loaded is not None

        new_state = await commit_subtitles(
            db, video_id=vid, loaded=loaded, cache_root=str(tmp_path)
        )

        assert new_state == "indexed"
        assert db.videos[vid].state == "indexed"
        rows = [r for r in db.subtitle_files.values() if r.video_id == vid]
        assert {r.format for r in rows} == {"srt", "vtt"}
        for r in rows:
            p = Path(r.path)
            assert p.is_relative_to(tmp_path)
            text = p.read_text(encoding="utf-8")
            assert "salam" in text and "alaykum" in text
            assert r.transcript_id == tid

    asyncio.run(run())


def test_commit_subtitles_replay_guard_no_double_advance(tmp_path: Path) -> None:
    async def run() -> None:
        db = FakeAudioDB(dialect="postgres")
        vid = db.add_video(state="transcribed")
        tid = db.seed_transcript(video_id=vid, language="ara")
        loaded = await load_transcript_cues(db, transcript_id=tid)
        assert loaded is not None

        s1 = await commit_subtitles(db, video_id=vid, loaded=loaded, cache_root=str(tmp_path))
        assert s1 == "indexed"
        # Second run: video already INDEXED — must NOT raise
        # IllegalStateTransition; the advance is a no-op.
        s2 = await commit_subtitles(db, video_id=vid, loaded=loaded, cache_root=str(tmp_path))
        assert s2 == "indexed"
        # UPSERT deduped — still exactly two rows.
        rows = [r for r in db.subtitle_files.values() if r.video_id == vid]
        assert len(rows) == 2

    asyncio.run(run())


def test_commit_subtitles_second_format_failure_rolls_both_rows_back(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """First format registers OK, second register fails → ZERO rows.

    Pins the two-row registry atomicity specifically: the SRT row's
    ``register_subtitle`` succeeds, then the VTT row's raises. Because
    both writes share one ``async with db.transaction():``, the SRT row
    must roll back — the registry is never observed SRT-only. The job
    is then retryable: a clean re-run registers exactly two rows.

    Without the transaction wrap this asserts FALSE — the SRT row would
    persist (a transient SRT-only state), so this test is the regression
    guard for the atomicity fix.
    """

    async def run() -> None:
        db = FakeAudioDB(dialect="postgres")
        vid = db.add_video(state="transcribed")
        tid = db.seed_transcript(
            video_id=vid,
            language="ara",
            segments=[(0, 0.0, 2.5, "salam"), (1, 2.5, 5.0, "alaykum")],
        )
        loaded = await load_transcript_cues(db, transcript_id=tid)
        assert loaded is not None

        import maktaba_pipeline.subtitle.manager as mgr

        real_register = mgr.register_subtitle
        calls = {"n": 0}

        async def _flaky_register(conn: object, record: object) -> int:
            calls["n"] += 1
            if calls["n"] == 2:  # the VTT row, after SRT already inserted
                raise OSError("registry write failed mid-persist")
            return await real_register(conn, record)  # type: ignore[arg-type]

        # commit_subtitles imports register_subtitle lazily from manager,
        # so patching the manager attribute is sufficient.
        monkeypatch.setattr(mgr, "register_subtitle", _flaky_register)

        with pytest.raises(OSError, match="registry write failed"):
            await commit_subtitles(db, video_id=vid, loaded=loaded, cache_root=str(tmp_path))

        # Atomic rollback: NEITHER row persisted (not SRT-only), and the
        # FSM did not advance — the job is retryable.
        assert [r for r in db.subtitle_files.values() if r.video_id == vid] == []
        assert db.videos[vid].state == "transcribed"

        # Retry with the flake cleared → exactly two rows, advance OK.
        monkeypatch.setattr(mgr, "register_subtitle", real_register)
        new_state = await commit_subtitles(
            db, video_id=vid, loaded=loaded, cache_root=str(tmp_path)
        )
        assert new_state == "indexed"
        rows = [r for r in db.subtitle_files.values() if r.video_id == vid]
        assert len(rows) == 2
        assert {r.format for r in rows} == {"srt", "vtt"}

    asyncio.run(run())


def test_commit_subtitles_missing_video_raises_lookup(tmp_path: Path) -> None:
    async def run() -> None:
        db = FakeAudioDB(dialect="postgres")
        vid = db.add_video(state="transcribed")
        tid = db.seed_transcript(video_id=vid, language="ara")
        loaded = await load_transcript_cues(db, transcript_id=tid)
        assert loaded is not None
        # Drop the video row so the post-write state read misses.
        del db.videos[vid]
        raised = False
        try:
            await commit_subtitles(db, video_id=vid, loaded=loaded, cache_root=str(tmp_path))
        except LookupError:
            raised = True
        assert raised

    asyncio.run(run())
