"""``clip_groups``: shorts, clips and stand-alone videos into playable collections.

Every leftover of a library (in no season, not a second upload, not a linked promo) goes
into exactly one group, by :func:`~maktaba_pipeline.classify.clip_groups.plan_groups`:

* **clips** (filler: Shorts, clip playlists, anything under 9 min) and **standalone**
  (programme material: a one-off interview or lecture), kept apart;
* within each: the most specific real playlist holding at least 5 of them, else
  "فتاوى ومسائل" (questions and fatwas, whatever their topic), else the topic tag
  (at least 10), else one "متنوعة" group;
* the library's linked promos (``promo_links``) form one more group, "إعلانات".

A topic collection's own playlist is not used again (one playlist, one collection).
Output: ``clip_groups`` + ``clip_group_items`` (slot 0094), replaced per library. The
Plex extension writes each as a collection; the channel planner pads slots with them.
"""

from __future__ import annotations

from typing import Any
from uuid import UUID

from ...classify.clip_groups import Group, group_title, plan_groups
from ...db.jobs import Job
from ..context import StageContext
from ..graph import CLIP_GROUPS_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..ids import clip_group_uuid
from ..sql import now, to_ts, uid
from ._leftovers import library_name, load_leftovers
from .registry import StageImpl

__all__ = ["IMPL", "plan_library_groups"]


async def plan_library_groups(db: Any, library_id: UUID) -> list[Group]:
    topic_pls = {
        str(r["playlist_id"])
        for r in await db.fetch(
            "SELECT playlist_id FROM topic_collections WHERE library_id = $1", uid(library_id)
        )
    }
    promos = {
        str(r["video_id"])
        for r in await db.fetch(
            "SELECT video_id FROM promo_links WHERE library_id = $1", uid(library_id)
        )
    }
    left = await load_leftovers(db, library_id, skip_playlists=topic_pls)
    linked = [left.items[v] for v in promos if v in left.items]
    items = [it for v, it in left.items.items() if v not in promos]
    groups = plan_groups(items, left.playlist_titles)
    if linked:  # linked promos air next to their show, and are one collection per library
        linked.sort(key=lambda i: (i.upload or "9999", i.path))
        groups.append(Group("clips", "promo", "إعلانات", None, [i.path for i in linked]))
    return groups


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: seasons, videos, topic tags, topic playlists and promo links of the library."""
    lib = uid(subject.id)
    seasons = await db.fetch(
        "SELECT id, membership_hash FROM series_seasons WHERE library_id = $1"
        " AND retired_at IS NULL ORDER BY id",
        lib,
    )
    vids = await db.fetch(
        "SELECT id, size_bytes, mtime FROM videos WHERE library_id = $1 AND deleted_at IS NULL"
        " ORDER BY id",
        lib,
    )
    tags = await db.fetch(
        "SELECT t.video_id AS v, t.tag AS t, t.score AS s FROM video_topic_tags t"
        " JOIN videos v ON v.id = t.video_id WHERE v.library_id = $1 ORDER BY 1, 2",
        lib,
    )
    topics = await db.fetch(
        "SELECT playlist_id FROM topic_collections WHERE library_id = $1 ORDER BY 1", lib
    )
    promos = await db.fetch(
        "SELECT video_id FROM promo_links WHERE library_id = $1 ORDER BY 1", lib
    )
    return canonical_hash(
        {
            "seasons": [[str(r["id"]), str(r["membership_hash"])] for r in seasons],
            "videos": [[str(r["id"]), int(r["size_bytes"]), str(r["mtime"])] for r in vids],
            "tags": [[str(r["v"]), str(r["t"]), round(float(r["s"]), 3)] for r in tags],
            "topics": [str(r["playlist_id"]) for r in topics],
            "promos": [str(r["video_id"]) for r in promos],
            "v": CLIP_GROUPS_VERSION,
        }
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id: UUID = job.subject.id
    name = await library_name(db, library_id)
    if name is None:
        return StageResult(output_hash=canonical_hash({"library": None}), summary={"gone": True})
    groups = await plan_library_groups(db, library_id)
    stamp = to_ts(db, now())
    rows = []
    async with db.transaction():
        await db.execute("DELETE FROM clip_groups WHERE library_id = $1", uid(library_id))
        for g in groups:
            gid = clip_group_uuid(library_id, g.kind, g.basis, g.name)
            rows.append([str(gid), g.name, g.items])
            await db.execute(
                "INSERT INTO clip_groups (id, library_id, kind, basis, name, topic, title,"
                " membership_hash, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)",
                uid(gid),
                uid(library_id),
                g.kind,
                g.basis,
                g.name,
                g.topic,
                group_title(name, g.kind, g.basis, g.name),
                canonical_hash([g.name, g.items]),
                stamp,
            )
            for pos, vid in enumerate(g.items, 1):
                await db.execute(
                    "INSERT INTO clip_group_items (group_id, video_id, position) VALUES ($1,$2,$3)",
                    uid(gid),
                    vid,
                    pos,
                )
    return StageResult(
        output_hash=canonical_hash(sorted(rows)),
        summary={
            "groups": len(groups),
            "clips": sum(len(g.items) for g in groups if g.kind == "clips"),
            "standalone": sum(len(g.items) for g in groups if g.kind == "standalone"),
            "promos": sum(len(g.items) for g in groups if g.basis == "promo"),
            "qa": sum(len(g.items) for g in groups if g.topic == "qa_fatwa"),
        },
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
