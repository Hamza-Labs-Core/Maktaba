"""The transcript analysis stages: chapter_infer, topic_assign, topic_recluster."""

from __future__ import annotations

import asyncio
import dataclasses
import math
import random
import uuid
from collections.abc import Iterator
from datetime import UTC, datetime
from pathlib import Path
from types import SimpleNamespace
from typing import Any, cast
from uuid import UUID

import pytest

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.context import StageContext
from maktaba_pipeline.engine.dispatch import dispatch_job
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH, StageDef, Subject, get_stage
from maktaba_pipeline.engine.sql import dumps, is_pg, loads
from maktaba_pipeline.engine.stages import (
    chapter_infer,
    chapter_thumbs,
    topic_assign,
    topic_recluster,
)
from maktaba_pipeline.engine.stages._analysis import (
    VideoEmbedding,
    load_embedding,
    pack_vec,
    store_embedding,
)
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.search.embedder import embed_id_for
from tests.engine.factory import make_library, make_video
from tests.engine.migrate import MIGRATIONS_DIR, up_statements
from tests.engine.test_index_stage import _transcript
from tests.engine.toy import Recorder, drain, rows, setup
from tests.search.semantic_fakes import FakeCollection

DIM = 8


class _Ctx:
    async def progress(self, *a: Any, **kw: Any) -> None:
        return None

    async def check_cancel(self) -> None:
        return None

    def local_path(self, db_path: str) -> str:
        return db_path


def _ctx() -> StageContext:
    return cast(StageContext, _Ctx())


def _job(kind: str, sid: UUID, library: UUID | None = None) -> Job:
    subject = Subject(kind=kind, id=sid, library_id=library)  # type: ignore[arg-type]
    return cast(Job, SimpleNamespace(subject=subject, subject_kind=kind, id=1))


def _unit(i: int) -> list[float]:
    v = [1.0] + [0.0] * (DIM - 1)
    v[1 + i] = 0.4
    n = math.sqrt(sum(x * x for x in v))
    return [x / n for x in v]


@pytest.fixture
def coll() -> Iterator[FakeCollection]:
    c = FakeCollection()
    chapter_infer.set_collection_factory(lambda: c)
    yield c
    chapter_infer.reset_collection_factory()


async def _indexed(
    db: Database, coll: FakeCollection, video: UUID, library: UUID, topics: list[int], per: int
) -> UUID:
    """A transcript of ``per`` segments per topic, and its vectors in the fake store."""
    texts = [f"جملة طويلة عن الموضوع {t} رقم {k}" for t in topics for k in range(per)]
    tid = await _transcript(db, video, texts, created=datetime(2026, 5, 1, tzinfo=UTC))
    rows = await db.fetch(
        "SELECT id, seq, start_sec, end_sec, text FROM transcript_segments"
        " WHERE transcript_id = $1 ORDER BY seq",
        str(tid),
    )
    rng = random.Random(5)
    for r in rows:
        base = _unit(topics[int(r["seq"]) // per])
        coll.rows[embed_id_for(tid, int(r["seq"]))] = (
            [x + rng.gauss(0.0, 0.01) for x in base],
            {
                "video_id": str(video),
                "library_id": str(library),
                "transcript_id": str(tid),
                "segment_id": int(r["id"]),
                "seq": int(r["seq"]),
                "start_sec": float(r["start_sec"]),
                "end_sec": float(r["end_sec"]),
            },
            str(r["text"]),
        )
    return tid


async def _chapters(db: Database, video: UUID) -> list[Any]:
    return await db.fetch(
        "SELECT seq, start_sec, end_sec, title, source FROM chapters WHERE video_id = $1"
        " ORDER BY seq",
        str(video),
    )


async def _set(db: Database, library: UUID, settings: dict[str, Any]) -> None:
    await db.execute(
        "UPDATE libraries SET settings = $2 WHERE id = $1",
        str(library),
        dumps(settings),
    )


# ------------------------------------------------------------------- graph


def _stage(name: str) -> StageDef:
    s = get_stage(name)
    assert s is not None
    return s


def test_graph_runs_analysis_after_index_and_posters_from_the_file() -> None:
    index = _stage("index")
    assert [(e.to, e.legacy) for e in index.edges] == [("chapter_infer", False)]
    # posters and sprites follow the file's identity, not its transcript
    content = _stage("content_hash")
    assert ("thumbnail", False) in [(e.to, e.legacy) for e in content.edges]
    chapters = _stage("chapter_infer")
    assert {e.to for e in chapters.edges} == {"chapter_thumbs", "topic_assign"}
    frames = _stage("chapter_thumbs")
    assert frames.pool == "nas_read" and frames.placement == "home" and not frames.legacy
    assert not any(e.legacy for e in chapters.edges)
    assert chapters.placement == "home" and not chapters.legacy
    recluster = _stage("topic_recluster")
    assert recluster.subject_kinds == ("library",) and recluster.debounce_sec > 0
    edge = _stage("topic_assign").edges[0]
    assert (edge.to, edge.map, edge.when) == ("topic_recluster", "library_of", "always")
    legacy = {s.name for s in GRAPH if s.legacy}
    assert not {"chapter_infer", "topic_assign", "topic_recluster"} & legacy


# ------------------------------------------------------------------- chapter_infer


@pytest.mark.asyncio
async def test_chapters_and_the_video_vector_are_written(
    db: Database, coll: FakeCollection
) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    tid = await _indexed(db, coll, v, lib, [0, 1, 2], per=60)  # 3 subjects, 4 s segments
    res = await chapter_infer.run(db, _job("video", v, lib), _ctx())
    rows = await _chapters(db, v)
    assert res.summary["chapters"] == len(rows) == 3
    assert {r["source"] for r in rows} == {"inferred"}
    assert all(r["title"].startswith("جملة طويلة") for r in rows)
    assert abs(float(rows[1]["start_sec"]) - 60 * 4) <= 3 * 4
    emb = await load_embedding(db, v)
    assert emb is not None and emb.transcript_id == str(tid) and emb.segments == 180
    assert len(emb.vec) == DIM and abs(sum(x * x for x in emb.vec) - 1.0) < 1e-5
    # a re-run replaces, never duplicates
    again = await chapter_infer.run(db, _job("video", v, lib), _ctx())
    assert len(await _chapters(db, v)) == 3 and again.output_hash == res.output_hash


@pytest.mark.asyncio
async def test_authored_chapters_or_the_setting_suppress_inference(
    db: Database, coll: FakeCollection
) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    await _indexed(db, coll, v, lib, [0, 1, 2], per=60)
    subject = Subject(kind="video", id=v, library_id=lib)
    before = await chapter_infer.input_fingerprint(db, subject)
    await db.execute(
        "INSERT INTO chapters (video_id, seq, start_sec, end_sec, title, source)"
        " VALUES ($1, 0, 0, 100, 'مقدمة', 'embedded')",
        str(v),
    )
    assert await chapter_infer.input_fingerprint(db, subject) != before
    res = await chapter_infer.run(db, _job("video", v, lib), _ctx())
    rows = await _chapters(db, v)
    assert [r["source"] for r in rows] == ["embedded"]
    assert "embedded or manual" in res.summary["reason"]
    assert await load_embedding(db, v) is not None  # topics still get the vector

    v2 = await make_video(db, lib)
    await _indexed(db, coll, v2, lib, [0, 1, 2], per=60)
    await _set(db, lib, {"chapter_inference": False})
    res2 = await chapter_infer.run(db, _job("video", v2, lib), _ctx())
    assert await _chapters(db, v2) == [] and "off" in res2.summary["reason"]


@pytest.mark.asyncio
async def test_waits_for_the_index_and_cleans_up_without_a_transcript(
    db: Database, coll: FakeCollection
) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    await _indexed(db, coll, v, lib, [0, 1], per=60)
    await chapter_infer.run(db, _job("video", v, lib), _ctx())
    assert await _chapters(db, v)
    # a new transcript the index has not embedded yet
    await _transcript(db, v, ["جديد"], created=datetime(2026, 9, 1, tzinfo=UTC))
    waiting = await chapter_infer.run(db, _job("video", v, lib), _ctx())
    assert waiting.outcome == "waiting_inputs"
    # no active transcript any more: inferred chapters and the vector go
    await db.execute(
        "UPDATE transcripts SET is_active = $2 WHERE video_id = $1",
        str(v),
        False if is_pg(db) else 0,
    )
    gone = await chapter_infer.run(db, _job("video", v, lib), _ctx())
    assert gone.summary["reason"] == "no active transcript"
    assert await _chapters(db, v) == [] and await load_embedding(db, v) is None
    assert await chapter_infer.input_fingerprint(db, Subject(kind="video", id=uuid.uuid4())) is None


# ------------------------------------------------------------------- topics


async def _vector(db: Database, video: UUID, library: UUID, vec: list[float]) -> None:
    await store_embedding(
        db,
        VideoEmbedding(
            video_id=video,
            library_id=library,
            transcript_id=str(uuid.uuid4()),
            model="intfloat/multilingual-e5-small",
            segments=10,
            vec=vec,
        ),
    )


@pytest.mark.asyncio
async def test_topic_assign_tags_the_nearest_library_topics(db: Database) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    none = await topic_assign.run(db, _job("video", v, lib), _ctx())
    assert none.summary["topics"] == [] and "no video vector" in none.summary["reason"]
    await _vector(db, v, lib, _unit(1))
    empty = await topic_assign.run(db, _job("video", v, lib), _ctx())
    assert empty.summary["reason"].startswith("the library has no topics of")
    for t in range(4):
        await db.execute(
            "INSERT INTO library_topics (library_id, topic_id, label, centroid_vec, model)"
            " VALUES ($1, $2, $3, $4, 'intfloat/multilingual-e5-small')",
            str(lib),
            t,
            f"t{t}",
            pack_vec(_unit(t)),
        )
    # a centroid of another model (dimension) is never compared
    await db.execute(
        "INSERT INTO library_topics (library_id, topic_id, label, centroid_vec)"
        " VALUES ($1, 9, 'other', $2)",
        str(lib),
        pack_vec([1.0, 0.0]),
    )
    res = await topic_assign.run(db, _job("video", v, lib), _ctx())
    assert res.summary["topics"][0] == 1 and len(res.summary["topics"]) == 3
    assert 9 not in res.summary["topics"]
    rows = await db.fetch("SELECT topic_id FROM video_topics WHERE video_id = $1", str(v))
    assert {int(r["topic_id"]) for r in rows} == set(res.summary["topics"])


@pytest.mark.asyncio
async def test_topic_recluster_builds_topics_and_tags_every_video(db: Database) -> None:
    pytest.importorskip("numpy")
    lib = await make_library(db)
    small = await topic_recluster.run(db, _job("library", lib), _ctx())
    assert small.summary["topics"] == 0 and "topics start at" in small.summary["reason"]
    rng = random.Random(2)
    videos: list[UUID] = []
    for i in range(120):
        group = i % 2
        v = await make_video(db, lib, title=f"{'فقه' if group else 'سيرة'} درس {i}")
        await _vector(db, v, lib, [x + rng.gauss(0.0, 0.02) for x in _unit(group * 3)])
        videos.append(v)
    await _set(db, lib, {"topic_clusters": 2})
    subject = Subject(kind="library", id=lib)
    fp = await topic_recluster.input_fingerprint(db, subject)
    res = await topic_recluster.run(db, _job("library", lib), _ctx())
    assert res.summary["topics"] == 2 and res.summary["videos"] == 120
    topics = await db.fetch(
        "SELECT topic_id, label, video_count FROM library_topics WHERE library_id = $1"
        " ORDER BY topic_id",
        str(lib),
    )
    assert sorted(int(t["video_count"]) for t in topics) == [60, 60]
    models = await db.fetch(
        "SELECT DISTINCT model FROM library_topics WHERE library_id = $1", str(lib)
    )
    assert [m["model"] for m in models] == ["intfloat/multilingual-e5-small"]
    labels = {str(t["label"]).split()[0] for t in topics}
    assert labels == {"فقه", "سيرة"}
    n = await db.fetchval("SELECT COUNT(*) FROM video_topics WHERE library_id = $1", str(lib))
    assert int(n) == 120 * 2  # top-3 capped at k
    # turning topics off removes them (and the videos' tags with them)
    await _set(db, lib, {"auto_tag_topics": False, "topic_clusters": 2})
    assert await topic_recluster.input_fingerprint(db, subject) != fp
    off = await topic_recluster.run(db, _job("library", lib), _ctx())
    assert off.summary["topics"] == 0
    assert int(await db.fetchval("SELECT COUNT(*) FROM video_topics")) == 0


# ------------------------------------------------------------------- propagation


@pytest.mark.asyncio
async def test_an_indexed_video_gets_chapters_frames_and_topics(db: Database) -> None:
    """index -> chapter_infer -> chapter_thumbs + topic_assign -> topic_recluster
    (debounced), through the engine's own claim and propagation; no second thumbnail."""
    names = {"index", "chapter_infer", "chapter_thumbs", "thumbnail", "topic_assign"}
    names |= {"topic_recluster"}
    graph = tuple(s for s in GRAPH if s.name in names)
    rec = Recorder()
    rt = await setup(db, rec, graph)
    lib = await make_library(db)
    v = await make_video(db, lib)
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="index")
    await drain(db, rt)  # the cpu pool: index, chapter_infer, topic_assign
    assert [c[0] for c in rec.calls] == ["index", "chapter_infer", "topic_assign"]
    frames = await rows(db, "chapter_thumbs")
    assert len(frames) == 1 and frames[0]["state"] == "pending"  # nas_read: not drained
    assert frames[0]["triggered_by_stage"] == "chapter_infer"
    assert await rows(db, "thumbnail") == []  # the poster came from content_hash
    recluster = await rows(db, "topic_recluster")
    assert len(recluster) == 1 and str(recluster[0]["subject_id"]) == str(lib)
    assert recluster[0]["not_before"] is not None  # debounced, not run yet


@pytest.mark.asyncio
async def test_a_video_without_audio_still_gets_its_poster_job(db: Database) -> None:
    """content_hash -> thumbnail: the poster never waits for (or needs) a transcript."""
    names = {"content_hash", "thumbnail", "transcribe", "thumb_small"}
    graph = tuple(s for s in GRAPH if s.name in names)
    rec = Recorder()
    rt = await setup(db, rec, graph)
    lib = await make_library(db)
    v = await make_video(db, lib)
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="content_hash")
    job = await claim_for_pool(
        db, worker_id="w1", pool="nas_read", stages=["content_hash"], scope="global"
    )
    assert job is not None
    await dispatch_job(db, job, runtime=rt, legacy_handlers={})
    thumb = await rows(db, "thumbnail")
    assert len(thumb) == 1 and thumb[0]["state"] == "pending"
    assert str(thumb[0]["video_id"]) == str(v)  # what the legacy handler reads
    assert thumb[0]["triggered_by_stage"] == "content_hash"
    # transcribe is queued alongside, but nothing about the poster depends on it
    assert len(await rows(db, "transcribe")) == 1


# ------------------------------------------------------------------- chapter_thumbs


@pytest.mark.asyncio
async def test_chapter_frames_follow_the_chapters(db: Database, tmp_path: Path) -> None:
    calls: list[list[str]] = []

    async def fake(args: list[str]) -> None:
        calls.append(args)
        Path(args[-1]).write_bytes(b"jpg")

    chapter_thumbs.set_frame_runner(fake, str(tmp_path))
    try:
        lib = await make_library(db)
        v = await make_video(db, lib)
        h = await db.fetchval("SELECT content_hash FROM videos WHERE id = $1", str(v))
        subject = Subject(kind="video", id=v, library_id=lib)
        none = await chapter_thumbs.run(db, _job("video", v, lib), _ctx())
        assert none.summary == {"frames": 0, "removed": 0} and calls == []
        fp0 = await chapter_thumbs.input_fingerprint(db, subject)
        for seq, start in ((0, 0.0), (1, 300.0), (2, 600.0)):
            await db.execute(
                "INSERT INTO chapters (video_id, seq, start_sec, title, source)"
                " VALUES ($1, $2, $3, '', 'inferred')",
                str(v),
                seq,
                start,
            )
        assert await chapter_thumbs.input_fingerprint(db, subject) != fp0
        res = await chapter_thumbs.run(db, _job("video", v, lib), _ctx())
        assert res.summary["frames"] == 3 and len(calls) == 3
        assert {p.name for p in (tmp_path / str(h)).iterdir()} == {
            "chapter-0.jpg",
            "chapter-1.jpg",
            "chapter-2.jpg",
        }
        await db.execute("DELETE FROM chapters WHERE video_id = $1 AND seq = 2", str(v))
        again = await chapter_thumbs.run(db, _job("video", v, lib), _ctx())
        assert again.summary == {"frames": 2, "removed": 1}
        assert not (tmp_path / str(h) / "chapter-2.jpg").exists()
    finally:
        chapter_thumbs.reset_frame_runner()


# ------------------------------------------------------------------- backfill (slot 0121)


def _backfill_sql(db: Database) -> str:
    name = "0121_video_embeddings" + ("" if is_pg(db) else ".sqlite") + ".sql"
    text = (MIGRATIONS_DIR / name).read_text(encoding="utf-8")
    (stmt,) = [s for s in up_statements(text) if "INSERT INTO processing_jobs" in s]
    return stmt


@pytest.mark.asyncio
async def test_the_migration_queues_chapter_infer_once_for_indexed_videos(
    db: Database,
) -> None:
    lib = await make_library(db)
    indexed = await make_video(db, lib)
    await _transcript(db, indexed, ["أ", "ب"])
    no_transcript = await make_video(db, lib)
    for v in (indexed, no_transcript):
        await enqueue_job(db, subject_kind="video", subject_id=v, stage="index")
    await db.execute("UPDATE processing_jobs SET state = 'done' WHERE stage = 'index'")
    not_indexed = await make_video(db, lib)
    await _transcript(db, not_indexed, ["ج"])
    sql = _backfill_sql(db)
    await db.execute(sql)
    await db.execute(sql)  # idempotent
    jobs = await rows(db, "chapter_infer")
    assert [str(j["subject_id"]) for j in jobs] == [str(indexed)]
    job = jobs[0]
    assert job["state"] == "pending" and int(job["priority"]) == 500
    assert str(job["video_id"]) == str(indexed) and str(job["library_id"]) == str(lib)
    assert "slot 0121" in loads(job["reasons"])[0]["why"]


# ------------------------------------------------------------------- embeddings


@pytest.mark.asyncio
async def test_an_unchanged_vector_leaves_the_row_alone(db: Database) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    e = VideoEmbedding(v, lib, str(uuid.uuid4()), "m", 4, [0.5, 0.5, 0.5, 0.5])
    await store_embedding(db, e)
    at = await db.fetchval("SELECT updated_at FROM video_embeddings WHERE video_id = $1", str(v))
    await asyncio.sleep(0.01)
    await store_embedding(db, e)
    same = await db.fetchval("SELECT updated_at FROM video_embeddings WHERE video_id = $1", str(v))
    assert same == at
    await store_embedding(db, dataclasses.replace(e, vec=[1.0, 0.0, 0.0, 0.0]))
    moved = await db.fetchval("SELECT updated_at FROM video_embeddings WHERE video_id = $1", str(v))
    assert moved != at
    stored = await load_embedding(db, v)
    assert stored is not None and stored.vec == [1.0, 0.0, 0.0, 0.0]


@pytest.mark.asyncio
async def test_topics_of_another_model_are_never_scored(db: Database) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    await _vector(db, v, lib, _unit(1))  # intfloat/multilingual-e5-small, DIM floats
    await db.execute(
        "INSERT INTO library_topics (library_id, topic_id, label, centroid_vec, model)"
        " VALUES ($1, 0, 'same dim, other model', $2, 'intfloat/multilingual-e5-base')",
        str(lib),
        pack_vec(_unit(1)),
    )
    res = await topic_assign.run(db, _job("video", v, lib), _ctx())
    assert res.summary["topics"] == []
    assert "no topics of intfloat/multilingual-e5-small" in res.summary["reason"]
