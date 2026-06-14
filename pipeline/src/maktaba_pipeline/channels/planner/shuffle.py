"""Shuffle planner — a fair bag with no adjacent repeats (AC3).

A "bag" shuffle draws every item once before any repeats, which feels
fairer than independent random picks (no long droughts, no clusters).
The bag is persisted in the channel cursor so a horizon top-up continues
the same bag rather than reshuffling from scratch (D5/EC1).
"""

from __future__ import annotations

import random
from datetime import datetime, timedelta
from typing import Any

from ..base import ChannelDef, ContentItem, PlanItem

__all__ = ["ShufflePlanner"]

# Safety cap so a library of zero-duration rows can't spin forever.
_MAX_ITEMS = 100_000


class ShufflePlanner:
    def __init__(
        self, channel: ChannelDef, *, cursor: dict[str, Any] | None = None, seed: int | None = None
    ) -> None:
        self.channel = channel
        self._rng = random.Random(seed)
        cursor = cursor or {}
        self._bag: list[str] = list(cursor.get("bag", []))
        self._last: str | None = cursor.get("last")

    def plan(
        self, content: list[ContentItem], *, start_at: datetime, until: datetime
    ) -> list[PlanItem]:
        if not content:
            return []
        by_id = {str(c.video_id): c for c in content if c.duration_ms > 0}
        if not by_id:
            return []
        horizon_ms = int((until - start_at) / timedelta(milliseconds=1))
        out: list[PlanItem] = []
        acc = 0
        guard = 0
        while acc < horizon_ms and guard < _MAX_ITEMS:
            guard += 1
            vid = self._next(list(by_id.keys()))
            item = by_id.get(vid)
            if item is None:
                # Source changed since the bag was filled — drop stale id.
                continue
            out.append(
                PlanItem(
                    video_id=item.video_id,
                    duration_ms=item.duration_ms,
                    offset_ms=0,
                    snapshot=item.to_snapshot(),
                )
            )
            acc += item.duration_ms
            self._last = vid
        return out

    def _next(self, all_ids: list[str]) -> str:
        if not self._bag:
            self._refill(all_ids)
        return self._bag.pop(0)

    def _refill(self, all_ids: list[str]) -> None:
        candidates = list(all_ids)
        self._rng.shuffle(candidates)
        # Avoid an adjacent repeat across the bag boundary (AC3): if the
        # first item to emit equals the last emitted, swap it deeper.
        if len(candidates) > 1 and self._last is not None and candidates[0] == self._last:
            candidates[0], candidates[1] = candidates[1], candidates[0]
        self._bag = candidates

    def export_state(self) -> dict[str, Any]:
        return {"bag": list(self._bag), "last": self._last}
