# ruff: noqa: E501  (long SQL strings read better unwrapped)
"""Builders for the Epic 33 tests: channels with a generation-0 schedule on the real
migrated database."""

from __future__ import annotations

import uuid
from datetime import UTC, datetime
from typing import Any
from uuid import UUID

from maktaba_pipeline.engine.sql import dumps, is_pg, loads
from tests.engine.factory import make_library, make_video

#: a fixed "now" for the tests (a Monday at noon, UTC)
NOW = datetime(2026, 6, 15, 12, 0, tzinfo=UTC)


async def library_with(db: Any, n: int, *, minutes: float = 30.0, tag: str = "v") -> UUID:
    lib = await make_library(db)
    for i in range(n):
        await make_video(db, lib, title=f"{tag} {i:02d}", duration=minutes * 60.0)
    return lib


async def add_channel(
    db: Any, lib: UUID | None, number: int, *, mode: str = "shuffle", slug: str | None = None
) -> str:
    cid = str(uuid.uuid4())
    await db.execute(
        "INSERT INTO channels (id, library_id, number, name, slug, mode, mode_config)"
        " VALUES ($1, $2, $3, $4, $5, $6, $7)",
        cid,
        None if lib is None else str(lib),
        number,
        f"Channel {number}",
        slug or f"ch-{number}",
        mode,
        dumps({}),
    )
    return cid


async def set_staging(db: Any, **values: Any) -> None:
    await db.execute(
        "INSERT INTO app_settings (key, value) VALUES ('rules.tv_staging', $1)"
        " ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value",
        dumps(values),
    )


async def blocks(db: Any, channel_id: str, gen: int) -> list[tuple[str | None, str, str]]:
    """``(video, start, end)`` of a channel's blocks in one generation, in order."""
    rows = await db.fetch(
        "SELECT video_id, start_at, end_at FROM channel_programs"
        " WHERE channel_id = $1 AND sched_gen = $2 ORDER BY start_at",
        channel_id,
        gen,
    )
    return [
        (
            None if r["video_id"] is None else str(r["video_id"]),
            str(r["start_at"]),
            str(r["end_at"]),
        )
        for r in rows
    ]


async def channel_def(db: Any, channel_id: str) -> dict[str, Any]:
    from maktaba_pipeline.tvrelease.snapshot import DEF_COLUMNS, _def

    row = await db.fetchrow(
        f"SELECT {', '.join(DEF_COLUMNS)} FROM channels WHERE id = $1", channel_id
    )
    return _def(row)


def truthy(db: Any, v: Any) -> Any:
    return v if is_pg(db) else int(bool(v))


__all__ = [
    "NOW",
    "add_channel",
    "blocks",
    "channel_def",
    "library_with",
    "loads",
    "set_staging",
    "truthy",
]
