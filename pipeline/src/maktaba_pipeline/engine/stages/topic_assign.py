"""``topic_assign``: a video's nearest library topics (Story 9.9 AC-3).

Runs after ``chapter_infer`` has stored the video's mean vector (``video_embeddings``).
Tags the video with its :data:`~maktaba_pipeline.library_mgmt.topics.TOP_K_PER_VIDEO`
nearest centroids of its library (``library_topics``, written by ``topic_recluster``) in
``video_topics``. A library without topics yet (younger than
:data:`~maktaba_pipeline.library_mgmt.topics.MIN_VIDEOS_FOR_TOPICS` transcribed videos,
or ``auto_tag_topics`` off) leaves the video untagged; ``topic_recluster``, queued from
here and debounced per library, tags every video when it builds the topics.

Pure SQL and arithmetic: no model, no vector store, so it runs on any worker.
"""

from __future__ import annotations

from typing import Any

from ...db.jobs import Job
from ...library_mgmt.topics import TopicModel, assign_topics
from ..context import StageContext
from ..graph import TOPIC_ASSIGN_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..sql import uid
from ._analysis import library_setting, load_embedding, unpack_vec
from ._common import load_video, require_video
from .registry import StageImpl

__all__ = ["IMPL", "TOPIC_ASSIGN_VERSION", "library_topics_signature"]


async def library_topics_signature(db: Any, library_id: Any) -> list[Any]:
    """What a change of the library's topics looks like: their count and newest build."""
    row = await db.fetchrow(
        "SELECT COUNT(*) AS n, MAX(computed_at) AS at FROM library_topics WHERE library_id = $1",
        uid(library_id),
    )
    return [int(row["n"] or 0), None if row["at"] is None else str(row["at"])]


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    v = await load_video(db, subject.id)
    if v is None:
        return None
    e = await load_embedding(db, v.id)
    return canonical_hash(
        {
            "embedding": None if e is None else [e.transcript_id, e.model, e.segments],
            "topics": await library_topics_signature(db, v.library_id),
            "enabled": bool(await library_setting(db, v.library_id, "auto_tag_topics")),
            "v": TOPIC_ASSIGN_VERSION,
        }
    )


async def _centroids(
    db: Any, library_id: Any, model: str, dim: int
) -> tuple[list[int], TopicModel]:
    rows = await db.fetch(
        "SELECT topic_id, centroid_vec FROM library_topics WHERE library_id = $1"
        " AND model = $2 ORDER BY topic_id",
        uid(library_id),
        model,
    )
    # centroids of another embedding model are never compared, whatever their dimension
    # (topics from before slot 0120 have no model: the next recluster replaces them)
    usable = [r for r in rows if len(bytes(r["centroid_vec"])) == 4 * dim]
    return (
        [int(r["topic_id"]) for r in usable],
        TopicModel(
            centroids=[unpack_vec(r["centroid_vec"], dim) for r in usable],
            video_count=[0] * len(usable),
        ),
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    v = await require_video(db, job.subject.id)
    e = await load_embedding(db, v.id)
    picks: list[tuple[int, float]] = []
    reason: str | None = None
    if e is None:
        reason = "no video vector (not transcribed and indexed yet)"
    elif not await library_setting(db, v.library_id, "auto_tag_topics"):
        reason = "auto_tag_topics is off for this library"
    else:
        ids, model = await _centroids(db, v.library_id, e.model, len(e.vec))
        if model.k == 0:
            reason = f"the library has no topics of {e.model} yet"
        picks = [(ids[a.topic_id], a.score) for a in assign_topics(e.vec, model)]
    async with db.transaction():
        await db.execute("DELETE FROM video_topics WHERE video_id = $1", uid(v.id))
        await db.executemany(
            "INSERT INTO video_topics (video_id, library_id, topic_id, score)"
            " VALUES ($1, $2, $3, $4)",
            [(uid(v.id), uid(v.library_id), tid, float(score)) for tid, score in picks],
        )
    return StageResult(
        output_hash=canonical_hash({"topics": [[t, round(s, 4)] for t, s in picks]}),
        summary={"topics": [t for t, _ in picks], **({"reason": reason} if reason else {})},
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
