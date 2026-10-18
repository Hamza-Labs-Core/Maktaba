"""``index``: a video's active transcript as vectors for semantic search, one job per video.

Reads the video's active ``transcripts`` row (the newest when several audio tracks have
one) and its ``transcript_segments``, embeds every segment with the configured model
(:func:`maktaba_pipeline.search.semantic.embed_model`, default
``intfloat/multilingual-e5-small``, ``"passage: "`` prefix, batched, on Apple's GPU when
there is one) and replaces the video's vectors in the persistent Chroma collection
(``MAKTABA_CHROMA_DIR``). The video's previous vectors are deleted first, so a
re-transcription never leaves the old transcript searchable.

The input fingerprint is the active transcript, its segment count, the model and
:data:`~maktaba_pipeline.search.semantic.INDEX_VERSION`: a new transcript, a changed
model or a new index format indexes the video again; anything else is skipped.

A video without an active transcript has its vectors removed and succeeds (there is
nothing to search). Segments are embedded and written in batches
(``MAKTABA_EMBED_BATCH``, default 64), each in a worker thread, with progress and a cancel
check between batches.
"""

from __future__ import annotations

import asyncio
from collections.abc import Callable
from typing import Any
from uuid import UUID

from ...db.jobs import Job
from ...search.semantic import (
    INDEX_VERSION,
    IndexedSegment,
    SemanticIndex,
    SemanticUnavailable,
    batch_size,
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


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: the active transcript, its size, the model and the index format."""
    if await load_video(db, subject.id) is None:
        return None
    active = await active_transcript(db, subject.id)
    return canonical_hash(
        {
            "transcript": active[0] if active else None,
            "segments": active[1] if active else 0,
            "model": embed_model(),
            "v": INDEX_VERSION,
        }
    )


def _open_index() -> SemanticIndex:
    try:
        return _index_factory()
    except SemanticUnavailable as exc:
        raise StageFailure("search_unavailable", str(exc), retryable=False) from exc


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    index = await asyncio.to_thread(_open_index)
    model = index.embedder.model_name
    active = await active_transcript(db, v.id)
    if active is None:
        await asyncio.to_thread(index.delete_video, v.id)
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
    size = batch_size()
    total = len(segments)
    await ctx.progress("embed", 0, total, force=True)
    written = 0
    try:
        await asyncio.to_thread(index.delete_video, v.id)
        for start in range(0, total, size):
            await ctx.check_cancel()
            written += await asyncio.to_thread(
                index.upsert_segments,
                video_id=v.id,
                library_id=v.library_id,
                segments=segments[start : start + size],
            )
            await ctx.progress("embed", min(start + size, total), total)
    except SemanticUnavailable as exc:
        raise StageFailure("search_unavailable", str(exc), retryable=False) from exc
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
        summary={"transcript_id": transcript_id, "segments": written, "model": model},
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
