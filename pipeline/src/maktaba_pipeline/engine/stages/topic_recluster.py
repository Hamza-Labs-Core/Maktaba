"""``topic_recluster``: a library's topics from its videos' vectors (Story 9.9 AC-1, AC-2).

Library-level and debounced: every ``topic_assign`` queues it, so it runs once a batch of
videos has been transcribed, not once per video. Clusters the library's
``video_embeddings`` (of the current embedding model) with
:func:`~maktaba_pipeline.library_mgmt.topics.kmeans_fit`, K =
:func:`~maktaba_pipeline.library_mgmt.topics.pick_k` (``sqrt(N)/2`` capped at 32, or the
library's ``topic_clusters``), and rewrites ``library_topics`` and every video's
``video_topics`` in one transaction. Each topic is named after the video closest to its
centroid (its title, cut to 64 characters): a user can rename it.

Below :data:`~maktaba_pipeline.library_mgmt.topics.MIN_VIDEOS_FOR_TOPICS` vectors, or with
``auto_tag_topics`` off, the library's topics are removed. Needs numpy (the ``search``
extra).
"""

from __future__ import annotations

import asyncio
import importlib.util
from typing import Any

from ...db.jobs import Job
from ...library_mgmt.topics import (
    MIN_VIDEOS_FOR_TOPICS,
    TopicAssignment,
    TopicModel,
    assign_all,
    kmeans_fit,
    pick_k,
)
from ...search.semantic import embed_model
from ..context import StageContext
from ..graph import TOPIC_RECLUSTER_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..sql import now, to_ts, uid
from ._analysis import library_setting, pack_vec, unpack_vec
from .registry import StageImpl

__all__ = ["IMPL", "TOPIC_RECLUSTER_VERSION"]

_LABEL_CHARS = 64


async def _settings(db: Any, library_id: Any) -> tuple[bool, int | None]:
    enabled = bool(await library_setting(db, library_id, "auto_tag_topics"))
    raw = await library_setting(db, library_id, "topic_clusters")
    try:
        override = int(raw) if raw is not None else None
    except (TypeError, ValueError):
        override = None
    return enabled, override


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    if await db.fetchval("SELECT 1 FROM libraries WHERE id = $1", uid(subject.id)) is None:
        return None
    row = await db.fetchrow(
        "SELECT COUNT(*) AS n, MAX(updated_at) AS at FROM video_embeddings"
        " WHERE library_id = $1 AND model = $2",
        uid(subject.id),
        embed_model(),
    )
    enabled, override = await _settings(db, subject.id)
    return canonical_hash(
        {
            "n": int(row["n"] or 0),
            "at": None if row["at"] is None else str(row["at"]),
            "model": embed_model(),
            "enabled": enabled,
            "k": override,
            "v": TOPIC_RECLUSTER_VERSION,
        }
    )


def _fit(
    vectors: list[list[float]], k: int
) -> tuple[TopicModel, list[list[TopicAssignment]], list[int]]:
    model = kmeans_fit(vectors, k)
    rows, nearest = assign_all(vectors, model)
    return model, rows, nearest


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id = job.subject.id
    model_name = embed_model()
    enabled, override = await _settings(db, library_id)
    rows = await db.fetch(
        "SELECT e.video_id, e.dim, e.vec, COALESCE(v.title, v.filename) AS title"
        " FROM video_embeddings e JOIN videos v ON v.id = e.video_id"
        " WHERE e.library_id = $1 AND e.model = $2 AND v.deleted_at IS NULL"
        " ORDER BY e.video_id",
        uid(library_id),
        model_name,
    )
    n = len(rows)
    if not enabled or n < MIN_VIDEOS_FOR_TOPICS:
        async with db.transaction():
            await db.execute("DELETE FROM library_topics WHERE library_id = $1", uid(library_id))
        reason = (
            "auto_tag_topics is off for this library"
            if not enabled
            else f"{n} transcribed videos; topics start at {MIN_VIDEOS_FOR_TOPICS}"
        )
        return StageResult(
            output_hash=canonical_hash({"topics": None}),
            summary={"topics": 0, "videos": n, "reason": reason},
        )
    vectors = [unpack_vec(r["vec"], int(r["dim"])) for r in rows]
    k = pick_k(n, override)
    await ctx.progress("cluster", 0, 1, force=True)
    model, assignments, nearest = await asyncio.to_thread(_fit, vectors, k)
    await ctx.check_cancel()
    labels = [
        " ".join(str(rows[i]["title"] or "").split())[:_LABEL_CHARS] or f"topic {t + 1}"
        for t, i in enumerate(nearest)
    ]
    ts = to_ts(db, now())
    async with db.transaction():
        # video_topics rows go with their topics (ON DELETE CASCADE)
        await db.execute("DELETE FROM library_topics WHERE library_id = $1", uid(library_id))
        await db.executemany(
            "INSERT INTO library_topics (library_id, topic_id, label, centroid_vec,"
            " video_count, computed_at) VALUES ($1, $2, $3, $4, $5, $6)",
            [
                (uid(library_id), t, labels[t], pack_vec(c), model.video_count[t], ts)
                for t, c in enumerate(model.centroids)
            ],
        )
        await db.executemany(
            "INSERT INTO video_topics (video_id, library_id, topic_id, score)"
            " VALUES ($1, $2, $3, $4)",
            [
                (uid(str(r["video_id"])), uid(library_id), a.topic_id, float(a.score))
                for r, picks in zip(rows, assignments, strict=True)
                for a in picks
            ],
        )
    await ctx.progress("cluster", 1, 1, force=True)
    return StageResult(
        output_hash=canonical_hash({"k": model.k, "labels": labels, "counts": model.video_count}),
        summary={"topics": model.k, "videos": n, "model": model_name, "labels": labels[:8]},
    )


def unavailable() -> str | None:
    """Why this worker cannot cluster (numpy, from the ``search`` extra, is missing)."""
    if importlib.util.find_spec("numpy") is None:
        return "numpy is not installed (the search extra)"
    return None


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint, unavailable=unavailable)
