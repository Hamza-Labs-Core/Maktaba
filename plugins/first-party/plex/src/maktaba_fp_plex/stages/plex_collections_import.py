"""``plex_collections_import`` (Epic 56 story 56.07, moved in story 54.35): Plex's own
collections, read into Maktaba after each index."""

from __future__ import annotations

from typing import Any

from maktaba_pipeline.engine.graph import StageResult
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.extensions.plex.collections_import import run_collections_import

from ._service import SERVICE, door

__all__ = ["IMPL"]


async def run(db: Any, job: Any, ctx: Any) -> StageResult:
    with door(db, ctx):
        return await run_collections_import(db, job, ctx, SERVICE)


# no input fingerprint: every run looks at Plex (the state is there, not here)
IMPL = StageImpl(run=run, input_fingerprint=None, output_fingerprint=None)
