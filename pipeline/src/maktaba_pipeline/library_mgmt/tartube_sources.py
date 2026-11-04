"""Make a library's YouTube sources from Tartube's records (the ``youtube_tartube`` stage).

The home's YouTube downloads before Maktaba were made with Tartube: each of its channels
and playlists downloaded into a folder of its ``name`` (under Tartube's data folder), from
its ``source`` URL. A library whose root folder has that name is that channel's or
playlist's: it gets a source of the URL's kind, **off**, and the ids Tartube downloaded
are seeded ``done`` (:func:`.youtube_sources.seed_done`, as the per-library import does).

It is idempotent: a source the library already has (the same kind and id) is left as it
is, its mode untouched; the seeding never asks for a video twice. Nothing is turned on
and no library is made: a Tartube entry with no library is reported (``not_in_maktaba``),
and so is every entry skipped (no URL, not YouTube, a search page, a URL it cannot read,
a channel whose id YouTube did not give).

A channel named by its handle (``/@name``) is resolved to its ``UC…`` id first from what
the database knows (a source or ``youtube_channels`` row of the same URL), then with one
flat page of yt-dlp (the listing's runner, :func:`..integrations.youtube_download.run_ytdlp`).
"""

from __future__ import annotations

import asyncio
import json
import os
import re
from collections.abc import Awaitable, Callable, Iterable
from dataclasses import dataclass
from typing import Any
from urllib.parse import parse_qs, urlsplit
from uuid import UUID

from ..engine.sql import now, to_ts, uid
from ..integrations import youtube_download as ydl
from . import tartube_import as ti
from . import youtube_sources as ys

__all__ = [
    "Target",
    "classify",
    "import_sources",
    "resolve_channel",
]

#: the report keeps at most this many rows of each list
MAX_ROWS = 500

_CHANNEL_ID = re.compile(r"^UC[A-Za-z0-9_-]{22}$")
_PLAYLIST_ID = re.compile(r"^(?:PL|OL|UU|FL|LL)[A-Za-z0-9_-]{8,64}$")
_HOSTS = frozenset({"youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com"})
_NOT_YOUTUBE_OK = frozenset({"youtu.be"})


@dataclass(frozen=True, slots=True)
class Target:
    """What a Tartube URL is as a source: ``external_id`` None for a channel named by
    its handle (resolved before it is written)."""

    kind: str  # channel | playlist
    url: str
    external_id: str | None


def classify(url: str | None) -> Target | str:
    """The source a Tartube URL makes, or why it makes none: ``no_url``, ``not_youtube``
    (x.com and every other site), ``search`` (a ``results?search_query=`` page) or
    ``unrecognised`` (a YouTube page that is no channel or playlist)."""
    if not url or not url.strip():
        return "no_url"
    raw = url.strip()
    if "://" not in raw:
        raw = "https://" + raw
    try:
        u = urlsplit(raw)
    except ValueError:
        return "unrecognised"
    host = (u.hostname or "").lower()
    if host not in _HOSTS:
        return "unrecognised" if host in _NOT_YOUTUBE_OK else "not_youtube"
    path = u.path or "/"
    if path.startswith("/results"):
        return "search"
    lst = (parse_qs(u.query).get("list") or [""])[0]
    if _PLAYLIST_ID.match(lst):
        return Target("playlist", f"https://www.youtube.com/playlist?list={lst}", lst)
    parts = [p for p in path.split("/") if p]
    if len(parts) >= 2 and parts[0] == "channel" and _CHANNEL_ID.match(parts[1]):
        return Target("channel", f"https://www.youtube.com{path}", parts[1])
    if parts and (parts[0].startswith("@") or (parts[0] in ("c", "user") and len(parts) >= 2)):
        # the tab Tartube downloaded (/videos, /streams) is kept: the listing reads it
        return Target("channel", f"https://www.youtube.com{path.rstrip('/')}", None)
    return "unrecognised"


Resolver = Callable[[str], Awaitable[tuple[str, str | None] | None]]


async def resolve_channel(url: str) -> tuple[str, str | None] | None:
    """A channel page's ``UC…`` id and title with one flat page of yt-dlp, or None."""
    if ydl.unavailable() is not None:
        return None
    res = await ydl.run_ytdlp(
        [*ydl.ytdlp_command(), "--flat-playlist", "-J", "--playlist-end", "1", "--no-warnings",
         "--ignore-config", "--", url],
        timeout=120.0,
    )  # fmt: skip
    if res.returncode != 0:
        return None
    try:
        doc = json.loads("".join(res.stdout))
    except ValueError:
        return None
    if not isinstance(doc, dict):
        return None
    for key in ("channel_id", "id"):
        cid = doc.get(key)
        if isinstance(cid, str) and _CHANNEL_ID.match(cid):
            title = doc.get("channel") or doc.get("uploader")
            return cid, title if isinstance(title, str) and title else None
    return None


def _url_key(url: str) -> str:
    u = url.strip().lower().rstrip("/")
    for tab in ("/videos", "/streams", "/shorts", "/featured", "/playlists"):
        if u.endswith(tab):
            u = u[: -len(tab)]
    return u.replace("://m.", "://www.").replace("://youtube.com", "://www.youtube.com")


def _cap(rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
    return rows[:MAX_ROWS]


async def _libraries(db: Any) -> dict[str, list[tuple[UUID, str]]]:
    """Library roots by their folder's name (:func:`.tartube_import.folder_key`)."""
    out: dict[str, list[tuple[UUID, str]]] = {}
    for r in await db.fetch("SELECT id, name, roots FROM libraries ORDER BY name, id"):
        roots = r["roots"]
        if isinstance(roots, str):
            roots = json.loads(roots)
        for root in roots or []:
            base = os.path.basename(str(root).rstrip("/\\").replace("\\", "/"))
            if base:
                key = ti.folder_key(base)
                lib = (UUID(str(r["id"])), str(r["name"]))
                if lib not in out.setdefault(key, []):
                    out[key].append(lib)
    return out


async def import_sources(
    db: Any,
    path: str,
    *,
    database: str | None = None,
    to_local: Callable[[str], str] = lambda p: p,
    resolve: Resolver | None = None,
) -> dict[str, Any]:
    """Make the sources Tartube's records name (off) and seed what it downloaded.
    ``resolve``: a handle's channel id (default :func:`resolve_channel`)."""
    resolve = resolve or resolve_channel
    srcs, recs = await asyncio.to_thread(ti.read_tartube, path, database=database)
    by_folder = await _libraries(db)
    have: set[tuple[UUID, str, str]] = set()
    known: dict[str, str] = {}
    for r in await db.fetch("SELECT library_id, kind, external_id, url FROM youtube_sources"):
        have.add((UUID(str(r["library_id"])), str(r["kind"]), str(r["external_id"])))
        if r["kind"] == "channel":
            known[_url_key(str(r["url"]))] = str(r["external_id"])
    for r in await db.fetch("SELECT channel_id, url FROM youtube_channels"):
        known.setdefault(_url_key(str(r["url"])), str(r["channel_id"]))

    created: list[dict[str, Any]] = []
    existing: list[dict[str, Any]] = []
    skipped: list[dict[str, Any]] = []
    missing: list[dict[str, Any]] = []
    matched: dict[UUID, set[str]] = {}
    seen: set[str] = set()
    for s in srcs:
        key = ti.folder_key(s.name)
        if key in seen:
            continue
        seen.add(key)
        target = classify(s.url)
        if isinstance(target, str):
            skipped.append({"name": s.name, "url": s.url, "reason": target})
            continue
        libs = by_folder.get(key)
        if not libs:
            missing.append({"name": s.name, "kind": target.kind, "url": target.url})
            continue
        ext = target.external_id
        if ext is None:
            ext = known.get(_url_key(target.url))
            if ext is None and all(_follows(have, lib_id, target.kind) for lib_id, _ in libs):
                # every library of the folder already follows a channel: that one is
                # Tartube's (no network asked to say so), and it is left as it is
                for lib_id, lib_name in libs:
                    matched.setdefault(lib_id, set()).add(key)
                    existing.append({
                        "library_id": str(lib_id), "library": lib_name, "name": s.name,
                        "kind": target.kind, "external_id": None, "url": target.url,
                    })  # fmt: skip
                continue
            if ext is None:
                got = await resolve(target.url)
                if got is not None:
                    ext = got[0]
                    known[_url_key(target.url)] = ext
        if ext is None:
            skipped.append({"name": s.name, "url": target.url, "reason": "unresolved"})
            continue
        for lib_id, lib_name in libs:
            row = {
                "library_id": str(lib_id), "library": lib_name, "name": s.name,
                "kind": target.kind, "external_id": ext, "url": target.url,
            }  # fmt: skip
            matched.setdefault(lib_id, set()).add(key)
            if (lib_id, target.kind, ext) in have:
                existing.append(row)
                continue
            rows = await db.fetch(
                "INSERT INTO youtube_sources (library_id, kind, url, external_id, mode, added_at)"
                " VALUES ($1, $2, $3, $4, 'off', $5)"
                " ON CONFLICT (library_id, kind, external_id) DO NOTHING RETURNING id",
                uid(lib_id), target.kind, target.url, ext, to_ts(db, now()),
            )  # fmt: skip
            have.add((lib_id, target.kind, ext))
            (created if rows else existing).append(row)

    by_parent, rep = await asyncio.to_thread(ti.downloaded_by_parent, recs, to_local)
    seeded: dict[str, int] = {}
    for lib_id, keys in matched.items():
        ids = _unique(i for k in keys for i in by_parent.get(k, []))
        if not ids:
            continue
        linked = await ys.linked_video_ids(db, lib_id)
        seeded[str(lib_id)] = await ys.seed_done(
            db, lib_id, ids, reason="tartube", video_ids={i: linked[i] for i in ids if i in linked}
        )
    return {
        "entries": len(srcs),
        "created": _cap(created),
        "existing": _cap(existing),
        "skipped": _cap(skipped),
        "not_in_maktaba": _cap(missing),
        "counts": {
            "created": len(created),
            "existing": len(existing),
            "skipped": len(skipped),
            "not_in_maktaba": len(missing),
            "seeded": sum(seeded.values()),
            "downloaded": rep.downloaded,
        },  # fmt: skip
        "seeded": seeded,
    }


def _follows(have: set[tuple[UUID, str, str]], lib: UUID, kind: str) -> bool:
    return any(h[0] == lib and h[1] == kind for h in have)


def _unique(ids: Iterable[str]) -> list[str]:
    return list(dict.fromkeys(ids))
