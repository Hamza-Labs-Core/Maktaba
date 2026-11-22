"""``keyword_index`` on the engine (Epic 49, story 49.6): a video's transcript, title and
speakers in, its keyword search document out, in ``video_search_docs``.

On a database: the integration tier (SQLite; Postgres with ``MAKTABA_TEST_PG_URL``)."""

from __future__ import annotations

from collections.abc import Iterator, Sequence
from typing import Any
from uuid import uuid4

import pytest
from maktaba_fp_transcription.stages import transcribe as tr

from maktaba_pipeline.engine import graph as graph_mod
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.sql import is_pg, loads
from maktaba_pipeline.engine.stages import keyword_index as stage
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.search.textnorm import normalize
from tests.engine.factory import make_library, make_video
from tests.stages.world import core_runtime, drain_core

pytestmark = pytest.mark.integration


@pytest.fixture(autouse=True)
def clean_stage() -> Iterator[None]:
    stage._LAST_PLAN.clear()
    yield
    stage._LAST_PLAN.clear()


async def _transcript(db: Database, vid: Any, lines: Sequence[str], *, language: str = "ar") -> str:
    """The video's active transcript of ``lines`` (any earlier one made inactive)."""
    await db.execute("UPDATE transcripts SET is_active = false WHERE video_id = $1", str(vid))
    tid = str(uuid4())
    track = await tr._first_audio_track(db, vid)
    await db.execute(
        "INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model,"
        " word_level, diarized, is_active, metadata)"
        " VALUES ($1, $2, $4, $3, 'mlx-batched', 'whisper-test', false, false, true, '{}')",
        tid, str(vid), language, track,
    )  # fmt: skip
    for seq, text in enumerate(lines):
        await db.execute(
            "INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text,"
            " confidence, metadata) VALUES ($1, $2, $3, $4, $5, 0.9, '{}')",
            tid, seq, seq * 5.0, seq * 5.0 + 4, text,
        )  # fmt: skip
    return tid


async def _speaker(db: Database, vid: Any, tid: str, name: str, seqs: Sequence[int],
                   *, unknown: int | None = None) -> str:  # fmt: skip
    sid = str(uuid4())
    await db.execute(
        "INSERT INTO speakers (id, video_id, name, unknown_index) VALUES ($1, $2, $3, $4)",
        sid, str(vid), name, unknown,
    )  # fmt: skip
    for seq in seqs:
        seg = await db.fetchval(
            "SELECT id FROM transcript_segments WHERE transcript_id = $1 AND seq = $2", tid, seq
        )
        await db.execute(
            "INSERT INTO segment_speakers (segment_id, speaker_id) VALUES ($1, $2)", seg, sid
        )
    return sid


async def _doc(db: Database, vid: Any) -> dict[str, Any] | None:
    r = await db.fetchrow("SELECT * FROM video_search_docs WHERE video_id = $1", str(vid))
    return None if r is None else dict(r)


async def _index(db: Database, rt: Any, vid: Any) -> dict[str, Any]:
    """Run the stage for the video; the job's summary."""
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="keyword_index")
    assert await drain_core(db, rt, only={"keyword_index"}) == ["keyword_index"]
    row = await db.fetchrow(
        "SELECT state, output_summary FROM processing_jobs WHERE stage = 'keyword_index'"
        " AND subject_id = $1 ORDER BY id DESC LIMIT 1",
        str(vid),
    )
    assert row["state"] == "done"
    return dict(loads(row["output_summary"]) or {})


def test_the_stage_follows_the_second_listen_and_shows_on_the_queues_page() -> None:
    """A new transcript queues it (the second listen's edge, which every transcript
    passes), and the Queues page lists it by its label (job_stages, from the graph)."""
    by = {s.name: s for s in GRAPH}
    assert "keyword_index" in {e.to for e in by["second_listen"].edges}
    st = by["keyword_index"]
    assert set(st.subject_kinds) == {"video", "library"} and st.pool == "cpu"
    assert st.label == "Keyword index" and st.version == graph_mod.KEYWORD_INDEX_VERSION


def test_python_folds_as_go_and_sql_do() -> None:
    assert normalize("إِسْلَامٌ أحمد آمن ٱلله هدى مدرسة مؤمن سائل صبــر ١٢٣ ۴۵") == (
        "اسلام احمد امن الله هدي مدرسه مومن سايل صبر 123 45"
    )
    assert normalize("café ﷲ") == "café ﷲ"  # Latin and presentation forms as they are


def test_the_title_is_the_one_shown_with_the_file_name() -> None:
    row = {"display_title_manual": None, "display_title": "درس الصبر", "title": "raw",
           "filename": "lesson-01.mp4"}  # fmt: skip
    assert stage.shown_title(row) == "درس الصبر\nlesson-01"
    row |= {"display_title_manual": "  ", "display_title": "", "title": "", "filename": "a.b.mkv"}
    assert stage.shown_title(row) == "a.b"
    assert stage.strip_ext("notes") == "notes" and stage.strip_ext(".mp4") == ".mp4"


@pytest.mark.asyncio
async def test_a_transcript_becomes_a_document(db: Database) -> None:
    rt = await core_runtime(db)
    lib = await make_library(db)
    vid = await make_video(db, lib, title="درس في الصبر", filename="sabr-01.mp4")
    tid = await _transcript(db, vid, ["إن الصبر عند المصيبة", "  ", "والصابرون لهم أجر"])
    await _speaker(db, vid, tid, "الشيخ أحمد", [0, 2])
    await _speaker(db, vid, tid, "Speaker 2", [2], unknown=2)  # not named: left out
    summary = await _index(db, rt, vid)
    assert summary["keyword_index"] == "written"
    d = await _doc(db, vid)
    assert d is not None
    assert d["title"] == "درس في الصبر\nsabr-01"
    assert d["speakers"] == "الشيخ أحمد"
    assert d["body"] == "إن الصبر عند المصيبة\nوالصابرون لهم أجر"  # blank lines dropped
    assert (str(d["transcript_id"]), d["transcript_revision"], d["doc_version"]) == (
        tid, 0, stage.KEYWORD_INDEX_VERSION)  # fmt: skip
    assert str(d["library_id"]) == str(lib) and d["language"] == "ar"
    if not is_pg(db):
        # SQLite's FTS5 row, normalised: a spelling variant finds it
        got = await db.fetchval(
            "SELECT f.video_id FROM video_search_docs_fts f WHERE video_search_docs_fts MATCH $1",
            normalize("المصيبه"),
        )
        assert str(got) == str(vid)
    # run again with nothing changed: unchanged, the row as it was
    assert (await _index(db, rt, vid))["keyword_index"] == "current"


@pytest.mark.asyncio
async def test_a_video_with_no_transcript_left_loses_its_document(db: Database) -> None:
    rt = await core_runtime(db)
    lib = await make_library(db)
    vid = await make_video(db, lib, title="t")
    await _transcript(db, vid, ["كلمة"])
    await _index(db, rt, vid)
    await db.execute("UPDATE transcripts SET is_active = false WHERE video_id = $1", str(vid))
    assert (await _index(db, rt, vid))["keyword_index"] == "removed"
    assert await _doc(db, vid) is None
    if not is_pg(db):
        assert await db.fetchval("SELECT count(*) FROM video_search_docs_fts") == 0
    assert (await _index(db, rt, vid))["keyword_index"] == "no transcript"


@pytest.mark.asyncio
async def test_the_plan_queues_what_is_stale_one_job_a_video_behind_all_work(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A new transcript, a title change, an edit (revision) and a version bump each queue
    the video again; the backfill is one job a video, at the lowest priority."""
    rt = await core_runtime(db)
    lib = await make_library(db)
    vids = [await make_video(db, lib, title=f"درس {i}") for i in range(3)]
    tids = [await _transcript(db, v, [f"كلمة {i}"]) for i, v in enumerate(vids)]
    bare = await make_video(db, lib, title="no transcript")

    def pending() -> Any:
        return db.fetch(
            "SELECT subject_kind, subject_id, priority FROM processing_jobs"
            " WHERE stage = 'keyword_index' AND state = 'pending' ORDER BY subject_id"
        )

    plan = await stage.plan_library(db, lib)
    assert plan.summary == {"planned": 3, "videos": 4}
    jobs = await pending()
    assert {str(j["subject_id"]) for j in jobs} == {str(v) for v in vids}  # not `bare`
    assert {(j["subject_kind"], j["priority"]) for j in jobs} == {
        ("video", stage.BACKFILL_PRIORITY)
    }
    assert stage.BACKFILL_PRIORITY > 100
    # planning again while they wait queues nothing twice
    assert (await stage.plan_library(db, lib)).summary["planned"] == 0
    await drain_core(db, rt, only={"keyword_index"})
    assert all([await _doc(db, v) for v in vids]) and await _doc(db, bare) is None
    plan = await stage.plan_library(db, lib)
    assert plan.outcome == "unchanged" and plan.summary["planned"] == 0

    # a title changes (any writer: the API, display_title, enrich): that video
    await db.execute(
        "UPDATE videos SET display_title_manual = 'عنوان جديد' WHERE id = $1", str(vids[0])
    )
    # a transcript is edited (its revision bumped): that video
    await db.execute("UPDATE transcripts SET revision = 1 WHERE id = $1", tids[1])
    # a new transcript arrives some other way (a merge): that video
    await _transcript(db, bare, ["وصل نص"])
    plan = await stage.plan_library(db, lib)
    assert {str(j["subject_id"]) for j in await pending()} == {
        str(vids[0]),
        str(vids[1]),
        str(bare),
    }
    await drain_core(db, rt, only={"keyword_index"})
    d0, d1 = await _doc(db, vids[0]), await _doc(db, vids[1])
    assert d0 is not None and d0["title"].startswith("عنوان جديد")
    assert d1 is not None and d1["transcript_revision"] == 1

    # the stage's version is bumped: every document again, one job a video, at the
    # backfill's priority; until its job runs a video keeps its document
    monkeypatch.setattr(stage, "KEYWORD_INDEX_VERSION", stage.KEYWORD_INDEX_VERSION + 1)
    plan = await stage.plan_library(db, lib)
    assert plan.summary["planned"] == 4
    jobs = await pending()
    assert len(jobs) == 4 and {j["priority"] for j in jobs} == {stage.BACKFILL_PRIORITY}
    assert await drain_core(db, rt, only={"keyword_index"}, max_jobs=1) == ["keyword_index"]
    v0 = stage.KEYWORD_INDEX_VERSION - 1
    versions = sorted([(await _doc(db, v) or {})["doc_version"] for v in (*vids, bare)])
    assert versions == [v0, v0, v0, v0 + 1]
    await drain_core(db, rt, only={"keyword_index"})
    assert {(await _doc(db, v) or {})["doc_version"] for v in (*vids, bare)} == {v0 + 1}


@pytest.mark.asyncio
async def test_a_deleted_video_loses_its_document_through_the_plan(db: Database) -> None:
    rt = await core_runtime(db)
    lib = await make_library(db)
    vid = await make_video(db, lib, title="t")
    await _transcript(db, vid, ["كلمة"])
    await _index(db, rt, vid)
    await db.execute("UPDATE videos SET deleted_at = created_at WHERE id = $1", str(vid))
    assert (await stage.plan_library(db, lib)).summary["planned"] == 1
    await drain_core(db, rt, only={"keyword_index"})
    assert await _doc(db, vid) is None


@pytest.mark.asyncio
async def test_the_worker_plans_each_library_now_and_then(db: Database) -> None:
    await core_runtime(db)
    lib = await make_library(db)
    await stage.refresh(db, False)
    assert not await db.fetchval(
        "SELECT count(*) FROM processing_jobs WHERE stage = 'keyword_index'"
    )
    await stage.refresh(db, True)
    await stage.refresh(db, True)  # within the interval: once
    jobs = await db.fetch(
        "SELECT subject_kind, subject_id, priority FROM processing_jobs"
        " WHERE stage = 'keyword_index'"
    )
    assert [(j["subject_kind"], str(j["subject_id"]), j["priority"]) for j in jobs] == [
        ("library", str(lib), stage.BACKFILL_PRIORITY)]  # fmt: skip


@pytest.mark.asyncio
async def test_python_folds_as_postgres_does(db: Database) -> None:
    """SQLite's FTS5 rows are normalised in Python, Postgres's indexes by
    ``maktaba_normalize()``: both fold the same text the same way (Go's textnorm is held to
    the same function by its own test)."""
    if not is_pg(db):
        pytest.skip("maktaba_normalize() is Postgres's")
    corpus = ["إسلام", "أحمد آمن ٱلله", "هدى مدرسة مؤمن سائل", "اللَّهُ أَكْبَرُ", "صبــر",
              "الرَّحْمٰنِ الرَّحِيمِ", "١٢٣٤٥٦٧٨٩٠ ۰۱۲۳۴۵۶۷۸۹", "café crème déjà vu",
              "Ayah 255: آية الكرسي", "", "ًٌٍَُِّْٰـ", "plain ascii 0123", "قرآن – ﷲ ﻻ"]  # fmt: skip
    for s in corpus:
        assert normalize(s) == await db.fetchval("SELECT maktaba_normalize($1)", s), s


@pytest.mark.asyncio
async def test_a_write_cut_short_leaves_no_document_without_its_search_row(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Review M1: the row and SQLite's FTS5 row are written together. A run that fails
    between them leaves neither, so its retry writes both."""
    rt = await core_runtime(db)
    lib = await make_library(db)
    vid = await make_video(db, lib, title="درس")
    await _transcript(db, vid, ["الصبر عند المصيبة"])
    real = normalize
    target = "maktaba_pipeline.engine.stages.keyword_index.normalize"

    def broken(s: str) -> str:
        raise RuntimeError("cut short")

    monkeypatch.setattr(target, broken if not is_pg(db) else real)
    doc = await stage.build(db, vid)
    assert doc is not None
    if not is_pg(db):
        with pytest.raises(RuntimeError):
            await stage._write(db, doc)
        assert await _doc(db, vid) is None
    monkeypatch.setattr(target, real)
    assert (await _index(db, rt, vid))["keyword_index"] == "written"
    if not is_pg(db):
        assert await db.fetchval("SELECT count(*) FROM video_search_docs_fts") == 1


# --- story 49.8: the terms dictionary -------------------------------------------------


async def _terms(db: Database, lib: Any) -> dict[str, tuple[str, int]]:
    rows = await db.fetch(
        "SELECT term, shown, df FROM search_terms WHERE library_id = $1", str(lib)
    )
    return {str(r["term"]): (str(r["shown"]), int(r["df"])) for r in rows}


def test_a_documents_words_are_keyed_as_search_matches_them() -> None:
    from maktaba_pipeline.search.terms import doc_terms, key, words

    assert words("اللَّهُ أكبرُ، في ٢٠٢٤ x Café-au-lait 2024") == [
        "الله", "أكبر", "في", "Café", "au", "lait"]  # fmt: skip
    assert key("أكبرُ") == "اكبر" and key("Café") == "cafe" and key("مدرسة") == "مدرسه"
    # the shown form is the one written most (harakat gone), Latin in lower case
    assert doc_terms("مدرسةٌ مدرسة مدرسه", "Café") == {"مدرسه": "مدرسة", "cafe": "café"}


def test_python_keys_words_as_go_does() -> None:
    """The cases Go's searchterms test reads too (shared/testdata/searchterms.json)."""
    import json
    from pathlib import Path

    from maktaba_pipeline.search.terms import doc_terms

    cases = json.loads(
        (Path(__file__).resolve().parents[3] / "shared/testdata/searchterms.json").read_text()
    )
    for c in cases:
        assert doc_terms(c["text"]) == c["terms"], c["text"]


@pytest.mark.asyncio
async def test_the_dictionary_counts_the_documents_words(db: Database) -> None:
    """Each document's words count once per library; an edit moves the counts, a lost
    transcript takes them off, and a library's words never count in another's."""
    rt = await core_runtime(db)
    lib, other = await make_library(db), await make_library(db)
    a = await make_video(db, lib, title="درس", filename="a.mp4")
    b = await make_video(db, lib, title="درس", filename="b.mp4")
    c = await make_video(db, other, title="Other", filename="c.mp4")
    ta = await _transcript(db, a, ["إن الصبر عند المصيبة", "الصبر الصبر"])
    await _speaker(db, a, ta, "الشيخ  أحمد", [0])
    await _transcript(db, b, ["الصبرُ مفتاح الفرج"])
    await _transcript(db, c, ["كلمة أخرى"])
    for v in (a, b, c):
        await _index(db, rt, v)
    t = await _terms(db, lib)
    assert t["الصبر"] == ("الصبر", 2) and t["المصيبه"] == ("المصيبة", 1)
    assert t["درس"] == ("درس", 2) and "كلمه" not in t
    assert (await _terms(db, other))["كلمه"] == ("كلمة", 1)

    # the named speakers, by each word start
    sp = {
        (str(r["key"]), str(r["name"])): int(r["df"])
        for r in await db.fetch(
            "SELECT key, name, df FROM search_speakers WHERE library_id = $1", str(lib)
        )
    }
    assert sp == {("الشيخ احمد", "الشيخ أحمد"): 1, ("احمد", "الشيخ أحمد"): 1}, sp

    # an edit: the old words leave, the new ones join
    await _transcript(db, a, ["الصبر جميل"])
    await _index(db, rt, a)
    t = await _terms(db, lib)
    assert t["الصبر"][1] == 2 and "المصيبه" not in t and t["جميل"] == ("جميل", 1)
    # the new transcript names no one: the speaker leaves
    assert await db.fetchval("SELECT count(*) FROM search_speakers") == 0

    # no transcript left: the document and its words go
    await db.execute("UPDATE transcripts SET is_active = false WHERE video_id = $1", str(b))
    await _index(db, rt, b)
    t = await _terms(db, lib)
    assert t["الصبر"][1] == 1 and "الفرج" not in t


@pytest.mark.asyncio
async def test_a_document_built_before_the_dictionary_is_counted_once(db: Database) -> None:
    """A document written by version 1 (its words not counted) is rebuilt by the plan; its
    words are added and nothing is taken off."""
    rt = await core_runtime(db)
    lib = await make_library(db)
    vid = await make_video(db, lib, title="t", filename="t.mp4")
    await _transcript(db, vid, ["الصبر"])
    await _index(db, rt, vid)
    await db.execute(
        "UPDATE video_search_docs SET terms_counted = $2, doc_version = 1 WHERE video_id = $1",
        str(vid), False,
    )  # fmt: skip
    await db.execute("DELETE FROM search_terms")
    assert (await stage.plan_library(db, lib)).summary["planned"] == 1
    await drain_core(db, rt, only={"keyword_index"})
    assert (await _terms(db, lib))["الصبر"] == ("الصبر", 1)
    d = await _doc(db, vid)
    assert d is not None and bool(d["terms_counted"])
