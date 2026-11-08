"""``dizquetv_audit`` (issue #827): is what each dizqueTV channel airs about the channel? Read
from the service door's projections of the channels and filler lists (no programme body)."""

from __future__ import annotations

from typing import Any

from maktaba_pipeline.engine.graph import StageResult
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.extensions.dizquetv.audit import fingerprint, run_audit

from ._service import AUDIT, door

__all__ = ["IMPL"]


async def run(db: Any, job: Any, ctx: Any) -> StageResult:
    with door(db, ctx):
        return await run_audit(db, job, ctx, AUDIT)


IMPL = StageImpl(run=run, input_fingerprint=fingerprint, output_fingerprint=None)
