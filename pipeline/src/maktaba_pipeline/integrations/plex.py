"""Plex HTTP client (ported from ``scripts/mediaintel/scan_plex.py`` and ``export_plex.py``).

Only the ``plex`` and ``dizquetv`` extensions import this module. The client is
synchronous and talks through an injectable :class:`Transport`, so tests drive it
with a fake server and handlers call it through ``asyncio.to_thread``.

The token travels in the ``X-Plex-Token`` header, never in the URL, so it cannot
leak into logs or error messages.
"""

from __future__ import annotations

import contextlib
import json
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass
from typing import Any, Protocol

__all__ = [
    "PlexClient",
    "PlexCollection",
    "PlexDvr",
    "PlexError",
    "PlexItem",
    "PlexSection",
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
#: ``type`` query value that lists playable items of a section type
_LIST_TYPE = {"movie": 1, "show": 4}


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


def in_order(got: list[str], target: list[str]) -> set[str]:
    """The items of ``got`` already in ``target``'s order: the longest increasing run of
    their target positions. They stay; the others are moved."""
    pos = {k: i for i, k in enumerate(target)}
    seq = [k for k in got if k in pos]
    tails: list[int] = []  # tails[j]: index in seq ending the best run of length j + 1
    prev = [-1] * len(seq)
    for i, k in enumerate(seq):
        lo, hi = 0, len(tails)
        while lo < hi:
            mid = (lo + hi) // 2
            if pos[seq[tails[mid]]] < pos[k]:
                lo = mid + 1
            else:
                hi = mid
        prev[i] = tails[lo - 1] if lo else -1
        if lo == len(tails):
            tails.append(i)
        else:
            tails[lo] = i
    out: set[str] = set()
    i = tails[-1] if tails else -1
    while i >= 0:
        out.add(seq[i])
        i = prev[i]
    return out


@dataclass(frozen=True, slots=True)
class PlexSection:
    key: str
    title: str
    type: str
    locations: tuple[str, ...] = ()


@dataclass(frozen=True, slots=True)
class PlexItem:
    rating_key: str
    title: str
    file: str
    section_key: str
    duration_ms: int | None = None


@dataclass(frozen=True, slots=True)
class PlexCollection:
    rating_key: str
    title: str
    smart: bool = False
    child_count: int | None = None
    #: label tags as listed (empty when the listing did not include them)
    labels: tuple[str, ...] = ()


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
    #: the lineup identifiers of its enabled channel mappings; None when Plex listed none
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
        ident = str(m.get("lineupIdentifier") or m.get("channelKey") or "").strip()
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


def _tags(raw: Any) -> tuple[str, ...]:
    return tuple(str(t["tag"]) for t in raw or () if isinstance(t, dict) and t.get("tag"))


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

    def sections(self, types: tuple[str, ...] = ("movie", "show")) -> list[PlexSection]:
        out: list[PlexSection] = []
        for d in self._container("/library/sections").get("Directory", []):
            if d.get("type") not in types:
                continue
            locs = tuple(str(loc["path"]) for loc in d.get("Location", []) if isinstance(loc, dict))
            out.append(PlexSection(str(d["key"]), str(d.get("title", "")), str(d["type"]), locs))
        return out

    def section_listing(self, section: PlexSection) -> list[dict[str, Any]]:
        """Plex's full record of every item of a section, in one call. The listing
        carries every field a dizqueTV programme needs (title, key, duration,
        thumb, dates, media part), so callers can save it instead of asking
        ``/library/metadata`` item by item later."""
        ptype = _LIST_TYPE.get(section.type, 1)
        items = self._container(f"/library/sections/{section.key}/all?type={ptype}")
        md = items.get("Metadata", [])
        return [m for m in md if isinstance(m, dict)] if isinstance(md, list) else []

    def section_items(self, section: PlexSection) -> list[PlexItem]:
        """Every playable item of a section that has a media file."""
        out: list[PlexItem] = []
        for m in self.section_listing(section):
            try:
                part = m["Media"][0]["Part"][0]
                file = str(part["file"])
            except (KeyError, IndexError, TypeError):
                continue
            out.append(
                PlexItem(
                    str(m["ratingKey"]),
                    str(m.get("title", "")),
                    file,
                    section.key,
                    int(m["duration"]) if m.get("duration") else None,
                )
            )
        return out

    def item_section(self, rating_key: str) -> str | None:
        """The library section a rating key lives in."""
        md = self._container(f"/library/metadata/{rating_key}").get("Metadata", [])
        if not md:
            return None
        sid = md[0].get("librarySectionID")
        return None if sid is None else str(sid)

    def metadata(self, keys: list[str]) -> dict[str, dict[str, Any]]:
        """Full metadata for rating keys (batched), keyed by rating key."""
        out: dict[str, dict[str, Any]] = {}
        for i in range(0, len(keys), 50):
            md = self._container(f"/library/metadata/{','.join(keys[i : i + 50])}")
            for m in md.get("Metadata", []):
                out[str(m["ratingKey"])] = m
        return out

    def collections(self, section_key: str) -> list[PlexCollection]:
        md = self._container(f"/library/sections/{section_key}/collections").get("Metadata", [])
        return [
            PlexCollection(
                str(m["ratingKey"]),
                str(m.get("title", "")),
                bool(m.get("smart")),
                int(m["childCount"]) if m.get("childCount") is not None else None,
                _tags(m.get("Label")),
            )
            for m in md
        ]

    def collection_labels(self, rating_key: str) -> tuple[str, ...]:
        """A collection's label tags, read from its own metadata."""
        md = self._container(f"/library/metadata/{rating_key}").get("Metadata", [])
        return _tags(md[0].get("Label")) if md else ()

    def collection_children(self, rating_key: str) -> list[str]:
        md = self._container(f"/library/collections/{rating_key}/children").get("Metadata", [])
        return [str(m["ratingKey"]) for m in md]

    # ------------------------------------------------------------------ writes

    def delete_metadata(self, rating_key: str) -> None:
        self.send("DELETE", f"/library/metadata/{rating_key}")

    def create_collection(self, machine: str, section_key: str, title: str, keys: list[str]) -> str:
        """Create a manual collection holding ``keys`` in order; returns its rating key
        (items Plex refuses stay out: :meth:`create_collection_report` names them).

        Plex stores manual-collection membership as a tag named after the
        collection, so a same-named collection must be deleted *before* this is
        called, never after.
        """
        return self.create_collection_report(machine, section_key, title, keys)[0]

    def create_collection_report(
        self, machine: str, section_key: str, title: str, keys: list[str]
    ) -> tuple[str, dict[str, str]]:
        """:meth:`create_collection`, returning also the items Plex refused (HTTP 400)
        with what it said: a refused item never stops the others. A batch Plex refuses is
        added item by item. Only the items out of place are moved (one call each); Plex
        keeps the order items were added in, so usually none are."""
        if not keys:
            raise ValueError("a Plex collection needs at least one member")

        def uri(ks: list[str]) -> str:
            return quote(
                f"server://{machine}/com.plexapp.plugins.library/library/metadata/{','.join(ks)}"
            )

        refused: dict[str, str] = {}
        new = ""
        rest: list[str] = []
        for at, first in enumerate(keys):  # created with its first item Plex accepts
            try:
                created = self.send(
                    "POST",
                    f"/library/collections?type=1&title={quote(title)}&smart=0"
                    f"&sectionId={section_key}&uri={uri([first])}",
                )
            except PlexError as e:
                if e.status != 400:
                    raise
                refused[first] = str(e)
                continue
            new = str(created["MediaContainer"]["Metadata"][0]["ratingKey"])
            rest = keys[at + 1 :]
            break
        if not new:
            raise PlexError(f"plex refused every item of {title!r}", 400)
        for i in range(0, len(rest), 100):
            part = rest[i : i + 100]
            try:
                self.send("PUT", f"/library/collections/{new}/items?uri={uri(part)}")
            except PlexError as e:
                if e.status != 400:
                    raise
                for k in part:  # one by one: only the item Plex refuses stays out
                    try:
                        self.send("PUT", f"/library/collections/{new}/items?uri={uri([k])}")
                    except PlexError as e1:
                        if e1.status != 400:
                            raise
                        refused[k] = str(e1)
        placed = [k for k in keys if k not in refused]
        self.send("PUT", f"/library/metadata/{new}/prefs?collectionSort=2")
        got = self.collection_children(new)
        if got != placed:
            keep = in_order(got, placed)
            for i, cur in enumerate(placed):
                if cur in keep:  # in target order: its predecessor is already in place
                    continue
                if i:
                    self.send(
                        "PUT", f"/library/collections/{new}/items/{cur}/move?after={placed[i - 1]}"
                    )
                else:  # to the front; rare, the collection was made with it
                    with contextlib.suppress(PlexError):
                        self.send("PUT", f"/library/collections/{new}/items/{cur}/move")
            if self.collection_children(new) != placed:  # Plex did not follow: every item
                for prev, cur in zip(placed, placed[1:], strict=False):
                    self.send("PUT", f"/library/collections/{new}/items/{cur}/move?after={prev}")
        return new, refused

    def add_label(self, section_key: str, rating_key: str, label: str) -> None:
        """Put a (locked) label on a collection. Plex shows the first letter capitalised
        ("maktaba-auto" reads "Maktaba-auto" in the Plex UI); the stored tag is as given."""
        self.send(
            "PUT",
            f"/library/sections/{section_key}/all?type=18&id={rating_key}"
            f"&label[0].tag.tag={quote(label)}&label.locked=1",
        )

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
