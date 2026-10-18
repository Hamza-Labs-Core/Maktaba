"""Download a broken file again from YouTube and put it in its place.

Shared by the queue engine's ``repair_broken`` stage and the stand-alone mediaintel tool
(``scripts/mediaintel/repair_broken.py``), which keep their own bookkeeping (the database
rows) and call this for everything that touches files:

* :func:`check`: ffprobe a file now (a passing failure, a busy share, is no reason to replace it);
* :func:`fetch_and_swap`: yt-dlp into a local folder under a plain name (H.264 first, at most
  ``max_height``), put in the file's container, checked with ffprobe, copied beside the
  original on the share, checked there, and only then swapped in; the broken file is kept as
  ``<name>.broken`` (when asked).

Subprocesses go through the injectable ``run`` (``subprocess.run``'s signature), so tests
need neither yt-dlp, ffmpeg nor the network.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from ..engine.media import FFMPEG, FFPROBE

__all__ = [
    "FORMAT",
    "FORMAT_MP4",
    "PARTIAL",
    "Probed",
    "RepairOutcome",
    "backup_name",
    "check",
    "download_args",
    "fetch_and_swap",
    "format_for",
    "made",
    "plain_name",
    "probe_failure",
]

#: the library's own format: at most 1080p, H.264 first (what the rest of the library is
#: and what the GPU decodes; YouTube's 1080p is often AV1), mp4-ready streams for an .mp4
FORMAT = "bv*[height<=1080]+ba/b[height<=1080]/b"
FORMAT_MP4 = (
    "bv*[ext=mp4][vcodec^=avc1][height<=1080]+ba[ext=m4a]/"
    "bv*[ext=mp4][height<=1080]+ba[ext=m4a]/b[ext=mp4][height<=1080]/" + FORMAT
)
#: yt-dlp's work files, never the result
PARTIAL = (".part", ".ytdl", ".temp")

Run = Callable[..., Any]


def format_for(ext: str, *, max_height: int = 1080, h264_first: bool = True) -> str:
    """yt-dlp ``-f`` for a file of container ``ext``: at most ``max_height``; for ``mp4``
    mp4-ready streams, H.264 (avc1) first when ``h264_first``."""
    h = int(max_height)
    plain = f"bv*[height<={h}]+ba/b[height<={h}]/b"
    if ext != "mp4":
        return plain
    avc = f"bv*[ext=mp4][vcodec^=avc1][height<={h}]+ba[ext=m4a]/" if h264_first else ""
    return avc + f"bv*[ext=mp4][height<={h}]+ba[ext=m4a]/b[ext=mp4][height<={h}]/" + plain


def probe_failure(stderr: str) -> str:
    """The job error for a file ffprobe could not measure: a broken file says so (an
    unfinished download has no index: "moov atom not found"), anything else is generic."""
    last = next(
        (ln.split("] ", 1)[-1].strip() for ln in reversed(stderr.splitlines()) if ln.strip()), ""
    )
    if "moov atom not found" in stderr:
        return "broken file: unfinished download (moov atom not found)"
    if "Invalid data found" in stderr or "EBML header parsing failed" in stderr:
        return f"broken file: {last}"
    return f"ffprobe gave no duration{': ' + last if last else ''}"


@dataclass(frozen=True, slots=True)
class Probed:
    """A file read now: ``duration`` is None (with ``why``) when it could not be."""

    duration: float | None
    width: int | None
    height: int | None
    why: str = ""

    @property
    def readable(self) -> bool:
        return self.duration is not None


def check(path: str, *, ffprobe: str = FFPROBE, run: Run = subprocess.run) -> Probed:
    """ffprobe ``path`` now."""
    try:
        out = run(
            [ffprobe, "-v", "error", "-select_streams", "v:0",
             "-show_entries", "format=duration:stream=width,height", "-of", "json", path],
            capture_output=True, timeout=120, check=False,
        )  # fmt: skip
    except (subprocess.TimeoutExpired, OSError) as e:
        return Probed(None, None, None, f"could not read it ({type(e).__name__})")
    err = out.stderr.decode("utf-8", "replace")
    try:
        j = json.loads(out.stdout)
        dur = float(j["format"]["duration"])
    except (ValueError, KeyError, TypeError):
        return Probed(None, None, None, probe_failure(err))
    st = (j.get("streams") or [{}])[0]
    return Probed(dur, st.get("width"), st.get("height"))


def plain_name(youtube_id: str) -> str:
    """A plain file name for the download (the id: letters, digits, - and _)."""
    return "v-" + "".join(ch for ch in youtube_id if ch.isascii() and (ch.isalnum() or ch in "-_"))


def download_args(
    youtube_id: str,
    target: Path,
    cookies_from_browser: str | None,
    work: Path,
    *,
    max_height: int = 1080,
    h264_first: bool = True,
) -> list[str]:
    """yt-dlp argv writing the video into the local folder ``work`` under a plain name:
    yt-dlp's merge step fails writing to the NAS share, and on titles with «⧸ ｜ ۞ ＂»."""
    ext = target.suffix.lstrip(".").lower() or "mp4"
    args = [
        sys.executable, "-m", "yt_dlp", "--no-playlist", "--no-progress",
        "-f", format_for(ext, max_height=max_height, h264_first=h264_first),
        "-o", str(work / f"{plain_name(youtube_id)}.%(ext)s"),
    ]  # fmt: skip
    if cookies_from_browser:
        args += ["--cookies-from-browser", cookies_from_browser]
    return [*args, f"https://www.youtube.com/watch?v={youtube_id}"]


def made(work: Path) -> list[Path]:
    """The downloaded files in ``work`` (never yt-dlp's work files)."""
    return sorted(p for p in work.iterdir() if p.suffix.lower() not in PARTIAL)


def backup_name(target: Path) -> Path:
    """``<name>.broken``, or ``.broken2`` ... : a second backup never overwrites the first."""
    backup = target.with_name(target.name + ".broken")
    n = 1
    while backup.exists():
        n += 1
        backup = target.with_name(f"{target.name}.broken{n}")
    return backup


@dataclass(frozen=True, slots=True)
class RepairOutcome:
    """What happened to one file. ``ok``: it was replaced (``probed`` is the new file)."""

    ok: bool
    message: str
    probed: Probed | None = None
    backup: Path | None = None
    size: int | None = None
    mtime: float | None = None


def fetch_and_swap(
    path: str,
    youtube_id: str,
    work: Path,
    *,
    cookies_from_browser: str | None = None,
    max_height: int = 1080,
    h264_first: bool = True,
    keep_broken: bool = True,
    download_timeout_s: float = 3600.0,
    run: Run = subprocess.run,
    ffmpeg: str = FFMPEG,
    ffprobe: str = FFPROBE,
) -> RepairOutcome:
    """Fetch, check and swap one file (``work`` is an empty local folder); never raises
    for a download or file problem, it says what happened. The broken file is only moved
    or removed once the new copy is on the share and as big as the checked local one."""
    target = Path(path)
    try:
        out = run(
            download_args(
                youtube_id, target, cookies_from_browser, work,
                max_height=max_height, h264_first=h264_first,
            ),
            capture_output=True, timeout=download_timeout_s, check=False,
        )  # fmt: skip
    except (subprocess.TimeoutExpired, OSError) as e:
        return RepairOutcome(False, f"not downloaded: {type(e).__name__}")
    got = made(work)
    if out.returncode != 0 or not got:
        err = out.stderr.decode("utf-8", "replace").strip().splitlines()
        why = next((ln for ln in reversed(err) if "ERROR" in ln), err[-1] if err else "")
        return RepairOutcome(False, f"not downloaded: {why[:160]}")
    local = work / f"final{target.suffix}"
    if got[0].suffix.lower() == target.suffix.lower():
        os.replace(got[0], local)
    else:  # another container: remux, then with the audio made AAC
        r = None
        for audio in (["-c", "copy"], ["-c:v", "copy", "-c:a", "aac", "-b:a", "160k"]):
            local.unlink(missing_ok=True)
            try:
                r = run([ffmpeg, "-v", "error", "-y", "-i", str(got[0]),
                         "-map", "0:v:0", "-map", "0:a:0?", *audio, str(local)],
                        capture_output=True, timeout=1800, check=False)  # fmt: skip
            except (subprocess.TimeoutExpired, OSError):
                r = None
            if r is not None and r.returncode == 0 and local.is_file():
                break
        if r is None or r.returncode != 0 or not local.is_file():
            return RepairOutcome(
                False, f"downloaded as {got[0].suffix}, could not put it in {target.suffix}"
            )
    probed = check(str(local), ffprobe=ffprobe, run=run)
    if not probed.readable:
        return RepairOutcome(
            False,
            f"downloaded, but the new copy does not read either ({probed.why}): left as it was",
        )
    # onto the share beside the file, checked there too, then swapped in
    fresh = target.with_name(f".{plain_name(youtube_id)}.repair{target.suffix}")
    try:
        shutil.copyfile(local, fresh)
    except OSError as e:
        fresh.unlink(missing_ok=True)
        return RepairOutcome(False, f"could not copy it to the share ({e})")
    if fresh.stat().st_size != local.stat().st_size:
        fresh.unlink(missing_ok=True)
        return RepairOutcome(False, "the copy on the share is incomplete: left as it was")
    backup: Path | None = None
    if target.exists():
        if keep_broken:
            backup = backup_name(target)
            os.replace(target, backup)
        else:
            target.unlink()
    os.replace(fresh, target)
    st = target.stat()
    dur = probed.duration or 0.0
    kept = f" (old file kept as {backup.name})" if backup else ""
    return RepairOutcome(
        True,
        f"repaired: {dur / 60:.1f} min, {probed.width}x{probed.height}{kept}",
        probed,
        backup,
        st.st_size,
        st.st_mtime,
    )
