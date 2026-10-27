"""Reading a table an older database may not have yet.

A node can run newer code than its home's schema, and a fresh database lacks the
tables later slots add. Code that reads such a table falls back to "nothing there"
when it is missing. Catching *every* exception for that hid real failures (a dropped
connection, a syntax error, a permission refusal) as an empty answer at DEBUG.

:func:`older_schema` tells the missing table or column apart; :func:`fallback` logs
the fallback at the level the cause deserves, so the caller can keep its empty answer
without hiding a real failure.
"""

from __future__ import annotations

import asyncio
from typing import Any

__all__ = ["connection_lost", "fallback", "older_schema"]

#: asyncpg's errors for a connection that failed, closed or is refused for now (by name:
#: no asyncpg import here, SQLite installs do not have it): class 08 and 57P03
_CONNECTION_ERRORS = frozenset(
    {"PostgresConnectionError", "CannotConnectNowError", "ConnectionDoesNotExistError"}
)


def older_schema(exc: BaseException) -> bool:
    """The table or column is not there yet: Postgres undefined_table / undefined_column
    (42P01 / 42703), SQLite's "no such table" / "no such column"."""
    if getattr(exc, "sqlstate", None) in ("42P01", "42703"):
        return True
    if type(exc).__name__ in ("UndefinedTableError", "UndefinedColumnError"):
        return True
    text = str(exc).lower()
    return "no such table" in text or "no such column" in text


def connection_lost(exc: BaseException) -> bool:
    """The database did not answer (as opposed to answering with an error)."""
    if isinstance(exc, (OSError, TimeoutError, asyncio.TimeoutError)):  # noqa: UP041
        return True
    names = {c.__name__ for c in type(exc).__mro__}
    if names & _CONNECTION_ERRORS:
        return True
    # asyncpg's "connection is closed" / "pool is closing"
    return "InterfaceError" in names and "clos" in str(exc)


def fallback(log: Any, event: str, exc: BaseException, **fields: Any) -> str:
    """Log that a read fell back to its empty answer; the cause it was put down to.

    - ``older_schema``: DEBUG, expected on an older or bare database.
    - ``connection_lost``: INFO, without a traceback: the worker's own heartbeat says
      the database is gone, once, and this repeats each round until it is back.
    - anything else: WARNING with the traceback: a real failure, not hidden.
    """
    error = f"{type(exc).__name__}: {exc}"
    if older_schema(exc):
        log.debug(event, cause="older_schema", error=error, **fields)
        return "older_schema"
    if connection_lost(exc):
        log.info(event, cause="connection_lost", error=error, **fields)
        return "connection_lost"
    log.warning(event, cause="error", error=error, exc_info=exc, **fields)
    return "error"
