"""``plex_groups`` (story 54.35): topic and clip collections in Plex, labelled, retired
when gone."""

from __future__ import annotations

from typing import Any

from maktaba_pipeline.engine.graph import StageResult, Subject
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.extensions.plex.groups import fingerprint, run_groups

from ._service import SERVICE, door

__all__ = ["IMPL"]


async def run(db: Any, job: Any, ctx: Any) -> StageResult:
    with door(db, ctx):
        return await run_groups(db, job, ctx, SERVICE)


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    row = await db.fetchrow("SELECT write_mode FROM job_stages WHERE stage = $1", "plex_groups")
    return await fingerprint(db, subject, "auto" if row is None else str(row["write_mode"]))


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint, output_fingerprint=None)
