"""Moving the failures from before Epic 40 into the new model (story 40.6).

Every ``failed`` row without a ``failure_class`` is classified from its stored cause (or,
for the oldest rows, its ``error`` kind and message), the same way a new failure is
(:mod:`.failure_classes`). Then, for the row that is still the latest of its subject and
stage:

* a dependency or a resource (the 6,577 ``check_tags`` model failures, a full shared
  memory) gets a new **parked** row: the watcher releases it within one probe interval when
  the dependency is up, so the work resumes at once if the model answers;
* a transient failure still under its class budget is queued again;
* anything else keeps its failed row, now with its class: ``item_permanent`` and spent
  budgets are what the queues page shows as *Needs you*.

A row that is not the latest (a later run superseded it) is only classified. It runs in
batches on the dependency watchers' leader round until nothing is left, so a home with
thousands of old failures is moved without a long transaction.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import timedelta
from typing import Any
from uuid import UUID

from .causes import classify
from .enqueue import enqueue_job
from .failure_classes import (
    ITEM_PERMANENT,
    MODEL,
    SHARE,
    TRANSIENT,
    QueueHealthConfig,
    class_for,
    is_parking,
)
from .failure_policy import budget, library_root, root_answers
from .graph import get_stage
from .sql import loads, now, to_ts

__all__ = ["BackfillReport", "backfill_once"]

BATCH = 500

_TODO = f"""
SELECT j.id, j.stage, j.subject_kind, j.subject_id, j.video_id, j.attempts, j.max_attempts,
       j.error, j.error_cause,
       NOT EXISTS (SELECT 1 FROM processing_jobs n
                    WHERE n.subject_kind = j.subject_kind AND n.subject_id = j.subject_id
                      AND n.stage = j.stage AND n.id > j.id) AS latest
  FROM processing_jobs j
 WHERE j.state = 'failed' AND j.failure_class IS NULL
 ORDER BY j.id
 LIMIT {BATCH}
"""


@dataclass(frozen=True, slots=True)
class BackfillReport:
    classified: int = 0
    parked: int = 0
    requeued: int = 0


def _kind_message(error: Any) -> tuple[str, str]:
    try:
        doc = loads(error)
    except ValueError:
        return "error", str(error or "")
    if isinstance(doc, dict):
        return str(doc.get("kind") or "error"), str(doc.get("message") or "")
    return "error", str(error or "")


async def backfill_once(db: Any, cfg: QueueHealthConfig) -> BackfillReport:
    """One batch; an empty report means nothing was left."""
    todo = await db.fetch(_TODO)
    if not todo:
        return BackfillReport()
    roots_up: dict[str, bool] = {}
    parked = requeued = 0
    t = now()
    for r in todo:
        kind, message = _kind_message(r["error"])
        cause = str(r["error_cause"]) if r["error_cause"] else classify(kind, message)
        cls = class_for(cause, cfg.class_overrides)
        stage = str(r["stage"])
        if cls == MODEL:
            s = get_stage(stage)
            cls = f"{MODEL}:{(s.model_rules if s is not None else None) or 'model'}"
        elif cause.startswith("file_missing") and cls == ITEM_PERMANENT and r["latest"]:
            root = await library_root(db, r["video_id"])
            if root is not None:
                if root not in roots_up:
                    roots_up[root] = await root_answers(root, cfg.probe_timeout_seconds)
                if not roots_up[root]:
                    cls = f"{SHARE}:{root}"
        await db.execute(
            "UPDATE processing_jobs SET failure_class = $2 WHERE id = $1", int(r["id"]), cls
        )
        if not r["latest"]:
            continue
        park = is_parking(cls)
        again = cls == TRANSIENT and int(r["attempts"]) < budget(cls, int(r["max_attempts"]), cfg)
        if not (park or again):
            continue
        res = await enqueue_job(
            db,
            subject_kind=str(r["subject_kind"]),
            subject_id=UUID(str(r["subject_id"])),
            stage=stage,
            reason={"why": "queue health: an older failure moved to the new model", "by": "system"},
        )
        if res.job_id is None or res.outcome != "inserted":
            continue
        if park:
            await db.execute(
                "UPDATE processing_jobs SET parked_on = $2, failure_class = $2, not_before = $3"
                " WHERE id = $1 AND state = 'pending'",
                res.job_id,
                cls,
                to_ts(db, t + timedelta(hours=cfg.park_max_hours)),
            )
            parked += 1
        else:
            requeued += 1
    return BackfillReport(len(todo), parked, requeued)
