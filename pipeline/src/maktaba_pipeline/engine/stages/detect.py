# ruff: noqa: E501  (long SQL string literals read better unwrapped)
"""``detect``: place a library's episodes into series and seasons (plan 06-11 sections 3.4, 4.4).

The pure detector (:mod:`maktaba_pipeline.classify.series_detector`) is called
unchanged. This module is the I/O half, ported from the mediaintel prototype's
``detect.py`` (``load_facts`` / ``store``) and ``signatures.plan``:

* **load** the library's videos with everything known about them (YouTube link and
  dates, thumbnail dHash, audio / frame fingerprints), grouped by folder - a folder is
  one channel, which is the unit the detector reasons over;
* **plan** series / seasons / episodes and the fingerprint plan of every season that has
  unplaced candidates (``fp_plan``: ``PER_SEASON`` samples and the candidates that could
  still join);
* **compute_changes** diffs that plan against the stored state into a typed
  :class:`~maktaba_pipeline.engine.changes.ChangeSet`, and **apply_changes** writes it;
* return the explicit requests of the two-pass fingerprint flow (samples, then ``fp_gate``).

Output: ``series``, ``series_seasons``, ``series_episodes`` and a hash of each season's
``membership_hash``. Only a membership change (episodes, order, kind, number, name, extras)
propagates; dates and fingerprint plans do not.
"""

from __future__ import annotations

import asyncio
import math
import posixpath
from collections import Counter, defaultdict
from collections.abc import Sequence
from dataclasses import dataclass, field
from datetime import date
from typing import Any
from uuid import UUID

from ... import settings
from ...audio.chromaprint import decode
from ...classify.series_detector import (
    DETECTOR_VERSION,
    Detection,
    DetectorConfig,
    PlaylistFacts,
    VideoFacts,
    detect,
)
from ...db.jobs import Job
from ...rules import read as read_rules
from ..changes import ChangeSet, apply_or_review
from ..context import StageContext
from ..graph import SIG_VERSION, Request, StageResult, Subject
from ..hashing import canonical_hash
from ..ids import season_uuid, series_uuid
from ..sql import dumps, is_pg, loads, now, to_date, to_ts, uid
from ._common import load_video  # noqa: F401 - re-export for tests
from .registry import StageImpl

__all__ = [
    "IMPL",
    "PER_SEASON",
    "LibraryPlan",
    "PlannedSeason",
    "apply_changes",
    "compute_changes",
    "load_rows",
    "plan_library",
]

PER_SEASON = 4
LENGTH_BAND = 0.35  # |log(duration / season median)|: about +-40 %
DATE_SLACK_DAYS = 180
MIN_CANDIDATE_S = 180.0
SUCCESS = "('changed','unchanged','skipped_unchanged','imported')"


# --------------------------------------------------------------------------- loading


@dataclass(frozen=True, slots=True)
class Row:
    """One video with everything the detector can use."""

    id: UUID
    folder: str
    title: str
    duration_s: float | None
    air_date: date | None
    date_exact: bool
    dhash: int | None
    is_short: bool
    audio_head: tuple[int, ...]
    audio_tail: tuple[int, ...]
    frames: tuple[int, ...]
    fingerprinted: bool
    channel_title: str | None
    youtube_id: str | None

    def facts(self) -> VideoFacts:
        return VideoFacts(
            key=str(self.id),
            title=self.title,
            duration_s=self.duration_s,
            air_date=self.air_date,
            date_exact=self.date_exact,
            dhash=self.dhash,
            is_short=self.is_short,
            audio_head=self.audio_head,
            audio_tail=self.audio_tail,
            frames=self.frames,
        )


def _date(v: Any) -> date | None:
    if v is None:
        return None
    if isinstance(v, date):
        return v
    return date.fromisoformat(str(v)[:10])


_LOAD_SQL = """
SELECT v.id AS id, v.path AS path, v.filename AS filename, v.title AS title,
       v.duration_sec AS duration_sec, v.metadata AS metadata,
       vs.thumb_dhash AS thumb_dhash, vs.audio_head AS audio_head, vs.audio_tail AS audio_tail,
       vs.frame_hashes AS frame_hashes, vs.fp_version AS fp_version, vs.fp_partial AS fp_partial,
       m.external_id AS youtube_id,
       yv.title AS yt_title, yv.upload_date AS upload_date, yv.date_quality AS date_quality,
       yv.duration_s AS yt_duration, yv.tab AS tab, yc.title AS channel_title
  FROM videos v
  LEFT JOIN video_signals vs ON vs.video_id = v.id
  LEFT JOIN media_metadata_enrichment m
         ON m.video_id = v.id AND m.provider = 'youtube' AND m.is_dismissed = {false}
  LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id
  LEFT JOIN youtube_channels yc ON yc.channel_id = yv.channel_id
 WHERE v.library_id = $1 AND v.deleted_at IS NULL
 ORDER BY v.id
"""


async def load_rows(db: Any, library_id: UUID) -> list[Row]:
    sql = _LOAD_SQL.replace("{false}", "false" if is_pg(db) else "0")
    out: list[Row] = []
    for r in await db.fetch(sql, uid(library_id)):
        meta = loads(r["metadata"]) or {}
        w, h = meta.get("width"), meta.get("height")
        stem = str(r["filename"]).rsplit(".", 1)[0]
        dur = r["duration_sec"] if r["duration_sec"] is not None else r["yt_duration"]
        frames = tuple(int(x, 16) for x in (r["frame_hashes"] or "").split(",") if x)
        out.append(
            Row(
                id=UUID(str(r["id"])),
                folder=posixpath.dirname(str(r["path"]).replace("\\", "/")),
                title=str(r["yt_title"] or r["title"] or stem),
                duration_s=None if dur is None else float(dur),
                air_date=_date(r["upload_date"]),
                date_exact=r["date_quality"] == "exact",
                dhash=int(r["thumb_dhash"], 16) if r["thumb_dhash"] else None,
                is_short=r["tab"] == "shorts" or bool(w and h and h > w),
                audio_head=tuple(decode(r["audio_head"])),
                audio_tail=tuple(decode(r["audio_tail"])),
                frames=frames,
                fingerprinted=bool(
                    r["fp_version"] == SIG_VERSION
                    and not r["fp_partial"]
                    and (r["audio_head"] or r["audio_tail"] or r["frame_hashes"])
                ),
                channel_title=None if r["channel_title"] is None else str(r["channel_title"]),
                youtube_id=None if r["youtube_id"] is None else str(r["youtube_id"]),
            )
        )
    return out


async def _playlists(
    db: Any, library_id: UUID
) -> dict[str, tuple[str, list[tuple[str, int | None]]]]:
    rows = await db.fetch(
        "SELECT p.playlist_id AS pid, p.title AS title, m.video_id AS vid, i.position AS pos"
        " FROM youtube_playlists p"
        " JOIN youtube_playlist_items i ON i.playlist_id = p.playlist_id"
        " JOIN media_metadata_enrichment m ON m.external_id = i.youtube_id AND m.provider = 'youtube'"
        " JOIN videos v ON v.id = m.video_id"
        " WHERE p.is_real = $2 AND v.library_id = $1 AND v.deleted_at IS NULL"
        " ORDER BY p.playlist_id, i.position",
        uid(library_id),
        True if is_pg(db) else 1,
    )
    by_pl: dict[str, tuple[str, list[tuple[str, int | None]]]] = {}
    for r in rows:
        title, members = by_pl.setdefault(str(r["pid"]), (str(r["title"]), []))
        members.append((str(r["vid"]), None if r["pos"] is None else int(r["pos"])))
    return by_pl


# --------------------------------------------------------------------------- planning


@dataclass(frozen=True, slots=True)
class PlannedSeason:
    id: UUID
    series_id: UUID
    series_key: str
    series_name: str
    kind: str
    number: int
    episodes: tuple[UUID, ...]
    numbers: tuple[int, ...]
    extras: tuple[UUID, ...]
    attached: tuple[UUID, ...]
    duplicates: tuple[UUID, ...]
    playlists: tuple[str, ...]
    first_air: date | None
    last_air: date | None
    median_duration_s: float | None
    membership_hash: str
    fp_plan: dict[str, list[str]] | None = None
    #: a named season (a talk show's guest) and the year a playlist title gives it
    season_name: str | None = None
    season_year: int | None = None
    #: the series is a title brand ("شاهد على العصر | …"), not playlist seeds
    brand: bool = False


@dataclass(frozen=True, slots=True)
class LibraryPlan:
    seasons: tuple[PlannedSeason, ...]
    unassigned: tuple[UUID, ...]
    shorts: tuple[UUID, ...]
    group_count: int = 1

    @property
    def episode_count(self) -> int:
        return sum(len(s.episodes) for s in self.seasons)


def _spread(items: Sequence[UUID], n: int) -> list[UUID]:
    if len(items) <= n:
        return list(items)
    return [items[int((i + 0.5) * len(items) / n)] for i in range(n)]


def _membership_hash(
    name: str,
    kind: str,
    number: int,
    eps: Sequence[UUID],
    nums: Sequence[int],
    extras: Sequence[UUID],
    season_name: str | None = None,
) -> str:
    return canonical_hash(
        {
            "name": name,
            "season_name": season_name,
            "kind": kind,
            "number": number,
            "episodes": [[str(e), n] for e, n in zip(eps, nums, strict=True)],
            "extras": sorted(str(x) for x in extras),
        }
    )


def _fp_plan(
    det: Detection, rows: dict[str, Row], season_eps: dict[tuple[int, int], list[UUID]]
) -> dict[tuple[int, int], dict[str, list[str]]]:
    """Samples and candidates per season (port of ``signatures.plan``).

    Candidates are the group's long-enough, dated videos that no season took, whose length
    is within ``LENGTH_BAND`` of the season median and whose air date is within
    ``DATE_SLACK_DAYS`` of the season (no upper limit for an ``ongoing`` show, which keeps
    airing - matching the detector's own ``_fingerprint_worth_it``).
    """
    placed = {k for s in det.series for se in s.seasons for k in se.episodes} | set(det.shorts)
    seasons: list[tuple[tuple[int, int], float | None, int, int | None]] = []
    for i, ser in enumerate(det.series):
        if ser.kind == "clips":
            continue
        for se in ser.seasons:
            if not (se.first_air and se.last_air):
                continue
            last = None if ser.kind == "ongoing" else se.last_air.toordinal()
            seasons.append(((i, se.number), se.median_duration_s, se.first_air.toordinal(), last))
    plan: dict[tuple[int, int], dict[str, list[str]]] = {}
    for key, row in rows.items():
        if key in placed or row.air_date is None:
            continue
        if row.duration_s is None or row.duration_s < MIN_CANDIDATE_S:
            continue
        d = row.air_date.toordinal()
        for sk, med, first, last in seasons:
            if med and abs(math.log(row.duration_s / med)) > LENGTH_BAND:
                continue
            if d >= first - DATE_SLACK_DAYS and (last is None or d <= last + DATE_SLACK_DAYS):
                plan.setdefault(sk, {"samples": [], "candidates": []})["candidates"].append(key)
    for sk in plan:
        plan[sk]["samples"] = [str(v) for v in _spread(season_eps[sk], PER_SEASON)]
        plan[sk]["candidates"].sort()
    return plan


def distinct_series_key(
    library_id: UUID,
    key: str,
    kind: str,
    numbers: Sequence[int],
    folder: str,
    first_episode: str,
    used: set[UUID],
) -> str:
    """The key a series' ids are made from: its own, unless one of its seasons' ids is
    taken already (the same name in another folder, or a second show of that name the
    detector split by length): then ``key@folder``, then ``key@folder#<first episode>``.
    """
    base = posixpath.basename(folder) or "root"
    for cand in (key, f"{key}@{base}", f"{key}@{base}#{first_episode}"):
        if not any(season_uuid(library_id, cand, kind, n) in used for n in numbers):
            return cand
    return cand


def plan_library(
    library_id: UUID,
    rows: Sequence[Row],
    playlists: dict[str, tuple[str, list[tuple[str, int | None]]]],
    *,
    config: DetectorConfig | None = None,
    progress: Any = None,
) -> LibraryPlan:
    """Run the detector per folder and turn its result into stable, id-bearing seasons."""
    cfg = config or DetectorConfig()
    groups: dict[str, list[Row]] = defaultdict(list)
    for r in rows:
        groups[r.folder].append(r)
    seasons: list[PlannedSeason] = []
    unassigned: list[UUID] = []
    shorts: list[UUID] = []
    used_ids: set[UUID] = set()
    for folder in sorted(groups):
        grows = groups[folder]
        by_key = {str(r.id): r for r in grows}
        pls = [
            PlaylistFacts(pid, title, tuple(m for m in members if m[0] in by_key))
            for pid, (title, members) in playlists.items()
            if any(m[0] in by_key for m in members)
        ]
        channel = Counter(r.channel_title for r in grows if r.channel_title).most_common(1)
        presenter = [channel[0][0]] if channel else []
        presenter.append(posixpath.basename(folder) or folder)
        det = detect(
            [r.facts() for r in grows],
            pls,
            presenter_words=presenter,
            config=cfg,
            progress=progress,
        )
        # keyed by the series' position: two shows of one name (split by length) are two series
        season_eps: dict[tuple[int, int], list[UUID]] = {}
        for i, ser in enumerate(det.series):
            for se in ser.seasons:
                # samples come from the seed members, not from videos the detector attached: a
                # sample set that moved whenever a candidate joined would re-read the NAS forever
                seed = [k for k in se.episodes if k not in set(se.attached)] or list(se.episodes)
                season_eps[(i, se.number)] = [UUID(k) for k in seed]
        fp = _fp_plan(det, by_key, season_eps)
        for i, ser in enumerate(det.series):
            key = distinct_series_key(
                library_id,
                ser.key,
                ser.kind,
                [se.number for se in ser.seasons],
                folder,
                min((k for se in ser.seasons for k in se.episodes), default=""),
                used_ids,
            )
            for se in ser.seasons:
                sid = season_uuid(library_id, key, ser.kind, se.number)
                used_ids.add(sid)
                eps = tuple(UUID(k) for k in se.episodes)
                numbers = getattr(se, "numbers", None)
                nums = (
                    tuple(int(n) for n in numbers)
                    if numbers and len(numbers) == len(eps)
                    else tuple(range(1, len(eps) + 1))
                )
                extras = tuple(UUID(k) for k in se.extras)
                seasons.append(
                    PlannedSeason(
                        id=sid,
                        series_id=series_uuid(library_id, key, ser.kind),
                        series_key=key,
                        series_name=ser.name,
                        kind=ser.kind,
                        number=se.number,
                        episodes=eps,
                        numbers=nums,
                        extras=extras,
                        attached=tuple(UUID(k) for k in se.attached),
                        duplicates=tuple(UUID(k) for k in se.duplicates),
                        playlists=tuple(se.playlists),
                        first_air=se.first_air,
                        last_air=se.last_air,
                        median_duration_s=se.median_duration_s,
                        membership_hash=_membership_hash(
                            ser.name, ser.kind, se.number, eps, nums, extras, se.name
                        ),
                        fp_plan=fp.get((i, se.number)),
                        season_name=se.name,
                        season_year=se.year,
                        brand=ser.brand,
                    )
                )
        unassigned.extend(UUID(k) for k in det.unassigned)
        shorts.extend(UUID(k) for k in det.shorts)
    return LibraryPlan(tuple(seasons), tuple(unassigned), tuple(shorts), len(groups))


# --------------------------------------------------------------------------- changes


@dataclass(frozen=True, slots=True)
class CurrentSeason:
    id: UUID
    name: str
    kind: str
    number: int
    membership_hash: str
    retired: bool
    metadata: dict[str, Any]
    episodes: tuple[str, ...] = ()


async def load_current(db: Any, library_id: UUID) -> dict[UUID, CurrentSeason]:
    rows = await db.fetch(
        "SELECT id, name, kind, season, membership_hash, retired_at, metadata"
        " FROM series_seasons WHERE library_id = $1",
        uid(library_id),
    )
    eps: dict[str, list[str]] = defaultdict(list)
    for e in await db.fetch(
        "SELECT ss.id AS sid, e.video_id AS vid FROM series_episodes e"
        " JOIN series_seasons ss ON ss.series_id = e.series_id AND ss.season = e.season"
        " WHERE ss.library_id = $1 ORDER BY ss.id, e.episode, e.video_id",
        uid(library_id),
    ):
        eps[str(e["sid"])].append(str(e["vid"]))
    return {
        UUID(str(r["id"])): CurrentSeason(
            id=UUID(str(r["id"])),
            name=str(r["name"]),
            kind=str(r["kind"]),
            number=int(r["season"]),
            membership_hash=str(r["membership_hash"]),
            retired=r["retired_at"] is not None,
            metadata=loads(r["metadata"]) or {},
            episodes=tuple(eps.get(str(r["id"]), ())),
        )
        for r in rows
    }


def compute_changes(
    plan: LibraryPlan, current: dict[UUID, CurrentSeason]
) -> tuple[ChangeSet, list[tuple[str, UUID]]]:
    """Diff the plan against the stored seasons.

    Returns the change set and the subjects to propagate.

    The second value is every season whose membership changed, is new (or revived), or retired:
    exactly what the ``seasons_changed`` edges fan out to.
    """
    added: list[dict[str, Any]] = []
    changed: list[dict[str, Any]] = []
    removed: list[dict[str, Any]] = []
    touched: list[tuple[str, UUID]] = []
    planned_ids = {s.id for s in plan.seasons}
    for s in plan.seasons:
        cur = current.get(s.id)
        if cur is None or cur.retired:
            added.append(
                {
                    "season_id": str(s.id),
                    "name": s.series_name,
                    "kind": s.kind,
                    "number": s.number,
                    "episodes": len(s.episodes),
                }
            )
            touched.append(("season", s.id))
        elif cur.membership_hash != s.membership_hash:
            old, new = list(cur.episodes), [str(e) for e in s.episodes]
            old_set, new_set = set(old), set(new)  # once per season, not per episode
            changed.append(
                {
                    "season_id": str(s.id),
                    "name": s.series_name,
                    "from": {"episodes": len(old), "kind": cur.kind},
                    "to": {"episodes": len(new), "kind": s.kind},
                    "episodes_added": [e for e in new if e not in old_set][:20],
                    "episodes_removed": [e for e in old if e not in new_set][:20],
                    "reordered": [e for e in old if e in new_set]
                    != [e for e in new if e in old_set],
                }
            )
            touched.append(("season", s.id))
    for sid, cur in current.items():
        if sid not in planned_ids and not cur.retired:
            removed.append(
                {
                    "season_id": str(sid),
                    "name": cur.name,
                    "number": cur.number,
                    "episodes": len(cur.episodes),
                }
            )
            touched.append(("season", sid))
    cs = ChangeSet(
        "detect",
        added=tuple(added),
        removed=tuple(removed),
        changed=tuple(changed),
        meta={
            "seasons": len(plan.seasons),
            "episodes": plan.episode_count,
            "unassigned": len(plan.unassigned),
            "shorts": len(plan.shorts),
        },
    )
    return cs, touched


# --------------------------------------------------------------------------- applying

_CHUNK = 400


async def _delete_in(db: Any, table: str, col: str, ids: Sequence[str]) -> None:
    for i in range(0, len(ids), _CHUNK):
        chunk = ids[i : i + _CHUNK]
        marks = ",".join(f"${n + 1}" for n in range(len(chunk)))
        await db.execute(f"DELETE FROM {table} WHERE {col} IN ({marks})", *chunk)


async def apply_changes(
    db: Any,
    library_id: UUID,
    plan: LibraryPlan,
    current: dict[UUID, CurrentSeason],
) -> None:
    """Write the plan: series, seasons, episodes, retirements. One transaction."""
    t = to_ts(db, now())
    async with db.transaction():
        seen_series: set[UUID] = set()
        for s in plan.seasons:
            if s.series_id not in seen_series:
                seen_series.add(s.series_id)
                await db.execute(
                    "INSERT INTO series (id, library_id, name, year, metadata, created_at, updated_at)"
                    " VALUES ($1,$2,$3,$4,$5,$6,$6)"
                    " ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, year = EXCLUDED.year,"
                    " metadata = EXCLUDED.metadata, library_id = EXCLUDED.library_id,"
                    " updated_at = EXCLUDED.updated_at",
                    uid(s.series_id),
                    uid(library_id),
                    s.series_name,
                    s.first_air.year if s.first_air else None,
                    dumps(
                        {
                            "series_key": s.series_key,
                            "kind": s.kind,
                            "brand": s.brand,
                            "source": "detector",
                        }
                    ),
                    t,
                )
        for s in plan.seasons:
            old = current.get(s.id)
            meta: dict[str, Any] = {
                "playlists": list(s.playlists),
                "extras": [str(x) for x in s.extras],
                "attached": [str(x) for x in s.attached],
                "duplicates": [str(x) for x in s.duplicates],
                "fp_plan": s.fp_plan,
                "season_name": s.season_name,
                "season_year": s.season_year,
                "brand": s.brand,
            }
            if old is not None and "signature" in old.metadata and s.fp_plan is not None:
                meta["signature"] = old.metadata[
                    "signature"
                ]  # decided by fp_gate; survives re-detect
            await db.execute(
                "INSERT INTO series_seasons (id, series_id, library_id, season, kind, name,"
                " first_air, last_air, median_duration_s, episode_count, membership_hash,"
                " detector_version, metadata, retired_at, created_at, updated_at)"
                " VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,NULL,$14,$14)"
                " ON CONFLICT (id) DO UPDATE SET series_id = EXCLUDED.series_id, kind = EXCLUDED.kind,"
                " season = EXCLUDED.season, name = EXCLUDED.name, first_air = EXCLUDED.first_air,"
                " last_air = EXCLUDED.last_air, median_duration_s = EXCLUDED.median_duration_s,"
                " episode_count = EXCLUDED.episode_count, membership_hash = EXCLUDED.membership_hash,"
                " detector_version = EXCLUDED.detector_version, metadata = EXCLUDED.metadata,"
                " retired_at = NULL, updated_at = EXCLUDED.updated_at",
                uid(s.id),
                uid(s.series_id),
                uid(library_id),
                s.number,
                s.kind,
                s.series_name,
                to_date(db, s.first_air),
                to_date(db, s.last_air),
                s.median_duration_s,
                len(s.episodes),
                s.membership_hash,
                DETECTOR_VERSION,
                dumps(meta),
                t,
            )
        # episodes: one row per placed video; user overrides (season_override /
        # episode_override) are not in the SET list, so they survive
        placed: dict[str, tuple[UUID, int, int]] = {}
        for s in plan.seasons:
            for vid, num in zip(s.episodes, s.numbers, strict=True):
                placed[str(vid)] = (s.series_id, s.number, num)
        # a row the user placed by hand (an override) is never removed: detection not
        # placing it again is no reason to undo the user's edit
        existing = {
            str(r["video_id"])
            for r in await db.fetch(
                "SELECT e.video_id AS video_id FROM series_episodes e"
                " JOIN videos v ON v.id = e.video_id WHERE v.library_id = $1"
                " AND e.season_override IS NULL AND e.episode_override IS NULL",
                uid(library_id),
            )
        }
        await _delete_in(db, "series_episodes", "video_id", sorted(existing - set(placed)))
        for placed_vid, (series_id, season_no, num) in placed.items():
            await db.execute(
                "INSERT INTO series_episodes (series_id, video_id, season, episode, created_at)"
                " VALUES ($1,$2,$3,$4,$5) ON CONFLICT (video_id) DO UPDATE SET"
                " series_id = EXCLUDED.series_id, season = EXCLUDED.season,"
                " episode = EXCLUDED.episode",
                uid(series_id),
                placed_vid,
                season_no,
                num,
                t,
            )
        planned_ids = {s.id for s in plan.seasons}
        for sid, cur in current.items():
            if sid not in planned_ids and not cur.retired:
                await db.execute(
                    "UPDATE series_seasons SET retired_at = $2, updated_at = $2 WHERE id = $1",
                    uid(sid),
                    t,
                )


# --------------------------------------------------------------------------- stage


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: every input the detector reads, as hashes - no NAS access.

    Video identity and titles, the latest successful output hash of ``probe`` /
    ``thumb_hash`` / ``link`` / ``fingerprint`` per video, the library's last
    ``youtube_sync`` output, and any user season / episode overrides.
    """
    lib = uid(subject.id)
    videos = await db.fetch(
        "SELECT id, title, filename FROM videos WHERE library_id = $1 AND deleted_at IS NULL"
        " ORDER BY id",
        lib,
    )
    signals = await db.fetch(
        "SELECT j.subject_id AS vid, j.stage AS stage, j.output_hash AS h FROM processing_jobs j"
        " WHERE j.library_id = $1 AND j.subject_kind = 'video'"
        " AND j.stage IN ('probe','thumb_hash','link','fingerprint')"
        f" AND j.state = 'done' AND j.outcome IN {SUCCESS}"
        " AND j.id = (SELECT max(k.id) FROM processing_jobs k WHERE k.subject_kind = 'video'"
        "   AND k.subject_id = j.subject_id AND k.stage = j.stage AND k.state = 'done'"
        f"   AND k.outcome IN {SUCCESS}) ORDER BY j.subject_id, j.stage",
        lib,
    )
    sync = await db.fetchrow(
        "SELECT output_hash FROM processing_jobs WHERE subject_kind = 'library' AND subject_id = $1"
        f" AND stage = 'youtube_sync' AND state = 'done' AND outcome IN {SUCCESS}"
        " ORDER BY id DESC LIMIT 1",
        lib,
    )
    overrides = await db.fetch(
        "SELECT e.video_id AS vid, e.season_override AS so, e.episode_override AS eo"
        " FROM series_episodes e JOIN videos v ON v.id = e.video_id"
        " WHERE v.library_id = $1 AND (e.season_override IS NOT NULL OR e.episode_override IS NOT NULL)"
        " ORDER BY e.video_id",
        lib,
    )
    return canonical_hash(
        {
            "videos": [[str(r["id"]), r["title"], r["filename"]] for r in videos],
            "signals": [[str(r["vid"]), r["stage"], r["h"]] for r in signals],
            "sync": None if sync is None else sync["output_hash"],
            "overrides": [[str(r["vid"]), r["so"], r["eo"]] for r in overrides],
            # the rules as set: a changed detection setting detects again
            "rules": settings.to_json((await read_rules(db, "detect"))[0]),
            "v": DETECTOR_VERSION,
        }
    )


@dataclass
class _Progress:
    done: int = 0
    total: int = 0
    seen: list[tuple[int, int]] = field(default_factory=list)

    def __call__(self, done: int, total: int) -> None:
        self.done, self.total = done, total


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id = job.subject.id
    rows = await load_rows(db, library_id)
    playlists = await _playlists(db, library_id)
    prog = _Progress()
    task = asyncio.create_task(_report_progress(ctx, prog))
    try:
        detect_cfg = await ctx.rules("detect")
        plan = await asyncio.to_thread(
            plan_library, library_id, rows, playlists, config=detect_cfg, progress=prog
        )
    finally:
        task.cancel()
    current = await load_current(db, library_id)
    changes, touched = compute_changes(plan, current)

    async def apply(_cs: ChangeSet) -> None:
        await apply_changes(db, library_id, plan, current)

    if changes.is_empty:
        # nothing visible changed, but dates / fingerprint plans / metadata may still have:
        # write silently so stored state matches the plan
        await apply_changes(db, library_id, plan, current)
        mode = "noop"
    else:
        mode = await apply_or_review(ctx, changes, apply)
    if mode == "review":
        return StageResult(
            output_hash=canonical_hash("review"),
            outcome="awaiting_review",
            summary={"changes": changes.counts},
        )

    rowmap = {r.id: r for r in rows}
    requests = _fingerprint_requests(plan, rowmap)
    # what downstream stages read of a season: its members, and also its name, kind,
    # dates and length (a corrected air date or season name re-plans the channels even
    # when no episode moved)
    out = canonical_hash(
        sorted(
            [
                str(s.id), s.membership_hash, s.series_name, s.kind, s.season_name,
                s.season_year, s.brand, s.first_air.isoformat() if s.first_air else None,
                s.last_air.isoformat() if s.last_air else None,
                round(s.median_duration_s, 1) if s.median_duration_s is not None else None,
            ]
            for s in plan.seasons
        )
    )  # fmt: skip
    return StageResult(
        output_hash=out,
        changed_subjects=tuple(touched),  # type: ignore[arg-type]
        requests=tuple(requests),
        summary={
            "placed": plan.episode_count,
            "seasons": len(plan.seasons),
            "seasons_changed": len(touched),
            "unassigned": len(plan.unassigned),
            "shorts": len(plan.shorts),
            "changes": changes.counts,
        },
    )


async def _report_progress(ctx: StageContext, prog: _Progress) -> None:
    try:
        while True:
            await asyncio.sleep(2.0)
            if prog.total:
                await ctx.progress("matching", prog.done, prog.total, force=True)
    except asyncio.CancelledError:
        return


def _fingerprint_requests(plan: LibraryPlan, rows: dict[UUID, Row]) -> list[Request]:
    """Pass 1 of the two-pass flow: sample fingerprints per season, then the gate."""
    samples: dict[UUID, set[str]] = defaultdict(set)
    gates: list[Request] = []
    for s in plan.seasons:
        if not s.fp_plan or not s.fp_plan["candidates"]:
            continue
        for v in s.fp_plan["samples"]:
            row = rows.get(UUID(v))
            if row is not None and not row.fingerprinted:
                samples[UUID(v)].add(str(s.id))
        gates.append(Request("fp_gate", "season", s.id, f"unplaced candidates for {s.series_name}"))
    reqs = [
        Request(
            "fingerprint",
            "video",
            vid,
            "sample of a season with unplaced videos",
            payload={"role": "sample", "seasons": sorted(seasons)},
        )
        for vid, seasons in sorted(samples.items(), key=lambda kv: str(kv[0]))
    ]
    return reqs + gates


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
