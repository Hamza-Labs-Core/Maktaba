"""A node's pipeline connects to its home's database as the node's API does (Go's
``supervisor.HomeDSN``): the home's CA verified, the machine's cluster-link certificate
offered; and a DSN that asks for TLS gets it or no connection (db/pgtls.py)."""

from __future__ import annotations

import os
import ssl
from pathlib import Path
from urllib.parse import parse_qs, urlsplit

import pytest

from maktaba_pipeline.cluster import linkcert
from maktaba_pipeline.cluster.config import NodeConfig
from maktaba_pipeline.cluster.homedb import HOME_DATABASE_CA_FILE, home_dsn
from maktaba_pipeline.db.pgtls import TLSConfigError, connect_args

from .tls_certs import CA

pytestmark = pytest.mark.unit

ROLE = "maktaba_node_6f1c2a3b4d5e4f608a7192b3c4d5e6f7"
JOINED = f"postgres://{ROLE}:pw@192.168.50.177:54329/maktaba?sslmode=prefer"


def _node(url: str = JOINED, database_ca: str = "", database_tls: str = "") -> NodeConfig:
    return NodeConfig(
        node_id="6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7",
        name="pc",
        private_key="AQID",
        database_url=url,
        database_ca=database_ca,
        database_tls=database_tls,
    )


def _query(dsn: str) -> dict[str, str]:
    return {k: v[0] for k, v in parse_qs(urlsplit(dsn).query).items()}


def _with_cert(directory: Path, ca: CA) -> None:
    ca.client(ROLE).write(directory / linkcert.CERT_FILE, directory / linkcert.KEY_FILE)


def test_the_home_ca_verified_and_the_certificate_offered(tmp_path: Path) -> None:
    db_ca, cluster_ca = CA("home database"), CA("cluster")
    _with_cert(tmp_path, cluster_ca)
    dsn = home_dsn(_node(database_ca=db_ca.pem), tmp_path)
    q = _query(dsn)
    assert q["sslmode"] == "verify-ca"
    assert Path(q["sslrootcert"]) == tmp_path / HOME_DATABASE_CA_FILE
    assert (tmp_path / HOME_DATABASE_CA_FILE).read_text(encoding="ascii") == db_ca.pem
    assert Path(q["sslcert"]) == tmp_path / linkcert.CERT_FILE
    assert Path(q["sslkey"]) == tmp_path / linkcert.KEY_FILE
    parts = urlsplit(dsn)
    assert (parts.username, parts.password, parts.hostname, parts.port, parts.path) == (
        ROLE,
        "pw",
        "192.168.50.177",
        54329,
        "/maktaba",
    )
    # and what asyncpg is handed: TLS that must hold, the CA's, the certificate loaded
    bare, ctx = connect_args(dsn)
    assert ctx is not None and ctx.verify_mode == ssl.CERT_REQUIRED and not ctx.check_hostname
    assert [c["subject"] for c in ctx.get_ca_certs()] == [((("commonName", "home database"),),)]
    assert not any(k.startswith("ssl") for k in _query(bare))


def test_no_certificate_yet_still_verifies(tmp_path: Path) -> None:
    q = _query(home_dsn(_node(database_ca=CA("home database").pem), tmp_path))
    assert q["sslmode"] == "verify-ca" and "sslcert" not in q and "sslkey" not in q


def test_a_home_without_tls_and_one_not_known_yet(tmp_path: Path) -> None:
    _with_cert(tmp_path, CA("cluster"))
    q = _query(home_dsn(_node(database_tls="off"), tmp_path))
    assert q["sslmode"] == "disable" and "sslcert" not in q and "sslrootcert" not in q
    # neither known (joined before its home sealed the CA): as node.json has it
    assert home_dsn(_node(), tmp_path) == JOINED


def test_lib_pq_settings_asyncpg_would_send_the_server_are_left_out(tmp_path: Path) -> None:
    url = JOINED + "&connect_timeout=10"
    assert "connect_timeout" not in _query(
        home_dsn(_node(url=url, database_ca=CA("db").pem), tmp_path)
    )


def test_a_database_ca_that_is_no_certificate_is_refused(tmp_path: Path) -> None:
    with pytest.raises(ValueError, match="not a certificate"):
        home_dsn(_node(database_ca="not a pem"), tmp_path)


def test_the_ca_kept_is_rewritten_only_when_it_changed(tmp_path: Path) -> None:
    ca = CA("db")
    home_dsn(_node(database_ca=ca.pem), tmp_path)
    path = tmp_path / HOME_DATABASE_CA_FILE
    before = path.stat().st_mtime_ns
    home_dsn(_node(database_ca=ca.pem), tmp_path)
    assert path.stat().st_mtime_ns == before
    other = CA("db2")
    home_dsn(_node(database_ca=other.pem), tmp_path)
    assert path.read_text(encoding="ascii") == other.pem


@pytest.mark.parametrize(
    ("mode", "hostname", "verify"),
    [
        ("verify-full", True, ssl.CERT_REQUIRED),
        ("verify-ca", False, ssl.CERT_REQUIRED),
        ("require", False, ssl.CERT_REQUIRED),
    ],
)
def test_tls_the_dsn_asks_for(
    tmp_path: Path, mode: str, hostname: bool, verify: ssl.VerifyMode
) -> None:
    ca = CA("db")
    (tmp_path / "ca.crt").write_text(ca.pem, encoding="ascii")
    root = (tmp_path / "ca.crt").as_posix()  # C:/… on Windows
    _, ctx = connect_args(f"postgres://u@h/db?sslmode={mode}&sslrootcert={root}&application_name=x")
    assert ctx is not None and ctx.check_hostname is hostname and ctx.verify_mode == verify


def test_require_without_a_ca_encrypts_without_verifying() -> None:
    dsn, ctx = connect_args("postgres://u@h/db?sslmode=require")
    assert ctx is not None and ctx.verify_mode == ssl.CERT_NONE and "sslmode" not in dsn


def test_a_verify_mode_without_a_ca_is_an_error() -> None:
    with pytest.raises(TLSConfigError):
        connect_args("postgres://u@h/db?sslmode=verify-ca")


def test_the_client_certificate_with_a_windows_path(tmp_path: Path) -> None:
    ca, cluster = CA("db"), CA("cluster")
    (tmp_path / "ca.crt").write_text(ca.pem, encoding="ascii")
    _with_cert(tmp_path, cluster)
    for sep in ("/", "\\") if os.name == "nt" else ("/",):

        def p(name: str, sep: str = sep) -> str:
            return str(tmp_path / name).replace("\\", sep).replace("/", sep)

        _, ctx = connect_args(
            f"postgres://u@h/db?sslmode=verify-ca&sslrootcert={p('ca.crt')}"
            f"&sslcert={p(linkcert.CERT_FILE)}&sslkey={p(linkcert.KEY_FILE)}"
        )
        assert ctx is not None


def test_modes_that_may_go_plain_are_asyncpgs() -> None:
    for mode in ("", "disable", "allow", "prefer"):
        dsn = "postgres://u@h/db" + (f"?sslmode={mode}" if mode else "")
        assert connect_args(dsn) == (dsn, None)
