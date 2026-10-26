"""``copy-chroma`` (story 43.3) against a real persistent Chroma and, when
``MAKTABA_TEST_PG_URL`` names a Postgres with pgvector, a real database.

Chroma comes from the optional ``search`` extra; each test skips without it.
"""

from __future__ import annotations

import json
import os
import uuid
from collections.abc import AsyncIterator
from pathlib import Path
from typing import Any

import pytest
import pytest_asyncio

np = pytest.importorskip("numpy")

from maktaba_pipeline.search import chroma_copy, semantic  # noqa: E402
from maktaba_pipeline.search.chroma_copy import (  # noqa: E402
    ChromaFiles,
    CopyConfig,
    Cursor,
    split_id,
)

MODEL = "intfloat/multilingual-e5-small"
DIM = 384
PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")


def _unit(rng: Any, n: int) -> Any:
    v = rng.standard_normal((n, DIM)).astype(np.float32)
    return v / np.linalg.norm(v, axis=1, keepdims=True)


class Built:
    """A Chroma collection as the index stage writes it, and what it should hold."""

    def __init__(self, root: Path) -> None:
        self.root = root
        self.vectors: dict[str, Any] = {}
        self.transcripts: list[uuid.UUID] = []
        self.orphan_transcript = uuid.uuid4()
        self.coll: Any = None
        self.client: Any = None


def _build(root: Path, *, transcripts: int = 30, per: int = 100, seed: int = 7) -> Built:
    """``transcripts`` transcripts of ``per`` segments, one more whose transcript is not
    in the database (orphans), some deleted, and the last writes left in Chroma's queue
    (its sync threshold is 1000: the files hold the writes up to the last thousand)."""
    chromadb = pytest.importorskip("chromadb")
    b = Built(root)
    b.client = chromadb.PersistentClient(
        path=str(root), settings=chromadb.Settings(anonymized_telemetry=False)
    )
    b.coll = b.client.get_or_create_collection(
        name=semantic.collection_name(MODEL),
        metadata={"hnsw:space": "cosine"},
        embedding_function=None,
    )
    rng = np.random.default_rng(seed)
    b.transcripts = [uuid.uuid4() for _ in range(transcripts)]
    for tid in [b.orphan_transcript, *b.transcripts]:
        ids = [f"{tid}:{s}" for s in range(per)]
        vecs = _unit(rng, per)
        b.coll.upsert(
            ids=ids,
            embeddings=vecs,
            metadatas=[{"seq": s} for s in range(per)],
            documents=["x"] * per,
        )
        b.vectors.update(zip(ids, vecs, strict=True))
    # deleted: two in the files (the first transcript) and two only in the queue (the last)
    gone = [f"{b.transcripts[0]}:0", f"{b.transcripts[0]}:1"]
    gone += [f"{b.transcripts[-1]}:5", f"{b.transcripts[-1]}:6"]
    b.coll.delete(ids=gone)
    for g in gone:
        b.vectors.pop(g)
    # written again, the newer vector only in the queue
    again = f"{b.transcripts[-1]}:7"
    b.vectors[again] = _unit(rng, 1)[0]
    b.coll.upsert(
        ids=[again], embeddings=[b.vectors[again]], metadatas=[{"seq": 7}], documents=["x"]
    )
    return b


def _close(b: Built) -> None:
    semantic.close_clients()
    if b.client is not None:
        close = getattr(b.client, "close", None)
        if callable(close):
            close()


# ------------------------------------------------------------------ Chroma's files


def test_split_id() -> None:
    tid = uuid.uuid4()
    assert split_id(f"{tid}:12") == (tid, 12)
    assert split_id("no-colon") is None
    assert split_id("not-a-uuid:3") is None


def test_cursor_round_trip() -> None:
    cur = Cursor(segment="s", phase="load", next=10, total=20, orphans=2, skip=[4, 9])
    assert Cursor.parse(cur.dump()) == cur
    assert Cursor.parse(None) is None
    assert Cursor.parse("{not json") is None


def test_the_files_read_without_chroma(tmp_path: Path) -> None:
    """Every vector Chroma holds is in its files or its queue, exactly as written."""
    b = _build(tmp_path / "chroma")
    try:
        files = ChromaFiles.open(b.root, semantic.collection_name(MODEL))
        files.load_labels()
        assert files.header.dim == DIM
        fd = os.open(files.segment_dir / "data_level0.bin", os.O_RDONLY)
        try:
            el = files.read(fd, 0, files.elements())
        finally:
            os.close(fd)
        seen: dict[str, Any] = {}
        for label, vec, live in zip(el.labels.tolist(), el.vectors, el.live.tolist(), strict=True):
            eid = files.label_to_id.get(label)
            if eid is not None and live:
                seen[eid] = vec
        tail = files.queue_tail()
        for _seq, op, eid, vec in tail:
            if op == 3:
                seen.pop(eid, None)
            elif vec is not None:
                seen[eid] = vec
        assert tail, "the last writes are still in the queue"
        assert set(seen) == set(b.vectors)
        for eid, vec in b.vectors.items():
            assert np.allclose(seen[eid], vec, atol=1e-5), eid
        # and the files agree with Chroma's own read
        some = list(b.vectors)[:50]
        got = b.coll.get(ids=some, include=["embeddings"])
        for eid, vec in zip(got["ids"], got["embeddings"], strict=True):
            assert np.allclose(seen[eid], vec, atol=1e-5)
    finally:
        _close(b)


def test_copy_rows_is_a_binary_copy() -> None:
    tid = uuid.uuid4()
    vec = np.full((1, 4), 0.5, dtype=np.float32)
    raw = chroma_copy.copy_rows([(tid, 3)], vec)
    assert raw.startswith(b"PGCOPY\n\xff\r\n\x00")
    assert raw.endswith(b"\xff\xff")
    body = raw[19:-2]
    assert body[:2] == b"\x00\x03"  # three fields
    assert tid.bytes in body
    assert body.endswith(b"\x00\x04\x00\x00" + b"\x38\x00" * 4)  # dim 4, half 0.5


# ------------------------------------------------------------------ against Postgres

SCHEMA = f"""
CREATE EXTENSION IF NOT EXISTS vector;
CREATE TABLE videos (
    id UUID PRIMARY KEY, library_id UUID NOT NULL, detected_language TEXT
);
CREATE TABLE transcripts (
    id UUID PRIMARY KEY, video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE
);
CREATE TABLE transcript_segments (
    id BIGSERIAL PRIMARY KEY,
    transcript_id UUID NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    seq INT NOT NULL,
    UNIQUE (transcript_id, seq)
);
CREATE TABLE segment_embeddings (
    segment_id BIGINT PRIMARY KEY REFERENCES transcript_segments(id) ON DELETE CASCADE,
    video_id   UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    library_id UUID NOT NULL,
    language   TEXT,
    model      TEXT NOT NULL,
    embedding  halfvec({DIM}) NOT NULL
);
CREATE INDEX segment_embeddings_hnsw ON segment_embeddings
    USING hnsw (embedding halfvec_cosine_ops) WITH (m = 16, ef_construction = 64);
CREATE INDEX segment_embeddings_video ON segment_embeddings (video_id);
CREATE INDEX segment_embeddings_library ON segment_embeddings (library_id);
CREATE TABLE semantic_store_state (
    store TEXT NOT NULL DEFAULT 'chroma' CHECK (store IN ('chroma', 'postgres')),
    copy_cursor TEXT,
    copied_rows BIGINT NOT NULL DEFAULT 0,
    copy_done_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO semantic_store_state (store) VALUES ('chroma');
CREATE FUNCTION semantic_store_ready() RETURNS boolean LANGUAGE sql AS $$
    SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector')
       AND to_regclass('segment_embeddings') IS NOT NULL
$$;
"""


def _swap_db(url: str, name: str) -> str:
    base, _, _ = url.rpartition("/")
    return f"{base}/{name}"


@pytest_asyncio.fixture
async def pg() -> AsyncIterator[Any]:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    import asyncpg  # type: ignore[import-untyped]

    name = "maktaba_copy_" + uuid.uuid4().hex[:8]
    admin = await asyncpg.connect(PG_URL)
    await admin.execute(f'CREATE DATABASE "{name}"')
    conn = await asyncpg.connect(_swap_db(PG_URL, name))
    try:
        try:
            await conn.execute(SCHEMA)
        except asyncpg.exceptions.FeatureNotSupportedError as exc:
            pytest.skip(f"no pgvector: {exc}")
        except asyncpg.exceptions.UndefinedFileError as exc:
            pytest.skip(f"no pgvector: {exc}")
        yield conn
    finally:
        await conn.close()
        await admin.execute(f'DROP DATABASE IF EXISTS "{name}" WITH (FORCE)')
        await admin.close()


async def _segments(conn: Any, b: Built, per: int) -> dict[str, int]:
    """The database's rows for every transcript but the orphan one; id to segment."""
    out: dict[str, int] = {}
    lib = uuid.uuid4()
    for tid in b.transcripts:
        vid = uuid.uuid4()
        await conn.execute("INSERT INTO videos VALUES ($1, $2, 'ar')", vid, lib)
        await conn.execute("INSERT INTO transcripts VALUES ($1, $2)", tid, vid)
        rows = await conn.fetch(
            "INSERT INTO transcript_segments (transcript_id, seq) "
            "SELECT $1, s FROM generate_series(0, $2 - 1) s RETURNING id, seq",
            tid,
            per,
        )
        out.update({f"{tid}:{r['seq']}": int(r["id"]) for r in rows})
    return out


def _cfg(root: Path, tmp: Path, **kw: Any) -> CopyConfig:
    return CopyConfig(
        chroma_dir=root,
        model=MODEL,
        page=kw.pop("page", 500),
        samples=kw.pop("samples", 200),
        min_free_bytes=0,
        build_mem="64MB",
        progress_file=tmp / "progress.json",
        done_file=tmp / "chroma-copy.done",
        progress_every=0.0,
        **kw,
    )


async def _copied(conn: Any) -> dict[int, Any]:
    rows = await conn.fetch("SELECT segment_id, embedding::text AS e FROM segment_embeddings")
    return {int(r["segment_id"]): np.array(json.loads(r["e"]), dtype=np.float32) for r in rows}


@pytest.mark.integration
@pytest.mark.asyncio
async def test_the_copy_moves_every_vector_and_switches(pg: Any, tmp_path: Path) -> None:
    b = _build(tmp_path / "chroma")
    try:
        seg = await _segments(pg, b, 100)
        cfg = _cfg(b.root, tmp_path)
        assert await chroma_copy.copy_chroma(pg, cfg) == chroma_copy.EXIT_OK
    finally:
        _close(b)
    got = await _copied(pg)
    want = {seg[eid]: v for eid, v in b.vectors.items() if eid in seg}
    assert set(got) == set(want)  # orphans and deleted ones are not copied
    for sid, vec in want.items():
        assert np.allclose(got[sid], vec, atol=2e-3)  # halfvec
    row = await pg.fetchrow("SELECT * FROM semantic_store_state")
    assert row["store"] == "postgres" and row["copy_done_at"] is not None
    assert json.loads(row["copy_cursor"])["orphans"] == 100
    meta = await pg.fetchrow("SELECT DISTINCT model, language FROM segment_embeddings")
    assert (meta["model"], meta["language"]) == (MODEL, "ar")
    valid = await pg.fetchval(
        "SELECT i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid "
        "WHERE c.relname = 'segment_embeddings_hnsw'"
    )
    assert valid is True
    done = json.loads((tmp_path / "chroma-copy.done").read_text())
    assert done["chroma_dir"] == str(b.root)
    assert json.loads((tmp_path / "progress.json").read_text())["phase"] == "done"
    # a second run has nothing to do
    assert await chroma_copy.copy_chroma(pg, cfg) == chroma_copy.EXIT_OK


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_killed_copy_continues(
    pg: Any, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A stop in the middle: the next run goes on from the last page, keeps what the
    index stage wrote meanwhile, and ends with the same rows."""
    b = _build(tmp_path / "chroma")
    try:
        seg = await _segments(pg, b, 100)
        real = chroma_copy._write_page
        pages = {"n": 0}

        async def dies(*a: Any, **kw: Any) -> int:
            pages["n"] += 1
            if pages["n"] == 4:
                raise RuntimeError("killed")
            return await real(*a, **kw)

        monkeypatch.setattr(chroma_copy, "_write_page", dies)
        cfg = _cfg(b.root, tmp_path)
        with pytest.raises(RuntimeError):
            await chroma_copy.copy_chroma(pg, cfg)
        await pg.execute("SELECT pg_advisory_unlock_all()")
        cur = Cursor.parse(await pg.fetchval("SELECT copy_cursor FROM semantic_store_state"))
        assert cur is not None and cur.phase == "load" and 0 < cur.next < cur.total
        # the index was dropped for the load
        assert await pg.fetchval("SELECT to_regclass('segment_embeddings_hnsw')") is None
        # the index stage writes one meanwhile (both stores): its vector stays
        eid = f"{b.transcripts[-2]}:99"
        newer = np.zeros(DIM, dtype=np.float32)
        newer[0] = 1.0
        await pg.execute(
            "INSERT INTO segment_embeddings SELECT ts.id, t.video_id, v.library_id, 'ar', $2, "
            "$3::halfvec FROM transcript_segments ts JOIN transcripts t ON t.id = ts.transcript_id "
            "JOIN videos v ON v.id = t.video_id WHERE ts.id = $1",
            seg[eid],
            MODEL,
            "[" + ",".join(str(float(x)) for x in newer) + "]",
        )
        monkeypatch.setattr(chroma_copy, "_write_page", real)
        assert await chroma_copy.copy_chroma(pg, cfg) == chroma_copy.EXIT_OK
    finally:
        _close(b)
    got = await _copied(pg)
    want = {seg[e]: v for e, v in b.vectors.items() if e in seg}
    assert set(got) == set(want)
    assert np.allclose(got[seg[eid]], newer)
    # the load's rows, counted once though a page ran twice (the queue's are not counted)
    copied = await pg.fetchval("SELECT copied_rows FROM semantic_store_state")
    assert len(want) - 1000 < copied <= len(want)


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_failed_check_leaves_search_on_chroma(pg: Any, tmp_path: Path) -> None:
    b = _build(tmp_path / "chroma", transcripts=15)
    try:
        await _segments(pg, b, 100)
        cfg = _cfg(b.root, tmp_path, min_recall=1.01)
        assert await chroma_copy.copy_chroma(pg, cfg) == chroma_copy.EXIT_VERIFY_FAILED
    finally:
        _close(b)
    row = await pg.fetchrow("SELECT * FROM semantic_store_state")
    assert row["store"] == "chroma" and row["copy_done_at"] is None
    assert json.loads(row["copy_cursor"])["phase"] == "verify"
    assert not (tmp_path / "chroma-copy.done").exists()


@pytest.mark.integration
@pytest.mark.asyncio
async def test_no_pgvector_store_no_copy(pg: Any, tmp_path: Path) -> None:
    await pg.execute(
        "CREATE OR REPLACE FUNCTION semantic_store_ready() RETURNS boolean "
        "LANGUAGE sql AS $$ SELECT false $$"
    )
    cfg = _cfg(tmp_path / "chroma", tmp_path)
    assert await chroma_copy.copy_chroma(pg, cfg) == chroma_copy.EXIT_NOT_READY


def test_ties_count_as_found() -> None:
    """Many passages share one vector (a greeting): any ten of them is the exact top 10,
    so a result as close as the exact 10th counts, whatever its id."""
    from maktaba_pipeline.search.chroma_copy import tie_hits

    truth = [0.0] * 10  # ten exact duplicates
    assert tie_hits([0.0] * 10, truth) == 10  # other duplicates: all correct
    assert tie_hits([0.0] * 9 + [0.2], truth) == 9  # one farther: a miss
    near = [0.01, 0.02, 0.03, 0.0333, 0.0333, 0.0333, 0.0333, 0.0333, 0.0333, 0.0333]
    assert tie_hits(near, near) == 10
    assert tie_hits([0.5] * 10, near) == 0
    assert tie_hits([], []) == 0
