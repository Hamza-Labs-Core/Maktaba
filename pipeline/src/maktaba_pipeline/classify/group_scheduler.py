"""Debounced, coalesced library-level group passes (Story 26.7 §4/D5).

Series detection (26.3) and the auto-collection builder (26.4) are
**library-level** passes, not per-video: adding one episode can rename a
whole series or move a collection's membership, so running them per-video
would thrash. Instead a per-library debounce timer resets on each
``classify``/``enrich`` completion and, when it finally fires, runs both
passes **once** for that library — O(batches), not O(videos).

Coalescing is anchored by a *persisted* marker
(``library_group_pending``, slot 0079) rather than only the in-memory
timer, so a crash with a pending pass still runs it on restart (Story
26.7 resume): :func:`take_group_pending` is a claim — the first caller to
take the marker runs the passes; concurrent/duplicate fires find it gone
and no-op.
"""

from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable
from contextlib import AbstractAsyncContextManager
from datetime import UTC, datetime
from typing import Any, Protocol
from uuid import UUID

__all__ = [
    "GroupScheduler",
    "mark_group_pending",
    "run_group",
    "take_group_pending",
]

GROUP_DEBOUNCE_SEC = 30.0

GroupPass = Callable[["Conn", UUID], Awaitable[None]]


class _Row(Protocol):
    def __getitem__(self, key: str) -> Any: ...


class Conn(Protocol):
    dialect: str

    def transaction(self) -> AbstractAsyncContextManager[Any]: ...

    async def fetchrow(self, sql: str, *args: Any) -> _Row | None: ...


_MARK_SQL = """
INSERT INTO library_group_pending (library_id, marked_at)
VALUES ($1, $2)
ON CONFLICT (library_id) DO UPDATE SET marked_at = EXCLUDED.marked_at
RETURNING library_id
"""

_TAKE_SQL = """
DELETE FROM library_group_pending WHERE library_id = $1 RETURNING library_id
"""


async def mark_group_pending(conn: Conn, library_id: UUID, *, now: datetime | None = None) -> None:
    """Record that a group pass is owed for ``library_id`` (idempotent)."""
    await conn.fetchrow(_MARK_SQL, str(library_id), now or datetime.now(UTC))


async def take_group_pending(conn: Conn, library_id: UUID) -> bool:
    """Atomically claim the pending marker; ``True`` if one was owed.

    The DELETE … RETURNING makes this a one-shot claim: many debounce
    fires (or a restart-drain racing a live fire) collapse to a single
    group pass — the second caller finds no row and returns ``False``.
    """
    row = await conn.fetchrow(_TAKE_SQL, str(library_id))
    return row is not None


async def run_group(
    conn: Conn,
    library_id: UUID,
    *,
    series_detect: GroupPass,
    auto_collections: GroupPass,
) -> bool:
    """Run both library passes once, gated by the pending claim.

    Returns ``True`` if the passes ran (the marker was owed), ``False`` if
    another fire already coalesced it.
    """
    if not await take_group_pending(conn, library_id):
        return False
    await series_detect(conn, library_id)
    await auto_collections(conn, library_id)
    return True


class GroupScheduler:
    """Per-library debounce that coalesces a burst into one group pass.

    Each :meth:`schedule` marks the library pending (persisted) and
    (re)arms a timer; the timer firing calls ``run_group`` once. A burst
    of N ``classify``/``enrich`` completions for a library therefore
    yields exactly one series-detect + one auto-collection pass — even
    across a restart, because the marker is persisted and drained on
    startup.

    ``delay`` is injectable so tests run with a tiny window instead of the
    30 s production default.
    """

    def __init__(
        self,
        conn: Conn,
        *,
        series_detect: GroupPass,
        auto_collections: GroupPass,
        delay: float = GROUP_DEBOUNCE_SEC,
    ) -> None:
        self._conn = conn
        self._series_detect = series_detect
        self._auto_collections = auto_collections
        self._delay = delay
        self._timers: dict[UUID, asyncio.Task[None]] = {}

    async def schedule(self, library_id: UUID) -> None:
        """Mark the library pending and (re)arm its debounce timer."""
        await mark_group_pending(self._conn, library_id)
        existing = self._timers.get(library_id)
        if existing is not None and not existing.done():
            existing.cancel()
        self._timers[library_id] = asyncio.create_task(self._fire_after(library_id))

    async def _fire_after(self, library_id: UUID) -> None:
        try:
            await asyncio.sleep(self._delay)
        except asyncio.CancelledError:
            return
        await self.flush(library_id)

    async def flush(self, library_id: UUID) -> bool:
        """Run the pending group pass for one library now (used on
        shutdown / startup drain / on-demand regroup)."""
        self._timers.pop(library_id, None)
        return await run_group(
            self._conn,
            library_id,
            series_detect=self._series_detect,
            auto_collections=self._auto_collections,
        )

    async def cancel_all(self) -> None:
        """Cancel outstanding timers (graceful shutdown). The persisted
        markers survive, so a restart drains them."""
        for task in self._timers.values():
            if not task.done():
                task.cancel()
        self._timers.clear()
