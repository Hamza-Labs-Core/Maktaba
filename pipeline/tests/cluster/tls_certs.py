"""Certificates for the TLS tests: a CA, and leaves it signs (a server's, a machine's)."""

from __future__ import annotations

import datetime as dt
import ipaddress
from dataclasses import dataclass
from pathlib import Path

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.x509.oid import NameOID


@dataclass(frozen=True)
class Issued:
    cert_pem: str
    key_pem: str

    def write(self, cert: Path, key: Path) -> None:
        cert.write_text(self.cert_pem, encoding="ascii")
        key.write_text(self.key_pem, encoding="ascii")
        key.chmod(0o600)


class CA:
    """A CA that signs leaves (``server``: a TLS server's, for 127.0.0.1 and localhost;
    ``client``: a machine's, its CN its database role)."""

    def __init__(self, name: str) -> None:
        self._key = ec.generate_private_key(ec.SECP256R1())
        subject = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, name)])
        now = dt.datetime.now(dt.UTC)
        self.cert = (
            x509.CertificateBuilder()
            .subject_name(subject)
            .issuer_name(subject)
            .public_key(self._key.public_key())
            .serial_number(x509.random_serial_number())
            .not_valid_before(now - dt.timedelta(hours=1))
            .not_valid_after(now + dt.timedelta(days=30))
            .add_extension(x509.BasicConstraints(ca=True, path_length=0), critical=True)
            .add_extension(
                x509.KeyUsage(
                    digital_signature=True,
                    content_commitment=False,
                    key_encipherment=False,
                    data_encipherment=False,
                    key_agreement=False,
                    key_cert_sign=True,
                    crl_sign=True,
                    encipher_only=False,
                    decipher_only=False,
                ),
                critical=True,
            )
            .sign(self._key, hashes.SHA256())
        )

    @property
    def pem(self) -> str:
        return self.cert.public_bytes(serialization.Encoding.PEM).decode()

    def _leaf(self, cn: str, *, server: bool) -> Issued:
        key = ec.generate_private_key(ec.SECP256R1())
        now = dt.datetime.now(dt.UTC)
        b = (
            x509.CertificateBuilder()
            .subject_name(x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, cn)]))
            .issuer_name(self.cert.subject)
            .public_key(key.public_key())
            .serial_number(x509.random_serial_number())
            .not_valid_before(now - dt.timedelta(minutes=5))
            .not_valid_after(now + dt.timedelta(days=7))
            .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
        )
        if server:
            b = b.add_extension(
                x509.SubjectAlternativeName(
                    [x509.DNSName("localhost"), x509.IPAddress(ipaddress.ip_address("127.0.0.1"))]
                ),
                critical=False,
            )
        cert = b.sign(self._key, hashes.SHA256())
        key_pem = key.private_bytes(
            serialization.Encoding.PEM,
            serialization.PrivateFormat.PKCS8,
            serialization.NoEncryption(),
        ).decode()
        return Issued(cert.public_bytes(serialization.Encoding.PEM).decode(), key_pem)

    def server(self) -> Issued:
        return self._leaf("localhost", server=True)

    def client(self, role: str) -> Issued:
        return self._leaf(role, server=False)
