"""``index``: a video's active transcript as vectors for semantic search, one job per video.

Reads the video's active ``transcripts`` row (the newest when several audio tracks have
one) and its ``transcript_segments``, embeds every segment with the configured model
(:func:`maktaba_pipeline.search.semantic.embed_model`, default
``intfloat/multilingual-e5-small``, ``"passage: "`` prefix, batched, on Apple's GPU when
there is one) and replaces the video's vectors in the persistent Chroma collection
(``MAKTABA_CHROMA_DIR``). Once the new vectors are written, the vectors the video may
still have of its other transcripts, and of segments of this one that are blank now,
are deleted by id (the ids come from the database: a delete by video, or a read of the
video's ids, scans the whole collection's metadata), so a re-transcription never leaves
the old transcript searchable.

The input fingerprint is the active transcript, its segment count, the model and
:data:`~maktaba_pipeline.search.semantic.INDEX_VERSION`: a new transcript, a changed
model or a new index format indexes the video again; anything else is skipped. Once
search reads Postgres (:mod:`~maktaba_pipeline.search.pgstore`), a video with segments
and no row in ``segment_embeddings`` counts as not indexed (a new home after a
switchover: the machines' copies hold no vectors) and is indexed again.

Where the vectors go follows ``semantic_store_state`` (Epic 43): ``segment_embeddings``
whenever pgvector is ready, and Chroma too while search still reads it (the copy's dual
write). A Postgres write is one statement (atomic), outside the write gate.

A video without an active transcript has its vectors removed and succeeds (there is
nothing to search). Segments are embedded and written in batches
(``MAKTABA_EMBED_BATCH``, default 64), each in a worker thread, with progress and a cancel
check between batches. Every Chroma write goes through :mod:`~maktaba_pipeline.search.gate`:
one write at a time in the process, none cut short by the worker stopping. Only the
upsert holds it: a batch is embedded before (the model has its own lock), so one job
embeds while another writes. Each job logs where its time went (``index_timings``: the
embedding, the writes, the wait for the write lock, the read), also in its summary.
"""

from __future__ import annotations

import asyncio
import time
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any
from uuid import UUID

from ...db.jobs import Job
from ...log import get_logger
from ...search import gate
from ...search.embedder import embed_id_for
from ...search.pgstore import PgVectorStore, store_state
from ...search.semantic import (
    INDEX_VERSION,
    Embedder,
    IndexedSegment,
    SemanticIndex,
    SemanticUnavailable,
    batch_size,
    collection_open,
    embed_model,
    embed_segments,
    get_embedder,
)
from ..context import StageContext, StageFailure
from ..graph import StageResult, Subject
from ..hashing import canonical_hash
from ..sql import uid
from ._common import load_video, require_video
from .registry import StageImpl

__all__ = [
    "IMPL",
    "active_transcript",
    "reset_embedder_factory",
    "reset_index_factory",
    "set_embedder_factory",
    "set_index_factory",
]

_ACTIVE_SQL = (
    "SELECT t.id AS id,"
    " (SELECT COUNT(*) FROM transcript_segments s WHERE s.transcript_id = t.id) AS n"
    " FROM transcripts t WHERE t.video_id = $1 AND t.is_active"
    " ORDER BY t.created_at DESC, t.id DESC LIMIT 1"
)

#: the segments of the video's other transcripts: their vectors (if any) are stale once
#: the active one is written. Transcripts and their segments are never deleted but with
#: the video, so these are every vector the video can have besides the active one's
_OTHER_SEGMENTS_SQL = (
    "SELECT s.transcript_id AS transcript_id, s.seq AS seq FROM transcript_segments s"
    " JOIN transcripts t ON t.id = s.transcript_id WHERE t.video_id = $1 AND t.id <> $2"
)

_SEGMENTS_SQL = (
    "SELECT id, seq, start_sec, end_sec, text FROM transcript_segments"
    " WHERE transcript_id = $1 ORDER BY seq"
)

#: builds the vector store the stage writes to; tests swap in a fake (``set_index_factory``)
_index_factory: Callable[[], SemanticIndex] = SemanticIndex.default


def set_index_factory(factory: Callable[[], SemanticIndex]) -> None:
    global _index_factory
    _index_factory = factory


def reset_index_factory() -> None:
    global _index_factory
    _index_factory = SemanticIndex.default


#: the embedding model when Chroma is not written (search reads Postgres); tests swap in
#: a fake (``set_embedder_factory``)
_embedder_factory: Callable[[], Embedder] = get_embedder


def set_embedder_factory(factory: Callable[[], Embedder]) -> None:
    global _embedder_factory
    _embedder_factory = factory


def reset_embedder_factory() -> None:
    global _embedder_factory
    _embedder_factory = get_embedder


async def active_transcript(db: Any, video_id: UUID | str) -> tuple[str, int] | None:
    """``(transcript id, segment count)`` of the video's active transcript, or None."""
    row = await db.fetchrow(_ACTIVE_SQL, uid(video_id))
    if row is None:
        return None
    return str(row["id"]), int(row["n"])


async def video_language(db: Any, video_id: UUID | str) -> str | None:
    """The video's detected language: each vector carries it, for the language filter."""
    lang = await db.fetchval("SELECT detected_language FROM videos WHERE id = $1", uid(video_id))
    return None if lang is None else str(lang)


async def _fingerprint(
    db: Any, video_id: UUID | str, active: tuple[str, int] | None, *, missing: bool = False
) -> str:
    fields: dict[str, Any] = {
        "transcript": active[0] if active else None,
        "segments": active[1] if active else 0,
        "language": await video_language(db, video_id),
        "model": embed_model(),
        "v": INDEX_VERSION,
    }
    if missing:
        fields["stored"] = False
    return canonical_hash(fields)


async def _missing_vectors(db: Any, video_id: UUID | str, active: tuple[str, int] | None) -> bool:
    """Search reads Postgres and the video has segments but no vectors there (a new home
    after a switchover, a video indexed into Chroma and not copied)."""
    if active is None or active[1] == 0:
        return False
    state = await store_state(db)
    if not (state.read_postgres and state.ready):
        return False
    return not await PgVectorStore(db, embed_model()).has_vectors(video_id)


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: the active transcript, its size, the language, the model and the format;
    and, once search reads Postgres, whether the video has vectors there."""
    if await load_video(db, subject.id) is None:
        return None
    active = await active_transcript(db, subject.id)
    missing = await _missing_vectors(db, subject.id, active)
    return await _fingerprint(db, subject.id, active, missing=missing)


_log = get_logger()


@dataclass(slots=True)
class _Timings:
    """Seconds a job spent: embedding, writing (the write lock held), waiting for the
    write lock, reading the video's ids."""

    embed: float = 0.0
    write: float = 0.0
    write_wait: float = 0.0
    read: float = 0.0

    def as_dict(self) -> dict[str, float]:
        return {
            "embed_sec": round(self.embed, 2),
            "write_sec": round(self.write, 2),
            "write_wait_sec": round(self.write_wait, 2),
            "read_sec": round(self.read, 2),
        }


async def _timed_write(t: _Timings, fn: Callable[..., Any], *args: Any, **kwargs: Any) -> Any:
    ran = 0.0

    def call() -> Any:
        nonlocal ran
        began = time.perf_counter()
        try:
            return fn(*args, **kwargs)
        finally:
            ran = time.perf_counter() - began

    began = time.perf_counter()
    try:
        return await gate.write(call)
    finally:
        t.write += ran
        t.write_wait += max(0.0, time.perf_counter() - began - ran)


async def _index_for_job() -> SemanticIndex:
    """The index: under the write lock only to open it (the first job; opening may write
    the collection's settings and apply Chroma's log), a lookup after."""
    if collection_open():
        return _open_index()
    return await gate.write(_open_index)


def _open_index() -> SemanticIndex:
    try:
        return _index_factory()
    except SemanticUnavailable as exc:
        raise StageFailure("search_unavailable", str(exc), retryable=False) from exc


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    state = await store_state(db)
    index = await _index_for_job() if state.write_chroma else None
    if index is None and not state.write_postgres:
        raise StageFailure(
            "search_unavailable",
            "semantic search is on Postgres and pgvector is not installed",
            retryable=False,
        )
    embedder = index.embedder if index is not None else _embedder_factory()
    model = embedder.model_name
    pg = PgVectorStore(db, model) if state.write_postgres else None
    active = await active_transcript(db, v.id)
    if active is None:
        if index is not None:
            await gate.write(index.delete_video, v.id)
        if pg is not None:
            await pg.delete_video(v.id)
        return StageResult(
            output_hash=canonical_hash({"transcript": None, "model": model}),
            summary={"segments": 0, "reason": "no active transcript", "model": model},
        )
    transcript_id, _ = active
    rows = await db.fetch(_SEGMENTS_SQL, transcript_id)
    segments = [
        IndexedSegment(
            segment_id=int(r["id"]),
            transcript_id=UUID(transcript_id),
            seq=int(r["seq"]),
            start_sec=float(r["start_sec"]),
            end_sec=float(r["end_sec"]),
            text=str(r["text"] or ""),
        )
        for r in rows
    ]
    language = await video_language(db, v.id)
    size = batch_size()
    total = len(segments)
    await ctx.progress("embed", 0, total, force=True)
    written = 0
    stale_count = 0
    t = _Timings()
    try:
        others: list[Any] = []
        if index is not None:
            began = time.perf_counter()
            # the stale ids come from the database, not from Chroma: asking Chroma for
            # the video's ids scans the collection's metadata (about a second on 12
            # million vectors, serialised with the writes), and a delete of ids it does
            # not have costs nothing
            others = await db.fetch(_OTHER_SEGMENTS_SQL, uid(v.id), transcript_id)
            t.read += time.perf_counter() - began
        for start in range(0, total, size):
            await ctx.check_cancel()
            began = time.perf_counter()
            chunk, vectors = await asyncio.to_thread(
                embed_segments, embedder, segments[start : start + size]
            )
            t.embed += time.perf_counter() - began
            done = 0
            if pg is not None:
                began = time.perf_counter()
                done = await pg.write_vectors(
                    video_id=v.id,
                    library_id=v.library_id,
                    chunk=chunk,
                    vectors=vectors,
                    language=language,
                )
                t.write += time.perf_counter() - began
            if index is not None:
                done = await _timed_write(
                    t,
                    index.write_vectors,
                    video_id=v.id,
                    library_id=v.library_id,
                    chunk=chunk,
                    vectors=vectors,
                    language=language,
                )
            written += done
            await ctx.progress("embed", min(start + size, total), total)
        if index is not None:
            stale = {embed_id_for(UUID(str(r["transcript_id"])), int(r["seq"])) for r in others}
            # blank segments are not embedded: a vector of one (an edit blanked it) is stale
            stale |= {embed_id_for(s.transcript_id, s.seq) for s in segments if not s.text.strip()}
            if stale:
                await _timed_write(t, index.delete_ids, sorted(stale))
            stale_count = len(stale)
        if pg is not None:
            # every vector of the video but its active transcript's non-blank segments
            began = time.perf_counter()
            keep = [s.segment_id for s in segments if s.text.strip()]
            dropped = await pg.keep_only(v.id, keep)
            t.write += time.perf_counter() - began
            stale_count = stale_count or dropped
    except SemanticUnavailable as exc:
        raise StageFailure("search_unavailable", str(exc), retryable=False) from exc
    except ValueError as exc:  # a model whose vectors segment_embeddings cannot hold
        raise StageFailure("embedding_dimensions", str(exc), retryable=False) from exc
    _log.info(
        "index_timings",
        video_id=str(v.id),
        segments=written,
        stale=stale_count,
        store=state.store,
        **t.as_dict(),
    )
    await ctx.progress("embed", total, total, force=True)
    return StageResult(
        output_hash=canonical_hash(
            {
                "transcript": transcript_id,
                "segments": [[s.segment_id, s.seq, s.text] for s in segments],
                "model": model,
                "v": INDEX_VERSION,
            }
        ),
        # the video as it is now, stored (a fingerprint that said "not stored" is answered)
        input_hash=await _fingerprint(db, v.id, active),
        summary={
            "transcript_id": transcript_id,
            "segments": written,
            "model": model,
            "stale_ids": stale_count,
            "store": state.store,
            **t.as_dict(),
        },
    )


def unavailable() -> str | None:
    """Why this worker cannot index (the optional ``search`` extra is missing), or None."""
    from ...search.semantic import search_available

    if search_available():
        return None
    return "chromadb / sentence-transformers are not installed (the search extra)"


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint, unavailable=unavailable)
