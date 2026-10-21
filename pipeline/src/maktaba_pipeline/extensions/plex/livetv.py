"""The ``plex_livetv`` stage: Maktaba's channels in Plex Live TV (audit P0-1).

What dizqueTV's "auto refresh guide" and "auto refresh channels" do, aimed at Maktaba's own
HDHomeRun tuner only, plus adding that tuner to Plex once:

* **find** Plex's DVRs (``GET /livetv/dvrs``) and tuners (``GET /media/grabbers/devices``)
  and Maktaba's tuner among them, by its HDHomeRun DeviceID (``hdhr_device``) or its address;
* **register** (``POST /media/grabbers/devices?uri=..``), only when an admin asked for it
  (``POST /api/tv/plex/register``, or a switch of the home: Epic 37) or
  ``rules.plexlive.auto_register`` is on: Plex's settings
  page shows one DVR (the first; since Plex's multi-lineup DVRs further tuners are its
  "channel sources", each with its own guide), so the tuner joins that DVR as Plex web's "Add
  Another Device" does (channel map, ``PUT /livetv/dvrs/{key}/lineups`` with Maktaba's XMLTV
  guide, ``PUT /livetv/dvrs/{key}/devices/{device}``) and is named. A separate DVR holding only
  Maktaba's tuner (what earlier versions made; Plex hides it) is deleted first. With no DVR
  at all, ``POST /livetv/dvrs`` makes one. Refused when the shown DVR has the tuner already,
  unless at another address;
* **move** the tuner Plex has at another machine's address (the home switched, or its address
  changed) to this one's, when registering: added again at the new address, Plex keeps the
  device (the same DeviceID) and takes the address in place, or else the old one leaves its
  DVR and Plex and the new one joins as a registration does; the guide that lived at the old
  address gives way to this one's;
* **channel map** (``PUT /media/grabbers/devices/{key}/channelmap``, dizqueTV's exact query)
  when the lineup differs from the one last pushed, or Plex's actual map on Maktaba's tuner
  (the DVR listing's enabled ``ChannelMapping``) differs from the lineup. The second is
  *drift*: another program replaced the map (dizqueTV's Plex refresh writes its own channel
  list onto every tuner in the DVR). It is restored on any run while
  ``auto_refresh_channels`` is on, reported, and counted: after ``drift_warn_repeats``
  restores in a row (each within ``drift_forget_hours`` of the previous) the status advises
  turning off channel mapping in dizqueTV. After a push the listing is read again, so
  ``channels.in_sync`` is what Plex holds, never what was sent;
* **reload the guide** (``POST /livetv/dvrs/{key}/reloadGuide``) after a channel-map push, an
  explicit refresh, or when the schedule grew (then at most every
  ``guide_refresh_min_minutes``: an early run is given back until then). A refresh that
  changes nothing within ``GUIDE_LOAD_SECONDS`` of the last reload sends nothing: Plex is
  still loading that guide, and a reload or channel-map push meanwhile aborts the load.

Other tuners (dizqueTV's, an antenna's) are listed in the status and never changed: their
channel maps, names and guides stay as they are. The DVR Maktaba joins is shared, so its
guide reload reloads every guide that DVR carries.

Triggers: the edge ``write_channels -> plex_livetv`` (the lineup changed), the scheduler's
top-up (``StageDef.after_topup``: the schedule grew), the reconciler every
``drift_check_minutes`` (``StageDef.every_minutes_rule``: the drift check) and the API
(``force``: refresh now, register). Everything the stage saw and did is kept in
``plex_livetv_state`` (slot 0116), which ``GET /api/tv/plex`` reports.
"""

from __future__ import annotations

import asyncio
import os
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass, field, replace
from datetime import datetime, timedelta
from typing import Any

from ...engine.context import JobDeferred, StageContext, StageFailure
from ...engine.graph import StageResult
from ...engine.hashing import canonical_hash
from ...engine.sql import dumps, loads, now, parse_ts, to_ts
from ...integrations.plex import (
    PlexClient,
    PlexDvr,
    PlexError,
    PlexTuner,
    TransportError,
    find_tuner,
    same_host,
    xmltv_lineup,
    xmltv_url_of,
)
from .collection import PlexService

__all__ = [
    "LiveState",
    "Target",
    "classify",
    "load_state",
    "resolve_target",
    "run_livetv",
]

XMLTV_PATH = "/xmltv.xml"

# Plex takes a minute or two to load a full guide (~90 s for 6,000 programmes) and a
# reloadGuide sent meanwhile aborts the load ("EPG[xmltv]: Failed to load EPG"), so a
# "Refresh Plex now" that changes nothing waits this long after the last reload.
GUIDE_LOAD_SECONDS = 300.0


@dataclass(frozen=True, slots=True)
class Target:
    """The addresses Plex is given: the tuner's base URL and its XMLTV guide."""

    tuner_url: str
    xmltv_url: str
    #: where the tuner address came from: ``rules``, ``env``, ``api`` or "" (none)
    source: str


@dataclass(slots=True)
class LiveState:
    register_requested_at: datetime | None = None
    requested_base_url: str = ""
    status: dict[str, Any] = field(default_factory=dict)
    guide_reloaded_at: datetime | None = None
    guide_version: str = ""
    channelmap_at: datetime | None = None
    channelmap_hash: str = ""


async def load_state(db: Any) -> LiveState:
    row = await db.fetchrow("SELECT * FROM plex_livetv_state WHERE id = 1")
    if row is None:
        return LiveState()
    return LiveState(
        register_requested_at=parse_ts(row["register_requested_at"]),
        requested_base_url=str(row["requested_base_url"] or ""),
        status=dict(loads(row["status"]) or {}),
        guide_reloaded_at=parse_ts(row["guide_reloaded_at"]),
        guide_version=str(row["guide_version"] or ""),
        channelmap_at=parse_ts(row["channelmap_at"]),
        channelmap_hash=str(row["channelmap_hash"] or ""),
    )


async def _save(
    db: Any,
    st: LiveState,
    *,
    at: datetime,
    error: str | None,
    clear_register: bool,
) -> None:
    await db.execute(
        "INSERT INTO plex_livetv_state (id, status, checked_at, guide_reloaded_at, guide_version,"
        " channelmap_at, channelmap_hash, last_error, last_error_at, updated_at)"
        " VALUES (1, $1, $2, $3, $4, $5, $6, $7, $8, $2)"
        " ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status,"
        " checked_at = EXCLUDED.checked_at, guide_reloaded_at = EXCLUDED.guide_reloaded_at,"
        " guide_version = EXCLUDED.guide_version, channelmap_at = EXCLUDED.channelmap_at,"
        " channelmap_hash = EXCLUDED.channelmap_hash, last_error = EXCLUDED.last_error,"
        " last_error_at = COALESCE(EXCLUDED.last_error_at, plex_livetv_state.last_error_at),"
        " updated_at = EXCLUDED.updated_at",
        dumps(st.status),
        to_ts(db, at),
        to_ts(db, st.guide_reloaded_at),
        st.guide_version or None,
        to_ts(db, st.channelmap_at),
        st.channelmap_hash or None,
        error,
        to_ts(db, at) if error else None,
    )
    if clear_register:
        # only the request this run answered: one made while it ran stays for the next run
        await db.execute(
            "UPDATE plex_livetv_state SET register_requested_at = NULL"
            " WHERE id = 1 AND register_requested_at <= $1",
            to_ts(db, at),
        )


def resolve_target(cfg: Any, st: LiveState, env: Mapping[str, str]) -> Target:
    """The tuner address: ``rules.plexlive.tuner_url``, else ``MAKTABA_PUBLIC_BASE_URL`` (what
    the streaming service advertises), else what ``/api/tv/info`` reported when an admin
    asked to register. The guide: ``rules.plexlive.xmltv_url``, else ``<tuner>/xmltv.xml``."""
    base, source = str(cfg.tuner_url or "").strip(), "rules"
    if not base:
        base, source = str(env.get("MAKTABA_PUBLIC_BASE_URL") or "").strip(), "env"
    if not base:
        base, source = st.requested_base_url.strip(), "api"
    if not base:
        source = ""
    elif "://" not in base:
        base = f"http://{base}"
    base = base.rstrip("/")
    xmltv = str(cfg.xmltv_url or "").strip() or (f"{base}{XMLTV_PATH}" if base else "")
    return Target(base, xmltv, source)


def classify(t: PlexTuner, ours: PlexTuner | None, dizquetv_url: str) -> str:
    """``maktaba``, ``dizquetv`` (its address, or its name says so) or ``other``."""
    if ours is not None and t.key == ours.key:
        return "maktaba"
    text = f"{t.title} {t.make} {t.model}".casefold()
    if same_host(t.uri, dizquetv_url) or "dizque" in text:
        return "dizquetv"
    return "other"


async def _device(db: Any) -> tuple[str, str, bool] | None:
    """Maktaba's HDHomeRun device: (DeviceID, name, enabled), None before it exists."""
    try:
        row = await db.fetchrow(
            "SELECT device_id, friendly_name, enabled FROM hdhr_device WHERE id = 1"
        )
    except Exception as exc:  # noqa: BLE001 - a bare database without the table
        if "hdhr_device" not in str(exc):
            raise
        return None
    if row is None:
        return None
    return str(row["device_id"]), str(row["friendly_name"]), bool(row["enabled"])


async def _lineup(db: Any) -> list[str]:
    """The HDHomeRun lineup's channel numbers, in its order (streaming ``hdhr`` Lineup)."""
    rows = await db.fetch(
        "SELECT number FROM channels WHERE enabled = true ORDER BY sort_order ASC, number ASC"
    )
    return [str(int(r["number"])) for r in rows]


async def _guide_version(db: Any) -> str:
    """When any channel's schedule was last (re)generated: the guide changed since then."""
    v = await db.fetchval("SELECT max(last_generated_at) FROM channel_schedule_state")
    if v is None:
        return ""
    ts = parse_ts(v)
    return ts.isoformat() if ts is not None else str(v)


def _tuners(dvrs: Sequence[PlexDvr], devices: Sequence[PlexTuner]) -> list[PlexTuner]:
    """Every tuner once: a DVR's listing (it carries the channel mapping) before the
    device list's."""
    out: dict[str, PlexTuner] = {}
    for d in dvrs:
        for t in d.devices:
            out.setdefault(t.key or t.uuid, t)
    for t in devices:
        out.setdefault(t.key or t.uuid, t)
    return list(out.values())


def _dvr_of(dvrs: Sequence[PlexDvr], tuner: PlexTuner | None) -> PlexDvr | None:
    if tuner is None:
        return None
    for d in dvrs:
        if any(t.key == tuner.key for t in d.devices):
            return d
    return next((d for d in dvrs if tuner.dvr_key and d.key == tuner.dvr_key), None)


def _shown(dvrs: Sequence[PlexDvr], dvr: PlexDvr | None) -> bool:
    """Plex's Live TV & DVR settings show the first DVR only."""
    return dvr is not None and bool(dvrs) and dvrs[0].key == dvr.key


DRIFT_RESTORED = "Plex's channel list was changed by another program (dizqueTV?): restored"
DRIFT_NOT_RESTORED = (
    "Plex's channel list was changed by another program (dizqueTV?): not restored, "
    '"Refresh the channels in Plex" is off'
)


def drift_repeated(times: int) -> str:
    """The warning once restores keep happening (``drift_warn_repeats``)."""
    return (
        f"Another program replaced Maktaba's channels in Plex {times} times in a row. "
        "dizqueTV's Plex refresh writes its channel list onto every tuner in the DVR: "
        'turn off its channel mapping ("auto refresh channels") in dizqueTV\'s Plex settings'
    )


def _differs(mapped: Sequence[str] | None, lineup: Sequence[str]) -> bool:
    """Plex holds another channel list than the lineup (None: Plex listed no mapping)."""
    return mapped is not None and sorted(mapped) != sorted(lineup)


def _wrap(exc: Exception) -> StageFailure:
    kind = "plex_unreachable" if isinstance(exc, TransportError) else "plex_error"
    return StageFailure(kind, f"plex: {exc}", retryable=True)


@dataclass(slots=True)
class _Run:
    """What one run saw and did (becomes ``status``)."""

    dvrs: list[PlexDvr]
    tuners: list[PlexTuner]
    ours: PlexTuner | None = None
    ours_dvr: PlexDvr | None = None
    register: str = ""
    #: ``in_place`` (Plex took the new address) or ``re_added``; "" when not moved
    move: str = ""
    moved_from: str = ""
    channel_map: str = ""
    guide: str = ""
    warnings: list[str] = field(default_factory=list)


def _list(client: PlexClient) -> tuple[list[PlexDvr], list[PlexTuner]]:
    dvrs = client.dvrs()
    try:
        devices = client.grabber_devices()
    except PlexError:  # the DVRs list their tuners; the device list only adds idle ones
        devices = []
    return dvrs, devices


async def _reread(client: PlexClient, run: _Run, device_id: str, target: Target) -> None:
    """After a channel-map push: what Plex holds now (the status reports that, not what was
    sent). A listing that fails leaves the map unknown; the push itself went through."""
    assert run.ours is not None
    try:
        dvrs, devices = await asyncio.to_thread(_list, client)
    except (PlexError, TransportError) as exc:
        run.ours = replace(run.ours, mapped=None)
        run.warnings.append(f"could not read Plex's channel list back: {exc}")
        return
    run.dvrs, run.tuners = dvrs, _tuners(dvrs, devices)
    fresh = find_tuner(run.tuners, device_id, target.tuner_url)
    if fresh is None:
        run.ours = replace(run.ours, mapped=None)
        return
    run.ours = fresh
    run.ours_dvr = _dvr_of(run.dvrs, fresh) or run.ours_dvr


async def _join(
    client: PlexClient,
    run: _Run,
    tuner: PlexTuner,
    lineup: Sequence[str],
    target: Target,
    cfg: Any,
    name: str,
) -> tuple[str, bool]:
    """Put Maktaba's tuner where Plex's settings page shows it. Returns why not ("" when
    done; the caller checks) and whether the channel map was pushed on the way."""
    title = str(cfg.guide_title or "Maktaba")
    host = next((d for d in run.dvrs if any(t.key != tuner.key for t in d.devices)), None)
    own = _dvr_of(run.dvrs, tuner)
    if host is None:  # no other DVR: one of Maktaba's own is the one Plex shows
        if own is None:
            await asyncio.to_thread(
                client.create_dvr,
                tuner.uuid,
                target.xmltv_url,
                title,
                str(cfg.language or "ara"),
            )
    else:
        if own is not None and own.key != host.key:
            if any(t.key != tuner.key for t in own.devices):
                return "in_another_dvr", False  # shared: not Maktaba's to delete
            await asyncio.to_thread(client.delete_dvr, own.key)
        # Plex web's "Add Another Device": the channels, the guide, then the tuner
        await asyncio.to_thread(client.put_channel_map, tuner.key, lineup)
        guide = xmltv_lineup(target.xmltv_url, title)
        if guide not in host.lineups:
            await asyncio.to_thread(client.add_dvr_lineup, host.key, guide)
        await asyncio.to_thread(client.add_dvr_device, host.key, tuner.key)
    await asyncio.to_thread(client.set_device_title, tuner.key, name)
    return "", host is not None


def _mine(tuners: Sequence[PlexTuner], device_id: str) -> list[PlexTuner]:
    """Plex's tuners with Maktaba's DeviceID, each once."""
    out: dict[str, PlexTuner] = {}
    for t in tuners:
        if device_id and t.device_id.casefold() == device_id.casefold():
            out.setdefault(t.key, t)
    return list(out.values())


async def _relist(client: PlexClient, run: _Run, device_id: str, target: Target) -> None:
    dvrs, devices = await asyncio.to_thread(_list, client)
    run.dvrs, run.tuners = dvrs, _tuners(dvrs, devices)
    run.ours = find_tuner(run.tuners, device_id, target.tuner_url)
    run.ours_dvr = _dvr_of(run.dvrs, run.ours)


async def _move(client: PlexClient, run: _Run, target: Target, device_id: str) -> PlexTuner | None:
    """Maktaba's tuner, which Plex has at another machine's address, pointed at this one.

    Plex reads ``discover.json`` at the new address; a tuner with the same DeviceID is the
    same device to Plex, which takes the address in place (its DVR, channels and name kept:
    ``in_place``). When Plex keeps the old address, the old tuner leaves its DVR (the DVR
    goes when it carried that tuner only) and Plex, and is added again here (``re_added``:
    the caller joins it to the DVR Plex shows, as a registration does). Returns the tuner
    at the new address."""
    old = run.ours
    added = await asyncio.to_thread(client.add_grabber_device, target.tuner_url)
    await _relist(client, run, device_id, target)
    mine = _mine([*run.tuners, *([added] if added else [])], device_id)
    here = next((t for t in mine if same_host(t.uri, target.tuner_url)), None)
    if here is not None and old is not None and here.key == old.key:
        run.move = "in_place"
        run.ours, run.ours_dvr = here, _dvr_of(run.dvrs, here)
        return here
    for t in mine:
        if same_host(t.uri, target.tuner_url):
            continue
        dvr = _dvr_of(run.dvrs, t)
        if dvr is not None and all(d.key == t.key for d in dvr.devices):
            await asyncio.to_thread(client.delete_dvr, dvr.key)
        elif dvr is not None:
            await asyncio.to_thread(client.remove_dvr_device, dvr.key, t.key)
        await asyncio.to_thread(client.delete_grabber_device, t.key)
    if here is None:
        added = await asyncio.to_thread(client.add_grabber_device, target.tuner_url)
    await _relist(client, run, device_id, target)
    run.move = "re_added"
    return find_tuner([*run.tuners, *([added] if added else [])], device_id, target.tuner_url)


async def _move_guide(client: PlexClient, run: _Run, target: Target, cfg: Any) -> None:
    """The guide Maktaba's DVR carried from the old address gives way to this machine's."""
    dvr = run.ours_dvr
    if dvr is None:
        return
    guide = xmltv_lineup(target.xmltv_url, str(cfg.guide_title or "Maktaba"))
    if guide not in dvr.lineups:
        await asyncio.to_thread(client.add_dvr_lineup, dvr.key, guide)
    for lineup in dvr.lineups:
        url = xmltv_url_of(lineup)
        if lineup == guide or not url or not same_host(url, run.moved_from):
            continue
        try:
            await asyncio.to_thread(client.remove_dvr_lineup, dvr.key, lineup)
        except PlexError as exc:  # the old guide only lingers: the new one is there
            run.warnings.append(f"the guide at the old address stays in Plex: {exc}")


async def run_livetv(  # noqa: C901, PLR0912, PLR0915 - one decision list, kept in order
    db: Any,
    job: Any,
    ctx: StageContext,
    service: PlexService,
    *,
    env: Mapping[str, str] | None = None,
    wall: Callable[[], datetime] = now,
) -> StageResult:
    cfg = await ctx.rules("plexlive")
    settings = ctx.extension_settings("plex")
    dz_url = str(ctx.extension_settings("dizquetv").get("url", ""))
    st = await load_state(db)
    manual = bool(getattr(job, "force", False) or getattr(job, "force_cascade", False))
    asked = st.register_requested_at is not None
    target = resolve_target(cfg, st, os.environ if env is None else env)
    device = await _device(db)
    device_id = device[0] if device else ""
    lineup = await _lineup(db)
    guide_version = await _guide_version(db)
    client = service.client(settings)
    started = wall()
    # restores of a map another program replaced, kept across runs in the status
    prev = st.status.get("drift")
    prev = prev if isinstance(prev, dict) else {}
    drift_repairs = int(prev.get("repairs") or 0)
    drift_last = parse_ts(prev.get("last_at"))
    drift_seen = False
    if drift_last is not None and started - drift_last > timedelta(
        hours=float(cfg.drift_forget_hours)
    ):
        drift_repairs = 0  # quiet long enough: the next restore counts from one

    def status(run: _Run | None, error: str | None) -> dict[str, Any]:
        ours = run.ours if run else None
        ours_dvr = run.ours_dvr if run else None
        seen_dvrs = run.dvrs if run else []
        mapped = None if ours is None or ours.mapped is None else list(ours.mapped)
        return {
            "reachable": run is not None,
            "server": str(settings.get("url", "")),
            "error": error,
            "target": {
                "tuner_url": target.tuner_url,
                "xmltv_url": target.xmltv_url,
                "source": target.source,
            },
            "device": {
                "device_id": device_id,
                "name": device[1] if device else "",
                "enabled": bool(device and device[2]),
            },
            "dvrs": [
                {
                    "key": d.key,
                    "title": d.title,
                    "lineup": d.lineup,
                    "maktaba": ours_dvr is not None and d.key == ours_dvr.key,
                    "devices": [
                        {
                            "key": t.key,
                            "uri": t.uri,
                            "device_id": t.device_id,
                            "title": t.title,
                            "kind": classify(t, ours, dz_url),
                        }
                        for t in d.devices
                    ],
                }
                for d in seen_dvrs
            ],
            "tuners": [
                {
                    "key": t.key,
                    "uri": t.uri,
                    "device_id": t.device_id,
                    "title": t.title,
                    "kind": classify(t, ours, dz_url),
                    "in_dvr": _dvr_of(seen_dvrs, t) is not None,
                }
                for t in (run.tuners if run else [])
            ],
            "maktaba": {
                "tuner_key": ours.key if ours else None,
                "dvr_key": run.ours_dvr.key if run and run.ours_dvr else None,
                "registered": bool(run and _shown(run.dvrs, run.ours_dvr)),
                # in a DVR of its own that Plex's settings page does not show
                "separate_dvr": bool(
                    run and run.ours_dvr is not None and not _shown(run.dvrs, run.ours_dvr)
                ),
            },
            "dizquetv_present": bool(
                run and any(classify(t, ours, dz_url) == "dizquetv" for t in run.tuners)
            ),
            "channels": {
                "lineup": len(lineup),
                "mapped": len(mapped) if mapped is not None else None,
                "in_sync": None if mapped is None else sorted(mapped) == sorted(lineup),
                # this run found Plex holding another map than the one Maktaba pushed
                "drift": drift_seen,
            },
            "drift": {
                "repairs": drift_repairs,
                "last_at": drift_last.isoformat() if drift_last else None,
            },
            "actions": {
                "register": run.register if run else "",
                "move": run.move if run else "",
                "moved_from": run.moved_from if run else "",
                "channel_map": run.channel_map if run else "",
                "guide": run.guide if run else "",
            },
            "warnings": run.warnings if run else [],
            "auto": {
                "register": bool(cfg.auto_register),
                "refresh_guide": bool(cfg.auto_refresh_guide),
                "refresh_channels": bool(cfg.auto_refresh_channels),
                "guide_refresh_min_minutes": int(cfg.guide_refresh_min_minutes),
                "drift_check_minutes": int(cfg.drift_check_minutes),
            },
        }

    try:
        dvrs, devices = await asyncio.to_thread(_list, client)
    except (PlexError, TransportError) as exc:
        st.status = status(None, str(exc))
        await _save(db, st, at=started, error=f"plex: {exc}", clear_register=False)
        raise _wrap(exc) from exc

    run = _Run(dvrs, _tuners(dvrs, devices))
    run.ours = find_tuner(run.tuners, device_id, target.tuner_url)
    run.ours_dvr = _dvr_of(run.dvrs, run.ours)
    others = [t for t in run.tuners if run.ours is None or t.key != run.ours.key]
    if others:
        run.warnings.append(f"other tuners left as they are: {len(others)}")

    registered_now = joined_map = False
    error: str | None = None
    try:
        # ---- register (once; refused when a DVR already has Maktaba's tuner at this address)
        elsewhere = bool(
            run.ours is not None
            and run.ours.uri
            and target.tuner_url
            and not same_host(run.ours.uri, target.tuner_url)
        )
        if asked or (cfg.auto_register and not _shown(run.dvrs, run.ours_dvr)):
            if _shown(run.dvrs, run.ours_dvr) and not elsewhere:
                run.register = "already_registered"
            elif device is None or not device[2]:
                run.register = "tuner_off"
            elif not target.tuner_url:
                run.register = "no_address"
            elif not lineup:
                run.register = "no_channels"
            else:
                tuner = run.ours
                if elsewhere and tuner is not None:
                    run.moved_from = tuner.uri
                    tuner = await _move(client, run, target, device_id)
                elif tuner is None:
                    added = await asyncio.to_thread(client.add_grabber_device, target.tuner_url)
                    _, devices = await asyncio.to_thread(_list, client)
                    tuner = find_tuner(
                        [*([added] if added else []), *devices], device_id, target.tuner_url
                    )
                if tuner is None or not tuner.uuid:
                    run.register = "tuner_not_added"
                else:
                    if not (run.move == "in_place" and _shown(run.dvrs, run.ours_dvr)):
                        run.register, joined_map = await _join(
                            client, run, tuner, lineup, target, cfg, device[1] or "Maktaba"
                        )
                    await _relist(client, run, device_id, target)
                    if run.moved_from:
                        run.ours = run.ours or tuner
                        run.ours_dvr = _dvr_of(run.dvrs, run.ours)
                        await _move_guide(client, run, target, cfg)
                        await _relist(client, run, device_id, target)
                    run.ours = run.ours or tuner
                    run.ours_dvr = _dvr_of(run.dvrs, run.ours)
                    registered_now = _shown(run.dvrs, run.ours_dvr)
                    if not run.register:
                        run.register = (
                            ("moved" if run.moved_from else "registered")
                            if registered_now
                            else "dvr_not_created"
                        )
            await ctx.log("info", f"plex live tv: register {run.register}")

        # ---- channel map, then the guide (Maktaba's tuner and DVR only)
        if run.ours is not None and run.ours_dvr is not None:
            want = canonical_hash(lineup)
            # what Plex holds on Maktaba's tuner, not only what Maktaba sent last
            differs = _differs(run.ours.mapped, lineup)
            drift_seen = bool(lineup) and differs and want == st.channelmap_hash
            changed = want != st.channelmap_hash or differs
            last = st.guide_reloaded_at
            since = None if last is None else (wall() - last).total_seconds()
            # a refresh that changes nothing while Plex still loads the last reload: leave it be
            settling = (
                manual
                and not registered_now
                and not changed
                and since is not None
                and since < GUIDE_LOAD_SECONDS
            )
            pushed = False
            if not lineup:
                run.channel_map = "no_channels"
            elif settling:
                run.channel_map = "unchanged"
            elif registered_now or ((manual or cfg.auto_refresh_channels) and (manual or changed)):
                if not joined_map:  # joining pushed it already (and listed Plex after)
                    await asyncio.to_thread(client.put_channel_map, run.ours.key, lineup)
                    await _reread(client, run, device_id, target)
                st.channelmap_hash, st.channelmap_at, pushed = want, wall(), True
                run.channel_map = "pushed"
                await ctx.log("info", f"plex live tv: channel map pushed ({len(lineup)} channels)")
                if drift_seen:
                    drift_repairs, drift_last = drift_repairs + 1, wall()
                    run.warnings.append(DRIFT_RESTORED)
                    await ctx.log("warn", f"plex live tv: {DRIFT_RESTORED} ({drift_repairs})")
            elif drift_seen:
                run.channel_map = "off"
                run.warnings.append(DRIFT_NOT_RESTORED)
            else:
                run.channel_map = "unchanged" if want == st.channelmap_hash else "off"
            assert run.ours is not None and run.ours_dvr is not None
            if _differs(run.ours.mapped, lineup) and not (drift_seen and not pushed):
                run.warnings.append("Plex's channel list differs from Maktaba's lineup")
            if drift_repairs >= int(cfg.drift_warn_repeats):
                run.warnings.append(drift_repeated(drift_repairs))

            due = guide_version != st.guide_version
            if settling:
                if due and cfg.auto_refresh_guide:  # the guide grew too: reload once loaded
                    run.guide = "loading"
                    st.status = status(run, None)
                    await _save(db, st, at=started, error=None, clear_register=asked)
                    raise JobDeferred(
                        "Plex is still loading the guide",
                        GUIDE_LOAD_SECONDS - (since or 0.0),
                    )
                reload = False
                run.guide = "loading"
            elif registered_now or manual or (pushed and cfg.auto_refresh_guide):
                reload = True
            elif cfg.auto_refresh_guide and due:
                wait = (
                    0.0
                    if last is None
                    else float(cfg.guide_refresh_min_minutes) * 60.0
                    - (wall() - last).total_seconds()
                )
                if wait > 0:
                    run.guide = "waiting"
                    st.status = status(run, None)
                    await _save(db, st, at=started, error=None, clear_register=asked)
                    raise JobDeferred(
                        f"guide reloaded less than {cfg.guide_refresh_min_minutes} min ago",
                        wait,
                    )
                reload = True
            else:
                reload = False
                run.guide = "unchanged" if not due else "off"
            if reload:
                await asyncio.to_thread(client.reload_guide, run.ours_dvr.key)
                st.guide_reloaded_at, st.guide_version = wall(), guide_version
                run.guide = "reloaded"
                await ctx.log("info", f"plex live tv: guide reloaded (DVR {run.ours_dvr.key})")
        else:
            run.channel_map = run.guide = "not_registered"
    except (PlexError, TransportError) as exc:
        error = f"plex: {exc}"
        st.status = status(run, str(exc))
        # a register that failed half-way (the tuner added, the DVR not created) keeps the
        # admin's request for the retry: cleared, the retry would only report
        # "not_registered" and never create the DVR
        await _save(db, st, at=started, error=error, clear_register=asked and bool(run.register))
        raise _wrap(exc) from exc

    st.status = status(run, None)
    await _save(db, st, at=started, error=None, clear_register=asked)
    wrote = run.register == "registered" or run.channel_map == "pushed" or run.guide == "reloaded"
    return StageResult(
        output_hash=canonical_hash(
            {
                "dvr": run.ours_dvr.key if run.ours_dvr else None,
                "map": st.channelmap_hash,
                "guide": st.guide_version,
            }
        ),
        outcome="changed" if wrote else "unchanged",
        summary={
            "registered": _shown(run.dvrs, run.ours_dvr),
            "register": run.register or None,
            "channel_map": run.channel_map,
            "guide": run.guide,
            "dvrs": len(run.dvrs),
            "channels": len(lineup),
        },
    )
