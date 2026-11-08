"""Plex HTTP client for Plex Live TV (identity, DVRs, tuners, channel maps, guide reloads).

Only the ``plex`` extension (and the relay that carries its calls) imports it. The client is
synchronous and talks through an injectable :class:`Transport`, so tests drive it
with a fake server and handlers call it through ``asyncio.to_thread``.

The token travels in the ``X-Plex-Token`` header, never in the URL, so it cannot
leak into logs or error messages.
"""

from __future__ import annotations

import json
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass
from typing import Any, Protocol

__all__ = [
    "PlexClient",
    "PlexDvr",
    "PlexError",
    "PlexTuner",
    "Transport",
    "TransportError",
    "UrllibTransport",
    "channel_map_query",
    "find_tuner",
    "hdhr_device_id",
    "quote",
    "same_host",
    "xmltv_lineup",
]

DEFAULT_TIMEOUT = 120.0
#: the lineup URI scheme of Plex's XMLTV guide provider
XMLTV_LINEUP_PREFIX = "lineup://tv.plex.providers.epg.xmltv/"
#: the uuid Plex gives an HDHomeRun tuner: ``device://tv.plex.grabbers.hdhomerun/<DeviceID>``
HDHR_UUID_PREFIX = "device://tv.plex.grabbers.hdhomerun/"


class TransportError(Exception):
    """The request never produced an HTTP response (refused, timeout, DNS ...)."""


class PlexError(Exception):
    """Plex answered with an error status, or could not be reached."""

    def __init__(self, message: str, status: int | None = None) -> None:
        super().__init__(message)
        self.status = status


class Transport(Protocol):
    """One HTTP exchange; returns ``(status, body)`` and raises :class:`TransportError`."""

    def request(
        self,
        method: str,
        url: str,
        headers: Mapping[str, str],
        body: bytes | None,
        timeout: float,
    ) -> tuple[int, bytes]: ...


class UrllibTransport:
    """The default transport (standard library only)."""

    def request(
        self,
        method: str,
        url: str,
        headers: Mapping[str, str],
        body: bytes | None,
        timeout: float,
    ) -> tuple[int, bytes]:
        req = urllib.request.Request(url, data=body, method=method, headers=dict(headers))  # noqa: S310
        try:
            with urllib.request.urlopen(req, timeout=timeout) as r:  # noqa: S310 - user-configured server
                return int(r.status), bytes(r.read())
        except urllib.error.HTTPError as exc:
            return int(exc.code), bytes(exc.read() or b"")
        except (urllib.error.URLError, TimeoutError, OSError) as exc:
            raise TransportError(str(getattr(exc, "reason", exc))) from exc


def quote(s: str) -> str:
    return urllib.parse.quote(s, safe="")


@dataclass(frozen=True, slots=True)
class PlexTuner:
    """A tuner ("grabber device") Plex knows, as ``/media/grabbers/devices`` or a DVR's
    ``Device`` lists it."""

    key: str
    uuid: str = ""
    uri: str = ""
    #: the HDHomeRun DeviceID (``deviceId``, else the tail of ``uuid``)
    device_id: str = ""
    title: str = ""
    make: str = ""
    model: str = ""
    #: the DVR it belongs to (``parentID``, or the DVR that lists it)
    dvr_key: str = ""
    #: the channel numbers Plex has enabled on it (``ChannelMapping`` with ``enabled`` 1, by
    #: ``deviceIdentifier``: the tuner's GuideNumber); None when Plex listed no mapping
    mapped: tuple[str, ...] | None = None


@dataclass(frozen=True, slots=True)
class PlexDvr:
    """One Live TV DVR (``/livetv/dvrs``): a guide (``lineup``) and its tuners."""

    key: str
    uuid: str = ""
    title: str = ""
    lineup: str = ""
    language: str = ""
    devices: tuple[PlexTuner, ...] = ()
    #: every guide the DVR carries (``Lineup``): one per channel source since Plex's
    #: multi-lineup DVRs
    lineups: tuple[str, ...] = ()


def _list(raw: Any) -> list[dict[str, Any]]:
    """Plex gives one element as an object and several as a list."""
    if isinstance(raw, dict):
        return [raw]
    return [x for x in raw if isinstance(x, dict)] if isinstance(raw, list) else []


def hdhr_device_id(uuid: str) -> str:
    """``device://tv.plex.grabbers.hdhomerun/1A2B3C4D`` -> ``1A2B3C4D`` ("" for any other)."""
    return uuid[len(HDHR_UUID_PREFIX) :] if uuid.startswith(HDHR_UUID_PREFIX) else ""


def _mapped(raw: Any) -> tuple[str, ...] | None:
    if raw is None:
        return None
    out: list[str] = []
    for m in _list(raw):
        if str(m.get("enabled", "1")).strip().lower() in ("0", "false"):
            continue
        # the tuner's own channel number; an older listing without it: the guide channel
        ident = str(
            m.get("deviceIdentifier") or m.get("lineupIdentifier") or m.get("channelKey") or ""
        ).strip()
        if ident and ident not in out:
            out.append(ident)
    return tuple(out)


def _tuner(d: Mapping[str, Any], dvr_key: str = "") -> PlexTuner:
    uuid = str(d.get("uuid") or "")
    return PlexTuner(
        key=str(d.get("key") or ""),
        uuid=uuid,
        uri=str(d.get("uri") or ""),
        device_id=str(d.get("deviceId") or hdhr_device_id(uuid)),
        title=str(d.get("title") or d.get("name") or ""),
        make=str(d.get("make") or ""),
        model=str(d.get("model") or ""),
        dvr_key=str(d.get("parentID") or dvr_key),
        mapped=_mapped(d.get("ChannelMapping")),
    )


def _dvr(d: Mapping[str, Any]) -> PlexDvr:
    key = str(d.get("key") or "")
    return PlexDvr(
        key=key,
        uuid=str(d.get("uuid") or ""),
        title=str(d.get("title") or d.get("lineupTitle") or ""),
        lineup=str(d.get("lineup") or ""),
        language=str(d.get("language") or ""),
        devices=tuple(_tuner(x, key) for x in _list(d.get("Device"))),
        lineups=tuple(str(x["id"]) for x in _list(d.get("Lineup")) if x.get("id"))
        or ((str(d["lineup"]),) if d.get("lineup") else ()),
    )


def _host(url: str) -> str:
    u = urllib.parse.urlsplit(url if "://" in url else f"http://{url}")
    try:
        port = u.port
    except ValueError:
        port = None
    port = port or (443 if u.scheme == "https" else 80)
    return f"{(u.hostname or '').lower()}:{port}"


def same_host(a: str, b: str) -> bool:
    """Do two addresses name the same host and port (scheme and path aside)?"""
    return bool(a) and bool(b) and _host(a) == _host(b)


def find_tuner(tuners: Iterable[PlexTuner], device_id: str, base_url: str) -> PlexTuner | None:
    """Maktaba's tuner among Plex's: by its HDHomeRun DeviceID first, else by its address."""
    tuners = list(tuners)
    if device_id:
        for t in tuners:
            if t.device_id and t.device_id.casefold() == device_id.casefold():
                return t
    if base_url:
        for t in tuners:
            if same_host(t.uri, base_url):
                return t
    return None


def xmltv_lineup(xmltv_url: str, title: str) -> str:
    """The lineup id of an XMLTV guide, ``lineup://tv.plex.providers.epg.xmltv/<url>#<title>``,
    the guide's address and title each percent-encoded (as Plex Web sends it)."""
    return f"{XMLTV_LINEUP_PREFIX}{quote(xmltv_url)}#{quote(title)}"


def xmltv_url_of(lineup: str) -> str:
    """The guide address of an XMLTV lineup id (:func:`xmltv_lineup`); "" for any other."""
    if not lineup.startswith(XMLTV_LINEUP_PREFIX):
        return ""
    return urllib.parse.unquote(lineup[len(XMLTV_LINEUP_PREFIX) :].partition("#")[0])


def channel_map_query(numbers: Sequence[int | str]) -> list[tuple[str, str]]:
    """The query of ``PUT /media/grabbers/devices/{key}/channelmap``, built exactly as
    dizqueTV's ``RefreshChannels`` builds it: ``channelsEnabled`` (the numbers, comma
    joined), then per channel ``channelMapping[n]=n`` and ``channelMappingByKey[n]=n`` (the
    XMLTV channel id is the channel number, as the HDHomeRun GuideNumber is)."""
    nums = [str(n) for n in numbers]
    out = [("channelsEnabled", ",".join(nums))]
    for n in nums:
        out.append((f"channelMapping[{n}]", n))
        out.append((f"channelMappingByKey[{n}]", n))
    return out


class PlexClient:
    """Plex Media Server JSON API."""

    def __init__(
        self,
        url: str,
        token: str,
        *,
        transport: Transport | None = None,
        timeout: float = DEFAULT_TIMEOUT,
    ) -> None:
        self.uri = url.rstrip("/")
        self._token = token
        self._transport: Transport = transport or UrllibTransport()
        self.timeout = timeout

    @property
    def token(self) -> str:
        return self._token

    # ------------------------------------------------------------------ raw

    def send(self, method: str, path: str) -> dict[str, Any]:
        headers = {"Accept": "application/json", "X-Plex-Token": self._token}
        status, body = self._transport.request(
            method, f"{self.uri}{path}", headers, None, self.timeout
        )
        if status >= 400:
            raise PlexError(f"plex {method} {path.split('?', 1)[0]} -> HTTP {status}", status)
        if not body:
            return {}
        try:
            doc = json.loads(body)
        except ValueError as exc:
            raise PlexError(f"plex {method} {path.split('?', 1)[0]}: invalid JSON") from exc
        return doc if isinstance(doc, dict) else {}

    def get(self, path: str) -> dict[str, Any]:
        return self.send("GET", path)

    @staticmethod
    def _q(path: str, query: Sequence[tuple[str, str]] | Mapping[str, str]) -> str:
        return f"{path}?{urllib.parse.urlencode(query, quote_via=urllib.parse.quote)}"

    def _container(self, path: str) -> dict[str, Any]:
        c = self.get(path).get("MediaContainer")
        return c if isinstance(c, dict) else {}

    # ------------------------------------------------------------------ reads

    def machine_identifier(self) -> str:
        return str(self._container("/identity").get("machineIdentifier", ""))

    def ping(self) -> None:
        """Raise unless the server answers ``/identity``."""
        self.get("/identity")

    # ------------------------------------------------------------------ Live TV

    def dvrs(self) -> list[PlexDvr]:
        """Every Live TV DVR (``GET /livetv/dvrs``) with its tuners."""
        return [_dvr(d) for d in _list(self._container("/livetv/dvrs").get("Dvr"))]

    def grabber_devices(self) -> list[PlexTuner]:
        """Every tuner Plex knows (``GET /media/grabbers/devices``), in a DVR or not."""
        devs = self._container("/media/grabbers/devices").get("Device")
        return [_tuner(d) for d in _list(devs)]

    def add_grabber_device(self, uri: str) -> PlexTuner | None:
        """``POST /media/grabbers/devices?uri=<tuner base>``: Plex reads the tuner's
        ``discover.json`` and adds it. Returns the device when Plex answers with it."""
        doc = self.send("POST", self._q("/media/grabbers/devices", {"uri": uri}))
        mc = doc.get("MediaContainer")
        devs = _list(mc.get("Device")) if isinstance(mc, dict) else []
        return _tuner(devs[0]) if devs else None

    def create_dvr(
        self, device_uuid: str, xmltv_url: str, title: str, language: str
    ) -> PlexDvr | None:
        """``POST /livetv/dvrs?language=..&device=<uuid>&lineup=<xmltv lineup>&lineupTitle=..``:
        a DVR on that tuner whose guide is the XMLTV file. Returns it when Plex answers
        with it (callers list the DVRs again otherwise)."""
        query = [
            ("language", language),
            ("device", device_uuid),
            ("lineup", xmltv_lineup(xmltv_url, title)),
            ("lineupTitle", title),
        ]
        doc = self.send("POST", self._q("/livetv/dvrs", query))
        mc = doc.get("MediaContainer")
        dvrs = _list(mc.get("Dvr")) if isinstance(mc, dict) else []
        return _dvr(dvrs[0]) if dvrs else None

    def add_dvr_lineup(self, dvr_key: str, lineup: str) -> None:
        """``PUT /livetv/dvrs/{key}/lineups?lineup=..``: the DVR carries this guide too
        (Plex web's "Add Another Device", before the device itself)."""
        self.send("PUT", self._q(f"/livetv/dvrs/{quote(dvr_key)}/lineups", {"lineup": lineup}))

    def add_dvr_device(self, dvr_key: str, device_key: str) -> None:
        """``PUT /livetv/dvrs/{key}/devices/{device}``: the tuner becomes one of the DVR's
        channel sources."""
        self.send("PUT", f"/livetv/dvrs/{quote(dvr_key)}/devices/{quote(device_key)}")

    def delete_dvr(self, dvr_key: str) -> None:
        """``DELETE /livetv/dvrs/{key}``: the DVR goes; its tuners stay known to Plex."""
        self.send("DELETE", f"/livetv/dvrs/{quote(dvr_key)}")

    def remove_dvr_lineup(self, dvr_key: str, lineup: str) -> None:
        """``DELETE /livetv/dvrs/{key}/lineups?lineup=..``: the DVR no longer carries this
        guide."""
        self.send("DELETE", self._q(f"/livetv/dvrs/{quote(dvr_key)}/lineups", {"lineup": lineup}))

    def remove_dvr_device(self, dvr_key: str, device_key: str) -> None:
        """``DELETE /livetv/dvrs/{key}/devices/{device}``: the tuner is no longer one of the
        DVR's channel sources (Plex still knows it)."""
        self.send("DELETE", f"/livetv/dvrs/{quote(dvr_key)}/devices/{quote(device_key)}")

    def delete_grabber_device(self, device_key: str) -> None:
        """``DELETE /media/grabbers/devices/{key}``: Plex forgets the tuner."""
        self.send("DELETE", f"/media/grabbers/devices/{quote(device_key)}")

    def set_device_title(self, device_key: str, title: str) -> None:
        """``PUT /media/grabbers/devices/{key}?title=..&enabled=1``: the name Plex shows for
        the channel source (Plex refuses the title without ``enabled``)."""
        path = f"/media/grabbers/devices/{quote(device_key)}"
        self.send("PUT", self._q(path, [("title", title), ("enabled", "1")]))

    def reload_guide(self, dvr_key: str) -> None:
        """``POST /livetv/dvrs/{key}/reloadGuide``: Plex fetches the XMLTV guide again."""
        self.send("POST", f"/livetv/dvrs/{quote(dvr_key)}/reloadGuide")

    def put_channel_map(self, device_key: str, numbers: Sequence[int | str]) -> None:
        """``PUT /media/grabbers/devices/{key}/channelmap`` with :func:`channel_map_query`:
        exactly these channels enabled, each mapped to its guide channel."""
        path = f"/media/grabbers/devices/{quote(device_key)}/channelmap"
        self.send("PUT", self._q(path, channel_map_query(numbers)))
