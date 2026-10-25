"""The cluster-link certificate a join keeps (story 35.16): its key made here, the
certificate checked against the key, the CA and the CA's fingerprint."""

from __future__ import annotations

import datetime as dt
import hashlib
from pathlib import Path

import pytest
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.x509.oid import NameOID

from maktaba_pipeline.cluster import linkcert

pytestmark = pytest.mark.unit


def _ca() -> tuple[ec.EllipticCurvePrivateKey, x509.Certificate]:
    key = ec.generate_private_key(ec.SECP256R1())
    name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "test CA")])
    now = dt.datetime.now(dt.UTC)
    cert = (
        x509.CertificateBuilder()
        .subject_name(name)
        .issuer_name(name)
        .public_key(key.public_key())
        .serial_number(1)
        .not_valid_before(now - dt.timedelta(hours=1))
        .not_valid_after(now + dt.timedelta(days=365))
        .add_extension(x509.BasicConstraints(ca=True, path_length=0), critical=True)
        .sign(key, hashes.SHA256())
    )
    return key, cert


def _sign(ca_key: ec.EllipticCurvePrivateKey, ca: x509.Certificate, csr_pem: str) -> str:
    csr = x509.load_pem_x509_csr(csr_pem.encode())
    now = dt.datetime.now(dt.UTC)
    cert = (
        x509.CertificateBuilder()
        .subject_name(csr.subject)
        .issuer_name(ca.subject)
        .public_key(csr.public_key())
        .serial_number(2)
        .not_valid_before(now - dt.timedelta(minutes=5))
        .not_valid_after(now + dt.timedelta(days=30))
        .sign(ca_key, hashes.SHA256())
    )
    return cert.public_bytes(serialization.Encoding.PEM).decode()


def test_join_keeps_the_sealed_certificate(tmp_path: Path) -> None:
    ca_key, ca = _ca()
    ca_pem = ca.public_bytes(serialization.Encoding.PEM).decode()
    fp = hashlib.sha256(ca.public_bytes(serialization.Encoding.DER)).hexdigest()
    pending = linkcert.new_request("n1")
    cert = _sign(ca_key, ca, pending.csr_pem)
    with pytest.raises(linkcert.LinkCertError):
        linkcert.save(tmp_path, pending, cert, ca_pem, "00" + fp[2:])
    other = linkcert.new_request("n1")
    with pytest.raises(linkcert.LinkCertError):
        linkcert.save(tmp_path, other, cert, ca_pem, fp)
    linkcert.save(tmp_path, pending, cert, ca_pem, fp)
    assert (tmp_path / linkcert.CERT_FILE).read_text() == cert
    assert (tmp_path / linkcert.CA_FILE).read_text() == ca_pem
    assert (tmp_path / linkcert.KEY_FILE).read_bytes() == pending.key_pem
