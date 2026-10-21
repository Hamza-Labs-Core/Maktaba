"""Plex Live TV (the Plex extension's ``plex_livetv``): the client's Live TV calls, the
channel map exactly as dizqueTV builds it, registering once, refreshing, the throttle."""

from __future__ import annotations

import http.server
import json
import threading
import urllib.parse
import uuid
from collections.abc import Iterator, Mapping, Sequence
from dataclasses import dataclass, field
from datetime import timedelta
from typing import Any

import pytest

from maktaba_pipeline.channels.pass_ import notify_listeners
from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.dispatch import EngineRuntime, dispatch_job
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import CATALOG_ID
from maktaba_pipeline.engine.loader import build_full_graph
from maktaba_pipeline.engine.reconcile import reconcile_once
from maktaba_pipeline.engine.sql import dumps, loads, now, to_ts
from maktaba_pipeline.extensions.plex import PlexExtension
from maktaba_pipeline.extensions.plex.livetv import (
    DRIFT_NOT_RESTORED,
    DRIFT_RESTORED,
    Target,
    drift_repeated,
    load_state,
    resolve_target,
)
from maktaba_pipeline.integrations.plex import (
    PlexClient,
    PlexTuner,
    TransportError,
    channel_map_query,
    find_tuner,
    xmltv_lineup,
)
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.schedule.tv_settings import PlexLiveConfig
from tests.extensions.harness import error_of, run_stage, setup_runtime, summary_of

BASE = "http://plex.test"
TUNER = "http://192.168.1.20:8782"
DIZQUE = "http://192.168.1.20:8000"
DEVICE_ID = "A1B2C3D4"
SETTINGS = {
    "plex": {"url": BASE, "token": "T"},
    "dizquetv": {"url": DIZQUE},
}


# ------------------------------------------------------------------ fake Plex (Live TV)


@dataclass
class FakeLiveTV:
    """The Live TV half of a Plex Media Server; implements the client's ``Transport``."""

    #: tuner address -> the DeviceID its discover.json gives (what Plex can add)
    reachable: dict[str, str] = field(default_factory=dict)
    #: device key -> record
    devices: dict[str, dict[str, Any]] = field(default_factory=dict)
    #: dvr key -> record (``devices``: device keys)
    dvrs: dict[str, dict[str, Any]] = field(default_factory=dict)
    #: (method, path, raw query)
    calls: list[tuple[str, str, str]] = field(default_factory=list)
    down: bool = False
    answer_with_objects: bool = True
    #: ``POST /livetv/dvrs`` fails this many times first (Plex timing out on the guide)
    dvr_failures: int = 0
    #: Plex answers a channel-map push but keeps the map it had
    ignore_channelmap: bool = False
    _next: int = 10

    def add_device(self, uri: str, device_id: str, title: str = "HDHomeRun") -> str:
        self._next += 1
        key = str(self._next)
        self.devices[key] = {
            "key": key,
            "uuid": f"device://tv.plex.grabbers.hdhomerun/{device_id}",
            "uri": uri,
            "deviceId": device_id,
            "make": "Silicondust",
            "model": "HDTC-2US",
            "title": title,
        }
        return key

    def add_dvr(self, device_key: str, title: str, lineup: str = "lineup://x") -> str:
        self._next += 1
        key = str(self._next)
        self.dvrs[key] = {
            "title": title,
            "lineup": lineup,
            "lineups": [lineup],
            "devices": [device_key],
        }
        self.devices[device_key]["parentID"] = key
        return key

    def writes(self) -> list[tuple[str, str]]:
        return [(m, p) for m, p, _ in self.calls if m != "GET"]

    def _device_json(self, key: str) -> dict[str, Any]:
        return dict(self.devices[key])

    def _dvr_json(self, key: str) -> dict[str, Any]:
        d = self.dvrs[key]
        return {
            "key": key,
            "uuid": f"dvr-{key}",
            "title": d["title"],
            "lineup": d["lineup"],
            "language": d.get("language", ""),
            "Device": [self._device_json(k) for k in d["devices"]],
            "Lineup": [{"id": x} for x in d["lineups"]],
        }

    def request(
        self,
        method: str,
        url: str,
        headers: Mapping[str, str],
        body: bytes | None,
        timeout: float,
    ) -> tuple[int, bytes]:
        if self.down:
            raise TransportError("connection refused")
        assert headers.get("X-Plex-Token") == "T"
        assert url.startswith(BASE), url
        path, _, raw = url[len(BASE) :].partition("?")
        self.calls.append((method, path, raw))
        q = {k: v[0] for k, v in urllib.parse.parse_qs(raw).items()}
        out = self._route(method, path, q)
        if out is None:
            return 404, b""
        if isinstance(out, int):
            return out, b""
        return 200, json.dumps(out).encode()

    def _route(self, method: str, path: str, q: dict[str, str]) -> Any:  # noqa: C901
        parts = path.strip("/").split("/")
        if method == "GET" and path == "/livetv/dvrs":
            return {"MediaContainer": {"Dvr": [self._dvr_json(k) for k in self.dvrs]}}
        if method == "GET" and path == "/media/grabbers/devices":
            return {"MediaContainer": {"Device": [self._device_json(k) for k in self.devices]}}
        if method == "POST" and path == "/media/grabbers/devices":
            uri = q.get("uri", "")
            if uri not in self.reachable:
                return 400
            key = self.add_device(uri, self.reachable[uri], "Maktaba")
            return (
                {"MediaContainer": {"Device": [self._device_json(key)]}}
                if self.answer_with_objects
                else {}
            )
        if method == "POST" and path == "/livetv/dvrs":
            if self.dvr_failures > 0:
                self.dvr_failures -= 1
                raise TransportError("timed out")
            dev = next((k for k, d in self.devices.items() if d["uuid"] == q.get("device")), None)
            if dev is None:
                return 400
            key = self.add_dvr(dev, q.get("lineupTitle", ""), q["lineup"])
            self.dvrs[key]["language"] = q.get("language", "")
            return (
                {"MediaContainer": {"Dvr": [self._dvr_json(key)]}}
                if self.answer_with_objects
                else {}
            )
        if parts[:2] == ["livetv", "dvrs"] and len(parts) >= 3 and parts[2] not in self.dvrs:
            return None
        if method == "DELETE" and parts[:2] == ["livetv", "dvrs"] and len(parts) == 3:
            for k in self.dvrs.pop(parts[2])["devices"]:
                self.devices[k].pop("parentID", None)
            return {}
        if method == "PUT" and parts[:2] == ["livetv", "dvrs"] and parts[3:] == ["lineups"]:
            self.dvrs[parts[2]]["lineups"].append(q["lineup"])
            return {}
        if method == "PUT" and parts[:2] == ["livetv", "dvrs"] and parts[3:4] == ["devices"]:
            if parts[4] not in self.devices or not self.devices[parts[4]].get("ChannelMapping"):
                return 400  # Plex web maps the channels first
            for d in self.dvrs.values():
                if parts[4] in d["devices"]:
                    d["devices"].remove(parts[4])
            self.dvrs[parts[2]]["devices"].append(parts[4])
            self.devices[parts[4]]["parentID"] = parts[2]
            return {"MediaContainer": {"Dvr": [self._dvr_json(parts[2])]}}
        if method == "PUT" and parts[:3] == ["media", "grabbers", "devices"] and len(parts) == 4:
            if parts[3] not in self.devices or "enabled" not in q:
                return 400  # Plex refuses a title without enabled
            self.devices[parts[3]]["title"] = q.get("title", "")
            return {}
        if method == "POST" and parts[:2] == ["livetv", "dvrs"] and parts[3:] == ["reloadGuide"]:
            return {} if parts[2] in self.dvrs else None
        if (
            method == "PUT"
            and parts[:3] == ["media", "grabbers", "devices"]
            and parts[4:] == ["channelmap"]
        ):
            device = self.devices.get(parts[3])
            if device is None:
                return None
            if self.ignore_channelmap:
                return {}
            enabled = q.get("channelsEnabled", "").split(",")
            device["ChannelMapping"] = mapping(enabled)
            return {}
        return None


def mapping(enabled: Sequence[str], disabled: Sequence[str] = ()) -> list[dict[str, str]]:
    """A DVR listing's ``ChannelMapping`` as Plex gives it after a channel-map push."""
    return [
        {"channelKey": n, "deviceIdentifier": n, "lineupIdentifier": n, "enabled": on}
        for ns, on in ((enabled, "1"), (disabled, "0"))
        for n in ns
        if n
    ]


# ------------------------------------------------------------------ client + pure helpers


def dizquetv_query(numbers: list[int]) -> dict[str, Any]:
    """dizqueTV ``src/plex.js`` RefreshChannels, line for line."""
    channels = list(numbers)
    qs: dict[str, Any] = {"channelsEnabled": ",".join(str(c) for c in channels)}
    for c in channels:
        qs[f"channelMapping[{c}]"] = c
        qs[f"channelMappingByKey[{c}]"] = c
    return qs


def test_channel_map_query_is_dizquetvs() -> None:
    nums = [1, 2, 10, 105]
    got = channel_map_query(nums)
    want = dizquetv_query(nums)
    assert [k for k, _ in got] == list(want)  # same keys, same order
    assert dict(got) == {k: str(v) for k, v in want.items()}
    assert got[:3] == [
        ("channelsEnabled", "1,2,10,105"),
        ("channelMapping[1]", "1"),
        ("channelMappingByKey[1]", "1"),
    ]


def test_put_channel_map_url_is_encoded_as_dizquetv_sends_it() -> None:
    """``request``'s ``qs`` (dizqueTV) percent-encodes the brackets and the commas."""
    fake = FakeLiveTV()
    key = fake.add_device(TUNER, DEVICE_ID)
    PlexClient(BASE, "T", transport=fake).put_channel_map(key, [1, 2])
    method, path, raw = fake.calls[-1]
    assert (method, path) == ("PUT", f"/media/grabbers/devices/{key}/channelmap")
    assert raw == (
        "channelsEnabled=1%2C2&channelMapping%5B1%5D=1&channelMappingByKey%5B1%5D=1"
        "&channelMapping%5B2%5D=2&channelMappingByKey%5B2%5D=2"
    )
    assert "T" not in raw  # the token is a header, never in the URL


def test_xmltv_lineup_encodes_the_guide_address_and_title() -> None:
    assert xmltv_lineup("http://192.168.1.20:8782/xmltv.xml", "Maktaba TV") == (
        "lineup://tv.plex.providers.epg.xmltv/http%3A%2F%2F192.168.1.20%3A8782%2Fxmltv.xml"
        "#Maktaba%20TV"
    )


def test_dvrs_and_devices_are_parsed_including_single_objects() -> None:
    fake = FakeLiveTV()
    k = fake.add_device(TUNER, DEVICE_ID)
    d = fake.add_dvr(k, "Maktaba")
    fake.devices[k]["ChannelMapping"] = {"channelKey": "5", "lineupIdentifier": "5"}
    client = PlexClient(BASE, "T", transport=fake)
    (dvr,) = client.dvrs()
    assert (dvr.key, dvr.title) == (d, "Maktaba")
    (t,) = dvr.devices
    assert (t.key, t.device_id, t.uri, t.dvr_key, t.mapped) == (k, DEVICE_ID, TUNER, d, ("5",))
    assert [x.key for x in client.grabber_devices()] == [k]


def test_find_tuner_by_device_id_then_by_address() -> None:
    a = PlexTuner("1", uri="http://10.0.0.5:8000", device_id="DIZQUE01")
    b = PlexTuner("2", uri="http://192.168.1.20:8782", device_id="a1b2c3d4")
    assert find_tuner([a, b], DEVICE_ID, "") is b  # case-insensitive DeviceID
    assert find_tuner([a, b], "", "http://192.168.1.20:8782/") is b
    assert find_tuner([a, b], "", "192.168.1.20:8782") is b
    assert find_tuner([a, b], "FFFFFFFF", "http://192.168.1.99:8782") is None


def test_create_dvr_and_add_device_send_plexs_parameters() -> None:
    fake = FakeLiveTV(reachable={TUNER: DEVICE_ID})
    client = PlexClient(BASE, "T", transport=fake)
    dev = client.add_grabber_device(TUNER)
    assert dev is not None and dev.device_id == DEVICE_ID
    assert fake.calls[-1][2] == "uri=http%3A%2F%2F192.168.1.20%3A8782"
    dvr = client.create_dvr(dev.uuid, f"{TUNER}/xmltv.xml", "Maktaba", "ara")
    assert dvr is not None and dvr.devices[0].key == dev.key
    q = urllib.parse.parse_qs(fake.calls[-1][2])
    assert q["language"] == ["ara"]
    assert q["device"] == [f"device://tv.plex.grabbers.hdhomerun/{DEVICE_ID}"]
    assert q["lineup"] == [xmltv_lineup(f"{TUNER}/xmltv.xml", "Maktaba")]
    assert q["lineupTitle"] == ["Maktaba"]
    client.reload_guide(dvr.key)
    assert fake.calls[-1][:2] == ("POST", f"/livetv/dvrs/{dvr.key}/reloadGuide")


def test_channel_source_calls_send_plex_webs_parameters() -> None:
    fake = FakeLiveTV(reachable={TUNER: DEVICE_ID})
    client = PlexClient(BASE, "T", transport=fake)
    dz = fake.add_device(DIZQUE, "DIZQUE01", "dizqueTV")
    dvr = fake.add_dvr(dz, "dizqueTV")
    dev = client.add_grabber_device(TUNER)
    assert dev is not None
    guide = xmltv_lineup(f"{TUNER}/xmltv.xml", "Maktaba")
    client.put_channel_map(dev.key, ["1"])
    client.add_dvr_lineup(dvr, guide)
    assert urllib.parse.parse_qs(fake.calls[-1][2]) == {"lineup": [guide]}
    client.add_dvr_device(dvr, dev.key)
    client.set_device_title(dev.key, "Maktaba TV")
    assert urllib.parse.parse_qs(fake.calls[-1][2]) == {"title": ["Maktaba TV"], "enabled": ["1"]}
    got = next(d for d in client.dvrs() if d.key == dvr)
    assert [t.key for t in got.devices] == [dz, dev.key] and got.lineups[1] == guide
    client.delete_dvr(dvr)
    assert fake.calls[-1][:2] == ("DELETE", f"/livetv/dvrs/{dvr}") and client.dvrs() == []


@pytest.fixture
def http_plex() -> Iterator[tuple[str, list[tuple[str, str, dict[str, str]]]]]:
    """A real HTTP server answering like Plex: the default transport's wire format."""
    seen: list[tuple[str, str, dict[str, str]]] = []

    class H(http.server.BaseHTTPRequestHandler):
        def _any(self) -> None:
            seen.append((self.command, self.path, dict(self.headers)))
            body = b'{"MediaContainer":{"Dvr":[{"key":"7","Device":{"key":"3"}}]}}'
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        do_GET = do_POST = do_PUT = _any  # noqa: N815

        def log_message(self, *a: Any) -> None:
            pass

    srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), H)
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    try:
        yield f"http://127.0.0.1:{srv.server_address[1]}", seen
    finally:
        srv.shutdown()


def test_default_transport_against_a_real_http_server(
    http_plex: tuple[str, list[tuple[str, str, dict[str, str]]]],
) -> None:
    url, seen = http_plex
    client = PlexClient(url, "secret-token", timeout=5)
    (dvr,) = client.dvrs()
    assert dvr.key == "7" and dvr.devices[0].key == "3"  # one Device as an object
    client.put_channel_map("3", [4, 9])
    client.reload_guide("7")
    assert [(m, p.split("?")[0]) for m, p, _ in seen] == [
        ("GET", "/livetv/dvrs"),
        ("PUT", "/media/grabbers/devices/3/channelmap"),
        ("POST", "/livetv/dvrs/7/reloadGuide"),
    ]
    assert seen[1][1].endswith(
        "?channelsEnabled=4%2C9&channelMapping%5B4%5D=4&channelMappingByKey%5B4%5D=4"
        "&channelMapping%5B9%5D=9&channelMappingByKey%5B9%5D=9"
    )
    assert all(h.get("X-Plex-Token") == "secret-token" for _, _, h in seen)
    assert all("secret-token" not in p for _, p, _ in seen)


def test_target_from_rules_then_env_then_the_api() -> None:
    from maktaba_pipeline.extensions.plex.livetv import LiveState

    st = LiveState(requested_base_url="http://api.lan:8782")
    assert resolve_target(PlexLiveConfig(tuner_url="tv.lan:9000/"), st, {}) == Target(
        "http://tv.lan:9000", "http://tv.lan:9000/xmltv.xml", "rules"
    )
    env = {"MAKTABA_PUBLIC_BASE_URL": "http://env.lan:8782"}
    assert resolve_target(PlexLiveConfig(), st, env).source == "env"
    t = resolve_target(PlexLiveConfig(xmltv_url="http://g/x.xml"), st, {})
    assert t == Target("http://api.lan:8782", "http://g/x.xml", "api")
    assert resolve_target(PlexLiveConfig(), type(st)(), {}).source == ""


def test_defaults_touch_no_dvr_until_asked() -> None:
    cfg = PlexLiveConfig()
    assert cfg.auto_register is False
    assert cfg.auto_refresh_guide and cfg.auto_refresh_channels
    assert cfg.guide_refresh_min_minutes == 60


# ------------------------------------------------------------------ the stage


async def set_rules(db: Any, value: dict[str, Any]) -> None:
    await db.execute(
        "INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, $3)"
        " ON CONFLICT (key) DO UPDATE SET value = excluded.value",
        "rules.plexlive",
        dumps(value),
        to_ts(db, now()),
    )


async def add_channel(db: Any, number: int, *, enabled: bool = True) -> str:
    cid = str(uuid.uuid4())
    await db.execute(
        "INSERT INTO channels (id, number, name, slug, mode, mode_config, enabled, sort_order)"
        " VALUES ($1, $2, $3, $4, 'shuffle', $5, $6, $2)",
        cid,
        number,
        f"Channel {number}",
        f"ch-{number}",
        dumps({}),
        enabled,
    )
    return cid


async def generated(db: Any, channel_id: str, minutes_ago: float = 0) -> None:
    """The scheduler (re)generated this channel's timeline (the guide changed)."""
    at = to_ts(db, now() - timedelta(minutes=minutes_ago))
    await db.execute(
        "INSERT INTO channel_schedule_state (channel_id, last_generated_at, horizon_until)"
        " VALUES ($1, $2, $2) ON CONFLICT (channel_id, sched_gen) DO UPDATE SET"
        " last_generated_at = excluded.last_generated_at",
        channel_id,
        at,
    )


@dataclass
class World:
    fake: FakeLiveTV
    rt: EngineRuntime
    channels: list[str]


async def make_world(db: Any, *, enabled_tuner: bool = True, dizquetv: bool = True) -> World:
    await db.execute(
        "INSERT INTO hdhr_device (id, device_id, device_uuid, friendly_name, tuner_count, enabled)"
        " VALUES (1, $1, $2, 'Maktaba', 4, $3)",
        DEVICE_ID,
        str(uuid.uuid4()),
        enabled_tuner,
    )
    chans = [await add_channel(db, n) for n in (1, 2, 5)]
    await add_channel(db, 9, enabled=False)  # not in the lineup
    await set_rules(db, {"tuner_url": TUNER})
    fake = FakeLiveTV(reachable={TUNER: DEVICE_ID})
    if dizquetv:  # the user's dizqueTV tuner and DVR, set up by hand in Plex
        dk = fake.add_device(DIZQUE, "DIZQUE01", "dizqueTV")
        fake.add_dvr(dk, "dizqueTV", "lineup://tv.plex.providers.epg.xmltv/dz#dizqueTV")
    ext = PlexExtension(client_factory=lambda s: PlexClient(s["url"], s["token"], transport=fake))
    rt = await setup_runtime(db, [ext], SETTINGS)
    return World(fake, rt, chans)


async def livetv(db: Any, w: World, *, force: bool = False) -> Any:
    return await run_stage(db, w.rt, "plex_livetv", "catalog", CATALOG_ID, force=force)


async def ask_register(db: Any) -> None:
    await db.execute(
        "INSERT INTO plex_livetv_state (id, register_requested_at, requested_base_url, status,"
        " updated_at) VALUES (1, $1, $2, $3, $1) ON CONFLICT (id) DO UPDATE SET"
        " register_requested_at = excluded.register_requested_at",
        to_ts(db, now() - timedelta(seconds=5)),
        TUNER,
        "{}",
    )


async def status(db: Any) -> dict[str, Any]:
    row = await db.fetchrow("SELECT status FROM plex_livetv_state WHERE id = 1")
    return dict(loads(row["status"]))


def test_graph_wires_plex_livetv_after_write_channels_and_the_topup() -> None:
    ext = PlexExtension()
    graph = {s.name: s for s in build_full_graph([ext])}
    st = graph["plex_livetv"]
    assert st.extension == "plex" and st.after_topup and st.placement == "home"
    assert (st.subject_kinds, st.pool, st.debounce_sec) == (("catalog",), "network_plex", 30)
    assert ("plex_livetv", "same") in {(e.to, e.map) for e in graph["write_channels"].edges}
    assert [s.name for s in graph.values() if s.after_topup] == ["plex_livetv"]


@pytest.mark.asyncio
async def test_without_maktaba_in_plex_it_only_reports(db: Database) -> None:
    w = await make_world(db)
    row = await livetv(db, w)
    assert row["state"] == "done", error_of(row)
    assert w.fake.writes() == []  # auto_register is off: Plex is not changed
    s = await status(db)
    assert s["reachable"] and not s["maktaba"]["registered"]
    assert s["dizquetv_present"] is True
    assert [d["devices"][0]["kind"] for d in s["dvrs"]] == ["dizquetv"]
    assert s["channels"]["lineup"] == 3
    assert s["actions"]["channel_map"] == "not_registered"


def dizque_snapshot(w: World) -> Any:
    dk = next(k for k, d in w.fake.devices.items() if d["deviceId"] == "DIZQUE01")
    dvr = next(k for k, d in w.fake.dvrs.items() if dk in d["devices"])
    return dk, dvr, json.dumps(w.fake.devices[dk], sort_keys=True)


@pytest.mark.asyncio
async def test_register_joins_the_dvr_plex_shows_as_another_channel_source(
    db: Database,
) -> None:
    # Plex's Live TV & DVR page shows one DVR; further tuners are its channel sources,
    # each with its own guide ("Add Another Device"). A DVR of Maktaba's own stays hidden.
    w = await make_world(db)
    dz_key, dz_dvr, dz_before = dizque_snapshot(w)
    await ask_register(db)
    row = await livetv(db, w, force=True)
    assert row["state"] == "done", error_of(row)
    ours = next(k for k, d in w.fake.devices.items() if d["deviceId"] == DEVICE_ID)
    assert [(m, p) for m, p in w.fake.writes()] == [
        ("POST", "/media/grabbers/devices"),
        ("PUT", f"/media/grabbers/devices/{ours}/channelmap"),
        ("PUT", f"/livetv/dvrs/{dz_dvr}/lineups"),
        ("PUT", f"/livetv/dvrs/{dz_dvr}/devices/{ours}"),
        ("PUT", f"/media/grabbers/devices/{ours}"),
        ("POST", f"/livetv/dvrs/{dz_dvr}/reloadGuide"),
    ]
    assert len(w.fake.dvrs) == 1 and w.fake.dvrs[dz_dvr]["devices"] == [dz_key, ours]
    assert w.fake.dvrs[dz_dvr]["lineups"][1] == xmltv_lineup(f"{TUNER}/xmltv.xml", "Maktaba")
    assert w.fake.devices[ours]["title"] == "Maktaba"
    put = next(raw for m, p, raw in w.fake.calls if p.endswith("/channelmap"))
    assert urllib.parse.parse_qsl(put) == channel_map_query(["1", "2", "5"])
    # dizqueTV's tuner: same channels, name and guide
    assert dizque_snapshot(w)[2] == dz_before
    assert w.fake.dvrs[dz_dvr]["lineups"][0] == "lineup://tv.plex.providers.epg.xmltv/dz#dizqueTV"
    st = await load_state(db)
    assert st.register_requested_at is None and st.guide_reloaded_at is not None
    s = await status(db)
    assert s["maktaba"]["registered"] and not s["maktaba"]["separate_dvr"]
    assert s["actions"]["register"] == "registered" and s["channels"]["in_sync"] is True
    assert [t["kind"] for t in s["dvrs"][0]["devices"]] == ["dizquetv", "maktaba"]
    assert summary_of(row)["registered"] is True


@pytest.mark.asyncio
async def test_register_makes_a_dvr_when_plex_has_none(db: Database) -> None:
    w = await make_world(db, dizquetv=False)
    await ask_register(db)
    row = await livetv(db, w, force=True)
    assert row["state"] == "done", error_of(row)
    assert [p.rsplit("/", 1)[-1] for _, p in w.fake.writes()] == [
        "devices",  # the tuner
        "dvrs",  # a DVR with Maktaba's guide
        next(iter(w.fake.devices)),  # named
        "channelmap",
        "reloadGuide",
    ]
    assert (await status(db))["maktaba"]["registered"]


@pytest.mark.asyncio
async def test_a_dvr_of_its_own_is_shown_as_hidden_and_moved_in(db: Database) -> None:
    # what earlier versions made: a second DVR, which Plex's settings page leaves out
    w = await make_world(db)
    _, dz_dvr, dz_before = dizque_snapshot(w)
    ours = w.fake.add_device(TUNER, DEVICE_ID, "")
    w.fake.devices[ours]["ChannelMapping"] = [{"channelKey": "1", "lineupIdentifier": "1"}]
    own = w.fake.add_dvr(ours, "Maktaba", xmltv_lineup(f"{TUNER}/xmltv.xml", "Maktaba"))
    await livetv(db, w, force=True)
    s = await status(db)
    assert not s["maktaba"]["registered"] and s["maktaba"]["separate_dvr"]
    w.fake.calls.clear()
    await ask_register(db)
    row = await livetv(db, w, force=True)
    assert row["state"] == "done", error_of(row)
    assert ("DELETE", f"/livetv/dvrs/{own}") == w.fake.writes()[0]
    assert list(w.fake.dvrs) == [dz_dvr] and ours in w.fake.dvrs[dz_dvr]["devices"]
    assert dizque_snapshot(w)[2] == dz_before
    s = await status(db)
    assert s["maktaba"]["registered"] and not s["maktaba"]["separate_dvr"]


@pytest.mark.asyncio
async def test_a_dvr_shared_with_other_tuners_is_not_deleted(db: Database) -> None:
    w = await make_world(db)
    ours = w.fake.add_device(TUNER, DEVICE_ID, "")
    own = w.fake.add_dvr(ours, "Antenna and Maktaba")
    antenna = w.fake.add_device("http://192.168.1.30", "ANTENNA1", "Antenna")
    w.fake.dvrs[own]["devices"].append(antenna)
    await ask_register(db)
    row = await livetv(db, w, force=True)
    assert row["state"] == "done", error_of(row)
    assert own in w.fake.dvrs and not any(m == "DELETE" for m, _ in w.fake.writes())
    assert (await status(db))["actions"]["register"] == "in_another_dvr"


@pytest.mark.asyncio
async def test_register_finds_the_dvr_when_plex_answers_without_objects(db: Database) -> None:
    w = await make_world(db, dizquetv=False)
    w.fake.answer_with_objects = False
    await ask_register(db)
    row = await livetv(db, w, force=True)
    assert row["state"] == "done", error_of(row)
    assert (await status(db))["maktaba"]["registered"]


@pytest.mark.asyncio
async def test_register_is_idempotent_and_refused_when_a_dvr_has_the_tuner(
    db: Database,
) -> None:
    w = await make_world(db)
    await ask_register(db)
    await livetv(db, w, force=True)
    before = len(w.fake.dvrs)
    w.fake.calls.clear()
    await ask_register(db)
    row = await livetv(db, w, force=True)
    assert row["state"] == "done", error_of(row)
    assert len(w.fake.dvrs) == before
    assert not any(p in ("/media/grabbers/devices", "/livetv/dvrs") for _, p in w.fake.writes())
    assert (await status(db))["actions"]["register"] == "already_registered"


@pytest.mark.asyncio
async def test_register_refuses_while_the_tuner_is_off(db: Database) -> None:
    w = await make_world(db, enabled_tuner=False)
    await ask_register(db)
    row = await livetv(db, w, force=True)
    assert row["state"] == "done"
    assert w.fake.writes() == []
    assert (await status(db))["actions"]["register"] == "tuner_off"
    assert (await load_state(db)).register_requested_at is None  # answered


@pytest.mark.asyncio
async def test_auto_register_on_adds_it_without_a_request(db: Database) -> None:
    w = await make_world(db)
    await set_rules(db, {"tuner_url": TUNER, "auto_register": True})
    await livetv(db, w)
    assert (await status(db))["maktaba"]["registered"]


@pytest.mark.asyncio
async def test_a_tuner_plex_found_by_itself_is_used_not_added_again(db: Database) -> None:
    w = await make_world(db, dizquetv=False)
    w.fake.add_device("http://192.168.1.20:8782", DEVICE_ID, "Maktaba")  # SSDP found it
    await ask_register(db)
    await livetv(db, w, force=True)
    assert [p for _, p in w.fake.writes()][:1] == ["/livetv/dvrs"]
    assert len(w.fake.devices) == 1


@pytest.mark.asyncio
async def test_lineup_change_pushes_the_map_and_reloads_unchanged_does_nothing(
    db: Database,
) -> None:
    w = await make_world(db)
    await ask_register(db)
    await livetv(db, w, force=True)
    w.fake.calls.clear()
    row = await livetv(db, w)  # nothing changed
    assert row["state"] == "done", error_of(row)
    assert w.fake.writes() == []
    assert row["outcome"] == "unchanged"
    await add_channel(db, 7)  # write_channels added a channel
    row = await livetv(db, w)
    assert row["state"] == "done", error_of(row)
    writes = [p for _, p in w.fake.writes()]
    assert len(writes) == 2 and writes[0].endswith("/channelmap")
    assert writes[1].endswith("/reloadGuide")
    put = next(raw for m, _p, raw in w.fake.calls if m == "PUT")
    assert dict(urllib.parse.parse_qsl(put))["channelsEnabled"] == "1,2,5,7"


@pytest.mark.asyncio
async def test_auto_refresh_channels_off_leaves_the_map(db: Database) -> None:
    w = await make_world(db)
    await ask_register(db)
    await livetv(db, w, force=True)
    await set_rules(db, {"tuner_url": TUNER, "auto_refresh_channels": False})
    await add_channel(db, 7)
    w.fake.calls.clear()
    await livetv(db, w)
    assert not any(p.endswith("/channelmap") for _, p in w.fake.writes())
    s = await status(db)
    assert "Plex's channel list differs from Maktaba's lineup" in s["warnings"]
    # "Refresh Plex now" pushes it anyway
    await livetv(db, w, force=True)
    assert any(p.endswith("/channelmap") for _, p in w.fake.writes())


@pytest.mark.asyncio
async def test_guide_growth_reloads_at_most_every_n_minutes(db: Database) -> None:
    w = await make_world(db)
    await ask_register(db)
    await livetv(db, w, force=True)  # reloaded just now
    await generated(db, w.channels[0])  # the top-up grew the guide
    w.fake.calls.clear()
    row = await livetv(db, w)
    assert row["state"] == "paused"  # given back until the window has passed
    assert w.fake.writes() == []
    assert (await status(db))["actions"]["guide"] == "waiting"
    # an hour later the same job runs and reloads
    await db.execute(
        "UPDATE plex_livetv_state SET guide_reloaded_at = $1",
        to_ts(db, now() - timedelta(minutes=61)),
    )
    await db.execute("UPDATE processing_jobs SET not_before = NULL WHERE id = $1", row["id"])
    job = await claim_for_pool(db, worker_id="w1", pool="network_plex", stages=["plex_livetv"])
    assert job is not None and job.id == row["id"]
    await dispatch_job(db, job, runtime=w.rt, legacy_handlers={})
    done = await db.fetchrow("SELECT state FROM processing_jobs WHERE id = $1", row["id"])
    assert done["state"] == "done"
    assert [p for _, p in w.fake.writes()] == [
        f"/livetv/dvrs/{(await status(db))['maktaba']['dvr_key']}/reloadGuide"
    ]


@pytest.mark.asyncio
async def test_guide_auto_refresh_off_waits_for_refresh_now(db: Database) -> None:
    w = await make_world(db)
    await ask_register(db)
    await livetv(db, w, force=True)
    await set_rules(db, {"tuner_url": TUNER, "auto_refresh_guide": False})
    await generated(db, w.channels[1])
    w.fake.calls.clear()
    row = await livetv(db, w)
    assert row["state"] == "done" and w.fake.writes() == []
    await reloaded_minutes_ago(db, 10)  # Plex has finished loading the register's reload
    await livetv(db, w, force=True)
    assert any(p.endswith("/reloadGuide") for _, p in w.fake.writes())


async def reloaded_minutes_ago(db: Any, minutes: float) -> None:
    await db.execute(
        "UPDATE plex_livetv_state SET guide_reloaded_at = $1",
        to_ts(db, now() - timedelta(minutes=minutes)),
    )


@pytest.mark.asyncio
async def test_refresh_while_plex_loads_the_guide_sends_nothing(db: Database) -> None:
    # Plex aborts a guide load when another reload or channel map arrives meanwhile:
    # "Add to Plex" then two "Refresh Plex now" clicks left it with no guide at all
    w = await make_world(db)
    await ask_register(db)
    await livetv(db, w, force=True)  # added, mapped, reloaded just now
    w.fake.calls.clear()
    row = await livetv(db, w, force=True)
    assert row["state"] == "done", error_of(row)
    assert w.fake.writes() == []
    assert (await status(db))["actions"] == {
        "register": "",
        "channel_map": "unchanged",
        "guide": "loading",
    }
    # a lineup change is still pushed (and reloaded) at once
    await add_channel(db, 7)
    await livetv(db, w, force=True)
    assert [p.rsplit("/", 1)[1] for _, p in w.fake.writes()] == ["channelmap", "reloadGuide"]
    # once Plex has had time to load it, a refresh reloads again
    w.fake.calls.clear()
    await reloaded_minutes_ago(db, 6)
    await livetv(db, w, force=True)
    assert [p.rsplit("/", 1)[1] for _, p in w.fake.writes()] == ["channelmap", "reloadGuide"]


@pytest.mark.asyncio
async def test_refresh_while_loading_keeps_a_grown_guide_for_later(db: Database) -> None:
    # the top-up grew the guide and a refresh coalesced with it: wait, don't drop it
    w = await make_world(db)
    await ask_register(db)
    await livetv(db, w, force=True)
    await generated(db, w.channels[0])
    w.fake.calls.clear()
    row = await livetv(db, w, force=True)
    assert row["state"] == "paused" and w.fake.writes() == []
    assert (await status(db))["actions"]["guide"] == "loading"
    await reloaded_minutes_ago(db, 6)
    await db.execute("UPDATE processing_jobs SET not_before = NULL WHERE id = $1", row["id"])
    job = await claim_for_pool(db, worker_id="w1", pool="network_plex", stages=["plex_livetv"])
    assert job is not None and job.id == row["id"]
    await dispatch_job(db, job, runtime=w.rt, legacy_handlers={})
    assert any(p.endswith("/reloadGuide") for _, p in w.fake.writes())


@pytest.mark.asyncio
async def test_a_register_that_fails_half_way_keeps_the_request_for_the_retry(
    db: Database,
) -> None:
    w = await make_world(db, dizquetv=False)  # no DVR yet: Maktaba makes one
    await ask_register(db)
    w.fake.dvr_failures = 1  # the tuner is added, then the DVR call times out
    row = await livetv(db, w, force=True)
    assert row["state"] in ("pending", "failed")
    assert len(w.fake.devices) == 1 and len(w.fake.dvrs) == 0
    assert (await load_state(db)).register_requested_at is not None  # not answered yet
    row = await livetv(db, w, force=True)
    assert row["state"] == "done", error_of(row)
    # the retry uses the tuner the first run added and creates the DVR
    assert len(w.fake.devices) == 1 and len(w.fake.dvrs) == 1
    s = await status(db)
    assert s["maktaba"]["registered"] and s["actions"]["register"] == "registered"
    assert (await load_state(db)).register_requested_at is None


@pytest.mark.asyncio
async def test_plex_down_fails_and_reports_it(db: Database) -> None:
    w = await make_world(db)
    w.fake.down = True
    row = await livetv(db, w)
    assert row["state"] in ("pending", "failed")
    assert error_of(row)["kind"] == "plex_unreachable"
    s = await status(db)
    assert s["reachable"] is False and "connection refused" in s["error"]
    st = await db.fetchrow("SELECT last_error FROM plex_livetv_state WHERE id = 1")
    assert "connection refused" in st["last_error"]


@pytest.mark.asyncio
async def test_the_topup_enqueues_its_listeners(db: Database) -> None:
    w = await make_world(db)
    await notify_listeners(db, ("plex_livetv",), 3)
    await notify_listeners(db, ("plex_livetv",), 1)  # coalesced into the same job
    rows = await db.fetch("SELECT stage, state FROM processing_jobs WHERE stage = 'plex_livetv'")
    assert [(r["stage"], r["state"]) for r in rows] == [("plex_livetv", "pending")]
    assert w.rt.graph["plex_livetv"].after_topup


# ------------------------------------------------------------------ drift (another program)


def test_mapped_reads_the_tuners_channel_numbers_enabled_only() -> None:
    fake = FakeLiveTV()
    k = fake.add_device(TUNER, DEVICE_ID)
    fake.add_dvr(k, "Maktaba")
    # dizqueTV's refresh enabled its numbers on Maktaba's tuner; Maktaba's are switched off
    fake.devices[k]["ChannelMapping"] = [
        *mapping(["101", "102"], ["201", "202"]),
        {"deviceIdentifier": "103", "lineupIdentifier": "guide-103", "enabled": "1"},
    ]
    (t,) = PlexClient(BASE, "T", transport=fake).dvrs()[0].devices
    assert t.mapped == ("101", "102", "103")  # by deviceIdentifier, enabled only


async def registered(db: Any) -> tuple[World, str]:
    """Maktaba added to Plex next to dizqueTV, its channels (1, 2, 5) mapped."""
    w = await make_world(db)
    await ask_register(db)
    row = await livetv(db, w, force=True)
    assert row["state"] == "done", error_of(row)
    ours = next(k for k, d in w.fake.devices.items() if d["deviceId"] == DEVICE_ID)
    return w, ours


def dizquetv_refresh(w: World, ours: str) -> None:
    """dizqueTV's hourly Plex refresh: its channel map onto every tuner, Maktaba's too."""
    w.fake.devices[ours]["ChannelMapping"] = mapping(["101", "102", "103"], ["1", "2", "5"])


def pushed_numbers(w: World) -> list[str]:
    raw = next(raw for m, p, raw in w.fake.calls if m == "PUT" and p.endswith("/channelmap"))
    return dict(urllib.parse.parse_qsl(raw))["channelsEnabled"].split(",")


@pytest.mark.asyncio
async def test_a_map_another_program_replaced_is_restored_on_a_normal_run(
    db: Database,
) -> None:
    w, ours = await registered(db)
    await reloaded_minutes_ago(db, 10)
    dizquetv_refresh(w, ours)
    w.fake.calls.clear()
    row = await livetv(db, w)  # not forced: the periodic check, the top-up
    assert row["state"] == "done", error_of(row)
    assert [p.rsplit("/", 1)[1] for _, p in w.fake.writes()] == ["channelmap", "reloadGuide"]
    assert pushed_numbers(w) == ["1", "2", "5"]
    s = await status(db)
    assert s["channels"] == {"lineup": 3, "mapped": 3, "in_sync": True, "drift": True}
    assert DRIFT_RESTORED in s["warnings"]
    assert s["drift"]["repairs"] == 1 and s["drift"]["last_at"]
    assert s["actions"]["channel_map"] == "pushed"
    # the next check finds Plex as Maktaba left it: nothing is sent, the count stays
    w.fake.calls.clear()
    row = await livetv(db, w)
    assert w.fake.writes() == [] and row["outcome"] == "unchanged"
    s = await status(db)
    assert s["channels"]["drift"] is False and s["drift"]["repairs"] == 1
    assert DRIFT_RESTORED not in s["warnings"]


@pytest.mark.asyncio
async def test_in_sync_is_what_plex_holds_not_what_was_sent(db: Database) -> None:
    w, ours = await registered(db)
    dizquetv_refresh(w, ours)
    w.fake.ignore_channelmap = True  # Plex answers but keeps dizqueTV's map
    await reloaded_minutes_ago(db, 10)
    await livetv(db, w)
    s = await status(db)
    assert s["actions"]["channel_map"] == "pushed"
    assert s["channels"]["in_sync"] is False and s["channels"]["mapped"] == 3
    assert "Plex's channel list differs from Maktaba's lineup" in s["warnings"]


@pytest.mark.asyncio
async def test_drift_is_reported_not_restored_with_auto_refresh_channels_off(
    db: Database,
) -> None:
    w, ours = await registered(db)
    await set_rules(db, {"tuner_url": TUNER, "auto_refresh_channels": False})
    dizquetv_refresh(w, ours)
    w.fake.calls.clear()
    await livetv(db, w)
    assert not any(p.endswith("/channelmap") for _, p in w.fake.writes())
    s = await status(db)
    assert s["channels"]["in_sync"] is False and s["channels"]["drift"] is True
    assert DRIFT_NOT_RESTORED in s["warnings"] and s["drift"]["repairs"] == 0


@pytest.mark.asyncio
async def test_restores_in_a_row_advise_turning_off_dizquetvs_channel_mapping(
    db: Database,
) -> None:
    w, ours = await registered(db)
    await set_rules(db, {"tuner_url": TUNER, "drift_warn_repeats": 2})
    for n in (1, 2):
        dizquetv_refresh(w, ours)
        await reloaded_minutes_ago(db, 10)
        await livetv(db, w)
        s = await status(db)
        assert s["drift"]["repairs"] == n
        assert (drift_repeated(2) in s["warnings"]) is (n == 2)
    # it stays on the panel while it keeps happening
    await livetv(db, w)
    assert drift_repeated(2) in (await status(db))["warnings"]
    # a restore long after the last one counts from one again
    s = await status(db)
    s["drift"]["last_at"] = (now() - timedelta(hours=25)).isoformat()
    await db.execute("UPDATE plex_livetv_state SET status = $1 WHERE id = 1", dumps(s))
    dizquetv_refresh(w, ours)
    await reloaded_minutes_ago(db, 10)
    await livetv(db, w)
    s = await status(db)
    assert s["drift"]["repairs"] == 1 and not any("in a row" in x for x in s["warnings"])


# ------------------------------------------------------------------ refresh now, periodic check


@pytest.mark.asyncio
async def test_refresh_now_runs_at_once_while_a_guide_reload_waits_its_window(
    db: Database,
) -> None:
    # the top-up's reload waits for guide_refresh_min_minutes (given back, ~50 min);
    # "Refresh Plex now" (a forced enqueue) must not wait behind it
    w = await make_world(db)
    await ask_register(db)
    await livetv(db, w, force=True)
    await reloaded_minutes_ago(db, 10)
    await generated(db, w.channels[0])
    w.fake.calls.clear()
    row = await livetv(db, w)
    assert row["state"] == "paused" and row["not_before"] is not None
    res = await enqueue_job(
        db,
        subject_kind="catalog",
        subject_id=CATALOG_ID,
        stage="plex_livetv",
        force=True,
        reason={"why": "refresh", "by": "user:1"},
    )
    assert (res.outcome, res.job_id) == ("coalesced", row["id"])
    job_row = await db.fetchrow("SELECT * FROM processing_jobs WHERE id = $1", row["id"])
    assert job_row["not_before"] is None and bool(job_row["force"]) is True
    job = await claim_for_pool(db, worker_id="w1", pool="network_plex", stages=["plex_livetv"])
    assert job is not None and job.id == row["id"]
    await dispatch_job(db, job, runtime=w.rt, legacy_handlers={})
    done = await db.fetchrow("SELECT state FROM processing_jobs WHERE id = $1", row["id"])
    assert done["state"] == "done"
    assert any(p.endswith("/reloadGuide") for _, p in w.fake.writes())


async def plex_jobs(db: Any) -> list[Any]:
    return list(
        await db.fetch("SELECT id, state, reasons FROM processing_jobs WHERE stage = 'plex_livetv'")
    )


@pytest.mark.asyncio
async def test_the_reconciler_checks_plex_every_drift_check_minutes(db: Database) -> None:
    w = await make_world(db)
    graph = w.rt.graph
    assert graph["plex_livetv"].every_minutes_rule == ("plexlive", "drift_check_minutes")
    assert PlexLiveConfig().drift_check_minutes == 15
    report = await reconcile_once(db, graph)
    assert report.periodic == 1
    (job,) = await plex_jobs(db)
    assert job["state"] == "pending"
    assert loads(job["reasons"])[0]["why"] == "periodic check"
    # queued already: nothing more
    assert (await reconcile_once(db, graph)).periodic == 0
    # finished a minute ago: not yet
    await db.execute(
        "UPDATE processing_jobs SET state = 'done', finished_at = $1 WHERE id = $2",
        to_ts(db, now() - timedelta(minutes=1)),
        job["id"],
    )
    assert (await reconcile_once(db, graph)).periodic == 0
    # finished 16 minutes ago: checked again
    await db.execute(
        "UPDATE processing_jobs SET finished_at = $1 WHERE id = $2",
        to_ts(db, now() - timedelta(minutes=16)),
        job["id"],
    )
    assert (await reconcile_once(db, graph)).periodic == 1
    assert len(await plex_jobs(db)) == 2


@pytest.mark.asyncio
async def test_drift_check_minutes_zero_switches_the_periodic_check_off(db: Database) -> None:
    w = await make_world(db)
    await set_rules(db, {"tuner_url": TUNER, "drift_check_minutes": 0})
    assert (await reconcile_once(db, w.rt.graph)).periodic == 0
    assert await plex_jobs(db) == []
