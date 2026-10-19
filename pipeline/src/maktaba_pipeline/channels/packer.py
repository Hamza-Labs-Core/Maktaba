"""The packer — lay :class:`PlanItem`s onto the absolute timeline (D2/D6).

The contiguity invariant is the whole epic: every block's ``end_at``
equals the next block's ``start_at``, with no gaps and no overlaps, so
the wall-clock anchoring downstream (live engine + guide) stays correct.
The packer is where padding happens — a program rarely fills its slot
exactly, so the gap to the next boundary (and the tail up to the horizon)
is always closed with filler clips, the slate taking what no clip fits (D6/D9).
Pure: no clock, no DB.
"""

from __future__ import annotations

from datetime import datetime, timedelta
from uuid import UUID

from .base import Block, BlockKind, FillerItem, PlanItem
from .filler import FillerPicker, FillerSet

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
    filler: FillerPicker | FillerSet | list[FillerItem] | None = None,
    base_seq: int = 0,
    slate_title: str = "Up Next",
) -> list[Block]:
    """Lay ``items`` from ``start_at``, padding every gap, covering up to
    ``until``. Returns a contiguous, monotonically-sequenced block list.

    A PROGRAM item becomes one program block (trimmed if it would cross
    ``until``); a LIVE item one live block. A FILLER/pad item, or any leftover gap to
    ``until``, is closed by :func:`_fill`: clips one after the other, chosen by the
    channel's filler lists (:class:`~.filler.FillerPicker`), then a slate for what no
    clip fits (D9).
    """
    picker = filler if isinstance(filler, FillerPicker) else FillerPicker(filler)
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
        if end <= t:
            continue
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
        elif item.kind == BlockKind.LIVE:
            blocks.append(
                Block(
                    channel_id=channel_id,
                    seq=seq,
                    kind=BlockKind.LIVE,
                    start_at=t,
                    end_at=end,
                    source_offset=0,
                    source_duration=int((end - t) / timedelta(milliseconds=1)),
                    title_snapshot=dict(item.snapshot),
                    source_url=item.source_url,
                )
            )
            seq += 1
        else:
            # A pad request: fill [t, end) from the filler lists / slate.
            for b in _fill(channel_id, picker, t, end, seq, slate_title, item.pool):
                blocks.append(b)
                seq += 1
        t = end

    # Guarantee coverage up to the horizon.
    if t < until:
        for b in _fill(channel_id, picker, t, until, seq, slate_title, None):
            blocks.append(b)
            seq += 1
        t = until

    _assert_contiguous(blocks)
    return blocks


def _fill(
    channel_id: UUID,
    picker: FillerPicker,
    start: datetime,
    end: datetime,
    seq: int,
    slate_title: str,
    pool: str | None,
) -> list[Block]:
    """Close ``[start, end)`` with clips back to back (each its own block, played whole
    from its start), then a slate block for what is left when no clip fits it (D9)."""
    if end <= start:
        return []
    out: list[Block] = []
    t = start
    for clip, at in picker.fill(start, end, pool):
        stop = at + timedelta(milliseconds=clip.duration_ms)
        out.append(
            Block(
                channel_id=channel_id,
                seq=seq + len(out),
                kind=clip.kind,
                start_at=at,
                end_at=stop,
                source_offset=0,
                source_duration=clip.duration_ms,
                video_id=clip.video_id,
                filler_item_id=clip.filler_item_id,
                title_snapshot=dict(clip.snapshot) or {"title": slate_title},
            )
        )
        t = stop
    if t < end:
        out.append(
            Block(
                channel_id=channel_id,
                seq=seq + len(out),
                kind=BlockKind.SLATE,
                start_at=t,
                end_at=end,
                source_offset=0,
                source_duration=int((end - t) / timedelta(milliseconds=1)),
                title_snapshot={"title": slate_title},
            )
        )
    return out


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
