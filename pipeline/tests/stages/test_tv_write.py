# ruff: noqa: E501  (long SQL strings read better unwrapped)
"""write_channels gives Maktaba's channels what the dizqueTV export gives dizqueTV's (the
filler pools, each channel's filler lists, its logo, watermark and loudness), and the
scheduler airs from them on the real database: clips in the gaps, live broadcasts."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.channels import run_schedule
from maktaba_pipeline.channels.pass_ import channel_tz
from maktaba_pipeline.channels.repo import SqlRepo
from maktaba_pipeline.engine.sql import dumps, loads
from maktaba_pipeline.engine.stages.write_channels import apply_changes, compute_changes
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.schedule.tv_settings import MaktabaTvConfig
from tests.engine.factory import make_library, make_video
from tests.stages.world import set_rules


async def _plan(db: Database, tmp: Path) -> tuple[dict[str, Any], list[UUID], list[UUID]]:
    lib = await make_library(db)
    clips = [await make_video(db, lib, title=f"clip {i}", duration=60.0) for i in range(6)]
    talks = [await make_video(db, lib, title=f"talk {i}", duration=25 * 60.0) for i in range(4)]
    (tmp / "logos").mkdir()
    (tmp / "logos" / "news.png").write_bytes(b"png")
    (tmp / "logos" / "loading-news.png").write_bytes(b"png")
    (tmp / "logos" / "default.png").write_bytes(b"png")
    (tmp / "wm.png").write_bytes(b"png")
    rec = [{"path": str(v), "duration_s": 60.0, "title": f"clip {i}"} for i, v in enumerate(clips)]
    plan = {
        "planner_version": 9,
        "config": {
            "dz_logo_dir": str(tmp / "logos"),
            "dz_watermark_file": str(tmp / "wm.png"),
            "dz_watermark_position": "bottom-left",
            "dz_watermark_width": 10.0,
            "dz_watermark_margin": 3.0,
            "dz_filler_cooldown_ms": 600_000,
            "dz_weight_topic": 300,
            "dz_weight_general": 100,
        },
        "filler_pools": {
            "news.topic": {"name": "News · topic", "items": rec[:4]},
            "library.general": {"name": "General", "items": rec[3:] + [{"path": "not-an-id", "duration_s": 9}]},
            "unused": {"name": "Nobody's", "items": rec},
        },
        "channels": [
            {
                "key": "news",
                "kind": "theme",
                "filler_pools": ["news.topic", "library.general"],
                "maktaba": {
                    "number": 105,
                    "name": "الأخبار",
                    "mode": "schedule",
                    "mode_config": {"slots": [
                        {"start": "00:00", "end": "19:30", "kind": "flex", "filler_pool": "news.topic"},
                        {"start": "19:30", "end": "24:00", "filler_pool": "news.topic"}]},
                },
            },
            {
                "key": "other",
                "kind": "theme",
                "filler_pools": ["library.general"],
                "maktaba": {"number": 106, "name": "Other", "mode": "schedule",
                            "mode_config": {"slots": []}},
            },
        ],
    }  # fmt: skip
    return plan, clips, talks


async def _write(db: Database, plan: dict[str, Any]) -> Any:
    cs, wp = await compute_changes(db, plan, MaktabaTvConfig())
    await apply_changes(db, wp)
    return cs


async def _pools(db: Database) -> dict[str, set[str]]:
    out: dict[str, set[str]] = {}
    for r in await db.fetch(
        "SELECT p.external_key AS k, i.video_id AS v FROM filler_pools p"
        " LEFT JOIN filler_items i ON i.pool_id = p.id WHERE p.external_key IS NOT NULL"
    ):
        out.setdefault(str(r["k"]), set())
        if r["v"] is not None:
            out[str(r["k"])].add(str(r["v"]))
    return out


@pytest.mark.asyncio
async def test_filler_pools_lists_and_look_are_written_idempotently(
    db: Database, tmp_path: Path
) -> None:
    plan, clips, _ = await _plan(db, tmp_path)
    await db.execute(  # a pool made by hand: never touched
        "INSERT INTO filler_pools (id, channel_id, name) VALUES ('00000000-0000-0000-0000-0000000000f1', NULL, 'Mine')"
    )
    await _write(db, plan)
    pools = await _pools(db)
    assert set(pools) == {"news.topic", "library.general"}  # only what a channel uses
    assert pools["news.topic"] == {str(v) for v in clips[:4]}
    assert pools["library.general"] == {str(v) for v in clips[3:]}  # a bad path is left out
    row = await db.fetchrow("SELECT logo_path, mode_config FROM channels WHERE number = 105")
    assert row["logo_path"] == str(tmp_path / "logos" / "news.png")
    mc = loads(row["mode_config"])
    assert mc["filler"] == {
        "lists": [{"pool": "news.topic", "weight": 300, "cooldown_ms": 0},
                  {"pool": "library.general", "weight": 100, "cooldown_ms": 0}],
        "repeat_cooldown_ms": 600_000,
    }  # fmt: skip
    assert mc["look"] == {
        "watermark": {"path": str(tmp_path / "wm.png"), "position": "bottom-left",
                      "width_pct": 10.0, "margin_pct": 3.0},
        "loading_path": str(tmp_path / "logos" / "loading-news.png"),
        "loudnorm": {"enabled": True, "target_lufs": -16.0},
        "watermark_skip_filler": True,  # rules tv.watermark_skip_filler, as dizqueTV
    }  # fmt: skip
    other = await db.fetchrow("SELECT logo_path, mode_config FROM channels WHERE number = 106")
    assert other["logo_path"] == str(tmp_path / "logos" / "default.png")
    assert loads(other["mode_config"])["look"]["loading_path"] is None

    again = await _write(db, plan)
    assert again.is_empty  # nothing to do the second time
    n_items = await db.fetchval("SELECT count(*) FROM filler_items")

    # the plan drops a pool and a clip: only the tool's rows change
    plan["channels"][0]["filler_pools"] = ["news.topic"]
    plan["channels"][1]["filler_pools"] = []
    plan["filler_pools"]["news.topic"]["items"] = plan["filler_pools"]["news.topic"]["items"][:3]
    await _write(db, plan)
    assert {k: len(v) for k, v in (await _pools(db)).items()} == {"news.topic": 3}
    assert await db.fetchval("SELECT count(*) FROM filler_pools WHERE name = 'Mine'") == 1
    assert await db.fetchval("SELECT count(*) FROM filler_items") < n_items


_OVERRIDES_PG = """CREATE TABLE IF NOT EXISTS channel_art_overrides (
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('logo','watermark','loading')),
    path TEXT, settings JSONB, base JSONB,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY (channel_id, kind))"""
_OVERRIDES_SQLITE = """CREATE TABLE IF NOT EXISTS channel_art_overrides (
    channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('logo','watermark','loading')),
    path TEXT, settings TEXT, base TEXT,
    updated_at TEXT NOT NULL DEFAULT (datetime('now')), PRIMARY KEY (channel_id, kind))"""


async def _override(
    db: Database, cid: str, kind: str, path: str | None, settings: Any = None
) -> None:
    """What the API's art upload does (feat/tv-access, slot 0114): the effective art on
    the channel, the override recorded."""
    await db.execute(
        "INSERT INTO channel_art_overrides (channel_id, kind, path, settings) VALUES ($1,$2,$3,$4)",
        cid, kind, path, None if settings is None else dumps(settings),
    )  # fmt: skip
    r = await db.fetchrow("SELECT logo_path, mode_config FROM channels WHERE id = $1", cid)
    mc = loads(r["mode_config"])
    logo = r["logo_path"]
    if kind == "logo":
        logo = path
    elif kind == "loading":
        mc["look"]["loading_path"] = path
    else:
        mc["look"]["watermark"] = {
            **(mc["look"]["watermark"] or {}),
            **({"path": path} if path else {}),
            **(settings or {}),
        }
    await db.execute(
        "UPDATE channels SET logo_path = $2, mode_config = $3 WHERE id = $1", cid, logo, dumps(mc)
    )


@pytest.mark.asyncio
async def test_the_plan_carries_its_art_in_the_maktaba_keys(db: Database, tmp_path: Path) -> None:
    from maktaba_pipeline.engine.stages._plan_json import _with_art

    plan, _, _ = await _plan(db, tmp_path)
    m = _with_art(plan["channels"][0]["maktaba"], plan, "news")
    assert m["logo_path"] == str(tmp_path / "logos" / "news.png")
    assert m["mode_config"]["look"]["loading_path"] == str(tmp_path / "logos" / "loading-news.png")
    assert m["mode_config"]["look"]["watermark"]["position"] == "bottom-left"
    # write_channels takes the plan's art from there (and adds the look rules)
    m["logo_path"] = str(tmp_path / "logos" / "default.png")
    plan["channels"][0]["maktaba"] = m
    await _write(db, plan)
    row = await db.fetchrow("SELECT logo_path, mode_config FROM channels WHERE number = 105")
    assert row["logo_path"] == str(tmp_path / "logos" / "default.png")
    look_ = loads(row["mode_config"])["look"]
    assert look_["loudnorm"] == {"enabled": True, "target_lufs": -16.0}
    assert look_["watermark_skip_filler"] is True

    # the corner-logo rule turned off: the channel's look changes, nothing else
    _, wp = await compute_changes(db, plan, MaktabaTvConfig(watermark_skip_filler=False))
    assert wp.update and not wp.user_edited
    for _, want in wp.update:
        assert want.values["mode_config"]["look"]["watermark_skip_filler"] is False


@pytest.mark.asyncio
async def test_art_set_by_hand_is_kept_and_never_a_user_edit(db: Database, tmp_path: Path) -> None:
    plan, _, _ = await _plan(db, tmp_path)
    await _write(db, plan)
    await db.execute(_OVERRIDES_PG if db.dialect == "postgres" else _OVERRIDES_SQLITE)
    cid = str(await db.fetchval("SELECT id FROM channels WHERE number = 105"))
    await _override(db, cid, "logo", "/data/channel-art/x/logo-1.png")
    await _override(db, cid, "watermark", None, {"position": "top-left", "width_pct": 20.0})
    cs, wp = await compute_changes(db, plan, MaktabaTvConfig())
    assert not wp.user_edited and not wp.update  # the hand-set art is what it should be

    # the plan's art changes: what was set by hand stays, the rest follows the plan
    (tmp_path / "wm2.png").write_bytes(b"png")
    plan["config"]["dz_watermark_file"] = str(tmp_path / "wm2.png")
    plan["config"]["dz_watermark_margin"] = 5.0
    (tmp_path / "logos" / "loading-news.png").unlink()
    cs, wp = await compute_changes(db, plan, MaktabaTvConfig())
    assert not wp.user_edited and cid in [c for c, _ in wp.update]  # (and 106: same watermark)
    assert [c["fields"] for c in cs.changed if c.get("channel_id") == cid] == [["look"]]
    await apply_changes(db, wp)
    row = await db.fetchrow("SELECT logo_path, mode_config FROM channels WHERE id = $1", cid)
    look_ = loads(row["mode_config"])["look"]
    assert row["logo_path"] == "/data/channel-art/x/logo-1.png"
    assert look_["watermark"] == {"path": str(tmp_path / "wm2.png"), "position": "top-left",
                                  "width_pct": 20.0, "margin_pct": 5.0}  # fmt: skip
    assert look_["loading_path"] is None

    # a real edit (the name) is still a user edit
    await db.execute("UPDATE channels SET name = 'Mine' WHERE id = $1", cid)
    _, wp = await compute_changes(db, plan, MaktabaTvConfig())
    assert [u["channel_id"] for u in wp.user_edited] == [cid]


@pytest.mark.asyncio
async def test_the_scheduler_airs_the_written_filler_and_live_broadcasts(
    db: Database, tmp_path: Path
) -> None:
    if db.dialect != "postgres":
        pytest.skip("the scheduler's SqlRepo binds UUIDs and datetimes as Postgres takes them")
    plan, clips, talks = await _plan(db, tmp_path)
    await _write(db, plan)
    await set_rules(db, "dizquetv", {"live": [  # the dizqueTV rows serve Maktaba TV too
        {"channel": 105, "url": "https://www.youtube.com/@aljazeera/live", "title": "Live",
         "start": "20:00", "end": "22:00", "every_minutes": 60, "minutes": 10}]})  # fmt: skip
    cid = UUID(str(await db.fetchval("SELECT id FROM channels WHERE number = 105")))
    repo = SqlRepo(db)
    fs = await repo.load_filler(await repo.load_channel(cid))  # type: ignore[arg-type]
    assert [(x.key, x.weight, len(x.items)) for x in fs.lists] == [
        ("news.topic", 300, 4),
        ("library.general", 100, 3),
    ]
    assert set(fs.pools) == {"news.topic"} and fs.repeat_cooldown_ms == 600_000
    tz = channel_tz({})
    now = datetime(2026, 6, 14, 19, 0, tzinfo=tz).astimezone(UTC)
    async with db.transaction():
        await run_schedule(repo, cid, now=now, horizon=timedelta(hours=4))
    rows = await db.fetch(
        "SELECT kind, video_id, filler_item_id, source_url, title_snapshot, start_at, end_at, source_duration"
        " FROM channel_programs WHERE channel_id = $1 ORDER BY seq",
        cid,
    )
    kinds = {r["kind"] for r in rows}
    assert {"program", "filler", "live"} <= kinds
    live = [r for r in rows if r["kind"] == "live"]
    assert len(live) == 2 and all(
        r["source_url"].endswith("/live") and r["video_id"] is None for r in live
    )
    assert loads(live[0]["title_snapshot"]) == {"title": "Live"}
    filler = [r for r in rows if r["kind"] == "filler"]
    assert all(
        r["filler_item_id"] is not None and str(r["video_id"]) in {str(c) for c in clips}
        for r in filler
    )
    assert all(int(r["source_duration"]) == 60_000 for r in filler)
    assert {str(r["video_id"]) for r in rows if r["kind"] == "program"} <= {
        str(t) for t in talks
    } | {str(c) for c in clips}


@pytest.mark.asyncio
async def test_a_live_block_is_stored_with_its_address(db: Database) -> None:
    """Slot 0114: channel_programs takes kind 'live' with a source_url (both dialects)."""
    cid = "00000000-0000-0000-0000-0000000000c1"
    await db.execute(
        "INSERT INTO channels (id, number, name, slug, mode, mode_config) VALUES ($1, 901, 'n', 'n', 'schedule', '{}')",
        cid,
    )
    from maktaba_pipeline.engine.sql import to_ts

    t = datetime(2026, 6, 14, 20, 0, tzinfo=UTC)
    await db.execute(
        "INSERT INTO channel_programs (id, channel_id, seq, kind, start_at, end_at, source_duration, source_url)"
        " VALUES ('00000000-0000-0000-0000-0000000000d1', $1, 0, 'live', $2, $3, 600000, $4)",
        cid, to_ts(db, t), to_ts(db, t + timedelta(minutes=10)), "https://example.org/live",
    )  # fmt: skip
    got = await db.fetchrow(
        "SELECT kind, source_url FROM channel_programs WHERE channel_id = $1", cid
    )
    assert (got["kind"], got["source_url"]) == ("live", "https://example.org/live")
    assert (
        await db.fetchval("SELECT count(*) FROM filler_pools WHERE external_key IS NOT NULL") == 0
    )


@pytest.mark.asyncio
async def test_a_series_or_collection_slot_plays_its_own_queue(db: Database) -> None:
    """A slot a person picked in the channel editor: a series' episodes in season /
    episode order, a collection's items in their order, ``order: "shuffle"`` shuffled the
    same way every time."""
    from maktaba_pipeline.channels.base import ChannelDef, ChannelMode

    lib = await make_library(db)
    eps = [await make_video(db, lib, title=f"ep{i}", duration=1500.0) for i in range(4)]
    sid, col = "00000000-0000-0000-0000-0000000005e1", "00000000-0000-0000-0000-00000000c011"
    await db.execute(
        "INSERT INTO series (id, library_id, name) VALUES ($1, $2, 'S')", sid, str(lib)
    )
    for n, v in enumerate(reversed(eps)):  # inserted out of order: the order is season/episode
        await db.execute(
            "INSERT INTO series_episodes (series_id, video_id, season, episode) VALUES ($1, $2, 1, $3)",
            sid, str(v), 4 - n,
        )  # fmt: skip
    await db.execute(
        "INSERT INTO collections (id, library_id, name) VALUES ($1, $2, 'C')", col, str(lib)
    )
    for n, v in enumerate(eps[:2]):
        await db.execute(
            "INSERT INTO collection_items (collection_id, video_id, position) VALUES ($1, $2, $3)",
            col, str(v), 2 - n,
        )  # fmt: skip
    slots = [
        {"start": "00:00", "end": "06:00", "source": {"type": "series", "series_id": sid}},
        {"start": "06:00", "end": "12:00", "source": {"type": "collection", "collection_id": col}},
        {"start": "12:00", "end": "18:00",
         "source": {"type": "series", "series_id": sid, "order": "shuffle"}},
        {"start": "18:00", "end": "24:00", "source": {"type": "filler"}},
    ]  # fmt: skip
    ch = ChannelDef(
        id=UUID("00000000-0000-0000-0000-00000000c4a1"),
        mode=ChannelMode.SCHEDULE,
        mode_config={"slots": slots},
        source_filter=None,
        library_id=None,
    )
    got = await SqlRepo(db).resolve_slot_content(ch)
    assert got is not None
    assert [c.video_id for c in got[0] or []] == eps
    assert [c.video_id for c in got[1] or []] == [eps[1], eps[0]]
    assert sorted(c.video_id for c in got[2] or []) == sorted(eps)
    again = await SqlRepo(db).resolve_slot_content(ch)
    assert again is not None and again[2] == got[2]  # the same shuffle every time
    assert got[3] is None
    assert got[0] and got[0][0].duration_ms == 1_500_000
