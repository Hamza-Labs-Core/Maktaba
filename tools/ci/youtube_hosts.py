"""The hosts yt-dlp contacts for tv.maktaba.youtube, against its manifest (story 54.33 T02).

``tv.maktaba.youtube`` runs in the ``net`` runner group, whose sandbox opens outbound
network as a whole: it cannot filter by host. Its manifest's ``[trusted] net`` list is what
the plugin is meant to reach, reviewed with the manifest and shown on the Plugins page.
Nothing else compares that list with what yt-dlp really reaches. This does, every night
(.github/workflows/youtube-hosts-nightly.yml):

1. a recording proxy on 127.0.0.1 notes the host of every request (an HTTP ``CONNECT`` for
   HTTPS, the absolute URL for plain HTTP) and relays the bytes unchanged: it reads no TLS
   and keeps no content;
2. the real yt-dlp runs, through it, the command lines the plugin's stages build, from the
   same functions (``youtube_sync``'s and ``youtube_backfill``'s listing,
   ``youtube_fetch``'s download): a channel listing and one public test video downloaded
   with its thumbnail and subtitles;
3. every host seen must match a ``net`` pattern (``*.googlevideo.com`` matches one or more
   labels before the domain). A host outside the list fails the run, naming it: the fix is
   a reviewed edit of the manifest, never a wider sandbox.

    uv run --project pipeline --extra mediaintel python tools/ci/youtube_hosts.py [--json out]

``--hosts-only FILE`` checks a list of hosts (one per line) without running yt-dlp.
"""

from __future__ import annotations

import argparse
import asyncio
import contextlib
import json
import os
import subprocess
import sys
import tempfile
import tomllib
from collections.abc import Iterable
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = ROOT / "plugins" / "first-party" / "youtube" / "manifest.toml"

#: a public video that has stayed up since 2005 (19 seconds), and its channel
TEST_VIDEO = "jNQXAC9IVRw"
TEST_CHANNEL = "https://www.youtube.com/@jawed/videos"


def manifest_hosts(path: Path = MANIFEST) -> list[str]:
    """The ``[trusted] net`` patterns of the plugin's manifest."""
    doc = tomllib.loads(path.read_text())
    net = (doc.get("trusted") or {}).get("net") or []
    if not isinstance(net, list) or not all(isinstance(h, str) for h in net):
        raise SystemExit(f"{path}: [trusted] net is not a list of hosts")
    return [h.lower() for h in net]


def allowed(host: str, patterns: Iterable[str]) -> bool:
    """Whether ``host`` matches a pattern: exactly, or ``*.domain`` for any name under it."""
    h = host.lower().rstrip(".")
    return any(h.endswith(p[1:]) if p.startswith("*.") else h == p for p in patterns)


class Recorder:
    """A forwarding proxy that notes each request's host and relays its bytes."""

    def __init__(self) -> None:
        self.hosts: set[str] = set()
        self.port = 0
        self._server: asyncio.base_events.Server | None = None

    async def start(self) -> None:
        self._server = await asyncio.start_server(self._client, "127.0.0.1", 0)
        self.port = self._server.sockets[0].getsockname()[1]

    async def close(self) -> None:
        if self._server is not None:
            self._server.close()
            await self._server.wait_closed()

    async def _client(self, r: asyncio.StreamReader, w: asyncio.StreamWriter) -> None:
        try:
            head = await r.readuntil(b"\r\n\r\n")
        except (asyncio.IncompleteReadError, asyncio.LimitOverrunError):
            w.close()
            return
        line = head.split(b"\r\n", 1)[0].decode("latin-1")
        try:
            method, target, _ = line.split(" ", 2)
        except ValueError:
            w.close()
            return
        if method.upper() == "CONNECT":
            host, _, port = target.rpartition(":")
            rest = b""
        else:
            # plain HTTP: an absolute URL, sent on as an origin-form request
            scheme, _, after = target.partition("://")
            hostport, _, path = after.partition("/")
            host, _, port = hostport.rpartition(":") if ":" in hostport else (hostport, "", "80")
            port = port or ("443" if scheme == "https" else "80")
            rest = head.replace(target.encode("latin-1"), b"/" + path.encode("latin-1"), 1)
        host = host.strip("[]").lower()
        self.hosts.add(host)
        try:
            ur, uw = await asyncio.open_connection(host, int(port))
        except (OSError, ValueError):
            with contextlib.suppress(Exception):
                w.write(b"HTTP/1.1 502 Bad Gateway\r\n\r\n")
                await w.drain()
            w.close()
            return
        if method.upper() == "CONNECT":
            w.write(b"HTTP/1.1 200 Connection established\r\n\r\n")
            await w.drain()
        else:
            uw.write(rest)
            await uw.drain()
        await asyncio.gather(_pipe(r, uw), _pipe(ur, w), return_exceptions=True)


async def _pipe(r: asyncio.StreamReader, w: asyncio.StreamWriter) -> None:
    try:
        while data := await r.read(65536):
            w.write(data)
            await w.drain()
    finally:
        with contextlib.suppress(Exception):
            w.close()


def stage_commands(work: Path) -> list[tuple[str, list[str]]]:
    """The command lines the plugin's stages run, built by the stages' own functions."""
    from maktaba_pipeline.integrations.youtube_download import (
        DownloadOptions,
        download_argv,
        listing_argv,
    )

    listing = listing_argv(TEST_CHANNEL)
    # the newest few entries are enough to reach every host a listing uses
    listing = [*listing[:-1], "--playlist-end", "3", listing[-1]]
    opts = DownloadOptions(max_height=144, sub_langs="en", auto_subs=True)
    return [
        ("youtube_sync / youtube_backfill: a channel listing", listing),
        ("youtube_fetch: a download", download_argv(TEST_VIDEO, work, archive=None, options=opts)),
    ]


#: what yt-dlp prints when YouTube refuses the runner's address (data-centre addresses get it
#: often): the night is inconclusive for that step, not a host problem
BOT_WALL = ("confirm you’re not a bot", "confirm you're not a bot", "Sign in to confirm")


def blocked(message: str) -> bool:
    return any(b in message for b in BOT_WALL)


async def record(work: Path) -> tuple[set[str], list[str]]:
    rec = Recorder()
    await rec.start()
    proxy = f"http://127.0.0.1:{rec.port}"
    env = {**os.environ, "HTTPS_PROXY": proxy, "HTTP_PROXY": proxy, "ALL_PROXY": proxy}
    env.pop("NO_PROXY", None)
    failures: list[str] = []
    try:
        for what, argv in stage_commands(work):
            argv = [*argv[:-1], "--proxy", proxy, argv[-1]]
            p = await asyncio.create_subprocess_exec(
                *argv, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE
            )
            _, err = await p.communicate()
            if p.returncode != 0:
                failures.append(f"{what}: yt-dlp exited {p.returncode}: {err.decode()[-800:]}")
    finally:
        await rec.close()
    return rec.hosts, failures


def check(hosts: Iterable[str], patterns: list[str]) -> list[str]:
    return sorted(h for h in set(hosts) if not allowed(h, patterns))


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--json", help="write the hosts seen and the verdict here")
    ap.add_argument("--hosts-only", help="check these hosts (one per line); run no yt-dlp")
    args = ap.parse_args(argv)
    patterns = manifest_hosts()
    failures: list[str] = []
    if args.hosts_only:
        hosts = {x.strip() for x in Path(args.hosts_only).read_text().splitlines() if x.strip()}
    else:
        with tempfile.TemporaryDirectory(prefix="yt-hosts-") as d:
            hosts, failures = asyncio.run(record(Path(d)))
    outside = check(hosts, patterns)
    if args.json:
        Path(args.json).write_text(
            json.dumps(
                {
                    "hosts": sorted(hosts),
                    "outside": outside,
                    "net": patterns,
                    "failures": failures,
                    "inconclusive": [f for f in failures if blocked(f)],
                },
                indent=2,
            )
        )
    print("hosts yt-dlp contacted:", ", ".join(sorted(hosts)) or "none")
    walled = [f for f in failures if blocked(f)]
    errors = [f for f in failures if not blocked(f)]
    for f in walled:
        print(
            "::warning::YouTube refused this runner (its bot check), so this step is inconclusive"
            " tonight; the hosts it reached before are still checked: " + f.replace("\n", " ")
        )
    for f in errors:
        print("::error::" + f.replace("\n", " "))
    for h in outside:
        print(f"::error::yt-dlp contacted {h}, which tv.maktaba.youtube's [trusted] net does not list")
    if not hosts and not failures:
        print("::error::no host was recorded: the proxy was not used")
        return 1
    return 1 if outside or errors else 0


if __name__ == "__main__":
    sys.exit(main())
