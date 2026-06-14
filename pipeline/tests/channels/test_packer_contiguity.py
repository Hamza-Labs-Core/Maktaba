"""Packer contiguity invariant (Plan 27.2 §3 / §8)."""

from __future__ import annotations

import random
from datetime import timedelta
from uuid import uuid4

import pytest

from maktaba_pipeline.channels import packer
from maktaba_pipeline.channels.base import BlockKind, PlanItem

from .helpers import dt, mk_filler


def _program(ms: int) -> PlanItem:
    return PlanItem(video_id=uuid4(), duration_ms=ms, snapshot={"title": "x"})


def test_contiguous_no_gaps_no_overlaps() -> None:
    items = [_program(30 * 60_000), _program(45 * 60_000), _program(15 * 60_000)]
    start = dt("2026-06-14T20:00:00")
    until = start + timedelta(hours=2)
    blocks = packer.pack(items, channel_id=uuid4(), start_at=start, until=until)
    assert blocks[0].start_at == start
    assert blocks[-1].end_at == until
    for a, b in zip(blocks, blocks[1:], strict=False):
        assert a.end_at == b.start_at


def test_seq_is_monotonic_from_base() -> None:
    items = [_program(10 * 60_000) for _ in range(5)]
    start = dt("2026-06-14T00:00:00")
    blocks = packer.pack(
        items, channel_id=uuid4(), start_at=start, until=start + timedelta(hours=1), base_seq=100
    )
    seqs = [b.seq for b in blocks]
    assert seqs == list(range(100, 100 + len(seqs)))


def test_tail_padded_to_horizon_with_slate_when_no_filler() -> None:
    # One 10-minute program then a 50-minute gap to the 1h horizon.
    start = dt("2026-06-14T00:00:00")
    until = start + timedelta(hours=1)
    blocks = packer.pack([_program(10 * 60_000)], channel_id=uuid4(), start_at=start, until=until)
    assert blocks[-1].kind == BlockKind.SLATE
    assert blocks[-1].end_at == until


def test_tail_padded_with_filler_when_available() -> None:
    start = dt("2026-06-14T00:00:00")
    until = start + timedelta(hours=1)
    blocks = packer.pack(
        [_program(10 * 60_000)],
        channel_id=uuid4(),
        start_at=start,
        until=until,
        filler=mk_filler(),
    )
    tail = blocks[-1]
    assert tail.kind == BlockKind.FILLER
    assert tail.filler_item_id is not None
    # Coalesced into a single looping block (EC8), not many micro-blocks.
    assert tail.end_at == until


def test_program_crossing_horizon_is_trimmed() -> None:
    start = dt("2026-06-14T00:00:00")
    until = start + timedelta(minutes=30)
    blocks = packer.pack([_program(60 * 60_000)], channel_id=uuid4(), start_at=start, until=until)
    assert len(blocks) == 1
    assert blocks[0].end_at == until
    assert blocks[0].source_duration == 30 * 60_000


def test_pad_item_filled_inline() -> None:
    start = dt("2026-06-14T00:00:00")
    until = start + timedelta(hours=1)
    items = [
        _program(20 * 60_000),
        PlanItem(video_id=None, duration_ms=10 * 60_000, kind=BlockKind.FILLER),
        _program(20 * 60_000),
    ]
    blocks = packer.pack(items, channel_id=uuid4(), start_at=start, until=until, filler=mk_filler())
    kinds = [b.kind for b in blocks]
    assert BlockKind.PROGRAM in kinds and BlockKind.FILLER in kinds
    for a, b in zip(blocks, blocks[1:], strict=False):
        assert a.end_at == b.start_at


@pytest.mark.parametrize("seed", range(8))
def test_random_inputs_stay_contiguous(seed: int) -> None:
    rng = random.Random(seed)
    items = [_program(rng.randint(1, 90) * 60_000) for _ in range(rng.randint(1, 40))]
    start = dt("2026-06-14T12:00:00")
    until = start + timedelta(hours=rng.randint(1, 12))
    blocks = packer.pack(items, channel_id=uuid4(), start_at=start, until=until, filler=mk_filler())
    assert blocks[0].start_at == start
    assert blocks[-1].end_at == until
    for a, b in zip(blocks, blocks[1:], strict=False):
        assert a.end_at == b.start_at
        assert a.end_at > a.start_at
