"""``plex_index`` (story 54.35): each library's Plex items, listed once and saved."""

from __future__ import annotations

from typing import Any

from maktaba_pipeline.engine.graph import StageResult
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.extensions.plex.index import run_index

from ._service import SERVICE, door

__all__ = ["IMPL"]


async def run(db: Any, job: Any, ctx: Any) -> StageResult:
    with door(db, ctx):
        return await run_index(db, job, ctx, SERVICE)


IMPL = StageImpl(run=run, input_fingerprint=None, output_fingerprint=None)
