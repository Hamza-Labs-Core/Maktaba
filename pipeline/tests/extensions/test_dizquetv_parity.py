"""The dizqueTV channel export does what the stand-alone mediaintel exporter does: removes what
the plan dropped (only what this tool made), logos and corner watermarks, guide settings,
filler cap, padded and Ramadan grids, and a rewrite that keeps what airs."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.engine.sql import dumps, now, to_ts
from maktaba_pipeline.integrations.dizquetv import PREFIX
from maktaba_pipeline.runtime import Database
from tests.extensions.harness import FakeDizque, error_of, events, summary_of
from tests.extensions.test_dizquetv import (
    channel_world,
    channels_job,
    make_plan,
    save_plan,
)


async def set_rules(db: Any, section: str, value: dict[str, Any]) -> None:
    await db.execute(
        "INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, $3)"
        " ON CONFLICT (key) DO UPDATE SET value = excluded.value",
        f"rules.{section}",
        dumps(value),
        to_ts(db, now()),
    )


def leftovers(dz: FakeDizque) -> None:
    """What an earlier export made and the plan no longer has, and what a person made."""
    dz.channels[9] = {"number": 9, "name": "Old", "maktabaPlan": {"key": "old"}, "programs": []}
    dz.channels[7] = {"number": 7, "name": "Mine", "programs": []}
    dz.shows["s-old"] = {"name": PREFIX + "Gone [G]", "content": []}
    dz.shows["s-mine"] = {"name": "A show I made", "content": []}
    dz.fillers["f-old"] = {"name": PREFIX + "Gone pool [P]", "content": []}
    dz.fillers["f-mine"] = {"name": "My fillers", "content": []}


def deleted(dz: FakeDizque) -> list[str]:
    return [p for m, p in dz.calls if m == "DELETE"]


@pytest.mark.asyncio
async def test_nothing_is_removed_unless_the_rules_say_so(db: Database) -> None:
    w, rt = await channel_world(db)
    leftovers(w.dz)
    job = await channels_job(db, rt)
    assert job["state"] == "done", error_of(job)
    assert deleted(w.dz) == [] and {7, 9} <= set(w.dz.channels)


@pytest.mark.asyncio
async def test_prune_removes_only_what_this_tool_made_and_the_plan_dropped(db: Database) -> None:
    w, rt = await channel_world(db)
    leftovers(w.dz)
    await set_rules(db, "dizquetv", {"prune": True})
    job = await channels_job(db, rt)
    assert job["state"] == "done", error_of(job)
    assert set(w.dz.channels) == {5, 7}  # 9 went; the person's 7 and the planned 5 stay
    assert "s-old" not in w.dz.shows and "f-old" not in w.dz.fillers
    assert "s-mine" in w.dz.shows and "f-mine" in w.dz.fillers
    (applied,) = await events(db, job["id"], "applied")
    assert sorted(r["kind"] for r in applied["changes"]["removed"]) == ["channel", "filler", "show"]
    # channels go first: they name the shows and the lists
    assert deleted(w.dz)[0] == "/api/channel"


@pytest.mark.asyncio
async def test_clear_channels_also_removes_other_channels_not_in_the_plan(db: Database) -> None:
    w, rt = await channel_world(db)
    leftovers(w.dz)
    await set_rules(db, "dizquetv", {"clear_channels": True})
    job = await channels_job(db, rt)
    assert job["state"] == "done", error_of(job)
    assert set(w.dz.channels) == {5}
    assert "s-mine" in w.dz.shows  # shows and lists a person made are never deleted


@pytest.mark.asyncio
async def test_only_channels_limits_the_run_and_never_prunes(db: Database) -> None:
    w, rt = await channel_world(db)
    leftovers(w.dz)
    await set_rules(db, "dizquetv", {"prune": True, "only_channels": "99"})
    job = await channels_job(db, rt)
    assert job["state"] == "done", error_of(job)
    assert 5 not in w.dz.channels and deleted(w.dz) == []


@pytest.mark.asyncio
async def test_a_channel_a_person_made_is_replaced_only_when_asked(db: Database) -> None:
    w, rt = await channel_world(db)
    w.dz.channels[5] = {"number": 5, "name": "Mine", "programs": []}
    await channels_job(db, rt)
    assert "maktabaPlan" not in w.dz.channels[5]
    await set_rules(db, "dizquetv", {"replace_existing": True})
    job = await channels_job(db, rt)
    assert job["state"] == "done", error_of(job)
    assert w.dz.channels[5]["maktabaPlan"]["key"] == "ch-1" and w.dz.channels[5]["programs"]


def with_config(plan: dict[str, Any], **cfg: Any) -> dict[str, Any]:
    plan["config"] = cfg
    return plan


async def replan(db: Any, plan: dict[str, Any], name: str) -> None:
    await db.execute("DELETE FROM channel_plans")
    await save_plan(db, plan, name)


@pytest.mark.asyncio
async def test_logo_and_corner_watermark_are_uploaded_and_reachable_by_the_tvs(
    db: Database, tmp_path: Path
) -> None:
    w, rt = await channel_world(db)
    (tmp_path / "default.png").write_bytes(b"png")
    (tmp_path / "wm.gif").write_bytes(b"gif")
    cfg = {
        "dz_logo_dir": str(tmp_path),
        "dz_watermark_file": str(tmp_path / "wm.gif"),
        "dz_watermark_position": "top-left",
        "dz_public_url": "http://192.168.1.20:8002/",
    }
    await replan(db, with_config(make_plan(), **cfg), "plan-art")
    job = await channels_job(db, rt)
    assert job["state"] == "done", error_of(job)
    assert sorted(w.dz.uploads) == ["maktaba-default.png", "maktaba-wm.gif"]
    ch = w.dz.channels[5]
    assert ch["icon"].startswith("http://192.168.1.20:8002/images/uploads/maktaba-default.png?v=")
    wm = ch["watermark"]
    assert wm["enabled"] and wm["animated"] and wm["position"] == "top-left"
    assert wm["url"].startswith("http://192.168.1.20:8002/images/uploads/maktaba-wm.gif?v=")


@pytest.mark.asyncio
async def test_a_changed_logo_rewrites_the_channel_but_not_what_airs(
    db: Database, tmp_path: Path
) -> None:
    w, rt = await channel_world(db)
    await channels_job(db, rt)
    before = w.dz.channels[5]
    slots_before = [c for c in w.dz.calls if "time-slots" in c[1]]
    (tmp_path / "default.png").write_bytes(b"png")
    await replan(db, with_config(make_plan(), dz_logo_dir=str(tmp_path)), "plan-logo")
    job = await channels_job(db, rt)
    assert job["outcome"] == "changed", error_of(job)
    after = w.dz.channels[5]
    assert after["icon"].endswith(tuple("0123456789abcdef")) and "?v=" in after["icon"]
    # the same schedule and programmes: the stored timeline stays as it airs
    assert after["programs"] == before["programs"] and after["startTime"] == before["startTime"]
    assert [c for c in w.dz.calls if "time-slots" in c[1]] == slots_before


@pytest.mark.asyncio
async def test_no_logo_settings_upload_nothing(db: Database) -> None:
    w, rt = await channel_world(db)
    await channels_job(db, rt)
    assert w.dz.uploads == [] and w.dz.channels[5]["icon"] == ""


@pytest.mark.asyncio
async def test_logos_on_a_web_host_are_checked_not_uploaded(db: Database, tmp_path: Path) -> None:
    w, rt = await channel_world(db)
    (tmp_path / "default.png").write_bytes(b"png")
    cfg = {"dz_logo_dir": str(tmp_path), "dz_art_url": "https://art.example/logos"}
    await replan(db, with_config(make_plan(), **cfg), "plan-host")
    w.dz.missing_urls.add("https://art.example/logos/default.png")
    job = await channels_job(db, rt)
    assert error_of(job)["kind"] == "logo_not_hosted"
    assert 5 not in w.dz.channels and w.dz.uploads == []
    w.dz.missing_urls.clear()
    job = await channels_job(db, rt)
    assert job["state"] == "done", error_of(job)
    assert w.dz.channels[5]["icon"].startswith("https://art.example/logos/default.png?v=")
    assert w.dz.uploads == []


@pytest.mark.asyncio
async def test_guide_settings_come_from_the_plans_config(db: Database) -> None:
    w, rt = await channel_world(db)
    cfg = {"dz_guide_min_program_s": 90, "dz_guide_max_padding_s": 45, "dz_guide_filler_title": "x"}
    await replan(db, with_config(make_plan(), **cfg), "plan-guide")
    await channels_job(db, rt)
    ch = w.dz.channels[5]
    assert ch["guideMinimumDurationSeconds"] == 90 and ch["guideMaxPaddingSeconds"] == 45
    assert ch["guideFlexPlaceholder"] == "x"


@pytest.mark.asyncio
async def test_a_filler_list_holds_at_most_the_setting(db: Database) -> None:
    w, rt = await channel_world(db)
    plan = with_config(make_plan(), dz_filler_max_items=2)
    plan["filler_pools"]["P1"]["items"] = [
        {"plex_rating_key": str(300 + i), "duration_s": 60.0, "title": f"c{i}"} for i in range(5)
    ]
    for i in range(5):
        w.plex.add_item(str(300 + i), f"c{i}", f"/lib/c{i}.mp4")
    await replan(db, plan, "plan-cap")
    await channels_job(db, rt)
    (filler,) = w.dz.fillers.values()
    assert [p["ratingKey"] for p in filler["content"]] == ["300", "301"]


def three_slots(plan: dict[str, Any], **sched: Any) -> dict[str, Any]:
    dz = plan["channels"][0]["exports"]["dizquetv"]
    dz["schedule"] = {
        **dz["schedule"],
        **sched,
        "slots": [
            {"time": h * 3_600_000, "showId": "lane:L1", "order": "next"} for h in (0, 8, 16)
        ],
    }
    return plan


@pytest.mark.asyncio
async def test_slots_of_one_show_are_merged_unless_the_channel_is_padded(db: Database) -> None:
    w, rt = await channel_world(db)
    await replan(db, three_slots(make_plan()), "plan-merged")
    await channels_job(db, rt)
    assert len(w.dz.channels[5]["scheduleBackup"]["slots"]) == 1  # one slot all day
    await replan(db, three_slots(make_plan(), pad=3_600_000), "plan-padded")
    await channels_job(db, rt)
    slots = w.dz.channels[5]["scheduleBackup"]["slots"]
    assert [s["time"] for s in slots] == [0, 8 * 3_600_000, 16 * 3_600_000]  # one per slot


@pytest.mark.asyncio
async def test_the_ramadan_grid_is_used_when_the_rules_ask(db: Database) -> None:
    w, rt = await channel_world(db)
    plan = make_plan()
    dz = plan["channels"][0]["exports"]["dizquetv"]
    dz["ramadan_schedule"] = {
        **dz["schedule"],
        "slots": [{"time": 3_600_000, "showId": "lane:L1", "order": "next"}],
    }
    await replan(db, plan, "plan-ramadan")
    await channels_job(db, rt)
    assert [s["time"] for s in w.dz.channels[5]["scheduleBackup"]["slots"]][0] == 0
    await set_rules(db, "dizquetv", {"ramadan": True})
    await channels_job(db, rt)
    assert [s["time"] for s in w.dz.channels[5]["scheduleBackup"]["slots"]] == [3_600_000]


@pytest.mark.asyncio
async def test_a_rewrite_lets_what_is_on_air_finish_and_keeps_every_episode(db: Database) -> None:
    w, rt = await channel_world(db)
    await channels_job(db, rt)
    await save_plan(db, make_plan(["103", "102", "101", "104"]), "plan-2")
    w.plex.add_item("104", "Episode 4", "/lib/e4.mp4")
    job = await channels_job(db, rt)
    assert job["outcome"] == "changed", error_of(job)
    keys = [p["ratingKey"] for p in w.dz.channels[5]["programs"] if not p.get("isOffline")]
    assert {"101", "102", "103", "104"} <= set(keys)  # none dropped


@pytest.mark.asyncio
async def test_without_keep_airing_the_timeline_is_rebuilt_from_the_start(db: Database) -> None:
    w, rt = await channel_world(db)
    await set_rules(db, "dizquetv", {"keep_airing": False})
    await channels_job(db, rt)
    await save_plan(db, make_plan(["103", "102", "101"]), "plan-2")
    await channels_job(db, rt)
    assert [p["ratingKey"] for p in w.dz.channels[5]["programs"]][:3] == ["103", "102", "101"]


@pytest.mark.asyncio
async def test_changing_a_dizquetv_rule_runs_the_stage_again(db: Database) -> None:
    w, rt = await channel_world(db)
    await channels_job(db, rt)
    assert (await channels_job(db, rt))["outcome"] == "skipped_unchanged"
    await set_rules(db, "dizquetv", {"prune": True})
    again = await channels_job(db, rt)
    assert again["outcome"] != "skipped_unchanged"
    assert summary_of(again)["status"] == "noop"  # it ran: nothing to prune


def test_filler_weights_are_the_planners_settings() -> None:
    from types import SimpleNamespace

    from maktaba_pipeline.integrations.dizquetv_export import filler_collections
    from maktaba_pipeline.schedule.channel_planner import PlannerConfig

    cfg = PlannerConfig(
        dz_weight_topic=7, dz_weight_promos=6, dz_weight_breaks=5, dz_weight_general=4
    )
    ch = SimpleNamespace(
        kind="theme",
        key="fiqh",
        filler_pools=("library.general", "fiqh.topic", "fiqh.breaks", "fiqh.promos"),
    )
    got = {f["pool"]: f["weight"] for f in filler_collections(ch, cfg)}  # type: ignore[arg-type]
    assert got == {"fiqh.topic": 7, "fiqh.promos": 6, "fiqh.breaks": 5, "library.general": 4}
    quran = SimpleNamespace(kind="quran", key="quran", filler_pools=("quran.topic",))
    assert [f["weight"] for f in filler_collections(quran, cfg)] == [7]  # type: ignore[arg-type]
