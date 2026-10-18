"""Join cryptography (Epic 31 design section 3.2); the Go home must match byte for byte.

- X25519 keys, 32 raw bytes, standard base64 on the wire.
- ``shared = X25519(own_private, peer_public)``; ``salt = node_pub || home_pub``.
- SAS code: ``HKDF-SHA256(shared, salt, "maktaba-join-sas", 4)`` as a big-endian
  integer modulo 1 000 000, six digits.
- Seal key: ``HKDF-SHA256(shared, salt, "maktaba-join-seal", 32)``; ChaCha20-Poly1305
  with a random 12-byte nonce and the node id (lowercase uuid, ASCII) as AAD.

``shared/testdata/cluster/vectors.json`` pins every value.
"""

from __future__ import annotations

import base64
import os
from dataclasses import dataclass

from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.asymmetric.x25519 import X25519PrivateKey, X25519PublicKey
from cryptography.hazmat.primitives.ciphers.aead import ChaCha20Poly1305
from cryptography.hazmat.primitives.kdf.hkdf import HKDF
from cryptography.hazmat.primitives.serialization import (
    Encoding,
    NoEncryption,
    PrivateFormat,
    PublicFormat,
)

__all__ = [
    "KeyPair",
    "b64d",
    "b64e",
    "format_code",
    "generate_keypair",
    "keypair_from_private",
    "open_sealed",
    "sas_code",
    "seal",
    "seal_key",
    "shared_secret",
]

SAS_INFO = b"maktaba-join-sas"
SEAL_INFO = b"maktaba-join-seal"
NONCE_SIZE = 12


def b64e(raw: bytes) -> str:
    return base64.b64encode(raw).decode("ascii")


def b64d(text: str) -> bytes:
    return base64.b64decode(text, validate=True)


@dataclass(frozen=True, slots=True)
class KeyPair:
    private: bytes
    public: bytes

    @property
    def private_b64(self) -> str:
        return b64e(self.private)

    @property
    def public_b64(self) -> str:
        return b64e(self.public)


def keypair_from_private(private: bytes) -> KeyPair:
    if len(private) != 32:
        raise ValueError("an X25519 private key is 32 bytes")
    key = X25519PrivateKey.from_private_bytes(private)
    public = key.public_key().public_bytes(Encoding.Raw, PublicFormat.Raw)
    return KeyPair(private=private, public=public)


def generate_keypair() -> KeyPair:
    key = X25519PrivateKey.generate()
    private = key.private_bytes(Encoding.Raw, PrivateFormat.Raw, NoEncryption())
    return keypair_from_private(private)


def shared_secret(own_private: bytes, peer_public: bytes) -> bytes:
    if len(peer_public) != 32:
        raise ValueError("an X25519 public key is 32 bytes")
    key = X25519PrivateKey.from_private_bytes(own_private)
    return key.exchange(X25519PublicKey.from_public_bytes(peer_public))


def _hkdf(shared: bytes, node_pub: bytes, home_pub: bytes, info: bytes, length: int) -> bytes:
    return HKDF(
        algorithm=hashes.SHA256(), length=length, salt=node_pub + home_pub, info=info
    ).derive(shared)


def sas_code(shared: bytes, node_pub: bytes, home_pub: bytes) -> str:
    """The six-digit pairing code both sides derive (``"761353"``)."""
    okm = _hkdf(shared, node_pub, home_pub, SAS_INFO, 4)
    return f"{int.from_bytes(okm, 'big') % 1_000_000:06d}"


def format_code(code: str) -> str:
    """``"761353"`` -> ``"761 353"``, the way the code is shown."""
    return f"{code[:3]} {code[3:]}"


def seal_key(shared: bytes, node_pub: bytes, home_pub: bytes) -> bytes:
    return _hkdf(shared, node_pub, home_pub, SEAL_INFO, 32)


def seal(
    key: bytes, node_id: str, plaintext: bytes, *, nonce: bytes | None = None
) -> dict[str, str]:
    """Envelope ``{"nonce": b64, "ciphertext": b64}`` (the ciphertext carries the tag)."""
    nonce = nonce if nonce is not None else os.urandom(NONCE_SIZE)
    if len(nonce) != NONCE_SIZE:
        raise ValueError("the nonce is 12 bytes")
    ct = ChaCha20Poly1305(key).encrypt(nonce, plaintext, node_id.lower().encode("ascii"))
    return {"nonce": b64e(nonce), "ciphertext": b64e(ct)}


def open_sealed(key: bytes, node_id: str, envelope: dict[str, str]) -> bytes:
    """Decrypt an envelope; raises ``cryptography.exceptions.InvalidTag`` when tampered."""
    nonce = b64d(envelope["nonce"])
    ct = b64d(envelope["ciphertext"])
    return ChaCha20Poly1305(key).decrypt(nonce, ct, node_id.lower().encode("ascii"))
