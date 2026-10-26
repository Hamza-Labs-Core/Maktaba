"""The home's database as this machine's pipeline reaches it: as the server's roles do
(Go's ``supervisor.HomeDSN``, Epic 35 stories 35.8 and 35.16).

``node.json``'s ``database_url`` is the join's (``sslmode=prefer``): TLS when the home has
it, unverified, and no certificate. A home that requires its machines' cluster-link
certificates (``clientcert=verify-full`` once the cluster link is required) refuses that
login, and with ``prefer`` the plain try after it ("no pg_hba.conf entry … no
encryption"). So the pipeline connects as the node's API does: the home's database CA
verified (``verify-ca``, the CA ``node.json`` keeps, written beside it), the machine's
cluster-link certificate offered (``cluster-node.crt`` / ``cluster-node.key`` beside
``node.json``); plain only on the home's word that its database has no TLS; as
``node.json`` has it while neither is known. asyncpg reads every one of these from the
URL (``sslmode``, ``sslrootcert``, ``sslcert``, ``sslkey``).
"""

from __future__ import annotations

import contextlib
import os
from pathlib import Path
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit

from . import linkcert
from .config import NodeConfig, config_path

__all__ = ["HOME_DATABASE_CA_FILE", "home_dsn"]

#: the home's database CA, beside node.json (the Go server's name for its pinned copy)
HOME_DATABASE_CA_FILE = "home-database-ca.crt"

#: the TLS modes a client certificate is offered on (as the Go roles: withClientCert)
_CERT_MODES = ("require", "verify-ca", "verify-full")


def home_dsn(node: NodeConfig, node_dir: Path | None = None) -> str:
    """``node``'s connection to its home's database (node_dir: where node.json and the
    cluster-link certificate are; default beside this machine's node.json). A CA that
    cannot be written raises (OSError, or ValueError for one that is no certificate): never
    a connection that does not verify."""
    url = node.database_url
    parts = urlsplit(url)
    if not parts.hostname:
        return url
    directory = node_dir if node_dir is not None else config_path().parent
    query = dict(parse_qsl(parts.query, keep_blank_values=True))
    # lib/pq's, which asyncpg would send the server as a setting of the session (refused)
    query.pop("connect_timeout", None)
    if node.database_ca.strip():
        ca = directory / HOME_DATABASE_CA_FILE
        _pin(ca, node.database_ca)
        query["sslmode"] = "verify-ca"
        query["sslrootcert"] = ca.as_posix()
    elif node.database_tls.strip().lower() == "off":
        query["sslmode"] = "disable"
        query.pop("sslrootcert", None)
    if query.get("sslmode", "").lower() in _CERT_MODES:
        cert, key = directory / linkcert.CERT_FILE, directory / linkcert.KEY_FILE
        if cert.is_file() and key.is_file():
            query["sslcert"] = cert.as_posix()
            query["sslkey"] = key.as_posix()
    return urlunsplit(parts._replace(query=urlencode(query)))


def _pin(path: Path, pem: str) -> None:
    """Writes the CA (0600) unless the file holds it already; anything but a certificate is
    refused."""
    if "-----BEGIN CERTIFICATE-----" not in pem:
        raise ValueError("node.json's database_ca is not a certificate")
    with contextlib.suppress(OSError):
        if path.read_text(encoding="ascii") == pem:
            return
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(f".{path.name}-{os.getpid()}.tmp")
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    try:
        with os.fdopen(fd, "w", encoding="ascii", newline="") as f:
            f.write(pem)
        os.replace(tmp, path)
    finally:
        with contextlib.suppress(FileNotFoundError):
            tmp.unlink()
