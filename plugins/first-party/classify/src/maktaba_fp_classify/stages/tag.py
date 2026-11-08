# ruff: noqa: E501  (long SQL string literals read better unwrapped)
"""``tag``: topic tags for a season (and its episodes) and for a library's loose videos, in
``tv.maktaba.classify`` (Epic 54 wave 4, story 54.31).

A season job tags the season from its episodes' titles, transcripts and playlists; a library
job tags the videos that are an episode of no season. Either keeps the stored tags when the
keyword tags are the same as last time (they may carry the model's and the book's
corrections). The tagging itself, the stored tags and the fingerprint are the pipeline's
(``engine/stages/_tag.py``), shared with mediaintel's import.
"""

from __future__ import annotations

from collections import defaultdict
from typing import Any

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.changes import ChangeSet, apply_or_review
from maktaba_pipeline.engine.context import StageContext
from maktaba_pipeline.engine.graph import StageResult
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.propagate import last_success
from maktaba_pipeline.engine.sql import dumps, uid
from maktaba_pipeline.engine.stages import _tag as tg
from maktaba_pipeline.engine.stages.registry import StageImpl

__all__ = ["IMPL"]


async def _run_season(db: Any, job: Job, ctx: StageContext) -> StageResult:
    season_id = job.subject.id
    res = await tg.season_tags(db, season_id, await ctx.rules("tag"))
    if res is None:
        return StageResult(output_hash=canonical_hash("retired"), summary={"tagged": False})
    new = res.scores
    out = res.digest()
    prev = await last_success(db, job)
    if (
        prev is not None
        and str(prev["output_hash"]) == out
        and await db.fetchval(
            "SELECT count(*) FROM season_tags WHERE season_id = $1", uid(season_id)
        )
    ):
        # the same keyword tags as last time: the stored ones may carry the model's and the
        # book's corrections, which a rewrite would drop
        return StageResult(output_hash=out, summary={"tags": len(new), "kept": True})
    evidence = {t.tag: t.evidence for t in res.tags}
    old = {
        str(r["tag"]): float(r["score"])
        for r in await db.fetch(
            "SELECT tag, score FROM season_tags WHERE season_id = $1", uid(season_id)
        )
    }
    old_eps = await tg.stored_episode_tags(db, season_id)
    changes = tg.diff_tags("tag:season", old, new)

    async def apply(_cs: ChangeSet) -> None:
        async with db.transaction():
            await db.execute("DELETE FROM season_tags WHERE season_id = $1", uid(season_id))
            for t, score in new.items():
                await db.execute(
                    "INSERT INTO season_tags (season_id, tag, score, evidence) VALUES ($1,$2,$3,$4)",
                    uid(season_id),
                    t,
                    score,
                    dumps({"why": evidence.get(t, "")}),
                )
            await tg.write_episode_tags(db, old_eps, res.episodes)
            await tg.drop_departed_episode_tags(db, res.library_id)

    mode = await apply_or_review(ctx, changes, apply)
    if mode == "review":
        return StageResult(output_hash=canonical_hash("review"), outcome="awaiting_review")
    if mode == "noop":
        async with db.transaction():  # the season's tags stand; its episodes' own may have moved
            await tg.write_episode_tags(db, old_eps, res.episodes)
            await tg.drop_departed_episode_tags(db, res.library_id)
    genre = next((t[6:] for t in sorted(new) if t.startswith("genre:")), None)
    return StageResult(
        output_hash=out,
        summary={
            "tags": len(new),
            "genre": genre,
            "episodes_tagged": len(res.episodes),
            "changes": changes.counts,
        },
    )


async def _run_library(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id = job.subject.id
    new = await tg.library_tags(db, library_id, await ctx.rules("detect"), await ctx.rules("tag"))
    out = canonical_hash(
        [[v, sorted([t, round(s, 3)] for t, s in tags.items())] for v, tags in sorted(new.items())]
    )
    old: dict[str, dict[str, float]] = defaultdict(dict)
    for r in await db.fetch(
        "SELECT t.video_id AS vid, t.tag AS tag, t.score AS score FROM video_topic_tags t"
        " JOIN videos v ON v.id = t.video_id WHERE v.library_id = $1",
        uid(library_id),
    ):
        old[str(r["vid"])][str(r["tag"])] = float(r["score"])
    prev = await last_success(db, job)
    if prev is not None and str(prev["output_hash"]) == out and old:
        # the same keyword tags as last time: the stored ones may carry the model's
        # corrections (check_clips), which a rewrite would drop
        return StageResult(output_hash=out, summary={"videos": len(new), "kept": True})

    added: list[dict[str, Any]] = []
    removed: list[dict[str, Any]] = []
    changed: list[dict[str, Any]] = []
    for vid in sorted(set(new) | set(old)):
        o, n = old.get(vid, {}), new.get(vid, {})
        if o == n:
            continue
        d = tg.diff_tags("tag:video", o, n)
        entry = {
            "video_id": vid,
            "added": [x["tag"] for x in d.added],
            "removed": [x["tag"] for x in d.removed],
            "changed": [x["tag"] for x in d.changed],
        }
        if not d.added and not d.removed and not d.changed:
            continue
        (added if vid not in old else removed if vid not in new else changed).append(entry)
    changes = ChangeSet(
        "tag:library", tuple(added), tuple(removed), tuple(changed), (), {"videos": len(new)}
    )

    async def apply(_cs: ChangeSet) -> None:
        async with db.transaction():
            for vid in sorted(set(old) - set(new)):
                await db.execute("DELETE FROM video_topic_tags WHERE video_id = $1", vid)
            for vid, tags in new.items():
                if old.get(vid) == tags:
                    continue
                await db.execute("DELETE FROM video_topic_tags WHERE video_id = $1", vid)
                for t, score in tags.items():
                    await db.execute(
                        "INSERT INTO video_topic_tags (video_id, tag, score) VALUES ($1,$2,$3)",
                        vid,
                        t,
                        score,
                    )

    mode = await apply_or_review(ctx, changes, apply)
    if mode == "review":
        return StageResult(output_hash=canonical_hash("review"), outcome="awaiting_review")
    return StageResult(output_hash=out, summary={"videos": len(new), "changes": changes.counts})


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    if job.subject_kind == "season":
        return await _run_season(db, job, ctx)
    return await _run_library(db, job, ctx)


IMPL = StageImpl(run=run, input_fingerprint=tg.input_fingerprint)
