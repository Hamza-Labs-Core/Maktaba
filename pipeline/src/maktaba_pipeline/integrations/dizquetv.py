"""dizqueTV HTTP client (ported from ``export_dizquetv.py`` and ``dizquetv_shows.py``).

Only the ``dizquetv`` extension imports this module. Synchronous, behind the same
injectable :class:`~maktaba_pipeline.integrations.plex.Transport` as the Plex client.
"""

from __future__ import annotations

import json
import urllib.parse
import uuid
from collections.abc import Mapping
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any

from .plex import Transport, TransportError, UrllibTransport

__all__ = [
    "PREFIX",
    "DizqueError",
    "DizqueTV",
    "State",
    "image_upload_body",
    "program_from_plex",
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

    def plex_server_name(self, plex_uri: str | None = None) -> str | None:
        """The name dizqueTV registered the Plex server at ``plex_uri`` under (programmes
        name their server by it). Without a match, the only registered server; None when
        there is none, or several and none at ``plex_uri``."""
        servers = list(self.get("/api/plex-servers") or [])
        if plex_uri:
            want = plex_uri.rstrip("/").lower()
            for s in servers:
                if str(s.get("uri", "")).rstrip("/").lower() == want:
                    return str(s["name"])
        return str(servers[0]["name"]) if len(servers) == 1 else None


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


def program_from_plex(
    meta: Mapping[str, Any], server_name: str, plex_uri: str, plex_token: str
) -> dict[str, Any] | None:
    """A dizqueTV programme from a Plex ``/library/metadata/RK`` item.

    The shape the dizqueTV web UI saves (``tools/fill-custom-shows.js``). Returns
    None for items that cannot be played (no duration, not a movie or episode).
    """
    if not meta.get("duration") or meta.get("type") not in ("movie", "episode"):
        return None
    part = meta["Media"][0]["Part"][0]

    def icon(t: str | None) -> str | None:
        return f"{plex_uri}{t}?X-Plex-Token={plex_token}" if t else None

    date = meta.get("originallyAvailableAt") or (
        f"{meta['year']}-01-01" if meta.get("year") else None
    )
    p: dict[str, Any] = {
        "title": meta.get("title", ""),
        "key": meta["key"],
        "ratingKey": str(meta["ratingKey"]),
        "icon": icon(meta.get("thumb")),
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
            icon=icon(meta.get("grandparentThumb")),
        )
    else:
        p.update(showTitle=meta.get("title", ""), season=1, episode=1)
    p["serverKey"] = server_name
    p["commercials"] = []
    return p
