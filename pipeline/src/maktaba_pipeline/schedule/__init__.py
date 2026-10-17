"""Channel planning: which live channels a library supports and their daypart grids.

:mod:`.channel_planner` is pure (no DB, no I/O). It turns detected seasons
(with topic tags and production tier) and loose videos into channel
suggestions whose ``schedule``-mode config matches Epic 27's channel model
(slot 0081 ``mode_config``), filler pools for Story 27.10, and dizqueTV
time-slot schedules.
"""

from __future__ import annotations

from .channel_planner import (
    DEFAULT_DAYPARTS,
    DEFAULT_RAMADAN_DAYPARTS,
    DEFAULT_THEMES,
    PLANNER_VERSION,
    Airing,
    ChannelSuggestion,
    Daypart,
    EpisodeRef,
    FillerItem,
    FillerPool,
    Lane,
    LooseVideo,
    PlannerConfig,
    PlanResult,
    ProgramUnit,
    SeasonCandidate,
    SeriesCandidate,
    Slot,
    ThemeSpec,
    dizquetv_time_slots,
    group_series,
    maktaba_channel_definition,
    simulate,
    suggest_channels,
)
from .entry import plan_catalog

__all__ = [
    "plan_catalog",
    "DEFAULT_DAYPARTS",
    "DEFAULT_RAMADAN_DAYPARTS",
    "DEFAULT_THEMES",
    "PLANNER_VERSION",
    "Airing",
    "ChannelSuggestion",
    "Daypart",
    "EpisodeRef",
    "FillerItem",
    "FillerPool",
    "Lane",
    "LooseVideo",
    "PlanResult",
    "PlannerConfig",
    "ProgramUnit",
    "SeasonCandidate",
    "SeriesCandidate",
    "Slot",
    "ThemeSpec",
    "dizquetv_time_slots",
    "group_series",
    "maktaba_channel_definition",
    "simulate",
    "suggest_channels",
]
