"""The ``youtube_sync`` stage: discovery, listing, change detection, failures."""

from __future__ import annotations

import json
import uuid
from collections.abc import Iterator
from pathlib import Path
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.engine.sql import dumps, is_pg
from maktaba_pipeline.engine.stages.youtube_sync import discover_folders, set_fetcher_factory
from maktaba_pipeline.integrations.youtube import (
    FetchError,
    classify_error,
    list_channel,
)
from tests.engine.factory import make_library, make_video
from tests.extensions.harness import error_of, events, run_stage, setup_runtime, summary_of

CHANNEL = "UCchan0000000000000000"
CHANNEL_URL = f"https://www.youtube.com/channel/{CHANNEL}"
A, B = "AAAAAAAAAAA", "BBBBBBBBBBB"
PLAYLIST_URL = "https://www.youtube.com/playlist?list=PLlist00001"


class FakeFetcher:
    def __init__(self) -> None:
        self.pages: dict[str, dict[str, Any] | None] = {}
        self.approx: dict[str, dict[str, Any] | None] = {}
        self.errors: dict[str, str] = {}
        self.calls: list[tuple[str, str, bool]] = []

    def extract(
        self, url: str, *, lang: str, flat: bool = True, approximate: bool = False
    ) -> dict[str, Any] | None:
        self.calls.append((url, lang, approximate))
        if url in self.errors:
            raise FetchError(self.errors[url])
        return (self.approx if approximate else self.pages).get(url)


def entry(vid: str, title: str, duration: int = 600) -> dict[str, Any]:
    return {"id": vid, "title": title, "duration": duration}


def make_fetcher() -> FakeFetcher:
    f = FakeFetcher()
    f.pages[CHANNEL_URL + "/videos"] = {
        "channel": "Chan",
        "entries": [entry(A, "Video A"), entry(B, "Video B")],
    }
    f.approx[CHANNEL_URL + "/videos"] = {"entries": [{"id": A, "timestamp": 1_700_000_000}]}
    f.pages[CHANNEL_URL + "/playlists"] = {"entries": [{"id": "PLlist00001", "title": "Season 1"}]}
    f.pages[PLAYLIST_URL] = {
        "title": "Season 1",
        "entries": [entry(B, "Video B"), entry(A, "Video A")],
    }
    return f


@pytest.fixture
def fetcher() -> Iterator[FakeFetcher]:
    f = make_fetcher()
    previous = set_fetcher_factory(lambda: f)
    yield f
    set_fetcher_factory(previous)


async def make_library_with_folder(db: Any, tmp_path: Path) -> UUID:
    folder = tmp_path / "Chan"
    folder.mkdir()
    (folder / "a.mp4").write_bytes(b"x")
    (folder / "a.info.json").write_text(json.dumps({"id": A, "title": "A"}))  # per-video: ignored
    tab = {
        "_type": "playlist",
        "id": CHANNEL,
        "title": "Chan - Videos",
        "channel": "Chan",
        "channel_id": CHANNEL,
        "channel_url": CHANNEL_URL,
        "entries": [{"id": A}],
    }
    (folder / "Chan - Videos.info.json").write_text(json.dumps(tab))
    real = {
        "_type": "playlist",
        "id": "PLreal00001",
        "title": "Real list",
        "channel_id": CHANNEL,
        "playlist_count": 2,
        "entries": [{"id": A}, {"id": B}],
    }
    (folder / "Real list.info.json").write_text(json.dumps(real))
    lib = await make_library(db, "YT")
    roots = [str(tmp_path)]
    await db.execute(
        "UPDATE libraries SET roots = $2 WHERE id = $1",
        str(lib),
        roots if is_pg(db) else dumps(roots),
    )
    return lib


async def link_video(db: Any, lib: UUID, yt_id: str | None) -> UUID:
    v = await make_video(db, lib)
    if yt_id:
        await db.execute(
            "INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, mapped,"
            " confidence, is_accepted) VALUES ($1,$2,'youtube',$3,$4,1.0,$5)",
            str(uuid.uuid4()),
            str(v),
            yt_id,
            "{}",
            True,
        )
    return v


async def link_jobs(db: Any, job_id: int) -> set[str]:
    """Videos the finished job asked ``link`` to run for (from its ``triggered`` event)."""
    out: set[str] = set()
    for data in await events(db, job_id, "triggered"):
        out.update(t["subject_id"] for t in data["to"] if t["stage"] == "link")
    return out


async def sync_job(db: Any, rt: Any, lib: UUID, **kw: Any) -> Any:
    return await run_stage(db, rt, "youtube_sync", "library", lib, **kw)


async def channel_status(db: Any) -> str:
    return str(
        await db.fetchval("SELECT status FROM youtube_channels WHERE channel_id = $1", CHANNEL)
    )


# ------------------------------------------------------------------ pure pieces


def test_folder_discovery_reads_only_folder_level_sidecars(tmp_path: Path) -> None:
    folder = tmp_path / "x"
    folder.mkdir()
    (folder / "a.mp4").write_bytes(b"x")
    (folder / "a.info.json").write_text(json.dumps({"id": A, "title": "A", "channel_id": "UCno"}))
    (folder / "tab.info.json").write_text(
        json.dumps({"_type": "playlist", "id": CHANNEL, "channel_id": CHANNEL, "entries": []})
    )
    found = discover_folders([str(tmp_path)])
    assert list(found.channels) == [CHANNEL]  # not UCno: that sidecar belongs to a video
    assert found.playlists == []  # a channel tab is not a real playlist


def test_classify_and_list_channel_with_a_fake_fetcher() -> None:
    assert classify_error("Sign in to confirm you're not a bot") == "bot_check"
    assert classify_error("This channel does not exist") == "gone"
    assert classify_error("boom") == "error"
    listing = list_channel(make_fetcher(), CHANNEL, CHANNEL_URL, lang="ar")
    assert listing.title == "Chan"
    assert {v.youtube_id for v in listing.videos} == {A, B}
    dated = {v.youtube_id: v.upload_date for v in listing.videos}
    assert dated[A] is not None and dated[B] is None
    assert listing.playlists[0].items == (B, A) and listing.playlists[0].is_real


# ------------------------------------------------------------------ the stage


@pytest.mark.asyncio
async def test_lists_channels_and_stores_rows(db, tmp_path, fetcher) -> None:
    lib = await make_library_with_folder(db, tmp_path)
    rt = await setup_runtime(db, [])
    job = await sync_job(db, rt, lib)
    assert job["state"] == "done" and job["outcome"] == "changed", error_of(job)

    ch = await db.fetchrow("SELECT * FROM youtube_channels WHERE channel_id = $1", CHANNEL)
    assert ch["status"] == "ok" and ch["title"] == "Chan" and str(ch["library_id"]) == str(lib)
    vids = {r["youtube_id"]: r for r in await db.fetch("SELECT * FROM youtube_videos")}
    assert set(vids) == {A, B}
    assert vids[A]["date_quality"] == "approximate" and vids[B]["date_quality"] is None
    assert vids[A]["source"] == "youtube" and vids[A]["tab"] == "videos"
    pls = {r["playlist_id"]: r for r in await db.fetch("SELECT * FROM youtube_playlists")}
    assert set(pls) == {"PLlist00001", "PLreal00001"}
    assert pls["PLreal00001"]["is_real"] in (True, 1)  # from the sidecar
    items = await db.fetch(
        "SELECT youtube_id, position FROM youtube_playlist_items WHERE playlist_id = $1"
        " ORDER BY position",
        "PLlist00001",
    )
    assert [(r["youtube_id"], r["position"]) for r in items] == [(B, 1), (A, 2)]
    summary = summary_of(job)
    assert summary["ok"] == 1 and summary["videos"] == 2


@pytest.mark.asyncio
async def test_identical_listing_is_unchanged_and_propagates_nothing(db, tmp_path, fetcher) -> None:
    lib = await make_library_with_folder(db, tmp_path)
    await link_video(db, lib, A)
    rt = await setup_runtime(db, [])
    first = await sync_job(db, rt, lib)
    second = await sync_job(db, rt, lib)  # always runs: the listing is external
    assert second["outcome"] == "unchanged"
    assert second["output_hash"] == first["output_hash"]
    assert await link_jobs(db, second["id"]) == set()


@pytest.mark.asyncio
async def test_changed_rows_name_the_linked_videos(db, tmp_path, fetcher) -> None:
    lib = await make_library_with_folder(db, tmp_path)
    va = await link_video(db, lib, A)
    vb = await link_video(db, lib, B)
    rt = await setup_runtime(db, [])
    first = await sync_job(db, rt, lib)
    assert await link_jobs(db, first["id"]) == {str(va), str(vb)}

    entries = fetcher.pages[CHANNEL_URL + "/videos"]["entries"]  # type: ignore[index]
    entries[0]["title"] = "Video A (renamed)"
    job = await sync_job(db, rt, lib)
    assert job["outcome"] == "changed"
    title = await db.fetchval("SELECT title FROM youtube_videos WHERE youtube_id = $1", A)
    assert title == "Video A (renamed)"
    assert str(va) in await link_jobs(db, job["id"])


@pytest.mark.asyncio
async def test_bot_check_is_recorded_and_fails_without_retry(db, tmp_path, fetcher) -> None:
    lib = await make_library_with_folder(db, tmp_path)
    fetcher.errors[CHANNEL_URL + "/videos"] = "ERROR: Sign in to confirm you're not a bot"
    rt = await setup_runtime(db, [])
    job = await sync_job(db, rt, lib)
    err = error_of(job)
    assert job["state"] == "failed"
    assert err["kind"] == "youtube_bot_check" and err["retryable"] is False
    assert await channel_status(db) == "bot_check"


@pytest.mark.asyncio
async def test_gone_channel_is_recorded_and_skipped_until_refresh(db, tmp_path, fetcher) -> None:
    lib = await make_library_with_folder(db, tmp_path)
    fetcher.errors[CHANNEL_URL + "/videos"] = "ERROR: This channel does not exist"
    rt = await setup_runtime(db, [])
    job = await sync_job(db, rt, lib)
    assert job["state"] == "done" and summary_of(job)["gone"] == 1
    assert await channel_status(db) == "gone"

    fetcher.calls.clear()
    again = await sync_job(db, rt, lib)
    assert summary_of(again)["skipped_gone"] == 1 and fetcher.calls == []

    del fetcher.errors[CHANNEL_URL + "/videos"]
    refreshed = await sync_job(db, rt, lib, payload={"refresh": True})
    assert summary_of(refreshed)["ok"] == 1
    assert await channel_status(db) == "ok"


@pytest.mark.asyncio
async def test_other_errors_are_recorded_as_error(db, tmp_path, fetcher) -> None:
    lib = await make_library_with_folder(db, tmp_path)
    fetcher.errors[CHANNEL_URL + "/videos"] = "ERROR: unexpected breakage"
    rt = await setup_runtime(db, [])
    job = await sync_job(db, rt, lib)
    assert job["state"] == "done" and summary_of(job)["error"] == 1
    row = await db.fetchrow(
        "SELECT status, error FROM youtube_channels WHERE channel_id = $1", CHANNEL
    )
    assert row["status"] == "error" and "breakage" in row["error"]


@pytest.mark.asyncio
async def test_missing_yt_dlp_fails_without_retry(db, tmp_path) -> None:
    def broken() -> Any:
        raise ImportError("no yt_dlp")

    previous = set_fetcher_factory(broken)
    try:
        lib = await make_library_with_folder(db, tmp_path)
        rt = await setup_runtime(db, [])
        job = await sync_job(db, rt, lib)
    finally:
        set_fetcher_factory(previous)
    assert error_of(job)["kind"] == "yt_dlp_missing" and error_of(job)["retryable"] is False
