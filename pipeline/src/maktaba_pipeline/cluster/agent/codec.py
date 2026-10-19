"""The agent channel's cryptography and framing (design-agent.md sections 4.2-4.4).

Handshake v2 (mutual, forward secret), each side from its own private halves:

- ``dh_ee = X25519(client_eph, server_eph)``; a node adds ``dh_se = X25519(node_static,
  server_eph)`` and ``dh_es = X25519(client_eph, home_static)`` and uses
  ``ikm = dh_ee || dh_se || dh_es``; a home process uses ``ikm = home_key || dh_ee``.
  Any all-zero DH result is refused.
- ``th = SHA-256(hello_bytes || challenge_bytes)``: the transcript, as sent.
- ``okm = HKDF-SHA256(ikm, salt = client_nonce || server_nonce, info = "maktaba-agent-v2",
  L = 64)``; ``k_c2s = okm[:32]``, ``k_s2c = okm[32:]``.
- Proofs: the process's ``HMAC-SHA256(k_c2s, "maktaba-agent-auth" || th)`` in ``auth``;
  the hub's ``HMAC-SHA256(k_s2c, "maktaba-agent-server" || th)`` in the sealed ``welcome``.
- Sealed frame: ``ChaCha20-Poly1305(k_dir, nonce = 4 zero bytes || 8-byte big-endian
  counter, plaintext = JSON, aad = "")``; the wire carries the ciphertext (with its tag)
  only, the counter is implicit: each direction starts at 0 and adds one per frame, so a
  gap, a repeat or a tampered frame fails the tag and closes the connection.

``shared/testdata/cluster/agent_vectors.json`` pins every value (Go checks the same).
"""

from __future__ import annotations

import hashlib
import hmac
import json
from dataclasses import dataclass
from typing import Any

from cryptography.exceptions import InvalidTag
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.ciphers.aead import ChaCha20Poly1305
from cryptography.hazmat.primitives.kdf.hkdf import HKDF

from .. import crypto

__all__ = [
    "AUTH_LABEL",
    "HKDF_INFO",
    "PROTOCOL_VERSION",
    "SERVER_LABEL",
    "FrameError",
    "Opener",
    "Sealer",
    "SessionKeys",
    "client_proof",
    "derive_keys",
    "dh",
    "dumps",
    "home_ikm",
    "node_ikm",
    "server_proof",
    "transcript_hash",
]

PROTOCOL_VERSION = 2
HKDF_INFO = b"maktaba-agent-v2"
AUTH_LABEL = b"maktaba-agent-auth"
SERVER_LABEL = b"maktaba-agent-server"
NONCE_PREFIX = b"\x00\x00\x00\x00"
MAX_COUNTER = 2**64 - 1
_ZERO = bytes(32)


class FrameError(Exception):
    """A sealed frame that does not open (tampered, replayed, out of order)."""


@dataclass(frozen=True, slots=True)
class SessionKeys:
    c2s: bytes
    s2c: bytes


def dh(private: bytes, public: bytes) -> bytes:
    """X25519, refusing an all-zero result (a low-order public key)."""
    try:
        out = crypto.shared_secret(private, public)
    except ValueError as exc:  # the library refuses the all-zero result itself
        raise ValueError("all-zero X25519 result (low-order key)") from exc
    if hmac.compare_digest(out, _ZERO):
        raise ValueError("all-zero X25519 result (low-order key)")
    return out


def node_ikm(
    client_eph_private: bytes, node_static_private: bytes, server_eph: bytes, home_static: bytes
) -> bytes:
    """``dh_ee || dh_se || dh_es`` for a machine's process."""
    return (
        dh(client_eph_private, server_eph)
        + dh(node_static_private, server_eph)
        + dh(client_eph_private, home_static)
    )


def home_ikm(home_key: bytes, client_eph_private: bytes, server_eph: bytes) -> bytes:
    """``home_key || dh_ee`` for a home process."""
    if len(home_key) != 32:
        raise ValueError("the home agent key is 32 bytes")
    return home_key + dh(client_eph_private, server_eph)


def transcript_hash(hello: bytes, challenge: bytes) -> bytes:
    return hashlib.sha256(hello + challenge).digest()


def derive_keys(ikm: bytes, client_nonce: bytes, server_nonce: bytes) -> SessionKeys:
    if len(ikm) < 32:
        raise ValueError("the key material is too short")
    okm = HKDF(
        algorithm=hashes.SHA256(), length=64, salt=client_nonce + server_nonce, info=HKDF_INFO
    ).derive(ikm)
    return SessionKeys(c2s=okm[:32], s2c=okm[32:])


def client_proof(k_c2s: bytes, th: bytes) -> bytes:
    return hmac.new(k_c2s, AUTH_LABEL + th, hashlib.sha256).digest()


def server_proof(k_s2c: bytes, th: bytes) -> bytes:
    return hmac.new(k_s2c, SERVER_LABEL + th, hashlib.sha256).digest()


def dumps(obj: Any) -> bytes:
    """The JSON both sides seal: sorted keys, no spaces, UTF-8 (non-JSON values as text)."""
    return json.dumps(
        obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False, default=str
    ).encode("utf-8")


def _nonce(counter: int) -> bytes:
    return NONCE_PREFIX + counter.to_bytes(8, "big")


class Sealer:
    """One direction's sender: seals frames with a strictly increasing counter."""

    def __init__(self, key: bytes) -> None:
        self._aead = ChaCha20Poly1305(key)
        self.counter = 0

    def seal_bytes(self, plaintext: bytes) -> bytes:
        if self.counter > MAX_COUNTER:
            raise FrameError("frame counter exhausted")
        out = self._aead.encrypt(_nonce(self.counter), plaintext, b"")
        self.counter += 1
        return out

    def seal(self, obj: Any) -> bytes:
        return self.seal_bytes(dumps(obj))


class Opener:
    """One direction's receiver: opens frames only in order (the next counter)."""

    def __init__(self, key: bytes) -> None:
        self._aead = ChaCha20Poly1305(key)
        self.counter = 0

    def open_bytes(self, data: bytes) -> bytes:
        try:
            plain = self._aead.decrypt(_nonce(self.counter), data, b"")
        except InvalidTag as exc:
            raise FrameError(f"frame {self.counter} does not open") from exc
        self.counter += 1
        return plain

    def open(self, data: bytes) -> dict[str, Any]:
        plain = self.open_bytes(data)
        try:
            obj = json.loads(plain)
        except ValueError as exc:
            raise FrameError("frame is not JSON") from exc
        if not isinstance(obj, dict):
            raise FrameError("frame is not a JSON object")
        return obj
