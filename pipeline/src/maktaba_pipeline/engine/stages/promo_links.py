"""``promo_links``: each promo, trailer or teaser linked to the show it advertises.

A leftover whose title says it advertises (برومو / إعلان / تشويق / promo / trailer …,
:func:`~maktaba_pipeline.classify.clip_groups.is_promo`) is linked to:

1. the season detection already put it with (its ``extras``), score 1.0;
2. else the show its title names (:func:`~maktaba_pipeline.classify.clip_groups.link_promo`):
   the same library first (0.9), then any library (0.7); the season it names, or the
   show's first (:func:`~maktaba_pipeline.classify.clip_groups.promo_season`).

A promo that names no show (or two equally) stays a plain clip. Output: ``promo_links``
(slot 0094), replaced per library. ``clip_groups`` turns the linked ones into the
library's "إعلانات" group, and the channel planner airs a show's promos near its slots.
"""

from __future__ import annotations

from typing import Any
from uuid import UUID

from ...classify.clip_groups import is_promo, link_promo, promo_season
from ...db.jobs import Job
from ..context import StageContext
from ..graph import PROMO_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..sql import now, to_ts, uid
from ._leftovers import load_leftovers, load_shows
from .registry import StageImpl

__all__ = ["IMPL", "plan_links"]

Link = tuple[str, str, int | None, float, str]  # video, series id, season, score, evidence


async def plan_links(db: Any, library_id: UUID) -> list[Link]:
    left = await load_leftovers(db, library_id)
    shows = await load_shows(db)
    by_id = {s.series_key: s for s in shows}
    out: list[Link] = []
    for vid, it in sorted(left.items.items()):
        if not is_promo(it.title):
            continue
        with_season = left.extras_of.get(vid)
        if with_season is not None and with_season[0] in by_id:
            out.append(
                (vid, with_season[0], with_season[1], 1.0, "aired with its season (detection)")
            )
            continue
        hit = link_promo(it.title, str(library_id), shows)
        if hit is not None:
            show, score, why = hit
            out.append((vid, show.series_key, promo_season(it.title, show), score, why))
    return out


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: every live season in the catalog (a promo may name any show) and the
    library's videos."""
    seasons = await db.fetch(
        "SELECT id, membership_hash FROM series_seasons WHERE retired_at IS NULL ORDER BY id"
    )
    vids = await db.fetch(
        "SELECT id, size_bytes, mtime FROM videos WHERE library_id = $1 AND deleted_at IS NULL"
        " ORDER BY id",
        uid(subject.id),
    )
    return canonical_hash(
        {
            "seasons": [[str(r["id"]), str(r["membership_hash"])] for r in seasons],
            "videos": [[str(r["id"]), int(r["size_bytes"]), str(r["mtime"])] for r in vids],
            "v": PROMO_VERSION,
        }
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id: UUID = job.subject.id
    links = await plan_links(db, library_id)
    seasons = {
        (str(r["series_id"]), int(r["season"])): str(r["id"])
        for r in await db.fetch(
            "SELECT id, series_id, season FROM series_seasons WHERE retired_at IS NULL"
        )
    }
    stamp = to_ts(db, now())
    async with db.transaction():
        await db.execute("DELETE FROM promo_links WHERE library_id = $1", uid(library_id))
        for vid, series_id, season, score, why in links:
            await db.execute(
                "INSERT INTO promo_links (video_id, library_id, series_id, season_id, season,"
                " score, evidence, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)",
                vid,
                uid(library_id),
                series_id,
                seasons.get((series_id, season)) if season is not None else None,
                season,
                score,
                why,
                stamp,
            )
    return StageResult(
        output_hash=canonical_hash(sorted([v, s, n] for v, s, n, _, _ in links)),
        summary={
            "promos": len(links),
            "from_detection": sum(1 for x in links if x[3] >= 1.0),
            "other_library": sum(1 for x in links if x[3] < 0.9),
        },
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
