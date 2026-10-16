"""The control centre over real HTTP: requests not from its own page are refused."""

from __future__ import annotations

import http.client
import json
import socket
import sys
import threading
import time
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))  # pipeline/ (scripts package)

from scripts.mediaintel.serve import serve  # noqa: E402

# a real socket, and HTTPServer.server_bind's getfqdn() can take ~1 s
pytestmark = pytest.mark.integration


def test_server_refuses_foreign_requests(tmp_path: Path) -> None:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        port = s.getsockname()[1]
    reports = tmp_path / "reports"
    threading.Thread(
        target=serve,
        args=(str(tmp_path / "m.db"), None, port),
        kwargs={"reports": reports},
        daemon=True,
    ).start()

    def ask(method: str, headers: dict[str, str], body: str = "") -> int:
        for _ in range(50):
            try:
                c = http.client.HTTPConnection("127.0.0.1", port, timeout=5)
                c.request(method, "/api/reports" if method == "GET" else "/api/run", body,
                          headers)  # fmt: skip
                return c.getresponse().status
            except ConnectionRefusedError:
                time.sleep(0.05)
        raise AssertionError("server did not start")

    host = f"localhost:{port}"
    assert ask("GET", {"Host": host}) == 200
    assert ask("GET", {"Host": f"evil.example:{port}"}) == 403
    assert ask("POST", {"Host": host, "Content-Type": "text/plain"}, "{}") == 403
    json_ = {"Host": host, "Content-Type": "application/json"}
    assert ask("POST", {**json_, "Origin": "https://evil.example"}, "{}") == 403
    assert ask("POST", json_, json.dumps({"folders": ["--plex"]})) == 400
