"""scan-local: gather everything the files on disk can tell us.

Per video file: size, mtime, duration (ffprobe), thumbnail + dHash, and the
yt-dlp ``.info.json`` (YouTube id, upload date, playlist + position) when
present. Per folder: the channel it was downloaded from (channel-tab
``.info.json``), real playlist ``.info.json`` files, and the
``ytdl-archive.txt`` order.

Results are cached on (size, mtime): re-running only probes new/changed
files.
"""

from __future__ import annotations

import json
import os
import re
import sqlite3
import subprocess
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

from maktaba_pipeline.audio.chromaprint import (
    AUDIO_FP_VERSION,
    encode,
    ffmpeg_chromaprint_args,
    parse_raw,
    windows,
)
from maktaba_pipeline.discovery.ytdlp_sidecars import (
    THUMB_EXTS,
    PlaylistInfo,
    VideoInfo,
    parse_archive,
    parse_info_json,
)
from maktaba_pipeline.thumbnail.dhash import (
    FRAMES_VERSION,
    dhash_from_gray,
    dhashes_from_frames,
    ffmpeg_frame_args,
    ffmpeg_gray_args,
    frame_times,
)

from . import jobs
from .bins import FFMPEG, FFPROBE

VIDEO_EXTS = (".mp4", ".mkv", ".webm", ".m4v", ".mov", ".avi")
# yt-dlp leaves per-format pieces ("<title>.f251.webm") when a merge didn't finish
_FORMAT_PIECE_RE = re.compile(r"\.f\d{2,4}\.[^.]+$")


def _probe(path: str) -> tuple[float | None, int | None, int | None]:
    """(duration seconds, width, height) of the first video stream."""
    try:
        out = subprocess.run(
            [FFPROBE, "-v", "error", "-select_streams", "v:0",
             "-show_entries", "format=duration:stream=width,height", "-of", "json", path],
            capture_output=True, timeout=120, check=False,
        )  # fmt: skip
        j = json.loads(out.stdout)
    except (ValueError, TypeError, subprocess.TimeoutExpired, OSError):
        return None, None, None
    try:
        dur: float | None = float(j["format"]["duration"])
    except (KeyError, TypeError, ValueError):
        dur = None
    st = (j.get("streams") or [{}])[0]
    return dur, st.get("width"), st.get("height")


def _thumb_dhash(path: str) -> str | None:
    try:
        out = subprocess.run(
            ffmpeg_gray_args(path, binary=FFMPEG), capture_output=True, timeout=60, check=False
        )
        return f"{dhash_from_gray(out.stdout):016x}"
    except (ValueError, subprocess.TimeoutExpired, OSError):
        return None


def _signature(path: str, duration_s: float) -> tuple[str | None, str | None, str]:
    """(audio head, audio tail, frame hashes) of one episode-length video."""
    audio: dict[str, str | None] = {"head": None, "tail": None}
    for which, start, length in windows(duration_s):
        try:
            out = subprocess.run(
                ffmpeg_chromaprint_args(path, start, length, binary=FFMPEG),
                capture_output=True, timeout=180, check=False,
            )  # fmt: skip
            fp = parse_raw(out.stdout)
            audio[which] = encode(fp) if fp else None
        except (subprocess.TimeoutExpired, OSError):
            pass
    raw = b""
    for t in frame_times(duration_s):
        try:
            raw += subprocess.run(
                ffmpeg_frame_args(path, t, binary=FFMPEG),
                capture_output=True,
                timeout=60,
                check=False,
            ).stdout[:72]
        except (subprocess.TimeoutExpired, OSError):
            continue
    frames = ",".join(f"{h:016x}" for h in dhashes_from_frames(raw))
    return audio["head"], audio["tail"], frames


def _upsert_video_info(conn: sqlite3.Connection, v: VideoInfo) -> None:
    conn.execute(
        """INSERT INTO yt_videos (youtube_id, title, upload_date, date_quality, duration_s,
                                  channel_id, channel, description, source)
           VALUES (?,?,?,?,?,?,?,?, 'info_json')
           ON CONFLICT (youtube_id) DO UPDATE SET
             title=excluded.title,
             upload_date=COALESCE(excluded.upload_date, yt_videos.upload_date),
             date_quality=CASE WHEN excluded.upload_date IS NOT NULL THEN 'exact'
                               ELSE yt_videos.date_quality END,
             duration_s=COALESCE(excluded.duration_s, yt_videos.duration_s),
             channel_id=COALESCE(excluded.channel_id, yt_videos.channel_id),
             channel=COALESCE(excluded.channel, yt_videos.channel),
             description=COALESCE(excluded.description, yt_videos.description)""",
        (
            v.youtube_id, v.title, v.upload_date.isoformat() if v.upload_date else None,
            "exact" if v.upload_date else None, v.duration_s, v.channel_id, v.channel,
            v.description,
        ),
    )  # fmt: skip
    if v.playlist_id and v.playlist_title:
        _upsert_playlist(conn, v.playlist_id, v.playlist_title, v.channel_id, v.channel, None)
        conn.execute(
            """INSERT INTO yt_playlist_items (playlist_id, youtube_id, position) VALUES (?,?,?)
               ON CONFLICT DO UPDATE SET position=COALESCE(excluded.position, position)""",
            (v.playlist_id, v.youtube_id, v.playlist_index),
        )


def _upsert_playlist(
    conn: sqlite3.Connection,
    pid: str,
    title: str,
    channel_id: str | None,
    channel: str | None,
    count: int | None,
) -> None:
    from maktaba_pipeline.discovery.ytdlp_sidecars import is_real_playlist

    conn.execute(
        """INSERT INTO yt_playlists (playlist_id, title, channel_id, channel, item_count, is_real, source)
           VALUES (?,?,?,?,?,?, 'info_json')
           ON CONFLICT (playlist_id) DO UPDATE SET title=excluded.title,
             item_count=COALESCE(excluded.item_count, item_count)""",
        (pid, title, channel_id, channel, count, int(is_real_playlist(pid))),
    )


def _work_left(conn: sqlite3.Connection, folder_s: str, *, probe: bool, signatures: bool) -> bool:
    """True when a job of this folder's files is still due: a failed one with attempts
    left, or (for the stages this pass runs) one never run, run on another file state,
    or made by an older stage version."""
    if conn.execute(
        """SELECT 1 FROM index_jobs j JOIN files f ON f.path = j.path
           WHERE f.folder=? AND j.state='failed' AND j.attempts < j.max_attempts LIMIT 1""",
        (folder_s,),
    ).fetchone():
        return True
    stages: list[tuple[str, int, str]] = []
    if probe:
        stages.append(("probe", 1, ""))
        if signatures:
            sig_v = AUDIO_FP_VERSION * 100 + FRAMES_VERSION
            stages.append(("signature", sig_v, " AND f.duration_s >= 180"))
    for stage, version, only in stages:
        if conn.execute(
            f"""SELECT 1 FROM files f LEFT JOIN index_jobs j ON j.path = f.path AND j.stage=?
                WHERE f.folder=?{only} AND (j.path IS NULL OR j.version != ?
                  OR j.size IS NOT f.size OR j.mtime IS NOT f.mtime) LIMIT 1""",
            (stage, folder_s, version),
        ).fetchone():
            return True
    return False


def scan_folder(
    conn: sqlite3.Connection,
    folder: Path,
    *,
    workers: int = 8,
    probe: bool = True,
    signatures: bool = False,
) -> dict[str, int]:
    """``probe=False`` records files without ffprobe (``scan-plex`` can fill length and
    size from Plex; a second pass probes only what is still missing)."""
    folder_s = folder.as_posix()  # stored with '/' on every OS (Windows accepts it)
    jobs.ensure(conn)
    try:
        dir_mtime = folder.stat().st_mtime
    except OSError:
        return {"videos": 0}
    done = conn.execute(
        "SELECT done_mtime, done_probe FROM folders WHERE folder=?", (folder_s,)
    ).fetchone()
    if (
        done
        and done[0] == dir_mtime
        and (done[1] or not probe)
        and not _work_left(conn, folder_s, probe=probe, signatures=signatures)
    ):
        # nothing added, removed or renamed since a complete scan, and no job is due
        # (retry, new stage version): skip the per-file stats. A file rewritten in place
        # under the same name keeps the folder's mtime and is not noticed here.
        return {"videos": 0, "skipped": 1}
    try:
        with os.scandir(folder) as it:
            # DirEntry.stat() is served from the listing on Windows (no round-trip per file)
            entries = {e.name: e for e in it if e.is_file(follow_symlinks=False)}
        names = sorted(entries)
    except OSError:
        return {"videos": 0}
    name_set = set(names)
    stats = {"videos": 0, "probed": 0, "info_json": 0, "thumbs": 0}

    videos = [
        n
        for n in names
        if n.lower().endswith(VIDEO_EXTS)
        and ".part" not in n
        and not n.endswith(".ytdl")
        and not _FORMAT_PIECE_RE.search(n)
    ]
    # forget files that are gone (or were format pieces counted by an older scan)
    present = {(folder / n).as_posix() for n in videos}
    gone = [
        (p,)
        for (p,) in conn.execute("SELECT path FROM files WHERE folder=?", (folder_s,))
        if p not in present
    ]
    conn.executemany("DELETE FROM files WHERE path=?", gone)
    stems = {n.rsplit(".", 1)[0] for n in videos}

    # folder-level json: channel tab or real playlist (stem doesn't belong to a video)
    channel_id = channel_url = channel = None
    for n in names:
        if not n.endswith(".info.json") or n[: -len(".info.json")] in stems:
            continue
        info = parse_info_json((folder / n).read_text(encoding="utf-8", errors="replace"))
        if isinstance(info, PlaylistInfo):
            if info.is_real:
                _upsert_playlist(
                    conn, info.playlist_id, info.title, info.channel_id, info.channel, info.count
                )
                for pos, vid in enumerate(info.entry_ids, 1):
                    conn.execute(
                        "INSERT OR IGNORE INTO yt_playlist_items (playlist_id, youtube_id, position) VALUES (?,?,?)",
                        (info.playlist_id, vid, pos),
                    )
            channel_id = channel_id or info.channel_id
            channel_url = channel_url or info.channel_url
            channel = channel or info.channel
        elif isinstance(info, VideoInfo):
            channel_id = channel_id or info.channel_id
            channel = channel or info.channel
    archive = (
        parse_archive((folder / "ytdl-archive.txt").read_text(errors="replace"))
        if "ytdl-archive.txt" in name_set
        else []
    )
    conn.execute(
        """INSERT INTO folders (folder, channel_id, channel_url, channel, archive_ids) VALUES (?,?,?,?,?)
           ON CONFLICT (folder) DO UPDATE SET channel_id=COALESCE(excluded.channel_id, channel_id),
             channel_url=COALESCE(excluded.channel_url, channel_url),
             channel=COALESCE(excluded.channel, channel), archive_ids=excluded.archive_ids""",
        (folder_s, channel_id, channel_url, channel, json.dumps(archive)),
    )

    todo_probe: list[str] = []
    todo_thumb: list[tuple[str, str]] = []
    for n in videos:
        p = folder / n
        stem, ext = n.rsplit(".", 1)
        try:
            st = entries[n].stat()
        except OSError:
            continue
        stats["videos"] += 1
        thumb = next((stem + e for e in THUMB_EXTS if stem + e in name_set), None)
        thumb_path = (folder / thumb).as_posix() if thumb else None
        row = conn.execute(
            "SELECT probed_size, probed_mtime, thumb_path, thumb_dhash, width, duration_s FROM files WHERE path=?",
            (p.as_posix(),),
        ).fetchone()
        conn.execute(
            """INSERT INTO files (path, folder, stem, ext, size, mtime, thumb_path) VALUES (?,?,?,?,?,?,?)
               ON CONFLICT (path) DO UPDATE SET size=excluded.size, mtime=excluded.mtime,
                 thumb_path=excluded.thumb_path""",
            (p.as_posix(), folder_s, stem, ext.lower(), st.st_size, st.st_mtime, thumb_path),
        )
        if probe and jobs.needed(conn, p.as_posix(), "probe", 1, st.st_size, st.st_mtime):
            todo_probe.append(p.as_posix())
        # a fingerprint only matters next to a season's other thumbnails (a series' shared
        # design), so: not while just listing, and never for shorts (never episodes)
        too_short = row is not None and row["duration_s"] is not None and row["duration_s"] < 180
        if (
            probe
            and thumb_path
            and not too_short
            and jobs.needed(conn, p.as_posix(), "thumbnail", 1, st.st_size, st.st_mtime)
        ):
            todo_thumb.append((p.as_posix(), thumb_path))
        if stem + ".info.json" in name_set:
            info = parse_info_json(
                (folder / (stem + ".info.json")).read_text(encoding="utf-8", errors="replace")
            )
            if isinstance(info, VideoInfo):
                stats["info_json"] += 1
                _upsert_video_info(conn, info)
                conn.execute(
                    "UPDATE files SET youtube_id=?, link_method='info_json' WHERE path=?",
                    (info.youtube_id, p.as_posix()),
                )

    # probing is slow (network share): don't hold the write lock through it; every
    # result below is committed as soon as it arrives
    conn.commit()
    with ThreadPoolExecutor(workers) as pool:
        for path, (dur, w, h) in zip(todo_probe, pool.map(_probe, todo_probe), strict=True):
            size, mtime = conn.execute(
                "SELECT size, mtime FROM files WHERE path=?", (path,)
            ).fetchone()
            conn.execute(
                "UPDATE files SET duration_s=?, width=?, height=?, probed_size=?, probed_mtime=? "
                "WHERE path=?",
                (dur, w, h, size, mtime, path),
            )
            stats["probed"] += 1
            jobs.record(conn, path, "probe", 1, size, mtime,
                        None if dur is not None else "ffprobe gave no duration")  # fmt: skip
            conn.commit()  # per file: a restart never re-probes finished files
        for (path, _), dh in zip(
            todo_thumb, pool.map(_thumb_dhash, [t for _, t in todo_thumb]), strict=True
        ):
            conn.execute("UPDATE files SET thumb_dhash=? WHERE path=?", (dh, path))
            size, mtime = conn.execute(
                "SELECT size, mtime FROM files WHERE path=?", (path,)
            ).fetchone()
            jobs.record(conn, path, "thumbnail", 1, size, mtime,
                        None if dh is not None else "thumbnail unreadable")  # fmt: skip
            stats["thumbs"] += 1
            conn.commit()
    if probe and signatures:
        # every episode-length video (opt-in: the run fingerprints selectively, see signatures.py)
        sig_v = AUDIO_FP_VERSION * 100 + FRAMES_VERSION
        sig_todo = [
            (r[0], float(r[1]))
            for r in conn.execute(
                "SELECT path, duration_s, size, mtime FROM files WHERE folder=? AND duration_s >= 180",
                (folder_s,),
            )
            if jobs.needed(conn, r[0], "signature", sig_v, r[2], r[3])
        ]
        with ThreadPoolExecutor(workers) as pool:
            for (path, _), (head, tail, frames) in zip(
                sig_todo, pool.map(lambda x: _signature(*x), sig_todo), strict=True
            ):
                conn.execute(
                    """UPDATE files SET audio_head=?, audio_tail=?, audio_version=?,
                         frame_hashes=?, frames_version=? WHERE path=?""",
                    (head, tail, AUDIO_FP_VERSION, frames, FRAMES_VERSION, path),
                )
                size, mtime = conn.execute(
                    "SELECT size, mtime FROM files WHERE path=?", (path,)
                ).fetchone()
                jobs.record(conn, path, "signature", sig_v, size, mtime,
                            None if head or tail or frames else "no audio or frames decoded")  # fmt: skip
                conn.commit()
                stats["signatures"] = stats.get("signatures", 0) + 1
    # a listing-only pass over a changed folder leaves its new files unprobed: the
    # earlier complete probe no longer covers it
    conn.execute(
        "UPDATE folders SET done_mtime=?, done_probe=CASE WHEN done_mtime IS ? "
        "THEN MAX(COALESCE(done_probe, 0), ?) ELSE ? END WHERE folder=?",
        (dir_mtime, dir_mtime, int(probe), int(probe), folder_s),
    )
    conn.commit()
    return stats


def _walk_dirs(root: Path) -> list[Path]:
    """``root`` and every folder below it. Uses ``os.scandir`` (the directory listing
    already says what is a folder) instead of ``Path.rglob`` + ``is_dir``, which costs
    one extra network round-trip per *file* on a share."""
    out: list[Path] = []
    stack = [root]
    while stack:
        d = stack.pop()
        out.append(d)
        try:
            with os.scandir(d) as it:
                subs = sorted(Path(e.path) for e in it if e.is_dir(follow_symlinks=False))
        except OSError:
            continue
        stack.extend(reversed(subs))
    return out


def scan_roots(
    conn: sqlite3.Connection,
    roots: list[Path],
    *,
    workers: int = 8,
    probe: bool = True,
    parallel: int = 4,
) -> None:
    """Scan every folder under ``roots``; ``parallel`` folders at a time, each on its own
    connection with ``workers`` ffprobe/ffmpeg threads (the share is latency-bound, so
    overlapping folders keeps it busy)."""
    from concurrent.futures import as_completed

    from .db import connect

    folders = [f for root in roots for f in _walk_dirs(root)]
    if parallel <= 1:
        for folder in folders:
            st = scan_folder(conn, folder, workers=workers, probe=probe)
            if st.get("videos"):
                print(f"{folder}: {st}", flush=True)
        return
    db_path = conn.execute("PRAGMA database_list").fetchone()[2]
    conn.commit()

    def one(folder: Path) -> tuple[Path, dict[str, int]]:
        c = connect(db_path)
        try:
            return folder, scan_folder(c, folder, workers=workers, probe=probe)
        finally:
            c.close()

    with ThreadPoolExecutor(parallel) as pool:
        for k, fut in enumerate(as_completed([pool.submit(one, f) for f in folders]), 1):
            folder, st = fut.result()
            if st.get("videos") or st.get("skipped"):
                print(f"[{k}/{len(folders)}] {folder.as_posix()}: {st}", flush=True)
