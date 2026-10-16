"""``detect``: placing episodes into series and seasons, and what it triggers."""

from __future__ import annotations

from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.engine.dispatch import EngineRuntime
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import Subject
from maktaba_pipeline.engine.ids import season_uuid, series_uuid
from maktaba_pipeline.engine.sql import loads, to_ts
from maktaba_pipeline.engine.stages.detect import compute_changes, input_fingerprint, plan_library
from maktaba_pipeline.runtime import Database
from tests.stages.world import core_runtime, drain_core, make_show, set_playlist

SEASON_KEY = "stories of the prophets"


async def _detect(
    db: Database, rt: EngineRuntime, library: UUID, *, force: bool = False
) -> list[str]:
    await enqueue_job(db, subject_kind="library", subject_id=library, stage="detect", force=force)
    return await drain_core(db, rt, only={"detect"})


async def _jobs(db: Database, stage: str) -> list[Any]:
    return list(await db.fetch("SELECT * FROM processing_jobs WHERE stage = $1 ORDER BY id", stage))


@pytest.mark.asyncio
async def test_detect_places_episodes_and_creates_series_and_season(db: Database) -> None:
    rt = await core_runtime(db)
    w = await make_show(db)
    assert await _detect(db, rt, w.library) == ["detect"]

    sid = season_uuid(w.library, SEASON_KEY, "episodic", 1)
    season = await db.fetchrow("SELECT * FROM series_seasons WHERE id = $1", str(sid))
    assert season is not None
    assert str(season["series_id"]) == str(series_uuid(w.library, SEASON_KEY, "episodic"))
    assert season["episode_count"] == 12 and season["kind"] == "episodic"
    assert season["retired_at"] is None and season["detector_version"] >= 2
    assert str(
        await db.fetchval("SELECT library_id FROM series WHERE id = $1", str(season["series_id"]))
    ) == str(w.library)

    eps = await db.fetch(
        "SELECT video_id, episode FROM series_episodes WHERE series_id = $1 ORDER BY episode",
        str(season["series_id"]),
    )
    assert [str(e["video_id"]) for e in eps] == [str(v) for v in w.episodes]
    assert [e["episode"] for e in eps] == list(range(1, 13))
    # the loose talks are in no season
    assert await db.fetchval("SELECT count(*) FROM series_episodes") == 12

    job = (await _jobs(db, "detect"))[0]
    assert job["outcome"] == "changed"
    summary = loads(job["output_summary"])
    assert summary["placed"] == 12 and summary["seasons"] == 1 and summary["unassigned"] == 3


@pytest.mark.asyncio
async def test_detect_triggers_exactly_its_dependents(db: Database) -> None:
    rt = await core_runtime(db)
    w = await make_show(db)
    await _detect(db, rt, w.library)
    sid = str(season_uuid(w.library, SEASON_KEY, "episodic", 1))

    tags = await _jobs(db, "tag")
    kinds = sorted((j["subject_kind"], str(j["subject_id"])) for j in tags)
    assert kinds == sorted([("season", sid), ("library", str(w.library))])
    rate = await _jobs(db, "rate_style")
    assert [(j["subject_kind"], str(j["subject_id"])) for j in rate] == [("season", sid)]
    plan = await _jobs(db, "plan_channels")
    assert len(plan) == 1 and plan[0]["subject_kind"] == "catalog"
    assert all(j["triggered_by_stage"] == "detect" for j in [*tags, *rate, *plan])
    # no unrelated work: 12 videos, one gate-less season => no fingerprint requests
    assert await _jobs(db, "fingerprint") == [] and await _jobs(db, "fp_gate") == []


@pytest.mark.asyncio
async def test_unchanged_detection_propagates_nothing(db: Database) -> None:
    rt = await core_runtime(db)
    w = await make_show(db)
    await _detect(db, rt, w.library)
    before = {s: len(await _jobs(db, s)) for s in ("tag", "rate_style", "plan_channels")}
    await _detect(db, rt, w.library, force=True)
    second = (await _jobs(db, "detect"))[-1]
    assert second["outcome"] == "unchanged"
    assert {s: len(await _jobs(db, s)) for s in before} == before


@pytest.mark.asyncio
async def test_unchanged_inputs_are_skipped_without_running_the_detector(db: Database) -> None:
    rt = await core_runtime(db)
    w = await make_show(db)
    await _detect(db, rt, w.library)
    await _detect(db, rt, w.library)  # same inputs: skipped
    assert (await _jobs(db, "detect"))[-1]["outcome"] == "skipped_unchanged"


@pytest.mark.asyncio
async def test_membership_change_retriggers_only_the_changed_season(db: Database) -> None:
    rt = await core_runtime(db)
    w = await make_show(db)
    await _detect(db, rt, w.library)
    await drain_core(db, rt, only={"tag"})
    n_tag = len(await _jobs(db, "tag"))
    # a 13th episode joins the playlist
    from datetime import date

    from tests.stages.world import add_youtube_video

    vid, yt = await add_youtube_video(
        db, w.library, title="Stories of the Prophets - Episode 13", filename="show_13.mp4",
        upload=date(2024, 3, 13), duration=1513.0,
    )  # fmt: skip
    members = [(v, w.youtube[v]) for v in w.episodes] + [(vid, yt)]
    await set_playlist(db, "PL1", "Stories of the Prophets", members)
    await _detect(db, rt, w.library)
    last = (await _jobs(db, "detect"))[-1]
    assert last["outcome"] == "changed"
    assert loads(last["output_summary"])["seasons_changed"] == 1
    season = await db.fetchrow("SELECT episode_count FROM series_seasons")
    assert season["episode_count"] == 13
    tags = await _jobs(db, "tag")
    season_tags = [j for j in tags if j["subject_kind"] == "season"]
    assert len(season_tags) == 2  # the season is re-tagged
    assert len(tags) == n_tag + 2  # plus the library-level tag, which is cheap:
    await drain_core(db, rt, only={"tag"})
    library_tags = [j for j in await _jobs(db, "tag") if j["subject_kind"] == "library"]
    assert library_tags[-1]["outcome"] == "skipped_unchanged"  # the loose set did not change
    changed_event = await db.fetchrow(
        "SELECT data FROM job_events WHERE kind = 'applied' AND stage = 'detect' ORDER BY id DESC"
    )
    data = loads(changed_event["data"])["changes"]
    assert data["counts"]["changed"] == 1
    assert data["changed"][0]["episodes_added"] == [str(vid)]


@pytest.mark.asyncio
async def test_a_vanished_season_is_retired_and_its_dependents_run(db: Database) -> None:
    rt = await core_runtime(db)
    w = await make_show(db)
    await _detect(db, rt, w.library)
    sid = str(season_uuid(w.library, SEASON_KEY, "episodic", 1))
    await drain_core(db, rt, only={"tag", "rate_style"})
    # the playlist disappears and the titles no longer share a name
    await db.execute("DELETE FROM youtube_playlist_items")
    for i, v in enumerate(w.episodes):
        await db.execute(
            "UPDATE youtube_videos SET title = $2 WHERE youtube_id = $1",
            w.youtube[v],
            # no shared word and no number: nothing a numbered-show rule can join
            (
                "apple orchard tour",
                "river stone bridge",
                "winter market lights",
                "copper kettle story",
                "desert night sky",
                "harbor fog morning",
                "violin workshop visit",
                "garden tea ritual",
                "mountain rail line",
                "paper lantern craft",
                "salt marsh birds",
                "clock tower repair",
            )[i % 12]
            + f" {'abcdefghijkl'[i % 12] * 3}",
        )
    await _detect(db, rt, w.library, force=True)  # (a real change arrives with a new sync hash)
    row = await db.fetchrow(
        "SELECT retired_at, episode_count FROM series_seasons WHERE id = $1", sid
    )
    assert row["retired_at"] is not None
    assert await db.fetchval("SELECT count(*) FROM series_episodes") == 0
    tag_jobs = [j for j in await _jobs(db, "tag") if str(j["subject_id"]) == sid]
    assert len(tag_jobs) == 2  # re-run for the retired season so dependents can clean up


@pytest.mark.asyncio
async def test_user_overrides_survive_a_redetection(db: Database) -> None:
    rt = await core_runtime(db)
    w = await make_show(db)
    await _detect(db, rt, w.library)
    await db.execute(
        "UPDATE series_episodes SET episode_override = 99 WHERE video_id = $1", str(w.episodes[0])
    )
    await _detect(db, rt, w.library, force=True)
    assert (
        await db.fetchval(
            "SELECT episode_override FROM series_episodes WHERE video_id = $1", str(w.episodes[0])
        )
        == 99
    )


@pytest.mark.asyncio
async def test_input_fingerprint_follows_inputs_only(db: Database) -> None:
    rt = await core_runtime(db)
    w = await make_show(db)
    subject = Subject("library", w.library)
    a = await input_fingerprint(db, subject)
    assert a == await input_fingerprint(db, subject)
    await db.execute("UPDATE videos SET title = 'renamed' WHERE id = $1", str(w.episodes[0]))
    b = await input_fingerprint(db, subject)
    assert b != a
    del rt


@pytest.mark.unit
def test_plan_is_deterministic_and_ids_are_stable() -> None:
    from datetime import date, timedelta
    from uuid import uuid4

    from maktaba_pipeline.engine.stages.detect import Row

    lib = uuid4()
    rows = [
        Row(
            id=uuid4(),
            folder="/lib/c",
            title=f"Show Alpha - Episode {i + 1}",
            duration_s=1500.0 + i,
            air_date=date(2024, 1, 1) + timedelta(days=i),
            date_exact=True,
            dhash=None,
            is_short=False,
            audio_head=(),
            audio_tail=(),
            frames=(),
            fingerprinted=False,
            channel_title=None,
            youtube_id=None,
        )  # fmt: skip
        for i in range(10)
    ]
    pls: dict[str, tuple[str, list[tuple[str, int | None]]]] = {
        "PL9": ("Show Alpha", [(str(r.id), i + 1) for i, r in enumerate(rows)])
    }
    a = plan_library(lib, rows, pls)
    b = plan_library(lib, rows, pls)
    assert [s.id for s in a.seasons] == [s.id for s in b.seasons]
    assert [s.membership_hash for s in a.seasons] == [s.membership_hash for s in b.seasons]
    cs, touched = compute_changes(a, {})
    assert cs.counts["added"] == 1 and len(touched) == 1
    cs2, touched2 = compute_changes(a, {})
    assert cs2.digest() == cs.digest()
    assert to_ts is not None
