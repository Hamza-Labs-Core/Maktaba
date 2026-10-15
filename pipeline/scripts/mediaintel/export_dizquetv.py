"""export-dizquetv: create / update the planned channels in dizqueTV (dry run unless --apply).

Reads ``channel-plan.json`` (``plan-channels``) and the current dizqueTV state
(read-only ``GET`` calls: ``/api/channels``, ``/api/channel/programless/N``,
``/api/shows``, ``/api/show/ID``, ``/api/fillers``, ``/api/filler/ID``,
``/api/plex-servers``), then prints every object the plan maps to, with the
real endpoint that would write it:

======================== ================================= ==========================
plan                     dizqueTV object                   endpoint
======================== ================================= ==========================
lane (ordered seasons)   custom show ``maktaba-plan · …``  ``PUT /api/show`` (create),
                         (episodes in lane order)          ``POST /api/show/ID``
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
"""

from __future__ import annotations

import json
import urllib.parse
import urllib.request
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from datetime import datetime
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
    verb: str  # create | update | unchanged | conflict | skip
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
        object_action("filler", k, f"{p['name']} [{k}]", p["items"], state.fillers, "clips")
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
                                             "maktabaPlan", "fillerRepeatCooldown")}}  # fmt: skip
        actions.append(Action("update", "channel", f"#{c['number']} {dzc['name']}", "PUT",
                              "/api/channel", upd, detail + [
                                  f"keeps name '{ex.get('name')}', icon, watermark, transcoding; "
                                  "replaces programs, filler lists and schedule"], c["key"]))  # fmt: skip
    return actions


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
    url: str, actions: Sequence[Action], server: Any, plan: Mapping[str, Any], **plan_kw: Any
) -> None:
    """Write the plan: shows and filler lists first (programmes built from Plex
    metadata), then — with dizqueTV re-read so slots name the real ``custom.ID`` and
    filler ids — each channel's programmes via ``POST /api/channel-tools/time-slots``,
    saved with ``POST``/``PUT /api/channel``. Every object is read back."""
    from .dizquetv_shows import _Dz, _metadata, _program

    dzw = _Dz(url)
    objs = [a for a in actions if a.kind in ("show", "filler") and a.verb in ("create", "update")]
    keys = sorted({str(i["ratingKey"]) for a in objs for i in (a.body or {}).get("content", [])})
    meta = _metadata(server, keys) if keys else {}
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
        sched = body["scheduleBackup"]
        programs: list[dict[str, Any]] = []
        for sid in dict.fromkeys(s["showId"] for s in sched["slots"]):
            if not sid.startswith("custom."):
                continue
            show_id = sid[len("custom.") :]
            if show_id not in shows_by_id:
                shows_by_id[show_id] = dzw.call("GET", f"/api/show/{show_id}")
            show = shows_by_id[show_id]
            for n, prog in enumerate(show.get("content", [])):
                programs.append({**prog, "customShowId": show_id,
                                 "customShowName": show["name"], "customOrder": n})  # fmt: skip
        if not programs:
            print(f"  -  channel {c_act.name}: nothing playable yet, skipped")
            continue
        res = dzw.call("POST", "/api/channel-tools/time-slots",
                       {"programs": programs, "schedule": sched})  # fmt: skip
        body["programs"] = res["programs"]
        body["startTime"] = res["startTime"]
        dzw.call(c_act.method or "POST", "/api/channel", body)
        back = dzw.call("GET", f"/api/channel/programless/{body['number']}")
        if not isinstance(back.get("maktabaPlan"), dict) or back.get("name") != body["name"]:
            raise RuntimeError(f"channel {body['number']}: read-back does not match")
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
            replace_existing=replace_existing,
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
    ap.add_argument("--dump", type=Path, help="write the request bodies to this folder")
    a = ap.parse_args(argv)
    run(a.plan, url=a.url, offline=a.offline, only=set(a.channel or []) or None,
        ramadan=a.ramadan, tz_offset=a.tz_offset, replace_existing=a.replace_existing,
        dump=a.dump)  # fmt: skip
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
