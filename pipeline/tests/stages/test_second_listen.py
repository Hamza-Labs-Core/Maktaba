"""The ``second_listen`` stage on the engine: Whisper's stored transcript and the file's
audio in, the corrected transcript out, the stages that read a transcript after it.

On a database: the integration tier (SQLite; Postgres with ``MAKTABA_TEST_PG_URL``)."""

from __future__ import annotations

import json
from datetime import timedelta
from pathlib import Path
from typing import Any

import numpy as np
import pytest

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH, Subject
from maktaba_pipeline.engine.sql import now, to_ts
from maktaba_pipeline.engine.stages import second_listen as sl
from maktaba_pipeline.engine.stages import transcribe as tr
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.stt import gpu_turns, model_share
from maktaba_pipeline.stt.batched import Sentence, Transcript
from maktaba_pipeline.stt.settings import Word
from maktaba_pipeline.stt.placement import TranscriptionConfig
from tests.stages.test_transcribe import LINES, FakeTranscriber, _one_video
from tests.stages.world import core_runtime, drain_core

pytestmark = pytest.mark.integration


class _WithWords(FakeTranscriber):
    """Whisper's sentences with their words, as the stage stores them for the listen."""

    async def transcribe(self, audio: np.ndarray, **kw: Any) -> Transcript:
        t = await super().transcribe(audio, **kw)
        t.sentences = [
            Sentence(s.start, s.end, s.text, s.avg_logprob, s.no_speech_prob,
                     words=[Word(s.start, s.end, s.text)])
            for s in t.sentences
        ]  # fmt: skip
        return t


@pytest.fixture
def fake(monkeypatch: pytest.MonkeyPatch) -> FakeTranscriber:
    f = _WithWords()

    async def decode(path: str, timeout: float = 0, *, stream: int = 0) -> np.ndarray:
        decoded.append(path)
        return np.zeros(16000 * 6, np.float32)

    async def no_share(db: Any) -> Any:
        return None

    decoded: list[str] = []
    f.decoded = decoded  # type: ignore[attr-defined]
    monkeypatch.setattr(tr, "decode_audio", decode)
    monkeypatch.setattr(tr, "get_transcriber", lambda settings: f)
    monkeypatch.setattr(tr, "use_vad", lambda: False)
    monkeypatch.setattr(model_share, "resolve_models_dir", no_share)
    for var in ("MAKTABA_STT_MODEL", "MAKTABA_STT_REFINE", "MAKTABA_STT_REFINE_MODEL"):
        monkeypatch.delenv(var, raising=False)
    monkeypatch.delenv("MAKTABA_STT_STORE_WORDS", raising=False)
    monkeypatch.setattr(tr, "_RULES", TranscriptionConfig())
    monkeypatch.setattr(tr, "_LAST_RELISTEN", [])
    return f


def _hears(monkeypatch: pytest.MonkeyPatch, here: bool = True) -> list[list[str]]:
    """This worker runs the second listen: a fake that corrects each sentence's first word
    (``here`` False: it cannot run here)."""
    heard: list[list[str]] = []
    monkeypatch.setattr(tr, "runs_second_listen", lambda: here)
    monkeypatch.setattr(tr, "second_listen_problem", lambda: None if here else "no GPU here")

    def refine(result: Transcript, audio: Any, model: str, *_a: Any, **_k: Any) -> Any:
        heard.append([s.text for s in result.sentences])
        assert all(s.words for s in result.sentences)  # the stored words, for the alignment
        for s in result.sentences:
            s.text = "قال " + s.text
        return {"model": model, "source": "hf", "changed": len(result.sentences)}

    monkeypatch.setattr(tr, "_refine", refine)
    return heard


async def _active(db: Database, vid: Any) -> Any:
    return await db.fetchrow(
        "SELECT id, metadata FROM transcripts WHERE video_id = $1 AND is_active", str(vid)
    )


def _meta(row: Any) -> dict[str, Any]:
    m = row["metadata"]
    return m if isinstance(m, dict) else json.loads(m or "{}")


async def _texts(db: Database, tid: Any) -> list[str]:
    rows = await db.fetch(
        "SELECT text FROM transcript_segments WHERE transcript_id = $1 ORDER BY seq", tid
    )
    return [r["text"] for r in rows]


async def _words_left(db: Database, tid: Any) -> int:
    rows = await db.fetch(
        "SELECT metadata, word_data FROM transcript_segments WHERE transcript_id = $1", tid
    )
    return sum(1 for r in rows if "words" in _meta(r) or r["word_data"])


def test_the_stages_that_read_a_transcript_follow_the_second_listen() -> None:
    by = {s.name: s for s in GRAPH}
    assert [e.to for e in by["transcribe"].edges] == ["second_listen"]
    after = {e.to for e in by["second_listen"].edges}
    assert {"subtitle_gen", "index", "tag"} <= after
    assert by["second_listen"].pool == "listen" and by["second_listen"].lane == "speech"
    assert (
        by["transcribe"].lane_order < by["second_listen"].lane_order < by["subtitle_gen"].lane_order
    )


@pytest.mark.asyncio
async def test_it_corrects_whisper_s_transcript_and_the_subtitles_follow(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    heard = _hears(monkeypatch)
    rt = await core_runtime(db)
    vid, media = await _one_video(db, tmp_path)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    ran = await drain_core(db, rt, only={"transcribe", "second_listen", "subtitle_gen"})
    assert ran == ["transcribe", "second_listen", "subtitle_gen"]
    assert fake.calls == 1 and len(heard) == 1 and len(fake.decoded) == 2  # type: ignore[attr-defined]

    row = await _active(db, vid)
    meta = _meta(row)
    whisper = meta["refine"]["whisper"]
    assert meta["refine"]["source"] == "hf" and whisper != str(row["id"])
    # Epic 52 (story 52.1): the corrected transcript keeps its words, packed
    assert meta["words"] is True and await _words_left(db, row["id"]) == len(LINES)
    assert await _texts(db, row["id"]) == ["قال " + t for _a, _b, t in LINES]
    # Whisper's transcript is kept, superseded, without its words now
    old = await db.fetchrow("SELECT is_active FROM transcripts WHERE id = $1", whisper)
    assert old is not None and not old["is_active"]
    assert await _words_left(db, whisper) == 0
    assert await _texts(db, whisper) == [t for _a, _b, t in LINES]
    # the subtitles are the corrected words
    vtt = media.with_name("درس السيرة 01.ar.vtt").read_text(encoding="utf-8")
    assert "قال السلام عليكم" in vtt

    # its fingerprint is Whisper's transcript: heard once
    fp = await sl.input_fingerprint(db, Subject("video", vid))
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="second_listen")
    await drain_core(db, rt, only={"second_listen"})
    assert len(heard) == 1 and fp == await sl.input_fingerprint(db, Subject("video", vid))


@pytest.mark.asyncio
async def test_with_the_second_listen_off_it_passes_the_transcript_on(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("MAKTABA_STT_REFINE", "0")
    heard = _hears(monkeypatch)
    rt = await core_runtime(db)
    vid, _ = await _one_video(db, tmp_path)
    before = None
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    ran = await drain_core(db, rt, only={"transcribe", "second_listen", "subtitle_gen"})
    assert ran == ["transcribe", "second_listen", "subtitle_gen"]
    row = await _active(db, vid)
    assert heard == [] and "refine" not in _meta(row) and row["id"] != before
    assert fake.decoded == [str(tmp_path / "درس السيرة 01.mp4")]  # type: ignore[attr-defined]
    # Epic 52 (story 52.1): every transcript keeps its words, listen or not
    assert await _words_left(db, row["id"]) == len(LINES)


@pytest.mark.asyncio
async def test_a_machine_without_it_waits_for_one_with_it_and_then_passes_it_on(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The rules let it take the jobs (without_second_listen): it gives a job back for a
    quarter of an hour, for a machine that runs the second listen, then records the
    transcript as made without it."""
    _hears(monkeypatch, here=False)
    monkeypatch.setattr(tr, "_RULES", TranscriptionConfig(without_second_listen=True))
    assert sl.unavailable() is None
    rt = await core_runtime(db)
    vid, _ = await _one_video(db, tmp_path)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe", "second_listen"})
    job = await db.fetchrow(
        "SELECT id, state, paused_reason FROM processing_jobs"
        " WHERE subject_id = $1 AND stage = 'second_listen'",
        str(vid),
    )
    assert job["state"] == "paused" and "left to a machine that runs it" in job["paused_reason"]
    assert "refine" not in _meta(await _active(db, vid))

    await db.execute(
        "UPDATE processing_jobs SET state = 'pending', not_before = NULL, created_at = $2"
        " WHERE id = $1",
        job["id"],
        to_ts(db, now() - timedelta(seconds=sl.PASS_ON_AFTER_S + 60)),
    )
    await drain_core(db, rt, only={"second_listen"})
    refine = _meta(await _active(db, vid))["refine"]
    assert refine["source"] == "skipped" and refine["skipped"] == "no GPU here"
    done = await db.fetchrow(
        "SELECT state, input_hash FROM processing_jobs WHERE id = $1", job["id"]
    )
    fp = await sl.input_fingerprint(db, Subject("video", vid))
    assert done["state"] == "done" and done["input_hash"] == tr.skipped_fingerprint(str(fp))


@pytest.mark.asyncio
async def test_a_transcript_from_before_the_split_has_no_words_to_align_on(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    heard = _hears(monkeypatch)
    rt = await core_runtime(db)
    vid, _ = await _one_video(db, tmp_path)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    tid = (await _active(db, vid))["id"]
    await db.execute(
        "UPDATE transcript_segments SET metadata = '{}', word_data = NULL WHERE transcript_id = $1",
        tid,
    )
    await drain_core(db, rt, only={"second_listen"})
    refine = _meta(await _active(db, vid))["refine"]
    assert heard == [] and refine["source"] == "skipped" and "no word timings" in refine["skipped"]


@pytest.mark.asyncio
async def test_no_room_beside_whisper_turns_to_taking_turns_and_hears_it_then(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The GPU was to hold both, but another program took part of it: the second listen
    found no room beside Whisper. From then on they take turns, and this file is heard in
    Cohere's turn, with no room kept for Whisper."""
    calls: list[bool] = []
    mode = {"m": gpu_turns.TOGETHER}
    forgot: list[str] = []
    monkeypatch.setattr(gpu_turns, "sharing", lambda model_id, settings: mode["m"])

    def take_turns(model_id: str) -> None:
        mode["m"] = gpu_turns.SEQUENTIAL

    monkeypatch.setattr(gpu_turns, "take_turns", take_turns)
    monkeypatch.setattr(gpu_turns, "_unload", lambda m: None)
    monkeypatch.setattr(gpu_turns, "_load", lambda m: None)
    monkeypatch.setattr(gpu_turns, "_SHARED", [])
    from maktaba_pipeline.stt import cohere

    monkeypatch.setattr(cohere, "forget_failure", forgot.append)
    monkeypatch.setattr(cohere, "loaded", lambda m: True)

    def refine(result: Any, audio: Any, model: str, share: Any, settings: Any, beside: bool) -> Any:
        calls.append(beside)
        if beside:
            return {"model": model, "source": "skipped", "skipped": "NoRoom: full", "no_room": True}
        return {"model": model, "source": "hf"}

    monkeypatch.setattr(tr, "_refine", refine)
    t = Transcript(sentences=[Sentence(0, 1, "x", -0.1, 0.0)], audio_sec=600.0, language="ar",
                   model="w", gpu_sec=0.0)  # fmt: skip
    out = await sl.listen(db, t, np.zeros(16), "m")
    assert calls == [True, False] and forgot == ["m"] and out["source"] == "hf"
    assert out["rtf"] > 0  # its own speed, in the job's summary
