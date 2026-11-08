"""tools/ci/fetch-pinned.sh: a pinned CI download, checked, retried with backoff.

GitHub's release downloads answered 504 for minutes at a time (2026-11-08) and failed the
Core bundle-size job and the Windows plugin sandbox. The helper keeps a restored file that
has the pin (no download), retries a 5xx, 408, 429 or a dropped connection, stops at once
on any other 4xx, fails on other bytes, and names the URL in an `::error::` when it gives
up. Here against a local server that answers what each test scripts.
"""

from __future__ import annotations

import hashlib
import http.server
import os
import re
import subprocess
import threading
from collections.abc import Iterator
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/ci/fetch-pinned.sh"
BODY = b"pinned archive bytes\n" * 100
SHA = hashlib.sha256(BODY).hexdigest()


class _Server:
    """Answers each request with the next scripted reply: an int status, or "reset"."""

    def __init__(self) -> None:
        self.replies: list[int | str] = []
        self.requests = 0
        server = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self) -> None:  # noqa: N802 (http.server's name)
                server.requests += 1
                reply = server.replies.pop(0) if server.replies else 200
                if reply == "reset":
                    self.close_connection = True
                    self.connection.shutdown(2)
                    return
                assert isinstance(reply, int)
                body = BODY if reply == 200 else b"Gateway Time-out"
                if reply == 299:  # 200 with other bytes
                    reply, body = 200, b"other bytes"
                self.send_response(reply)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *args: object) -> None:
                pass

        self.httpd = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.httpd.server_address[1]}/releases/download/16.15.0/postgresql-16.15.0-x86_64-pc-windows-msvc.zip"
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()


@pytest.fixture
def server() -> Iterator[_Server]:
    s = _Server()
    yield s
    s.httpd.shutdown()


def _run(*args: str) -> subprocess.CompletedProcess[str]:
    env = {**os.environ, "FETCH_PINNED_DELAYS": "0 0 0 0", "RUNNER_OS": "Windows"}
    return subprocess.run(["bash", str(SCRIPT), *args], capture_output=True, text=True, env=env, timeout=60)


def test_retries_a_504_until_it_downloads(server: _Server, tmp_path: Path) -> None:
    server.replies = [504, 504, 200]
    dst = tmp_path / "dl" / "postgresql-16.15.0.zip"
    r = _run(server.url, SHA, str(dst))
    assert r.returncode == 0, r.stderr
    assert server.requests == 3
    assert dst.read_bytes() == BODY
    assert r.stdout.strip() == str(dst)
    assert "HTTP 504, again in 0 s" in r.stderr
    assert not dst.with_name(dst.name + ".part").exists()


@pytest.mark.parametrize("reply", [408, 429, 500, 502, 503, "reset"])
def test_retries_what_another_try_can_change(server: _Server, tmp_path: Path, reply: int | str) -> None:
    server.replies = [reply, 200]
    r = _run(server.url, SHA, str(tmp_path / "f.zip"))
    assert r.returncode == 0, r.stderr
    assert server.requests == 2


@pytest.mark.parametrize("status", [401, 403, 404, 410])
def test_stops_at_once_on_another_4xx(server: _Server, tmp_path: Path, status: int) -> None:
    server.replies = [status]
    dst = tmp_path / "f.zip"
    r = _run(server.url, SHA, str(dst))
    assert r.returncode != 0
    assert server.requests == 1
    assert f"::error::f.zip: {server.url} answered HTTP {status}" in r.stdout
    assert not dst.exists()


def test_gives_up_after_five_attempts_naming_the_url(server: _Server, tmp_path: Path) -> None:
    server.replies = [504] * 10
    dst = tmp_path / "f.zip"
    r = _run(server.url, SHA, str(dst))
    assert r.returncode != 0
    assert server.requests == 5
    assert f"::error::f.zip: {server.url} not downloaded after 5 attempts (last: HTTP 504)" in r.stdout
    assert not dst.exists()


def test_other_bytes_fail_and_leave_nothing(server: _Server, tmp_path: Path) -> None:
    server.replies = [299]
    dst = tmp_path / "f.zip"
    r = _run(server.url, SHA, str(dst))
    assert r.returncode != 0
    assert server.requests == 1
    assert "::error::" in r.stdout and f"the pin says {SHA}" in r.stdout
    assert not dst.exists()
    assert not dst.with_name("f.zip.part").exists()


def test_a_restored_file_with_the_pin_downloads_nothing(server: _Server, tmp_path: Path) -> None:
    dst = tmp_path / "f.zip"
    dst.write_bytes(BODY)
    r = _run(server.url, SHA, str(dst))
    assert r.returncode == 0, r.stderr
    assert server.requests == 0
    assert "cached copy has the pinned SHA-256" in r.stderr


def test_a_restored_file_with_other_bytes_is_fetched_again(server: _Server, tmp_path: Path) -> None:
    dst = tmp_path / "f.zip"
    dst.write_bytes(b"truncated")
    r = _run(server.url, SHA, str(dst))
    assert r.returncode == 0, r.stderr
    assert server.requests == 1
    assert dst.read_bytes() == BODY


def test_the_cache_key_names_tool_version_platform_and_pin() -> None:
    url = "https://github.com/theseus-rs/postgresql-binaries/releases/download/16.15.0/postgresql-16.15.0-aarch64-apple-darwin.tar.gz"
    r = _run("--key", url, SHA)
    assert r.returncode == 0, r.stdout
    assert r.stdout.strip() == f"pinned-windows-postgresql-16.15.0-aarch64-apple-darwin.tar.gz-{SHA[:16]}"


# The workflows put each file where payload.sh looks, under the name payload.sh fetches it
# as: a name that drifted would leave payload.sh downloading it again, unretried.
FETCH_LINE = re.compile(r'\$f "\$(\w+)_URL" "\$\1_SHA256" "\$d/([^"]+)"')
PAYLOAD_FETCH = re.compile(r'fetch "\$(\w+)_URL" "\$\1_SHA256" "([^"]+)"')


@pytest.mark.parametrize("app", ["mac-app", "win-app"])
def test_the_prefetched_names_are_payload_sh_names(app: str) -> None:
    workflow = (ROOT / f".github/workflows/_{app}.yml").read_text(encoding="utf-8")
    payload = (ROOT / f"tools/release/{app}/payload.sh").read_text(encoding="utf-8")
    wanted = dict(PAYLOAD_FETCH.findall(payload))
    found = FETCH_LINE.findall(workflow)
    assert found, f"_{app}.yml prefetches nothing"
    for piece, name in found:
        assert wanted.get(piece) == name, f"_{app}.yml fetches {piece} as {name}, payload.sh as {wanted.get(piece)}"
    assert {p for p, _ in found} == set(wanted), f"_{app}.yml leaves out {set(wanted) - {p for p, _ in found}}"
