# ruff: noqa: E501  (long SQL string literals read better unwrapped)
"""``tag``: topic / format / audience / occasion tags (plan 06-11 section 3.2).

Runs :mod:`maktaba_pipeline.classify.topic_tags` over what ``detect`` stored; ported from the
prototype's ``classify_tags._tag_folder``:

* subject **season** -> ``season_tags``: tagged from the series name, its playlists' titles, its
  episodes' titles and descriptions, the channel and folder name, the median duration and the
  exact air dates, with the channel's pasted footer (``boilerplate_phrases``) ignored;
* subject **library** -> ``video_topic_tags``: every video that is in no season (unassigned or a
  short) is tagged on its own so it can be used as themed filler.

Both also read what was said: the opening (``transcript_max_minutes``) of each video's active
transcript, sampled down to ``transcript_max_episodes`` for a big season, is a separate evidence
source (see :func:`~maktaba_pipeline.classify.topic_tags.tag_series`). The input fingerprint holds
the active transcript ids, so a new transcript re-tags its season (or the library's loose videos),
and the tagging rules as set (settings ``rules.tag``).

Both are change-set stages (notes N1a): ``compute_changes`` diffs the new tags against the stored
ones, ``apply_changes`` writes them, and the change set lands in the job log.
"""

from __future__ import annotations

import posixpath
from collections import defaultdict
from collections.abc import Iterable, Mapping, Sequence
from datetime import date
from typing import Any
from uuid import UUID

from ... import settings
from ...classify.series_detector import DetectorConfig, VideoFacts
from ...classify.series_detector import is_short as detector_is_short
from ...classify.topic_tags import (
    TAGGER_VERSION,
    Tag,
    TaggerConfig,
    boilerplate_phrases,
    sample_evenly,
    tag_series,
    tag_video,
)
from ...db.jobs import Job
from ...rules import read as read_rules
from ..changes import ChangeSet, apply_or_review
from ..context import StageContext
from ..graph import StageResult, Subject
from ..hashing import canonical_hash
from ..sql import dumps, is_pg, loads, uid
from ._common import load_season, season_episode_ids
from .registry import StageImpl

__all__ = ["IMPL", "diff_tags"]

_EPS = 0.005


def diff_tags(
    kind: str,
    old: Mapping[str, float],
    new: Mapping[str, float],
    *,
    meta: Mapping[str, Any] | None = None,
) -> ChangeSet:
    added = tuple({"tag": t, "score": round(s, 3)} for t, s in sorted(new.items()) if t not in old)
    removed = tuple(
        {"tag": t, "score": round(s, 3)} for t, s in sorted(old.items()) if t not in new
    )
    changed = tuple(
        {"tag": t, "from": round(old[t], 3), "to": round(new[t], 3)}
        for t in sorted(new)
        if t in old and abs(old[t] - new[t]) > _EPS
    )
    return ChangeSet(kind, added, removed, changed, (), meta or {})


def _library_noise_key(path: str) -> str:
    return posixpath.dirname(path.replace("\\", "/"))


async def _folder_descriptions(db: Any, library_id: UUID, folder: str) -> list[str]:
    rows = await db.fetch(
        "SELECT yv.description AS d FROM videos v"
        " JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'"
        " JOIN youtube_videos yv ON yv.youtube_id = m.external_id"
        " WHERE v.library_id = $1 AND v.deleted_at IS NULL AND v.path LIKE $2"
        " AND yv.description IS NOT NULL",
        uid(library_id),
        folder.rstrip("/") + "/%",
    )
    return [str(r["d"]) for r in rows]


async def _channel_names(db: Any, library_id: UUID, folder: str) -> list[str]:
    rows = await db.fetch(
        "SELECT title FROM youtube_channels WHERE library_id = $1 AND title IS NOT NULL"
        " ORDER BY channel_id",
        uid(library_id),
    )
    names = [str(r["title"]) for r in rows][:3]
    base = posixpath.basename(folder)
    if base and base not in names:
        names.append(base)
    return names


# ------------------------------------------------------------------------- transcripts

_BATCH = 500


def _first_per_video(rows: Iterable[Any]) -> dict[str, str]:
    """video id -> its active transcript id; with several audio tracks, the first track's
    (rows come ordered by video, track, id)."""
    out: dict[str, str] = {}
    for r in rows:
        out.setdefault(str(r["video_id"]), str(r["id"]))
    return out


async def _season_transcript_ids(db: Any, video_ids: Sequence[UUID]) -> dict[str, str]:
    if not video_ids:
        return {}
    marks = ",".join(f"${i + 1}" for i in range(len(video_ids)))
    rows = await db.fetch(
        "SELECT id, video_id FROM transcripts"
        f" WHERE is_active AND video_id IN ({marks})"
        " ORDER BY video_id, audio_track_id, id",
        *[str(v) for v in video_ids],
    )
    return _first_per_video(rows)


_LOOSE_TRANSCRIPTS_SQL = """
SELECT t.id AS id, t.video_id AS video_id
  FROM transcripts t
  JOIN videos v ON v.id = t.video_id
 WHERE v.library_id = $1 AND v.deleted_at IS NULL AND t.is_active
   AND NOT EXISTS (SELECT 1 FROM series_episodes e WHERE e.video_id = v.id)
 ORDER BY t.video_id, t.audio_track_id, t.id
"""


async def _loose_transcript_ids(db: Any, library_id: UUID) -> dict[str, str]:
    return _first_per_video(await db.fetch(_LOOSE_TRANSCRIPTS_SQL, uid(library_id)))


async def _transcript_texts(
    db: Any, transcript_ids: Sequence[str], cfg: TaggerConfig
) -> dict[str, str]:
    """transcript id -> the text of its segments starting in the first
    ``transcript_max_minutes``, at most ``transcript_max_chars`` long."""
    parts: dict[str, list[str]] = defaultdict(list)
    size: dict[str, int] = defaultdict(int)
    limit_s = cfg.transcript_max_minutes * 60.0
    for at in range(0, len(transcript_ids), _BATCH):
        batch = list(transcript_ids[at : at + _BATCH])
        marks = ",".join(f"${i + 2}" for i in range(len(batch)))
        for r in await db.fetch(
            "SELECT transcript_id AS tid, text FROM transcript_segments"
            f" WHERE start_sec < $1 AND transcript_id IN ({marks})"
            " ORDER BY transcript_id, seq",
            limit_s,
            *batch,
        ):
            tid = str(r["tid"])
            if size[tid] >= cfg.transcript_max_chars:
                continue
            text = str(r["text"] or "").strip()
            if text:
                parts[tid].append(text)
                size[tid] += len(text) + 1
    return {tid: " ".join(ps)[: cfg.transcript_max_chars] for tid, ps in parts.items()}


# ------------------------------------------------------------------------- season


async def _season_tags(
    db: Any, season_id: UUID, cfg: TaggerConfig
) -> tuple[list[Tag], dict[str, float]] | None:
    s = await load_season(db, season_id)
    if s is None or s.retired or s.library_id is None:
        return None
    ids = await season_episode_ids(db, season_id)
    if not ids:
        return [], {}
    by_video = await _season_transcript_ids(db, ids)
    # in episode order, spread evenly over the season
    chosen = sample_evenly(
        [by_video[str(i)] for i in ids if str(i) in by_video], cfg.transcript_max_episodes
    )
    texts = await _transcript_texts(db, chosen, cfg)
    marks = ",".join(f"${i + 1}" for i in range(len(ids)))
    rows = {
        str(r["id"]): r
        for r in await db.fetch(
            "SELECT v.id AS id, v.path AS path, COALESCE(yv.title, v.title, v.filename) AS title,"
            " yv.description AS description, COALESCE(v.duration_sec, yv.duration_s) AS dur,"
            " yv.upload_date AS upload_date, yv.date_quality AS dq FROM videos v"
            " LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'"
            " LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id"
            f" WHERE v.id IN ({marks})",
            *[str(i) for i in ids],
        )
    }
    eps = [rows[str(i)] for i in ids if str(i) in rows]
    folder = _library_noise_key(str(eps[0]["path"])) if eps else ""
    noise = boilerplate_phrases(await _folder_descriptions(db, s.library_id, folder))
    pl_ids = list(s.metadata.get("playlists") or [])
    pl_titles: list[str] = []
    if pl_ids:
        pm = ",".join(f"${i + 1}" for i in range(len(pl_ids)))
        pl_titles = [
            str(r["title"])
            for r in await db.fetch(
                f"SELECT title FROM youtube_playlists WHERE playlist_id IN ({pm})", *pl_ids
            )
        ]
    dates = [
        d if isinstance(d, date) else date.fromisoformat(str(d)[:10])
        for e in eps
        if e["dq"] == "exact" and (d := e["upload_date"]) is not None
    ]
    durs = sorted(float(e["dur"]) for e in eps if e["dur"])
    median = s.median_duration_s or (durs[len(durs) // 2] if durs else None)
    tags = tag_series(
        s.name,
        playlist_titles=pl_titles,
        episode_titles=[str(e["title"]) for e in eps],
        descriptions=[str(e["description"] or "") for e in eps],
        channel=await _channel_names(db, s.library_id, folder),
        median_duration_s=median,
        kind=s.kind,
        air_dates=dates,
        noise=noise,
        transcripts=[texts[t] for t in chosen if t in texts],
        config=cfg,
    )
    return tags, {t.tag: float(t.score) for t in tags}


async def _run_season(db: Any, job: Job, ctx: StageContext) -> StageResult:
    season_id = job.subject.id
    res = await _season_tags(db, season_id, await ctx.rules("tag"))
    if res is None:
        return StageResult(output_hash=canonical_hash("retired"), summary={"tagged": False})
    tags, new = res
    evidence = {t.tag: t.evidence for t in tags}
    old = {
        str(r["tag"]): float(r["score"])
        for r in await db.fetch(
            "SELECT tag, score FROM season_tags WHERE season_id = $1", uid(season_id)
        )
    }
    changes = diff_tags("tag:season", old, new)

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

    mode = await apply_or_review(ctx, changes, apply)
    if mode == "review":
        return StageResult(output_hash=canonical_hash("review"), outcome="awaiting_review")
    genre = next((t[6:] for t in sorted(new) if t.startswith("genre:")), None)
    return StageResult(
        output_hash=canonical_hash(sorted([t, round(s, 3)] for t, s in new.items())),
        summary={"tags": len(new), "genre": genre, "changes": changes.counts},
    )


# ------------------------------------------------------------------------- library


_LOOSE_SQL = """
SELECT v.id AS id, v.path AS path, COALESCE(yv.title, v.title, v.filename) AS title,
       yv.description AS description, COALESCE(v.duration_sec, yv.duration_s) AS dur,
       yv.tab AS tab, v.metadata AS metadata
  FROM videos v
  LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'
  LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id
 WHERE v.library_id = $1 AND v.deleted_at IS NULL
   AND NOT EXISTS (SELECT 1 FROM series_episodes e WHERE e.video_id = v.id)
 ORDER BY v.id
"""


def _is_short(row: Any) -> bool:
    meta = loads(row["metadata"]) or {}
    w, h = meta.get("width"), meta.get("height")
    facts = VideoFacts(
        key=str(row["id"]),
        title=str(row["title"]),
        duration_s=None if row["dur"] is None else float(row["dur"]),
        is_short=row["tab"] == "shorts" or bool(w and h and h > w),
    )
    return detector_is_short(facts, DetectorConfig())


def _ids_hash(by_video: Mapping[str, str]) -> str:
    return canonical_hash(sorted(by_video.values()))


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    # the rules as set: a changed tagging setting tags again
    rules = settings.to_json((await read_rules(db, "tag"))[0])
    if subject.kind == "season":
        s = await load_season(db, subject.id)
        if s is None:
            return canonical_hash("gone")
        by_video = await _season_transcript_ids(db, await season_episode_ids(db, subject.id))
        return canonical_hash(
            {
                "membership": s.membership_hash,
                "retired": s.retired,
                "v": TAGGER_VERSION,
                "transcripts": _ids_hash(by_video),
                "rules": rules,
            }
        )
    rows = await db.fetch(_LOOSE_SQL, uid(subject.id))
    return canonical_hash(
        {
            "loose": [[str(r["id"]), r["title"], r["dur"], r["tab"]] for r in rows],
            "v": TAGGER_VERSION,
            "transcripts": _ids_hash(await _loose_transcript_ids(db, subject.id)),
            "rules": rules,
        }
    )


async def _run_library(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id = job.subject.id
    cfg: TaggerConfig = await ctx.rules("tag")
    rows = await db.fetch(_LOOSE_SQL, uid(library_id))
    by_video = await _loose_transcript_ids(db, library_id)
    texts = await _transcript_texts(db, sorted(by_video.values()), cfg)
    by_folder: dict[str, list[Any]] = defaultdict(list)
    for r in rows:
        by_folder[_library_noise_key(str(r["path"]))].append(r)
    new: dict[str, dict[str, float]] = {}
    for folder, grp in by_folder.items():
        noise = boilerplate_phrases(str(r["description"] or "") for r in grp)
        channel = await _channel_names(db, library_id, folder)
        for r in grp:
            tags = tag_video(
                str(r["title"]),
                str(r["description"] or ""),
                None if r["dur"] is None else float(r["dur"]),
                channel=channel,
                is_short=_is_short(r),
                noise=noise,
                transcript=texts.get(by_video.get(str(r["id"]), ""), ""),
                config=cfg,
            )
            new[str(r["id"])] = {t.tag: float(t.score) for t in tags}
    old: dict[str, dict[str, float]] = defaultdict(dict)
    for r in await db.fetch(
        "SELECT t.video_id AS vid, t.tag AS tag, t.score AS score FROM video_topic_tags t"
        " JOIN videos v ON v.id = t.video_id WHERE v.library_id = $1",
        uid(library_id),
    ):
        old[str(r["vid"])][str(r["tag"])] = float(r["score"])

    added: list[dict[str, Any]] = []
    removed: list[dict[str, Any]] = []
    changed: list[dict[str, Any]] = []
    for vid in sorted(set(new) | set(old)):
        o, n = old.get(vid, {}), new.get(vid, {})
        if o == n:
            continue
        d = diff_tags("tag:video", o, n)
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
    return StageResult(
        output_hash=canonical_hash(
            [
                [v, sorted([t, round(s, 3)] for t, s in tags.items())]
                for v, tags in sorted(new.items())
            ]
        ),
        summary={"videos": len(new), "changes": changes.counts},
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    if job.subject_kind == "season":
        return await _run_season(db, job, ctx)
    return await _run_library(db, job, ctx)


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
_ = is_pg
