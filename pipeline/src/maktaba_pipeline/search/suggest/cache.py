"""LRU + TTL cache for suggest results.

The cache sits in front of :class:`SuggestService` so that a typing
user does not hit the database for every keystroke. Entries expire
``ttl_sec`` seconds after insertion and the oldest non-expired entry
is evicted once the cache hits ``max_entries``. Both bounds are
intentionally tiny — the working set is a handful of prefixes per
user per minute and the values themselves are short lists of
:class:`Suggestion` dataclasses.
"""

from __future__ import annotations

import time
from collections import OrderedDict
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from .service import Suggestion

__all__ = ["SuggestCache"]


class SuggestCache:
    """Tiny LRU cache with a monotonic-clock TTL.

    Keys are arbitrary strings the caller composes (typically
    ``f"{library_id}|{normalized_prefix}|{limit}"``). Values are
    immutable lists the cache hands back by reference — the caller
    must not mutate them.

    The cache is not thread-safe and not asyncio-safe across loop
    boundaries; callers running multiple concurrent ``suggest`` tasks
    on the same loop are fine because every operation is synchronous
    and atomic from the loop's perspective.
    """

    def __init__(self, *, max_entries: int = 1024, ttl_sec: float = 60.0) -> None:
        if max_entries <= 0:
            raise ValueError("max_entries must be positive")
        if ttl_sec <= 0:
            raise ValueError("ttl_sec must be positive")
        self._max_entries = max_entries
        self._ttl_sec = ttl_sec
        # Insertion-ordered; move_to_end on read keeps it LRU.
        self._data: OrderedDict[str, tuple[float, list[Suggestion]]] = OrderedDict()

    def get(self, key: str) -> list[Suggestion] | None:
        """Return the cached value or ``None`` on miss / expiry.

        Expired entries are removed eagerly on access so the cache
        does not accumulate stale rows. A miss never raises.
        """
        entry = self._data.get(key)
        if entry is None:
            return None
        expires_at, value = entry
        if expires_at <= time.monotonic():
            # Expired: drop and miss.
            self._data.pop(key, None)
            return None
        # LRU touch.
        self._data.move_to_end(key)
        return value

    def put(self, key: str, value: list[Suggestion]) -> None:
        """Insert or refresh an entry.

        If ``key`` already exists, its TTL is reset and it becomes
        the most-recently-used entry. If the cache is at capacity the
        oldest entry is evicted before the new one is inserted.
        """
        expires_at = time.monotonic() + self._ttl_sec
        if key in self._data:
            self._data.pop(key)
        elif len(self._data) >= self._max_entries:
            # Pop the LRU (front of the OrderedDict).
            self._data.popitem(last=False)
        self._data[key] = (expires_at, value)

    def __len__(self) -> int:
        return len(self._data)

    def clear(self) -> None:
        """Drop all entries. Used by tests; production rarely needs it."""
        self._data.clear()
