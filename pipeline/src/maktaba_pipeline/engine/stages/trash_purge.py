"""``trash_purge``: empty a library's file trash of what is past its grace period (Epic 47).

Subject: a library. Pool ``nas_write``. Every ``file_actions`` row of the library that
sent a file to the trash (``<root>/.maktaba/trash/...``), is ``done`` and is past its
``purge_after`` (the library's ``settings.trash.days``, 30 by default) has its file
deleted and ``purged_at`` recorded; a merged duplicate's superseded video row is deleted
with it (its derived data cascades). A file whose size is not the one the ledger
recorded stops the job, non-retryable, with nothing after it deleted: something other
than Maktaba changed the trash. Restore works until the purge.
"""

from __future__ import annotations

from datetime import UTC, datetime
from typing import Any

from ...db.jobs import Job
from ...library_mgmt.trash import TrashSizeMismatch, purge_due
from ..context import StageContext, StageFailure
from ..graph import StageResult
from ..hashing import canonical_hash
from .registry import StageImpl

__all__ = ["IMPL", "run"]


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id = job.subject.id

    async def before_each(done: int, total: int) -> None:
        await ctx.check_cancel()
        await ctx.progress("purge", done, total)

    try:
        report = await purge_due(
            db,
            library_id,
            at=datetime.now(UTC),
            to_local=ctx.local_path,
            before_each=before_each,
        )
    except TrashSizeMismatch as exc:
        raise StageFailure("trash_size_mismatch", str(exc), retryable=False) from exc
    summary = {
        "purged": len(report.purged),
        "missing": len(report.missing),
        "rows_deleted": len(report.rows_deleted),
        "bytes_freed": report.bytes_freed,
    }
    touched = report.purged or report.missing
    return StageResult(
        output_hash=canonical_hash({"purged": sorted(str(i) for i in touched)}),
        outcome="changed" if touched else "unchanged",
        summary=summary,
    )


IMPL = StageImpl(run=run)
