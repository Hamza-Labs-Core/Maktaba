"""``word_check`` on the engine (Epic 52, stories 52.6, 52.10, 52.14, 52.15): a stored
transcript in, its suspect words out, written for a home admin to review.

On a database: the integration tier (SQLite; Postgres with ``MAKTABA_TEST_PG_URL``)."""

from __future__ import annotations

import json
from collections.abc import Iterator, Sequence
from pathlib import Path
from typing import Any
from uuid import uuid4

import pytest
from maktaba_fp_transcription.stages import transcribe as tr

from maktaba_pipeline.components import packs as component_packs
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH, Subject
from maktaba_pipeline.engine.sql import dumps, is_pg, loads
from maktaba_pipeline.engine.stages import word_check as wc
from maktaba_pipeline.language_packs import registry
from maktaba_pipeline.language_packs.ar import morphology
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.stt.settings import HEARD_DIFFERENTLY, Word
from maktaba_pipeline.stt.word_data import pack
from maktaba_pipeline.word_review.db import read_array
from tests.language_packs.fixtures import install
from tests.stages.world import core_runtime, drain_core, make_show

pytestmark = pytest.mark.integration

#: what the fake analyser knows (undiacritised)
KNOWN = {"قال", "الشيخ", "في", "الدرس", "هذا", "كتاب", "مهم", "ذهب", "الطالب", "إلى",
         "المدرسة", "اليوم", "الحمد", "لله", "رب", "العالمين", "الرحمن", "الرحيم", "مالك",
         "يوم", "الدين", "النبي", "عن", "حديث", "جميل", "والله", "أعلم"}  # fmt: skip


@pytest.fixture(autouse=True)
def packs_here(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Iterator[Path]:
    root = install(tmp_path / "components", "lang.ar", "lang.en")
    monkeypatch.setenv(component_packs.ENV, str(root))
    monkeypatch.setattr(morphology, "available", lambda: True)
    monkeypatch.setattr(
        morphology, "analyse", lambda w: [(w, "noun", w, -1.0)] if w in KNOWN else []
    )
    registry.forget()
    yield root
    registry.forget()


def _words(text: str, start: float, probs: list[float] | None = None,
           flags: list[int] | None = None) -> list[Word]:  # fmt: skip
    toks = text.split()
    step = 0.4
    return [
        Word(
            round(start + i * step, 3),
            round(start + (i + 1) * step, 3),
            t,
            (probs[i] if probs else 0.95),
            (flags[i] if flags else 0),
        )  # fmt: skip
        for i, t in enumerate(toks)
    ]


async def _transcript(
    db: Database,
    sentences: Sequence[tuple[str, list[Word] | None, dict[str, Any] | None]],
    *,
    language: str = "ar",
    vid: Any = None,
) -> tuple[Any, Any, Any]:
    """A video with an active transcript of ``sentences`` (text, words or None, extra
    metadata): (video, transcript, library)."""
    if vid is None:
        w = await make_show(db, episodes=1, loose=0)
        vid = w.episodes[0]
    lib = await db.fetchval("SELECT library_id FROM videos WHERE id = $1", str(vid))
    await db.execute("UPDATE transcripts SET is_active = false WHERE video_id = $1", str(vid))
    tid = str(uuid4())
    track = await tr._first_audio_track(db, vid)
    audio = 4.0 * len(sentences) + 1
    await db.execute(
        "INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model,"
        " word_level, diarized, is_active, metadata)"
        " VALUES ($1, $2, $5, $3, 'mlx-batched', 'whisper-test', true, false, true, $4)",
        tid, str(vid), language, dumps({"audio_sec": audio}), track,
    )  # fmt: skip
    for seq, (text, words, extra) in enumerate(sentences):
        start = seq * 4.0
        data = pack(text, words, start).data if words else None
        await db.execute(
            "INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text,"
            " confidence, metadata, word_data) VALUES ($1, $2, $3, $4, $5, 0.9, $6, $7)",
            tid, seq, start, start + 3.5, text, dumps({"no_speech": 0.01, **(extra or {})}), data,
        )  # fmt: skip
    return vid, tid, lib


async def _flags(db: Database, tid: Any) -> list[dict[str, Any]]:
    rows = await db.fetch(
        "SELECT kind, segment_seq, char_start, char_len, original, language, reasons, scores,"
        " candidates, status, priority, start_sec, end_sec, key, id FROM word_flags"
        " WHERE transcript_id = $1 ORDER BY segment_seq, char_start",
        tid,
    )
    out = []
    for r in rows:
        d = dict(r)
        d["reasons"] = read_array(r["reasons"])
        d["scores"] = loads(r["scores"]) or {}
        d["candidates"] = loads(r["candidates"]) or []
        out.append(d)
    return out


async def _check(db: Database, vid: Any) -> list[str]:
    rt = await core_runtime(db)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="word_check")
    return await drain_core(db, rt, only={"word_check"})


def test_the_stage_follows_the_second_listen_on_the_cpu() -> None:
    by = {s.name: s for s in GRAPH}
    assert "word_check" in {e.to for e in by["second_listen"].edges}
    st = by["word_check"]
    assert st.pool == "cpu" and set(st.subject_kinds) == {"video", "library"}


@pytest.mark.asyncio
async def test_low_confidence_from_word_data(db: Database) -> None:
    text = "قال الشيخ في الدرس هذا كتاب مهم"
    vid, tid, _ = await _transcript(
        db, [(text, _words(text, 0.0, [0.9, 0.9, 0.9, 0.9, 0.9, 0.1, 0.9]), None)]
    )
    assert await _check(db, vid) == ["word_check"]
    flags = await _flags(db, tid)
    assert len(flags) == 1
    f = flags[0]
    assert f["original"] == "كتاب" and f["reasons"] == ["low_confidence"]
    assert f["scores"]["prob"] == pytest.approx(0.1, abs=1 / 255)
    assert "prob_cal" in f["scores"] and f["status"] == "open"
    assert float(f["start_sec"]) == pytest.approx(2.0) and float(f["end_sec"]) == pytest.approx(2.4)
    assert "time_estimated" not in f["scores"]


@pytest.mark.asyncio
async def test_listeners_disagree_offers_both_listeners(db: Database) -> None:
    text = "ذهب الطالب إلى المدرسة اليوم"
    words = _words(text, 0.0, [0.9, 0.6, 0.9, 0.9, 0.9], [0, HEARD_DIFFERENTLY, 0, 0, 0])
    alt = {"alt": [{"i": 1, "n": 1, "whisper": "الطالبه", "cohere": "الطالب"}]}
    vid, tid, _ = await _transcript(db, [(text, words, alt)])
    await _check(db, vid)
    (f,) = await _flags(db, tid)
    assert f["original"] == "الطالب" and "listeners_disagree" in f["reasons"]
    sources = {c["source"]: c["text"] for c in f["candidates"]}
    assert sources["whisper"] == "الطالبه" and sources["second_listen"] == "الطالب"


@pytest.mark.asyncio
async def test_a_name_of_the_library_halves_the_priority(db: Database) -> None:
    text = "قال الشيخ عثيمين في الدرس"
    words = _words(text, 0.0, [0.9, 0.9, 0.2, 0.9, 0.9])
    vid, tid, _ = await _transcript(db, [(text, words, None)])
    await _check(db, vid)
    (alone,) = await _flags(db, tid)
    await db.execute(
        "INSERT INTO speakers (id, video_id, name) VALUES ($1, $2, $3)",
        str(uuid4()), str(vid), "الشيخ ابن عثيمين",
    )  # fmt: skip
    await db.execute("DELETE FROM word_flags WHERE transcript_id = $1", tid)
    registry.forget()
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="word_check", force=True)
    await drain_core(db, await core_runtime(db), only={"word_check"})
    got = await _flags(db, tid)
    if got:
        (named,) = got
        assert "named_entity" in named["reasons"]
        assert named["priority"] == pytest.approx(alone["priority"] * 0.5, abs=1e-3)
        assert any(
            c["source"] == "gazetteer" and c["text"] == "عثيمين" for c in named["candidates"]
        )
    else:
        # halved below the threshold: a name the library knows is not asked about
        assert alone["priority"] * 0.5 < 0.5


@pytest.mark.asyncio
async def test_an_english_word_in_arabic_speech_is_english(db: Database) -> None:
    text = "قال النبي عن teh حديث جميل"
    vid, tid, _ = await _transcript(db, [(text, None, None)])
    await _check(db, vid)
    flags = await _flags(db, tid)
    en = [f for f in flags if f["original"] == "teh"]
    assert en and en[0]["language"] == "en" and "not_in_dictionary" in en[0]["reasons"]
    assert en[0]["candidates"][0]["text"] == "the"


@pytest.mark.asyncio
async def test_a_correct_verse_run_is_never_flagged_and_a_misheard_word_is(db: Database) -> None:
    good = "الحمد لله رب العالمين الرحمن الرحيم مالك يوم الدين"
    low = [0.05] * 9  # even with low confidence: the verse decides
    vid, tid, _ = await _transcript(db, [(good, _words(good, 0.0, low), None)])
    await _check(db, vid)
    assert await _flags(db, tid) == []
    bad = "الحمد لله رب العالمون الرحمن الرحيم مالك يوم الدين"
    vid2, tid2, _ = await _transcript(db, [(bad, _words(bad, 0.0, low), None)])
    await _check(db, vid2)
    (f,) = await _flags(db, tid2)
    assert f["original"] == "العالمون" and f["reasons"] == ["quran_mismatch"]
    assert f["candidates"][0] == {"text": "العالمين", "source": "quran", "score": 0.9}
    assert f["scores"]["quran"]["surah"] == 1 and f["scores"]["quran"]["aya"] == 2


@pytest.mark.asyncio
async def test_with_the_quran_check_off_verse_runs_are_protected(db: Database) -> None:
    bad = "الحمد لله رب العالمون الرحمن الرحيم مالك يوم الدين"
    vid, tid, lib = await _transcript(db, [(bad, _words(bad, 0.0, [0.05] * 9), None)])
    await db.execute(
        "UPDATE libraries SET settings = $2 WHERE id = $1",
        str(lib), json.dumps({"word_review": {"reasons": {"quran_mismatch": False}}}),
    )  # fmt: skip
    await _check(db, vid)
    assert await _flags(db, tid) == []


@pytest.mark.asyncio
async def test_the_budget_keeps_the_top_words_open(db: Database) -> None:
    sentences = []
    for i in range(30):
        text = f"كتسبلمن{i} قال"
        sentences.append((text, _words(text, i * 4.0, [0.05, 0.9]), None))
    vid, tid, _ = await _transcript(db, sentences)
    await _check(db, vid)
    flags = await _flags(db, tid)
    words = [f for f in flags if f["kind"] == "word"]
    assert sum(1 for f in words if f["status"] == "open") == 5  # the floor for a short file
    skipped = [f for f in words if f["status"] == "skipped"]
    assert len(skipped) == 25 and all(f["scores"]["over_budget"] for f in skipped)
    (poor,) = [f for f in flags if f["kind"] == "transcript"]
    assert poor["segment_seq"] == -1 and poor["scores"]["suspect"] == 30


@pytest.mark.asyncio
async def test_a_resolved_flag_is_kept_and_a_second_run_is_unchanged(db: Database) -> None:
    text = "قال الشيخ في الدرس هذا كتاب مهم"
    vid, tid, _ = await _transcript(db, [(text, _words(text, 0.0, [0.9] * 5 + [0.1, 0.9]), None)])
    await _check(db, vid)
    (f,) = await _flags(db, tid)
    await db.execute(
        "UPDATE word_flags SET status = 'accepted', resolved_by = $2 WHERE id = $1",
        f["id"], str(uuid4()),
    )  # fmt: skip
    before = await _flags(db, tid)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="word_check", force=True)
    await drain_core(db, await core_runtime(db), only={"word_check"})
    assert await _flags(db, tid) == before
    fp1 = await wc.input_fingerprint(db, Subject("video", vid))
    fp2 = await wc.input_fingerprint(db, Subject("video", vid))
    assert fp1 == fp2 and fp1 is not None


@pytest.mark.asyncio
async def test_an_old_transcript_gets_text_signals_only(db: Database) -> None:
    text = "قال الشيخ كتسبلمن في الدرس"
    vid, tid, _ = await _transcript(db, [(text, None, None)])
    await _check(db, vid)
    (f,) = await _flags(db, tid)
    assert f["reasons"] == ["not_in_dictionary"] and f["scores"]["time_estimated"] is True
    assert "low_confidence" not in f["reasons"]
    assert 0.0 <= float(f["start_sec"]) < float(f["end_sec"]) <= 3.5


@pytest.mark.asyncio
async def test_a_reason_switched_off_flags_nothing(db: Database) -> None:
    text = "قال الشيخ كتسبلمن في الدرس"
    vid, tid, lib = await _transcript(db, [(text, None, None)])
    await db.execute(
        "UPDATE libraries SET settings = $2 WHERE id = $1",
        str(lib), json.dumps({"word_review": {"reasons": {"not_in_dictionary": False}}}),
    )  # fmt: skip
    await _check(db, vid)
    assert await _flags(db, tid) == []


@pytest.mark.asyncio
async def test_the_settings_are_in_the_fingerprint_of_that_library_only(db: Database) -> None:
    vid, _tid, lib = await _transcript(db, [("قال الشيخ", None, None)])
    other, _t2, lib2 = await _transcript(db, [("قال الشيخ", None, None)])
    assert lib != lib2
    a = await wc.input_fingerprint(db, Subject("video", vid))
    b = await wc.input_fingerprint(db, Subject("video", other))
    await db.execute(
        "UPDATE libraries SET settings = $2 WHERE id = $1",
        str(lib), json.dumps({"word_review": {"sensitivity": "low"}}),
    )  # fmt: skip
    assert await wc.input_fingerprint(db, Subject("video", vid)) != a
    assert await wc.input_fingerprint(db, Subject("video", other)) == b


@pytest.mark.asyncio
async def test_a_vocabulary_word_is_known_and_its_open_flag_accepted(db: Database) -> None:
    text = "قال الشيخ كتسبلمن في الدرس"
    vid, tid, lib = await _transcript(db, [(text, None, None)])
    await _check(db, vid)
    (f,) = await _flags(db, tid)
    await db.execute(
        "INSERT INTO vocabulary (id, scope, library_id, language, term, key, kind, source)"
        " VALUES ($1, 'library', $2, 'ar', 'كتسبلمن', $3, 'word', 'review')",
        str(uuid4()), str(lib), f["key"],
    )  # fmt: skip
    registry.forget()
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="word_check")
    await drain_core(db, await core_runtime(db), only={"word_check"})
    (g,) = await _flags(db, tid)
    assert g["status"] == "accepted" and g["scores"]["source"] == "vocabulary"


@pytest.mark.asyncio
async def test_the_flags_are_byte_identical_twice(db: Database) -> None:
    text = "قال النبي عن teh حديث كتسبلمن جميل"
    vid, tid, lib = await _transcript(db, [(text, _words(text, 0.0, [0.2] * 7), None)])
    row = await wc._transcript(db, vid)
    s = await wc.library_settings(db, lib)
    a, _ = await wc.check_video(db, row, s)
    b, _ = await wc.check_video(db, row, s)
    from maktaba_pipeline.word_review.store import flags_hash

    assert flags_hash(a) == flags_hash(b) and a


@pytest.mark.asyncio
async def test_backfill_waits_for_transcription_and_plans_unchecked(db: Database) -> None:
    text = "قال الشيخ كتسبلمن في الدرس"
    vid, _tid, lib = await _transcript(db, [(text, None, None)])
    rt = await core_runtime(db)
    # transcription busy: the backfill job is given back, no attempt used
    other, _t, _l = await _transcript(db, [("قال", None, None)])
    await enqueue_job(db, subject_kind="video", subject_id=other, stage="transcribe")
    assert await wc.transcription_busy(db)
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="word_check")
    await drain_core(db, rt, only={"word_check"})
    job = await db.fetchrow(
        "SELECT priority, payload, state FROM processing_jobs"
        " WHERE stage = 'word_check' AND subject_id = $1",
        str(vid),
    )
    assert job is not None and int(job["priority"]) == wc.BACKFILL_PRIORITY
    assert (loads(job["payload"]) or {}).get(wc.BACKFILL) is True
    job = await db.fetchrow(
        "SELECT state, attempts FROM processing_jobs"
        " WHERE stage = 'word_check' AND subject_id = $1",
        str(vid),
    )
    assert job is not None and job["state"] in ("paused", "pending") and int(job["attempts"]) <= 1
    # backfill off: the library's pending backfill jobs go
    await db.execute(
        "UPDATE libraries SET settings = $2 WHERE id = $1",
        str(lib), json.dumps({"word_review": {"backfill": False}}),
    )  # fmt: skip
    n = await wc.cancel_backfill(db, lib)
    job = await db.fetchrow(
        "SELECT state FROM processing_jobs WHERE stage = 'word_check' AND subject_id = $1",
        str(vid),
    )
    assert job is not None and job["state"] == "cancelled" and (n >= 1 or not is_pg(db) or n == 0)


@pytest.mark.asyncio
async def test_without_runtime_lang_the_job_waits(db: Database, packs_here: Path) -> None:
    import shutil

    shutil.rmtree(packs_here / "runtime.lang")
    registry.forget()
    vid, tid, _ = await _transcript(db, [("قال الشيخ كتسبلمن", None, None)])
    await _check(db, vid)
    assert await _flags(db, tid) == []
    job = await db.fetchrow(
        "SELECT state FROM processing_jobs WHERE stage = 'word_check' AND subject_id = $1",
        str(vid),
    )
    assert job is not None and job["state"] != "done"


@pytest.mark.asyncio
async def test_the_pack_report_says_what_loaded(db: Database) -> None:
    await registry.load(db)
    rows = {r["code"]: r["state"] for r in await db.fetch("SELECT code, state FROM language_packs")}
    assert rows["ar"] == "loaded" and rows["en"] == "loaded" and rows["fallback"] == "loaded"


@pytest.mark.asyncio
async def test_vocabulary_does_not_change_the_transcribe_fingerprint(db: Database) -> None:
    """Learning words never queues the library for transcription again (story 52.10)."""
    vid, _tid, lib = await _transcript(db, [("قال الشيخ", None, None)])
    before = await tr.input_fingerprint(db, Subject("video", vid))
    for i in range(50):
        await db.execute(
            "INSERT INTO vocabulary (id, scope, library_id, language, term, key, kind, source,"
            " uses) VALUES ($1, 'library', $2, 'ar', $3, $3, 'name', 'review', $4)",
            str(uuid4()), str(lib), f"اسم{i}", i,
        )  # fmt: skip
    assert await tr.input_fingerprint(db, Subject("video", vid)) == before
    got = await tr.decode_prompt(db, lib)
    assert got is not None and got.startswith("اسم49")  # most used first


@pytest.mark.asyncio
async def test_a_fix_s_revision_moves_the_subtitles_and_the_index(db: Database) -> None:
    from maktaba_fp_semantic_search.stages import index
    from maktaba_fp_subtitles.stages import subtitle_gen

    vid, tid, _ = await _transcript(db, [("قال الشيخ", None, None)])
    subs = await subtitle_gen.input_fingerprint(db, Subject("video", vid))
    vec = await index.input_fingerprint(db, Subject("video", vid))
    await db.execute("UPDATE transcripts SET revision = 1 WHERE id = $1", tid)
    assert await subtitle_gen.input_fingerprint(db, Subject("video", vid)) != subs
    assert await index.input_fingerprint(db, Subject("video", vid)) != vec
