"""Semantic search's vectors in Postgres (search/pgstore.py, Epic 43).

The unit tests run everywhere; the ones on a real pgvector Postgres need
``MAKTABA_TEST_PG_URL`` (a server with the ``vector`` extension available) and skip
otherwise.
"""

from __future__ import annotations

import random
import uuid
from collections.abc import AsyncIterator, Iterator
from datetime import UTC, datetime
from types import SimpleNamespace
from typing import Any, cast
from uuid import UUID

import pytest
import pytest_asyncio
from maktaba_fp_semantic_search.stages import chapter_infer
from maktaba_fp_semantic_search.stages import index as stage

from maktaba_pipeline import grpc_server
from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.context import StageContext
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH, Subject
from maktaba_pipeline.engine.reconcile import _semantic_sweep
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.search import pgstore, semantic
from maktaba_pipeline.search.pgstore import (
    DIMENSIONS,
    PgVectorStore,
    StoreState,
    prepare,
    store_state,
)
from maktaba_pipeline.search.semantic import IndexedSegment
from tests.engine.factory import make_library, make_video
from tests.engine.fixtures_db import PG_URL, _swap_db, pg_template  # noqa: F401
from tests.engine.test_index_stage import _transcript
from tests.search.semantic_fakes import FakeEmbedder, bag_vector

# --------------------------------------------------------------------------- units


def test_the_state_says_whether_pgvector_is_ready() -> None:
    """Story 43.5: no store to choose any more, only whether segment_embeddings is usable."""
    assert StoreState(ready=True).ready
    assert StoreState() == StoreState(ready=False)  # SQLite, an older schema
    assert not hasattr(StoreState(), "store")


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


class _StateDB:
    """A Postgres look-alike for the state reads: counts them."""

    dialect = "postgres"

    def __init__(self, store: str | None = "chroma", ready: bool = True) -> None:
        self.row: dict[str, Any] | None = {"store": store, "ready": ready}
        self.reads = 0
        self.fail = False
        self.updates: list[str] = []
        self.executed: list[str] = []

    async def execute(self, sql: str, *args: Any) -> Any:
        self.executed.append(sql)
        return "GRANT"

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
    db = _StateDB(ready=False)
    assert await store_state(db) == StoreState(ready=False)
    assert db.row is not None
    db.row["ready"] = True
    assert await store_state(db) == StoreState(ready=False)
    assert db.reads == 1  # cached
    later = pgstore._clock() + pgstore.STATE_TTL_SEC + 1
    monkeypatch.setattr(pgstore, "_clock", lambda: later)
    assert (await store_state(db)).ready  # read again after the TTL
    assert db.reads == 2
    assert (await store_state(db, fresh=True)).ready and db.reads == 3
    pgstore.forget_state()


@pytest.mark.asyncio
async def test_an_older_schema_or_sqlite_has_no_semantic_store() -> None:
    pgstore.forget_state()
    db = _StateDB()
    db.fail = True
    assert await store_state(db) == StoreState()
    assert await store_state(cast(Any, SimpleNamespace(dialect="sqlite"))) == StoreState()
    pgstore.forget_state()


@pytest.mark.asyncio
@pytest.mark.parametrize("store", ["chroma", "postgres", None])
async def test_prepare_never_writes_the_store_row(store: str | None) -> None:
    """A home on Chroma (its copy not done) is ready the same as one switched: the row
    is left as the release before left it, for a rollback to that release."""
    pgstore.forget_state()
    db = _StateDB(store=store)
    assert await prepare(db) == StoreState(ready=True)
    assert db.updates == []
    assert not any("semantic_store_state" in q for q in db.executed)
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
async def test_prepare_leaves_a_home_mid_copy_as_it_is(pg: Database) -> None:
    """A home whose Chroma copy was under way when it updated: prepare is ready and the
    row keeps what the release before wrote (a rollback goes on with its copy)."""
    await pg.execute("UPDATE semantic_store_state SET copied_rows = 10, copy_cursor = '{}'")
    assert (await prepare(pg)).ready
    row = await pg.fetchrow("SELECT store, copied_rows, copy_cursor FROM semantic_store_state")
    assert (row["store"], row["copied_rows"], row["copy_cursor"]) == ("chroma", 10, "{}")


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

    # deletes: by segment, by keep list, by video, and by cascade
    await store.delete_segments([(await _segments(pg, tid))[1].segment_id])
    assert sorted(x.seq for x in await store.video_vectors(v)) == [0, 2]
    first = (await _segments(pg, tid))[0].segment_id
    assert await store.keep_only(v, [first]) == 1
    assert [x.seq for x in await store.video_vectors(v)] == [0]
    await store.delete_video(v)
    assert await _count(pg, v) == 0 and not await store.has_vectors(v, tid)
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
def emb() -> Iterator[FakeEmbedder]:
    e = _emb()
    stage.set_embedder_factory(lambda: e)
    yield e
    stage.reset_embedder_factory()


@pytest.mark.integration
@pytest.mark.asyncio
async def test_the_index_stage_writes_postgres_whatever_the_store_row_says(
    pg: Database, emb: FakeEmbedder
) -> None:
    """Story 43.5: a home whose row still says chroma (its copy not done) indexes into
    Postgres the same as one switched."""
    lib = await make_library(pg)
    v = await make_video(pg, lib)
    await _transcript(pg, v, ["قديم"])
    ctx = cast(StageContext, _Ctx())
    res = await stage.run(pg, _job(v, lib), ctx)  # the row: chroma
    assert await _count(pg, v) == 1 and "store" not in res.summary

    await _set_store(pg, "postgres")
    new = await _transcript(pg, v, ["صلاة", "", "زكاة"], created=datetime(2026, 3, 1, tzinfo=UTC))
    res = await stage.run(pg, _job(v, lib), ctx)
    vecs = await PgVectorStore(pg, "fake/e5").video_vectors(v)
    assert {x.transcript_id for x in vecs} == {str(new)} and [x.seq for x in vecs] == [0, 2]
    assert res.summary["segments"] == 2 and res.summary["stale_ids"] == 1  # the old one

    await pg.execute("UPDATE transcripts SET is_active = false WHERE video_id = $1", str(v))
    await stage.run(pg, _job(v, lib), ctx)
    assert await _count(pg, v) == 0


@pytest.mark.integration
@pytest.mark.asyncio
@pytest.mark.parametrize("row", ["chroma", "postgres"])
async def test_a_video_without_vectors_in_postgres_is_not_indexed(
    pg: Database, emb: FakeEmbedder, row: str
) -> None:
    """A video with segments and no row (a new home after a switchover; a video whose
    vectors were only in Chroma, never copied) has another fingerprint; once indexed it
    is the plain one. The store row does not matter any more (story 43.5)."""
    await _set_store(pg, row)
    lib = await make_library(pg)
    v = await make_video(pg, lib)
    subject = Subject(kind="video", id=v, library_id=lib)
    await _transcript(pg, v, ["أ", "ب"])
    missing = await stage.input_fingerprint(pg, subject)
    res = await stage.run(pg, _job(v, lib), cast(StageContext, _Ctx()))
    assert missing is not None and missing != res.input_hash
    assert await stage.input_fingerprint(pg, subject) == res.input_hash  # the video, stored


@pytest.mark.integration
@pytest.mark.asyncio
async def test_without_pgvector_the_store_is_unavailable(
    pg: Database, emb: FakeEmbedder, monkeypatch: pytest.MonkeyPatch
) -> None:
    lib = await make_library(pg)
    v = await make_video(pg, lib)
    await _transcript(pg, v, ["أ"])

    async def no_pgvector(db: Any, *, fresh: bool = False) -> StoreState:
        return StoreState(ready=False)

    monkeypatch.setattr(stage, "store_state", no_pgvector)
    monkeypatch.setattr(chapter_infer, "store_state", no_pgvector)
    # no failed job: both succeed unchanged, nothing written
    res = await stage.run(pg, _job(v, lib), cast(StageContext, _Ctx()))
    assert res.outcome == "unchanged" and await _count(pg, v) == 0
    res = await chapter_infer.run(pg, _job(v, lib), cast(StageContext, _Ctx()))
    assert res.outcome == "unchanged" and res.summary["chapters"] == 0


@pytest.mark.integration
@pytest.mark.asyncio
async def test_chapter_infer_reads_postgres(pg: Database, emb: FakeEmbedder) -> None:
    lib = await make_library(pg)
    v = await make_video(pg, lib)
    await _transcript(pg, v, ["صلاة الفجر", "زكاة المال"])
    await stage.run(pg, _job(v, lib), cast(StageContext, _Ctx()))
    res = await chapter_infer.run(pg, _job(v, lib), cast(StageContext, _Ctx()))
    assert res.outcome is None and res.summary["segments"] == 2
    dim = await pg.fetchval("SELECT dim FROM video_embeddings WHERE video_id = $1", str(v))
    assert dim == DIMENSIONS


@pytest.mark.integration
@pytest.mark.asyncio
async def test_grpc_search_reads_postgres(
    pg: Database, emb: FakeEmbedder, monkeypatch: pytest.MonkeyPatch
) -> None:
    lib = await make_library(pg)
    v = await make_video(pg, lib)
    tid = await _transcript(pg, v, ["صوم رمضان"])
    await _write(pg, v, lib, tid)
    monkeypatch.setattr(semantic, "search_available", lambda: True)
    monkeypatch.setattr(semantic, "get_query_embedder", lambda model=None: emb)
    _, search, _ = grpc_server.default_search_backends(pg)
    assert search is not None
    for row in ("chroma", "postgres"):  # the row does not matter any more (story 43.5)
        await _set_store(pg, row)
        hits = await search("صوم رمضان", 3, [str(lib)], [])
        assert [h["video_id"] for h in hits] == [str(v)] and hits[0]["text"] == "صوم رمضان"
    assert await search("  ", 3, [], []) == []

    async def no_pgvector(db: Any, *, fresh: bool = False) -> StoreState:
        return StoreState(ready=False)

    monkeypatch.setattr(pgstore, "store_state", no_pgvector)
    _, search, _ = grpc_server.default_search_backends(pg)
    assert search is not None
    with pytest.raises(RuntimeError, match="backend not configured"):
        await search("x", 3, [], [])
    _, search, _ = grpc_server.default_search_backends(None)
    assert search is not None
    with pytest.raises(RuntimeError, match="backend not configured"):
        await search("x", 3, [], [])


@pytest.mark.integration
@pytest.mark.asyncio
async def test_the_reconciler_queues_videos_without_vectors(
    pg: Database, emb: FakeEmbedder
) -> None:
    """A home that updated with its Chroma copy not done (the row still says chroma):
    every video indexed into Chroma only is queued again."""
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
    assert await pg.fetchval("SELECT store FROM semantic_store_state") == "chroma"
    assert await _semantic_sweep(pg, graph) == 1  # v only: blank has no words, kept has rows
    queued = await pg.fetch(
        "SELECT subject_id FROM processing_jobs WHERE stage = 'index' AND state = 'pending'"
    )
    assert [str(r["subject_id"]) for r in queued] == [str(v)]
    assert await _semantic_sweep(pg, graph) == 0  # queued already


@pytest.mark.integration
@pytest.mark.asyncio
async def test_prepare_grants_the_semantic_search_plugin_its_vectors_table(pg: Database) -> None:
    """Epic 54 wave 3: the plugin's roles migration grants only the tables that exist, and
    segment_embeddings comes with pgvector, later: prepare() grants it then."""
    if not await pg.fetchval(
        "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)", pgstore.PLUGIN_ROLE
    ):
        pytest.skip("this database has no tv.maktaba.semantic-search role")
    await pg.execute(f"REVOKE ALL ON segment_embeddings FROM {pgstore.PLUGIN_ROLE}")
    await prepare(pg)
    assert await pg.fetchval(
        "SELECT has_table_privilege($1, 'segment_embeddings', 'INSERT')", pgstore.PLUGIN_ROLE
    )


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_video_copied_in_part_is_indexed_again(pg: Database, emb: FakeEmbedder) -> None:
    """The Chroma copy of a release before 43.5 committed page by page: one stopped part
    way leaves a video with some of its vectors. Its fingerprint says not stored, and the
    reconciler's one-time pass (while the state row says a copy was under way) queues it;
    a whole video and one whose transcript is blank are left alone."""
    import maktaba_pipeline.engine.reconcile as rec

    graph = {s.name: s for s in GRAPH}
    lib = await make_library(pg)
    part, whole, blank = [await make_video(pg, lib) for _ in range(3)]
    for vid, texts in ((part, ["أ", "ب", "ج"]), (whole, ["د", "ه"]), (blank, [""])):
        tid = await _transcript(pg, vid, texts)
        res = await enqueue_job(pg, subject_kind="video", subject_id=vid, stage="index")
        await pg.execute(
            "UPDATE processing_jobs SET state = 'done', outcome = 'changed',"
            " finished_at = now() WHERE id = $1",
            res.job_id,
        )
        segs = await _segments(pg, tid)
        if vid == part:
            segs = segs[:1]  # the copy stopped after the first page
        if vid != blank:
            await PgVectorStore(pg, "fake/e5").write_vectors(
                video_id=vid,
                library_id=lib,
                chunk=segs,
                vectors=emb.embed_passages([s.text for s in segs]),
            )
    subject = Subject(kind="video", id=part, library_id=lib)
    before = await stage.input_fingerprint(pg, subject)
    assert await _semantic_sweep(pg, graph) == 0  # every video has some row
    rec._partial_cursor = ""
    try:
        assert await rec._partial_copy_sweep(pg, graph) == 0  # no copy was under way
        assert rec._partial_cursor is None
        rec._partial_cursor = ""
        await pg.execute("UPDATE semantic_store_state SET copied_rows = 2, copy_done_at = NULL")
        assert await rec._partial_copy_sweep(pg, graph) == 1
        queued = await pg.fetch(
            "SELECT subject_id FROM processing_jobs WHERE stage = 'index' AND state = 'pending'"
        )
        assert [str(r["subject_id"]) for r in queued] == [str(part)]
        assert await rec._partial_copy_sweep(pg, graph) == 0  # the pass is over
        assert rec._partial_cursor is None
    finally:
        rec._partial_cursor = ""
    res = await stage.run(pg, _job(part, lib), cast(StageContext, _Ctx()))
    assert res.input_hash != before and await _count(pg, part) == 3
    assert await stage.input_fingerprint(pg, subject) == res.input_hash
