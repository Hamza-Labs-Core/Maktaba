"""Deterministic ids for detected series and seasons (plan 06-11 section 4.4).

A re-detection of unchanged content must produce the same ids, so jobs, tags,
styles and external links keep pointing at the same season. The ids hash the
library, the detector's folded series key, the series kind and the season number.
"""

from __future__ import annotations

from uuid import UUID, uuid5

__all__ = [
    "NS_CLIP_GROUP",
    "NS_SEASON",
    "NS_SERIES",
    "NS_TOPIC",
    "clip_group_uuid",
    "season_uuid",
    "series_uuid",
    "topic_uuid",
]

NS_SERIES = UUID("6f0f8a52-3b6d-5a41-9f0e-5e5a1c0d7a01")
NS_SEASON = UUID("6f0f8a52-3b6d-5a41-9f0e-5e5a1c0d7a02")
NS_TOPIC = UUID("6f0f8a52-3b6d-5a41-9f0e-5e5a1c0d7a03")
NS_CLIP_GROUP = UUID("6f0f8a52-3b6d-5a41-9f0e-5e5a1c0d7a04")


def series_uuid(library_id: UUID | str, series_key: str, kind: str) -> UUID:
    return uuid5(NS_SERIES, f"{library_id}|{series_key}|{kind}")


def season_uuid(library_id: UUID | str, series_key: str, kind: str, season: int) -> UUID:
    return uuid5(NS_SEASON, f"{library_id}|{series_key}|{kind}|{season}")


def topic_uuid(library_id: UUID | str, playlist_id: str) -> UUID:
    """A topic collection ("ملف"): one per library and curated playlist."""
    return uuid5(NS_TOPIC, f"{library_id}|{playlist_id}")


def clip_group_uuid(library_id: UUID | str, kind: str, basis: str, name: str) -> UUID:
    """A clip / stand-alone group: stable while its kind, basis and name are."""
    return uuid5(NS_CLIP_GROUP, f"{library_id}|{kind}|{basis}|{name}")
