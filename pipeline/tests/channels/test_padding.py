"""Padding semantics — fit, coalesce, slate fallback (D6/D9)."""

from __future__ import annotations

from datetime import timedelta
from uuid import uuid4

from maktaba_pipeline.channels import packer
from maktaba_pipeline.channels.base import BlockKind, PlanItem

from .helpers import dt, mk_filler


def _program(ms: int):
    return PlanItem(video_id=uuid4(), duration_ms=ms, snapshot={"title": "x"})


def test_gap_coalesced_into_single_filler_block():
    # A 55-minute gap filled by 1-minute bumpers must NOT shred into 55
    # rows — it coalesces into ONE looping filler block (EC8).
    start = dt("2026-06-14T00:00:00")
    until = start + timedelta(hours=1)
    blocks = packer.pack(
        [_program(5 * 60_000)],
        channel_id=uuid4(),
        start_at=start,
        until=until,
        filler=mk_filler(2, dur_ms=60_000),
    )
    filler_blocks = [b for b in blocks if b.kind == BlockKind.FILLER]
    assert len(filler_blocks) == 1
    assert filler_blocks[0].source_duration == 55 * 60_000


def test_slate_when_no_filler():
    start = dt("2026-06-14T00:00:00")
    until = start + timedelta(hours=1)
    blocks = packer.pack([_program(5 * 60_000)], channel_id=uuid4(), start_at=start, until=until)
    assert blocks[-1].kind == BlockKind.SLATE
    assert blocks[-1].title_snapshot.get("title")


def test_no_pad_when_programs_fill_exactly():
    start = dt("2026-06-14T00:00:00")
    until = start + timedelta(hours=1)
    blocks = packer.pack(
        [_program(30 * 60_000), _program(30 * 60_000)],
        channel_id=uuid4(),
        start_at=start,
        until=until,
        filler=mk_filler(),
    )
    assert all(b.kind == BlockKind.PROGRAM for b in blocks)


def test_empty_items_all_slate():
    start = dt("2026-06-14T00:00:00")
    until = start + timedelta(hours=2)
    blocks = packer.pack([], channel_id=uuid4(), start_at=start, until=until)
    assert len(blocks) == 1
    assert blocks[0].kind == BlockKind.SLATE
    assert blocks[0].start_at == start and blocks[0].end_at == until
