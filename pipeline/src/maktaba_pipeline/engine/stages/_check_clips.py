"""What ``check_clips`` shares with the pipeline (mediaintel's import): the library's clips and
their questions, a verdict applied, and the stage's input fingerprint. The stage itself is
``tv.maktaba.classify``'s (Epic 54 wave 4, story 54.31).

``check_clips``: a language model checks what each short video is about (mediaintel
``check-clips``).

Subject: a library. Runs after ``tag``. The keyword tagger reads a clip's title word by word,
so a fatwa that names a prophet («هل يجوز الدعاء بحق الأنبياء») was a prophets' story and
padded that channel. For every clip that airs as filler (in no season, up to
``clip_max_minutes``, with a topic tag of at least 0.5): the model's verdict on its title
(cached per input in ``clip_checks``: library, title, candidate topics, model, prompt version)
corrects ``video_topic_tags`` (:func:`maktaba_pipeline.classify.tag_check.apply_clip`): a topic
it does not keep is damped, so topic channels and clip groups no longer take the clip by it.

One job asks for a whole library, ``clip_batch`` titles per question to the model (batching is
what makes it fast: a question takes seconds, a library has thousands of clips). Every batch is
saved when it is answered, so a job that stops (the model server went away, a cancel) loses
nothing and the retry carries on with the clips not yet answered. A clip whose tags already
carry its verdict is not asked again, and a new ``tag`` run (which restores the keyword
tags) gets the cached verdict applied again without asking.

Settings: ``rules.tagcheck`` (``enabled``, ``clips``, ``clip_batch``, ``clip_max_minutes`` ...).
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any
from uuid import UUID

from ...classify import tag_check as tc
from ...rules import read as read_rules
from ...settings import to_json
from ..graph import Subject
from ..hashing import canonical_hash
from ..sql import uid
from ._tagging import latest_output, read_video_tags, write_video_tags

__all__ = [
    "ClipPlan",
    "apply_verdict",
    "clips_on",
    "input_fingerprint",
    "load_clips",
    "plan_clips",
    "video_titles",
]


_CLIPS_SQL = """
SELECT v.id AS vid, COALESCE(yv.title, v.title, v.filename) AS title
  FROM videos v
  LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'
  LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id
 WHERE v.library_id = $1 AND v.deleted_at IS NULL
   AND COALESCE(v.duration_sec, yv.duration_s) <= $2
   AND NOT EXISTS (SELECT 1 FROM series_episodes e WHERE e.video_id = v.id)
   AND EXISTS (SELECT 1 FROM video_topic_tags t WHERE t.video_id = v.id
               AND t.tag LIKE 'topic:%' AND t.score >= 0.5)
 ORDER BY v.path
"""


@dataclass(frozen=True, slots=True)
class Clip:
    video_id: str
    title: str
    tags: dict[str, float]

    @property
    def candidates(self) -> tuple[str, ...]:
        return tc.clip_candidates(self.tags)


@dataclass(slots=True)
class ClipPlan:
    """What a run has to do about the library's clips."""

    clips: int = 0
    #: answered before, tags already carry the verdict
    carried: int = 0
    #: answered before, the verdict is applied again to the restored keyword tags:
    #: (video id, topics kept, hash of the tags it was last applied to)
    to_apply: list[tuple[str, tuple[str, ...], str | None]] = field(default_factory=list)
    #: not answered yet: (clip, its input hash)
    todo: list[tuple[Clip, str]] = field(default_factory=list)


async def load_clips(db: Any, library_id: UUID, cfg: tc.TagCheckConfig) -> list[Clip]:
    """The library's clips worth asking about, with their stored tags."""
    rows = await db.fetch(_CLIPS_SQL, uid(library_id), cfg.clip_max_minutes * 60)
    tags: dict[str, dict[str, float]] = {}
    for r in await db.fetch(
        "SELECT t.video_id AS v, t.tag AS tag, t.score AS score FROM video_topic_tags t"
        " JOIN videos v ON v.id = t.video_id WHERE v.library_id = $1",
        uid(library_id),
    ):
        tags.setdefault(str(r["v"]), {})[str(r["tag"])] = round(float(r["score"]), 4)
    return [Clip(str(r["vid"]), str(r["title"] or ""), tags.get(str(r["vid"]), {})) for r in rows]


async def plan_clips(db: Any, library_id: UUID, lib: str, cfg: tc.TagCheckConfig) -> ClipPlan:
    """Sort the library's clips: answered (their tags carry the verdict, or the cached
    verdict is applied again) or still to ask. The same three cases as mediaintel's step."""
    clips = await load_clips(db, library_id, cfg)
    done = {
        str(r["video_id"]): r
        for r in await db.fetch(
            "SELECT c.video_id, c.input_hash, c.topics, c.applied_hash FROM clip_checks c"
            " JOIN videos v ON v.id = c.video_id WHERE v.library_id = $1",
            uid(library_id),
        )
    }
    plan = ClipPlan(clips=len(clips))
    for clip in clips:
        h = tc.clip_input_hash(lib, clip.title, clip.candidates, cfg)
        row = done.get(clip.video_id)
        if row is not None and row["applied_hash"] == tc.clip_tags_hash(clip.tags):
            plan.carried += 1  # its tags carry the verdict: the topics it dropped are low now
        elif row is not None and row["input_hash"] == h:
            plan.to_apply.append(
                (clip.video_id, tuple(json.loads(row["topics"])), row["applied_hash"])
            )
        else:
            plan.todo.append((clip, h))
    return plan


async def video_titles(db: Any, library_id: UUID) -> dict[str, str]:
    """Title of every video of the library, as the clip questions read it."""
    return {
        str(r["vid"]): str(r["title"] or "")
        for r in await db.fetch(
            "SELECT v.id AS vid, COALESCE(yv.title, v.title, v.filename) AS title FROM videos v"
            " LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'"
            " LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id"
            " WHERE v.library_id = $1 AND v.deleted_at IS NULL",
            uid(library_id),
        )
    }


async def apply_verdict(
    db: Any, video_id: str, keep: tuple[str, ...], applied_hash: str | None, cfg: tc.TagCheckConfig
) -> bool:
    """Apply the topics the model kept to the clip's stored tags, unless they already carry
    the verdict. True: the tags changed."""
    tags = await read_video_tags(db, video_id)
    if not tags or applied_hash == tc.clip_tags_hash(tags):
        return False
    new = tc.apply_clip(tags, keep, cfg)
    await db.execute(
        "UPDATE clip_checks SET applied_hash = $2 WHERE video_id = $1",
        uid(video_id),
        tc.clip_tags_hash(new),
    )
    if new == tags:
        return False  # the model kept every topic: nothing to correct
    await write_video_tags(db, video_id, new)
    return True


def clips_on(cfg: tc.TagCheckConfig) -> bool:
    return cfg.enabled and cfg.clips


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: the library's short videos and seasons, the tagcheck settings and the newest
    ``tag`` output of the library (a new tag run asks for the verdicts to be applied again)."""
    cfg: tc.TagCheckConfig = (await read_rules(db, "tagcheck"))[0]
    if not clips_on(cfg):
        return canonical_hash({"off": True, "v": tc.CLIP_CHECK_VERSION})
    lib = uid(subject.id)
    seasons = await db.fetch(
        "SELECT id, membership_hash FROM series_seasons WHERE library_id = $1"
        " AND retired_at IS NULL ORDER BY id",
        lib,
    )
    vids = await db.fetch(
        "SELECT v.id AS id, COALESCE(yv.title, v.title, v.filename) AS title FROM videos v"
        " LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'"
        " LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id"
        " WHERE v.library_id = $1 AND v.deleted_at IS NULL"
        " AND COALESCE(v.duration_sec, yv.duration_s) <= $2 ORDER BY v.id",
        lib,
        cfg.clip_max_minutes * 60,
    )
    return canonical_hash(
        {
            "seasons": [[str(r["id"]), str(r["membership_hash"])] for r in seasons],
            "videos": [[str(r["id"]), str(r["title"])] for r in vids],
            "rules": to_json(cfg),
            "tag": await latest_output(db, "library", subject.id, "tag"),
            "v": tc.CLIP_CHECK_VERSION,
        }
    )
