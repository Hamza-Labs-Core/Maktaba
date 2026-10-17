"""The channel planner's one public entry point (the hook for the TV-grid planner).

The ``plan_channels`` queue stage (``engine/stages/plan_channels.py``) loads the catalog
from the database and calls :func:`plan_catalog`, and nothing else in the planner. A new
planner (``feat/tv-grid``: a TV slot grid where an ongoing show keeps its slot,
cadence, fillers, non-stop Quran channels) plugs in by changing this function's body to
call it. The stage, its inputs, its plan JSON and every extension stay as they are, so
plugging it in is a merge, not a rewrite.

Contract:

* **in:** the catalog as :class:`~.channel_planner.SeasonCandidate` (live seasons with
  episodes, tags, production tier) and :class:`~.channel_planner.LooseVideo` (everything
  in no season; ``group`` is its clip group's title, ``promo_of`` the
  ``"<folder>|<series key>"`` of the show a promo advertises), plus a
  :class:`~.channel_planner.PlannerConfig`;
* **out:** a :class:`~.channel_planner.PlanResult` (channels, lanes, filler pools, slots),
  which ``engine/stages/_plan_json.build_plan_json`` serialises for ``channel_plans``;
* **version:** :data:`PLANNER_VERSION` is part of the stage's input fingerprint; a new
  planner bumps it so every plan is rebuilt once.
"""

from __future__ import annotations

from collections.abc import Sequence

from .channel_planner import (
    PLANNER_VERSION,
    LooseVideo,
    PlannerConfig,
    PlanResult,
    SeasonCandidate,
    suggest_channels,
)

__all__ = ["PLANNER_VERSION", "plan_catalog"]


def plan_catalog(
    seasons: Sequence[SeasonCandidate],
    loose: Sequence[LooseVideo] = (),
    config: PlannerConfig | None = None,
) -> PlanResult:
    """Plan the live channels of the whole catalog. Pure: no database, no I/O."""
    return suggest_channels(seasons, loose, config or PlannerConfig())
