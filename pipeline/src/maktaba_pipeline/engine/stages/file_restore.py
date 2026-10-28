"""``file_restore``: undo ledger rows from the file-actions page (Epic 47).

Subject: a library. Pool ``nas_write``. The payload lists the ``file_actions`` rows to
restore (``{"action_ids": [uuid, ...]}``; the API adds to it while the job waits or
runs). Each goes through :func:`library_mgmt.swap.restore`: the trashed file goes back
to its old path and the DB change is reversed (a conversion gets its original back and
the MP4 goes to the trash; a merged duplicate is revived). The API never moves a file
itself, so Restore is a queue job like the others.

A row that is already restored (``rolled_back``, e.g. by an earlier run of this job) is
skipped. A row that cannot be restored (purged, its trash file gone, a kind with no
undo) is reported in the summary; the job fails, not retried, only when no row was
restored at all. A file error (the share went away) fails the job so it retries.
"""

from __future__ import annotations

from typing import Any
from uuid import UUID

from ...db.jobs import Job
from ...library_mgmt.ledger import get_action
from ...library_mgmt.swap import RestoreError, restore
from ..context import StageContext, StageFailure
from ..graph import StageResult
from ..hashing import canonical_hash
from .registry import StageImpl

__all__ = ["IMPL", "run"]


def _action_ids(payload: Any) -> list[str]:
    raw = payload.get("action_ids") if isinstance(payload, dict) else None
    out: list[str] = []
    for v in raw if isinstance(raw, list) else []:
        try:
            out.append(str(UUID(str(v))))
        except ValueError:
            continue
    return sorted(set(out))


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    ids = _action_ids(job.payload)
    if not ids:
        raise StageFailure("restore_bad_payload", "no file actions to restore", retryable=False)
    restored: list[str] = []
    already: list[str] = []
    refused: list[dict[str, str]] = []
    for n, action_id in enumerate(ids):
        await ctx.check_cancel()
        await ctx.progress("restore", n, len(ids))
        row = await get_action(db, action_id)
        if row is None or str(row.library_id) != str(job.subject.id):
            refused.append({"id": action_id, "error": "not a file action of this library"})
            continue
        if row.state == "rolled_back":
            already.append(action_id)
            continue
        try:
            await restore(db, action_id, job_id=job.id, to_local=ctx.local_path)
        except RestoreError as exc:
            refused.append({"id": action_id, "error": str(exc)[:300]})
            continue
        restored.append(action_id)
    await ctx.progress("restore", len(ids), len(ids))
    if refused and not restored and not already:
        raise StageFailure(
            "restore_refused",
            "; ".join(f"{r['id']}: {r['error']}" for r in refused[:5]),
            retryable=False,
        )
    return StageResult(
        output_hash=canonical_hash({"restored": restored, "already": already}),
        outcome="changed" if restored else "unchanged",
        summary={"restored": restored, "already_restored": already, "refused": refused},
    )


IMPL = StageImpl(run=run)
