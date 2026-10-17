"""The settings file of a mediaintel cache: ``settings.json`` beside the database.

Sections: ``detect`` (series detection), ``groups`` (the group step) and
``planner`` (channel planning). Each holds only what was changed from the
built-in defaults; the settings page of ``serve`` edits it.
"""

from __future__ import annotations

import sqlite3
from pathlib import Path
from typing import Any

from maktaba_pipeline import settings
from maktaba_pipeline.rules import SECTIONS  # noqa: F401 — the sections and their help


def path_of(db_file: str | Path) -> Path:
    return Path(db_file).with_name("settings.json")


def file_of(where: sqlite3.Connection | Path) -> Path | None:
    """The settings file of a cache connection (None for an in-memory one), or ``where``."""
    if isinstance(where, Path):
        return where
    db = next((r[2] for r in where.execute("PRAGMA database_list") if r[1] == "main"), "")
    return path_of(db) if db else None


def changed(where: sqlite3.Connection | Path, section: str) -> dict[str, Any]:
    file = file_of(where)
    return settings.load(file).get(section, {}) if file is not None else {}


def get(where: sqlite3.Connection | Path, section: str) -> Any:
    """The config of ``section`` with the file's changes applied."""
    return settings.apply(SECTIONS[section][0], changed(where, section))


def note(conn: sqlite3.Connection, section: str) -> None:
    """Print the changed settings a step runs with, so the log says why a result differs."""
    if ch := changed(conn, section):
        print(f"  settings ({section}): " + ", ".join(sorted(ch)), flush=True)
