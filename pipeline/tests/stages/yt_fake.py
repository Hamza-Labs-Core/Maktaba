"""A fake yt-dlp for the Epic 47 YouTube tests: recorded listings and downloads.

Installed with :func:`maktaba_pipeline.integrations.youtube_download.set_runner`; nothing
reaches YouTube. Listings emulate ``--break-on-existing`` against the archive passed in
the argv; a download writes the video and its sidecars into the ``-o`` folder, or answers
with recorded stderr (per error class) and exit code 1.
"""

from __future__ import annotations

import json
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from maktaba_pipeline.integrations.youtube_download import RunResult

#: recorded yt-dlp stderr per error class (real messages, trimmed)
STDERR: dict[str, str] = {
    "bot_check": (
        "ERROR: [youtube] AAAAAAAAAAA: Sign in to confirm you're not a bot. Use"
        " --cookies-from-browser or --cookies for the authentication."
    ),
    # recorded 2026-10-28 from yt-dlp 2026.08.19 (a curly apostrophe)
    "bot_check_curly": (
        "WARNING: [youtube] No supported JavaScript runtime could be found.\n"
        "ERROR: [youtube] aqz-KE-bpKQ: Sign in to confirm you\u2019re not a bot. Use"
        " --cookies-from-browser or --cookies for the authentication."
    ),
    "unavailable": "ERROR: [youtube] BaW_jenozKc: This video is unavailable",
    "rate_limited": "ERROR: unable to download video data: HTTP Error 429: Too Many Requests",
    "members_only": (
        "ERROR: [youtube] AAAAAAAAAAA: Join this channel to get access to members-only"
        " content like this video, and other exclusive perks."
    ),
    "private": "ERROR: [youtube] AAAAAAAAAAA: Video unavailable. This video is private",
    "age_gate": (
        "ERROR: [youtube] AAAAAAAAAAA: Sign in to confirm your age. This video may be"
        " inappropriate for some users."
    ),
    "geo": (
        "ERROR: [youtube] AAAAAAAAAAA: Video unavailable. The uploader has not made this"
        " video available in your country"
    ),
    "removed": (
        "ERROR: [youtube] AAAAAAAAAAA: Video unavailable. This video has been removed by"
        " the uploader"
    ),
    "terminated": (
        "ERROR: [youtube] AAAAAAAAAAA: Video unavailable. This video is no longer available"
        " because the YouTube account associated with this video has been terminated."
    ),
    "network": (
        "ERROR: [download] Got error: <urlopen error [Errno 60] Operation timed out>."
        " Giving up after 10 retries"
    ),
    "http_503": "ERROR: unable to download video data: HTTP Error 503: Service Unavailable",
    "format": (
        "ERROR: [youtube] AAAAAAAAAAA: Requested format is not available. Use --list-formats"
        " for a list of available formats"
    ),
    "unknown": "ERROR: something nobody expected happened",
}


def entry(
    yid: str,
    title: str = "",
    *,
    upload_date: str | None = None,
    availability: str | None = None,
    live_status: str | None = None,
) -> dict[str, Any]:
    e: dict[str, Any] = {"_type": "url", "ie_key": "Youtube", "id": yid, "title": title or yid}
    if upload_date:
        e["upload_date"] = upload_date
    if availability:
        e["availability"] = availability
    if live_status:
        e["live_status"] = live_status
    return e


def _arg(argv: list[str], flag: str) -> str | None:
    return argv[argv.index(flag) + 1] if flag in argv else None


@dataclass
class FakeYtDlp:
    #: url -> flat entries (newest first) or an error class (``STDERR`` key)
    listings: dict[str, list[dict[str, Any]] | str] = field(default_factory=dict)
    #: youtube id -> a list of answers, one per attempt (the last one repeats):
    #: "ok", an ``STDERR`` key, or "stop" (waits for the cancel flag)
    downloads: dict[str, list[str]] = field(default_factory=dict)
    #: id -> (title, yyyymmdd, duration) for the .info.json
    meta: dict[str, tuple[str, str, int]] = field(default_factory=dict)
    calls: list[list[str]] = field(default_factory=list)
    downloaded: list[str] = field(default_factory=list)
    listed: list[tuple[str, bool]] = field(default_factory=list)
    #: called before each download with the id (a test adds work mid-run)
    before_download: Callable[[str], Awaitable[None]] | None = None
    media_source: Path | None = None
    tries: dict[str, int] = field(default_factory=dict)

    async def __call__(
        self,
        argv: list[str],
        *,
        on_line: Any = None,
        should_stop: Any = None,
        timeout: float = 0.0,
    ) -> RunResult:
        self.calls.append(argv)
        if "--flat-playlist" in argv:
            return self._list(argv)
        return await self._download(argv, on_line, should_stop)

    def _list(self, argv: list[str]) -> RunResult:
        url = argv[-1]
        brk = "--break-on-existing" in argv
        self.listed.append((url, brk))
        got = self.listings.get(url)
        if got is None:
            return RunResult(
                1,
                [],
                f"ERROR: [youtube:tab] x: This channel does not have a {Path(url).name} tab",
            )
        if isinstance(got, str):
            return RunResult(1, [], STDERR[got])
        archive: set[str] = set()
        path = _arg(argv, "--download-archive")
        if path and Path(path).is_file():
            archive = {ln.split()[1] for ln in Path(path).read_text().splitlines() if ln.strip()}
        out: list[str] = []
        for e in got:
            if brk and e["id"] in archive:
                return RunResult(101, out, "")
            out.append(json.dumps(e))
        return RunResult(0, out, "")

    async def _download(self, argv: list[str], on_line: Any, should_stop: Any) -> RunResult:
        yid = argv[-1].rsplit("=", 1)[-1]
        if self.before_download is not None:
            await self.before_download(yid)
        n = self.tries.get(yid, 0)
        self.tries[yid] = n + 1
        plan = self.downloads.get(yid, ["ok"])
        answer = plan[min(n, len(plan) - 1)]
        if answer == "stop":
            assert should_stop is not None
            if await should_stop():
                return RunResult(-15, [], "", stopped=True)
            raise AssertionError("the test meant the job to be cancelled first")
        if answer == "format_ok":  # a format error unless the fallback -f b is asked
            answer = "ok" if _arg(argv, "-f") == "b" else "format"
        if answer != "ok":
            return RunResult(1, [], STDERR[answer])
        template = _arg(argv, "-o")
        assert template is not None
        work = Path(template).parent
        stem = Path(template).name.split(".%(ext)s")[0]
        if on_line is not None:
            await on_line('download:{"downloaded_bytes": 1000, "total_bytes": 2000}')
        title, day, duration = self.meta.get(yid, (f"Title {yid}", "20240701", 60))
        if self.media_source is not None:
            (work / f"{stem}.mp4").write_bytes(self.media_source.read_bytes())
        else:
            (work / f"{stem}.mp4").write_bytes(f"fake video {yid}".encode() * 50)
        info = {"id": yid, "title": title, "upload_date": day, "duration": duration,
                "channel_id": "UCchan0000000000000000"}  # fmt: skip
        (work / f"{stem}.info.json").write_text(json.dumps(info))
        (work / f"{stem}.jpg").write_bytes(b"jpg")
        (work / f"{stem}.ar.srt").write_text("1\n00:00:00,000 --> 00:00:01,000\nhi\n")
        archive = _arg(argv, "--download-archive")
        if archive:
            with open(archive, "a") as f:
                f.write(f"youtube {yid}\n")
        self.downloaded.append(yid)
        return RunResult(0, [f"[download] Destination: {work / stem}.mp4"], "")
