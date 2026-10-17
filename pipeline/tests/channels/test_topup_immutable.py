"""Pass-level: past/current blocks immutable across regen; degenerate
library never raises (D3/D9)."""

from __future__ import annotations

from datetime import timedelta

import pytest

from maktaba_pipeline.channels import run_schedule, topup_all
from maktaba_pipeline.channels.base import BlockKind, ChannelMode

from .helpers import FakeRepo, dt, mk_channel, mk_content


@pytest.mark.asyncio
async def test_generation_is_contiguous_and_covers_horizon() -> None:
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
async def test_topup_does_not_rewrite_past_or_current() -> None:
    ch = mk_channel(ChannelMode.SHUFFLE)
    repo = FakeRepo(ch, mk_content(6, dur_ms=20 * 60_000))

    # First generation at T0 with a 4h horizon.
    t0 = dt("2026-06-14T18:00:00")
    await run_schedule(repo, ch.id, now=t0, horizon=timedelta(hours=4))
    before = sorted(repo.blocks, key=lambda b: b.start_at)
    # Snapshot the blocks that are in the past/current at the top-up time.
    t1 = dt("2026-06-14T19:00:00")  # 1h later
    hu = repo.state.horizon_until
    assert hu is not None
    frozen = [
        (b.seq, b.start_at, b.end_at, b.video_id)
        for b in before
        if b.start_at < hu and b.end_at <= t1 or b.start_at <= t1 < b.end_at
    ]

    # Top-up at T1: extend horizon. Past/current blocks must be byte-identical.
    await run_schedule(repo, ch.id, now=t1, horizon=timedelta(hours=4))
    after = {(b.seq, b.start_at, b.end_at, b.video_id) for b in repo.blocks}
    for f in frozen:
        assert f in after, f"past/current block was rewritten: {f}"


@pytest.mark.asyncio
async def test_empty_library_yields_single_slate_never_raises() -> None:
    ch = mk_channel(ChannelMode.SHUFFLE)
    repo = FakeRepo(ch, [])  # degenerate: no content
    now = dt("2026-06-14T20:00:00")
    n = await run_schedule(repo, ch.id, now=now, horizon=timedelta(hours=2))
    assert n == 1
    assert repo.blocks[0].kind == BlockKind.SLATE
    assert repo.blocks[0].start_at == now
    assert repo.blocks[0].end_at == now + timedelta(hours=2)


@pytest.mark.asyncio
async def test_disabled_channel_skipped() -> None:
    import dataclasses

    ch = dataclasses.replace(mk_channel(ChannelMode.SHUFFLE), enabled=False)
    repo = FakeRepo(ch, mk_content(3))
    n = await run_schedule(repo, ch.id, now=dt("2026-06-14T20:00:00"))
    assert n == 0
    assert repo.blocks == []


@pytest.mark.asyncio
async def test_topup_all_touches_due_channel() -> None:
    ch = mk_channel(ChannelMode.SHUFFLE)
    repo = FakeRepo(ch, mk_content(4))
    now = dt("2026-06-14T20:00:00")
    touched = await topup_all(repo, now=now, horizon=timedelta(hours=6))
    assert touched == 1
    assert repo.blocks


@pytest.mark.asyncio
async def test_stale_regen_starts_after_the_block_on_air() -> None:
    # An edited channel (stale) is regenerated from now, but the block on air
    # stays (D3): the new timeline follows it instead of overlapping it.
    ch = mk_channel(ChannelMode.SHUFFLE)
    repo = FakeRepo(ch, mk_content(6, dur_ms=20 * 60_000))
    t0 = dt("2026-06-14T18:00:00")
    await run_schedule(repo, ch.id, now=t0, horizon=timedelta(hours=4))
    repo.state.stale = True
    t1 = t0 + timedelta(minutes=10)  # mid-way through the first 20-min block
    await run_schedule(repo, ch.id, now=t1, horizon=timedelta(hours=4))
    blocks = sorted(repo.blocks, key=lambda b: b.start_at)
    for a, b in zip(blocks, blocks[1:], strict=False):
        assert a.end_at == b.start_at, (a, b)
    assert blocks[0].start_at == t0 and blocks[1].start_at == t0 + timedelta(minutes=20)


@pytest.mark.asyncio
async def test_slot_times_are_the_channels_wall_clock() -> None:
    """A 05:00 dawn slot in Asia/Riyadh starts at 02:00 UTC, not at 05:00 UTC."""
    from datetime import UTC

    cfg = {"slots": [{"start": "05:00", "end": "07:00"}], "timezone": "Asia/Riyadh"}
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config=cfg)
    repo = FakeRepo(ch, mk_content(8, dur_ms=30 * 60_000))
    now = dt("2026-06-14T00:00:00")  # 03:00 in Riyadh
    await run_schedule(repo, ch.id, now=now, horizon=timedelta(hours=6))
    progs = sorted(
        (b for b in repo.blocks if b.kind == BlockKind.PROGRAM), key=lambda b: b.start_at
    )
    assert progs and progs[0].start_at.astimezone(UTC) == dt("2026-06-14T02:00:00")
    assert progs[-1].end_at.astimezone(UTC) <= dt("2026-06-14T04:00:00")
