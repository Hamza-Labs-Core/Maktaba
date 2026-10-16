"""Small dialect helpers shared by the engine (Postgres vs SQLite).

The engine writes one SQL text with ``$n`` placeholders; the runtime facade
rewrites them for SQLite. What still differs is value encoding: SQLite stores
ids, timestamps and JSON as TEXT, Postgres uses native types.
"""

from __future__ import annotations

import json
from datetime import UTC, datetime
from typing import Any
from uuid import UUID

__all__ = [
    "dumps",
    "is_pg",
    "loads",
    "now",
    "parse_ts",
    "to_date",
    "to_ts",
    "uid",
]


def is_pg(db: Any) -> bool:
    return bool(db.dialect == "postgres")


def now() -> datetime:
    return datetime.now(UTC)


def uid(value: UUID | str | None) -> str | None:
    """Ids are passed as strings on both dialects (asyncpg coerces to uuid)."""
    return None if value is None else str(value)


def to_ts(db: Any, value: datetime | None) -> datetime | str | None:
    """A timestamp parameter: ``datetime`` on Postgres, canonical ISO text on SQLite."""
    if value is None:
        return None
    if value.tzinfo is None:
        value = value.replace(tzinfo=UTC)
    if is_pg(db):
        return value
    v = value.astimezone(UTC)
    return v.strftime("%Y-%m-%dT%H:%M:%S.") + f"{v.microsecond // 1000:03d}Z"


def parse_ts(value: Any) -> datetime | None:
    if value is None:
        return None
    if isinstance(value, datetime):
        return value if value.tzinfo else value.replace(tzinfo=UTC)
    dt = datetime.fromisoformat(str(value))
    return dt if dt.tzinfo else dt.replace(tzinfo=UTC)


def dumps(obj: Any) -> str:
    return json.dumps(obj, separators=(",", ":"), ensure_ascii=False, default=str)


def loads(value: Any) -> Any:
    """Decode a JSON column (text on SQLite, text or parsed on Postgres)."""
    if value is None:
        return None
    if isinstance(value, (dict, list)):
        return value
    return json.loads(value)


def to_date(db: Any, value: Any) -> Any:
    """A DATE parameter: ``date`` on Postgres, ISO text on SQLite (``None`` passes through)."""
    if value is None:
        return None
    return value if is_pg(db) else value.isoformat()
