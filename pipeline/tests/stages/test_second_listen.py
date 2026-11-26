"""The ``second_listen`` stage on the engine: Whisper's stored transcript and the file's
audio in, the corrected transcript out, the stages that read a transcript after it.

On a database: the integration tier (SQLite; Postgres with ``MAKTABA_TEST_PG_URL``)."""

from __future__ import annotations

import json
from datetime import timedelta
from pathlib import Path
from typing import Any
from uuid import uuid4

import numpy as np
import pytest
from maktaba_fp_transcription.stages import second_listen as sl
from maktaba_fp_transcription.stages import transcribe as tr

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH, Subject
from maktaba_pipeline.engine.sql import is_pg, now, to_ts
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.stt import gpu_turns, model_share
from maktaba_pipeline.stt.batched import Sentence, Transcript
from maktaba_pipeline.stt.placement import TranscriptionConfig
from maktaba_pipeline.stt.settings import Word
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
        assert any(s.words for s in result.sentences)  # the stored words, for the alignment
        for s in result.sentences:
            if s.words:
                s.text = "قال " + s.text
                s.words = [Word(s.start, s.start, "قال"), *s.words]
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
    assert heard == [] and refine["source"] == "skipped" and refine["skipped"] == tr.NO_WORDS

    # its words are gone, whatever its mark said: the sweep transcribes it again, once
    assert "words" not in _meta(await _active(db, vid))
    assert await _sweep(db, monkeypatch) == 1
    ran = await drain_core(db, rt, only={"transcribe", "second_listen"})
    assert ran == ["transcribe", "second_listen"] and len(heard) == 1 and fake.calls == 2
    assert _meta(await _active(db, vid))["refine"]["source"] == "hf"
    assert await _sweep(db, monkeypatch) == 0


# ------------------------------------------------- sentences without words (issue #1756)


async def _left_out(
    db: Database, rt: Any, tmp_path: Path, monkeypatch: pytest.MonkeyPatch, why: str
) -> Any:
    """A video whose transcript has a sentence without words, as an earlier second listen
    left it: skipped for ``why``, its tries used up, on machines the home knows."""
    monkeypatch.setattr(tr, "second_listen_capability", lambda: ("this machine", {}))
    vid, _ = await _one_video(db, tmp_path)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    row = await _active(db, vid)
    meta = {
        **{k: v for k, v in _meta(row).items() if k != "words_by_sentence"},
        "refine": {"model": "m", "source": "skipped", "skipped": why},
    }
    assert meta["words"] is True
    await db.execute(
        "UPDATE transcripts SET metadata = $2 WHERE id = $1", row["id"], json.dumps(meta)
    )
    await db.execute(
        "UPDATE transcript_segments SET word_data = NULL WHERE transcript_id = $1 AND seq = 1",
        row["id"],
    )
    await db.execute(
        "UPDATE processing_jobs SET state = 'done' WHERE subject_id = $1 AND state = 'pending'",
        str(vid),
    )
    at = to_ts(db, now())
    await db.execute(
        "INSERT INTO relisten_attempts (video_id, attempts, last_at, next_at, ended_at,"
        " stopped_at) VALUES ($1, 6, $2, $2, $2, $2)",
        str(vid),
        at,
    )
    for kind, key in (("model", str(tr.refine_model())), ("capability", "this machine")):
        await db.execute(
            "INSERT INTO relisten_seen (kind, key, detail, first_seen) VALUES ($1, $2, '{}', $3)"
            " ON CONFLICT (kind, key) DO NOTHING",
            kind,
            key,
            at,
        )
    return vid


async def _fixed(db: Database) -> int:
    return int(await db.fetchval("SELECT count(*) FROM relisten_seen WHERE kind = 'fix'"))


@pytest.mark.asyncio
async def test_a_video_stopped_for_a_sentence_without_words_is_transcribed_again_once(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Its stored words were given to the sentences wrongly, so the first sweep after the
    update queues transcribe, at the bulk priority; the second listen and the subtitles
    follow, a reviewer's fix is carried over, and no later sweep queues it."""
    heard = _hears(monkeypatch)
    rt = await core_runtime(db)
    vid = await _left_out(db, rt, tmp_path, monkeypatch, tr.NO_WORDS_BEFORE)
    old = (await _active(db, vid))["id"]
    word = LINES[0][2].split()[0]
    edit = str(uuid4())
    await db.execute(
        "INSERT INTO transcript_edits (id, transcript_id, segment_seq, char_start, char_len,"
        " before, after) VALUES ($1, $2, 0, 0, $3, $4, 'مصحّحة')",
        edit,
        old,
        len(word),
        word,
    )
    assert await _sweep(db, monkeypatch) == 1
    job = await db.fetchrow(
        "SELECT stage, priority FROM processing_jobs WHERE subject_id = $1 AND state = 'pending'",
        str(vid),
    )
    assert (job["stage"], job["priority"]) == ("transcribe", tr.RELISTEN_PRIORITY)
    assert await _fixed(db) == 1
    ran = await drain_core(db, rt, only={"transcribe", "second_listen", "subtitle_gen"})
    assert ran == ["transcribe", "second_listen", "subtitle_gen"]
    assert fake.calls == 2 and len(heard) == 1
    row = await _active(db, vid)
    assert _meta(row)["refine"]["source"] == "hf" and _meta(row)["words"] is True
    texts = await _texts(db, row["id"])
    assert "مصحّحة" in texts[0] and len(texts) == len(LINES)
    assert await _sweep(db, monkeypatch) == 0
    tries = await db.fetchval(
        "SELECT count(*) FROM relisten_attempts WHERE video_id = $1", str(vid)
    )
    assert tries == 0

    # left out so again (a machine from before the fix): not transcribed a second time
    meta = {**_meta(row), "refine": {"source": "skipped", "skipped": tr.NO_WORDS_BEFORE}}
    await db.execute(
        "UPDATE transcripts SET metadata = $2 WHERE id = $1", row["id"], json.dumps(meta)
    )
    assert await _sweep(db, monkeypatch) == 1
    assert (await _last_listen_job(db, vid))["state"] == "pending"
    await drain_core(db, rt, only={"transcribe", "second_listen"})
    assert fake.calls == 2 and len(heard) == 2


@pytest.mark.parametrize("tries", [0, 1])
@pytest.mark.asyncio
async def test_a_job_queued_before_the_update_leaves_it_to_transcribe(
    db: Database,
    tmp_path: Path,
    fake: FakeTranscriber,
    monkeypatch: pytest.MonkeyPatch,
    tries: int,
) -> None:
    """The sweep takes the mark but leaves a video with an open job alone: that job does not
    hear the old words, makes no try, and the next sweep queues transcribe."""
    heard = _hears(monkeypatch)
    rt = await core_runtime(db)
    vid = await _left_out(db, rt, tmp_path, monkeypatch, tr.NO_WORDS_BEFORE)
    res = await enqueue_job(
        db,
        subject_kind="video",
        subject_id=vid,
        stage="second_listen",
        payload={tr.RELISTEN: True},
    )
    await db.execute(
        "UPDATE relisten_attempts SET attempts = 2, stopped_at = NULL, ended_at = NULL,"
        " wait_s = 3600, last_job_id = $2 WHERE video_id = $1",
        str(vid),
        res.job_id,
    )
    assert await _sweep(db, monkeypatch) == 0 and await _fixed(db) == 1
    await db.execute(
        "UPDATE relisten_attempts SET attempts = $2 WHERE video_id = $1", str(vid), tries
    )
    assert await drain_core(db, rt, only={"transcribe", "second_listen"}) == ["second_listen"]
    assert heard == [] and fake.calls == 1
    meta = _meta(await _active(db, vid))
    assert meta["refine"]["skipped"] == tr.NO_WORDS_BEFORE and "words" not in meta

    assert await _sweep(db, monkeypatch) == 1
    made = await db.fetchval("SELECT attempts FROM relisten_attempts WHERE video_id = $1", str(vid))
    assert made == 1  # this one's
    ran = await drain_core(db, rt, only={"transcribe", "second_listen"})
    assert ran == ["transcribe", "second_listen"] and fake.calls == 2 and len(heard) == 1
    assert _meta(await _active(db, vid))["refine"]["source"] == "hf"
    assert await _sweep(db, monkeypatch) == 0


@pytest.mark.asyncio
async def test_a_video_stopped_for_another_reason_stays_stopped(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    _hears(monkeypatch)
    rt = await core_runtime(db)
    vid = await _left_out(db, rt, tmp_path, monkeypatch, "it did not load")
    assert await _sweep(db, monkeypatch) == 0 and await _fixed(db) == 1
    assert _meta(await _active(db, vid))["words"] is True


@pytest.mark.asyncio
async def test_the_sweep_waits_for_a_worker_from_before_the_fix(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    from maktaba_pipeline.engine.workers import deregister_worker, register_worker

    _hears(monkeypatch)
    rt = await core_runtime(db)
    await _left_out(db, rt, tmp_path, monkeypatch, tr.NO_WORDS_BEFORE)

    async def worker(name: str, stages: list[str], caps: dict[str, Any]) -> None:
        await register_worker(
            db, worker_id=name, host=name, stages=stages, pools=["gpu"], capabilities=caps
        )

    await worker("new", ["transcribe", "second_listen"], {"words_by_sentence": True})
    await worker("other", ["probe"], {})
    await worker("old", ["second_listen"], {})
    assert await _sweep(db, monkeypatch) == 0 and await _fixed(db) == 0
    await deregister_worker(db, "old")
    assert await _sweep(db, monkeypatch) == 1 and await _fixed(db) == 1


@pytest.mark.asyncio
async def test_a_rollback_and_reupdate_does_not_strand_videos(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Issue #1765 (item 1): after the once-per-home key is already recorded, a rollback
    that re-transcribes with the old placement (no ``words_by_sentence`` mark) and spends
    tries with ``NO_WORDS_BEFORE`` must still be recovered on the next update, without
    endlessly resetting a video whose ``words`` mark was already stripped."""
    heard = _hears(monkeypatch)
    rt = await core_runtime(db)
    # 1. First update spends the once-per-home key
    assert await _sweep(db, monkeypatch) == 0 and await _fixed(db) == 1

    # 2. Rollback: previous release produces a transcript without words_by_sentence,
    #    records NO_WORDS_BEFORE with words: true, and uses up all tries
    vid = await _left_out(db, rt, tmp_path, monkeypatch, tr.NO_WORDS_BEFORE)
    assert await _fixed(db) == 1

    # 3. Re-update: sweep still recovers the stranded video via transcribe
    assert await _sweep(db, monkeypatch) == 1
    job = await db.fetchrow(
        "SELECT stage, priority FROM processing_jobs WHERE subject_id = $1 AND state = 'pending'",
        str(vid),
    )
    assert (job["stage"], job["priority"]) == ("transcribe", tr.RELISTEN_PRIORITY)
    ran = await drain_core(db, rt, only={"transcribe", "second_listen"})
    assert ran == ["transcribe", "second_listen"] and fake.calls == 2 and len(heard) == 1
    row = await _active(db, vid)
    assert _meta(row)["refine"]["source"] == "hf"
    assert _meta(row)["words_by_sentence"] is True

    # 4. A transcript whose words mark is already gone is NOT reset again once stopped
    v2_dir = tmp_path / "v2"
    v2_dir.mkdir()
    vid2 = await _left_out(db, rt, v2_dir, monkeypatch, tr.NO_WORDS_BEFORE)
    row2 = await _active(db, vid2)
    meta2 = {k: v for k, v in _meta(row2).items() if k != "words"}
    await db.execute(
        "UPDATE transcripts SET metadata = $2 WHERE id = $1", row2["id"], json.dumps(meta2)
    )
    assert await _sweep(db, monkeypatch) == 0
    tries2 = await db.fetchrow(
        "SELECT attempts, stopped_at FROM relisten_attempts WHERE video_id = $1", str(vid2)
    )
    assert tries2["attempts"] == 6 and tries2["stopped_at"] is not None


@pytest.mark.asyncio
async def test_a_transcript_stored_now_with_one_sentence_without_words_is_heard(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    heard = _hears(monkeypatch)
    rt = await core_runtime(db)
    vid, _ = await _one_video(db, tmp_path)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    await db.execute(
        "UPDATE transcript_segments SET word_data = NULL WHERE transcript_id = $1 AND seq = 1",
        (await _active(db, vid))["id"],
    )
    await drain_core(db, rt, only={"second_listen"})
    row = await _active(db, vid)
    texts = await _texts(db, row["id"])
    assert len(heard) == 1 and _meta(row)["refine"]["source"] == "hf"
    assert texts[0].startswith("قال ") and texts[1] == LINES[1][2]


@pytest.mark.asyncio
async def test_words_stored_before_word_data_are_read(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """2026-11-03 to slot 0186 (2026-11-06) each sentence's words were in its
    ``metadata.words``, as ``[start, end, text]``, and ``word_data`` is NULL."""
    heard = _hears(monkeypatch)
    rt = await core_runtime(db)
    vid, _ = await _one_video(db, tmp_path)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    tid = (await _active(db, vid))["id"]
    for seq, (a, b, text) in enumerate(LINES):
        words = [[a, b, w] for w in text.split()]
        await db.execute(
            "UPDATE transcript_segments SET word_data = NULL, metadata = $3"
            " WHERE transcript_id = $1 AND seq = $2",
            tid,
            seq,
            json.dumps({"words": words}),
        )
    await drain_core(db, rt, only={"second_listen"})
    assert len(heard) == 1
    assert _meta(await _active(db, vid))["refine"]["source"] == "hf"


@pytest.mark.parametrize("elsewhere", [False, True])  # words, but of other sentences
@pytest.mark.asyncio
async def test_a_transcript_whisper_gave_no_words_for_is_not_queued_again(
    db: Database,
    tmp_path: Path,
    fake: FakeTranscriber,
    monkeypatch: pytest.MonkeyPatch,
    elsewhere: bool,
) -> None:
    """Transcribing it again would give none either: it keeps Whisper's text, the stages
    after it run, and no sweep queues it."""
    heard = _hears(monkeypatch)

    async def no_words(audio: np.ndarray, **kw: Any) -> Transcript:
        t = await FakeTranscriber.transcribe(fake, audio, **kw)
        for x in t.sentences if elsewhere else []:
            x.words = [Word(x.start, x.end, "غيرها")]
        return t

    monkeypatch.setattr(fake, "transcribe", no_words)
    rt = await core_runtime(db)
    vid, _ = await _one_video(db, tmp_path)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    ran = await drain_core(db, rt, only={"transcribe", "second_listen", "subtitle_gen"})
    assert ran == ["transcribe", "second_listen", "subtitle_gen"] and heard == []
    meta = _meta(await _active(db, vid))
    assert meta["words"] is False and meta["refine"]["source"] == "none"
    assert meta.get("words_unmatched", False) is elsewhere
    assert ("do not match" in meta["refine"]["skipped"]) is elsewhere
    assert await _sweep(db, monkeypatch) == 0


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


# ------------------------------------------------- a machine's copy published (slot 0237)

_REFUSED = 'cannot update table "transcript_segments"'


async def _last_listen_job(db: Database, vid: Any) -> Any:
    return await db.fetchrow(
        "SELECT id, state, error_cause, payload FROM processing_jobs"
        " WHERE subject_id = $1 AND stage = 'second_listen' ORDER BY id DESC LIMIT 1",
        str(vid),
    )


async def _sweep(db: Database, monkeypatch: pytest.MonkeyPatch) -> int:
    """The relisten sweep now, on a worker with the model, the transcripts two hours old."""
    monkeypatch.setattr(tr, "_has_second_listen_model", lambda model: True)
    monkeypatch.setattr(tr, "_LAST_RELISTEN", [])
    await db.execute(
        "UPDATE transcripts SET created_at = $1", to_ts(db, now() - timedelta(hours=2))
    )
    return await tr.relisten(db)


@pytest.mark.asyncio
async def test_a_transcript_whose_second_listen_failed_to_store_is_heard_again(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The second listen ran, but the database refused to store it (a home on PostgreSQL 18
    from 2026-11-20 to slot 0237): the job failed for good, the transcript stayed Whisper's
    with no ``refine``, and the stages after it never ran. The sweep queues it again, and it
    is corrected and its subtitles follow; a failure of another kind is not queued by it."""
    heard = _hears(monkeypatch)
    rt = await core_runtime(db)
    vid, media = await _one_video(db, tmp_path)
    real_store = sl._store
    fail = {"with": "the share timed out"}

    async def refused(*a: Any, **k: Any) -> Any:
        raise RuntimeError(fail["with"])

    monkeypatch.setattr(sl, "_store", refused)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe", "second_listen", "subtitle_gen"})
    job = await _last_listen_job(db, vid)
    assert job["state"] == "failed" and "refine" not in _meta(await _active(db, vid))
    assert await _sweep(db, monkeypatch) == 0  # another failure: not this sweep's

    fail["with"] = f"42P10: {_REFUSED}"
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="second_listen")
    await drain_core(db, rt, only={"second_listen", "subtitle_gen"})
    job = await _last_listen_job(db, vid)
    assert job["state"] == "failed" and "cannot update table" in job["error_cause"]
    assert not media.with_name("درس السيرة 01.ar.vtt").exists()

    monkeypatch.setattr(sl, "_store", real_store)
    assert await _sweep(db, monkeypatch) == 1
    job = await _last_listen_job(db, vid)
    payload = job["payload"] if isinstance(job["payload"], dict) else json.loads(job["payload"])
    assert job["state"] == "pending" and payload == {tr.RELISTEN: True}
    before = len(heard)
    ran = await drain_core(db, rt, only={"second_listen", "subtitle_gen"})
    assert ran == ["second_listen", "subtitle_gen"] and len(heard) == before + 1
    assert _meta(await _active(db, vid))["refine"]["source"] == "hf"
    vtt = media.with_name("درس السيرة 01.ar.vtt").read_text(encoding="utf-8")
    assert "قال السلام عليكم" in vtt
    assert await _sweep(db, monkeypatch) == 0  # corrected: nothing left


@pytest.mark.asyncio
async def test_it_stores_with_a_machine_s_copy_published(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The home as it was on 2026-11-20: a machine's copy published (slot 0147) and
    transcript_segments at REPLICA IDENTITY FULL with its generated search_tsv. On
    PostgreSQL 18 the second listen's UPDATE is refused (on 16 it is not); slot 0237 gives the
    table its primary key as its identity, the sweep queues the video again, and it is
    stored. The publication keeps the table, without search_tsv."""
    if not is_pg(db):
        pytest.skip("a publication and a replica identity are Postgres's")
    from tests.engine.migrate import list_migrations, up_statements

    heard = _hears(monkeypatch)
    rt = await core_runtime(db)
    vid, _ = await _one_video(db, tmp_path)
    pub = await db.fetchval("SELECT cluster_copy_publication($1)", str(uuid4()))
    await db.execute("ALTER TABLE transcript_segments REPLICA IDENTITY FULL")
    major = int(await db.fetchval("SELECT current_setting('server_version_num')::int / 10000"))

    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe", "second_listen", "subtitle_gen"})
    job = await _last_listen_job(db, vid)
    if major >= 18:
        assert job["state"] == "failed" and "cannot update table" in job["error_cause"]
        assert "refine" not in _meta(await _active(db, vid))
    else:
        assert job["state"] == "done"

    (slot,) = [p for p in list_migrations(sqlite=False) if p.name.startswith("0237_")]
    for st in up_statements(slot.read_text(encoding="utf-8")):
        await db.execute(st)
    ident = await db.fetchval(
        "SELECT relreplident::text FROM pg_class WHERE oid = 'transcript_segments'::regclass"
    )
    assert ident == "d"
    before = len(heard)
    if major >= 18:
        assert await _sweep(db, monkeypatch) == 1
        await drain_core(db, rt, only={"second_listen", "subtitle_gen"})
        assert len(heard) == before + 1
    assert (await _last_listen_job(db, vid))["state"] == "done"
    row = await _active(db, vid)
    assert _meta(row)["refine"]["source"] == "hf"
    assert await _texts(db, row["id"]) == ["قال " + t for _a, _b, t in LINES]
    cols = await db.fetchval(
        "SELECT attnames FROM pg_publication_tables WHERE pubname = $1"
        " AND tablename = 'transcript_segments'",
        pub,
    )
    assert cols is not None and "text" in cols and "search_tsv" not in cols
    # a reviewer's fix and a delete go through too
    await db.execute(
        "UPDATE transcript_segments SET text = text WHERE transcript_id = $1", row["id"]
    )
    await db.execute("DELETE FROM transcripts WHERE id = $1", _meta(row)["refine"]["whisper"])
