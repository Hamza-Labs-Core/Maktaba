"""Time-slot planner — a daypart grid (AC5), as the dizqueTV time-slot tool airs it.

Maps wall-clock windows to sources: e.g. cartoons 06:00–09:00, news at
noon, movies in prime time. The planner walks the clock from the anchor
to the horizon; each slot window is filled with that slot's content and
padded to its boundary, so the laid-out timeline aligns to the grid. A
period with no active slot is padded (filler/slate). Times are
interpreted in the datetimes handed in — the pass localises before
calling (EC3); durations are measured in elapsed time (UTC), so a slot across a DST
change is as long as it really is.

What the dizqueTV grid does, this does too:

* the Ramadan grid (``mode_config.ramadan.slots``) during the Hijri month it names
  (``activate.hijri_month``, 9), when the ``tv`` rule ``ramadan_grids`` is on;
* a ``flex`` slot (``kind``) or a ``filler`` source airs filler only;
* a nonstop lane (``source.pattern == "nonstop"``): with ``slot_minutes`` one episode a
  slot, starting on the slot, the rest of the slot filler (dizqueTV's pad); without,
  back to back, never a gap;
* a gap at the end of a slot longer than ``slot_max_filler_minutes`` takes the next
  episode though it runs past the slot (dizqueTV's maxFlex); with
  ``filler_between_episodes`` the spare time is shared out between the slot's episodes
  (dizqueTV's ``flexPreference: distribute``);
* live broadcasts (the ``tv`` rule ``live``, the same rows as ``dizquetv.live``): a live
  block at each start; nothing runs into it (a programme that would not end before it
  is not started, filler fills up to it) and the slot's show goes on after it.

A slot window is never cut at the horizon: the last one is planned to its end, so a
top-up starts on a slot boundary and no episode is cut in two.
"""

from __future__ import annotations

import random
from collections.abc import Sequence
from datetime import UTC, date, datetime, time, timedelta
from typing import Any
from uuid import UUID

from ...schedule.channel_planner import PlannerConfig
from ...schedule.tv_settings import LiveSlot
from ..base import BlockKind, ChannelDef, ContentItem, PlanItem
from ..hijri import RAMADAN, in_hijri_month

__all__ = ["LiveWindow", "SchedulePlanner", "live_windows", "parse_hhmm", "slot_days"]

_WEEKDAYS = {
    "mon": 0,
    "tue": 1,
    "wed": 2,
    "thu": 3,
    "fri": 4,
    "sat": 5,
    "sun": 6,
}
# a window is one slot occurrence, gap or live broadcast: a month of five-minute slots
# with a live break in each stays below this (each channel is planned on its own)
_MAX_WINDOWS = 200_000
_MS = timedelta(milliseconds=1)


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


LiveWindow = tuple[datetime, datetime, str, str]  # start, end, title, url


def live_windows(
    lives: Sequence[LiveSlot], number: int, start_at: datetime, until: datetime
) -> list[LiveWindow]:
    """The live broadcasts channel ``number`` carries in ``[start_at, until)``, in the
    datetimes' wall clock: from ``start`` every ``every_minutes`` until ``end`` (00:00:
    midnight; an end before the start runs past midnight), ``minutes`` long, on ``days``.
    Overlapping broadcasts are merged (the first one's title and address)."""
    mine = [x for x in lives if x.channel == number and x.url.strip() and x.minutes > 0]
    if not mine:
        return []
    tz = start_at.tzinfo
    out: list[LiveWindow] = []
    day: date = start_at.date() - timedelta(days=1)
    while day <= until.date():
        for x in mine:
            days = {_WEEKDAYS[d] for d in (s.strip().lower()[:3] for s in x.days) if d in _WEEKDAYS}
            if days and day.weekday() not in days:
                continue
            first = x.start
            last = x.end or 24 * 60
            if last <= first:
                last += 24 * 60
            for m in range(first, last, max(1, x.every_minutes)):
                d, mm = divmod(m, 24 * 60)
                s = datetime.combine(day + timedelta(days=d), time(mm // 60, mm % 60), tzinfo=tz)
                e = s + timedelta(minutes=x.minutes)
                if e > start_at and s < until:
                    out.append((s, e, x.title or x.url.strip(), x.url.strip()))
        day += timedelta(days=1)
    out.sort(key=lambda w: (w[0], w[1]))
    merged: list[LiveWindow] = []
    for w in out:
        if merged and w[0] < merged[-1][1]:
            if w[1] > merged[-1][1]:
                merged[-1] = (merged[-1][0], w[1], merged[-1][2], merged[-1][3])
            continue
        merged.append(w)
    return merged


class SchedulePlanner:
    def __init__(
        self,
        channel: ChannelDef,
        *,
        cursor: dict[str, Any] | None = None,
        seed: int | None = None,
        slot_content: list[list[ContentItem] | None] | None = None,
        live: Sequence[LiveSlot] = (),
        ramadan_grids: bool = True,
        hijri_adjust_days: int = 0,
    ) -> None:
        self.channel = channel
        mc = channel.mode_config
        self._rng = random.Random(seed)
        self._slots: list[dict[str, Any]] = list(mc.get("slots", []))
        ram: dict[str, Any] = mc["ramadan"] if isinstance(mc.get("ramadan"), dict) else {}
        self._ramadan: list[dict[str, Any]] = list(ram.get("slots") or []) if ramadan_grids else []
        act: dict[str, Any] = ram["activate"] if isinstance(ram.get("activate"), dict) else {}
        self._ramadan_month = int(act.get("hijri_month") or RAMADAN)
        self._hijri_adjust = int(hijri_adjust_days)
        # every slot of both grids, by index: the regular grid first, then the Ramadan one
        # (slot_content is indexed the same way)
        self._all: list[dict[str, Any]] = self._slots + self._ramadan
        self._slot_content = slot_content
        self._live = list(live)
        # how far an item may run past its slot: the planner's rule, written into the
        # channel (an older channel without it: the planner's default)
        tol = mc.get("overrun_tolerance_minutes")
        if not isinstance(tol, int | float) or tol < 0:
            tol = PlannerConfig().overrun_tolerance_minutes
        self._tolerance_ms = int(tol * 60_000)
        gap = mc.get("slot_max_filler_minutes")
        self._max_gap_ms = int(gap * 60_000) if isinstance(gap, int | float) and gap > 0 else 0
        self._between = bool(mc.get("filler_between_episodes", False))
        cursor = cursor or {}
        # Round-robin cursor so a top-up doesn't restart a slot. Keyed by the
        # slot's lane when it has one: a lane airing in several slots (days)
        # is one queue that carries on, not one that restarts per slot.
        self._rr: dict[str, int] = {str(k): int(v) for k, v in cursor.get("rr", {}).items()}
        self._ramadan_days: dict[date, bool] = {}

    # ------------------------------------------------------------------ the grid

    def _content_for(self, slot_idx: int, default: list[ContentItem]) -> list[ContentItem]:
        if self._slot_content is not None and slot_idx < len(self._slot_content):
            own = self._slot_content[slot_idx]
            if own is not None:
                return own
        return default

    def _queue_key(self, slot_idx: int) -> str:
        src = self._all[slot_idx].get("source")
        if isinstance(src, dict) and src.get("lane"):
            return f"lane:{src['lane']}"
        # a series / collection picked in the channel editor: one queue too, whichever
        # slots play it
        for kind, key in (("series", "series_id"), ("collection", "collection_id")):
            if isinstance(src, dict) and src.get("type") == kind and src.get(key):
                return f"{kind}:{src[key]}"
        return str(slot_idx)

    def _in_ramadan(self, day: date) -> bool:
        got = self._ramadan_days.get(day)
        if got is None:
            got = bool(self._ramadan) and in_hijri_month(
                day, self._ramadan_month, adjust_days=self._hijri_adjust
            )
            self._ramadan_days[day] = got
        return got

    def _grid(self, day: date) -> range:
        """The indexes of the grid airing on ``day``."""
        if self._in_ramadan(day):
            return range(len(self._slots), len(self._all))
        return range(len(self._slots))

    def _active_slot(self, t: datetime) -> int | None:
        tod = t.hour * 60 + t.minute
        wd = t.weekday()
        for i in self._grid(t.date()):
            slot = self._all[i]
            if wd not in slot_days(slot):
                continue
            start = parse_hhmm(slot.get("start", "00:00"))
            end = parse_hhmm(slot.get("end", "24:00")) or 24 * 60
            if start <= tod < end:
                return i
        return None

    def _slot_bounds(self, t: datetime, slot_idx: int) -> tuple[datetime, datetime]:
        slot = self._all[slot_idx]
        midnight = t.replace(hour=0, minute=0, second=0, microsecond=0)
        start = midnight + timedelta(minutes=parse_hhmm(slot.get("start", "00:00")))
        end = midnight + timedelta(minutes=parse_hhmm(slot.get("end", "24:00")) or 24 * 60)
        return start, end

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

    @staticmethod
    def _is_flex(slot: dict[str, Any]) -> bool:
        src = slot.get("source")
        return slot.get("kind") == "flex" or (isinstance(src, dict) and src.get("type") == "filler")

    # ------------------------------------------------------------------ planning

    def plan(
        self, content: list[ContentItem], *, start_at: datetime, until: datetime
    ) -> list[PlanItem]:
        if not self._all:
            return []
        # the last slot is planned to its end, past ``until`` (the pass runs the timeline
        # to it), so a broadcast in that overhang must cut it too: look a day further
        lives = live_windows(self._live, self.channel.number, start_at, until + timedelta(days=1))
        out: list[PlanItem] = []
        t = start_at
        li = 0
        guard = 0
        while t < until and guard < _MAX_WINDOWS:
            guard += 1
            while li < len(lives) and lives[li][1] <= t:
                li += 1
            if li < len(lives) and lives[li][0] <= t:
                _, end, title, url = lives[li]
                out.append(
                    PlanItem(
                        video_id=None,
                        duration_ms=_span(t, end),
                        kind=BlockKind.LIVE,
                        snapshot={"title": title},
                        source_url=url,
                    )
                )
                t = end
                li += 1
                continue
            live_at = lives[li][0] if li < len(lives) else None
            slot_idx = self._active_slot(t)
            if slot_idx is None:
                gap_end = self._next_slot_start(t, until)
                if live_at is not None:
                    gap_end = min(gap_end, live_at)
                out.append(self._pad(t, gap_end))
                t = gap_end
                continue
            slot = self._all[slot_idx]
            slot_start, slot_end = self._slot_bounds(t, slot_idx)
            window_end = slot_end if live_at is None else min(slot_end, live_at)
            window_ms = _span(t, window_end)
            fp = slot.get("filler_pool")
            pool_key = fp if isinstance(fp, str) and fp else None
            emitted: list[PlanItem] = []
            if not self._is_flex(slot):
                emitted = self._fill_window(
                    slot_idx,
                    self._content_for(slot_idx, content),
                    window_ms=window_ms,
                    natural_ms=_span(t, slot_end),
                    limit_ms=None if live_at is None else _span(t, live_at),
                    at_slot_start=t == slot_start,
                    pool_key=pool_key,
                )
            out.extend(emitted)
            used = sum(p.duration_ms for p in emitted)
            if used < window_ms:
                out.append(self._pad_ms(window_ms - used, pool_key))
            # items play back to back: an episode that ran past the slot (by the
            # overrun tolerance) delays the next slot's start, which absorbs it, so
            # the grid never drifts from the clock
            t = max(window_end, _after(t, used))
        return out

    def _fill_window(
        self,
        slot_idx: int,
        pool: list[ContentItem],
        *,
        window_ms: int,
        natural_ms: int,
        limit_ms: int | None,
        at_slot_start: bool,
        pool_key: str | None,
    ) -> list[PlanItem]:
        """The slot's queue, in order, for as long as each item fits what is left of
        the window (an item may run past the end by the planner's overrun tolerance,
        never into a live broadcast: ``limit_ms``). An item longer than the whole slot
        can never air in it and is passed over; one that would fit an empty window
        waits for the next airing."""
        pool = [c for c in pool if c.duration_ms > 0]
        if not pool:
            return []
        src = self._all[slot_idx].get("source")
        src = src if isinstance(src, dict) else {}
        nonstop = src.get("pattern") == "nonstop"
        clock = nonstop and int(src.get("slot_minutes") or 0) > 0  # one episode a slot
        b2b = nonstop and not clock  # back to back: never a gap
        if clock and not at_slot_start:
            return []  # an episode starts on its slot (after a live broadcast: filler)
        limit = limit_ms if limit_ms is not None else 1 << 62
        room = min(window_ms + self._tolerance_ms, limit)
        whole = natural_ms + self._tolerance_ms
        picks: list[ContentItem] = []
        acc = 0
        key = self._queue_key(slot_idx)
        rr = self._rr.get(key, 0) % len(pool)
        passed = 0  # items in a row too long for the slot: all of them means none fits
        while acc < window_ms and passed < len(pool):
            c = pool[rr % len(pool)]
            if b2b:
                if acc + c.duration_ms > limit:
                    break  # it would run into a live broadcast: filler up to it
            else:
                if c.duration_ms > whole:
                    rr += 1
                    passed += 1
                    continue
                if acc + c.duration_ms > room:
                    # dizqueTV's maxFlex: a gap longer than slot_max_filler_minutes takes
                    # the next episode though it runs past the slot (never into a live one)
                    long_gap = (
                        not clock
                        and bool(picks)
                        and self._max_gap_ms > 0
                        and window_ms - acc > self._max_gap_ms
                        and acc + c.duration_ms <= limit
                    )
                    if not long_gap:
                        break  # its turn comes at the next airing; the rest is padded
            picks.append(c)
            acc += c.duration_ms
            rr += 1
            passed = 0
            if clock:
                break
        self._rr[key] = rr
        items = [
            PlanItem(video_id=c.video_id, duration_ms=c.duration_ms, snapshot=c.to_snapshot())
            for c in picks
        ]
        spare = window_ms - acc
        if self._between and not nonstop and len(items) > 1 and spare > 0:
            share = spare // len(items)
            spread: list[PlanItem] = []
            for n, it in enumerate(items):
                spread.append(it)
                if n < len(items) - 1 and share > 0:
                    spread.append(self._pad_ms(share, pool_key))
            return spread
        return items

    @staticmethod
    def _pad(start: datetime, end: datetime) -> PlanItem:
        return PlanItem(video_id=None, duration_ms=max(_span(start, end), 0), kind=BlockKind.FILLER)

    @staticmethod
    def _pad_ms(ms: int, pool: str | None = None) -> PlanItem:
        return PlanItem(video_id=None, duration_ms=max(ms, 0), kind=BlockKind.FILLER, pool=pool)

    def rewind(self, dropped: Sequence[UUID], content: list[ContentItem]) -> None:
        """Put each queue back on the first of ``dropped`` (the programmes of a planned
        future a rewrite replaces, in airing order) it holds, so the rewrite goes on
        with the show where the channel left it on air (dizqueTV's keep_airing)."""
        done: set[str] = set()
        for idx in range(len(self._all)):
            key = self._queue_key(idx)
            if key in done:
                continue
            done.add(key)
            queue = [c for c in self._content_for(idx, content) if c.duration_ms > 0]
            first: dict[UUID, int] = {}
            for i, c in enumerate(queue):
                first.setdefault(c.video_id, i)
            for vid in dropped:
                if vid in first:
                    self._rr[key] = first[vid]
                    break

    def export_state(self) -> dict[str, Any]:
        return {"rr": dict(self._rr)}


def _span(a: datetime, b: datetime) -> int:
    """Elapsed ms from ``a`` to ``b``. Subtracting two datetimes of one zone compares
    their wall clocks (a DST change is not counted), so both go to UTC first."""
    if a.tzinfo is None or b.tzinfo is None:
        return int((b - a) / _MS)
    return int((b.astimezone(UTC) - a.astimezone(UTC)) / _MS)


def _after(t: datetime, ms: int) -> datetime:
    """``t`` plus ``ms`` of elapsed time, in ``t``'s zone (DST-aware, unlike ``+``)."""
    if t.tzinfo is None:
        return t + timedelta(milliseconds=ms)
    return (t.astimezone(UTC) + timedelta(milliseconds=ms)).astimezone(t.tzinfo)
