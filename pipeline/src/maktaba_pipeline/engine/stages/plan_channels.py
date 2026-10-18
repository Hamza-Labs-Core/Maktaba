"""``plan_channels``: suggest live TV channels and their time-slot grids (plan 06-11 section 3.2).

Subject: the single global ``catalog``. Reads what ``detect`` / ``tag`` / ``rate_style`` stored
(seasons, episodes, tags, production style, loose videos, clip groups, promo links) and
runs the planner through its one public entry point,
:func:`maktaba_pipeline.schedule.entry.plan_catalog` (the hook a new planner replaces;
plan 06-11 section 17.6), ported from the prototype's ``plan_channels.load_inputs``.
The result is one ``channel_plans`` row (the latest wins;
20 are kept).

* ``compute_changes`` diffs the new lineup against the previous plan (channels added, removed,
  changed) - exactly what a viewer of the history wants to see;
* ``apply_changes`` stores the plan;
* an unchanged lineup writes nothing and propagates nothing.

Extensions can add per-channel data to the plan (for example dizqueTV time slots) through
:func:`register_plan_exporter`; the core knows nothing about what they export.
"""

from __future__ import annotations

import asyncio
import posixpath
from collections import Counter, defaultdict
from collections.abc import Mapping
from datetime import date
from typing import Any
from uuid import UUID

from ...classify.series_detector import VideoFacts
from ...classify.series_detector import is_short as detector_is_short
from ...db.jobs import Job
from ...rules import read as read_rules
from ...schedule.channel_planner import (
    PLANNER_VERSION,
    EpisodeRef,
    LooseVideo,
    SeasonCandidate,
)
from ...schedule.entry import plan_catalog
from ..changes import ChangeSet, apply_or_review
from ..context import StageContext
from ..graph import StageResult, Subject
from ..hashing import canonical_hash
from ..sql import dumps, loads, now, to_ts
from ._plan_json import PlanExporter, build_plan_json, config_json, strip_volatile
from .registry import StageImpl

__all__ = [
    "IMPL",
    "compute_changes",
    "load_inputs",
    "plan_exporters",
    "register_plan_exporter",
    "reset_plan_exporters",
]

SUCCESS = "('changed','unchanged','skipped_unchanged','imported')"
_EXPORTERS: dict[str, PlanExporter] = {}


def register_plan_exporter(name: str, fn: PlanExporter) -> None:
    """Let an extension add ``channels[i]["exports"][name]`` to every stored plan."""
    _EXPORTERS[name] = fn


def reset_plan_exporters() -> None:
    _EXPORTERS.clear()


def plan_exporters() -> Mapping[str, PlanExporter]:
    return dict(_EXPORTERS)


def _iso(v: Any) -> str | None:
    if v is None:
        return None
    return v.isoformat() if isinstance(v, date) else str(v)[:10]


# ------------------------------------------------------------------------------ inputs

_EPISODES_SQL = """
SELECT ss.id AS season_id, v.id AS vid, v.path AS path,
       COALESCE(yv.title, v.title, v.filename) AS title,
       COALESCE(v.duration_sec, yv.duration_s) AS dur, yv.upload_date AS upload_date,
       yv.date_quality AS date_quality,
       e.season AS season, e.episode AS episode, yc.title AS channel
  FROM series_episodes e
  JOIN series_seasons ss ON ss.series_id = e.series_id
       AND ss.season = COALESCE(e.season_override, e.season) AND ss.retired_at IS NULL
  JOIN videos v ON v.id = e.video_id AND v.deleted_at IS NULL
  LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'
  LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id
  LEFT JOIN youtube_channels yc ON yc.channel_id = yv.channel_id
 ORDER BY ss.id, COALESCE(e.episode_override, e.episode), v.id
"""

_LOOSE_SQL = """
SELECT v.id AS vid, v.path AS path, COALESCE(yv.title, v.title, v.filename) AS title,
       COALESCE(v.duration_sec, yv.duration_s) AS dur, yv.upload_date AS upload_date,
       yv.tab AS tab, v.metadata AS metadata, yc.title AS channel
  FROM videos v
  LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'
  LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id
  LEFT JOIN youtube_channels yc ON yc.channel_id = yv.channel_id
 WHERE v.deleted_at IS NULL
   AND NOT EXISTS (SELECT 1 FROM series_episodes e WHERE e.video_id = v.id)
 ORDER BY v.id
"""


async def load_inputs(
    db: Any,
) -> tuple[list[SeasonCandidate], list[LooseVideo], list[str]]:
    """Seasons and loose videos from the database, plus notes on what was missing."""
    detect_cfg = (await read_rules(db, "detect"))[0]
    notes: list[str] = []
    tags: dict[str, dict[str, float]] = defaultdict(dict)
    for r in await db.fetch("SELECT season_id, tag, score FROM season_tags"):
        tags[str(r["season_id"])][str(r["tag"])] = float(r["score"])
    if not tags:
        notes.append("no season tags yet: run the tag stage (seasons are untagged)")
    style: dict[str, tuple[str, int, float]] = {}
    for r in await db.fetch(
        "SELECT season_id, style, tier, confidence, override_style FROM season_style"
    ):
        st = r["override_style"] or r["style"]
        if st is None:
            continue
        tier = r["tier"] if r["override_style"] is None else _TIERS.get(str(st), r["tier"])
        style[str(r["season_id"])] = (
            str(st),
            int(tier if tier is not None else 1),
            float(r["confidence"] or 0.0),
        )
    if not style:
        notes.append("no season styles yet: production tier unknown for every season")

    # an episode's own tags, from its title and chapter playlist (the tag stage)
    ep_tags: dict[str, dict[str, float]] = defaultdict(dict)
    for r in await db.fetch("SELECT video_id, tag, score FROM episode_tags"):
        ep_tags[str(r["video_id"])][str(r["tag"])] = float(r["score"])
    eps: dict[str, list[Any]] = defaultdict(list)
    for r in await db.fetch(_EPISODES_SQL):
        eps[str(r["season_id"])].append(r)

    season_rows = await db.fetch(
        "SELECT ss.id AS id, ss.series_id AS series_id, ss.name AS name, ss.kind AS kind,"
        " ss.season AS season,"
        " ss.first_air AS first_air, ss.median_duration_s AS median, ss.metadata AS metadata,"
        " s.metadata AS series_meta, l.name AS library"
        " FROM series_seasons ss JOIN series s ON s.id = ss.series_id"
        " LEFT JOIN libraries l ON l.id = ss.library_id WHERE ss.retired_at IS NULL ORDER BY ss.id"
    )
    hidden: set[str] = set()
    seasons: list[SeasonCandidate] = []
    show_key: dict[str, str] = {}  # series id -> "<folder>|<series key>", as lanes name shows
    for r in season_rows:
        sid = str(r["id"])
        meta = loads(r["metadata"]) or {}
        hidden.update(str(x) for x in meta.get("extras") or [])
        hidden.update(str(x) for x in meta.get("duplicates") or [])
        stags = tags.get(sid, {})
        genre = next((t[6:] for t in sorted(stags) if t.startswith("genre:")), "general")
        st = style.get(sid)
        rows = eps.get(sid, [])
        folder = posixpath.dirname(str(rows[0]["path"]).replace("\\", "/")) if rows else ""
        channel = Counter(str(x["channel"]) for x in rows if x["channel"]).most_common(1)
        presenter = (
            channel[0][0] if channel else (posixpath.basename(folder) or str(r["library"] or ""))
        )
        first_air = _iso(r["first_air"])
        series_meta = loads(r["series_meta"]) or {}
        show_key.setdefault(
            str(r["series_id"]), f"{folder}|{series_meta.get('series_key') or r['name']}"
        )
        episodes = tuple(
            EpisodeRef(
                path=str(x["vid"]),
                duration_s=float(x["dur"]),
                title=str(x["title"] or ""),
                season=None if x["season"] is None else int(x["season"]),
                episode=None if x["episode"] is None else int(x["episode"]),
                air_date=_iso(x["upload_date"]),
                # the grid reads a show's cadence (daily strip, weekly) from exact dates only
                date_exact=x["date_quality"] == "exact",
                tags=ep_tags.get(str(x["vid"]), {}),
            )
            for x in rows
            if x["dur"]
        )
        seasons.append(
            SeasonCandidate(
                series_id=sid,
                name=str(r["name"]),
                season=int(r["season"]),
                kind=str(r["kind"]),
                series_key=str(series_meta.get("series_key") or r["name"]),
                folder=folder,
                presenter=presenter,
                library=str(r["library"] or ""),
                year=int(first_air[:4]) if first_air else None,
                first_air=first_air,
                median_duration_s=None if r["median"] is None else float(r["median"]),
                tags=stags,
                genre=genre,
                style=st[0] if st else None,
                tier=st[1] if st else None,
                style_confidence=st[2] if st else 0.0,
                episodes=episodes,
                # the TV grid keeps a title-brand talk show in one slot, named seasons
                # as its guests
                brand=bool(meta.get("brand") or series_meta.get("brand")),
                season_name=meta.get("season_name") or None,
            )
        )

    # the clip_groups stage's groups (filler by topic) and the promo_links stage's links
    group_of: dict[str, tuple[str, str | None]] = {
        str(r["vid"]): (str(r["title"]), None if r["topic"] is None else str(r["topic"]))
        for r in await db.fetch(
            "SELECT i.video_id AS vid, g.title AS title, g.topic AS topic"
            " FROM clip_group_items i JOIN clip_groups g ON g.id = i.group_id"
        )
    }
    promo_of = {
        str(r["vid"]): show_key[str(r["series_id"])]
        for r in await db.fetch("SELECT video_id AS vid, series_id FROM promo_links")
        if str(r["series_id"]) in show_key
    }
    vtags: dict[str, dict[str, float]] = defaultdict(dict)
    for r in await db.fetch("SELECT video_id, tag, score FROM video_topic_tags"):
        vtags[str(r["video_id"])][str(r["tag"])] = float(r["score"])
    loose: list[LooseVideo] = []
    for r in await db.fetch(_LOOSE_SQL):
        vid = str(r["vid"])
        if not r["dur"] or (vid in hidden and vid not in promo_of):
            continue  # a promo linked to its show airs next to it; other extras never air
        meta = loads(r["metadata"]) or {}
        w, h = meta.get("width"), meta.get("height")
        vertical = bool(w and h and h > w)
        folder = posixpath.dirname(str(r["path"]).replace("\\", "/"))
        t = dict(vtags.get(vid, {}))
        group, group_topic = group_of.get(vid, ("", None))
        if group_topic and not any(x.startswith("topic:") for x in t):
            # an untagged clip takes its group's topic, so on-topic pools can use it
            t[f"topic:{group_topic}"] = 0.5
            t[f"genre:{group_topic}"] = 0.5
        facts = VideoFacts(
            key=vid,
            title=str(r["title"]),
            duration_s=float(r["dur"]),
            is_short=r["tab"] == "shorts" or vertical,
        )
        loose.append(
            LooseVideo(
                path=vid,
                duration_s=float(r["dur"]),
                title=str(r["title"] or ""),
                folder=folder,
                presenter=str(r["channel"] or posixpath.basename(folder)),
                tags=t,
                genre=next((x[6:] for x in sorted(t) if x.startswith("genre:")), "general"),
                air_date=_iso(r["upload_date"]),
                is_short=detector_is_short(facts, detect_cfg),
                vertical=vertical,
                group=group,
                promo_of=promo_of.get(vid, ""),
            )
        )
    return seasons, loose, notes


_TIERS = {"static": 0, "talk": 1, "produced": 2, "cinematic": 3}


# ------------------------------------------------------------------------------ changes


def _channel_digest(ch: Mapping[str, Any]) -> str:
    return canonical_hash({k: v for k, v in ch.items() if k not in ("sample_day", "stats")})


def compute_changes(old: Mapping[str, Any] | None, new: Mapping[str, Any]) -> ChangeSet:
    """Channels added / removed / changed between two plans."""
    o = {c["key"]: c for c in (old or {}).get("channels", [])}
    n = {c["key"]: c for c in new.get("channels", [])}
    added = tuple(
        {"key": k, "number": n[k]["number"], "name": n[k]["name"], "hours": n[k]["hours"]}
        for k in n
        if k not in o
    )
    removed = tuple(
        {"key": k, "number": o[k]["number"], "name": o[k]["name"]} for k in o if k not in n
    )
    changed = tuple(
        {
            "key": k,
            "name": n[k]["name"],
            "number": [o[k]["number"], n[k]["number"]],
            "hours": [o[k]["hours"], n[k]["hours"]],
            "lanes": {
                "added": sorted(set(n[k]["lanes"]) - set(o[k]["lanes"])),
                "removed": sorted(set(o[k]["lanes"]) - set(n[k]["lanes"])),
            },
        }
        for k in n
        if k in o and _channel_digest(o[k]) != _channel_digest(n[k])
    )
    return ChangeSet(
        "plan_channels",
        added,
        removed,
        changed,
        (),
        {"channels": len(n), "lanes": len(new.get("lanes", {}))},
    )


# ------------------------------------------------------------------------------ stage


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """Hash of every latest ``detect`` / ``tag`` / ``rate_style`` output plus the planner config."""
    rows = await db.fetch(
        "SELECT j.stage AS stage, j.subject_id AS sid, j.output_hash AS h FROM processing_jobs j"
        " WHERE j.stage IN ('detect','tag','check_tags','check_clips','books','rate_style',"
        "'topic_collections','promo_links','clip_groups') AND j.state = 'done'"
        f" AND j.outcome IN {SUCCESS}"
        " AND j.id = (SELECT max(k.id) FROM processing_jobs k WHERE k.subject_kind = j.subject_kind"
        "  AND k.subject_id = j.subject_id AND k.stage = j.stage AND k.state = 'done'"
        f"  AND k.outcome IN {SUCCESS})"
        " ORDER BY j.stage, j.subject_id"
    )
    return canonical_hash(
        {
            "outputs": [[r["stage"], str(r["sid"]), r["h"]] for r in rows],
            # the rules as set: a changed planner setting plans the channels again
            "config": canonical_hash(config_json((await read_rules(db, "planner"))[0])),
            "exporters": sorted(_EXPORTERS),
            "v": PLANNER_VERSION,
        }
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    seasons, loose, notes = await load_inputs(db)
    exporters = plan_exporters()
    plan_input = await input_fingerprint(db, job.subject) or ""

    planner = await ctx.rules("planner")

    def build() -> dict[str, Any]:
        result = plan_catalog(seasons, loose, planner)
        return build_plan_json(result, source="catalog", notes=notes, exporters=exporters)

    plan = await asyncio.to_thread(build)
    plan_hash = canonical_hash(strip_volatile(plan))
    latest = await db.fetchrow(
        "SELECT plan, input_hash FROM channel_plans ORDER BY id DESC LIMIT 1"
    )
    old = None if latest is None else loads(latest["plan"])
    changes = compute_changes(old, plan)
    unchanged = old is not None and canonical_hash(strip_volatile(old)) == plan_hash

    async def apply(_cs: ChangeSet) -> None:
        await _store(db, plan, plan_input)

    if unchanged:
        return StageResult(
            output_hash=plan_hash,
            summary={"channels": len(plan["channels"]), "changes": changes.counts},
        )
    if changes.is_empty:
        await _store(db, plan, plan_input)  # same lineup, different detail: keep the newest plan
    else:
        mode = await apply_or_review(ctx, changes, apply)
        if mode == "review":
            return StageResult(output_hash=canonical_hash("review"), outcome="awaiting_review")
    return StageResult(
        output_hash=plan_hash,
        summary={
            "channels": len(plan["channels"]),
            "lanes": len(plan["lanes"]),
            "changes": changes.counts,
        },
    )


async def _store(db: Any, plan: Mapping[str, Any], input_hash: str) -> None:
    await db.execute(
        "INSERT INTO channel_plans (created_at, planner_version, input_hash, plan)"
        " VALUES ($1,$2,$3,$4)",
        to_ts(db, now()),
        PLANNER_VERSION,
        input_hash,
        dumps(plan),
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
_ = UUID
