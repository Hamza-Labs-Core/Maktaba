"""Subject helpers: labels and the subject mapping rules of plan 06-11 section 3.3."""

from __future__ import annotations

from typing import Any
from uuid import UUID

from .graph import CATALOG_ID, Subject, SubjectKind
from .sql import uid

__all__ = [
    "label_of",
    "library_of_video",
    "season_of_video",
    "seasons_of_series",
    "subject_for_row",
    "video_ids_of_library",
    "video_ids_of_season",
]


async def label_of(db: Any, kind: str, subject_id: UUID | str) -> str:
    """Human label for a subject (video title, season name, library name, Catalog)."""
    sid = uid(subject_id)
    if kind == "video":
        row = await db.fetchrow(
            "SELECT COALESCE(title, filename) AS l FROM videos WHERE id = $1", sid
        )
    elif kind == "library":
        row = await db.fetchrow("SELECT name AS l FROM libraries WHERE id = $1", sid)
    elif kind == "season":
        row = await db.fetchrow(
            "SELECT CASE WHEN kind = 'ongoing' AND first_air IS NOT NULL"
            " THEN name || ' · ' || substr(CAST(first_air AS TEXT), 1, 4)"
            " ELSE name || ' · S' || season END AS l FROM series_seasons WHERE id = $1",
            sid,
        )
    elif kind == "series":
        row = await db.fetchrow("SELECT name AS l FROM series WHERE id = $1", sid)
    else:
        return "Catalog"
    return str(row["l"]) if row is not None and row["l"] is not None else str(sid)


async def library_of_video(db: Any, video_id: UUID | str) -> UUID | None:
    row = await db.fetchrow("SELECT library_id FROM videos WHERE id = $1", uid(video_id))
    return None if row is None or row["library_id"] is None else UUID(str(row["library_id"]))


async def season_of_video(db: Any, video_id: UUID | str) -> UUID | None:
    """The season a video is placed in (0 or 1 row; retired seasons count as none)."""
    row = await db.fetchrow(
        "SELECT ss.id AS id FROM series_episodes e"
        " JOIN series_seasons ss ON ss.series_id = e.series_id"
        "  AND ss.season = COALESCE(e.season_override, e.season)"
        " WHERE e.video_id = $1 AND ss.retired_at IS NULL",
        uid(video_id),
    )
    return None if row is None else UUID(str(row["id"]))


async def seasons_of_series(db: Any, series_id: UUID | str) -> list[UUID]:
    rows = await db.fetch(
        "SELECT id FROM series_seasons WHERE series_id = $1 AND retired_at IS NULL ORDER BY season",
        uid(series_id),
    )
    return [UUID(str(r["id"])) for r in rows]


async def video_ids_of_library(db: Any, library_id: UUID | str) -> list[UUID]:
    rows = await db.fetch(
        "SELECT id FROM videos WHERE library_id = $1 AND deleted_at IS NULL ORDER BY id",
        uid(library_id),
    )
    return [UUID(str(r["id"])) for r in rows]


async def video_ids_of_season(db: Any, season_id: UUID | str) -> list[UUID]:
    rows = await db.fetch(
        "SELECT e.video_id AS id FROM series_episodes e"
        " JOIN series_seasons ss ON ss.series_id = e.series_id"
        "  AND ss.season = COALESCE(e.season_override, e.season)"
        " WHERE ss.id = $1 ORDER BY e.episode, e.video_id",
        uid(season_id),
    )
    return [UUID(str(r["id"])) for r in rows]


def subject_for_row(row: Any) -> Subject:
    """Build the :class:`Subject` of a ``processing_jobs`` row."""
    kind: SubjectKind = row["subject_kind"]
    sid = row["subject_id"]
    lib = row["library_id"]
    return Subject(
        kind=kind,
        id=CATALOG_ID if kind == "catalog" and sid is None else UUID(str(sid)),
        library_id=None if lib is None else UUID(str(lib)),
    )
