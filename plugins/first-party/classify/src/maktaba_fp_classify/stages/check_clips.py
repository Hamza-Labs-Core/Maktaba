"""``check_clips``: a language model checks what each short video is about (mediaintel
``check-clips``), in ``tv.maktaba.classify`` (Epic 54 wave 4, story 54.31).

One job asks for a whole library, ``clip_batch`` titles per question; each answer is saved
when it comes, so a job that stops loses nothing asked. In the plugin's runner the question
goes through the plugin API's ``compute.generate``, which asks this stage's
``rules.tagcheck`` server and model with the same request (§2). The library's clips, the
verdicts and the fingerprint are shared with mediaintel's import
(``engine/stages/_check_clips.py``).

Settings: ``rules.tagcheck`` (``enabled``, ``clips``, ``clip_batch``, ``clip_max_minutes`` ...).
"""

from __future__ import annotations

import json
from typing import Any
from uuid import UUID

from maktaba_pipeline.classify import tag_check as tc
from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.graph import StageResult
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.sql import now, to_ts, uid
from maktaba_pipeline.engine.stages._check_clips import (
    apply_verdict,
    clips_on,
    input_fingerprint,
    plan_clips,
)
from maktaba_pipeline.engine.stages._leftovers import library_name
from maktaba_pipeline.engine.stages._tagging import video_tags_digest
from maktaba_pipeline.engine.stages.registry import StageImpl

__all__ = ["IMPL", "MAX_UNANSWERED_IN_A_ROW"]

#: unanswered questions in a row after which the model server is taken as gone and the job
#: stops (the answers so far are kept; the retry asks the rest)
MAX_UNANSWERED_IN_A_ROW = 5


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id: UUID = job.subject.id
    cfg: tc.TagCheckConfig = await ctx.rules("tagcheck")
    lib = await library_name(db, library_id)
    if lib is None:
        return StageResult(output_hash=canonical_hash({"library": None}), summary={"gone": True})
    if not clips_on(cfg):
        return StageResult(
            output_hash=await video_tags_digest(db, library_id), summary={"enabled": False}
        )
    plan = await plan_clips(db, library_id, lib, cfg)
    counts = {"clips": plan.clips, "asked": 0, "cached": plan.carried, "applied": 0, "failed": 0}
    for vid, keep, applied in plan.to_apply:
        counts["cached"] += 1
        if await apply_verdict(db, vid, keep, applied, cfg):
            counts["applied"] += 1
    size = max(1, cfg.clip_batch)
    batches = [plan.todo[i : i + size] for i in range(0, len(plan.todo), size)]
    in_a_row = 0  # unanswered questions in a row: the model server went away
    for b, batch in enumerate(batches, 1):
        await ctx.check_cancel()
        await ctx.progress("ask", b - 1, len(batches), f"{counts['asked']} clips answered")
        items = [(lib, c.title, c.candidates) for c, _ in batch]
        try:
            answer = await tc.ask_job(db, ctx, tc.clip_prompt(items, cfg), cfg)
        except (OSError, KeyError, ValueError) as e:
            counts["failed"] += len(batch)
            in_a_row += 1
            if in_a_row >= MAX_UNANSWERED_IN_A_ROW:
                await ctx.log("warn", f"question {b}: the model is not answering ({e}); stopping")
                break
            continue
        in_a_row = 0
        got = tc.parse_clips(answer, [c.candidates for c, _ in batch])
        for i, (clip, h) in enumerate(batch, 1):
            if i not in got:
                counts["failed"] += 1  # left out of the answer: asked again next run
                continue
            counts["asked"] += 1
            await db.execute(
                "INSERT INTO clip_checks (video_id, input_hash, model, topics, applied_hash,"
                " checked_at) VALUES ($1,$2,$3,$4,NULL,$5) ON CONFLICT (video_id) DO UPDATE SET"
                " input_hash = EXCLUDED.input_hash, model = EXCLUDED.model,"
                " topics = EXCLUDED.topics, applied_hash = NULL, checked_at = EXCLUDED.checked_at",
                uid(clip.video_id),
                h,
                cfg.model,
                json.dumps(got[i]),
                to_ts(db, now()),
            )
            if await apply_verdict(db, clip.video_id, got[i], None, cfg):
                counts["applied"] += 1
    if counts["failed"]:
        # what was answered is saved; the retry asks the rest
        raise StageFailure(
            "model_unavailable",
            f"{counts['failed']} of {len(plan.todo)} clips got no answer from the model",
        )
    return StageResult(output_hash=await video_tags_digest(db, library_id), summary=counts)


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
