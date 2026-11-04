"""Slot 0175: every library's YouTube sources from Tartube's records (``youtube_tartube``).

Every fixture is invented here (folder names, channels, ids); no real Tartube file is read.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.engine.sql import dumps, is_pg, now, to_ts
from maktaba_pipeline.library_mgmt import tartube_sources
from maktaba_pipeline.library_mgmt.tartube_import import folder_key, read_tartube
from maktaba_pipeline.library_mgmt.tartube_sources import Target, classify
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library
from tests.extensions.harness import error_of, run_stage, setup_runtime, summary_of

CATALOG = UUID("00000000-0000-0000-0000-00000000ca7a")
HANDLE_ID = "UC" + "h" * 22
TALKS_ID = "UC" + "t" * 22
PLAYLIST = "PLinvented0001"
NESTED = "PLinvented0002"
V1, V2, V3 = "vvvvvvvvvv1", "vvvvvvvvvv2", "vvvvvvvvvv3"


def test_classify() -> None:
    assert classify("https://www.youtube.com/@lessons/videos") == Target(
        "channel", "https://www.youtube.com/@lessons/videos", None
    )
    assert classify(f"https://youtube.com/playlist?list={PLAYLIST}&feature=shared") == Target(
        "playlist", f"https://www.youtube.com/playlist?list={PLAYLIST}", PLAYLIST
    )
    # a video page in a playlist: the playlist
    assert classify(f"https://www.youtube.com/watch?v={V1}&list={PLAYLIST}&index=8") == Target(
        "playlist", f"https://www.youtube.com/playlist?list={PLAYLIST}", PLAYLIST
    )
    assert classify(f"https://www.youtube.com/channel/{TALKS_ID}/videos") == Target(
        "channel", f"https://www.youtube.com/channel/{TALKS_ID}/videos", TALKS_ID
    )
    assert classify("https://www.youtube.com/results?search_query=talks") == "search"
    assert classify("https://x.com/someone/media") == "not_youtube"
    assert classify("https://www.youtube.com/watch?v=" + V1) == "unrecognised"
    assert classify(None) == "no_url"


def test_folder_key_ignores_case_marks_and_spaces() -> None:
    assert folder_key("Lessons‬‎ ") == folder_key("lessons")


def _native(path: Path) -> Path:
    """Tartube's own export: channels, playlists, a folder holding one, and the skips."""
    doc = {
        "file_type": "db_export",
        "db_dict": {
            "1": {"type": "channel", "name": "Lessons",
                  "source": "https://www.youtube.com/@lessons/videos",
                  "db_dict": {"11": {"type": "video", "vid": V1, "file": "a.mp4"}}},
            "2": {"type": "playlist", "name": "Recitations",
                  "source": f"https://www.youtube.com/playlist?list={PLAYLIST}", "db_dict": {}},
            "3": {"type": "channel", "name": "Talks",
                  "source": f"https://www.youtube.com/channel/{TALKS_ID}/videos", "db_dict": {}},
            "4": {"type": "channel", "name": "Not here yet",
                  "source": "https://www.youtube.com/@elsewhere", "db_dict": {}},
            "5": {"type": "channel", "name": "A search",
                  "source": "https://www.youtube.com/results?search_query=x", "db_dict": {}},
            "6": {"type": "channel", "name": "Another site", "source": "https://x.com/a/media",
                  "db_dict": {}},
            "7": {"type": "folder", "name": "Unsorted", "db_dict": {
                "71": {"type": "channel", "name": "Nested",
                       "source": f"https://www.youtube.com/playlist?list={NESTED}",
                       "db_dict": {}}}},
            "8": {"type": "channel", "name": "Unresolved",
                  "source": "https://www.youtube.com/@nobody", "db_dict": {}},
            "9": {"type": "channel", "name": "Follows",
                  "source": "https://www.youtube.com/@follows/videos", "db_dict": {}},
        },
    }  # fmt: skip
    path.write_text(json.dumps(doc))
    return path


def _mio(path: Path) -> Path:
    """Make-It-Offline's shape: channels and playlists apart, with ``isDownloaded``."""
    doc = {
        "channels": [{"dbid": 1, "name": "Lessons", "url": "https://www.youtube.com/@lessons"}],
        "playlists": [{"dbid": 2, "name": "Recitations",
                       "url": f"https://www.youtube.com/playlist?list={PLAYLIST}"}],
        "videos": [
            {"dbid": 3, "videoId": V1, "channelDbid": 1, "isDownloaded": True},
            {"dbid": 4, "videoId": V2, "playlistDbids": [2], "isDownloaded": True},
            {"dbid": 5, "videoId": V3, "playlistDbids": [2], "isDownloaded": False},
        ],
    }  # fmt: skip
    path.write_text(json.dumps(doc))
    return path


def test_both_shapes_name_their_channels_and_playlists(tmp_path: Path) -> None:
    native, _ = read_tartube(str(_native(tmp_path / "n.json")))
    assert {(s.kind, s.name) for s in native} >= {
        ("channel", "Lessons"), ("playlist", "Recitations"), ("channel", "Nested")
    }  # fmt: skip
    mio, recs = read_tartube(str(_mio(tmp_path / "m.json")))
    assert {(s.kind, s.name) for s in mio} == {("channel", "Lessons"), ("playlist", "Recitations")}
    assert {r.vid for r in recs} == {V1, V2, V3}


async def _library(db: Any, root: Path) -> UUID:
    lib = await make_library(db)
    roots = [str(root)]
    await db.execute(
        "UPDATE libraries SET roots = $2 WHERE id = $1",
        str(lib),
        roots if is_pg(db) else dumps(roots),
    )
    return lib


async def _sources(db: Any) -> dict[tuple[str, str], dict[str, Any]]:
    rows = await db.fetch("SELECT library_id, kind, external_id, url, mode FROM youtube_sources")
    return {(str(r["library_id"]), str(r["external_id"])): dict(r) for r in rows}


@pytest.mark.asyncio
async def test_the_stage_adds_sources_off_and_reports_the_rest(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    base = tmp_path / "Youtube"
    lessons = await _library(db, base / "lessons")  # the folder's case differs
    recits = await _library(db, base / "Recitations")
    talks = await _library(db, base / "Talks")
    nested = await _library(db, base / "Nested")
    unresolved = await _library(db, base / "Unresolved")
    # Talks already follows its channel, on: it keeps its mode
    await db.execute(
        "INSERT INTO youtube_sources (id, library_id, kind, url, external_id, mode, added_at)"
        " VALUES ($1, $2, 'channel', 'https://www.youtube.com/channel/x', $3, 'new_only', $4)",
        "5a000000-0000-4000-8000-0000000000aa",
        str(talks),
        TALKS_ID,
        to_ts(db, now()),
    )
    asked: list[str] = []

    async def resolve(url: str) -> tuple[str, str | None] | None:
        asked.append(url)
        return (HANDLE_ID, "Lessons") if "@lessons" in url else None

    monkeypatch.setattr(tartube_sources, "resolve_channel", resolve)
    export = _native(tmp_path / "tartube_db_export.json")
    rt = await setup_runtime(db, [])
    job = await run_stage(
        db, rt, "youtube_tartube", "catalog", CATALOG, payload={"tartube": {"path": str(export)}}
    )
    assert job["state"] == "done", error_of(job)
    rep = summary_of(job)
    got = await _sources(db)
    assert got[(str(lessons), HANDLE_ID)]["mode"] == "off"
    assert got[(str(lessons), HANDLE_ID)]["url"] == "https://www.youtube.com/@lessons/videos"
    assert got[(str(recits), PLAYLIST)]["kind"] == "playlist"
    assert got[(str(recits), PLAYLIST)]["mode"] == "off"
    assert got[(str(nested), NESTED)]["mode"] == "off"
    assert got[(str(talks), TALKS_ID)]["mode"] == "new_only"  # untouched
    assert not any(k[0] == str(unresolved) for k in got)
    assert {r["name"] for r in rep["created"]} == {"Lessons", "Recitations", "Nested"}
    assert [r["name"] for r in rep["existing"]] == ["Talks"]
    assert {(r["name"], r["reason"]) for r in rep["skipped"]} == {
        ("A search", "search"), ("Another site", "not_youtube"), ("Unresolved", "unresolved")
    }  # fmt: skip
    assert {r["name"] for r in rep["not_in_maktaba"]} == {"Not here yet", "Follows"}
    # a library is never made for it
    assert await db.fetchval("SELECT count(*) FROM libraries") == 5
    assert "https://www.youtube.com/@elsewhere" not in asked  # no library: never resolved

    # a library that follows a channel already: a handle in its folder is that channel,
    # with no network asked (and it is left as it is)
    asked.clear()
    follows = await _library(db, base / "Follows")
    await db.execute(
        "INSERT INTO youtube_sources (id, library_id, kind, url, external_id, mode, added_at)"
        " VALUES ($1, $2, 'channel', 'https://www.youtube.com/channel/y', $3, 'off', $4)",
        "5a000000-0000-4000-8000-0000000000bb", str(follows), "UC" + "f" * 22, to_ts(db, now()),
    )  # fmt: skip

    # again: nothing new, nothing changed
    await db.execute("UPDATE youtube_sources SET mode = 'new_and_backfill' WHERE library_id = $1",
                     str(recits))  # fmt: skip
    again = await run_stage(
        db, rt, "youtube_tartube", "catalog", CATALOG, force=True,
        payload={"tartube": {"path": str(export)}},
    )  # fmt: skip
    rep2 = summary_of(again)
    assert rep2["counts"]["created"] == 0 and rep2["counts"]["existing"] == 5
    assert "https://www.youtube.com/@follows/videos" not in asked
    assert [r for r in rep2["existing"] if r["name"] == "Follows"][0]["external_id"] is None
    assert (await _sources(db))[(str(recits), PLAYLIST)]["mode"] == "new_and_backfill"


@pytest.mark.asyncio
async def test_what_tartube_downloaded_is_seeded_done(db: Database, tmp_path: Path) -> None:
    base = tmp_path / "Youtube"
    lessons = await _library(db, base / "Lessons")
    recits = await _library(db, base / "Recitations")
    rep = await tartube_sources.import_sources(
        db, str(_mio(tmp_path / "mio.json")), resolve=_fixed(HANDLE_ID)
    )
    assert rep["counts"]["created"] == 2 and rep["counts"]["seeded"] == 2
    done = {
        (str(r["library_id"]), str(r["youtube_id"]))
        for r in await db.fetch("SELECT library_id, youtube_id FROM youtube_downloads"
                                " WHERE state = 'done' AND reason = 'tartube'")
    }  # fmt: skip
    assert done == {(str(lessons), V1), (str(recits), V2)}  # V3 was never downloaded


def _fixed(cid: str) -> Any:
    async def resolve(url: str) -> tuple[str, str | None] | None:
        return cid, None

    return resolve


@pytest.mark.asyncio
async def test_a_bad_path_fails_without_retry(db: Database, tmp_path: Path) -> None:
    rt = await setup_runtime(db, [])
    job = await run_stage(
        db, rt, "youtube_tartube", "catalog", CATALOG,
        payload={"tartube": {"path": str(tmp_path / "missing.json")}},
    )  # fmt: skip
    assert job["state"] == "failed" and error_of(job)["kind"] == "tartube_missing"
