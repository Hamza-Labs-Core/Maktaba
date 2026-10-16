"""Plex HTTP client (ported from ``scripts/mediaintel/scan_plex.py`` and ``export_plex.py``).

Only the ``plex`` and ``dizquetv`` extensions import this module. The client is
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
from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any, Protocol

__all__ = [
    "PlexClient",
    "PlexCollection",
    "PlexError",
    "PlexItem",
    "PlexSection",
    "Transport",
    "TransportError",
    "UrllibTransport",
    "quote",
]

DEFAULT_TIMEOUT = 120.0
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

    def section_items(self, section: PlexSection) -> list[PlexItem]:
        """Every playable item of a section that has a media file."""
        ptype = _LIST_TYPE.get(section.type, 1)
        items = self._container(f"/library/sections/{section.key}/all?type={ptype}")
        out: list[PlexItem] = []
        for m in items.get("Metadata", []):
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
            )
            for m in md
        ]

    def collection_children(self, rating_key: str) -> list[str]:
        md = self._container(f"/library/collections/{rating_key}/children").get("Metadata", [])
        return [str(m["ratingKey"]) for m in md]

    # ------------------------------------------------------------------ writes

    def delete_metadata(self, rating_key: str) -> None:
        self.send("DELETE", f"/library/metadata/{rating_key}")

    def create_collection(self, machine: str, section_key: str, title: str, keys: list[str]) -> str:
        """Create a manual collection holding ``keys`` in order; returns its rating key.

        Plex stores manual-collection membership as a tag named after the
        collection, so a same-named collection must be deleted *before* this is
        called, never after.
        """
        if not keys:
            raise ValueError("a Plex collection needs at least one member")

        def uri(ks: list[str]) -> str:
            return quote(
                f"server://{machine}/com.plexapp.plugins.library/library/metadata/{','.join(ks)}"
            )

        created = self.send(
            "POST",
            f"/library/collections?type=1&title={quote(title)}&smart=0"
            f"&sectionId={section_key}&uri={uri(keys[:1])}",
        )
        new = str(created["MediaContainer"]["Metadata"][0]["ratingKey"])
        for i in range(1, len(keys), 100):
            self.send("PUT", f"/library/collections/{new}/items?uri={uri(keys[i : i + 100])}")
        self.send("PUT", f"/library/metadata/{new}/prefs?collectionSort=2")
        for prev, cur in zip(keys, keys[1:], strict=False):
            self.send("PUT", f"/library/collections/{new}/items/{cur}/move?after={prev}")
        return new
