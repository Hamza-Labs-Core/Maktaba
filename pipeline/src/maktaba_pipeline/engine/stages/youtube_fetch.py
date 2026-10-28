"""``youtube_fetch`` (Epic 47, story 47.9): download a library's new YouTube uploads.

Subject: a library. Pool ``network_yt`` (size 1, shared with the listing and the back
catalogue: one stream of requests per home). Queued by ``youtube_sync`` at priority 120
when it marks a new upload ``wanted``. One job drains the library's new items, newest
first; the work is :func:`._youtube_drain.drain`.
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
    return await drain(db, job, ctx, backfill=False)


IMPL = StageImpl(run=run, unavailable=unavailable)
