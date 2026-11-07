"""A library's YouTube listing as the database holds it (videos, playlists and their items),
and its digest: ``youtube_sync``'s output (``tv.maktaba.youtube``, Epic 54 wave 6), which the
mediaintel import computes too, so it lives in the pipeline."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any
from uuid import UUID

from ..engine.hashing import canonical_hash
from ..engine.sql import uid

__all__ = ["Snapshot", "_OWN_CHANNELS", "_snapshot", "changed_youtube_ids"]

_OWN_CHANNELS = "(SELECT channel_id FROM youtube_channels WHERE library_id = $1)"


@dataclass(slots=True)
class Snapshot:
    videos: dict[str, tuple[Any, ...]]
    playlists: dict[str, tuple[Any, ...]]
    items: dict[str, tuple[tuple[str, int | None], ...]]

    def digest(self) -> str:
        return canonical_hash(
            {
                "videos": sorted([k, *v] for k, v in self.videos.items()),
                "playlists": sorted([k, *v] for k, v in self.playlists.items()),
                "items": sorted([k, [list(i) for i in v]] for k, v in self.items.items()),
            }
        )


async def _snapshot(db: Any, library_id: UUID) -> Snapshot:
    lib = uid(library_id)
    vrows = await db.fetch(
        "SELECT youtube_id, channel_id, title, CAST(upload_date AS TEXT) AS d, date_quality,"
        " duration_s, tab FROM youtube_videos"
        f" WHERE channel_id IN {_OWN_CHANNELS} ORDER BY youtube_id",
        lib,
    )
    videos = {
        str(r["youtube_id"]): (
            r["channel_id"],
            r["title"],
            None if r["d"] is None else str(r["d"])[:10],
            r["date_quality"],
            None if r["duration_s"] is None else round(float(r["duration_s"]), 3),
            r["tab"],
        )
        for r in vrows
    }
    prows = await db.fetch(
        "SELECT playlist_id, channel_id, title, item_count, is_real FROM youtube_playlists"
        f" WHERE channel_id IN {_OWN_CHANNELS} ORDER BY playlist_id",
        lib,
    )
    playlists = {
        str(r["playlist_id"]): (
            r["channel_id"],
            r["title"],
            None if r["item_count"] is None else int(r["item_count"]),
            bool(r["is_real"]),
        )
        for r in prows
    }
    irows = await db.fetch(
        "SELECT i.playlist_id AS playlist_id, i.youtube_id AS youtube_id, i.position AS position"
        " FROM youtube_playlist_items i JOIN youtube_playlists p ON p.playlist_id = i.playlist_id"
        f" WHERE p.channel_id IN {_OWN_CHANNELS} ORDER BY i.playlist_id, i.youtube_id",
        lib,
    )
    items: dict[str, list[tuple[str, int | None]]] = {}
    for r in irows:
        items.setdefault(str(r["playlist_id"]), []).append(
            (str(r["youtube_id"]), None if r["position"] is None else int(r["position"]))
        )
    return Snapshot(videos, playlists, {k: tuple(v) for k, v in items.items()})


def changed_youtube_ids(before: Snapshot, after: Snapshot) -> set[str]:
    """YouTube ids whose own row changed, plus every member of a changed playlist."""
    changed = {k for k, v in after.videos.items() if before.videos.get(k) != v}
    for pid in set(before.playlists) | set(after.playlists):
        if before.playlists.get(pid) == after.playlists.get(pid) and before.items.get(
            pid
        ) == after.items.get(pid):
            continue
        for snap in (before, after):
            changed.update(y for y, _ in snap.items.get(pid, ()))
    return changed
