"""``topic_collections``: curated playlists that cut across shows ("ملف"), per library.

A playlist such as "الشأن الفلسطيني" gathers episodes of several shows. It is its own
collection, and it may overlap seasons: an episode keeps its one season, and being in a
topic never counts as a placement anywhere else.

The pure rule is the detector's own :func:`~maktaba_pipeline.classify.series_detector.find_topics`
(the same code mediaintel's ``detect`` stores from ``Detection.topics``). This stage runs it
on the *stored* seasons, so a topic follows every detection without running the detector
again. Output: ``topic_collections`` + ``topic_items`` (slot 0094), replaced per library.
"""

from __future__ import annotations

from collections import defaultdict
from typing import Any
from uuid import UUID

from ...classify.series_detector import PlaylistFacts, Season, Series, find_topics
from ...db.jobs import Job
from ..context import StageContext
from ..graph import TOPIC_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..ids import topic_uuid
from ..sql import loads, now, to_ts, uid
from .detect import _playlists
from .registry import StageImpl

__all__ = ["IMPL", "plan_topics"]


async def _stored_series(db: Any, library_id: UUID) -> list[Series]:
    """The library's live seasons as detector ``Series`` (episodes in order)."""
    seasons = await db.fetch(
        "SELECT ss.id AS id, ss.series_id AS series_id, ss.season AS season, ss.kind AS kind,"
        " ss.name AS name, ss.metadata AS metadata FROM series_seasons ss"
        " WHERE ss.library_id = $1 AND ss.retired_at IS NULL ORDER BY ss.series_id, ss.season",
        uid(library_id),
    )
    eps: dict[tuple[str, int], list[str]] = defaultdict(list)
    for r in await db.fetch(
        "SELECT e.series_id AS series_id, COALESCE(e.season_override, e.season) AS season,"
        " e.video_id AS vid FROM series_episodes e JOIN series s ON s.id = e.series_id"
        " WHERE s.library_id = $1"
        " ORDER BY e.series_id, COALESCE(e.season_override, e.season),"
        " COALESCE(e.episode_override, e.episode), e.video_id",
        uid(library_id),
    ):
        eps[(str(r["series_id"]), int(r["season"] or 1))].append(str(r["vid"]))
    by_series: dict[str, Series] = {}
    for s in seasons:
        sid = str(s["series_id"])
        meta = loads(s["metadata"]) or {}
        ser = by_series.setdefault(
            sid, Series(str(s["name"]), sid, [], str(s["kind"]), bool(meta.get("brand")))
        )
        ser.seasons.append(
            Season(
                int(s["season"]),
                eps.get((sid, int(s["season"])), []),
                playlists=list(meta.get("playlists") or []),
            )
        )
    return list(by_series.values())


async def plan_topics(db: Any, library_id: UUID) -> list[tuple[UUID, str, str, list[str]]]:
    """``(id, playlist id, name, video ids in playlist order)`` of the library's topics."""
    series = await _stored_series(db, library_id)
    pls = [
        PlaylistFacts(pid, title, tuple(members))
        for pid, (title, members) in sorted((await _playlists(db, library_id)).items())
    ]
    known = {
        str(r["id"])
        for r in await db.fetch(
            "SELECT id FROM videos WHERE library_id = $1 AND deleted_at IS NULL", uid(library_id)
        )
    }
    return [
        (topic_uuid(library_id, t.playlist_id), t.playlist_id, t.name, list(t.items))
        for t in find_topics(series, pls, known)
    ]


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: the library's seasons (membership hashes) and its playlist memberships."""
    seasons = await db.fetch(
        "SELECT id, membership_hash FROM series_seasons WHERE library_id = $1"
        " AND retired_at IS NULL ORDER BY id",
        uid(subject.id),
    )
    pls = await _playlists(db, subject.id)
    return canonical_hash(
        {
            "seasons": [[str(r["id"]), str(r["membership_hash"])] for r in seasons],
            "playlists": sorted([pid, t, [m[0] for m in ms]] for pid, (t, ms) in pls.items()),
            "v": TOPIC_VERSION,
        }
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id: UUID = job.subject.id
    topics = await plan_topics(db, library_id)
    stamp = to_ts(db, now())
    async with db.transaction():
        await db.execute("DELETE FROM topic_collections WHERE library_id = $1", uid(library_id))
        for tid, pid, name, items in topics:
            await db.execute(
                "INSERT INTO topic_collections (id, library_id, playlist_id, name,"
                " membership_hash, updated_at) VALUES ($1,$2,$3,$4,$5,$6)",
                uid(tid),
                uid(library_id),
                pid,
                name,
                canonical_hash([name, items]),
                stamp,
            )
            for pos, vid in enumerate(dict.fromkeys(items), 1):
                await db.execute(
                    "INSERT INTO topic_items (topic_id, video_id, position) VALUES ($1,$2,$3)",
                    uid(tid),
                    vid,
                    pos,
                )
    return StageResult(
        output_hash=canonical_hash(sorted([str(t[0]), t[2], t[3]] for t in topics)),
        summary={
            "topics": len(topics),
            "videos": sum(len(t[3]) for t in topics),
            "names": [t[2] for t in topics][:20],
        },
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
