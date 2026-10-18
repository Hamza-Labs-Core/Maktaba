"""``check_clips``: a language model checks what each short video is about (mediaintel
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

import asyncio
import json
import urllib.error
from dataclasses import dataclass, field
from typing import Any
from uuid import UUID

from ...classify import tag_check as tc
from ...db.jobs import Job
from ...rules import read as read_rules
from ...settings import to_json
from ..context import StageContext, StageFailure
from ..graph import StageResult, Subject
from ..hashing import canonical_hash
from ..sql import now, to_ts, uid
from ._leftovers import library_name
from ._tagging import latest_output, read_video_tags, video_tags_digest, write_video_tags
from .registry import StageImpl

__all__ = ["ClipPlan", "IMPL", "apply_verdict", "load_clips", "plan_clips", "video_titles"]

#: unanswered questions in a row after which the model server is taken as gone and the job
#: stops (the answers so far are kept; the retry asks the rest)
MAX_UNANSWERED_IN_A_ROW = 5

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


def _ask(items: list[tuple[str, str, tuple[str, ...]]], cfg: tc.TagCheckConfig) -> str:
    return tc.ask(tc.clip_prompt(items, cfg), cfg)


def _on(cfg: tc.TagCheckConfig) -> bool:
    return cfg.enabled and cfg.clips


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: the library's short videos and seasons, the tagcheck settings and the newest
    ``tag`` output of the library (a new tag run asks for the verdicts to be applied again)."""
    cfg: tc.TagCheckConfig = (await read_rules(db, "tagcheck"))[0]
    if not _on(cfg):
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


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id: UUID = job.subject.id
    cfg: tc.TagCheckConfig = await ctx.rules("tagcheck")
    lib = await library_name(db, library_id)
    if lib is None:
        return StageResult(output_hash=canonical_hash({"library": None}), summary={"gone": True})
    if not _on(cfg):
        return StageResult(
            output_hash=await video_tags_digest(db, library_id), summary={"enabled": False}
        )
    plan = await plan_clips(db, library_id, lib, cfg)
    counts = {"clips": plan.clips, "asked": 0, "cached": plan.carried, "applied": 0, "failed": 0}
    for vid, keep, applied in plan.to_apply:
        counts["cached"] += 1
        if await apply_verdict(db, vid, keep, applied, cfg):
            counts["applied"] += 1
    size = max(1, cfg.clip_batch)
    batches = [plan.todo[i : i + size] for i in range(0, len(plan.todo), size)]
    in_a_row = 0  # unanswered questions in a row: the model server went away
    for b, batch in enumerate(batches, 1):
        await ctx.check_cancel()
        await ctx.progress("ask", b - 1, len(batches), f"{counts['asked']} clips answered")
        try:
            answer = await asyncio.to_thread(
                _ask, [(lib, c.title, c.candidates) for c, _ in batch], cfg
            )
        except (OSError, urllib.error.URLError, KeyError, ValueError) as e:
            counts["failed"] += len(batch)
            in_a_row += 1
            if in_a_row >= MAX_UNANSWERED_IN_A_ROW:
                await ctx.log("warn", f"question {b}: the model is not answering ({e}); stopping")
                break
            continue
        in_a_row = 0
        got = tc.parse_clips(answer, [c.candidates for c, _ in batch])
        for i, (clip, h) in enumerate(batch, 1):
            if i not in got:
                counts["failed"] += 1  # left out of the answer: asked again next run
                continue
            counts["asked"] += 1
            await db.execute(
                "INSERT INTO clip_checks (video_id, input_hash, model, topics, applied_hash,"
                " checked_at) VALUES ($1,$2,$3,$4,NULL,$5) ON CONFLICT (video_id) DO UPDATE SET"
                " input_hash = EXCLUDED.input_hash, model = EXCLUDED.model,"
                " topics = EXCLUDED.topics, applied_hash = NULL, checked_at = EXCLUDED.checked_at",
                uid(clip.video_id),
                h,
                cfg.model,
                json.dumps(got[i]),
                to_ts(db, now()),
            )
            if await apply_verdict(db, clip.video_id, got[i], None, cfg):
                counts["applied"] += 1
    if counts["failed"]:
        # what was answered is saved; the retry asks the rest
        raise StageFailure(
            "model_unavailable",
            f"{counts['failed']} of {len(plan.todo)} clips got no answer from the model",
        )
    return StageResult(output_hash=await video_tags_digest(db, library_id), summary=counts)


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
