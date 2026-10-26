"""Semantic search's vectors in Postgres (search/pgstore.py, Epic 43).

The unit tests run everywhere; the ones on a real pgvector Postgres need
``MAKTABA_TEST_PG_URL`` (a server with the ``vector`` extension available) and skip
otherwise.
"""

from __future__ import annotations

import random
import sqlite3
import uuid
from collections.abc import AsyncIterator, Iterator
from datetime import UTC, datetime
from pathlib import Path
from types import SimpleNamespace
from typing import Any, cast
from uuid import UUID

import pytest
import pytest_asyncio

from maktaba_pipeline import grpc_server
from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH, Subject
from maktaba_pipeline.engine.reconcile import _semantic_sweep
from maktaba_pipeline.engine.stages import chapter_infer
from maktaba_pipeline.engine.stages import index as stage
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.search import pgstore, semantic
from maktaba_pipeline.search.pgstore import (
    DIMENSIONS,
    PgVectorStore,
    StoreState,
    chroma_has_vectors,
    prepare,
    store_state,
)
from maktaba_pipeline.search.semantic import IndexedSegment, SemanticIndex
from tests.engine.factory import make_library, make_video
from tests.engine.fixtures_db import PG_URL, _swap_db, pg_template  # noqa: F401
from tests.engine.test_index_stage import _transcript
from tests.search.semantic_fakes import FakeCollection, FakeEmbedder, bag_vector

# --------------------------------------------------------------------------- units


def test_the_state_says_where_to_write_and_read() -> None:
    copying = StoreState(store="chroma", ready=True)
    assert copying.write_chroma and copying.write_postgres and not copying.read_postgres
    switched = StoreState(store="postgres", ready=True)
    assert not switched.write_chroma and switched.write_postgres and switched.read_postgres
    no_pgvector = StoreState(store="postgres", ready=False)
    assert not no_pgvector.write_chroma and not no_pgvector.write_postgres
    assert StoreState() == StoreState(store="chroma", ready=False)  # SQLite


def test_literals_round_trip() -> None:
    vec = [0.5, -0.25, 1.0]
    lit = pgstore._literal(vec)
    assert lit == "[0.5,-0.25,1.0]"
    assert pgstore._parse(lit) == vec
    assert pgstore._parse("[]") == []


def test_library_filter_keeps_valid_ids() -> None:
    good = str(uuid.uuid4())
    assert pgstore._uuids(None) is None and pgstore._uuids([]) is None
    assert pgstore._uuids([good, "not-a-uuid"]) == [good]
    assert pgstore._uuids(["nope"]) == []  # a filter that matches nothing


def _chroma_file(path: Path, *, embeddings: int = 0, queued: int = 0) -> None:
    path.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(path / "chroma.sqlite3")
    conn.execute("CREATE TABLE embeddings (id INTEGER PRIMARY KEY)")
    conn.execute("CREATE TABLE embeddings_queue (seq_id INTEGER PRIMARY KEY)")
    conn.executemany("INSERT INTO embeddings DEFAULT VALUES", [()] * embeddings)
    conn.executemany("INSERT INTO embeddings_queue DEFAULT VALUES", [()] * queued)
    conn.commit()
    conn.close()


def test_chroma_vectors_are_found_without_chromadb(tmp_path: Path) -> None:
    assert not chroma_has_vectors(str(tmp_path / "none"))  # no folder
    _chroma_file(tmp_path / "empty")
    assert not chroma_has_vectors(str(tmp_path / "empty"))  # a collection, no vector
    _chroma_file(tmp_path / "full", embeddings=2)
    assert chroma_has_vectors(str(tmp_path / "full"))
    _chroma_file(tmp_path / "log", queued=1)
    assert chroma_has_vectors(str(tmp_path / "log"))  # still in its write log
    broken = tmp_path / "broken"
    broken.mkdir()
    (broken / "chroma.sqlite3").write_bytes(b"not a database")
    assert chroma_has_vectors(str(broken))  # unreadable counts as yes


class _StateDB:
    """A Postgres look-alike for the state reads: counts them."""

    dialect = "postgres"

    def __init__(self, store: str | None = "chroma", ready: bool = True) -> None:
        self.row: dict[str, Any] | None = {"store": store, "ready": ready}
        self.reads = 0
        self.fail = False
        self.updates: list[str] = []

    async def fetchrow(self, sql: str, *args: Any) -> Any:
        self.reads += 1
        if self.fail:
            raise RuntimeError("function semantic_store_ready() does not exist")
        return self.row

    async def fetchval(self, sql: str, *args: Any) -> Any:
        if sql.startswith("UPDATE"):
            self.updates.append(sql)
            assert self.row is not None
            self.row["store"] = "postgres"
            return "postgres"
        return True


@pytest.mark.asyncio
async def test_the_state_is_cached_for_a_while(monkeypatch: pytest.MonkeyPatch) -> None:
    pgstore.forget_state()
    db = _StateDB()
    assert await store_state(db) == StoreState("chroma", True)
    assert await store_state(db) == StoreState("chroma", True)
    assert db.reads == 1  # cached
    assert db.row is not None
    db.row["store"] = "postgres"
    later = pgstore._clock() + pgstore.STATE_TTL_SEC + 1
    monkeypatch.setattr(pgstore, "_clock", lambda: later)
    assert (await store_state(db)).store == "postgres"  # read again after the TTL
    assert db.reads == 2
    assert (await store_state(db, fresh=True)).store == "postgres" and db.reads == 3
    pgstore.forget_state()


@pytest.mark.asyncio
async def test_an_older_schema_or_sqlite_reads_chroma() -> None:
    pgstore.forget_state()
    db = _StateDB()
    db.fail = True
    assert await store_state(db) == StoreState()
    assert await store_state(cast(Any, SimpleNamespace(dialect="sqlite"))) == StoreState()
    pgstore.forget_state()


@pytest.mark.asyncio
async def test_a_home_without_chroma_vectors_moves_to_postgres(tmp_path: Path) -> None:
    pgstore.forget_state()
    db = _StateDB()
    assert (await prepare(db, chroma_path=str(tmp_path / "none"))).store == "postgres"
    assert len(db.updates) == 1
    pgstore.forget_state()
    kept = _StateDB()
    _chroma_file(tmp_path / "full", embeddings=1)
    assert (await prepare(kept, chroma_path=str(tmp_path / "full"))).store == "chroma"
    assert kept.updates == []
    pgstore.forget_state()


# --------------------------------------------------------------------------- Postgres


@pytest_asyncio.fixture
async def pg(pg_template: str) -> AsyncIterator[Database]:  # noqa: F811
    """A migrated Postgres database with pgvector ready (else the test skips)."""
    import asyncpg  # type: ignore[import-untyped]  # no stubs ship with asyncpg

    name = "maktaba_t_" + uuid.uuid4().hex[:10]
    admin = await asyncpg.connect(PG_URL)
    await admin.execute(f'CREATE DATABASE "{name}" TEMPLATE "{pg_template}"')
    await admin.close()
    database = await Database.connect(_swap_db(PG_URL, name))
    pgstore.forget_state()
    try:
        if not (await store_state(database, fresh=True)).ready:
            pytest.skip("pgvector is not installed on MAKTABA_TEST_PG_URL's server")
        yield database
    finally:
        pgstore.forget_state()
        await database.close()
        admin = await asyncpg.connect(PG_URL)
        await admin.execute(f'DROP DATABASE IF EXISTS "{name}" WITH (FORCE)')
        await admin.close()


async def _set_store(db: Database, store: str) -> None:
    await db.execute("UPDATE semantic_store_state SET store = $1", store)
    pgstore.forget_state()


def _emb() -> FakeEmbedder:
    return FakeEmbedder(dim=DIMENSIONS)


async def _segments(db: Database, tid: UUID) -> list[IndexedSegment]:
    rows = await db.fetch(
        "SELECT id, seq, start_sec, end_sec, text FROM transcript_segments"
        " WHERE transcript_id = $1 ORDER BY seq",
        str(tid),
    )
    return [
        IndexedSegment(
            segment_id=int(r["id"]),
            transcript_id=tid,
            seq=int(r["seq"]),
            start_sec=float(r["start_sec"]),
            end_sec=float(r["end_sec"]),
            text=str(r["text"]),
        )
        for r in rows
    ]


async def _write(
    db: Database, video: UUID, library: UUID, tid: UUID, language: str | None = "ar"
) -> PgVectorStore:
    store = PgVectorStore(db, "fake/e5")
    segs = await _segments(db, tid)
    emb = _emb()
    await store.write_vectors(
        video_id=video,
        library_id=library,
        chunk=segs,
        vectors=emb.embed_passages([s.text for s in segs]),
        language=language,
    )
    return store


async def _count(db: Database, video: UUID) -> int:
    return int(
        await db.fetchval("SELECT count(*) FROM segment_embeddings WHERE video_id = $1", str(video))
    )


@pytest.mark.integration
@pytest.mark.asyncio
async def test_migration_made_the_table_and_the_state_row(pg: Database) -> None:
    row = await pg.fetchrow("SELECT * FROM semantic_store_state")
    assert row["store"] == "chroma" and row["copied_rows"] == 0 and row["copy_cursor"] is None
    assert await pg.fetchval("SELECT ensure_segment_embeddings()") is True  # idempotent
    idx = {
        r["indexname"]
        for r in await pg.fetch(
            "SELECT indexname FROM pg_indexes WHERE tablename = 'segment_embeddings'"
        )
    }
    assert {"segment_embeddings_hnsw", "segment_embeddings_video"} <= idx
    excluded = await pg.fetchval("SELECT cluster_copy_excluded()")
    assert {"segment_embeddings", "semantic_store_state", "cluster_certs"} <= set(excluded)
    # what was deferred is made at the next start
    await pg.execute("DROP TABLE segment_embeddings")
    assert await pg.fetchval("SELECT semantic_store_ready()") is False
    assert await pg.fetchval("SELECT ensure_segment_embeddings()") is True
    assert await pg.fetchval("SELECT semantic_store_ready()") is True


@pytest.mark.integration
@pytest.mark.asyncio
async def test_prepare_moves_a_fresh_home_but_never_a_copy_under_way(
    pg: Database, tmp_path: Path
) -> None:
    await pg.execute("UPDATE semantic_store_state SET copied_rows = 10")
    assert (await prepare(pg, chroma_path=str(tmp_path))).store == "chroma"  # copy began
    await pg.execute("UPDATE semantic_store_state SET copied_rows = 0")
    assert (await prepare(pg, chroma_path=str(tmp_path))).store == "postgres"


@pytest.mark.integration
@pytest.mark.asyncio
async def test_write_read_search_and_delete(pg: Database) -> None:
    lib, other_lib = await make_library(pg), await make_library(pg)
    v = await make_video(pg, lib)
    w = await make_video(pg, other_lib)
    tid = await _transcript(pg, v, ["صلاة الفجر", "زكاة المال", "صوم رمضان"])
    wid = await _transcript(pg, w, ["صلاة الفجر جماعة"])
    store = await _write(pg, v, lib, tid)
    await _write(pg, w, other_lib, wid, language="en")
    assert await _count(pg, v) == 3

    # upsert: the same segments again stay three rows, with the new values
    segs = await _segments(pg, tid)
    n = await store.write_vectors(
        video_id=v, library_id=lib, chunk=segs, vectors=_emb().embed_passages(["x"] * 3)
    )
    assert n == 3 and await _count(pg, v) == 3
    assert (
        await pg.fetchval(
            "SELECT count(*) FROM segment_embeddings WHERE video_id = $1 AND language IS NULL",
            str(v),
        )
        == 3
    )
    store = await _write(pg, v, lib, tid)

    vecs = await store.video_vectors(v)
    assert [x.seq for x in vecs] == [0, 1, 2] and vecs[0].text == "صلاة الفجر"
    assert len(vecs[0].embedding) == DIMENSIONS
    want = bag_vector("صلاة الفجر", DIMENSIONS)
    assert max(abs(a - b) for a, b in zip(vecs[0].embedding, want, strict=True)) < 1e-3
    assert await store.video_vectors(v, uuid.uuid4()) == []

    q = _emb().embed_query("صلاة الفجر")
    hits = await store.search(q, k=5)
    assert {h.video_id for h in hits} == {str(v), str(w)}
    assert hits[0].score > 0.99 and hits[0].text == "صلاة الفجر"
    assert [h.score for h in hits] == sorted((h.score for h in hits), reverse=True)
    only = await store.search(q, k=5, library_ids=[str(other_lib)])
    assert [h.video_id for h in only] == [str(w)]  # filtered inside the scan
    assert await store.search(q, k=5, library_ids=["nope"]) == []
    ar = await store.search(q, k=5, languages=["ar"])
    assert {h.video_id for h in ar} == {str(v)}
    assert await store.search(q, k=5, library_ids=[str(lib)], languages=["en"]) == []

    # deletes: by Chroma id, by keep list, by video, and by cascade
    await store.delete_ids([f"{tid}:1", "junk"])
    assert sorted(x.seq for x in await store.video_vectors(v)) == [0, 2]
    first = (await _segments(pg, tid))[0].segment_id
    assert await store.keep_only(v, [first]) == 1
    assert [x.seq for x in await store.video_vectors(v)] == [0]
    await store.delete_video(v)
    assert await _count(pg, v) == 0 and not await store.has_vectors(v)
    await pg.execute("DELETE FROM transcripts WHERE id = $1", str(wid))
    assert await _count(pg, w) == 0  # deleted with its segments


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_small_library_gets_its_top_k_through_the_index(pg: Database) -> None:
    """With the HNSW index forced (no library index, no sequential scan), a library of 5
    rows among 1505 still gets its hits: the iterative scan goes on past ef_search."""
    big, small = await make_library(pg), await make_library(pg)
    vb, vs = await make_video(pg, big), await make_video(pg, small)
    tb = await _transcript(pg, vb, [f"نص {i}" for i in range(1500)])
    ts = await _transcript(pg, vs, [f"خاص {i}" for i in range(5)])
    rng = random.Random(7)  # dense vectors: bag-of-words ones repeat, a poor HNSW graph
    for vid, lib, tid in ((vb, big, tb), (vs, small, ts)):
        segs = await _segments(pg, tid)
        await PgVectorStore(pg, "fake/e5").write_vectors(
            video_id=vid,
            library_id=lib,
            chunk=segs,
            vectors=[[rng.gauss(0.0, 1.0) for _ in range(DIMENSIONS)] for _ in segs],
        )
    await pg.execute("DROP INDEX segment_embeddings_library")
    name = await pg.fetchval("SELECT current_database()")
    await pg.execute(f'ALTER DATABASE "{name}" SET enable_seqscan = off')
    forced = await Database.connect(_swap_db(PG_URL, name))
    try:
        store = PgVectorStore(forced, "fake/e5")
        q = [rng.gauss(0.0, 1.0) for _ in range(DIMENSIONS)]
        hits = await store.search(q, k=5, library_ids=[str(small)])
        assert len(hits) == 5 and {h.video_id for h in hits} == {str(vs)}
        async with forced.transaction():
            plan = "\n".join(
                r[0]
                for r in await forced.fetch(
                    "EXPLAIN SELECT segment_id FROM segment_embeddings"
                    " WHERE library_id = ANY($2::uuid[])"
                    " ORDER BY embedding <=> $1::text::halfvec LIMIT 5",
                    pgstore._literal(q),
                    [str(small)],
                )
            )
        assert "segment_embeddings_hnsw" in plan
    finally:
        await forced.close()


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_wrong_size_is_refused(pg: Database) -> None:
    lib = await make_library(pg)
    v = await make_video(pg, lib)
    tid = await _transcript(pg, v, ["a"])
    segs = await _segments(pg, tid)
    with pytest.raises(ValueError, match="384"):
        await PgVectorStore(pg, "m").write_vectors(
            video_id=v, library_id=lib, chunk=segs, vectors=[[1.0, 0.0]]
        )


@pytest.mark.integration
@pytest.mark.asyncio
async def test_the_search_uses_the_hnsw_index(pg: Database) -> None:
    """The query vector as a text parameter cast to halfvec still orders by the index
    (on an empty table only with sequential scans off; a narrow library filter may take
    the library index, an exact sort of few rows)."""
    q = pgstore._literal(bag_vector("x", DIMENSIONS))
    async with pg.transaction():
        await pg.execute("SET LOCAL enable_seqscan = off")
        plan = "\n".join(
            r[0]
            for r in await pg.fetch(
                "EXPLAIN SELECT segment_id FROM segment_embeddings"
                " ORDER BY embedding <=> $1::text::halfvec LIMIT 10",
                q,
            )
        )
    assert "segment_embeddings_hnsw" in plan


class _Ctx:
    async def progress(self, *a: Any, **kw: Any) -> None:
        return None

    async def check_cancel(self) -> None:
        return None


def _job(video: UUID, library: UUID) -> Job:
    subject = Subject(kind="video", id=video, library_id=library)
    return cast(Job, SimpleNamespace(subject=subject, subject_kind="video", id=1))


@pytest.fixture
def fakes() -> Iterator[tuple[FakeCollection, FakeEmbedder]]:
    coll, emb = FakeCollection(), _emb()
    stage.set_index_factory(lambda: SemanticIndex(coll, emb))
    stage.set_embedder_factory(lambda: emb)
    chapter_infer.set_collection_factory(lambda: coll)
    yield coll, emb
    stage.reset_index_factory()
    stage.reset_embedder_factory()
    chapter_infer.reset_collection_factory()


@pytest.mark.integration
@pytest.mark.asyncio
async def test_the_index_stage_writes_both_during_the_copy_then_postgres_only(
    pg: Database, fakes: tuple[FakeCollection, FakeEmbedder]
) -> None:
    coll, _ = fakes
    lib = await make_library(pg)
    v = await make_video(pg, lib)
    await _transcript(pg, v, ["قديم"])
    ctx = cast(StageContext, _Ctx())
    res = await stage.run(pg, _job(v, lib), ctx)  # store chroma: both
    assert len(coll.rows) == 1 and await _count(pg, v) == 1
    assert res.summary["store"] == "chroma"

    await _set_store(pg, "postgres")
    coll.rows.clear()
    new = await _transcript(pg, v, ["صلاة", "", "زكاة"], created=datetime(2026, 3, 1, tzinfo=UTC))
    res = await stage.run(pg, _job(v, lib), ctx)
    assert coll.rows == {}  # Chroma no longer written
    vecs = await PgVectorStore(pg, "fake/e5").video_vectors(v)
    assert {x.transcript_id for x in vecs} == {str(new)} and [x.seq for x in vecs] == [0, 2]
    assert res.summary["segments"] == 2 and res.summary["stale_ids"] == 1  # the old one

    await pg.execute("UPDATE transcripts SET is_active = false WHERE video_id = $1", str(v))
    await stage.run(pg, _job(v, lib), ctx)
    assert await _count(pg, v) == 0


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_video_without_vectors_in_postgres_is_not_indexed(
    pg: Database, fakes: tuple[FakeCollection, FakeEmbedder]
) -> None:
    """After the switch (a new home whose copy held no vectors), the fingerprint of a
    video with segments and no row changes; once indexed it is the plain one again."""
    lib = await make_library(pg)
    v = await make_video(pg, lib)
    subject = Subject(kind="video", id=v, library_id=lib)
    await _transcript(pg, v, ["أ", "ب"])
    plain = await stage.input_fingerprint(pg, subject)  # store chroma: as before
    await _set_store(pg, "postgres")
    missing = await stage.input_fingerprint(pg, subject)
    assert missing is not None and missing != plain
    res = await stage.run(pg, _job(v, lib), cast(StageContext, _Ctx()))
    assert res.input_hash == plain  # what it answers: the video, stored
    assert await stage.input_fingerprint(pg, subject) == plain


@pytest.mark.integration
@pytest.mark.asyncio
async def test_without_pgvector_the_postgres_store_is_unavailable(
    pg: Database, fakes: tuple[FakeCollection, FakeEmbedder], monkeypatch: pytest.MonkeyPatch
) -> None:
    lib = await make_library(pg)
    v = await make_video(pg, lib)
    await _transcript(pg, v, ["أ"])

    async def no_pgvector(db: Any, *, fresh: bool = False) -> StoreState:
        return StoreState(store="postgres", ready=False)

    monkeypatch.setattr(stage, "store_state", no_pgvector)
    monkeypatch.setattr(chapter_infer, "store_state", no_pgvector)
    with pytest.raises(StageFailure) as err:
        await stage.run(pg, _job(v, lib), cast(StageContext, _Ctx()))
    assert err.value.kind == "search_unavailable" and not err.value.retryable
    with pytest.raises(StageFailure) as err:
        await chapter_infer.run(pg, _job(v, lib), cast(StageContext, _Ctx()))
    assert err.value.kind == "search_unavailable"


@pytest.mark.integration
@pytest.mark.asyncio
async def test_chapter_infer_reads_postgres_after_the_switch(
    pg: Database, fakes: tuple[FakeCollection, FakeEmbedder]
) -> None:
    coll, _ = fakes
    await _set_store(pg, "postgres")
    lib = await make_library(pg)
    v = await make_video(pg, lib)
    await _transcript(pg, v, ["صلاة الفجر", "زكاة المال"])
    await stage.run(pg, _job(v, lib), cast(StageContext, _Ctx()))
    res = await chapter_infer.run(pg, _job(v, lib), cast(StageContext, _Ctx()))
    assert res.outcome is None and res.summary["segments"] == 2
    assert coll.gets == []  # Chroma is not read
    dim = await pg.fetchval("SELECT dim FROM video_embeddings WHERE video_id = $1", str(v))
    assert dim == DIMENSIONS


@pytest.mark.integration
@pytest.mark.asyncio
async def test_grpc_search_follows_the_store(
    pg: Database, fakes: tuple[FakeCollection, FakeEmbedder], monkeypatch: pytest.MonkeyPatch
) -> None:
    coll, emb = fakes
    lib = await make_library(pg)
    v = await make_video(pg, lib)
    tid = await _transcript(pg, v, ["صوم رمضان"])
    await _write(pg, v, lib, tid)
    monkeypatch.setattr(semantic, "search_available", lambda: True)
    monkeypatch.setattr(semantic, "get_query_embedder", lambda model=None: emb)
    monkeypatch.setattr(
        semantic.SemanticIndex, "for_queries", classmethod(lambda cls: cls(coll, emb))
    )
    _, search, _ = grpc_server.default_search_backends(pg)
    assert search is not None
    assert await search("صوم رمضان", 3, [str(lib)], []) == []  # chroma: the empty fake
    await _set_store(pg, "postgres")
    hits = await search("صوم رمضان", 3, [str(lib)], [])
    assert [h["video_id"] for h in hits] == [str(v)] and hits[0]["text"] == "صوم رمضان"

    async def no_pgvector(db: Any, *, fresh: bool = False) -> StoreState:
        return StoreState(store="postgres", ready=False)

    monkeypatch.setattr(pgstore, "store_state", no_pgvector)
    _, search, _ = grpc_server.default_search_backends(pg)
    assert search is not None
    with pytest.raises(RuntimeError, match="backend not configured"):
        await search("x", 3, [], [])


@pytest.mark.integration
@pytest.mark.asyncio
async def test_the_reconciler_queues_videos_without_vectors(
    pg: Database, fakes: tuple[FakeCollection, FakeEmbedder]
) -> None:
    graph = {s.name: s for s in GRAPH}
    lib = await make_library(pg)
    v, blank, kept = [await make_video(pg, lib) for _ in range(3)]
    for vid, texts in ((v, ["أ"]), (blank, [""]), (kept, ["ب"])):
        await _transcript(pg, vid, texts)
        res = await enqueue_job(pg, subject_kind="video", subject_id=vid, stage="index")
        await pg.execute(
            "UPDATE processing_jobs SET state = 'done', outcome = 'changed',"
            " finished_at = now() WHERE id = $1",
            res.job_id,
        )
    tid = await pg.fetchval(
        "SELECT id FROM transcripts WHERE video_id = $1 AND is_active", str(kept)
    )
    await _write(pg, kept, lib, UUID(str(tid)))
    assert await _semantic_sweep(pg, graph) == 0  # chroma: nothing to do
    await _set_store(pg, "postgres")
    assert await _semantic_sweep(pg, graph) == 1  # v only: blank has no words, kept has rows
    queued = await pg.fetch(
        "SELECT subject_id FROM processing_jobs WHERE stage = 'index' AND state = 'pending'"
    )
    assert [str(r["subject_id"]) for r in queued] == [str(v)]
    assert await _semantic_sweep(pg, graph) == 0  # queued already
