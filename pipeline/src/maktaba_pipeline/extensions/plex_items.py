"""``plex_items`` access shared by the built-in extensions (slot 0092).

The Plex extension's ``plex_index`` stage saves Plex's record of every item of a
library (one listing per section). ``plex_collection`` resolves season members
from it and ``dizquetv_show`` builds its programmes from it, so neither asks Plex
item by item; only what the index lacks goes to the network.
"""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from typing import Any

from ..engine.sql import dumps, loads, now, to_ts, uid

__all__ = [
    "DETAIL_KEYS",
    "IndexedItem",
    "detail",
    "details_for",
    "items_for_videos",
    "replace_section",
]

_CHUNK = 400

#: the item fields kept in ``plex_items.detail``: what a dizqueTV programme is built from
DETAIL_KEYS = (
    "ratingKey",
    "key",
    "type",
    "title",
    "summary",
    "duration",
    "thumb",
    "year",
    "originallyAvailableAt",
    "contentRating",
    "grandparentTitle",
    "grandparentThumb",
    "parentIndex",
    "index",
    "addedAt",
    "updatedAt",
    "librarySectionID",
)


@dataclass(frozen=True, slots=True)
class IndexedItem:
    """One saved item, as ``plex_index`` wrote it."""

    rating_key: str
    section_key: str
    file: str | None
    duration_ms: int | None
    video_id: str | None


def detail(m: Mapping[str, Any]) -> dict[str, Any]:
    """The part of a Plex item later stages use (first media part only)."""
    d = {k: m[k] for k in DETAIL_KEYS if k in m}
    try:
        md = m["Media"][0]
        part = md["Part"][0]
        d["Media"] = [
            {
                "width": md.get("width"),
                "height": md.get("height"),
                "Part": [{"key": part.get("key"), "file": part.get("file")}],
            }
        ]
    except (KeyError, IndexError, TypeError):
        pass
    return d


def _file_of(m: Mapping[str, Any]) -> str | None:
    try:
        return str(m["Media"][0]["Part"][0]["file"])
    except (KeyError, IndexError, TypeError):
        return None


async def replace_section(
    db: Any,
    *,
    server: str,
    section_key: str,
    section_title: str,
    items: Sequence[Mapping[str, Any]],
    library_of: Mapping[str, str | None],
    video_of: Mapping[str, str | None],
) -> int:
    """Replace one section's saved items with a fresh listing (items Plex dropped
    are removed). ``library_of`` / ``video_of`` map a rating key to the Maktaba
    library / video it belongs to. Returns how many rows were written."""
    stamp = to_ts(db, now())
    async with db.transaction():
        await db.execute(
            "DELETE FROM plex_items WHERE server = $1 AND section_key = $2", server, section_key
        )
        for m in items:
            rk = str(m["ratingKey"])
            await db.execute(
                "INSERT INTO plex_items (server, rating_key, section_key, section_title,"
                " library_id, video_id, file, duration_ms, plex_updated_at, detail, seen_at)"
                " VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)",
                server,
                rk,
                section_key,
                section_title,
                uid(library_of.get(rk)),
                uid(video_of.get(rk)),
                _file_of(m),
                int(m["duration"]) if m.get("duration") else None,
                int(m["updatedAt"]) if m.get("updatedAt") else None,
                dumps(detail(m)),
                stamp,
            )
    return len(items)


async def details_for(db: Any, keys: Sequence[str]) -> dict[str, dict[str, Any]]:
    """Saved Plex records for these rating keys (any server), keyed by rating key."""
    out: dict[str, dict[str, Any]] = {}
    ids = list(dict.fromkeys(str(k) for k in keys))
    for i in range(0, len(ids), _CHUNK):
        chunk = ids[i : i + _CHUNK]
        marks = ",".join(f"${n + 1}" for n in range(len(chunk)))
        rows = await db.fetch(
            f"SELECT rating_key, detail FROM plex_items WHERE rating_key IN ({marks})",  # noqa: S608
            *chunk,
        )
        out.update({str(r["rating_key"]): dict(loads(r["detail"]) or {}) for r in rows})
    return out


async def items_for_videos(db: Any, video_ids: Sequence[str]) -> dict[str, IndexedItem]:
    """The saved Plex item of each video (by ``plex_items.video_id``)."""
    out: dict[str, IndexedItem] = {}
    ids = list(dict.fromkeys(str(v) for v in video_ids))
    for i in range(0, len(ids), _CHUNK):
        chunk = ids[i : i + _CHUNK]
        marks = ",".join(f"${n + 1}" for n in range(len(chunk)))
        rows = await db.fetch(
            "SELECT rating_key, section_key, file, duration_ms, video_id FROM plex_items"
            f" WHERE video_id IN ({marks})",  # noqa: S608
            *chunk,
        )
        for r in rows:
            vid = str(r["video_id"])
            out[vid] = IndexedItem(
                str(r["rating_key"]),
                str(r["section_key"]),
                None if r["file"] is None else str(r["file"]),
                None if r["duration_ms"] is None else int(r["duration_ms"]),
                vid,
            )
    return out
