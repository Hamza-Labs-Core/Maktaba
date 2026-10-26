"""A node's pipeline against a home whose Postgres requires its machines' certificates
(``hostssl … clientcert=verify-full``, the cluster link required: story 35.16), on a real
Postgres: it logs in with the machine's certificate, as the node's API does; without the
certificate, or against a server without TLS, it does not connect at all (never a plain
retry); and the join's own URL (``sslmode=prefer``, what the pipeline used before) is
refused as it was live ("no pg_hba.conf entry … no encryption")."""

from __future__ import annotations

import asyncio
import glob
import os
import shutil
import socket
import subprocess
from collections.abc import Iterator
from dataclasses import dataclass
from pathlib import Path

import pytest

from maktaba_pipeline.cluster import linkcert
from maktaba_pipeline.cluster.config import NodeConfig
from maktaba_pipeline.cluster.homedb import home_dsn
from maktaba_pipeline.runtime import Database

from .tls_certs import CA

pytestmark = pytest.mark.integration

ROLE = "maktaba_node_6f1c2a3b4d5e4f608a7192b3c4d5e6f7"


def _pg_bin() -> Path | None:
    """Postgres's programs: MAKTABA_TEST_PG_BIN, PATH, or Debian/Ubuntu's (GitHub's
    runners carry them)."""
    if v := os.environ.get("MAKTABA_TEST_PG_BIN"):
        return Path(v)
    if found := shutil.which("initdb"):
        return Path(found).parent
    dirs = sorted(glob.glob("/usr/lib/postgresql/*/bin/initdb"))
    return Path(dirs[-1]).parent if dirs else None


def _free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return int(s.getsockname()[1])


@dataclass
class Home:
    bin: Path
    data: Path
    log: Path
    port: int
    db_ca: CA
    cluster_ca: CA

    def ctl(self, *args: str) -> None:
        # never a pipe: the server pg_ctl starts holds it, and reading it waits for the server
        r = subprocess.run(
            [str(self.bin / "pg_ctl"), "-D", str(self.data), "-l", str(self.log), *args],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            timeout=60,
            check=False,
        )
        if r.returncode != 0:
            raise AssertionError(f"pg_ctl {' '.join(args)}: {self.log.read_text(errors='replace')}")

    def sql(self, statement: str) -> None:
        async def run() -> None:
            import asyncpg  # type: ignore[import-untyped]

            conn = await asyncpg.connect(
                host="127.0.0.1", port=self.port, user="postgres", database="postgres", ssl=False
            )
            try:
                await conn.execute(statement)
            finally:
                await conn.close()

        asyncio.run(run())


@pytest.fixture(scope="module")
def home(tmp_path_factory: pytest.TempPathFactory) -> Iterator[Home]:
    pg = _pg_bin()
    if pg is None:
        pytest.skip("no Postgres programs (initdb) on this machine")
    if hasattr(os, "geteuid") and os.geteuid() == 0:
        pytest.skip("postgres refuses to run as root")
    root = tmp_path_factory.mktemp("home-pg")
    data = root / "data"
    subprocess.run(
        [
            str(pg / "initdb"),
            "-D",
            str(data),
            "-U",
            "postgres",
            "--auth=trust",
            "-E",
            "UTF8",
            "--no-instructions",
        ],
        check=True,
        capture_output=True,
        timeout=120,
    )
    db_ca, cluster_ca = CA("home database"), CA("cluster")
    db_ca.server().write(data / "server.crt", data / "server.key")
    (data / "cluster-ca.crt").write_text(cluster_ca.pem, encoding="ascii")
    port = _free_port()
    with (data / "postgresql.conf").open("a", encoding="utf-8") as f:
        f.write(
            f"\nport = {port}\nlisten_addresses = '127.0.0.1'\nunix_socket_directories = ''\n"
            "ssl = on\nssl_cert_file = 'server.crt'\nssl_key_file = 'server.key'\n"
            "ssl_ca_file = 'cluster-ca.crt'\n"
        )
    # as pgbundle's hbaConf has it once the cluster link is required (and the test's own
    # superuser, plain, to set the roles up)
    (data / "pg_hba.conf").write_text(
        "hostssl maktaba +maktaba_worker 127.0.0.1/32 scram-sha-256 clientcert=verify-full\n"
        "host    all     postgres        127.0.0.1/32 trust\n",
        encoding="ascii",
    )
    h = Home(bin=pg, data=data, log=root / "pg.log", port=port, db_ca=db_ca, cluster_ca=cluster_ca)
    h.ctl("-w", "start")
    try:
        h.sql("CREATE ROLE maktaba_worker NOLOGIN")
        h.sql(f"CREATE ROLE {ROLE} LOGIN PASSWORD 'pw' IN ROLE maktaba_worker")
        h.sql(f"CREATE DATABASE maktaba OWNER {ROLE}")
        yield h
    finally:
        h.ctl("-m", "immediate", "-w", "stop")


def _node(h: Home) -> NodeConfig:
    return NodeConfig(
        node_id="6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7",
        name="pc",
        private_key="AQID",
        database_url=f"postgres://{ROLE}:pw@127.0.0.1:{h.port}/maktaba?sslmode=prefer",
        database_ca=h.db_ca.pem,
    )


async def _whoami(dsn: str) -> tuple[str, bool, str]:
    db = await Database.connect(dsn)
    try:
        row = await db.fetchrow(
            "SELECT current_user::text, s.ssl, COALESCE(s.client_dn, '') FROM pg_stat_ssl s"
            " WHERE s.pid = pg_backend_pid()"
        )
        listener = await db.acquire_listener()  # its own connection, the same TLS
        try:
            assert await listener.fetchval(
                "SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()"
            )
        finally:
            await listener.close()
        return str(row[0]), bool(row[1]), str(row[2])
    finally:
        await db.close()


def test_the_pipeline_logs_in_with_the_machines_certificate(home: Home, tmp_path: Path) -> None:
    home.cluster_ca.client(ROLE).write(tmp_path / linkcert.CERT_FILE, tmp_path / linkcert.KEY_FILE)
    user, on_tls, dn = asyncio.run(_whoami(home_dsn(_node(home), tmp_path)))
    assert (user, on_tls) == (ROLE, True)
    assert dn.endswith(f"CN={ROLE}")


def test_without_its_certificate_it_does_not_connect_and_never_in_plain(
    home: Home, tmp_path: Path
) -> None:
    with pytest.raises(Exception, match="certificate") as exc:
        asyncio.run(_whoami(home_dsn(_node(home), tmp_path)))
    assert "no encryption" not in str(exc.value)


def test_another_machines_certificate_is_refused(home: Home, tmp_path: Path) -> None:
    home.cluster_ca.client("maktaba_node_someone_else").write(
        tmp_path / linkcert.CERT_FILE, tmp_path / linkcert.KEY_FILE
    )
    # Postgres answers a certificate whose CN is not the role with the password's failure
    # (its log says "CN mismatch"): refused all the same
    with pytest.raises(Exception, match="authentication failed|certificate"):
        asyncio.run(_whoami(home_dsn(_node(home), tmp_path)))


def test_the_joins_own_url_is_refused_as_it_was_live(home: Home) -> None:
    with pytest.raises(Exception, match="no encryption|SSL off"):
        asyncio.run(_whoami(_node(home).database_url))


def test_a_home_without_tls_is_not_logged_in_to_in_plain(home: Home, tmp_path: Path) -> None:
    home.cluster_ca.client(ROLE).write(tmp_path / linkcert.CERT_FILE, tmp_path / linkcert.KEY_FILE)
    with (home.data / "pg_hba.conf").open("a", encoding="ascii") as f:
        f.write("host maktaba +maktaba_worker 127.0.0.1/32 scram-sha-256\n")
    home.sql("ALTER SYSTEM SET ssl = off")
    home.sql("SELECT pg_reload_conf()")
    try:
        with pytest.raises(Exception) as exc:
            asyncio.run(_whoami(home_dsn(_node(home), tmp_path)))
        assert "rejected SSL" in str(exc.value) or "SSL" in str(exc.value)
    finally:
        home.sql("ALTER SYSTEM RESET ssl")
        home.sql("SELECT pg_reload_conf()")
