"""Smart-mix planner — classification-driven daypart balance (AC6).

Programs the day the way a real network would: a target genre mix per
block, filled greedily so the running distribution tracks the target.
This is the one mode that consumes Epic 26's classification
(``video_classification`` → ``genre``). When that data is absent or
disabled for a library it must NOT fail — it degrades to a weighted
shuffle and logs the fallback (D7), so channels work on a library that
never ran enrichment.

The sampler is deliberately a simple proportional chooser (pick the
genre furthest below its target share, then its next item), not a model:
O(items) and fully deterministic given a seed.
"""

from __future__ import annotations

from datetime import datetime, timedelta
from typing import Any

from ...log import get_logger
from ..base import ChannelDef, ContentItem, PlanItem
from .shuffle import ShufflePlanner

__all__ = ["SmartMixPlanner"]

_log = get_logger()
_MAX_ITEMS = 100_000


class SmartMixPlanner:
    def __init__(
        self, channel: ChannelDef, *, cursor: dict[str, Any] | None = None, seed: int | None = None
    ) -> None:
        self.channel = channel
        self._seed = seed
        cursor = cursor or {}
        self._rr: dict[str, int] = {str(k): int(v) for k, v in cursor.get("rr", {}).items()}
        # Lazily-constructed fallback planner; its state is threaded
        # through our cursor under "fallback" so a top-up keeps its bag.
        self._fallback: ShufflePlanner | None = None
        self._fallback_cursor: dict[str, Any] = dict(cursor.get("fallback", {}))
        self._used_fallback = False

    def plan(
        self, content: list[ContentItem], *, start_at: datetime, until: datetime
    ) -> list[PlanItem]:
        if not content:
            return []
        classified = [c for c in content if c.genre and c.duration_ms > 0]
        if not classified:
            # D7: no Epic 26 classification → weighted shuffle fallback.
            _log.warning(
                "smartmix.fallback_to_shuffle",
                channel_id=str(self.channel.id),
                reason="no video_classification rows for source",
            )
            self._used_fallback = True
            self._fallback = ShufflePlanner(
                self.channel, cursor=self._fallback_cursor, seed=self._seed
            )
            return self._fallback.plan(content, start_at=start_at, until=until)

        return self._mix(classified, start_at, until)

    def _mix(
        self, content: list[ContentItem], start_at: datetime, until: datetime
    ) -> list[PlanItem]:
        by_genre: dict[str, list[ContentItem]] = {}
        for c in content:
            by_genre.setdefault(c.genre or "", []).append(c)

        weights = self.channel.mode_config.get("weights", {})
        genres = sorted(by_genre.keys())
        # Target share per genre: explicit weight, else uniform.
        raw = {g: float(weights.get(g, 1.0)) for g in genres}
        total_w = sum(raw.values()) or 1.0
        target = {g: raw[g] / total_w for g in genres}

        horizon_ms = int((until - start_at) / timedelta(milliseconds=1))
        running: dict[str, int] = {g: 0 for g in genres}
        out: list[PlanItem] = []
        acc = 0
        guard = 0
        while acc < horizon_ms and guard < _MAX_ITEMS:
            guard += 1
            total = acc or 1
            # Pick the genre furthest below its target share.
            g = min(genres, key=lambda gg: (running[gg] / total) - target[gg])
            pool = by_genre[g]
            rr = self._rr.get(g, 0) % len(pool)
            c = pool[rr]
            self._rr[g] = rr + 1
            out.append(
                PlanItem(video_id=c.video_id, duration_ms=c.duration_ms, snapshot=c.to_snapshot())
            )
            running[g] += c.duration_ms
            acc += c.duration_ms
        return out

    def export_state(self) -> dict[str, Any]:
        state: dict[str, Any] = {"rr": dict(self._rr)}
        if self._used_fallback and self._fallback is not None:
            state["fallback"] = self._fallback.export_state()
        elif self._fallback_cursor:
            state["fallback"] = self._fallback_cursor
        return state
