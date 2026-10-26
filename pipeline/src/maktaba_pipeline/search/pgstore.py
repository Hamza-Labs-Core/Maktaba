"""Semantic search's vectors in Postgres: ``segment_embeddings`` (pgvector, Epic 43).

One row per transcript segment: its e5 vector as ``halfvec(384)``, its video, library and
language; an HNSW index (cosine) answers the nearest ones. Slot 0152 creates the table
where the ``vector`` extension is installed (``ensure_segment_embeddings()``, called
again at every start: a later start with pgvector creates what was deferred).

Which store search reads is ``semantic_store_state.store``:

* ``chroma`` until the one-time copy of the Chroma vectors (story 43.3) is done: search
  and ``chapter_infer`` read Chroma; the index stage writes both (Postgres when it is
  ready), so nothing indexed during the copy is missed;
* ``postgres`` after it, and on a home that never had a Chroma collection (decided at
  start, :func:`prepare`): everything reads and writes Postgres. Without pgvector the
  semantic leg is then off (the API falls back to full-text search).

The state is read again every :data:`STATE_TTL_SEC`, so the switch takes effect without a
restart. SQLite has neither table: there the store is Chroma.
"""

from __future__ import annotations

import sqlite3
import time
import weakref
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from uuid import UUID

from ..log import get_logger
from .semantic import IndexedSegment, SegmentHit, StoredVector, chroma_dir

__all__ = [
    "CHROMA",
    "DIMENSIONS",
    "POSTGRES",
    "STATE_TTL_SEC",
    "PgVectorStore",
    "StoreState",
    "chroma_has_vectors",
    "forget_state",
    "prepare",
    "store_state",
]

CHROMA = "chroma"
POSTGRES = "postgres"

#: the vectors' size (``halfvec(384)``: multilingual-e5-small)
DIMENSIONS = 384

#: how long a read of ``semantic_store_state`` is kept
STATE_TTL_SEC = 30.0

#: HNSW candidates per query (pgvector's default is 40, its most 1000)
EF_SEARCH = 100
EF_SEARCH_MAX = 1000

_log = get_logger()

#: the state cache's clock (tests move it)
_clock = time.monotonic


@dataclass(frozen=True, slots=True)
class StoreState:
    """Which store search reads, and whether ``segment_embeddings`` can be used."""

    store: str = CHROMA
    ready: bool = False

    @property
    def write_postgres(self) -> bool:
        return self.ready

    @property
    def write_chroma(self) -> bool:
        """Chroma is written while it is what search reads (the copy's dual write)."""
        return self.store == CHROMA

    @property
    def read_postgres(self) -> bool:
        return self.store == POSTGRES


_STATE: weakref.WeakKeyDictionary[Any, tuple[float, StoreState]] = weakref.WeakKeyDictionary()


def forget_state() -> None:
    """Drop the cached reads (tests, and after :func:`prepare` changed the row)."""
    _STATE.clear()


async def store_state(db: Any, *, fresh: bool = False) -> StoreState:
    """The store's state, at most :data:`STATE_TTL_SEC` old. SQLite, or a database
    older than slot 0152: Chroma."""
    if getattr(db, "dialect", "") != "postgres":
        return StoreState()
    now = _clock()
    hit = _STATE.get(db)
    if hit is not None and not fresh and now - hit[0] < STATE_TTL_SEC:
        return hit[1]
    try:
        row = await db.fetchrow(
            "SELECT semantic_store_ready() AS ready,"
            " (SELECT store FROM semantic_store_state LIMIT 1) AS store"
        )
    except Exception as exc:  # noqa: BLE001 - before slot 0152: Chroma, as before
        _log.debug("semantic_store_state_unread", error=f"{type(exc).__name__}: {exc}")
        state = StoreState()
    else:
        store = str(row["store"] or CHROMA) if row is not None else CHROMA
        state = StoreState(
            store=store if store in (CHROMA, POSTGRES) else CHROMA,
            ready=bool(row["ready"]) if row is not None else False,
        )
    _STATE[db] = (now, state)
    return state


def chroma_has_vectors(path: str | None = None) -> bool:
    """Whether the Chroma folder holds any vector (in a collection or still in its
    write log). Read with SQLite, not chromadb; anything unreadable counts as yes."""
    db_file = Path(path or chroma_dir()) / "chroma.sqlite3"
    if not db_file.exists():
        return False
    try:
        conn = sqlite3.connect(f"file:{db_file}?mode=ro", uri=True, timeout=5)
        try:
            for table in ("embeddings", "embeddings_queue"):
                if conn.execute(f"SELECT 1 FROM {table} LIMIT 1").fetchone() is not None:  # noqa: S608
                    return True
        finally:
            conn.close()
    except sqlite3.Error:
        return True
    return False


async def prepare(db: Any, *, chroma_path: str | None = None) -> StoreState:
    """At a home worker's start: create what pgvector allows (slot 0152's deferred part),
    and move a home that has no Chroma vectors and no copy under way to Postgres."""
    if getattr(db, "dialect", "") != "postgres":
        return StoreState()
    try:
        await db.fetchval("SELECT ensure_segment_embeddings()")
    except Exception as exc:  # noqa: BLE001 - before slot 0152, or no right to: as it is
        _log.warning("semantic_store_ensure_failed", error=f"{type(exc).__name__}: {exc}")
        return await store_state(db, fresh=True)
    state = await store_state(db, fresh=True)
    if state.store == CHROMA and not chroma_has_vectors(chroma_path):
        moved = await db.fetchval(
            "UPDATE semantic_store_state SET store = 'postgres', updated_at = now()"
            " WHERE store = 'chroma' AND copied_rows = 0 AND copy_cursor IS NULL"
            " RETURNING store"
        )
        if moved is not None:
            _log.info("semantic_store_postgres", reason="no Chroma vectors", ready=state.ready)
            state = await store_state(db, fresh=True)
    return state


def _literal(vec: Sequence[float]) -> str:
    """A pgvector text literal: ``[0.1,0.2,...]``."""
    return "[" + ",".join(repr(float(x)) for x in vec) + "]"


def _parse(text: str) -> list[float]:
    body = text.strip()[1:-1]
    return [float(x) for x in body.split(",")] if body else []


def _uuids(values: Sequence[str] | None) -> list[str] | None:
    """The valid ids of a filter (None: no filter); an id that is not a UUID matches
    nothing, as in Chroma."""
    if not values:
        return None
    out: list[str] = []
    for v in values:
        try:
            out.append(str(UUID(str(v))))
        except ValueError:
            continue
    return out


class PgVectorStore:
    """``segment_embeddings`` for one embedding model, over the pipeline's database."""

    def __init__(self, db: Any, model: str) -> None:
        self.db = db
        self.model = model

    async def write_vectors(
        self,
        *,
        video_id: UUID | str,
        library_id: UUID | str,
        chunk: Sequence[IndexedSegment],
        vectors: Sequence[Sequence[float]],
        language: str | None = None,
    ) -> int:
        """Upsert one embedded batch in one statement; a segment deleted meanwhile is
        skipped. The number written."""
        if not chunk:
            return 0
        for v in vectors:
            if len(v) != DIMENSIONS:
                raise ValueError(
                    f"{self.model} gives {len(v)}-number vectors; segment_embeddings holds "
                    f"{DIMENSIONS}"
                )
        status = await self.db.execute(
            "INSERT INTO segment_embeddings"
            " (segment_id, video_id, library_id, language, model, embedding)"
            " SELECT u.segment_id, $1::uuid, $2::uuid, $3, $4, u.embedding::halfvec"
            " FROM unnest($5::bigint[], $6::text[]) AS u(segment_id, embedding)"
            " JOIN transcript_segments s ON s.id = u.segment_id"
            " ON CONFLICT (segment_id) DO UPDATE SET video_id = EXCLUDED.video_id,"
            " library_id = EXCLUDED.library_id, language = EXCLUDED.language,"
            " model = EXCLUDED.model, embedding = EXCLUDED.embedding",
            str(video_id),
            str(library_id),
            language or None,
            self.model,
            [int(s.segment_id) for s in chunk],
            [_literal(v) for v in vectors],
        )
        try:
            return int(str(status).rsplit(" ", 1)[-1])
        except ValueError:
            return len(chunk)

    async def delete_segments(self, segment_ids: Sequence[int]) -> None:
        if segment_ids:
            await self.db.execute(
                "DELETE FROM segment_embeddings WHERE segment_id = ANY($1::bigint[])",
                [int(i) for i in segment_ids],
            )

    async def delete_ids(self, ids: Sequence[str]) -> None:
        """Delete by Chroma ids (``{transcript_id}:{seq}``), mapped to their segments."""
        tids: list[str] = []
        seqs: list[int] = []
        for i in ids:
            tid, _, seq = str(i).rpartition(":")
            try:
                tids.append(str(UUID(tid)))
                seqs.append(int(seq))
            except ValueError:
                continue
        if tids:
            await self.db.execute(
                "DELETE FROM segment_embeddings e USING transcript_segments s,"
                " unnest($1::uuid[], $2::int[]) AS u(transcript_id, seq)"
                " WHERE s.transcript_id = u.transcript_id AND s.seq = u.seq"
                " AND e.segment_id = s.id",
                tids,
                seqs,
            )

    async def delete_video(self, video_id: UUID | str) -> None:
        await self.db.execute(
            "DELETE FROM segment_embeddings WHERE video_id = $1::uuid", str(video_id)
        )

    async def keep_only(self, video_id: UUID | str, segment_ids: Sequence[int]) -> int:
        """Delete the video's vectors but those of ``segment_ids`` (its other
        transcripts', its blank segments'); the number deleted."""
        status = await self.db.execute(
            "DELETE FROM segment_embeddings WHERE video_id = $1::uuid"
            " AND segment_id <> ALL($2::bigint[])",
            str(video_id),
            [int(i) for i in segment_ids],
        )
        try:
            return int(str(status).rsplit(" ", 1)[-1])
        except ValueError:
            return 0

    async def has_vectors(self, video_id: UUID | str) -> bool:
        row = await self.db.fetchrow(
            "SELECT 1 FROM segment_embeddings WHERE video_id = $1::uuid LIMIT 1", str(video_id)
        )
        return row is not None

    async def video_vectors(
        self, video_id: UUID | str, transcript_id: UUID | str | None = None
    ) -> list[StoredVector]:
        """The video's vectors (only ``transcript_id``'s when given) in segment order."""
        sql = (
            "SELECT e.segment_id, s.transcript_id, s.seq, s.start_sec, s.end_sec, s.text,"
            " e.embedding::text AS embedding"
            " FROM segment_embeddings e JOIN transcript_segments s ON s.id = e.segment_id"
            " WHERE e.video_id = $1::uuid"
        )
        args: list[Any] = [str(video_id)]
        if transcript_id is not None:
            sql += " AND s.transcript_id = $2::uuid"
            args.append(str(transcript_id))
        rows = await self.db.fetch(sql + " ORDER BY s.seq", *args)
        return [
            StoredVector(
                segment_id=int(r["segment_id"]),
                transcript_id=str(r["transcript_id"]),
                seq=int(r["seq"]),
                start_sec=float(r["start_sec"]),
                end_sec=float(r["end_sec"]),
                text=str(r["text"] or ""),
                embedding=_parse(str(r["embedding"])),
            )
            for r in rows
        ]

    async def search(
        self,
        query_vec: Sequence[float],
        *,
        k: int = 50,
        library_ids: Sequence[str] | None = None,
        languages: Sequence[str] | None = None,
    ) -> list[SegmentHit]:
        """The ``k`` segments nearest to ``query_vec`` (score = cosine similarity), only
        from ``library_ids`` and of ``languages`` when given. The filters apply inside
        the index scan (iterative scans), so a small library still gets its top k."""
        if k <= 0 or not query_vec:
            return []
        libs = _uuids(library_ids)
        langs = [str(x) for x in (languages or []) if str(x)] or None
        if libs is not None and not libs:
            return []
        where: list[str] = []
        args: list[Any] = [_literal(query_vec), int(k)]
        if libs is not None:
            args.append(libs)
            where.append(f"library_id = ANY(${len(args)}::uuid[])")
        if langs is not None:
            args.append(langs)
            where.append(f"language = ANY(${len(args)}::text[])")
        sql = (
            "WITH nn AS (SELECT segment_id, video_id,"
            " embedding <=> $1::text::halfvec AS dist FROM segment_embeddings"
            + (" WHERE " + " AND ".join(where) if where else "")
            + " ORDER BY embedding <=> $1::text::halfvec LIMIT $2)"
            " SELECT nn.segment_id, nn.video_id, nn.dist, s.start_sec, s.end_sec, s.text"
            " FROM nn JOIN transcript_segments s ON s.id = nn.segment_id ORDER BY nn.dist"
        )
        async with self.db.transaction():
            await self.db.execute(
                "SELECT set_config('hnsw.ef_search', $1, true),"
                " set_config('hnsw.iterative_scan', 'relaxed_order', true)",
                str(min(EF_SEARCH_MAX, max(EF_SEARCH, int(k)))),
            )
            rows = await self.db.fetch(sql, *args)
        return [
            SegmentHit(
                segment_id=int(r["segment_id"]),
                video_id=str(r["video_id"]),
                start_sec=float(r["start_sec"]),
                end_sec=float(r["end_sec"]),
                score=1.0 - float(r["dist"]),
                text=str(r["text"] or ""),
            )
            for r in rows
        ]
