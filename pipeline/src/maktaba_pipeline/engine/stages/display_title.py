"""``display_title``: a short, clean on-air title for every video of a library (Story 27.11).

Subject: a library. Runs after ``detect`` (the seasons and their episode order are what
the episode number comes from). For every video the rules of
:mod:`maktaba_pipeline.classify.display_title` (settings ``rules.display_titles``) make
``videos.display_title`` from its library title: the channel's and presenter's names
(:func:`~maktaba_pipeline.classify.topic_tags.branding` of the library's YouTube channels),
its series' name and the phrases most of the library's or the series' titles share are
dropped with hashtags, emojis, links, dates and file leftovers. Each series gets
``series.display_name`` the same way.

With ``use_llm`` the local language model (the server and model of ``rules.tagcheck``,
batched, ``llm_batch`` titles a question) rewrites what the rules leave long or in several
parts; each answer is cached in ``display_title_llm`` by its input hash, so a title is
asked once and a re-run asks nothing. A title set by hand (``display_title_manual``) is
never touched.

When a display title changes, the future blocks of every channel that airs the video get
the new title in their ``title_snapshot`` at once (the guide, XMLTV and the strip read only
the snapshot), so no re-plan is needed.
"""

from __future__ import annotations

import asyncio
import json
import urllib.error
from collections import defaultdict
from typing import Any
from uuid import UUID

from ...channels.display import display_info, merge_snapshot
from ...classify import display_title as dt
from ...classify import tag_check as tc
from ...db.jobs import Job
from ...rules import read as read_rules
from ...settings import to_json
from ..context import StageContext
from ..graph import StageResult, Subject
from ..hashing import canonical_hash
from ..sql import loads, now, to_ts, uid
from ._leftovers import library_name
from .registry import StageImpl

__all__ = ["IMPL", "compute", "input_fingerprint", "run"]

_VIDEOS_SQL = """
SELECT v.id AS id, COALESCE(yv.title, v.title, v.filename) AS raw,
       v.display_title AS cur, v.display_title_source AS src,
       v.display_title_manual AS manual,
       se.series_id AS sid,
       COALESCE(se.episode_override, se.episode, se.absolute_number) AS ep
  FROM videos v
  LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'
  LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id
  LEFT JOIN series_episodes se ON se.video_id = v.id
 WHERE v.library_id = $1 AND v.deleted_at IS NULL
 ORDER BY v.id
"""
_SERIES_SQL = (
    "SELECT id, COALESCE(name_override, name) AS name, display_name FROM series"
    " WHERE library_id = $1 ORDER BY id"
)


async def _channel_names(db: Any, library_id: UUID) -> list[str]:
    rows = await db.fetch(
        "SELECT title FROM youtube_channels WHERE library_id = $1 AND title IS NOT NULL"
        " ORDER BY channel_id",
        uid(library_id),
    )
    names = [str(r["title"]) for r in rows][:3]
    lib = await library_name(db, library_id)
    if lib and lib not in names:
        names.append(lib)
    return names


def compute(
    videos: list[Any],
    series: dict[str, str],
    channels: list[str],
    cfg: dt.DisplayTitleConfig,
) -> tuple[dict[str, dt.Cleaned], dict[str, str]]:
    """The rules' title of every video (by id) and display name of every series. Pure."""
    lib_boiler = dt.title_boilerplate((str(v["raw"] or "") for v in videos), cfg)
    by_series: dict[str, list[str]] = defaultdict(list)
    for v in videos:
        if v["sid"] is not None:
            by_series[str(v["sid"])].append(str(v["raw"] or ""))
    series_boiler = {s: dt.title_boilerplate(ts, cfg) for s, ts in by_series.items()}
    base_brands = dt.brand_phrases(channels)
    names = {
        sid: dt.clean_series_name(name, cfg, brands=base_brands) for sid, name in series.items()
    }
    out: dict[str, dt.Cleaned] = {}
    for v in videos:
        sid = None if v["sid"] is None else str(v["sid"])
        sname = series.get(sid, "") if sid else ""
        brands = dt.brand_phrases(channels, [sname, names.get(sid, "")] if sid else [])
        boiler = lib_boiler | series_boiler.get(sid or "", frozenset())
        c = dt.clean_title(str(v["raw"] or ""), cfg, brands=brands, boilerplate=boiler)
        ep = v["ep"]
        if ep is not None and cfg.keep_episode_number:
            c = dt.Cleaned(c.title, int(ep), c.segments)
        out[str(v["id"])] = c
    return out, names


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: the library's titles, series and episode order, channel names, the rules
    (and the model when it is asked), the version."""
    cfg: dt.DisplayTitleConfig = (await read_rules(db, "display_titles"))[0]
    lib = uid(subject.id)
    vids = await db.fetch(_VIDEOS_SQL, lib)
    ser = await db.fetch(_SERIES_SQL, lib)
    model = None
    if cfg.use_llm:
        tcfg: tc.TagCheckConfig = (await read_rules(db, "tagcheck"))[0]
        model = [tcfg.url, tcfg.model]
    return canonical_hash(
        {
            "v": dt.DISPLAY_TITLE_VERSION,
            "videos": [
                [str(r["id"]), str(r["raw"]), str(r["sid"]), r["ep"], r["manual"]] for r in vids
            ],
            "series": [[str(r["id"]), str(r["name"])] for r in ser],
            "channels": await _channel_names(db, subject.id),
            "rules": to_json(cfg),
            "model": model,
        }
    )


async def _llm(
    db: Any,
    ctx: StageContext,
    todo: list[tuple[str, str, str]],
    cfg: dt.DisplayTitleConfig,
    counts: dict[str, int],
) -> dict[str, str]:
    """The model's titles for ``(video id, library title, series)``, cached by input."""
    tcfg: tc.TagCheckConfig = await ctx.rules("tagcheck")
    hashes = {vid: dt.llm_input_hash(raw, s, tcfg.model, cfg) for vid, raw, s in todo}
    cached: dict[str, str] = {}
    for h in sorted(set(hashes.values())):
        row = await db.fetchrow("SELECT title FROM display_title_llm WHERE input_hash = $1", h)
        if row is not None:
            cached[h] = str(row["title"])
    out = {vid: cached[h] for vid, h in hashes.items() if h in cached}
    counts["llm_cached"] += len(out)
    ask = [t for t in todo if hashes[t[0]] not in cached]
    size = max(1, cfg.llm_batch)
    misses = 0
    for b in range(0, len(ask), size):
        await ctx.check_cancel()
        batch = ask[b : b + size]
        await ctx.progress("ask", b // size, (len(ask) + size - 1) // size, "display titles")
        try:
            answer = await asyncio.to_thread(
                tc.ask, dt.llm_prompt([(raw, s) for _, raw, s in batch], cfg), tcfg
            )
        except (OSError, urllib.error.URLError, KeyError, ValueError) as e:
            counts["llm_failed"] += len(batch)
            misses += 1
            if misses >= 3:
                await ctx.log("warn", f"the model is not answering ({e}); rules only")
                break
            continue
        misses = 0
        got = dt.parse_llm(answer, len(batch), cfg)
        for i, (vid, _raw, _s) in enumerate(batch, 1):
            if i not in got:
                counts["llm_failed"] += 1
                continue
            counts["llm_asked"] += 1
            out[vid] = got[i]
            await db.execute(
                "INSERT INTO display_title_llm (input_hash, model, title, created_at)"
                " VALUES ($1,$2,$3,$4) ON CONFLICT (input_hash) DO UPDATE SET"
                " title = EXCLUDED.title, model = EXCLUDED.model",
                hashes[vid],
                tcfg.model,
                got[i],
                to_ts(db, now()),
            )
    return out


async def refresh_snapshots(db: Any, library_id: UUID, cfg: dt.DisplayTitleConfig) -> int:
    """Lay the display titles (a hand-set one first) over the future blocks that air the
    library's videos; only blocks whose snapshot changes are written."""
    rows = await db.fetch(
        "SELECT cp.id AS id, cp.video_id AS vid, cp.title_snapshot AS snap"
        " FROM channel_programs cp JOIN videos v ON v.id = cp.video_id"
        " WHERE cp.end_at > $1 AND v.library_id = $2",
        to_ts(db, now()),
        uid(library_id),
    )
    info = await display_info(db, sorted({str(r["vid"]) for r in rows}))
    n = 0
    for r in rows:
        vid = str(r["vid"])
        old = loads(r["snap"]) or {}
        if not isinstance(old, dict):
            old = {}
        new = merge_snapshot(old, info.get(vid), cfg)
        if new != old:
            await db.execute(
                "UPDATE channel_programs SET title_snapshot = $2 WHERE id = $1",
                r["id"],
                json.dumps(new, ensure_ascii=False),
            )
            n += 1
    return n


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id: UUID = job.subject.id
    cfg: dt.DisplayTitleConfig = await ctx.rules("display_titles")
    lib = uid(library_id)
    if await library_name(db, library_id) is None:
        return StageResult(output_hash=canonical_hash({"library": None}), summary={"gone": True})
    videos = list(await db.fetch(_VIDEOS_SQL, lib))
    series_rows = list(await db.fetch(_SERIES_SQL, lib))
    series = {str(r["id"]): str(r["name"] or "") for r in series_rows}
    counts = {"videos": len(videos), "changed": 0, "series_changed": 0, "llm_asked": 0,
              "llm_cached": 0, "llm_failed": 0, "blocks": 0}  # fmt: skip
    if not cfg.enabled:
        return StageResult(output_hash=canonical_hash({"off": True}), summary={"enabled": False})
    cleaned, names = compute(videos, series, await _channel_names(db, library_id), cfg)
    titles = {vid: (c.title, "rules") for vid, c in cleaned.items()}
    if cfg.use_llm:
        raw = {str(v["id"]): str(v["raw"] or "") for v in videos}
        sname = {str(v["id"]): names.get(str(v["sid"]), "") for v in videos if v["sid"]}
        todo = [
            (vid, raw[vid], sname.get(vid, ""))
            for vid, c in cleaned.items()
            if dt.needs_llm(raw[vid], c, cfg)
        ]
        for vid, t in (await _llm(db, ctx, todo, cfg, counts)).items():
            titles[vid] = (t, "llm")
    changed: list[str] = []
    for v in videos:
        vid = str(v["id"])
        t, src = titles[vid]
        if v["cur"] != t or v["src"] != src:
            await db.execute(
                "UPDATE videos SET display_title = $2, display_title_source = $3 WHERE id = $1",
                vid,
                t,
                src,
            )
            changed.append(vid)
    for r in series_rows:
        sid = str(r["id"])
        if r["display_name"] != names.get(sid):
            await db.execute("UPDATE series SET display_name = $2 WHERE id = $1", sid, names[sid])
            counts["series_changed"] += 1
            changed.extend(str(v["id"]) for v in videos if str(v["sid"]) == sid)
    changed = sorted(set(changed))
    counts["changed"] = len(changed)
    counts["blocks"] = await refresh_snapshots(db, library_id, cfg)
    return StageResult(
        output_hash=canonical_hash(sorted([k, *v] for k, v in titles.items())),
        summary=counts,
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
