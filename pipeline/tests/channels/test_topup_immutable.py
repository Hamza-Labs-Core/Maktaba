"""Pass-level: past/current blocks immutable across regen; degenerate
library never raises (D3/D9)."""

from __future__ import annotations

from datetime import timedelta

import pytest

from maktaba_pipeline.channels import run_schedule, topup_all
from maktaba_pipeline.channels.base import BlockKind, ChannelMode

from .helpers import FakeRepo, dt, mk_channel, mk_content


@pytest.mark.asyncio
async def test_generation_is_contiguous_and_covers_horizon():
    ch = mk_channel(ChannelMode.SHUFFLE)
    repo = FakeRepo(ch, mk_content(6, dur_ms=20 * 60_000))
    now = dt("2026-06-14T20:00:00")
    n = await run_schedule(repo, ch.id, now=now, horizon=timedelta(hours=6))
    assert n > 0
    blocks = sorted(repo.blocks, key=lambda b: b.start_at)
    assert blocks[0].start_at == now
    assert blocks[-1].end_at == now + timedelta(hours=6)
    for a, b in zip(blocks, blocks[1:], strict=False):
        assert a.end_at == b.start_at
    assert repo.state.horizon_until == now + timedelta(hours=6)


@pytest.mark.asyncio
async def test_topup_does_not_rewrite_past_or_current():
    ch = mk_channel(ChannelMode.SHUFFLE)
    repo = FakeRepo(ch, mk_content(6, dur_ms=20 * 60_000))

    # First generation at T0 with a 4h horizon.
    t0 = dt("2026-06-14T18:00:00")
    await run_schedule(repo, ch.id, now=t0, horizon=timedelta(hours=4))
    before = sorted(repo.blocks, key=lambda b: b.start_at)
    # Snapshot the blocks that are in the past/current at the top-up time.
    t1 = dt("2026-06-14T19:00:00")  # 1h later
    frozen = [
        (b.seq, b.start_at, b.end_at, b.video_id)
        for b in before
        if b.start_at < repo.state.horizon_until and b.end_at <= t1 or b.start_at <= t1 < b.end_at
    ]

    # Top-up at T1: extend horizon. Past/current blocks must be byte-identical.
    await run_schedule(repo, ch.id, now=t1, horizon=timedelta(hours=4))
    after = {(b.seq, b.start_at, b.end_at, b.video_id) for b in repo.blocks}
    for f in frozen:
        assert f in after, f"past/current block was rewritten: {f}"


@pytest.mark.asyncio
async def test_empty_library_yields_single_slate_never_raises():
    ch = mk_channel(ChannelMode.SHUFFLE)
    repo = FakeRepo(ch, [])  # degenerate: no content
    now = dt("2026-06-14T20:00:00")
    n = await run_schedule(repo, ch.id, now=now, horizon=timedelta(hours=2))
    assert n == 1
    assert repo.blocks[0].kind == BlockKind.SLATE
    assert repo.blocks[0].start_at == now
    assert repo.blocks[0].end_at == now + timedelta(hours=2)


@pytest.mark.asyncio
async def test_disabled_channel_skipped():
    import dataclasses

    ch = dataclasses.replace(mk_channel(ChannelMode.SHUFFLE), enabled=False)
    repo = FakeRepo(ch, mk_content(3))
    n = await run_schedule(repo, ch.id, now=dt("2026-06-14T20:00:00"))
    assert n == 0
    assert repo.blocks == []


@pytest.mark.asyncio
async def test_topup_all_touches_due_channel():
    ch = mk_channel(ChannelMode.SHUFFLE)
    repo = FakeRepo(ch, mk_content(4))
    now = dt("2026-06-14T20:00:00")
    touched = await topup_all(repo, now=now, horizon=timedelta(hours=6))
    assert touched == 1
    assert repo.blocks
