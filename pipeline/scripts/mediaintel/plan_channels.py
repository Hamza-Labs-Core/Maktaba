"""plan-channels: suggest live TV channels and their time-slot grids.

Reads what ``detect`` / ``tag`` / ``scan-visual`` stored (series, episodes,
tags, production style, shorts, loose videos) and runs the pure planner
:mod:`maktaba_pipeline.schedule.channel_planner`. Writes, into ``out_dir``:

- ``channel-plan.json`` — machine-readable plan: channels (number, name,
  reasons, weekly slot grid, Ramadan grid, filler pools, a simulated sample
  day), lanes (ordered seasons → episodes with Plex rating keys and file
  paths), filler pools (items with rating keys and paths), and for every
  channel the Maktaba ``POST /api/channels`` body and the dizqueTV time-slot
  schedule (``export-dizquetv`` turns that into API calls);
- ``channel-plan.html`` — a review page (same look as ``report``).

Read-only: the cache is never written. Missing tables degrade: no
``series_tags``/``video_tags`` → untagged (run ``tag`` first for themed
channels); no ``series_style`` → no production-style tie-breaker (planning does not need it).
"""

from __future__ import annotations

import contextlib
import html
import json
import sqlite3
from collections import Counter, defaultdict
from collections.abc import Iterable, Mapping, Sequence
from dataclasses import asdict
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from maktaba_pipeline.integrations.dizquetv_export import filler_collections
from maktaba_pipeline.schedule.channel_planner import (
    DAY_MINUTES,
    WEEKDAYS,
    Airing,
    ChannelSuggestion,
    EpisodeRef,
    Lane,
    LooseVideo,
    PlannerConfig,
    PlanResult,
    SeasonCandidate,
    Slot,
    dizquetv_time_slots,
    fmt_hhmm,
    maktaba_channel_definition,
    simulate,
    slot_timeline,
    suggest_channels,
)
from maktaba_pipeline.text.arabic import fold

from .report import CSS as REPORT_CSS

PLAN_FORMAT = 1
SAMPLE_WEEKDAY = "fri"


# --------------------------------------------------------------------------- read


def _rows(conn: sqlite3.Connection, sql: str) -> sqlite3.Cursor:
    cur = conn.cursor()
    cur.row_factory = sqlite3.Row
    return cur.execute(sql)


def _tables(conn: sqlite3.Connection) -> set[str]:
    return {r[0] for r in conn.execute("SELECT name FROM sqlite_master WHERE type='table'")}


def _presenter(folder: str, channels: Mapping[str, str]) -> str:
    return channels.get(folder) or Path(folder.replace("\\", "/")).name


def load_inputs(
    conn: sqlite3.Connection,
) -> tuple[list[SeasonCandidate], list[LooseVideo], list[str]]:
    """Seasons and loose videos from the cache, plus notes on what was missing."""
    tables = _tables(conn)
    notes: list[str] = []
    channels = {
        r[0]: r[1] for r in conn.execute("SELECT folder, channel FROM folders") if r[1] is not None
    }
    files = {
        r[0]: r
        for r in _rows(
            conn,
            """SELECT f.path, f.folder, COALESCE(y.title, f.stem) AS title,
                      COALESCE(f.duration_s, y.duration_s) AS dur, f.width, f.height,
                      f.plex_rating_key, f.plex_library, y.upload_date, y.tab, y.date_quality
               FROM files f LEFT JOIN yt_videos y ON y.youtube_id = f.youtube_id""",
        )
    }
    series_tags: dict[str, dict[str, float]] = defaultdict(dict)
    video_tags: dict[str, dict[str, float]] = defaultdict(dict)
    if "series_tags" in tables:
        for sid, tag, score in conn.execute("SELECT series_id, tag, score FROM series_tags"):
            series_tags[sid][tag] = float(score)
    else:
        notes.append("no series_tags table: run `tag` first (seasons are untagged)")
    if "video_tags" in tables:
        for path, tag, score in conn.execute("SELECT path, tag, score FROM video_tags"):
            video_tags[path][tag] = float(score)
    else:
        notes.append("no video_tags table: filler and loose videos are untagged")
    episode_tags: dict[str, dict[str, float]] = defaultdict(dict)
    if "episode_tags" in tables:
        for path, tag, score in conn.execute("SELECT path, tag, score FROM episode_tags"):
            episode_tags[path][tag] = float(score)
    else:
        notes.append("no episode_tags table: themes take whole shows only (run `tag`)")
    style: dict[str, tuple[str, int, float]] = {}
    if "series_style" in tables:
        for sid, st, tier, conf in conn.execute(
            "SELECT series_id, style, tier, confidence FROM series_style"
        ):
            style[sid] = (str(st), int(tier), float(conf))
    else:
        notes.append("no series_style table: production tier unknown for every season")

    eps_by_series: dict[str, list[sqlite3.Row]] = defaultdict(list)
    for r in conn.execute(
        "SELECT series_id, path, season, episode FROM series_episodes "
        "ORDER BY series_id, season, episode"
    ):
        eps_by_series[r[0]].append(r)
    in_series = {r[1] for rs in eps_by_series.values() for r in rs}
    hidden: set[str] = set()  # promos / trailers / second uploads: never programmes or filler
    from . import settings_store

    promo_re = settings_store.get(conn, "groups").promo_re()
    promo_of: dict[str, str] = {}
    if "promo_links" in tables:
        promo_of = {
            p: f"{folder}|{key}"
            for p, folder, key in conn.execute(
                "SELECT path, series_folder, series_key FROM promo_links"
            )
        }
    # a promo inside a clip playlist is not one of its clips: linked, it airs next to
    # its show (a loose video with promo_of); unlinked, it never airs
    clip_promos: set[str] = set()
    seasons: list[SeasonCandidate] = []
    for sid, library, name, year, meta_raw in conn.execute(
        "SELECT id, library, name, year, metadata FROM series"
    ):
        meta = json.loads(meta_raw or "{}")
        hidden.update(meta.get("extras") or [])
        hidden.update(meta.get("duplicates") or [])
        tags = series_tags.get(sid, {})
        genre = next((t[6:] for t in tags if t.startswith("genre:")), "general")
        st = style.get(sid)
        folder = meta.get("folder") or ""
        eps: list[EpisodeRef] = []
        for r in eps_by_series.get(sid, []):
            f = files.get(r[1])
            if f is None or not f["dur"]:
                continue
            if meta.get("kind") == "clips" and (
                r[1] in promo_of or promo_re.search(fold(f["title"] or ""))
            ):
                clip_promos.add(r[1])
                continue
            eps.append(
                EpisodeRef(
                    path=r[1],
                    duration_s=float(f["dur"]),
                    title=f["title"] or "",
                    season=r[2],
                    episode=r[3],
                    plex_rating_key=f["plex_rating_key"],
                    plex_library=f["plex_library"],
                    air_date=f["upload_date"],
                    date_exact=f["date_quality"] == "exact",
                    tags=episode_tags.get(r[1], {}),
                )
            )
        seasons.append(
            SeasonCandidate(
                series_id=sid,
                name=name,
                season=int(meta.get("season") or 1),
                kind=meta.get("kind") or "episodic",
                series_key=meta.get("series_key") or name,
                folder=folder,
                presenter=_presenter(folder, channels) if folder else library,
                library=library,
                year=year,
                first_air=meta.get("first_air"),
                median_duration_s=meta.get("median_duration_s"),
                tags=tags,
                genre=genre,
                style=st[0] if st else None,
                tier=st[1] if st else None,
                style_confidence=st[2] if st else 0.0,
                episodes=tuple(eps),
                brand=bool(meta.get("brand")),
                season_name=meta.get("season_name"),
            )
        )
    shorts = {r[0] for r in conn.execute("SELECT path FROM shorts")}
    # the group step's clip / stand-alone groups (filler by topic) and promo links
    group_of: dict[str, tuple[str, str | None]] = {}
    in_series -= clip_promos
    if "clip_groups" in tables:
        from .group_clips import group_title

        for path, lib, kind, basis, gname, topic in conn.execute(
            """SELECT i.path, g.library, g.kind, g.basis, g.name, g.topic FROM clip_group_items i
               JOIN clip_groups g ON g.id = i.group_id"""
        ):
            group_of[path] = (group_title(lib, kind, basis, gname), topic)
    # a second file of a video (X.mkv beside X.mp4) is not a loose video
    with contextlib.suppress(sqlite3.OperationalError):
        hidden |= {r[0] for r in conn.execute("SELECT path FROM file_copies")}
    loose: list[LooseVideo] = []
    for path, f in files.items():
        if path in in_series or not f["dur"]:
            continue
        if path in hidden and path not in promo_of:
            continue  # a promo linked to its show airs next to it; other extras never air
        if path not in promo_of and promo_re.search(fold(f["title"] or "")):
            continue  # a promo whose show was not found advertises nothing on air
        tags = dict(video_tags.get(path, {}))
        group, group_topic = group_of.get(path, ("", None))
        if group_topic and not any(t.startswith("topic:") for t in tags):
            # an untagged clip takes its group's topic, so on-topic pools can use it
            tags[f"topic:{group_topic}"] = 0.5
            tags[f"genre:{group_topic}"] = 0.5
        vertical = bool(f["width"] and f["height"] and f["height"] > f["width"])
        loose.append(
            LooseVideo(
                path=path,
                duration_s=float(f["dur"]),
                title=f["title"] or "",
                folder=f["folder"],
                presenter=_presenter(f["folder"], channels),
                tags=tags,
                genre=next((t[6:] for t in tags if t.startswith("genre:")), "general"),
                air_date=f["upload_date"],
                plex_rating_key=f["plex_rating_key"],
                plex_library=f["plex_library"],
                is_short=path in shorts or f["tab"] == "shorts" or vertical,
                vertical=vertical,
                group=group,
                promo_of=promo_of.get(path, ""),
            )
        )
    return seasons, loose, notes


# --------------------------------------------------------------------------- JSON


def _slot_json(s: Slot, plan: PlanResult) -> dict[str, Any]:
    lane = plan.lanes.get(s.lane) if s.lane else None
    return {
        "days": list(s.days) or "daily",
        "start": fmt_hhmm(s.start),
        "end": fmt_hhmm(s.end) if s.end < DAY_MINUTES else "24:00",
        "minutes": s.minutes,
        "daypart": s.daypart,
        "kind": s.kind,
        "lane": s.lane,
        "lane_name": lane.name if lane else None,
        "show": lane.shows[0] if lane and lane.shows else None,
        "pattern": (lane.pattern or lane.special) if lane else None,
        "cadence": lane.cadence if lane else None,
        "filler_pool": s.filler_pool,
        "reasons": list(s.reasons),
    }


def _lane_airtime(slots: Iterable[Slot]) -> dict[str, float]:
    """Average daily minutes each lane is on air."""
    mins: dict[str, float] = defaultdict(float)
    for s in slots:
        if s.lane:
            mins[s.lane] += s.minutes * (len(s.days) if s.days else 7) / 7
    return dict(mins)


def _stats(ch: ChannelSuggestion, plan: PlanResult, week: Sequence[Airing]) -> dict[str, Any]:
    kinds: dict[str, float] = defaultdict(float)
    for a in week:
        kinds[a.kind] += a.end_s - a.start_s
    total = sum(kinds.values()) or 1.0
    air = {**_lane_airtime(ch.ramadan_slots), **_lane_airtime(ch.slots)}
    # dead air: flex (nothing fits) longer than the warning threshold, per slot occurrence
    dead: dict[tuple[int, int | None, str | None], float] = defaultdict(float)
    start: dict[tuple[int, int | None, str | None], float] = {}
    for a in week:
        if a.kind == "flex":
            k = (int(a.start_s // 86400), a.slot, a.lane)
            dead[k] += a.end_s - a.start_s
            start.setdefault(k, a.start_s)
    warn = plan.config.dead_air_warn_minutes * 60
    dead_slots = [
        {
            "day": WEEKDAYS[k[0] % 7],
            "at": fmt_hhmm(int(start[k] % 86400) // 60),
            "slot": plan.lanes[k[2]].name
            if k[2] is not None and k[2] in plan.lanes
            else "filler only",
            "minutes": round(v / 60, 1),
        }
        for k, v in sorted(dead.items(), key=lambda kv: -kv[1])
        if v > warn
    ]
    return {
        "per_day_minutes": {k: round(v / 7 / 60, 1) for k, v in sorted(kinds.items())},
        "program_share": round(kinds["program"] / total, 3),
        "filler_share": round(kinds["filler"] / total, 3),
        "dead_air_share": round(kinds["flex"] / total, 4),
        "dead_air_slots": dead_slots[:40],
        "dead_air_slot_count": len(dead_slots),
        "lane_repeat_days": {
            k: round(plan.lanes[k].duration_s / 60 / m, 1) for k, m in air.items() if m > 0
        },
    }


def _airing_json(a: Airing) -> dict[str, Any]:
    def ts(s: float) -> str:
        s = int(round(s)) % (DAY_MINUTES * 60)
        return f"{s // 3600:02d}:{s // 60 % 60:02d}:{s % 60:02d}"

    return {
        "start": ts(a.start_s),
        "end": ts(a.end_s),
        "minutes": round((a.end_s - a.start_s) / 60, 1),
        "kind": a.kind,
        "title": a.title,
        "lane": a.lane,
        "path": a.path,
    }


def _ep_json(e: EpisodeRef) -> dict[str, Any]:
    return {
        "path": e.path,
        "plex_rating_key": e.plex_rating_key,
        "plex_library": e.plex_library,
        "duration_s": round(e.duration_s, 1),
        "title": e.title,
        "season": e.season,
        "episode": e.episode,
        "air_date": e.air_date,
    }


def _lane_json(ln: Lane) -> dict[str, Any]:
    return {
        "name": ln.name,
        "pattern": ln.pattern,
        "cadence": ln.cadence,
        "slot_minutes": ln.slot_minutes,
        "shows": list(ln.shows),
        "tier_group": ln.tier_group,
        "rank": round(ln.rank, 2),
        "order": ln.order,
        "ramadan": ln.ramadan,
        "special": ln.special or None,
        "hours": round(ln.hours, 2),
        "episode_count": ln.episode_count,
        "max_episode_minutes": round(ln.max_episode_s / 60, 1),
        "reasons": list(ln.reasons),
        "units": [
            {
                "series_id": u.series_id,
                "name": u.name,
                "season": u.season,
                "kind": u.kind,
                "presenter": u.presenter,
                "genre": u.genre,
                "tier": u.tier,
                "style": u.style,
                "ramadan": u.ramadan,
                "first_air": None if u.first_air == "9999" else u.first_air,
                "hours": round(u.duration_s / 3600, 2),
                "episodes": [_ep_json(e) for e in u.episodes],
            }
            for u in ln.units
        ],
    }


def _config_json(cfg: PlannerConfig) -> dict[str, Any]:
    d = asdict(cfg)
    d["themes"] = [asdict(t) for t in cfg.themes]
    for k in ("dayparts", "ramadan_dayparts"):
        d[k] = [
            {**asdict(p), "start": fmt_hhmm(p.start), "end": fmt_hhmm(p.end)}
            for p in getattr(cfg, k)
        ]
    d["mix_daypart_topics"] = {k: list(v) for k, v in cfg.mix_daypart_topics.items()}
    d["khutbah_start"] = fmt_hhmm(cfg.khutbah_start)
    d["khutbah_end"] = fmt_hhmm(cfg.khutbah_end)
    return d


def build_plan_json(
    plan: PlanResult, *, source: str, notes: Sequence[str], sample_weekday: str = SAMPLE_WEEKDAY
) -> dict[str, Any]:
    channels: list[dict[str, Any]] = []
    for k_ch, ch in enumerate(plan.channels, 1):
        print(f"  [{k_ch}/{len(plan.channels)}] {ch.name_ar or ch.name}", flush=True)
        week = simulate(ch, plan, weekday="mon", days=7)
        sample = simulate(ch, plan, weekday=sample_weekday, days=1)
        channels.append(
            {
                "key": ch.key,
                "number": ch.number,
                "name": ch.name,
                "name_ar": ch.name_ar,
                "kind": ch.kind,
                "category": ch.category,
                "hours": round(ch.hours, 1),
                "reasons": list(ch.reasons),
                "warnings": list(ch.warnings),
                "lanes": list(ch.lanes),
                "filler_pools": list(ch.filler_pools),
                "slots": [_slot_json(s, plan) for s in ch.slots],
                "ramadan_slots": [_slot_json(s, plan) for s in ch.ramadan_slots],
                "stats": _stats(ch, plan, week),
                # what each slot airs over the next 12 months (continuity at a glance)
                "timelines": {
                    k: slot_timeline(
                        plan.lanes[k],
                        ch.slots,
                        weeks=52,
                        tolerance_minutes=plan.config.overrun_tolerance_minutes,
                    )
                    for k in ch.lanes
                    if k in plan.lanes and any(s.lane == k for s in ch.slots)
                },
                "sample_day": {
                    "weekday": sample_weekday,
                    "airings": [_airing_json(a) for a in sample],
                },
                "maktaba": maktaba_channel_definition(ch, plan),
                "dizquetv": {
                    "number": ch.number,
                    "name": ch.name_ar or ch.name,
                    "schedule": dizquetv_time_slots(ch, plan),
                    "ramadan_schedule": (
                        dizquetv_time_slots(ch, plan, ramadan=True) if ch.ramadan_slots else None
                    ),
                    "fillerCollections": filler_collections(ch, plan.config),
                    "fillerRepeatCooldown": plan.config.dz_filler_cooldown_ms,
                },
            }
        )
    pools = {
        k: {
            "name": p.name,
            "fallback": p.fallback,
            "reasons": list(p.reasons),
            "count": len(p.items),
            "minutes": round(p.duration_s / 60, 1),
            "sources": dict(Counter(i.source for i in p.items)),
            "items": [
                {
                    "path": i.path,
                    "plex_rating_key": i.plex_rating_key,
                    "plex_library": i.plex_library,
                    "duration_s": round(i.duration_s, 1),
                    "title": i.title,
                    "source": i.source,
                    "genre": i.genre,
                    "vertical": i.vertical,
                }
                for i in p.items
            ],
        }
        for k, p in plan.filler_pools.items()
    }
    return {
        "format": PLAN_FORMAT,
        "planner_version": plan.version,
        "generated_at": datetime.now(UTC).isoformat(timespec="seconds"),
        "source": source,
        "notes": list(notes),
        "config": _config_json(plan.config),
        "channels": channels,
        "lanes": {k: _lane_json(v) for k, v in plan.lanes.items()},
        "filler_pools": pools,
        "unplaced": [{"what": a, "why": b} for a, b in plan.unplaced],
    }


# --------------------------------------------------------------------------- HTML

CSS = """
.ch{margin:28px 0;border-top:1px solid var(--line);padding-top:14px}
.num{display:inline-block;min-width:2.6em;text-align:center;border-radius:6px;background:var(--acc);color:var(--bg);font-weight:600;margin-inline-end:8px}
ul.why{margin:6px 0 10px;padding-inline-start:20px;color:var(--mute)}ul.warn{color:var(--warn)}
.bar{display:flex;height:34px;border:1px solid var(--line);border-radius:6px;overflow:hidden;margin:4px 0 2px}
.bar div{overflow:hidden;white-space:nowrap;text-overflow:ellipsis;font-size:11px;padding:2px 4px;border-inline-end:1px solid var(--bg);color:#111}
.dayrow{display:grid;grid-template-columns:92px 1fr;gap:8px;align-items:center}.dayrow .d{font-size:12px;color:var(--mute)}
.ticks{display:flex;font-size:10px;color:var(--mute);margin-inline-start:100px}.ticks span{flex:1}
.tg-talk{background:#9cc3e6}.tg-produced{background:#e6c27a}.tg-cinematic{background:#e59a9a}.tg-static{background:#c8d6b0}
.tg-unknown{background:#cfc6e6}.tg-mixed{background:#a9dcc8}.tg-flex{background:repeating-linear-gradient(45deg,var(--line),var(--line) 4px,var(--card) 4px,var(--card) 8px);color:var(--mute)!important}
.tg-weekly{background:#9cc3e6}.tg-strip{background:#e6c27a}.tg-strip7{background:#e59a9a}.tg-quran{background:#c8d6b0}.tg-khutbah{background:#cfc6e6}
.tl{font-size:12px;margin:0;padding-inline-start:18px}
table{border-collapse:collapse;width:100%;font-size:12.5px;margin:6px 0}th,td{border-bottom:1px solid var(--line);padding:3px 6px;text-align:start;vertical-align:top}
th{color:var(--mute);font-weight:600}.k-filler td{color:var(--mute)}.k-flex td{color:var(--warn)}
.pill{font-size:11px;border:1px solid var(--line);border-radius:10px;padding:0 6px;margin-inline-end:4px;white-space:nowrap}
.legend span{display:inline-block;padding:1px 8px;border-radius:8px;margin-inline-end:6px;font-size:12px;color:#111}
"""


def _e(s: object) -> str:
    return html.escape(str(s))


def _name(s: str) -> str:
    return f"<bdi>{_e(s)}</bdi>"


def _day_rows(slots: Sequence[dict[str, Any]]) -> str:
    """One bar per distinct weekday pattern (all days alike → one 'daily' row)."""
    patterns: dict[str, list[str]] = {}
    per_day: dict[str, list[dict[str, Any]]] = {}
    for day in WEEKDAYS:
        segs = [s for s in slots if s["days"] == "daily" or day in s["days"]]
        segs.sort(key=lambda s: s["start"])
        per_day[day] = segs
        sig = json.dumps([(s["start"], s["end"], s["lane"]) for s in segs])
        patterns.setdefault(sig, []).append(day)
    rows: list[str] = []
    for days in patterns.values():
        label = "daily" if len(days) == 7 else ", ".join(days)
        cells = []
        for s in per_day[days[0]]:
            tg = s["pattern"] or "flex"
            title = f"{s['start']}–{s['end']} {s['lane_name'] or 'filler only'} ({s['daypart']})"
            cells.append(
                f'<div class="tg-{_e(tg)}" style="flex:{s["minutes"]}" title="{_e(title)}">'
                f"{_e(s['start'])} {_name(s['lane_name'] or 'flex')}</div>"
            )
        rows.append(f'<div class="dayrow"><span class="d">{_e(label)}</span>'
                    f'<div class="bar">{"".join(cells)}</div></div>')  # fmt: skip
    ticks = "".join(f"<span>{h:02d}</span>" for h in range(0, 24, 3))
    return "".join(rows) + f'<div class="ticks">{ticks}</div>'


def _slot_table(slots: Sequence[dict[str, Any]]) -> str:
    rows = "".join(
        f"<tr><td>{_e(s['start'])}–{_e(s['end'])}</td>"
        f"<td>{_e(s['days'] if s['days'] == 'daily' else ', '.join(s['days']))}</td>"
        f"<td>{_e(s['daypart'])}</td><td>{_name(s['lane_name'] or '— filler only')}</td>"
        f"<td>{_e(s['pattern'] or '-')}</td><td>{_e(s['filler_pool'] or '-')}</td>"
        f"<td>{'<br>'.join(_e(r) for r in s['reasons'])}</td></tr>"
        for s in slots
    )
    return ("<table><tr><th>time</th><th>days</th><th>daypart</th><th>slot</th><th>repeats</th>"
            f"<th>filler pool</th><th>why</th></tr>{rows}</table>")  # fmt: skip


def render_html(plan: dict[str, Any]) -> str:
    lanes: dict[str, Any] = plan["lanes"]
    pools: dict[str, Any] = plan["filler_pools"]
    nav: list[str] = []
    body: list[str] = []
    for ch in plan["channels"]:
        anchor = f"ch{ch['number']}"
        nav.append(f'<a href="#{anchor}">{ch["number"]} · {_name(ch["name_ar"] or ch["name"])}</a>')
        st = ch["stats"]
        per_day = " · ".join(f"{k} {v:.0f} min/day" for k, v in st["per_day_minutes"].items())

        def timeline(k: str, ch: dict[str, Any] = ch) -> str:
            rows = ch["timelines"].get(k) or []
            return (
                '<ol class="tl">'
                + "".join(
                    f"<li>weeks {r['from_week']}–{r['to_week']}: {_name(r['show'])}</li>"
                    for r in rows[:12]
                )
                + ("<li>…</li>" if len(rows) > 12 else "")
                + "</ol>"
            )

        lane_rows = "".join(
            f"<tr><td>{_name(lanes[k]['name'])}</td>"
            f"<td>{_e(lanes[k]['pattern'] or lanes[k]['special'] or '-')} · "
            f"{_e(lanes[k]['cadence'] or '')} · {lanes[k]['slot_minutes'] or '-'} min</td>"
            f"<td>{lanes[k]['hours']:.1f} h</td><td>{lanes[k]['episode_count']}</td>"
            f"<td>{_e(st['lane_repeat_days'].get(k, '-'))}</td>"
            f"<td>{len(lanes[k]['shows'])} show(s)"
            f"{' · ramadan' if lanes[k]['ramadan'] else ''}</td>"
            f"<td>{timeline(k)}</td>"
            f"<td><details><summary>{len(lanes[k]['units'])} season(s)</summary>"
            + "".join(
                f'<div><span class="pill">{_e(u["genre"])}</span>{_name(u["name"])} S{u["season"]}'
                f" · {len(u['episodes'])} ep · {u['hours']:.1f} h · {_e(u['first_air'] or '?')}"
                f"{' · ramadan' if u['ramadan'] else ''}</div>"
                for u in lanes[k]["units"]
            )
            + "</details></td></tr>"
            for k in ch["lanes"]
        )
        pool_rows = "".join(
            f"<tr><td>{_e(k)}</td><td>{pools[k]['count']}</td><td>{pools[k]['minutes']:.0f} min</td>"
            f"<td>{_e(', '.join(f'{s} {n}' for s, n in pools[k]['sources'].items()))}</td>"
            f"<td>{_e(pools[k]['fallback'] or '-')}</td></tr>"
            for k in ch["filler_pools"]
            if k in pools
        )
        sample = "".join(
            f'<tr class="k-{_e(a["kind"])}"><td>{_e(a["start"])}</td><td>{a["minutes"]}</td>'
            f"<td>{_e(a['kind'])}</td><td>{_name(a['title'])}</td></tr>"
            for a in ch["sample_day"]["airings"]
        )
        ramadan = (
            f"<h3>Ramadan grid</h3>{_day_rows(ch['ramadan_slots'])}"
            f"<details><summary>Ramadan slots</summary>{_slot_table(ch['ramadan_slots'])}</details>"
            if ch["ramadan_slots"]
            else ""
        )
        body.append(
            f'<section class="ch" id="{anchor}"><h2><span class="num">{ch["number"]}</span>'
            f"{_name(ch['name_ar'])} <span class=\"meta\">{_e(ch['name'])}</span>"
            f'<span class="kind">{_e(ch["kind"])} · {_e(ch["category"])} · {ch["hours"]:.1f} h</span></h2>'
            f'<ul class="why">{"".join(f"<li>{_e(r)}</li>" for r in ch["reasons"])}</ul>'
            + (f'<ul class="why warn">{"".join(f"<li>{_e(w)}</li>" for w in ch["warnings"])}</ul>'
               if ch["warnings"] else "")
            + f"<h3>Weekly grid</h3>{_day_rows(ch['slots'])}"
            f'<div class="sum">{_e(per_day)} · programme {st["program_share"]:.0%} · filler '
            f'{st["filler_share"]:.0%} · dead air {st["dead_air_share"]:.1%} '
            f'({st["dead_air_slot_count"]} slot(s) over {plan["config"]["dead_air_warn_minutes"]:.0f} min, '
            "simulated week)</div>"
            + ("<details><summary>Slots with dead air</summary><table>" + "".join(
                f"<tr><td>{_e(d['day'])} {_e(d['at'])}</td><td>{_name(d['slot'])}</td>"
                f"<td>{d['minutes']} min</td></tr>" for d in st["dead_air_slots"]) + "</table></details>"
               if st["dead_air_slots"] else "")
            + ""
            f"<details><summary>Slots</summary>{_slot_table(ch['slots'])}</details>"
            f"{ramadan}"
            "<h3>Slots and their queues</h3><table><tr><th>slot</th><th>repeats</th><th>content</th>"
            "<th>episodes</th><th>days to repeat</th><th></th><th>next 12 months</th>"
            f"<th>seasons</th></tr>{lane_rows}</table>"
            "<h3>Filler pools</h3><table><tr><th>pool</th><th>items</th><th>total</th>"
            f"<th>sources</th><th>fallback</th></tr>{pool_rows}</table>"
            f"<details><summary>Sample day ({_e(ch['sample_day']['weekday'])}, "
            f"{len(ch['sample_day']['airings'])} blocks)</summary>"
            f"<table><tr><th>start</th><th>min</th><th>kind</th><th>title</th></tr>{sample}</table>"
            "</details></section>"
        )  # fmt: skip
    unplaced = "".join(f"<tr><td>{_name(u['what'])}</td><td>{_e(u['why'])}</td></tr>"
                       for u in plan["unplaced"])  # fmt: skip
    notes = "".join(f"<li>{_e(n)}</li>" for n in plan["notes"])
    legend = "".join(f'<span class="tg-{t}">{label}</span>' for t, label in (
        ("weekly", "weekly show"), ("strip", "weekday strip"), ("strip7", "every day"),
        ("quran", "Quran"), ("khutbah", "khutbah")))  # fmt: skip
    return (
        '<!doctype html><html lang="en" dir="ltr"><head><meta charset="utf-8">'
        '<meta name="viewport" content="width=device-width,initial-scale=1">'
        f"<title>Channel plan</title><style>{REPORT_CSS}{CSS}</style></head><body>"
        f"<header><h1>Channel plan</h1><div class=\"sum\">{len(plan['channels'])} channels · "
        f"{len(lanes)} lanes · {len(pools)} filler pools · planner v{plan['planner_version']} · "
        f"{_e(plan['generated_at'])}</div><ul class=\"why\">{notes}</ul>"
        f'<div class="legend">{legend}<span class="tg-flex">filler only</span></div></header>'
        f"<main><nav>{''.join(nav)}<a href=\"#unplaced\">unplaced</a></nav>{''.join(body)}"
        f'<section class="ch" id="unplaced"><h2>Not on a channel / used as filler</h2>'
        f"<table>{unplaced}</table></section></main></body></html>"
    )  # fmt: skip


# --------------------------------------------------------------------------- run


def run(
    conn: sqlite3.Connection,
    out_dir: Path,
    config: PlannerConfig | None = None,
    *,
    review: Path | None = None,
    focus: Sequence[str] = (),
) -> Path:
    """Plan channels from the cache behind ``conn`` (read-only) into ``out_dir``.
    ``review``: also write the plain-text review (:func:`review_text`) there.
    Returns the JSON path."""
    if config is None:
        from . import settings_store

        config = settings_store.get(conn, "planner")
        settings_store.note(conn, "planner")
    print("phase: reading seasons, episodes and tags", flush=True)
    seasons, loose, notes = load_inputs(conn)
    print(f"phase: choosing the lineup and grids from {len(seasons)} seasons", flush=True)
    plan = suggest_channels(seasons, loose, config)
    src = next((r[2] for r in conn.execute("PRAGMA database_list") if r[1] == "main"), "")
    print(f"phase: a year of programmes per channel ({len(plan.channels)} channels)", flush=True)
    data = build_plan_json(plan, source=str(src), notes=notes)
    out_dir.mkdir(parents=True, exist_ok=True)
    jpath = out_dir / "channel-plan.json"
    print("phase: writing channel-plan.json", flush=True)
    jpath.write_text(json.dumps(data, ensure_ascii=False, indent=1), encoding="utf-8")
    print("phase: writing channel-plan.html", flush=True)
    (out_dir / "channel-plan.html").write_text(render_html(data), encoding="utf-8")
    if review is not None:
        review.parent.mkdir(parents=True, exist_ok=True)
        review.write_text(review_text(data, focus), encoding="utf-8")
    print(summary(data))
    print(f"\n{jpath}\n{out_dir / 'channel-plan.html'}")
    return jpath


def summary(plan: Mapping[str, Any]) -> str:
    lines = [f"{len(plan['channels'])} channels"]
    for n in plan["notes"]:
        lines.append(f"  note: {n}")
    for ch in plan["channels"]:
        st = ch["stats"]
        lines.append(
            f"\n{ch['number']:4} {ch['name_ar']} ({ch['name']}, {ch['kind']}) — {ch['hours']:.1f} h, "
            f"{len(ch['lanes'])} lanes, programme share {st['program_share']:.0%}"
        )
        for s in ch["slots"]:
            days = "daily" if s["days"] == "daily" else ",".join(s["days"])
            lines.append(
                f"       {s['start']}–{s['end']} {days:9} {s['daypart']:9} "
                f"{s['lane_name'] or '(filler only)'}"
            )
        for p in ch["filler_pools"]:
            pool = plan["filler_pools"].get(p)
            if pool:
                lines.append(f"       filler {p}: {pool['count']} items, {pool['minutes']:.0f} min")
    return "\n".join(lines)


def _week_lines(ch: Mapping[str, Any], *, skip_quran: bool = True) -> list[str]:
    """Each day's slots, one line per slot ("20:00–21:00 · weekly · <slot>")."""
    out: list[str] = []
    for day in ("sat", "sun", "mon", "tue", "wed", "thu", "fri"):
        segs = sorted(
            (s for s in ch["slots"] if s["days"] == "daily" or day in s["days"]),
            key=lambda s: s["start"],
        )
        out.append(f"  {day}:")
        for s in segs:
            if skip_quran and s["kind"] == "quran" and ch["kind"] != "quran":
                out.append(f"    {s['start']}–{s['end']}  Quran at dawn")
                continue
            what = s["show"] or s["lane_name"] or "(filler only)"
            out.append(f"    {s['start']}–{s['end']}  {s['pattern'] or s['kind']:7} {what}")
    return out


def review_text(plan: Mapping[str, Any], focus: Sequence[str] = (), weeks: int = 52) -> str:
    """A plain-text review of a plan: every channel's shares, then the full week and the
    slot timelines of the channels whose name contains one of ``focus``, and the Quran
    channel's week."""
    lanes: Mapping[str, Any] = plan["lanes"]
    lines = [f"channel plan v{plan['planner_version']} · {plan['generated_at']} · "
             f"{len(plan['channels'])} channels", ""]  # fmt: skip
    lines.append("num  kind      slots strips weekly  programme filler dead-air (slots)  name")
    for ch in plan["channels"]:
        st = ch["stats"]
        pats = Counter(lanes[k]["pattern"] for k in ch["lanes"] if k in lanes)
        lines.append(
            f"{ch['number']:4} {ch['kind']:9} {len(ch['lanes']):5} {pats['strip']:6} "
            f"{pats['weekly']:6}  {st['program_share']:8.0%} {st['filler_share']:6.0%} "
            f"{st['dead_air_share']:7.1%} ({st['dead_air_slot_count']:3})  {ch['name_ar']}"
            + (f"  [{len(ch['warnings'])} warning(s)]" if ch["warnings"] else "")
        )
    picked = [ch for ch in plan["channels"]
              if ch["kind"] == "quran" or any(f in ch["name_ar"] or f in ch["name"] for f in focus)]  # fmt: skip
    for ch in picked:
        st = ch["stats"]
        lines += ["", "=" * 78, f"{ch['number']} {ch['name_ar']} ({ch['kind']})", "=" * 78]
        lines += [f"  - {r}" for r in ch["reasons"]]
        lines += [f"  ! {w}" for w in ch["warnings"][:8]]
        lines.append(
            f"  shares (simulated week): programme {st['program_share']:.0%}, filler "
            f"{st['filler_share']:.0%}, dead air {st['dead_air_share']:.1%} in "
            f"{st['dead_air_slot_count']} slot(s)"
        )
        for d in st["dead_air_slots"][:5]:
            lines.append(f"    dead air: {d['day']} {d['at']} {d['slot']} — {d['minutes']} min")
        lines.append("  the week:")
        lines += _week_lines(ch)
        if ch["kind"] == "quran":
            lines.append("  reciter lanes:")
            for k in ch["lanes"]:
                lines.append(f"    {lanes[k]['name']}: {lanes[k]['episode_count']} recitations, "
                             f"{lanes[k]['hours']:.0f} h ({lanes[k]['cadence']})")  # fmt: skip
            for p in ch["filler_pools"]:
                pool = plan["filler_pools"].get(p) or {}
                lines.append(f"  filler {p}: {pool.get('count', 0)} items "
                             f"({', '.join(f'{s} {n}' for s, n in (pool.get('sources') or {}).items())})")  # fmt: skip
            continue
        lines.append(f"  next {weeks} weeks, slot by slot (season runs):")
        for k, rows in ch["timelines"].items():
            ln = lanes[k]
            if ln.get("special"):
                continue
            lines.append(f"    {ln['name']}  [{ln['pattern']}, {ln['slot_minutes']} min, "
                         f"{len(ln['shows'])} show(s) in the queue]")  # fmt: skip
            for r in rows[:10]:
                lines.append(f"      weeks {r['from_week']:>2}–{r['to_week']:<2} {r['show']}")
            if len(rows) > 10:
                lines.append(f"      … {len(rows) - 10} more")
    return "\n".join(lines) + "\n"


def main(argv: list[str] | None = None) -> int:
    import argparse

    ap = argparse.ArgumentParser(prog="plan_channels", description=__doc__)
    ap.add_argument("--db", required=True, help="mediaintel SQLite cache (opened read-only)")
    ap.add_argument("--out", type=Path, default=Path("reports"))
    a = ap.parse_args(argv)
    conn = sqlite3.connect(f"file:{Path(a.db).as_posix()}?mode=ro", uri=True, timeout=60)
    conn.row_factory = sqlite3.Row
    run(conn, a.out)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
