"""The ``dizquetv_channels`` stage: write the latest channel plan into dizqueTV.

Ported from ``scripts/mediaintel/export_dizquetv.py``. Split as plan notes N1/N1a ask:

* :func:`plan_actions` is pure: the plan JSON (the newest ``channel_plans`` row)
  against the current dizqueTV :class:`~maktaba_pipeline.integrations.dizquetv.State`
  gives a list of :class:`Action`; :func:`changes_from` turns it into the typed
  :class:`~maktaba_pipeline.engine.changes.ChangeSet`;
* :func:`apply_actions` performs it (blocking; the handler runs it in a thread).

Safety rules: a show or filler list this tool did not create (no ``maktaba-plan``
name prefix) and a channel without the ``maktabaPlan`` marker are **never**
touched (they are reported as conflicts); an existing channel keeps every
setting except its programmes, filler lists and schedule; every written object
is read back and verified (``readback_mismatch``).
"""

from __future__ import annotations

import asyncio
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any

from ...engine.changes import ChangeSet, apply_or_review
from ...engine.context import StageContext, StageFailure
from ...engine.graph import StageResult, Subject
from ...engine.hashing import canonical_hash
from ...engine.sql import loads
from ...integrations.dizquetv import (
    PREFIX,
    DizqueError,
    DizqueTV,
    State,
    program_from_plex,
    read_state,
)
from ...integrations.plex import PlexClient, PlexError, TransportError
from ..links import Link, get_links, put_link

__all__ = [
    "CHANNEL_TARGET",
    "DAY_MS",
    "Action",
    "ChannelsService",
    "apply_actions",
    "changes_from",
    "fingerprint",
    "plan_actions",
    "run_channels",
]

CHANNEL_TARGET = "dizquetv_channel"
DAY_MS = 86_400_000


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
    #: hash of the desired content (stable across runs)
    digest: str = ""


# ------------------------------------------------------------------ pure planning


def _stub(item: Mapping[str, Any], server: str | None) -> dict[str, Any]:
    """Stand-in for a programme in the planned body (Plex is not called while planning)."""
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
        return "conflict", ["same name, not created by this tool: left alone"]
    if list(old) == list(new):
        return "unchanged", []
    add = len(set(new) - set(old))
    rem = len(set(old) - set(new))
    return "update", [f"+{add} -{rem}" + (" (reordered)" if not add and not rem else "")]


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


def plan_actions(plan: Mapping[str, Any], state: State, *, tz_offset: int = 0) -> list[Action]:
    """Every dizqueTV object the plan maps to, with what would be done to it."""
    lanes: Mapping[str, Any] = plan["lanes"]
    pools: Mapping[str, Any] = plan["filler_pools"]
    channels = [c for c in plan["channels"] if (c.get("exports") or {}).get("dizquetv")]
    actions: list[Action] = []
    lane_ids: dict[str, str] = {}
    pool_ids: dict[str, str] = {}
    keys_of: dict[str, list[str]] = {}  # "show:lane" / "filler:pool" -> ordered rating keys

    used_lanes: list[str] = []
    used_pools: list[str] = []
    for c in channels:
        sch = c["exports"]["dizquetv"]["schedule"]
        for s in sch["slots"]:
            if s["showId"].startswith("lane:") and s["showId"][5:] not in used_lanes:
                used_lanes.append(s["showId"][5:])
        for f in c["exports"]["dizquetv"]["fillerCollections"]:
            if f["pool"] not in used_pools and f["pool"] in pools:
                used_pools.append(f["pool"])

    def object_action(
        kind: str,
        ref: str,
        label: str,
        items: Sequence[Mapping[str, Any]],
        existing: Mapping[str, dict[str, Any]],
        what: str,
    ) -> None:
        name = PREFIX + label
        keep = [i for i in items if i.get("plex_rating_key")]
        missing = len(items) - len(keep)
        keys = [str(i["plex_rating_key"]) for i in keep]
        keys_of[f"{kind}:{ref}"] = keys
        body: dict[str, Any] = {"name": name, "content": [_stub(i, state.server) for i in keep]}
        if kind == "filler":
            body["mode"] = "custom"
        create_ep = "/api/show" if kind == "show" else "/api/filler"
        detail = [f"{len(keep)} {what}, {sum(float(i['duration_s']) for i in keep) / 3600:.1f} h"]
        if missing:
            detail.append(f"{missing} item(s) have no Plex item yet: left out")
        ex = existing.get(name)
        ids = lane_ids if kind == "show" else pool_ids
        digest = canonical_hash({"name": name, "keys": keys})
        if not keep:
            actions.append(
                Action("skip", kind, name, detail=[*detail, "nothing playable"], ref=ref)
            )
            return
        if ex is None:
            ids[ref] = f"<new {kind}: {ref}>"
            actions.append(
                Action("create", kind, name, "PUT", create_ep, body, detail, ref, digest)
            )
            return
        verb, d2 = _diff(ex["keys"], keys)
        if verb == "conflict":  # never overwrite what the user made
            actions.append(Action("conflict", kind, name, detail=[*detail, *d2], ref=ref))
            return
        ids[ref] = ex["id"]
        actions.append(
            Action(
                verb,
                kind,
                name,
                "POST" if verb == "update" else None,
                f"{create_ep}/{ex['id']}" if verb == "update" else None,
                body if verb == "update" else None,
                [*detail, *d2],
                ref,
                digest,
            )
        )

    for k in used_pools:
        p = pools[k]
        object_action("filler", k, f"{p['name']} [{k}]", p["items"], state.fillers, "clips")
    for k in used_lanes:
        ln = lanes[k]
        eps = [e for u in ln["units"] for e in u["episodes"]]
        object_action("show", k, f"{ln['name']} [{k}]", eps, state.shows, "episodes in order")

    for c in channels:
        dzc = c["exports"]["dizquetv"]
        sch = dict(dzc["schedule"])
        slots: list[dict[str, Any]] = []
        for s in sch["slots"]:
            sid = s["showId"]
            if sid.startswith("lane:"):
                lid = lane_ids.get(sid[5:])
                sid = f"custom.{lid}" if lid else "flex."  # nothing playable yet: flex
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
        lane_refs = sorted(
            {s["showId"][5:] for s in dzc["schedule"]["slots"] if s["showId"].startswith("lane:")}
        )
        digest = canonical_hash(
            {
                "schedule": sch,
                "fillers": fillers,
                "cooldown": dzc["fillerRepeatCooldown"],
                "lanes": {r: keys_of.get(f"show:{r}", []) for r in lane_refs},
                "pools": {
                    f["pool"]: keys_of.get(f"filler:{f['pool']}", [])
                    for f in dzc["fillerCollections"]
                },
            }
        )
        marker = {"key": c["key"], "planner_version": plan["planner_version"], "hash": digest}
        body = {
            "number": c["number"],
            "name": dzc["name"],
            "groupTitle": "dizqueTV",
            "programs": "<computed by POST /api/channel-tools/time-slots>",
            "fillerCollections": fillers,
            "fillerRepeatCooldown": dzc["fillerRepeatCooldown"],
            "scheduleBackup": sch,
            "maktabaPlan": marker,
        }
        label = f"#{c['number']} {dzc['name']}"
        detail = [
            f"time slots ({len(slots)}), tz offset {tz_offset} min",
            "filler: "
            + (
                ", ".join(f"{f['pool']} w{f['weight']}" for f in dzc["fillerCollections"]) or "none"
            ),
        ]
        ex = state.channels.get(c["number"])
        if ex is None:
            actions.append(
                Action(
                    "create",
                    "channel",
                    label,
                    "POST",
                    "/api/channel",
                    {**new_channel_defaults(), **body},
                    detail,
                    c["key"],
                    digest,
                )
            )
            continue
        ours = isinstance(ex.get("maktabaPlan"), dict)
        if not ours:  # never overwrite a channel the user made
            detail.append(
                f"channel {c['number']} exists ('{ex.get('name')}') and was not created by this "
                "tool: left alone"
            )
            actions.append(Action("conflict", "channel", label, detail=detail, ref=c["key"]))
            continue
        if ex["maktabaPlan"].get("hash") == digest:
            actions.append(
                Action("unchanged", "channel", label, detail=detail, ref=c["key"], digest=digest)
            )
            continue
        upd = {
            **ex,
            **{
                k: body[k]
                for k in (
                    "programs",
                    "fillerCollections",
                    "scheduleBackup",
                    "maktabaPlan",
                    "fillerRepeatCooldown",
                )
            },
        }
        actions.append(
            Action(
                "update",
                "channel",
                label,
                "PUT",
                "/api/channel",
                upd,
                [
                    *detail,
                    "keeps name, icon, watermark, transcoding; replaces programmes, "
                    "filler lists and schedule",
                ],
                c["key"],
                digest,
            )
        )
    return actions


def changes_from(actions: Sequence[Action]) -> ChangeSet:
    """The typed diff: created objects are ``added``, rewritten ones ``changed``."""
    added = tuple(
        {"kind": a.kind, "name": a.name, "ref": a.ref, "detail": a.detail}
        for a in actions
        if a.verb == "create"
    )
    changed = tuple(
        {"kind": a.kind, "name": a.name, "ref": a.ref, "detail": a.detail}
        for a in actions
        if a.verb == "update"
    )
    conflicts = [{"kind": a.kind, "name": a.name} for a in actions if a.verb == "conflict"]
    meta: dict[str, Any] = {}
    if conflicts:
        meta["conflicts"] = conflicts
    skipped = [a.name for a in actions if a.verb == "skip"]
    if skipped:
        meta["skipped_nothing_playable"] = skipped
    return ChangeSet("dizquetv_channels", added=added, changed=changed, meta=meta)


# ------------------------------------------------------------------ apply (blocking)


def _content(
    keys: Sequence[str], meta: Mapping[str, Mapping[str, Any]], server: str, plex: PlexClient
) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for k in keys:
        m = meta.get(k)
        prog = None if m is None else program_from_plex(m, server, plex.uri, plex.token)
        if prog is not None:
            out.append(prog)
    return out


def apply_actions(
    dz: DizqueTV,
    plex: PlexClient,
    actions: Sequence[Action],
    plan: Mapping[str, Any],
    *,
    tz_offset: int,
) -> list[dict[str, Any]]:
    """Write shows and filler lists, then the channels; read every object back.

    Returns one record per written channel (``key``, ``number``, ``name``, ``digest``).
    """
    server = dz.plex_server_name()
    if server is None:
        raise StageFailure(
            "dizquetv_no_plex_server", "dizqueTV has no Plex server configured", retryable=False
        )
    objs = [a for a in actions if a.kind in ("show", "filler") and a.verb in ("create", "update")]
    keys = sorted({str(i["ratingKey"]) for a in objs for i in (a.body or {}).get("content", [])})
    meta = plex.metadata(keys) if keys else {}
    for a in objs:
        assert a.body is not None and a.endpoint is not None
        content = _content([str(i["ratingKey"]) for i in a.body["content"]], meta, server, plex)
        body = {**a.body, "content": content}
        if a.verb == "create":
            oid = dz.call("PUT", a.endpoint, body)["id"]
        else:
            dz.call("POST", a.endpoint, body)
            oid = a.endpoint.rsplit("/", 1)[-1]
        back = dz.call("GET", f"/api/{a.kind}/{oid}")
        got = [str(x.get("ratingKey")) for x in back.get("content", [])]
        if got != [p["ratingKey"] for p in content]:
            raise StageFailure(
                "readback_mismatch",
                f"{a.name}: dizqueTV holds {len(got)} items, expected {len(content)}",
            )

    # second pass: every lane / pool exists now, so channel slots name real ids
    state = read_state(dz)
    written: list[dict[str, Any]] = []
    shows_by_id: dict[str, dict[str, Any]] = {}
    for act in plan_actions(plan, state, tz_offset=tz_offset):
        if act.kind != "channel" or act.verb not in ("create", "update"):
            continue
        assert act.body is not None
        body = dict(act.body)
        sched = body["scheduleBackup"]
        programs: list[dict[str, Any]] = []
        for sid in dict.fromkeys(s["showId"] for s in sched["slots"]):
            if not sid.startswith("custom."):
                continue
            show_id = sid[len("custom.") :]
            if show_id not in shows_by_id:
                shows_by_id[show_id] = dz.call("GET", f"/api/show/{show_id}")
            show = shows_by_id[show_id]
            for n, prog in enumerate(show.get("content", [])):
                programs.append(
                    {
                        **prog,
                        "customShowId": show_id,
                        "customShowName": show["name"],
                        "customOrder": n,
                    }
                )
        if not programs:
            continue  # nothing playable yet
        res = dz.call(
            "POST", "/api/channel-tools/time-slots", {"programs": programs, "schedule": sched}
        )
        body["programs"] = res["programs"]
        body["startTime"] = res["startTime"]
        dz.call(act.method or "POST", "/api/channel", body)
        back = dz.call("GET", f"/api/channel/programless/{body['number']}")
        if not isinstance(back.get("maktabaPlan"), dict) or back.get("name") != body["name"]:
            raise StageFailure(
                "readback_mismatch", f"channel {body['number']}: read-back does not match"
            )
        written.append(
            {"key": act.ref, "number": body["number"], "name": body["name"], "digest": act.digest}
        )
    return written


# ------------------------------------------------------------------ handler


class ChannelsService:
    def __init__(
        self,
        dz_factory: Callable[[Mapping[str, str]], DizqueTV],
        plex_factory: Callable[[Mapping[str, str]], PlexClient],
        tz_offset: Callable[[], int],
    ) -> None:
        self.dz = dz_factory
        self.plex = plex_factory
        self.tz_offset = tz_offset


async def latest_plan(db: Any) -> tuple[str, dict[str, Any]] | None:
    row = await db.fetchrow("SELECT input_hash, plan FROM channel_plans ORDER BY id DESC LIMIT 1")
    if row is None:
        return None
    return str(row["input_hash"]), dict(loads(row["plan"]) or {})


async def fingerprint(db: Any, subject: Subject, write_mode: str) -> str | None:
    got = await latest_plan(db)
    if got is None:
        return None
    return canonical_hash({"plan": got[0], "write_mode": write_mode})


def _wrap(exc: Exception) -> StageFailure:
    down = isinstance(exc, TransportError) or isinstance(exc.__cause__, TransportError)
    kind = "dizquetv_unreachable" if down else "dizquetv_error"
    return StageFailure(kind, str(exc), retryable=True)


async def resolve_plan_keys(db: Any, plan: dict[str, Any]) -> dict[str, Any]:
    """Fill ``plex_rating_key`` of every lane episode and filler item.

    The core plan names videos by Maktaba video id (``path``); the Plex rating key
    comes from the cached ``external_links(target='plex_item')`` rows. Items with
    no Plex item yet keep ``None`` and are left out of the dizqueTV objects.
    """
    items: list[dict[str, Any]] = []
    for ln in plan.get("lanes", {}).values():
        for u in ln.get("units", []):
            items.extend(u.get("episodes", []))
    for pool in plan.get("filler_pools", {}).values():
        items.extend(pool.get("items", []))
    wanted = [str(i["path"]) for i in items if not i.get("plex_rating_key") and i.get("path")]
    links = await get_links(db, "plex_item", "video", wanted)
    for i in items:
        link = links.get(str(i.get("path")))
        if link is not None and not i.get("plex_rating_key"):
            i["plex_rating_key"] = link.external_id
    return plan


async def run_channels(
    db: Any, job: Any, ctx: StageContext, service: ChannelsService
) -> StageResult:
    got = await latest_plan(db)
    if got is None:
        return StageResult(
            output_hash=canonical_hash("no-plan"),
            outcome="waiting_inputs",
            summary={"reason": "no_channel_plan"},
        )
    plan_hash, plan = got
    plan = await resolve_plan_keys(db, plan)
    settings = ctx.extension_settings("dizquetv")
    dz = service.dz(settings)
    tz = service.tz_offset()
    try:
        state = await asyncio.to_thread(read_state, dz)
    except (DizqueError, TransportError) as exc:
        raise _wrap(exc) from exc
    actions = plan_actions(plan, state, tz_offset=tz)
    changes = changes_from(actions)
    output_hash = canonical_hash({"plan": plan_hash, "tz": tz})
    written: list[dict[str, Any]] = []

    async def apply(cs: ChangeSet) -> None:
        plex_settings = ctx.extension_settings("plex")
        if not plex_settings.get("url") or not plex_settings.get("token"):
            raise StageFailure(
                "plex_not_configured",
                "writing dizqueTV programmes needs the Plex settings (url and token)",
                retryable=False,
            )
        plex = service.plex(plex_settings)
        try:
            written.extend(
                await asyncio.to_thread(apply_actions, dz, plex, actions, plan, tz_offset=tz)
            )
        except (DizqueError, PlexError, TransportError) as exc:
            raise _wrap(exc) from exc

    status = await apply_or_review(ctx, changes, apply)
    for w in written:
        await put_link(
            db,
            Link(
                CHANNEL_TARGET,
                "catalog",
                str(w["key"]),
                str(w["number"]),
                str(w["name"]),
                str(w["digest"]),
                True,
            ),
        )
    return StageResult(
        output_hash=output_hash,
        outcome="awaiting_review" if status == "review" else None,
        summary={"status": status, "channels": len(written), "changes": changes.to_json()},
    )
