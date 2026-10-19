"""The daily garbage collection of staging (story 33.3 AC8, design 4.7).

Deleted: the blocks and generator state of every schedule generation that is neither
live, nor the in-flight change's, nor the previous one within
``rollback_retention_hours`` of being superseded; ``retired`` channels past that
retention (their programmes go with them); ``staged`` channels no open change's release
still names (a failed or cancelled change's additions). Releases themselves are kept
(a few rows each): they are the record of what aired.
"""

from __future__ import annotations

from datetime import datetime, timedelta
from typing import Any

from .. import rules
from ..channels.repo import live_gen
from ..engine.sql import loads, now, parse_ts, to_ts
from ..log import get_logger
from .store import IN_FLIGHT

__all__ = ["collect"]

_log = get_logger()


async def _kept_generations(db: Any, clock: datetime, retention: timedelta) -> set[int]:
    keep = {await live_gen(db)}
    marks = ",".join(f"'{s}'" for s in (*IN_FLIGHT, "draft"))
    for r in await db.fetch(
        "SELECT r.sched_gen AS g FROM tv_changes c JOIN tv_releases r ON r.id = c.release_id"
        f" WHERE c.state IN ({marks})"
    ):
        keep.add(int(r["g"]))
    route = await db.fetchrow(
        "SELECT previous_gen, switched_at FROM tv_routing WHERE scope = 'cluster'"
    )
    if route is not None and route["previous_gen"] is not None:
        switched = parse_ts(route["switched_at"])
        if switched is None or switched + retention > clock:
            keep.add(int(route["previous_gen"]))
    return keep


async def collect(db: Any, *, clock: datetime | None = None) -> dict[str, int]:
    """One sweep; returns what was deleted."""
    clock = clock or now()
    cfg, _, _ = await rules.read(db, "tv_staging")
    retention = timedelta(hours=int(cfg.rollback_retention_hours))
    keep = await _kept_generations(db, clock, retention)
    gens = [
        int(r["g"]) for r in await db.fetch("SELECT DISTINCT sched_gen AS g FROM channel_programs")
    ]
    gens += [
        int(r["g"])
        for r in await db.fetch("SELECT DISTINCT sched_gen AS g FROM channel_schedule_state")
    ]
    out = {"generations": 0, "programs": 0, "retired_channels": 0, "staged_channels": 0}
    for g in sorted(set(gens) - keep):
        n = await db.fetchval("SELECT count(*) FROM channel_programs WHERE sched_gen = $1", g)
        await db.execute("DELETE FROM channel_programs WHERE sched_gen = $1", g)
        await db.execute("DELETE FROM channel_schedule_state WHERE sched_gen = $1", g)
        out["generations"] += 1
        out["programs"] += int(n or 0)
    cut = to_ts(db, clock - retention)
    for r in await db.fetch(
        "SELECT id FROM channels WHERE lifecycle = 'retired' AND retired_at < $1", cut
    ):
        await db.execute("DELETE FROM channels WHERE id = $1", str(r["id"]))
        out["retired_channels"] += 1
    wanted: set[str] = set()
    marks = ",".join(f"'{s}'" for s in IN_FLIGHT)
    for r in await db.fetch(
        "SELECT rc.channel_id AS cid FROM tv_changes c"
        " JOIN tv_release_channels rc ON rc.release_id = c.release_id"
        f" WHERE c.state IN ({marks})"
    ):
        wanted.add(str(r["cid"]))
    for r in await db.fetch("SELECT diff FROM tv_changes WHERE state = 'draft'"):
        pending = (loads(r["diff"]) or {}).get("pending") or {}
        wanted |= {str(k) for k in (pending.get("channels") or {})}
    for r in await db.fetch("SELECT id FROM channels WHERE lifecycle = 'staged'"):
        if str(r["id"]) not in wanted:
            await db.execute("DELETE FROM channels WHERE id = $1", str(r["id"]))
            out["staged_channels"] += 1
    _log.info("tv_gc", **out)
    return out
