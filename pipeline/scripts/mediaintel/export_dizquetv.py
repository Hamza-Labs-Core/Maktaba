"""export-dizquetv: create / update the planned channels in dizqueTV (dry run unless --apply).

Reads ``channel-plan.json`` (``plan-channels``) and the current dizqueTV state
(read-only ``GET`` calls: ``/api/channels``, ``/api/channel/programless/N``,
``/api/shows``, ``/api/show/ID``, ``/api/fillers``, ``/api/filler/ID``,
``/api/plex-servers``), then prints every object the plan maps to, with the
real endpoint that would write it:

======================== ================================= ==========================
plan                     dizqueTV object                   endpoint
======================== ================================= ==========================
slot queue (its shows,   custom show ``maktaba-plan · …``  ``PUT /api/show`` (create),
one after the other)     (every episode in queue order)    ``POST /api/show/ID``
filler pool              filler list ``maktaba-plan · …``  ``PUT /api/filler``,
                                                           ``POST /api/filler/ID``
channel slot grid        time-slot schedule (lane →        ``POST /api/channel-tools/
                         ``custom.ID``, ``order: next``)   time-slots`` (pure compute)
channel                  channel #number with programs,    ``POST /api/channel`` (new),
                         ``fillerCollections``,            ``PUT /api/channel``
                         ``scheduleBackup``                (update)
======================== ================================= ==========================

Without ``--apply`` nothing is written. With it (:func:`apply_actions`): each
programme is built from Plex metadata, the show / filler actions are sent, the
``lane:`` show ids resolve to the real ``custom.ID``, the time-slot tool computes
each channel's programmes, the channel is saved — and every object is read back.

Safety rules mirrored from ``export_plex``: a dizqueTV object this tool did not
create (no ``maktaba-plan · `` name prefix, or a channel without the
``maktabaPlan`` marker) is never touched unless ``--replace-existing``;
existing channels keep every setting except ``programs``, ``fillerCollections``
and ``scheduleBackup``.

``--prune`` deletes what this tool made earlier and the plan no longer has (its
channels, ``maktaba-plan · `` shows and filler lists); ``--clear-channels`` also
deletes every other channel that is not in the plan (a fresh lineup). Neither
runs with ``--channel``.
"""

from __future__ import annotations

import hashlib
import json
import math
import time
import urllib.parse
import urllib.request
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

PREFIX = "maktaba-plan · "
DAY_MS = 86_400_000
_DZ_DAYS = ("thu", "fri", "sat", "sun", "mon", "tue", "wed")


# --------------------------------------------------------------------------- client


class DizqueTV:
    """Read-only dizqueTV client (GET only)."""

    def __init__(self, base: str, timeout: float = 30.0) -> None:
        self.base = base.rstrip("/")
        self.timeout = timeout

    def get(self, path: str) -> Any:
        url = self.base + urllib.parse.quote(path, safe="/:?=&")
        req = urllib.request.Request(url, headers={"Accept": "application/json"})
        with urllib.request.urlopen(req, timeout=self.timeout) as r:  # noqa: S310 — local API
            return json.loads(r.read().decode("utf-8"))


@dataclass(slots=True)
class State:
    """What dizqueTV holds now (empty when offline)."""

    channels: dict[int, dict[str, Any]] = field(default_factory=dict)  # programless
    shows: dict[str, dict[str, Any]] = field(default_factory=dict)  # name → {id, keys}
    fillers: dict[str, dict[str, Any]] = field(default_factory=dict)  # name → {id, keys}
    server: str | None = None
    online: bool = False


def read_state(dz: DizqueTV) -> State:
    st = State(online=True)
    for n in dz.get("/api/channels"):
        st.channels[int(n)] = dz.get(f"/api/channel/programless/{int(n)}")
    for info in dz.get("/api/shows"):
        if str(info.get("name", "")).startswith(PREFIX):
            show = dz.get(f"/api/show/{info['id']}")
            keys = [str(p.get("ratingKey")) for p in show.get("content", [])]
            st.shows[info["name"]] = {"id": info["id"], "keys": keys}
        else:
            st.shows.setdefault(info["name"], {"id": info["id"], "keys": None})
    for info in dz.get("/api/fillers"):
        if str(info.get("name", "")).startswith(PREFIX):
            fl = dz.get(f"/api/filler/{info['id']}")
            keys = [str(p.get("ratingKey")) for p in fl.get("content", [])]
            st.fillers[info["name"]] = {"id": info["id"], "keys": keys}
        else:
            st.fillers.setdefault(info["name"], {"id": info["id"], "keys": None})
    servers = dz.get("/api/plex-servers")
    st.server = servers[0]["name"] if servers else None  # never print tokens
    return st


# --------------------------------------------------------------------------- mapping


@dataclass(slots=True)
class Action:
    verb: str  # create | update | unchanged | conflict | skip | delete
    kind: str  # filler | show | channel
    name: str
    method: str | None = None
    endpoint: str | None = None
    body: dict[str, Any] | None = None
    detail: list[str] = field(default_factory=list)
    ref: str = ""  # lane / pool / channel key


def plex_program(meta: Mapping[str, Any], server_name: str) -> dict[str, Any] | None:
    """A dizqueTV programme from a Plex ``/library/metadata/RK`` item, in the
    shape the web UI saves (``tools/fill-custom-shows.js`` ``toProgram``).
    Used by ``--apply``; icons are left to dizqueTV's image cache."""
    if not meta.get("duration") or meta.get("type") not in ("movie", "episode"):
        return None
    part = meta["Media"][0]["Part"][0]
    p: dict[str, Any] = {
        "title": meta.get("title", ""),
        "key": meta["key"],
        "ratingKey": str(meta["ratingKey"]),
        "type": meta["type"],
        "duration": int(meta["duration"]),
        "summary": meta.get("summary", ""),
        "date": meta.get("originallyAvailableAt"),
        "year": meta.get("year"),
        "plexFile": part["key"],
        "file": part["file"],
        "serverKey": server_name,
        "commercials": [],
    }
    if meta["type"] == "episode":
        p.update(showTitle=meta.get("grandparentTitle"), season=meta.get("parentIndex"),
                 episode=meta.get("index"))  # fmt: skip
    else:
        p.update(showTitle=meta.get("title", ""), season=1, episode=1)
    return p


def _stub(item: Mapping[str, Any], server: str | None) -> dict[str, Any]:
    """Dry-run stand-in for :func:`plex_program` (Plex is not called)."""
    rk = str(item["plex_rating_key"])
    return {
        "title": item.get("title", ""),
        "key": f"/library/metadata/{rk}",
        "ratingKey": rk,
        "type": "movie",
        "duration": int(round(float(item["duration_s"]) * 1000)),
        "serverKey": server or "<plex server>",
        "plexFile": "<from Plex>",
        "file": "<from Plex>",
    }


def _diff(old: Sequence[str] | None, new: Sequence[str]) -> tuple[str, list[str]]:
    if old is None:
        return "conflict", ["same name, not created by this tool (needs --replace-existing)"]
    if list(old) == list(new):
        return "unchanged", []
    add = len(set(new) - set(old))
    rem = len(set(old) - set(new))
    reorder = not add and not rem
    return "update", [f"+{add} −{rem}" + (" (reordered)" if reorder else "")]


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


def filler_items(
    plan: Mapping[str, Any], items: Sequence[Mapping[str, Any]]
) -> Sequence[Mapping[str, Any]]:
    """A dizqueTV filler list needs variety, not the whole pool: the first
    ``dz_filler_max_items`` of it (the pool is in priority order: own promos, then
    on-topic clips). A whole pool can be 75,000 clips, past dizqueTV's 50 MB request limit."""
    cap = int((plan.get("config") or {}).get("dz_filler_max_items") or 2000)
    return items[: max(1, cap)]


def _items_with_keys(items: Sequence[Mapping[str, Any]]) -> tuple[list[Mapping[str, Any]], int]:
    keep = [i for i in items if i.get("plex_rating_key")]
    return keep, len(items) - len(keep)


def _hms(ms: int) -> str:
    m = ms // 60_000
    return f"{m // 60:02d}:{m % 60:02d}"


def _slot_text(time_ms: int, period: int) -> str:
    if period == DAY_MS:
        return _hms(time_ms)
    return f"{_DZ_DAYS[time_ms // DAY_MS]} {_hms(time_ms % DAY_MS)}"


def tz_offset_minutes() -> int:
    """Like JavaScript ``Date.getTimezoneOffset`` (UTC − local, minutes): what
    the dizqueTV web UI sends as ``schedule.timeZoneOffset``."""
    off = datetime.now().astimezone().utcoffset()
    return -int(off.total_seconds() // 60) if off is not None else 0


def plan_actions(
    plan: Mapping[str, Any],
    state: State,
    *,
    only: set[int] | None = None,
    ramadan: bool = False,
    tz_offset: int = 0,
    replace_existing: bool = False,
    prune: bool = False,
    clear_channels: bool = False,
) -> list[Action]:
    lanes: Mapping[str, Any] = plan["lanes"]
    pools: Mapping[str, Any] = plan["filler_pools"]
    channels = [c for c in plan["channels"] if not only or c["number"] in only]
    sched_key = "ramadan_schedule" if ramadan else "schedule"
    actions: list[Action] = []
    lane_ids: dict[str, str] = {}
    pool_ids: dict[str, str] = {}

    used_lanes: list[str] = []
    used_pools: list[str] = []
    for c in channels:
        sch = c["dizquetv"].get(sched_key) or c["dizquetv"]["schedule"]
        for s in sch["slots"]:
            if s["showId"].startswith("lane:") and s["showId"][5:] not in used_lanes:
                used_lanes.append(s["showId"][5:])
        for f in c["dizquetv"]["fillerCollections"]:
            if f["pool"] not in used_pools and f["pool"] in pools:
                used_pools.append(f["pool"])

    def object_action(kind: str, ref: str, label: str, items: Sequence[Mapping[str, Any]],
                      existing: Mapping[str, dict[str, Any]], what: str) -> None:  # fmt: skip
        name = PREFIX + label
        keep, missing = _items_with_keys(items)
        keys = [str(i["plex_rating_key"]) for i in keep]
        body: dict[str, Any] = {"name": name, "content": [_stub(i, state.server) for i in keep]}
        if kind == "filler":
            body["mode"] = "custom"
        create_ep = "/api/show" if kind == "show" else "/api/filler"
        detail = [f"{len(keep)} {what}, {sum(float(i['duration_s']) for i in keep) / 3600:.1f} h"]
        if missing:
            detail.append(f"{missing} item(s) have no Plex item yet: left out")
        if keep:
            detail.append(f"first: {keep[0].get('title', '')[:70]}")
            if len(keep) > 1:
                detail.append(f"last:  {keep[-1].get('title', '')[:70]}")
        ex = existing.get(name)
        ids = lane_ids if kind == "show" else pool_ids
        if not keep:
            actions.append(
                Action("skip", kind, name, detail=detail + ["nothing playable"], ref=ref)
            )
            return
        if ex is None:
            ids[ref] = f"<new {kind}: {ref}>"
            actions.append(Action("create", kind, name, "PUT", create_ep, body, detail, ref))
            return
        verb, d2 = _diff(ex["keys"], keys)
        if verb == "conflict" and replace_existing:
            verb = "update"
        ids[ref] = ex["id"]
        actions.append(
            Action(verb, kind, name, "POST" if verb == "update" else None,
                   f"{create_ep}/{ex['id']}" if verb == "update" else None,
                   body if verb == "update" else None, detail + d2, ref)
        )  # fmt: skip

    for k in used_pools:
        p = pools[k]
        object_action(
            "filler", k, f"{p['name']} [{k}]", filler_items(plan, p["items"]), state.fillers,
            "clips",
        )  # fmt: skip
    for k in used_lanes:
        ln = lanes[k]
        eps = [e for u in ln["units"] for e in u["episodes"]]
        object_action("show", k, f"{ln['name']} [{k}]", eps, state.shows, "episodes in order")

    for c in channels:
        dzc = c["dizquetv"]
        sch = dict(dzc.get(sched_key) or dzc["schedule"])
        slots: list[dict[str, Any]] = []
        for s in sch["slots"]:
            sid = s["showId"]
            if sid.startswith("lane:"):
                lid = lane_ids.get(sid[5:])
                sid = f"custom.{lid}" if lid else "flex."  # nothing playable yet → flex
            if slots and slots[-1]["showId"] == sid:
                continue  # merge with the previous slot
            slots.append({**s, "showId": sid, "order": "shuffle" if sid == "flex." else s["order"]})
        if len(slots) > 1 and slots[0]["showId"] == slots[-1]["showId"]:
            slots = slots[1:]  # the last slot runs on past midnight into the first one
        sch["slots"] = slots
        sch["timeZoneOffset"] = tz_offset
        fillers = [
            {"id": pool_ids[f["pool"]], "weight": f["weight"], "cooldown": f["cooldown"]}
            for f in dzc["fillerCollections"]
            if f["pool"] in pool_ids
        ]
        body = {
            "number": c["number"],
            "name": dzc["name"],
            "groupTitle": "dizqueTV",
            "programs": "<computed by POST /api/channel-tools/time-slots>",
            "fillerCollections": fillers,
            "fillerRepeatCooldown": dzc["fillerRepeatCooldown"],
            "scheduleBackup": sch,
            "maktabaPlan": {"key": c["key"], "planner_version": plan["planner_version"]},
            **channel_art(plan, c["key"]),
        }
        per = int(sch["period"])
        names = {lane_ids.get(k): lanes[k]["name"] for k in lanes}
        grid = " | ".join(
            f"{_slot_text(int(s['time']), per)} "
            f"{names.get(s['showId'][7:], 'flex') if s['showId'].startswith('custom.') else 'flex'}"
            for s in slots
        )
        detail = [
            f"time slots ({'weekly' if per != DAY_MS else 'daily'}, tz offset {tz_offset} min, "
            f"lateness {int(sch['lateness']) // 60_000} min, maxDays {sch['maxDays']}): {grid}",
            "filler: " + (", ".join(f"{f['pool']} w{f['weight']}" for f in dzc["fillerCollections"])
                          or "none"),
            "programs: POST /api/channel-tools/time-slots {programs: every lane episode with "
            "customShowId/customOrder, schedule} → channel.programs",
        ]  # fmt: skip
        if ramadan and not dzc.get("ramadan_schedule"):
            detail.append("no Ramadan grid for this channel: regular grid used")
        ex = state.channels.get(c["number"])
        if ex is None:
            new_body = {**_new_channel_defaults(), **body}
            actions.append(Action("create", "channel", f"#{c['number']} {dzc['name']}", "POST",
                                  "/api/channel", new_body, detail, c["key"]))  # fmt: skip
            continue
        ours = isinstance(ex.get("maktabaPlan"), dict)
        if not ours and not replace_existing:
            actions.append(Action(
                "conflict", "channel", f"#{c['number']} {dzc['name']}", detail=detail + [
                    f"channel {c['number']} exists ('{ex.get('name')}') and was not created by this "
                    "tool: needs --replace-existing (or renumber the plan)"], ref=c["key"]))  # fmt: skip
            continue
        upd = {**ex, **{k: body[k] for k in ("programs", "fillerCollections", "scheduleBackup",
                                             "maktabaPlan", "fillerRepeatCooldown", "icon",
                                             "watermark") if k in body}}  # fmt: skip
        actions.append(Action("update", "channel", f"#{c['number']} {dzc['name']}", "PUT",
                              "/api/channel", upd, detail + [
                                  f"keeps name '{ex.get('name')}', icon, watermark, transcoding; "
                                  "replaces programs, filler lists and schedule"], c["key"]))  # fmt: skip
    if (prune or clear_channels) and not only:
        actions.extend(_removals(plan, state, actions, clear_channels=clear_channels))
    return actions


def _removals(plan: Mapping[str, Any], state: State, actions: Sequence[Action], *,
              clear_channels: bool) -> list[Action]:  # fmt: skip
    """What the plan no longer has: this tool's channels, shows and filler lists, and
    with ``clear_channels`` every other channel too."""
    out: list[Action] = []
    numbers = {int(c["number"]) for c in plan["channels"]}
    for n, ch in sorted(state.channels.items()):
        if n in numbers:
            continue
        ours = isinstance(ch.get("maktabaPlan"), dict)
        if ours or clear_channels:
            out.append(Action("delete", "channel", f"#{n} {ch.get('name', '')}", "DELETE",
                              "/api/channel", {"number": n},
                              ["made by this tool, not in the plan" if ours
                               else "not in the plan (--clear-channels)"]))  # fmt: skip
    planned = {a.name for a in actions if a.kind in ("show", "filler")}
    for kind, have in (("show", state.shows), ("filler", state.fillers)):
        for name, info in sorted(have.items()):
            if name.startswith(PREFIX) and name not in planned:
                out.append(Action("delete", kind, name, "DELETE", f"/api/{kind}/{info['id']}",
                                  None, ["made by this tool, not in the plan"]))  # fmt: skip
    return out


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
    if (wm := str(cfg.get("dz_watermark_file") or "")) and Path(wm).is_file():
        margin = float(cfg.get("dz_watermark_margin", 2.0))
        out["watermark"] = {
            "enabled": True, "url": wm, "width": float(cfg.get("dz_watermark_width", 12.0)),
            "verticalMargin": margin, "horizontalMargin": margin, "duration": 0,
            "position": str(cfg.get("dz_watermark_position") or "top-right"),
            "fixedSize": False, "animated": wm.lower().endswith(".gif"),
        }  # fmt: skip
    return out


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


def _hosted(base: str, path: str) -> str:
    """A logo on a web host (``dz_art_url``): its link, checked to answer."""
    link = f"{base.rstrip('/')}/{urllib.parse.quote(Path(path).name)}"
    req = urllib.request.Request(link, method="HEAD")
    try:
        with urllib.request.urlopen(req, timeout=30) as r:  # noqa: S310 — the user's host
            ok = 200 <= r.status < 300
    except OSError as e:
        raise RuntimeError(f"logo {Path(path).name} is not at {link}: {e}") from e
    if not ok:
        raise RuntimeError(f"logo {Path(path).name} is not at {link}")
    return link


# ------------------------------------------------------------ continuity on a rewrite


def fingerprint(sched: Mapping[str, Any], programs: Sequence[tuple[str, int, str]]) -> str:
    """What a channel's timeline is computed from: the same schedule and programmes give
    the same timeline, so the channel keeps the one it airs (a logo change is no reason
    to rebuild it). ``programs``: (ratingKey, duration, show id) in each show's order."""
    src = {"schedule": sched, "programs": [list(p) for p in programs]}
    return hashlib.sha1(json.dumps(src, sort_keys=True).encode()).hexdigest()


def aired(old: Mapping[str, Any], now_ms: int) -> tuple[list[Mapping[str, Any]], int, int]:
    """The old timeline up to now: the programmes aired so far (the one on air last),
    and when the one on air started and ends (ms). Nothing on air: ``([], now, now)``."""
    progs = old.get("programs") or []
    total = sum(int(p.get("duration", 0)) for p in progs)
    try:
        start = int(datetime.fromisoformat(str(old["startTime"]).replace("Z", "+00:00"))
                    .timestamp() * 1000)  # fmt: skip
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
    """The new timeline, but what is on air now finishes first: the new one takes over
    when it ends (the time until the new one's next programme is filler), so a rewrite
    never cuts a programme a viewer is watching."""
    so_far, on_start, on_end = aired(old, now_ms)
    if not so_far or so_far[-1].get("isOffline"):
        return dict(new)
    n0 = int(datetime.fromisoformat(str(new["startTime"]).replace("Z", "+00:00"))
             .timestamp() * 1000)  # fmt: skip
    out: list[Mapping[str, Any]] = [so_far[-1]]
    t = n0
    gap_from = on_end
    for p in new["programs"]:
        d = int(p.get("duration", 0))
        if t + d <= on_end:  # over before the old programme ends
            t += d
            continue
        if t < on_end:  # straddles the switch: its time is filler
            t += d
            continue
        if t > gap_from:
            out.append({"duration": t - gap_from, "isOffline": True})
        gap_from = t + d
        out.append(p)
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


def _upload(base: str, path: str, name: str) -> str:
    """``POST /api/upload/image`` (multipart, field ``image``) → the file's URL."""
    import uuid

    boundary = uuid.uuid4().hex
    data = Path(path).read_bytes()
    ctype = "image/gif" if name.lower().endswith(".gif") else "image/png"
    payload = (
        (
            f'--{boundary}\r\nContent-Disposition: form-data; name="image"; filename="{name}"\r\n'
            f"Content-Type: {ctype}\r\n\r\n"
        ).encode()
        + data
        + f"\r\n--{boundary}--\r\n".encode()
    )
    req = urllib.request.Request(
        base.rstrip("/") + "/api/upload/image", data=payload, method="POST",
        headers={"Content-Type": f"multipart/form-data; boundary={boundary}"},
    )  # fmt: skip
    with urllib.request.urlopen(req, timeout=120) as r:  # noqa: S310 — local API
        res = json.loads(r.read().decode("utf-8"))
    if not res.get("status"):
        raise RuntimeError(f"upload {name}: {res.get('message')}")
    return str(res["data"]["fileUrl"])


def _new_channel_defaults() -> dict[str, Any]:
    """What the web UI gives a new channel (``channel-config.js``)."""
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


def apply_actions(
    url: str,
    actions: Sequence[Action],
    server: Any,
    plan: Mapping[str, Any],
    conn: Any = None,
    **plan_kw: Any,
) -> None:
    """Write the plan: shows and filler lists first (programmes built from Plex
    metadata), then — with dizqueTV re-read so slots name the real ``custom.ID`` and
    filler ids — each channel's programmes via ``POST /api/channel-tools/time-slots``,
    saved with ``POST``/``PUT /api/channel``. Every object is read back."""
    from .dizquetv_shows import _Dz, _metadata, _program, as_registered

    dzw = _Dz(url)
    # what the plan no longer has goes first: channels (they name the shows and lists),
    # then the shows and filler lists; all are checked gone with one re-read per kind
    removals = sorted((a for a in actions if a.verb == "delete"),
                      key=lambda a: a.kind != "channel")  # fmt: skip
    for a in removals:
        assert a.endpoint is not None
        dzw.call("DELETE", a.endpoint, a.body)
    if removals:
        channels_left = {int(n) for n in dzw.call("GET", "/api/channels")}
        ids_left = {k: {str(x.get("id")) for x in dzw.call("GET", f"/api/{k}s")}
                    for k in ("show", "filler") if any(a.kind == k for a in removals)}  # fmt: skip
        for a in removals:
            assert a.endpoint is not None
            gone = (int((a.body or {})["number"]) not in channels_left if a.kind == "channel"
                    else a.endpoint.rsplit("/", 1)[-1] not in ids_left[a.kind])  # fmt: skip
            if not gone:
                raise RuntimeError(f"{a.kind} {a.name}: still there after DELETE")
        print(f"  ✓  deleted {len(removals)}: "
              + ", ".join(f"{sum(a.kind == k for a in removals)} {k}(s)"
                          for k in ("channel", "show", "filler")))  # fmt: skip
    server = as_registered(server, dzw.call("GET", "/api/plex-servers"))
    urls: dict[str, str] = {}

    cfg = plan.get("config") or {}

    def uploaded(path: str) -> str:
        """A logo's link, once per run: on the logos' web host (dz_art_url), else
        uploaded to dizqueTV under a name of its own (maktaba-<file>)."""
        if path not in urls:
            if host := str(cfg.get("dz_art_url") or ""):
                urls[path] = _hosted(host, path)
                print(f"  ✓  {Path(path).name} at {urls[path]}")
            else:
                got = _upload(url, path, f"maktaba-{Path(path).name}")
                urls[path] = art_link(cfg, url, got)
                print(f"  ✓  uploaded {Path(path).name} → {urls[path]}")
        return urls[path]

    objs = [a for a in actions if a.kind in ("show", "filler") and a.verb in ("create", "update")]
    keys = sorted({str(i["ratingKey"]) for a in objs for i in (a.body or {}).get("content", [])})
    # the plex-index step's saved records first: only what it lacks is asked of Plex
    # (all of a big plan from Plex is 100k+ lookups, hours while Plex is busy)
    meta = _metadata(server, keys, conn=conn) if keys else {}
    for a in objs:
        assert a.body is not None and a.endpoint is not None
        content = [
            prog
            for i in a.body["content"]
            if (m := meta.get(str(i["ratingKey"]))) and (prog := _program(m, server))
        ]
        body = {**a.body, "content": content}
        if a.verb == "create":
            oid = dzw.call("PUT", a.endpoint, body)["id"]
        else:
            dzw.call("POST", a.endpoint, body)
            oid = a.endpoint.rsplit("/", 1)[-1]
        back = dzw.call("GET", f"/api/{a.kind}/{oid}")
        got = [str(x.get("ratingKey")) for x in back.get("content", [])]
        if got != [x["ratingKey"] for x in content]:
            raise RuntimeError(
                f"{a.name}: dizqueTV holds {len(got)} items, expected {len(content)}"
            )
        print(f"  ✓  {a.kind} {a.name}: {len(content)} items")

    # second pass: every lane / pool now exists, so channel slots resolve to real ids
    state = read_state(DizqueTV(url))
    shows_by_id: dict[str, dict[str, Any]] = {}
    for c_act in plan_actions(plan, state, **plan_kw):
        if c_act.kind != "channel" or c_act.verb not in ("create", "update"):
            if c_act.kind == "channel":
                print(f"  -  channel {c_act.name}: {c_act.verb}")
            continue
        assert c_act.body is not None
        body = dict(c_act.body)
        # the logo and the corner watermark: local files uploaded once, then their URLs
        if isinstance(body.get("icon"), str) and not body["icon"].startswith("http"):
            body["icon"] = uploaded(body["icon"])
        if isinstance(body.get("watermark"), dict) and body["watermark"].get("enabled"):
            wm = dict(body["watermark"])
            if not str(wm["url"]).startswith("http"):
                wm["url"] = uploaded(wm["url"])
            body["watermark"] = wm
        sched = body["scheduleBackup"]
        programs: list[dict[str, Any]] = []
        slot_ms = slot_lengths(sched)
        number = int(body["number"])
        old = (dzw.call("GET", f"/api/channel/{number}")
               if number in state.channels else None)  # fmt: skip
        now_ms = int(time.time() * 1000)
        so_far: list[Mapping[str, Any]] = aired(old, now_ms)[0] if old else []
        base: list[tuple[str, int, str]] = []  # the programmes before any queue goes on
        for sid in dict.fromkeys(s["showId"] for s in sched["slots"]):
            if not sid.startswith("custom."):
                continue
            show_id = sid[len("custom.") :]
            if show_id not in shows_by_id:
                shows_by_id[show_id] = dzw.call("GET", f"/api/show/{show_id}")
            show = shows_by_id[show_id]
            content = show.get("content", [])
            # only what the schedule can air before dizqueTV repeats it (maxDays): the mix
            # channel's whole archive (55,000 episodes) is past its 50 MB request limit
            cap = airable(sched, slot_ms.get(sid, []), [int(p["duration"]) for p in content])
            base.extend((str(p.get("ratingKey")), int(p.get("duration", 0)), show_id)
                        for p in content[:cap])  # fmt: skip
            content = [dict(p) for p in resume(content, so_far, show_id)]
            for n, prog in enumerate(content[:cap]):
                programs.append({**prog, "customShowId": show_id,
                                 "customShowName": show["name"], "customOrder": n})  # fmt: skip
        if not programs:
            print(f"  -  channel {c_act.name}: nothing playable yet, skipped")
            continue
        fp = fingerprint(sched, base)
        body["maktabaPlan"] = {**body["maktabaPlan"], "fingerprint": fp}
        if old and (old.get("maktabaPlan") or {}).get("fingerprint") == fp:
            # the same timeline: kept as it airs (only the name, logo … change)
            body["programs"] = old["programs"]
            body["startTime"] = old["startTime"]
            res = {"programs": old["programs"]}
            if all(old.get(k) == v for k, v in body.items()):
                print(f"  =  channel {c_act.name}: unchanged, left as it airs")
                continue  # a save restarts the stream of anyone watching: none at all
        else:
            res = dzw.call("POST", "/api/channel-tools/time-slots",
                           {"programs": programs, "schedule": sched})  # fmt: skip
            if old:  # what is on air finishes; the queues went on above
                res = splice(old, res, int(time.time() * 1000), int(sched["period"]))
            body["programs"] = res["programs"]
            body["startTime"] = res["startTime"]
        dzw.call(c_act.method or "POST", "/api/channel", body)
        back = dzw.call("GET", f"/api/channel/programless/{body['number']}")
        if not isinstance(back.get("maktabaPlan"), dict) or back.get("name") != body["name"]:
            raise RuntimeError(f"channel {body['number']}: read-back does not match")
        if "icon" in body and back.get("icon") != body["icon"]:
            raise RuntimeError(f"channel {body['number']}: its logo did not stick")
        print(f"  ✓  channel {c_act.name}: {len(res['programs'])} programmes, "
              f"{len(body['fillerCollections'])} filler lists")  # fmt: skip


# --------------------------------------------------------------------------- output


def render(actions: Sequence[Action], *, base: str, online: bool) -> str:
    lines = [
        f"dizqueTV {base} — plan"
        + ("" if online else " (offline: current dizqueTV state not read, everything is new)")
    ]
    titles = {
        "filler": "FILLER LISTS",
        "show": "CUSTOM SHOWS (one per lane)",
        "channel": "CHANNELS",
    }
    for kind in ("filler", "show", "channel"):
        acts = [a for a in actions if a.kind == kind]
        if not acts:
            continue
        lines.append(f"\n{titles[kind]}")
        for a in acts:
            call = f"{a.method} {a.endpoint}" if a.method else "-"
            lines.append(f"  {a.verb:9} {call:28} {a.name}")
            lines.extend(f"  {'':9} {'':28}   {d}" for d in a.detail)
    counts: dict[str, int] = {}
    for a in actions:
        counts[f"{a.verb} {a.kind}"] = counts.get(f"{a.verb} {a.kind}", 0) + 1
    lines.append("\n" + ", ".join(f"{v} × {k}" for k, v in sorted(counts.items())))
    return "\n".join(lines)


def run(
    plan_path: Path,
    *,
    url: str = "http://localhost:8002",
    offline: bool = False,
    only: set[int] | None = None,
    ramadan: bool = False,
    tz_offset: int | None = None,
    replace_existing: bool = False,
    dump: Path | None = None,
    apply: bool = False,
    server: Any = None,
    conn: Any = None,
    prune: bool = False,
    clear_channels: bool = False,
) -> list[Action]:
    plan = json.loads(plan_path.read_text(encoding="utf-8"))
    dz = DizqueTV(url)
    state = State() if offline else read_state(dz)
    actions = plan_actions(
        plan,
        state,
        only=only,
        ramadan=ramadan,
        tz_offset=tz_offset_minutes() if tz_offset is None else tz_offset,
        replace_existing=replace_existing,
        prune=prune,
        clear_channels=clear_channels,
    )
    print(render(actions, base=url, online=state.online))
    if apply:
        if server is None:
            raise SystemExit(
                "--apply needs the Plex server (--dizquetv-db or --plex-url/--plex-token)"
            )
        print("\napplying …")
        apply_actions(
            url, actions, server, plan, only=only, ramadan=ramadan,
            tz_offset=tz_offset_minutes() if tz_offset is None else tz_offset,
            replace_existing=replace_existing, conn=conn,
        )  # fmt: skip
    if dump is not None:  # local files only: the request bodies --apply would send
        dump.mkdir(parents=True, exist_ok=True)
        for i, a in enumerate(actions):
            if a.body is not None:
                (dump / f"{i:03d}-{a.kind}-{a.verb}.json").write_text(
                    json.dumps({"method": a.method, "endpoint": a.endpoint, "body": a.body},
                               ensure_ascii=False, indent=1), encoding="utf-8")  # fmt: skip
        print(f"\nrequest bodies: {dump}")
    return actions


def main(argv: list[str] | None = None) -> int:
    import argparse

    ap = argparse.ArgumentParser(prog="export_dizquetv", description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)  # fmt: skip
    ap.add_argument("plan", type=Path, help="channel-plan.json from plan-channels")
    ap.add_argument("--url", default="http://localhost:8002")
    ap.add_argument("--offline", action="store_true", help="don't read dizqueTV's current state")
    ap.add_argument("--channel", type=int, action="append", help="only this channel number")
    ap.add_argument("--ramadan", action="store_true", help="use the Ramadan grids")
    ap.add_argument("--tz-offset", type=int, help="minutes, UTC − local (default: this machine)")
    ap.add_argument("--replace-existing", action="store_true")
    ap.add_argument(
        "--prune",
        action="store_true",
        help="delete this tool's channels / shows / filler lists the plan no longer has",
    )
    ap.add_argument(
        "--clear-channels",
        action="store_true",
        help="also delete every other channel that is not in the plan",
    )
    ap.add_argument("--dump", type=Path, help="write the request bodies to this folder")
    a = ap.parse_args(argv)
    run(a.plan, url=a.url, offline=a.offline, only=set(a.channel or []) or None,
        ramadan=a.ramadan, tz_offset=a.tz_offset, replace_existing=a.replace_existing,
        dump=a.dump, prune=a.prune, clear_channels=a.clear_channels)  # fmt: skip
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
