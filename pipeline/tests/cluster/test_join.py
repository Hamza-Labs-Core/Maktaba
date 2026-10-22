"""The join client against a fake home (http.server in a thread)."""

from __future__ import annotations

import io
import json
import os
import threading
import urllib.parse
from collections.abc import Iterator
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.cluster import config as node_config
from maktaba_pipeline.cluster import crypto
from maktaba_pipeline.cluster.discover import Home
from maktaba_pipeline.cluster.join import JoinError, join, pick_home

DB_URL = "postgres://maktaba_node_x:s3cr3t@192.168.1.20:5432/maktaba?sslmode=prefer"
DB_CA = "-----BEGIN CERTIFICATE-----\nTUFLVEFCQQ==\n-----END CERTIFICATE-----\n"
CLUSTER_PUB = "RSujjNFA61v0BQ1NiOVfwb46U0QPe3dj8ve08aDH0V8="


@dataclass
class FakeHome:
    accept: bool = True
    conflict: str | None = None
    approve_after: int = 2  # polls answered "pending" before approval
    final_state: str = "approved"
    tamper: bool = False
    keys: crypto.KeyPair = field(default_factory=crypto.generate_keypair)
    joins: list[dict[str, Any]] = field(default_factory=list)
    polls: list[str] = field(default_factory=list)
    node_pub: bytes = b""

    def code(self) -> str:
        shared = crypto.shared_secret(self.keys.private, self.node_pub)
        return crypto.sas_code(shared, self.node_pub, self.keys.public)

    def sealed(self, node_id: str) -> dict[str, str]:
        shared = crypto.shared_secret(self.keys.private, self.node_pub)
        key = crypto.seal_key(shared, self.node_pub, self.keys.public)
        payload = {
            "v": 1,
            "node_id": node_id,
            "database_url": DB_URL,
            "database_ca": DB_CA,
            "home": {
                "instance_id": "0b9a",
                "name": "home-mac",
                "api_url": "http://h:8080",
                "cluster_pub": CLUSTER_PUB,
            },
        }
        env = crypto.seal(key, node_id, json.dumps(payload).encode())
        if self.tamper:
            env["nonce"] = crypto.b64e(b"\0" * 12)
        return env


def _serve(home: FakeHome) -> tuple[ThreadingHTTPServer, str]:
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args: Any) -> None:
            pass

        def _send(self, status: int, body: dict[str, Any]) -> None:
            raw = json.dumps(body).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def do_POST(self) -> None:  # noqa: N802
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            if self.path != "/api/cluster/join":
                return self._send(404, {"error": "not_found"})
            if not home.accept:
                return self._send(403, {"error": "joins_closed"})
            if home.conflict:
                return self._send(409, {"code": home.conflict})
            home.joins.append(body)
            home.node_pub = crypto.b64d(body["pubkey"])
            self._send(
                202,
                {
                    "request_id": body["node_id"],
                    "home_pubkey": home.keys.public_b64,
                    "home": {"instance_id": "0b9a", "name": "home-mac"},
                    "expires_at": "2026-10-18T12:15:00Z",
                },
            )

        def do_GET(self) -> None:  # noqa: N802
            url = urllib.parse.urlparse(self.path)
            node_id = url.path.rsplit("/", 1)[-1]
            pub = urllib.parse.parse_qs(url.query).get("pubkey", [""])[0]
            home.polls.append(pub)
            if crypto.b64d(pub) != home.node_pub:
                return self._send(403, {"error": "pubkey_mismatch"})
            if len(home.polls) <= home.approve_after:
                return self._send(200, {"state": "pending"})
            if home.final_state != "approved":
                return self._send(200, {"state": home.final_state})
            self._send(200, {"state": "approved", "sealed": home.sealed(node_id)})

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server, f"http://127.0.0.1:{server.server_address[1]}"


@pytest.fixture
def fake() -> Iterator[tuple[FakeHome, str]]:
    home = FakeHome()
    server, url = _serve(home)
    try:
        yield home, url
    finally:
        server.shutdown()
        server.server_close()


def _join(url: str, path: Path, out: io.StringIO, **kw: Any) -> node_config.NodeConfig:
    return join(home_url=url, name="linux-box", path=path, out=out, sleep=lambda s: None, **kw)


def test_join_shows_the_code_waits_and_saves_the_credentials(
    fake: tuple[FakeHome, str], tmp_path: Path
) -> None:
    home, url = fake
    path = tmp_path / "node.json"
    out = io.StringIO()
    cfg = _join(url, path, out)

    body = home.joins[0]
    assert set(body) == {
        "node_id", "name", "os", "arch", "versions", "capabilities", "storage", "pubkey",
    }  # fmt: skip
    assert body["name"] == "linux-box" and body["node_id"] == cfg.node_id
    assert body["capabilities"]["pipeline_version"] and "pipeline" in body["versions"]
    code = home.code()
    assert f"{code[:3]} {code[3:]}" in out.getvalue()  # the code both sides derive, shown
    assert len(home.polls) == 3  # pending, pending, approved

    saved = node_config.load(path)
    assert saved == cfg
    assert cfg.database_url == DB_URL and cfg.joined_at
    assert (cfg.home.url, cfg.home.name, cfg.home.instance_id) == (
        "http://h:8080",
        "home-mac",
        "0b9a",
    )
    assert cfg.home.cluster_pub == CLUSTER_PUB  # the hub proves itself with it (agent v2)
    # the home's database CA, sealed: the machine verifies its database with it
    assert cfg.database_ca == DB_CA and cfg.database_tls == ""
    # the identity kept is the one whose public key was sent
    kp = crypto.keypair_from_private(crypto.b64d(cfg.private_key))
    assert kp.public_b64 == body["pubkey"]
    # asking again on a joined machine does nothing
    assert _join(url, path, io.StringIO()) == cfg and len(home.joins) == 1


def test_closed_joins_are_explained_and_the_identity_is_kept(
    fake: tuple[FakeHome, str], tmp_path: Path
) -> None:
    home, url = fake
    home.accept = False
    path = tmp_path / "node.json"
    with pytest.raises(JoinError, match="Accept new machines"):
        _join(url, path, io.StringIO())
    first = node_config.load(path)
    assert first is not None and not first.joined
    # the next attempt is the same machine
    home.accept = True
    cfg = _join(url, path, io.StringIO())
    assert cfg.node_id == first.node_id and cfg.private_key == first.private_key


@pytest.mark.parametrize("state", ["rejected", "expired"])
def test_a_refused_request_fails(fake: tuple[FakeHome, str], tmp_path: Path, state: str) -> None:
    home, url = fake
    home.final_state = state
    with pytest.raises(JoinError, match=state):
        _join(url, tmp_path / "node.json", io.StringIO())


def test_credentials_that_do_not_open_are_refused(
    fake: tuple[FakeHome, str], tmp_path: Path
) -> None:
    home, url = fake
    home.tamper = True
    path = tmp_path / "node.json"
    with pytest.raises(JoinError, match="could not be opened"):
        _join(url, path, io.StringIO())
    saved = node_config.load(path)
    assert saved is not None and not saved.joined


def test_gives_up_after_the_wait(fake: tuple[FakeHome, str], tmp_path: Path) -> None:
    home, url = fake
    home.approve_after = 10_000
    ticks = iter(range(100))
    with pytest.raises(JoinError, match="15 minutes"):
        _join(url, tmp_path / "n.json", io.StringIO(), clock=lambda: next(ticks) * 60.0)


def test_an_unreachable_home(tmp_path: Path) -> None:
    with pytest.raises(JoinError, match="cannot reach"):
        _join("http://127.0.0.1:9", tmp_path / "n.json", io.StringIO())


@pytest.mark.unit
def test_pick_home() -> None:
    a, b = Home(url="http://a", name="a", join=False), Home(url="http://b", name="b", join=True)
    out = io.StringIO()
    with pytest.raises(JoinError, match="--home"):
        pick_home([], ask=None, out=out)
    assert pick_home([a, b], ask=None, out=out) == b  # non-interactive: an open one
    answers = iter(["x", "1"])
    assert pick_home([a, b], ask=lambda _: next(answers), out=out) == a
    assert "1. a" in out.getvalue()


def test_a_request_waiting_with_another_key_is_explained(
    fake: tuple[FakeHome, str], tmp_path: Path
) -> None:
    home, url = fake
    home.conflict = "pending_elsewhere"
    with pytest.raises(JoinError, match="another key"):
        _join(url, tmp_path / "node.json", io.StringIO())
    home.conflict = "already_member"
    with pytest.raises(JoinError, match="already has this machine"):
        _join(url, tmp_path / "node.json", io.StringIO())


@pytest.mark.skipif(os.name != "nt", reason="Windows ACLs")
def test_windows_cuts_the_config_acl_to_the_user(tmp_path: Path) -> None:
    """The node config's DACL is the user's alone, inheritance off: explicit entries a
    temp file may have been made with (here Users, read) go too."""
    import subprocess

    path = tmp_path / "node.json"
    path.write_text("{}")
    subprocess.run(
        ["icacls", str(path), "/grant", "*S-1-5-32-545:(R)"], check=True, capture_output=True
    )
    node_config._restrict_to_user(path)
    out = subprocess.run(["icacls", str(path)], check=True, capture_output=True, text=True).stdout
    aces = [
        line.replace(str(path), "", 1).strip()
        for line in out.splitlines()
        if line.strip() and not line.startswith("Successfully processed")
    ]
    assert len(aces) == 1 and os.environ["USERNAME"].lower() in aces[0].lower(), aces
    assert "(I)" not in aces[0]
