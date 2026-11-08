"""``detect``: place a library's episodes into series and seasons (plan 06-11 sections 3.4,
4.4), in ``tv.maktaba.classify`` (Epic 54 wave 4, story 54.31).

The run: load the library, plan it in a thread (the pure detector), diff the plan against
what is stored and write it (or hand it to review), then ask for the fingerprints the
two-pass flow needs. Loading, planning and writing are the pipeline's
(``engine/stages/_detect.py``), shared with mediaintel's import.
"""

from __future__ import annotations

import asyncio
from collections import defaultdict
from dataclasses import dataclass, field
from typing import Any
from uuid import UUID

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.changes import ChangeSet, apply_or_review
from maktaba_pipeline.engine.context import StageContext
from maktaba_pipeline.engine.graph import Request, StageResult
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.stages._detect import (
    LibraryPlan,
    Row,
    _playlists,
    apply_changes,
    compute_changes,
    input_fingerprint,
    load_current,
    load_rows,
    output_hash,
    plan_library,
)
from maktaba_pipeline.engine.stages.registry import StageImpl

__all__ = ["IMPL"]


@dataclass
class _Progress:
    done: int = 0
    total: int = 0
    seen: list[tuple[int, int]] = field(default_factory=list)

    def __call__(self, done: int, total: int) -> None:
        self.done, self.total = done, total


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id = job.subject.id
    rows = await load_rows(db, library_id)
    playlists = await _playlists(db, library_id)
    prog = _Progress()
    task = asyncio.create_task(_report_progress(ctx, prog))
    try:
        detect_cfg = await ctx.rules("detect")
        plan = await asyncio.to_thread(
            plan_library, library_id, rows, playlists, config=detect_cfg, progress=prog
        )
    finally:
        task.cancel()
    current = await load_current(db, library_id)
    changes, touched = compute_changes(plan, current)

    async def apply(_cs: ChangeSet) -> None:
        await apply_changes(db, library_id, plan, current)

    if changes.is_empty:
        # nothing visible changed, but dates / fingerprint plans / metadata may still have:
        # write silently so stored state matches the plan
        await apply_changes(db, library_id, plan, current)
        mode = "noop"
    else:
        mode = await apply_or_review(ctx, changes, apply)
    if mode == "review":
        return StageResult(
            output_hash=canonical_hash("review"),
            outcome="awaiting_review",
            summary={"changes": changes.counts},
        )

    rowmap = {r.id: r for r in rows}
    requests = _fingerprint_requests(plan, rowmap)
    out = output_hash(plan.seasons)
    return StageResult(
        output_hash=out,
        changed_subjects=tuple(touched),  # type: ignore[arg-type]
        requests=tuple(requests),
        summary={
            "placed": plan.episode_count,
            "seasons": len(plan.seasons),
            "seasons_changed": len(touched),
            "unassigned": len(plan.unassigned),
            "shorts": len(plan.shorts),
            "changes": changes.counts,
        },
    )


async def _report_progress(ctx: StageContext, prog: _Progress) -> None:
    try:
        while True:
            await asyncio.sleep(2.0)
            if prog.total:
                await ctx.progress("matching", prog.done, prog.total, force=True)
    except asyncio.CancelledError:
        return


def _fingerprint_requests(plan: LibraryPlan, rows: dict[UUID, Row]) -> list[Request]:
    """Pass 1 of the two-pass flow: sample fingerprints per season, then the gate."""
    samples: dict[UUID, set[str]] = defaultdict(set)
    gates: list[Request] = []
    for s in plan.seasons:
        if not s.fp_plan or not s.fp_plan["candidates"]:
            continue
        for v in s.fp_plan["samples"]:
            row = rows.get(UUID(v))
            if row is not None and not row.fingerprinted:
                samples[UUID(v)].add(str(s.id))
        gates.append(Request("fp_gate", "season", s.id, f"unplaced candidates for {s.series_name}"))
    reqs = [
        Request(
            "fingerprint",
            "video",
            vid,
            "sample of a season with unplaced videos",
            payload={"role": "sample", "seasons": sorted(seasons)},
        )
        for vid, seasons in sorted(samples.items(), key=lambda kv: str(kv[0]))
    ]
    return reqs + gates


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
