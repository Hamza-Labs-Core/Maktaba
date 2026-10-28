"""``youtube_backfill`` (Epic 47, story 47.10): download a library's back catalogue.

The same work as ``youtube_fetch`` (:func:`._youtube_drain.drain`), as its own stage so it
has its own tile on the Queues page and pauses, resumes and cancels without touching new
uploads. Pool ``network_yt`` (shared with ``youtube_fetch``: the two never run at once),
priority 300: below new uploads (120), normal pipeline work (100) and conversion (150).

Each job takes a batch of 25 back-catalogue items, oldest first, and queues itself again
while any are left. Before each item after the first it checks whether its job or stage was
paused, or more urgent work waits for the pool (a new upload): then it stops after the item
it is on, and the next batch waits behind that work. Cancel stops the job; the items stay
``wanted``, and *Resume backfill* (a new job) carries on where it stopped.
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
