"""Row factories for engine tests (work on both dialects)."""

from __future__ import annotations

import hashlib
import uuid
from datetime import UTC, datetime
from typing import Any
from uuid import UUID

from maktaba_pipeline.engine.sql import dumps, is_pg, to_ts


async def make_library(db: Any, name: str | None = None) -> UUID:
    lib = uuid.uuid4()
    roots = ["/lib"] if not is_pg(db) else ["/lib"]
    await db.execute(
        "INSERT INTO libraries (id, name, roots) VALUES ($1, $2, $3)",
        str(lib),
        name or f"lib-{lib.hex[:6]}",
        roots if is_pg(db) else dumps(roots),
    )
    return lib


async def make_video(
    db: Any,
    library_id: UUID,
    *,
    filename: str | None = None,
    title: str | None = None,
    duration: float | None = 1800.0,
    size: int = 1000,
    mtime: datetime | None = None,
    path: str | None = None,
) -> UUID:
    vid = uuid.uuid4()
    fn = filename or f"{vid.hex[:8]}.mp4"
    await db.execute(
        "INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime,"
        " title, duration_sec) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)",
        str(vid),
        str(library_id),
        hashlib.sha256(vid.bytes).hexdigest(),
        path or f"/lib/{fn}",
        fn,
        size,
        to_ts(db, mtime or datetime(2024, 1, 1, tzinfo=UTC)),
        title,
        duration,
    )
    return vid


async def make_series_and_season(
    db: Any, library_id: UUID, *, name: str = "Show", season: int = 1, kind: str = "episodic"
) -> tuple[UUID, UUID]:
    sid = uuid.uuid4()
    seid = uuid.uuid4()
    await db.execute(
        "INSERT INTO series (id, library_id, name) VALUES ($1,$2,$3)",
        str(sid),
        str(library_id),
        name,
    )
    await db.execute(
        "INSERT INTO series_seasons (id, series_id, library_id, season, kind, name,"
        " membership_hash, detector_version) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)",
        str(seid),
        str(sid),
        str(library_id),
        season,
        kind,
        name,
        "h0",
        2,
    )
    return sid, seid
