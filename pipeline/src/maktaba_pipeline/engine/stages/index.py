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
model or a new index format indexes the video again; anything else is skipped.

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
from ...search.semantic import (
    INDEX_VERSION,
    IndexedSegment,
    SemanticIndex,
    SemanticUnavailable,
    batch_size,
    collection_open,
    embed_model,
)
from ..context import StageContext, StageFailure
from ..graph import StageResult, Subject
from ..hashing import canonical_hash
from ..sql import uid
from ._common import load_video, require_video
from .registry import StageImpl

__all__ = ["IMPL", "active_transcript", "reset_index_factory", "set_index_factory"]

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


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: the active transcript, its size, the language, the model and the format."""
    if await load_video(db, subject.id) is None:
        return None
    active = await active_transcript(db, subject.id)
    return canonical_hash(
        {
            "transcript": active[0] if active else None,
            "segments": active[1] if active else 0,
            "language": await video_language(db, subject.id),
            "model": embed_model(),
            "v": INDEX_VERSION,
        }
    )


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
    index = await _index_for_job()
    model = index.embedder.model_name
    active = await active_transcript(db, v.id)
    if active is None:
        await gate.write(index.delete_video, v.id)
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
    t = _Timings()
    try:
        began = time.perf_counter()
        # the stale ids come from the database, not from Chroma: asking Chroma for the
        # video's ids scans the collection's metadata (about a second on 12 million
        # vectors, serialised with the writes), and a delete of ids it does not have
        # costs nothing
        others = await db.fetch(_OTHER_SEGMENTS_SQL, uid(v.id), transcript_id)
        t.read += time.perf_counter() - began
        for start in range(0, total, size):
            await ctx.check_cancel()
            began = time.perf_counter()
            chunk, vectors = await asyncio.to_thread(
                index.embed_segments, segments[start : start + size]
            )
            t.embed += time.perf_counter() - began
            written += await _timed_write(
                t,
                index.write_vectors,
                video_id=v.id,
                library_id=v.library_id,
                chunk=chunk,
                vectors=vectors,
                language=language,
            )
            await ctx.progress("embed", min(start + size, total), total)
        stale = {embed_id_for(UUID(str(r["transcript_id"])), int(r["seq"])) for r in others}
        # blank segments are not embedded: a vector of one (an edit blanked it) is stale
        stale |= {embed_id_for(s.transcript_id, s.seq) for s in segments if not s.text.strip()}
        if stale:
            await _timed_write(t, index.delete_ids, sorted(stale))
    except SemanticUnavailable as exc:
        raise StageFailure("search_unavailable", str(exc), retryable=False) from exc
    _log.info(
        "index_timings",
        video_id=str(v.id),
        segments=written,
        stale=len(stale),
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
        summary={
            "transcript_id": transcript_id,
            "segments": written,
            "model": model,
            "stale_ids": len(stale),
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
