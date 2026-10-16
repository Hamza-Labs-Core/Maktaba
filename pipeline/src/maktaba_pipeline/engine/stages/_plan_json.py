"""JSON shape of a channel plan (ported from the prototype's ``plan_channels.build_plan_json``).

Core Live TV only: the plan carries lanes, slots, filler pools and Maktaba's own channel
definition. Anything specific to an external system (dizqueTV time slots, for example) is added
by an extension through :func:`~maktaba_pipeline.engine.stages.plan_channels.register_plan_exporter`
under ``channels[i]["exports"][<name>]``.

In the plan, an episode's or a filler item's ``path`` is the Maktaba **video id**
(the planner treats it as an opaque identity).
"""

from __future__ import annotations

from collections import Counter, defaultdict
from collections.abc import Callable, Iterable, Mapping, Sequence
from dataclasses import asdict
from datetime import UTC, datetime
from typing import Any

from ...schedule.channel_planner import (
    DAY_MINUTES,
    Airing,
    ChannelSuggestion,
    EpisodeRef,
    Lane,
    PlannerConfig,
    PlanResult,
    Slot,
    fmt_hhmm,
    maktaba_channel_definition,
    simulate,
)

__all__ = ["PLAN_FORMAT", "PlanExporter", "build_plan_json", "config_json", "strip_volatile"]

PLAN_FORMAT = 2
SAMPLE_WEEKDAY = "fri"

PlanExporter = Callable[[ChannelSuggestion, PlanResult], dict[str, Any]]


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
        "tier_group": lane.tier_group if lane else None,
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
    return {
        "per_day_minutes": {k: round(v / 7 / 60, 1) for k, v in sorted(kinds.items())},
        "program_share": round(kinds["program"] / total, 3),
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
        "tier_group": ln.tier_group,
        "rank": round(ln.rank, 2),
        "order": ln.order,
        "ramadan": ln.ramadan,
        "special": ln.special or None,
        # the TV grid: the slot this lane is the queue of, its shows' cadence, its shows in
        # queue order (one after the other, never interleaved)
        "pattern": ln.pattern or None,
        "cadence": ln.cadence or None,
        "slot_minutes": ln.slot_minutes,
        "shows": list(ln.shows),
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


def config_json(cfg: PlannerConfig) -> dict[str, Any]:
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
    plan: PlanResult,
    *,
    source: str,
    notes: Sequence[str],
    exporters: Mapping[str, PlanExporter] | None = None,
    sample_weekday: str = SAMPLE_WEEKDAY,
) -> dict[str, Any]:
    channels: list[dict[str, Any]] = []
    for ch in plan.channels:
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
                "sample_day": {
                    "weekday": sample_weekday,
                    "airings": [_airing_json(a) for a in sample],
                },
                "maktaba": maktaba_channel_definition(ch, plan),
                "exports": {name: fn(ch, plan) for name, fn in (exporters or {}).items()},
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
        "config": config_json(plan.config),
        "channels": channels,
        "lanes": {k: _lane_json(v) for k, v in plan.lanes.items()},
        "filler_pools": pools,
        "unplaced": [{"what": a, "why": b} for a, b in plan.unplaced],
    }


def strip_volatile(plan: Mapping[str, Any]) -> dict[str, Any]:
    """The plan without fields that change on every run (``generated_at``)."""
    return {k: v for k, v in plan.items() if k != "generated_at"}
