"""The agent channel's handshake v2 and sealed frames against the interop vectors."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.cluster import crypto
from maktaba_pipeline.cluster.agent.codec import (
    FrameError,
    Opener,
    Sealer,
    client_proof,
    derive_keys,
    dh,
    dumps,
    home_ikm,
    node_ikm,
    server_proof,
    transcript_hash,
)

pytestmark = pytest.mark.unit

VECTORS = Path(__file__).resolve().parents[4] / "shared/testdata/cluster/agent_vectors.json"


def _v() -> dict[str, Any]:
    data: dict[str, Any] = json.loads(VECTORS.read_text())
    return data


def b(s: str) -> bytes:
    return crypto.b64d(s)


def test_node_handshake() -> None:
    v = _v()
    for prefix in ("node_static", "client_eph", "server_eph", "home_static"):
        kp = crypto.keypair_from_private(b(v[f"{prefix}_private"]))
        assert kp.public == b(v[f"{prefix}_public"])
    th = transcript_hash(v["hello"].encode(), v["challenge"].encode())
    assert th == b(v["transcript_hash"])
    eph, static = b(v["client_eph_private"]), b(v["node_static_private"])
    server_eph, home = b(v["server_eph_public"]), b(v["home_static_public"])
    assert dh(eph, server_eph) == b(v["dh_ee"])
    assert dh(static, server_eph) == b(v["dh_se"])
    assert dh(eph, home) == b(v["dh_es"])
    # the hub's side gives the same three results
    assert dh(b(v["server_eph_private"]), b(v["client_eph_public"])) == b(v["dh_ee"])
    assert dh(b(v["server_eph_private"]), b(v["node_static_public"])) == b(v["dh_se"])
    assert dh(b(v["home_static_private"]), b(v["client_eph_public"])) == b(v["dh_es"])
    ikm = node_ikm(eph, static, server_eph, home)
    keys = derive_keys(ikm, b(v["client_nonce"]), b(v["server_nonce"]))
    assert keys.c2s == b(v["k_c2s"]) and keys.s2c == b(v["k_s2c"])
    assert client_proof(keys.c2s, th) == b(v["client_proof"])
    assert server_proof(keys.s2c, th) == b(v["server_proof"])
    # the hello and challenge are the JSON the agent writes (sorted, compact)
    assert dumps(json.loads(v["hello"])) == v["hello"].encode()


def test_home_handshake() -> None:
    v = _v()
    h = v["home"]
    th = transcript_hash(h["hello"].encode(), h["challenge"].encode())
    assert th == b(h["transcript_hash"])
    ikm = home_ikm(b(h["home_key"]), b(v["client_eph_private"]), b(v["server_eph_public"]))
    keys = derive_keys(ikm, b(v["client_nonce"]), b(v["server_nonce"]))
    assert keys.c2s == b(h["k_c2s"]) and keys.s2c == b(h["k_s2c"])
    assert client_proof(keys.c2s, th) == b(h["client_proof"])
    assert server_proof(keys.s2c, th) == b(h["server_proof"])


def test_all_zero_dh_is_refused() -> None:
    eph = crypto.generate_keypair()
    with pytest.raises(ValueError, match="all-zero"):
        dh(eph.private, bytes(32))  # the identity point: a low-order key


def test_frames_seal_and_open() -> None:
    v = _v()
    keys = {"c2s": b(v["k_c2s"]), "s2c": b(v["k_s2c"])}
    sealers = {d: Sealer(k) for d, k in keys.items()}
    openers = {d: Opener(k) for d, k in keys.items()}
    for f in v["frames"]:
        s = sealers[f["dir"]]
        assert s.counter == f["counter"]
        # our JSON encoding reproduces the vector's plaintext byte for byte
        assert dumps(json.loads(f["plaintext"])) == f["plaintext"].encode()
        assert s.seal(json.loads(f["plaintext"])) == b(f["sealed"])
        assert openers[f["dir"]].open(b(f["sealed"])) == json.loads(f["plaintext"])
    welcome = json.loads(v["frames"][0]["plaintext"])
    assert welcome["proof"] == v["server_proof"]


def test_counter_is_strict() -> None:
    key = bytes(range(32))
    s = Sealer(key)
    f0, f1, f2 = s.seal({"n": 0}), s.seal({"n": 1}), s.seal({"n": 2})
    o = Opener(key)
    assert o.open(f0) == {"n": 0}
    with pytest.raises(FrameError):
        o.open(f0)  # a repeat
    o2 = Opener(key)
    o2.open(f0)
    with pytest.raises(FrameError):
        o2.open(f2)  # a gap
    o3 = Opener(key)
    o3.open(f0)
    tampered = bytearray(f1)
    tampered[0] ^= 1
    with pytest.raises(FrameError):
        o3.open(bytes(tampered))
    with pytest.raises(FrameError):
        Opener(bytes(32)).open(f0)  # the other direction's key
