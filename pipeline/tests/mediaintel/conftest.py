"""The mediaintel tests' catalog databases are throwaway files: no disk syncs.

``scripts.mediaintel.db.connect`` opens the catalog with SQLite's default durability, so
each commit waits for the disk; on a busy CI runner a three-commit unit test took 4 s
against its 0.1 s cap. The tests keep real files (some reopen them by path) but skip
the syncs.
"""

from __future__ import annotations

import sqlite3
import sys
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))

from scripts.mediaintel import db  # noqa: E402


class _NoSync:
    """``sqlite3`` as ``db`` sees it, with connections that skip disk syncs from the
    start (the schema is created at connect, before a caller could set a pragma)."""

    def __getattr__(self, name: str) -> Any:
        return getattr(sqlite3, name)

    @staticmethod
    def connect(*args: Any, **kwargs: Any) -> sqlite3.Connection:
        conn: sqlite3.Connection = sqlite3.connect(*args, **kwargs)
        conn.execute("PRAGMA synchronous = OFF")
        conn.execute("PRAGMA journal_mode = MEMORY")
        return conn


@pytest.fixture(autouse=True)
def _no_disk_syncs(monkeypatch: pytest.MonkeyPatch) -> Iterator[None]:
    monkeypatch.setattr(db, "sqlite3", _NoSync())
    yield
