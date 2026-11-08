"""``review_pages``: one HTML page per library to review its seasons by eye, in
``tv.maktaba.classify`` (Epic 54 wave 4, story 54.31).

Its own pool (``reports``, one at a time), nothing depends on it, and it reads only the
database and the small thumbnails ``thumb_small`` made (``files.read``:
``data.thumbnails``). The page goes to the reports folder (``files.write``: ``data.review``,
``~/.maktaba/cache/reviews/<library_id>.html``) and links each episode's thumbnail by a
relative path. The folder, the page's rendering and the fingerprint are the pipeline's
(``engine/stages/_review_pages.py``): the API serves the folder.
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Any
from uuid import UUID

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.context import StageContext
from maktaba_pipeline.engine.graph import StageResult
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.sql import uid
from maktaba_pipeline.engine.stages import _review_pages as rp
from maktaba_pipeline.engine.stages._review_pages import input_fingerprint
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.thumbnail import small as thumb_small

__all__ = ["IMPL"]


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id: UUID = job.subject.id
    lib = await db.fetchrow("SELECT name FROM libraries WHERE id = $1", uid(library_id))
    if lib is None:
        return StageResult(output_hash=canonical_hash({"library": None}), summary={"gone": True})
    seasons = await rp.seasons(db, library_id)
    episodes = await rp.episodes(db, library_id)
    out = rp.page_path(library_id)
    thumbs: dict[str, str] = {}
    n = 0
    for eps in episodes.values():
        for ep in eps:
            h = str(ep["content_hash"])
            p = thumb_small.small_path(h)
            if p.is_file():
                thumbs[h] = Path(os.path.relpath(p, out.parent)).as_posix()
            n += 1
    await ctx.progress("render", 1, 2, f"{len(seasons)} seasons, {n} episodes", force=True)
    page = rp.render(str(lib["name"]), seasons, episodes, thumbs)
    out.parent.mkdir(parents=True, exist_ok=True)
    tmp = out.with_suffix(".part.html")
    tmp.write_text(page, encoding="utf-8")
    tmp.replace(out)
    return StageResult(
        output_hash=canonical_hash(page),
        summary={
            "page": str(out),
            "seasons": len(seasons),
            "episodes": n,
            "with_thumbnail": len(thumbs),
        },
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
