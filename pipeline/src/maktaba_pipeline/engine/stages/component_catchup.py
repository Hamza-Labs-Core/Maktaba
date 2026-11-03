"""``component_catchup`` (Epic 51, story 51.06): a component enabled again gets the jobs its
stages missed while it was off.

While a component is off its stages make no jobs: an upstream job that finishes logs the
edge as ``skipped_component_off`` (``engine/propagate.py``). When the component comes back
its leader (``components/runner.py``) enqueues this job with the payload
``{"components": [{"component": "<id>", "off_since": "<ts>" | null}]}`` (entries of two
components enabled at once are merged into one job, slot 0176).

For each component's stage S and each graph edge U -> S, the ``done`` jobs of U with a
success outcome that finished since ``off_since`` (all of them when it is unknown) are
walked in batches of :data:`BATCH` by id, and the propagation runs again for each,
restricted to S (``enqueue_dependents(..., only=S)``) at :data:`PRIORITY`. An upstream job
that already fed S (an S job with ``triggered_by`` it) is passed over, and the enqueue's
"already queued" rule coalesces the rest, so a second catch-up adds nothing. An edge that
answers ``skipped_component_off`` (the component went off again) ends that component's
walk: the next enable starts a new catch-up.
"""

from __future__ import annotations

from collections.abc import Mapping
from datetime import datetime
from typing import Any

from ...db.jobs import Job
from ..claim import row_to_job
from ..context import StageContext
from ..graph import StageDef, StageResult
from ..hashing import canonical_hash
from ..propagate import SKIPPED_COMPONENT_OFF, enqueue_dependents
from ..sql import parse_ts, to_ts
from .registry import StageImpl

__all__ = ["BATCH", "IMPL", "PRIORITY", "catch_up", "entries_of"]

BATCH = 500
#: bulk work: behind new media (100) and a library conversion (150)
PRIORITY = 500

#: the upstream outcomes whose edges fire: a change (every edge) ...
_CHANGED = ("changed", "imported")
#: ... or any settled success (the ``when="always"`` edges only)
_SETTLED = ("changed", "unchanged", "skipped_unchanged", "imported")


def entries_of(payload: Mapping[str, Any] | None) -> dict[str, datetime | None]:
    """component -> since when it was off (None: unknown, all history). Two entries of
    one component keep the earlier time, and an unknown time wins."""
    out: dict[str, datetime | None] = {}
    for e in (payload or {}).get("components") or []:
        if not isinstance(e, Mapping) or not e.get("component"):
            continue
        cid = str(e["component"])
        since = parse_ts(e.get("off_since")) if e.get("off_since") else None
        if cid in out:
            prev = out[cid]
            since = None if prev is None or since is None else min(prev, since)
        out[cid] = since
    return out


def _feeds(graph: Mapping[str, StageDef], stage: str) -> list[tuple[StageDef, bool]]:
    """The stages with an engine edge into ``stage``; True when every such edge fires
    only on a change (False: one is ``when="always"``)."""
    out: list[tuple[StageDef, bool]] = []
    for up in graph.values():
        edges = [e for e in up.edges if e.to == stage and not e.legacy]
        if edges:
            out.append((up, all(e.when != "always" for e in edges)))
    return out


def _where(on_change: bool, since: bool, first: int) -> str:
    outs = ",".join(f"'{o}'" for o in (_CHANGED if on_change else _SETTLED))
    return (
        "j.stage = $1 AND j.state = 'done'"
        f" AND j.outcome IN ({outs})"
        + (f" AND j.finished_at >= ${first}" if since else "")
        + " AND NOT EXISTS (SELECT 1 FROM processing_jobs k"
        "  WHERE k.stage = $2 AND k.triggered_by = j.id)"
    )


async def catch_up(
    db: Any,
    graph: Mapping[str, StageDef],
    entries: Mapping[str, datetime | None],
    *,
    job: Job | None = None,
    ctx: StageContext | None = None,
) -> dict[str, dict[str, int]]:
    """Run the catch-up for ``entries`` (component -> off since); per component the
    counts ``{"queued", "coalesced", "upstream", "skipped"}``."""
    from ...components.catalog import default

    cat = default()
    plan: list[tuple[str, StageDef, StageDef, bool, datetime | None]] = []
    for cid, since in sorted(entries.items()):
        comp = cat.get(cid)
        if comp is None:
            continue
        for stage in comp.stages:
            if stage not in graph:
                continue
            for up, on_change in _feeds(graph, stage):
                plan.append((cid, up, graph[stage], on_change, since))
    total = 0
    for _cid, up, target, on_change, since in plan:
        args: list[Any] = [up.name, target.name]
        if since is not None:
            args.append(to_ts(db, since))
        sql = "SELECT count(*) FROM processing_jobs j WHERE " + _where(
            on_change, since is not None, 3
        )
        total += int(await db.fetchval(sql, *args) or 0)
    done = 0
    out: dict[str, dict[str, int]] = {
        cid: {"queued": 0, "coalesced": 0, "upstream": 0, "skipped": 0} for cid in entries
    }
    stopped: set[str] = set()
    by = f"job:{job.id}" if job is not None else "component_catchup"
    for cid, up, target, on_change, since in plan:
        counts = out[cid]
        last = 0
        sql = (
            "SELECT * FROM processing_jobs j WHERE "
            + _where(on_change, since is not None, 4)
            + f" AND j.id > $3 ORDER BY j.id LIMIT {BATCH}"
        )
        while cid not in stopped:
            args = [up.name, target.name, last]
            if since is not None:
                args.append(to_ts(db, since))
            rows = await db.fetch(sql, *args)
            if not rows:
                break
            async with db.transaction():
                for r in rows:
                    last = int(r["id"])
                    counts["upstream"] += 1
                    trig = await enqueue_dependents(
                        db,
                        row_to_job(r),
                        up,
                        graph,
                        changed=str(r["outcome"]) in _CHANGED,
                        only=target.name,
                        priority=PRIORITY,
                        reason={
                            "why": f"{cid} is on again: catching up",
                            "by": by,
                            "from_stage": up.name,
                        },
                    )
                    for t in trig:
                        if t["outcome"] == SKIPPED_COMPONENT_OFF:
                            counts["skipped"] += 1
                            stopped.add(cid)
                        elif t["outcome"] == "inserted":
                            counts["queued"] += 1
                        elif t["outcome"] in ("coalesced", "rerun_requested"):
                            counts["coalesced"] += 1
                    if cid in stopped:
                        break
            done += len(rows)
            if ctx is not None:
                await ctx.progress("catch_up", min(done, total), total)
    if ctx is not None:
        await ctx.progress("catch_up", total, total, force=True)
    return out


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    out = await catch_up(db, ctx.graph, entries_of(job.payload), job=job, ctx=ctx)
    return StageResult(
        output_hash=canonical_hash(["component_catchup", job.id, out]),
        summary={"components": out},
    )


IMPL = StageImpl(run=run)
