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

from datetime import datetime, timedelta
from uuid import UUID

from ..log import get_logger
from . import packer, slate
from .planner import make_planner
from .repo import ScheduleRepo, ScheduleState

__all__ = ["run_schedule", "topup_all", "DEFAULT_HORIZON", "TOPUP_LOW_WATER"]

_log = get_logger()

# Default look-ahead and the threshold below which a top-up extends a
# channel (Plan 27.2 §2).
DEFAULT_HORIZON = timedelta(hours=48)
TOPUP_LOW_WATER = timedelta(hours=24)


# Per-channel seed so a regen is deterministic for a given channel (the
# shuffle bag still varies channel-to-channel) without a wall-clock seed.
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
    until = now + horizon
    if until <= anchor:
        # Already covered past the requested horizon; nothing to do.
        return 0

    planner = make_planner(channel, cursor=state.cursor, seed=_seed_for(channel_id))
    content = await repo.resolve_content(channel)
    filler = await repo.load_filler(channel)

    items = planner.plan(content, start_at=anchor, until=until)
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
