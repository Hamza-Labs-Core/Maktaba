"""Every copy of a file is tracked (Epic 47, story 47.5).

``videos`` is unique on ``(library_id, content_hash)``, so two byte-identical files of a
library are one video. Until 47.5 the scanner's upsert moved the row to whichever copy it
saw last, and the other copy was tracked by nobody. Now a second identical file at
another path is recorded in ``video_copies`` and the row keeps its own path while that
file is still there; ``dedup_scan`` reports the copies as exact duplicates.

When a row's own file is gone and one of its copies is still on disk, the copy takes its
place (:func:`promote_copy`): removing either file of a pair keeps the video. A copy whose
file is gone is forgotten. Neither happens for a path under a root that does not answer
(an unmounted share says nothing about its files), nor for a superseded row (a merged
duplicate, whose file is in the trash on purpose).

Paths are the database's form (``videos.path``); ``exists`` maps them to this machine.
"""

from __future__ import annotations

import os
from collections.abc import Callable, Sequence
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any
from uuid import UUID

from ..engine.paths import find_existing, to_local
from ..engine.sql import loads, now, to_ts, uid

__all__ = [
    "CopiesReport",
    "path_exists",
    "promote_copy",
    "reconcile_copies",
    "record_copy",
    "root_answers_locally",
]

Exists = Callable[[str], bool]


def path_exists(db_path: str) -> bool:
    """Whether a database path is a file on this machine (NFC/NFD tolerant)."""
    return os.path.exists(find_existing(to_local(db_path)))


def root_answers_locally(roots: Sequence[str], db_path: str) -> bool:
    """Whether the root ``db_path`` is under is a directory here (mounted, readable)."""
    best: str | None = None
    for r in roots:
        rr = r.rstrip("/")
        if (db_path == rr or db_path.startswith(rr + "/")) and (
            best is None or len(rr) > len(best)
        ):
            best = rr
    return best is not None and os.path.isdir(to_local(best))


async def record_copy(
    db: Any,
    video_id: UUID | str,
    path: str,
    size_bytes: int,
    mtime: datetime | None,
    *,
    seen_at: datetime | None = None,
) -> None:
    """Record (or refresh) ``path`` as a byte-identical copy of ``video_id``."""
    await db.execute(
        "INSERT INTO video_copies (video_id, path, size_bytes, mtime, seen_at)"
        " VALUES ($1, $2, $3, $4, $5)"
        " ON CONFLICT (video_id, path) DO UPDATE SET size_bytes = excluded.size_bytes,"
        " mtime = excluded.mtime, seen_at = excluded.seen_at",
        uid(video_id),
        path,
        size_bytes,
        to_ts(db, mtime),
        to_ts(db, seen_at or now()),
    )


async def promote_copy(
    db: Any, video_id: UUID | str, *, exists: Exists = path_exists
) -> str | None:
    """The row's own file is gone: its newest copy still on disk becomes its path.

    Returns the promoted path, or ``None`` (no copy on disk, or the row is superseded).
    Copies whose file is gone are forgotten on the way."""
    row = await db.fetchrow("SELECT state FROM videos WHERE id = $1", uid(video_id))
    if row is None or str(row["state"]) == "superseded":
        return None
    copies = await db.fetch(
        "SELECT path, size_bytes, mtime FROM video_copies WHERE video_id = $1"
        " ORDER BY seen_at DESC, path",
        uid(video_id),
    )
    for c in copies:
        p = str(c["path"])
        if not exists(p):
            await db.execute(
                "DELETE FROM video_copies WHERE video_id = $1 AND path = $2", uid(video_id), p
            )
            continue
        async with db.transaction():
            mtime_sql = ", mtime = $5" if c["mtime"] is not None else ""
            args: list[Any] = [uid(video_id), p, os.path.basename(p), int(c["size_bytes"])]
            if c["mtime"] is not None:
                args.append(c["mtime"])
            await db.execute(
                "UPDATE videos SET path = $2, filename = $3, size_bytes = $4,"
                f" missing_confirmed_at = NULL{mtime_sql} WHERE id = $1",
                *args,
            )
            await db.execute(
                "DELETE FROM video_copies WHERE video_id = $1 AND path = $2", uid(video_id), p
            )
        return p
    return None


@dataclass
class CopiesReport:
    promoted: list[tuple[UUID, str]] = field(default_factory=list)
    forgotten: list[tuple[UUID, str]] = field(default_factory=list)


async def reconcile_copies(
    db: Any, library_id: UUID | str, *, exists: Exists = path_exists
) -> CopiesReport:
    """After a scan: forget the copies whose file is gone and promote a copy of every
    row whose own file is gone. Paths under a root that does not answer are left."""
    report = CopiesReport()
    raw = await db.fetchval("SELECT roots FROM libraries WHERE id = $1", uid(library_id))
    roots = [str(r) for r in (loads(raw) if isinstance(raw, str) else raw or [])]
    rows = await db.fetch(
        "SELECT c.video_id AS video_id, c.path AS path, v.path AS own, v.state AS state"
        " FROM video_copies c JOIN videos v ON v.id = c.video_id"
        " WHERE v.library_id = $1 ORDER BY c.video_id",
        uid(library_id),
    )
    by_video: dict[str, list[Any]] = {}
    for r in rows:
        by_video.setdefault(str(r["video_id"]), []).append(r)
    for vid, cs in by_video.items():
        own = str(cs[0]["own"])
        for c in cs:
            p = str(c["path"])
            if root_answers_locally(roots, p) and not exists(p):
                await db.execute(
                    "DELETE FROM video_copies WHERE video_id = $1 AND path = $2", vid, p
                )
                report.forgotten.append((UUID(vid), p))
        if str(cs[0]["state"]) == "superseded":
            continue
        if root_answers_locally(roots, own) and not exists(own):
            got = await promote_copy(db, vid, exists=exists)
            if got is not None:
                report.promoted.append((UUID(vid), got))
    return report
