"""The one way to put a job on the queue (plan 06-11 section 4.2 and D6/D7/D8).

On Postgres this calls the SQL function ``processing_jobs_enqueue`` that Go
also calls, so both languages share one set of coalescing rules. On SQLite
:func:`_enqueue_sqlite` carries the same semantics; the two are held to the
same golden test table (tests/engine/test_enqueue.py).

Semantics, in short:

* one live row per ``(subject_kind, subject_id, stage)``;
* a trigger on a *pending* row coalesces: lowest priority wins, ``force`` is
  ORed, reasons are merged (newest first, capped at 20) and, for debounced
  stages, ``not_before`` is pushed out but never beyond
  ``created_at + debounce_max_sec``;
* a trigger on a claimed / running / resuming / paused row sets
  ``rerun_requested``: when that run ends the engine inserts one follow-up.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import timedelta
from typing import Any, Literal
from uuid import UUID

from ..db.pubsub import JOBS_NEW, get_bus
from .events import log_event
from .graph import CATALOG_ID
from .sql import dumps, is_pg, loads, now, to_ts, uid

__all__ = [
    "REASON_CAP",
    "EnqueueOutcome",
    "EnqueueResult",
    "enqueue_job",
    "merge_payload",
    "prepend_reason",
]

REASON_CAP = 20
LIVE_STATES = ("pending", "claimed", "running", "resuming", "paused")

EnqueueOutcome = Literal[
    "inserted",
    "coalesced",
    "rerun_requested",
    "disabled",
    "unknown_stage",
    "invalid_subject",
]


@dataclass(frozen=True, slots=True)
class EnqueueResult:
    job_id: int | None
    outcome: EnqueueOutcome

    @property
    def enqueued(self) -> bool:
        return self.job_id is not None


def prepend_reason(old: list[Any], entry: dict[str, Any]) -> list[Any]:
    """New reason first, list capped at :data:`REASON_CAP`."""
    return [entry, *old][:REASON_CAP]


def merge_payload(old: dict[str, Any] | None, new: dict[str, Any] | None) -> dict[str, Any] | None:
    """Keep the original payload; the only merged key is ``seasons`` (set union)."""
    if old is None:
        return new
    if not new or "seasons" not in new or not isinstance(old, dict):
        return old
    seasons = sorted({*map(str, old.get("seasons", [])), *map(str, new["seasons"])})
    return {**old, "seasons": seasons}


async def enqueue_job(
    db: Any,
    *,
    subject_kind: str,
    subject_id: UUID | str,
    stage: str,
    priority: int | None = None,
    force: bool = False,
    force_cascade: bool = False,
    reason: dict[str, Any] | None = None,
    triggered_by: int | None = None,
    payload: dict[str, Any] | None = None,
    max_attempts: int | None = None,
) -> EnqueueResult:
    """Enqueue ``stage`` for a subject. Safe to call concurrently and repeatedly."""
    reason = reason or {"why": "enqueue", "by": "system"}
    if is_pg(db):
        row = await db.fetchrow(
            "SELECT job_id, outcome FROM processing_jobs_enqueue($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)",
            subject_kind,
            uid(subject_id),
            stage,
            priority,
            force,
            force_cascade,
            dumps(reason),
            triggered_by,
            None if payload is None else dumps(payload),
            max_attempts,
        )
        result = EnqueueResult(
            None if row["job_id"] is None else int(row["job_id"]),
            row["outcome"],
        )
    else:
        result = await _enqueue_sqlite(
            db,
            subject_kind=subject_kind,
            subject_id=str(subject_id),
            stage=stage,
            priority=priority,
            force=force,
            force_cascade=force_cascade,
            reason=reason,
            triggered_by=triggered_by,
            payload=payload,
            max_attempts=max_attempts,
        )
    if result.job_id is not None:
        await _log(db, result, subject_kind, subject_id, stage, reason)
    return result


async def _log(
    db: Any,
    result: EnqueueResult,
    subject_kind: str,
    subject_id: UUID | str,
    stage: str,
    reason: dict[str, Any],
) -> None:
    assert result.job_id is not None
    kind = {"inserted": "enqueued", "coalesced": "coalesced"}.get(result.outcome, "rerun_requested")
    lib = await db.fetchrow("SELECT library_id FROM processing_jobs WHERE id = $1", result.job_id)
    await log_event(
        db,
        job_id=result.job_id,
        subject_kind=subject_kind,
        subject_id=subject_id,
        library_id=None if lib is None else lib["library_id"],
        stage=stage,
        level="debug",
        kind=kind,
        message=str(reason.get("why", kind))[:500],
        data={"reason": reason, "outcome": result.outcome},
    )


# ------------------------------------------------------------------ SQLite twin


async def _enqueue_sqlite(
    db: Any,
    *,
    subject_kind: str,
    subject_id: str,
    stage: str,
    priority: int | None,
    force: bool,
    force_cascade: bool,
    reason: dict[str, Any],
    triggered_by: int | None,
    payload: dict[str, Any] | None,
    max_attempts: int | None,
) -> EnqueueResult:
    async with db.transaction():
        s = await db.fetchrow(
            "SELECT subject_kinds, enabled, debounce_sec, debounce_max_sec, max_attempts"
            " FROM job_stages WHERE stage = $1",
            stage,
        )
        if s is None:
            return EnqueueResult(None, "unknown_stage")
        if not s["enabled"]:
            return EnqueueResult(None, "disabled")
        if subject_kind not in json.loads(s["subject_kinds"]):
            return EnqueueResult(None, "invalid_subject")
        cols = await _resolve_subject(db, subject_kind, subject_id)
        if cols is None:
            return EnqueueResult(None, "invalid_subject")

        t = now()
        entry = {**reason, "at": to_ts(db, t)}
        debounce = int(s["debounce_sec"])
        live = await db.fetchrow(
            "SELECT id, state, priority, force, force_cascade, reasons, created_at, payload,"
            " rerun_force, rerun_reasons, enqueue_count FROM processing_jobs"
            " WHERE subject_kind = $1 AND subject_id = $2 AND stage = $3"
            " AND state IN ('pending','claimed','running','resuming','paused')",
            subject_kind,
            subject_id,
            stage,
        )
        if live is None:
            from_stage = reason.get("from_stage")
            if from_stage is None and triggered_by is not None:
                r = await db.fetchrow(
                    "SELECT stage FROM processing_jobs WHERE id = $1", triggered_by
                )
                from_stage = None if r is None else r["stage"]
            row = await db.fetchrow(
                "INSERT INTO processing_jobs (subject_kind, subject_id, video_id, library_id,"
                " series_id, season_id, stage, state, priority, max_attempts, payload, not_before,"
                " force, force_cascade, reasons, triggered_by, triggered_by_stage)"
                " VALUES ($1,$2,$3,$4,$5,$6,$7,'pending',$8,$9,$10,$11,$12,$13,$14,$15,$16)"
                " RETURNING id",
                subject_kind,
                subject_id,
                cols["video_id"],
                cols["library_id"],
                cols["series_id"],
                cols["season_id"],
                stage,
                100 if priority is None else priority,
                int(max_attempts if max_attempts is not None else s["max_attempts"]),
                None if payload is None else dumps(payload),
                to_ts(db, t + timedelta(seconds=debounce)) if debounce > 0 else None,
                force,
                force_cascade,
                dumps([entry]),
                triggered_by,
                from_stage,
            )
            job_id = int(row["id"])
            get_bus().publish(
                JOBS_NEW,
                {
                    "id": job_id,
                    "video_id": cols["video_id"],
                    "library_id": cols["library_id"],
                    "subject_kind": subject_kind,
                    "subject_id": subject_id,
                    "stage": stage,
                    "priority": 100 if priority is None else priority,
                },
            )
            return EnqueueResult(job_id, "inserted")

        pending = live["state"] == "pending"
        new_payload = merge_payload(loads(live["payload"]), payload)
        if pending:
            created = _as_dt(live["created_at"])
            nb: Any = None
            if debounce > 0:
                nb = min(
                    t + timedelta(seconds=debounce),
                    created + timedelta(seconds=int(s["debounce_max_sec"])),
                )
            await db.execute(
                "UPDATE processing_jobs SET priority = $2, force = $3, force_cascade = $4,"
                " reasons = $5, payload = $6, enqueue_count = enqueue_count + 1,"
                " not_before = CASE WHEN $7 IS NULL THEN not_before ELSE $7 END WHERE id = $1",
                live["id"],
                min(int(live["priority"]), 100 if priority is None else priority),
                bool(live["force"]) or force,
                bool(live["force_cascade"]) or force_cascade,
                dumps(prepend_reason(loads(live["reasons"]) or [], entry)),
                None if new_payload is None else dumps(new_payload),
                to_ts(db, nb),
            )
            return EnqueueResult(int(live["id"]), "coalesced")
        await db.execute(
            "UPDATE processing_jobs SET rerun_requested = 1, rerun_force = $2, rerun_reasons = $3,"
            " payload = $4, enqueue_count = enqueue_count + 1 WHERE id = $1",
            live["id"],
            bool(live["rerun_force"]) or force or force_cascade,
            dumps(prepend_reason(loads(live["rerun_reasons"]) or [], entry)),
            None if new_payload is None else dumps(new_payload),
        )
        return EnqueueResult(int(live["id"]), "rerun_requested")


def _as_dt(value: Any) -> Any:
    from .sql import parse_ts

    return parse_ts(value)


async def _resolve_subject(db: Any, kind: str, sid: str) -> dict[str, str | None] | None:
    """Typed FK columns for a subject, or None when the subject does not exist."""
    cols: dict[str, str | None] = {
        "video_id": None,
        "library_id": None,
        "series_id": None,
        "season_id": None,
    }
    if kind == "video":
        r = await db.fetchrow("SELECT library_id FROM videos WHERE id = $1", sid)
        if r is None:
            return None
        cols["video_id"], cols["library_id"] = sid, str(r["library_id"])
    elif kind == "season":
        r = await db.fetchrow("SELECT library_id FROM series_seasons WHERE id = $1", sid)
        if r is None:
            return None
        cols["season_id"] = sid
        cols["library_id"] = None if r["library_id"] is None else str(r["library_id"])
    elif kind == "series":
        r = await db.fetchrow("SELECT library_id FROM series WHERE id = $1", sid)
        if r is None:
            return None
        cols["series_id"] = sid
        cols["library_id"] = None if r["library_id"] is None else str(r["library_id"])
    elif kind == "library":
        r = await db.fetchrow("SELECT id FROM libraries WHERE id = $1", sid)
        if r is None:
            return None
        cols["library_id"] = sid
    elif kind == "catalog":
        if sid != str(CATALOG_ID):
            return None
    else:
        return None
    return cols
