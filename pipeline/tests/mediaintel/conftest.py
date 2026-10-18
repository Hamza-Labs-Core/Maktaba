"""The mediaintel tests' SQLite databases are throwaway files: no disk syncs.

The catalog and the tests open SQLite files with its default durability, so every
commit waits for the disk; on a busy CI runner a three-commit unit test took 4 s
against its 0.1 s cap. Every connection these tests open (theirs, ``scripts.mediaintel``'s,
read-only ones included) skips the syncs; the files stay real (some are reopened by
path).
"""

from __future__ import annotations

import contextlib
import sqlite3
from collections.abc import Iterator
from typing import Any

import pytest

_real_connect = sqlite3.connect


def _connect_without_syncs(*args: Any, **kwargs: Any) -> sqlite3.Connection:
    conn: sqlite3.Connection = _real_connect(*args, **kwargs)
    with contextlib.suppress(sqlite3.Error):  # one that refuses it keeps its default
        conn.execute("PRAGMA synchronous = OFF")
    return conn


@pytest.fixture(autouse=True)
def _no_disk_syncs(monkeypatch: pytest.MonkeyPatch) -> Iterator[None]:
    monkeypatch.setattr(sqlite3, "connect", _connect_without_syncs)
    yield
