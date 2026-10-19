"""A refused database login stops a machine for good (exit 77) only when it is the home's
revocation (28P01, or 28000 "is not permitted to log in"); any other 28000 (no pg_hba.conf
entry, ...) is fixable on the home: exit 1 at start, keep going mid-run."""

from __future__ import annotations

import asyncio
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest
import structlog

from maktaba_pipeline import __main__ as cli_main
from maktaba_pipeline.cluster import config as node_config
from maktaba_pipeline.cluster import exitcodes
from maktaba_pipeline.cluster.node import WorkerAgent
from maktaba_pipeline.cluster.stages import StageSelector


class InvalidPasswordError(Exception):  # 28P01, as asyncpg names it
    pass


class InvalidAuthorizationSpecificationError(Exception):  # 28000
    pass


NOLOGIN = InvalidAuthorizationSpecificationError('role "maktaba_node_x" is not permitted to log in')
PG_HBA = InvalidAuthorizationSpecificationError(
    'no pg_hba.conf entry for host "192.168.50.31", user "maktaba_node_x", database "maktaba"'
)
PASSWORD = InvalidPasswordError('password authentication failed for user "maktaba_node_x"')


@pytest.mark.unit
def test_which_refusals_are_revocations() -> None:
    assert exitcodes.login_revoked(PASSWORD)
    assert exitcodes.login_revoked(NOLOGIN)
    assert not exitcodes.login_revoked(PG_HBA)
    assert exitcodes.is_auth_error(PG_HBA)
    assert not exitcodes.login_revoked(OSError("connection refused"))
    assert not exitcodes.is_auth_error(OSError("connection refused"))


@pytest.fixture
def node(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Iterator[None]:
    cfg = node_config.NodeConfig(
        node_id="6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7",
        name="linux-box",
        private_key="AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=",
        home=node_config.HomeRef(url="http://192.168.50.20:8080"),
        database_url="postgres://maktaba_node_x:pw@192.168.50.20:5432/maktaba",
    )
    path = tmp_path / "node.json"
    node_config.save(cfg, path)
    monkeypatch.setenv("MAKTABA_NODE_CONFIG", str(path))
    monkeypatch.delenv("MAKTABA_DATABASE_URL", raising=False)
    yield
    structlog.reset_defaults()  # main() configured logging onto the captured stdout
    structlog.contextvars.clear_contextvars()


@pytest.mark.integration  # main() runs an event loop
@pytest.mark.parametrize(
    ("error", "code"), [(PASSWORD, 77), (NOLOGIN, 77), (PG_HBA, 1)], ids=["28P01", "nologin", "hba"]
)
def test_at_start(node: None, monkeypatch: pytest.MonkeyPatch, error: Exception, code: int) -> None:
    async def connect(url: str) -> Any:
        raise error

    monkeypatch.setattr("maktaba_pipeline.__main__.Database.connect", connect)
    assert cli_main.main(["run"]) == code


async def _worker(error: Exception, monkeypatch: pytest.MonkeyPatch) -> tuple[WorkerAgent, Any]:
    from maktaba_pipeline.cluster import nodes_db

    async def beat(db: Any, node_id: str) -> None:
        raise error

    monkeypatch.setattr(nodes_db, "beat", beat)
    stop = asyncio.Event()
    agent = WorkerAgent(
        db=None,
        worker_id="w1",
        selector=StageSelector(graph=()),
        graph=(),
        shutdown=stop,
        node_id="6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7",
    )
    return agent, stop


@pytest.mark.integration
@pytest.mark.asyncio
@pytest.mark.parametrize("error", [PASSWORD, NOLOGIN], ids=["28P01", "nologin"])
async def test_mid_run_revocation_stops_with_77(
    monkeypatch: pytest.MonkeyPatch, error: Exception
) -> None:
    agent, stop = await _worker(error, monkeypatch)
    await agent.beat()
    assert stop.is_set() and exitcodes.exit_code(0) == 77


@pytest.mark.integration
@pytest.mark.asyncio
async def test_mid_run_pg_hba_keeps_the_worker(monkeypatch: pytest.MonkeyPatch) -> None:
    agent, stop = await _worker(PG_HBA, monkeypatch)
    with pytest.raises(InvalidAuthorizationSpecificationError):
        await agent.beat()  # the heartbeat loop logs it and tries again in 10 s
    assert not stop.is_set() and exitcodes.exit_code(0) == 0
