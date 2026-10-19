"""Schedule generations (story 33.3, design 4.3 - 4.4).

Generation N+1 of a staged release is generation N copied, then regenerated after each
changed channel's divergence boundary ``B_c`` - the end of N's block on air at the
planned swap instant ``T``. So N and N+1 are the same before ``B_c``: a switch at any
moment before it changes the encoder, never the programme (nothing is cut).

The regeneration is the scheduler's own stale rewrite (``run_schedule`` with an
``anchor``): N+1 starts as a copy of N with N's cursor, so each queue carries on from
the first programme the rewrite drops, exactly as a stale channel's does today.
"""

from __future__ import annotations

from datetime import datetime, timedelta
from typing import Any
from uuid import UUID

from ..channels.repo import ReleaseView, SqlRepo, live_gen
from ..engine.sql import is_pg, loads, now, parse_ts
from ..log import get_logger
from .build import Release, filler_item_id, load_release

__all__ = [
    "planned_swap",
    "boundary",
    "build_generation",
    "open_generations",
    "pool_overlay",
    "reanchor",
    "release_view",
]

_log = get_logger()

#: a uuid4-shaped text id on SQLite (no gen_random_uuid there)
_SQLITE_UUID = (
    "lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)), 2)"
    " || '-' || substr('89ab', 1 + (abs(random()) % 4), 1) || substr(hex(randomblob(2)), 2)"
    " || '-' || hex(randomblob(6)))"
)
#: the advisory lock the daily clean-up holds (shared by every generation build)
GC_LOCK = "tv_gc"
_PROGRAM_COLS = (
    "channel_id, seq, kind, video_id, filler_item_id, start_at, end_at, source_offset,"
    " source_duration, title_snapshot, source_url"
)


async def pool_overlay(db: Any, pending: dict[str, Any]) -> dict[str, list[tuple[str, str, int]]]:
    """The filler pools a pending ``write_channels`` change writes, as they will be after
    the swap: ``key -> [(filler item id, video id, ms)]`` (kept clips keep their ids, new
    ones get the ids the swap will insert them with)."""
    out: dict[str, list[tuple[str, str, int]]] = {}
    pools = pending.get("pools") or {}
    for pw in pools.get("write") or []:
        key = str(pw["key"])
        items: dict[str, int] = {str(k): int(v) for k, v in (pw.get("items") or {}).items()}
        have: dict[str, tuple[str, int]] = {}
        if pw.get("pool_id"):
            for r in await db.fetch(
                "SELECT id, video_id, duration_ms FROM filler_items WHERE pool_id = $1",
                pw["pool_id"],
            ):
                have[str(r["video_id"])] = (str(r["id"]), int(r["duration_ms"] or 0))
        clips: list[tuple[str, str, int]] = []
        for vid, ms in sorted(items.items()):
            fid = have[vid][0] if vid in have else filler_item_id(key, vid)
            clips.append((fid, vid, ms))
        out[key] = clips
    for key, _ in pools.get("delete") or []:
        out[str(key)] = []
    return out


async def release_view(db: Any, rel: Release, pending: dict[str, Any] | None = None) -> ReleaseView:
    return ReleaseView(
        defs=rel.defs,
        tv=dict(rel.settings.get("tv") or {}),
        pools=await pool_overlay(db, pending or {}),
        filler=dict((pending or {}).get("filler") or {}),
    )


async def boundary(db: Any, channel_id: str, gen: int, at: datetime) -> datetime:
    """``B_c``: the end of the block of generation ``gen`` on air at ``at`` (``at``
    itself when nothing is on air there)."""
    v = await db.fetchval(
        "SELECT max(end_at) FROM channel_programs WHERE channel_id = $1 AND sched_gen = $2"
        " AND start_at <= $3 AND end_at > $3",
        channel_id,
        gen,
        at,
    )
    got = parse_ts(v)
    return got if got is not None and got > at else at


async def max_wait(db: Any) -> timedelta:
    """``boundary_max_wait_minutes`` (``tv_staging``): how long a swap waits at most
    for the programme on the air to end (0: no cap)."""
    from .. import rules

    cfg, _, _ = await rules.read(db, "tv_staging")
    return timedelta(minutes=max(0, int(cfg.boundary_max_wait_minutes)))


async def swap_boundary(
    db: Any, channel_id: str, gen: int, at: datetime, cap: timedelta
) -> tuple[datetime, bool]:
    """Where a channel switches for a swap at ``at``: the end of the programme on the
    air (:func:`boundary`), at most ``cap`` later - the same instant the Changes page
    shows and the front door switches at (README ``boundary_max_wait_minutes``).
    Returns ``(instant, capped)``."""
    b = await boundary(db, channel_id, gen, at)
    if cap > timedelta(0) and b > at + cap:
        return at + cap, True
    return b, False


async def _copy_channel(db: Any, channel_id: str, src: int, dst: int) -> int:
    """Generation ``dst`` of a channel := a copy of generation ``src`` (blocks and the
    generator state). Returns the blocks copied."""
    await db.execute(
        "DELETE FROM channel_programs WHERE channel_id = $1 AND sched_gen = $2", channel_id, dst
    )
    await db.execute(
        "DELETE FROM channel_schedule_state WHERE channel_id = $1 AND sched_gen = $2",
        channel_id,
        dst,
    )
    new_id = "gen_random_uuid()" if is_pg(db) else _SQLITE_UUID
    await db.execute(
        f"INSERT INTO channel_programs (id, {_PROGRAM_COLS}, sched_gen)"
        f" SELECT {new_id}, {_PROGRAM_COLS}, $3 FROM channel_programs"
        " WHERE channel_id = $1 AND sched_gen = $2",
        channel_id,
        src,
        dst,
    )
    await db.execute(
        "INSERT INTO channel_schedule_state (channel_id, sched_gen, anchor_at, horizon_until,"
        " last_generated_at, generator_version, cursor, stale)"
        " SELECT channel_id, $3, anchor_at, horizon_until, last_generated_at, generator_version,"
        " cursor, stale FROM channel_schedule_state WHERE channel_id = $1 AND sched_gen = $2",
        channel_id,
        src,
        dst,
    )
    return int(
        await db.fetchval(
            "SELECT count(*) FROM channel_programs WHERE channel_id = $1 AND sched_gen = $2",
            channel_id,
            dst,
        )
        or 0
    )


async def _build_channel(
    db: Any,
    rel: Release,
    view: ReleaseView,
    channel_id: str,
    *,
    base_gen: int,
    at: datetime,
    clock: datetime,
    cap: timedelta = timedelta(0),
) -> datetime | None:
    """One channel of generation N+1, in its own transaction (as a top-up writes one
    channel: short locks). Returns the divergence boundary of a regenerated channel."""
    from ..channels.pass_ import TOPUP_ADVISORY_LOCK_KEY, lock_key, run_schedule

    async with db.transaction():
        if is_pg(db):
            # the top-up extends this generation too (open_generations): its (channel,
            # generation) lock keeps a sweep from appending while the copy is rewritten;
            # the clean-up's lock keeps a sweep from deleting it half built
            await db.execute(
                "SELECT pg_advisory_xact_lock($1, $2)",
                TOPUP_ADVISORY_LOCK_KEY,
                lock_key(UUID(channel_id), rel.sched_gen),
            )
            await db.execute("SELECT pg_advisory_xact_lock_shared(hashtext($1))", GC_LOCK)
        copied = await _copy_channel(db, channel_id, base_gen, rel.sched_gen)
        if channel_id not in rel.sched_changed:
            return None
        repo = SqlRepo(db, gen=rel.sched_gen, release=view)
        if copied == 0:
            # a channel generation N does not have (an addition): it starts now
            b = clock
        else:
            b, capped = await swap_boundary(db, channel_id, base_gen, at, cap)
            if capped:
                # the programme on the air runs past boundary_max_wait: in this
                # generation it ends at the cap, where the new schedule starts
                await db.execute(
                    "UPDATE channel_programs SET end_at = $3 WHERE channel_id = $1"
                    " AND sched_gen = $2 AND start_at < $3 AND end_at > $3",
                    channel_id,
                    rel.sched_gen,
                    b,
                )
        await run_schedule(repo, UUID(channel_id), now=max(at, clock), anchor=b)
        await db.execute(
            "UPDATE tv_release_channels SET boundary_at = $3 WHERE release_id = $1"
            " AND channel_id = $2",
            rel.id,
            channel_id,
            b,
        )
        return b


async def build_generation(
    db: Any,
    rel: Release,
    *,
    at: datetime,
    pending: dict[str, Any] | None = None,
    clock: datetime | None = None,
) -> dict[str, str]:
    """Write generation ``rel.sched_gen`` for every channel of the release, planned for
    a swap at ``at``. Returns ``channel id -> divergence boundary`` (ISO) of the
    regenerated channels (AC3)."""
    clock = clock or now()
    base_gen = await live_gen(db)
    view = await release_view(db, rel, pending)
    out: dict[str, str] = {}
    cap = await max_wait(db)
    for cid in sorted(rel.defs):
        try:
            b = await _build_channel(
                db, rel, view, cid, base_gen=base_gen, at=at, clock=clock, cap=cap
            )
        except Exception:
            _log.exception("tv.generation_channel_failed", channel_id=cid, gen=rel.sched_gen)
            raise
        if b is not None:
            out[cid] = b.isoformat()
    _log.info(
        "tv.generation_built", gen=rel.sched_gen, release=rel.id, regenerated=len(out),
        channels=len(rel.defs),
    )  # fmt: skip
    return out


async def reanchor(
    db: Any,
    rel: Release,
    *,
    at: datetime,
    pending: dict[str, Any] | None = None,
    clock: datetime | None = None,
) -> dict[str, str]:
    """Re-anchor generation N+1 at a new swap instant ``at`` (AC4): every regenerated
    channel whose boundary moved is rebuilt - N copied again, the tail regenerated after
    the new boundary. A channel whose boundary did not move is left as it is."""
    clock = clock or now()
    base_gen = await live_gen(db)
    view = await release_view(db, rel, pending)
    out: dict[str, str] = {}
    rows = await db.fetch(
        "SELECT channel_id, boundary_at FROM tv_release_channels WHERE release_id = $1"
        " AND sched_changed = $2",
        rel.id,
        True if is_pg(db) else 1,
    )
    cap = await max_wait(db)
    for r in rows:
        cid = str(r["channel_id"])
        old = parse_ts(r["boundary_at"])
        new, _ = await swap_boundary(db, cid, base_gen, at, cap)
        if old is not None and old == new:
            continue
        b = await _build_channel(db, rel, view, cid, base_gen=base_gen, at=at, clock=clock, cap=cap)
        if b is not None:
            out[cid] = b.isoformat()
    return out


async def open_generations(db: Any, at: datetime) -> list[tuple[bool, SqlRepo]]:
    """The generations the top-up extends (R3.4): the live one, the in-flight change's,
    the previous one while it is kept for a rollback. ``(is_live, repo)`` each."""
    gen = await live_gen(db)
    out: list[tuple[bool, SqlRepo]] = [(True, SqlRepo(db, gen=gen))]
    try:
        rows = await db.fetch(
            "SELECT r.id AS id, r.sched_gen AS g, c.diff AS diff FROM tv_changes c"
            " JOIN tv_releases r ON r.id = c.release_id"
            " WHERE c.state IN ('staged','verifying','ready','approved','swapping')"
        )
        prev = await db.fetchrow(
            "SELECT previous_release_id, previous_gen, previous_until FROM tv_routing"
            " WHERE scope = 'cluster'"
        )
    except Exception as exc:  # noqa: BLE001 - a database before slot 0127
        if "no such table" not in str(exc).lower() and "does not exist" not in str(exc):
            raise
        return out
    seen = {gen}
    wanted: list[tuple[str, dict[str, Any]]] = [
        (str(r["id"]), (loads(r["diff"]) or {}).get("pending") or {})
        for r in rows
        if int(r["g"]) not in seen
    ]
    if prev is not None and prev["previous_release_id"] is not None:
        until = parse_ts(prev["previous_until"])
        if until is not None and until > at and int(prev["previous_gen"] or 0) not in seen:
            wanted.append((str(prev["previous_release_id"]), {}))
    for rid, pending in wanted:
        rel = await load_release(db, rid)
        if rel is None or rel.sched_gen in seen:
            continue
        seen.add(rel.sched_gen)
        out.append(
            (False, SqlRepo(db, gen=rel.sched_gen, release=await release_view(db, rel, pending)))
        )
    return out


def planned_swap(swap_at: datetime | None, soak_seconds: int, clock: datetime) -> datetime:
    """``T``: the planned swap instant a generation is built for - the change's
    ``swap_at``, else now plus the verification time."""
    if swap_at is not None and swap_at > clock:
        return swap_at
    return clock + timedelta(seconds=max(60, soak_seconds + 60))
