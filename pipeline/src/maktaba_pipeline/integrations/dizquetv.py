"""dizqueTV HTTP client (ported from ``export_dizquetv.py`` and ``dizquetv_shows.py``).

Only the ``dizquetv`` extension imports this module. Synchronous, behind the same
injectable :class:`~maktaba_pipeline.integrations.plex.Transport` as the Plex client.
"""

from __future__ import annotations

import json
import re
import urllib.parse
import uuid
from collections.abc import Mapping
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any

from .plex import Transport, TransportError, UrllibTransport

__all__ = [
    "DOOR_BASE",
    "PREFIX",
    "DizqueError",
    "DizqueTV",
    "DoorDizqueTV",
    "State",
    "image_upload_body",
    "program_from_plex",
    "program_spec",
    "read_state",
    "tz_offset_minutes",
]

#: name prefix of every show / filler list this tool creates in dizqueTV
PREFIX = "maktaba-plan · "
DEFAULT_TIMEOUT = 120.0


class DizqueError(Exception):
    """dizqueTV answered with an error status, or could not be reached."""

    def __init__(self, message: str, status: int | None = None) -> None:
        super().__init__(message)
        self.status = status


def image_upload_body(data: bytes, name: str) -> tuple[str, bytes]:
    """``(Content-Type, body)`` of the multipart form ``POST /api/upload/image`` takes
    (field ``image``)."""
    boundary = uuid.uuid4().hex
    ctype = "image/gif" if name.lower().endswith(".gif") else "image/png"
    head = (
        f'--{boundary}\r\nContent-Disposition: form-data; name="image"; filename="{name}"\r\n'
        f"Content-Type: {ctype}\r\n\r\n"
    ).encode()
    tail = f"\r\n--{boundary}--\r\n".encode()
    return f"multipart/form-data; boundary={boundary}", head + data + tail


class DizqueTV:
    """dizqueTV REST API (``/api/...``)."""

    def __init__(
        self, base: str, *, transport: Transport | None = None, timeout: float = DEFAULT_TIMEOUT
    ) -> None:
        self.base = base.rstrip("/")
        self._transport: Transport = transport or UrllibTransport()
        self.timeout = timeout

    def call(self, method: str, path: str, body: Any = None) -> Any:
        data = json.dumps(body, ensure_ascii=False).encode() if body is not None else None
        headers = {"Accept": "application/json"}
        if data is not None:
            headers["Content-Type"] = "application/json"
        url = self.base + urllib.parse.quote(path, safe="/:?=&")
        try:
            status, raw = self._transport.request(method, url, headers, data, self.timeout)
        except TransportError as exc:
            raise DizqueError(f"dizqueTV unreachable: {exc}") from exc
        if status >= 400:
            raise DizqueError(f"dizqueTV {method} {path} -> HTTP {status}", status)
        return json.loads(raw) if raw else None

    def get(self, path: str) -> Any:
        return self.call("GET", path)

    def upload_image(self, data: bytes, name: str) -> str:
        """Upload a picture (``POST /api/upload/image``); the URL dizqueTV serves it at."""
        ctype, body = image_upload_body(data, name)
        try:
            status, raw = self._transport.request(
                "POST",
                self.base + "/api/upload/image",
                {"Content-Type": ctype, "Accept": "application/json"},
                body,
                self.timeout,
            )
        except TransportError as exc:
            raise DizqueError(f"dizqueTV unreachable: {exc}") from exc
        if status >= 400:
            raise DizqueError(f"dizqueTV POST /api/upload/image -> HTTP {status}", status)
        res = json.loads(raw) if raw else {}
        if not res.get("status"):
            raise DizqueError(f"upload {name}: {res.get('message')}")
        return str(res["data"]["fileUrl"])

    def head_status(self, url: str) -> int:
        """HTTP status of a ``HEAD`` request to any URL (0 when unreachable)."""
        try:
            return self._transport.request("HEAD", url, {}, None, 30.0)[0]
        except TransportError:
            return 0

    def ping(self) -> None:
        """Raise unless the server lists its channels."""
        self.get("/api/channels")

    # ------------------------------------------------------------------ programmes

    def programme(
        self, meta: Mapping[str, Any], server: str, plex: Any
    ) -> dict[str, Any] | None:
        """The programme a show or filler list is written with: built here from the Plex
        record, with the Plex client's address and token (the worker's own client)."""
        return program_from_plex(meta, server, plex.uri, plex.token)

    def can_build(self, plex_settings: Mapping[str, str]) -> bool:
        """Whether programmes can be built: the Plex settings hold the address and token."""
        return bool(plex_settings.get("url")) and bool(plex_settings.get("token"))

    def plex_server_name(self, plex_uri: str | None = None) -> str | None:
        """The name dizqueTV registered the Plex server at ``plex_uri`` under (programmes
        name their server by it). Without a match, the only registered server; None when
        there is none, or several and none at ``plex_uri``."""
        servers = list(self.get("/api/plex-servers") or [])  # never kept: they hold tokens
        if plex_uri:
            want = plex_uri.rstrip("/").lower()
            for s in servers:
                if str(s.get("uri", "")).rstrip("/").lower() == want:
                    return str(s["name"])
        return str(servers[0]["name"]) if len(servers) == 1 else None


#: what a plugin's dizqueTV client has for an address: the door knows the real one
DOOR_BASE = "service://dizquetv"

#: the raw reads the door answers with a token-free projection instead (issue #827)
_DOOR_READ = re.compile(r"^/api/(channels|shows|fillers|plex-servers|(show|filler)/[^/]+|channel/(programless/)?[0-9]+)$")
#: the writes whose programmes the door builds or fills on the home
_DOOR_WRITE = re.compile(r"^/api/((show|filler)(/[^/]+)?|channel)$")


class DoorDizqueTV(DizqueTV):
    """dizqueTV through the home's service door, for a first-party plugin's stage (issue
    #827). The plugin never holds the Plex token, and no answer it gets carries it:

    * a read of a show, filler list, channel or the Plex servers is the door's projection
      (``/maktaba/...``): rating keys, durations and settings, never a programme's picture
      or a server's token;
    * a programme is written as its token-free Plex record (:func:`program_spec`); the door
      checks it and builds the programme, the picture's URL with the token included;
    * a channel's programmes are sent as the projections they were read as; the door fills
      each from the stored show (or, by ``maktabaRef``, from the channel as stored).

    Everything else (the time-slot tool, a logo upload, a delete) goes as sent, through the
    door's allow-list."""

    def __init__(self, transport: Transport, *, timeout: float = DEFAULT_TIMEOUT) -> None:
        super().__init__(DOOR_BASE, transport=transport, timeout=timeout)

    def call(self, method: str, path: str, body: Any = None) -> Any:
        if (method == "GET" and _DOOR_READ.match(path)) or (
            method in ("PUT", "POST") and _DOOR_WRITE.match(path)
        ):
            path = "/maktaba/" + path.removeprefix("/api/")
        return super().call(method, path, body)

    def programme(
        self, meta: Mapping[str, Any], server: str, plex: Any
    ) -> dict[str, Any] | None:
        """The token-free record: the door builds the programme from it on the home."""
        return program_spec(meta)

    def can_build(self, plex_settings: Mapping[str, str]) -> bool:
        """The door holds the Plex settings (it refuses a write when they are missing)."""
        return True

    def plex_server_name(self, plex_uri: str | None = None) -> str | None:
        """As :meth:`DizqueTV.plex_server_name`, from the door's projection: each server's
        name, and whether it is the home's own Plex (its address is never shown)."""
        servers = list(self.get("/api/plex-servers") or [])
        if plex_uri:
            for s in servers:
                if s.get("home"):
                    return str(s["name"])
        return str(servers[0]["name"]) if len(servers) == 1 else None

    def head_status(self, url: str) -> int:
        """A logo's status on its web host: the door checks only a link under the home's own
        ``dz_art_url`` (0 when unreachable or refused)."""
        target = f"{self.base}/maktaba/art?url={urllib.parse.quote(url, safe='')}"
        try:
            status = self._transport.request("HEAD", target, {}, None, 30.0)[0]
        except TransportError:
            return 0
        return 0 if status == 403 else status


@dataclass(slots=True)
class State:
    """What dizqueTV holds now (empty when offline)."""

    #: channel number -> programless channel document
    channels: dict[int, dict[str, Any]] = field(default_factory=dict)
    #: show name -> ``{"id", "keys"}``; ``keys`` is None for shows not created by this tool
    shows: dict[str, dict[str, Any]] = field(default_factory=dict)
    fillers: dict[str, dict[str, Any]] = field(default_factory=dict)
    server: str | None = None


def read_state(dz: DizqueTV) -> State:
    """Read every channel, tool-owned show and tool-owned filler list (GET only)."""
    st = State()
    for n in dz.get("/api/channels"):
        num = int(n["number"] if isinstance(n, dict) else n)
        st.channels[num] = dz.get(f"/api/channel/programless/{num}")
    for kind, bucket in (("show", st.shows), ("filler", st.fillers)):
        for info in dz.get(f"/api/{kind}s"):
            if str(info.get("name", "")).startswith(PREFIX):
                doc = dz.get(f"/api/{kind}/{info['id']}")
                keys = [str(p.get("ratingKey")) for p in doc.get("content", [])]
                bucket[info["name"]] = {"id": info["id"], "keys": keys}
            else:
                bucket.setdefault(info["name"], {"id": info["id"], "keys": None})
    st.server = dz.plex_server_name()  # never the token
    return st


def tz_offset_minutes() -> int:
    """Like JavaScript ``Date.getTimezoneOffset`` (UTC minus local, minutes)."""
    off = datetime.now().astimezone().utcoffset()
    return -int(off.total_seconds() // 60) if off is not None else 0


def _hms(ms: int) -> str:
    s = ms // 1000
    return f"{s // 3600:02d}:{s // 60 % 60:02d}:{s % 60:02d}.{ms % 1000 // 100}"


def program_spec(meta: Mapping[str, Any]) -> dict[str, Any] | None:
    """A programme's token-free record from a Plex ``/library/metadata/RK`` item: the
    programme without what names Plex's address or token (``icon`` is the picture's Plex
    path, there is no ``serverKey``). The home's service door builds the programme from it
    for a plugin (issue #827); :func:`program_from_plex` does in the worker.

    None for items that cannot be played (no duration, not a movie or episode).
    """
    if not meta.get("duration") or meta.get("type") not in ("movie", "episode"):
        return None
    part = meta["Media"][0]["Part"][0]
    date = meta.get("originallyAvailableAt") or (
        f"{meta['year']}-01-01" if meta.get("year") else None
    )
    p: dict[str, Any] = {
        "title": meta.get("title", ""),
        "key": meta["key"],
        "ratingKey": str(meta["ratingKey"]),
        "icon": meta.get("thumb") or None,
        "type": meta["type"],
        "duration": int(meta["duration"]),
        "durationStr": _hms(int(meta["duration"])),
        "summary": meta.get("summary", ""),
        "rating": meta.get("contentRating"),
        "date": date,
        "year": meta.get("year"),
        "plexFile": part["key"],
        "file": part["file"],
    }
    if meta["type"] == "episode":
        p.update(
            showTitle=meta.get("grandparentTitle"),
            season=meta.get("parentIndex"),
            episode=meta.get("index"),
            icon=meta.get("grandparentThumb") or None,
        )
    else:
        p.update(showTitle=meta.get("title", ""), season=1, episode=1)
    p["commercials"] = []
    return p


def program_from_plex(
    meta: Mapping[str, Any], server_name: str, plex_uri: str, plex_token: str
) -> dict[str, Any] | None:
    """A dizqueTV programme from a Plex ``/library/metadata/RK`` item.

    The shape the dizqueTV web UI saves (``tools/fill-custom-shows.js``): the record of
    :func:`program_spec` with the picture's URL (Plex's address and token) and the server's
    name. Returns None for items that cannot be played (no duration, not a movie or episode).
    """
    p = program_spec(meta)
    if p is None:
        return None
    t = p["icon"]
    p["icon"] = f"{plex_uri}{t}?X-Plex-Token={plex_token}" if t else None
    del p["commercials"]
    p["serverKey"] = server_name
    p["commercials"] = []
    return p
