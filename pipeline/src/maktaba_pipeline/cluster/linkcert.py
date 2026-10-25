"""The machine's cluster-link certificate at join (Epic 35 story 35.16, design §18.3).

The join request carries a certificate request whose key is made here and never leaves
the machine; the home's approval seals the certificate, its cluster CA and the CA's
fingerprint into the credentials (pinned through the pairing code's protection: no trust
on first use). The files sit beside ``node.json``, where the node's API (which holds the
machine's link) reads them; the names and checks are the API's
(``api/internal/cluster/link/files.go``). A machine whose join carried no certificate
asks for one on its channel session instead.
"""

from __future__ import annotations

import contextlib
import hashlib
import os
from dataclasses import dataclass
from pathlib import Path

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.x509.oid import NameOID

__all__ = ["CA_FILE", "CERT_FILE", "KEY_FILE", "LinkCertError", "Pending", "new_request", "save"]

KEY_FILE = "cluster-node.key"
CERT_FILE = "cluster-node.crt"
CA_FILE = "cluster-ca.crt"


class LinkCertError(ValueError):
    """The certificate the home sealed cannot be used (kept out; the machine asks again
    on its channel session)."""


@dataclass(frozen=True)
class Pending:
    """A key made for a join and its certificate request (PEM)."""

    key_pem: bytes
    csr_pem: str


def new_request(node_id: str) -> Pending:
    """A P-256 key and its certificate request."""
    key = ec.generate_private_key(ec.SECP256R1())
    csr = (
        x509.CertificateSigningRequestBuilder()
        .subject_name(x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, node_id)]))
        .sign(key, hashes.SHA256())
    )
    key_pem = key.private_bytes(
        serialization.Encoding.PEM,
        serialization.PrivateFormat.TraditionalOpenSSL,
        serialization.NoEncryption(),
    )
    return Pending(key_pem=key_pem, csr_pem=csr.public_bytes(serialization.Encoding.PEM).decode())


def _write(path: Path, data: bytes) -> None:
    tmp = path.with_name(path.name + ".tmp")
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "wb") as f:
        f.write(data)
    os.replace(tmp, path)
    with contextlib.suppress(OSError):
        os.chmod(path, 0o600)


def save(directory: Path, pending: Pending, cert_pem: str, ca_pem: str, ca_sha256: str) -> None:
    """Keeps the sealed certificate with its key: the CA must match its fingerprint and the
    certificate its key and the CA."""
    try:
        ca = x509.load_pem_x509_certificate(ca_pem.encode())
        cert = x509.load_pem_x509_certificate(cert_pem.encode())
        key = serialization.load_pem_private_key(pending.key_pem, password=None)
    except ValueError as exc:
        raise LinkCertError(f"the sealed certificate cannot be read: {exc}") from exc
    digest = hashlib.sha256(ca.public_bytes(serialization.Encoding.DER)).hexdigest()
    if ca_sha256 and digest.lower() != ca_sha256.strip().lower():
        raise LinkCertError("the cluster CA does not match its fingerprint")
    if cert.public_key().public_numbers() != key.public_key().public_numbers():  # type: ignore[union-attr]
        raise LinkCertError("the certificate is not for this machine's key")
    try:
        cert.verify_directly_issued_by(ca)
    except (ValueError, TypeError) as exc:
        raise LinkCertError("the certificate was not issued by the cluster CA") from exc
    except Exception as exc:  # noqa: BLE001 - InvalidSignature: not issued by this CA
        raise LinkCertError("the certificate was not issued by the cluster CA") from exc
    directory.mkdir(parents=True, exist_ok=True)
    # the CA first, the key last: a reader in between sees a key that does not match its
    # certificate (refused, read again) rather than a half-new set
    _write(directory / CA_FILE, ca_pem.encode())
    _write(directory / CERT_FILE, cert_pem.encode())
    _write(directory / KEY_FILE, pending.key_pem)
