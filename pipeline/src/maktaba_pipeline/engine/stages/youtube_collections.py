"""``youtube_collections``: a collection from each YouTube playlist and channel (story 56.06).

Most of a library came from YouTube, whose creators already grouped it: a playlist is a
season when it matches one (``detect``) and a topic when it cuts across shows
(``topic_collections``); every other real playlist, and every channel of a library that
holds several, was lost to browsing. This stage, after ``topic_collections``, makes:

* a **playlist collection** (``source_kind`` ``youtube_playlist``, ``source_ref`` the
  playlist id) for every real playlist with at least :data:`MIN_PLAYLIST` of the library's
  videos, its members in the playlist's order, unless its members are exactly one season's
  (the season stands in) or it is a topic collection's playlist (the topic stands in);
* a **channel collection** (``youtube_channel``) for each channel with at least
  :data:`MIN_CHANNEL` of the library's videos, newest published first, when the library
  holds videos of two channels or more (one channel would be the whole library).

The collections are kept in step by
:func:`~maktaba_pipeline.engine.sourced_collections.sync_sourced`: an unchanged run
writes nothing, and an admin's name, hidden flag, rows and sort title stay. Off (the
``collections.youtube`` component), the stage makes no job and writes nothing.
"""

from __future__ import annotations

from collections import defaultdict
from typing import Any
from uuid import UUID

from ...db.jobs import Job
from ..context import StageContext
from ..graph import YOUTUBE_COLLECTIONS_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..sourced_collections import Desired, sync_sourced
from ..sql import uid
from .detect import _playlists
from .registry import StageImpl

__all__ = ["IMPL", "MIN_CHANNEL", "MIN_PLAYLIST", "plan_youtube_collections"]

#: a playlist needs this many of the library's videos to be a collection
MIN_PLAYLIST = 3
#: a channel needs this many of the library's videos
MIN_CHANNEL = 3
KINDS = ("youtube_playlist", "youtube_channel")


async def _season_sets(db: Any, library_id: UUID) -> set[frozenset[str]]:
    """Each live season's episodes, as a set (a playlist equal to one is that season)."""
    by: dict[tuple[str, int], set[str]] = defaultdict(set)
    for r in await db.fetch(
        "SELECT e.series_id AS sid, COALESCE(e.season_override, e.season) AS season,"
        " e.video_id AS vid FROM series_episodes e JOIN series s ON s.id = e.series_id"
        " WHERE s.library_id = $1",
        uid(library_id),
    ):
        by[(str(r["sid"]), int(r["season"] or 1))].add(str(r["vid"]))
    return {frozenset(v) for v in by.values() if v}


async def _channels(db: Any, library_id: UUID) -> dict[str, tuple[str, list[str]]]:
    """``channel id -> (title, video ids newest published first)`` of the linked videos."""
    rows = await db.fetch(
        "SELECT yv.channel_id AS cid, COALESCE(c.title, yv.channel_id) AS title, v.id AS vid"
        " FROM media_metadata_enrichment m JOIN videos v ON v.id = m.video_id"
        " JOIN youtube_videos yv ON yv.youtube_id = m.external_id"
        " LEFT JOIN youtube_channels c ON c.channel_id = yv.channel_id"
        " WHERE m.provider = 'youtube' AND v.library_id = $1 AND v.deleted_at IS NULL"
        " AND yv.channel_id IS NOT NULL"
        " ORDER BY yv.channel_id, COALESCE(v.published_at, v.mtime) DESC, v.id",
        uid(library_id),
    )
    out: dict[str, tuple[str, list[str]]] = {}
    for r in rows:
        title, vids = out.setdefault(str(r["cid"]), (str(r["title"]), []))
        if str(r["vid"]) not in vids:
            vids.append(str(r["vid"]))
    return out


async def plan_youtube_collections(db: Any, library_id: UUID) -> list[Desired]:
    """The library's playlist and channel collections, as their sources have them now."""
    seasons = await _season_sets(db, library_id)
    topics = {
        str(r["playlist_id"])
        for r in await db.fetch(
            "SELECT playlist_id FROM topic_collections WHERE library_id = $1"
            " AND playlist_id IS NOT NULL",
            uid(library_id),
        )
    }
    out: list[Desired] = []
    for pid, (title, members) in sorted((await _playlists(db, library_id)).items()):
        ordered = list(dict.fromkeys(v for v, _ in members))
        if len(ordered) < MIN_PLAYLIST or pid in topics or frozenset(ordered) in seasons:
            continue
        out.append(Desired("youtube_playlist", pid, title or pid, tuple(ordered)))
    channels = await _channels(db, library_id)
    if len(channels) >= 2:
        for cid, (title, vids) in sorted(channels.items()):
            if len(vids) >= MIN_CHANNEL:
                out.append(Desired("youtube_channel", cid, title, tuple(vids)))
    return out


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: what the plan reads, so an unchanged library runs nothing."""
    plan = await plan_youtube_collections(db, subject.id)
    return canonical_hash(
        {
            "plan": sorted([d.source_kind, d.source_ref, d.membership_hash()] for d in plan),
            "v": YOUTUBE_COLLECTIONS_VERSION,
        }
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id: UUID = job.subject.id
    plan = await plan_youtube_collections(db, library_id)
    stats = await sync_sourced(db, library_id, KINDS, plan)
    return StageResult(
        output_hash=canonical_hash(sorted([d.source_ref, d.membership_hash()] for d in plan)),
        outcome="changed" if stats.writes else "unchanged",
        summary={
            "playlists": sum(1 for d in plan if d.source_kind == "youtube_playlist"),
            "channels": sum(1 for d in plan if d.source_kind == "youtube_channel"),
            **stats.as_dict(),
            "names": [d.name for d in plan][:20],
        },
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
