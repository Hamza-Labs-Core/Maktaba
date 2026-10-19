"""What Maktaba already knows about a video: the internal intel a suggestion starts from.

Read from the database, never from the network: the file and folder names (and what the
title parser makes of them), the library, the YouTube listing it came from, the series and
season it is an episode of (and its sibling episodes), the book that series explains
(books-are-sources: a lesson series explains a real book), the topic tags, the named
speakers and entities, the chapters, the opening of its transcript and the collections it
is in.
"""

from __future__ import annotations

import json
import re
from dataclasses import dataclass, field
from pathlib import PurePosixPath, PureWindowsPath
from typing import Any
from uuid import UUID

from ..classify import books as bk
from ..classify import title_parser
from ..classify.topic_tags import TOPIC_NAMES_AR
from ..engine.sql import loads

__all__ = ["Book", "VideoIntel", "gather_intel"]

#: transcript characters kept (the opening is where a lesson names its book and speaker)
TRANSCRIPT_CHARS = 1500
SIBLINGS = 12
CHAPTERS = 20


@dataclass(frozen=True, slots=True)
class Book:
    name: str
    chapter: str = ""
    title: str = ""
    url: str = ""
    summary: str = ""
    subjects: tuple[str, ...] = ()
    #: ``lookup``: found in the encyclopedia (the books stage); ``name``: only read from
    #: the series' name
    via: str = "name"


@dataclass
class VideoIntel:
    video_id: UUID
    library_id: UUID | None = None
    library: str = ""
    filename: str = ""
    folders: tuple[str, ...] = ()
    title: str = ""
    display_title: str = ""
    description: str = ""
    language: str = ""
    duration_s: float | None = None
    parsed_show: str = ""
    parsed_season: int | None = None
    parsed_episode: int | None = None
    parsed_year: int | None = None
    yt_title: str = ""
    yt_description: str = ""
    yt_year: int | None = None
    yt_channel: str = ""
    series: str = ""
    season: int | None = None
    episode: int | None = None
    season_name: str = ""
    season_id: str = ""
    siblings: list[str] = field(default_factory=list)
    book: Book | None = None
    topics: list[str] = field(default_factory=list)
    speakers: list[str] = field(default_factory=list)
    people: list[str] = field(default_factory=list)
    chapters: list[str] = field(default_factory=list)
    transcript: str = ""
    collections: list[str] = field(default_factory=list)

    @property
    def best_title(self) -> str:
        return (
            self.display_title
            or self.title
            or self.yt_title
            or self.parsed_show
            or _stem(self.filename)
        )

    def summary_lines(self) -> list[tuple[str, str, str]]:
        """(kind, label, text) per piece of intel, for the model's evidence list."""
        out: list[tuple[str, str, str]] = []

        def add(kind: str, label: str, text: str) -> None:
            if text and text.strip():
                out.append((kind, label, text.strip()))

        add("file", "File and folder names", " / ".join((*self.folders, self.filename)))
        add("library", "Library", self.library)
        add("title", "Title in the library", self.title or self.display_title)
        add("youtube", "YouTube listing", "\n".join(
            x for x in (self.yt_title, f"Channel: {self.yt_channel}" if self.yt_channel else "",
                        f"Uploaded: {self.yt_year}" if self.yt_year else "",
                        self.yt_description[:1200]) if x))  # fmt: skip
        if self.series:
            se = f"Series: {self.series}"
            if self.season is not None:
                se += f", season {self.season}"
            if self.episode is not None:
                se += f", episode {self.episode}"
            if self.siblings:
                se += "\nOther episodes: " + " | ".join(self.siblings)
            add("series", "Series and sibling episodes", se)
        if self.book is not None:
            b = self.book
            add("book", "Book the series explains", "\n".join(x for x in (
                f"Book: {b.title or b.name}", f"Chapter: {b.chapter}" if b.chapter else "",
                f"Subject: {', '.join(b.subjects)}" if b.subjects else "",
                b.summary[:800]) if x))  # fmt: skip
        add("topics", "Topic tags", ", ".join(self.topics))
        add("speakers", "Speakers and people named", ", ".join(
            dict.fromkeys([*self.speakers, *self.people])))  # fmt: skip
        add("chapters", "Chapters", " | ".join(self.chapters))
        add("transcript", "Opening of the transcript", self.transcript)
        add("collections", "Collections", ", ".join(self.collections))
        return out


def _stem(filename: str) -> str:
    s = re.sub(r"\.[A-Za-z0-9]{2,4}$", "", filename or "")
    return re.sub(r"[._]+", " ", s).strip()


def _folders(path: str) -> tuple[str, ...]:
    if not path:
        return ()
    p = PureWindowsPath(path) if "\\" in path else PurePosixPath(path)
    parts = [x for x in p.parent.parts if x not in ("/", "\\") and not x.endswith(":\\")]
    return tuple(parts[-2:])


def _year(v: Any) -> int | None:
    m = re.match(r"(\d{4})", str(v or ""))
    return int(m.group(1)) if m else None


async def gather_intel(db: Any, video_id: UUID) -> VideoIntel:
    """Everything the database says about ``video_id`` (raises ``LookupError`` when the
    video does not exist)."""
    v = await db.fetchrow(
        "SELECT v.id, v.library_id, v.path, v.filename, v.title, v.display_title,"
        " v.display_title_manual, v.description, v.detected_language, v.duration_sec,"
        " l.name AS library FROM videos v LEFT JOIN libraries l ON l.id = v.library_id"
        " WHERE v.id = $1",
        str(video_id),
    )
    if v is None:
        raise LookupError(f"video {video_id}")
    vid = str(video_id)
    intel = VideoIntel(
        video_id=video_id,
        library_id=UUID(str(v["library_id"])) if v["library_id"] else None,
        library=str(v["library"] or ""),
        filename=str(v["filename"] or ""),
        folders=_folders(str(v["path"] or "")),
        title=str(v["title"] or ""),
        display_title=str(v["display_title_manual"] or v["display_title"] or ""),
        description=str(v["description"] or ""),
        language=str(v["detected_language"] or ""),
        duration_s=float(v["duration_sec"]) if v["duration_sec"] is not None else None,
    )
    parsed = title_parser.parse(intel.filename, dirnames=intel.folders)
    intel.parsed_show = parsed.show_name or ""
    intel.parsed_season, intel.parsed_episode = parsed.season, parsed.episode
    intel.parsed_year = parsed.year

    yt = await db.fetchrow(
        "SELECT y.title, y.description, y.upload_date, c.title AS channel"
        " FROM media_metadata_enrichment e JOIN youtube_videos y ON y.youtube_id = e.external_id"
        " LEFT JOIN youtube_channels c ON c.channel_id = y.channel_id"
        " WHERE e.video_id = $1 AND e.provider = 'youtube' LIMIT 1",
        vid,
    )
    if yt is not None:
        intel.yt_title = str(yt["title"] or "")
        intel.yt_description = str(yt["description"] or "")
        intel.yt_year = _year(yt["upload_date"])
        intel.yt_channel = str(yt["channel"] or "")

    await _series(db, vid, intel)
    await _book(db, intel)
    await _topics(db, vid, intel)

    intel.speakers = [
        str(r["name"])
        for r in await db.fetch(
            "SELECT DISTINCT name FROM speakers WHERE video_id = $1 AND name IS NOT NULL"
            " AND name <> '' ORDER BY name LIMIT 10",
            vid,
        )
    ]
    intel.people = [
        str(r["name"])
        for r in await db.fetch(
            "SELECT DISTINCT m.name FROM video_entities ve JOIN media_entities m"
            " ON m.id = ve.entity_id WHERE ve.video_id = $1 AND m.kind = 'person'"
            " ORDER BY m.name LIMIT 10",
            vid,
        )
    ]
    intel.chapters = [
        str(r["title"])
        for r in await db.fetch(
            "SELECT title FROM chapters WHERE video_id = $1 AND title IS NOT NULL AND title <> ''"
            " ORDER BY seq LIMIT $2",
            vid,
            CHAPTERS,
        )
    ]
    t = await db.fetchrow(
        "SELECT id, language, detected_language FROM transcripts WHERE video_id = $1"
        " ORDER BY is_active DESC, created_at DESC LIMIT 1",
        vid,
    )
    if t is not None:
        intel.language = intel.language or str(t["detected_language"] or t["language"] or "")
        text, size = [], 0
        for r in await db.fetch(
            "SELECT text FROM transcript_segments WHERE transcript_id = $1 ORDER BY seq LIMIT 200",
            str(t["id"]),
        ):
            piece = str(r["text"] or "").strip()
            if not piece:
                continue
            text.append(piece)
            size += len(piece) + 1
            if size >= TRANSCRIPT_CHARS:
                break
        intel.transcript = " ".join(text)[:TRANSCRIPT_CHARS]
    if not intel.language:
        c = await db.fetchrow("SELECT language FROM video_classification WHERE video_id = $1", vid)
        intel.language = str(c["language"] or "") if c is not None else ""
    intel.collections = [
        str(r["name"])
        for r in await db.fetch(
            "SELECT c.name FROM collection_items i JOIN collections c ON c.id = i.collection_id"
            " WHERE i.video_id = $1 AND c.dismissed_at IS NULL ORDER BY c.name LIMIT 10",
            vid,
        )
    ]
    return intel


async def _series(db: Any, vid: str, intel: VideoIntel) -> None:
    r = await db.fetchrow(
        "SELECT se.series_id, COALESCE(se.season_override, se.season) AS season,"
        " COALESCE(se.episode_override, se.episode) AS episode,"
        " COALESCE(s.name_override, s.display_name, s.name) AS name"
        " FROM series_episodes se JOIN series s ON s.id = se.series_id"
        " WHERE se.video_id = $1 LIMIT 1",
        vid,
    )
    if r is None:
        return
    intel.series = str(r["name"] or "")
    intel.season = int(r["season"]) if r["season"] is not None else None
    intel.episode = int(r["episode"]) if r["episode"] is not None else None
    ss = await db.fetchrow(
        "SELECT id, name FROM series_seasons WHERE series_id = $1 AND season = $2"
        " AND retired_at IS NULL LIMIT 1",
        str(r["series_id"]),
        intel.season if intel.season is not None else 1,
    )
    if ss is not None:
        intel.season_name = str(ss["name"] or "")
        intel.season_id = str(ss["id"])
    rows = await db.fetch(
        "SELECT COALESCE(v.display_title, v.title, v.filename) AS t"
        " FROM series_episodes se JOIN videos v ON v.id = se.video_id"
        " WHERE se.series_id = $1 AND se.video_id <> $2 AND v.deleted_at IS NULL"
        " ORDER BY COALESCE(se.season_override, se.season),"
        " COALESCE(se.episode_override, se.episode) LIMIT $3",
        str(r["series_id"]),
        vid,
        SIBLINGS,
    )
    intel.siblings = [str(x["t"]) for x in rows if x["t"]]


async def _book(db: Any, intel: VideoIntel) -> None:
    season_id = intel.season_id
    if season_id:
        r = await db.fetchrow(
            "SELECT sb.name, sb.chapter, sb.fields, b.title, b.url, b.summary"
            " FROM series_books sb LEFT JOIN books b ON b.name = sb.name"
            " WHERE sb.season_id = $1 LIMIT 1",
            season_id,
        )
        if r is not None:
            subjects = loads(r["fields"]) if r["fields"] else []
            intel.book = Book(
                name=str(r["name"]),
                chapter=str(r["chapter"] or ""),
                title=str(r["title"] or ""),
                url=str(r["url"] or ""),
                summary=str(r["summary"] or ""),
                subjects=tuple(TOPIC_NAMES_AR.get(str(s), str(s)) for s in subjects or ()),
                via="lookup" if r["title"] else "name",
            )
            return
    # not looked up yet (or not a season): the book's name as the series gives it
    for name in (intel.series, intel.season_name, intel.title, intel.yt_title):
        ref = bk.book_of(name) if name else None
        if ref is not None:
            intel.book = Book(name=ref.name, chapter=ref.chapter)
            return


async def _topics(db: Any, vid: str, intel: VideoIntel) -> None:
    tags: dict[str, float] = {}
    season_id = intel.season_id
    if season_id:
        for r in await db.fetch(
            "SELECT tag, score FROM season_tags WHERE season_id = $1", season_id
        ):
            tags[str(r["tag"])] = float(r["score"] or 0)
    for r in await db.fetch("SELECT tag, score FROM episode_tags WHERE video_id = $1", vid):
        tags[str(r["tag"])] = max(tags.get(str(r["tag"]), 0.0), float(r["score"] or 0))
    ranked = sorted(
        ((t.partition(":")[2], s) for t, s in tags.items() if t.startswith("topic:") and s >= 0.5),
        key=lambda x: -x[1],
    )
    intel.topics = [TOPIC_NAMES_AR.get(t, t) for t, _ in ranked[:3]]
    for r in await db.fetch(
        "SELECT lt.label FROM video_topics vt JOIN library_topics lt"
        " ON lt.library_id = vt.library_id AND lt.topic_id = vt.topic_id"
        " WHERE vt.video_id = $1 AND lt.label IS NOT NULL ORDER BY vt.score DESC LIMIT 2",
        vid,
    ):
        label = str(r["label"])
        if label not in intel.topics:
            intel.topics.append(label)


def to_json(intel: VideoIntel) -> str:
    """A stable text of the intel (the cache key of the model's answer)."""
    return json.dumps(intel.summary_lines(), ensure_ascii=False, sort_keys=True)
