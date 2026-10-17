"""The scheduler pass — entry points (D1).

``run_schedule`` (re)generates one channel's future tail from the next
boundary after ``now``; ``topup_all`` extends every enabled channel whose
horizon is running low (the ~6-hourly cron). Both are debounced/periodic,
never request-time — generating a 48 h timeline is batch planning that
mirrors Epic 26's auto-collection pass model.

The pass is the only place that touches the clock; the planner and packer
are pure. Generation never raises (D9): an empty source yields a single
rolling slate block so a degenerate library still has a defined timeline.
"""

from __future__ import annotations

import asyncio
import contextlib
from datetime import UTC, datetime, timedelta, tzinfo
from typing import Any
from uuid import UUID
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

from ..log import get_logger
from . import packer, slate
from .planner import make_planner
from .repo import ScheduleRepo, ScheduleState, SqlRepo

__all__ = [
    "run_schedule",
    "topup_all",
    "run_topup_loop",
    "DEFAULT_HORIZON",
    "TOPUP_LOW_WATER",
    "TOPUP_INTERVAL_S",
]

_log = get_logger()

# Default look-ahead and the threshold below which a top-up extends a
# channel (Plan 27.2 §2).
DEFAULT_HORIZON = timedelta(hours=48)
TOPUP_LOW_WATER = timedelta(hours=24)

# How often a worker looks for channels due a top-up. A tick that finds none
# is one query, so it runs well inside the 24 h low water: a channel that is
# new or edited (``stale``) gets its timeline within minutes, not hours.
TOPUP_INTERVAL_S = 600.0

# pg advisory lock class: (this key, the channel) — one writer per channel.
TOPUP_ADVISORY_LOCK_KEY = 0x6D6B_7470  # "mktp"


# Per-channel seed so a regen is deterministic for a given channel (the
# shuffle bag still varies channel-to-channel) without a wall-clock seed.
def channel_tz(mode_config: dict[str, Any]) -> tzinfo:
    """The channel's ``timezone`` (IANA), else the server's local zone."""
    name = mode_config.get("timezone")
    if isinstance(name, str) and name:
        try:
            return ZoneInfo(name)
        except (ZoneInfoNotFoundError, ValueError):
            _log.warning("channel.unknown_timezone", timezone=name)
    local = datetime.now().astimezone().tzinfo
    return local if local is not None else UTC


def _seed_for(channel_id: UUID) -> int:
    return int(channel_id.int & 0x7FFFFFFF)


async def run_schedule(
    repo: ScheduleRepo,
    channel_id: UUID,
    *,
    now: datetime,
    horizon: timedelta = DEFAULT_HORIZON,
) -> int:
    """(Re)generate ``channel_id``'s schedule out to ``now + horizon``.

    Returns the number of blocks written (0 if the channel is missing or
    disabled). Past/current blocks are never rewritten (D3): the anchor
    is ``max(now, horizon_until)``, so regeneration only appends/refreshes
    the future tail.
    """
    channel = await repo.load_channel(channel_id)
    if channel is None or not channel.enabled:
        return 0

    state = await repo.load_state(channel_id)
    # Anchor at the existing horizon when topping up (continue the
    # timeline) but never before now, and never rewrite the current block.
    anchor = now
    if state.horizon_until is not None and state.horizon_until > now and not state.stale:
        anchor = state.horizon_until
    else:
        # A regeneration starts after the block on air: it is kept (D3), so a
        # new timeline from ``now`` would overlap it.
        on_air = await repo.airing_until(channel_id, now)
        if on_air is not None and on_air > anchor:
            anchor = on_air
    until = now + horizon
    if until <= anchor:
        # Already covered past the requested horizon; nothing to do.
        return 0

    slot_content = await repo.resolve_slot_content(channel)
    planner = make_planner(
        channel, cursor=state.cursor, seed=_seed_for(channel_id), slot_content=slot_content
    )
    content = await repo.resolve_content(channel)
    filler = await repo.load_filler(channel)

    # slot times are wall-clock times in the channel's timezone (server local when
    # the plan names none): the planner reads hours and weekdays off these datetimes
    tz = channel_tz(channel.mode_config)
    items = planner.plan(content, start_at=anchor.astimezone(tz), until=until.astimezone(tz))
    base_seq = await repo.max_seq_before(channel_id, anchor) + 1
    blocks = packer.pack(
        items,
        channel_id=channel_id,
        start_at=anchor,
        until=until,
        filler=filler,
        base_seq=base_seq,
    )
    if not blocks:
        # D9: degenerate source → a single rolling slate block.
        s = slate.rolling(channel_id, anchor, until)
        s.seq = base_seq
        blocks = [s]

    await repo.replace_future_blocks(channel_id, from_at=anchor, blocks=blocks)
    await repo.save_state(
        ScheduleState(
            channel_id=channel_id,
            anchor_at=state.anchor_at or anchor,
            horizon_until=until,
            last_generated_at=now,
            cursor=planner.export_state(),
            stale=False,
        )
    )
    _log.info(
        "channel.schedule_generated",
        channel_id=str(channel_id),
        blocks=len(blocks),
        anchor=anchor.isoformat(),
        horizon=until.isoformat(),
    )
    return len(blocks)


async def topup_all(
    repo: ScheduleRepo,
    *,
    now: datetime,
    horizon: timedelta = DEFAULT_HORIZON,
    low_water: timedelta = TOPUP_LOW_WATER,
) -> int:
    """Extend every enabled channel whose horizon is within ``low_water``
    of ``now`` (or marked stale). Returns the count of channels touched."""
    due = await repo.channels_needing_topup(now, now + low_water)
    touched = 0
    for channel_id in due:
        try:
            await run_schedule(repo, channel_id, now=now, horizon=horizon)
            touched += 1
        except Exception:  # noqa: BLE001 — one bad channel must not stop the sweep
            _log.exception("channel.topup_failed", channel_id=str(channel_id))
    return touched


async def topup_once(db: Any, *, now: datetime | None = None) -> int:
    """Top up every channel that is due, each in its own short transaction.

    One transaction per channel, never one for the sweep: writing a channel's
    blocks takes key-share locks on the videos they reference, which would hold
    off ``content_hash`` (it updates a unique column of ``videos``) for the whole
    sweep. On Postgres a per-channel advisory lock lets several workers share
    the sweep without writing the same channel twice."""
    now = now or datetime.now(UTC)
    repo = SqlRepo(db)  # ``db``'s methods join each transaction (and rewrite $N for SQLite)
    touched = 0
    for channel_id in await repo.channels_needing_topup(now, now + TOPUP_LOW_WATER):
        try:
            async with db.transaction():
                if db.dialect == "postgres" and not await db.fetchval(
                    "SELECT pg_try_advisory_xact_lock($1, $2)",
                    TOPUP_ADVISORY_LOCK_KEY,
                    _seed_for(channel_id),
                ):
                    continue
                await run_schedule(repo, channel_id, now=now)
            touched += 1
        except Exception:  # noqa: BLE001 - one bad channel must not stop the sweep
            _log.exception("channel.topup_failed", channel_id=str(channel_id))
    return touched


async def run_topup_loop(
    db: Any, stop: asyncio.Event, *, interval_sec: float = TOPUP_INTERVAL_S
) -> None:
    """The worker's background loop that keeps every channel's timeline ahead."""
    while not stop.is_set():
        try:
            touched = await topup_once(db)
            if touched:
                _log.info("channel.topup", channels=touched)
        except Exception:  # noqa: BLE001 - the guide must never take the worker down
            _log.exception("channel.topup_loop_failed")
        with contextlib.suppress(TimeoutError, asyncio.TimeoutError):  # noqa: UP041
            await asyncio.wait_for(stop.wait(), timeout=interval_sec)
