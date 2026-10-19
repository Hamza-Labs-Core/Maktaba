"""The dizqueTV block of the channel plan, contributed to core's plan exporter hook.

Core's ``plan_channels`` stage calls every registered exporter per channel and stores
the result under ``channels[i]["exports"][<name>]``; the ``dizquetv_channels`` stage
reads it back from there.
"""

from __future__ import annotations

from typing import Any

from ...integrations.dizquetv_export import filler_collections
from ...schedule.channel_planner import (
    ChannelSuggestion,
    PlanResult,
    dizquetv_time_slots,
)

__all__ = ["EXPORT_NAME", "export_channel"]

EXPORT_NAME = "dizquetv"


def export_channel(channel: ChannelSuggestion, plan: PlanResult) -> dict[str, Any]:
    return {
        "number": channel.number,
        "name": channel.name_ar or channel.name,
        "schedule": dizquetv_time_slots(channel, plan),
        "ramadan_schedule": (
            dizquetv_time_slots(channel, plan, ramadan=True) if channel.ramadan_slots else None
        ),
        "fillerCollections": filler_collections(channel, plan.config),
        "fillerRepeatCooldown": plan.config.dz_filler_cooldown_ms,
    }
