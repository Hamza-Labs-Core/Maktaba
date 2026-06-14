"""Degenerate-channel slate (D9).

When a channel's source resolves to nothing (empty library, a filter
that matches no videos), the timeline must still be defined and
contiguous so "what's on now" and the live engine have something to
answer with. :func:`rolling` produces a single slate block spanning the
whole horizon — never a gap, never an exception.
"""

from __future__ import annotations

from datetime import datetime, timedelta
from uuid import UUID

from .base import Block, BlockKind

__all__ = ["rolling"]


def rolling(
    channel_id: UUID, start_at: datetime, until: datetime, *, title: str = "No programming"
) -> Block:
    """One slate block covering ``[start_at, until)``."""
    span_ms = int((until - start_at) / timedelta(milliseconds=1))
    return Block(
        channel_id=channel_id,
        seq=0,
        kind=BlockKind.SLATE,
        start_at=start_at,
        end_at=until,
        source_offset=0,
        source_duration=max(span_ms, 0),
        title_snapshot={"title": title},
    )
