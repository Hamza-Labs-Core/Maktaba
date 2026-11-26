"""The copy of the Chroma vectors into Postgres (search/chroma_copy.py, stories 43.3, 43.5).

The Chroma folder is the one Chroma 1.5.9 wrote (``tests/search/data/chroma-1.5.9``,
made by ``tests/search/chroma_fixture.py``): chromadb is no dependency. The copy reads
the files only. The Postgres tests need ``MAKTABA_TEST_PG_URL`` with pgvector.
"""

from __future__ import annotations

import argparse
import asyncio
import contextlib
import json
import os
import shutil
import sqlite3
import time
import uuid
from collections.abc import AsyncIterator
from pathlib import Path
from typing import Any
from unittest.mock import AsyncMock, Mock

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
from maktaba_pipeline.search.pgstore import COPY_LOCK_KEY, copy_lock_held  # noqa: E402
from tests.search import chroma_fixture  # noqa: E402

MODEL = "intfloat/multilingual-e5-small"
DIM = chroma_fixture.DIM
PER = chroma_fixture.PER
PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")
FIXTURE = Path(__file__).parent / "data" / "chroma-1.5.9"


class Built:
    """The fixture's Chroma folder (a copy of it) and what it holds."""

    def __init__(self, root: Path) -> None:
        self.root = root
        self.vectors: dict[str, Any] = chroma_fixture.expected()
        self.transcripts: list[uuid.UUID] = list(chroma_fixture.TRANSCRIPTS)
        self.orphan_transcript = chroma_fixture.ORPHAN


def _build(root: Path) -> Built:
    """The folder Chroma 1.5.9 wrote: five transcripts of fifty segments and one more
    whose transcript is not in the database (orphans); four deleted and one written
    again after the files' last write (those only in Chroma's queue)."""
    shutil.copytree(FIXTURE, root)
    return Built(root)


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
    """Every vector Chroma 1.5.9 holds is in its files or its queue, exactly as written."""
    b = _build(tmp_path / "chroma")
    files = ChromaFiles.open(b.root, chroma_copy.collection_name(MODEL))
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


def test_the_collection_and_folder_names() -> None:
    assert chroma_copy.collection_name(MODEL) == chroma_fixture.COLLECTION


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


def test_only_queued_ids_are_inverted() -> None:
    labels = {i: str(i) for i in range(10_000)}
    labels[10_000] = labels[30]
    queued = {"5": None, "30": None, "absent": None}
    original = {eid: label for label, eid in labels.items()}
    inverse = chroma_copy._queued_labels(labels, queued)
    assert inverse == {eid: original[eid] for eid in queued if eid in original}
    assert len(inverse) <= len(queued)
    assert chroma_copy._queued_labels(labels, {}) == {}


@pytest.mark.parametrize("damaged", ["sqlite", "header", "header_missing", "data", "metadata"])
@pytest.mark.parametrize("entrypoint", ["cli", "library"])
def test_unreadable_chroma_reports_failed_and_exits_78(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, damaged: str, entrypoint: str
) -> None:
    b = _build(tmp_path / "chroma")
    files = ChromaFiles.open(b.root, chroma_copy.collection_name(MODEL))
    if damaged == "sqlite":
        (b.root / "chroma.sqlite3").write_bytes(b"not a database" * 100)
    elif damaged == "header":
        (files.segment_dir / "header.bin").write_bytes(b"short header")
    elif damaged == "header_missing":
        (files.segment_dir / "header.bin").unlink()
    elif damaged == "data":
        (files.segment_dir / "data_level0.bin").write_bytes(b"short data")
    else:
        (files.segment_dir / "index_metadata.pickle").write_bytes(b"broken pickle")
    monkeypatch.setattr(time, "sleep", lambda _: None)
    conn = AsyncMock()
    conn.fetchrow.return_value = {
        "store": "chroma",
        "copy_cursor": None,
        "copied_rows": 0,
        "copy_done_at": None,
    }
    drop_index = AsyncMock()
    save = AsyncMock()
    monkeypatch.setattr(chroma_copy, "_drop_index", drop_index)
    monkeypatch.setattr(chroma_copy, "_save", save)
    monkeypatch.setattr(chroma_copy, "_check_disk", AsyncMock(return_value=None))
    progress = tmp_path / "status.json"
    progress.write_text('{"phase": "load"}')
    if entrypoint == "cli":
        asyncpg = pytest.importorskip("asyncpg")
        monkeypatch.setattr(asyncpg, "connect", AsyncMock(return_value=conn))
        monkeypatch.setattr(semantic, "embed_model", lambda: MODEL)
        args = argparse.Namespace(
            database_url="postgres://example.invalid/test",
            chroma_dir=str(b.root),
            page=30,
            samples=3,
            min_recall=0.95,
            ef_search=400,
            min_free_gb=0,
            build_mem="",
            progress_file=str(progress),
            done_file=None,
            no_switch=False,
        )
        code = chroma_copy.main(args)
        conn.close.assert_awaited_once()
    else:
        cfg = CopyConfig(chroma_dir=b.root, model=MODEL, progress_file=progress)
        code = asyncio.run(chroma_copy.copy_chroma(conn, cfg))
        conn.close.assert_not_awaited()
    assert code == chroma_copy.EXIT_NOT_READY
    status = json.loads(progress.read_text())
    assert status["phase"] == "failed" and status["permanent"] is True and status["reason"]
    assert drop_index.await_count == 0
    save.assert_not_awaited()
    conn.fetchval.assert_awaited_with("SELECT pg_advisory_unlock($1::bigint)", COPY_LOCK_KEY)


@pytest.mark.asyncio
@pytest.mark.parametrize("unlock_fails", [False, True])
async def test_copy_attempts_unlock_without_masking_an_error(
    tmp_path: Path, unlock_fails: bool
) -> None:
    conn = AsyncMock()
    conn.fetchval.side_effect = [True, True, ConnectionError("closed") if unlock_fails else True]
    conn.fetchrow.side_effect = RuntimeError("copy interrupted")
    with pytest.raises(RuntimeError, match="copy interrupted"):
        await chroma_copy.copy_chroma(conn, CopyConfig(chroma_dir=tmp_path, model=MODEL))
    conn.fetchval.assert_awaited_with("SELECT pg_advisory_unlock($1::bigint)", COPY_LOCK_KEY)


@pytest.mark.asyncio
async def test_copy_leaves_another_sessions_lock_alone(tmp_path: Path) -> None:
    conn = AsyncMock()
    conn.fetchval.side_effect = [True, False]
    assert (
        await chroma_copy.copy_chroma(conn, CopyConfig(chroma_dir=tmp_path, model=MODEL))
        == chroma_copy.EXIT_FAILED
    )
    assert conn.fetchval.await_count == 2
    conn.fetchval.assert_awaited_with("SELECT pg_try_advisory_lock($1::bigint)", COPY_LOCK_KEY)


def test_a_locked_chroma_database_can_be_retried() -> None:
    busy = sqlite3.OperationalError("database is locked")
    busy.sqlite_errorcode = sqlite3.SQLITE_BUSY
    with pytest.raises(sqlite3.OperationalError, match="locked"), chroma_copy._read_store():
        raise busy


def test_only_surviving_segments_of_the_current_transcript_are_eligible() -> None:
    with contextlib.closing(sqlite3.connect(":memory:")) as conn:
        conn.create_function("regexp", 2, lambda pattern, text: bool(text.strip()))
        conn.executescript(
            """
            CREATE TABLE videos (id);
            CREATE TABLE transcripts (id, video_id, is_active, created_at);
            CREATE TABLE transcript_segments (id, transcript_id, seq, text);
            CREATE TABLE chroma_copy_page (transcript_id, seq);
            INSERT INTO videos VALUES (1);
            INSERT INTO transcripts VALUES (10, 1, false, 3), (11, 1, true, 1), (12, 1, true, 2);
            INSERT INTO transcript_segments VALUES
                (1, 10, 0, 'inactive'), (2, 11, 0, 'older'), (3, 12, 0, 'current'),
                (4, 12, 1, ' ');
            INSERT INTO chroma_copy_page VALUES (10, 0), (11, 0), (12, 0), (12, 1), (12, 2);
            """
        )
        query = (
            "SELECT ts.id FROM chroma_copy_page p"
            + chroma_copy._ELIGIBLE
            + chroma_copy._CURRENT.replace("ts.text ~", "ts.text REGEXP")
        )
        assert conn.execute(query).fetchall() == [(3,)]


@pytest.mark.asyncio
@pytest.mark.parametrize("active_count, present", [(0, 0), (40, 40), (150, 0)])
async def test_verify_counts_only_current_source_rows(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, active_count: int, present: int
) -> None:
    b = _build(tmp_path / "chroma")
    files = ChromaFiles.open(b.root, chroma_copy.collection_name(MODEL))
    files.load_labels()
    cur = Cursor(segment=files.segment_id, phase="verify", total=files.elements() - 1)
    assert cur.total > 150
    conn = AsyncMock()
    conn.transaction = lambda **_: contextlib.nullcontext()
    conn.fetch.return_value = [
        {"ordinal": i + 1, "id": i + 1, "present": i < present} for i in range(active_count)
    ]
    recall = AsyncMock(return_value=(1.0, 1))
    sample = Mock(wraps=chroma_copy._sample)
    monkeypatch.setattr(chroma_copy, "_recall", recall)
    monkeypatch.setattr(chroma_copy, "_sample", sample)
    verdict = await chroma_copy._verify(conn, files, _cfg(b.root, tmp_path), cur)
    assert verdict.expected == active_count and verdict.rows == present
    assert verdict.ok is (active_count == present)
    assert recall.await_count == (2 if active_count and verdict.ok else 0)
    assert sample.call_args.args[-1] == cur.total


# ------------------------------------------------------------------ against Postgres

SCHEMA = f"""
CREATE EXTENSION IF NOT EXISTS vector;
CREATE TABLE videos (
    id UUID PRIMARY KEY, library_id UUID NOT NULL, detected_language TEXT
);
CREATE TABLE transcripts (
    id UUID PRIMARY KEY, video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    is_active BOOLEAN NOT NULL DEFAULT true, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE transcript_segments (
    id BIGSERIAL PRIMARY KEY,
    transcript_id UUID NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    seq INT NOT NULL,
    text TEXT NOT NULL DEFAULT 'a sentence',
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
        await conn.execute("INSERT INTO transcripts (id, video_id) VALUES ($1, $2)", tid, vid)
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
        samples=kw.pop("samples", 50),
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
    seg = await _segments(pg, b, PER)
    cfg = _cfg(b.root, tmp_path)
    assert await chroma_copy.copy_chroma(pg, cfg) == chroma_copy.EXIT_OK
    got = await _copied(pg)
    want = {seg[eid]: v for eid, v in b.vectors.items() if eid in seg}
    assert set(got) == set(want)  # orphans and deleted ones are not copied
    for sid, vec in want.items():
        assert np.allclose(got[sid], vec, atol=2e-3)  # halfvec
    row = await pg.fetchrow("SELECT * FROM semantic_store_state")
    assert row["store"] == "postgres" and row["copy_done_at"] is not None
    assert json.loads(row["copy_cursor"])["orphans"] == PER
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
    seg = await _segments(pg, b, PER)
    real = chroma_copy._write_page
    pages = {"n": 0}

    async def dies(*a: Any, **kw: Any) -> int:
        pages["n"] += 1
        if pages["n"] == 4:
            raise RuntimeError("killed")
        return await real(*a, **kw)

    monkeypatch.setattr(chroma_copy, "_write_page", dies)
    cfg = _cfg(b.root, tmp_path, page=40)
    with pytest.raises(RuntimeError):
        await chroma_copy.copy_chroma(pg, cfg)
    assert not await copy_lock_held(pg)
    cur = Cursor.parse(await pg.fetchval("SELECT copy_cursor FROM semantic_store_state"))
    assert cur is not None and cur.phase == "load" and 0 < cur.next < cur.total
    # the index was dropped for the load
    assert await pg.fetchval("SELECT to_regclass('segment_embeddings_hnsw')") is None
    # the index stage writes one meanwhile (both stores): its vector stays
    eid = f"{b.transcripts[-2]}:{PER - 1}"
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
    got = await _copied(pg)
    want = {seg[e]: v for e, v in b.vectors.items() if e in seg}
    assert set(got) == set(want)
    assert np.allclose(got[seg[eid]], newer)
    # the load's rows, counted once though a page ran twice (the queue's are not counted)
    copied = await pg.fetchval("SELECT copied_rows FROM semantic_store_state")
    assert 0 < copied <= len(want)


@pytest.mark.integration
@pytest.mark.asyncio
async def test_copy_cannot_restore_vectors_removed_by_reindexing(pg: Any, tmp_path: Path) -> None:
    b = _build(tmp_path / "chroma")
    seg = await _segments(pg, b, PER)
    inactive, older, blank, active = b.transcripts[:4]
    await pg.execute("UPDATE transcripts SET is_active = false WHERE id = $1", inactive)
    await pg.execute(
        "INSERT INTO transcripts (id, video_id, created_at) "
        "SELECT $2, video_id, created_at + interval '1 second' FROM transcripts WHERE id = $1",
        older,
        uuid.uuid4(),
    )
    await pg.execute(
        "UPDATE transcript_segments SET text = E' \\t' WHERE transcript_id = $1", blank
    )
    tids = [inactive, older, blank, active]
    vec = np.full((len(tids), DIM), 1 / DIM**0.5, dtype=np.float32)
    async with pg.transaction():
        await chroma_copy._temp_table(pg, DIM)
        matched = await chroma_copy._write_page(pg, MODEL, [(tid, 0) for tid in tids], vec)
    assert matched == 1
    assert set(await _copied(pg)) == {seg[f"{active}:0"]}


@pytest.mark.integration
@pytest.mark.asyncio
async def test_verify_accepts_large_deletions_since_load(pg: Any, tmp_path: Path) -> None:
    b = _build(tmp_path / "chroma")
    await _segments(pg, b, PER)
    cfg = _cfg(b.root, tmp_path, switch=False)
    assert await chroma_copy.copy_chroma(pg, cfg) == chroma_copy.EXIT_OK
    await pg.execute(
        "DELETE FROM videos WHERE id IN (SELECT video_id FROM transcripts WHERE id = ANY($1))",
        b.transcripts[:2],
    )
    await pg.execute("UPDATE transcripts SET is_active = false WHERE id = $1", b.transcripts[2])
    await pg.execute(
        "DELETE FROM segment_embeddings WHERE segment_id IN "
        "(SELECT id FROM transcript_segments WHERE transcript_id = $1)",
        b.transcripts[2],
    )
    assert await pg.fetchval("SELECT copied_rows FROM semantic_store_state") > (
        100 + await pg.fetchval("SELECT count(*) FROM segment_embeddings")
    )
    assert not await copy_lock_held(pg)
    cfg.switch = True
    assert await chroma_copy.copy_chroma(pg, cfg) == chroma_copy.EXIT_OK


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_failed_check_leaves_the_copy_not_done(pg: Any, tmp_path: Path) -> None:
    b = _build(tmp_path / "chroma")
    await _segments(pg, b, PER)
    cfg = _cfg(b.root, tmp_path, min_recall=1.01)
    assert await chroma_copy.copy_chroma(pg, cfg) == chroma_copy.EXIT_VERIFY_FAILED
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
