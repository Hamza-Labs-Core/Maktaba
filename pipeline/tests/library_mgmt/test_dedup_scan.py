"""Story 47.6: ``dedup_scan`` groups exact copies, other files of the same video, and
nothing else (real clips, real scanner, real SQLite; Postgres with MAKTABA_TEST_PG_URL)."""

from __future__ import annotations

import shutil
from pathlib import Path
from typing import Any
from unittest.mock import MagicMock

import pytest

from maktaba_pipeline.engine.context import StageContext
from maktaba_pipeline.engine.sql import loads
from maktaba_pipeline.engine.stages import dedup_scan
from maktaba_pipeline.identity.hasher import hash_full_file
from maktaba_pipeline.library_mgmt.duplicates import (
    find_groups,
    frames_match,
    members_key,
    normalise_title,
    write_groups,
)
from maktaba_pipeline.runtime import Database
from tests.library_mgmt.dedup_world import lecture, other_video, probe_into_db, scan, video_at
from tests.library_mgmt.epic47_world import World, make_world, same
from tests.library_mgmt.media_fixtures import needs_ffmpeg, reencode_to_mp4


def test_titles_and_frames() -> None:
    assert normalise_title("Lecture 1 — Tafsir (Part ١)") == normalise_title(
        "lecture 1 tafsir part ١"
    )
    assert normalise_title(None) == ""
    a = [0x0F0F0F0F0F0F0F0F, 0xFFFF0000FFFF0000, 0x1234567812345678, 0, 1]
    near = [x ^ 0b111 for x in a]  # 3 bits apart each
    assert frames_match(a, near)
    far = list(near)
    far[0] ^= 0xFF00  # 1 of 5 far: 80 % still match
    assert frames_match(a, far)
    far[1] ^= 0xFF00  # 2 of 5: 60 %
    assert not frames_match(a, far)
    assert not frames_match([], a)


@pytest.mark.asyncio
async def test_members_key_is_order_free() -> None:
    from maktaba_pipeline.library_mgmt.duplicates import Member

    m1 = Member("v1", "l", "/a", False, 1)
    m2 = Member("v1", "l", "/b", True, 1)
    assert members_key([m1, m2]) == members_key([m2, m1]) != members_key([m1])


async def _library(db: Database, tmp_path: Path) -> tuple[World, dict[str, Any]]:
    w = await make_world(db, tmp_path)
    a = lecture(w.root / "a" / "Lecture 1.mkv")
    b = w.root / "b" / "Lecture 1.mkv"
    b.parent.mkdir()
    shutil.copyfile(a, b)  # byte-identical
    c = w.root / "c" / "Lecture 1.mp4"
    c.parent.mkdir()
    reencode_to_mp4(a, c)  # the same video, another file
    d = w.root / "d" / "Lecture 1.mkv"
    d.parent.mkdir()
    other_video(d)  # same title and length, another picture
    x = w.root / "e" / "Talk.mkv"
    y = w.root / "f" / "Talk (re-upload).mkv"
    for p, data in ((x, b"talk x " * 3000), (y, b"talk y " * 3000)):
        p.parent.mkdir()
        p.write_bytes(data)
    await scan(db, w)
    ids = {k: await video_at(db, p) for k, p in (("a", a), ("c", c), ("d", d), ("x", x), ("y", y))}
    # the scanner kept a or b as the row's path: name them by what it chose
    own = str(await db.fetchval("SELECT path FROM videos WHERE id = $1", ids["a"]))
    ids["copy"] = str(b) if own == str(a) else str(a)
    ids["own"] = own
    for k, p in (("a", Path(own)), ("c", c), ("d", d)):
        await probe_into_db(db, ids[k], p)
    for k in ("x", "y"):
        await db.execute(
            "INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id)"
            " VALUES ($1, $2, 'youtube', 'dQw4w9WgXcQ')",
            f"00000000-0000-4000-8000-00000000000{1 if k == 'x' else 2}",
            ids[k],
        )
    return w, ids


def _by_tier(groups: list[Any]) -> dict[str, list[Any]]:
    out: dict[str, list[Any]] = {}
    for g in groups:
        out.setdefault(g.tier, []).append(g)
    return out


@needs_ffmpeg
@pytest.mark.asyncio
async def test_the_groups(db: Database, tmp_path: Path) -> None:
    w, ids = await _library(db, tmp_path)
    plan = await find_groups(db, str(w.library_id), to_local=same)
    tiers = _by_tier(plan.groups)

    (exact,) = tiers["exact"]
    assert {(m.video_id, m.path, m.is_copy) for m in exact.members} == {
        (ids["a"], ids["own"], False),
        (ids["a"], ids["copy"], True),
    }
    assert exact.keeper_video_id == ids["a"] and exact.auto
    full = hash_full_file(ids["own"])
    assert {s["full_hash"] for s in exact.scores.values()} == {full}
    assert plan.full_hashes == [(ids["a"], full)]
    assert exact.library_id == str(w.library_id)

    same_video = {frozenset(g.video_ids): g for g in tiers["same_video"]}
    assert set(same_video) == {
        frozenset((ids["a"], ids["c"])),  # the re-encode: title, length and frames
        frozenset((ids["x"], ids["y"])),  # one YouTube id
    }
    reenc = same_video[frozenset((ids["a"], ids["c"]))]
    assert reenc.keeper_video_id == ids["c"]  # an MP4 plays directly: rule 1
    assert {m.path for m in reenc.members} >= {ids["own"], ids["copy"]}  # the copy goes too
    assert not reenc.auto and "title" in reenc.reason
    assert "YouTube" in same_video[frozenset((ids["x"], ids["y"]))].reason
    # the other video of the same title and length is in no group
    assert all(ids["d"] not in g.video_ids for g in plan.groups)


@needs_ffmpeg
@pytest.mark.asyncio
async def test_write_dismiss_and_scan_again(db: Database, tmp_path: Path) -> None:
    w, ids = await _library(db, tmp_path)
    lib = str(w.library_id)
    plan = await find_groups(db, lib, to_local=same)
    rep = await write_groups(db, plan, lib)
    assert len(rep.created) == 3 and len(rep.auto) == 1
    rows = await db.fetch("SELECT id, tier, keeper_video_id, members_key FROM duplicate_groups")
    assert len(rows) == 3
    members = await db.fetch(
        "SELECT group_id, video_id, path, is_copy, score FROM duplicate_members"
    )
    assert len(members) == 2 + 3 + 2
    assert all(isinstance(loads(m["score"]), dict) for m in members)
    full = await db.fetchval("SELECT full_hash FROM videos WHERE id = $1", ids["a"])
    assert full == hash_full_file(ids["own"])

    yt = next(r for r in rows if str(r["keeper_video_id"]) in (ids["x"], ids["y"]))
    await db.execute(
        "UPDATE duplicate_groups SET status = 'dismissed' WHERE id = $1", str(yt["id"])
    )

    again = await find_groups(db, lib, to_local=same)
    assert again.groups == [] and len(again.known) == 3  # nothing read again
    assert again.bytes_hashed == 0
    rep2 = await write_groups(db, again, lib)
    assert rep2.created == [] and rep2.removed_stale == 0
    status = {
        str(r["id"]): str(r["status"])
        for r in await db.fetch("SELECT id, status FROM duplicate_groups")
    }
    assert status[str(yt["id"])] == "dismissed" and len(status) == 3

    # a group whose member went is removed at the next scan
    await db.execute("DELETE FROM media_metadata_enrichment WHERE video_id = $1", ids["y"])
    await db.execute("UPDATE duplicate_groups SET status = 'open' WHERE id = $1", str(yt["id"]))
    rep3 = await write_groups(db, await find_groups(db, lib, to_local=same), lib)
    assert rep3.removed_stale == 1
    assert await db.fetchval("SELECT COUNT(*) FROM duplicate_groups") == 2


def _job(library_id: Any, payload: dict[str, Any]) -> Any:
    job = MagicMock()
    job.id = 1
    job.subject.id = library_id
    job.payload = payload
    return job


def _ctx() -> Any:
    ctx = MagicMock(spec=StageContext)
    ctx.local_path.side_effect = same

    async def nothing(*a: Any, **k: Any) -> None:
        return None

    ctx.check_cancel.side_effect = nothing
    ctx.progress.side_effect = nothing
    return ctx


@needs_ffmpeg
@pytest.mark.asyncio
async def test_the_stage_dry_run_and_auto_apply(db: Database, tmp_path: Path) -> None:
    w, ids = await _library(db, tmp_path)
    dry = await dedup_scan.run(db, _job(w.library_id, {"dry_run": True}), _ctx())
    assert dry.summary["dry_run"] and dry.summary["exact"] == 1
    assert dry.summary["same_video"] == 2 and len(dry.summary["plan"]) == 3
    assert not dry.requests
    assert await db.fetchval("SELECT COUNT(*) FROM duplicate_groups") == 0
    assert await db.fetchval("SELECT full_hash FROM videos WHERE id = $1", ids["a"]) is None

    res = await dedup_scan.run(db, _job(w.library_id, {}), _ctx())
    assert res.outcome == "changed" and res.summary["created"] == 3
    exact = await db.fetchval("SELECT id FROM duplicate_groups WHERE tier = 'exact'")
    assert [(r.stage, r.subject_kind, str(r.subject_id)) for r in res.requests] == [
        ("dedup_apply", "dup_group", str(exact))
    ]
    assert res.requests[0].priority == 150

    # auto_exact off: the exact group waits for review like the rest
    await db.execute("DELETE FROM duplicate_groups")
    await db.execute(
        "UPDATE libraries SET settings = $2 WHERE id = $1",
        str(w.library_id),
        '{"dedup": {"auto_exact": false}}',
    )
    off = await dedup_scan.run(db, _job(w.library_id, {}), _ctx())
    assert off.summary["created"] == 3 and not off.requests
