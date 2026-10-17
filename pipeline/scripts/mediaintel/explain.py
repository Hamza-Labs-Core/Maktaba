"""explain: everything the cache knows about matching videos, and where they ended up.

The question this answers is "why is this video in (or missing from) that
season?": its title, air date and how sure that date is, length, shape,
playlists and positions, how it was linked to YouTube, its Plex item, and
the detector's verdict (episode N of a season, promo/extra, second upload,
short, or unassigned).
"""

from __future__ import annotations

import json
import sqlite3
from pathlib import Path


def _verdicts(conn: sqlite3.Connection) -> dict[str, str]:
    out: dict[str, str] = {}
    for r in conn.execute(
        """SELECT s.name, s.metadata, e.path, e.episode FROM series s
           JOIN series_episodes e ON e.series_id=s.id"""
    ):
        m = json.loads(r["metadata"])
        att = " (attached)" if r["path"] in (m.get("attached") or []) else ""
        out[r["path"]] = f"{r['name']} S{m['season']} E{r['episode']}{att}"
    for r in conn.execute("SELECT name, metadata FROM series"):
        m = json.loads(r["metadata"])
        for k in m.get("extras") or []:
            out.setdefault(k, f"extra (promo/recap) of {r['name']} S{m['season']}")
        for k in m.get("duplicates") or []:
            out.setdefault(k, f"second upload of an episode in {r['name']} S{m['season']}")
    for (p,) in conn.execute("SELECT path FROM shorts"):
        out.setdefault(p, "short (never an episode)")
    return out


def explain(
    conn: sqlite3.Connection,
    pattern: str,
    folder: str | None = None,
    limit: int = 50,
    *,
    since: str | None = None,
    until: str | None = None,
    min_minutes: float | None = None,
    max_minutes: float | None = None,
    verdict: str | None = None,
) -> None:
    """``since``/``until`` (ISO dates) and the minute bounds narrow the search, e.g. "long
    videos aired after season 1 that ended up nowhere" (``verdict="unassigned"``)."""
    rows = conn.execute(
        """SELECT f.*, y.title AS yt_title, y.upload_date, y.date_quality, y.tab,
                  y.duration_s AS yt_duration
           FROM files f LEFT JOIN yt_videos y ON y.youtube_id=f.youtube_id
           WHERE (f.stem LIKE ? OR y.title LIKE ?) AND (? IS NULL OR f.folder LIKE ?)
             AND (? IS NULL OR y.upload_date >= ?) AND (? IS NULL OR y.upload_date <= ?)
             AND (? IS NULL OR COALESCE(f.duration_s, y.duration_s) >= ? * 60)
             AND (? IS NULL OR COALESCE(f.duration_s, y.duration_s) <= ? * 60)
           ORDER BY y.upload_date, f.stem""",
        (f"%{pattern}%", f"%{pattern}%", folder, f"%{folder}%", since, since, until, until,
         min_minutes, min_minutes, max_minutes, max_minutes),
    ).fetchall()  # fmt: skip
    remote = conn.execute(
        """SELECT y.youtube_id, y.title, y.upload_date, y.date_quality, y.duration_s, y.tab,
                  (SELECT group_concat(p.title || ' #' || i.position, ' / ')
                   FROM yt_playlist_items i JOIN yt_playlists p ON p.playlist_id=i.playlist_id
                   WHERE i.youtube_id=y.youtube_id) AS pls
           FROM yt_videos y
           WHERE y.title LIKE ? AND NOT EXISTS (SELECT 1 FROM files f WHERE f.youtube_id=y.youtube_id)
             AND (? IS NULL OR y.channel_id IN (SELECT channel_id FROM folders WHERE folder LIKE ?))
           ORDER BY y.upload_date LIMIT ?""",
        (f"%{pattern}%", folder, f"%{folder}%", limit),
    ).fetchall()
    if remote:
        print(f"On YouTube but not downloaded ({len(remote)}):")
        for r in remote:
            mins = f"{r['duration_s'] / 60:.0f} min" if r["duration_s"] else "? min"
            print(f"  {r['upload_date'] or '?':10} {mins:>7} {r['tab'] or '-':7} {r['title']}"
                  + (f"   [{r['pls']}]" if r["pls"] else "")
                  + f"   https://youtu.be/{r['youtube_id']}")  # fmt: skip
    if not rows:
        print("no matching downloaded video")
        return
    verdicts = _verdicts(conn)
    if verdict:
        rows = [r for r in rows if verdict in verdicts.get(r["path"], "unassigned (standalone)")]
    rows = rows[:limit]
    print(f"{len(rows)} downloaded videos")
    for r in rows:
        pls = conn.execute(
            """SELECT p.title, i.position FROM yt_playlist_items i
               JOIN yt_playlists p ON p.playlist_id=i.playlist_id
               WHERE i.youtube_id=? AND p.is_real=1""",
            (r["youtube_id"],),
        ).fetchall()
        dur = r["duration_s"] or r["yt_duration"]
        print(f"\n{r['yt_title'] or r['stem']}")
        print(f"  file      {Path(r['folder']).name}/{r['stem']}.{r['ext']}")
        print(f"  aired     {r['upload_date'] or '?'} ({r['date_quality'] or 'no date'})")
        print(
            f"  length    {dur / 60:.1f} min" if dur else "  length    ?",
            f"  {r['width']}x{r['height']}" if r["width"] else "",
        )
        print(f"  youtube   {r['youtube_id'] or '-'} via {r['link_method'] or '-'}"
              f"  tab={r['tab'] or '-'}")  # fmt: skip
        for p in pls:
            print(f"  playlist  {p['title']} #{p['position']}")
        print(f"  plex      {r['plex_library'] or '-'} / {r['plex_rating_key'] or '-'}")
        print(f"  verdict   {verdicts.get(r['path'], 'unassigned (standalone)')}")
