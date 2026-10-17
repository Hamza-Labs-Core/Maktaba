"""scan-plex: attach Plex library items (ratingKey, library, title) to files.

Plex reports server-side paths (``/volume1/...``); ``path_map`` rewrites
them to the local view of the same share (``Y:/...``) so rows join on
``files.path``.
"""

from __future__ import annotations

import json
import sqlite3
import time
import urllib.error
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
        return self.send("GET", path)

    def send(self, method: str, path: str) -> dict[str, Any]:
        """One Plex call. A connection that fails (Plex restarting, a network blip) is
        retried with backoff (5 tries, up to 2 min apart); Plex's own answers (404 …) are
        raised at once."""
        sep = "&" if "?" in path else "?"
        req = urllib.request.Request(
            f"{self.uri}{path}{sep}X-Plex-Token={self.token}",
            method=method,
            headers={"Accept": "application/json"},
        )
        for attempt, wait in enumerate((0, 10, 30, 60, 120)):
            if wait:
                print(f"     (Plex not answering; retry {attempt}/4 in {wait} s)", flush=True)
                time.sleep(wait)
            try:
                with urllib.request.urlopen(req, timeout=120) as r:  # noqa: S310
                    body = r.read()
                return json.loads(body) if body else {}
            except urllib.error.HTTPError as e:
                said = e.read().decode("utf-8", "replace").strip()[:300] if e.fp else ""
                raise PlexError(method, path, e.code, said) from None
            except (urllib.error.URLError, TimeoutError, ConnectionError) as e:
                last = e
        raise last


class PlexError(RuntimeError):
    """Plex answered a call with an error (400, 404 …): the call, the status and what Plex
    said. Never carries the token."""

    def __init__(self, method: str, path: str, status: int, said: str) -> None:
        super().__init__(f"Plex refused {method} {path.split('?')[0]}: {status} {said}".strip())
        self.status = status
        self.said = said


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


#: the item fields kept in ``plex_items.detail``: what a dizqueTV programme is built from
DETAIL_KEYS = (
    "ratingKey", "key", "type", "title", "summary", "duration", "thumb", "year",
    "originallyAvailableAt", "contentRating", "grandparentTitle", "grandparentThumb",
    "parentIndex", "index", "addedAt", "updatedAt",
)  # fmt: skip


def detail(m: dict[str, Any]) -> dict[str, Any]:
    """The part of a Plex item later steps use (first media part only)."""
    d = {k: m[k] for k in DETAIL_KEYS if k in m}
    try:
        md, part = m["Media"][0], m["Media"][0]["Part"][0]
        d["Media"] = [{"width": md.get("width"), "height": md.get("height"),
                       "Part": [{"key": part.get("key"), "file": part.get("file")}]}]  # fmt: skip
    except (KeyError, IndexError):
        pass
    return d


def scan_plex(
    conn: sqlite3.Connection,
    server: PlexServer,
    path_map: list[tuple[str, str]],
    library: str | None,
) -> None:
    """plex-index: one listing per library. Saves every item's record (``plex_items``,
    items Plex dropped are removed) and attaches Plex keys and lengths to files."""
    sections = [
        s
        for s in server.get("/library/sections")["MediaContainer"].get("Directory", [])
        if s.get("type") in ("movie", "show")
        and not (library and library.lower() not in s["title"].lower())
    ]
    print(f"reading {len(sections)} Plex libraries", flush=True)
    for n_sec, sec in enumerate(sections, 1):
        ptype = 1 if sec["type"] == "movie" else 4
        items = server.get(f"/library/sections/{sec['key']}/all?type={ptype}")[
            "MediaContainer"
        ].get("Metadata", [])
        matched = 0
        seen: list[tuple[str, str, str, str | None, int | None, str]] = []
        for m in items:
            try:
                plex_file = m["Media"][0]["Part"][0]["file"]
            except (KeyError, IndexError):
                plex_file = None
            local = map_path(plex_file, path_map) if plex_file else None
            seen.append((str(m["ratingKey"]), str(sec["key"]), sec["title"], local,
                         m.get("updatedAt"), json.dumps(detail(m), ensure_ascii=False)))  # fmt: skip
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
                       WHERE path=? AND (duration_s IS NULL OR width IS NULL
                         OR probed_size IS NOT size OR probed_mtime IS NOT mtime)""",
                    (m["duration"] / 1000, md.get("width"), md.get("height"), local),
                )
                from . import jobs

                jobs.ensure(conn)
                frow = conn.execute(
                    "SELECT size, mtime FROM files WHERE path=? AND probed_size IS size "
                    "AND probed_mtime IS mtime",
                    (local,),
                ).fetchone()
                if frow:  # measured for this file state: the probe job is done without ffprobe
                    jobs.record(conn, local, "probe", 1, frow[0], frow[1])
        conn.execute("DELETE FROM plex_items WHERE section=?", (str(sec["key"]),))
        conn.executemany("INSERT OR REPLACE INTO plex_items VALUES (?,?,?,?,?,?)", seen)
        conn.commit()
        print(f"  [{n_sec}/{len(sections)}] {sec['title']}: {len(items)} items, "
              f"{matched} matched to scanned files", flush=True)  # fmt: skip


def cached_items(conn: sqlite3.Connection, keys: list[str]) -> dict[str, dict[str, Any]]:
    """Saved Plex records (``plex_items``) for these rating keys."""
    out: dict[str, dict[str, Any]] = {}
    for i in range(0, len(keys), 500):
        part = keys[i : i + 500]
        for k, d in conn.execute(
            f"SELECT rating_key, detail FROM plex_items WHERE rating_key IN ({','.join('?' * len(part))})",  # noqa: S608
            part,
        ):
            out[k] = json.loads(d)
    return out


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
