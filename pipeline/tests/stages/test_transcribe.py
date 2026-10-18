"""transcribe -> subtitle_gen on the engine: the transcript, and the files beside the video."""

from __future__ import annotations

import asyncio
from pathlib import Path
from typing import Any

import numpy as np
import pytest

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import Subject
from maktaba_pipeline.engine.sql import is_pg
from maktaba_pipeline.engine.stages import transcribe as tr
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.stt.batched import Sentence, Transcript
from tests.stages.world import core_runtime, drain_core, make_show

SEG = {"start": 0.5, "end": 1.5, "text": "بسم الله", "avg_logprob": -0.1, "no_speech_prob": 0.0}
LINES = [(0.5, 2.25, "السلام عليكم ورحمة الله"), (2.5, 5.0, "اليوم نتكلم عن السيرة النبوية")]


class FakeTranscriber:
    def __init__(self) -> None:
        self.calls = 0
        self.detected: str | None = None
        self.resumed: Any = None
        self.slow = 0.0

    async def transcribe(
        self, audio: np.ndarray, *, resume: Any = None, progress: Any = None, scale: float = 1.0
    ) -> Transcript:
        self.calls += 1
        self.resumed = resume
        if progress is not None and self.slow:
            progress.publish(150, 1.5, "ar", [dict(SEG)])
            await asyncio.sleep(self.slow)
        return Transcript(
            sentences=[Sentence(a, b, t, -0.1, 0.0) for a, b, t in LINES],
            audio_sec=len(audio) / 16000,
            language="ar",
            model="fake-whisper",
            gpu_sec=1.0,
            detected_language=self.detected,
        )


@pytest.fixture
def fake(monkeypatch: pytest.MonkeyPatch) -> FakeTranscriber:
    f = FakeTranscriber()

    async def decode(path: str, timeout: float = 0) -> np.ndarray:
        return np.zeros(16000 * 6, np.float32)

    monkeypatch.setattr(tr, "decode_audio", decode)
    monkeypatch.setattr(tr, "get_transcriber", lambda settings: f)
    monkeypatch.setattr(tr, "use_vad", lambda: False)  # the fake audio is silence
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
    got = [(r["format"], r["source"]) for r in rows]
    assert got == [("srt", "generated"), ("vtt", "generated")]

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
        await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe", force=True)
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


@pytest.mark.asyncio
async def test_the_language_recorded_is_the_one_heard_not_the_one_forced(
    db: Database, tmp_path: Path, fake: FakeTranscriber
) -> None:
    rt = await core_runtime(db)
    vid, _ = await _one_video(db, tmp_path)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    row = await db.fetchrow(
        "SELECT t.language, t.detected_language, v.detected_language AS video_lang"
        " FROM transcripts t JOIN videos v ON v.id = t.video_id WHERE t.video_id = $1",
        str(vid),
    )
    assert (row["language"], row["detected_language"], row["video_lang"]) == ("ar", None, None)

    fake.detected = "en"
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe", force=True)
    await drain_core(db, rt, only={"transcribe"})
    row = await db.fetchrow(
        "SELECT t.detected_language, v.detected_language AS video_lang FROM transcripts t"
        " JOIN videos v ON v.id = t.video_id WHERE t.video_id = $1 AND t.is_active",
        str(vid),
    )
    assert (row["detected_language"], row["video_lang"]) == ("en", "en")


@pytest.mark.asyncio
async def test_a_subtitle_file_identical_to_ours_is_ours_even_unrecorded(
    db: Database, tmp_path: Path, fake: FakeTranscriber
) -> None:
    """A worker stopped between writing the file and recording it."""
    from maktaba_pipeline.engine.stages.subtitle_gen import srt

    rt = await core_runtime(db)
    vid, media = await _one_video(db, tmp_path)
    media.with_name("درس السيرة 01.ar.srt").write_text(srt(LINES), encoding="utf-8")
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe", "subtitle_gen"})
    rows = await db.fetch(
        "SELECT format FROM subtitle_files WHERE video_id = $1 AND deleted_at IS NULL"
        " ORDER BY format",
        str(vid),
    )
    assert [r["format"] for r in rows] == ["srt", "vtt"]  # recorded, not kept as foreign
    assert not list(tmp_path.glob("*.maktaba-tmp"))


@pytest.mark.asyncio
async def test_the_probe_s_default_audio_track_stands(
    db: Database, tmp_path: Path, fake: FakeTranscriber
) -> None:
    rt = await core_runtime(db)
    vid, _ = await _one_video(db, tmp_path)
    yes: Any = True if is_pg(db) else 1
    await db.execute(
        "INSERT INTO audio_tracks (video_id, track_index, is_default) VALUES ($1, 1, $2)",
        str(vid),
        yes,
    )
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    rows = await db.fetch(
        "SELECT track_index, is_default FROM audio_tracks WHERE video_id = $1 ORDER BY track_index",
        str(vid),
    )
    assert [(r["track_index"], bool(r["is_default"])) for r in rows] == [(0, False), (1, True)]


@pytest.mark.asyncio
async def test_progress_is_saved_while_it_runs_and_a_new_attempt_resumes_from_it(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("MAKTABA_STT_CHECKPOINT_S", "0.01")
    rt = await core_runtime(db)
    vid, _ = await _one_video(db, tmp_path)
    fp = await tr.input_fingerprint(db, Subject(kind="video", id=vid))
    assert fp is not None

    # a slow run saves its progress as it goes
    fake.slow = 0.1
    progress_rows: list[Any] = []

    async def spy(db_: Any, video_id: Any, fingerprint: str, cp: Any, **kw: Any) -> None:
        progress_rows.append(cp)
        await saved(db_, video_id, fingerprint, cp, **kw)

    saved = tr.save_checkpoint
    monkeypatch.setattr(tr, "save_checkpoint", spy)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    assert progress_rows and progress_rows[0].seek == 150
    assert progress_rows[0].segments[0]["text"] == "بسم الله"
    # stored: the checkpoint is gone
    q = "SELECT count(*) FROM transcribe_checkpoints WHERE video_id = $1"
    n = await db.fetchval(q, str(vid))
    assert n == 0

    # a checkpoint left by a stopped worker is resumed from, for the same input only
    from maktaba_pipeline.stt.settings import Checkpoint

    await saved(db, vid, fp, Checkpoint(seek=900, last_speech=8.5, segments=[dict(SEG)]))
    fake.slow = 0.0
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe", force=True)
    await drain_core(db, rt, only={"transcribe"})
    assert fake.resumed is not None and fake.resumed.seek == 900
    assert fake.resumed.segments[0]["text"] == "بسم الله"
    await saved(db, vid, "another-file", Checkpoint(seek=900))
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe", force=True)
    await drain_core(db, rt, only={"transcribe"})
    assert fake.resumed is None


@pytest.mark.asyncio
async def test_speech_only_decoding_keeps_the_file_s_timeline(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The pauses are cut before Whisper; its times are mapped back to the file."""
    from maktaba_pipeline.stt import vad

    monkeypatch.setattr(tr, "use_vad", lambda: True)
    sr = 16000
    # speech at 1-3 s and 4-6 s of the 6 s file: Whisper sees 4 s of audio
    monkeypatch.setattr(vad, "speech_chunks", lambda audio: [(1 * sr, 3 * sr), (4 * sr, 6 * sr)])
    seen: list[tuple[int, float]] = []
    orig = fake.transcribe

    async def spy(audio: np.ndarray, **kw: Any) -> Transcript:
        seen.append((len(audio), kw.get("scale", 1.0)))
        return await orig(audio, **kw)

    monkeypatch.setattr(fake, "transcribe", spy)
    rt = await core_runtime(db)
    vid, _ = await _one_video(db, tmp_path)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    assert seen == [(4 * sr, 1.5)]
    rows = await db.fetch(
        "SELECT s.start_sec, s.end_sec FROM transcript_segments s JOIN transcripts t"
        " ON t.id = s.transcript_id WHERE t.video_id = $1 AND t.is_active ORDER BY s.seq",
        str(vid),
    )
    # LINES are (0.5, 2.25) and (2.5, 5.0) in packed time
    assert [(float(r["start_sec"]), float(r["end_sec"])) for r in rows] == [(1.5, 4.25), (4.5, 7.0)]
    summary = await db.fetchval(
        "SELECT output_summary FROM processing_jobs WHERE stage = 'transcribe'"
    )
    import json

    got = summary if isinstance(summary, dict) else json.loads(summary)
    assert got["audio_sec"] == 6.0  # the file's length, not the speech-only audio's


@pytest.mark.asyncio
async def test_saves_append_new_sentences_and_a_failed_save_does_not_stop_the_work(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    from maktaba_pipeline.stt.settings import Checkpoint

    vid, _ = await _one_video(db, tmp_path)
    one, two = dict(SEG), {**SEG, "start": 2.0, "end": 3.0, "text": "الحمد لله"}
    await tr.save_checkpoint(db, vid, "fp", Checkpoint(seek=150, segments=[one]))
    await tr.save_checkpoint(db, vid, "fp", Checkpoint(seek=300, segments=[two]), append=True)
    cp = await tr.load_checkpoint(db, vid, "fp")
    assert cp is not None and cp.seek == 300
    assert [s["text"] for s in cp.segments] == ["بسم الله", "الحمد لله"]

    # every save fails: the file is still transcribed
    monkeypatch.setenv("MAKTABA_STT_CHECKPOINT_S", "0.01")

    async def broken(*a: Any, **k: Any) -> None:
        raise RuntimeError("could not resize shared memory segment")

    monkeypatch.setattr(tr, "save_checkpoint", broken)
    fake.slow = 0.05
    rt = await core_runtime(db)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    state = await db.fetchval("SELECT state FROM processing_jobs WHERE stage = 'transcribe'")
    assert state == "done"


@pytest.mark.asyncio
async def test_a_dropped_job_s_checkpoint_is_pruned_after_a_week(
    db: Database, tmp_path: Path, fake: FakeTranscriber
) -> None:
    from datetime import UTC, datetime, timedelta

    from maktaba_pipeline.engine.prune import prune_once
    from maktaba_pipeline.engine.sql import to_ts
    from maktaba_pipeline.stt.settings import Checkpoint

    vid, _ = await _one_video(db, tmp_path)
    await tr.save_checkpoint(db, vid, "fp", Checkpoint(seek=150))
    old = to_ts(db, datetime.now(UTC) - timedelta(days=8))
    await db.execute("UPDATE transcribe_checkpoints SET updated_at = $1", old)
    await prune_once(db)
    assert await db.fetchval("SELECT count(*) FROM transcribe_checkpoints") == 0
