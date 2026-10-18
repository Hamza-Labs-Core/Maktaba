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
from maktaba_pipeline.engine.sql import loads
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


@pytest.mark.asyncio
async def test_a_channel_written_before_the_logo_was_written_is_not_taken_for_a_user_edit(
    db: Database, tmp_path: Path
) -> None:
    from maktaba_pipeline.engine.hashing import canonical_hash
    from maktaba_pipeline.engine.stages.write_channels import _FIELDS_V1

    plan, _, _ = await _plan(db, tmp_path)
    await _write(db, plan)
    cid = await db.fetchval("SELECT id FROM channels WHERE number = 105")
    r = await db.fetchrow("SELECT * FROM channels WHERE id = $1", cid)
    row = {k: (loads(r[k]) if k == "mode_config" else r[k]) for k in _FIELDS_V1}
    row["enabled"] = bool(row["enabled"])
    # as write_channels 1 left it: no logo, a hash without one
    await db.execute("UPDATE channels SET logo_path = NULL WHERE id = $1", cid)
    await db.execute(
        "UPDATE external_links SET content_hash = $1 WHERE target = 'maktaba_channel' AND subject_id = 'news'",
        canonical_hash(row),
    )
    cs, wp = await compute_changes(db, plan, MaktabaTvConfig())
    assert not wp.user_edited and [c for c, _ in wp.update] == [str(cid)]


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
    """Slot 0109: channel_programs takes kind 'live' with a source_url (both dialects)."""
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
