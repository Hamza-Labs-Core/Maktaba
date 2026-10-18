"""tag: topic/format/audience/occasion tags for every series and loose video.

Runs :mod:`maktaba_pipeline.classify.topic_tags` over what ``detect``
stored: each ``series`` row (one per season) is tagged from its name, its
playlists' titles, its episodes' titles and descriptions, the channel and
folder name, its median duration and its exact air dates; every video that
is in no series (unassigned or a short) is tagged on its own so it can be
used as themed filler.

Channel boilerplate (the footer pasted under every upload) is measured per
folder with :func:`~maktaba_pipeline.classify.topic_tags.boilerplate_phrases`
and ignored, otherwise a hashtag such as ``#السيرة_النبوية_<presenter>``
under every video would make the whole channel "seerah".

Tables (cache-local; see the final report / README for the mapping onto
Maktaba's ``video_classification`` / ``video_topics``):

- ``series_tags(series_id, tag, score, evidence)`` — all namespaces, plus
  one ``genre:<topic>`` row (the primary topic, smart-mix's ``genre``);
- ``video_tags(path, tag, score)`` — same vocabulary for loose videos.

Re-runnable: a folder's rows are replaced on each run, and rows of series
that ``detect`` has since removed are dropped.
"""

from __future__ import annotations

import json
import sqlite3
from collections import Counter
from datetime import date
from pathlib import Path
from statistics import median

from maktaba_pipeline.classify.topic_tags import (
    TAGGER_VERSION,
    Tag,
    boilerplate_phrases,
    tag_series,
    tag_video,
)

SCHEMA = """
CREATE TABLE IF NOT EXISTS series_tags (
    series_id TEXT NOT NULL,
    tag       TEXT NOT NULL,           -- namespace:name (topic|format|audience|occasion|genre)
    score     REAL NOT NULL,           -- 0..1
    evidence  TEXT,
    PRIMARY KEY (series_id, tag)
);
CREATE INDEX IF NOT EXISTS series_tags_tag_idx ON series_tags (tag, score);
CREATE TABLE IF NOT EXISTS video_tags (
    path  TEXT NOT NULL,
    tag   TEXT NOT NULL,
    score REAL NOT NULL,
    PRIMARY KEY (path, tag)
);
CREATE INDEX IF NOT EXISTS video_tags_tag_idx ON video_tags (tag, score);
-- an episode of a series by its own title: a Palestine episode of a talk show, a tajweed
-- lesson inside a lecture series (themed channels take such episodes one by one)
CREATE TABLE IF NOT EXISTS episode_tags (
    path  TEXT NOT NULL,
    tag   TEXT NOT NULL,
    score REAL NOT NULL,
    PRIMARY KEY (path, tag)
);
"""

#: what an episode keeps of its own tags: what it is about, for whom, for when
_EPISODE_NAMESPACES = ("topic:", "audience:", "occasion:")


def _channel_names(conn: sqlite3.Connection, folder: str) -> list[str]:
    row = conn.execute("SELECT channel FROM folders WHERE folder=?", (folder,)).fetchone()
    names = [row[0]] if row and row[0] else []
    base = Path(folder.replace("\\", "/")).name
    if base not in names:
        names.append(base)
    return names


def _short(tags: list[Tag], ns: str, n: int = 3) -> str:
    picked = [t for t in tags if t.tag.startswith(ns + ":")][:n]
    return ", ".join(f"{t.tag.split(':', 1)[1]}({t.score:.2f})" for t in picked) or "-"


def run(conn: sqlite3.Connection, only_folder: str | None) -> None:
    conn.executescript(SCHEMA)
    conn.execute("DELETE FROM series_tags WHERE series_id NOT IN (SELECT id FROM series)")
    folders = [
        r[0]
        for r in conn.execute("SELECT DISTINCT folder FROM files ORDER BY folder")
        if not only_folder or only_folder in r[0]
    ]
    for folder in folders:
        _tag_folder(conn, folder)
    conn.commit()


def _tag_folder(conn: sqlite3.Connection, folder: str) -> None:
    channel = _channel_names(conn, folder)
    rows = conn.execute(
        """SELECT f.path, COALESCE(y.title, f.stem) AS title, y.description,
                  COALESCE(f.duration_s, y.duration_s) AS dur, y.upload_date, y.date_quality,
                  y.tab, f.width, f.height
           FROM files f LEFT JOIN yt_videos y ON y.youtube_id=f.youtube_id WHERE f.folder=?""",
        (folder,),
    ).fetchall()
    by_path = {r["path"]: r for r in rows}
    noise = boilerplate_phrases(r["description"] or "" for r in rows)
    pl_titles = {r[0]: r[1] for r in conn.execute("SELECT playlist_id, title FROM yt_playlists")}

    series = [
        (r["id"], r["name"], json.loads(r["metadata"] or "{}"))
        for r in conn.execute("SELECT id, name, metadata FROM series")
    ]
    series = [s for s in series if s[2].get("folder") == folder]
    # a playlist holding several shows («رمضان 1437», «غزة») names a collection, not
    # each show in it: only a show's own playlists are evidence of its topic
    shows_in: dict[str, set[str]] = {}
    for _sid, nm, md in series:
        for p in md.get("playlists", []):
            shows_in.setdefault(p, set()).add(md.get("series_key") or nm)
    conn.executemany("DELETE FROM series_tags WHERE series_id=?", [(s[0],) for s in series])

    print(f"\n=== tags v{TAGGER_VERSION}: {Path(folder.replace(chr(92), '/')).name}: "
          f"{len(series)} series, {len(noise)} boilerplate phrases ignored"
          + (f" ({', '.join(sorted(noise)[:6])})" if noise else ""))  # fmt: skip
    for sid, name, meta in sorted(series, key=lambda s: (s[1], s[2].get("season") or 0)):
        eps = [
            by_path[p]
            for (p,) in conn.execute(
                "SELECT path FROM series_episodes WHERE series_id=? ORDER BY episode", (sid,)
            )
            if p in by_path
        ]
        durs = [e["dur"] for e in eps if e["dur"]]
        med = meta.get("median_duration_s") or (median(durs) if durs else None)
        dates = [
            date.fromisoformat(e["upload_date"])
            for e in eps
            if e["upload_date"] and e["date_quality"] == "exact"
        ]
        tags = tag_series(
            name,
            playlist_titles=[
                pl_titles[p]
                for p in meta.get("playlists", [])
                if p in pl_titles and len(shows_in.get(p, ())) <= 1
            ],  # fmt: skip
            episode_titles=[e["title"] for e in eps],
            descriptions=[e["description"] or "" for e in eps],
            channel=channel,
            median_duration_s=med,
            kind=meta.get("kind"),
            air_dates=dates,
            noise=noise,
        )
        conn.executemany(
            "INSERT OR REPLACE INTO series_tags (series_id, tag, score, evidence) VALUES (?,?,?,?)",
            [(sid, t.tag, t.score, t.evidence) for t in tags],
        )
        genre = next((t.tag[6:] for t in tags if t.tag.startswith("genre:")), "-")
        label = f"{name} S{meta.get('season', 1)}"
        print(f"  {label[:34]:34} {genre:17} topics: {_short(tags, 'topic')}")
        print(f"  {'':34} {'':17} format: {_short(tags, 'format', 2)} | "
              f"aud: {_short(tags, 'audience', 2)} | occ: {_short(tags, 'occasion', 2)}")  # fmt: skip

    in_series = {
        p
        for (p,) in conn.execute(
            "SELECT e.path FROM series_episodes e JOIN files f ON f.path=e.path WHERE f.folder=?",
            (folder,),
        )
    }
    conn.execute("DELETE FROM episode_tags WHERE path IN (SELECT path FROM files WHERE folder=?)",
                 (folder,))  # fmt: skip
    ep_rows: list[tuple[str, str, float]] = []
    for p in in_series:
        r = by_path.get(p)
        if r is None:
            continue
        for t in tag_video(r["title"], r["description"] or "", r["dur"], channel=channel,
                           noise=noise):  # fmt: skip
            if t.tag.startswith(_EPISODE_NAMESPACES) and t.tag != "audience:general":
                ep_rows.append((p, t.tag, t.score))
    conn.executemany("INSERT OR REPLACE INTO episode_tags (path, tag, score) VALUES (?,?,?)",
                     ep_rows)  # fmt: skip
    print(f"  episodes tagged by their own titles: {len(in_series)} ({len(ep_rows)} tags)")
    shorts = {p for (p,) in conn.execute("SELECT path FROM shorts WHERE folder=?", (folder,))}
    conn.execute("DELETE FROM video_tags WHERE path IN (SELECT path FROM files WHERE folder=?)",
                 (folder,))  # fmt: skip
    genres: Counter[str] = Counter()
    loose = [r for r in rows if r["path"] not in in_series]
    for r in loose:
        is_short = (
            r["path"] in shorts
            or r["tab"] == "shorts"
            or bool(r["width"] and r["height"] and r["height"] > r["width"])
        )
        tags = tag_video(
            r["title"],
            r["description"] or "",
            r["dur"],
            channel=channel,
            is_short=is_short,
            noise=noise,
        )
        conn.executemany(
            "INSERT OR REPLACE INTO video_tags (path, tag, score) VALUES (?,?,?)",
            [(r["path"], t.tag, t.score) for t in tags],
        )
        genres[next((t.tag[6:] for t in tags if t.tag.startswith("genre:")), "(none)")] += 1
    print(f"  loose videos: {len(loose)} ({len(shorts)} shorts) — genre: "
          + ", ".join(f"{g} {n}" for g, n in genres.most_common(12)))  # fmt: skip
