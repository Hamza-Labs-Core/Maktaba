"""``video_signals`` rows of the stages that read a video's picture and sound, as fixed texts.

``thumb_hash``, ``fingerprint`` and ``style_sample`` each write their own columns of the
video's row and leave the others alone. In the core they shared
:func:`maktaba_pipeline.engine.stages._common.upsert_signals`, which builds its statement
from the columns it is given; the plugin API runs only statements registered from constant
texts (story 54.24), so each stage has its own here, the same statement it built.
"""

from __future__ import annotations

from typing import Any
from uuid import UUID

from maktaba_pipeline.engine.sql import now, to_ts, uid

__all__ = ["upsert_fingerprint", "upsert_style", "upsert_thumb"]


async def upsert_thumb(db: Any, video_id: UUID | str, *, dhash: str | None, version: int) -> None:
    await db.execute(
        "INSERT INTO video_signals (video_id, thumb_dhash, thumb_version, updated_at)"
        " VALUES ($1, $2, $3, $4) ON CONFLICT (video_id) DO UPDATE SET"
        " thumb_dhash = EXCLUDED.thumb_dhash, thumb_version = EXCLUDED.thumb_version,"
        " updated_at = EXCLUDED.updated_at",
        uid(video_id),
        dhash,
        version,
        to_ts(db, now()),
    )


async def upsert_fingerprint(
    db: Any,
    video_id: UUID | str,
    *,
    head: str | None,
    tail: str | None,
    frames: str,
    version: int,
    partial: bool,
) -> None:
    await db.execute(
        "INSERT INTO video_signals (video_id, audio_head, audio_tail, frame_hashes, fp_version,"
        " fp_partial, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7)"
        " ON CONFLICT (video_id) DO UPDATE SET audio_head = EXCLUDED.audio_head,"
        " audio_tail = EXCLUDED.audio_tail, frame_hashes = EXCLUDED.frame_hashes,"
        " fp_version = EXCLUDED.fp_version, fp_partial = EXCLUDED.fp_partial,"
        " updated_at = EXCLUDED.updated_at",
        uid(video_id),
        head,
        tail,
        frames,
        version,
        partial,
        to_ts(db, now()),
    )


async def upsert_style(db: Any, video_id: UUID | str, *, metrics: str, version: int) -> None:
    await db.execute(
        "INSERT INTO video_signals (video_id, style_metrics, style_version, updated_at)"
        " VALUES ($1, $2, $3, $4) ON CONFLICT (video_id) DO UPDATE SET"
        " style_metrics = EXCLUDED.style_metrics, style_version = EXCLUDED.style_version,"
        " updated_at = EXCLUDED.updated_at",
        uid(video_id),
        metrics,
        version,
        to_ts(db, now()),
    )
