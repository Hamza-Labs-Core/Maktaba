"""Season data the Plex and dizqueTV extensions export: title, ordered members, hash."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any
from uuid import UUID

from ..engine.sql import loads, uid

__all__ = ["Member", "SeasonInfo", "collection_title", "load_members", "load_season"]


@dataclass(frozen=True, slots=True)
class SeasonInfo:
    id: UUID
    library: str
    series: str
    season: int
    season_count: int
    kind: str
    membership_hash: str
    retired: bool
    #: a named season (a talk show's guest, "وليد جنبلاط"), from the detector
    season_name: str | None = None

    @property
    def title(self) -> str:
        return collection_title(
            self.library, self.series, self.season, self.season_count, self.season_name
        )


@dataclass(frozen=True, slots=True)
class Member:
    video_id: UUID
    path: str
    title: str


def collection_title(
    library: str, series: str, season: int, season_count: int, season_name: str | None = None
) -> str:
    """``<library> - <series>`` for a one-season series, ``... - الموسم N`` otherwise.

    A named season of a series with several (a talk show's guest) is
    ``<library> - <series> - <name>``; an ongoing show's season is its year
    (``<library> - <series> - 2016``). Same rule as mediaintel's ``export_plex._title``.
    """
    base = f"{library} - {series}"
    if season_name and season_count > 1:
        return f"{base} - {season_name}"
    if season >= 1900:
        return f"{base} - {season}"
    return base if season_count == 1 else f"{base} - الموسم {season}"


async def load_season(db: Any, season_id: UUID | str) -> SeasonInfo | None:
    row = await db.fetchrow(
        "SELECT ss.id AS id, ss.season AS season, ss.kind AS kind,"
        " ss.membership_hash AS membership_hash, ss.retired_at AS retired_at,"
        " ss.metadata AS metadata,"
        " COALESCE(s.name_override, s.name) AS series, COALESCE(l.name, '') AS library,"
        " (SELECT count(*) FROM series_seasons x WHERE x.series_id = ss.series_id"
        "   AND x.retired_at IS NULL) AS n"
        " FROM series_seasons ss JOIN series s ON s.id = ss.series_id"
        " LEFT JOIN libraries l ON l.id = ss.library_id WHERE ss.id = $1",
        uid(season_id),
    )
    if row is None:
        return None
    return SeasonInfo(
        UUID(str(row["id"])),
        str(row["library"]),
        str(row["series"]),
        int(row["season"]),
        max(1, int(row["n"])),
        str(row["kind"]),
        str(row["membership_hash"]),
        row["retired_at"] is not None,
        (loads(row["metadata"]) or {}).get("season_name") or None,
    )


async def load_members(db: Any, season_id: UUID | str) -> list[Member]:
    """The season's episodes in episode order (overrides win)."""
    rows = await db.fetch(
        "SELECT v.id AS id, v.path AS path, COALESCE(v.title, v.filename) AS title"
        " FROM series_episodes e"
        " JOIN series_seasons ss ON ss.series_id = e.series_id"
        "  AND ss.season = COALESCE(e.season_override, e.season)"
        " JOIN videos v ON v.id = e.video_id"
        " WHERE ss.id = $1 AND v.deleted_at IS NULL"
        " ORDER BY (COALESCE(e.episode_override, e.episode) IS NULL),"
        " COALESCE(e.episode_override, e.episode), e.absolute_number, v.id",
        uid(season_id),
    )
    return [Member(UUID(str(r["id"])), str(r["path"]), str(r["title"])) for r in rows]
