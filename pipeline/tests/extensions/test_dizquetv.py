"""The dizqueTV extension: a custom show per season and the planned channels."""

from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.engine.graph import CATALOG_ID, Subject
from maktaba_pipeline.engine.sql import now, to_ts
from maktaba_pipeline.extensions.dizquetv import DizqueTVExtension, health_probes
from maktaba_pipeline.extensions.dizquetv import extension as default_extension
from maktaba_pipeline.extensions.dizquetv.channels import Action, changes_from, plan_actions
from maktaba_pipeline.extensions.dizquetv.shows import (
    DesiredShow,
    RemoteShow,
    ShowRemote,
    compute_changes,
)
from maktaba_pipeline.extensions.links import Link, get_link, put_link
from maktaba_pipeline.extensions.plex import PlexExtension
from maktaba_pipeline.integrations.dizquetv import PREFIX, DizqueTV, State
from maktaba_pipeline.integrations.plex import PlexClient
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_series_and_season, make_video
from tests.extensions.harness import (
    FakeDizque,
    FakePlex,
    add_episode,
    all_event_text,
    error_of,
    events,
    program_keys,
    run_stage,
    set_membership,
    setup_runtime,
    summary_of,
)

TOKEN = "SECRET-PLEX-TOKEN"
SETTINGS = {
    "plex": {"url": "http://plex.test", "token": TOKEN},
    "dizquetv": {"url": "http://dz.test"},
}
DAY = 86_400_000


@dataclass
class World:
    library: UUID
    series: UUID
    season: UUID
    videos: list[UUID]
    plex: FakePlex
    dz: FakeDizque
    extensions: list[Any]


def make_extensions(plex: FakePlex, dz: FakeDizque) -> list[Any]:
    return [
        PlexExtension(client_factory=lambda s: PlexClient(s["url"], s["token"], transport=plex)),
        DizqueTVExtension(
            dz_factory=lambda s: DizqueTV(s["url"], transport=dz),
            plex_factory=lambda s: PlexClient(s["url"], s["token"], transport=plex),
            tz_offset=lambda: 0,
        ),
    ]


async def make_world(db: Any, n: int = 3, *, cached: bool = True) -> World:
    lib = await make_library(db, "Lib")
    series, season = await make_series_and_season(db, lib, name="Show")
    plex, dz = FakePlex(), FakeDizque()
    videos: list[UUID] = []
    for i in range(1, n + 1):
        v = await make_video(db, lib, path=f"/lib/e{i}.mp4", title=f"Episode {i}")
        await add_episode(db, series, v, episode=i)
        videos.append(v)
        plex.add_item(str(100 + i), f"Episode {i}", f"/lib/e{i}.mp4")
        if cached:  # what the plex_collection stage leaves behind
            await put_link(
                db, Link("plex_item", "video", str(v), str(100 + i), f"Episode {i}", "1:x", False)
            )
    return World(lib, series, season, videos, plex, dz, make_extensions(plex, dz))


async def show_job(db: Any, rt: Any, w: World, **kw: Any) -> Any:
    return await run_stage(db, rt, "dizquetv_show", "season", w.season, **kw)


# ------------------------------------------------------------------ dizquetv_show


def test_show_compute_changes_is_pure() -> None:
    desired = DesiredShow("T", ("1", "2"))
    assert compute_changes(desired, ShowRemote()).added == ("1", "2")
    assert compute_changes(desired, ShowRemote(RemoteShow("a", "T", ("1", "2")))).is_empty
    moved = compute_changes(desired, ShowRemote(RemoteShow("a", "T", ("2", "1"))))
    assert moved.reordered == ("1", "2")
    foreign = compute_changes(desired, ShowRemote(None, foreign_name=True))
    assert foreign.is_empty and foreign.meta["action"] == "conflict"
    gone = compute_changes(
        DesiredShow("T", (), delete=True), ShowRemote(RemoteShow("a", "T", ("1",)))
    )
    assert gone.removed == ("1",) and gone.meta["action"] == "delete"


@pytest.mark.asyncio
async def test_show_is_created_from_cached_plex_keys_and_the_change_set_is_stored(
    db: Database,
) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, w.extensions, SETTINGS)
    job = await show_job(db, rt, w)
    assert job["state"] == "done" and job["outcome"] == "changed"

    ((sid, show),) = w.dz.shows.items()
    assert show["name"] == "Lib - Show"
    assert program_keys(show["content"]) == ["101", "102", "103"]
    assert show["content"][0]["serverKey"] == "PlexServer"

    (applied,) = await events(db, job["id"], "applied")
    assert applied["changes"]["kind"] == "dizquetv_show"
    assert applied["changes"]["added"] == ["101", "102", "103"]
    link = await get_link(db, "dizquetv_show", "season", str(w.season))
    assert link is not None and link.external_id == sid and link.created_by_tool
    assert TOKEN not in await all_event_text(db)


@pytest.mark.asyncio
async def test_show_second_run_writes_nothing_and_reorder_updates_in_place(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, w.extensions, SETTINGS)
    await show_job(db, rt, w)
    writes = list(w.dz.writes)
    forced = await show_job(db, rt, w, force=True)
    assert forced["outcome"] == "unchanged"
    assert w.dz.writes == writes
    assert await events(db, forced["id"], "applied") == []

    await db.execute(
        "UPDATE series_episodes SET episode = 4 - episode WHERE series_id = $1", str(w.series)
    )
    await set_membership(db, w.season, "h1")
    job = await show_job(db, rt, w)
    assert job["outcome"] == "changed"
    (show,) = w.dz.shows.values()  # updated in place: still one show
    assert program_keys(show["content"]) == ["103", "102", "101"]
    (applied,) = await events(db, job["id"], "applied")
    assert applied["changes"]["reordered"] == ["103", "102", "101"]
    assert [m for m, _ in w.dz.writes[len(writes) :]] == ["POST"]


@pytest.mark.asyncio
async def test_show_waits_when_a_plex_key_is_missing(db: Database) -> None:
    w = await make_world(db)
    await db.execute(
        "DELETE FROM external_links WHERE target = 'plex_item' AND subject_id = $1",
        str(w.videos[1]),
    )
    rt = await setup_runtime(db, w.extensions, SETTINGS)
    job = await show_job(db, rt, w)
    assert job["outcome"] == "waiting_inputs"
    assert summary_of(job)["reason"] == "plex_items_missing"
    assert w.dz.writes == []


@pytest.mark.asyncio
async def test_show_readback_mismatch_fails(db: Database) -> None:
    w = await make_world(db)
    w.dz.corrupt = True
    rt = await setup_runtime(db, w.extensions, SETTINGS)
    job = await show_job(db, rt, w)
    assert error_of(job)["kind"] == "readback_mismatch"
    assert await events(db, job["id"], "applied") == []


@pytest.mark.asyncio
async def test_a_show_the_user_made_is_never_touched(db: Database) -> None:
    w = await make_world(db)
    w.dz.shows["mine"] = {"name": "Lib - Show", "content": [{"ratingKey": "999"}]}
    rt = await setup_runtime(db, w.extensions, SETTINGS)
    job = await show_job(db, rt, w)
    assert job["outcome"] == "waiting_inputs"
    assert summary_of(job)["reason"] == "name_conflict"
    assert w.dz.writes == []
    assert w.dz.shows["mine"]["content"] == [{"ratingKey": "999"}]
    assert await get_link(db, "dizquetv_show", "season", str(w.season)) is None


@pytest.mark.asyncio
async def test_retired_season_deletes_only_the_show_we_made(db: Database) -> None:
    w = await make_world(db)
    w.dz.shows["mine"] = {"name": "Unrelated", "content": []}
    rt = await setup_runtime(db, w.extensions, SETTINGS)
    await show_job(db, rt, w)
    assert len(w.dz.shows) == 2
    await db.execute(
        "UPDATE series_seasons SET retired_at = $2 WHERE id = $1",
        str(w.season),
        to_ts(db, now()),
    )
    job = await show_job(db, rt, w)
    assert job["state"] == "done"
    assert list(w.dz.shows) == ["mine"]
    assert await get_link(db, "dizquetv_show", "season", str(w.season)) is None


@pytest.mark.asyncio
async def test_show_without_plex_settings_fails_non_retryably(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, w.extensions, {"dizquetv": SETTINGS["dizquetv"]})
    job = await show_job(db, rt, w)
    err = error_of(job)
    assert err["kind"] == "plex_not_configured" and err["retryable"] is False


@pytest.mark.asyncio
async def test_dizquetv_down_is_a_retryable_failure(db: Database) -> None:
    w = await make_world(db)
    w.dz.down = True
    rt = await setup_runtime(db, w.extensions, SETTINGS)
    job = await show_job(db, rt, w)
    assert error_of(job)["kind"] == "dizquetv_unreachable"
    assert error_of(job)["retryable"] is True


@pytest.mark.asyncio
async def test_show_dry_fingerprint_is_stable_and_database_only(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, w.extensions, SETTINGS)
    fp = w.extensions[1].impls()["dizquetv_show"].input_fingerprint
    assert fp is not None
    subject = Subject("season", w.season)
    a = await fp(db, subject)
    assert a is not None and a == await fp(db, subject)
    assert w.dz.calls == [] and w.plex.calls == []

    await set_membership(db, w.season, "h2")
    b = await fp(db, subject)
    assert b != a
    await db.execute(
        "DELETE FROM external_links WHERE target = 'plex_item' AND subject_id = $1",
        str(w.videos[0]),
    )
    assert await fp(db, subject) != b
    # the Plex collection's output joins the fingerprint once that stage has run
    c = await fp(db, subject)
    await run_stage(db, rt, "plex_collection", "season", w.season)
    assert await fp(db, subject) != c


# ------------------------------------------------------------------ dizquetv_channels


def lane_items(keys: list[str]) -> list[dict[str, Any]]:
    return [{"plex_rating_key": k, "duration_s": 1500.0, "title": f"Episode {k}"} for k in keys]


def make_plan(keys: list[str] | None = None, *, number: int = 5) -> dict[str, Any]:
    keys = keys or ["101", "102", "103"]
    return {
        "planner_version": 1,
        "lanes": {"L1": {"name": "Lane One", "units": [{"episodes": lane_items(keys)}]}},
        "filler_pools": {
            "P1": {
                "name": "Pool",
                "items": [{"plex_rating_key": "201", "duration_s": 60.0, "title": "c"}],
            }
        },
        "channels": [
            {
                "key": "ch-1",
                "number": number,
                "exports": {
                    "dizquetv": {
                        "number": number,
                        "name": "Channel One",
                        "schedule": {
                            "period": DAY,
                            "lateness": 300_000,
                            "maxDays": 365,
                            "flexPreference": "distribute",
                            "pad": 1,
                            "slots": [
                                {"time": 0, "showId": "lane:L1", "order": "next"},
                                {"time": DAY // 2, "showId": "flex.", "order": "shuffle"},
                            ],
                        },
                        "ramadan_schedule": None,
                        "fillerCollections": [{"pool": "P1", "weight": 300, "cooldown": 0}],
                        "fillerRepeatCooldown": 1000,
                    }
                },
            }
        ],
    }


async def save_plan(db: Any, plan: dict[str, Any], input_hash: str) -> None:
    await db.execute(
        "INSERT INTO channel_plans (planner_version, input_hash, plan) VALUES ($1,$2,$3)",
        1,
        input_hash,
        json.dumps(plan),
    )


async def channels_job(db: Any, rt: Any, **kw: Any) -> Any:
    return await run_stage(db, rt, "dizquetv_channels", "catalog", CATALOG_ID, **kw)


async def channel_world(db: Any) -> tuple[World, Any]:
    w = await make_world(db)
    w.plex.add_item("201", "Clip", "/lib/clip.mp4")
    rt = await setup_runtime(db, w.extensions, SETTINGS)
    await save_plan(db, make_plan(), "plan-1")
    return w, rt


@pytest.mark.asyncio
async def test_channels_written_from_the_latest_plan(db: Database) -> None:
    w, rt = await channel_world(db)
    job = await channels_job(db, rt)
    assert job["state"] == "done" and job["outcome"] == "changed", error_of(job)

    names = sorted(s["name"] for s in w.dz.shows.values())
    assert names == [PREFIX + "Lane One [L1]"]
    assert [f["name"] for f in w.dz.fillers.values()] == [PREFIX + "Pool [P1]"]
    ch = w.dz.channels[5]
    assert ch["name"] == "Channel One" and ch["maktabaPlan"]["key"] == "ch-1"
    assert {p["ratingKey"] for p in ch["programs"]} == {"101", "102", "103"}
    assert ch["scheduleBackup"]["slots"][0]["showId"].startswith("custom.")
    assert ch["fillerCollections"][0]["id"] in w.dz.fillers

    (applied,) = await events(db, job["id"], "applied")
    changes = applied["changes"]
    assert changes["kind"] == "dizquetv_channels"
    assert sorted(a["kind"] for a in changes["added"]) == ["channel", "filler", "show"]
    assert changes["counts"]["added"] == 3

    link = await get_link(db, "dizquetv_channel", "catalog", "ch-1")
    assert link is not None and link.external_id == "5" and link.created_by_tool
    assert TOKEN not in await all_event_text(db)


@pytest.mark.asyncio
async def test_channels_second_run_with_identical_remote_state_writes_nothing(db: Database) -> None:
    w, rt = await channel_world(db)
    first = await channels_job(db, rt)
    writes = list(w.dz.writes)
    assert writes
    again = await channels_job(db, rt)  # same plan, same write mode: skipped by the dry fingerprint
    assert again["outcome"] == "skipped_unchanged"
    forced = await channels_job(db, rt, force=True)
    assert forced["outcome"] == "unchanged"
    assert forced["output_hash"] == first["output_hash"]
    assert w.dz.writes == writes
    assert await events(db, forced["id"], "applied") == []


@pytest.mark.asyncio
async def test_channels_update_keeps_user_settings_and_rewrites_the_lane(db: Database) -> None:
    w, rt = await channel_world(db)
    await channels_job(db, rt)
    w.dz.channels[5]["name"] = "My own name"  # the user renamed it in dizqueTV
    w.dz.channels[5]["icon"] = "http://icon"
    await save_plan(db, make_plan(["103", "102", "101"]), "plan-2")
    job = await channels_job(db, rt)
    assert job["outcome"] == "changed", error_of(job)
    (applied,) = await events(db, job["id"], "applied")
    kinds = sorted(a["kind"] for a in applied["changes"]["changed"])
    assert kinds == ["channel", "show"]
    (show,) = w.dz.shows.values()
    assert program_keys(show["content"]) == ["103", "102", "101"]
    ch = w.dz.channels[5]
    assert ch["icon"] == "http://icon"
    assert [p["ratingKey"] for p in ch["programs"]][:3] == ["103", "102", "101"]
    assert not any(m == "DELETE" for m, _ in w.dz.calls)  # nothing is ever deleted


@pytest.mark.asyncio
async def test_a_channel_the_user_made_is_never_overwritten(db: Database) -> None:
    w, rt = await channel_world(db)
    w.dz.channels[5] = {"number": 5, "name": "Mine", "programs": []}  # no maktabaPlan marker
    job = await channels_job(db, rt)
    assert job["state"] == "done"
    assert w.dz.channels[5] == {"number": 5, "name": "Mine", "programs": []}
    (applied,) = await events(db, job["id"], "applied")
    assert applied["changes"]["meta"]["conflicts"] == [
        {"kind": "channel", "name": "#5 Channel One"}
    ]
    assert await get_link(db, "dizquetv_channel", "catalog", "ch-1") is None


@pytest.mark.asyncio
async def test_channels_wait_for_a_plan(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, w.extensions, SETTINGS)
    job = await channels_job(db, rt)
    assert job["outcome"] == "waiting_inputs"
    assert summary_of(job)["reason"] == "no_channel_plan"


@pytest.mark.asyncio
async def test_channels_readback_mismatch_fails(db: Database) -> None:
    w, rt = await channel_world(db)
    w.dz.corrupt = True
    job = await channels_job(db, rt)
    assert error_of(job)["kind"] == "readback_mismatch"
    assert await events(db, job["id"], "applied") == []


@pytest.mark.asyncio
async def test_channels_dry_fingerprint_follows_plan_and_write_mode(db: Database) -> None:
    w, rt = await channel_world(db)
    fp = w.extensions[1].impls()["dizquetv_channels"].input_fingerprint
    assert fp is not None
    subject = Subject("catalog", CATALOG_ID)
    a = await fp(db, subject)
    assert a is not None and a == await fp(db, subject)
    assert w.dz.calls == []
    await save_plan(db, make_plan(), "plan-2")
    b = await fp(db, subject)
    assert b != a
    await db.execute(
        "UPDATE job_stages SET write_mode = 'review' WHERE stage = 'dizquetv_channels'"
    )
    assert await fp(db, subject) != b


def test_plan_actions_is_pure_and_reports_conflicts() -> None:
    state = State(server="PlexServer")
    actions = plan_actions(make_plan(), state, tz_offset=0)
    assert [(a.kind, a.verb) for a in actions] == [
        ("filler", "create"),
        ("show", "create"),
        ("channel", "create"),
    ]
    assert all(isinstance(a, Action) for a in actions)
    cs = changes_from(actions)
    assert cs.counts["added"] == 3
    # the same plan against the same state gives the same digests
    assert [a.digest for a in actions] == [a.digest for a in plan_actions(make_plan(), state)]


# ------------------------------------------------------------------ health and manifest


@pytest.mark.asyncio
async def test_health_probe_reports_ms_or_minus_one() -> None:
    plex, dz = FakePlex(), FakeDizque()
    ext = make_extensions(plex, dz)[1]
    probes = ext.health_probes(SETTINGS["dizquetv"])
    assert set(probes) == {"net.tv_ms"}
    assert await probes["net.tv_ms"]() >= 0
    dz.down = True
    assert await probes["net.tv_ms"]() == -1
    assert ext.health_probes({}) == {}
    assert health_probes({}) == {}


def test_manifest_and_configuration() -> None:
    m = default_extension.manifest
    assert m.name == "dizquetv" and m.stages == ("dizquetv_show", "dizquetv_channels")
    assert {f.key: f.env for f in m.settings} == {"url": "MAKTABA_DIZQUETV_URL"}
    assert default_extension.is_configured({"url": "http://x"})
    assert not default_extension.is_configured({})


@pytest.mark.asyncio
async def test_plan_videos_are_resolved_to_plex_keys_through_external_links(db: Database) -> None:
    w, rt = await channel_world(db)
    plan = make_plan()
    eps = plan["lanes"]["L1"]["units"][0]["episodes"]
    for e, v in zip(eps, w.videos, strict=True):  # core plans name videos by id, no Plex key
        e["path"], e["plex_rating_key"] = str(v), None
    await db.execute("DELETE FROM channel_plans")
    await save_plan(db, plan, "plan-ids")
    job = await channels_job(db, rt)
    assert job["state"] == "done", error_of(job)
    (show,) = w.dz.shows.values()
    assert program_keys(show["content"]) == ["101", "102", "103"]


def test_exporter_builds_the_dizquetv_block_from_a_real_plan() -> None:
    from maktaba_pipeline.schedule.channel_planner import (
        EpisodeRef,
        PlannerConfig,
        SeasonCandidate,
        suggest_channels,
    )

    eps = tuple(EpisodeRef(path=f"v{i}", duration_s=1800.0, title=f"t{i}") for i in range(40))
    seasons = [SeasonCandidate(series_id=f"s{i}", name=f"Show {i}", episodes=eps) for i in range(3)]
    # 60 h: a small library's rules (the default wants a month per channel)
    small = PlannerConfig(channel_min_days=1.0, channel_day_hours=10.0)
    plan = suggest_channels(seasons, (), small)
    assert plan.channels
    exporters = default_extension.plan_exporters()
    assert set(exporters) == {"dizquetv"}
    block = exporters["dizquetv"](plan.channels[0], plan)
    assert block["number"] == plan.channels[0].number
    assert block["schedule"]["slots"] and "fillerCollections" in block
    assert block["fillerRepeatCooldown"] == plan.config.dz_filler_cooldown_ms


def test_plex_server_name_is_the_one_at_the_configured_plex_url() -> None:
    class Listed(DizqueTV):
        def __init__(self, servers: list[dict[str, Any]]) -> None:
            super().__init__("http://dz")
            self.servers = servers

        def get(self, path: str) -> Any:
            assert path == "/api/plex-servers"
            return self.servers

    two = [
        {"name": "old", "uri": "http://old:32400"},
        {"name": "home", "uri": "http://plex:32400/"},
    ]
    assert Listed(two).plex_server_name("http://PLEX:32400") == "home"
    assert Listed(two).plex_server_name("http://elsewhere") is None  # ambiguous: never guess
    assert Listed(two).plex_server_name() is None
    one = [{"name": "only", "uri": "http://x"}]
    assert Listed(one).plex_server_name("http://elsewhere") == "only"
    assert Listed([]).plex_server_name("http://plex:32400") is None
