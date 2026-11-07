"""tools/ci/youtube_hosts.py (story 54.33 T02): the host patterns, the manifest's list, and
the recording proxy (a CONNECT and a plain HTTP request through it to servers on this machine:
each host noted, the bytes relayed unchanged)."""

from __future__ import annotations

import asyncio
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import youtube_hosts as yh  # noqa: E402


def test_a_pattern_matches_its_host_or_any_name_under_its_domain() -> None:
    net = ["www.youtube.com", "*.googlevideo.com"]
    assert yh.allowed("www.youtube.com", net)
    assert yh.allowed("WWW.YouTube.com.", net)
    assert yh.allowed("rr3---sn-4g5e6nzz.googlevideo.com", net)
    assert yh.allowed("a.b.googlevideo.com", net)
    assert not yh.allowed("googlevideo.com", net), "the domain itself is not under it"
    assert not yh.allowed("evilgooglevideo.com", net)
    assert not yh.allowed("youtube.com", net)
    assert not yh.allowed("m.youtube.com", net)
    assert yh.check(["www.youtube.com", "tracker.example"], net) == ["tracker.example"]


def test_the_list_is_the_plugins_manifest() -> None:
    net = yh.manifest_hosts()
    assert "www.youtube.com" in net and "*.googlevideo.com" in net


def test_hosts_only_fails_on_a_host_outside_the_list(tmp_path: Path) -> None:
    ok = tmp_path / "ok.txt"
    ok.write_text("www.youtube.com\ni.ytimg.com\n")
    assert yh.main(["--hosts-only", str(ok)]) == 0
    bad = tmp_path / "bad.txt"
    bad.write_text("www.youtube.com\nads.example.net\n")
    assert yh.main(["--hosts-only", str(bad)]) == 1


async def _echo_server() -> tuple[asyncio.base_events.Server, int]:
    async def serve(r: asyncio.StreamReader, w: asyncio.StreamWriter) -> None:
        data = await r.read(65536)
        if data.startswith(b"GET "):
            line = data.split(b"\r\n", 1)[0]
            body = b"seen " + line
            w.write(b"HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n" % len(body) + body)
        else:
            w.write(b"echo " + data)
        await w.drain()
        w.close()

    server = await asyncio.start_server(serve, "127.0.0.1", 0)
    return server, server.sockets[0].getsockname()[1]


def test_the_proxy_notes_each_host_and_relays_the_bytes() -> None:
    async def go() -> tuple[set[str], bytes, bytes]:
        server, port = await _echo_server()
        rec = yh.Recorder()
        await rec.start()
        try:
            # HTTPS's way: CONNECT, then the bytes (TLS in real life) go through untouched
            r, w = await asyncio.open_connection("127.0.0.1", rec.port)
            w.write(f"CONNECT localhost:{port} HTTP/1.1\r\nHost: localhost:{port}\r\n\r\n".encode())
            await w.drain()
            assert (await r.readuntil(b"\r\n\r\n")).startswith(b"HTTP/1.1 200")
            w.write(b"\x16\x03\x01 opaque")
            await w.drain()
            tunnelled = await r.read(65536)
            w.close()
            # plain HTTP's way: an absolute URL, sent on in origin form
            r, w = await asyncio.open_connection("127.0.0.1", rec.port)
            w.write(f"GET http://127.0.0.1:{port}/a/b?c=1 HTTP/1.1\r\nHost: x\r\n\r\n".encode())
            await w.drain()
            plain = await r.read(65536)
            w.close()
        finally:
            await rec.close()
            server.close()
            await server.wait_closed()
        return rec.hosts, tunnelled, plain

    hosts, tunnelled, plain = asyncio.run(go())
    assert hosts == {"localhost", "127.0.0.1"}
    assert tunnelled == b"echo \x16\x03\x01 opaque"
    assert plain.endswith(b"seen GET /a/b?c=1 HTTP/1.1")


@pytest.mark.parametrize("bad", ["", "GARBAGE\r\n\r\n"])
def test_a_malformed_request_is_dropped(bad: str) -> None:
    async def go() -> set[str]:
        rec = yh.Recorder()
        await rec.start()
        r, w = await asyncio.open_connection("127.0.0.1", rec.port)
        w.write(bad.encode())
        await w.drain()
        w.write_eof()
        await r.read()
        await rec.close()
        return rec.hosts

    assert asyncio.run(go()) == set()


def test_youtubes_bot_check_makes_a_step_inconclusive_not_a_failure(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    wall = "ERROR: [youtube] x: Sign in to confirm you\u2019re not a bot. Use --cookies"

    async def walled(work: Path) -> tuple[set[str], list[str]]:
        return {"www.youtube.com"}, ["youtube_fetch: a download: yt-dlp exited 1: " + wall]

    monkeypatch.setattr(yh, "record", walled)
    assert yh.main([]) == 0

    async def walled_and_outside(work: Path) -> tuple[set[str], list[str]]:
        return {"www.youtube.com", "ads.example.net"}, ["youtube_fetch: " + wall]

    monkeypatch.setattr(yh, "record", walled_and_outside)
    assert yh.main([]) == 1, "a host outside the list fails the night even when walled"

    async def broken(work: Path) -> tuple[set[str], list[str]]:
        return {"www.youtube.com"}, ["youtube_fetch: a download: yt-dlp exited 2: no such option"]

    monkeypatch.setattr(yh, "record", broken)
    assert yh.main([]) == 1
