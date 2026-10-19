"""The agent's whole handshake, byte for byte, against the interop vectors the Go hub's
tests check too (``shared/testdata/cluster/agent_vectors.json``): with its randomness
pinned to the vectors, the agent sends the vector's ``hello`` and ``auth`` and accepts the
vector's sealed ``welcome`` (whose proof is the hub's)."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.cluster import crypto
from maktaba_pipeline.cluster.agent.client import AgentIdentity, ChannelAgent, Refused
from maktaba_pipeline.cluster.agent.codec import Sealer
from maktaba_pipeline.cluster.agent.logship import LogShipper

pytestmark = [pytest.mark.integration, pytest.mark.asyncio]  # an event loop

V: dict[str, Any] = json.loads(
    (Path(__file__).resolve().parents[4] / "shared/testdata/cluster/agent_vectors.json").read_text()
)


def b(s: str) -> bytes:
    return crypto.b64d(s)


class ScriptedSocket:
    """Answers the agent with the hub's side of the vectors."""

    def __init__(self, challenge: str, welcome: bytes) -> None:
        self.inbox: list[Any] = [challenge, welcome]
        self.sent: list[str] = []
        self.closed: tuple[int, str] | None = None

    async def send(self, data: str) -> None:
        self.sent.append(data)

    async def recv(self) -> Any:
        return self.inbox.pop(0)

    async def close(self, code: int = 1000, reason: str = "") -> None:
        self.closed = (code, reason)


def _agent(ident: AgentIdentity) -> ChannelAgent:
    agent = ChannelAgent(ident, shipper=LogShipper())
    agent._new_eph = lambda: crypto.keypair_from_private(b(V["client_eph_private"]))
    agent._new_nonce = lambda: b(V["client_nonce"])
    return agent


async def test_node_handshake_matches_the_vectors() -> None:
    hello = json.loads(V["hello"])
    ident = AgentIdentity(
        kind="node",
        url="http://home:8080",
        worker_id=hello["worker_id"],
        host=hello["host"],
        role="node",
        pid=hello["pid"],
        node_id=hello["node_id"],
        node_private=b(V["node_static_private"]),
        home_static=b(V["home_static_public"]),
    )
    ws = ScriptedSocket(V["challenge"], b(V["frames"][0]["sealed"]))
    agent = _agent(ident)
    session = await agent._handshake(ws)
    assert ws.sent[0] == V["hello"]  # byte for byte: the transcript's first half
    assert json.loads(ws.sent[1]) == {"t": "auth", "proof": V["client_proof"]}
    assert session.id == "s1" and agent.settings == {"log_level": "info", "metrics_every": 2}
    # the session goes on sealing where the vectors do (c2s counter 0, then 1)
    assert session.sealer.counter == 0
    for f in V["frames"][1:]:
        assert session.sealer.seal(json.loads(f["plaintext"])) == b(f["sealed"])


async def test_home_handshake_matches_the_vectors(tmp_path: Path) -> None:
    h = V["home"]
    hello = json.loads(h["hello"])
    key = tmp_path / "home-agent.key"
    key.write_bytes(b(h["home_key"]))
    ident = AgentIdentity(
        kind="home",
        url="http://127.0.0.1:8080",
        worker_id=hello["worker_id"],
        host=hello["host"],
        role="home",
        pid=hello["pid"],
        home_key_path=key,
    )
    welcome = Sealer(b(h["k_s2c"])).seal(
        {"t": "welcome", "session": "s9", "settings": {}, "proof": h["server_proof"]}
    )
    ws = ScriptedSocket(h["challenge"], welcome)
    session = await _agent(ident)._handshake(ws)
    assert ws.sent[0] == h["hello"]
    assert json.loads(ws.sent[1])["proof"] == h["client_proof"]
    assert session.id == "s9"


async def test_a_welcome_without_the_hubs_proof_is_refused() -> None:
    hello = json.loads(V["hello"])
    ident = AgentIdentity(
        kind="node",
        url="http://home:8080",
        worker_id=hello["worker_id"],
        host=hello["host"],
        role="node",
        pid=hello["pid"],
        node_id=hello["node_id"],
        node_private=b(V["node_static_private"]),
        home_static=b(V["home_static_public"]),
    )
    forged = json.loads(V["frames"][0]["plaintext"])
    forged["proof"] = crypto.b64e(bytes(32))
    ws = ScriptedSocket(V["challenge"], Sealer(b(V["k_s2c"])).seal(forged))
    with pytest.raises(Refused, match="not the home I joined"):
        await _agent(ident)._handshake(ws)
    assert ws.closed is not None and ws.closed[0] == 4004
