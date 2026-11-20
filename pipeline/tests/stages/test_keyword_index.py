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
    versions = sorted([(await _doc(db, v) or {})["doc_version"] for v in (*vids, bare)])
    assert versions == [1, 1, 1, 2]
    await drain_core(db, rt, only={"keyword_index"})
    assert {(await _doc(db, v) or {})["doc_version"] for v in (*vids, bare)} == {2}


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
