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

import json
import urllib.parse
import urllib.request
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from maktaba_pipeline.integrations.dizquetv import PREFIX, image_upload_body, tz_offset_minutes
from maktaba_pipeline.integrations.dizquetv_export import (
    DAY_MS,
    GUIDE_KEYS,
    airable,
    aired,
    art_link,
    channel_art,
    filler_items,
    fingerprint,
    guide_settings,
    hosted_link,
    removals,
    resolve_slots,
    resume,
    slot_lengths,
    slot_text,
    splice,
    versioned,
    write_channel,
)
from maktaba_pipeline.integrations.dizquetv_export import (
    new_channel_defaults as _new_channel_defaults,
)

__all__ = [
    "GUIDE_KEYS",
    "PREFIX",
    "airable",
    "aired",
    "art_link",
    "channel_art",
    "filler_items",
    "fingerprint",
    "guide_settings",
    "resume",
    "slot_lengths",
    "splice",
]


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


def _items_with_keys(items: Sequence[Mapping[str, Any]]) -> tuple[list[Mapping[str, Any]], int]:
    keep = [i for i in items if i.get("plex_rating_key")]
    return keep, len(items) - len(keep)


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
        sch = resolve_slots(sch, lane_ids, tz_offset)
        slots = sch["slots"]
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
            **guide_settings(plan),
        }
        per = int(sch["period"])
        names = {lane_ids.get(k): lanes[k]["name"] for k in lanes}
        grid = " | ".join(
            f"{slot_text(int(s['time']), per)} "
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
                                             "watermark", *GUIDE_KEYS) if k in body}}  # fmt: skip
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
    gone = removals(
        {int(c["number"]) for c in plan["channels"]}, state.channels, state.shows, state.fillers,
        {a.name for a in actions if a.kind in ("show", "filler")}, PREFIX,
        clear_channels=clear_channels)  # fmt: skip
    return [Action("delete", r.kind, r.name, "DELETE", r.endpoint, r.body, [r.why.replace(
        "clear channels", "--clear-channels")]) for r in gone]  # fmt: skip


def _upload(base: str, path: str, name: str) -> str:
    """``POST /api/upload/image`` (multipart, field ``image``) → the file's URL."""
    ctype, payload = image_upload_body(Path(path).read_bytes(), name)
    req = urllib.request.Request(
        base.rstrip("/") + "/api/upload/image", data=payload, method="POST",
        headers={"Content-Type": ctype},
    )  # fmt: skip
    with urllib.request.urlopen(req, timeout=120) as r:  # noqa: S310 — local API
        res = json.loads(r.read().decode("utf-8"))
    if not res.get("status"):
        raise RuntimeError(f"upload {name}: {res.get('message')}")
    return str(res["data"]["fileUrl"])


def _head_status(link: str) -> int:
    try:
        with urllib.request.urlopen(  # noqa: S310 — the user's host
            urllib.request.Request(link, method="HEAD"), timeout=30
        ) as r:
            return int(r.status)
    except OSError:
        return 0


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
    if removals:
        print(f"phase: removing {len(removals)} channels / shows / filler lists", flush=True)
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
                urls[path] = hosted_link(host, path, _head_status)
                print(f"  ✓  {Path(path).name} at {urls[path]}")
            else:
                got = _upload(url, path, f"maktaba-{Path(path).name}")
                urls[path] = versioned(art_link(cfg, url, got), path)
                print(f"  ✓  uploaded {Path(path).name} → {urls[path]}")
        return urls[path]

    objs = [a for a in actions if a.kind in ("show", "filler") and a.verb in ("create", "update")]
    keys = sorted({str(i["ratingKey"]) for a in objs for i in (a.body or {}).get("content", [])})
    # the plex-index step's saved records first: only what it lacks is asked of Plex
    # (all of a big plan from Plex is 100k+ lookups, hours while Plex is busy)
    print(f"phase: episode details for {len(objs)} shows and filler lists", flush=True)
    meta = _metadata(server, keys, conn=conn) if keys else {}
    print("phase: shows and filler lists", flush=True)
    for k_obj, a in enumerate(objs, 1):
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
        print(f"  [{k_obj}/{len(objs)}] ✓  {a.kind} {a.name}: {len(content)} items", flush=True)

    # second pass: every lane / pool now exists, so channel slots resolve to real ids
    state = read_state(DizqueTV(url))
    shows_by_id: dict[str, dict[str, Any]] = {}
    ch_acts = [a for a in plan_actions(plan, state, **plan_kw) if a.kind == "channel"]
    print("phase: channels", flush=True)
    for k_ch, c_act in enumerate(ch_acts, 1):
        at = f"  [{k_ch}/{len(ch_acts)}]"
        if c_act.verb not in ("create", "update"):
            print(f"{at} -  channel {c_act.name}: {c_act.verb}", flush=True)
            continue
        assert c_act.body is not None
        body = dict(c_act.body)
        # the logo and the corner watermark: local files uploaded once, then their URLs
        if body.get("icon") and not str(body["icon"]).startswith("http"):  # "": no logo
            body["icon"] = uploaded(body["icon"])
        if isinstance(body.get("watermark"), dict) and body["watermark"].get("enabled"):
            wm = dict(body["watermark"])
            if not str(wm["url"]).startswith("http"):
                wm["url"] = uploaded(wm["url"])
            body["watermark"] = wm
        number = int(body["number"])
        done = write_channel(dzw.call, body, c_act.method or "POST", exists=number in state.channels,
                             shows=shows_by_id, keep_airing=True)  # fmt: skip
        if done.status == "skipped":
            print(f"{at} -  channel {c_act.name}: nothing playable yet, skipped", flush=True)
            continue
        if done.status == "unchanged":
            print(f"{at} =  channel {c_act.name}: unchanged, left as it airs", flush=True)
            continue
        print(f"{at} ✓  channel {c_act.name}: {done.programmes} programmes, "
              f"{done.fillers} filler lists")  # fmt: skip


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
