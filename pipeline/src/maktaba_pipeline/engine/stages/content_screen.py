"""``content_screen`` (Epic 57, story 57.17): a video's age tier worked out from its
transcript on the home's own model, written to ``video_content_signals``.

The work is :mod:`maktaba_pipeline.classify.content_screen`; this is its place in the
engine. It runs on the home only, after the transcript's second listen.

On a video it writes the row, or leaves it when the input and the model are the ones the
row answers (no model is asked). It has no input fingerprint of its own: the row keeps
what was asked, so a run that found no model is simply run again later, where an engine
fingerprint would call it done.

On a library it plans: every video with no row, and every video whose row a different
model wrote, is enqueued at :data:`BACKFILL_PRIORITY`, the lowest of any stage. So a
change of the home's model asks about every video again, behind all other work, and each
video keeps its earlier tier until its new row lands. Music and recitation are left out,
and nothing is planned while no model can be reached.
"""

from __future__ import annotations

import time
from typing import Any

from ...classify import content_screen as screen
from ...db.jobs import Job
from ...llm import LLMError
from ...log import get_logger
from ..context import StageContext, StageFailure
from ..graph import CONTENT_SCREEN_VERSION, StageResult
from ..hashing import canonical_hash
from ..sql import loads, uid
from .registry import StageImpl

__all__ = [
    "BACKFILL_PRIORITY",
    "CONTENT_SCREEN_VERSION",
    "IMPL",
    "PLAN_BATCH",
    "plan_library",
    "refresh",
    "run",
]

_log = get_logger()

#: the planner's priority: behind everything else (lower runs first; normal is 100)
BACKFILL_PRIORITY = 900
#: how often a worker that serves the stage plans every library (s)
PLAN_EVERY_S = 1800.0
#: how many videos one planning job enqueues at most
PLAN_BATCH = 5000
#: after a run found no machine with the model, how long this worker plans nothing (s)
NO_MODEL_HOLD_S = 6 * 3600.0

#: stands in for the model in tests (None: the cluster door)
ASK: screen.Asker | None = None

_NO_MODEL_AT: list[float] = []
_LAST_PLAN: list[float] = []


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    if job.subject.kind == "library":
        return await plan_library(db, job.subject.id)
    try:
        out = await screen.screen_video(db, job.subject.id, ask=ASK)
    except LLMError as exc:
        raise StageFailure("llm", f"{type(exc).__name__}: {exc}"[:300], retryable=True) from exc
    if out.state == "no_model":
        _NO_MODEL_AT[:] = [time.monotonic()]
    elif out.state == "written":
        _NO_MODEL_AT.clear()
    return StageResult(
        output_hash=canonical_hash([out.state, out.input_hash, out.tier, out.confidence]),
        outcome="changed" if out.state in ("written", "not_checked") else "unchanged",
        summary=out.summary(),
    )


def _nothing(detail: str) -> StageResult:
    summary = {"content_screen": "no_model", **({"detail": detail} if detail else {})}
    return StageResult(output_hash=canonical_hash("no model"), outcome="unchanged", summary=summary)


async def plan_library(db: Any, library_id: Any) -> StageResult:
    """Enqueue the library's videos that have no row, a row another model wrote, or a row
    that says there is no transcript when there now is one."""
    from ..enqueue import enqueue_job

    model = await screen.model_name(db)
    if model is None:
        return _nothing("")
    if _NO_MODEL_AT and time.monotonic() - _NO_MODEL_AT[0] < NO_MODEL_HOLD_S:
        return _nothing("held")
    if not await screen.model_reachable(model):
        return _nothing("not reachable")
    rows = await db.fetch(
        "SELECT v.id, s.video_id AS has_row, s.not_checked FROM videos v"
        " LEFT JOIN video_content_signals s ON s.video_id = v.id"
        " WHERE v.library_id = $1 AND v.deleted_at IS NULL"
        "   AND (s.video_id IS NULL OR (s.not_checked IS NULL AND s.model <> $2)"
        # a transcript that reached the video by another way than the stage before this
        # one (a merge of two copies moves it): the row still says there is none
        "        OR (s.not_checked = 'no_transcript' AND EXISTS ("
        "              SELECT 1 FROM transcripts t"
        "              JOIN transcript_segments g ON g.transcript_id = t.id"
        "              WHERE t.video_id = v.id AND t.is_active AND trim(g.text) <> '')))"
        "   AND COALESCE(v.content_type, '') <> 'music_video'"
        "   AND NOT EXISTS (SELECT 1 FROM processing_jobs j"
        "                   WHERE j.stage = 'content_screen' AND j.subject_id = v.id"
        "                     AND j.state IN ('pending', 'claimed', 'running', 'paused',"
        "                                     'resuming'))"
        " ORDER BY v.created_at DESC LIMIT $3",
        uid(library_id),
        model,
        PLAN_BATCH,
    )
    new = again = 0
    for r in rows:
        if r["has_row"] is None:
            # a video whose last run left it without a row on purpose (recitation by its
            # tags) is asked about again only when its transcript changes (the graph's edge)
            if await _left_out(db, r["id"]):
                continue
            new += 1
        elif r["not_checked"] is not None:
            new += 1
        else:
            again += 1
        why = (
            "content_screen_model"
            if r["not_checked"] is None and r["has_row"]
            else ("content_screen_backfill")
        )
        await enqueue_job(
            db,
            subject_kind="video",
            subject_id=r["id"],
            stage="content_screen",
            priority=BACKFILL_PRIORITY,
            reason={"why": why, "by": "system"},
        )
    return StageResult(
        output_hash=canonical_hash(["plan", model, new, again]),
        outcome="changed" if new or again else "unchanged",
        summary={"model": model, "new": new, "model_changed": again},
    )


async def _left_out(db: Any, video_id: Any) -> bool:
    row = await db.fetchrow(
        "SELECT output_summary FROM processing_jobs WHERE stage = 'content_screen'"
        " AND subject_id = $1 AND state = 'done' ORDER BY id DESC LIMIT 1",
        uid(video_id),
    )
    if row is None:
        return False
    summary = loads(row["output_summary"]) or {}
    return isinstance(summary, dict) and summary.get("content_screen") == "skipped"


async def refresh(db: Any, serving: bool) -> None:
    """A worker that serves the stage plans every library now and then."""
    if not serving:
        return
    t = time.monotonic()
    if _LAST_PLAN and t - _LAST_PLAN[0] < PLAN_EVERY_S:
        return
    _LAST_PLAN[:] = [t]
    from ..enqueue import enqueue_job

    try:
        if await screen.model_name(db) is None:
            return
        for r in await db.fetch("SELECT id FROM libraries"):
            await enqueue_job(
                db, subject_kind="library", subject_id=r["id"], stage="content_screen",
                priority=BACKFILL_PRIORITY,
                reason={"why": "content_screen_backfill", "by": "system"},
            )  # fmt: skip
    except Exception as exc:  # noqa: BLE001 - the next refresh plans again
        _log.warning("content_screen_plan_failed", error=f"{type(exc).__name__}: {exc}"[:200])


IMPL = StageImpl(run=run, refresh=refresh)
