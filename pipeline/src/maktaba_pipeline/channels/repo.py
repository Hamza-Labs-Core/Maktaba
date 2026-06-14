"""Database access for the channel scheduler (slots 0081/0082).

:class:`SqlRepo` is the production wiring over the pipeline connection
facade; the pass (and its tests) depend only on the :class:`ScheduleRepo`
Protocol, so a fake repo drives the planner/packer logic without a
database. SQL is parameterised with ``$N`` placeholders (the SQLite path
rewrites them at the connection-wrapper layer, the same convention as
``db/jobs.py``).

Content resolution is intentionally best-effort: it selects the library's
playable videos. Epic 26 enrichment (genre for smart-mix, season/episode
for marathon) is layered in when the columns are present; when absent the
planners degrade exactly as documented (smart-mix → shuffle, marathon →
title order). The ``smart_query`` ``source_filter`` evaluator is a future
reuse hook (Plan 27.2 §6) — until then resolution is library-scoped.
"""

from __future__ import annotations

import json
from contextlib import AbstractAsyncContextManager
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any, Protocol
from uuid import UUID

from .base import Block, ChannelDef, ChannelMode, ContentItem, FillerItem

__all__ = ["ScheduleState", "ScheduleRepo", "SqlRepo"]


@dataclass(slots=True)
class ScheduleState:
    """The per-channel generator cursor (slot 0082
    ``channel_schedule_state``)."""

    channel_id: UUID
    anchor_at: datetime | None = None
    horizon_until: datetime | None = None
    last_generated_at: datetime | None = None
    cursor: dict[str, Any] = field(default_factory=dict)
    stale: bool = False


class _Row(Protocol):
    def __getitem__(self, key: str) -> Any: ...


class DBConn(Protocol):
    """Minimal async connection shape (matches ``db/jobs.py``)."""

    dialect: str

    def transaction(self) -> AbstractAsyncContextManager[Any]: ...

    async def fetch(self, sql: str, *args: Any) -> list[_Row]: ...

    async def fetchrow(self, sql: str, *args: Any) -> _Row | None: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


class ScheduleRepo(Protocol):
    """The surface the pass needs. Production is :class:`SqlRepo`; tests
    supply a fake."""

    async def load_channel(self, channel_id: UUID) -> ChannelDef | None: ...

    async def load_state(self, channel_id: UUID) -> ScheduleState: ...

    async def resolve_content(self, channel: ChannelDef) -> list[ContentItem]: ...

    async def load_filler(self, channel: ChannelDef) -> list[FillerItem]: ...

    async def max_seq_before(self, channel_id: UUID, anchor: datetime) -> int: ...

    async def replace_future_blocks(
        self, channel_id: UUID, *, from_at: datetime, blocks: list[Block]
    ) -> None: ...

    async def save_state(self, state: ScheduleState) -> None: ...

    async def channels_needing_topup(self, now: datetime, low_water: datetime) -> list[UUID]: ...


class SqlRepo:
    """Production repo over a pipeline DB connection."""

    def __init__(self, conn: DBConn) -> None:
        self._c = conn

    async def load_channel(self, channel_id: UUID) -> ChannelDef | None:
        row = await self._c.fetchrow(
            """
            SELECT id, library_id, mode, mode_config, source_filter, transition, enabled
            FROM channels WHERE id = $1
            """,
            channel_id,
        )
        if row is None:
            return None
        return ChannelDef(
            id=UUID(str(row["id"])),
            mode=ChannelMode(row["mode"]),
            mode_config=_json(row["mode_config"]),
            source_filter=_json(row["source_filter"]) or None,
            library_id=UUID(str(row["library_id"])) if row["library_id"] else None,
            enabled=bool(row["enabled"]),
            transition=row["transition"],
        )

    async def load_state(self, channel_id: UUID) -> ScheduleState:
        row = await self._c.fetchrow(
            """
            SELECT channel_id, anchor_at, horizon_until, last_generated_at, cursor, stale
            FROM channel_schedule_state WHERE channel_id = $1
            """,
            channel_id,
        )
        if row is None:
            return ScheduleState(channel_id=channel_id)
        return ScheduleState(
            channel_id=channel_id,
            anchor_at=row["anchor_at"],
            horizon_until=row["horizon_until"],
            last_generated_at=row["last_generated_at"],
            cursor=_json(row["cursor"]),
            stale=bool(row["stale"]),
        )

    async def resolve_content(self, channel: ChannelDef) -> list[ContentItem]:
        if channel.library_id is not None:
            rows = await self._c.fetch(
                """
                SELECT id, title, duration_sec
                FROM videos
                WHERE library_id = $1 AND duration_sec IS NOT NULL AND duration_sec > 0
                  AND deleted_at IS NULL
                ORDER BY title ASC
                """,
                channel.library_id,
            )
        else:
            rows = await self._c.fetch(
                """
                SELECT id, title, duration_sec
                FROM videos
                WHERE duration_sec IS NOT NULL AND duration_sec > 0 AND deleted_at IS NULL
                ORDER BY title ASC
                """,
            )
        out: list[ContentItem] = []
        for r in rows:
            dur_ms = int(float(r["duration_sec"]) * 1000)
            out.append(
                ContentItem(
                    video_id=UUID(str(r["id"])),
                    duration_ms=dur_ms,
                    title=r["title"] or "",
                )
            )
        return out

    async def load_filler(self, channel: ChannelDef) -> list[FillerItem]:
        # Filler pools land in slot 0085 (Story 27.10); until then there
        # is no filler and the packer falls back to slate (D6/D9).
        return []

    async def max_seq_before(self, channel_id: UUID, anchor: datetime) -> int:
        row = await self._c.fetchrow(
            "SELECT max(seq) AS m FROM channel_programs WHERE channel_id = $1 AND start_at < $2",
            channel_id,
            anchor,
        )
        if row is None or row["m"] is None:
            return -1
        return int(row["m"])

    async def replace_future_blocks(
        self, channel_id: UUID, *, from_at: datetime, blocks: list[Block]
    ) -> None:
        async with self._c.transaction():
            # D3: only the future tail is rewritten — past/current blocks
            # (start_at < from_at) are immutable.
            await self._c.execute(
                "DELETE FROM channel_programs WHERE channel_id = $1 AND start_at >= $2",
                channel_id,
                from_at,
            )
            for b in blocks:
                await self._c.execute(
                    """
                    INSERT INTO channel_programs
                        (channel_id, seq, kind, video_id, filler_item_id,
                         start_at, end_at, source_offset, source_duration, title_snapshot)
                    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
                    """,
                    channel_id,
                    b.seq,
                    b.kind.value,
                    b.video_id,
                    b.filler_item_id,
                    b.start_at,
                    b.end_at,
                    b.source_offset,
                    b.source_duration,
                    json.dumps(b.title_snapshot),
                )

    async def save_state(self, state: ScheduleState) -> None:
        await self._c.execute(
            """
            INSERT INTO channel_schedule_state
                (channel_id, anchor_at, horizon_until, last_generated_at, cursor, stale)
            VALUES ($1,$2,$3,$4,$5,false)
            ON CONFLICT (channel_id) DO UPDATE SET
                anchor_at = EXCLUDED.anchor_at,
                horizon_until = EXCLUDED.horizon_until,
                last_generated_at = EXCLUDED.last_generated_at,
                cursor = EXCLUDED.cursor,
                stale = false
            """,
            state.channel_id,
            state.anchor_at,
            state.horizon_until,
            state.last_generated_at,
            json.dumps(state.cursor),
        )

    async def channels_needing_topup(self, now: datetime, low_water: datetime) -> list[UUID]:
        rows = await self._c.fetch(
            """
            SELECT c.id AS id
            FROM channels c
            LEFT JOIN channel_schedule_state s ON s.channel_id = c.id
            WHERE c.enabled = true
              AND (s.horizon_until IS NULL OR s.horizon_until < $1 OR s.stale = true)
            """,
            low_water,
        )
        return [UUID(str(r["id"])) for r in rows]


def _json(val: Any) -> dict[str, Any]:
    """Decode a JSONB/TEXT column into a dict (asyncpg may hand back a
    str on SQLite; Postgres returns the parsed value)."""
    if val is None:
        return {}
    if isinstance(val, dict):
        return val
    if isinstance(val, (str, bytes)):
        try:
            parsed = json.loads(val)
            return parsed if isinstance(parsed, dict) else {}
        except (ValueError, TypeError):
            return {}
    return {}
