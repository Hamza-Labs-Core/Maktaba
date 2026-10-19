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
import os
from datetime import UTC, datetime, timedelta, tzinfo
from pathlib import Path
from typing import Any
from uuid import UUID
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

from ..log import get_logger
from . import packer, slate
from .base import ChannelMode
from .filler import FillerPicker
from .planner import make_planner
from .repo import ScheduleRepo, ScheduleState, SqlRepo

__all__ = [
    "channel_tz",
    "local_zone",
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

_EPOCH = datetime(1970, 1, 1, tzinfo=UTC)


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
    return local_zone()


def local_zone() -> tzinfo:
    """The server's zone as a real IANA zone (its DST rules included): ``TZ``, then
    ``/etc/localtime`` (its link name, else the file), then ``/etc/timezone``. Only when
    none names one (a host without tzdata): the current fixed offset."""
    name = os.environ.get("TZ", "").lstrip(":").strip()
    if name:
        with contextlib.suppress(ZoneInfoNotFoundError, ValueError, OSError):
            return ZoneInfo(name)
    link = Path("/etc/localtime")
    if link.exists():
        target = str(link.resolve()).replace("\\", "/")
        if "/zoneinfo/" in target:
            with contextlib.suppress(ZoneInfoNotFoundError, ValueError, OSError):
                return ZoneInfo(target.split("/zoneinfo/", 1)[1])
        with contextlib.suppress(ValueError, OSError), link.open("rb") as fh:
            return ZoneInfo.from_file(fh, key="localtime")
    with contextlib.suppress(ZoneInfoNotFoundError, ValueError, OSError):
        named = Path("/etc/timezone").read_text(encoding="utf-8").strip()
        if named:
            return ZoneInfo(named)
    local = datetime.now().astimezone().tzinfo
    return local if local is not None else UTC


def _filler_state_before(state: Any, anchor: datetime) -> dict[str, Any] | None:
    """The saved filler plays that aired before ``anchor``. A rewrite replaces the
    timeline from the anchor, so a play at or after it never aired; kept, it would put
    a clip "last played" in the future of the new plan and keep it out of every gap."""
    if not isinstance(state, dict):
        return None
    cut = int((anchor - _EPOCH) / timedelta(milliseconds=1))
    out: dict[str, Any] = {}
    for key in ("clips", "lists"):
        got = state.get(key)
        if isinstance(got, dict):
            out[key] = {
                k: m for k, v in got.items() if (m := _ms_or_none(v)) is not None and m < cut
            }
    return out


def _ms_or_none(v: Any) -> int | None:
    try:
        return int(v)
    except (TypeError, ValueError):
        return None


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

    tv = await repo.load_tv()
    slot_content = await repo.resolve_slot_content(channel)
    planner = make_planner(
        channel, cursor=state.cursor, seed=_seed_for(channel_id), slot_content=slot_content, tv=tv
    )
    content = await repo.resolve_content(channel)
    if state.horizon_until is not None and anchor < state.horizon_until:
        # a rewrite of a future already planned (a stale channel): each queue goes on
        # from the first programme the rewrite drops, not from where that plan ended
        rewind = getattr(planner, "rewind", None)
        if rewind is not None:
            rewind(await repo.future_programs(channel_id, anchor), content)
    picker = FillerPicker(
        await repo.load_filler(channel),
        max_per_gap=tv.max_filler_per_gap,
        slot_first=tv.slot_filler_first,
        seed=_seed_for(channel_id) ^ int(anchor.timestamp()),
        state=_filler_state_before((state.cursor or {}).get("filler"), anchor),
    )

    # slot times are wall-clock times in the channel's timezone (server local when
    # the plan names none): the planner reads hours and weekdays off these datetimes
    tz = channel_tz(channel.mode_config)
    items = planner.plan(content, start_at=anchor.astimezone(tz), until=until.astimezone(tz))
    if channel.mode == ChannelMode.SCHEDULE:
        # the grid's last slot is planned to its end: the timeline runs to it, so the
        # next top-up starts on a slot boundary and no episode is cut at the horizon
        planned = anchor + timedelta(milliseconds=sum(i.duration_ms for i in items))
        until = max(until, planned)
    base_seq = await repo.max_seq_before(channel_id, anchor) + 1
    blocks = packer.pack(
        items,
        channel_id=channel_id,
        start_at=anchor,
        until=until,
        filler=picker,
        base_seq=base_seq,
    )
    if not blocks:
        # D9: degenerate source → a single rolling slate block.
        s = slate.rolling(channel_id, anchor, until)
        s.seq = base_seq
        blocks = [s]

    cursor = planner.export_state()
    if fstate := picker.export_state(keep_after=until):
        cursor["filler"] = fstate
    await repo.replace_future_blocks(channel_id, from_at=anchor, blocks=blocks)
    await repo.save_state(
        ScheduleState(
            channel_id=channel_id,
            anchor_at=state.anchor_at or anchor,
            horizon_until=until,
            last_generated_at=now,
            cursor=cursor,
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
