"""Per-mode planners (D4).

Each planner isolates one mode's logic and yields the ordered
:class:`~maktaba_pipeline.channels.base.PlanItem` sequence the shared
packer lays onto the timeline. :func:`make_planner` is the registry the
pass uses to pick the right one for a channel.
"""

from __future__ import annotations

from typing import Any

from ..base import ChannelDef, ChannelMode, Planner
from .marathon import MarathonPlanner
from .schedule import SchedulePlanner
from .shuffle import ShufflePlanner
from .smartmix import SmartMixPlanner

__all__ = [
    "MarathonPlanner",
    "SchedulePlanner",
    "ShufflePlanner",
    "SmartMixPlanner",
    "make_planner",
]


def make_planner(
    channel: ChannelDef, *, cursor: dict[str, Any] | None = None, seed: int | None = None
) -> Planner:
    """Construct the planner for ``channel``'s mode, restoring its cursor
    (shuffle bag / marathon index) so a top-up continues (D5)."""
    cursor = cursor or {}
    if channel.mode == ChannelMode.SHUFFLE:
        return ShufflePlanner(channel, cursor=cursor, seed=seed)
    if channel.mode == ChannelMode.MARATHON:
        return MarathonPlanner(channel, cursor=cursor)
    if channel.mode == ChannelMode.SCHEDULE:
        return SchedulePlanner(channel, cursor=cursor, seed=seed)
    if channel.mode == ChannelMode.SMART_MIX:
        return SmartMixPlanner(channel, cursor=cursor, seed=seed)
    raise ValueError(f"unknown channel mode: {channel.mode!r}")
