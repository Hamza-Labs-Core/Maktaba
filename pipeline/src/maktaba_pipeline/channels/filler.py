"""Filling a gap with clips, the way dizqueTV fills a channel's flex time.

A channel has weighted filler lists (``mode_config.filler.lists``: the plan's
``<channel>.topic`` / ``.promos`` / ``.breaks`` / ``library.general`` pools with the
planner's ``dz_weight_*``), each with its own cooldown, and a repeat cooldown for one
clip (``dz_filler_cooldown_ms``). A gap is filled clip after clip: a list is drawn by
weight among those off cooldown with a clip that fits what is left, then one of its
fitting clips not played within the repeat cooldown. A slot that names its own pool
(``filler_pool``) tries that pool first (the ``tv`` rule ``slot_filler_first``). What no
clip fits, or what is left after ``max_filler_per_gap`` clips, is the slate.

The picker remembers when each clip and list last played (``export_state``), so a
horizon top-up keeps the cooldowns. Pure: no clock, no DB.
"""

from __future__ import annotations

import random
from bisect import bisect_right
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from datetime import UTC, datetime, timedelta
from typing import Any

from .base import FillerItem

__all__ = ["FillerList", "FillerPicker", "FillerSet"]

_EPOCH = datetime(1970, 1, 1, tzinfo=UTC)


def _ms(t: datetime) -> int:
    return int((t - _EPOCH) / timedelta(milliseconds=1))


@dataclass(frozen=True, slots=True)
class FillerList:
    """One filler list of a channel (or a slot's own pool)."""

    key: str
    items: tuple[FillerItem, ...]
    weight: int = 1
    cooldown_ms: int = 0


@dataclass(frozen=True, slots=True)
class FillerSet:
    """What a channel fills its gaps from: its weighted ``lists``, the slots' own
    ``pools`` (by plan key) and the repeat cooldown of a single clip."""

    lists: tuple[FillerList, ...] = ()
    pools: Mapping[str, FillerList] = field(default_factory=dict)
    repeat_cooldown_ms: int = 0

    @classmethod
    def of(cls, items: Sequence[FillerItem]) -> FillerSet:
        """A plain clip list as one list of weight 1."""
        return cls(lists=(FillerList("default", tuple(items)),) if items else ())

    @property
    def empty(self) -> bool:
        return not any(x.items for x in self.lists) and not any(
            x.items for x in self.pools.values()
        )


class FillerPicker:
    def __init__(
        self,
        filler: FillerSet | Sequence[FillerItem] | None,
        *,
        max_per_gap: int = 0,
        slot_first: bool = True,
        seed: int | None = None,
        state: Mapping[str, Any] | None = None,
    ) -> None:
        if filler is None:
            filler = FillerSet()
        elif not isinstance(filler, FillerSet):
            filler = FillerSet.of(list(filler))
        self.set = filler
        self.max_per_gap = max(0, int(max_per_gap))
        self.slot_first = slot_first
        self._rng = random.Random(seed)
        state = state or {}
        self._clip_at: dict[str, int] = {str(k): int(v) for k, v in state.get("clips", {}).items()}
        self._list_at: dict[str, int] = {str(k): int(v) for k, v in state.get("lists", {}).items()}
        self._by_len: dict[str, list[FillerItem]] = {}
        self._lens: dict[str, list[int]] = {}

    @property
    def empty(self) -> bool:
        return self.set.empty

    def fill(
        self, start: datetime, end: datetime, pool: str | None = None
    ) -> list[tuple[FillerItem, datetime]]:
        """The clips that fill ``[start, end)``, each with its start, back to back from
        ``start``. What they leave at the end is the caller's slate."""
        out: list[tuple[FillerItem, datetime]] = []
        t = start
        own = self.set.pools.get(pool) if pool and self.slot_first else None
        while self.max_per_gap == 0 or len(out) < self.max_per_gap:
            left = int((end - t) / timedelta(milliseconds=1))
            if left <= 0:
                break
            got = None
            if own is not None:
                got = self._pick([own], t, left, weighted=False)
            if got is None:
                lists = list(self.set.lists)
                if not lists and pool and pool in self.set.pools:  # a slot pool, no lists
                    lists = [self.set.pools[pool]]
                got = self._pick(lists, t, left, weighted=True)
            if got is None:
                break
            lst, clip = got
            now = _ms(t)
            self._clip_at[self._clip_key(clip)] = now
            self._list_at[lst.key] = now
            out.append((clip, t))
            t += timedelta(milliseconds=clip.duration_ms)
        return out

    def _pick(
        self, lists: Sequence[FillerList], t: datetime, left: int, *, weighted: bool
    ) -> tuple[FillerList, FillerItem] | None:
        now = _ms(t)
        ready: list[tuple[FillerList, int]] = []  # (list, how many of its clips fit)
        for lst in lists:
            last = self._list_at.get(lst.key)
            if lst.cooldown_ms > 0 and last is not None and now - last < lst.cooldown_ms:
                continue
            n = bisect_right(self._durations(lst), left)
            if n:
                ready.append((lst, n))
        while ready:
            if weighted and len(ready) > 1:
                weights = [max(1, x.weight) for x, _ in ready]
                at = self._rng.choices(range(len(ready)), weights=weights, k=1)[0]
            else:
                at = 0
            lst, n = ready[at]
            clip = self._clip_in(self._sorted(lst)[:n], now)
            if clip is not None:
                return lst, clip
            ready.pop(at)  # every clip of it that fits is cooling down
        return None

    def _clip_in(self, fits: Sequence[FillerItem], now: int) -> FillerItem | None:
        """A random clip of ``fits`` not played within the repeat cooldown: a few random
        draws first (a long list rarely needs more), then the ones left."""
        rep = self.set.repeat_cooldown_ms

        def cool(c: FillerItem) -> bool:
            at = self._clip_at.get(self._clip_key(c))
            return rep <= 0 or at is None or now - at >= rep

        for _ in range(min(16, len(fits))):
            c = fits[self._rng.randrange(len(fits))]
            if cool(c):
                return c
        free = [c for c in fits if cool(c)]
        return free[self._rng.randrange(len(free))] if free else None

    def _sorted(self, lst: FillerList) -> list[FillerItem]:
        got = self._by_len.get(lst.key)
        if got is None:
            got = sorted((c for c in lst.items if c.duration_ms > 0), key=lambda c: c.duration_ms)
            self._by_len[lst.key] = got
            self._lens[lst.key] = [c.duration_ms for c in got]
        return got

    def _durations(self, lst: FillerList) -> list[int]:
        self._sorted(lst)
        return self._lens[lst.key]

    @staticmethod
    def _clip_key(c: FillerItem) -> str:
        return str(c.video_id or c.filler_item_id)

    def export_state(self, *, keep_after: datetime | None = None) -> dict[str, Any]:
        """When each clip and list last played, without what no cooldown can reach from
        ``keep_after`` (the horizon) any more."""
        longest = max(
            [self.set.repeat_cooldown_ms]
            + [x.cooldown_ms for x in self.set.lists]
            + [x.cooldown_ms for x in self.set.pools.values()]
        )
        if longest <= 0:
            return {}
        floor = _ms(keep_after) - longest if keep_after is not None else None

        def keep(d: dict[str, int]) -> dict[str, int]:
            return {k: v for k, v in d.items() if floor is None or v >= floor}

        return {"clips": keep(self._clip_at), "lists": keep(self._list_at)}
