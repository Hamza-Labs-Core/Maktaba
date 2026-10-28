"""YouTube sources and downloads of a library (Epic 47, stories 47.8 to 47.12).

``youtube_sources`` says which channels and playlists a library follows and how
(``mode``: ``off``, ``new_and_backfill``, ``new_only``); ``youtube_downloads`` is one row
per video the library wants or has: ``wanted`` -> ``downloading`` -> ``done``, or
``skipped`` / ``gone`` / ``failed`` with a reason. The database is the truth; the yt-dlp
archive files are rebuilt from it.

* :func:`list_source`: the incremental listing (``--flat-playlist`` and, once a channel
  was listed in full, ``--break-on-existing`` against the ids already known);
* :func:`mark_listed`: every listed id that is not local becomes ``wanted``, with
  ``backfill = upload_date < added_at`` (a video without a date: back catalogue on the
  source's first listing only);
* :func:`local_ids` and :func:`write_archive`: the download archive (done or linked).
"""

from __future__ import annotations

import json as _json
import os
import tempfile
from collections.abc import Iterable, Sequence
from dataclasses import dataclass, field
from datetime import UTC, date, datetime
from typing import Any, Literal
from uuid import UUID

from ..discovery.ytdlp_sidecars import parse_upload_date
from ..engine.sql import is_pg, loads, now, parse_ts, to_date, to_ts, uid
from ..integrations.youtube_download import (
    DownloadOptions,
    listing_argv,
    run_ytdlp,
    sanitize_name,
)
from ..integrations.youtube_errors import Classified, classify

__all__ = [
    "ARCHIVE_NAME",
    "ListedEntry",
    "ListingFailed",
    "MarkReport",
    "Source",
    "archive_path",
    "known_ids",
    "list_source",
    "load_sources",
    "local_ids",
    "mark_listed",
    "parse_entry",
    "set_source_status",
    "wanted_count",
    "write_archive",
]

ARCHIVE_NAME = "yt-dlp-archive.txt"
TABS = ("videos", "shorts", "streams")
_CHUNK = 400

Mode = Literal["off", "new_and_backfill", "new_only"]


@dataclass(frozen=True, slots=True)
class Source:
    id: UUID
    library_id: UUID
    kind: Literal["channel", "playlist"]
    url: str
    external_id: str
    folder: str | None
    mode: Mode
    added_at: datetime
    options: DownloadOptions
    status: str | None
    last_checked_at: datetime | None

    @property
    def enabled(self) -> bool:
        return self.mode != "off"

    def listing_urls(self) -> list[str]:
        if self.kind == "playlist":
            return [f"https://www.youtube.com/playlist?list={self.external_id}"]
        base = self.url.rstrip("/")
        for tab in (*TABS, "featured", "playlists", "about"):
            if base.endswith("/" + tab):
                base = base[: -len(tab) - 1]
        if not base.startswith("http"):
            base = f"https://www.youtube.com/channel/{self.external_id}"
        return [f"{base}/{tab}" for tab in TABS]


def _source(r: Any) -> Source:
    added = parse_ts(r["added_at"]) or now()
    return Source(
        id=UUID(str(r["id"])),
        library_id=UUID(str(r["library_id"])),
        kind=r["kind"],
        url=str(r["url"]),
        external_id=str(r["external_id"]),
        folder=r["folder"],
        mode=r["mode"],
        added_at=added,
        options=DownloadOptions.from_json(loads(r["options"])),
        status=r["status"],
        last_checked_at=parse_ts(r["last_checked_at"]),
    )


async def load_sources(db: Any, library_id: UUID, *, enabled_only: bool = False) -> list[Source]:
    rows = await db.fetch(
        "SELECT id, library_id, kind, url, external_id, folder, mode, added_at, options,"
        " status, last_checked_at FROM youtube_sources WHERE library_id = $1"
        + (" AND mode <> 'off'" if enabled_only else "")
        + " ORDER BY kind, external_id",
        uid(library_id),
    )
    return [_source(r) for r in rows]


async def ensure_channel_sources(db: Any, library_id: UUID) -> int:
    """A ``channel`` source (``off``) for each of the library's ``youtube_channels`` that
    has none: the migration did this once; channels found later get theirs here."""
    rows = await db.fetch(
        "SELECT c.channel_id AS cid, c.url AS url, c.title AS title FROM youtube_channels c"
        " WHERE c.library_id = $1 AND NOT EXISTS (SELECT 1 FROM youtube_sources s"
        "  WHERE s.library_id = c.library_id AND s.kind = 'channel'"
        "  AND s.external_id = c.channel_id)",
        uid(library_id),
    )
    for r in rows:
        await db.execute(
            "INSERT INTO youtube_sources (library_id, kind, url, external_id, mode, added_at)"
            " VALUES ($1, 'channel', $2, $3, 'off', $4)"
            " ON CONFLICT (library_id, kind, external_id) DO NOTHING",
            uid(library_id),
            str(r["url"]),
            str(r["cid"]),
            to_ts(db, now()),
        )
    return len(rows)


async def set_source_status(
    db: Any,
    source_id: UUID,
    status: str,
    error: str | None = None,
    *,
    checked: bool = False,
    folder: str | None = None,
) -> None:
    t = to_ts(db, now())
    await db.execute(
        "UPDATE youtube_sources SET status = $2, error = $3, updated_at = $4,"
        " last_checked_at = CASE WHEN $5 THEN $4 ELSE last_checked_at END,"
        " folder = COALESCE(folder, $6) WHERE id = $1",
        uid(source_id),
        status,
        None if error is None else error[:500],
        t,
        checked if is_pg(db) else int(checked),
        folder,
    )


# ------------------------------------------------------------------ ids


async def _ids(db: Any, sql: str, library_id: UUID) -> set[str]:
    return {str(r["y"]) for r in await db.fetch(sql, uid(library_id))}


_LINKED = (
    "SELECT DISTINCT m.external_id AS y FROM media_metadata_enrichment m"
    " JOIN videos v ON v.id = m.video_id WHERE v.library_id = $1 AND v.deleted_at IS NULL"
    " AND m.provider = 'youtube' AND m.external_id IS NOT NULL"
)


async def linked_ids(db: Any, library_id: UUID) -> set[str]:
    """YouTube ids of the library's videos (the ``link`` stage's rows)."""
    return await _ids(db, _LINKED, library_id)


async def linked_video_ids(db: Any, library_id: UUID) -> dict[str, UUID]:
    """YouTube id -> one of the library's videos linked to it."""
    rows = await db.fetch(
        "SELECT m.external_id AS y, m.video_id AS v FROM media_metadata_enrichment m"
        " JOIN videos v ON v.id = m.video_id WHERE v.library_id = $1 AND v.deleted_at IS NULL"
        " AND m.provider = 'youtube' AND m.external_id IS NOT NULL",
        uid(library_id),
    )
    out: dict[str, UUID] = {}
    for r in rows:
        out.setdefault(str(r["y"]), UUID(str(r["v"])))
    return out


async def local_ids(db: Any, library_id: UUID) -> set[str]:
    """What the library has: downloads that are ``done`` and linked videos."""
    done = await _ids(
        db,
        "SELECT youtube_id AS y FROM youtube_downloads WHERE library_id = $1 AND state = 'done'",
        library_id,
    )
    return done | await linked_ids(db, library_id)


async def known_ids(db: Any, library_id: UUID) -> set[str]:
    """Every id the library already decided about (any download state, or linked): the
    listing archive, where a refresh stops."""
    rows = await _ids(
        db, "SELECT youtube_id AS y FROM youtube_downloads WHERE library_id = $1", library_id
    )
    return rows | await linked_ids(db, library_id)


def archive_path(root_local: str) -> str:
    return os.path.join(root_local, ".maktaba", ARCHIVE_NAME)


def write_archive(path: str, ids: Iterable[str]) -> int:
    """``youtube <id>`` lines (sorted), written atomically; how many."""
    lines = sorted({i for i in ids if i})
    folder = os.path.dirname(path)
    os.makedirs(folder, exist_ok=True)
    fd, tmp = tempfile.mkstemp(prefix=".archive-", dir=folder)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.writelines(f"youtube {i}\n" for i in lines)
        os.replace(tmp, path)
    except BaseException:
        if os.path.exists(tmp):
            os.unlink(tmp)
        raise
    return len(lines)


def append_archive(path: str, youtube_id: str) -> None:
    """Add one id unless it is there (yt-dlp itself writes it after a download)."""
    try:
        with open(path, encoding="utf-8") as f:
            if any(line.split()[1:2] == [youtube_id] for line in f):
                return
    except FileNotFoundError:
        os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "a", encoding="utf-8") as f:
        f.write(f"youtube {youtube_id}\n")


# ------------------------------------------------------------------ listing


@dataclass(frozen=True, slots=True)
class ListedEntry:
    youtube_id: str
    title: str | None
    upload_date: date | None
    #: why it is not wanted at all, from the listing alone (``members_only``, ``live``)
    skip: str | None = None


_SKIP_AVAILABILITY = {
    "subscriber_only": "members_only",
    "premium_only": "members_only",
    "needs_auth": "private",
    "private": "private",
}


def parse_entry(doc: Any) -> ListedEntry | None:
    """One ``-j`` line of a flat listing; None for anything that is not a video."""
    if not isinstance(doc, dict):
        return None
    vid = doc.get("id")
    if not isinstance(vid, str) or len(vid) != 11:
        return None
    if doc.get("_type") not in (None, "url", "video"):
        return None
    when = parse_upload_date(doc.get("upload_date"))
    ts = doc.get("timestamp") or doc.get("release_timestamp")
    if when is None and isinstance(ts, int | float) and not isinstance(ts, bool):
        when = datetime.fromtimestamp(ts, UTC).date()
    skip = None
    live = doc.get("live_status")
    if live in ("is_live", "is_upcoming"):
        skip = "live"
    avail = doc.get("availability")
    if isinstance(avail, str) and avail in _SKIP_AVAILABILITY:
        skip = _SKIP_AVAILABILITY[avail]
    title = doc.get("title")
    return ListedEntry(vid, title if isinstance(title, str) else None, when, skip)


class ListingFailed(Exception):
    def __init__(self, url: str, error: Classified) -> None:
        super().__init__(f"{url}: {error.message}")
        self.url = url
        self.error = error


@dataclass(slots=True)
class Listing:
    entries: list[ListedEntry] = field(default_factory=list)
    title: str | None = None
    #: tabs a channel does not have (no shorts)
    missing_tabs: int = 0


async def list_source(source: Source, *, archive: str | None, break_on_existing: bool) -> Listing:
    """List ``source`` flat. Raises :class:`ListingFailed` for an error that is not a
    missing tab (the caller decides what a bot check or a gone channel means)."""
    out = Listing()
    seen: set[str] = set()
    for url in source.listing_urls():
        res = await run_ytdlp(
            listing_argv(
                url,
                archive=archive,
                break_on_existing=break_on_existing,
                approximate_dates=source.kind == "channel",
                cookies_from_browser=source.options.cookies_from_browser,
            ),
            timeout=1800.0,
        )
        docs: list[dict[str, Any]] = []
        for line in res.stdout:
            line = line.strip()
            if not line.startswith("{"):
                continue
            try:
                doc = _json.loads(line)
            except ValueError:
                continue
            if isinstance(doc, dict):
                docs.append(doc)
        # 101: yt-dlp stopped at an id already in the archive (--break-on-existing)
        if res.returncode not in (0, 101) and not docs:
            if "does not have a" in res.stderr and "tab" in res.stderr:
                out.missing_tabs += 1
                continue
            raise ListingFailed(url, classify(res.stderr or "\n".join(res.stdout)))
        for doc in docs:
            if out.title is None:
                t = doc.get("playlist_title") or doc.get("channel") or doc.get("uploader")
                if isinstance(t, str) and t:
                    out.title = t.removesuffix(" - Videos").removesuffix(" - Shorts")
            e = parse_entry(doc)
            if e is not None and e.youtube_id not in seen:
                seen.add(e.youtube_id)
                out.entries.append(e)
    return out


def default_folder(source: Source, title: str | None) -> str:
    return sanitize_name(title, fallback=sanitize_name(source.external_id))


# ------------------------------------------------------------------ marking wanted


@dataclass(slots=True)
class MarkReport:
    listed: int = 0
    new: int = 0
    backfill: int = 0
    skipped: int = 0
    local: int = 0
    known: int = 0

    def as_json(self) -> dict[str, int]:
        return {
            "listed": self.listed,
            "new": self.new,
            "backfill": self.backfill,
            "skipped": self.skipped,
            "local": self.local,
            "known": self.known,
        }


async def mark_listed(
    db: Any,
    source: Source,
    entries: Sequence[ListedEntry],
    *,
    first_listing: bool,
    local: set[str],
) -> MarkReport:
    """Record the listed ids the library lacks. An id with a row already keeps it (a
    ``skipped`` or ``gone`` one waits for *Retry*); a local one gets no row (seeding
    writes those). ``new_only``: the back catalogue is ``skipped`` (``too_old``), so the
    next refresh stops at it."""
    rep = MarkReport(listed=len(entries))
    cutoff = source.added_at.astimezone(UTC).date()
    t = to_ts(db, now())
    existing = await _existing(db, source.library_id, [e.youtube_id for e in entries])
    async with db.transaction():
        for e in entries:
            if e.youtube_id in local:
                rep.local += 1
                continue
            if e.youtube_id in existing:
                rep.known += 1
                continue
            back = e.upload_date < cutoff if e.upload_date is not None else first_listing
            state, reason = "wanted", None
            if e.skip is not None:
                state, reason = "skipped", e.skip
            elif back and source.mode == "new_only":
                state, reason = "skipped", "too_old"
            await db.execute(
                "INSERT INTO youtube_downloads (library_id, youtube_id, source_id, state,"
                " backfill, reason, upload_date, requested_at)"
                " VALUES ($1,$2,$3,$4,$5,$6,$7,$8)"
                " ON CONFLICT (library_id, youtube_id) DO NOTHING",
                uid(source.library_id),
                e.youtube_id,
                uid(source.id),
                state,
                back if is_pg(db) else int(back),
                reason,
                to_date(db, e.upload_date),
                t,
            )
            existing.add(e.youtube_id)
            if state == "skipped":
                rep.skipped += 1
            elif back:
                rep.backfill += 1
            else:
                rep.new += 1
    return rep


async def _existing(db: Any, library_id: UUID, ids: Sequence[str]) -> set[str]:
    found: set[str] = set()
    for i in range(0, len(ids), _CHUNK):
        chunk = list(ids[i : i + _CHUNK])
        marks = ",".join(f"${n + 2}" for n in range(len(chunk)))
        rows = await db.fetch(
            "SELECT youtube_id FROM youtube_downloads WHERE library_id = $1"
            f" AND youtube_id IN ({marks})",
            uid(library_id),
            *chunk,
        )
        found.update(str(r["youtube_id"]) for r in rows)
    return found


async def wanted_count(db: Any, library_id: UUID, *, backfill: bool) -> int:
    """Items ready to download from an enabled source (the back catalogue only from a
    ``new_and_backfill`` one)."""
    modes = "('new_and_backfill')" if backfill else "('new_and_backfill','new_only')"
    n = await db.fetchval(
        "SELECT count(*) FROM youtube_downloads d JOIN youtube_sources s ON s.id = d.source_id"
        f" WHERE d.library_id = $1 AND d.state = 'wanted' AND s.mode IN {modes}"
        " AND d.backfill = $2 AND (d.not_before IS NULL OR d.not_before <= $3)",
        uid(library_id),
        backfill if is_pg(db) else int(backfill),
        to_ts(db, now()),
    )
    return int(n or 0)


# ------------------------------------------------------------------ seeding (story 47.12)


async def seed_done(
    db: Any,
    library_id: UUID,
    ids: Iterable[str],
    *,
    reason: str,
    video_ids: dict[str, UUID] | None = None,
) -> int:
    """Record ``ids`` as ``done`` (the library has them): a new row, or an existing one
    that is not ``done`` yet (a ``downloading`` one is left to its job). They go into the
    archive, so neither the listing nor the back catalogue ever asks for them again."""
    video_ids = video_ids or {}
    t = to_ts(db, now())
    n = 0
    no = False if is_pg(db) else 0
    async with db.transaction():
        for yid in ids:
            vid = video_ids.get(yid)
            rows = await db.fetch(
                "INSERT INTO youtube_downloads (library_id, youtube_id, state, backfill, reason,"
                " video_id, requested_at, done_at) VALUES ($1,$2,'done',$3,$4,$5,$6,$6)"
                " ON CONFLICT (library_id, youtube_id) DO UPDATE SET state = 'done',"
                " reason = EXCLUDED.reason, done_at = EXCLUDED.done_at,"
                " video_id = COALESCE(youtube_downloads.video_id, EXCLUDED.video_id),"
                " failure_class = NULL, error = NULL, not_before = NULL"
                " WHERE youtube_downloads.state NOT IN ('done', 'downloading')"
                " RETURNING youtube_id",
                uid(library_id),
                yid,
                no,
                reason,
                uid(vid),
                t,
            )
            n += len(rows)
    return n


async def seed_from_library(db: Any, library_id: UUID) -> int:
    """The library's videos linked to a YouTube id are ``done`` (reason ``library``)."""
    rows = await db.fetch(
        "SELECT m.external_id AS y, m.video_id AS v FROM media_metadata_enrichment m"
        " JOIN videos v ON v.id = m.video_id WHERE v.library_id = $1 AND v.deleted_at IS NULL"
        " AND m.provider = 'youtube' AND m.external_id IS NOT NULL"
        " AND NOT EXISTS (SELECT 1 FROM youtube_downloads d WHERE d.library_id = v.library_id"
        "   AND d.youtube_id = m.external_id AND d.state = 'done')",
        uid(library_id),
    )
    found: dict[str, UUID] = {}
    for r in rows:
        found.setdefault(str(r["y"]), UUID(str(r["v"])))
    if not found:
        return 0
    return await seed_done(db, library_id, sorted(found), reason="library", video_ids=found)
