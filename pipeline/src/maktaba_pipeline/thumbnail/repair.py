"""Posters the home cannot serve: paths another machine's thumbnail cache wrote.

Before the ``thumbnail`` stage was home-only (Epic 31 R3.4), a node could claim it and
record its own cache path (``C:\\Users\\...\\.maktaba\\cache\\thumbnails\\...``) in
``videos.poster_path`` / ``sprite_path``. The home runs :func:`clear_foreign_thumbnails`
at its start: a row whose poster or sprite is in *another* machine's thumbnail cache (a
``.maktaba/cache/thumbnails`` folder that is not the home's) or spelled for another OS
(a drive path on the Mac) gets both columns cleared and its ``thumbnail`` stage enqueued
again, so the home makes them. Whether a file exists is not asked: a share that is not
mounted at that moment must not look like thousands of foreign posters. Idempotent: a
cleared row no longer matches, a row the home made is under its cache, so a second run
changes nothing. Not a migration: which paths are foreign depends on the machine it
runs on.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from ..log import get_logger
from .generator import thumbnail_dir_for

__all__ = ["ForeignThumbnails", "clear_foreign_thumbnails", "is_foreign"]

_log = get_logger()

_SELECT = (
    "SELECT id, poster_path, sprite_path FROM videos"
    " WHERE poster_path IS NOT NULL OR sprite_path IS NOT NULL"
)
_CLEAR = "UPDATE videos SET poster_path = NULL, sprite_path = NULL WHERE id = $1"


@dataclass
class ForeignThumbnails:
    checked: int = 0
    cleared: int = 0
    enqueued: int = 0
    samples: list[str] = field(default_factory=list)


def _norm(p: str) -> str:
    return p.replace("\\", "/").rstrip("/").lower()


_CACHE_MARK = "/.maktaba/cache/thumbnails/"


def is_foreign(value: str, cache: str, system: str | None = None) -> bool:
    """``value`` was written by another machine's thumbnail stage."""
    from ..cluster.share import foreign_path
    from ..sharepath.canonical import parse_canonical

    v = _norm(value)
    if v == cache or v.startswith(cache + "/") or parse_canonical(value) is not None:
        return False
    return _CACHE_MARK in v + "/" or foreign_path(value, system) is not None


async def clear_foreign_thumbnails(
    db: Any,
    *,
    cache_root: str | os.PathLike[str] | None = None,
    system: str | None = None,
    apply: bool = True,
    enqueue: bool = True,
) -> ForeignThumbnails:
    """Clear the poster / sprite paths this machine (the home) cannot serve and ask for
    the ``thumbnail`` stage again; ``apply=False`` only counts them."""
    cache = _norm(str(Path(thumbnail_dir_for("x", root=cache_root)).parent))
    out = ForeignThumbnails()
    rows = await db.fetch(_SELECT)
    for row in rows:
        out.checked += 1
        values = [str(v) for v in (row["poster_path"], row["sprite_path"]) if v is not None]
        if not any(is_foreign(v, cache, system) for v in values):
            continue
        out.cleared += 1
        if len(out.samples) < 5:
            out.samples.append(values[0])
        if not apply:
            continue
        await db.execute(_CLEAR, row["id"])
        if enqueue:
            from ..engine.enqueue import enqueue_job

            res = await enqueue_job(
                db,
                subject_kind="video",
                subject_id=str(row["id"]),
                stage="thumbnail",
                force=True,
                reason={"why": "poster made on another machine", "by": "home"},
            )
            out.enqueued += int(res.enqueued)
    if out.cleared:
        _log.info(
            "thumbnail_foreign_paths_cleared",
            cleared=out.cleared,
            enqueued=out.enqueued,
            checked=out.checked,
            applied=apply,
            samples=out.samples,
        )
    return out
