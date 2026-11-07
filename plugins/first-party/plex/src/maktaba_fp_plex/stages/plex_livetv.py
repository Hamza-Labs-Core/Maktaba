"""``plex_livetv`` (story 54.35): Maktaba's channels in Plex Live TV (tuner, XMLTV DVR,
channel map, guide reloads), on the home only."""

from __future__ import annotations

from typing import Any

from maktaba_pipeline.engine.graph import StageResult
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.extensions.plex.livetv import run_livetv

from ._service import SERVICE, door

__all__ = ["IMPL"]


async def run(db: Any, job: Any, ctx: Any) -> StageResult:
    with door(db, ctx):
        return await run_livetv(db, job, ctx, SERVICE)


IMPL = StageImpl(run=run, input_fingerprint=None, output_fingerprint=None)
