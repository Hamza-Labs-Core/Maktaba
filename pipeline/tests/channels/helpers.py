"""Shared builders + an in-memory fake repo for the scheduler tests.

Keeping the test doubles here lets every test exercise the real planner /
packer / pass logic with zero database — the whole point of the pure-
logic split (Plan 27.2 §1).
"""

from __future__ import annotations

from datetime import UTC, datetime
from typing import Any
from uuid import UUID, uuid4

from maktaba_pipeline.channels.base import (
    Block,
    ChannelDef,
    ChannelMode,
    ContentItem,
    FillerItem,
)
from maktaba_pipeline.channels.repo import ScheduleState


def dt(s: str) -> datetime:
    """Parse 'YYYY-MM-DDTHH:MM:SS' as a UTC datetime."""
    return datetime.fromisoformat(s).replace(tzinfo=UTC)


def mk_channel(
    mode: ChannelMode,
    *,
    mode_config: dict[str, Any] | None = None,
    library_id: UUID | None = None,
    channel_id: UUID | None = None,
) -> ChannelDef:
    return ChannelDef(
        id=channel_id or uuid4(),
        mode=mode,
        mode_config=mode_config or {},
        source_filter=None,
        library_id=library_id or uuid4(),
        enabled=True,
    )


def mk_content(n: int, *, dur_ms: int = 30 * 60_000) -> list[ContentItem]:
    """n videos each `dur_ms` long (default 30 min)."""
    return [
        ContentItem(video_id=uuid4(), duration_ms=dur_ms, title=f"Video {i:02d}") for i in range(n)
    ]


def mk_filler(n: int = 2, *, dur_ms: int = 60_000) -> list[FillerItem]:
    return [
        FillerItem(
            filler_item_id=uuid4(),
            video_id=uuid4(),
            duration_ms=dur_ms,
            snapshot={"title": f"Bumper {i}"},
        )
        for i in range(n)
    ]


class FakeRepo:
    """In-memory :class:`ScheduleRepo` for the pass tests. Blocks with
    ``start_at < from_at`` survive a regen (modelling the immutable past),
    so a top-up test can assert the current/past tail is untouched."""

    def __init__(
        self,
        channel: ChannelDef,
        content: list[ContentItem],
        *,
        filler: list[FillerItem] | None = None,
        state: ScheduleState | None = None,
    ) -> None:
        self.channel = channel
        self.content = content
        self.filler = filler or []
        self.state = state or ScheduleState(channel_id=channel.id)
        self.blocks: list[Block] = []
        self.save_calls = 0

    async def load_channel(self, channel_id: UUID) -> ChannelDef | None:
        return self.channel if channel_id == self.channel.id else None

    async def load_state(self, channel_id: UUID) -> ScheduleState:
        return self.state

    async def resolve_content(self, channel: ChannelDef) -> list[ContentItem]:
        return self.content

    async def load_filler(self, channel: ChannelDef) -> list[FillerItem]:
        return self.filler

    async def max_seq_before(self, channel_id: UUID, anchor: datetime) -> int:
        surviving = [b.seq for b in self.blocks if b.start_at < anchor]
        return max(surviving) if surviving else -1

    async def replace_future_blocks(
        self, channel_id: UUID, *, from_at: datetime, blocks: list[Block]
    ) -> None:
        self.blocks = [b for b in self.blocks if b.start_at < from_at] + list(blocks)

    async def save_state(self, state: ScheduleState) -> None:
        self.state = state
        self.save_calls += 1

    async def channels_needing_topup(self, now: datetime, low_water: datetime) -> list[UUID]:
        hu = self.state.horizon_until
        if self.channel.enabled and (hu is None or hu < low_water or self.state.stale):
            return [self.channel.id]
        return []
