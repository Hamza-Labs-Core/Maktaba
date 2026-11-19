"""``index``: a video's active transcript as vectors for semantic search, one job per video.

Reads the video's active ``transcripts`` row (the newest when several audio tracks have
one) and its ``transcript_segments``, embeds every segment with the configured model
(:func:`maktaba_pipeline.search.semantic.embed_model`, default
``intfloat/multilingual-e5-small``, ``"passage: "`` prefix, batched, on Apple's GPU when
there is one) and writes the vectors to ``segment_embeddings``
(:mod:`~maktaba_pipeline.search.pgstore`, pgvector). Once the new vectors are written,
the vectors the video may still have of its other transcripts, and of segments of this
one that are blank now, are deleted, so a re-transcription never leaves the old
transcript searchable. Each write is one statement (atomic).

The input fingerprint is the active transcript, its segment count, the model and
:data:`~maktaba_pipeline.search.semantic.INDEX_VERSION`: a new transcript, a changed
model or a new index format indexes the video again; anything else is skipped. A video
with segments and no row in ``segment_embeddings`` counts as not indexed (a new home
after a switchover: the machines' copies hold no vectors; a video whose vectors were only
in Chroma, retired in story 43.5) and is indexed again. Without pgvector (SQLite, or a
Postgres without the extension) the stage fails ``search_unavailable``: search is then
full-text only.

A video without an active transcript has its vectors removed and succeeds (there is
nothing to search). Segments are embedded and written in batches
(``MAKTABA_EMBED_BATCH``, default 64), each embedded in a worker thread, with progress
and a cancel check between batches. Each job logs where its time went
(``index_timings``: the embedding and the writes), also in its summary.
"""

from __future__ import annotations

import asyncio
import time
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any
from uuid import UUID

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.graph import StageResult, Subject
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.sql import uid
from maktaba_pipeline.engine.stages._common import load_video, require_video
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.log import get_logger
from maktaba_pipeline.search.pgstore import PgVectorStore, store_state
from maktaba_pipeline.search.semantic import (
    INDEX_VERSION,
    Embedder,
    IndexedSegment,
    SemanticUnavailable,
    batch_size,
    embed_model,
    embed_segments,
    get_embedder,
)

__all__ = [
    "IMPL",
    "active_transcript",
    "reset_embedder_factory",
    "set_embedder_factory",
]

_ACTIVE_SQL = (
    "SELECT t.id AS id, COALESCE(t.revision, 0) AS revision,"
    " (SELECT COUNT(*) FROM transcript_segments s WHERE s.transcript_id = t.id) AS n"
    " FROM transcripts t WHERE t.video_id = $1 AND t.is_active"
    " ORDER BY t.created_at DESC, t.id DESC LIMIT 1"
)

_SEGMENTS_SQL = (
    "SELECT s.transcript_id AS transcript_id, s.seq AS seq FROM transcript_segments s"
    " JOIN transcripts t ON t.id = s.transcript_id WHERE t.video_id = $1 AND t.id <> $2"
)

_SEGMENTS_SQL = (
    "SELECT id, seq, start_sec, end_sec, text FROM transcript_segments"
    " WHERE transcript_id = $1 ORDER BY seq"
)

#: the embedding model; tests swap in a fake (``set_embedder_factory``)
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


async def _revision(db: Any, transcript_id: str) -> int:
    got = await db.fetchval(
        "SELECT COALESCE(revision, 0) FROM transcripts WHERE id = $1", transcript_id
    )
    return int(got or 0)


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
    # Epic 52 (story 52.9): a reviewer's fix bumps the revision; only once there is one, so
    # a transcript nobody edited keeps its fingerprint
    rev = await _revision(db, active[0]) if active else 0
    if rev:
        fields["revision"] = rev
    return canonical_hash(fields)


async def _missing_vectors(db: Any, video_id: UUID | str, active: tuple[str, int] | None) -> bool:
    """The video has segments but no vectors (a new home after a switchover, a video
    whose vectors were only in Chroma)."""
    if active is None or active[1] == 0:
        return False
    if not (await store_state(db)).ready:
        return False
    return not await PgVectorStore(db, embed_model()).has_vectors(video_id)


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: the active transcript, its size, the language, the model and the format;
    and whether the video has vectors."""
    if await load_video(db, subject.id) is None:
        return None
    active = await active_transcript(db, subject.id)
    missing = await _missing_vectors(db, subject.id, active)
    return await _fingerprint(db, subject.id, active, missing=missing)


_log = get_logger()


@dataclass(slots=True)
class _Timings:
    """Seconds a job spent: embedding, writing."""

    embed: float = 0.0
    write: float = 0.0

    def as_dict(self) -> dict[str, float]:
        return {"embed_sec": round(self.embed, 2), "write_sec": round(self.write, 2)}


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    if not (await store_state(db)).ready:
        raise StageFailure(
            "search_unavailable",
            "semantic search needs Postgres with pgvector",
            retryable=False,
        )
    embedder = _embedder_factory()
    model = embedder.model_name
    pg = PgVectorStore(db, model)
    active = await active_transcript(db, v.id)
    if active is None:
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
    # Epic 52 (story 52.9): a word fix names the sentences it changed: only those are
    # embedded again (the others' vectors stand)
    payload = getattr(job, "payload", None) or {}
    seqs = payload.get("seqs")
    only = (
        {int(s) for s in seqs}
        if isinstance(seqs, list) and payload.get("transcript_id") == transcript_id
        else None
    )
    everything = segments
    if only is not None:
        segments = [s for s in segments if s.seq in only]
    language = await video_language(db, v.id)
    size = batch_size()
    total = len(segments)
    await ctx.progress("embed", 0, total, force=True)
    written = 0
    t = _Timings()
    try:
        for start in range(0, total, size):
            await ctx.check_cancel()
            began = time.perf_counter()
            chunk, vectors = await asyncio.to_thread(
                embed_segments, embedder, segments[start : start + size]
            )
            t.embed += time.perf_counter() - began
            began = time.perf_counter()
            written += await pg.write_vectors(
                video_id=v.id,
                library_id=v.library_id,
                chunk=chunk,
                vectors=vectors,
                language=language,
            )
            t.write += time.perf_counter() - began
            await ctx.progress("embed", min(start + size, total), total)
        # every vector of the video but its active transcript's non-blank segments
        began = time.perf_counter()
        keep = [s.segment_id for s in everything if s.text.strip()]
        stale_count = await pg.keep_only(v.id, keep)
        t.write += time.perf_counter() - began
    except SemanticUnavailable as exc:
        raise StageFailure("search_unavailable", str(exc), retryable=False) from exc
    except ValueError as exc:  # a model whose vectors segment_embeddings cannot hold
        raise StageFailure("embedding_dimensions", str(exc), retryable=False) from exc
    _log.info(
        "index_timings",
        video_id=str(v.id),
        segments=written,
        stale=stale_count,
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
            **t.as_dict(),
        },
    )


def unavailable() -> str | None:
    """Why this worker cannot index (the optional ``search`` extra is missing), or None."""
    from maktaba_pipeline.search.semantic import search_available

    if search_available():
        return None
    return "sentence-transformers is not installed (the search extra)"


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint, unavailable=unavailable)
