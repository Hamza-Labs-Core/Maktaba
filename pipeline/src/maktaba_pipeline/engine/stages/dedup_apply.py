"""``dedup_apply`` (Epic 47, story 47.7): merge one duplicate group.

Subject: a ``duplicate_groups`` row (``dup_group``). Pool ``nas_write``. The merge is
:func:`maktaba_pipeline.library_mgmt.dedup_merge.apply_group`: the user data to the
keeper, the losers superseded, every other file to the library's trash.

* An **exact** group of one library with ``settings.dedup.auto_exact`` on (the default)
  is applied at once (``dedup_scan`` requests it).
* Every other group (same video, other file; or exact across libraries) is **always
  reviewed**: the job parks with outcome ``awaiting_review`` and the group's digest in
  ``output_summary.review_digests``, whatever the stage's ``write_mode``. It is applied
  by the queue's Apply (``{"apply": true, "plan_digests": [...]}``) or by the duplicates
  page's per-group Apply, whose job carries ``{"apply": true, "group_id": <id>}`` and
  optionally ``"keeper_video_id"`` (the admin's choice) and ``"decided_by"``.
* ``{"dry_run": true}``: the keeper and the files that would go to the trash are the
  ``output_summary``; nothing changes.

Failures: a foreign key to ``videos`` no rule covers (``fk_not_covered``) and an
unreadable keeper (``keeper_unreadable``, the group ``failed``) are not retried. A file
that could not be moved to the trash (``trash_move_failed``) is retried: the merge has
committed, so the retry moves the files only.
"""

from __future__ import annotations

from typing import Any

from ...db.jobs import Job
from ...library_mgmt.dedup_merge import KeeperUnreadable, MergeRefused, apply_group
from ...library_mgmt.duplicates import auto_exact
from ..changes import approved_digests
from ..context import StageContext, StageFailure
from ..graph import StageResult
from ..hashing import canonical_hash
from .registry import StageImpl

__all__ = ["IMPL", "group_digest", "run"]


def group_digest(group_id: str, members_key: str) -> str:
    """What an Apply approves: this group with these members."""
    return canonical_hash({"group": group_id, "members_key": members_key})


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    gid = str(job.subject.id)
    payload = job.payload or {}
    g = await db.fetchrow(
        "SELECT library_id, tier, status, members_key FROM duplicate_groups WHERE id = $1", gid
    )
    if g is None:
        return StageResult(output_hash=canonical_hash("gone"), outcome="unchanged")
    library_id = None if g["library_id"] is None else str(g["library_id"])
    digest = group_digest(gid, str(g["members_key"]))
    auto = str(g["tier"]) == "exact" and await auto_exact(db, library_id)
    approved = digest in approved_digests(payload) or (
        bool(payload.get("apply")) and str(payload.get("group_id") or "") == gid
    )
    dry_run = bool(payload.get("dry_run"))
    if str(g["status"]) in ("open", "failed") and not auto and not approved and not dry_run:
        ctx.review_digests.append(digest)
        await ctx.log(
            "info",
            f"{g['tier']} duplicate group awaiting review",
            {"group_id": gid, "digest": digest},
        )
        return StageResult(
            output_hash=canonical_hash({"review": digest}),
            outcome="awaiting_review",
            summary={"group_id": gid, "tier": str(g["tier"]), "status": "awaiting_review"},
        )
    keeper = payload.get("keeper_video_id")
    decided_by = payload.get("decided_by") or ("auto_exact" if auto and not approved else None)
    try:
        out = await apply_group(
            db,
            gid,
            keeper_video_id=None if keeper is None else str(keeper),
            decided_by=None if decided_by is None else str(decided_by),
            job_id=job.id,
            to_local=ctx.local_path,
            dry_run=dry_run,
        )
    except MergeRefused as exc:
        raise StageFailure("fk_not_covered", str(exc), retryable=False) from exc
    except KeeperUnreadable as exc:
        raise StageFailure("keeper_unreadable", str(exc), retryable=False) from exc
    if out.failed_moves:
        raise StageFailure(
            "trash_move_failed",
            "; ".join(f"{p}: {e}" for p, e in out.failed_moves[:5]),
            retryable=True,
        )
    return StageResult(
        output_hash=canonical_hash(
            {"group": gid, "status": out.status, "keeper": out.keeper_video_id}
        ),
        outcome="changed" if out.status == "applied" else "unchanged",
        summary=out.summary(),
    )


IMPL = StageImpl(run=run)
