"""What writing a channel plan into dizqueTV needs, apart from talking to dizqueTV.

Shared by the queue engine's ``dizquetv_channels`` stage
(:mod:`maktaba_pipeline.extensions.dizquetv.channels`) and the stand-alone mediaintel
exporter (``scripts/mediaintel/export_dizquetv.py``), so a plan gives the same dizqueTV
channels from either:

* the filler lists a channel uses (:func:`filler_collections`, :func:`filler_items`);
* the time-slot grid as dizqueTV takes it (:func:`resolve_slots`: ``lane:`` show ids
  resolved, slots merged unless the channel is padded one episode a slot), with the live
  broadcasts it carries (:func:`add_live_slots`);
* the channel's look: guide settings, logo and corner watermark (:func:`guide_settings`,
  :func:`channel_art`, :func:`art_link`);
* what the plan no longer has (:func:`removals`: only objects this tool made, unless asked);
* continuity of a rewrite (:func:`write_channel`): a show's queue goes on where the channel
  left it, what is on air finishes, none of the new programmes is dropped and no gap opens
  (:func:`aired`, :func:`resume`, :func:`splice`, :func:`fingerprint`).

Everything here is pure, or talks to dizqueTV only through the ``call(method, path, body)``
callable it is handed.
"""

from __future__ import annotations

import hashlib
import json
import math
import time
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass, field
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from ..schedule.channel_planner import ChannelSuggestion, PlannerConfig
from ..schedule.tv_settings import LiveSlot

__all__ = [
    "DAY_MS",
    "GUIDE_KEYS",
    "ChannelWrite",
    "ReadbackMismatch",
    "Removal",
    "add_live_slots",
    "airable",
    "aired",
    "art_link",
    "art_version",
    "channel_art",
    "filler_collections",
    "filler_items",
    "fingerprint",
    "guide_settings",
    "hosted_link",
    "new_channel_defaults",
    "removals",
    "resolve_slots",
    "resume",
    "slot_lengths",
    "slot_text",
    "splice",
    "timeline_programs",
    "versioned",
    "write_channel",
]

DAY_MS = 86_400_000
_DZ_DAYS = ("thu", "fri", "sat", "sun", "mon", "tue", "wed")
GUIDE_KEYS = ("guideMinimumDurationSeconds", "guideMaxPaddingSeconds", "guideFlexPlaceholder")

DzCall = Callable[..., Any]


class ReadbackMismatch(RuntimeError):
    """dizqueTV does not hold what was just written."""


# --------------------------------------------------------------------------- filler


def filler_collections(ch: ChannelSuggestion, cfg: PlannerConfig) -> list[dict[str, Any]]:
    """dizqueTV fills per channel (not per slot): the channel's on-topic filler, its
    shows' promos, the breaks (nasheeds, short clips), the library's general filler;
    the Quran channel only its own. Weights: settings ``dz_weight_*``."""
    if ch.kind == "quran":
        return [{"pool": p, "weight": cfg.dz_weight_topic, "cooldown": 0}
                for p in ch.filler_pools]  # fmt: skip
    want = (
        (f"{ch.key}.topic", cfg.dz_weight_topic),
        (f"{ch.key}.promos", cfg.dz_weight_promos),
        (f"{ch.key}.breaks", cfg.dz_weight_breaks),
        ("library.general", cfg.dz_weight_general),
    )
    return [{"pool": p, "weight": w, "cooldown": 0} for p, w in want if p in ch.filler_pools]


def filler_items(
    plan: Mapping[str, Any], items: Sequence[Mapping[str, Any]]
) -> Sequence[Mapping[str, Any]]:
    """A dizqueTV filler list needs variety, not the whole pool: the first
    ``dz_filler_max_items`` of it (the pool is in priority order: own promos, then
    on-topic clips). A whole pool can be 75,000 clips, past dizqueTV's 50 MB request limit."""
    cap = int((plan.get("config") or {}).get("dz_filler_max_items") or 2000)
    return items[: max(1, cap)]


# --------------------------------------------------------------------------- schedule


def resolve_slots(
    sch: Mapping[str, Any], lane_ids: Mapping[str, str], tz_offset: int
) -> dict[str, Any]:
    """The schedule dizqueTV takes: ``lane:KEY`` show ids become ``custom.ID`` (a lane with
    nothing playable yet is flex), a slot is merged into the previous one of the same
    show, but a padded schedule (a nonstop channel: one episode a slot) keeps every slot."""
    out = dict(sch)
    one_a_slot = int(sch.get("pad") or 1) > 1
    slots: list[dict[str, Any]] = []
    for s in sch["slots"]:
        sid = s["showId"]
        if sid.startswith("lane:"):
            lid = lane_ids.get(sid[5:])
            sid = f"custom.{lid}" if lid else "flex."  # nothing playable yet: flex
        if slots and slots[-1]["showId"] == sid and not one_a_slot:
            continue
        slots.append({**s, "showId": sid, "order": "shuffle" if sid == "flex." else s["order"]})
    if len(slots) > 1 and slots[0]["showId"] == slots[-1]["showId"] and not one_a_slot:
        slots = slots[1:]  # the last slot runs on past midnight into the first one
    out["slots"] = slots
    out["timeZoneOffset"] = tz_offset
    return out


def _live_windows(live: LiveSlot, period: int) -> list[int]:
    """When ``live`` starts in one period of the grid (ms after its start)."""
    every = max(1, live.every_minutes) * 60_000
    start = live.start * 60_000
    end = (live.end or 24 * 60) * 60_000
    if end <= start:
        end += DAY_MS
    times = list(range(start, end, every))
    if period == DAY_MS:
        return sorted({t % DAY_MS for t in times})
    days = [d for d in (x.strip().lower()[:3] for x in live.days) if d in _DZ_DAYS] or list(
        _DZ_DAYS
    )
    return sorted({(_DZ_DAYS.index(d) * DAY_MS + t) % period for d in days for t in times})


def add_live_slots(
    sched: Mapping[str, Any], number: int, lives: Sequence[LiveSlot]
) -> dict[str, Any]:
    """The grid with the live broadcasts channel ``number`` carries: a ``live.`` slot at
    each start (dizqueTV plays the stream for the slot, and starts nothing that would run
    into it), and the show that had the time goes on when it ends. A daily grid with a
    broadcast on some days only becomes a weekly one. No live broadcast: unchanged."""
    mine = [x for x in lives if x.channel == number and x.url.strip() and x.minutes > 0]
    if not mine:
        return dict(sched)
    out = dict(sched)
    period = int(sched["period"])
    slots = sorted((dict(x) for x in sched["slots"]), key=lambda x: int(x["time"]))
    if period == DAY_MS and any(x.days for x in mine):
        period = 7 * DAY_MS
        slots = [{**x, "time": int(x["time"]) + d * DAY_MS} for d in range(7) for x in slots]
    windows: dict[int, tuple[int, dict[str, Any]]] = {}
    for x in mine:
        sid = "live." + hashlib.sha1(x.url.strip().encode()).hexdigest()[:10]  # noqa: S324
        slot = {"showId": sid, "order": "next",
                "live": {"url": x.url.strip(), "title": x.title or x.url.strip()}}  # fmt: skip
        for t in _live_windows(x, period):
            windows.setdefault(t, (x.minutes * 60_000, slot))

    def inside(t: int) -> bool:
        return any(0 <= (t - w) % period < d for w, (d, _) in windows.items())

    def active(t: int) -> dict[str, Any]:  # the grid's own slot at t
        before = [x for x in slots if int(x["time"]) <= t]
        return (before or slots)[-1]

    new = [x for x in slots if not inside(int(x["time"]))]
    for w, (d, slot) in windows.items():
        new.append({**slot, "time": w})
        back = (w + d) % period
        if slots and not inside(back) and all(int(x["time"]) != back for x in new):
            new.append({**active(back), "time": back})
    out["slots"] = sorted(new, key=lambda x: int(x["time"]))
    out["period"] = period
    return out


def _hms(ms: int) -> str:
    m = ms // 60_000
    return f"{m // 60:02d}:{m % 60:02d}"


def slot_text(time_ms: int, period: int) -> str:
    """A slot's start for the plan listing: ``HH:MM``, or ``day HH:MM`` on a weekly grid."""
    if period == DAY_MS:
        return _hms(time_ms)
    return f"{_DZ_DAYS[time_ms // DAY_MS]} {_hms(time_ms % DAY_MS)}"


def slot_lengths(sched: Mapping[str, Any]) -> dict[str, list[int]]:
    """Each show's slot lengths (ms) in one schedule period: a slot runs until the next."""
    period = int(sched["period"])
    slots = sorted(sched["slots"], key=lambda s: int(s["time"]))
    out: dict[str, list[int]] = {}
    for i, s in enumerate(slots):
        nxt = int(slots[(i + 1) % len(slots)]["time"]) + (period if i + 1 == len(slots) else 0)
        out.setdefault(s["showId"], []).append(max(0, nxt - int(s["time"])))
    return out


def airable(
    sched: Mapping[str, Any], lengths_ms: Sequence[int], durations_ms: Sequence[int]
) -> int:
    """How many of a show's episodes its slots can air before dizqueTV repeats the
    schedule (``maxDays``): per slot and period, its length over the show's median episode
    (plus one), times the periods in ``maxDays``. At least one."""
    if not durations_ms:
        return 0
    med = max(60_000, sorted(durations_ms)[len(durations_ms) // 2])
    periods = math.ceil(int(sched.get("maxDays") or 365) * 86_400_000 / int(sched["period"]))
    per_period = sum(-(-ms // med) + 1 for ms in lengths_ms) or 1
    return max(1, per_period * periods)


# --------------------------------------------------------------------------- a channel's look


def new_channel_defaults() -> dict[str, Any]:
    """What the dizqueTV web UI gives a new channel (``channel-config.js``)."""
    return {
        "icon": "",
        "iconWidth": 120,
        "iconDuration": 60,
        "iconPosition": "2",
        "disableFillerOverlay": True,
        "guideMinimumDurationSeconds": 300,
        "guideFlexPlaceholder": "",
        "offlineMode": "pic",
        "offlinePicture": "",
        "offlineSoundtrack": "",
        "stealth": False,
        "fallback": [],
        "transcoding": {"targetResolution": ""},
        "watermark": {"enabled": False},
        "onDemand": {"isOnDemand": False, "modulo": 1},
        "mergeAdjacentPrograms": False,
        "startTime": "<now>",
    }


def guide_settings(plan: Mapping[str, Any]) -> dict[str, Any]:
    """The guide shows what runs: every programme from a minute, filler time as its own
    entry («فواصل») rather than added to the programme before it (dizqueTV melds up to
    30 minutes; ``guideMaxPaddingSeconds`` is this install's channel option for it)."""
    cfg = plan.get("config") or {}
    return {
        "guideMinimumDurationSeconds": int(cfg.get("dz_guide_min_program_s", 60)),
        "guideMaxPaddingSeconds": int(cfg.get("dz_guide_max_padding_s", 120)),
        "guideFlexPlaceholder": str(cfg.get("dz_guide_filler_title", "فواصل")),
    }


def channel_art(plan: Mapping[str, Any], key: str) -> dict[str, Any]:
    """The channel's logo and corner watermark from the plan's settings, as local files
    (``apply`` uploads them): ``{"icon": path, "watermark": {...}}``, only what is set."""
    cfg = plan.get("config") or {}
    out: dict[str, Any] = {}
    if folder := str(cfg.get("dz_logo_dir") or ""):
        for name in (f"{key}.png", "default.png"):
            if (Path(folder) / name).is_file():
                out["icon"] = str(Path(folder) / name)
                break
    wm = str(cfg.get("dz_watermark_file") or "")
    if folder_wm := str(cfg.get("dz_watermark_dir") or ""):
        own = [Path(folder_wm) / f"{key}{ext}" for ext in (".gif", ".png")]
        wm = next((str(p) for p in own if p.is_file()), wm)
    if wm and Path(wm).is_file():
        margin = float(cfg.get("dz_watermark_margin", 2.0))
        out["watermark"] = {
            "enabled": True, "url": wm, "width": float(cfg.get("dz_watermark_width", 12.0)),
            "verticalMargin": margin, "horizontalMargin": margin, "duration": 0,
            "position": str(cfg.get("dz_watermark_position") or "top-right"),
            "fixedSize": False, "animated": wm.lower().endswith(".gif"),
        }  # fmt: skip
    return out


def art_version(path: str) -> str:
    """The picture's version: a changed logo is a new link, never a cached old picture."""
    return hashlib.sha1(Path(path).read_bytes()).hexdigest()[:10]  # noqa: S324 - a cache key


def versioned(link: str, path: str) -> str:
    """``link`` with the file's version in it: a new picture is a new link, so Plex and
    the TVs fetch it instead of showing the one they cached (``&v=`` when the link
    already has a query)."""
    return f"{link}{'&' if '?' in link else '?'}v={art_version(path)}"


def art_link(cfg: Mapping[str, Any], api_url: str, uploaded_url: str) -> str:
    """An uploaded logo's link as the TVs reach it: dizqueTV's own answer names the host
    the export talked to (localhost); ``dz_public_url`` replaces it."""
    pub = str(cfg.get("dz_public_url") or "").rstrip("/")
    if not pub:
        return uploaded_url
    base = api_url.rstrip("/")
    if uploaded_url.startswith(base):
        return pub + uploaded_url[len(base) :]
    i = uploaded_url.find("/", uploaded_url.find("//") + 2)
    return pub + (uploaded_url[i:] if i > 0 else "")


def hosted_link(base: str, path: str, status_of: Callable[[str], int]) -> str:
    """A logo on a web host (``dz_art_url``): its versioned link, checked to answer.
    ``status_of(url)`` is the HTTP status of a ``HEAD`` request (0 when unreachable)."""
    import urllib.parse

    link = f"{base.rstrip('/')}/{urllib.parse.quote(Path(path).name)}"
    status = status_of(link)
    if not 200 <= status < 300:
        raise RuntimeError(f"logo {Path(path).name} is not at {link}")
    return versioned(link, path)


# --------------------------------------------------------------------------- removals


@dataclass(frozen=True, slots=True)
class Removal:
    kind: str  # channel | show | filler
    name: str
    endpoint: str
    body: dict[str, Any] | None
    why: str


def removals(
    planned_numbers: set[int],
    channels: Mapping[int, Mapping[str, Any]],
    shows: Mapping[str, Mapping[str, Any]],
    fillers: Mapping[str, Mapping[str, Any]],
    planned_names: set[str],
    prefix: str,
    *,
    clear_channels: bool = False,
) -> list[Removal]:
    """What the plan no longer has: this tool's channels (``maktabaPlan`` marker), shows
    and filler lists (``prefix`` in the name); with ``clear_channels`` every other channel
    that is not in the plan too. A planned channel is never listed."""
    out: list[Removal] = []
    for n, ch in sorted(channels.items()):
        if n in planned_numbers:
            continue
        ours = isinstance(ch.get("maktabaPlan"), dict)
        if ours or clear_channels:
            out.append(Removal(
                "channel", f"#{n} {ch.get('name', '')}", "/api/channel", {"number": n},
                "made by this tool, not in the plan" if ours
                else "not in the plan (clear channels)"))  # fmt: skip
    for kind, have in (("show", shows), ("filler", fillers)):
        for name, info in sorted(have.items()):
            if name.startswith(prefix) and name not in planned_names:
                out.append(Removal(kind, name, f"/api/{kind}/{info['id']}", None,
                                   "made by this tool, not in the plan"))  # fmt: skip
    return out


# --------------------------------------------------------------------------- continuity


def fingerprint(sched: Mapping[str, Any], programs: Sequence[tuple[str, int, str]]) -> str:
    """What a channel's timeline is computed from: the same schedule and programmes give
    the same timeline, so the channel keeps the one it airs (a logo change is no reason
    to rebuild it). ``programs``: (ratingKey, duration, show id) in each show's order."""
    src = {"schedule": sched, "programs": [list(p) for p in programs]}
    return hashlib.sha1(json.dumps(src, sort_keys=True).encode()).hexdigest()  # noqa: S324


def _ms(iso: str) -> int:
    return int(datetime.fromisoformat(str(iso).replace("Z", "+00:00")).timestamp() * 1000)


def aired(old: Mapping[str, Any], now_ms: int) -> tuple[list[Mapping[str, Any]], int, int]:
    """The old timeline up to now: the programmes aired so far (the one on air last),
    and when the one on air started and ends (ms). Nothing on air: ``([], now, now)``."""
    progs = old.get("programs") or []
    total = sum(int(p.get("duration", 0)) for p in progs)
    try:
        start = _ms(old["startTime"])
    except (KeyError, ValueError):
        return [], now_ms, now_ms
    if not progs or total <= 0 or start > now_ms:
        return [], now_ms, now_ms
    loops, elapsed = divmod(now_ms - start, total)
    t = start + loops * total
    for i, p in enumerate(progs):
        d = int(p.get("duration", 0))
        if elapsed < d:
            return list(progs[: i + 1]), t, t + d
        elapsed -= d
        t += d
    return list(progs), now_ms, now_ms


def resume(content: Sequence[Mapping[str, Any]], so_far: Sequence[Mapping[str, Any]],
           show_id: str) -> list[Mapping[str, Any]]:  # fmt: skip
    """A show's episodes from the one after the last it aired (its queue goes on where
    the channel left it, instead of from episode 1 on every rewrite)."""
    keys = [str(p.get("ratingKey")) for p in content]
    for p in reversed(so_far):
        if str(p.get("customShowId")) == show_id and not p.get("isOffline"):
            k = str(p.get("ratingKey"))
            if k in keys:
                i = keys.index(k) + 1
                return list(content[i:]) + list(content[:i])
            break
    return list(content)


def splice(old: Mapping[str, Any], new: Mapping[str, Any], now_ms: int,
           period: int) -> dict[str, Any]:  # fmt: skip
    """The new timeline, but what is on air now finishes first and the new one goes on
    the moment it ends: none of its programmes is dropped (a series keeps its order),
    each starts as soon as the one before ends, and its filler only fills up to where
    the timeline would be, so a late start is caught up in the breaks. A rewrite never
    cuts a programme a viewer is watching, skips an episode, nor leaves a gap."""
    so_far, on_start, on_end = aired(old, now_ms)
    if not so_far or so_far[-1].get("isOffline"):
        return dict(new)
    n0 = _ms(new["startTime"])
    out: list[Mapping[str, Any]] = [so_far[-1]]
    t = n0  # where the new timeline has each programme
    at = on_end  # where it actually airs
    for p in new["programs"]:
        d = int(p.get("duration", 0))
        if p.get("isOffline"):  # filler: only up to the timeline's own time
            if at < t + d:
                out.append({**p, "duration": t + d - at})
                at = t + d
        else:
            if at < t:  # early (the new timeline starts after the switch): filler first
                out.append({"duration": t - at, "isOffline": True})
                at = t
            out.append(p)
            at += d
        t += d
    total = sum(int(p.get("duration", 0)) for p in out)
    if period > 0 and total % period:  # keep the loop on the schedule's period
        out.append({"duration": period - total % period, "isOffline": True})
    return {
        "programs": out,
        "startTime": datetime.fromtimestamp(on_start / 1000, tz=UTC)
        .isoformat()
        .replace("+00:00", "Z"),
    }


def timeline_programs(
    sched: Mapping[str, Any],
    get_show: Callable[[str], Mapping[str, Any]],
    so_far: Sequence[Mapping[str, Any]],
) -> tuple[list[dict[str, Any]], list[tuple[str, int, str]]]:
    """The programmes ``/api/channel-tools/time-slots`` is given, and the ``base`` they
    were taken from (for :func:`fingerprint`): only what the schedule can air before
    dizqueTV repeats it (``maxDays``: the mix channel's whole archive, 55,000 episodes, is
    past dizqueTV's 50 MB request limit), each show's queue gone on after what aired."""
    slot_ms = slot_lengths(sched)
    programs: list[dict[str, Any]] = []
    base: list[tuple[str, int, str]] = []
    for sid in dict.fromkeys(s["showId"] for s in sched["slots"]):
        if not sid.startswith("custom."):
            continue
        show_id = sid[len("custom.") :]
        show = get_show(show_id)
        content = show.get("content", [])
        cap = airable(sched, slot_ms.get(sid, []), [int(p["duration"]) for p in content])
        base.extend((str(p.get("ratingKey")), int(p.get("duration", 0)), show_id)
                    for p in content[:cap])  # fmt: skip
        queue = [dict(p) for p in resume(content, so_far, show_id)]
        for n, prog in enumerate(queue[:cap]):
            programs.append({**prog, "customShowId": show_id,
                             "customShowName": show["name"], "customOrder": n})  # fmt: skip
    return programs, base


@dataclass(slots=True)
class ChannelWrite:
    """How one channel's write ended."""

    #: ``written`` | ``unchanged`` (the timeline as it airs was kept, nothing saved) |
    #: ``skipped`` (nothing playable yet)
    status: str
    programmes: int = 0
    fillers: int = 0
    notes: list[str] = field(default_factory=list)


def write_channel(
    call: DzCall,
    body: Mapping[str, Any],
    method: str,
    *,
    exists: bool,
    shows: dict[str, Any],
    keep_airing: bool = True,
    now_ms: int | None = None,
) -> ChannelWrite:
    """Compute the channel's programmes with dizqueTV's time-slot tool, save the channel and
    read it back.

    ``exists``: the channel is already in dizqueTV; with ``keep_airing`` its queues go on
    where they stopped, the programme on air finishes first and a rewrite of the same
    timeline (same schedule and programmes: only a name, logo ...) keeps the stored one
    untouched, so nobody watching is restarted. ``shows`` caches ``GET /api/show/ID``.
    """
    out = dict(body)
    sched = out["scheduleBackup"]
    number = int(out["number"])
    now = int(time.time() * 1000) if now_ms is None else now_ms
    old = call("GET", f"/api/channel/{number}") if exists else None
    so_far: list[Mapping[str, Any]] = aired(old, now)[0] if old and keep_airing else []

    def get_show(show_id: str) -> Mapping[str, Any]:
        if show_id not in shows:
            shows[show_id] = call("GET", f"/api/show/{show_id}")
        show: Mapping[str, Any] = shows[show_id]
        return show

    programs, base = timeline_programs(sched, get_show, so_far)
    if not programs:
        return ChannelWrite("skipped")
    fp = fingerprint(sched, base)
    out["maktabaPlan"] = {**out["maktabaPlan"], "fingerprint": fp}
    if old and keep_airing and (old.get("maktabaPlan") or {}).get("fingerprint") == fp:
        # the same timeline: kept as it airs (only the name, logo ... change)
        out["programs"] = old["programs"]
        out["startTime"] = old["startTime"]
        res = {"programs": old["programs"]}
        if all(old.get(k) == v for k, v in out.items()):
            return ChannelWrite("unchanged", len(old["programs"]), len(out["fillerCollections"]))
    else:
        res = call(
            "POST", "/api/channel-tools/time-slots", {"programs": programs, "schedule": sched}
        )
        if old and keep_airing:  # what is on air finishes; the queues went on above
            res = splice(old, res, int(time.time() * 1000) if now_ms is None else now_ms,
                         int(sched["period"]))  # fmt: skip
        out["programs"] = res["programs"]
        out["startTime"] = res["startTime"]
    call(method, "/api/channel", out)
    back = call("GET", f"/api/channel/programless/{number}")
    if not isinstance(back.get("maktabaPlan"), dict) or back.get("name") != out["name"]:
        raise ReadbackMismatch(f"channel {number}: read-back does not match")
    if "icon" in out and back.get("icon") != out["icon"]:
        raise ReadbackMismatch(f"channel {number}: its logo did not stick")
    return ChannelWrite("written", len(res["programs"]), len(out["fillerCollections"]))
