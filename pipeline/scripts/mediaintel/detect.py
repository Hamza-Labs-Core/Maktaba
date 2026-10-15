"""detect: run series detection per folder (channel) and store the result."""

from __future__ import annotations

import hashlib
import json
import sqlite3
from datetime import date
from pathlib import Path

from maktaba_pipeline.audio.chromaprint import decode
from maktaba_pipeline.classify.series_detector import (
    DETECTOR_VERSION,
    Detection,
    DetectorConfig,
    PlaylistFacts,
    VideoFacts,
    detect,
)


def load_facts(
    conn: sqlite3.Connection, folder: str
) -> tuple[list[VideoFacts], list[PlaylistFacts], list[str]]:
    rows = conn.execute(
        """SELECT f.path, COALESCE(y.title, f.stem) AS title, COALESCE(f.duration_s, y.duration_s) AS dur,
                  y.upload_date, y.date_quality, f.thumb_dhash, y.tab, f.width, f.height,
                  f.audio_head, f.audio_tail, f.frame_hashes
           FROM files f LEFT JOIN yt_videos y ON y.youtube_id=f.youtube_id WHERE f.folder=?""",
        (folder,),
    ).fetchall()
    videos = [
        VideoFacts(
            key=r["path"],
            title=r["title"],
            duration_s=r["dur"],
            air_date=date.fromisoformat(r["upload_date"]) if r["upload_date"] else None,
            date_exact=r["date_quality"] == "exact",
            dhash=int(r["thumb_dhash"], 16) if r["thumb_dhash"] else None,
            is_short=r["tab"] == "shorts"
            or bool(r["width"] and r["height"] and r["height"] > r["width"]),
            audio_head=tuple(decode(r["audio_head"])),
            audio_tail=tuple(decode(r["audio_tail"])),
            frames=tuple(int(h, 16) for h in (r["frame_hashes"] or "").split(",") if h),
        )
        for r in rows
    ]
    pl_rows = conn.execute(
        """SELECT p.playlist_id, p.title, f.path, i.position
           FROM yt_playlists p JOIN yt_playlist_items i ON i.playlist_id=p.playlist_id
           JOIN files f ON f.youtube_id=i.youtube_id
           WHERE p.is_real=1 AND f.folder=? ORDER BY p.playlist_id, i.position""",
        (folder,),
    ).fetchall()
    by_pl: dict[str, tuple[str, list[tuple[str, int | None]]]] = {}
    for r in pl_rows:
        by_pl.setdefault(r["playlist_id"], (r["title"], []))[1].append((r["path"], r["position"]))
    playlists = [PlaylistFacts(pid, t, tuple(m)) for pid, (t, m) in by_pl.items()]
    ch = conn.execute("SELECT channel FROM folders WHERE folder=?", (folder,)).fetchone()
    presenter = [ch["channel"]] if ch and ch["channel"] else []
    presenter.append(Path(folder).name)
    return videos, playlists, presenter


def store(conn: sqlite3.Connection, library: str, folder: str, det: Detection) -> None:
    conn.execute("DELETE FROM shorts WHERE folder=?", (folder,))
    conn.executemany(
        "INSERT INTO shorts (path, folder) VALUES (?,?)", [(k, folder) for k in det.shorts]
    )
    conn.execute(
        "DELETE FROM series WHERE library=? AND id NOT IN "
        "(SELECT series_id FROM series_episodes WHERE is_user_override=1)",
        (library,),
    )
    for ser in det.series:
        for se in ser.seasons:
            sid = hashlib.sha1(f"{folder}|{ser.key}|{ser.kind}|{se.number}".encode()).hexdigest()[
                :16
            ]
            meta = {
                "series_key": ser.key, "kind": ser.kind, "season": se.number,
                "season_count": len(ser.seasons), "playlists": se.playlists,
                "extras": se.extras, "attached": se.attached, "duplicates": se.duplicates,
                "first_air": se.first_air.isoformat() if se.first_air else None,
                "last_air": se.last_air.isoformat() if se.last_air else None,
                "median_duration_s": se.median_duration_s, "folder": folder,
            }  # fmt: skip
            year = se.first_air.year if se.first_air else None
            conn.execute(
                "INSERT OR REPLACE INTO series (id, library, name, year, metadata, detector_version) "
                "VALUES (?,?,?,?,?,?)",
                (
                    sid,
                    library,
                    ser.name,
                    year,
                    json.dumps(meta, ensure_ascii=False),
                    DETECTOR_VERSION,
                ),
            )
            for n, path in enumerate(se.episodes, 1):
                conn.execute(
                    "INSERT OR IGNORE INTO series_episodes (series_id, path, season, episode) "
                    "VALUES (?,?,?,?)",
                    (sid, path, se.number, n),
                )
    conn.commit()


def run(
    conn: sqlite3.Connection, only_folder: str | None, min_episode_minutes: float = 3.0
) -> None:
    folders = [
        r[0]
        for r in conn.execute("SELECT DISTINCT folder FROM files ORDER BY folder")
        if not only_folder or only_folder in r[0]
    ]
    for folder in folders:
        videos, playlists, presenter = load_facts(conn, folder)
        cfg = DetectorConfig(min_episode_s=min_episode_minutes * 60)
        det = detect(videos, playlists, presenter_words=presenter, config=cfg)
        lib = conn.execute(
            "SELECT plex_library FROM files WHERE folder=? AND plex_library IS NOT NULL LIMIT 1",
            (folder,),
        ).fetchone()
        library = lib[0] if lib else Path(folder).name
        store(conn, library, folder, det)
        eps = sum(s.episode_count for s in det.series if s.kind != "clips")
        print(f"\n=== {library}: {len(videos)} videos, {len(det.series)} series, {eps} episodes, "
              f"{len(det.unassigned)} unassigned, {len(det.shorts)} shorts excluded")  # fmt: skip
        for ser in det.series:
            seasons = ", ".join(
                f"S{se.number}:{len(se.episodes)}"
                + (f"(+{len(se.attached)})" if se.attached else "")
                + (f"[{se.first_air:%Y-%m}]" if se.first_air else "")
                for se in ser.seasons
            )
            extras = sum(len(se.extras) for se in ser.seasons)
            print(
                f"  {ser.kind:10} {ser.name[:60]:60} {seasons}"
                + (f"  extras={extras}" if extras else "")
            )
