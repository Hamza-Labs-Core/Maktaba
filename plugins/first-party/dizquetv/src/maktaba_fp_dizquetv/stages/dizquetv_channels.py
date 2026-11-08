"""``dizquetv_channels`` (issue #827): the latest channel plan written into dizqueTV; each
channel's programmes filled on the home by the service door from the stored shows."""

from __future__ import annotations

from typing import Any

from maktaba_pipeline.engine.graph import StageResult, Subject
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.extensions.dizquetv.channels import fingerprint, run_channels

from ._service import CHANNELS, door, write_mode

__all__ = ["IMPL"]


async def run(db: Any, job: Any, ctx: Any) -> StageResult:
    with door(db, ctx):
        return await run_channels(db, job, ctx, CHANNELS)


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    return await fingerprint(db, subject, await write_mode(db, "dizquetv_channels"))


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint, output_fingerprint=None)
