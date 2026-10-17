"""group: shorts, clips and stand-alone videos into playable collections; promos to shows.

Detection places episodes in seasons. What is left over is still worth airing:

- **Clips** (filler): YouTube Shorts, clip playlists ("مقاطع مختارة …") and anything
  shorter than :data:`FILLER_MAX_S`. A channel pads its slots with them.
- **Stand-alone videos** (programme material): longer videos in no show, a one-off
  interview or lecture.

Each library's leftovers are grouped, one group per video: by the most specific real
playlist holding at least :data:`MIN_PLAYLIST` of them, else by their topic tag
(``genre:*``, at least :data:`MIN_TOPIC`), else one "متنوعة" group per kind.

**Promos** (برومو / إعلان / تشويق …) are not grouped: each is linked to the show it
advertises (``promo_links``), from the season detection put it with, else from the show
name its title carries (the same channel first, then any library). A channel airing the
show uses them as filler near its slots.

Tables (see :mod:`.db`): ``clip_groups`` + ``clip_group_items``, ``promo_links``;
replaced per folder on each run.
"""

from __future__ import annotations

import json
import sqlite3
from collections import Counter, defaultdict
from pathlib import Path

from maktaba_pipeline.classify.clip_groups import (  # noqa: F401 - re-exported
    DEFAULT,
    FILLER_MAX_S,
    MIN_PLAYLIST,
    MIN_TOPIC,
    PROMO_RE,
    QA_RE,
    Group,
    GroupConfig,
    Item,
    Show,
    group_id,
    group_title,
    link_promo,
    name_words,
    plan_groups,
    promo_season,
)
from maktaba_pipeline.text.arabic import fold

__all__ = [
    "DEFAULT",
    "FILLER_MAX_S",
    "MIN_PLAYLIST",
    "MIN_TOPIC",
    "PROMO_RE",
    "QA_RE",
    "Group",
    "GroupConfig",
    "Item",
    "Show",
    "group_id",
    "group_title",
    "link_promo",
    "name_words",
    "plan_groups",
    "promo_season",
    "group_folder",
    "run",
]


def _shows(conn: sqlite3.Connection) -> list[Show]:
    first: dict[tuple[str, str], tuple[str, str]] = {}
    seasons: dict[tuple[str, str], set[int]] = defaultdict(set)
    for library, name, meta_s in conn.execute("SELECT library, name, metadata FROM series"):
        m = json.loads(meta_s)
        if m.get("kind") == "clips":
            continue
        key = (m.get("folder", ""), m.get("series_key", name))
        first.setdefault(key, (library, name))
        seasons[key].add(int(m.get("season") or 1))
    return [
        Show(lib, k[0], name, k[1], name_words(name), tuple(sorted(seasons[k])))
        for k, (lib, name) in first.items()
    ]


def run(
    conn: sqlite3.Connection,
    only_folder: str | None = None,
    folders: list[str] | None = None,
    cfg: GroupConfig = DEFAULT,
) -> dict[str, int]:
    """Group every folder's leftovers (those under ``folders`` / matching ``only_folder``)
    and link its promos. Returns totals."""
    folders = [
        r[0]
        for r in conn.execute("SELECT DISTINCT folder FROM files ORDER BY folder")
        if (not only_folder or only_folder in r[0])
        and (folders is None or any(r[0].startswith(f.rstrip("/")) for f in folders))
        and "/." not in r[0]
    ]
    shows = _shows(conn)
    if cfg is DEFAULT:
        from . import settings_store

        cfg = settings_store.get(conn, "groups")
        settings_store.note(conn, "groups")
    totals: Counter[str] = Counter()
    print(f"grouping clips in {len(folders)} libraries", flush=True)
    for n, folder in enumerate(folders, 1):
        lib = conn.execute(
            "SELECT plex_library FROM files WHERE folder=? AND plex_library IS NOT NULL LIMIT 1",
            (folder,),
        ).fetchone()
        library = lib[0] if lib else Path(folder).name
        got = group_folder(conn, library, folder, shows, cfg)
        totals.update(got)
        print(f"  [{n}/{len(folders)}] {library}: {got['groups']} groups "
              f"({got['clips']} clips, {got['standalone']} stand-alone), "
              f"{got['promos']} promos linked", flush=True)  # fmt: skip
    return dict(totals)


def group_folder(
    conn: sqlite3.Connection,
    library: str,
    folder: str,
    shows: list[Show],
    cfg: GroupConfig = DEFAULT,
) -> Counter[str]:
    promo_re, qa_re = cfg.promo_re(), cfg.qa_re()
    placed: set[str] = set()
    clip_members: set[str] = set()
    extras_of: dict[str, tuple[Show, int]] = {}
    show_by_key = {(s.folder, s.series_key): s for s in shows}
    for sid, meta_s in conn.execute(
        "SELECT id, metadata FROM series WHERE json_extract(metadata, '$.folder') = ?", (folder,)
    ).fetchall():
        m = json.loads(meta_s)
        eps = {
            p for (p,) in conn.execute("SELECT path FROM series_episodes WHERE series_id=?", (sid,))
        }
        if m.get("kind") == "clips":
            clip_members |= eps
            continue
        placed |= eps
        show = show_by_key.get((folder, m.get("series_key")))
        for p in m.get("extras") or []:
            if show is not None:
                extras_of[p] = (show, int(m.get("season") or 1))
        # a second upload is not an episode, but it still plays: it is grouped like any
        # leftover so no long video sits in no collection
    copies = {p for (p,) in conn.execute("SELECT path FROM file_copies WHERE folder=?", (folder,))}
    shorts = {p for (p,) in conn.execute("SELECT path FROM shorts WHERE folder=?", (folder,))}
    rows = conn.execute(
        """SELECT f.path, COALESCE(y.title, f.stem), COALESCE(f.duration_s, y.duration_s),
                  y.upload_date, f.youtube_id
           FROM files f LEFT JOIN yt_videos y ON y.youtube_id = f.youtube_id
           WHERE f.folder = ?""",
        (folder,),
    ).fetchall()
    items: dict[str, Item] = {}
    yt_of: dict[str, str] = {}
    for path, title, dur, upload, yid in rows:
        if path in placed or path in copies or not dur:
            continue
        items[path] = Item(path, title or "", float(dur), upload)
        items[path].clip = (
            path in shorts or path in clip_members or float(dur) < cfg.filler_max_minutes * 60
        )
        if yid:
            yt_of[yid] = path
    # promos: linked to the show, not grouped
    links: list[tuple[str, Show, int | None, float, str]] = []
    unmatched: list[str] = []  # promos whose show is not found: kept out of clip groups
    promo_upload = {p: it.upload for p, it in items.items()}
    for path, it in list(items.items()):
        if not promo_re.search(fold(it.title)):
            continue
        with_season = extras_of.get(path)
        if with_season is not None:
            show, season = with_season
            links.append((path, show, season, 1.0, "aired with its season (detection)"))
        elif (hit := link_promo(it.title, folder, shows)) is not None:
            links.append((path, hit[0], promo_season(it.title, hit[0]), hit[1], hit[2]))
        else:
            unmatched.append(path)
        del items[path]
    titles: dict[str, str] = {}
    # a topic collection ("ملف") already holds its playlist's episodes: its clips are grouped
    # by topic instead, so one playlist is not two collections of one name
    topic_pls = {
        pid
        for (pid,) in conn.execute(
            "SELECT playlist_id FROM topic_collections WHERE folder=?", (folder,)
        )
    }
    yids = list(yt_of)
    for at in range(0, len(yids), 500):
        part = yids[at : at + 500]
        for pid, ptitle, yid, pos in conn.execute(
            f"""SELECT p.playlist_id, p.title, i.youtube_id, i.position FROM yt_playlist_items i
                JOIN yt_playlists p ON p.playlist_id = i.playlist_id
                WHERE p.is_real = 1 AND i.youtube_id IN ({",".join("?" * len(part))})""",  # noqa: S608
            part,
        ):
            path = yt_of[yid]
            if path in items and pid not in topic_pls:
                items[path].playlists.setdefault(pid, pos if pos is not None else 10**6)
                titles[pid] = ptitle
    # the tag step makes video_tags; without it every clip is untagged
    tagged = conn.execute(
        "SELECT 1 FROM sqlite_master WHERE type='table' AND name='video_tags'"
    ).fetchone()
    genre_rows = (
        conn.execute(
            """SELECT t.path, t.tag FROM video_tags t JOIN files f ON f.path = t.path
           WHERE f.folder = ? AND t.tag LIKE 'genre:%' ORDER BY t.score""",
            (folder,),
        )
        if tagged
        else []
    )
    for path, tag in genre_rows:
        if path in items:
            items[path].genre = tag.split(":", 1)[1]  # highest score last wins
    qa_tagged = (
        {
            p
            for (p,) in conn.execute(
                """SELECT t.path FROM video_tags t JOIN files f ON f.path = t.path
               WHERE f.folder = ? AND t.tag = 'format:qa_fatwa' AND t.score >= 0.5""",
                (folder,),
            )
        }
        if tagged
        else set()
    )
    for path, it in items.items():
        it.qa = path in qa_tagged or bool(qa_re.search(fold(it.title)))
    groups = plan_groups(list(items.values()), titles, cfg)
    # linked promos air next to their show, and they are also one collection per library
    promo_paths = sorted(
        [p for p, *_ in links] + unmatched, key=lambda p: (promo_upload.get(p) or "9999", p)
    )
    if promo_paths:
        groups.append(Group("clips", "promo", "إعلانات", None, promo_paths))
    conn.execute("DELETE FROM clip_groups WHERE folder=?", (folder,))
    conn.execute("DELETE FROM promo_links WHERE folder=?", (folder,))
    for g in groups:
        gid = group_id(folder, g.kind, g.basis, g.name)
        conn.execute(
            "INSERT OR REPLACE INTO clip_groups (id, library, folder, kind, basis, name, topic) "
            "VALUES (?,?,?,?,?,?,?)",
            (gid, library, folder, g.kind, g.basis, g.name, g.topic),
        )
        conn.executemany(
            "INSERT OR REPLACE INTO clip_group_items (group_id, path, position) VALUES (?,?,?)",
            [(gid, p, i) for i, p in enumerate(g.items, 1)],
        )
    conn.executemany(
        "INSERT OR REPLACE INTO promo_links (path, folder, library, series_key, series_library, "
        "series_folder, season, score, evidence) VALUES (?,?,?,?,?,?,?,?,?)",
        [
            (p, folder, library, s.series_key, s.library, s.folder, se, sc, ev)
            for p, s, se, sc, ev in links
        ],
    )
    conn.commit()
    return Counter(
        groups=len(groups),
        clips=sum(len(g.items) for g in groups if g.kind == "clips"),
        standalone=sum(len(g.items) for g in groups if g.kind == "standalone"),
        promos=len(links),
    )
