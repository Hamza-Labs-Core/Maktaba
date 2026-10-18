"""Join cryptography against the shared vectors (the Go home checks the same file)."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest
from cryptography.exceptions import InvalidTag

from maktaba_pipeline.cluster import crypto

pytestmark = pytest.mark.unit

VECTORS = Path(__file__).resolve().parents[3] / "shared" / "testdata" / "cluster" / "vectors.json"


@pytest.fixture(scope="module")
def v() -> dict[str, Any]:
    data: dict[str, Any] = json.loads(VECTORS.read_text(encoding="utf-8"))
    return data


def test_public_keys_follow_from_the_private_keys(v: dict[str, Any]) -> None:
    node = crypto.keypair_from_private(crypto.b64d(v["node_private"]))
    home = crypto.keypair_from_private(crypto.b64d(v["home_private"]))
    assert node.public_b64 == v["node_public"]
    assert home.public_b64 == v["home_public"]


def test_both_sides_derive_the_shared_secret_sas_and_seal_key(v: dict[str, Any]) -> None:
    node_priv, home_priv = crypto.b64d(v["node_private"]), crypto.b64d(v["home_private"])
    node_pub, home_pub = crypto.b64d(v["node_public"]), crypto.b64d(v["home_public"])
    shared = crypto.shared_secret(node_priv, home_pub)
    assert shared == crypto.shared_secret(home_priv, node_pub)
    assert crypto.b64e(shared) == v["shared"]
    assert crypto.sas_code(shared, node_pub, home_pub) == v["sas"]
    assert crypto.b64e(crypto.seal_key(shared, node_pub, home_pub)) == v["seal_key"]


def test_the_sealed_vector_opens_to_the_payload(v: dict[str, Any]) -> None:
    key = crypto.b64d(v["seal_key"])
    assert crypto.open_sealed(key, v["node_id"], v["sealed"]).decode() == v["payload"]
    # sealing with the vector's nonce reproduces the envelope byte for byte
    nonce = crypto.b64d(v["sealed"]["nonce"])
    assert crypto.seal(key, v["node_id"], v["payload"].encode(), nonce=nonce) == v["sealed"]


def test_tampering_or_another_node_id_fails(v: dict[str, Any]) -> None:
    key = crypto.b64d(v["seal_key"])
    with pytest.raises(InvalidTag):
        crypto.open_sealed(key, "00000000-0000-4000-8000-000000000000", v["sealed"])
    ct = bytearray(crypto.b64d(v["sealed"]["ciphertext"]))
    ct[0] ^= 1
    bad = {"nonce": v["sealed"]["nonce"], "ciphertext": crypto.b64e(bytes(ct))}
    with pytest.raises(InvalidTag):
        crypto.open_sealed(key, v["node_id"], bad)


def test_a_key_swapped_in_the_middle_changes_the_code(v: dict[str, Any]) -> None:
    node_pub, home_pub = crypto.b64d(v["node_public"]), crypto.b64d(v["home_public"])
    mitm = crypto.generate_keypair()
    node_side = crypto.sas_code(
        crypto.shared_secret(crypto.b64d(v["node_private"]), mitm.public), node_pub, mitm.public
    )
    assert node_side != v["sas"]
    assert crypto.sas_code(crypto.b64d(v["shared"]), node_pub, home_pub) == v["sas"]


def test_round_trip_with_fresh_keys_and_code_format() -> None:
    a, b = crypto.generate_keypair(), crypto.generate_keypair()
    shared = crypto.shared_secret(a.private, b.public)
    key = crypto.seal_key(shared, a.public, b.public)
    env = crypto.seal(key, "6F1C2A3B-4D5E-4F60-8A71-92B3C4D5E6F7", b"hi")
    assert crypto.open_sealed(key, "6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7", env) == b"hi"
    code = crypto.sas_code(shared, a.public, b.public)
    assert len(code) == 6 and code.isdigit()
    assert crypto.format_code("761353") == "761 353"
    with pytest.raises(ValueError):
        crypto.keypair_from_private(b"short")
