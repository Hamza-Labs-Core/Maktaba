"""Helpers shared by the transcript analysis stages (chapter_infer, topic_assign,
topic_recluster): library settings, packed vectors and the video's mean embedding."""

from __future__ import annotations

import struct
from collections.abc import Sequence
from dataclasses import dataclass
from typing import Any
from uuid import UUID

from ...library_mgmt.config import EFFECTIVE_DEFAULTS
from ..sql import loads, now, to_ts, uid

__all__ = [
    "VideoEmbedding",
    "drop_embedding",
    "library_setting",
    "load_embedding",
    "pack_vec",
    "store_embedding",
    "unpack_vec",
]


async def library_setting(db: Any, library_id: UUID | str, key: str) -> Any:
    """``libraries.settings[key]``, else the worker default (library_mgmt.config)."""
    raw = await db.fetchval("SELECT settings FROM libraries WHERE id = $1", uid(library_id))
    settings = loads(raw) if raw is not None else None
    if isinstance(settings, dict) and settings.get(key) is not None:
        return settings[key]
    return EFFECTIVE_DEFAULTS.get(key)


def pack_vec(vec: Sequence[float]) -> bytes:
    """Little-endian float32, the layout numpy's ``tobytes()`` gives library_topics."""
    return struct.pack(f"<{len(vec)}f", *vec)


def unpack_vec(buf: bytes, dim: int) -> list[float]:
    return list(struct.unpack(f"<{dim}f", bytes(buf)))


@dataclass(frozen=True, slots=True)
class VideoEmbedding:
    video_id: UUID
    library_id: UUID
    transcript_id: str
    model: str
    segments: int
    vec: list[float]


async def load_embedding(db: Any, video_id: UUID | str) -> VideoEmbedding | None:
    row = await db.fetchrow(
        "SELECT video_id, library_id, transcript_id, model, dim, segments, vec"
        " FROM video_embeddings WHERE video_id = $1",
        uid(video_id),
    )
    if row is None:
        return None
    return VideoEmbedding(
        video_id=UUID(str(row["video_id"])),
        library_id=UUID(str(row["library_id"])),
        transcript_id=str(row["transcript_id"]),
        model=str(row["model"]),
        segments=int(row["segments"]),
        vec=unpack_vec(row["vec"], int(row["dim"])),
    )


async def store_embedding(db: Any, e: VideoEmbedding) -> None:
    await db.execute(
        "INSERT INTO video_embeddings (video_id, library_id, transcript_id, model, dim,"
        " segments, vec, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)"
        " ON CONFLICT (video_id) DO UPDATE SET library_id = EXCLUDED.library_id,"
        " transcript_id = EXCLUDED.transcript_id, model = EXCLUDED.model,"
        " dim = EXCLUDED.dim, segments = EXCLUDED.segments, vec = EXCLUDED.vec,"
        " updated_at = EXCLUDED.updated_at",
        uid(e.video_id),
        uid(e.library_id),
        uid(e.transcript_id),
        e.model,
        len(e.vec),
        e.segments,
        pack_vec(e.vec),
        to_ts(db, now()),
    )


async def drop_embedding(db: Any, video_id: UUID | str) -> None:
    await db.execute("DELETE FROM video_embeddings WHERE video_id = $1", uid(video_id))
