"""``dedup_scan`` (Epic 47, story 47.6): find a library's duplicate groups.

Subject: a library. Pool ``nas_read``. Enqueued by the admin (``POST
/api/libraries/{id}/dedup-scan``, priority 150). Payload:

* ``{"scope": "all"}``: every library, groups across libraries included (the default is
  the library alone);
* ``{"dry_run": true}``: the groups it would write are the job's ``output_summary``
  (``plan``); nothing is written.

The groups go to ``duplicate_groups`` / ``duplicate_members``
(:mod:`maktaba_pipeline.library_mgmt.duplicates`). An exact group of one library whose
``settings.dedup.auto_exact`` is on (the default) is merged at once: this job requests
``dedup_apply`` for it. Every other group waits for review on ``/queue/duplicates``.
"""

from __future__ import annotations

from typing import Any, Literal
from uuid import UUID

from ...db.jobs import Job
from ...library_mgmt.duplicates import find_groups, write_groups
from ..context import StageContext
from ..graph import EPIC47_PRIORITIES, Request, StageResult
from ..hashing import canonical_hash
from .registry import StageImpl

__all__ = ["IMPL", "run"]

#: the groups a summary lists (the counts stay exact)
PLAN_LIMIT = 200


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id = str(job.subject.id)
    payload = job.payload or {}
    scope: Literal["library", "all"] = "all" if payload.get("scope") == "all" else "library"
    dry_run = bool(payload.get("dry_run"))

    async def progress(done: int, total: int) -> None:
        await ctx.progress("full_hash", done, total)

    plan = await find_groups(
        db,
        library_id,
        scope=scope,
        to_local=ctx.local_path,
        progress=progress,
        check_cancel=ctx.check_cancel,
    )
    counts = {
        "exact": sum(1 for g in plan.groups if g.tier == "exact"),
        "same_video": sum(1 for g in plan.groups if g.tier == "same_video"),
        "known": len(plan.known),
        "bytes_hashed": plan.bytes_hashed,
        "unreadable": len(plan.unreadable),
        "collisions": len(plan.collisions),
    }
    base: dict[str, Any] = {"scope": scope, "dry_run": dry_run, **counts}
    if plan.unreadable:
        base["unreadable_paths"] = plan.unreadable[:PLAN_LIMIT]
    if plan.collisions:
        base["collision_paths"] = plan.collisions[:PLAN_LIMIT]
    if dry_run:
        return StageResult(
            output_hash=canonical_hash({"dry_run": sorted(g.key for g in plan.groups)}),
            outcome="unchanged",
            summary={**base, "plan": [g.as_json() for g in plan.groups[:PLAN_LIMIT]]},
        )
    report = await write_groups(db, plan, library_id, scope=scope)
    requests = tuple(
        Request(
            stage="dedup_apply",
            subject_kind="dup_group",
            subject_id=UUID(gid),
            reason="byte-identical copies (auto_exact)",
            payload={"auto": True},
            priority=EPIC47_PRIORITIES["dedup_apply"],
        )
        for gid in report.auto
    )
    touched = report.created or report.reopened or report.removed_stale
    return StageResult(
        output_hash=canonical_hash(sorted([*(g.key for g in plan.groups), *plan.known])),
        outcome="changed" if touched else "unchanged",
        requests=requests,
        summary={
            **base,
            "created": len(report.created),
            "reopened": len(report.reopened),
            "dismissed_kept": report.skipped_dismissed,
            "removed_stale": report.removed_stale,
            "auto_apply": len(report.auto),
        },
    )


IMPL = StageImpl(run=run)
