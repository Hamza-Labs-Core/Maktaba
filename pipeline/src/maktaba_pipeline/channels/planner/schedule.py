"""Time-slot planner — a daypart grid (AC5).

Maps wall-clock windows to sources: e.g. cartoons 06:00–09:00, news at
noon, movies in prime time. The planner walks the clock from the anchor
to the horizon; each slot window is filled with that slot's content and
padded to its boundary, so the laid-out timeline aligns to the grid. A
period with no active slot is padded (filler/slate). Times are
interpreted in the datetimes handed in — the pass localises before
calling, so DST is the caller's concern (EC3).
"""

from __future__ import annotations

import random
from datetime import datetime, timedelta
from typing import Any

from ...schedule.channel_planner import PlannerConfig
from ..base import BlockKind, ChannelDef, ContentItem, PlanItem

__all__ = ["SchedulePlanner", "parse_hhmm", "slot_days"]

_WEEKDAYS = {
    "mon": 0,
    "tue": 1,
    "wed": 2,
    "thu": 3,
    "fri": 4,
    "sat": 5,
    "sun": 6,
}
_MAX_WINDOWS = 10_000


def parse_hhmm(s: str) -> int:
    """ "HH:MM" → minutes-since-midnight. Invalid → 0."""
    try:
        h, m = s.split(":", 1)
        return (int(h) % 24) * 60 + int(m) % 60
    except (ValueError, AttributeError):
        return 0


def slot_days(slot: dict[str, Any]) -> set[int]:
    """The weekday set a slot is active on (0=Mon..6=Sun). Absent/empty
    ``days`` ⇒ every day."""
    raw = slot.get("days")
    if not raw:
        return set(range(7))
    out: set[int] = set()
    for d in raw:
        if isinstance(d, int):
            out.add(d % 7)
        elif isinstance(d, str):
            key = d.strip().lower()[:3]
            if key in _WEEKDAYS:
                out.add(_WEEKDAYS[key])
    return out or set(range(7))


class SchedulePlanner:
    def __init__(
        self,
        channel: ChannelDef,
        *,
        cursor: dict[str, Any] | None = None,
        seed: int | None = None,
        slot_content: list[list[ContentItem] | None] | None = None,
    ) -> None:
        self.channel = channel
        self._rng = random.Random(seed)
        self._slots: list[dict[str, Any]] = list(channel.mode_config.get("slots", []))
        self._slot_content = slot_content
        # how far an item may run past its slot: the planner's rule, written into the
        # channel (an older channel without it: the planner's default)
        tol = channel.mode_config.get("overrun_tolerance_minutes")
        if not isinstance(tol, int | float) or tol < 0:
            tol = PlannerConfig().overrun_tolerance_minutes
        self._tolerance_ms = int(tol * 60_000)
        cursor = cursor or {}
        # Round-robin cursor so a top-up doesn't restart a slot. Keyed by the
        # slot's lane when it has one: a lane airing in several slots (days)
        # is one queue that carries on, not one that restarts per slot.
        self._rr: dict[str, int] = {str(k): int(v) for k, v in cursor.get("rr", {}).items()}

    def _content_for(self, slot_idx: int, default: list[ContentItem]) -> list[ContentItem]:
        if self._slot_content is not None and slot_idx < len(self._slot_content):
            own = self._slot_content[slot_idx]
            if own is not None:
                return own
        return default

    def _queue_key(self, slot_idx: int) -> str:
        src = self._slots[slot_idx].get("source")
        if isinstance(src, dict) and src.get("lane"):
            return f"lane:{src['lane']}"
        return str(slot_idx)

    def _active_slot(self, t: datetime) -> int | None:
        tod = t.hour * 60 + t.minute
        wd = t.weekday()
        for i, slot in enumerate(self._slots):
            if wd not in slot_days(slot):
                continue
            start = parse_hhmm(slot.get("start", "00:00"))
            end = parse_hhmm(slot.get("end", "24:00")) or 24 * 60
            if start <= tod < end:
                return i
        return None

    def _window_end(self, t: datetime, slot_idx: int, until: datetime) -> datetime:
        slot = self._slots[slot_idx]
        end = parse_hhmm(slot.get("end", "24:00")) or 24 * 60
        end_dt = t.replace(hour=0, minute=0, second=0, microsecond=0) + timedelta(minutes=end)
        if end_dt <= t:  # end already passed (wrap) — clamp to horizon
            end_dt = until
        return min(end_dt, until)

    def _next_slot_start(self, t: datetime, until: datetime) -> datetime:
        """The earliest upcoming slot boundary after ``t`` (scanning at
        most 24h ahead minute-coarse), else ``until``."""
        probe = t.replace(second=0, microsecond=0) + timedelta(minutes=1)
        steps = 0
        while probe < until and steps < 24 * 60:
            if self._active_slot(probe) is not None:
                return probe
            probe += timedelta(minutes=1)
            steps += 1
        return until

    def plan(
        self, content: list[ContentItem], *, start_at: datetime, until: datetime
    ) -> list[PlanItem]:
        if not self._slots:
            return []
        out: list[PlanItem] = []
        t = start_at
        guard = 0
        while t < until and guard < _MAX_WINDOWS:
            guard += 1
            slot_idx = self._active_slot(t)
            if slot_idx is None:
                gap_end = self._next_slot_start(t, until)
                out.append(self._pad(t, gap_end))
                t = gap_end
                continue
            window_end = self._window_end(t, slot_idx, until)
            window_ms = int((window_end - t) / timedelta(milliseconds=1))
            emitted = self._fill_window(slot_idx, self._content_for(slot_idx, content), window_ms)
            out.extend(emitted)
            used = sum(p.duration_ms for p in emitted)
            if used < window_ms:
                out.append(self._pad_ms(window_ms - used))
            # items play back to back: an episode that ran past the slot (by the
            # overrun tolerance) delays the next slot's start, which absorbs it, so
            # the grid never drifts from the clock
            t = max(window_end, t + timedelta(milliseconds=used))
        return out

    def _fill_window(
        self, slot_idx: int, pool: list[ContentItem], window_ms: int
    ) -> list[PlanItem]:
        """The slot's queue, in order, for as long as each item fits what is left of
        the window (an item may run past the end by the planner's overrun tolerance).
        An item longer than the whole window can never air in this slot and is passed
        over; one that would fit an empty window waits for the next airing."""
        pool = [c for c in pool if c.duration_ms > 0]
        if not pool:
            return []
        items: list[PlanItem] = []
        acc = 0
        key = self._queue_key(slot_idx)
        rr = self._rr.get(key, 0) % len(pool)
        room = window_ms + self._tolerance_ms
        passed = 0  # items in a row too long for the slot: all of them means none fits
        while acc < window_ms and passed < len(pool):
            c = pool[rr % len(pool)]
            if c.duration_ms > room:
                rr += 1
                passed += 1
                continue
            if acc + c.duration_ms > room:
                break  # its turn comes at the next airing; the rest is padded
            items.append(
                PlanItem(video_id=c.video_id, duration_ms=c.duration_ms, snapshot=c.to_snapshot())
            )
            acc += c.duration_ms
            rr += 1
            passed = 0
        self._rr[key] = rr
        return items

    @staticmethod
    def _pad(start: datetime, end: datetime) -> PlanItem:
        ms = int((end - start) / timedelta(milliseconds=1))
        return PlanItem(video_id=None, duration_ms=max(ms, 0), kind=BlockKind.FILLER)

    @staticmethod
    def _pad_ms(ms: int) -> PlanItem:
        return PlanItem(video_id=None, duration_ms=max(ms, 0), kind=BlockKind.FILLER)

    def export_state(self) -> dict[str, Any]:
        return {"rr": dict(self._rr)}
