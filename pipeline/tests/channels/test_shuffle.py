"""Shuffle planner — fair bag, no adjacent repeats, cursor continuity."""

from __future__ import annotations

from datetime import timedelta

from maktaba_pipeline.channels.base import ChannelMode
from maktaba_pipeline.channels.planner.shuffle import ShufflePlanner

from .helpers import dt, mk_channel, mk_content


def test_covers_horizon() -> None:
    ch = mk_channel(ChannelMode.SHUFFLE)
    content = mk_content(5, dur_ms=10 * 60_000)
    p = ShufflePlanner(ch, seed=1)
    start = dt("2026-06-14T00:00:00")
    items = p.plan(content, start_at=start, until=start + timedelta(hours=2))
    assert sum(i.duration_ms for i in items) >= 2 * 60 * 60_000


def test_no_adjacent_repeat() -> None:
    ch = mk_channel(ChannelMode.SHUFFLE)
    content = mk_content(6, dur_ms=10 * 60_000)
    p = ShufflePlanner(ch, seed=7)
    start = dt("2026-06-14T00:00:00")
    items = p.plan(content, start_at=start, until=start + timedelta(hours=6))
    ids = [str(i.video_id) for i in items]
    for a, b in zip(ids, ids[1:], strict=False):
        assert a != b, "shuffle produced an adjacent repeat"


def test_fair_bag_every_item_before_repeat() -> None:
    ch = mk_channel(ChannelMode.SHUFFLE)
    content = mk_content(4, dur_ms=10 * 60_000)
    p = ShufflePlanner(ch, seed=3)
    start = dt("2026-06-14T00:00:00")
    # Exactly one bag (4 * 10min = 40min ≈ one pass before any repeat).
    items = p.plan(content, start_at=start, until=start + timedelta(minutes=40))
    ids = [str(i.video_id) for i in items[:4]]
    assert len(set(ids)) == 4, "first bag should draw each item once"


def test_cursor_continues_across_topup() -> None:
    ch = mk_channel(ChannelMode.SHUFFLE)
    content = mk_content(5, dur_ms=10 * 60_000)
    start = dt("2026-06-14T00:00:00")

    p1 = ShufflePlanner(ch, seed=5)
    first = p1.plan(content, start_at=start, until=start + timedelta(minutes=25))
    cursor = p1.export_state()

    # Resume from the exported cursor: the next item must not be a stale
    # restart of the bag — it continues the same shuffle.
    p2 = ShufflePlanner(ch, cursor=cursor, seed=5)
    second = p2.plan(content, start_at=start, until=start + timedelta(minutes=25))
    assert first and second
    # The continuation's first id should equal what the un-split run would
    # have produced next (the bag carried over).
    p_full = ShufflePlanner(ch, seed=5)
    full = p_full.plan(content, start_at=start, until=start + timedelta(minutes=50))
    combined = [str(i.video_id) for i in first + second]
    full_ids = [str(i.video_id) for i in full]
    # The split run's prefix must reproduce the un-split run exactly — the
    # bag carried across the top-up rather than reshuffling from scratch.
    assert combined[: len(full_ids)] == full_ids


def test_empty_content_yields_nothing() -> None:
    ch = mk_channel(ChannelMode.SHUFFLE)
    p = ShufflePlanner(ch, seed=1)
    start = dt("2026-06-14T00:00:00")
    assert p.plan([], start_at=start, until=start + timedelta(hours=1)) == []
