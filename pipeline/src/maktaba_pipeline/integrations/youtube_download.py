"""yt-dlp as a subprocess for Epic 47: listing a source, downloading one video.

Everything that runs yt-dlp goes through one injectable runner (:func:`set_runner`), so the
tests drive the stages with recorded output and never reach YouTube. The real runner
streams yt-dlp's stdout line by line (progress), and kills it when the caller's
``should_stop`` says so (a cancelled job): the ``.part`` file stays in the work folder and
yt-dlp's ``--continue`` resumes it on the next run.

* :func:`listing_argv`: ``--flat-playlist -j`` (one JSON object per entry), with
  ``--break-on-existing`` against an archive of the ids already known, so a refresh reads
  only the new head of a channel;
* :func:`download_argv`: the spec's download (the archive, the library's format, the
  sidecars, subtitles, the live filter, polite sleeps, progress as JSON);
* :func:`sanitize_name` and :func:`video_filename`: ``<upload_date> <title> [<id>].mp4``.
"""

from __future__ import annotations

import asyncio
import contextlib
import importlib.util
import json
import os
import re
import shlex
import sys
import unicodedata
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import dataclass, field, replace
from datetime import date
from pathlib import Path
from typing import Any, Protocol

from ..engine.media import FFMPEG
from ..library_mgmt.repair import format_for, plain_name

__all__ = [
    "DEFAULT_OPTIONS",
    "DownloadOptions",
    "RunResult",
    "YtDlpRunner",
    "download_argv",
    "listing_argv",
    "parse_progress",
    "reset_runner",
    "run_ytdlp",
    "sanitize_name",
    "set_runner",
    "unavailable",
    "video_filename",
    "ytdlp_command",
]

#: a command line for yt-dlp (``MAKTABA_YTDLP="/opt/bin/yt-dlp"``); default: this Python's
#: ``yt_dlp`` module (the ``mediaintel`` extra)
YTDLP_ENV = "MAKTABA_YTDLP"
LIVE_FILTER = "!is_live & live_status!=is_upcoming"
PROGRESS_PREFIX = "download:"


@dataclass(frozen=True, slots=True)
class DownloadOptions:
    """A source's download options (``youtube_sources.options`` over the defaults)."""

    max_height: int = 1080
    sub_langs: str = "ar,en"
    auto_subs: bool = True
    audio_only: bool = False
    cookies_from_browser: str | None = None
    limit_rate: str | None = None

    @classmethod
    def from_json(cls, value: Mapping[str, Any] | None) -> DownloadOptions:
        """Options from a stored blob; a value of the wrong type keeps the default."""
        out = DEFAULT_OPTIONS
        if not isinstance(value, Mapping):
            return out
        h = value.get("max_height")
        if isinstance(h, int) and not isinstance(h, bool) and 144 <= h <= 4320:
            out = replace(out, max_height=h)
        langs = value.get("sub_langs")
        if isinstance(langs, list):
            langs = ",".join(str(x) for x in langs if isinstance(x, str))
        if isinstance(langs, str) and re.fullmatch(r"[\w.,*-]*", langs):
            out = replace(out, sub_langs=langs)
        for key in ("auto_subs", "audio_only"):
            if isinstance(value.get(key), bool):
                out = replace(out, **{key: value[key]})
        browser = value.get("cookies_from_browser")
        if isinstance(browser, str) and re.fullmatch(r"[\w:+./-]{1,100}", browser):
            out = replace(out, cookies_from_browser=browser)
        rate = value.get("limit_rate")
        if isinstance(rate, str) and re.fullmatch(r"\d+(?:\.\d+)?[KMG]?", rate):
            out = replace(out, limit_rate=rate)
        return out


DEFAULT_OPTIONS = DownloadOptions()


def ytdlp_command() -> list[str]:
    configured = os.environ.get(YTDLP_ENV, "").strip()
    if configured:
        return shlex.split(configured)
    return [sys.executable, "-m", "yt_dlp"]


def unavailable() -> str | None:
    """Why this worker cannot run yt-dlp, or None."""
    if os.environ.get(YTDLP_ENV, "").strip():
        return None
    if importlib.util.find_spec("yt_dlp") is None:
        return "yt-dlp is not installed (pip install 'maktaba-pipeline[mediaintel]')"
    return None


def _ffmpeg_args() -> list[str]:
    return ["--ffmpeg-location", FFMPEG] if os.path.isabs(FFMPEG) else []


def listing_argv(
    url: str,
    *,
    archive: str | None = None,
    break_on_existing: bool = False,
    approximate_dates: bool = True,
    cookies_from_browser: str | None = None,
) -> list[str]:
    """yt-dlp listing ``url`` flat, one JSON object per entry on stdout."""
    args = [
        *ytdlp_command(), "--flat-playlist", "-j", "--no-warnings", "--ignore-no-formats-error",
        "--sleep-requests", "1",
    ]  # fmt: skip
    if approximate_dates:
        args += ["--extractor-args", "youtubetab:approximate_date"]
    if archive is not None:
        args += ["--download-archive", archive]
        if break_on_existing:
            args.append("--break-on-existing")
    if cookies_from_browser:
        args += ["--cookies-from-browser", cookies_from_browser]
    return [*args, url]


def download_argv(
    youtube_id: str,
    work: Path,
    *,
    archive: str | None,
    options: DownloadOptions = DEFAULT_OPTIONS,
    fallback_format: bool = False,
) -> list[str]:
    """yt-dlp downloading one video into the local folder ``work`` under a plain name
    (yt-dlp's merge fails writing to the share, and on titles such as «⧸ ｜ ۞ ＂»).
    ``fallback_format``: ``-f b``, the retry when the library's format is not offered."""
    if options.audio_only:
        fmt = "ba[ext=m4a]/ba/b"
    else:
        fmt = "b" if fallback_format else format_for("mp4", max_height=options.max_height)
    args = [*ytdlp_command(), "--no-playlist", "--continue"]
    if archive is not None:
        args += ["--download-archive", archive]
    args += ["-f", fmt]
    if not options.audio_only:
        args += ["--merge-output-format", "mp4"]
    args += ["--write-info-json", "--write-thumbnail", "--convert-thumbnails", "jpg"]
    if options.sub_langs:
        args.append("--write-subs")
        if options.auto_subs:
            args.append("--write-auto-subs")
        args += ["--sub-langs", options.sub_langs, "--convert-subs", "srt"]
    args += [
        "--match-filter", LIVE_FILTER,
        "--retries", "10", "--fragment-retries", "10",
        "--sleep-requests", "1", "--sleep-interval", "5", "--max-sleep-interval", "30",
    ]  # fmt: skip
    if options.limit_rate:
        args += ["--limit-rate", options.limit_rate]
    if options.cookies_from_browser:
        args += ["--cookies-from-browser", options.cookies_from_browser]
    args += [
        *_ffmpeg_args(),
        "--newline", "--progress-template", PROGRESS_PREFIX + "%(progress)j",
        "-o", str(work / f"{plain_name(youtube_id)}.%(ext)s"),
        f"https://www.youtube.com/watch?v={youtube_id}",
    ]  # fmt: skip
    return args


def parse_progress(line: str) -> dict[str, Any] | None:
    """A ``download:{...}`` progress line as a dict (None for any other line)."""
    if not line.startswith(PROGRESS_PREFIX):
        return None
    try:
        doc = json.loads(line[len(PROGRESS_PREFIX) :])
    except ValueError:
        return None
    return doc if isinstance(doc, dict) else None


# ------------------------------------------------------------------ names

#: Make-It-Offline's invalid characters (Windows' set, which the share also refuses)
_BAD = re.compile(r'[\x00-\x1f\x7f<>:"/\\|?*]')
_SPACES = re.compile(r"\s+")


def _clip_utf8(text: str, max_bytes: int) -> str:
    raw = text.encode("utf-8")
    if len(raw) <= max_bytes:
        return text
    return raw[:max_bytes].decode("utf-8", errors="ignore")


def sanitize_name(name: str | None, *, max_bytes: int = 180, fallback: str = "Unknown") -> str:
    """A folder or file name from a title (port of Make-It-Offline's
    ``SanitizeFolderName``): NFC, invalid characters to ``_``, whitespace collapsed,
    no leading or trailing dots and spaces, at most ``max_bytes`` of UTF-8 (Arabic titles
    take two bytes a letter; the share's limit is 255 a name)."""
    text = unicodedata.normalize("NFC", name or "")
    text = _BAD.sub("_", text)
    text = _SPACES.sub(" ", text).strip().strip(".").strip()
    text = _clip_utf8(text, max_bytes).rstrip(". ")
    return text or fallback


def video_filename(youtube_id: str, title: str | None, upload_date: date | None, ext: str) -> str:
    """``<yyyy-mm-dd> <title> [<id>].<ext>``: the id keeps two same-titled videos apart."""
    day = upload_date.isoformat() if upload_date else "0000-00-00"
    safe_id = plain_name(youtube_id)[2:]
    return f"{day} {sanitize_name(title, max_bytes=160, fallback=safe_id)} [{safe_id}].{ext}"


# ------------------------------------------------------------------ the runner


@dataclass(slots=True)
class RunResult:
    returncode: int
    stdout: list[str] = field(default_factory=list)
    stderr: str = ""
    #: killed because ``should_stop`` said so
    stopped: bool = False


LineFn = Callable[[str], Awaitable[None]]
StopFn = Callable[[], Awaitable[bool]]


class YtDlpRunner(Protocol):
    async def __call__(
        self,
        argv: list[str],
        *,
        on_line: LineFn | None = None,
        should_stop: StopFn | None = None,
        timeout: float = 6 * 3600.0,
    ) -> RunResult: ...


STOP_POLL_SEC = 2.0


async def _real_runner(
    argv: list[str],
    *,
    on_line: LineFn | None = None,
    should_stop: StopFn | None = None,
    timeout: float = 6 * 3600.0,
) -> RunResult:
    try:
        proc = await asyncio.create_subprocess_exec(
            *argv,
            stdin=asyncio.subprocess.DEVNULL,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            limit=16 * 1024 * 1024,  # one ``-j`` line of a long playlist entry
        )
    except OSError as exc:
        return RunResult(127, [], str(exc))
    assert proc.stdout is not None and proc.stderr is not None
    out: list[str] = []
    err_task = asyncio.ensure_future(proc.stderr.read())
    stopped = False

    async def read_out() -> None:
        assert proc.stdout is not None
        async for raw in proc.stdout:
            line = raw.decode("utf-8", errors="replace").rstrip("\r\n")
            if line.startswith(PROGRESS_PREFIX):
                if on_line is not None:
                    await on_line(line)
                continue
            out.append(line)
            if on_line is not None:
                await on_line(line)

    reader = asyncio.ensure_future(read_out())
    loop = asyncio.get_running_loop()
    deadline = loop.time() + timeout
    try:
        while not reader.done():
            await asyncio.wait({reader}, timeout=STOP_POLL_SEC)
            if reader.done():
                break
            if should_stop is not None and await should_stop():
                stopped = True
                break
            if loop.time() > deadline:
                break
    finally:
        if not reader.done():
            with contextlib.suppress(ProcessLookupError):
                proc.terminate()
            try:
                await asyncio.wait_for(proc.wait(), timeout=10)
            except (TimeoutError, asyncio.TimeoutError):  # noqa: UP041
                with contextlib.suppress(ProcessLookupError):
                    proc.kill()
            reader.cancel()
            with contextlib.suppress(asyncio.CancelledError, Exception):
                await reader
    code = await proc.wait()
    err = (await err_task).decode("utf-8", errors="replace")
    if not stopped and code == 0 and not reader.done():
        code = 124
    return RunResult(code, out, err, stopped)


_runner: YtDlpRunner = _real_runner


def set_runner(runner: YtDlpRunner | None) -> YtDlpRunner:
    """Replace the runner (tests); None restores the real one. Returns the previous one."""
    global _runner
    previous = _runner
    _runner = runner or _real_runner
    return previous


def reset_runner() -> None:
    set_runner(None)


async def run_ytdlp(
    argv: list[str],
    *,
    on_line: LineFn | None = None,
    should_stop: StopFn | None = None,
    timeout: float = 6 * 3600.0,
) -> RunResult:
    return await _runner(argv, on_line=on_line, should_stop=should_stop, timeout=timeout)
