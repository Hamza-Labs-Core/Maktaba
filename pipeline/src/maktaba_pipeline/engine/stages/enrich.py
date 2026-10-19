"""``enrich``: a video's metadata suggestions ("Find metadata", "Re-enrich"; stub audit #4).

Subject: a video. Runs that video's queued ``enrich_jobs`` row through
:func:`maktaba_pipeline.enrich.worker.process_one_enrich_job` with the metadata provider
(:mod:`maktaba_pipeline.enrich.service`): the internal suggestion from Maktaba's own intel,
and the web suggestion a language model makes of a web search when they are configured.
Nothing is applied to the video: the suggestions wait for an accept in the review panel.

Enqueued by the API (Find metadata, Re-enrich) and by the home's sweep for any queued row
(:mod:`maktaba_pipeline.enrich.sweep`); an operator's retrigger with no queued row queues
one. Home only: the search keys are sealed in ``home_secrets``.

Settings: ``rules.enrich`` (search backend, model, pages, the daily caps, the cache).
"""

from __future__ import annotations

from typing import Any
from uuid import UUID

from ...db.jobs import Job
from ...enrich.jobs import enqueue_enrich
from ...enrich.providers import build_service
from ...enrich.service import EnrichResult
from ...enrich.worker import process_one_enrich_job
from ..context import StageContext, StageFailure
from ..graph import StageResult
from ..hashing import canonical_hash
from ..sql import now, to_ts
from .registry import StageImpl

__all__ = ["IMPL", "run"]


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    cfg = await ctx.rules("enrich")
    vid = UUID(str(job.subject.id))
    row = await db.fetchrow(
        "SELECT id, status, not_before FROM enrich_jobs WHERE video_id = $1"
        " AND status IN ('pending', 'running', 'deferred')",
        str(vid),
    )
    if row is None:
        # an operator's retrigger (or a sweep that raced a finished row): queue one
        await enqueue_enrich(db, vid, force=bool(job.force), origin="reenrich")
    elif job.force and str(row["status"]) in ("pending", "deferred"):
        # someone asked now: a deferred row does not wait out its window (the caps
        # still apply)
        await db.execute(
            "UPDATE enrich_jobs SET not_before = $2 WHERE id = $1", str(row["id"]),
            to_ts(db, now()),
        )  # fmt: skip
    service = await build_service(db, cfg)
    await ctx.log("info", "finding metadata: internal intel" + (
        f", web search ({service.search.name})" if service.search else ", no web search"
    ) + (f", model {service.reader.name}" if service.reader else ", no model"))  # fmt: skip
    done = await process_one_enrich_job(db, service, video_id=vid)
    if done is None:
        out: dict[str, Any] = {"status": "waiting"}
        if row is not None:
            out["not_before"] = str(row["not_before"])
        return StageResult(output_hash=canonical_hash(["enrich", out]), outcome="unchanged",
                           summary=out)  # fmt: skip
    if done.status in ("pending", "failed"):
        # the error is on the job (the queue row retries it with back-off, picked up by
        # the sweep): this engine run is not retried on top of that
        raise StageFailure("enrich_error", done.reason[:500] or "the provider failed",
                           retryable=False)  # fmt: skip
    result = done.result if isinstance(done.result, EnrichResult) else service.last
    out = {"status": done.status, "origin": done.job.origin}
    if done.job.query:
        out["manual_query"] = done.job.query
    if result is not None:
        out.update(result.as_json())
    if done.reason:
        out["reason"] = done.reason[:300]
    if done.status == "deferred":
        await ctx.log("warn", f"web part waits {round(done.retry_after_sec / 60)} min: "
                      f"{done.reason}")  # fmt: skip
    elif result is not None and result.skipped:
        await ctx.log("info", f"web part skipped: {result.skipped}")
    changed = result is not None and (result.internal or result.web_fields > 0)
    return StageResult(
        output_hash=canonical_hash(["enrich", out]),
        outcome="changed" if changed else "unchanged",
        summary=out,
    )


IMPL = StageImpl(run=run)
