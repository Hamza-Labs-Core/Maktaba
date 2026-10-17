"""Helpers shared by the stage handlers."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any
from uuid import UUID

from ..context import StageFailure
from ..sql import loads, now, to_ts, uid

__all__ = [
    "SeasonFacts",
    "VideoFacts",
    "load_season",
    "load_video",
    "require_video",
    "season_episode_ids",
    "upsert_signals",
]


@dataclass(frozen=True, slots=True)
class VideoFacts:
    id: UUID
    library_id: UUID
    path: str
    filename: str
    title: str | None
    size_bytes: int
    mtime: str
    duration_s: float | None
    poster_path: str | None
    metadata: dict[str, Any]


async def load_video(db: Any, video_id: UUID | str) -> VideoFacts | None:
    row = await db.fetchrow(
        "SELECT id, library_id, path, filename, title, size_bytes, mtime, duration_sec,"
        " poster_path, metadata FROM videos WHERE id = $1",
        uid(video_id),
    )
    if row is None:
        return None
    return VideoFacts(
        id=UUID(str(row["id"])),
        library_id=UUID(str(row["library_id"])),
        path=str(row["path"]),
        filename=str(row["filename"]),
        title=None if row["title"] is None else str(row["title"]),
        size_bytes=int(row["size_bytes"]),
        mtime=str(row["mtime"]),
        duration_s=None if row["duration_sec"] is None else float(row["duration_sec"]),
        poster_path=None if row["poster_path"] is None else str(row["poster_path"]),
        metadata=loads(row["metadata"]) or {},
    )


async def require_video(db: Any, video_id: UUID | str) -> VideoFacts:
    v = await load_video(db, video_id)
    if v is None:
        raise StageFailure("video_missing", f"video {video_id} no longer exists", retryable=False)
    return v


async def upsert_signals(db: Any, video_id: UUID | str, **cols: Any) -> None:
    """Insert or update selected ``video_signals`` columns, leaving the others alone."""
    names = list(cols)
    insert_cols = ", ".join(["video_id", *names, "updated_at"])
    params = ", ".join(f"${i + 1}" for i in range(len(names) + 2))
    updates = ", ".join(f"{c} = EXCLUDED.{c}" for c in [*names, "updated_at"])
    await db.execute(
        f"INSERT INTO video_signals ({insert_cols}) VALUES ({params})"
        f" ON CONFLICT (video_id) DO UPDATE SET {updates}",
        uid(video_id),
        *[cols[c] for c in names],
        to_ts(db, now()),
    )


@dataclass(frozen=True, slots=True)
class SeasonFacts:
    id: UUID
    series_id: UUID
    library_id: UUID | None
    name: str
    kind: str
    number: int
    membership_hash: str
    retired: bool
    median_duration_s: float | None
    metadata: dict[str, Any]


async def load_season(db: Any, season_id: UUID | str) -> SeasonFacts | None:
    row = await db.fetchrow(
        "SELECT id, series_id, library_id, name, kind, season, membership_hash, retired_at,"
        " median_duration_s, metadata FROM series_seasons WHERE id = $1",
        uid(season_id),
    )
    if row is None:
        return None
    return SeasonFacts(
        id=UUID(str(row["id"])),
        series_id=UUID(str(row["series_id"])),
        library_id=None if row["library_id"] is None else UUID(str(row["library_id"])),
        name=str(row["name"]),
        kind=str(row["kind"]),
        number=int(row["season"]),
        membership_hash=str(row["membership_hash"]),
        retired=row["retired_at"] is not None,
        median_duration_s=(
            None if row["median_duration_s"] is None else float(row["median_duration_s"])
        ),
        metadata=loads(row["metadata"]) or {},
    )


async def season_episode_ids(db: Any, season_id: UUID | str) -> list[UUID]:
    """Videos of a season in episode order."""
    rows = await db.fetch(
        "SELECT e.video_id AS id FROM series_episodes e"
        " JOIN series_seasons ss ON ss.series_id = e.series_id"
        "  AND ss.season = COALESCE(e.season_override, e.season)"
        " WHERE ss.id = $1 ORDER BY COALESCE(e.episode_override, e.episode), e.video_id",
        uid(season_id),
    )
    return [UUID(str(r["id"])) for r in rows]
