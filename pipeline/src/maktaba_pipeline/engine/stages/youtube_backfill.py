"""``youtube_backfill`` (Epic 47, story 47.10): download a library's back catalogue.

The same work as ``youtube_fetch`` (:func:`._youtube_drain.drain`), as its own stage so it
has its own tile on the Queues page and pauses, resumes and cancels without touching new
uploads. Pool ``network_yt`` (shared with ``youtube_fetch``: the two never run at once),
priority 300: below new uploads (120), normal pipeline work (100) and conversion (150).

One item at a time (the owner's rule): each job goes through the back catalogue oldest
first until one video lands (at most 25 items: skipped and gone ones do not count), asks
``convert_mp4`` for that video and itself for the next one (graph requests), so a video
converts while the next one downloads. A new upload's ``youtube_fetch`` (120) claims the
pool before the next back-catalogue job (300). A paused stage leaves the next job waiting;
cancel stops the job and queues nothing, the items stay ``wanted``, and *Resume backfill*
(a new job) carries on where it stopped.
"""

from __future__ import annotations

from typing import Any

from ...integrations.youtube_download import unavailable
from ..context import StageContext
from ..graph import StageResult
from ._youtube_drain import drain
from .registry import StageImpl

__all__ = ["IMPL", "run"]


async def run(db: Any, job: Any, ctx: StageContext) -> StageResult:
    return await drain(db, job, ctx, backfill=True)


IMPL = StageImpl(run=run, unavailable=unavailable)
