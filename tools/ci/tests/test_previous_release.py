"""tools/release/linux-app/previous-release.sh: the latest release's Linux payload for e2e.sh.

A plain curl of github.com's release download got a 504 and failed the Linux app job
(2026-11-08). The script looks the latest release up (retried), fetches checksums.txt pinned
by the digest the release lists and the payload pinned by its checksums.txt line, both
through fetch-pinned.sh (retried, the API's route first), and unpacks the payload. Here
against a local server standing in for api.github.com and github.com.
"""

from __future__ import annotations

import hashlib
import http.server
import io
import json
import os
import platform
import subprocess
import tarfile
import threading
from collections.abc import Iterator
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/release/linux-app/previous-release.sh"
TAG = "v1.2.3"
ASSET = f"maktaba-linux-payload-1.2.3-{platform.machine()}.tar.gz"


def _payload() -> bytes:
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        data = b"#!/bin/sh\necho server\n"
        info = tarfile.TarInfo("server/bin/maktaba-server")
        info.size, info.mode = len(data), 0o755
        tar.addfile(info, io.BytesIO(data))
    return buf.getvalue()


PAYLOAD = _payload()


class _Server:
    def __init__(self) -> None:
        self.latest_replies: list[int] = []
        self.download_replies: list[int] = []
        self.payload = PAYLOAD
        self.list_payload = True
        self.checksums = f"{hashlib.sha256(PAYLOAD).hexdigest()}  ./{ASSET}\nabc  ./other\n".encode()
        self.digest: str | None = None  # None: the checksums' own SHA-256
        self.downloads: list[str] = []
        self.auth: list[str] = []
        server = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self) -> None:  # noqa: N802 (http.server's name)
                status, body = 404, b"{}"
                if self.path == "/repos/o/r/releases/latest":
                    server.auth.append(self.headers.get("Authorization", ""))
                    status = server.latest_replies.pop(0) if server.latest_replies else 200
                    digest = server.digest if server.digest is not None else "sha256:" + hashlib.sha256(server.checksums).hexdigest()
                    assets = [{"name": "checksums.txt", "digest": digest}]
                    if server.list_payload:
                        assets.append({"name": ASSET, "digest": "sha256:" + hashlib.sha256(server.payload).hexdigest()})
                    body = json.dumps({"tag_name": TAG, "assets": assets}).encode() if status == 200 else b"no"
                elif self.path.startswith(f"/o/r/releases/download/{TAG}/"):
                    name = self.path.rsplit("/", 1)[1]
                    server.downloads.append(name)
                    status = server.download_replies.pop(0) if server.download_replies else 200
                    if status == 200:
                        body = server.checksums if name == "checksums.txt" else server.payload
                self.send_response(status)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *args: object) -> None:
                pass

        self.httpd = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.base = f"http://127.0.0.1:{self.httpd.server_address[1]}"
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()


@pytest.fixture
def server() -> Iterator[_Server]:
    s = _Server()
    yield s
    s.httpd.shutdown()


def _run(server: _Server, dst: Path, **extra: str) -> subprocess.CompletedProcess[str]:
    env = {k: v for k, v in os.environ.items() if k not in ("GITHUB_TOKEN", "GH_TOKEN")}
    env.update(
        {
            "FETCH_PINNED_DELAYS": "0 0 0 0",
            "PREVIOUS_RELEASE_DELAYS": "0 0 0 0",
            "FETCH_PINNED_API": server.base,
            "FETCH_PINNED_GITHUB": server.base,
            **extra,
        }
    )
    return subprocess.run(["bash", str(SCRIPT), "o/r", str(dst)], capture_output=True, text=True, env=env, timeout=60)


def test_fetches_checks_and_unpacks_the_payload(server: _Server, tmp_path: Path) -> None:
    dst = tmp_path / "previous"
    r = _run(server, dst, GITHUB_TOKEN="tok-not-printed")
    assert r.returncode == 0, r.stderr
    assert r.stdout.strip() == f"{dst}/server"
    assert (dst / "server/bin/maktaba-server").is_file()
    assert not (dst / ASSET).exists()
    assert server.downloads == ["checksums.txt", ASSET]
    assert server.auth == ["Bearer tok-not-printed"]
    assert "tok-not-printed" not in r.stdout + r.stderr


def test_a_504_on_a_download_is_retried(server: _Server, tmp_path: Path) -> None:
    server.download_replies = [504, 200, 504, 504, 200]
    r = _run(server, tmp_path / "p")
    assert r.returncode == 0, r.stderr
    assert server.downloads == ["checksums.txt"] * 2 + [ASSET] * 3


def test_the_lookup_is_retried(server: _Server, tmp_path: Path) -> None:
    server.latest_replies = [504, 502]
    r = _run(server, tmp_path / "p")
    assert r.returncode == 0, r.stderr
    assert r.stdout.strip().endswith("/server")
    assert "again in 0 s" in r.stderr


def test_a_lookup_that_never_answers_gives_no_previous_release(server: _Server, tmp_path: Path) -> None:
    server.latest_replies = [504] * 10
    r = _run(server, tmp_path / "p")
    assert r.returncode == 0, r.stderr
    assert r.stdout == ""
    assert "did not answer after 5 tries" in r.stderr
    assert server.downloads == []


def test_a_release_without_this_machine_s_payload_gives_nothing(server: _Server, tmp_path: Path) -> None:
    server.list_payload = False
    r = _run(server, tmp_path / "p")
    assert r.returncode == 0, r.stderr
    assert r.stdout == ""
    assert server.downloads == []


def test_a_payload_that_is_not_its_checksums_line_fails(server: _Server, tmp_path: Path) -> None:
    server.payload = b"other bytes"
    r = _run(server, tmp_path / "p")
    assert r.returncode == 1
    assert r.stdout == ""
    assert "could not be downloaded with its checksums.txt SHA-256" in r.stderr


def test_checksums_that_are_not_the_release_s_digest_fail(server: _Server, tmp_path: Path) -> None:
    server.digest = "sha256:" + hashlib.sha256(b"x").hexdigest()
    r = _run(server, tmp_path / "p")
    assert r.returncode == 1
    assert "checksums.txt but it could not be downloaded" in r.stderr
    assert server.downloads == ["checksums.txt"]


def test_no_digest_for_the_checksums_fails(server: _Server, tmp_path: Path) -> None:
    server.digest = ""
    r = _run(server, tmp_path / "p")
    assert r.returncode == 1
    assert "no SHA-256 digest for checksums.txt" in r.stderr


def test_checksums_without_the_payload_s_line_fail(server: _Server, tmp_path: Path) -> None:
    server.checksums = b"abc  ./other\n"
    r = _run(server, tmp_path / "p")
    assert r.returncode == 1
    assert f"has no line for {ASSET}" in r.stderr


def test_e2e_runs_it() -> None:
    e2e = (ROOT / "tools/release/linux-app/e2e.sh").read_text(encoding="utf-8")
    assert '"$SCRIPT_DIR/previous-release.sh" "$REPO" "$SCRATCH/previous"' in e2e
    assert "releases/download" not in e2e
