"""Slot 0182: every library's YouTube sources from its linked videos.

Every fixture is invented here (library names, channels, playlists, ids).
"""

from __future__ import annotations

import uuid
from datetime import timedelta
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.engine.reconcile import _youtube_derive
from maktaba_pipeline.engine.sql import dumps, loads, now, to_ts
from maktaba_pipeline.library_mgmt import youtube_derive as yd
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.tools.youtube_derive import render
from tests.engine.factory import make_library, make_video

SHARED = "UC" + "s" * 22  # one channel, a playlist per library
SOLO = "UC" + "o" * 22  # a channel no other library has
PL_B, PL_C = "PLinventedB0001", "PLinventedC0001"


async def _yt_video(
    db: Any, yid: str, channel: str | None, playlist: tuple[str, bool] | None
) -> None:
    await db.execute(
        "INSERT INTO youtube_videos (youtube_id, channel_id, title, source, fetched_at)"
        " VALUES ($1, $2, $3, 'info_json', $4)",
        yid, channel, f"title {yid}", to_ts(db, now()),
    )  # fmt: skip
    if playlist is not None:
        pid, real = playlist
        await db.execute(
            "INSERT INTO youtube_playlists (playlist_id, channel_id, title, is_real, source,"
            " fetched_at) VALUES ($1, $2, $3, $4, 'info_json', $5)"
            " ON CONFLICT (playlist_id) DO NOTHING",
            pid, channel, f"list {pid}", real, to_ts(db, now()),
        )  # fmt: skip
        await db.execute(
            "INSERT INTO youtube_playlist_items (playlist_id, youtube_id) VALUES ($1, $2)",
            pid,
            yid,
        )


async def _linked(
    db: Any,
    lib: UUID,
    n: int,
    channel: str | None,
    playlist: tuple[str, bool] | None = None,
    prefix: str = "",
) -> list[str]:
    """n videos of library lib, each linked to a YouTube video of channel (in playlist)."""
    ids = []
    for _ in range(n):
        yid = (prefix + uuid.uuid4().hex)[:11]
        await _yt_video(db, yid, channel, playlist)
        v = await make_video(db, lib)
        await db.execute(
            "INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, mapped,"
            " confidence, is_accepted, fetched_at) VALUES ($1, $2, 'youtube', $3, $4, 1.0, $5, $6)",
            str(uuid.uuid4()), str(v), yid, dumps({"link_method": "info_json"}), True,
            to_ts(db, now()),
        )  # fmt: skip
        ids.append(yid)
    return ids


async def _source(db: Any, lib: UUID, kind: str, ext: str, mode: str = "off") -> None:
    await db.execute(
        "INSERT INTO youtube_sources (id, library_id, kind, url, external_id, mode, added_at)"
        " VALUES ($1, $2, $3, $4, $5, $6, $7)",
        str(uuid.uuid4()), str(lib), kind, f"https://www.youtube.com/x/{ext}", ext, mode,
        to_ts(db, now()),
    )  # fmt: skip


async def _sources(db: Any) -> dict[tuple[str, str], dict[str, Any]]:
    rows = await db.fetch(
        "SELECT library_id, kind, external_id, mode, derived_at FROM youtube_sources"
    )
    return {(str(r["library_id"]), str(r["external_id"])): dict(r) for r in rows}


async def _shared_channel(db: Any) -> tuple[UUID, UUID, UUID]:
    """A holds the shared channel (youtube_channels and a channel source, off); B and C
    have one playlist each of it; A's own videos are on the channel's uploads tab."""
    a, b, c = [await make_library(db, n) for n in ("Holder", "Reciter B", "Reciter C")]
    await db.execute(
        "INSERT INTO youtube_channels (channel_id, library_id, url, fetched_at)"
        " VALUES ($1, $2, 'https://www.youtube.com/channel/x', $3)",
        SHARED, str(a), to_ts(db, now()),
    )  # fmt: skip
    await _source(db, a, "channel", SHARED)
    await _linked(db, a, 4, SHARED, ("UU" + "s" * 22, False))
    await _linked(db, b, 5, SHARED, (PL_B, True))
    await _linked(db, c, 3, SHARED, (PL_C, True))
    return a, b, c


@pytest.mark.asyncio
async def test_a_shared_channel_gives_each_library_its_playlist(db: Database) -> None:
    a, b, c = await _shared_channel(db)
    got = {d.library_id: d for d in await yd.derive(db)}
    assert (got[str(b)].outcome, got[str(b)].external_ids) == ("playlist", [PL_B])
    assert (got[str(c)].outcome, got[str(c)].external_ids) == ("playlist", [PL_C])
    # the holder keeps its channel source, and nothing is made for it
    assert (got[str(a)].outcome, got[str(a)].created) == ("channel", [])
    src = await _sources(db)
    assert src[(str(b), PL_B)]["mode"] == "off" and src[(str(b), PL_B)]["derived_at"] is not None
    assert src[(str(c), PL_C)]["kind"] == "playlist"
    assert src[(str(a), SHARED)]["derived_at"] is None
    assert len(src) == 3
    # the channel's libraries, many to many
    rows = await db.fetch(
        "SELECT library_id, linked_videos FROM youtube_channel_libraries WHERE channel_id = $1",
        SHARED,
    )
    assert {str(r["library_id"]): r["linked_videos"] for r in rows} == {
        str(a): 4,
        str(b): 5,
        str(c): 3,
    }
    # the decision table says why
    row = await db.fetchrow(
        "SELECT outcome, reason, created, seen FROM youtube_source_decisions WHERE library_id = $1",
        str(b),
    )
    assert row["outcome"] == "playlist" and "5 of 5" in row["reason"]
    assert loads(row["created"]) == [PL_B]
    assert set(loads(row["seen"])) == {SHARED, PL_B}


@pytest.mark.asyncio
async def test_a_channel_only_library_gets_its_channel(db: Database) -> None:
    d = await make_library(db, "Solo")
    await _linked(db, d, 3, SOLO, ("UU" + "o" * 22, False))  # only the uploads tab
    [dec] = await yd.derive(db)
    assert (dec.outcome, dec.kind, dec.created) == ("channel", "channel", [SOLO])
    src = await _sources(db)
    assert src[(str(d), SOLO)]["mode"] == "off"
    # the channel is known now, and it is this library's to list
    holder = await db.fetchval(
        "SELECT library_id FROM youtube_channels WHERE channel_id = $1", SOLO
    )
    assert str(holder) == str(d)


@pytest.mark.asyncio
async def test_a_clash_goes_to_the_hint_not_a_second_source(db: Database) -> None:
    a, _, _ = await _shared_channel(db)
    dup = await make_library(db, "Holder again")
    await _linked(db, dup, 1, SHARED)  # one video of the channel A follows, in no playlist
    # two libraries with no source and the same channel: the one with more of it wins
    big, small = await make_library(db, "Big"), await make_library(db, "Small")
    await _linked(db, big, 3, SOLO)
    await _linked(db, small, 2, SOLO)
    got = {d.library_id: d for d in await yd.derive(db)}
    assert got[str(dup)].outcome == "clash"
    assert (got[str(dup)].clash_external_id, got[str(dup)].clash_library_id) == (SHARED, str(a))
    assert got[str(big)].created == [SOLO]
    assert (got[str(small)].outcome, got[str(small)].clash_library_id) == ("clash", str(big))
    src = await _sources(db)
    assert [k for k in src if k[1] == SHARED] == [(str(a), SHARED)]
    assert [k for k in src if k[1] == SOLO] == [(str(big), SOLO)]
    row = await db.fetchrow(
        "SELECT outcome, clash_library_id FROM youtube_source_decisions WHERE library_id = $1",
        str(dup),
    )
    assert row["outcome"] == "clash" and str(row["clash_library_id"]) == str(a)


@pytest.mark.asyncio
async def test_a_playlist_another_library_follows_is_a_clash(db: Database) -> None:
    _, b, _ = await _shared_channel(db)
    other = await make_library(db, "Follows B's playlist")
    await _source(db, other, "playlist", PL_B, mode="new_only")
    [dec] = await yd.derive(db, [b])
    assert (dec.outcome, dec.clash_external_id, dec.clash_library_id) == ("clash", PL_B, str(other))
    assert (str(b), PL_B) not in await _sources(db)


@pytest.mark.asyncio
async def test_it_is_idempotent_and_never_changes_a_mode(db: Database) -> None:
    a, b, c = await _shared_channel(db)
    await _source(db, b, "playlist", PL_B, mode="new_only")
    await db.execute("UPDATE youtube_sources SET mode = 'new_and_backfill' WHERE library_id = $1",
                     str(a))  # fmt: skip
    first = {d.library_id: d for d in await yd.derive(db)}
    assert first[str(b)].created == [] and first[str(c)].created == [PL_C]
    before = await _sources(db)
    second = await yd.derive(db)
    assert all(d.created == [] for d in second)
    assert [(d.library_id, d.outcome, d.external_ids) for d in second] == sorted(
        (d.library_id, d.outcome, d.external_ids) for d in first.values()
    )
    after = await _sources(db)
    assert after == before
    assert (
        after[(str(b), PL_B)]["mode"] == "new_only" and after[(str(b), PL_B)]["derived_at"] is None
    )
    assert after[(str(a), SHARED)]["mode"] == "new_and_backfill"
    assert await db.fetchval("SELECT count(*) FROM youtube_source_decisions") == 3


@pytest.mark.asyncio
async def test_a_dry_run_writes_nothing(db: Database) -> None:
    _, b, _ = await _shared_channel(db)
    got = {d.library_id: d for d in await yd.derive(db, apply=False)}
    assert got[str(b)].external_ids == [PL_B] and got[str(b)].created == []
    assert len(await _sources(db)) == 1
    assert await db.fetchval("SELECT count(*) FROM youtube_source_decisions") == 0
    text = render([d.row() for d in got.values()], {str(b): "Reciter B"}, applied=False)
    assert "DRY RUN" in text and f"Reciter B: playlist playlist {PL_B}" in text


@pytest.mark.asyncio
async def test_after_link_decides_only_for_what_it_has_not_seen(db: Database) -> None:
    _, b, _ = await _shared_channel(db)
    [yid, *_] = await _linked(db, b, 1, SHARED, (PL_B, True))
    dec = await yd.after_link(db, b, yid)
    assert dec is not None and dec.created == [PL_B]
    assert await yd.after_link(db, b, yid) is None  # seen: nothing decided again
    # a link to a playlist the decision has not seen decides again
    [other] = await _linked(db, b, 1, SHARED, ("PLinventedB0002", True))
    again = await yd.after_link(db, b, other)
    assert again is not None and again.outcome == "playlist"


@pytest.mark.asyncio
async def test_the_reconciler_derives_once_then_waits(db: Database) -> None:
    _, b, _ = await _shared_channel(db)
    t = now()
    assert await _youtube_derive(db, t) == 2  # B and C were given a source
    assert await _youtube_derive(db, t + timedelta(hours=1)) == 0  # decided an hour ago
    assert (str(b), PL_B) in await _sources(db)
    # a library made after: the pass six hours on decides it too
    late = await make_library(db, "Late")
    await _linked(db, late, 2, SOLO)
    assert await _youtube_derive(db, t + timedelta(hours=7)) == 1
