"""``check_tags``: a language model checks each series' topic tags (mediaintel ``check-tags``),
in ``tv.maktaba.classify`` (Epic 54 wave 4, story 54.31).

Subject: a season. Runs after ``tag``. The model reads the series' name, its library and a
sample of its episode titles and names the topics it is about; its verdict corrects the
season's tags. The verdict is cached per input in ``tag_checks``, so a series is asked once.
In the plugin's runner the question goes through the plugin API's ``compute.generate``, which
asks this stage's ``rules.tagcheck`` server and model with the same request (§2).

Settings: ``rules.tagcheck`` (off, model server, model, sample size, damping ...).
"""

from __future__ import annotations

from typing import Any
from uuid import UUID

from maktaba_pipeline.classify import tag_check as tc
from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.graph import StageResult
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.sql import now, to_ts, uid
from maktaba_pipeline.engine.stages._check_tags import (
    apply_answer,
    input_fingerprint,
    season_inputs,
)
from maktaba_pipeline.engine.stages._tagging import season_tags_digest
from maktaba_pipeline.engine.stages.registry import StageImpl

__all__ = ["IMPL"]


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    season_id: UUID = job.subject.id
    cfg: tc.TagCheckConfig = await ctx.rules("tagcheck")
    got = await season_inputs(db, season_id)
    if got is None:
        return StageResult(output_hash=canonical_hash("retired"), summary={"checked": False})
    if not cfg.enabled:
        out = await season_tags_digest(db, season_id)
        return StageResult(output_hash=out, summary={"enabled": False})
    name, library, titles = got
    h = tc.input_hash(name, library, titles, cfg)
    row = await db.fetchrow(
        "SELECT input_hash, answer, applied_hash FROM tag_checks WHERE season_id = $1",
        uid(season_id),
    )
    asked = False
    if row is not None and row["input_hash"] == h:
        answer, applied = str(row["answer"]), row["applied_hash"]
    else:
        try:
            answer = await tc.ask_job(db, ctx, tc.prompt(name, library, titles, cfg), cfg)
        except (OSError, KeyError, ValueError) as e:
            raise StageFailure(
                "model_unavailable", f"{name[:50]}: the model did not answer ({e})"
            ) from e
        applied, asked = None, True
        await db.execute(
            "INSERT INTO tag_checks"
            " (season_id, input_hash, model, answer, applied_hash, checked_at)"
            " VALUES ($1,$2,$3,$4,NULL,$5) ON CONFLICT (season_id) DO UPDATE SET"
            " input_hash = EXCLUDED.input_hash, model = EXCLUDED.model, answer = EXCLUDED.answer,"
            " applied_hash = NULL, checked_at = EXCLUDED.checked_at",
            uid(season_id),
            h,
            cfg.model,
            answer,
            to_ts(db, now()),
        )
    corrected = await apply_answer(db, season_id, answer, applied, cfg)
    return StageResult(
        output_hash=await season_tags_digest(db, season_id),
        summary={"asked": asked, "corrected": corrected, "model": cfg.model},
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
