"""Fixtures for the engine tests: real migrated SQLite, and real Postgres when available.

SQLite always runs (the shipped ``.sqlite.sql`` twins are applied to a file
database). Postgres runs only when ``MAKTABA_TEST_PG_URL`` points at a server
the tests may create databases on (for example one started with ``pgserver``);
otherwise those parametrisations are skipped, the way the repo keeps its other
service-backed tests out of the default run.
"""

from __future__ import annotations

import asyncio
import concurrent.futures
import os
import shutil
import sqlite3
import uuid
from collections.abc import AsyncIterator
from pathlib import Path

import pytest
import pytest_asyncio

from maktaba_pipeline.runtime import Database
from tests.engine.migrate import apply_postgres, apply_sqlite

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")


@pytest.fixture(scope="session")
def sqlite_template(tmp_path_factory: pytest.TempPathFactory) -> Path:
    path = tmp_path_factory.mktemp("engine-sqlite") / "template.db"
    conn = sqlite3.connect(path)
    conn.execute("PRAGMA foreign_keys = ON")
    apply_sqlite(conn)
    conn.close()
    return path


def _pg_admin_url() -> str:
    return PG_URL


@pytest.fixture(scope="session")
def pg_template() -> str:
    """Create (once) a migrated template database; return its name."""
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    import asyncpg  # type: ignore[import-untyped]  # no stubs ship with asyncpg

    name = "maktaba_tpl_" + uuid.uuid4().hex[:8]

    async def build() -> None:
        admin = await asyncpg.connect(PG_URL)
        await admin.execute(f'CREATE DATABASE "{name}"')
        await admin.close()
        conn = await asyncpg.connect(_swap_db(PG_URL, name))
        await apply_postgres(conn)
        await conn.close()

    # a worker thread has no running loop, so this also works when requested from an async test
    with concurrent.futures.ThreadPoolExecutor(1) as pool:
        pool.submit(lambda: asyncio.run(build())).result()
    return name


def _swap_db(url: str, name: str) -> str:
    base, _, _ = url.rpartition("/")
    return f"{base}/{name}"


@pytest_asyncio.fixture(params=["sqlite", pytest.param("postgres", marks=pytest.mark.integration)])
async def db(request: pytest.FixtureRequest, tmp_path: Path) -> AsyncIterator[Database]:
    if request.param == "sqlite":
        template: Path = request.getfixturevalue("sqlite_template")
        path = tmp_path / "t.db"
        shutil.copy(template, path)
        database = await Database.connect(f"sqlite:///{path}")
        try:
            yield database
        finally:
            await database.close()
        return
    template_name: str = request.getfixturevalue("pg_template")
    import asyncpg

    name = "maktaba_t_" + uuid.uuid4().hex[:10]
    admin = await asyncpg.connect(PG_URL)
    await admin.execute(f'CREATE DATABASE "{name}" TEMPLATE "{template_name}"')
    await admin.close()
    database = await Database.connect(_swap_db(PG_URL, name))
    try:
        yield database
    finally:
        await database.close()
        admin = await asyncpg.connect(PG_URL)
        await admin.execute(f'DROP DATABASE IF EXISTS "{name}" WITH (FORCE)')
        await admin.close()
