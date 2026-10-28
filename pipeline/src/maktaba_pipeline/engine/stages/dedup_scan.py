"""``dedup_scan`` (Epic 47, story 47.6): find duplicates, one item at a time.

Pool ``nas_read``, priority 150. The order is the queue's (spec "Pipeline order"): per
video, download -> convert -> dedup, with no library-wide barrier.

* **A video** (the item's check): queued by its ``convert_mp4`` job when that finishes,
  whatever its outcome (the graph edge ``convert_mp4 -> dedup_scan``, ``same_video``,
  ``when="always"``). It compares the video with the library's files whose conversion
  is done (their latest ``convert_mp4`` job is ``done``); a file still queued for
  conversion is no candidate, and finds this one when its own check runs. A video whose
  conversion is not done is skipped (``summary.skipped = "not_converted"``).
* **A library** ("Find duplicates", ``POST /api/libraries/{id}/dedup-scan``) fans out
  only: it queues the check of every converted video, and ``convert_mp4`` for every
  video never planned for conversion (an MP4 among them finishes at once and its edge
  queues its check). A video whose latest conversion is still queued or running is left
  to its edge; one whose conversion failed is counted, not retried.

Payload: ``{"scope": "all"}`` (the library's videos compared with every library's,
groups across libraries included; carried to the items' checks), ``{"dry_run": true}``
(``output_summary.plan``: a library's groups among its converted videos, or an item's;
nothing written, nothing queued).

The groups go to ``duplicate_groups`` / ``duplicate_members``
(:mod:`maktaba_pipeline.library_mgmt.duplicates`): two checks that see one pair at once
converge on one group. An exact group of one library whose ``settings.dedup.auto_exact``
is on (the default) is merged at once: the check requests ``dedup_apply`` for it (one
live job per group). Every other group waits for review on ``/queue/duplicates``.
"""

from __future__ import annotations

from typing import Any, Literal
from uuid import UUID

from ...db.jobs import Job
from ...library_mgmt.duplicates import conversion_states, find_groups, write_groups
from ..context import StageContext
from ..enqueue import enqueue_job
from ..graph import EPIC47_PRIORITIES, Request, StageResult
from ..hashing import canonical_hash
from .registry import StageImpl

__all__ = ["IMPL", "run"]

#: the groups a summary lists (the counts stay exact)
PLAN_LIMIT = 200

#: videos that are no candidates at all
_SKIP_STATES = ("superseded", "missing")


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    payload = job.payload or {}
    scope: Literal["library", "all"] = "all" if payload.get("scope") == "all" else "library"
    dry_run = bool(payload.get("dry_run"))
    if job.subject.kind == "video":
        return await _check_video(db, job, ctx, str(job.subject.id), scope, dry_run)
    library_id = str(job.subject.id)
    if dry_run:
        plan = await find_groups(
            db, library_id, scope=scope, to_local=ctx.local_path, check_cancel=ctx.check_cancel
        )
        return StageResult(
            output_hash=canonical_hash({"dry_run": sorted(g.key for g in plan.groups)}),
            outcome="unchanged",
            summary={
                "scope": scope,
                "dry_run": True,
                "groups": len(plan.groups),
                "plan": [g.as_json() for g in plan.groups[:PLAN_LIMIT]],
            },
        )
    return await _fan_out(db, job, ctx, library_id, scope)


async def _fan_out(
    db: Any, job: Job, ctx: StageContext, library_id: str, scope: Literal["library", "all"]
) -> StageResult:
    skip = ", ".join(f"'{s}'" for s in _SKIP_STATES)
    videos = [
        str(r["id"])
        for r in await db.fetch(
            "SELECT id FROM videos WHERE library_id = $1"
            f" AND state NOT IN ({skip}) AND superseded_by IS NULL ORDER BY id",
            library_id,
        )
    ]
    states = await conversion_states(db, library_id)
    counts = {"checks": 0, "conversions": 0, "converting": 0, "convert_failed": 0, "queued": 0}
    for n, vid in enumerate(videos):
        await ctx.check_cancel()
        await ctx.progress("fan_out", n, len(videos), steps=["fan_out"])
        state = states.get(vid)
        if state == "done":
            res = await enqueue_job(
                db,
                subject_kind="video",
                subject_id=vid,
                stage="dedup_scan",
                priority=EPIC47_PRIORITIES["dedup_scan"],
                reason={"why": "find duplicates in the library", "by": f"job:{job.id}"},
                triggered_by=job.id,
                payload={"scope": scope} if scope == "all" else None,
            )
            counts["checks"] += 1
        elif state is None:
            # never planned for conversion: convert first; its edge queues the check
            res = await enqueue_job(
                db,
                subject_kind="video",
                subject_id=vid,
                stage="convert_mp4",
                priority=EPIC47_PRIORITIES["convert_mp4"],
                reason={"why": "find duplicates: convert first", "by": f"job:{job.id}"},
                triggered_by=job.id,
            )
            counts["conversions"] += 1
        elif state == "failed":
            counts["convert_failed"] += 1
            continue
        else:
            counts["converting"] += 1  # its conversion's edge queues its check
            continue
        counts["queued"] += int(res.enqueued)
    await ctx.progress("fan_out", len(videos), len(videos), steps=["fan_out"], force=True)
    return StageResult(
        output_hash=canonical_hash({"fan_out": counts}),
        outcome="changed" if counts["queued"] else "unchanged",
        summary={"scope": scope, "videos": len(videos), **counts},
    )


async def _check_video(
    db: Any,
    job: Job,
    ctx: StageContext,
    video_id: str,
    scope: Literal["library", "all"],
    dry_run: bool,
) -> StageResult:
    row = await db.fetchrow("SELECT library_id FROM videos WHERE id = $1", video_id)
    if row is None:
        return StageResult(output_hash=canonical_hash("gone"), outcome="unchanged")
    library_id = str(row["library_id"])
    if (await conversion_states(db, library_id)).get(video_id) != "done":
        # its check comes after its conversion (the edge brings it back)
        return StageResult(
            output_hash=canonical_hash("not_converted"),
            outcome="unchanged",
            summary={"video_id": video_id, "skipped": "not_converted"},
        )

    async def progress(done: int, total: int) -> None:
        await ctx.progress("full_hash", done, total)

    plan = await find_groups(
        db,
        library_id,
        scope=scope,
        video_id=video_id,
        to_local=ctx.local_path,
        progress=progress,
        check_cancel=ctx.check_cancel,
    )
    base: dict[str, Any] = {
        "video_id": video_id,
        "scope": scope,
        "dry_run": dry_run,
        "exact": sum(1 for g in plan.groups if g.tier == "exact"),
        "same_video": sum(1 for g in plan.groups if g.tier == "same_video"),
        "known": len(plan.known),
        "bytes_hashed": plan.bytes_hashed,
        "unreadable": len(plan.unreadable),
        "collisions": len(plan.collisions),
    }
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
    report = await write_groups(db, plan, library_id, scope=scope, video_id=video_id)
    requests = tuple(
        Request(
            stage="dedup_apply",
            subject_kind="dup_group",
            subject_id=UUID(gid),
            reason="byte-identical copies (auto_exact)",
            payload={"auto": True},
            priority=EPIC47_PRIORITIES["dedup_apply"],
        )
        for gid in dict.fromkeys(report.auto)
    )
    touched = report.created or report.reopened or report.updated or report.removed_stale
    return StageResult(
        output_hash=canonical_hash(sorted([*(g.key for g in plan.groups), *plan.known])),
        outcome="changed" if touched else "unchanged",
        requests=requests,
        summary={
            **base,
            "created": len(report.created),
            "updated": len(report.updated),
            "reopened": len(report.reopened),
            "dismissed_kept": report.skipped_dismissed,
            "removed_stale": report.removed_stale,
            "auto_apply": len(report.auto),
        },
    )


IMPL = StageImpl(run=run)
