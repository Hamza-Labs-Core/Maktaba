"""Finishing a job and pushing its change downstream (plan 06-11 D4 and section 6.1).

A successful job whose output hash differs from the previous successful run of
the same subject and stage enqueues its graph dependents, **in the same
transaction** as ``done``. An unchanged output enqueues nothing. A trigger that
arrived while the job ran (``rerun_requested``) becomes exactly one follow-up
row, inserted in that same transaction.
"""

from __future__ import annotations

from collections import Counter
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from typing import Any
from uuid import UUID

from ..db import claim_owner
from ..db.jobs import Job
from .enqueue import EnqueueResult, enqueue_job
from .events import job_ref, log_event, publish
from .graph import (
    CATALOG_ID,
    EPIC47_PRIORITIES,
    Edge,
    GraphViolation,
    Request,
    StageDef,
    SubjectKind,
)
from .sql import dumps, loads, now, to_ts, uid
from .subjects import library_of_video, season_of_video

__all__ = [
    "FinishResult",
    "after_legacy_done",
    "enqueue_follow_up",
    "finalize_success",
    "last_success",
    "resolve_edge",
]

#: outcomes that count as "the last successful run" for skip checks and change detection
_SUCCESS_OUTCOMES = ("changed", "unchanged", "skipped_unchanged", "imported")


@dataclass(frozen=True, slots=True)
class FinishResult:
    outcome: str
    changed: bool
    triggered: tuple[dict[str, Any], ...]
    follow_up: EnqueueResult | None = None


async def last_success(db: Any, job: Job) -> Any | None:
    """The newest earlier ``done`` row of the same subject and stage that counts as a success."""
    outs = ",".join(f"'{o}'" for o in _SUCCESS_OUTCOMES)
    return await db.fetchrow(
        "SELECT id, stage_version, input_hash, output_hash FROM processing_jobs"
        " WHERE subject_kind = $1 AND subject_id = $2 AND stage = $3 AND state = 'done'"
        f" AND id <> $4 AND outcome IN ({outs}) ORDER BY id DESC LIMIT 1",
        job.subject_kind,
        uid(job.subject.id),
        job.stage,
        job.id,
    )


# ------------------------------------------------------------------ edge mapping


async def resolve_edge(
    db: Any,
    edge: Edge,
    job: Job,
    changed_subjects: Sequence[tuple[SubjectKind, UUID]],
) -> list[tuple[SubjectKind, UUID]]:
    """Target subjects of one edge, per the mapping table of plan section 3.3."""
    subj = job.subject
    m = edge.map
    if m == "same":
        return [(subj.kind, subj.id)]
    if m == "same_video":
        return [(subj.kind, subj.id)] if subj.kind == "video" else []
    if m == "catalog":
        return [("catalog", CATALOG_ID)]
    if m == "library_of":
        lib = job.library_id
        if lib is None and subj.kind == "library":
            lib = subj.id  # a library's own job: its library is itself
        if lib is None and subj.kind == "video":
            lib = await library_of_video(db, subj.id)
        return [] if lib is None else [("library", lib)]
    if m == "loose_library_of":
        # loose as the library tag counts it: in no series at all
        if subj.kind != "video" or await db.fetchval(
            "SELECT count(*) FROM series_episodes WHERE video_id = $1", uid(subj.id)
        ):
            return []
        lib = job.library_id or await library_of_video(db, subj.id)
        return [] if lib is None else [("library", lib)]
    if m == "season_of":
        if subj.kind != "video":
            return []
        sid = await season_of_video(db, subj.id)
        return [] if sid is None else [("season", sid)]
    if m == "seasons_changed":
        return [(k, i) for k, i in changed_subjects if k == "season"]
    if m == "payload_seasons":
        seasons = (job.payload or {}).get("seasons") or []
        return [("season", UUID(str(s))) for s in seasons]
    if m == "videos_of":
        if subj.kind != "library":
            return []
        named: list[tuple[SubjectKind, UUID]] = [
            (k, i) for k, i in changed_subjects if k == "video"
        ]
        if named:
            return named
        return [("video", v) for v in await _unlinked_videos(db, subj.id)]
    return []


async def _unlinked_videos(db: Any, library_id: UUID) -> list[UUID]:
    """Videos of a library with no YouTube link yet (what ``youtube_sync -> link`` needs)."""
    rows = await db.fetch(
        "SELECT v.id AS id FROM videos v WHERE v.library_id = $1 AND v.deleted_at IS NULL"
        " AND NOT EXISTS (SELECT 1 FROM media_metadata_enrichment m"
        "  WHERE m.video_id = v.id AND m.provider = 'youtube') ORDER BY v.id",
        uid(library_id),
    )
    return [UUID(str(r["id"])) for r in rows]


# ------------------------------------------------------------------ finishing


async def finalize_success(
    db: Any,
    job: Job,
    stage: StageDef,
    graph: Mapping[str, StageDef],
    *,
    output_hash: str,
    outcome: str | None,
    input_hash: str | None,
    changed_subjects: Sequence[tuple[SubjectKind, UUID]] = (),
    requests: Sequence[Request] = (),
    summary: dict[str, Any] | None = None,
    skipped_from: Any | None = None,
    already_done: bool = False,
) -> FinishResult | None:
    """Mark ``job`` done and run the propagation, all in one transaction.

    ``skipped_from`` (the previous success row) turns this into a
    ``skipped_unchanged`` finish. ``already_done`` is the legacy path: the
    handler already set ``state='done'``; only the engine columns, the
    dependents and the follow-up are written. Returns None when the job was no
    longer active (cancelled or reaped meanwhile).
    """
    async with db.transaction():
        prev = skipped_from if skipped_from is not None else await last_success(db, job)
        if skipped_from is not None:
            output_hash = str(skipped_from["output_hash"])
            final_outcome = "skipped_unchanged"
            changed = False
        elif outcome in ("awaiting_review", "waiting_inputs"):
            final_outcome = outcome
            changed = False
        else:
            changed = job.force_cascade or prev is None or str(prev["output_hash"]) != output_hash
            final_outcome = outcome or ("changed" if changed else "unchanged")
        t = now()
        ts = to_ts(db, t)
        # a job still running: only while this worker holds its claim (db/claim_owner.py)
        active = (
            "state = 'done' AND propagated_at IS NULL"
            if already_done
            else f"state IN ('claimed','running','resuming') AND {claim_owner.guard(db, 8)}"
        )
        owner: tuple[Any, ...] = () if already_done else (claim_owner.current(),)
        row = await db.fetchrow(
            "UPDATE processing_jobs SET state = 'done', finished_at = COALESCE(finished_at, $2),"
            " stage_version = $3, input_hash = $4, output_hash = $5, output_summary = $6,"
            " outcome = $7, propagated_at = $2, claimed_by = NULL"
            f" WHERE id = $1 AND {active}"
            " RETURNING rerun_requested, rerun_force, rerun_force_cascade, rerun_reasons, payload,"
            " started_at, created_at",
            job.id,
            ts,
            stage.version,
            input_hash,
            output_hash,
            None if summary is None else dumps(summary),
            final_outcome,
            *owner,
        )
        if row is None:
            return None

        triggered: list[dict[str, Any]] = []
        reason = {
            "why": f"{stage.name} output changed",
            "by": f"job:{job.id}",
            "from_stage": stage.name,
        }
        # an edge declared when="always" fires on every finished run, changed or not
        # (not while a run waits for review or inputs)
        settled = final_outcome not in ("awaiting_review", "waiting_inputs")
        if changed or settled:
            for edge in stage.edges:
                if edge.legacy or not (changed or edge.when == "always"):
                    continue
                target = graph.get(edge.to)
                if target is None:
                    continue
                for kind, sid in await resolve_edge(db, edge, job, changed_subjects):
                    if kind not in target.subject_kinds:
                        continue
                    res = await enqueue_job(
                        db,
                        subject_kind=kind,
                        subject_id=sid,
                        stage=edge.to,
                        force_cascade=job.force_cascade,
                        reason=reason,
                        triggered_by=job.id,
                    )
                    triggered.append(_trig(edge.to, kind, sid, res))
        for req in requests:
            if req.stage not in stage.may_request:
                raise GraphViolation(
                    f"{stage.name} requested {req.stage!r}, not in may_request {stage.may_request}"
                )
            res = await enqueue_job(
                db,
                subject_kind=req.subject_kind,
                subject_id=req.subject_id,
                stage=req.stage,
                priority=req.priority,
                payload=req.payload,
                reason={"why": req.reason, "by": f"job:{job.id}", "from_stage": stage.name},
                triggered_by=job.id,
            )
            triggered.append(
                _trig(req.stage, req.subject_kind, req.subject_id, res, requested=True)
            )

        follow = await enqueue_follow_up(db, job, row)

        await _log_finish(db, job, stage, final_outcome, triggered, row, t)
        return FinishResult(final_outcome, changed, tuple(triggered), follow)


def _trig(
    stage: str, kind: str, sid: UUID | str, res: EnqueueResult, *, requested: bool = False
) -> dict[str, Any]:
    return {
        "stage": stage,
        "subject_kind": kind,
        "subject_id": str(sid),
        "outcome": res.outcome,
        "requested": requested,
    }


async def enqueue_follow_up(db: Any, job: Job, row: Any) -> EnqueueResult | None:
    """Insert the single follow-up row for a trigger that arrived mid-run."""
    if not row["rerun_requested"]:
        return None
    reasons = loads(row["rerun_reasons"]) or []
    return await enqueue_job(
        db,
        subject_kind=job.subject_kind,
        subject_id=job.subject.id,
        stage=job.stage,
        # an Epic 47 stage keeps its place (the back catalogue's next batch stays at 300)
        priority=job.priority if job.stage in EPIC47_PRIORITIES else None,
        force=bool(row["rerun_force"]),
        force_cascade=bool(row["rerun_force_cascade"]),
        reason={
            "why": "changed while running",
            "by": f"job:{job.id}",
            "from_stage": job.stage,
            "reasons": reasons[:5],
        },
        triggered_by=job.id,
        # the row's payload: a trigger that arrived mid-run merged its seasons into it
        # after this job was claimed (job.payload is the claim-time copy)
        payload=loads(row["payload"]) if row["payload"] is not None else job.payload,
    )


async def _log_finish(
    db: Any,
    job: Job,
    stage: StageDef,
    outcome: str,
    triggered: list[dict[str, Any]],
    row: Any,
    finished: Any,
) -> None:
    common: dict[str, Any] = {
        "job_id": job.id,
        "subject_kind": job.subject_kind,
        "subject_id": job.subject.id,
        "library_id": job.library_id,
        "stage": stage.name,
    }
    if outcome == "skipped_unchanged":
        await log_event(
            db, **common, level="debug", kind="skipped", message="inputs unchanged; skipped"
        )
        return
    await log_event(db, **common, level="info", kind="done", message=f"{stage.label}: {outcome}")
    if triggered:
        await log_event(
            db,
            **common,
            level="info",
            kind="triggered" if not all(t["requested"] for t in triggered) else "requested",
            message="queued " + ", ".join(sorted({t["stage"] for t in triggered})),
            data={"to": triggered[:100]},
        )
    started = row["started_at"] or row["created_at"]
    from .sql import parse_ts

    st = parse_ts(started)
    duration_ms = int((finished - st).total_seconds() * 1000) if st is not None else 0
    counts = Counter(t["stage"] for t in triggered)
    ref = await job_ref(db, job.id)
    await publish(
        db,
        "job.finished",
        {
            "job": ref,
            "duration_ms": max(0, duration_ms),
            "triggered": [{"stage": s, "count": n} for s, n in counts.items()],
        },
    )


# ------------------------------------------------------------------ legacy path


async def after_legacy_done(
    db: Any, job_id: int, graph: Mapping[str, StageDef]
) -> FinishResult | None:
    """Engine bookkeeping for a legacy handler that already marked its job ``done``.

    Computes the stage's output fingerprint, records ``output_hash`` /
    ``outcome`` / ``propagated_at``, propagates the non-legacy edges and inserts
    the follow-up, all in one transaction. A crash before this runs is repaired
    by the reconciler (``propagated_at IS NULL``).
    """
    from .claim import row_to_job
    from .hashing import canonical_hash

    row = await db.fetchrow("SELECT * FROM processing_jobs WHERE id = $1", job_id)
    if row is None or row["state"] != "done" or row["propagated_at"] is not None:
        return None
    job = row_to_job(row)
    stage = graph.get(job.stage)
    if stage is None:
        return None
    if stage.output_fingerprint is not None:
        out = await stage.output_fingerprint(db, job.subject)
    else:
        out = canonical_hash(str(row["finished_at"]))
    return await finalize_success(
        db,
        job,
        stage,
        graph,
        output_hash=out,
        outcome=None,
        input_hash=None,
        already_done=True,
    )
