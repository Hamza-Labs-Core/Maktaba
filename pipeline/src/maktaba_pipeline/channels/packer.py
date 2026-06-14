"""The packer — lay :class:`PlanItem`s onto the absolute timeline (D2/D6).

The contiguity invariant is the whole epic: every block's ``end_at``
equals the next block's ``start_at``, with no gaps and no overlaps, so
the wall-clock anchoring downstream (live engine + guide) stays correct.
The packer is where padding happens — a program rarely fills its slot
exactly, so the gap to the next boundary (and the tail up to the horizon)
is always closed with filler, falling back to a slate when no filler is
available (D6/D9). Pure: no clock, no DB.
"""

from __future__ import annotations

from datetime import datetime, timedelta
from uuid import UUID

from .base import Block, BlockKind, FillerItem, PlanItem

__all__ = ["pack", "ContiguityError"]


class ContiguityError(RuntimeError):
    """Raised if the packed timeline has a gap or overlap — a bug guard,
    never expected to fire in production (the packer constructs blocks
    contiguously by construction)."""


def pack(
    items: list[PlanItem],
    *,
    channel_id: UUID,
    start_at: datetime,
    until: datetime,
    filler: list[FillerItem] | None = None,
    base_seq: int = 0,
    slate_title: str = "Up Next",
) -> list[Block]:
    """Lay ``items`` from ``start_at``, padding every gap, covering up to
    ``until``. Returns a contiguous, monotonically-sequenced block list.

    A PROGRAM item becomes one program block (trimmed if it would cross
    ``until``). A FILLER/pad item, or any leftover gap to ``until``, is
    closed by :func:`_fill` — coalescing short clips into one looping
    filler block (EC8) or a slate when no filler exists (D9).
    """
    filler = filler or []
    blocks: list[Block] = []
    t = start_at
    seq = base_seq

    for item in items:
        if t >= until:
            break
        dur = timedelta(milliseconds=item.duration_ms)
        end = t + dur
        if end > until:
            end = until
        if item.kind == BlockKind.PROGRAM and item.video_id is not None:
            played_ms = int((end - t) / timedelta(milliseconds=1))
            blocks.append(
                Block(
                    channel_id=channel_id,
                    seq=seq,
                    kind=BlockKind.PROGRAM,
                    start_at=t,
                    end_at=end,
                    source_offset=item.offset_ms,
                    source_duration=played_ms,
                    video_id=item.video_id,
                    title_snapshot=dict(item.snapshot),
                )
            )
            seq += 1
        else:
            # A pad request: fill [t, end) from the filler pool / slate.
            for b in _fill(channel_id, filler, t, end, seq, slate_title):
                blocks.append(b)
                seq += 1
        t = end

    # Guarantee coverage up to the horizon.
    if t < until:
        for b in _fill(channel_id, filler, t, until, seq, slate_title):
            blocks.append(b)
            seq += 1
        t = until

    _assert_contiguous(blocks)
    return blocks


def _fill(
    channel_id: UUID,
    filler: list[FillerItem],
    start: datetime,
    end: datetime,
    seq: int,
    slate_title: str,
) -> list[Block]:
    """Close ``[start, end)`` with filler, coalesced into a single looping
    block (EC8), or a slate block when no filler exists (D9). Always
    returns exactly one block so sequencing stays simple."""
    span_ms = int((end - start) / timedelta(milliseconds=1))
    if span_ms <= 0:
        return []
    if not filler:
        return [
            Block(
                channel_id=channel_id,
                seq=seq,
                kind=BlockKind.SLATE,
                start_at=start,
                end_at=end,
                source_offset=0,
                source_duration=span_ms,
                title_snapshot={"title": slate_title},
            )
        ]
    # Coalesce: one filler block referencing the first pool item, spanning
    # the whole gap (the live engine loops the clip to fill the window).
    head = filler[0]
    return [
        Block(
            channel_id=channel_id,
            seq=seq,
            kind=head.kind,
            start_at=start,
            end_at=end,
            source_offset=0,
            source_duration=span_ms,
            video_id=head.video_id,
            filler_item_id=head.filler_item_id,
            title_snapshot=dict(head.snapshot) or {"title": slate_title},
        )
    ]


def _assert_contiguous(blocks: list[Block]) -> None:
    """Enforce the no-gap / no-overlap invariant (D2)."""
    for i in range(len(blocks) - 1):
        if blocks[i].end_at != blocks[i + 1].start_at:
            raise ContiguityError(
                f"gap/overlap between seq {blocks[i].seq} (ends {blocks[i].end_at}) "
                f"and seq {blocks[i + 1].seq} (starts {blocks[i + 1].start_at})"
            )
        if blocks[i].end_at <= blocks[i].start_at:
            raise ContiguityError(f"non-positive block at seq {blocks[i].seq}")
