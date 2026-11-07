"""``plex_collection`` (story 54.35): each detected season written to Plex as an ordered
manual collection."""

from __future__ import annotations

from typing import Any

from maktaba_pipeline.engine.graph import StageResult, Subject
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.extensions.plex.collection import fingerprint, run_collection

from ._service import SERVICE, door

__all__ = ["IMPL"]


async def run(db: Any, job: Any, ctx: Any) -> StageResult:
    with door(db, ctx):
        return await run_collection(db, job, ctx, SERVICE)


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    row = await db.fetchrow("SELECT write_mode FROM job_stages WHERE stage = $1", "plex_collection")
    return await fingerprint(db, subject, "auto" if row is None else str(row["write_mode"]))


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint, output_fingerprint=None)
