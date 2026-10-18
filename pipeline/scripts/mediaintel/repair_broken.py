"""``repair-broken``: download again the files ffprobe cannot read.

A file whose probe failed is read again first: a passing failure (a busy share, a
timeout) is no reason to replace it. A file that really is broken (an unfinished
download: "moov atom not found") and came from YouTube is fetched again beside it, put in
its container, checked with ffprobe, and only then put in its place; the broken file is
kept as ``<name>.broken``. A video no longer on YouTube, or a file without a YouTube id,
is reported and left as it is.
"""

from __future__ import annotations

import json
import os
import shutil
import sqlite3
import subprocess
import sys
import tempfile
from pathlib import Path

from . import jobs
from .bins import FFMPEG, FFPROBE
from .scan_local import probe_failure

#: the library's own format: at most 1080p, H.264 first (what the rest of the library is
#: and what the GPU decodes; YouTube's 1080p is often AV1), mp4-ready streams for an .mp4
FORMAT = "bv*[height<=1080]+ba/b[height<=1080]/b"
FORMAT_MP4 = (
    "bv*[ext=mp4][vcodec^=avc1][height<=1080]+ba[ext=m4a]/"
    "bv*[ext=mp4][height<=1080]+ba[ext=m4a]/b[ext=mp4][height<=1080]/" + FORMAT
)
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


def download_args(youtube_id: str, target: Path, cookies_from_browser: str | None,
                  work: Path) -> list[str]:  # fmt: skip
    """yt-dlp argv writing the video into the local folder ``work`` under a plain name:
    yt-dlp's merge step fails writing to the NAS share, and on titles with «⧸ ｜ ۞ ＂»."""
    ext = target.suffix.lstrip(".").lower() or "mp4"
    args = [
        sys.executable, "-m", "yt_dlp", "--no-playlist", "--no-progress",
        "-f", FORMAT_MP4 if ext == "mp4" else FORMAT,
        "-o", str(work / f"{_plain(youtube_id)}.%(ext)s"),
    ]  # fmt: skip
    if cookies_from_browser:
        args += ["--cookies-from-browser", cookies_from_browser]
    return [*args, f"https://www.youtube.com/watch?v={youtube_id}"]


def _plain(youtube_id: str) -> str:
    """A plain file name for the download (the id: letters, digits, - and _)."""
    return "v-" + "".join(ch for ch in youtube_id if ch.isascii() and (ch.isalnum() or ch in "-_"))


def _made(work: Path) -> list[Path]:
    """The downloaded files in ``work`` (never yt-dlp's work files)."""
    return sorted(p for p in work.iterdir() if p.suffix.lower() not in _PARTIAL)


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
    work = Path(tempfile.mkdtemp(prefix="maktaba-repair-"))
    try:
        return _repair_in(conn, path, youtube_id, cookies_from_browser, work)
    finally:
        shutil.rmtree(work, ignore_errors=True)


def _repair_in(conn: sqlite3.Connection, path: str, youtube_id: str,
               cookies_from_browser: str | None, work: Path) -> str:  # fmt: skip
    target = Path(path)
    try:
        out = subprocess.run(download_args(youtube_id, target, cookies_from_browser, work),
                             capture_output=True, timeout=3600, check=False)  # fmt: skip
    except (subprocess.TimeoutExpired, OSError) as e:
        return f"not downloaded: {type(e).__name__}"
    got = _made(work)
    if out.returncode != 0 or not got:
        err = out.stderr.decode("utf-8", "replace").strip().splitlines()
        why = next((ln for ln in reversed(err) if "ERROR" in ln), err[-1] if err else "")
        return f"not downloaded: {why[:160]}"
    local = work / f"final{target.suffix}"
    if got[0].suffix.lower() == target.suffix.lower():
        os.replace(got[0], local)
    else:  # another container: remux, then with the audio made AAC
        r = None
        for audio in (["-c", "copy"], ["-c:v", "copy", "-c:a", "aac", "-b:a", "160k"]):
            local.unlink(missing_ok=True)
            try:
                r = subprocess.run([FFMPEG, "-v", "error", "-y", "-i", str(got[0]),
                                    "-map", "0:v:0", "-map", "0:a:0?", *audio, str(local)],
                                   capture_output=True, timeout=1800, check=False)  # fmt: skip
            except (subprocess.TimeoutExpired, OSError):
                r = None
            if r is not None and r.returncode == 0 and local.is_file():
                break
        if r is None or r.returncode != 0 or not local.is_file():
            return f"downloaded as {got[0].suffix}, could not put it in {target.suffix}"
    dur, w, h, why = check(str(local))
    if dur is None:
        return f"downloaded, but the new copy does not read either ({why}): left as it was"
    # onto the share beside the file, checked there too, then swapped in
    fresh = target.with_name(f".{_plain(youtube_id)}.repair{target.suffix}")
    try:
        shutil.copyfile(local, fresh)
    except OSError as e:
        fresh.unlink(missing_ok=True)
        return f"could not copy it to the share ({e})"
    if fresh.stat().st_size != local.stat().st_size:
        fresh.unlink(missing_ok=True)
        return "the copy on the share is incomplete: left as it was"
    backup = _backup_name(target)
    if target.exists():
        os.replace(target, backup)
    os.replace(fresh, target)
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
