"""``dizquetv_show`` (issue #827): one dizqueTV custom show per season, its programmes built
on the home by the service door from the season's Plex records."""

from __future__ import annotations

from typing import Any

from maktaba_pipeline.engine.graph import StageResult, Subject
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.extensions.dizquetv.shows import fingerprint, run_show

from ._service import SHOWS, door, write_mode

__all__ = ["IMPL"]


async def run(db: Any, job: Any, ctx: Any) -> StageResult:
    with door(db, ctx):
        return await run_show(db, job, ctx, SHOWS)


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    return await fingerprint(db, subject, await write_mode(db, "dizquetv_show"))


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint, output_fingerprint=None)
