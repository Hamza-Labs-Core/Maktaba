"""Channel scheduler (Epic 27 / Story 27.2).

Turns a channel's programming rule + the library into a continuous,
wall-clock-anchored 48 h linear schedule written to ``channel_programs``.
Entry points live in :mod:`pass_`; the per-mode planners in
:mod:`planner`; the contiguity-preserving packer in :mod:`packer`.
"""

from __future__ import annotations

from .base import Block, BlockKind, ChannelDef, ChannelMode, ContentItem, FillerItem, PlanItem
from .pass_ import DEFAULT_HORIZON, run_schedule, topup_all

__all__ = [
    "Block",
    "BlockKind",
    "ChannelDef",
    "ChannelMode",
    "ContentItem",
    "DEFAULT_HORIZON",
    "FillerItem",
    "PlanItem",
    "run_schedule",
    "topup_all",
]
