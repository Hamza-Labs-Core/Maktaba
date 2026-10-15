"""scan-plex: attach Plex library items (ratingKey, library, title) to files.

Plex reports server-side paths (``/volume1/...``); ``path_map`` rewrites
them to the local view of the same share (``Y:/...``) so rows join on
``files.path``.
"""

from __future__ import annotations

import json
import sqlite3
import urllib.parse
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any


@dataclass(frozen=True)
class PlexServer:
    name: str
    uri: str
    token: str

    def get(self, path: str) -> dict[str, Any]:
        sep = "&" if "?" in path else "?"
        req = urllib.request.Request(
            f"{self.uri}{path}{sep}X-Plex-Token={self.token}",
            headers={"Accept": "application/json"},
        )
        with urllib.request.urlopen(req, timeout=120) as r:  # noqa: S310 — user-configured server
            body = r.read()
        return json.loads(body) if body else {}

    def send(self, method: str, path: str) -> dict[str, Any]:
        sep = "&" if "?" in path else "?"
        req = urllib.request.Request(
            f"{self.uri}{path}{sep}X-Plex-Token={self.token}",
            method=method,
            headers={"Accept": "application/json"},
        )
        with urllib.request.urlopen(req, timeout=120) as r:  # noqa: S310
            body = r.read()
        return json.loads(body) if body else {}


def load_server(dizquetv_db: Path | None, uri: str | None, token: str | None) -> PlexServer:
    if uri and token:
        return PlexServer("plex", uri.rstrip("/"), token)
    if dizquetv_db is None:
        raise SystemExit(
            "give --plex-url and --plex-token, or --dizquetv-db pointing at a .dizquetv folder"
        )
    servers = json.loads((dizquetv_db / "plex-servers.json").read_text(encoding="utf-8"))
    s = sorted(servers, key=lambda x: x.get("index", 0))[0]
    return PlexServer(s["name"], s["uri"].rstrip("/"), s["accessToken"])


def map_path(plex_path: str, path_map: list[tuple[str, str]]) -> str | None:
    for src, dst in path_map:
        if plex_path.startswith(src):
            return dst.rstrip("/") + "/" + plex_path[len(src) :].lstrip("/")
    return None


def scan_plex(
    conn: sqlite3.Connection,
    server: PlexServer,
    path_map: list[tuple[str, str]],
    library: str | None,
) -> None:
    sections = server.get("/library/sections")["MediaContainer"].get("Directory", [])
    for sec in sections:
        if sec.get("type") not in ("movie", "show"):
            continue
        if library and library.lower() not in sec["title"].lower():
            continue
        ptype = 1 if sec["type"] == "movie" else 4
        items = server.get(f"/library/sections/{sec['key']}/all?type={ptype}")[
            "MediaContainer"
        ].get("Metadata", [])
        matched = 0
        for m in items:
            try:
                plex_file = m["Media"][0]["Part"][0]["file"]
            except (KeyError, IndexError):
                continue
            local = map_path(plex_file, path_map)
            if local is None:
                continue
            cur = conn.execute(
                "UPDATE files SET plex_rating_key=?, plex_section=?, plex_library=?, plex_title=? WHERE path=?",
                (m["ratingKey"], str(sec["key"]), sec["title"], m.get("title"), local),
            )
            matched += cur.rowcount
            # Plex already measured the file: saves an ffprobe over the network share
            md = m["Media"][0]
            if m.get("duration") and md.get("width"):
                conn.execute(
                    """UPDATE files SET duration_s=?, width=?, height=?,
                         probed_size=size, probed_mtime=mtime
                       WHERE path=? AND (duration_s IS NULL OR width IS NULL)""",
                    (m["duration"] / 1000, md.get("width"), md.get("height"), local),
                )
                from . import jobs

                jobs.ensure(conn)
                frow = conn.execute(
                    "SELECT size, mtime FROM files WHERE path=?", (local,)
                ).fetchone()
                if frow:  # Plex measured it: the probe job is done without ffprobe
                    jobs.record(conn, local, "probe", 1, frow[0], frow[1])
        conn.commit()
        print(f"{sec['title']}: {len(items)} items, {matched} matched to scanned files")


def quote(s: str) -> str:
    return urllib.parse.quote(s, safe="")


def load_path_map(db_path: str | Path) -> list[tuple[str, str]]:
    """Plex path prefix → local prefix pairs from ``mediaintel.json`` next to the cache
    (``{"path_map": [["/volume1/share/", "Y:/"], …]}``): the mapping is per home, so
    it lives with the cache, not in the code."""
    cfg = Path(db_path).with_name("mediaintel.json")
    if not cfg.exists():
        return []
    data = json.loads(cfg.read_text(encoding="utf-8"))
    return [(str(a), str(b)) for a, b in data.get("path_map", [])]


def plex_roots(
    server: PlexServer,
    path_map: list[tuple[str, str]],
    *,
    library: str | None = None,
    exclude: tuple[str, ...] = (),
    types: tuple[str, ...] = ("show", "movie"),
) -> list[tuple[str, Path]]:
    """(library title, local folder) for every Plex library location the path map reaches."""
    out: list[tuple[str, Path]] = []
    for sec in server.get("/library/sections")["MediaContainer"].get("Directory", []):
        title = sec.get("title", "")
        if sec.get("type") not in types:
            continue
        if library and library.lower() not in title.lower():
            continue
        if any(x.lower() == title.lower() for x in exclude):
            continue
        for loc in sec.get("Location", []):
            local = map_path(loc["path"].rstrip("/") + "/", path_map)
            if local is None:
                print(f"  {title}: {loc['path']} is outside the path map — skipped")
                continue
            out.append((title, Path(local)))
    return out
