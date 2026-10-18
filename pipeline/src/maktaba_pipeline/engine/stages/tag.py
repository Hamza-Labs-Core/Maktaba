# ruff: noqa: E501  (long SQL string literals read better unwrapped)
"""``tag``: topic / format / audience / occasion tags (plan 06-11 section 3.2).

Runs :mod:`maktaba_pipeline.classify.topic_tags` over what ``detect`` stored; ported from the
prototype's ``classify_tags._tag_folder``:

* subject **season** -> ``season_tags``: tagged from the series name, its playlists' titles, its
  episodes' titles and descriptions, the channel and folder name, the median duration and the
  exact air dates, with the channel's pasted footer (``boilerplate_phrases``) ignored;
* subject **library** -> ``video_topic_tags``: every video that is in no season (unassigned or a
  short) is tagged on its own so it can be used as themed filler.

Both are change-set stages (notes N1a): ``compute_changes`` diffs the new tags against the stored
ones, ``apply_changes`` writes them, and the change set lands in the job log.

Also as in the prototype's ``tag`` step: a season's **episodes** are tagged by their own title
and by their chapter playlist («كتاب الصيام» of a hadith course), stored in ``episode_tags`` (a
Palestine episode of a talk show, a tajweed lesson inside a lecture series: themed channels
take such episodes one by one); a playlist holding several shows names a collection, not a
chapter of each show, so it is no evidence; the channel's branding is stripped from the
playlist titles; and what counts as a short follows the detection settings (``rules.detect``).

The model's and the book's corrections (``check_tags``, ``books``, ``check_clips``) are
written over these tags. A run whose keyword tags equal the last run's therefore writes
nothing, so those corrections stay; a run that does change them lets the corrections be
applied again from their cached answers (the output hash is the keyword tags').
"""

from __future__ import annotations

import posixpath
from collections import defaultdict
from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass
from datetime import date
from typing import Any
from uuid import UUID

from ...classify.series_detector import DetectorConfig, VideoFacts
from ...classify.series_detector import is_short as detector_is_short
from ...classify.topic_tags import (
    TAGGER_VERSION,
    Tag,
    boilerplate_phrases,
    branding,
    chapter_tags,
    tag_series,
    tag_video,
)
from ...db.jobs import Job
from ...rules import read as read_rules
from ...settings import to_json
from ..changes import ChangeSet, apply_or_review
from ..context import StageContext
from ..graph import StageResult, Subject
from ..hashing import canonical_hash
from ..propagate import last_success
from ..sql import dumps, is_pg, loads, uid
from ._common import load_season, season_episode_ids
from .registry import StageImpl

__all__ = ["IMPL", "diff_tags", "library_tags"]

_EPS = 0.005
#: ids per ``IN (...)`` query
_CHUNK = 500
#: what an episode keeps of its own tags: what it is about, for whom, for when
_EPISODE_NAMESPACES = ("topic:", "audience:", "occasion:")


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


# ------------------------------------------------------------------------- season


@dataclass(slots=True)
class SeasonTagging:
    """What the keyword tagger made of one season."""

    tags: list[Tag]
    scores: dict[str, float]
    #: episode (video id) -> its own tags, for the episodes that have any
    episodes: dict[str, dict[str, float]]

    def digest(self) -> str:
        """The output hash: the keyword tags of the season and of its episodes."""
        return canonical_hash(
            {
                "season": sorted([t, round(s, 3)] for t, s in self.scores.items()),
                "episodes": sorted(
                    [v, sorted([t, round(s, 3)] for t, s in tags.items())]
                    for v, tags in self.episodes.items()
                ),
            }
        )


async def _shows_in(db: Any, library_id: UUID, playlists: Iterable[str]) -> dict[str, int]:
    """How many shows (series) of the library each playlist is on. A playlist holding several
    shows («رمضان 1437», «غزة») names a collection, not each show in it."""
    out: dict[str, int] = {}
    for pl in sorted(set(playlists)):
        rows = await db.fetch(
            "SELECT DISTINCT series_id FROM series_seasons WHERE library_id = $1"
            " AND retired_at IS NULL AND CAST(metadata AS TEXT) LIKE $2",
            uid(library_id),
            f'%"{pl}"%',
        )
        out[pl] = len(rows)
    return out


async def _playlist_titles(db: Any, playlist_ids: Sequence[str]) -> dict[str, str]:
    out: dict[str, str] = {}
    for i in range(0, len(playlist_ids), _CHUNK):
        part = list(playlist_ids[i : i + _CHUNK])
        marks = ",".join(f"${n + 1}" for n in range(len(part)))
        for r in await db.fetch(
            f"SELECT playlist_id, title FROM youtube_playlists WHERE playlist_id IN ({marks})",
            *part,
        ):
            out[str(r["playlist_id"])] = str(r["title"])
    return out


async def _playlists_of(db: Any, youtube_ids: Sequence[str]) -> dict[str, list[str]]:
    out: dict[str, list[str]] = defaultdict(list)
    for i in range(0, len(youtube_ids), _CHUNK):
        part = list(youtube_ids[i : i + _CHUNK])
        marks = ",".join(f"${n + 1}" for n in range(len(part)))
        for r in await db.fetch(
            "SELECT playlist_id, youtube_id FROM youtube_playlist_items"
            f" WHERE youtube_id IN ({marks}) ORDER BY playlist_id",
            *part,
        ):
            out[str(r["youtube_id"])].append(str(r["playlist_id"]))
    return out


def _episode_tags(
    eps: Sequence[Any],
    channel: Sequence[str],
    noise: frozenset[str],
    playlists: Mapping[str, Sequence[str]],
    titles: Mapping[str, str],
    shows: Mapping[str, int],
) -> dict[str, dict[str, float]]:
    """Each episode's tags from its own title, plus its show's chapter playlists: an episode
    in «كتاب الصيام» of a hadith course is about that chapter, whatever its own title says
    («المجلس 14»)."""
    strip = branding(channel)
    chapter: dict[str, dict[str, float]] = {}  # playlist -> its chapter tags
    out: dict[str, dict[str, float]] = {}
    for e in eps:
        own: dict[str, float] = {}
        for t in tag_video(
            str(e["title"]),
            str(e["description"] or ""),
            None if e["dur"] is None else float(e["dur"]),
            channel=channel,
            noise=noise,
        ):
            if t.tag.startswith(_EPISODE_NAMESPACES) and t.tag != "audience:general":
                own[t.tag] = float(t.score)
        for pl in playlists.get(str(e["yid"] or ""), ()):
            if shows.get(pl, 0) > 1 or pl not in titles:
                continue  # a playlist holding several shows is a collection, not a chapter
            if pl not in chapter:
                chapter[pl] = chapter_tags(titles[pl], strip=strip)
            for tag, sc in chapter[pl].items():
                own[tag] = max(own.get(tag, 0.0), sc)
        if own:
            out[str(e["id"])] = own
    return out


async def _season_tags(db: Any, season_id: UUID) -> SeasonTagging | None:
    s = await load_season(db, season_id)
    if s is None or s.retired or s.library_id is None:
        return None
    ids = await season_episode_ids(db, season_id)
    if not ids:
        return SeasonTagging([], {}, {})
    marks = ",".join(f"${i + 1}" for i in range(len(ids)))
    rows = {
        str(r["id"]): r
        for r in await db.fetch(
            "SELECT v.id AS id, v.path AS path, COALESCE(yv.title, v.title, v.filename) AS title,"
            " yv.description AS description, COALESCE(v.duration_sec, yv.duration_s) AS dur,"
            " yv.upload_date AS upload_date, yv.date_quality AS dq,"
            " m.external_id AS yid FROM videos v"
            " LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'"
            " LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id"
            f" WHERE v.id IN ({marks})",
            *[str(i) for i in ids],
        )
    }
    eps = [rows[str(i)] for i in ids if str(i) in rows]
    folder = _library_noise_key(str(eps[0]["path"])) if eps else ""
    noise = boilerplate_phrases(await _folder_descriptions(db, s.library_id, folder))
    channel = await _channel_names(db, s.library_id, folder)
    own_pls = [str(p) for p in s.metadata.get("playlists") or []]
    by_episode = await _playlists_of(db, [str(e["yid"]) for e in eps if e["yid"]])
    every = sorted({*own_pls, *(p for pls in by_episode.values() for p in pls)})
    shows = await _shows_in(db, s.library_id, every)
    pl_titles = await _playlist_titles(db, every)
    dates = [
        d if isinstance(d, date) else date.fromisoformat(str(d)[:10])
        for e in eps
        if e["dq"] == "exact" and (d := e["upload_date"]) is not None
    ]
    durs = sorted(float(e["dur"]) for e in eps if e["dur"])
    median = s.median_duration_s or (durs[len(durs) // 2] if durs else None)
    tags = tag_series(
        s.name,
        # only a show's own playlists are evidence of its topic
        playlist_titles=[pl_titles[p] for p in own_pls if p in pl_titles and shows.get(p, 0) <= 1],
        episode_titles=[str(e["title"]) for e in eps],
        descriptions=[str(e["description"] or "") for e in eps],
        channel=channel,
        median_duration_s=median,
        kind=s.kind,
        air_dates=dates,
        noise=noise,
    )
    return SeasonTagging(
        tags,
        {t.tag: float(t.score) for t in tags},
        _episode_tags(eps, channel, noise, by_episode, pl_titles, shows),
    )


async def _stored_episode_tags(db: Any, season_id: UUID) -> dict[str, dict[str, float]]:
    out: dict[str, dict[str, float]] = defaultdict(dict)
    for r in await db.fetch(
        "SELECT t.video_id AS vid, t.tag AS tag, t.score AS score FROM episode_tags t"
        " WHERE t.video_id IN (SELECT e.video_id FROM series_episodes e"
        " JOIN series_seasons ss ON ss.series_id = e.series_id"
        "  AND ss.season = COALESCE(e.season_override, e.season) WHERE ss.id = $1)",
        uid(season_id),
    ):
        out[str(r["vid"])][str(r["tag"])] = round(float(r["score"]), 4)
    return dict(out)


async def _write_episode_tags(
    db: Any,
    old: Mapping[str, Mapping[str, float]],
    new: Mapping[str, Mapping[str, float]],
) -> None:
    for vid in sorted(set(old) | set(new)):
        if old.get(vid) == new.get(vid):
            continue
        await db.execute("DELETE FROM episode_tags WHERE video_id = $1", vid)
        for tag, score in (new.get(vid) or {}).items():
            await db.execute(
                "INSERT INTO episode_tags (video_id, tag, score) VALUES ($1,$2,$3)",
                vid,
                tag,
                score,
            )


async def _run_season(db: Any, job: Job, ctx: StageContext) -> StageResult:
    season_id = job.subject.id
    res = await _season_tags(db, season_id)
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
    old_eps = await _stored_episode_tags(db, season_id)
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
            await _write_episode_tags(db, old_eps, res.episodes)

    mode = await apply_or_review(ctx, changes, apply)
    if mode == "review":
        return StageResult(output_hash=canonical_hash("review"), outcome="awaiting_review")
    if mode == "noop":
        async with db.transaction():  # the season's tags stand; its episodes' own may have moved
            await _write_episode_tags(db, old_eps, res.episodes)
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


def _is_short(row: Any, detect: DetectorConfig) -> bool:
    meta = loads(row["metadata"]) or {}
    w, h = meta.get("width"), meta.get("height")
    facts = VideoFacts(
        key=str(row["id"]),
        title=str(row["title"]),
        duration_s=None if row["dur"] is None else float(row["dur"]),
        is_short=row["tab"] == "shorts" or bool(w and h and h > w),
    )
    return detector_is_short(facts, detect)


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    if subject.kind == "season":
        s = await load_season(db, subject.id)
        if s is None:
            return canonical_hash("gone")
        return canonical_hash(
            {"membership": s.membership_hash, "retired": s.retired, "v": TAGGER_VERSION}
        )
    rows = await db.fetch(_LOOSE_SQL, uid(subject.id))
    return canonical_hash(
        {
            "loose": [[str(r["id"]), r["title"], r["dur"], r["tab"]] for r in rows],
            # the detection settings decide what is a short
            "detect": to_json((await read_rules(db, "detect"))[0]),
            "v": TAGGER_VERSION,
        }
    )


async def library_tags(
    db: Any, library_id: UUID, detect: DetectorConfig
) -> dict[str, dict[str, float]]:
    """The keyword tags of every video of the library that is in no season, by video id."""
    rows = await db.fetch(_LOOSE_SQL, uid(library_id))
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
                is_short=_is_short(r, detect),
                noise=noise,
            )
            new[str(r["id"])] = {t.tag: float(t.score) for t in tags}
    return new


async def _run_library(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id = job.subject.id
    new = await library_tags(db, library_id, await ctx.rules("detect"))
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
    return StageResult(output_hash=out, summary={"videos": len(new), "changes": changes.counts})


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    if job.subject_kind == "season":
        return await _run_season(db, job, ctx)
    return await _run_library(db, job, ctx)


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
_ = is_pg
