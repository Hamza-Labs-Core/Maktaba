"""transcribe -> subtitle_gen on the engine: the transcript, and the files beside the video."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import numpy as np
import pytest

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.stages import transcribe as tr
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.stt.batched import Sentence, Transcript
from tests.stages.world import core_runtime, drain_core, make_show

LINES = [(0.5, 2.25, "السلام عليكم ورحمة الله"), (2.5, 5.0, "اليوم نتكلم عن السيرة النبوية")]


class FakeTranscriber:
    def __init__(self) -> None:
        self.calls = 0

    async def transcribe(self, audio: np.ndarray) -> Transcript:
        self.calls += 1
        return Transcript(
            sentences=[Sentence(a, b, t, -0.1, 0.0) for a, b, t in LINES],
            audio_sec=len(audio) / 16000,
            language="ar",
            model="fake-whisper",
            gpu_sec=1.0,
        )


@pytest.fixture
def fake(monkeypatch: pytest.MonkeyPatch) -> FakeTranscriber:
    f = FakeTranscriber()

    async def decode(path: str, timeout: float = 0) -> np.ndarray:
        return np.zeros(16000 * 6, np.float32)

    monkeypatch.setattr(tr, "decode_audio", decode)
    monkeypatch.setattr(tr, "get_transcriber", lambda settings: f)
    return f


async def _one_video(db: Database, tmp_path: Path) -> tuple[Any, Path]:
    w = await make_show(db, episodes=1, loose=0)
    vid = w.episodes[0]
    media = tmp_path / "درس السيرة 01.mp4"
    media.write_bytes(b"\x00" * 64)
    await db.execute("UPDATE videos SET path = $2 WHERE id = $1", str(vid), str(media))
    return vid, media


@pytest.mark.asyncio
async def test_a_transcript_and_its_subtitle_files(
    db: Database, tmp_path: Path, fake: FakeTranscriber
) -> None:
    rt = await core_runtime(db)
    vid, media = await _one_video(db, tmp_path)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    ran = await drain_core(db, rt, only={"transcribe", "subtitle_gen"})
    assert ran == ["transcribe", "subtitle_gen"]

    t = await db.fetchrow(
        "SELECT id, language, backend, model FROM transcripts WHERE video_id = $1 AND is_active",
        str(vid),
    )
    assert t is not None and t["language"] == "ar" and t["backend"] == "mlx-batched"
    segs = await db.fetch(
        "SELECT seq, start_sec, end_sec, text FROM transcript_segments"
        " WHERE transcript_id = $1 ORDER BY seq",
        t["id"],
    )
    assert [(r["seq"], float(r["start_sec"]), float(r["end_sec"]), r["text"]) for r in segs] == [
        (i, a, b, txt) for i, (a, b, txt) in enumerate(LINES)
    ]

    srt = media.with_name("درس السيرة 01.ar.srt")
    vtt = media.with_name("درس السيرة 01.ar.vtt")
    assert "00:00:00,500 --> 00:00:02,250" in srt.read_text(encoding="utf-8")
    assert vtt.read_text(encoding="utf-8").startswith("WEBVTT")
    rows = await db.fetch(
        "SELECT format, source, path FROM subtitle_files WHERE video_id = $1"
        " AND deleted_at IS NULL ORDER BY format",
        str(vid),
    )
    assert [(r["format"], r["source"]) for r in rows] == [("srt", "generated"), ("vtt", "generated")]

    # unchanged input: skipped, the model is not run again
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe", "subtitle_gen"})
    assert fake.calls == 1


@pytest.mark.asyncio
async def test_a_subtitle_file_maktaba_did_not_write_is_left_alone(
    db: Database, tmp_path: Path, fake: FakeTranscriber
) -> None:
    rt = await core_runtime(db)
    vid, media = await _one_video(db, tmp_path)
    mine = media.with_name("درس السيرة 01.ar.srt")
    mine.write_text("1\n00:00:00,000 --> 00:00:01,000\nhand made\n", encoding="utf-8")
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe", "subtitle_gen"})
    assert "hand made" in mine.read_text(encoding="utf-8")  # untouched
    assert media.with_name("درس السيرة 01.ar.vtt").exists()  # the other one is written
    summary = await db.fetchval(
        "SELECT output_summary FROM processing_jobs WHERE stage = 'subtitle_gen'"
        " ORDER BY id DESC LIMIT 1"
    )
    assert "درس السيرة 01.ar.srt" in str(summary)


@pytest.mark.asyncio
async def test_a_new_transcript_replaces_the_active_one(
    db: Database, tmp_path: Path, fake: FakeTranscriber
) -> None:
    rt = await core_runtime(db)
    vid, _ = await _one_video(db, tmp_path)
    for _ in range(2):
        await enqueue_job(
            db, subject_kind="video", subject_id=vid, stage="transcribe", force=True
        )
        await drain_core(db, rt, only={"transcribe"})
    assert fake.calls == 2
    n_active = await db.fetchval(
        "SELECT count(*) FROM transcripts WHERE video_id = $1 AND is_active", str(vid)
    )
    n_all = await db.fetchval("SELECT count(*) FROM transcripts WHERE video_id = $1", str(vid))
    assert (n_active, n_all) == (1, 2)


@pytest.mark.asyncio
async def test_a_missing_file_fails_for_good(
    db: Database, tmp_path: Path, fake: FakeTranscriber
) -> None:
    rt = await core_runtime(db)
    vid, media = await _one_video(db, tmp_path)
    media.unlink()
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    row = await db.fetchrow(
        "SELECT state, error FROM processing_jobs WHERE stage = 'transcribe' ORDER BY id DESC"
    )
    assert row["state"] == "failed" and "file_missing" in str(row["error"])
    assert fake.calls == 0
