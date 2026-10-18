"""``check_tags``: a language model checks each series' topic tags (mediaintel ``check-tags``).

Subject: a season. Runs after ``tag``. The model reads the series' name, its library and a
sample of its episode titles and names the topics it is about; its verdict corrects the
season's tags (:func:`maktaba_pipeline.classify.tag_check.apply`). The verdict is cached per
input (name, library, sampled titles, model, prompt version) in ``tag_checks``, so a series is
asked once. A season whose tags already carry the verdict is left alone (``applied_hash``);
a new ``tag`` run rewrites the keyword tags and the cached verdict is applied again without
asking the model.

Settings: ``rules.tagcheck`` (off, model server, model, sample size, damping ...).
"""

from __future__ import annotations

import asyncio
import urllib.error
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
from ._common import load_season
from ._leftovers import library_name
from ._tagging import latest_output, read_season_tags, season_tags_digest, write_season_tags
from .registry import StageImpl

__all__ = ["IMPL", "apply_answer", "season_inputs"]

_TITLES_SQL = """
SELECT COALESCE(yv.title, v.title, v.filename) AS title
  FROM series_episodes e
  JOIN series_seasons ss ON ss.series_id = e.series_id
   AND ss.season = COALESCE(e.season_override, e.season)
  JOIN videos v ON v.id = e.video_id AND v.deleted_at IS NULL
  LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'
  LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id
 WHERE ss.id = $1
 ORDER BY COALESCE(e.episode_override, e.episode), v.id
"""


async def season_inputs(db: Any, season_id: UUID) -> tuple[str, str, list[str]] | None:
    """``(series name, library name, episode titles)`` of a live season, or None."""
    s = await load_season(db, season_id)
    if s is None or s.retired:
        return None
    library = (await library_name(db, s.library_id)) if s.library_id else ""
    titles = [str(r["title"]) for r in await db.fetch(_TITLES_SQL, uid(season_id))]
    return s.name, library or "", titles


async def apply_answer(
    db: Any, season_id: UUID, answer: str, applied_hash: str | None, cfg: tc.TagCheckConfig
) -> bool:
    """Apply the model's ``answer`` to the season's stored tags, unless they already carry it
    (``applied_hash`` is the hash of the tags it was last applied to). True: tags changed."""
    verdict = tc.parse(answer, cfg)
    tags = await read_season_tags(db, season_id)
    if verdict is None or not tags or applied_hash == tc.tags_hash(tags):
        return False
    new = tc.apply(tags, verdict, cfg)
    await write_season_tags(db, season_id, new)
    await db.execute(
        "UPDATE tag_checks SET applied_hash = $2 WHERE season_id = $1",
        uid(season_id),
        tc.tags_hash(new),
    )
    return True


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: the question (what the cache is keyed by), the tagcheck settings and the
    newest ``tag`` output of the season (a new tag run asks for the verdict to be applied
    again)."""
    cfg: tc.TagCheckConfig = (await read_rules(db, "tagcheck"))[0]
    got = await season_inputs(db, subject.id)
    if got is None:
        return canonical_hash("gone")
    if not cfg.enabled:
        return canonical_hash({"off": True, "v": tc.TAG_CHECK_VERSION})
    name, library, titles = got
    return canonical_hash(
        {
            "question": tc.input_hash(name, library, titles, cfg),
            "rules": to_json(cfg),
            "tag": await latest_output(db, "season", subject.id, "tag"),
            "v": tc.TAG_CHECK_VERSION,
        }
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    season_id: UUID = job.subject.id
    cfg: tc.TagCheckConfig = await ctx.rules("tagcheck")
    got = await season_inputs(db, season_id)
    if got is None:
        return StageResult(output_hash=canonical_hash("retired"), summary={"checked": False})
    if not cfg.enabled:
        out = await season_tags_digest(db, season_id)
        return StageResult(output_hash=out, summary={"enabled": False})
    name, library, titles = got
    h = tc.input_hash(name, library, titles, cfg)
    row = await db.fetchrow(
        "SELECT input_hash, answer, applied_hash FROM tag_checks WHERE season_id = $1",
        uid(season_id),
    )
    asked = False
    if row is not None and row["input_hash"] == h:
        answer, applied = str(row["answer"]), row["applied_hash"]
    else:
        try:
            answer = await asyncio.to_thread(tc.ask, tc.prompt(name, library, titles, cfg), cfg)
        except (OSError, urllib.error.URLError, KeyError, ValueError) as e:
            raise StageFailure(
                "model_unavailable", f"{name[:50]}: the model did not answer ({e})"
            ) from e
        applied, asked = None, True
        await db.execute(
            "INSERT INTO tag_checks"
            " (season_id, input_hash, model, answer, applied_hash, checked_at)"
            " VALUES ($1,$2,$3,$4,NULL,$5) ON CONFLICT (season_id) DO UPDATE SET"
            " input_hash = EXCLUDED.input_hash, model = EXCLUDED.model, answer = EXCLUDED.answer,"
            " applied_hash = NULL, checked_at = EXCLUDED.checked_at",
            uid(season_id),
            h,
            cfg.model,
            answer,
            to_ts(db, now()),
        )
    corrected = await apply_answer(db, season_id, answer, applied, cfg)
    return StageResult(
        output_hash=await season_tags_digest(db, season_id),
        summary={"asked": asked, "corrected": corrected, "model": cfg.model},
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
