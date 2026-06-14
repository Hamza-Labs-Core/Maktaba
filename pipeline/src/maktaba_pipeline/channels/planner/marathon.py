"""Marathon planner — a series in order, optionally looping (AC4).

Plays a series start-to-finish in episode order (season then episode,
falling back to filename), then either loops back to the start or stops
(padding the tail with filler/slate via the packer). The cursor persists
the play position so a top-up resumes where the last generation left off
(D5) rather than restarting the marathon.
"""

from __future__ import annotations

from datetime import datetime, timedelta
from typing import Any

from ..base import ChannelDef, ContentItem, PlanItem

__all__ = ["MarathonPlanner"]

_MAX_ITEMS = 100_000


def _episode_key(c: ContentItem) -> tuple[int, int, str]:
    """Sort key: (season, episode, title). ``None`` sorts last so
    unnumbered specials trail the numbered run deterministically."""
    s = c.season if c.season is not None else 1 << 30
    e = c.episode if c.episode is not None else 1 << 30
    return (s, e, c.title)


class MarathonPlanner:
    def __init__(self, channel: ChannelDef, *, cursor: dict[str, Any] | None = None) -> None:
        self.channel = channel
        cursor = cursor or {}
        self._idx: int = int(cursor.get("idx", 0))
        self._loop: bool = bool(channel.mode_config.get("loop", True))

    def _order(self, content: list[ContentItem]) -> list[ContentItem]:
        order = self.channel.mode_config.get("order", "aired")
        if order == "filename":
            return sorted(content, key=lambda c: c.title)
        return sorted(content, key=_episode_key)

    def plan(
        self, content: list[ContentItem], *, start_at: datetime, until: datetime
    ) -> list[PlanItem]:
        episodes = [c for c in self._order(content) if c.duration_ms > 0]
        if not episodes:
            return []
        horizon_ms = int((until - start_at) / timedelta(milliseconds=1))
        out: list[PlanItem] = []
        acc = 0
        i = self._idx % len(episodes)
        guard = 0
        while acc < horizon_ms and guard < _MAX_ITEMS:
            guard += 1
            ep = episodes[i]
            out.append(
                PlanItem(
                    video_id=ep.video_id,
                    duration_ms=ep.duration_ms,
                    offset_ms=0,
                    snapshot=ep.to_snapshot(),
                )
            )
            acc += ep.duration_ms
            i += 1
            if i >= len(episodes):
                if not self._loop:
                    break  # tail padded by the packer
                i = 0
        self._idx = i
        return out

    def export_state(self) -> dict[str, Any]:
        return {"idx": self._idx}
