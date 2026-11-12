"""``content_screen`` (Epic 57, story 57.17): a video's age tier worked out from its
transcript on the home's own model, written to ``video_content_signals``.

The work is :mod:`maktaba_pipeline.classify.content_screen`; this is its place in the
engine. It runs on the home only, after the transcript's second listen.

On a video it writes the row, or leaves it when the input and the model are the ones the
row answers (no model is asked). It has no input fingerprint of its own: the row keeps
what was asked, so a run that found no model is simply run again later, where an engine
fingerprint would call it done.

On a library it plans: every video whose row is not what a run would leave now, worked
out as the stage itself works it out (:func:`content_screen.expected`, without asking any
model). That is a video with no row, one whose row another model wrote, one whose title,
description, kind, topic tags or transcript changed since (classification landing after
the first run is such a change), one that got a transcript another way, and one whose
last run could not read an answer (tried again up to ``MAX_ATTEMPTS`` times). Each is
enqueued at :data:`BACKFILL_PRIORITY`, the lowest of any stage, so a change of the home's
model asks about every video again behind all other work, and each video keeps its
earlier tier until its new row lands. Music and recitation are left out, a video whose
last job failed for good on this model is left alone until the model or the video
changes, and nothing is planned while no model can be reached.
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
from ..sql import uid
from .registry import StageImpl

__all__ = [
    "BACKFILL_PRIORITY",
    "CONTENT_SCREEN_VERSION",
    "IMPL",
    "PLAN_BATCH",
    "PLAN_PAGE",
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
#: how many of a library's videos the planner reads at a time
PLAN_PAGE = 1000
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
        # the model's name leads the error: the planner leaves a video alone whose last
        # job failed for good on the model the home still has (_failed_on)
        model = await screen.model_name(db)
        raise StageFailure(
            "llm", f"{_on(model or '')}{type(exc).__name__}: {exc}"[:300], retryable=True
        ) from exc
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


def _on(model: str) -> str:
    """How a failed job's error names the model it failed on."""
    return f"[{model}] "


def _failed_on(job: Any, model: str) -> bool:
    """Whether the video's newest job failed for good on this model: asking the same
    model about the same video again would fail the same way, three tries every plan."""
    return bool(job is not None and job["state"] == "failed" and _on(model) in str(job["error"]))


_LIVE = ("pending", "claimed", "running", "paused", "resuming")
#: one page of a library's videos, newest first ($1 the library, $2 the page, $3 its start)
_PAGE = (
    "SELECT v.id FROM videos v WHERE v.library_id = $1 AND v.deleted_at IS NULL"
    " ORDER BY v.created_at DESC, v.id LIMIT $2 OFFSET $3"
)


async def plan_library(db: Any, library_id: Any) -> StageResult:
    """Enqueue the library's videos whose row is not what a run would leave now (see the
    module's words). Videos that need nothing cost no place in the batch, so a library
    with many of them is still planned to its end."""
    from ..enqueue import enqueue_job

    model = await screen.model_name(db)
    if model is None:
        return _nothing("")
    if _NO_MODEL_AT and time.monotonic() - _NO_MODEL_AT[0] < NO_MODEL_HOLD_S:
        return _nothing("held")
    if not await screen.model_reachable(model):
        return _nothing("not reachable")
    new = again = 0
    offset = 0
    while new + again < PLAN_BATCH:
        page = (uid(library_id), PLAN_PAGE, offset)
        videos = await db.fetch(
            "SELECT v.id, v.title, v.description, v.content_type FROM videos v"
            f" WHERE v.id IN ({_PAGE}) ORDER BY v.created_at DESC, v.id",
            *page,
        )
        if not videos:
            break
        offset += len(videos)
        rows = {
            str(r["video_id"]): r
            for r in await db.fetch(
                "SELECT s.video_id, s.not_checked, s.model, s.input_hash, s.attempts,"
                " s.reviewed_at FROM video_content_signals s"
                f" WHERE s.video_id IN ({_PAGE})",
                *page,
            )
        }
        tags: dict[str, dict[str, float]] = {}
        for r in await db.fetch(
            "SELECT g.video_id, g.tag, g.score FROM video_topic_tags g"
            f" WHERE g.video_id IN ({_PAGE})",
            *page,
        ):
            tags.setdefault(str(r["video_id"]), {})[str(r["tag"])] = float(r["score"] or 0.0)
        # the transcript the stage would read (the first active one by id), and whether
        # it has a line that is not blank
        transcripts: dict[str, Any] = {}
        for r in await db.fetch(
            "SELECT t.video_id, t.id, t.language, COALESCE(t.revision, 0) AS revision,"
            " EXISTS (SELECT 1 FROM transcript_segments g WHERE g.transcript_id = t.id"
            "          AND trim(g.text) <> '') AS has_text"
            f" FROM transcripts t WHERE t.is_active AND t.video_id IN ({_PAGE}) ORDER BY t.id",
            *page,
        ):
            transcripts.setdefault(str(r["video_id"]), r)
        # each video's newest job of this stage, and whether one is still to run
        jobs: dict[str, Any] = {}
        live: set[str] = set()
        for r in await db.fetch(
            "SELECT j.subject_id, j.state, j.error FROM processing_jobs j"
            " WHERE j.stage = 'content_screen' AND j.subject_kind = 'video'"
            f"   AND j.subject_id IN ({_PAGE}) ORDER BY j.id",
            *page,
        ):
            jobs[str(r["subject_id"])] = r
            if r["state"] in _LIVE:
                live.add(str(r["subject_id"]))
        for v in videos:
            vid = str(v["id"])
            if vid in live:
                continue
            t = transcripts.get(vid)
            want = screen.expected(
                title=v["title"],
                description=v["description"],
                content_type=v["content_type"],
                tags=tags.get(vid, {}),
                transcript_id=None if t is None else t["id"],
                language=None if t is None else t["language"],
                revision=0 if t is None else t["revision"],
                has_text=bool(t is not None and t["has_text"]),
                model=model,
            )
            row = rows.get(vid)
            if screen.settled(row, want) or _failed_on(jobs.get(vid), model):
                continue
            asked_by_another = (
                row is not None and row["not_checked"] is None and row["model"] != model
            )
            if asked_by_another:
                again += 1
            else:
                new += 1
            why = "content_screen_model" if asked_by_another else "content_screen_backfill"
            await enqueue_job(
                db,
                subject_kind="video",
                subject_id=v["id"],
                stage="content_screen",
                priority=BACKFILL_PRIORITY,
                reason={"why": why, "by": "system"},
            )
            if new + again >= PLAN_BATCH:
                break
    return StageResult(
        output_hash=canonical_hash(["plan", model, new, again]),
        outcome="changed" if new or again else "unchanged",
        summary={"model": model, "new": new, "model_changed": again},
    )


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
