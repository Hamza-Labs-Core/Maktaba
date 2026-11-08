"""tools/release/fetch-pinned.sh: a pinned download (the app payloads' fetch, CI's), checked, retried with backoff.

GitHub's release downloads answered 504 for minutes at a time (2026-11-08) and failed the
Core bundle-size job and the Windows plugin sandbox. The helper keeps a restored file that
has the pin (no download), retries a 5xx, 408, 429 or a dropped connection, stops at once
on any other 4xx, fails on other bytes, and names the URL in an `::error::` when it gives
up. A github.com release download falls back to the API's route to the same asset (504s on
github.com while the API served the file). Here against a local server that answers what
each test scripts.
"""

from __future__ import annotations

import hashlib
import http.server
import json
import os
import re
import subprocess
import threading
from collections.abc import Iterator
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/release/fetch-pinned.sh"
BODY = b"pinned archive bytes\n" * 100
SHA = hashlib.sha256(BODY).hexdigest()


class _Server:
    """Answers each request with the next scripted reply: an int status, or "reset"."""

    def __init__(self) -> None:
        self.replies: list[int | str] = []
        self.requests = 0
        self.api: list[str] = []
        self.asset_replies: list[int] = []
        self.auth: list[str] = []
        self.release = True
        self.asset_name = "cpython-3.12.15+20261003.tar.gz"
        server = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self) -> None:  # noqa: N802 (http.server's name)
                if self.path.startswith("/repos/"):
                    server.api.append(self.path)
                    self._api()
                    return
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

            def _api(self) -> None:
                if self.path == "/repos/o/r/releases/tags/t" and server.release:
                    host = f"http://127.0.0.1:{server.httpd.server_address[1]}"
                    asset = {"name": server.asset_name, "url": f"{host}/repos/o/r/releases/assets/7"}
                    body = json.dumps({"assets": [{"name": "other", "url": "x"}, asset]}).encode()
                    status = 200
                elif (
                    self.path == "/repos/o/r/releases/assets/7" and self.headers["Accept"] == "application/octet-stream"
                ):
                    server.auth.append(self.headers.get("Authorization", ""))
                    status = server.asset_replies.pop(0) if server.asset_replies else 200
                    body = BODY if status == 200 else b"Gateway Time-out"
                else:
                    status, body = 404, b"{}"
                self.send_response(status)
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


def _run(*args: str, **extra: str) -> subprocess.CompletedProcess[str]:
    env = {k: v for k, v in os.environ.items() if k not in ("GITHUB_TOKEN", "GH_TOKEN")}
    env.update({"FETCH_PINNED_DELAYS": "0 0 0 0", "RUNNER_OS": "Windows", **extra})
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
    assert f"::error::f.zip: {server.url} answered HTTP {status}" in r.stderr
    assert not dst.exists()


def test_a_file_url_downloads_and_a_missing_one_stops_at_once(tmp_path: Path) -> None:
    # pins-check.sh's offline test of payload.sh's fetch: curl reports no HTTP code for file://
    src = tmp_path / "asset"
    src.write_bytes(BODY)
    r = _run(f"file://{src}", SHA, str(tmp_path / "out" / "a.tgz"))
    assert r.returncode == 0, r.stderr
    assert (tmp_path / "out" / "a.tgz").read_bytes() == BODY
    r = _run(f"file://{tmp_path}/missing", SHA, str(tmp_path / "out" / "m.tgz"))
    assert r.returncode != 0
    assert "attempt 2" not in r.stderr
    assert "curl exit 37" in r.stderr


def test_a_failure_prints_no_path_on_stdout(server: _Server, tmp_path: Path) -> None:
    # the payload scripts take stdout as the file's path (`tar -xzf "$(fetch ...)"`)
    server.replies = [404]
    r = _run(server.url, SHA, str(tmp_path / "f.zip"))
    assert r.returncode != 0
    assert r.stdout == ""


def test_gives_up_after_five_attempts_naming_the_url(server: _Server, tmp_path: Path) -> None:
    server.replies = [504] * 10
    dst = tmp_path / "f.zip"
    r = _run(server.url, SHA, str(dst))
    assert r.returncode != 0
    assert server.requests == 5
    assert f"::error::f.zip: {server.url} not downloaded after 5 attempts (last: HTTP 504)" in r.stderr
    assert not dst.exists()


def test_other_bytes_fail_and_leave_nothing(server: _Server, tmp_path: Path) -> None:
    server.replies = [299]
    dst = tmp_path / "f.zip"
    r = _run(server.url, SHA, str(dst))
    assert r.returncode != 0
    assert server.requests == 1
    assert "::error::" in r.stderr and f"the pin says {SHA}" in r.stderr
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


def _github(server: _Server) -> tuple[str, dict[str, str]]:
    """A github.com release URL on the test server, and the env that makes it github.com."""
    base = f"http://127.0.0.1:{server.httpd.server_address[1]}"
    url = f"{base}/o/r/releases/download/t/cpython-3.12.15%2B20261003.tar.gz"
    return url, {"FETCH_PINNED_GITHUB": base, "FETCH_PINNED_API": base}


def test_a_github_release_goes_by_the_api_first_with_the_token(server: _Server, tmp_path: Path) -> None:
    server.replies = [504] * 10  # github.com's front door is down: it is not asked at all
    url, env = _github(server)
    dst = tmp_path / "cpython.tar.gz"
    r = _run(url, SHA, str(dst), GITHUB_TOKEN="tok-not-printed", **env)
    assert r.returncode == 0, r.stdout + r.stderr
    assert server.requests == 0
    assert server.api == ["/repos/o/r/releases/tags/t", "/repos/o/r/releases/assets/7"]
    assert server.auth == ["Bearer tok-not-printed"]
    assert dst.read_bytes() == BODY
    assert "downloading by the GitHub API" in r.stderr
    assert "tok-not-printed" not in r.stdout + r.stderr


def test_the_github_url_is_the_fallback_in_the_same_try(server: _Server, tmp_path: Path) -> None:
    server.asset_replies = [504]
    url, env = _github(server)
    dst = tmp_path / "f"
    r = _run(url, SHA, str(dst), **env)
    assert r.returncode == 0, r.stdout + r.stderr
    assert server.requests == 1
    assert "API route HTTP 504; trying" in r.stderr
    assert "attempt 2" not in r.stderr
    assert dst.read_bytes() == BODY


def test_both_routes_down_retries_and_looks_the_asset_up_once(server: _Server, tmp_path: Path) -> None:
    server.replies = [504] * 10
    server.asset_replies = [504] * 10
    url, env = _github(server)
    r = _run(url, SHA, str(tmp_path / "f"), **env)
    assert r.returncode != 0
    assert server.requests == 5
    assert server.api.count("/repos/o/r/releases/tags/t") == 1
    assert server.api.count("/repos/o/r/releases/assets/7") == 5
    assert "not downloaded after 5 attempts (last: API route HTTP 504; HTTP 504)" in r.stderr


def test_a_failed_release_lookup_is_retried_and_the_url_tried(server: _Server, tmp_path: Path) -> None:
    server.replies = [504] * 10
    server.release = False  # the release lookup answers 404
    url, env = _github(server)
    r = _run(url, SHA, str(tmp_path / "f"), **env)
    assert r.returncode != 0
    assert server.requests == 5
    assert all(p == "/repos/o/r/releases/tags/t" for p in server.api) and len(server.api) == 5


def test_an_asset_the_release_lacks_and_a_404_stop_at_once(server: _Server, tmp_path: Path) -> None:
    server.asset_name = "another-file.tar.gz"
    server.replies = [404]
    url, env = _github(server)
    r = _run(url, SHA, str(tmp_path / "f"), **env)
    assert r.returncode != 0
    assert server.requests == 1
    assert "has no asset cpython-3.12.15+20261003.tar.gz" in r.stderr
    assert "answered HTTP 404" in r.stderr


def test_the_api_route_bytes_are_checked_too(server: _Server, tmp_path: Path) -> None:
    url, env = _github(server)
    other = hashlib.sha256(b"x").hexdigest()
    dst = tmp_path / "f"
    r = _run(url, other, str(dst), **env)
    assert r.returncode != 0
    assert server.requests == 0
    assert f"the pin says {other}" in r.stderr
    assert not dst.exists()


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
