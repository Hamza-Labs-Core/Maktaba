"""``component_catchup`` (Epic 51, story 51.06): a component on again gets the jobs its
stages missed while it was off, once (T08, T09)."""

from __future__ import annotations

from datetime import timedelta

import pytest

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.sql import now, to_ts
from maktaba_pipeline.engine.stages import component_catchup
from maktaba_pipeline.engine.stages.component_catchup import catch_up, entries_of
from maktaba_pipeline.runtime import Database
from tests.components._helpers import set_stage
from tests.engine.factory import make_library, make_video

STAGES = {s.name: s for s in GRAPH}


class _Progress:
    def __init__(self) -> None:
        self.calls: list[tuple[str, int, int]] = []

    async def progress(self, phase: str, done: int, total: int, *, force: bool = False) -> None:
        self.calls.append((phase, done, total))


async def _upstream(db: Database, n: int, *, ago: timedelta, outcome: str = "changed") -> None:
    """``n`` content_hash jobs done ``ago`` (their edge leads to thumbnail)."""
    lib = await make_library(db)
    at = to_ts(db, now() - ago)
    for _ in range(n):
        v = await make_video(db, lib)
        res = await enqueue_job(db, subject_kind="video", subject_id=v, stage="content_hash")
        await db.execute(
            "UPDATE processing_jobs SET state = 'done', outcome = $2, finished_at = $3"
            " WHERE id = $1",
            res.job_id,
            outcome,
            at,
        )


async def _thumbs(db: Database, stage: str = "thumbnail") -> int:
    return int(await db.fetchval("SELECT count(*) FROM processing_jobs WHERE stage = $1", stage))


def test_entries_keep_the_earliest_time_and_unknown_wins() -> None:
    got = entries_of(
        {
            "components": [
                {"component": "a", "off_since": "2026-11-02T00:00:00Z"},
                {"component": "a", "off_since": "2026-11-01T00:00:00Z"},
                {"component": "b", "off_since": "2026-11-01T00:00:00Z"},
                {"component": "b", "off_since": None},
                {"nothing": 1},
            ]
        }
    )
    assert set(got) == {"a", "b"}
    assert got["a"] is not None and got["a"].day == 1
    assert got["b"] is None


@pytest.mark.asyncio
async def test_t08_the_skipped_edges_are_queued_with_progress(db: Database) -> None:
    await _upstream(db, 4, ago=timedelta(days=3))  # before it went off: not caught up
    await _upstream(db, 300, ago=timedelta(hours=1))
    await _upstream(db, 3, ago=timedelta(hours=1), outcome="unchanged")  # no edge fired
    off_since = now() - timedelta(days=1)
    ctx = _Progress()
    got = await catch_up(db, STAGES, {"media.thumbnails": off_since}, ctx=ctx)  # type: ignore[arg-type]
    # content_hash leads to thumbnail and to thumb_small: both edges were skipped
    assert got["media.thumbnails"]["queued"] == 600
    assert got["media.thumbnails"]["upstream"] == 600
    assert await _thumbs(db) == 300
    assert await _thumbs(db, "thumb_small") == 300
    assert ctx.calls[-1] == ("catch_up", 600, 600)
    prio = await db.fetch("SELECT DISTINCT priority FROM processing_jobs WHERE stage = 'thumbnail'")
    assert [int(r["priority"]) for r in prio] == [component_catchup.PRIORITY]


@pytest.mark.asyncio
async def test_t08_batches_of_the_batch_size(db: Database, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(component_catchup, "BATCH", 7)
    await _upstream(db, 20, ago=timedelta(hours=1))
    ctx = _Progress()
    got = await catch_up(db, STAGES, {"media.thumbnails": None}, ctx=ctx)  # type: ignore[arg-type]
    assert got["media.thumbnails"]["queued"] == 40
    # thumbnail's walk (7, 7, 6), then thumb_small's (7, 7, 6), then the final report
    assert [c[1] for c in ctx.calls] == [7, 14, 20, 27, 34, 40, 40]


@pytest.mark.asyncio
async def test_t09_a_second_catchup_adds_nothing(db: Database) -> None:
    await _upstream(db, 25, ago=timedelta(hours=1))
    await catch_up(db, STAGES, {"media.thumbnails": None})
    again = await catch_up(db, STAGES, {"media.thumbnails": None})
    assert again["media.thumbnails"] == {"queued": 0, "coalesced": 0, "upstream": 0, "skipped": 0}
    assert await _thumbs(db) == 25 and await _thumbs(db, "thumb_small") == 25


@pytest.mark.asyncio
async def test_off_again_mid_catchup_ends_that_components_walk(db: Database) -> None:
    await _upstream(db, 5, ago=timedelta(hours=1))
    for st in ("thumbnail", "thumb_small", "chapter_thumbs"):
        await set_stage(db, st, False, "component_off")
    got = await catch_up(db, STAGES, {"media.thumbnails": None})
    assert got["media.thumbnails"]["skipped"] == 1
    assert got["media.thumbnails"]["upstream"] == 1
    assert await _thumbs(db) == 0 and await _thumbs(db, "thumb_small") == 0
