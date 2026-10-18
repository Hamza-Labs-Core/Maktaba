"""``repair-broken``: download again the files ffprobe cannot read.

A file whose probe failed is read again first: a passing failure (a busy share, a
timeout) is no reason to replace it. A file that really is broken (an unfinished
download: "moov atom not found") and came from YouTube is fetched again beside it, put in
its container, checked with ffprobe, and only then put in its place; the broken file is
kept as ``<name>.broken``. A video no longer on YouTube, or a file without a YouTube id,
is reported and left as it is.
"""

from __future__ import annotations

import glob
import json
import os
import sqlite3
import subprocess
import sys
from pathlib import Path

from . import jobs
from .bins import FFMPEG, FFPROBE
from .scan_local import probe_failure

#: the library's own format: at most 1080p; for an .mp4 the streams an mp4 holds first
#: (vp9/opus into mp4 fails to merge on some videos), anything else after
FORMAT = "bv*[height<=1080]+ba/b[height<=1080]/b"
FORMAT_MP4 = "bv*[ext=mp4][height<=1080]+ba[ext=m4a]/b[ext=mp4][height<=1080]/" + FORMAT
#: yt-dlp's work files, never the result
_PARTIAL = (".part", ".ytdl", ".temp")


def broken(conn: sqlite3.Connection) -> list[tuple[str, str | None]]:
    """(path, YouTube id) of every file whose probe failed."""
    return [
        (r[0], r[1])
        for r in conn.execute(
            """SELECT j.path, f.youtube_id FROM index_jobs j LEFT JOIN files f ON f.path = j.path
               WHERE j.stage = 'probe' AND j.state = 'failed' ORDER BY j.path"""
        )
    ]


def check(path: str) -> tuple[float | None, int | None, int | None, str]:
    """(duration, width, height, why it failed) of a file read now."""
    try:
        out = subprocess.run(
            [FFPROBE, "-v", "error", "-select_streams", "v:0",
             "-show_entries", "format=duration:stream=width,height", "-of", "json", path],
            capture_output=True, timeout=120, check=False,
        )  # fmt: skip
    except (subprocess.TimeoutExpired, OSError) as e:
        return None, None, None, f"could not read it ({type(e).__name__})"
    err = out.stderr.decode("utf-8", "replace")
    try:
        j = json.loads(out.stdout)
        dur = float(j["format"]["duration"])
    except (ValueError, KeyError, TypeError):
        return None, None, None, probe_failure(err)
    st = (j.get("streams") or [{}])[0]
    return dur, st.get("width"), st.get("height"), ""


def download_args(youtube_id: str, target: Path, cookies_from_browser: str | None) -> list[str]:
    """yt-dlp argv writing the video beside ``target`` as ``.repair-<id>.<ext>``: a plain
    name (titles with «⧸ ｜ ۞ ＂ ﷺ» failed yt-dlp's ffmpeg step), renamed once checked."""
    ext = target.suffix.lstrip(".").lower() or "mp4"
    # no merge into the target container here: a failed merge loses the download; a
    # different container is remuxed afterwards (repair_one)
    args = [
        sys.executable, "-m", "yt_dlp", "--no-playlist", "--no-progress",
        "-f", FORMAT_MP4 if ext == "mp4" else FORMAT,
        "-o", str(target.with_name(f"{_work(youtube_id)}.%(ext)s")),
    ]  # fmt: skip
    if cookies_from_browser:
        args += ["--cookies-from-browser", cookies_from_browser]
    return [*args, f"https://www.youtube.com/watch?v={youtube_id}"]


def _work(youtube_id: str) -> str:
    """The download's plain working name (the id: letters, digits, - and _)."""
    return ".repair-" + "".join(
        ch for ch in youtube_id if ch.isascii() and (ch.isalnum() or ch in "-_")
    )


def _made(target: Path, youtube_id: str) -> list[Path]:
    """Everything the download left beside ``target``."""
    return sorted(target.parent.glob(glob.escape(_work(youtube_id)) + "*"))


def _clean(target: Path, youtube_id: str) -> None:
    for p in _made(target, youtube_id):
        p.unlink(missing_ok=True)


def _backup_name(target: Path) -> Path:
    backup = target.with_name(target.name + ".broken")
    n = 1
    while backup.exists():
        n += 1
        backup = target.with_name(f"{target.name}.broken{n}")
    return backup


def repair_one(conn: sqlite3.Connection, path: str, youtube_id: str,
               cookies_from_browser: str | None = None) -> str:  # fmt: skip
    """Fetch, check and swap one file; returns what happened (one line)."""
    target = Path(path)
    try:
        out = subprocess.run(download_args(youtube_id, target, cookies_from_browser),
                             capture_output=True, timeout=3600, check=False)  # fmt: skip
    except (subprocess.TimeoutExpired, OSError) as e:
        _clean(target, youtube_id)
        return f"not downloaded: {type(e).__name__}"
    got = [p for p in _made(target, youtube_id) if p.suffix.lower() not in _PARTIAL]
    if out.returncode != 0 or not got:
        err = out.stderr.decode("utf-8", "replace").strip().splitlines()
        why = next((ln for ln in reversed(err) if "ERROR" in ln), err[-1] if err else "")
        _clean(target, youtube_id)
        return f"not downloaded: {why[:160]}"
    fresh = target.with_name(f"{_work(youtube_id)}.final{target.suffix}")
    if got[0] != fresh:  # another container: remux, then with the audio made AAC
        r = None
        for audio in (["-c", "copy"], ["-c:v", "copy", "-c:a", "aac", "-b:a", "160k"]):
            fresh.unlink(missing_ok=True)
            try:
                r = subprocess.run([FFMPEG, "-v", "error", "-y", "-i", str(got[0]),
                                    "-map", "0:v:0", "-map", "0:a:0?", *audio, str(fresh)],
                                   capture_output=True, timeout=1800, check=False)  # fmt: skip
            except (subprocess.TimeoutExpired, OSError):
                r = None
            if r is not None and r.returncode == 0 and fresh.is_file():
                break
        if r is None or r.returncode != 0 or not fresh.is_file():
            _clean(target, youtube_id)
            return f"downloaded as {got[0].suffix}, could not put it in {target.suffix}"
    dur, w, h, why = check(str(fresh))
    if dur is None:
        _clean(target, youtube_id)
        return f"downloaded, but the new copy does not read either ({why}): left as it was"
    backup = _backup_name(target)
    if target.exists():
        os.replace(target, backup)
    os.replace(fresh, target)
    _clean(target, youtube_id)
    st = target.stat()
    conn.execute(
        "UPDATE files SET size=?, mtime=?, duration_s=?, width=?, height=?, probed_size=?, "
        "probed_mtime=? WHERE path=?",
        (st.st_size, st.st_mtime, dur, w, h, st.st_size, st.st_mtime, path),
    )
    jobs.record(conn, path, "probe", 1, st.st_size, st.st_mtime, None)
    conn.commit()
    return f"repaired: {dur / 60:.1f} min, {w}x{h} (old file kept as {backup.name})"


def _record(conn: sqlite3.Connection, path: str, error: str | None, dur: float | None = None,
            w: int | None = None, h: int | None = None) -> None:  # fmt: skip
    row = conn.execute("SELECT size, mtime FROM files WHERE path=?", (path,)).fetchone()
    size, mtime = (row[0], row[1]) if row else (None, None)
    if dur is not None:
        conn.execute(
            "UPDATE files SET duration_s=?, width=?, height=?, probed_size=?, probed_mtime=? "
            "WHERE path=?",
            (dur, w, h, size, mtime, path),
        )
    jobs.record(conn, path, "probe", 1, size, mtime, error)
    conn.commit()


def run(conn: sqlite3.Connection, cookies_from_browser: str | None = None) -> dict[str, int]:
    rows = broken(conn)
    counts = {"repaired": 0, "readable": 0, "failed": 0, "no_id": 0}
    print(f"phase: {len(rows)} files whose probe failed", flush=True)
    for n, (path, yid) in enumerate(rows, 1):
        name = Path(path).name
        at = f"  [{n}/{len(rows)}]"
        dur, w, h, why = check(path)
        if dur is not None:  # a passing failure: the file is fine
            counts["readable"] += 1
            _record(conn, path, None, dur, w, h)
            print(f"{at} ✓  {name}: reads fine now, kept", flush=True)
            continue
        if not why.startswith("broken file"):  # cannot tell now: never replace on a guess
            counts["failed"] += 1
            print(f"{at} !  {name}: {why}; left for a later run", flush=True)
            continue
        if not yid:
            counts["no_id"] += 1
            _record(conn, path, f"{why}; no YouTube id, cannot fetch it again")
            print(f"{at} -  {name}: {why}; no YouTube id, cannot fetch it again", flush=True)
            continue
        what = repair_one(conn, path, yid, cookies_from_browser)
        if what.startswith("repaired"):
            counts["repaired"] += 1
        else:
            counts["failed"] += 1
            _record(conn, path, f"{why}; {what}")
        print(f"{at} {'✓' if what.startswith('repaired') else '!'}  {name}: {what}", flush=True)
    print(f"  ✓  {counts['repaired']} repaired, {counts['readable']} read fine after all, "
          f"{counts['failed']} could not be, {counts['no_id']} without a YouTube id")  # fmt: skip
    return counts
