"""``chapter_infer``: chapters at a video's topic shifts, and its mean vector (Story 9.18).

Runs after ``index``. Reads the video's segment vectors back out of the semantic index
(Chroma, no embedding model loaded), then:

* stores their normalised mean in ``video_embeddings``: what ``topic_assign`` and
  ``topic_recluster`` cluster (slot 0120);
* unless the library turned ``chapter_inference`` off, or the video already has chapters
  of its own (``embedded`` or ``manual``: those win, plan 08-12), replaces its
  ``source='inferred'`` chapters with :func:`~maktaba_pipeline.library_mgmt.chapter.
  infer_topic_chapters` spans, each titled with its own words
  (:func:`~maktaba_pipeline.library_mgmt.chapter.extractive_title`; empty when none fits).
  A video with no shift strong enough gets no inferred chapters (one chapter spanning
  the whole video says nothing).

The input fingerprint is the active transcript, its size, the embedding model, the
setting, whether authored chapters exist and :data:`CHAPTER_VERSION`. Only vectors of
the active transcript count: when ``index`` has not caught up with a new transcript yet,
the stage waits for it (``waiting_inputs``) rather than chaptering the old one.
"""

from __future__ import annotations

import asyncio
import hashlib
import importlib
from collections.abc import Callable
from typing import Any

from ...db.jobs import Job
from ...library_mgmt.chapter import Segment, extractive_title, infer_topic_chapters
from ...search.semantic import (
    SemanticUnavailable,
    StoredVector,
    VectorCollection,
    embed_model,
    open_collection,
    video_vectors,
)
from ..context import StageContext, StageFailure
from ..graph import CHAPTER_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..sql import dumps, uid
from ._analysis import VideoEmbedding, drop_embedding, library_setting, store_embedding
from ._common import load_video, require_video
from .index import active_transcript, unavailable
from .registry import StageImpl

__all__ = [
    "CHAPTER_VERSION",
    "IMPL",
    "reset_collection_factory",
    "set_collection_factory",
]

_AUTHORED_SQL = (
    "SELECT COUNT(*) FROM chapters WHERE video_id = $1 AND source IN ('embedded', 'manual')"
)


def _shared_collection() -> VectorCollection:
    """The index's own persistent collection (one Chroma client per process)."""
    collection: VectorCollection = open_collection(embed_model())
    return collection


#: the vector store the stage reads; tests swap in a fake (``set_collection_factory``)
_collection_factory: Callable[[], VectorCollection] = _shared_collection


def set_collection_factory(factory: Callable[[], VectorCollection]) -> None:
    global _collection_factory
    _collection_factory = factory


def reset_collection_factory() -> None:
    global _collection_factory
    _collection_factory = _shared_collection


async def _enabled(db: Any, library_id: Any) -> bool:
    return bool(await library_setting(db, library_id, "chapter_inference"))


async def _authored(db: Any, video_id: Any) -> int:
    return int(await db.fetchval(_AUTHORED_SQL, uid(video_id)) or 0)


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: the active transcript, the model, the setting and authored chapters."""
    v = await load_video(db, subject.id)
    if v is None:
        return None
    active = await active_transcript(db, v.id)
    return canonical_hash(
        {
            "transcript": active[0] if active else None,
            "segments": active[1] if active else 0,
            "model": embed_model(),
            "enabled": await _enabled(db, v.library_id),
            "authored": await _authored(db, v.id),
            "v": CHAPTER_VERSION,
        }
    )


def _read_vectors(video_id: Any) -> list[StoredVector]:
    try:
        return video_vectors(_collection_factory(), video_id)
    except SemanticUnavailable as exc:
        raise StageFailure("search_unavailable", str(exc), retryable=False) from exc


def _mean(vectors: list[StoredVector]) -> list[float]:
    """The normalised mean vector (numpy: the stage needs the search extra anyway)."""
    np: Any = importlib.import_module("numpy")
    m = np.asarray([v.embedding for v in vectors], dtype=np.float64).sum(axis=0)
    norm = float(np.linalg.norm(m))
    return [float(x) for x in (m / norm if norm > 0.0 else m)]


def _chapters(vectors: list[StoredVector]) -> list[dict[str, Any]]:
    segs = [Segment(v.start_sec, v.end_sec, v.embedding, v.text) for v in vectors]
    spans = infer_topic_chapters(segs)
    if len(spans) < 2:
        return []
    return [
        {
            "seq": c.seq,
            "start_sec": round(c.start_sec, 3),
            "end_sec": round(c.end_sec, 3),
            "title": extractive_title(c, segs) or "",
        }
        for c in spans
    ]


async def _replace_inferred(db: Any, video_id: Any, chapters: list[dict[str, Any]]) -> None:
    async with db.transaction():
        await db.execute(
            "DELETE FROM chapters WHERE video_id = $1 AND source = 'inferred'", uid(video_id)
        )
        await db.executemany(
            "INSERT INTO chapters (video_id, seq, start_sec, end_sec, title, source, metadata)"
            " VALUES ($1, $2, $3, $4, $5, 'inferred', $6)",
            [
                (
                    uid(video_id),
                    c["seq"],
                    c["start_sec"],
                    c["end_sec"],
                    c["title"],
                    dumps({"title_source": "extractive" if c["title"] else "none"}),
                )
                for c in chapters
            ],
        )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    active = await active_transcript(db, v.id)
    model = embed_model()
    if active is None:
        await _replace_inferred(db, v.id, [])
        await drop_embedding(db, v.id)
        return StageResult(
            output_hash=canonical_hash({"transcript": None}),
            summary={"chapters": 0, "reason": "no active transcript"},
        )
    transcript_id, _ = active
    await ctx.progress("read vectors", 0, 1, force=True)
    vectors = [
        vec
        for vec in await asyncio.to_thread(_read_vectors, v.id)
        if vec.transcript_id == transcript_id
    ]
    if not vectors:
        # the index has not written this transcript's vectors yet (or every segment was
        # blank): index's own success queues this stage again once it has
        return StageResult(
            output_hash=canonical_hash({"transcript": transcript_id, "vectors": 0}),
            outcome="waiting_inputs",
            summary={"chapters": 0, "reason": "no vectors for the active transcript yet"},
        )
    mean = _mean(vectors)
    await store_embedding(
        db,
        VideoEmbedding(
            video_id=v.id,
            library_id=v.library_id,
            transcript_id=transcript_id,
            model=model,
            segments=len(vectors),
            vec=mean,
        ),
    )
    reason: str | None = None
    chapters: list[dict[str, Any]] = []
    if not await _enabled(db, v.library_id):
        reason = "chapter_inference is off for this library"
    elif await _authored(db, v.id):
        reason = "the video has embedded or manual chapters"
    else:
        await ctx.check_cancel()
        chapters = await asyncio.to_thread(_chapters, vectors)
    await _replace_inferred(db, v.id, chapters)
    await ctx.progress("read vectors", 1, 1, force=True)
    embedding_id = hashlib.sha256(f"{transcript_id}:{model}:{len(vectors)}".encode()).hexdigest()[
        :16
    ]
    return StageResult(
        output_hash=canonical_hash(
            {
                "chapters": [[c["start_sec"], c["end_sec"], c["title"]] for c in chapters],
                "embedding": embedding_id,
            }
        ),
        summary={
            "chapters": len(chapters),
            "segments": len(vectors),
            "model": model,
            **({"reason": reason} if reason else {}),
        },
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint, unavailable=unavailable)
