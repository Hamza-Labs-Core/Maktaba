"""What detection left over in a library, and every show in the catalog.

Shared by ``promo_links`` and ``clip_groups`` (plan 06-11 section 17), the I/O half of
:mod:`maktaba_pipeline.classify.clip_groups`, ported from mediaintel's
``group_clips.group_folder``:

* a **leftover** is a video of the library in no season (or in a ``clips`` season,
  which is filler by nature), not a second upload of an episode;
* a **show** is a series with live seasons, in any library, for linking promos.

The library is the unit here (mediaintel used the folder, which was one channel).
"""

from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass
from typing import Any
from uuid import UUID

from ...classify.clip_groups import DEFAULT, GroupConfig, Item, Show, is_qa, name_words
from ...classify.series_detector import DetectorConfig, VideoFacts, is_short
from ..sql import loads, uid
from .detect import _playlists

__all__ = ["Leftovers", "load_leftovers", "load_shows", "library_name"]


_VIDEOS_SQL = """
SELECT v.id AS vid, COALESCE(yv.title, v.title, v.filename) AS title,
       COALESCE(v.duration_sec, yv.duration_s) AS dur, yv.upload_date AS upload_date,
       yv.tab AS tab, v.metadata AS metadata
  FROM videos v
  LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'
  LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id
 WHERE v.library_id = $1 AND v.deleted_at IS NULL
 ORDER BY v.id
"""


@dataclass(slots=True)
class Leftovers:
    items: dict[str, Item]
    #: a promo detection already put with a season: video -> (series id, season number)
    extras_of: dict[str, tuple[str, int]]
    #: playlist id -> title, for the playlists the items are in
    playlist_titles: dict[str, str]


async def library_name(db: Any, library_id: UUID) -> str | None:
    row = await db.fetchrow("SELECT name FROM libraries WHERE id = $1", uid(library_id))
    return None if row is None else str(row["name"])


def _iso(v: Any) -> str | None:
    return None if v is None else str(v)[:10]


async def load_leftovers(
    db: Any,
    library_id: UUID,
    *,
    skip_playlists: set[str] | None = None,
    groups: GroupConfig = DEFAULT,
    detect: DetectorConfig | None = None,
) -> Leftovers:
    """The library's leftovers with their titles, lengths, playlists, topic tags and
    whether each is a clip or a question. ``skip_playlists`` (the topic collections'
    own playlists) are not grouping candidates: one playlist, one collection.
    ``groups`` / ``detect``: the rules as set (clip length, Q&A phrases, shorts)."""
    det = detect or DetectorConfig()
    placed: set[str] = set()
    clip_members: set[str] = set()
    hidden: set[str] = set()
    extras_of: dict[str, tuple[str, int]] = {}
    seasons = await db.fetch(
        "SELECT ss.id AS id, ss.series_id AS series_id, ss.season AS season, ss.kind AS kind,"
        " ss.metadata AS metadata FROM series_seasons ss"
        " WHERE ss.library_id = $1 AND ss.retired_at IS NULL",
        uid(library_id),
    )
    members = await db.fetch(
        "SELECT e.video_id AS vid, ss.kind AS kind FROM series_episodes e"
        " JOIN series_seasons ss ON ss.series_id = e.series_id"
        "  AND ss.season = COALESCE(e.season_override, e.season) AND ss.retired_at IS NULL"
        " WHERE ss.library_id = $1",
        uid(library_id),
    )
    for r in members:
        (clip_members if r["kind"] == "clips" else placed).add(str(r["vid"]))
    for s in seasons:
        meta = loads(s["metadata"]) or {}
        hidden.update(str(x) for x in meta.get("duplicates") or [])
        for x in meta.get("extras") or []:
            extras_of[str(x)] = (str(s["series_id"]), int(s["season"]))
    tags: dict[str, dict[str, float]] = defaultdict(dict)
    for r in await db.fetch(
        "SELECT t.video_id AS vid, t.tag AS tag, t.score AS score FROM video_topic_tags t"
        " JOIN videos v ON v.id = t.video_id WHERE v.library_id = $1",
        uid(library_id),
    ):
        tags[str(r["vid"])][str(r["tag"])] = float(r["score"])
    items: dict[str, Item] = {}
    for r in await db.fetch(_VIDEOS_SQL, uid(library_id)):
        vid = str(r["vid"])
        if vid in placed or vid in hidden or not r["dur"]:
            continue
        meta = loads(r["metadata"]) or {}
        w, h = meta.get("width"), meta.get("height")
        dur = float(r["dur"])
        title = str(r["title"] or "")
        short = is_short(
            VideoFacts(
                key=vid,
                title=title,
                duration_s=dur,
                is_short=r["tab"] == "shorts" or bool(w and h and h > w),
            ),
            det,
        )
        it = Item(vid, title, dur, _iso(r["upload_date"]))
        it.clip = short or vid in clip_members or dur < groups.filler_max_minutes * 60
        t = tags.get(vid, {})
        genres = sorted((sc, g[6:]) for g, sc in t.items() if g.startswith("genre:"))
        it.genre = genres[-1][1] if genres else None
        it.qa = t.get("format:qa_fatwa", 0.0) >= 0.5 or is_qa(title, groups)
        items[vid] = it
    titles: dict[str, str] = {}
    skip = skip_playlists or set()
    for pid, (ptitle, pmembers) in (await _playlists(db, library_id)).items():
        if pid in skip:
            continue
        for vid, pos in pmembers:
            if vid in items:
                items[vid].playlists.setdefault(pid, pos if pos is not None else 10**6)
                titles[pid] = ptitle
    return Leftovers(items, extras_of, titles)


async def load_shows(db: Any) -> list[Show]:
    """Every series with live seasons, in every library (a promo may advertise a show
    another channel airs). ``folder`` is the library id: same-library shows win."""
    rows = await db.fetch(
        "SELECT s.id AS id, COALESCE(s.name_override, s.name) AS name, s.library_id AS lib,"
        " COALESCE(l.name, '') AS library, ss.season AS season FROM series s"
        " JOIN series_seasons ss ON ss.series_id = s.id AND ss.retired_at IS NULL"
        " AND ss.kind <> 'clips' LEFT JOIN libraries l ON l.id = s.library_id"
        " ORDER BY s.id, ss.season"
    )
    first: dict[str, tuple[str, str, str]] = {}
    seasons: dict[str, set[int]] = defaultdict(set)
    for r in rows:
        sid = str(r["id"])
        first.setdefault(sid, (str(r["library"]), str(r["lib"]), str(r["name"])))
        seasons[sid].add(int(r["season"]))
    return [
        Show(lib, folder, name, sid, name_words(name), tuple(sorted(seasons[sid])))
        for sid, (lib, folder, name) in first.items()
    ]
