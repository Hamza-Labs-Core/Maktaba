"""Parse the sidecar files yt-dlp leaves next to downloaded videos.

A yt-dlp download folder can hold, per video ``<stem>.<ext>``:

- ``<stem>.info.json`` — full metadata: YouTube id, upload date, duration,
  channel, and (when downloaded from a playlist) the playlist title, id and
  position.
- ``<stem>.description`` — the video description.
- ``<stem>.webp`` / ``.jpg`` / ``.png`` — the thumbnail, next to the video or in the
  folder's hidden ``.thumbs/`` subfolder (:func:`find_sidecar_thumbnail`).

and, per folder:

- a *playlist-level* ``.info.json`` (``_type == "playlist"``) — either a
  real playlist (``PL…``/``OL…`` id) or a channel tab (``UC…`` id or an
  ``@handle``; titles like ``"<channel> - Videos"``).
- ``ytdl-archive.txt`` — ``"youtube <id>"`` lines in download order.

Everything here is pure parsing over already-read content (the scanner
owns the filesystem walk), except :func:`find_sidecar_thumbnail`: a few stats.
Channel tabs are *not* series: only real playlists carry grouping meaning
(:func:`is_real_playlist`).
"""

from __future__ import annotations

import json
import os
from dataclasses import dataclass
from datetime import date
from typing import Any

__all__ = [
    "SIDECARS_VERSION",
    "THUMBS_DIR",
    "THUMB_EXTS",
    "PlaylistInfo",
    "VideoInfo",
    "find_sidecar_thumbnail",
    "is_real_playlist",
    "parse_archive",
    "parse_api_license",
    "parse_info_json",
    "parse_license",
    "parse_upload_date",
]

SIDECARS_VERSION: int = 1

THUMB_EXTS: tuple[str, ...] = (".webp", ".jpg", ".jpeg", ".png")

#: the hidden subfolder some download setups keep a channel's thumbnails in
#: (``<channel>/.thumbs/<stem>.jpg`` beside ``<channel>/<stem>.mp4``)
THUMBS_DIR = ".thumbs"


def find_sidecar_thumbnail(video_local_path: str) -> str | None:
    """The video's downloaded thumbnail, or None: ``<dir>/<stem><ext>`` first, then
    ``<dir>/.thumbs/<stem><ext>``, for each ext of :data:`THUMB_EXTS` in order.

    The one rule every reader of a video's own image goes through. Stats only, never a
    directory listing: the share can be slow and a channel folder can hold thousands of
    files. ``video_local_path`` is this machine's path (the spelling that exists, so the
    stem matches its sidecars' Unicode form).
    """
    folder, name = os.path.split(video_local_path)
    stem = os.path.splitext(name)[0]
    if not stem:
        return None
    for where in (folder, os.path.join(folder, THUMBS_DIR)):
        for ext in THUMB_EXTS:
            candidate = os.path.join(where, stem + ext)
            if os.path.isfile(candidate):  # False on any stat error
                return candidate
    return None


@dataclass(frozen=True, slots=True)
class VideoInfo:
    """The fields of a video ``.info.json`` that matter for grouping."""

    youtube_id: str
    title: str
    upload_date: date | None
    duration_s: float | None
    channel: str | None
    channel_id: str | None
    playlist_title: str | None
    playlist_id: str | None
    playlist_index: int | None
    description: str | None
    tags: tuple[str, ...]
    webpage_url: str | None
    #: YouTube's age limit for the video (0: none, 18: age-restricted); None when the
    #: file does not say. The age tiers read it (Epic 57, story 57.01).
    age_limit: int | None = None
    #: the video's licence: ``cc_by`` (Creative Commons Attribution), ``standard``, or None
    #: when the file does not say. Publishable sources read it (Epic 50, story 50.23).
    license: str | None = None


@dataclass(frozen=True, slots=True)
class PlaylistInfo:
    """A playlist-level ``.info.json`` (a real playlist or a channel tab)."""

    playlist_id: str
    title: str
    channel: str | None
    channel_id: str | None
    channel_url: str | None
    count: int | None
    entry_ids: tuple[str, ...]
    is_real: bool
    webpage_url: str | None


def is_real_playlist(playlist_id: str | None) -> bool:
    """True for user playlists (``PL…``/``OL…``), false for channel tabs/uploads."""
    return bool(playlist_id) and str(playlist_id).startswith(("PL", "OL"))


def parse_upload_date(value: Any) -> date | None:
    """yt-dlp ``YYYYMMDD`` → :class:`date` (``None`` if absent or malformed)."""
    if not isinstance(value, str) or len(value) != 8 or not value.isdigit():
        return None
    try:
        return date(int(value[:4]), int(value[4:6]), int(value[6:]))
    except ValueError:
        return None


def _str(v: Any) -> str | None:
    return v if isinstance(v, str) and v else None


def _int(v: Any) -> int | None:
    return v if isinstance(v, int) and not isinstance(v, bool) else None


def parse_age_limit(value: Any) -> int | None:
    """yt-dlp's ``age_limit`` (an age in years, 0 for none) → int; ``None`` if absent or
    not an age."""
    v = _int(value)
    return v if v is not None and 0 <= v <= 99 else None


#: words that make a Creative Commons licence something other than plain Attribution
_CC_NOT_BY = (
    "noncommercial",
    "non-commercial",
    "no derivative",
    "noderivative",
    "sharealike",
    "share alike",
    "-nc",
    "-nd",
    "-sa",
)


def parse_license(value: Any) -> str | None:
    """yt-dlp's ``license`` → ``cc_by``, ``standard`` or ``None`` (absent).

    YouTube writes "Creative Commons Attribution license (reuse allowed)" for its one
    Creative Commons licence; any casing is read the same. A Creative Commons licence with
    a further condition (non-commercial, no derivatives, share-alike) is not CC BY, so it
    counts as ``standard``: only CC BY makes an item publishable by itself (story 50.23).
    """
    v = _str(value)
    if v is None:
        return None
    low = v.strip().lower()
    if low in ("creativecommon", "creativecommons"):
        return "cc_by"
    is_cc = "creative commons" in low or low.startswith("cc by") or low.startswith("cc-by")
    if (
        is_cc
        and ("attribution" in low or "cc by" in low or "cc-by" in low)
        and not any(w in low for w in _CC_NOT_BY)
    ):
        return "cc_by"
    return "standard"


def parse_api_license(value: Any) -> str | None:
    """The YouTube Data API's ``status.license`` → ``cc_by`` (``creativeCommon``),
    ``standard`` (``youtube``) or ``None``."""
    v = _str(value)
    if v is None:
        return None
    return "cc_by" if v.strip().lower() == "creativecommon" else "standard"


def parse_info_json(text: str) -> VideoInfo | PlaylistInfo | None:
    """Parse an ``.info.json`` body. Returns ``None`` for unreadable/unknown content."""
    try:
        j = json.loads(text)
    except (ValueError, TypeError):
        return None
    if not isinstance(j, dict):
        return None
    if j.get("_type") == "playlist":
        pid = _str(j.get("id")) or ""
        raw_entries = j.get("entries")
        entries: list[Any] = raw_entries if isinstance(raw_entries, list) else []
        ids = tuple(
            e["id"] for e in entries if isinstance(e, dict) and isinstance(e.get("id"), str)
        )
        return PlaylistInfo(
            playlist_id=pid,
            title=_str(j.get("title")) or "",
            channel=_str(j.get("channel")) or _str(j.get("uploader")),
            channel_id=_str(j.get("channel_id")),
            channel_url=_str(j.get("channel_url")),
            count=_int(j.get("playlist_count")),
            entry_ids=ids,
            is_real=is_real_playlist(pid),
            webpage_url=_str(j.get("webpage_url")),
        )
    vid = _str(j.get("id"))
    if vid is None:
        return None
    dur = j.get("duration")
    raw_tags = j.get("tags")
    tags: list[Any] = raw_tags if isinstance(raw_tags, list) else []
    return VideoInfo(
        youtube_id=vid,
        title=_str(j.get("fulltitle")) or _str(j.get("title")) or "",
        upload_date=parse_upload_date(j.get("upload_date")),
        duration_s=float(dur) if isinstance(dur, int | float) else None,
        channel=_str(j.get("channel")) or _str(j.get("uploader")),
        channel_id=_str(j.get("channel_id")),
        playlist_title=_str(j.get("playlist_title")) or _str(j.get("playlist")),
        playlist_id=_str(j.get("playlist_id")),
        playlist_index=_int(j.get("playlist_index")),
        description=_str(j.get("description")),
        tags=tuple(t for t in tags if isinstance(t, str)),
        webpage_url=_str(j.get("webpage_url")),
        age_limit=parse_age_limit(j.get("age_limit")),
        license=parse_license(j.get("license")),
    )


def parse_archive(text: str) -> list[str]:
    """``ytdl-archive.txt`` → YouTube ids in download order (duplicates dropped)."""
    seen: set[str] = set()
    out: list[str] = []
    for line in (text or "").splitlines():
        parts = line.split()
        if len(parts) == 2 and parts[0] == "youtube" and parts[1] not in seen:
            seen.add(parts[1])
            out.append(parts[1])
    return out
