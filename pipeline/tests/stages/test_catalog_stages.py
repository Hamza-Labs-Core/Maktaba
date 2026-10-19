# ruff: noqa: E501  (long SQL strings and test data read better unwrapped)
"""tag, rate_style, plan_channels, write_channels: the Live TV half of the graph."""

from __future__ import annotations

from collections.abc import Iterator
from typing import Any

import pytest

from maktaba_pipeline.classify.production_style import STYLE_VERSION, StyleMetrics
from maktaba_pipeline.engine.dispatch import EngineRuntime
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import CATALOG_ID
from maktaba_pipeline.engine.media import reset_runner
from maktaba_pipeline.engine.sql import dumps, loads, now, to_ts
from maktaba_pipeline.engine.stages.write_channels import FOLLOW_PLAN, content_hash
from maktaba_pipeline.runtime import Database
from tests.stages.world import World, core_runtime, drain_core, make_fake_media, make_show


@pytest.fixture(autouse=True)
def _media() -> Iterator[None]:
    make_fake_media({})
    yield
    reset_runner()


async def _rows(db: Database, stage: str) -> list[Any]:
    return list(await db.fetch("SELECT * FROM processing_jobs WHERE stage = $1 ORDER BY id", stage))


async def _events(db: Database, kind: str, stage: str) -> list[Any]:
    return [
        loads(r["data"])
        for r in await db.fetch(
            "SELECT data FROM job_events WHERE kind = $1 AND stage = $2 ORDER BY id", kind, stage
        )
    ]


async def _pipeline(
    db: Database, *, name: str = "قصص الأنبياء", episodes: int = 30, staging: bool = False
) -> tuple[EngineRuntime, World]:
    """detect -> everything it triggers, run to quiescence. With ``staging`` off (these
    tests' default) write_channels writes the air directly, as before Epic 33 (story
    33.12 AC10); on, it makes a TV change."""
    rt = await core_runtime(db)
    await _small_library_rules(db)
    await db.execute(
        "INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, $3)"
        " ON CONFLICT (key) DO UPDATE SET value = excluded.value",
        "rules.tv_staging",
        dumps({"enabled": staging}),
        to_ts(db, now()),
    )
    w = await make_show(db, name=name, episodes=episodes, loose=4)
    await enqueue_job(db, subject_kind="library", subject_id=w.library, stage="detect")
    await drain_core(db, rt)
    return rt, w


async def _small_library_rules(db: Database) -> None:
    """A test library has hours, not the month a channel needs by default: the
    planner rules as a user with a small library would set them."""
    await db.execute(
        "INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, $3)"
        " ON CONFLICT (key) DO UPDATE SET value = excluded.value",
        "rules.planner",
        dumps({"channel_min_days": 1.0, "channel_day_hours": 10.0}),
        to_ts(db, now()),
    )


# ------------------------------------------------------------------------------ tag


@pytest.mark.asyncio
async def test_tag_stores_season_and_loose_video_tags_with_a_change_set(db: Database) -> None:
    _, w = await _pipeline(db)
    tags = await db.fetch("SELECT tag, score FROM season_tags")
    assert tags, "an Arabic prophets series must get topic tags"
    assert any(str(t["tag"]).startswith("genre:") for t in tags)
    assert all(0 < float(t["score"]) <= 1 for t in tags)
    loose = await db.fetchval("SELECT count(DISTINCT video_id) FROM video_topic_tags")
    assert loose >= 1  # every loose talk is tagged on its own
    applied = await _events(db, "applied", "tag")
    assert applied and applied[0]["changes"]["counts"]["added"] >= 1
    assert w.library  # silence unused


@pytest.mark.asyncio
async def test_tag_rerun_with_same_content_changes_nothing(db: Database) -> None:
    rt, _ = await _pipeline(db)
    n_events = len(await _events(db, "applied", "tag"))
    sid = await db.fetchval("SELECT id FROM series_seasons")
    await enqueue_job(db, subject_kind="season", subject_id=sid, stage="tag", force=True)
    await drain_core(db, rt)
    last = (await _rows(db, "tag"))[-1]
    assert last["outcome"] == "unchanged"
    assert len(await _events(db, "applied", "tag")) == n_events  # an empty change set is not logged


# ------------------------------------------------------------------------------ rate_style


def _metrics(**kw: float) -> dict[str, Any]:
    base: dict[str, Any] = dict(
        sampled_s=120.0, cuts_per_min=0.2, motion=0.8, motion_p90=1.5, still_share=0.1,
        luma_mean=120.0, luma_std=5.0, dark_share=0.0, sat_mean=40.0, sat_std=3.0,
        chroma_spread=2.0, looks_per_min=0.5, top3_share=0.9, revisit_share=0.8,
        insert_share=0.0, insert_luma=0.0, insert_sat=0.0, edge_density=0.05,
    )  # fmt: skip
    base.update(kw)
    return StyleMetrics(**base).to_dict()


@pytest.mark.asyncio
async def test_rate_style_waits_for_samples_then_rates_the_season(db: Database) -> None:
    rt, w = await _pipeline(db)
    # the pipeline already ran style_sample through the fake ffmpeg: a style exists
    row = await db.fetchrow("SELECT style, tier, sampled, style_version FROM season_style")
    assert row is not None and row["style"] in ("static", "talk", "produced", "cinematic")
    assert row["sampled"] == 3 and row["style_version"] == STYLE_VERSION
    jobs = await _rows(db, "rate_style")
    assert jobs[0]["outcome"] == "waiting_inputs"  # first pass: asked for the samples
    assert jobs[-1]["outcome"] in ("changed", "unchanged")
    samples = await _rows(db, "style_sample")
    assert 1 <= len(samples) <= 8 and all(loads(j["payload"])["seasons"] for j in samples)
    assert w.library


@pytest.mark.asyncio
async def test_a_user_override_wins_and_needs_no_sampling(db: Database) -> None:
    rt, _ = await _pipeline(db)
    sid = await db.fetchval("SELECT season_id FROM season_style")
    n_samples = len(await _rows(db, "style_sample"))
    await db.execute(
        "UPDATE season_style SET override_style = 'cinematic' WHERE season_id = $1", sid
    )
    await enqueue_job(db, subject_kind="season", subject_id=sid, stage="rate_style")
    await drain_core(db, rt)
    row = await db.fetchrow("SELECT style, tier, confidence FROM season_style")
    assert (row["style"], row["tier"], row["confidence"]) == ("cinematic", 3, 1.0)
    assert len(await _rows(db, "style_sample")) == n_samples
    assert (await _events(db, "applied", "rate_style"))[-1]["changes"]["changed"]


@pytest.mark.asyncio
async def test_samples_that_fail_for_good_do_not_block_the_rating(db: Database) -> None:
    from maktaba_pipeline.engine.media import ProcResult, set_runner

    async def broken(args: list[str], timeout: float) -> ProcResult:
        return ProcResult(b"", b"decode error", 1)

    set_runner(broken)
    rt = await core_runtime(db)
    w = await make_show(db, name="قصص الأنبياء", episodes=12, loose=0)
    sid_row = None
    await enqueue_job(db, subject_kind="library", subject_id=w.library, stage="detect")
    await drain_core(db, rt, limit=400)
    sid_row = await db.fetchrow("SELECT id FROM series_seasons")
    assert sid_row is not None
    # every style_sample failed terminally; rate_style must not wait forever
    states = {j["state"] for j in await _rows(db, "style_sample")}
    assert states == {"failed"}
    last = (await _rows(db, "rate_style"))[-1]
    assert last["state"] == "done"


# ------------------------------------------------------------------------------ plan + write


@pytest.mark.asyncio
async def test_plan_channels_stores_a_plan_and_write_channels_creates_live_tv(db: Database) -> None:
    rt, _ = await _pipeline(db, episodes=40)
    plans = await db.fetch("SELECT plan, input_hash, planner_version FROM channel_plans")
    assert len(plans) >= 1
    plan = loads(plans[-1]["plan"])
    assert plan["channels"], "forty 25-minute episodes are enough for at least the Mix channel"
    ch = plan["channels"][0]
    assert set(ch) >= {"key", "number", "name", "maktaba", "slots", "exports"}
    assert ch["exports"] == {}  # no extension asked to export anything
    # lane episodes are Maktaba video ids
    lane = next(iter(plan["lanes"].values()))
    ep_path = lane["units"][0]["episodes"][0]["path"]
    assert await db.fetchval("SELECT count(*) FROM videos WHERE id = $1", ep_path) == 1

    n = await db.fetchval("SELECT count(*) FROM channels")
    assert n == len(plan["channels"])
    link = await db.fetchrow("SELECT * FROM external_links WHERE target = 'maktaba_channel'")
    assert link["subject_kind"] == "catalog" and bool(link["created_by_tool"])
    stale = await db.fetchval("SELECT count(*) FROM channel_schedule_state WHERE stale")
    assert stale == n
    applied = await _events(db, "applied", "write_channels")
    assert applied[0]["changes"]["counts"]["added"] == n
    assert all(str(j["subject_id"]) == str(CATALOG_ID) for j in await _rows(db, "write_channels"))


@pytest.mark.asyncio
async def test_with_staging_write_channels_stages_a_tv_change(db: Database) -> None:
    """Story 33.12 AC1 / AC9 end to end: with staging on the plan's lineup
    becomes a staged TV change - nothing on the air, no Plex refresh - and the swap
    commit puts it on the air."""
    from maktaba_pipeline.tvrelease.commit import commit_change
    from maktaba_pipeline.tvrelease.store import update_change

    await _pipeline(db, episodes=40, staging=True)
    plan = loads((await db.fetch("SELECT plan FROM channel_plans ORDER BY id"))[-1]["plan"])
    assert await db.fetchval("SELECT count(*) FROM channels WHERE lifecycle = 'live'") == 0
    assert await db.fetchval("SELECT count(*) FROM external_links") == 0
    changes = await db.fetch("SELECT id, state, kinds FROM tv_changes")
    assert [(r["state"], loads(r["kinds"])) for r in changes] == [("staged", ["lineup"])]
    summary = loads((await _rows(db, "write_channels"))[-1]["output_summary"])
    assert summary["staged"] and summary["tv_change"]["id"] == str(changes[0]["id"])
    cid = str(changes[0]["id"])
    await update_change(db, cid, state="swapping")  # what the API's controller does on approval
    report = await commit_change(db, cid)
    assert report["readback"]["ok"]
    n = await db.fetchval("SELECT count(*) FROM channels WHERE lifecycle = 'live'")
    assert n == len(plan["channels"])
    assert (
        await db.fetchval("SELECT count(*) FROM external_links WHERE target = 'maktaba_channel'")
        == n
    )
    assert await db.fetchval("SELECT count(*) FROM channel_programs_live") > 0


@pytest.mark.asyncio
async def test_unchanged_lineup_stores_and_writes_nothing(db: Database) -> None:
    rt, _ = await _pipeline(db, episodes=40)
    plans = await db.fetchval("SELECT count(*) FROM channel_plans")
    writes = len(await _rows(db, "write_channels"))
    await enqueue_job(
        db, subject_kind="catalog", subject_id=CATALOG_ID, stage="plan_channels", force=True
    )
    await drain_core(db, rt)
    assert await db.fetchval("SELECT count(*) FROM channel_plans") == plans
    assert (await _rows(db, "plan_channels"))[-1]["outcome"] == "unchanged"
    assert (
        len(await _rows(db, "write_channels")) == writes
    )  # an unchanged plan does not re-trigger it


@pytest.mark.asyncio
async def test_write_channels_leaves_other_channels_and_user_edits_alone(db: Database) -> None:
    rt, _ = await _pipeline(db, episodes=40)
    mine = await db.fetchrow(
        "SELECT external_id, subject_id FROM external_links WHERE target = 'maktaba_channel' LIMIT 1"
    )
    # the user renames a channel the tool created, and owns an unrelated channel
    await db.execute("UPDATE channels SET name = 'My own name' WHERE id = $1", mine["external_id"])
    await db.execute(
        "INSERT INTO channels (id, number, name, slug, mode, mode_config) VALUES"
        " ('00000000-0000-0000-0000-0000000000aa', 9001, 'Mine', 'mine', 'shuffle', $1)",
        dumps({}),
    )
    # make the plan change so the writer has something to do
    await db.execute("DELETE FROM channel_plans")
    await enqueue_job(
        db, subject_kind="catalog", subject_id=CATALOG_ID, stage="plan_channels", force=True
    )
    await drain_core(db, rt)
    await enqueue_job(
        db, subject_kind="catalog", subject_id=CATALOG_ID, stage="write_channels", force=True
    )
    await drain_core(db, rt)
    assert (
        await db.fetchval("SELECT name FROM channels WHERE id = $1", mine["external_id"])
        == "My own name"
    )
    assert await db.fetchval("SELECT count(*) FROM channels WHERE number = 9001") == 1
    summary = loads((await _rows(db, "write_channels"))[-1]["output_summary"])
    assert any(u["key"] == mine["subject_id"] for u in summary["user_edited"])


@pytest.mark.asyncio
async def test_a_channel_handed_back_to_the_plan_is_rewritten(db: Database) -> None:
    rt, _ = await _pipeline(db, episodes=40)
    mine = await db.fetchrow(
        "SELECT external_id, subject_id FROM external_links WHERE target = 'maktaba_channel' LIMIT 1"
    )
    planned = await db.fetchval("SELECT name FROM channels WHERE id = $1", mine["external_id"])
    await db.execute("UPDATE channels SET name = 'My own name' WHERE id = $1", mine["external_id"])
    # "back to plan" (POST /api/channels/{id}/follow-plan) stores the sentinel hash
    await db.execute(
        "UPDATE external_links SET content_hash = $1 WHERE target = 'maktaba_channel'"
        " AND external_id = $2",
        FOLLOW_PLAN,
        mine["external_id"],
    )
    await enqueue_job(
        db, subject_kind="catalog", subject_id=CATALOG_ID, stage="write_channels", force=True
    )
    await drain_core(db, rt)
    assert (
        await db.fetchval("SELECT name FROM channels WHERE id = $1", mine["external_id"]) == planned
    )
    link = await db.fetchval(
        "SELECT content_hash FROM external_links WHERE target = 'maktaba_channel' AND external_id = $1",
        mine["external_id"],
    )
    assert link != FOLLOW_PLAN  # a real hash again: a later edit is noticed as before
    summary = loads((await _rows(db, "write_channels"))[-1]["output_summary"])
    assert not any(u["key"] == mine["subject_id"] for u in summary["user_edited"])


@pytest.mark.asyncio
async def test_a_number_held_by_a_foreign_channel_is_a_conflict_not_an_overwrite(
    db: Database,
) -> None:
    rt = await core_runtime(db)
    await _small_library_rules(db)
    w = await make_show(db, name="قصص الأنبياء", episodes=40, loose=4)
    for n in range(95, 160):  # squat on every number the planner might pick
        await db.execute(
            "INSERT INTO channels (id, number, name, slug, mode, mode_config)"
            " VALUES ($1, $2, 'x', $3, 'shuffle', '{}')",
            f"00000000-0000-0000-0000-{n:012d}", n, f"sq{n}",
        )  # fmt: skip
    await enqueue_job(db, subject_kind="library", subject_id=w.library, stage="detect")
    await drain_core(db, rt)
    assert (
        await db.fetchval("SELECT count(*) FROM external_links WHERE target = 'maktaba_channel'")
        == 0
    )
    summary = loads((await _rows(db, "write_channels"))[-1]["output_summary"])
    assert summary["conflicts"] and summary["created"] == 0
    assert await db.fetchval("SELECT count(*) FROM channels WHERE name = 'x'") == 65


@pytest.mark.asyncio
async def test_channels_the_plan_no_longer_has_are_removed_only_if_the_tool_made_them(
    db: Database,
) -> None:
    rt, _ = await _pipeline(db, episodes=40)
    n = await db.fetchval("SELECT count(*) FROM channels")
    assert n >= 1
    # the lineup empties (everything detected is retired)
    await db.execute("DELETE FROM series_episodes")
    await db.execute("UPDATE series_seasons SET retired_at = '2020-01-01T00:00:00Z'")
    await db.execute("DELETE FROM videos")
    await enqueue_job(
        db, subject_kind="catalog", subject_id=CATALOG_ID, stage="plan_channels", force=True
    )
    await drain_core(db, rt)
    assert await db.fetchval("SELECT count(*) FROM channels") == 0
    assert (
        await db.fetchval("SELECT count(*) FROM external_links WHERE target = 'maktaba_channel'")
        == 0
    )
    applied = await _events(db, "applied", "write_channels")
    assert applied[-1]["changes"]["counts"]["removed"] == n


@pytest.mark.unit
def test_content_hash_ignores_unrelated_keys() -> None:
    a = {"number": 1, "name": "x", "mode_config": {"a": 1}, "extra": "ignored"}
    assert content_hash(a) == content_hash({**a, "extra": "other"})
    assert content_hash(a) != content_hash({**a, "name": "y"})
