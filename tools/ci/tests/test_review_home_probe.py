"""Epic 58 story 58.04 T01 and T03: the review home's scheduled check, against a stand-in home and
cloud. A healthy home passes every check; each way it can fail a reviewer fails the check that
names it: an admin reviewer, an item without its Arabic title or licence note, a stream that will
not start, a cloud code that never reaches the TV."""

from __future__ import annotations

import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

import pytest

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools" / "review_home"))

import content  # noqa: E402
import probe  # noqa: E402

MANIFEST = content.load()


class Home:
    """What the stand-in home and cloud answer; each test breaks one thing."""

    def __init__(self) -> None:
        self.is_admin = False
        self.videos = [
            {"id": f"v{i}", "title": probe.content_title(it), "description": "Licence: CC BY 4.0. x\nالترخيص: CC BY 4.0. x"}
            for i, it in enumerate(MANIFEST["items"])
        ]
        self.session_status = 200
        self.polls_pending = 1
        self.connections: list[dict[str, str]] = []
        self.approved: list[str] = []
        self.redeemed: list[str] = []
        self.logouts = 0
        self.sessions: list[str] = []
        self.deleted: list[str] = []


def handler(home: Home) -> type[BaseHTTPRequestHandler]:
    class H(BaseHTTPRequestHandler):
        def _send(self, status: int, body: Any = None) -> None:
            raw = b"" if body is None else json.dumps(body).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def _body(self) -> Any:
            n = int(self.headers.get("Content-Length") or 0)
            return json.loads(self.rfile.read(n)) if n else None

        def _authed(self) -> bool:
            return self.headers.get("Authorization") == "Bearer viewer-token"

        def do_GET(self) -> None:  # noqa: N802
            if self.path == "/api/discovery/home":
                return self._send(200, {"id": "h1", "name": "Review", "api_level": 3})
            if self.path.startswith("/api/videos"):
                if not self._authed():
                    return self._send(401)
                if "cursor=" not in self.path:
                    return self._send(200, {"items": home.videos[:4], "next": "p2"})
                return self._send(200, {"items": home.videos[4:], "next": None})
            self._send(404)

        def do_DELETE(self) -> None:  # noqa: N802
            home.deleted.append(self.path.rsplit("/", 1)[-1])
            self._send(204)

        def do_POST(self) -> None:  # noqa: N802
            b = self._body()
            if self.path == "/api/auth/login":
                if b != {"username": "reviewer", "password": "the-viewer-pass-123"}:
                    return self._send(401)
                return self._send(200, {"access_token": "viewer-token", "user": {"is_admin": home.is_admin}})
            if self.path == "/api/stream/sessions":
                if not self._authed():
                    return self._send(401)
                home.sessions.append(b["video_id"])
                if home.session_status != 200:
                    return self._send(home.session_status)
                return self._send(200, {"session_id": f"s-{b['video_id']}", "mode": "direct"})
            if self.path == "/v1/tv/codes":
                return self._send(201, {"code": "ABCD-2345", "poll_secret": "ps", "interval": 5})
            if self.path == "/api/pairing/cloud-approve":
                if not self._authed():
                    return self._send(401)
                home.approved.append(b["code"])
                return self._send(200, {"status": "approved"})
            if self.path == "/v1/tv/codes/poll":
                if not home.approved:
                    return self._send(202, {"status": "pending"})
                if home.polls_pending > 0:
                    home.polls_pending -= 1
                    return self._send(202, {"status": "pending"})
                return self._send(200, {"status": "approved", "grant": "G", "connections": home.connections})
            if self.path == "/api/pairing/device/token":
                home.redeemed.append(b["device_code"])
                return self._send(200, {"access_token": "tv-token"})
            if self.path == "/api/auth/logout":
                home.logouts += 1
                return self._send(204)
            self._send(404)

        def log_message(self, *_: Any) -> None:
            pass

    return H


@pytest.fixture
def stand_in() -> Any:
    home = Home()
    srv = ThreadingHTTPServer(("127.0.0.1", 0), handler(home))
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    url = f"http://127.0.0.1:{srv.server_address[1]}"
    # the direct name does not answer; the relay does (the TV tries them in order)
    home.connections = [{"kind": "direct", "url": "http://127.0.0.1:9"}, {"kind": "relay", "url": url}]
    p = probe.Probe(home=url, cloud=url, viewer="reviewer", password="the-viewer-pass-123", manifest=MANIFEST, sleep=lambda _: None)
    p.http.timeout = 2
    yield home, p
    srv.shutdown()


def test_a_healthy_home_passes_every_check(stand_in: Any) -> None:
    home, p = stand_in
    results = p.run()
    assert {k: ok for k, (ok, _) in results.items()} == dict.fromkeys(probe.CHECKS, True), results
    assert home.approved == ["ABCD-2345"] and home.redeemed == ["G"]
    assert "relay" in results["cloud_code"][1] and "direct: unreachable" in results["cloud_code"][1]
    # one session per test file, each closed; the TV and the reviewer sign out
    assert len(home.sessions) == len(content.COVERS) == len(home.deleted)
    assert home.logouts == 2


def test_an_admin_reviewer_fails_the_sign_in(stand_in: Any) -> None:
    home, p = stand_in
    home.is_admin = True
    results = p.run()
    assert results["signin"] == (False, "the reviewer's account is an admin; it must be a viewer")
    # nothing past a refused sign-in runs: a viewer's checks would mean nothing as an admin
    assert results["cloud_code"] == (False, "not run: the sign-in failed")


def test_a_missing_title_or_licence_fails_the_content_check(stand_in: Any) -> None:
    home, p = stand_in
    home.videos[0]["title"] = "Spring"
    home.videos[1]["description"] = "a film"
    ok, note = p.run()["content"]
    assert not ok
    assert "Spring: needs an English and an Arabic title" in note
    assert "no licence note" in note
    assert "not listed: spring" in note


def test_a_stream_that_will_not_start_fails_the_streams_check(stand_in: Any) -> None:
    home, p = stand_in
    home.session_status = 503
    ok, note = p.run()["streams"]
    assert not ok and "the stream session answered 503" in note


def test_a_code_that_never_arrives_fails_the_cloud_code_check(stand_in: Any) -> None:
    home, p = stand_in
    home.polls_pending = 1000
    p.poll_for = 20
    ok, note = p.run()["cloud_code"]
    assert not ok and "the cloud's poll answered 202" in note


def test_no_connection_answering_fails_the_cloud_code_check(stand_in: Any) -> None:
    home, p = stand_in
    home.connections = [{"kind": "direct", "url": "http://127.0.0.1:9"}]
    ok, note = p.run()["cloud_code"]
    assert not ok and "no connection redeemed the grant (direct: unreachable)" in note


def test_metrics_name_each_check() -> None:
    text = probe.metrics({"discovery": (True, ""), "cloud_code": (False, "x")}, 1700000000.5)
    assert text.splitlines() == [
        'review_home_probe_success{check="discovery"} 1',
        'review_home_probe_success{check="cloud_code"} 0',
        "review_home_probe_last_run_seconds 1700000000",
    ]


def test_the_workflow_runs_the_probe_every_15_minutes() -> None:
    import yaml

    wf = yaml.safe_load((ROOT / ".github" / "workflows" / "review-home-probe.yml").read_text(encoding="utf-8"))
    on = wf.get("on") or wf.get(True)
    assert on["schedule"] == [{"cron": "*/15 * * * *"}]
    job = wf["jobs"]["probe"]
    assert job["if"] == "vars.REVIEW_HOME_PROBE == 'true'"
    run = next(s for s in job["steps"] if "probe.py" in str(s.get("run", "")))
    assert run["env"]["REVIEW_HOME_VIEWER_PASSWORD"] == "${{ secrets.REVIEW_HOME_VIEWER_PASSWORD }}"
    assert wf["permissions"] == {"contents": "read"}
