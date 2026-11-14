"""A published channel airs only publishable items (Epic 50, story 50.23 §3).

T03: rules mixing sources build from the publishable ones while ``publishable_only`` is
on, and from every one when it is off (or the channel is not published). T05: a Quran
reciter is a source like any other. The rule itself (which ids may air) is slot 0227's
SQL, tested against Postgres in the API; here the repo answers it.
"""

from __future__ import annotations

from datetime import timedelta
from uuid import UUID

import pytest

from maktaba_pipeline.channels.base import BlockKind, ChannelDef, ChannelMode
from maktaba_pipeline.channels.filler import FillerList, FillerSet
from maktaba_pipeline.channels.pass_ import run_schedule

from .helpers import FakeRepo, dt, mk_channel, mk_content, mk_filler


class PublishedRepo(FakeRepo):
    """A fake repo whose channel is published: ``allowed`` may air (None: not limited)."""

    allowed: set[UUID] | None = None

    async def publishable_ids(self, channel: ChannelDef, ids: set[UUID]) -> set[UUID] | None:
        if self.allowed is None:
            return None
        return ids & self.allowed


NOW = dt("2026-11-14T00:00:00")


def _aired(repo: FakeRepo) -> set[UUID]:
    return {b.video_id for b in repo.blocks if b.video_id is not None}


@pytest.mark.asyncio
async def test_published_channel_builds_from_publishable_items_only() -> None:
    ch = mk_channel(ChannelMode.SHUFFLE)
    permitted, other = mk_content(3), mk_content(3)
    repo = PublishedRepo(ch, permitted + other)
    repo.allowed = {c.video_id for c in permitted}
    assert await run_schedule(repo, ch.id, now=NOW, horizon=timedelta(hours=24)) > 0
    aired = _aired(repo)
    assert aired and aired <= repo.allowed  # the other source is skipped
    assert sum((b.end_at - b.start_at).total_seconds() for b in repo.blocks) >= 24 * 3600


@pytest.mark.asyncio
async def test_off_or_unpublished_builds_the_full_rules() -> None:
    ch = mk_channel(ChannelMode.SHUFFLE)
    permitted, other = mk_content(3), mk_content(3)
    repo = PublishedRepo(ch, permitted + other)
    repo.allowed = None  # publishable_only off, or the channel unpublished
    await run_schedule(repo, ch.id, now=NOW, horizon=timedelta(hours=24))
    assert _aired(repo) == {c.video_id for c in permitted + other}


@pytest.mark.asyncio
async def test_filler_and_slot_queues_are_limited_too() -> None:
    ch = mk_channel(ChannelMode.SHUFFLE)
    ok_filler, bad_filler = mk_filler(2), mk_filler(2)
    programmes = mk_content(2, dur_ms=50 * 60_000)
    repo = PublishedRepo(
        ch,
        programmes,
        filler=FillerSet(lists=(FillerList("default", tuple(ok_filler + bad_filler)),)),
    )
    repo.allowed = {c.video_id for c in programmes} | {f.video_id for f in ok_filler if f.video_id}
    await run_schedule(repo, ch.id, now=NOW, horizon=timedelta(hours=6))
    fill = {b.video_id for b in repo.blocks if b.kind == BlockKind.FILLER}
    assert not fill & {f.video_id for f in bad_filler}
    assert _aired(repo) <= repo.allowed


@pytest.mark.asyncio
async def test_nothing_publishable_is_the_slate() -> None:
    """A rule that runs dry while published is filled as any other: here, the slate."""
    ch = mk_channel(ChannelMode.SHUFFLE)
    repo = PublishedRepo(ch, mk_content(4))
    repo.allowed = set()
    await run_schedule(repo, ch.id, now=NOW, horizon=timedelta(hours=2))
    assert repo.blocks and all(b.video_id is None for b in repo.blocks)


@pytest.mark.asyncio
async def test_a_reciter_is_a_source() -> None:
    """T05: a recitation airs on a published channel only while its reciter is permitted."""
    ch = mk_channel(ChannelMode.SHUFFLE)
    recitations, own = mk_content(2), mk_content(2)
    repo = PublishedRepo(ch, recitations + own)
    repo.allowed = {c.video_id for c in own}  # the reciter has no permission
    await run_schedule(repo, ch.id, now=NOW, horizon=timedelta(hours=6))
    assert not _aired(repo) & {c.video_id for c in recitations}

    repo2 = PublishedRepo(ch, recitations + own)
    repo2.allowed = {c.video_id for c in recitations + own}  # permission recorded
    await run_schedule(repo2, ch.id, now=NOW, horizon=timedelta(hours=6))
    assert _aired(repo2) & {c.video_id for c in recitations}
