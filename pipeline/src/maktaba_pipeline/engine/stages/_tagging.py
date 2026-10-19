"""Reading and rewriting the stored tags, for the stages that correct what ``tag`` made.

``check_tags``, ``books`` (a season's tags) and ``check_clips`` (a loose video's tags) take the
tags as stored, correct them with the library code of ``classify`` and write them back. The
mediaintel import uses the same functions to apply the answers it brings along.
"""

from __future__ import annotations

from typing import Any
from uuid import UUID

from ..hashing import canonical_hash
from ..sql import dumps, loads, uid

__all__ = [
    "latest_output",
    "read_season_tags",
    "read_video_tags",
    "season_tags_digest",
    "video_tags_digest",
    "write_season_tags",
    "write_video_tags",
]

SeasonTags = dict[str, tuple[float, str]]

_SUCCESS = "('changed','unchanged','skipped_unchanged','imported')"


async def latest_output(db: Any, kind: str, subject_id: UUID | str, stage: str) -> str | None:
    """The output hash of the subject's newest successful run of ``stage`` (None: never run).
    A stage that reads what another wrote puts this in its input fingerprint, so it runs
    again when the other stage's output moves."""
    row = await db.fetchrow(
        "SELECT output_hash FROM processing_jobs WHERE subject_kind = $1 AND subject_id = $2"
        f" AND stage = $3 AND state = 'done' AND outcome IN {_SUCCESS} ORDER BY id DESC LIMIT 1",
        kind,
        uid(subject_id),
        stage,
    )
    return None if row is None or row["output_hash"] is None else str(row["output_hash"])


async def read_season_tags(db: Any, season_id: UUID | str) -> SeasonTags:
    """``tag -> (score, evidence)`` as stored (the evidence is the ``why`` of its JSON). Scores
    are the tagger's, to three decimals; they are rounded to four so that they read back the
    same from a single-precision column and hash as they did in the prototype's cache."""
    out: SeasonTags = {}
    for r in await db.fetch(
        "SELECT tag, score, evidence FROM season_tags WHERE season_id = $1", uid(season_id)
    ):
        ev = loads(r["evidence"]) if r["evidence"] else None
        why = ev.get("why", "") if isinstance(ev, dict) else str(ev or "")
        out[str(r["tag"])] = (round(float(r["score"]), 4), str(why or ""))
    return out


async def write_season_tags(db: Any, season_id: UUID | str, tags: SeasonTags) -> None:
    async with db.transaction():
        await db.execute("DELETE FROM season_tags WHERE season_id = $1", uid(season_id))
        for tag, (score, why) in tags.items():
            await db.execute(
                "INSERT INTO season_tags (season_id, tag, score, evidence) VALUES ($1,$2,$3,$4)",
                uid(season_id),
                tag,
                score,
                dumps({"why": why}),
            )


async def read_video_tags(db: Any, video_id: UUID | str) -> dict[str, float]:
    return {
        str(r["tag"]): round(float(r["score"]), 4)
        for r in await db.fetch(
            "SELECT tag, score FROM video_topic_tags WHERE video_id = $1", uid(video_id)
        )
    }


async def write_video_tags(db: Any, video_id: UUID | str, tags: dict[str, float]) -> None:
    async with db.transaction():
        await db.execute("DELETE FROM video_topic_tags WHERE video_id = $1", uid(video_id))
        for tag, score in tags.items():
            await db.execute(
                "INSERT INTO video_topic_tags (video_id, tag, score) VALUES ($1,$2,$3)",
                uid(video_id),
                tag,
                score,
            )


async def season_tags_digest(db: Any, season_id: UUID | str) -> str:
    """Hash of a season's stored tags: the output of the stages that correct them."""
    tags = await read_season_tags(db, season_id)
    return canonical_hash(sorted([t, round(s, 3), why] for t, (s, why) in tags.items()))


async def video_tags_digest(db: Any, library_id: UUID | str) -> str:
    """Hash of the stored tags of a library's videos: what the group stages read."""
    rows = await db.fetch(
        "SELECT t.video_id AS v, t.tag AS t, t.score AS s FROM video_topic_tags t"
        " JOIN videos v ON v.id = t.video_id WHERE v.library_id = $1 ORDER BY 1, 2",
        uid(library_id),
    )
    return canonical_hash([[str(r["v"]), str(r["t"]), round(float(r["s"]), 3)] for r in rows])
