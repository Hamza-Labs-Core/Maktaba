"""The SQLite test database skips the durability work (``FAST_PRAGMAS``): with it, one write
cost the Windows runner up to 1.8 s, and an agent command waited behind several."""

from __future__ import annotations

import pytest

from maktaba_pipeline.runtime import Database


@pytest.mark.asyncio
async def test_the_sqlite_test_database_neither_syncs_nor_writes_a_journal_file(
    db: Database,
) -> None:
    if db.dialect != "sqlite":
        pytest.skip("SQLite only")
    assert await db.fetchval("PRAGMA synchronous") == 0  # OFF
    assert await db.fetchval("PRAGMA journal_mode") == "memory"
